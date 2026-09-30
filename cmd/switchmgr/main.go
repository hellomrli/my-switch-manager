// my-switch-manager —— Switch 游戏库管理器
//
// 无参数运行：启动本机界面并自动打开浏览器（桌面模式）。
// 其余子命令见 usage()。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"my-switch-manager/internal/library"
	"my-switch-manager/internal/nsw"
	"my-switch-manager/internal/server"
	"my-switch-manager/internal/titledb"
)

var version = "dev"

func usage() {
	fmt.Print(`my-switch-manager ` + version + ` —— Switch 游戏库管理器

用法:
  switchmgr                         启动界面并打开浏览器（桌面模式）
  switchmgr serve [选项]            以服务方式运行（NAS / Docker）
      --listen 0.0.0.0:8765          监听地址（非回环地址需配合 --password 或确认处在可信内网）
      --password 密码                 启用 HTTP Basic 认证（也可用环境变量 SWITCHMGR_PASSWORD）
      --no-browser                    不自动打开浏览器
  switchmgr scan <目录>             扫描并列出识别结果
  switchmgr check <目录> [--todo 待补清单.csv]
                                    比对版本 / DLC
  switchmgr organize <目录> [--apply] [--media keep|into-folder]
                                    按中文名整理（默认只预览）
  switchmgr undo <_undo_*.json> [--apply]
                                    按撤销日志还原
  switchmgr redundant <目录> [--apply]
                                    列出冗余文件（旧版更新、重复、已合并的源）；
                                    --apply 移到库根的 _redundant/（可撤销，不删除）
  switchmgr findid <关键字>          在数据库里搜 TitleID
  switchmgr merge <输入...> --out <输出.nsp>
                                    合并 NSP（纯容器层，逐文件 SHA-256 校验）
  switchmgr xci2nsp <输入.xci> [--out 输出.nsp]
  switchmgr fetch-db                下载 / 更新元数据库
  switchmgr migrate <旧版 SwitchRomMgr 目录>
                                    导入旧版的 custom_names.json / manual_ids.json

通用选项:
  --data 目录    数据目录（设置、自定义名、扫描缓存；默认为程序所在目录，不可写时用用户配置目录）
  --db 目录      元数据库目录（默认取设置里的值）
  --keys 文件    prod.keys（可选，用于精确识别）
`)
}

