package server

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"my-switch-manager/internal/library"
	"my-switch-manager/internal/nsw"
	"my-switch-manager/internal/titledb"
)

// ============================ 状态与设置 ============================

func (a *App) hState(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	st := map[string]any{
		"app": AppName, "version": Version, "desktop": a.opt.Desktop, "dataDir": a.opt.DataDir,
		"config": a.cfg, "remote": a.opt.AllowRemote, "planGeneration": a.planGeneration,
		"defaults": library.Templates{Folder: library.DefaultFolderTemplate, File: library.DefaultFileTemplate},
	}
	dbs := map[string]any{"dir": a.cfg.DBDir, "loaded": a.db != nil, "error": a.dbErr}
	if a.db != nil {
		dbs["stats"] = a.db.Stats()
		dbs["loadedAt"] = a.db.LoadedAt
		dbs["warnings"] = a.db.Warnings
	}
	if fi, err := os.Stat(filepath.Join(a.cfg.DBDir, "versions.json")); err == nil {
		dbs["fileTime"] = fi.ModTime()
	}
	st["db"] = dbs
	ks := map[string]any{"path": a.cfg.KeysPath, "loaded": a.keys != nil, "error": a.keysErr}
	if a.keys != nil {
		ks["maxGen"] = a.keys.MaxKeyGeneration()
	}
	st["keys"] = ks
	st["library"] = map[string]any{"root": a.scanRoot, "scannedAt": a.scannedAt, "files": len(a.files), "games": len(a.games)}
	job, _ := a.jobs.Snapshot(0)
	st["job"] = job
	writeJSON(w, st)
}

func (a *App) hConfig(w http.ResponseWriter, r *http.Request) {
	var c Config
	if err := readJSON(r, &c); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if err := c.Templates.Validate(); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	switch c.MediaMode {
	case library.MediaNone, library.MediaKeep, library.MediaIntoFolder:
	default:
		c.MediaMode = library.MediaNone
	}
	c.LibraryRoot = strings.TrimSpace(c.LibraryRoot)
	c.DBDir = strings.TrimSpace(c.DBDir)
	c.KeysPath = strings.TrimSpace(c.KeysPath)
	if c.DBDir == "" {
		c.DBDir = filepath.Join(a.opt.DataDir, "titledb")
	}
	a.mu.Lock()
	a.invalidatePlanLocked()
	old := a.cfg
	a.cfg = c
	if c.KeysPath != old.KeysPath {
		a.loadKeys()
	}
	err := a.saveConfig()
	a.mu.Unlock()
	if err != nil {
		httpError(w, 500, "保存设置失败: "+err.Error())
		return
	}
	switch {
	case c.DBDir != old.DBDir:
		a.LoadDBAsync() // 重新加载后会顺带扫描
	case c.LibraryRoot != old.LibraryRoot && c.LibraryRoot != "":
		a.jobs.Start("scan", "扫描游戏库", a.scan) // 已有任务在跑时跳过，用户可稍后手动扫描
	}
	a.hState(w, r)
}

func (a *App) hDBReload(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	dir := a.cfg.DBDir
	a.mu.RUnlock()
	if err := a.reloadDB(dir); err != nil {
		httpError(w, 500, err.Error())
		return
	}
	a.mu.RLock()
	scanned := a.files != nil
	a.mu.RUnlock()
	if scanned {
		a.jobs.Start("scan", "重新比对游戏库", a.scan) // 已有任务在跑时跳过即可
	}
	a.hState(w, r)
}

