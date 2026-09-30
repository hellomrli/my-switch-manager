package nsw

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Entry 是容器里的一个文件（NCA / tik / cert / cnmt.xml …）
type Entry struct {
	Name   string
	Offset int64 // 在源文件中的绝对偏移
	Size   int64
}

// Container 是一个已打开的游戏文件
type Container struct {
	Path    string
	Format  string // nsp / nsz / xci / xcz
	Entries []Entry
	file    *os.File
}

func (c *Container) Close() error {
	if c.file == nil {
		return nil
	}
	return c.file.Close()
}

// ReaderAt 用于直接读取容器内某个文件的数据
func (c *Container) ReaderAt() io.ReaderAt { return c.file }

// 表头最大读取量：合集包里可能有上千个 NCA，条目+字符串表远超旧版固定读取的 64 KB
const maxTableSize = 16 << 20

// OpenContainer 按扩展名打开 NSP/NSZ（PFS0）或 XCI/XCZ（HFS0 secure 分区）
func OpenContainer(path string) (*Container, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	c := &Container{Path: path, file: f}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".nsp", ".nsz":
		c.Format = ext[1:]
		c.Entries, err = readPartition(f, 0, fi.Size(), "PFS0", 0x18)
	case ".xci", ".xcz":
		c.Format = ext[1:]
		c.Entries, err = readXCISecure(f, fi.Size())
	default:
		err = fmt.Errorf("不支持的格式: %s", ext)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return c, nil
}

// validRange 用减法检查范围，避免 off+size 本身溢出。
func validRange(off, size, limit int64) bool {
	return off >= 0 && size >= 0 && off <= limit && size <= limit-off
}

func readFullAt(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n != len(p) {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	return err
}

// readPartition 解析 PFS0（条目 0x18 字节）或 HFS0（条目 0x40 字节），limit 是所属分区的绝对末端。
// 数据区起点 = base + 0x10 + 条目数*条目大小 + 字符串表大小；字符串表已包含对齐填充，不能再对齐。
func readPartition(r io.ReaderAt, base, limit int64, magic string, entrySize int) ([]Entry, error) {
	if !validRange(base, 16, limit) || (entrySize != 0x18 && entrySize != 0x40) {
		return nil, fmt.Errorf("%s 分区范围异常", magic)
	}
	head := make([]byte, 16)
	if err := readFullAt(r, head, base); err != nil {
		return nil, fmt.Errorf("读取 %s 头失败: %w", magic, err)
	}
	if string(head[:4]) != magic {
		return nil, fmt.Errorf("偏移 0x%X 处不是 %s", base, magic)
	}
	n := int64(binary.LittleEndian.Uint32(head[4:8]))
	strSize := int64(binary.LittleEndian.Uint32(head[8:12]))
	if n <= 0 || n > 65535 {
		return nil, fmt.Errorf("%s 文件数异常: %d", magic, n)
	}
	tableLen := n*int64(entrySize) + strSize
	if tableLen > maxTableSize || !validRange(base+16, tableLen, limit) {
		return nil, fmt.Errorf("%s 文件表大小或范围异常: %d 字节", magic, tableLen)
	}
	table := make([]byte, int(tableLen))
	if err := readFullAt(r, table, base+16); err != nil {
		return nil, fmt.Errorf("读取 %s 文件表失败: %w", magic, err)
	}
	strTab := table[int(n)*entrySize:]
	dataStart := base + 16 + tableLen
	out := make([]Entry, 0, int(n))
	for i := 0; i < int(n); i++ {
		e := table[i*entrySize:]
		off := binary.LittleEndian.Uint64(e[0:8])
		size := binary.LittleEndian.Uint64(e[8:16])
		no := uint64(binary.LittleEndian.Uint32(e[16:20]))
		if no >= uint64(len(strTab)) || (no > 0 && strTab[no-1] != 0) {
			return nil, fmt.Errorf("%s 条目 %d 字符串偏移异常", magic, i)
		}
		end := int(no)
		for end < len(strTab) && strTab[end] != 0 {
			end++
		}
		if end == int(no) || end == len(strTab) || !utf8.Valid(strTab[int(no):end]) {
			return nil, fmt.Errorf("%s 条目 %d 文件名无效或未终止", magic, i)
		}
		if off > uint64(math.MaxInt64-dataStart) || size > math.MaxInt64 ||
			!validRange(dataStart+int64(off), int64(size), limit) {
			return nil, fmt.Errorf("%s 条目 %d 数据范围异常", magic, i)
		}
		out = append(out, Entry{Name: string(strTab[int(no):end]), Offset: dataStart + int64(off), Size: int64(size)})
	}
	return out, nil
}

// readXCISecure：卡带头（0x100 处 "HEAD"）→ 根 HFS0 → secure 分区 HFS0 → NCA
func readXCISecure(r io.ReaderAt, fileSize int64) ([]Entry, error) {
	magic := make([]byte, 4)
	if err := readFullAt(r, magic, 0x100); err != nil {
		return nil, err
	}
	if string(magic) != "HEAD" {
		return nil, errors.New("0x100 处不是 HEAD，不是标准 XCI")
	}
	// 卡带头 0x130 处记录了根 HFS0 的偏移；常规值是 0xF000
	off := make([]byte, 8)
	if err := readFullAt(r, off, 0x130); err != nil {
		return nil, err
	}
	rootBase := int64(binary.LittleEndian.Uint64(off))
	if rootBase <= 0 || !validRange(rootBase, 16, fileSize) {
		return nil, errors.New("根 HFS0 偏移异常")
	}
	parts, err := readPartition(r, rootBase, fileSize, "HFS0", 0x40)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		if p.Name == "secure" {
			sec, err := readPartition(r, p.Offset, p.Offset+p.Size, "HFS0", 0x40)
			if err != nil {
				return nil, fmt.Errorf("解析 secure 分区失败: %w", err)
			}
			if len(sec) == 0 {
				return nil, errors.New("secure 分区为空")
			}
			return sec, nil
		}
	}
	return nil, errors.New("根分区里没有 secure 分区")
}

// TicketTitleIDs 从 .tik/.cert 文件名（RightsID = TitleID + 密钥代数）读取 TitleID，不需要解密
func TicketTitleIDs(entries []Entry) []TitleID {
	seen := map[TitleID]bool{}
	var out []TitleID
	for _, e := range entries {
		low := strings.ToLower(e.Name)
		if !strings.HasSuffix(low, ".tik") && !strings.HasSuffix(low, ".cert") {
			continue
		}
		if len(e.Name) < 16 {
			continue
		}
		if t, ok := ParseTitleID(e.Name[:16]); ok && t.Kind() != KindUnknown && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// NCAID 返回 NCA 条目的内容 ID（文件名去掉 .nca/.ncz/.cnmt.nca），非 NCA 返回空
func NCAID(name string) string {
	low := strings.ToLower(name)
	for _, suf := range []string{".cnmt.nca", ".cnmt.ncz", ".nca", ".ncz"} {
		if strings.HasSuffix(low, suf) {
			id := low[:len(low)-len(suf)]
			if len(id) == 32 {
				return id
			}
			return ""
		}
	}
	return ""
}

// IsMetaNCA 判断是否为 CNMT 元数据 NCA
func IsMetaNCA(name string) bool {
	low := strings.ToLower(name)
	return strings.HasSuffix(low, ".cnmt.nca")
}