func main() {
	attachConsole()
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	server.Version = version
	switch cmd {
	case "":
		runServe(args, true)
	case "serve":
		runServe(args, false)
	case "scan", "check":
		runCheck(cmd, args)
	case "organize":
		runOrganize(args)
	case "undo":
		runUndo(args)
	case "redundant":
		runRedundant(args)
	case "findid":
		runFindID(args)
	case "merge", "xci2nsp":
		runMerge(cmd, args)
	case "fetch-db":
		runFetch(args)
	case "migrate":
		runMigrate(args)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

// ============================ 公共参数 ============================

type common struct {
	fs   *flag.FlagSet
	data *string
	db   *string
	keys *string
}

func newFlags(name string) *common {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = usage
	return &common{fs: fs, data: fs.String("data", "", ""), db: fs.String("db", "", ""), keys: fs.String("keys", "", "")}
}

// parse 允许选项出现在位置参数之后（flag 包默认遇到第一个位置参数就停止解析）
func (c *common) parse(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if f := c.fs.Lookup(name); f != nil && !strings.Contains(a, "=") && !isBool(f) && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	c.fs.Parse(flags)
	return append(pos, c.fs.Args()...)
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Dir(p)
}

// dataDir：优先程序所在目录（绿色便携，与旧版一致），不可写时退到用户配置目录
func dataDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("SWITCHMGR_DATA"); v != "" {
		return v
	}
	d := exeDir()
	if f, err := os.CreateTemp(d, ".write-test-*"); err == nil {
		f.Close()
		os.Remove(f.Name())
		// go run 产生的临时目录不适合存数据
		if !strings.Contains(d, "go-build") {
			return d
		}
	}
	if c, err := os.UserConfigDir(); err == nil {
		return filepath.Join(c, "my-switch-manager")
	}
	return "."
}

// env 汇总 CLI 需要的数据库、密钥与持久化数据
type env struct {
	dir    string
	cfg    server.Config
	db     *titledb.DB
	keys   *nsw.Keys
	custom *library.StringMap
	manual *library.StringMap
}

func loadEnv(c *common, needDB bool) *env {
	e := &env{dir: dataDir(*c.data)}
	e.cfg = server.Config{Simplified: true, DBDir: filepath.Join(e.dir, "titledb")}
	readJSON(filepath.Join(e.dir, "config.json"), &e.cfg)
	if *c.db != "" {
		e.cfg.DBDir = *c.db
	}
	if *c.keys != "" {
		e.cfg.KeysPath = *c.keys
	}
	e.custom = library.OpenStringMap(filepath.Join(e.dir, "custom_names.json"), true)
	e.manual = library.OpenStringMap(filepath.Join(e.dir, "manual_ids.json"), false)
	if e.cfg.KeysPath != "" {
		k, err := nsw.LoadKeys(e.cfg.KeysPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "提示：密钥读取失败（%v），改用非解密方式识别\n", err)
		} else {
			e.keys = k
		}
	}
	db, err := titledb.Load(e.cfg.DBDir)
	if err != nil {
		if needDB {
			fatalf("加载数据库失败：%v\n可先运行 switchmgr fetch-db，或用 --db 指定数据库目录", err)
		}
		fmt.Fprintf(os.Stderr, "提示：未加载数据库（%v）\n", err)
	} else {
		e.db = db
		for _, w := range db.Warnings {
			fmt.Fprintln(os.Stderr, "提示：", w)
		}
	}
	return e
}

func (e *env) namer() library.Namer {
	return library.Namer{DB: e.db, Custom: e.custom, Simplified: e.cfg.Simplified}
}

func (e *env) scan(root string) ([]*library.RomFile, []*library.Game) {
	cache := library.OpenScanCache(filepath.Join(e.dir, "scan_cache.json"))
	isTTY := false
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		isTTY = true
	}
	files, err := library.Scan(context.Background(), root, library.ScanOptions{
		DB: e.db, Keys: e.keys, Manual: e.manual, Cache: cache,
		Progress: func(done, total int, cur string) {
			if isTTY {
				fmt.Fprintf(os.Stderr, "\r扫描 %d/%d ", done, total)
			}
		},
	})
	if isTTY {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if err != nil {
		fatalf("扫描失败：%v", err)
	}
	return files, library.Group(files)
}

// ============================ 子命令 ============================

