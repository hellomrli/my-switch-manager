package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const journalVersion = 3

// Op 保存操作意图与进度。prepared 可能尚未执行，恢复时必须检查文件身份。
type Op struct {
	Op       string     `json:"op"`
	Path     string     `json:"path,omitempty"`
	From     string     `json:"from,omitempty"`
	To       string     `json:"to,omitempty"`
	Backup   string     `json:"backup,omitempty"`
	State    string     `json:"state,omitempty"`
	Before   *FileStamp `json:"before,omitempty"`
	OldHash  string     `json:"oldHash,omitempty"`
	NewHash  string     `json:"newHash,omitempty"`
	CaseOnly bool       `json:"caseOnly,omitempty"`
}

type UndoLog struct {
	Version   int         `json:"version"`
	Root      string      `json:"root"`
	Created   string      `json:"created"`
	Title     string      `json:"title"`
	Ops       []Op        `json:"ops"`
	FileMoves [][2]string `json:"file_moves,omitempty"`
	DirMoves  [][2]string `json:"dir_moves,omitempty"`
	CreatedV1 []string    `json:"created_dirs,omitempty"`
	source    string
	save      func(*UndoLog) error
}

type Journal struct {
	path   string
	log    UndoLog
	fs     *rootFS
	fatal  error
	manual *StringMap
	save   func(*UndoLog) error // 测试可注入某次持久化失败，无需破坏真实数据盘。
}

type Logf func(format string, args ...any)

func NewJournal(root, title string) (*Journal, error) {
	r, err := openRootFS(root)
	if err != nil {
		return nil, err
	}
	name := "_undo_" + time.Now().Format("20060102-150405.000000000") + "-" + uniqueSuffix() + ".json"
	f, err := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("无法创建撤销日志：%w", err)
	}
	if err := f.Close(); err != nil {
		r.Remove(name)
		r.Close()
		return nil, err
	}
	j := &Journal{path: filepath.Join(r.root, name), fs: r,
		log: UndoLog{Version: journalVersion, Root: r.root, Created: time.Now().Format(time.RFC3339Nano), Title: title, Ops: []Op{}}}
	j.log.source = j.path
	if err := j.persist(); err != nil {
		r.Remove(name)
		r.Close()
		return nil, err
	}
	return j, nil
}

func (j *Journal) Path() string           { return j.path }
func (j *Journal) Len() int               { return len(j.log.Ops) }
func (j *Journal) Err() error             { return j.fatal }
func (j *Journal) Close() error           { return j.fs.Close() }
func (j *Journal) SetManual(m *StringMap) { j.manual = m }

func (j *Journal) persist() error {
	if j.fatal != nil {
		return j.fatal
	}
	var err error
	if j.save != nil {
		err = j.save(&j.log)
	} else {
		err = j.fs.saveJSON(j.path, &j.log)
	}
	if err != nil {
		j.fatal = fmt.Errorf("撤销日志保存失败，已停止操作（%s）：%w", j.path, err)
	}
	return j.fatal
}

func (j *Journal) perform(op Op, action func() error) (bool, error) {
	if j.fatal != nil {
		return false, j.fatal
	}
	op.State = "prepared"
	j.log.Ops = append(j.log.Ops, op)
	i := len(j.log.Ops) - 1
	if err := j.persist(); err != nil {
		return false, err
	}
	if err := action(); err != nil {
		// 原子写可能已经替换文件，但在同步时失败；保持 prepared 并停止，交给恢复检查。
		if op.Op == "write" {
			j.fatal = fmt.Errorf("写入结果未能确认，已停止操作：%w", err)
			return false, j.fatal
		}
		j.log.Ops[i].State = "undone"
		if saveErr := j.persist(); saveErr != nil {
			return false, errors.Join(err, saveErr)
		}
		return false, err
	}
	if op.Op == "mkdir" {
		stamp, err := j.fs.stamp(op.Path)
		if err != nil {
			j.fatal = err
			return true, err
		}
		j.log.Ops[i].Before = stamp
	}
	if err := syncOpDirs(j.fs, &op); err != nil {
		j.fatal = fmt.Errorf("同步文件操作失败，已停止：%w", err)
		return true, j.fatal
	}
	j.log.Ops[i].State = "applied"
	return true, j.persist()
}

