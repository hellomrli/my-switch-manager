package nsw

import (
	"os"
	"testing"
)

// 需要真实密钥与样本，默认跳过：
//
//	SWITCHMGR_TEST_KEYS=/path/prod.keys SWITCHMGR_TEST_CNMT=/path/a.cnmt.nca go test ./internal/nsw -run CNMT -v
func TestReadCNMTWithRealKeys(t *testing.T) {
	kp, cp := os.Getenv("SWITCHMGR_TEST_KEYS"), os.Getenv("SWITCHMGR_TEST_CNMT")
	if kp == "" || cp == "" {
		t.Skip("未设置 SWITCHMGR_TEST_KEYS / SWITCHMGR_TEST_CNMT")
	}
	keys, err := LoadKeys(kp)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(cp)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	c, err := keys.ReadCNMT(f, 0, fi.Size())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TitleID=%s Kind=%s Version=%d", c.TitleID, c.Kind(), c.Version)
}