func runCheck(cmd string, args []string) {
	c := newFlags(cmd)
	todo := c.fs.String("todo", "", "")
	pos := c.parse(args)
	if len(pos) < 1 {
		fatalf("用法: switchmgr %s <目录>", cmd)
	}
	e := loadEnv(c, cmd == "check")
	files, games := e.scan(pos[0])
	namer := e.namer()
	if cmd == "scan" {
		fmt.Printf("%d 个文件，%d 个游戏\n\n", len(files), len(games))
		for _, f := range files {
			fmt.Println(pad(orDash(f.BaseID.String()), 18), pad(srcLabel(f.Source), 9), pad(f.KindLabel(), 16), pad(humanSize(f.Size), 10), f.Rel)
		}
		return
	}
	counts := map[string]int{}
	fmt.Println(pad("游戏", 36), pad("TitleID", 18), pad("本地", 10), pad("最新", 10), pad("DLC", 8), "状态")
	fmt.Println(strings.Repeat("-", 100))
	for _, g := range games {
		st := library.Evaluate(g, e.db, namer)
		counts[st.State]++
		if st.DLCMissing() > 0 {
			counts["dlc"]++
		}
		dlc := "-"
		if st.DLCTotal > 0 || st.DLCOwned > 0 {
			dlc = fmt.Sprintf("%d/%d", st.DLCOwned, st.DLCTotal)
			if st.DLCApprox {
				dlc += "≈"
			}
		}
		latest := "-"
		if st.Latest >= 0 {
			latest = fmt.Sprint(st.Latest)
		}
		state := library.StateLabel(st.State)
		if st.State == library.StateOutdated {
			state = fmt.Sprintf("★ 可更新（落后 %d 版）", st.Behind)
		}
		fmt.Println(pad(trunc(st.Name, 36), 36), pad(orDash(g.BaseID.String()), 18), pad(verStr(g.LocalVersion), 10), pad(latest, 10), pad(dlc, 8), state)
	}
	fmt.Printf("\n合计 %d 个游戏：可更新 %d、已最新 %d、缺 DLC %d、版本未知 %d、未识别 %d\n", len(games),
		counts[library.StateOutdated], counts[library.StateLatest], counts["dlc"],
		counts[library.StateUnknown]+counts[library.StateNoRecord], counts[library.StateUnidentified])
	if *todo != "" {
		f, err := os.Create(*todo)
		if err != nil {
			fatalf("%v", err)
		}
		n, err := library.WriteTodoCSV(f, games, e.db, namer)
		f.Close()
		if err != nil {
			fatalf("导出失败：%v", err)
		}
		fmt.Printf("待补清单已导出：%s（%d 个游戏）\n", *todo, n)
	}
}

func runOrganize(args []string) {
	c := newFlags("organize")
	apply := c.fs.Bool("apply", false, "")
	media := c.fs.String("media", "none", "")
	pos := c.parse(args)
	if len(pos) < 1 {
		fatalf("用法: switchmgr organize <目录> [--apply] [--media keep|into-folder]")
	}
	root, _ := filepath.Abs(pos[0])
	e := loadEnv(c, false)
	files, games := e.scan(root)
	items, err := library.BuildPlan(root, files, games, e.namer(), e.cfg.Templates)
	if err != nil {
		fatalf("%v", err)
	}
	if len(items) == 0 {
		fmt.Println("没有需要整理的条目（没有中文名的游戏不会被改动）。")
		return
	}
	for _, it := range items {
		mode := map[string]string{library.ModeRename: "文件夹改名", library.ModeMove: "移动文件", library.ModeCreate: "新建文件夹", library.ModeInPlace: "仅改文件名"}[it.Mode]
		fmt.Printf("\n[%s] %s\n  %s\n  → %s\n", mode, it.Name, it.OldDir, it.TargetDir)
		for _, m := range it.Moves {
			fmt.Printf("      %s\n    → %s\n", filepath.Base(m.From), filepath.Base(m.To))
		}
		for _, w := range it.Warnings {
			fmt.Printf("  ⚠ %s\n", w)
		}
		if it.Blocked != "" {
			fmt.Printf("  ⛔ %s\n", it.Blocked)
		}
	}
	fmt.Printf("\n共 %d 个游戏。\n", len(items))
	if !*apply {
		fmt.Println("以上为预览，确认无误后加 --apply 执行。")
		return
	}
	j, err := library.NewJournal(root, fmt.Sprintf("整理 %d 个游戏", len(items)))
	if err != nil {
		fatalf("%v", err)
	}
	j.SetManual(e.manual)
	logf := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }
	res := library.ExecutePlan(root, items, j, logf)
	mediaFailed := 0
	if *media != library.MediaNone && len(res.Renames) > 0 && j.Err() == nil {
		mp, err := library.BuildMediaPlan(root, library.RenameMapFrom(res.Renames), *media)
		if err != nil {
			mediaFailed++
			logf("[失败] 媒体方案：%v", err)
		} else {
			_, mediaFailed = library.ExecuteMediaPlan(root, mp, j, logf)
		}
	}
	closeErr := j.Close()
	fmt.Printf("\n结果：成功 %d，跳过 %d，失败 %d\n", res.OK, res.Skipped, res.Failed+mediaFailed)
	if j.Len() > 0 {
		fmt.Printf("撤销：switchmgr undo \"%s\" --apply\n", j.Path())
	}
	if j.Err() != nil {
		fatalf("%v", j.Err())
	}
	if closeErr != nil {
		fatalf("关闭库目录失败：%v", closeErr)
	}
	if res.Failed+mediaFailed > 0 {
		fatalf("整理未完全成功，请检查日志后重试或撤销")
	}
}

