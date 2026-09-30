package library

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"my-switch-manager/internal/nsw"
)

// ============================ 合并 / XCI→NSP ============================
//
// 纯容器层操作：把源容器里的文件原样拼进一个新的 PFS0，不接触任何加密数据，不需要密钥。
// NCA 文件名就是内容哈希，同名视为同一份，自动去重。

type mergeSrc struct {
	nsw.Entry
	Archive string
}

type MergeJob struct {
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Inputs []string `json:"inputs"` // 相对库根
	Output string   `json:"output"` // 相对库根
	Bytes  int64    `json:"bytes"`
	Note   string   `json:"note,omitempty"`
}

const mergedTag = "(merged)"

// MergeCandidates 找出可以合并的游戏：本体与更新/DLC 分散在多个文件里
func MergeCandidates(root string, games []*Game, namer Namer) []MergeJob {
	var jobs []MergeJob
	for _, g := range games {
		if g.BaseID == 0 {
			continue
		}
		var ins []*RomFile
		hasApp := false
		skip := map[*RomFile]bool{}
		for _, r := range redundantInGame(g) {
			skip[r.File] = true
		}
		for _, f := range g.Files {
			// 已合并的产物、NSZ/XCZ（压缩格式，不能与普通 NCA 混拼）、冗余文件（如旧版更新）不参与
			if strings.Contains(f.Name, mergedTag) || f.Ext == ".nsz" || f.Ext == ".xcz" || skip[f] {
				continue
			}
			ins = append(ins, f)
			if f.KindCode() == "APP" {
				hasApp = true
			}
		}
		if len(ins) < 2 || !hasApp {
			continue
		}
		name, _ := namer.DisplayName(g)
		j := MergeJob{Key: g.Key, Name: name}
		for _, f := range ins {
			j.Inputs = append(j.Inputs, f.Rel)
			j.Bytes += f.Size
		}
		dir := filepath.Dir(ins[0].Path)
		out := filepath.Join(dir, sanitizeName(fmt.Sprintf("%s[%s]%s", GuessTitle(ins[0].Name), g.BaseID, mergedTag))+".nsp")
		j.Output = relOf(root, out)
		if exists(out) {
			j.Note = "输出文件已存在"
		}
		for _, f := range ins {
			if f.Ext == ".xci" {
				j.Note = "包含 XCI：卡带不含票据，合并产物仅作归档"
			}
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].Name < jobs[b].Name })
	return jobs
}

// MergeProgress：已写字节 / 总字节
type MergeProgress func(done, total int64, stage string)

// Merge 把 inputs 合并为 out（PFS0），完成后逐文件 SHA-256 校验；源文件不删除
func Merge(ctx context.Context, inputs []string, out string, prog MergeProgress) (int, error) {
	if exists(out) {
		return 0, fmt.Errorf("输出文件已存在：%s", filepath.Base(out))
	}
	var files []mergeSrc
	seen := map[string]bool{}
	for _, in := range inputs {
		c, err := nsw.OpenContainer(in)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", filepath.Base(in), err)
		}
		for _, e := range c.Entries {
			if seen[e.Name] {
				continue
			}
			seen[e.Name] = true
			files = append(files, mergeSrc{e, in})
		}
		c.Close()
	}
	if len(files) == 0 {
		return 0, errors.New("没有任何可合并的文件")
	}
	// NCA 在前，票据/证书/XML 在后，贴近常见合并 NSP 的组织
	rank := func(n string) int {
		l := strings.ToLower(n)
		switch {
		case strings.HasSuffix(l, ".tik"):
			return 2
		case strings.HasSuffix(l, ".cert"):
			return 3
		case strings.HasSuffix(l, ".xml"):
			return 4
		}
		return 1
	}
	sort.SliceStable(files, func(i, j int) bool { return rank(files[i].Name) < rank(files[j].Name) })

	if err := writePFS0(ctx, out, files, prog); err != nil {
		return 0, err
	}
	n, err := verifyPFS0(ctx, out, files, prog)
	if err != nil {
		return n, fmt.Errorf("校验未通过（产物已保留，请勿使用）：%w", err)
	}
	return n, nil
}

