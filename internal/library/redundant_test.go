package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"my-switch-manager/internal/titledb"
)

// makeDB 写一个最小的元数据库：cnmts 为 TitleID -> 版本 -> 该版本包含的 NCA 内容 ID
func makeDB(t *testing.T, cnmts map[string]map[int64][]string) *titledb.DB {
	t.Helper()
	dir := t.TempDir()
	var cb, vb strings.Builder
	cb.WriteString("{")
	vb.WriteString("{")
	first := true
	for tid, vers := range cnmts {
		if !first {
			cb.WriteString(",")
		}
		first = false
		typ := 128
		switch {
		case strings.HasSuffix(tid, "800"):
			typ = 129
		case !strings.HasSuffix(tid, "000"):
			typ = 130
		}
		fmt.Fprintf(&cb, "%q:{", strings.ToLower(tid))
		i := 0
		for v, ncas := range vers {
			if i > 0 {
				cb.WriteString(",")
			}
			i++
			var ents []string
			for _, n := range ncas {
				ents = append(ents, fmt.Sprintf(`{"ncaId":%q}`, n))
			}
			fmt.Fprintf(&cb, `"%d":{"titleType":%d,"version":%d,"contentEntries":[%s]}`, v, typ, v, strings.Join(ents, ","))
		}
		cb.WriteString("}")
	}
	cb.WriteString("}")
	vb.WriteString("}")
	os.WriteFile(filepath.Join(dir, "cnmts.json"), []byte(cb.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "versions.json"), []byte(vb.String()), 0o644)
	db, err := titledb.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func nca(id string) [2]string { return [2]string{strings.Repeat(id, 32/len(id)) + ".nca", "N" + id} }
func meta(id string) [2]string {
	return [2]string{strings.Repeat(id, 32/len(id)) + ".cnmt.nca", "M" + id}
}

func redundantRels(t *testing.T, root string, db *titledb.DB) []string {
	t.Helper()
	files, err := Scan(context.Background(), root, ScanOptions{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range FindRedundant(Group(files), Namer{}) {
		out = append(out, r.Rel+" | "+r.Reason)
	}
	sort.Strings(out)
	return out
}

func TestRedundant(t *testing.T) {
	const base, upd, dlc = "0100AAAA00000000", "0100AAAA00000800", "0100AAAA00001001"
	db := makeDB(t, map[string]map[int64][]string{
		base: {0: {repeat("a1")}},
		upd:  {65536: {repeat("b1")}, 131072: {repeat("b2")}},
		dlc:  {0: {repeat("c1")}},
	})
	root := t.TempDir()
	g := filepath.Join(root, "Game")
	makeNSP(t, filepath.Join(g, "base.nsp"), [][2]string{nca("a1"), meta("d1")})
	makeNSP(t, filepath.Join(g, "upd1.nsp"), [][2]string{nca("b1"), meta("d2")})
	makeNSP(t, filepath.Join(g, "upd2.nsp"), [][2]string{nca("b2"), meta("d3")})
	// 只有票据 + 文件名版本的更新：内容不可精确判断，既不算冗余，也不能拿来覆盖别人
	makeNSP(t, filepath.Join(g, "upd3 [0100AAAA00000800][v196608].nsp"), [][2]string{tik(upd)})
	// 缺一个 CNMT 对应的记录（库里没有的 NCA）：内容不完整，不参与
	makeNSP(t, filepath.Join(g, "odd.nsp"), [][2]string{nca("b1"), meta("d4"), nca("ee"), meta("d5")})

	got := redundantRels(t, root, db)
	want := []string{"Game/upd1.nsp | 旧版更新 v65536，已有 v131072"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("冗余判断错误:\n%s", strings.Join(got, "\n"))
	}

	// 合并包包含了本体 + 新更新 + DLC：分散的源文件都成了冗余
	makeNSP(t, filepath.Join(g, "Game[0100AAAA00000000](merged).nsp"),
		[][2]string{nca("a1"), meta("d1"), nca("b2"), meta("d3"), nca("c1"), meta("d6")})
	got = redundantRels(t, root, db)
	want = []string{
		"Game/base.nsp | 内容已全部包含在另一文件中",
		"Game/upd1.nsp | 旧版更新 v65536，已有 v131072",
		"Game/upd2.nsp | 内容已全部包含在另一文件中",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("合并后冗余判断错误:\n%s", strings.Join(got, "\n"))
	}
	// 「保留」只能指向最终留下的文件（upd2 自己也被合并包覆盖了）
	files, _ := Scan(context.Background(), root, ScanOptions{DB: db})
	for _, r := range FindRedundant(Group(files), Namer{}) {
		if len(r.KeptBy) != 1 || !strings.Contains(r.KeptBy[0], mergedTag) {
			t.Fatalf("%s 的保留文件应为合并包，实际 %v", r.Rel, r.KeptBy)
		}
	}

	// 移走 → 重新扫描时不再出现 → 撤销后原样还原
	files, _ = Scan(context.Background(), root, ScanOptions{DB: db})
	var rels []string
	for _, r := range FindRedundant(Group(files), Namer{}) {
		rels = append(rels, r.Rel)
	}
	j, err := NewJournal(root, "移走冗余")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if ok, failed := MoveRedundant(root, append(rels, "../escape.nsp", RedundantDir+"/x.nsp"), j, t.Logf); ok != 3 || failed != 2 {
		t.Fatalf("移走结果 ok=%d failed=%d", ok, failed)
	}
	if !exists(filepath.Join(root, RedundantDir, "Game", "upd1.nsp")) {
		t.Fatal("冗余文件没有移到回收目录")
	}
	if n, _ := RedundantDirSize(root); n != 3 {
		t.Fatalf("回收目录应有 3 个文件，实际 %d", n)
	}
	files, _ = Scan(context.Background(), root, ScanOptions{DB: db})
	for _, f := range files {
		if strings.HasPrefix(f.Rel, RedundantDir) {
			t.Fatalf("扫描不应进入 %s：%s", RedundantDir, f.Rel)
		}
	}
	if got := redundantRels(t, root, db); len(got) != 0 {
		t.Fatalf("移走后仍有冗余: %v", got)
	}
	l, _ := ReadUndoLog(j.Path())
	if ok, failed := Undo(l, t.Logf); ok != 3 || failed != 0 {
		t.Fatalf("撤销 ok=%d failed=%d", ok, failed)
	}
	for _, r := range rels {
		if !exists(filepath.Join(root, r)) {
			t.Fatalf("撤销后缺少 %s", r)
		}
	}
}

// 同一内容的两个文件只留一个：体积小的（例如 NSZ）留下
func TestRedundantDuplicateKeepsSmaller(t *testing.T) {
	const base = "0100BBBB00000000"
	db := makeDB(t, map[string]map[int64][]string{base: {0: {repeat("a1")}}})
	root := t.TempDir()
	makeNSP(t, filepath.Join(root, "G", "big.nsp"), [][2]string{nca("a1"), meta("d1"), {"pad.xml", strings.Repeat("x", 500)}})
	makeNSP(t, filepath.Join(root, "G", "small.nsp"), [][2]string{nca("a1"), meta("d1")})
	got := redundantRels(t, root, db)
	if len(got) != 1 || got[0] != "G/big.nsp | 与另一文件内容相同" {
		t.Fatalf("重复文件判断错误: %v", got)
	}
}

func repeat(id string) string { return strings.Repeat(id, 16) }

// 合并候选不应带上旧版更新
func TestMergeSkipsRedundant(t *testing.T) {
	const base, upd = "0100CCCC00000000", "0100CCCC00000800"
	db := makeDB(t, map[string]map[int64][]string{
		base: {0: {repeat("a1")}},
		upd:  {65536: {repeat("b1")}, 131072: {repeat("b2")}},
	})
	root := t.TempDir()
	makeNSP(t, filepath.Join(root, "G", "base.nsp"), [][2]string{nca("a1"), meta("d1")})
	makeNSP(t, filepath.Join(root, "G", "upd1.nsp"), [][2]string{nca("b1"), meta("d2")})
	makeNSP(t, filepath.Join(root, "G", "upd2.nsp"), [][2]string{nca("b2"), meta("d3")})
	files, _ := Scan(context.Background(), root, ScanOptions{DB: db})
	jobs := MergeCandidates(root, Group(files), Namer{})
	if len(jobs) != 1 || strings.Join(jobs[0].Inputs, ",") != "G/base.nsp,G/upd2.nsp" {
		t.Fatalf("合并输入不对: %+v", jobs)
	}
}
