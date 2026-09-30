package library

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"my-switch-manager/internal/nsw"
)

func TestSharedNCAFindRedundant(t *testing.T) {
	const upd = "0100AAAA00000800"
	fixture := map[string]map[int64][]string{upd: {65536: {repeat("a1"), repeat("b1")}, 131072: {repeat("a1"), repeat("b2")}}}
	root := t.TempDir()
	makeNSP(t, filepath.Join(root, "old.nsp"), [][2]string{nca("a1"), nca("b1"), meta("d1")})
	makeNSP(t, filepath.Join(root, "new.nsp"), [][2]string{nca("a1"), nca("b2"), meta("d2")})
	makeNSP(t, filepath.Join(root, "ambiguous [v999999].nsp"), [][2]string{nca("a1"), meta("d3")})
	makeNSP(t, filepath.Join(root, "incomplete.nsp"), [][2]string{nca("b2"), meta("d4")})
	for i := 0; i < 20; i++ {
		db := makeDB(t, fixture)
		files, err := Scan(context.Background(), root, ScanOptions{DB: db})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			switch f.Name {
			case "old.nsp":
				if !f.Complete || f.GameVersion() != 65536 {
					t.Fatalf("old: %+v", f)
				}
			case "new.nsp":
				if !f.Complete || f.GameVersion() != 131072 {
					t.Fatalf("new: %+v", f)
				}
			default:
				if f.Complete || f.GameVersion() != -1 {
					t.Fatalf("ambiguous/incomplete: %+v", f)
				}
			}
		}
		got := FindRedundant(Group(files), Namer{})
		if len(got) != 1 || got[0].Rel != "old.nsp" || len(got[0].KeptBy) != 1 || got[0].KeptBy[0] != "new.nsp" {
			t.Fatalf("wrong redundant files: %+v", got)
		}
	}
}

func TestNCAMultiTitleBundleAndIdenticalSets(t *testing.T) {
	const base, upd, dlc = "0100AAAA00000000", "0100AAAA00000800", "0100AAAA00001001"
	db := makeDB(t, map[string]map[int64][]string{
		base: {0: {repeat("a1")}}, upd: {65536: {repeat("b1"), repeat("c1")}, 131072: {repeat("b1"), repeat("c2")}}, dlc: {0: {repeat("e1"), repeat("b1")}},
	})
	rf := &RomFile{Name: "bundle.nsp"}
	pr := &probe{Metas: 3, NCAs: []string{repeat("a1"), repeat("b1"), repeat("c1"), repeat("e1")}}
	derive(rf, pr, ScanOptions{DB: db})
	if !rf.Complete || len(rf.Contents) != 3 || rf.GameVersion() != 65536 || rf.BaseID.String() != base {
		t.Fatalf("bundle: %+v", rf)
	}
	want := append([]nsw.Content(nil), rf.Contents...)
	for i := 0; i < 50; i++ {
		rf = &RomFile{Name: "bundle.nsp"}
		derive(rf, pr, ScanOptions{DB: db})
		if !reflect.DeepEqual(rf.Contents, want) || rf.BaseID.String() != base {
			t.Fatalf("unstable bundle: %+v", rf)
		}
	}
	db = makeDB(t, map[string]map[int64][]string{upd: {65536: {repeat("b1")}, 131072: {repeat("b1")}}})
	root := t.TempDir()
	for _, name := range []string{"old.nsp", "new.nsp"} {
		makeNSP(t, filepath.Join(root, name), [][2]string{nca("b1"), meta("d1")})
	}
	files, err := Scan(context.Background(), root, ScanOptions{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Complete || f.GameVersion() != -1 {
			t.Fatalf("identical sets resolved: %+v", f)
		}
	}
	if got := FindRedundant(Group(files), Namer{}); len(got) != 0 {
		t.Fatalf("ambiguous files marked redundant: %+v", got)
	}
}

func TestDecryptedCNMTPriorityAndCompleteness(t *testing.T) {
	const upd = "0100AAAA00000800"
	tid, _ := nsw.ParseTitleID(upd)
	db := makeDB(t, map[string]map[int64][]string{upd: {65536: {repeat("a1")}, 131072: {repeat("a1"), repeat("b1")}}})
	pr := &probe{Metas: 1, NCAs: []string{repeat("a1"), repeat("b1")}, Cnmt: []nsw.Content{{TitleID: tid, Version: 65536}}, CnmtVerified: []nsw.TitleID{tid}}
	for _, cnmtErr := range []string{"", "another meta could not decrypt"} {
		pr.CnmtErr = cnmtErr
		rf := &RomFile{Name: "patch.nsp"}
		derive(rf, pr, ScanOptions{DB: db})
		if rf.Source != SrcCNMT || rf.GameVersion() != 65536 || !rf.Complete {
			t.Fatalf("decrypted CNMT lost priority: %+v", rf)
		}
	}
	pr.CnmtVerified = nil
	rf := &RomFile{Name: "patch.nsp"}
	derive(rf, pr, ScanOptions{DB: db})
	if rf.Complete || rf.GameVersion() != 65536 {
		t.Fatalf("missing contents must preserve version but not completeness: %+v", rf)
	}
}

func TestMalformedContainerInvalidatesOldCacheAndMerge(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.nsp")
	makeNSP(t, bad, [][2]string{nca("a1"), meta("d1")})
	b, err := os.ReadFile(bad)
	if err != nil {
		t.Fatal(err)
	}
	// First NCA remains valid; the second entry points outside the file.
	binary.LittleEndian.PutUint64(b[16+24:], uint64(len(b)+100))
	if err := os.WriteFile(bad, b, 0600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(bad)
	if err != nil {
		t.Fatal(err)
	}
	cache := OpenScanCache(filepath.Join(t.TempDir(), "cache.json"))
	cache.put(bad, &probe{V: 2, Size: fi.Size(), ModTime: fi.ModTime().UnixNano(), Metas: 1, NCAs: []string{repeat("a1")}})
	db := makeDB(t, map[string]map[int64][]string{"0100AAAA00000800": {65536: {repeat("a1")}}})
	files, err := Scan(context.Background(), root, ScanOptions{DB: db, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Error == "" || files[0].Complete {
		t.Fatalf("old cache bypassed strict validation: %+v", files)
	}
	if cached := cache.get(bad, fi.Size(), fi.ModTime().UnixNano()); cached == nil || cached.V != probeVersion || cached.Err == "" {
		t.Fatalf("cache not refreshed: %+v", cached)
	}
	good := filepath.Join(root, "good.nsp")
	makeNSP(t, good, [][2]string{nca("b1")})
	out := filepath.Join(root, "merged.nsp")
	if _, err := Merge(context.Background(), []string{good, bad}, out, nil); err == nil || !strings.Contains(err.Error(), "bad.nsp") {
		t.Fatalf("merge did not propagate invalid input: %v", err)
	}
	for _, path := range []string{out, out + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("merge created incomplete output %s: %v", path, err)
		}
	}
}