func (a *App) hDBFetch(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	dir := a.cfg.DBDir
	a.mu.RUnlock()
	a.startJob(w, "fetch-db", "更新元数据库", func(jc *JobCtx) error {
		a.mu.RLock()
		rescan := a.files != nil && a.cfg.LibraryRoot != ""
		a.mu.RUnlock()
		err := titledb.Fetch(jc, dir, jc.Logf, func(name string, done, total int64) {
			p := -1.0
			if total > 0 {
				p = float64(done) / float64(total)
			}
			jc.Progress(p, fmt.Sprintf("%s  %s", name, humanSize(done)))
		})
		jc.Progress(-1, "加载数据库")
		if lerr := a.reloadDB(dir); lerr != nil {
			return lerr
		}
		if err != nil {
			return err
		}
		jc.Summary("数据库已更新")
		if rescan {
			jc.Logf("按新数据库重新比对游戏库")
			return a.scan(jc)
		}
		return nil
	})
}

// ============================ 扫描 ============================

func (a *App) hScan(w http.ResponseWriter, r *http.Request) {
	a.startJob(w, "scan", "扫描游戏库", func(jc *JobCtx) error { return a.scan(jc) })
}

func (a *App) scan(jc *JobCtx) error {
	a.mu.Lock()
	a.invalidatePlanLocked()
	a.mu.Unlock()
	a.mu.RLock()
	root, db, keys := a.cfg.LibraryRoot, a.db, a.keys
	a.mu.RUnlock()
	if root == "" {
		return fmt.Errorf("请先在设置里填写游戏库目录")
	}
	if db == nil {
		jc.Logf("提示：数据库未加载，只能靠票据/文件名识别，版本比对不可用")
	}
	jc.Logf("扫描 %s", root)
	jc.Progress(-1, "列出文件…")
	start := time.Now()
	files, err := library.Scan(jc, root, library.ScanOptions{
		DB: db, Keys: keys, Manual: a.manual, Cache: a.cache,
		Progress: func(done, total int, cur string) {
			jc.Progress(float64(done)/float64(max(total, 1)), fmt.Sprintf("%d/%d  %s", done, total, cur))
		},
	})
	if err != nil {
		return err
	}
	games := library.Group(files)
	a.mu.Lock()
	a.invalidatePlanLocked()
	a.files, a.games, a.scannedAt, a.scanRoot = files, games, time.Now(), filepath.Clean(root)
	a.mu.Unlock()

	src := map[string]int{}
	var bad int
	for _, f := range files {
		src[f.Source]++
		if f.Error != "" {
			bad++
			jc.Logf("  [读取失败] %s：%s", f.Rel, f.Error)
		}
	}
	jc.Logf("完成：%d 个文件，%d 个游戏，用时 %s", len(files), len(games), time.Since(start).Round(time.Millisecond))
	jc.Logf("识别来源：CNMT 解密 %d、NCA 比对 %d、票据 %d、文件名 %d、手动 %d、未识别 %d",
		src[library.SrcCNMT], src[library.SrcNCA], src[library.SrcTicket], src[library.SrcFilename], src[library.SrcManual], src[""])
	jc.DefaultSummary(fmt.Sprintf("%d 个游戏 / %d 个文件", len(games), len(files)))
	return nil
}

// ============================ 游戏列表与详情 ============================

type contentDTO struct {
	TitleID string `json:"tid"`
	Kind    string `json:"kind"`
	Version int64  `json:"ver"`
}

type fileDTO struct {
	Name      string       `json:"name"`
	Rel       string       `json:"rel"`
	Ext       string       `json:"ext"`
	Size      int64        `json:"size"`
	Kind      string       `json:"kind"`
	KindCode  string       `json:"kindCode"`
	Source    string       `json:"source"`
	Version   int64        `json:"version"`
	Contents  []contentDTO `json:"contents"`
	Complete  bool         `json:"complete"`
	Redundant string       `json:"redundant,omitempty"` // 冗余原因
	Error     string       `json:"error,omitempty"`
}

type gameDTO struct {
	Key string `json:"key"`
	TID string `json:"tid"`
	library.Status
	StateLabel   string    `json:"stateLabel"`
	LocalVersion int64     `json:"localVersion"`
	HasApp       bool      `json:"hasApp"`
	HasPatch     bool      `json:"hasPatch"`
	TotalSize    int64     `json:"totalSize"`
	Redundant    int       `json:"redundant"` // 冗余文件数
	Files        []fileDTO `json:"files"`
}