func writePFS0(ctx context.Context, out string, files []mergeSrc, prog MergeProgress) (err error) {
	var strTab []byte
	nameOff := make([]uint32, len(files))
	var total int64
	for i, f := range files {
		nameOff[i] = uint32(len(strTab))
		strTab = append(strTab, f.Name...)
		strTab = append(strTab, 0)
		total += f.Size
	}
	// 字符串表补齐到让头部按 0x20 对齐（与主流工具输出一致；读取方按字符串表大小定位，不受影响）
	for (16+len(files)*24+len(strTab))%0x20 != 0 {
		strTab = append(strTab, 0)
	}

	tmp := out + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmp)
		}
	}()
	head := make([]byte, 16, 16+len(files)*24)
	copy(head, "PFS0")
	binary.LittleEndian.PutUint32(head[4:], uint32(len(files)))
	binary.LittleEndian.PutUint32(head[8:], uint32(len(strTab)))
	var off int64
	for i, sf := range files {
		ent := make([]byte, 24)
		binary.LittleEndian.PutUint64(ent[0:], uint64(off))
		binary.LittleEndian.PutUint64(ent[8:], uint64(sf.Size))
		binary.LittleEndian.PutUint32(ent[16:], nameOff[i])
		head = append(head, ent...)
		off += sf.Size
	}
	if _, err = f.Write(append(head, strTab...)); err != nil {
		return err
	}

	buf := make([]byte, 8<<20)
	var done int64
	for _, sf := range files {
		src, e := os.Open(sf.Archive)
		if e != nil {
			return e
		}
		r := io.NewSectionReader(src, sf.Offset, sf.Size)
		for {
			if ctx.Err() != nil {
				src.Close()
				return ctx.Err()
			}
			n, re := r.Read(buf)
			if n > 0 {
				if _, we := f.Write(buf[:n]); we != nil {
					src.Close()
					return we
				}
				done += int64(n)
				if prog != nil {
					prog(done, total, "写入")
				}
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				src.Close()
				return re
			}
		}
		src.Close()
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if exists(out) {
		return fmt.Errorf("输出文件已存在：%s", filepath.Base(out))
	}
	return os.Rename(tmp, out)
}

// verifyPFS0 重新解析产物，逐个文件比对大小与 SHA-256
func verifyPFS0(ctx context.Context, out string, files []mergeSrc, prog MergeProgress) (int, error) {
	c, err := nsw.OpenContainer(out)
	if err != nil {
		return 0, fmt.Errorf("输出容器无法解析: %w", err)
	}
	defer c.Close()
	if len(c.Entries) != len(files) {
		return 0, fmt.Errorf("文件数不符：期望 %d，实际 %d", len(files), len(c.Entries))
	}
	var total, done int64
	for _, f := range files {
		total += f.Size
	}
	for i, want := range files {
		got := c.Entries[i]
		if got.Name != want.Name || got.Size != want.Size {
			return i, fmt.Errorf("%s 条目不符", want.Name)
		}
		h1, err := hashAt(ctx, want.Archive, want.Offset, want.Size)
		if err != nil {
			return i, err
		}
		h2, err := hashAt(ctx, out, got.Offset, got.Size)
		if err != nil {
			return i, err
		}
		if h1 != h2 {
			return i, fmt.Errorf("%s 哈希不一致", want.Name)
		}
		done += want.Size
		if prog != nil {
			prog(done, total, "校验")
		}
	}
	return len(files), nil
}

func hashAt(ctx context.Context, path string, off, size int64) ([32]byte, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	r := io.NewSectionReader(f, off, size)
	buf := make([]byte, 4<<20)
	for {
		if ctx.Err() != nil {
			return sum, ctx.Err()
		}
		n, err := r.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return sum, err
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// XCIToNSP 把 XCI 的 secure 分区打成 PFS0。卡带不含票据，产物是「NCA 容器」，仅作归档用途。
func XCIToNSP(ctx context.Context, in, out string, prog MergeProgress) (int, error) {
	return Merge(ctx, []string{in}, out, prog)
}
