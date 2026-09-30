package nsw

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func partitionFixture(magic, name string, entrySize int, data []byte) []byte {
	b := make([]byte, 16+entrySize+len(name)+1)
	copy(b, magic)
	binary.LittleEndian.PutUint32(b[4:], 1)
	binary.LittleEndian.PutUint32(b[8:], uint32(len(name)+1))
	binary.LittleEndian.PutUint64(b[24:], uint64(len(data)))
	copy(b[16+entrySize:], name)
	return append(b, data...)
}

func TestContainerRejectsMalformedTable(t *testing.T) {
	original := partitionFixture("PFS0", "a.nca", 24, []byte("payload"))
	tests := map[string]func([]byte) []byte{
		"short-header":      func(b []byte) []byte { return b[:10] },
		"short-table":       func(b []byte) []byte { return b[:42] },
		"short-data":        func(b []byte) []byte { return b[:len(b)-1] },
		"name-offset":       func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32:], 999); return b },
		"mid-name-offset":   func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32:], 1); return b },
		"unterminated-name": func(b []byte) []byte { b[45] = 'x'; return b },
		"empty-name":        func(b []byte) []byte { b[40] = 0; return b },
		"invalid-utf8":      func(b []byte) []byte { b[40] = 0xff; return b },
		"negative-offset":   func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16:], math.MaxUint64); return b },
		"offset-overflow":   func(b []byte) []byte { binary.LittleEndian.PutUint64(b[16:], math.MaxInt64); return b },
		"negative-size":     func(b []byte) []byte { binary.LittleEndian.PutUint64(b[24:], math.MaxUint64); return b },
		"end-overflow":      func(b []byte) []byte { binary.LittleEndian.PutUint64(b[24:], math.MaxInt64); return b },
		"huge-table":        func(b []byte) []byte { binary.LittleEndian.PutUint32(b[8:], math.MaxUint32); return b },
	}
	for name, modify := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.nsp")
			if err := os.WriteFile(path, modify(bytes.Clone(original)), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := OpenContainer(path)
			if err == nil {
				c.Close()
				t.Fatal("malformed container accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "good.nsp")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := OpenContainer(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Entries) != 1 || c.Entries[0].Size != 7 {
		t.Fatalf("entries: %+v", c.Entries)
	}
}

type shortNilReader struct{ data []byte }

func (r shortNilReader) ReadAt(p []byte, off int64) (int, error) {
	n, _ := bytes.NewReader(r.data).ReadAt(p, off)
	return n, nil
}

func TestPartitionShortReadsAndBase(t *testing.T) {
	b := partitionFixture("PFS0", "a.nca", 24, []byte("x"))
	for _, base := range []int64{-1, math.MaxInt64 - 8} {
		if _, err := readPartition(bytes.NewReader(b), base, math.MaxInt64, "PFS0", 24); err == nil {
			t.Fatal("invalid base accepted")
		}
	}
	for _, length := range []int{8, 42} {
		if _, err := readPartition(shortNilReader{b[:length]}, 0, int64(len(b)), "PFS0", 24); err == nil {
			t.Fatal("short nil-error read accepted")
		}
	}
	// EOF from a partial table must not be ignored either.
	if _, err := readPartition(bytes.NewReader(b[:42]), 0, int64(len(b)), "PFS0", 24); err == nil {
		t.Fatal("partial table EOF accepted")
	}
}

func TestXCISecureBoundaries(t *testing.T) {
	secure := partitionFixture("HFS0", "a.nca", 64, []byte("x"))
	root := partitionFixture("HFS0", "secure", 64, secure)
	b := make([]byte, 0x200)
	copy(b[0x100:], "HEAD")
	binary.LittleEndian.PutUint64(b[0x130:], 0x200)
	b = append(b, root...)
	b = append(b, make([]byte, 100)...)
	if _, err := readXCISecure(bytes.NewReader(b), int64(len(b))); err != nil {
		t.Fatal(err)
	}
	// The NCA ends inside the XCI file but outside the declared secure partition.
	secureBase := 0x200 + 16 + 64 + len("secure") + 1
	binary.LittleEndian.PutUint64(b[secureBase+24:], 50)
	if _, err := readXCISecure(bytes.NewReader(b), int64(len(b))); err == nil {
		t.Fatal("entry outside secure accepted")
	}
	binary.LittleEndian.PutUint64(b[0x130:], math.MaxUint64)
	if _, err := readXCISecure(bytes.NewReader(b), int64(len(b))); err == nil {
		t.Fatal("invalid root offset accepted")
	}
}

func TestCNMTPartitionRejectsInvalidRanges(t *testing.T) {
	data := make([]byte, 0x20+0x38)
	tid, _ := ParseTitleID("0100AAAA00000800")
	binary.LittleEndian.PutUint64(data, uint64(tid))
	binary.LittleEndian.PutUint32(data[8:], 65536)
	binary.LittleEndian.PutUint16(data[0x10:], 1)
	for i := 0; i < 16; i++ {
		data[0x40+i] = 0xab
	}
	original := partitionFixture("PFS0", "a.cnmt", 24, data)
	info, err := parseCNMTPartition(original, 0)
	if err != nil || info.TitleID != tid || info.Version != 65536 || len(info.Required) != 1 || info.Required[0] != "abababababababababababababababab" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	for name, modify := range map[string]func([]byte){
		"negative-offset":     func(b []byte) { binary.LittleEndian.PutUint64(b[16:], math.MaxUint64-100) },
		"offset-overflow":     func(b []byte) { binary.LittleEndian.PutUint64(b[16:], math.MaxInt64) },
		"full-file-range":     func(b []byte) { binary.LittleEndian.PutUint64(b[24:], uint64(len(data)+1)) },
		"short-cnmt":          func(b []byte) { binary.LittleEndian.PutUint64(b[24:], 0x1f) },
		"short-content-table": func(b []byte) { binary.LittleEndian.PutUint64(b[24:], 0x20) },
		"extended-header":     func(b []byte) { binary.LittleEndian.PutUint16(b[47+0xe:], 0xffff) },
	} {
		t.Run(name, func(t *testing.T) {
			b := bytes.Clone(original)
			modify(b)
			if _, err := parseCNMTPartition(b, 0); err == nil {
				t.Fatal("invalid CNMT accepted")
			}
		})
	}
	for _, off := range []int64{-1, math.MaxInt64} {
		if _, err := parseCNMTPartition(original, off); err == nil {
			t.Fatal("invalid PFS offset accepted")
		}
	}
	k := &Keys{}
	for _, tc := range [][2]int64{{-1, 0xc00}, {0, -1}, {math.MaxInt64, 0xc00}, {0, 0xbff}} {
		if _, err := k.ReadCNMT(bytes.NewReader(nil), tc[0], tc[1]); err == nil {
			t.Fatal("invalid NCA range accepted")
		}
	}
}