func toFileDTO(f *library.RomFile) fileDTO {
	d := fileDTO{Name: f.Name, Rel: f.Rel, Ext: f.Ext, Size: f.Size, Kind: f.KindLabel(), KindCode: f.KindCode(),
		Source: f.Source, Version: f.GameVersion(), Complete: f.Complete, Error: f.Error}
	for _, c := range f.Contents {
		d.Contents = append(d.Contents, contentDTO{c.TitleID.String(), c.Kind().String(), c.Version})
	}
	return d
}

// redundantMap：文件 -> 冗余原因
func redundantMap(games []*library.Game, namer library.Namer) map[*library.RomFile]string {
	m := map[*library.RomFile]string{}
	for _, r := range library.FindRedundant(games, namer) {
		m[r.File] = r.Reason
	}
	return m
}

func (a *App) gameDTO(g *library.Game, db *titledb.DB, namer library.Namer, red map[*library.RomFile]string) gameDTO {
	st := library.Evaluate(g, db, namer)
	d := gameDTO{Key: g.Key, TID: g.BaseID.String(), Status: st, StateLabel: library.StateLabel(st.State),
		LocalVersion: g.LocalVersion, HasApp: g.HasApp, HasPatch: g.HasPatch, TotalSize: g.TotalSize}
	d.MissingDLC = nil // 列表里不需要，详情接口再给
	for _, f := range g.Files {
		fd := toFileDTO(f)
		if reason, ok := red[f]; ok {
			fd.Redundant = reason
			d.Redundant++
		}
		d.Files = append(d.Files, fd)
	}
	return d
}

func (a *App) hGames(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	games, db, namer := a.games, a.db, a.namer()
	a.mu.RUnlock()
	red := redundantMap(games, namer)
	out := make([]gameDTO, 0, len(games))
	for _, g := range games {
		out = append(out, a.gameDTO(g, db, namer, red))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out)
}

func (a *App) findGame(key string) *library.Game {
	for _, g := range a.games {
		if g.Key == key {
			return g
		}
	}
	return nil
}

func (a *App) hGame(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	g := a.findGame(r.PathValue("key"))
	if g == nil {
		httpError(w, 404, "游戏不存在（请重新扫描）")
		return
	}
	namer := a.namer()
	d := a.gameDTO(g, a.db, namer, redundantMap([]*library.Game{g}, namer))
	d.MissingDLC = library.Evaluate(g, a.db, namer).MissingDLC
	resp := map[string]any{"game": d}
	if g.BaseID != 0 {
		resp["info"] = library.GameInfo(g.BaseID, a.db, namer)
		resp["patches"] = library.PatchHistory(g, a.db)
		resp["dlcs"] = library.DLCList(g, a.db, namer)
	}
	writeJSON(w, resp)
}

// ============================ 中文名 / 手动匹配 ============================

