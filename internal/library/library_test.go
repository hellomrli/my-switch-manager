package library

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"my-switch-manager/internal/nsw"
)

// makeNSP 生成一个最小的 PFS0 文件，files 为 文件名 -> 内容
func makeNSP(t *testing.T, path string, files [][2]string) {
	t.Helper()
	var strTab []byte
	var ents []byte
	var data []byte
	for _, f := range files {
		ent := make([]byte, 24)
		binary.LittleEndian.PutUint64(ent[0:], uint64(len(data)))
		binary.LittleEndian.PutUint64(ent[8:], uint64(len(f[1])))
		binary.LittleEndian.PutUint32(ent[16:], uint32(len(strTab)))
		ents = append(ents, ent...)
		strTab = append(strTab, f[0]...)
		strTab = append(strTab, 0)
		data = append(data, f[1]...)
	}
	head := make([]byte, 16)
	copy(head, "PFS0")
	binary.LittleEndian.PutUint32(head[4:], uint32(len(files)))
	binary.LittleEndian.PutUint32(head[8:], uint32(len(strTab)))
	out := append(append(append(head, ents...), strTab...), data...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func tik(tid string) [2]string {
	return [2]string{strings.ToLower(tid) + "0000000000000005.tik", "TIK"}
}

func TestTitleIDRules(t *testing.T) {
	base, _ := nsw.ParseTitleID("0100ABCD0000E000")
	for _, n := range []nsw.TitleID{1, 0xFFF} {
		dlc := base + 0x1000 + n
		if dlc.Kind() != nsw.KindAddOn || dlc.Base() != base {
			t.Fatalf("DLC %s 归属错误：%s", dlc, dlc.Base())
		}
	}
	// 同一个 0x10000 号段里的另一个游戏（旧版按 TitleID>>16 分组会把两者并成一个）
	other, _ := nsw.ParseTitleID("0100ABCD0000C000")
	if other.Base() == base {
		t.Fatal("两个不同游戏被归为一组")
	}
	if (base + 0x800).Kind() != nsw.KindPatch {
		t.Fatal("更新识别错误")
	}
}

func TestScanPlanExecuteUndo(t *testing.T) {
	root := t.TempDir()
	// 1) 散放在根目录的合集包（票据只有更新与 DLC）
	makeNSP(t, filepath.Join(root, "Zelda BOTW [B+U983040+2DLC].nsp"), [][2]string{
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.nca", "NCA1"},
		tik("01007EF00011E800"), tik("01007EF00011F001"), tik("01007EF00011F002"),
	})
	// 2) 独立文件夹：本体 + 更新 + cheats
	makeNSP(t, filepath.Join(root, "Skyward", "Skyward Sword HD [01002DA013484000][v0].nsp"), [][2]string{tik("01002DA013484000")})
	makeNSP(t, filepath.Join(root, "Skyward", "Skyward Sword HD [01002DA013484800][v65536].nsp"), [][2]string{tik("01002DA013484800")})
	os.MkdirAll(filepath.Join(root, "Skyward", "cheats"), 0o755)
	os.WriteFile(filepath.Join(root, "Skyward", "cheats", "a.txt"), []byte("x"), 0o644)
	// 3) 无任何 ID 的文件 + 手动指定
	makeNSP(t, filepath.Join(root, "Xeno", "ゼノブレイド２ [B+U917504+6DLC].nsp"), [][2]string{{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.nca", "N"}})
	// 媒体与 gamelist
	os.MkdirAll(filepath.Join(root, "images"), 0o755)
	os.WriteFile(filepath.Join(root, "images", "Skyward Sword HD [01002DA013484000][v0]-thumb.jpg"), []byte("img"), 0o644)
	os.WriteFile(filepath.Join(root, "gamelist.xml"), []byte(`<gameList><game><path>./Skyward/Skyward Sword HD [01002DA013484000][v0].nsp</path><image>./images/Skyward Sword HD [01002DA013484000][v0]-thumb.jpg</image></game></gameList>`), 0o644)

	data := t.TempDir()
	manual := OpenStringMap(filepath.Join(data, "manual_ids.json"), false)
	manual.Set("ゼノブレイド２ [B+U917504+6DLC].nsp", "0100F3400332C000")
	custom := OpenStringMap(filepath.Join(data, "custom_names.json"), true)
	custom.SetMany(map[string]string{"01007EF00011E000": "塞尔达传说 旷野之息", "01002DA013484000": "塞尔达传说 御天之剑 HD", "0100F3400332C000": "异度神剑2"})

	files, err := Scan(context.Background(), root, ScanOptions{Manual: manual, Cache: OpenScanCache(filepath.Join(data, "cache.json"))})
	if err != nil {
		t.Fatal(err)
	}
	games := Group(files)
	if len(games) != 3 {
		t.Fatalf("应聚合为 3 个游戏，实际 %d", len(games))
	}
	for _, g := range games {
		t.Logf("%s ver=%d app=%v upd=%v dlc=%d files=%d", g.Key, g.LocalVersion, g.HasApp, g.HasPatch, len(g.OwnedDLC), len(g.Files))
	}
	var botw *Game
	for _, g := range games {
		if g.Key == "01007EF00011E000" {
			botw = g
		}
	}
	if botw == nil || botw.Key != "01007EF00011E000" || botw.LocalVersion != 983040 || len(botw.OwnedDLC) != 2 || !botw.HasApp {
		t.Fatalf("合集包识别错误: %+v", botw)
	}

	namer := Namer{Custom: custom}
	plan, err := BuildPlan(root, files, games, namer, Templates{})
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, it := range plan {
		modes[it.Key] = it.Mode
		t.Logf("%s [%s] %s -> %s moves=%v warn=%v blocked=%s", it.Name, it.Mode, it.OldDir, it.TargetDir, it.Moves, it.Warnings, it.Blocked)
	}
	if modes["01007EF00011E000"] != ModeCreate || modes["01002DA013484000"] != ModeRename {
		t.Fatalf("整理模式错误: %v", modes)
	}

	j, err := NewJournal(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	j.SetManual(manual)
	res := ExecutePlan(root, plan, j, t.Logf)
	if res.Failed != 0 || res.OK != 3 {
		t.Fatalf("执行结果 %+v", res)
	}
	mp, err := BuildMediaPlan(root, RenameMapFrom(res.Renames), MediaKeep)
	if err != nil {
		t.Fatal(err)
	}
	ExecuteMediaPlan(root, mp, j, t.Logf)
	if mp.PathFixes != 1 || mp.MediaFixes != 1 || len(mp.Moves) != 1 {
		t.Fatalf("媒体方案 %+v", mp)
	}
	// cheats 随文件夹一起走
	if !exists(filepath.Join(root, "[塞尔达传说 御天之剑 HD][Skyward Sword HD][01002DA013484000]", "cheats", "a.txt")) {
		t.Fatal("cheats 没有随文件夹一起改名")
	}
	// 再扫一遍：版本信息必须还在（旧版整理后会丢）
	files2, _ := Scan(context.Background(), root, ScanOptions{Manual: manual})
	for _, g := range Group(files2) {
		if g.Key == "01007EF00011E000" && g.LocalVersion != 983040 {
			t.Fatalf("整理后版本信息丢失: %d", g.LocalVersion)
		}
		if g.Key == "0100F3400332C000" && g.LocalVersion != 917504 {
			t.Fatalf("整理后手动指定的游戏版本丢失: %d (%v)", g.LocalVersion, g.Files[0].Name)
		}
	}
	// 再次生成方案应当为空（幂等）
	if p2, _ := BuildPlan(root, files2, Group(files2), namer, Templates{}); len(p2) != 0 {
		t.Fatalf("重复整理不应产生新方案: %+v", p2)
	}

	l, err := ReadUndoLog(j.Path())
	if err != nil {
		t.Fatal(err)
	}
	if ok, failed := Undo(l, t.Logf); failed != 0 || ok == 0 {
		t.Fatalf("撤销失败 ok=%d failed=%d", ok, failed)
	}
	for _, p := range []string{
		"Zelda BOTW [B+U983040+2DLC].nsp",
		"Skyward/Skyward Sword HD [01002DA013484800][v65536].nsp",
		"Skyward/cheats/a.txt",
		"images/Skyward Sword HD [01002DA013484000][v0]-thumb.jpg",
		"Xeno/ゼノブレイド２ [B+U917504+6DLC].nsp",
	} {
		if !exists(filepath.Join(root, p)) {
			t.Fatalf("撤销后缺少 %s", p)
		}
	}
	gl, _ := os.ReadFile(filepath.Join(root, "gamelist.xml"))
	if !strings.Contains(string(gl), "./Skyward/Skyward Sword HD [01002DA013484000][v0].nsp") {
		t.Fatalf("gamelist 未还原: %s", gl)
	}
}

func TestNoOverwrite(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp")
	os.WriteFile(a, []byte("a"), 0o644)
	os.WriteFile(b, []byte("b"), 0o644)
	if err := safeRename(a, b); err == nil {
		t.Fatal("应拒绝覆盖")
	}
	if d, _ := os.ReadFile(b); string(d) != "b" {
		t.Fatal("目标被覆盖")
	}
}

func TestMergeRoundTrip(t *testing.T) {
	root := t.TempDir()
	in1, in2 := filepath.Join(root, "base.nsp"), filepath.Join(root, "upd.nsp")
	makeNSP(t, in1, [][2]string{{"11111111111111111111111111111111.nca", strings.Repeat("A", 5000)}, tik("01002DA013484000")})
	makeNSP(t, in2, [][2]string{{"22222222222222222222222222222222.nca", strings.Repeat("B", 7000)}, tik("01002DA013484800"),
		{"11111111111111111111111111111111.nca", strings.Repeat("A", 5000)}})
	out := filepath.Join(root, "merged.nsp")
	n, err := Merge(context.Background(), []string{in1, in2}, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("合并后应有 4 个文件（去重后），实际 %d", n)
	}
	c, err := nsw.OpenContainer(out)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ids := nsw.TicketTitleIDs(c.Entries); len(ids) != 2 {
		t.Fatalf("票据数 %d", len(ids))
	}
}