func syncOpDirs(r *rootFS, op *Op) error {
	paths := []string{op.Path}
	if op.Op == "move" {
		paths = []string{op.From, op.To}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		name, err := r.relative(path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(name)
		if !seen[dir] {
			if err := syncRootDir(r.Root, dir); err != nil {
				return err
			}
			seen[dir] = true
		}
	}
	return nil
}

func (j *Journal) mkdirAll(dir string) error {
	if j.fatal != nil {
		return j.fatal
	}
	name, err := j.fs.relative(dir)
	if err != nil {
		return err
	}
	if name == "." {
		return nil
	}
	var missing []string
	for d := name; d != "."; d = filepath.Dir(d) {
		fi, err := j.fs.Stat(d)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("不是目录：%s", d)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, d)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		d := missing[i]
		if _, err := j.perform(Op{Op: "mkdir", Path: filepath.Join(j.fs.root, d)}, func() error { return j.fs.Mkdir(d, 0o777) }); err != nil {
			return err
		}
	}
	return nil
}

func (j *Journal) move(from, to string) error {
	if j.fatal != nil {
		return j.fatal
	}
	stamp, err := j.fs.stamp(from)
	if err != nil {
		return err
	}
	if _, err := j.fs.relative(to); err != nil {
		return err
	}
	action := func() (bool, error) {
		return j.perform(Op{Op: "move", From: from, To: to, Before: stamp,
			CaseOnly: runtime.GOOS == "windows" && from != to && strings.EqualFold(from, to)}, func() error { return j.fs.rename(from, to) })
	}
	if j.manual != nil && !stamp.Dir && RomExts[strings.ToLower(filepath.Ext(from))] {
		return j.manual.renameWithAlias(filepath.Base(from), filepath.Base(to), action)
	}
	_, err = action()
	return err
}

func (j *Journal) removeIfEmpty(dir, stop string) error {
	if j.fatal != nil {
		return j.fatal
	}
	for d := dir; withinRoot(stop, d) && d != stop; d = filepath.Dir(d) {
		entries, err := j.fs.readDir(d)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return nil
		}
		name, err := j.fs.relative(d)
		if err != nil {
			return err
		}
		if name == "." {
			return fmt.Errorf("不允许删除库根目录")
		}
		if _, err := j.perform(Op{Op: "rmdir", Path: d}, func() error { return j.fs.Remove(name) }); err != nil {
			return err
		}
	}
	return nil
}