func (a *App) hSetName(w http.ResponseWriter, r *http.Request) {
	tid, ok := nsw.ParseTitleID(r.PathValue("tid"))
	if !ok {
		httpError(w, 400, "TitleID 格式错误")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invalidatePlanLocked()
	if err := a.custom.Set(tid.String(), body.Name); err != nil {
		httpError(w, 500, "保存失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) hNames(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	db, simp := a.db, a.cfg.Simplified
	a.mu.RUnlock()
	type row struct {
		TID     string `json:"tid"`
		Name    string `json:"name"`
		LibName string `json:"libName"`
	}
	var out []row
	for _, k := range a.custom.Keys() {
		rw := row{TID: k, Name: a.custom.Get(k)}
		if t, ok := nsw.ParseTitleID(k); ok && db != nil {
			rw.LibName = db.Name(t, simp)
		}
		out = append(out, rw)
	}
	writeJSON(w, map[string]any{"path": a.custom.Path(), "names": out})
}

func (a *App) hImportNames(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invalidatePlanLocked()
	n, err := library.ImportNamesCSV(http.MaxBytesReader(w, r.Body, 16<<20), a.custom)
	if err != nil {
		httpError(w, 400, "导入失败: "+err.Error())
		return
	}
	writeJSON(w, map[string]int{"changed": n, "total": a.custom.Len()})
}

func (a *App) hExport(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	games, db, namer := a.games, a.db, a.namer()
	a.mu.RUnlock()
	var buf bytes.Buffer
	var name string
	switch r.PathValue("kind") {
	case "todo.csv":
		if db == nil {
			httpError(w, 400, "数据库未加载")
			return
		}
		library.WriteTodoCSV(&buf, games, db, namer)
		name = "待补清单.csv"
	case "library.csv":
		library.WriteLibraryCSV(&buf, games, db, namer)
		name = "游戏库清单.csv"
	case "names.csv":
		library.WriteNamesCSV(&buf, games, a.custom, namer)
		name = "游戏中文名表.csv"
	default:
		httpError(w, 404, "未知导出类型")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	w.Write(buf.Bytes())
}

func (a *App) hSearch(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	db, simp := a.db, a.cfg.Simplified
	a.mu.RUnlock()
	if db == nil {
		httpError(w, 400, "数据库未加载")
		return
	}
	writeJSON(w, db.Search(r.URL.Query().Get("q"), simp, 60))
}

func (a *App) hManual(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File string `json:"file"` // 文件名（不含路径）
		TID  string `json:"tid"`  // 空 = 清除
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	body.File = filepath.Base(strings.TrimSpace(body.File))
	if body.TID != "" {
		t, ok := nsw.ParseTitleID(body.TID)
		if !ok {
			httpError(w, 400, "TitleID 格式错误")
			return
		}
		body.TID = t.String()
	}
	a.mu.Lock()
	a.invalidatePlanLocked()
	err := a.manual.Set(body.File, body.TID)
	a.mu.Unlock()
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	// 重新推导（有缓存，很快）
	a.startJob(w, "scan", "应用手动指定", func(jc *JobCtx) error { return a.scan(jc) })
}

// ============================ 整理 ============================

// cachedPlan is owned by App.mu and never changed after publication.
// A single current preview bounds memory and makes replaced previews explicit.
type cachedPlan struct {
	token      string
	generation uint64
	root       string
	items      []library.PlanItem
	files      map[string]os.FileInfo
}

func (a *App) invalidatePlanLocked() { a.plan = nil; a.planGeneration++ }

// Snapshot metadata for the tree, including directory entries and symlinks.
// Do not hash multi-GB ROMs; identity, mode, size and nanosecond mtime are
// preconditions, with journal operations enforcing the filesystem boundary.
func planFiles(root string) (map[string]os.FileInfo, error) {
	out := map[string]os.FileInfo{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = info
		return nil
	})
	return out, err
}

func (a *App) hPlan(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.jobs.Running() {
		httpError(w, 409, "已有任务在运行，请完成后重新预览")
		return
	}
	if a.files == nil || a.scanRoot != filepath.Clean(a.cfg.LibraryRoot) {
		httpError(w, 400, "请先扫描当前游戏库")
		return
	}
	items, err := library.BuildPlan(a.scanRoot, a.files, a.games, a.namer(), a.cfg.Templates)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if items == nil {
		items = []library.PlanItem{}
	}
	files, err := planFiles(a.scanRoot)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	token := rand.Text()
	a.plan = &cachedPlan{token: token, generation: a.planGeneration, root: a.scanRoot, items: items, files: files}
	w.Header().Set("X-Plan-Token", token)
	writeJSON(w, items)
}

func (a *App) selectedPlanLocked(token string, keys []string) (*cachedPlan, []library.PlanItem, error) {
	p := a.plan
	stale := fmt.Errorf("整理预览已失效，请重新生成预览")
	if p == nil || token == "" || token != p.token || p.generation != a.planGeneration || p.root != filepath.Clean(a.cfg.LibraryRoot) {
		return nil, nil, stale
	}
	current, err := planFiles(p.root)
	if err != nil || len(current) != len(p.files) {
		return nil, nil, stale
	}
	for path, before := range p.files {
		after := current[path]
		if after == nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
			return nil, nil, stale
		}
	}
	want := map[string]bool{}
	for _, key := range keys {
		want[key] = true
	}
	var items []library.PlanItem
	for _, item := range p.items {
		if want[item.Key] {
			if item.Blocked != "" {
				return nil, nil, fmt.Errorf("所选条目不可执行，请重新预览")
			}
			items = append(items, item)
			delete(want, item.Key)
		}
	}
	if len(want) != 0 || len(items) == 0 {
		return nil, nil, fmt.Errorf("所选条目无效，请重新预览")
	}
	return p, items, nil
}

func taskResult(failed int, err error) error {
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("操作失败 %d 项，请查看日志", failed)
	}
	return nil
}

// Always refresh after partial operations, without masking the original error.
func (a *App) refreshAfter(jc *JobCtx, original error) error {
	err := a.scan(jc)
	if original != nil {
		if err != nil {
			jc.Logf("重新扫描失败：%v", err)
		}
		return original
	}
	return err
}

func (a *App) hApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys      []string `json:"keys"`
		MediaMode string   `json:"mediaMode"`
		Token     string   `json:"token"`
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	// Validate synchronously, then revalidate under the same state lock in the
	// worker: config/name requests cannot race the actual filesystem execution.
	a.mu.Lock()
	_, _, err := a.selectedPlanLocked(body.Token, body.Keys)
	a.mu.Unlock()
	if err != nil {
		httpError(w, 409, err.Error())
		return
	}
	a.startJob(w, "organize", "执行整理", func(jc *JobCtx) error {
		err := func() (result error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			p, items, err := a.selectedPlanLocked(body.Token, body.Keys)
			if err != nil {
				return err
			}
			a.invalidatePlanLocked()
			j, err := library.NewJournal(p.root, fmt.Sprintf("整理 %d 个游戏", len(items)))
			if err != nil {
				return err
			}
			defer func() {
				if err := j.Close(); result == nil {
					result = err
				}
			}()
			j.SetManual(a.manual)
			jc.Progress(-1, "移动文件")
			res := library.ExecutePlan(p.root, items, j, jc.Logf)
			original := taskResult(res.Failed, j.Err())
			jc.Logf("整理结果：成功 %d，跳过 %d，失败 %d", res.OK, res.Skipped, res.Failed)
			if original == nil && body.MediaMode != "" && body.MediaMode != library.MediaNone && len(res.Renames) > 0 {
				mp, err := library.BuildMediaPlan(p.root, library.RenameMapFrom(res.Renames), body.MediaMode)
				if err != nil {
					original = err
				} else {
					ok, failed := library.ExecuteMediaPlan(p.root, mp, j, jc.Logf)
					jc.Logf("[媒体] 成功 %d，失败 %d", ok, failed)
					original = taskResult(failed, j.Err())
				}
			}
			if j.Len() > 0 {
				jc.Logf("撤销日志：%s", filepath.Base(j.Path()))
			}
			jc.Summary(fmt.Sprintf("成功 %d / 跳过 %d / 失败 %d", res.OK, res.Skipped, res.Failed))
			return original
		}()
		return a.refreshAfter(jc, err)
	})
}

