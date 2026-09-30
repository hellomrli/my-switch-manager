// Package server 提供浏览器界面与 JSON API。桌面模式监听 127.0.0.1，NAS/Docker 模式可监听局域网并加密码。
package server

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"my-switch-manager/internal/library"
	"my-switch-manager/internal/nsw"
	"my-switch-manager/internal/titledb"
)

//go:embed web
var webFS embed.FS

const AppName = "my-switch-manager"

var Version = "dev"

// Config 是持久化到 <数据目录>/config.json 的用户设置
type Config struct {
	LibraryRoot string            `json:"libraryRoot"`
	DBDir       string            `json:"dbDir"`
	KeysPath    string            `json:"keysPath"`
	Simplified  bool              `json:"simplified"`
	Templates   library.Templates `json:"templates"`
	MediaMode   string            `json:"mediaMode"`
}

type Options struct {
	DataDir     string
	Listen      string
	Password    string // 非空时启用 HTTP Basic 认证（用户名任意）
	AllowRemote bool   // 监听非回环地址
	Desktop     bool   // 桌面模式：界面上提供「退出」
	OnShutdown  func()
}

type App struct {
	opt  Options
	jobs Jobs

	mu             sync.RWMutex
	cfg            Config
	db             *titledb.DB
	dbErr          string
	keys           *nsw.Keys
	keysErr        string
	custom         *library.StringMap
	manual         *library.StringMap
	cache          *library.ScanCache
	files          []*library.RomFile
	games          []*library.Game
	scannedAt      time.Time
	scanRoot       string
	plan           *cachedPlan
	planGeneration uint64
}

func New(opt Options) (*App, error) {
	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return nil, err
	}
	a := &App{opt: opt}
	a.cfg = Config{Simplified: true, MediaMode: library.MediaNone, DBDir: filepath.Join(opt.DataDir, "titledb")}
	loadJSONFile(a.path("config.json"), &a.cfg)
	a.custom = library.OpenStringMap(a.path("custom_names.json"), true)
	a.manual = library.OpenStringMap(a.path("manual_ids.json"), false)
	a.cache = library.OpenScanCache(a.path("scan_cache.json"))
	a.loadKeys()
	return a, nil
}

func (a *App) path(name string) string { return filepath.Join(a.opt.DataDir, name) }

// Start 在后台加载数据库（约 1 秒），不阻塞界面启动。
// 设置过游戏库目录时接着扫描一遍：有缓存只需列目录，重启后不必再手动点「扫描」。
func (a *App) Start() {
	a.mu.RLock()
	root := a.cfg.LibraryRoot
	a.mu.RUnlock()
	if root == "" {
		a.LoadDBAsync()
		return
	}
	if _, err := a.jobs.Start("scan", "载入游戏库", a.reloadAndScan); err != nil {
		a.LoadDBAsync()
	}
}

// LoadDBAsync 后台加载数据库；已扫描过时顺带重新比对（识别结果依赖数据库）
func (a *App) LoadDBAsync() {
	a.mu.RLock()
	scanned := a.files != nil
	a.mu.RUnlock()
	if scanned {
		if _, err := a.jobs.Start("scan", "重新加载元数据库", a.reloadAndScan); err == nil {
			return
		}
	}
	go func() {
		a.mu.RLock()
		dir := a.cfg.DBDir
		a.mu.RUnlock()
		a.reloadDB(dir)
	}()
}

func (a *App) reloadAndScan(jc *JobCtx) error {
	a.mu.RLock()
	dir := a.cfg.DBDir
	a.mu.RUnlock()
	jc.Progress(-1, "加载元数据库")
	if err := a.reloadDB(dir); err != nil {
		jc.Logf("元数据库未加载：%v", err)
	}
	return a.scan(jc)
}

func (a *App) reloadDB(dir string) error {
	db, err := titledb.Load(dir)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.invalidatePlanLocked()
	if err != nil {
		a.db, a.dbErr = nil, err.Error()
		return err
	}
	a.db, a.dbErr = db, ""
	return nil
}

