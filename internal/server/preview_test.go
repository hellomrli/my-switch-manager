package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"my-switch-manager/internal/library"
)

func previewFixture(t *testing.T) (*App, string, http.Handler) {
	t.Helper()
	a, root := newTestApp(t, Options{})
	a.Start()
	waitJob(t, a)
	h := a.Handler()
	w := do(t, h, "PUT", "/api/names/0100AAAA00000000", `{"name":"Test Game"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	return a, root, h
}

func getPreview(t *testing.T, h http.Handler) (string, []library.PlanItem) {
	t.Helper()
	w := do(t, h, "POST", "/api/organize/plan", `{}`)
	var items []library.PlanItem
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &items) != nil || len(items) == 0 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	token := w.Header().Get("X-Plan-Token")
	if token == "" {
		t.Fatal("missing preview token")
	}
	return token, items
}

func applyPreview(t *testing.T, h http.Handler, token string, items []library.PlanItem) int {
	t.Helper()
	keys := []string{}
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	body, _ := json.Marshal(map[string]any{"token": token, "keys": keys, "mediaMode": "none"})
	return do(t, h, "POST", "/api/organize/apply", string(body)).Code
}

func TestPreviewInvalidation(t *testing.T) {
	for _, change := range []string{"config", "name", "scan", "file", "new-file", "replacement", "token", "keys", "new-preview"} {
		t.Run(change, func(t *testing.T) {
			a, root, h := previewFixture(t)
			token, items := getPreview(t, h)
			switch change {
			case "config":
				a.mu.RLock()
				cfg := a.cfg
				a.mu.RUnlock()
				cfg.Templates.Folder = "Changed {name}"
				b, _ := json.Marshal(cfg)
				if w := do(t, h, "PUT", "/api/config", string(b)); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case "name":
				do(t, h, "PUT", "/api/names/0100AAAA00000000", `{"name":"Changed"}`)
			case "scan":
				do(t, h, "POST", "/api/scan", `{}`)
				waitJob(t, a)
			case "file":
				os.WriteFile(filepath.Join(root, "Game", "base.nsp"), []byte("changed"), 0600)
			case "new-file":
				os.WriteFile(filepath.Join(root, "Game", "extra.txt"), []byte("new"), 0600)
			case "replacement":
				path := filepath.Join(root, "Game", "base.nsp")
				info, _ := os.Stat(path)
				data, _ := os.ReadFile(path)
				os.WriteFile(path+".replacement", data, info.Mode())
				os.Chtimes(path+".replacement", info.ModTime(), info.ModTime())
				os.Rename(path+".replacement", path)
			case "token":
				token = "invalid"
			case "keys":
				items[0].Key = "not-in-preview"
			case "new-preview":
				getPreview(t, h)
			}
			if code := applyPreview(t, h, token, items); code != 409 {
				t.Fatalf("stale preview accepted: %d", code)
			}
			if _, err := os.Stat(filepath.Join(root, "Game", "base.nsp")); err != nil {
				t.Fatal("stale plan moved source", err)
			}
		})
	}
}

func TestPreviewExecutesCachedPaths(t *testing.T) {
	a, root, h := previewFixture(t)
	token, items := getPreview(t, h)
	if code := applyPreview(t, h, token, items); code != 200 {
		t.Fatalf("apply: %d", code)
	}
	waitJob(t, a)
	for _, it := range items {
		if it.DirMove != nil {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(it.DirMove.To))); err != nil {
				t.Fatal(err)
			}
		}
		for _, mv := range it.Moves {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(mv.To))); err != nil {
				t.Fatal("preview destination missing", err)
			}
		}
	}
	if code := applyPreview(t, h, token, items); code != 409 {
		t.Fatalf("replayed preview: %d", code)
	}
}

func waitFailedJob(t *testing.T, a *App) *Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for a.jobs.Running() {
		if time.Now().After(deadline) {
			t.Fatal("job timeout")
		}
		time.Sleep(time.Millisecond)
	}
	j, logs := a.jobs.Snapshot(0)
	if j == nil || j.State != "failed" || j.Error == "" {
		t.Fatalf("expected failed task: %+v logs=%v", j, logs)
	}
	return j
}

func TestRedundantFailureRefreshesWithoutSuccess(t *testing.T) {
	a, root, h := previewFixture(t)
	if err := os.Remove(filepath.Join(root, "Game", "upd-old.nsp")); err != nil {
		t.Fatal(err)
	}
	if w := do(t, h, "POST", "/api/redundant/apply", `{"rels":["Game/upd-old.nsp"]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitFailedJob(t, a)
	a.mu.RLock()
	count := len(a.files)
	a.mu.RUnlock()
	if count != 2 {
		t.Fatalf("failed operation did not refresh scan: %d files", count)
	}
}

func TestRefreshPreservesOriginalError(t *testing.T) {
	a, _ := newTestApp(t, Options{})
	a.cfg.LibraryRoot = ""
	original := fmt.Errorf("original journal failure")
	if taskResult(0, original) != original || taskResult(1, nil) == nil {
		t.Fatal("task error discarded")
	}
	_, err := a.jobs.Start("test", "refresh", func(jc *JobCtx) error { return a.refreshAfter(jc, original) })
	if err != nil {
		t.Fatal(err)
	}
	if j := waitFailedJob(t, a); j.Error != original.Error() {
		t.Fatalf("original error masked: %s", j.Error)
	}
}

func TestUndoFailureIsTaskError(t *testing.T) {
	a, root, h := previewFixture(t)
	token, items := getPreview(t, h)
	if applyPreview(t, h, token, items) != 200 {
		t.Fatal("apply failed")
	}
	waitJob(t, a)
	logs := library.ListUndoLogs(root)
	if len(logs) != 1 {
		t.Fatalf("logs: %v", logs)
	}
	// Prevent restoring the old directory. The journal must stay retryable.
	if err := os.MkdirAll(filepath.Join(root, "Game"), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "Game", "conflict.txt"), []byte("keep"), 0600)
	body, _ := json.Marshal(map[string]string{"file": filepath.Base(logs[0])})
	if w := do(t, h, "POST", "/api/undo/apply", string(body)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	waitFailedJob(t, a)
	if _, err := os.Stat(logs[0]); err != nil {
		t.Fatal("failed undo incorrectly marked done", err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "Game", "conflict.txt")); err != nil || !strings.Contains(string(b), "keep") {
		t.Fatal("conflict overwritten")
	}
}

func TestFolderOnlyPreviewMovesArray(t *testing.T) {
	a, root, h := previewFixture(t)
	for _, name := range []string{"upd-old.nsp", "upd-new.nsp"} {
		if err := os.Remove(filepath.Join(root, "Game", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(root, "Game", "base.nsp"), filepath.Join(root, "Game", "0100AAAA00000000.nsp")); err != nil {
		t.Fatal(err)
	}
	do(t, h, "POST", "/api/scan", `{}`)
	waitJob(t, a)
	a.mu.RLock()
	cfg := a.cfg
	a.mu.RUnlock()
	cfg.Templates.File = "{tid}"
	b, _ := json.Marshal(cfg)
	if w := do(t, h, "PUT", "/api/config", string(b)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	_, items := getPreview(t, h)
	if len(items) != 1 || items[0].DirMove == nil || items[0].Moves == nil || len(items[0].Moves) != 0 {
		t.Fatalf("folder-only plan must encode moves as []: %+v", items)
	}
}