func (a *App) libRoot() (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg.LibraryRoot == "" {
		return "", fmt.Errorf("请先设置游戏库目录")
	}
	return filepath.Clean(a.cfg.LibraryRoot), nil
}

func (a *App) hUndoList(w http.ResponseWriter, r *http.Request) {
	root, err := a.libRoot()
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	type item struct {
		File    string       `json:"file"`
		Title   string       `json:"title"`
		Created string       `json:"created"`
		Moves   int          `json:"moves"`
		Ops     int          `json:"ops"`
		Error   string       `json:"error,omitempty"`
		Sample  []library.Op `json:"sample,omitempty"`
	}
	out := []item{}
	for _, p := range library.ListUndoLogs(root) {
		it := item{File: filepath.Base(p)}
		if fi, err := os.Stat(p); err == nil {
			it.Created = fi.ModTime().Format("2006-01-02 15:04:05")
		}
		l, err := library.ReadUndoLog(p)
		if err != nil {
			it.Error = err.Error()
		} else {
			it.Title, it.Ops = l.Title, len(l.Ops)
			for _, op := range l.Ops {
				if op.Op == "move" {
					it.Moves++
					if len(it.Sample) < 8 {
						it.Sample = append(it.Sample, library.Op{Op: "move", From: rel(root, op.From), To: rel(root, op.To)})
					}
				}
			}
		}
		out = append(out, it)
	}
	writeJSON(w, out)
}

