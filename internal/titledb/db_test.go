package titledb

import (
	"os"
	"testing"
	"time"

	"my-switch-manager/internal/nsw"
)

// SWITCHMGR_TEST_DB=/path/titledb go test ./internal/titledb -v
func TestLoadRealDB(t *testing.T) {
	dir := os.Getenv("SWITCHMGR_TEST_DB")
	if dir == "" {
		t.Skip("未设置 SWITCHMGR_TEST_DB")
	}
	st := time.Now()
	db, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("加载耗时 %v，%+v", time.Since(st), db.Stats())
	smash, _ := nsw.ParseTitleID("01006A800016E000")
	t.Logf("名称: %s", db.Name(smash, true))
	t.Logf("DLC 数: %d", len(db.DLCs(smash)))
	candidates := db.LookupNCACandidates("d0aff042856a7770809d5df105278344")
	found := false
	for _, c := range candidates {
		if c.TitleID == smash {
			found = true
		}
	}
	if !found {
		t.Fatalf("NCA 候选里查不到大乱斗本体: %v", candidates)
	}
	for _, h := range db.Search("異度神劍", true, 5) {
		t.Logf("搜索: %+v", h)
	}
	for _, h := range db.Search("dark souls", true, 5) {
		t.Logf("搜索: %+v", h)
	}
}
