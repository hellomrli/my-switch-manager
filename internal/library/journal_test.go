package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testJournal(t *testing.T, root string) *Journal {
	t.Helper()
	j, err := NewJournal(root, "回归测试")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

func testFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func expectText(t *testing.T, path, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != text {
		t.Fatalf("%s: %q, %v; want %q", path, data, err, text)
	}
}

func loadJournal(t *testing.T, j *Journal) *UndoLog {
	t.Helper()
	l, err := ReadUndoLog(j.Path())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestJournalRequiresWritableRoot(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("需要普通用户的 Unix 目录权限")
	}
	root := t.TempDir()
	src := filepath.Join(root, "game", "old.nsp")
	testFile(t, src, "original")
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0755)
	if j, err := NewJournal(root, "不得改名"); err == nil {
		j.Close()
		t.Fatal("不应允许无法创建撤销日志的任务")
	}
	expectText(t, src, "original")
}

func TestJournalUniqueNames(t *testing.T) {
	root := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		j, err := NewJournal(root, "same")
		if err != nil {
			t.Fatal(err)
		}
		if seen[j.Path()] {
			t.Fatal("日志重名")
		}
		seen[j.Path()] = true
		if err := j.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(ListUndoLogs(root)); got != 32 {
		t.Fatalf("日志被覆盖：%d", got)
	}
}

func TestJournalPersistenceFailures(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			root := t.TempDir()
			a, b := filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp")
			testFile(t, a, "original")
			j := testJournal(t, root)
			calls := 0
			j.save = func(l *UndoLog) error {
				calls++
				if calls == failAt {
					return errors.New("injected disk failure")
				}
				return j.fs.saveJSON(j.Path(), l)
			}
			if err := j.move(a, b); err == nil || j.Err() == nil {
				t.Fatal("应传播并记住日志错误")
			}
			if failAt == 1 {
				expectText(t, a, "original")
			} else {
				expectText(t, b, "original")
			}
			c := filepath.Join(root, "c.nsp")
			testFile(t, c, "untouched")
			if err := j.move(c, filepath.Join(root, "d.nsp")); err == nil {
				t.Fatal("日志错误后不应继续")
			}
			expectText(t, c, "untouched")
			l := loadJournal(t, j)
			if _, failed := Undo(l, t.Logf); failed != 0 {
				t.Fatal("已落盘的意图应可恢复")
			}
			expectText(t, a, "original")
			if err := CompleteUndo(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUndoConflictStopsBeforeParent(t *testing.T) {
	root := t.TempDir()
	old, next := filepath.Join(root, "old"), filepath.Join(root, "new")
	testFile(t, filepath.Join(old, "a.nsp"), "rom")
	j := testJournal(t, root)
	if err := j.move(old, next); err != nil {
		t.Fatal(err)
	}
	if err := j.move(filepath.Join(next, "a.nsp"), filepath.Join(next, "b.nsp")); err != nil {
		t.Fatal(err)
	}
	testFile(t, filepath.Join(next, "a.nsp"), "conflict")
	l := loadJournal(t, j)
	if _, failed := Undo(l, t.Logf); failed != 1 {
		t.Fatal("冲突应中止撤销")
	}
	if exists(old) || !exists(next) {
		t.Fatal("子项失败后不能恢复父目录")
	}
	if err := CompleteUndo(l); err == nil {
		t.Fatal("未完成不能标为done")
	}
	if err := os.Remove(filepath.Join(next, "a.nsp")); err != nil {
		t.Fatal(err)
	}
	l = loadJournal(t, j)
	if _, failed := Undo(l, t.Logf); failed != 0 {
		t.Fatal("解除冲突后应能重试")
	}
	expectText(t, filepath.Join(old, "a.nsp"), "rom")
	if exists(filepath.Join(old, "b.nsp")) {
		t.Fatal("文件名未恢复")
	}
	if err := CompleteUndo(l); err != nil {
		t.Fatal(err)
	}
}

func TestUndoPersistenceFailureAfterRename(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp")
	testFile(t, a, "rom")
	j := testJournal(t, root)
	if err := j.move(a, b); err != nil {
		t.Fatal(err)
	}
	l := loadJournal(t, j)
	calls := 0
	l.save = func(log *UndoLog) error {
		calls++
		if calls == 3 {
			return errors.New("save after inverse failed")
		}
		return j.fs.saveJSON(j.Path(), log)
	}
	if _, failed := Undo(l, t.Logf); failed != 1 {
		t.Fatal("应报告进度保存失败")
	}
	expectText(t, a, "rom")
	if err := CompleteUndo(l); err == nil {
		t.Fatal("磁盘上仍为undoing，不应done")
	}
	l = loadJournal(t, j)
	if _, failed := Undo(l, t.Logf); failed != 0 {
		t.Fatal("应识别已经完成的逆操作")
	}
	if err := CompleteUndo(l); err != nil {
		t.Fatal(err)
	}
}

func TestUndoDoesNotAcceptMissingOrReplacedFile(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			root := t.TempDir()
			a, b := filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp")
			testFile(t, a, "rom")
			j := testJournal(t, root)
			if err := j.move(a, b); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(b); err != nil {
				t.Fatal(err)
			}
			if replace {
				testFile(t, b, "unrelated replacement")
			}
			l := loadJournal(t, j)
			if _, failed := Undo(l, t.Logf); failed != 1 {
				t.Fatal("丢失或替换的文件不能算恢复成功")
			}
			if exists(a) {
				t.Fatal("不能移动不相关文件")
			}
		})
	}
}

func TestJournalRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("无法创建符号链接：%v", err)
	}
	a := filepath.Join(root, "a.nsp")
	testFile(t, a, "rom")
	testFile(t, filepath.Join(outside, "gamelist.xml"), "outside")
	j := testJournal(t, root)
	if err := j.move(a, filepath.Join(root, "link", "a.nsp")); err == nil {
		t.Fatal("不应移到库外")
	}
	if err := j.mkdirAll(filepath.Join(root, "link", "new")); err == nil {
		t.Fatal("不应在库外建目录")
	}
	if err := j.writeFile(filepath.Join(root, "link", "gamelist.xml"), []byte("changed")); err == nil {
		t.Fatal("不应写库外文件")
	}
	expectText(t, a, "rom")
	expectText(t, filepath.Join(outside, "gamelist.xml"), "outside")
	if exists(filepath.Join(outside, "a.nsp")) || exists(filepath.Join(outside, "new")) {
		t.Fatal("库外目录被改变")
	}
}

func TestReadUndoRejectsEscapingPaths(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "_undo_bad.json")
	if err := saveJSON(p, UndoLog{Version: 2, Root: root, Ops: []Op{{Op: "move", From: filepath.Join(t.TempDir(), "outside"), To: filepath.Join(root, "inside")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUndoLog(p); err == nil {
		t.Fatal("不能加载越界恢复操作")
	}
}

func TestLegacyUndoLogs(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := t.TempDir()
			a, b := filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp")
			testFile(t, b, "legacy")
			log := UndoLog{Version: version}
			if version == 1 {
				log.FileMoves = [][2]string{{a, b}}
			} else {
				log.Root = root
				log.Ops = []Op{{Op: "move", From: a, To: b}}
			}
			p := filepath.Join(root, "_undo_legacy.json")
			if err := saveJSON(p, log); err != nil {
				t.Fatal(err)
			}
			l, err := ReadUndoLog(p)
			if err != nil {
				t.Fatal(err)
			}
			if _, failed := Undo(l, t.Logf); failed != 0 {
				t.Fatal("旧日志应兼容")
			}
			expectText(t, a, "legacy")
			if err := CompleteUndo(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJournalWriteRecovery(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "gamelist.xml")
	testFile(t, p, "old XML")
	j := testJournal(t, root)
	calls := 0
	j.save = func(l *UndoLog) error {
		calls++
		if calls == 2 {
			return errors.New("commit failure")
		}
		return j.fs.saveJSON(j.Path(), l)
	}
	if err := j.writeFile(p, []byte("new XML")); err == nil {
		t.Fatal("应报告提交失败")
	}
	expectText(t, p, "new XML")
	l := loadJournal(t, j)
	if _, failed := Undo(l, t.Logf); failed != 0 {
		t.Fatal("pending写入应从备份恢复")
	}
	expectText(t, p, "old XML")
}

func TestMediaFailureKeepsOldReference(t *testing.T) {
	root := t.TempDir()
	rom := filepath.Join(root, "new", "New.nsp")
	testFile(t, rom, "rom")
	oldImage := filepath.Join(root, "images", "Old-thumb.jpg")
	newImage := filepath.Join(root, "images", "New-thumb.jpg")
	testFile(t, oldImage, "image")
	gl := filepath.Join(root, "gamelist.xml")
	original := `<gameList><game><path>./old/Old.nsp</path><image>./images/Old-thumb.jpg</image></game></gameList>`
	testFile(t, gl, original)
	pl, err := BuildMediaPlan(root, RenameMap{"Old": rom}, MediaKeep)
	if err != nil || len(pl.Moves) != 1 {
		t.Fatalf("%+v %v", pl, err)
	}
	testFile(t, newImage, "conflict") // 规划后目标被占用，实际媒体改名必须失败。
	j := testJournal(t, root)
	if _, failed := ExecuteMediaPlan(root, pl, j, t.Logf); failed != 1 {
		t.Fatal("应报告媒体移动失败")
	}
	data, _ := os.ReadFile(gl)
	if !strings.Contains(string(data), "./new/New.nsp") || !strings.Contains(string(data), "./images/Old-thumb.jpg") {
		t.Fatalf("XML引用不对：%s", data)
	}
	expectText(t, oldImage, "image")
	expectText(t, newImage, "conflict")
	if _, failed := Undo(loadJournal(t, j), t.Logf); failed != 0 {
		t.Fatal("XML应能撤销")
	}
	expectText(t, gl, original)
}

func TestManualMatchSurvivesOrganizeAndUndo(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Original.nsp")
	const ticketID, correctID = "01002DA013484000", "01007EF00011E000"
	makeNSP(t, path, [][2]string{tik(ticketID)})
	manualPath := filepath.Join(t.TempDir(), "manual.json")
	manual := OpenStringMap(manualPath, false)
	if err := manual.Set("Original.nsp", correctID); err != nil {
		t.Fatal(err)
	}
	custom := OpenStringMap(filepath.Join(t.TempDir(), "custom.json"), true)
	if err := custom.Set(correctID, "正确游戏"); err != nil {
		t.Fatal(err)
	}
	files, err := Scan(context.Background(), root, ScanOptions{Manual: manual})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(root, files, Group(files), Namer{Custom: custom}, Templates{})
	if err != nil {
		t.Fatal(err)
	}
	j := testJournal(t, root)
	j.SetManual(manual)
	if result := ExecutePlan(root, plan, j, t.Logf); result.Failed != 0 || result.OK != 1 {
		t.Fatalf("%+v", result)
	}
	check := func() {
		m := OpenStringMap(manualPath, false)
		files, err := Scan(context.Background(), root, ScanOptions{Manual: m})
		if err != nil || len(files) != 1 || files[0].Source != SrcManual || files[0].BaseID.String() != correctID {
			t.Fatalf("手动纠正丢失：%+v %v", files, err)
		}
	}
	check()
	if _, failed := Undo(loadJournal(t, j), t.Logf); failed != 0 {
		t.Fatal("撤销失败")
	}
	check()
}

func TestManualAliasFailures(t *testing.T) {
	for _, mode := range []string{"save", "conflict", "move", "commit"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			a, b := filepath.Join(root, "a.nsp"), filepath.Join(root, "b.nsp")
			testFile(t, a, "rom")
			s := OpenStringMap(filepath.Join(t.TempDir(), "manual.json"), false)
			if err := s.Set("a.nsp", "correct"); err != nil {
				t.Fatal(err)
			}
			j := testJournal(t, root)
			j.SetManual(s)
			switch mode {
			case "save":
				block := filepath.Join(t.TempDir(), "block")
				testFile(t, block, "not a directory")
				s.path = filepath.Join(block, "manual.json")
			case "conflict":
				if err := s.Set("b.nsp", "other"); err != nil {
					t.Fatal(err)
				}
			case "move":
				testFile(t, b, "occupied")
			case "commit":
				calls := 0
				j.save = func(l *UndoLog) error {
					calls++
					if calls == 2 {
						return errors.New("commit failure")
					}
					return j.fs.saveJSON(j.Path(), l)
				}
			}
			if err := j.move(a, b); err == nil {
				t.Fatal("应失败")
			}
			if mode == "commit" {
				expectText(t, b, "rom")
				if s.Get("b.nsp") != "correct" {
					t.Fatal("已经改名，不能回滚新键")
				}
			} else {
				expectText(t, a, "rom")
				want := ""
				if mode == "conflict" {
					want = "other"
				}
				if s.Get("b.nsp") != want {
					t.Fatal("失败改变了目标匹配")
				}
			}
		})
	}
}

func TestStringMapFailedSaveKeepsMemory(t *testing.T) {
	root := t.TempDir()
	block := filepath.Join(root, "block")
	testFile(t, block, "file")
	s := OpenStringMap(filepath.Join(root, "map.json"), false)
	if err := s.Set("key", "before"); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(block, "map.json")
	if err := s.Set("key", "after"); err == nil {
		t.Fatal("应保存失败")
	}
	if s.Get("key") != "before" {
		t.Fatal("保存失败不应先改变内存")
	}
}