func (a *App) loadKeys() {
	if a.cfg.KeysPath == "" {
		a.keys, a.keysErr = nil, ""
		return
	}
	k, err := nsw.LoadKeys(a.cfg.KeysPath)
	if err != nil {
		a.keys, a.keysErr = nil, err.Error()
		return
	}
	a.keys, a.keysErr = k, ""
}

func (a *App) saveConfig() error { return saveJSONFile(a.path("config.json"), a.cfg) }

func (a *App) namer() library.Namer {
	return library.Namer{DB: a.db, Custom: a.custom, Simplified: a.cfg.Simplified}
}

// ============================ HTTP ============================

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(sub))

	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"app": AppName, "version": Version})
	})
	mux.HandleFunc("GET /api/state", a.hState)
	mux.HandleFunc("PUT /api/config", a.hConfig)
	mux.HandleFunc("POST /api/db/fetch", a.hDBFetch)
	mux.HandleFunc("POST /api/db/reload", a.hDBReload)
	mux.HandleFunc("POST /api/scan", a.hScan)
	mux.HandleFunc("GET /api/games", a.hGames)
	mux.HandleFunc("GET /api/games/{key}", a.hGame)
	mux.HandleFunc("PUT /api/names/{tid}", a.hSetName)
	mux.HandleFunc("GET /api/names", a.hNames)
	mux.HandleFunc("POST /api/import/names", a.hImportNames)
	mux.HandleFunc("GET /api/export/{kind}", a.hExport)
	mux.HandleFunc("GET /api/search", a.hSearch)
	mux.HandleFunc("PUT /api/manual", a.hManual)
	mux.HandleFunc("POST /api/organize/plan", a.hPlan)
	mux.HandleFunc("POST /api/organize/apply", a.hApply)
	mux.HandleFunc("GET /api/undo", a.hUndoList)
	mux.HandleFunc("POST /api/undo/apply", a.hUndoApply)
	mux.HandleFunc("POST /api/media/plan", a.hMediaPlan)
	mux.HandleFunc("POST /api/media/apply", a.hMediaApply)
	mux.HandleFunc("GET /api/redundant", a.hRedundantList)
	mux.HandleFunc("POST /api/redundant/apply", a.hRedundantApply)
	mux.HandleFunc("GET /api/merge", a.hMergeList)
	mux.HandleFunc("POST /api/merge/apply", a.hMergeApply)
	mux.HandleFunc("POST /api/xci2nsp", a.hXCI)
	mux.HandleFunc("GET /api/jobs/current", a.hJobCurrent)
	mux.HandleFunc("GET /api/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.jobs.History()) })
	mux.HandleFunc("POST /api/jobs/cancel", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"cancelled": a.jobs.Cancel()})
	})
	mux.HandleFunc("GET /api/fs", a.hFS)
	mux.HandleFunc("POST /api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if !a.opt.Desktop || a.opt.OnShutdown == nil {
			httpError(w, http.StatusForbidden, "仅桌面模式可用")
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		go func() { time.Sleep(300 * time.Millisecond); a.opt.OnShutdown() }()
	})
	return a.secure(mux)
}

// secure：
//   - 回环监听时校验 Host，防 DNS 重绑定
//   - 改动类请求必须带自定义头，浏览器跨站无法伪造（会触发 CORS 预检而被拒）
//   - 配置了密码时要求 HTTP Basic 认证
func (a *App) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.opt.AllowRemote {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if host != "localhost" && host != "127.0.0.1" && host != "::1" && host != "[::1]" {
				http.Error(w, "forbidden host", http.StatusForbidden)
				return
			}
		}
		if a.opt.Password != "" {
			_, pw, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(a.opt.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="my-switch-manager", charset="UTF-8"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "switchmgr" {
			http.Error(w, "missing X-Requested-With", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// ============================ 小工具 ============================

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("请求格式错误: " + err.Error())
	}
	return nil
}

func loadJSONFile(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

func saveJSONFile(path string, v any) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