func runUndo(args []string) {
	c := newFlags("undo")
	apply := c.fs.Bool("apply", false, "")
	pos := c.parse(args)
	if len(pos) < 1 {
		fatalf("用法: switchmgr undo <_undo_*.json> [--apply]")
	}
	l, err := library.ReadUndoLog(pos[0])
	if err != nil {
		fatalf("读取撤销日志失败：%v", err)
	}
	moves := 0
	for _, op := range l.Ops {
		if op.Op == "move" {
			moves++
		}
	}
	fmt.Printf("%s：%d 步操作（%d 次移动）\n", l.Title, len(l.Ops), moves)
	if !*apply {
		for i := len(l.Ops) - 1; i >= 0 && i >= len(l.Ops)-15; i-- {
			if op := l.Ops[i]; op.Op == "move" {
				fmt.Printf("  %s\n    → %s\n", op.To, op.From)
			}
		}
		fmt.Println("以上为预览，加 --apply 执行。")
		return
	}
	ok, failed := library.Undo(l, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
	fmt.Printf("结果：成功 %d，失败 %d\n", ok, failed)
	if failed != 0 {
		fatalf("撤销未完成，解决冲突后可使用同一日志重试")
	}
	if err := library.CompleteUndo(l); err != nil {
		fatalf("保存撤销完成标记失败：%v", err)
	}
}

func runRedundant(args []string) {
	c := newFlags("redundant")
	apply := c.fs.Bool("apply", false, "")
	pos := c.parse(args)
	if len(pos) < 1 {
		fatalf("用法: switchmgr redundant <目录> [--apply]")
	}
	root, _ := filepath.Abs(pos[0])
	e := loadEnv(c, true)
	_, games := e.scan(root)
	list := library.FindRedundant(games, e.namer())
	if len(list) == 0 {
		fmt.Println("没有发现冗余文件。")
		fmt.Println("（只判断内容能被精确识别的文件：需要元数据库能比对上 NCA，或配置了 prod.keys）")
		return
	}
	var total int64
	var rels []string
	last := ""
	for _, r := range list {
		if r.Name != last {
			fmt.Printf("\n%s\n", r.Name)
			last = r.Name
		}
		fmt.Printf("  %s  %s\n    %s\n", pad(humanSize(r.Size), 10), r.Reason, r.Rel)
		for _, k := range r.KeptBy {
			fmt.Printf("    保留：%s\n", k)
		}
		total += r.Size
		rels = append(rels, r.Rel)
	}
	fmt.Printf("\n共 %d 个冗余文件，%s。\n", len(list), humanSize(total))
	if !*apply {
		fmt.Printf("以上为预览，加 --apply 移到 %s（不删除，可撤销）。\n", filepath.Join(root, library.RedundantDir))
		return
	}
	j, err := library.NewJournal(root, fmt.Sprintf("移走冗余文件 %d 个", len(rels)))
	if err != nil {
		fatalf("%v", err)
	}
	ok, failed := library.MoveRedundant(root, rels, j, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
	closeErr := j.Close()
	fmt.Printf("\n结果：成功 %d，失败 %d\n", ok, failed)
	if j.Len() > 0 {
		fmt.Printf("撤销：switchmgr undo \"%s\" --apply\n", j.Path())
	}
	if j.Err() != nil {
		fatalf("%v", j.Err())
	}
	if closeErr != nil {
		fatalf("关闭库目录失败：%v", closeErr)
	}
	if failed > 0 {
		fatalf("移走冗余未完全成功，请检查日志")
	}
}

func runFindID(args []string) {
	c := newFlags("findid")
	pos := c.parse(args)
	if len(pos) < 1 {
		fatalf("用法: switchmgr findid <关键字>")
	}
	e := loadEnv(c, true)
	for _, q := range pos {
		hits := e.db.Search(q, e.cfg.Simplified, 20)
		fmt.Printf("=== 「%s」：%d 条 ===\n", q, len(hits))
		for _, h := range hits {
			dlc := ""
			if h.IsDLC {
				dlc = " [DLC]"
			}
			fmt.Printf("  %s  %-10s %s%s\n", h.TitleID, h.Release, h.Name, dlc)
		}
	}
}

func runMerge(cmd string, args []string) {
	c := newFlags(cmd)
	out := c.fs.String("out", "", "")
	pos := c.parse(args)
	if cmd == "xci2nsp" {
		if len(pos) != 1 {
			fatalf("用法: switchmgr xci2nsp <输入.xci> [--out 输出.nsp]")
		}
		if *out == "" {
			*out = strings.TrimSuffix(pos[0], filepath.Ext(pos[0])) + ".nsp"
		}
	} else if len(pos) < 2 || *out == "" {
		fatalf("用法: switchmgr merge <输入1> <输入2> [...] --out <输出.nsp>")
	}
	last := time.Now()
	n, err := library.Merge(context.Background(), pos, *out, func(done, total int64, stage string) {
		if time.Since(last) > 500*time.Millisecond || done == total {
			last = time.Now()
			fmt.Fprintf(os.Stderr, "\r%s %5.1f%%  %s / %s ", stage, float64(done)*100/float64(max(total, 1)), humanSize(done), humanSize(total))
		}
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fatalf("%v", err)
	}
	fmt.Printf("✓ 完成，%d 个文件逐一 SHA-256 校验一致 → %s\n", n, *out)
	if cmd == "xci2nsp" {
		fmt.Println("注意：卡带不含票据，产物是 NCA 容器，仅作归档用途。")
	}
}

func runFetch(args []string) {
	c := newFlags("fetch-db")
	c.parse(args)
	e := loadEnv(c, false)
	err := titledb.Fetch(context.Background(), e.cfg.DBDir, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }, nil)
	fmt.Printf("数据库位置：%s\n", e.cfg.DBDir)
	if err != nil {
		fatalf("%v", err)
	}
}

func runMigrate(args []string) {
	c := newFlags("migrate")
	pos := c.parse(args)
	if len(pos) < 1 {
		fatalf("用法: switchmgr migrate <旧版 SwitchRomMgr 目录>")
	}
	dir := dataDir(*c.data)
	for _, pair := range []struct {
		name  string
		upper bool
	}{{"custom_names.json", true}, {"manual_ids.json", false}} {
		var old map[string]string
		if err := readJSON(filepath.Join(pos[0], pair.name), &old); err != nil {
			fmt.Printf("%s：跳过（%v）\n", pair.name, err)
			continue
		}
		dst := library.OpenStringMap(filepath.Join(dir, pair.name), pair.upper)
		add := map[string]string{}
		for k, v := range old {
			if dst.Get(k) == "" {
				add[k] = v
			}
		}
		if err := dst.SetMany(add); err != nil {
			fatalf("写入 %s 失败：%v", pair.name, err)
		}
		fmt.Printf("%s：导入 %d 条（已有的不覆盖），现共 %d 条\n", pair.name, len(add), dst.Len())
	}
	if fi, err := os.Stat(filepath.Join(pos[0], "titledb", "versions.json")); err == nil && !fi.IsDir() {
		fmt.Printf("提示：旧目录里有 titledb，可在设置里把「元数据库目录」指向 %s\n", filepath.Join(pos[0], "titledb"))
	}
	fmt.Printf("数据目录：%s\n", dir)
}

// ============================ 服务 ============================

func runServe(args []string, desktop bool) {
	c := newFlags("serve")
	listen := c.fs.String("listen", "127.0.0.1:8765", "")
	password := c.fs.String("password", os.Getenv("SWITCHMGR_PASSWORD"), "")
	noBrowser := c.fs.Bool("no-browser", false, "")
	c.parse(args)
	dir := dataDir(*c.data)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatalf("无法创建数据目录 %s：%v", dir, err)
	}
	if desktop {
		// 双击运行时没有控制台，日志写文件方便排错
		if lf, err := os.OpenFile(filepath.Join(dir, "switchmgr.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, lf))
		}
	}

	host, _, _ := net.SplitHostPort(*listen)
	remote := !(host == "127.0.0.1" || host == "localhost" || host == "::1")
	if remote && *password == "" {
		log.Printf("警告：正在监听 %s 且未设置密码，局域网内任何人都能整理/改名你的游戏文件。建议加 --password。", *listen)
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		// 已有实例在运行：直接打开它
		if desktop && isOurs("http://"+*listen) {
			openBrowser("http://" + *listen)
			return
		}
		if desktop {
			ln, err = net.Listen("tcp", "127.0.0.1:0")
		}
		if err != nil {
			fatalf("无法监听 %s：%v", *listen, err)
		}
	}
	url := "http://" + ln.Addr().String()
	if strings.HasPrefix(ln.Addr().String(), "0.0.0.0:") || strings.HasPrefix(ln.Addr().String(), "[::]:") {
		url = fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	}

	stop := make(chan struct{}, 1)
	app, err := server.New(server.Options{
		DataDir: dir, Listen: *listen, Password: *password, AllowRemote: remote, Desktop: desktop,
		OnShutdown: func() { stop <- struct{}{} },
	})
	if err != nil {
		fatalf("%v", err)
	}
	app.Start()
	srv := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	log.Printf("my-switch-manager %s 已启动：%s（数据目录 %s）", version, url, dir)
	if desktop && !*noBrowser {
		openBrowser(url)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-stop:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func isOurs(base string) bool {
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(base + "/api/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return strings.Contains(string(b), server.AppName)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("无法自动打开浏览器，请手动访问 %s", url)
	}
}

// ============================ 小工具 ============================

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func srcLabel(s string) string {
	return map[string]string{library.SrcCNMT: "CNMT", library.SrcNCA: "NCA", library.SrcTicket: "票据",
		library.SrcFilename: "文件名", library.SrcManual: "手动", "": "未识别"}[s]
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func verStr(v int64) string {
	if v < 0 {
		return "未知"
	}
	return fmt.Sprint(v)
}

// dispWidth 估算终端显示宽度：CJK / 全角字符占两格
func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6, r >= 0x1F300 && r <= 0x1FAFF, r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// pad 按显示宽度右侧补空格
func pad(s string, n int) string {
	if w := dispWidth(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// trunc 按显示宽度截断，超出时以「…」结尾
func trunc(s string, n int) string {
	if dispWidth(s) <= n {
		return s
	}
	w := 0
	var b strings.Builder
	for _, r := range s {
		if w+runeWidth(r) > n-1 {
			break
		}
		w += runeWidth(r)
		b.WriteRune(r)
	}
	return b.String() + "…"
}

func humanSize(b int64) string {
	const u = 1024
	switch {
	case b >= u*u*u:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(u*u*u))
	case b >= u*u:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(u*u))
	}
	return fmt.Sprintf("%d KB", b/u)
}
