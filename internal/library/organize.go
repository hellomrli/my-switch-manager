package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"my-switch-manager/internal/nsw"
)

// ============================ 命名模板 ============================
//
// 可用标记：
//
//	{name}  中文名（自定义 > 库中名 > 文件名猜测）
//	{title} 从原文件名猜出的原名（去掉 [] 与 () 标记）
//	{tid}   文件夹：本体 TitleID；文件：该文件的主 TitleID（DLC 文件为 DLC 自己的 ID）
//	{kind}  APP / UPD / DLC（含本体的合集包记为 APP）
//	{ver}   版本标记，形如 [v917504]；版本未知或为 0 时为空
//	{dlc}   合集包里的 DLC 数，形如 [+6DLC]；不含 DLC 时为空
//
// {ver} 与 {dlc} 保持可被重新解析：旧版整理后把「B+U917504」这类版本信息丢掉了，
// 之后再检查就只能显示「版本未知」。
const (
	DefaultFolderTemplate = "[{name}][{title}][{tid}]"
	DefaultFileTemplate   = "{title}[{tid}][{kind}]{ver}{dlc}"
)

type Templates struct {
	Folder string `json:"folder"`
	File   string `json:"file"`
}

func (t Templates) withDefaults() Templates {
	if strings.TrimSpace(t.Folder) == "" {
		t.Folder = DefaultFolderTemplate
	}
	if strings.TrimSpace(t.File) == "" {
		t.File = DefaultFileTemplate
	}
	return t
}