func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func (a *App) hUndoApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File string `json:"file"`
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	root, err := a.libRoot()
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	name := filepath.Base(body.File)
	if !strings.HasPrefix(name, "_undo_") || !strings.HasSuffix(name, ".json") {
		httpError(w, 400, "不是撤销日志")
		return
	}
	p := filepath.Join(root, name)
	a.startJob(w, "undo", "撤销操作", func(jc *JobCtx) error {
		l, err := library.ReadUndoLog(p)
		if err != nil {
			return err
		}
		ok, failed := library.Undo(l, jc.Logf)
		jc.Logf("撤销结果：成功 %d，失败 %d", ok, failed)
		err = taskResult(failed, nil)
		if err == nil {
			err = library.CompleteUndo(l)
		}
		jc.Summary(fmt.Sprintf("还原 %d 项 / 失败 %d 项", ok, failed))
		return a.refreshAfter(jc, err)
	})
}

func (a *App) hMediaPlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	readJSON(r, &body)
	root, err := a.libRoot()
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	mf := library.LatestManifest(root)
	if mf == "" {
		httpError(w, 400, "库目录下找不到整理清单（_before_*.txt），先执行一次整理才会生成")
		return
	}
	rm, err := library.LoadManifest(mf, root)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	pl, err := library.BuildMediaPlan(root, rm, body.Mode)
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"manifest": filepath.Base(mf), "entries": len(rm), "plan": pl})
}

func (a *App) hMediaApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	readJSON(r, &body)
	root, err := a.libRoot()
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	a.startJob(w, "media", "同步媒体与 gamelist.xml", func(jc *JobCtx) (result error) {
		mf := library.LatestManifest(root)
		if mf == "" {
			return fmt.Errorf("找不到整理清单")
		}
		rm, err := library.LoadManifest(mf, root)
		if err != nil {
			return err
		}
		pl, err := library.BuildMediaPlan(root, rm, body.Mode)
		if err != nil {
			return err
		}
		j, err := library.NewJournal(root, "同步媒体")
		if err != nil {
			return err
		}
		defer func() {
			if err := j.Close(); result == nil {
				result = err
			}
		}()
		ok, fail := library.ExecuteMediaPlan(root, pl, j, jc.Logf)
		jc.Summary(fmt.Sprintf("成功 %d / 失败 %d", ok, fail))
		return a.refreshAfter(jc, taskResult(fail, j.Err()))
	})
}

// ============================ 冗余文件 ============================

func (a *App) hRedundantList(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	root := a.scanRoot
	list := library.FindRedundant(a.games, a.namer())
	a.mu.RUnlock()
	if list == nil {
		list = []library.RedundantFile{}
	}
	var total int64
	for _, x := range list {
		total += x.Size
	}
	resp := map[string]any{"files": list, "size": total, "dir": library.RedundantDir}
	if root != "" {
		n, size := library.RedundantDirSize(root)
		resp["binFiles"], resp["binSize"] = n, size
	}
	writeJSON(w, resp)
}