func hashData(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func (j *Journal) writeFile(path string, data []byte) error {
	if j.fatal != nil {
		return j.fatal
	}
	name, err := j.fs.relative(path)
	if err != nil {
		return err
	}
	old, err := j.fs.ReadFile(name)
	if err != nil {
		return err
	}
	fi, err := j.fs.Stat(name)
	if err != nil {
		return err
	}
	if string(old) == string(data) {
		return nil
	}
	backup := name + "." + uniqueSuffix() + ".bak"
	if err := j.fs.atomicWrite(backup, old, fi.Mode()); err != nil {
		return fmt.Errorf("备份失败：%w", err)
	}
	_, err = j.perform(Op{Op: "write", Path: path, Backup: filepath.Join(j.fs.root, backup), OldHash: hashData(old), NewHash: hashData(data)},
		func() error { return j.fs.atomicWrite(name, data, fi.Mode()) })
	return err
}

// safeRename 保留单目录调用的禁止覆盖语义；生产批量操作通过 Journal 的根目录句柄执行。
func safeRename(from, to string) error {
	r, err := openRootFS(filepath.Dir(from))
	if err != nil {
		return err
	}
	defer r.Close()
	return r.rename(from, to)
}

func ReadUndoLog(path string) (*UndoLog, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r, err := openRootFS(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := r.ReadFile(filepath.Base(abs))
	if err != nil {
		return nil, err
	}
	var l UndoLog
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Version > journalVersion {
		return nil, fmt.Errorf("不支持的撤销日志版本 %d", l.Version)
	}
	if l.Root != "" && filepath.Clean(l.Root) != r.root {
		return nil, fmt.Errorf("撤销日志的库根与日志所在目录不符")
	}
	l.Root, l.source = r.root, abs
	if l.Version < 2 {
		for _, d := range l.CreatedV1 {
			l.Ops = append(l.Ops, Op{Op: "mkdir", Path: d})
		}
		for _, m := range l.FileMoves {
			l.Ops = append(l.Ops, Op{Op: "move", From: m[0], To: m[1]})
		}
		for _, m := range l.DirMoves {
			l.Ops = append(l.Ops, Op{Op: "rmdir", Path: m[1]})
		}
		l.Title = "旧版 SwitchRomMgr 整理"
	}
	for i := range l.Ops {
		op := &l.Ops[i]
		if l.Version < journalVersion {
			op.State = "applied"
		}
		switch op.State {
		case "prepared", "applied", "undoing", "undone":
		default:
			return nil, fmt.Errorf("未知操作状态：%q", op.State)
		}
		var paths []string
		switch op.Op {
		case "move":
			paths = []string{op.From, op.To}
		case "mkdir", "rmdir":
			paths = []string{op.Path}
		case "write":
			paths = []string{op.Path, op.Backup}
		default:
			return nil, fmt.Errorf("未知撤销操作：%s", op.Op)
		}
		for _, p := range paths {
			name, err := r.relative(p)
			if p == "" || err != nil || name == "." {
				return nil, fmt.Errorf("撤销路径越界或为空：%s", p)
			}
		}
	}
	return &l, nil
}

func saveUndo(r *rootFS, l *UndoLog) error {
	if l.source == "" {
		return fmt.Errorf("撤销日志缺少源文件，无法保存恢复进度")
	}
	if l.save != nil {
		return l.save(l)
	}
	return r.saveJSON(l.source, l)
}

func optionalStamp(r *rootFS, path string, exact bool) (*FileStamp, error) {
	if exact {
		entries, err := r.readDir(filepath.Dir(path))
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		found := false
		for _, e := range entries {
			if e.Name() == filepath.Base(path) {
				found = true
				break
			}
		}
		if !found {
			return nil, nil
		}
	}
	s, err := r.stamp(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return s, err
}

// reconcile 返回该操作是否已恢复/尚未执行，不确定的文件状态绝不按成功跳过。
func reconcile(r *rootFS, op *Op) (bool, error) {
	switch op.Op {
	case "move":
		from, err := optionalStamp(r, op.From, op.CaseOnly)
		if err != nil {
			return false, err
		}
		to, err := optionalStamp(r, op.To, op.CaseOnly)
		if err != nil {
			return false, err
		}
		if from != nil && to == nil && op.Before.matches(from) {
			return true, nil
		}
		if from == nil && to != nil && op.Before.matches(to) {
			return false, nil
		}
		return false, fmt.Errorf("移动状态有冲突或文件身份不符：%s → %s", op.From, op.To)
	case "write":
		name, err := r.relative(op.Path)
		if err != nil {
			return false, err
		}
		data, err := r.ReadFile(name)
		if err != nil {
			return false, err
		}
		h := hashData(data)
		if op.OldHash != "" && h == op.OldHash {
			return true, nil
		}
		if op.NewHash != "" && h == op.NewHash {
			return false, nil
		}
		return false, fmt.Errorf("文件内容已变化，无法确定写入状态：%s", op.Path)
	case "mkdir", "rmdir":
		name, err := r.relative(op.Path)
		if err != nil {
			return false, err
		}
		fi, err := r.Stat(name)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if op.Op == "mkdir" {
			if os.IsNotExist(err) {
				return true, nil
			}
			current, err := r.stamp(op.Path)
			if err != nil {
				return false, err
			}
			if !op.Before.matches(current) {
				return false, fmt.Errorf("无法确认待恢复目录的身份：%s", op.Path)
			}
			return false, nil
		}
		if err == nil && !fi.IsDir() {
			return false, fmt.Errorf("目录被其他文件占用：%s", op.Path)
		}
		return err == nil, nil
	}
	return false, fmt.Errorf("未知操作：%s", op.Op)
}

func undoOne(r *rootFS, op *Op) error {
	switch op.Op {
	case "move":
		if op.Before != nil {
			current, err := r.stamp(op.To)
			if err != nil {
				return err
			}
			if !op.Before.matches(current) {
				return fmt.Errorf("文件已被替换或修改：%s", op.To)
			}
		}
		// 目录由对应的 rmdir 操作先行还原，不额外创建未经记录的目录。
		return r.rename(op.To, op.From)
	case "mkdir":
		name, err := r.relative(op.Path)
		if err != nil {
			return err
		}
		if op.Before != nil {
			current, err := r.stamp(op.Path)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !op.Before.matches(current) {
				return fmt.Errorf("目录已被替换：%s", op.Path)
			}
		}
		err = r.Remove(name) // 非空目录拒绝删除；遇错即停，不能继续恢复父目录。
		if os.IsNotExist(err) {
			return nil
		}
		return err
	case "rmdir":
		name, err := r.relative(op.Path)
		if err != nil {
			return err
		}
		fi, err := r.Stat(name)
		if err == nil && fi.IsDir() {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return r.Mkdir(name, 0o777)
	case "write":
		backup, err := r.relative(op.Backup)
		if err != nil {
			return err
		}
		data, err := r.ReadFile(backup)
		if err != nil {
			return err
		}
		if op.OldHash != "" && hashData(data) != op.OldHash {
			return fmt.Errorf("备份已被修改：%s", op.Backup)
		}
		name, err := r.relative(op.Path)
		if err != nil {
			return err
		}
		current, err := r.ReadFile(name)
		if err != nil {
			return err
		}
		if op.NewHash != "" && hashData(current) != op.NewHash {
			return fmt.Errorf("文件在操作后已被修改：%s", op.Path)
		}
		fi, err := r.Stat(name)
		if err != nil {
			return err
		}
		return r.atomicWrite(name, data, fi.Mode())
	}
	return fmt.Errorf("未知撤销操作：%s", op.Op)
}

func Undo(l *UndoLog, logf Logf) (ok, failed int) {
	r, err := openRootFS(l.Root)
	if err != nil {
		logf("[失败] 打开库目录：%v", err)
		return 0, 1
	}
	defer r.Close()
	// 旧日志只升级状态，不改变倒序操作顺序。
	if l.Version < journalVersion {
		for i := range l.Ops {
			op := &l.Ops[i]
			if op.State == "" {
				op.State = "applied"
			}
			if op.Op == "move" && op.Before == nil {
				op.Before, err = r.stamp(op.To)
				if err != nil {
					logf("[失败] 无法确认旧日志的文件：%v", err)
					return 0, 1
				}
			}
			if op.Op == "write" {
				b, e1 := r.relative(op.Backup)
				p, e2 := r.relative(op.Path)
				if e1 != nil || e2 != nil {
					logf("[失败] 写入路径越界")
					return 0, 1
				}
				old, e1 := r.ReadFile(b)
				current, e2 := r.ReadFile(p)
				if e1 != nil || e2 != nil {
					logf("[失败] 无法读取写入备份")
					return 0, 1
				}
				op.OldHash, op.NewHash = hashData(old), hashData(current)
			}
		}
		l.Version = journalVersion
		l.FileMoves, l.DirMoves, l.CreatedV1 = nil, nil, nil
	}
	if err := saveUndo(r, l); err != nil {
		logf("[失败] 保存撤销进度：%v", err)
		return 0, 1
	}
	for i := len(l.Ops) - 1; i >= 0; i-- {
		op := &l.Ops[i]
		if op.State == "undone" {
			continue
		}
		if op.State == "prepared" || op.State == "undoing" {
			done, err := reconcile(r, op)
			if err != nil {
				logf("[失败] %v", err)
				return ok, 1
			}
			if done {
				op.State = "undone"
				if err := saveUndo(r, l); err != nil {
					logf("[失败] 保存恢复进度：%v", err)
					return ok, 1
				}
				continue
			}
		}
		op.State = "undoing"
		if err := saveUndo(r, l); err != nil {
			logf("[失败] 保存恢复意图：%v", err)
			return ok, 1
		}
		if err := undoOne(r, op); err != nil {
			logf("[失败] 撤销 %s：%v", op.Op, err)
			return ok, 1
		}
		if err := syncOpDirs(r, op); err != nil {
			logf("[失败] 同步撤销结果：%v", err)
			return ok, 1
		}
		op.State = "undone"
		if err := saveUndo(r, l); err != nil {
			logf("[失败] 保存恢复结果：%v", err)
			return ok, 1
		}
		if op.Op == "move" || op.Op == "write" {
			ok++
		}
	}
	return ok, 0
}

// CompleteUndo 只给已经完整落盘的恢复结果加 .done，不能单凭调用方的计数判断。
func CompleteUndo(l *UndoLog) error {
	if l.source == "" {
		return fmt.Errorf("缺少撤销日志路径")
	}
	saved, err := ReadUndoLog(l.source)
	if err != nil {
		return err
	}
	if saved.Version != journalVersion {
		return fmt.Errorf("撤销结果尚未保存")
	}
	for _, op := range saved.Ops {
		if op.State != "undone" {
			return fmt.Errorf("仍有未恢复的操作")
		}
	}
	r, err := openRootFS(saved.Root)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.rename(l.source, l.source+".done"); err != nil {
		return err
	}
	return syncRootDir(r.Root, ".")
}