// Validate 检查模板是否安全可用
func (t Templates) Validate() error {
	t = t.withDefaults()
	if !strings.Contains(t.File, "{tid}") {
		return errors.New("文件名模板必须包含 {tid}，否则整理后可能再也识别不出游戏")
	}
	for _, s := range []string{t.Folder, t.File} {
		if strings.ContainsAny(s, `/\`) {
			return errors.New("模板里不能包含路径分隔符 / 或 \\")
		}
	}
	return nil
}

func render(tpl string, vals map[string]string) string {
	out := tpl
	for k, v := range vals {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	// 去掉空标记留下的空括号，如 "[]"
	for _, e := range []string{"[]", "()", "【】"} {
		out = strings.ReplaceAll(out, e, "")
	}
	return sanitizeName(out)
}

// sanitizeName 去掉 Windows / exFAT / Switch 上会出问题的字符
func sanitizeName(s string) string {
	r := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "：", "*", "_", "?", "？",
		"\"", "'", "<", "_", ">", "_", "|", "_", "\t", " ", "\n", " ", "\r", " ",
	)
	s = strings.Join(strings.Fields(r.Replace(s)), " ")
	s = strings.TrimRight(s, ". ") // 结尾的点和空格在 Windows 上会被吞掉
	// 单个文件名控制在 200 字节以内，给扩展名和 SMB 留余量
	for len(s) > 200 {
		rs := []rune(s)
		s = string(rs[:len(rs)-1])
	}
	if s == "" {
		s = "未命名"
	}
	return s
}

// ============================ 整理方案 ============================

const (
	ModeRename  = "rename"  // 整个游戏文件夹改名（连同 cheats、图片等附属文件一起）
	ModeMove    = "move"    // 逐个移动 ROM 到目标文件夹（文件夹里混有其他游戏，或目标已存在）
	ModeCreate  = "create"  // ROM 直接散放在库根目录：新建文件夹后移入
	ModeInPlace = "inplace" // 文件夹名已正确，只改文件名
)

type Move struct {
	From string `json:"from"` // 相对库根
	To   string `json:"to"`
}

type PlanItem struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Mode      string   `json:"mode"`
	OldDir    string   `json:"oldDir"`
	TargetDir string   `json:"targetDir"`
	DirMove   *Move    `json:"dirMove,omitempty"`
	Moves     []Move   `json:"moves"`
	Warnings  []string `json:"warnings,omitempty"`
	Blocked   string   `json:"blocked,omitempty"` // 非空表示该条不会执行
}

// BuildPlan 构建整理方案。
//
// 安全规则（旧版曾把扫描根目录本身改名，导致 Unraid shfs 崩溃，这几条必须保持）：
//   - 目标路径必须在库根目录之内，且不能等于库根目录
//   - ROM 散放在根目录时绝不改根目录名，而是新建子文件夹
//   - 任何目标文件已存在都拒绝覆盖（Linux 上 rename 会静默覆盖）
func BuildPlan(root string, files []*RomFile, games []*Game, namer Namer, tpl Templates) ([]PlanItem, error) {
	tpl = tpl.withDefaults()
	if err := tpl.Validate(); err != nil {
		return nil, err
	}
	root = filepath.Clean(root)

	// 每个目录（含子目录）下有哪些游戏，用来判断文件夹是否可以整体改名
	dirGames := map[string]map[string]bool{}
	for _, f := range files {
		key := "?" + f.Rel
		if f.BaseID != 0 {
			key = f.BaseID.String()
		}
		for d := filepath.Dir(f.Path); withinRoot(root, d) && d != root; d = filepath.Dir(d) {
			if dirGames[d] == nil {
				dirGames[d] = map[string]bool{}
			}
			dirGames[d][key] = true
		}
	}

	var items []PlanItem
	claimed := map[string]string{} // 目标路径 -> 占用它的游戏，防止两个游戏抢同一个名字
	for _, g := range games {
		if g.BaseID == 0 {
			continue
		}
		name, src := namer.DisplayName(g)
		if src == "file" {
			continue // 没有中文名就不动，避免误伤
		}
		ref := g.Files[0]
		for _, f := range g.Files {
			if f.KindCode() == "APP" {
				ref = f
				break
			}
		}
		it := PlanItem{Key: g.Key, Name: name, Moves: []Move{}}
		oldDir := filepath.Dir(ref.Path)
		newDirName := render(tpl.Folder, map[string]string{
			"name": name, "title": GuessTitle(ref.Name), "tid": g.BaseID.String(), "kind": "", "ver": "", "dlc": "",
		})

		var target string
		switch {
		case oldDir == root:
			it.Mode = ModeCreate
			target = filepath.Join(root, newDirName)
		default:
			target = filepath.Join(filepath.Dir(oldDir), newDirName)
			switch {
			case target == oldDir:
				it.Mode = ModeInPlace
			case len(dirGames[oldDir]) == 1 && !exists(target):
				it.Mode = ModeRename
			default:
				it.Mode = ModeMove
				if len(dirGames[oldDir]) > 1 {
					it.Warnings = append(it.Warnings, "原文件夹里还有其他游戏，只移动本游戏的 ROM")
				}
			}
		}
		it.OldDir, it.TargetDir = relOf(root, oldDir), relOf(root, target)
		if !withinRoot(root, target) || target == root {
			it.Blocked = "目标路径越界，为安全起见跳过"
			items = append(items, it)
			continue
		}
		if other, ok := claimed[target]; ok && other != g.Key {
			it.Blocked = "目标文件夹与另一个游戏重名：" + other
			items = append(items, it)
			continue
		}
		claimed[target] = g.Key
		if it.Mode == ModeRename {
			it.DirMove = &Move{From: it.OldDir, To: it.TargetDir}
		}

		// 文件级移动：rename 模式下源路径是改名后的文件夹里
		used := map[string]bool{}
		for _, f := range g.Files {
			newName := render(tpl.File, fileVals(f, name)) + f.Ext
			dest := filepath.Join(target, newName)
			srcPath := f.Path
			if it.Mode == ModeRename {
				if r, err := filepath.Rel(oldDir, f.Path); err == nil && !strings.HasPrefix(r, "..") {
					srcPath = filepath.Join(target, r)
					// 在子文件夹里的 ROM 只改名，不挪出子文件夹
					dest = filepath.Join(filepath.Dir(srcPath), newName)
				}
			}
			if used[strings.ToLower(dest)] {
				it.Warnings = append(it.Warnings, fmt.Sprintf("%s 与同组另一个文件改名后重名，保持原名", f.Name))
				continue
			}
			used[strings.ToLower(dest)] = true
			if srcPath == dest {
				continue
			}
			// 目标已存在就跳过；rename 模式下要检查的是改名前那个文件夹里的同名文件
			check := dest
			if it.Mode == ModeRename {
				if r, err := filepath.Rel(target, dest); err == nil {
					check = filepath.Join(oldDir, r)
				}
			}
			if exists(check) && !sameFile(f.Path, check) {
				it.Warnings = append(it.Warnings, fmt.Sprintf("目标已存在，跳过：%s", relOf(root, dest)))
				continue
			}
			it.Moves = append(it.Moves, Move{From: relOf(root, srcPath), To: relOf(root, dest)})
			if filepath.Dir(f.Path) != oldDir && it.Mode != ModeRename {
				it.Warnings = append(it.Warnings, fmt.Sprintf("%s 原本不在同一文件夹，将一并归入", f.Name))
			}
		}
		if it.DirMove == nil && len(it.Moves) == 0 {
			continue // 已经是整理好的状态
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func fileVals(f *RomFile, name string) map[string]string {
	tid := f.BaseID
	kind := f.KindCode()
	if kind != "APP" && len(f.Contents) == 1 {
		tid = f.Contents[0].TitleID // 独立的更新/DLC 文件用它自己的 ID
	} else if kind == "UPD" {
		tid = f.BaseID.PatchID()
	}
	ver := ""
	v := f.GameVersion()
	if kind == "DLC" && len(f.Contents) == 1 {
		v = f.Contents[0].Version
	}
	if v > 0 {
		ver = "[v" + strconv.FormatInt(v, 10) + "]"
	}
	dlc := ""
	if n := countKind(f, nsw.KindAddOn); n > 0 && kind != "DLC" {
		dlc = fmt.Sprintf("[+%dDLC]", n)
	} else if kind != "DLC" && f.DlcMark > 0 && f.Source != SrcCNMT && f.Source != SrcNCA {
		dlc = fmt.Sprintf("[+%dDLC]", f.DlcMark)
	}
	return map[string]string{"name": name, "title": GuessTitle(f.Name), "tid": tid.String(), "kind": kind, "ver": ver, "dlc": dlc}
}

func countKind(f *RomFile, k nsw.Kind) int {
	n := 0
	for _, c := range f.Contents {
		if c.Kind() == k {
			n++
		}
	}
	return n
}

// ============================ 执行与撤销日志 ============================

type ExecResult struct {
	OK       int               `json:"ok"`
	Failed   int               `json:"failed"`
	Skipped  int               `json:"skipped"`
	UndoPath string            `json:"undoPath"`
	Renames  map[string]string `json:"-"` // 旧绝对路径 -> 新绝对路径（ROM）
}

// ExecutePlan 执行整理方案。items 应当来自 BuildPlan，执行前会再次校验每一步。
func ExecutePlan(root string, items []PlanItem, j *Journal, logf Logf) ExecResult {
	root = filepath.Clean(root)
	res := ExecResult{Renames: map[string]string{}, UndoPath: j.Path()}
	if err := writeManifest(root, items, j, logf); err != nil {
		res.Failed = len(items)
		logf("[失败] 无法保存整理清单：%v", err)
		return res
	}
	abs := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

	for _, it := range items {
		if j.Err() != nil {
			break
		}
		if it.Blocked != "" {
			res.Skipped++
			logf("[跳过] %s：%s", it.Name, it.Blocked)
			continue
		}
		target := abs(it.TargetDir)
		if !withinRoot(root, target) || target == root {
			res.Failed++
			logf("[拒绝] %s：目标路径越界", it.Name)
			continue
		}
		bad := false
		if it.DirMove != nil {
			from, to := abs(it.DirMove.From), abs(it.DirMove.To)
			if from == root || !withinRoot(root, from) {
				res.Failed++
				logf("[拒绝] %s：不允许移动库根目录", it.Name)
				continue
			}
			if err := j.move(from, to); err != nil {
				res.Failed++
				logf("[失败] %s：文件夹改名失败：%v", it.Name, err)
				continue
			}
			// 使用受限文件系统读取改名后的目录，不经父目录链接走出库根。
			dir, err := j.fs.relative(to)
			if err == nil {
				err = fs.WalkDir(j.fs.FS(), filepath.ToSlash(dir), func(p string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !d.IsDir() && d.Type()&os.ModeSymlink == 0 && RomExts[strings.ToLower(filepath.Ext(p))] {
						newPath := filepath.Join(j.fs.root, filepath.FromSlash(p))
						r, _ := filepath.Rel(to, newPath)
						res.Renames[filepath.Join(from, r)] = newPath
					}
					return nil
				})
			}
			if err != nil {
				bad = true
				logf("[失败] 读取改名后的目录：%v", err)
			}
		} else if err := j.mkdirAll(target); err != nil {
			res.Failed++
			logf("[失败] %s：无法创建文件夹：%v", it.Name, err)
			continue
		}
		for _, mv := range it.Moves {
			if j.Err() != nil {
				bad = true
				break
			}
			from, to := abs(mv.From), abs(mv.To)
			if !withinRoot(root, to) {
				bad = true
				logf("[拒绝] %s：目标越界", mv.To)
				continue
			}
			if err := j.move(from, to); err != nil {
				bad = true
				logf("[失败] %s：%v", filepath.Base(mv.From), err)
				continue
			}
			orig := from
			for o, n := range res.Renames {
				if n == from {
					orig = o
				}
			}
			res.Renames[orig] = to
		}
		if it.Mode == ModeMove || it.Mode == ModeCreate {
			if err := j.removeIfEmpty(abs(it.OldDir), root); err != nil {
				bad = true
				logf("[失败] 清理空目录：%v", err)
			}
		}
		if bad {
			res.Failed++
		} else {
			res.OK++
			logf("[完成] %s → %s", it.Name, it.TargetDir)
		}
	}
	return res
}

// writeManifest 整理前写下全量清单（旧版格式：左列原完整路径，右列新完整路径）。
// 即使撤销日志丢失，也能凭它人工还原；单独修复媒体时也以它为映射依据。
func writeManifest(root string, items []PlanItem, j *Journal, logf Logf) error {
	name := "_before_" + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(j.Path()), "_undo_"), ".json") + ".txt"
	p := filepath.Join(root, name)
	var b strings.Builder
	fmt.Fprintf(&b, "# 整理前全量清单  生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "# 扫描根目录: %s\n", root)
	fmt.Fprintf(&b, "# 说明: 左列是整理前的完整路径，右列是整理后的完整路径\n\n")
	for _, it := range items {
		if it.Blocked != "" {
			continue
		}
		for _, mv := range it.Moves {
			from := mv.From
			if it.DirMove != nil {
				if r, err := filepath.Rel(it.DirMove.To, mv.From); err == nil && !strings.HasPrefix(r, "..") {
					from = filepath.Join(it.DirMove.From, r)
				}
			}
			fmt.Fprintf(&b, "%s\t%s\n", filepath.Join(root, from), filepath.Join(root, mv.To))
		}
	}
	if err := j.fs.atomicWrite(p, []byte(b.String()), 0o600); err != nil {
		return err
	}
	logf("已保存整理前清单：%s", filepath.Base(p))
	return nil
}

// ListUndoLogs 列出库根目录下的撤销日志，最新的在前
func ListUndoLogs(root string) []string {
	ms, _ := filepath.Glob(filepath.Join(root, "_undo_*.json"))
	sort.Sort(sort.Reverse(sort.StringSlice(ms)))
	return ms
}

// ============================ 小工具 ============================

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

func relOf(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(r)
}