func (a *App) hRedundantApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rels []string `json:"rels"`
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	want := map[string]bool{}
	for _, k := range body.Rels {
		want[k] = true
	}
	a.startJob(w, "redundant", "移走冗余文件", func(jc *JobCtx) (result error) {
		// 按最新扫描结果重新判断，只移走仍然冗余、且被用户勾选的文件
		a.mu.RLock()
		root := a.scanRoot
		stale := a.scanRoot != filepath.Clean(a.cfg.LibraryRoot)
		all := library.FindRedundant(a.games, a.namer())
		a.mu.RUnlock()
		if stale {
			return fmt.Errorf("游戏库目录已变更，请重新扫描")
		}
		var rels []string
		for _, x := range all {
			if want[x.Rel] {
				rels = append(rels, x.Rel)
			}
		}
		if len(rels) == 0 {
			return fmt.Errorf("没有选中任何文件（或选中的文件已不再冗余）")
		}
		j, err := library.NewJournal(root, fmt.Sprintf("移走冗余文件 %d 个", len(rels)))
		if err != nil {
			return err
		}
		defer func() {
			if err := j.Close(); result == nil {
				result = err
			}
		}()
		jc.Progress(-1, "移动文件")
		ok, failed := library.MoveRedundant(root, rels, j, jc.Logf)
		jc.Logf("移到 %s/：成功 %d，失败 %d。确认无误后可自行删除该目录；需要恢复时在「整理 → 撤销记录」里撤销。", library.RedundantDir, ok, failed)
		jc.Summary(fmt.Sprintf("移走 %d 个 / 失败 %d 个", ok, failed))
		return a.refreshAfter(jc, taskResult(failed, j.Err()))
	})
}

// ============================ 合并 / 转换 ============================

func (a *App) hMergeList(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	jobs := library.MergeCandidates(a.scanRoot, a.games, a.namer())
	if jobs == nil {
		jobs = []library.MergeJob{}
	}
	var xcis []fileDTO
	for _, f := range a.files {
		if f.Ext == ".xci" {
			xcis = append(xcis, toFileDTO(f))
		}
	}
	writeJSON(w, map[string]any{"merge": jobs, "xci": xcis})
}

func (a *App) hMergeApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	want := map[string]bool{}
	for _, k := range body.Keys {
		want[k] = true
	}
	a.mu.RLock()
	root := a.scanRoot
	all := library.MergeCandidates(root, a.games, a.namer())
	a.mu.RUnlock()
	var jobs []library.MergeJob
	for _, j := range all {
		if want[j.Key] {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) == 0 {
		httpError(w, 400, "没有选中任何游戏")
		return
	}
	a.startJob(w, "merge", fmt.Sprintf("合并 %d 个游戏", len(jobs)), func(jc *JobCtx) error {
		okN := 0
		for i, j := range jobs {
			jc.Logf("[%d/%d] %s", i+1, len(jobs), j.Name)
			var ins []string
			for _, in := range j.Inputs {
				ins = append(ins, filepath.Join(root, filepath.FromSlash(in)))
			}
			n, err := library.Merge(jc, ins, filepath.Join(root, filepath.FromSlash(j.Output)), func(done, total int64, stage string) {
				jc.Progress((float64(i)+float64(done)/float64(max(total, 1)))/float64(len(jobs)), fmt.Sprintf("%s %s  %s / %s", j.Name, stage, humanSize(done), humanSize(total)))
			})
			if err != nil {
				if jc.Err() != nil {
					return jc.Err()
				}
				jc.Logf("  失败：%v", err)
				continue
			}
			okN++
			jc.Logf("  ✓ 完成，%d 个文件 SHA-256 校验一致 → %s", n, j.Output)
		}
		jc.Logf("源文件保持原样未删除 —— 请确认产物可用后再自行清理。")
		jc.Summary(fmt.Sprintf("完成 %d / %d", okN, len(jobs)))
		return a.scan(jc)
	})
}

