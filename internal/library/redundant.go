package library

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"my-switch-manager/internal/nsw"
)

// ============================ 冗余文件 ============================
//
// 一个文件里的每个 Title，都能在同一游戏的另一个文件里找到同样或更新的版本，这个文件就是冗余的：
//
//	旧版更新      UPD v65536 与 UPD v131072 并存
//	重复文件      同一本体既有 NSP 又有 XCI
//	合并后的源    本体/更新/DLC 已经合并成 (merged) 大包
//
// 只看内容清单精确且完整的文件（RomFile.Complete）：靠文件名或票据识别的文件可能漏掉内容，
// 例如「[B+U917504+6DLC]」里的 6 个 DLC 具体是哪几个无从得知，不能据此判断谁包含谁。
//
// 冗余文件不会被删除，只移到库根下的 RedundantDir（扫描时跳过），并写撤销日志。

const RedundantDir = "_redundant"

type RedundantFile struct {
	Key    string   `json:"key"` // 所属游戏
	Name   string   `json:"name"`
	Rel    string   `json:"rel"`
	Size   int64    `json:"size"`
	Reason string   `json:"reason"`
	KeptBy []string `json:"keptBy"` // 覆盖了它全部内容的文件（相对库根）
	File   *RomFile `json:"-"`
}

// FindRedundant 找出所有冗余文件，按游戏名、游戏、路径排序
func FindRedundant(games []*Game, namer Namer) []RedundantFile {
	var out []RedundantFile
	for _, g := range games {
		if g.BaseID == 0 || len(g.Files) < 2 {
			continue
		}
		var name string
		for _, r := range redundantInGame(g) {
			if name == "" {
				name, _ = namer.DisplayName(g)
			}
			r.Name = name
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Key != out[j].Key { // 同名的不同游戏不能交错，界面按游戏分组
			return out[i].Key < out[j].Key
		}
		return out[i].Rel < out[j].Rel
	})
	return out
}

func redundantInGame(g *Game) []RedundantFile {
	var cands []*RomFile
	for _, f := range g.Files {
		if f.Complete && f.Error == "" && len(f.Contents) > 0 {
			cands = append(cands, f)
		}
	}
	if len(cands) < 2 {
		return nil
	}
	// 贪心：先检查「最不值得保留」的文件——内容少的、体积大的（同样内容时留下 NSZ 这类更省空间的）。
	// 覆盖关系可传递，所以只让未被判为冗余的文件去覆盖别人，最后留下的文件仍然覆盖全部内容。
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if len(a.Contents) != len(b.Contents) {
			return len(a.Contents) < len(b.Contents)
		}
		if a.Size != b.Size {
			return a.Size > b.Size
		}
		return a.Rel > b.Rel
	})
	gone := map[*RomFile]bool{}
	var red []*RomFile
	for _, f := range cands {
		if _, ok := coveredBy(f, cands, gone); ok {
			gone[f] = true
			red = append(red, f)
		}
	}
	// 第一遍找到的覆盖者自己也可能随后被判为冗余，对照最终保留的文件重新说明理由
	var out []RedundantFile
	for _, f := range red {
		keptBy, _ := coveredBy(f, cands, gone)
		r := RedundantFile{Key: g.Key, Rel: f.Rel, Size: f.Size, File: f, Reason: redundantReason(f, keptBy)}
		for _, k := range keptBy {
			r.KeptBy = append(r.KeptBy, k.Rel)
		}
		out = append(out, r)
	}
	return out
}

// coveredBy 返回能覆盖 f 全部内容的其他文件（尽量少：优先用一个文件覆盖全部）
func coveredBy(f *RomFile, cands []*RomFile, gone map[*RomFile]bool) ([]*RomFile, bool) {
	has := func(g *RomFile, c nsw.Content) bool {
		for _, x := range g.Contents {
			if x.TitleID == c.TitleID && x.Version >= c.Version {
				return true
			}
		}
		return false
	}
	for _, g := range cands {
		if g == f || gone[g] {
			continue
		}
		all := true
		for _, c := range f.Contents {
			if !has(g, c) {
				all = false
				break
			}
		}
		if all {
			return []*RomFile{g}, true
		}
	}
	var keep []*RomFile
	for _, c := range f.Contents {
		var hit *RomFile
		for _, g := range cands {
			if g != f && !gone[g] && has(g, c) {
				hit = g
				break
			}
		}
		if hit == nil {
			return nil, false
		}
		if !containsFile(keep, hit) {
			keep = append(keep, hit)
		}
	}
	return keep, true
}

func containsFile(fs []*RomFile, f *RomFile) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

func redundantReason(f *RomFile, keptBy []*RomFile) string {
	if len(f.Contents) == 1 && f.Contents[0].Kind() == nsw.KindPatch && len(keptBy) == 1 {
		c := f.Contents[0]
		for _, x := range keptBy[0].Contents {
			if x.TitleID == c.TitleID && x.Version > c.Version {
				return fmt.Sprintf("旧版更新 v%d，已有 v%d", c.Version, x.Version)
			}
		}
	}
	if len(keptBy) == 1 && sameContents(f, keptBy[0]) {
		return "与另一文件内容相同"
	}
	if len(keptBy) == 1 {
		return "内容已全部包含在另一文件中"
	}
	return fmt.Sprintf("内容已全部包含在另外 %d 个文件中", len(keptBy))
}

func sameContents(a, b *RomFile) bool {
	if len(a.Contents) != len(b.Contents) {
		return false
	}
	for i := range a.Contents { // Contents 已按 TitleID 排序
		if a.Contents[i] != b.Contents[i] {
			return false
		}
	}
	return true
}

// MoveRedundant 把选中的冗余文件移到 <库根>/RedundantDir/<原相对路径>，保留目录结构以便人工核对
func MoveRedundant(root string, rels []string, j *Journal, logf Logf) (ok, failed int) {
	root = filepath.Clean(root)
	bin := filepath.Join(root, RedundantDir)
	for _, rel := range rels {
		if j.Err() != nil {
			logf("[失败] %v", j.Err())
			return ok, failed + 1
		}
		from := filepath.Join(root, filepath.FromSlash(rel))
		to := filepath.Join(bin, filepath.FromSlash(rel))
		if !withinRoot(root, from) || from == root || withinRoot(bin, from) || !RomExts[strings.ToLower(filepath.Ext(from))] {
			failed++
			logf("[拒绝] %s：不是库内的游戏文件", rel)
			continue
		}
		if err := j.mkdirAll(filepath.Dir(to)); err != nil {
			failed++
			logf("[失败] %s：无法创建目录：%v", rel, err)
			continue
		}
		if err := j.move(from, to); err != nil {
			failed++
			logf("[失败] %s：%v", rel, err)
			continue
		}
		ok++
		logf("[移走] %s", rel)
		if err := j.removeIfEmpty(filepath.Dir(from), root); err != nil {
			failed++
			logf("[失败] 清理空目录：%v", err)
		}
	}
	return ok, failed
}

// RedundantDirSize 统计已移走、尚未清理的冗余文件
func RedundantDirSize(root string) (n int, size int64) {
	filepath.WalkDir(filepath.Join(root, RedundantDir), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n++
				size += fi.Size()
			}
		}
		return nil
	})
	return n, size
}
