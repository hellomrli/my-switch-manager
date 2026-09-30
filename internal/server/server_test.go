package server

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeNSP 生成最小的 PFS0，names 为容器内文件名
func writeNSP(t *testing.T, path string, names ...string) {
	t.Helper()
	var ents, strTab, data []byte
	for _, n := range names {
		ent := make([]byte, 24)
		binary.LittleEndian.PutUint64(ent[0:], uint64(len(data)))
		binary.LittleEndian.PutUint64(ent[8:], uint64(len(n)))
		binary.LittleEndian.PutUint32(ent[16:], uint32(len(strTab)))
		ents = append(ents, ent...)
		strTab = append(append(strTab, n...), 0)
		data = append(data, n...)
	}
	head := make([]byte, 16)
	copy(head, "PFS0")
	binary.LittleEndian.PutUint32(head[4:], uint32(len(names)))
	binary.LittleEndian.PutUint32(head[8:], uint32(len(strTab)))
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, append(append(append(head, ents...), strTab...), data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func id(c string) string { return strings.Repeat(c, 32) }

func newTestApp(t *testing.T, opt Options) (*App, string) {
	t.Helper()
	data, root := t.TempDir(), t.TempDir()
	db := filepath.Join(data, "titledb")
	os.MkdirAll(db, 0o755)
	os.WriteFile(filepath.Join(db, "versions.json"), []byte(`{"0100aaaa00000800":{"65536":"2024-01-01","131072":"2024-06-01"}}`), 0o644)
	os.WriteFile(filepath.Join(db, "cnmts.json"), []byte(`{
		"0100aaaa00000000":{"0":{"titleType":128,"version":0,"contentEntries":[{"ncaId":"`+id("a")+`"}]}},
		"0100aaaa00000800":{"65536":{"titleType":129,"version":65536,"contentEntries":[{"ncaId":"`+id("b")+`"}]},
		                    "131072":{"titleType":129,"version":131072,"contentEntries":[{"ncaId":"`+id("c")+`"}]}}}`), 0o644)
	cfg, _ := json.Marshal(Config{LibraryRoot: root, DBDir: db, Simplified: true})
	os.WriteFile(filepath.Join(data, "config.json"), cfg, 0o644)

	writeNSP(t, filepath.Join(root, "Game", "base.nsp"), id("a")+".nca", id("1")+".cnmt.nca")
	writeNSP(t, filepath.Join(root, "Game", "upd-old.nsp"), id("b")+".nca", id("2")+".cnmt.nca")
	writeNSP(t, filepath.Join(root, "Game", "upd-new.nsp"), id("c")+".nca", id("3")+".cnmt.nca")

	opt.DataDir = data
	a, err := New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return a, root
}

func do(t *testing.T, h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:8765"+path, strings.NewReader(body))
	r.Header.Set("X-Requested-With", "switchmgr")
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			r.Header.Del(hdr[i])
		} else {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	}
	if r.Header.Get("Host") != "" {
		r.Host = r.Header.Get("Host")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func waitJob(t *testing.T, a *App) *Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for a.jobs.Running() {
		if time.Now().After(deadline) {
			t.Fatal("任务超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, logs := a.jobs.Snapshot(0)
	if j == nil {
		t.Fatal("没有任务")
	}
	if j.State != "done" {
		t.Fatalf("任务 %s 状态 %s：%s\n%s", j.Title, j.State, j.Error, strings.Join(logs, "\n"))
	}
	return j
}

func TestSecurity(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	h := a.Handler()
	if w := do(t, h, "GET", "/api/ping", "", "Host", "evil.example:8765"); w.Code != 403 {
		t.Fatalf("回环监听应拒绝陌生 Host（DNS 重绑定），实际 %d", w.Code)
	}
	if w := do(t, h, "POST", "/api/scan", "", "X-Requested-With", ""); w.Code != 403 {
		t.Fatalf("缺少 X-Requested-With 的写请求应被拒绝，实际 %d", w.Code)
	}
	if w := do(t, h, "POST", "/api/shutdown", ""); w.Code != 403 {
		t.Fatalf("非桌面模式不允许关闭，实际 %d", w.Code)
	}

	a2, _ := newTestApp(t, Options{Password: "pw", AllowRemote: true})
	h2 := a2.Handler()
	if w := do(t, h2, "GET", "/api/state", "", "Host", "nas.lan:8765"); w.Code != 401 {
		t.Fatalf("设置密码后未认证应返回 401，实际 %d", w.Code)
	}
	r := httptest.NewRequest("GET", "http://nas.lan:8765/api/ping", nil)
	r.SetBasicAuth("any", "pw")
	w := httptest.NewRecorder()
	h2.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), AppName) {
		t.Fatalf("认证后应可访问：%d %s", w.Code, w.Body.String())
	}
}

func TestStartupScanAndRedundant(t *testing.T) {
	a, root := newTestApp(t, Options{})
	h := a.Handler()
	a.Start() // 设置过库目录：启动即加载数据库并扫描
	waitJob(t, a)

	var games []gameDTO
	json.Unmarshal(do(t, h, "GET", "/api/games", "").Body.Bytes(), &games)
	if len(games) != 1 || games[0].TID != "0100AAAA00000000" || games[0].LocalVersion != 131072 || games[0].State != "latest" {
		t.Fatalf("游戏列表不对: %+v", games)
	}
	if games[0].Redundant != 1 {
		t.Fatalf("应有 1 个冗余文件，实际 %d", games[0].Redundant)
	}

	var red struct {
		Files []struct {
			Rel    string   `json:"rel"`
			Reason string   `json:"reason"`
			KeptBy []string `json:"keptBy"`
		} `json:"files"`
	}
	json.Unmarshal(do(t, h, "GET", "/api/redundant", "").Body.Bytes(), &red)
	if len(red.Files) != 1 || red.Files[0].Rel != "Game/upd-old.nsp" || len(red.Files[0].KeptBy) != 1 || red.Files[0].KeptBy[0] != "Game/upd-new.nsp" {
		t.Fatalf("冗余列表不对: %+v", red)
	}

	if w := do(t, h, "POST", "/api/redundant/apply", `{"rels":["Game/upd-old.nsp","Game/base.nsp"]}`); w.Code != 200 {
		t.Fatalf("移走冗余失败: %d %s", w.Code, w.Body.String())
	}
	if j := waitJob(t, a); j.Summary != "移走 1 个 / 失败 0 个" {
		t.Fatalf("任务摘要被随后的扫描覆盖了: %q", j.Summary)
	}
	// base.nsp 不是冗余文件，即使请求里带上也不能被移走
	if _, err := os.Stat(filepath.Join(root, "Game", "base.nsp")); err != nil {
		t.Fatal("非冗余文件被移走了")
	}
	if _, err := os.Stat(filepath.Join(root, "_redundant", "Game", "upd-old.nsp")); err != nil {
		t.Fatal("冗余文件没有移到 _redundant/")
	}

	var undo []struct {
		File  string `json:"file"`
		Moves int    `json:"moves"`
	}
	json.Unmarshal(do(t, h, "GET", "/api/undo", "").Body.Bytes(), &undo)
	if len(undo) != 1 || undo[0].Moves != 1 {
		t.Fatalf("撤销记录不对: %+v", undo)
	}
	if w := do(t, h, "POST", "/api/undo/apply", `{"file":"`+undo[0].File+`"}`); w.Code != 200 {
		t.Fatalf("撤销失败: %s", w.Body.String())
	}
	waitJob(t, a)
	if _, err := os.Stat(filepath.Join(root, "Game", "upd-old.nsp")); err != nil {
		t.Fatal("撤销后文件没有回到原处")
	}
	if _, err := os.Stat(filepath.Join(root, "_redundant")); !os.IsNotExist(err) {
		t.Fatal("撤销后空的 _redundant/ 应当被清理")
	}
}