func (a *App) hXCI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rel string `json:"rel"`
	}
	if err := readJSON(r, &body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	a.mu.RLock()
	root := a.scanRoot
	var src *library.RomFile
	for _, f := range a.files {
		if f.Rel == body.Rel && f.Ext == ".xci" {
			src = f
		}
	}
	a.mu.RUnlock()
	if src == nil {
		httpError(w, 400, "找不到该 XCI 文件（请重新扫描）")
		return
	}
	out := strings.TrimSuffix(src.Path, filepath.Ext(src.Path)) + ".nsp"
	a.startJob(w, "xci2nsp", "XCI → NSP", func(jc *JobCtx) error {
		n, err := library.XCIToNSP(jc, src.Path, out, func(done, total int64, stage string) {
			jc.Progress(float64(done)/float64(max(total, 1)), fmt.Sprintf("%s %s / %s", stage, humanSize(done), humanSize(total)))
		})
		if err != nil {
			return err
		}
		jc.Logf("✓ %d 个文件校验一致 → %s", n, rel(root, out))
		jc.Logf("注意：卡带不含票据，产物是 NCA 容器，仅作归档用途。")
		jc.Summary(filepath.Base(out))
		return a.scan(jc)
	})
}

// ============================ 任务 ============================

func (a *App) startJob(w http.ResponseWriter, kind, title string, fn func(*JobCtx) error) {
	j, err := a.jobs.Start(kind, title, fn)
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, j)
}

func (a *App) hJobCurrent(w http.ResponseWriter, r *http.Request) {
	var since int
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
	job, logs := a.jobs.Snapshot(since)
	writeJSON(w, map[string]any{"job": job, "logs": logs})
}

// ============================ 目录浏览（选择路径用） ============================

func (a *App) hFS(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	files := r.URL.Query().Get("files") == "1"
	type ent struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"isDir"`
	}
	resp := map[string]any{"path": p}
	if p == "" {
		var roots []ent
		if runtime.GOOS == "windows" {
			for c := 'C'; c <= 'Z'; c++ {
				d := string(c) + `:\`
				if _, err := os.Stat(d); err == nil {
					roots = append(roots, ent{d, d, true})
				}
			}
		} else {
			roots = append(roots, ent{"/", "/", true})
			for _, d := range []string{"/mnt", "/media", "/games", "/data"} {
				if fi, err := os.Stat(d); err == nil && fi.IsDir() {
					roots = append(roots, ent{d, d, true})
				}
			}
		}
		if h, err := os.UserHomeDir(); err == nil {
			roots = append(roots, ent{"主目录", h, true})
		}
		resp["entries"] = roots
		writeJSON(w, resp)
		return
	}
	p = filepath.Clean(p)
	ds, err := os.ReadDir(p)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	var out []ent
	for _, d := range ds {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		isDir := d.IsDir()
		if !isDir && d.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(p, d.Name())); err == nil {
				isDir = fi.IsDir()
			}
		}
		if isDir || files {
			out = append(out, ent{d.Name(), filepath.Join(p, d.Name()), isDir})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	resp["path"] = p
	if parent := filepath.Dir(p); parent != p {
		resp["parent"] = parent
	} else {
		resp["parent"] = ""
	}
	resp["entries"] = out
	writeJSON(w, resp)
}

func humanSize(b int64) string {
	const u = 1024
	switch {
	case b >= u*u*u*u:
		return fmt.Sprintf("%.2f TB", float64(b)/float64(u*u*u*u))
	case b >= u*u*u:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(u*u*u))
	case b >= u*u:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(u*u))
	}
	return fmt.Sprintf("%d KB", b/u)
}
