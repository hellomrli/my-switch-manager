// Package library 是游戏库的业务层：扫描识别、按游戏聚合、版本/DLC 比对、整理、撤销、媒体同步与合并。
package library

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"my-switch-manager/internal/nsw"
	"my-switch-manager/internal/titledb"
)

var RomExts = map[string]bool{".nsp": true, ".xci": true, ".nsz": true, ".xcz": true}

// TitleID 识别来源，按可信度从高到低
const (
	SrcCNMT     = "cnmt"     // 解密 CNMT（需要 prod.keys），精确到版本
	SrcNCA      = "nca"      // NCA 内容 ID 与 cnmts.json 对上，精确到版本
	SrcTicket   = "ticket"   // 票据文件名，只有 TitleID
	SrcFilename = "filename" // 文件名里的 16 位 ID
	SrcManual   = "manual"   // 用户手动指定
)

// RomFile 是库里的一个游戏文件
type RomFile struct {
	Path        string        `json:"path"`
	Rel         string        `json:"rel"`
	Name        string        `json:"name"`
	Ext         string        `json:"ext"`
	Size        int64         `json:"size"`
	Contents    []nsw.Content `json:"contents"`
	Source      string        `json:"source"`
	BaseID      nsw.TitleID   `json:"-"`
	NameVersion int64         `json:"nameVersion"` // 文件名标注的版本，-1 = 无
	DlcMark     int           `json:"dlcMark"`     // 文件名标注的 DLC 数，-1 = 无
	Complete    bool          `json:"complete"`    // 内容清单精确且完整（每个 CNMT 都对上了，版本全部已知）
	Error       string        `json:"error,omitempty"`
}

// KindCode 用于命名模板：含本体即 APP，否则取唯一类别
func (f *RomFile) KindCode() string {
	has := f.kinds()
	switch {
	case has[nsw.KindApp]:
		return "APP"
	case has[nsw.KindPatch]:
		return "UPD"
	case has[nsw.KindAddOn]:
		return "DLC"
	}
	return "UNK"
}

func (f *RomFile) kinds() map[nsw.Kind]bool {
	m := map[nsw.Kind]bool{}
	for _, c := range f.Contents {
		m[c.Kind()] = true
	}
	return m
}

// KindLabel 是给人看的内容描述，例如 "本体+更新+6 DLC"
func (f *RomFile) KindLabel() string {
	var app, upd, dlc int
	for _, c := range f.Contents {
		switch c.Kind() {
		case nsw.KindApp:
			app++
		case nsw.KindPatch:
			upd++
		case nsw.KindAddOn:
			dlc++
		}
	}
	var p []string
	if app > 0 {
		p = append(p, "本体")
	}
	if upd > 0 {
		p = append(p, "更新")
	}
	if dlc == 1 && app+upd == 0 {
		p = append(p, "DLC")
	} else if dlc > 0 {
		p = append(p, fmt.Sprintf("%d DLC", dlc))
	}
	if len(p) == 0 {
		return "未识别"
	}
	return strings.Join(p, "+")
}

// GameVersion 返回该文件里本体/更新的最高版本，-1 = 未知
func (f *RomFile) GameVersion() int64 {
	v := int64(-1)
	for _, c := range f.Contents {
		if c.Kind() != nsw.KindAddOn && c.Version > v {
			v = c.Version
		}
	}
	return v
}

// ============================ 文件名解析 ============================
//
// 国内资源站命名里带的版本信息：
//
//	xxx [B+U1179648+1DLC].nsp     本体+更新 1179648，DLC 1 个
//	xxx [0100C66026536800][v65536].nsp
//	本工具整理后的命名：xxx[0100...][APP][v917504][+6DLC].nsp
var (
	buRe  = regexp.MustCompile(`(?i)B\+U(\d+)`)
	vRe   = regexp.MustCompile(`(?i)\[v(\d+)\]`)
	dlcRe = regexp.MustCompile(`(?i)\+(\d+)\s*DLC`)
	bRe   = regexp.MustCompile(`(?i)\[B(\+|\])`)
)

func parseNameVersion(name string) (ver int64, dlc int, bundleBase bool) {
	ver, dlc = -1, -1
	if m := buRe.FindStringSubmatch(name); m != nil {
		ver, _ = strconv.ParseInt(m[1], 10, 64)
	} else if m := vRe.FindStringSubmatch(name); m != nil {
		ver, _ = strconv.ParseInt(m[1], 10, 64)
	}
	if m := dlcRe.FindStringSubmatch(name); m != nil {
		dlc, _ = strconv.Atoi(m[1])
	}
	return ver, dlc, bRe.MatchString(name)
}

// ============================ 扫描缓存 ============================
//
// 缓存的是「文件本身的事实」（NCA 列表、票据、CNMT 解密结果），而不是推导结论，
// 这样数据库更新后无需重读文件；键为 路径+大小+修改时间。

// 缓存格式版本：probe 新增字段时递增，旧条目会被重新读取
const probeVersion = 3

type probe struct {
	V            int           `json:"v,omitempty"`
	Size         int64         `json:"size"`
	ModTime      int64         `json:"mtime"`
	NCAs         []string      `json:"ncas,omitempty"`
	Metas        int           `json:"metas,omitempty"` // CNMT NCA 个数 = 文件里包含的 Title 数
	Tickets      []nsw.TitleID `json:"tickets,omitempty"`
	Cnmt         []nsw.Content `json:"cnmt,omitempty"`
	CnmtVerified []nsw.TitleID `json:"cnmtVerified,omitempty"` // 必需内容全部存在的已解密 Title
	CnmtTried    bool          `json:"cnmtTried,omitempty"`
	CnmtErr      string        `json:"cnmtErr,omitempty"`
	CnmtKeys     string        `json:"cnmtKeys,omitempty"` // 尝试解密时所用密钥的指纹
	Err          string        `json:"err,omitempty"`
}

type ScanCache struct {
	path string
	mu   sync.Mutex
	m    map[string]*probe
	used map[string]bool
}

func OpenScanCache(path string) *ScanCache {
	c := &ScanCache{path: path, m: map[string]*probe{}, used: map[string]bool{}}
	loadJSON(path, &c.m)
	if c.m == nil {
		c.m = map[string]*probe{}
	}
	return c
}

func (c *ScanCache) get(path string, size, mtime int64) *probe {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.used[path] = true
	if p, ok := c.m[path]; ok && p.V == probeVersion && p.Size == size && p.ModTime == mtime {
		return p
	}
	return nil
}

func (c *ScanCache) put(path string, p *probe) {
	c.mu.Lock()
	c.m[path] = p
	c.used[path] = true
	c.mu.Unlock()
}

// Save 落盘，并丢弃本轮扫描中未出现、且位于 root 下的旧条目
func (c *ScanCache) Save(root string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if !c.used[k] && withinRoot(root, k) {
			delete(c.m, k)
		}
	}
	c.used = map[string]bool{}
	return saveJSON(c.path, c.m)
}

// ============================ 扫描 ============================

type ScanOptions struct {
	DB       *titledb.DB // 可为空：只能靠票据/文件名识别
	Keys     *nsw.Keys   // 可为空：不解密 CNMT
	Manual   *StringMap  // 文件名 -> TitleID
	Cache    *ScanCache  // 可为空
	Progress func(done, total int, current string)
	Workers  int
}

// 跳过回收站、NAS 系统目录与隐藏目录
func skipDir(name string) bool {
	low := strings.ToLower(name)
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") || name == RedundantDir ||
		low == "#recycle" || low == "$recycle.bin" || low == "system volume information"
}

func Scan(ctx context.Context, root string, opt ScanOptions) ([]*RomFile, error) {
	root = filepath.Clean(root)
	if fi, err := os.Stat(root); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("%s 不是目录", root)
	}
	type item struct {
		path string
		info fs.FileInfo
	}
	var items []item
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个目录读不了不影响整体
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !RomExts[strings.ToLower(filepath.Ext(p))] || strings.HasSuffix(p, ".part") {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			items = append(items, item{p, info})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]*RomFile, len(items))
	workers := opt.Workers
	if workers <= 0 {
		workers = min(8, runtime.NumCPU()*2)
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done int
		next = make(chan int)
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				it := items[i]
				out[i] = buildRomFile(root, it.path, it.info, opt)
				mu.Lock()
				done++
				if opt.Progress != nil {
					opt.Progress(done, len(items), it.info.Name())
				}
				mu.Unlock()
			}
		}()
	}
	for i := range items {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if opt.Cache != nil {
		opt.Cache.Save(root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

func buildRomFile(root, path string, info fs.FileInfo, opt ScanOptions) *RomFile {
	rel, _ := filepath.Rel(root, path)
	rf := &RomFile{Path: path, Rel: filepath.ToSlash(rel), Name: info.Name(),
		Ext: strings.ToLower(filepath.Ext(path)), Size: info.Size()}
	mtime := info.ModTime().UnixNano()

	var pr *probe
	if opt.Cache != nil {
		pr = opt.Cache.get(path, rf.Size, mtime)
		// 之前没有密钥、现在有了，或者上次解密失败而现在换了密钥（例如补上了更新的代数）：需要补读 CNMT
		if pr != nil && opt.Keys != nil && pr.Err == "" &&
			(!pr.CnmtTried || (pr.CnmtErr != "" && pr.CnmtKeys != opt.Keys.Fingerprint())) {
			pr = nil
		}
	}
	if pr == nil {
		pr = probeFile(path, opt.Keys)
		pr.V, pr.Size, pr.ModTime = probeVersion, rf.Size, mtime
		if opt.Cache != nil {
			opt.Cache.put(path, pr)
		}
	}
	derive(rf, pr, opt)
	return rf
}

// probeFile 读取容器文件表；有密钥时再解密每个 CNMT NCA
func probeFile(path string, keys *nsw.Keys) *probe {
	pr := &probe{}
	c, err := nsw.OpenContainer(path)
	if err != nil {
		pr.Err = err.Error()
		return pr
	}
	defer c.Close()
	for _, e := range c.Entries {
		if id := nsw.NCAID(e.Name); id != "" {
			pr.NCAs = append(pr.NCAs, id)
		}
		if nsw.IsMetaNCA(e.Name) {
			pr.Metas++
		}
	}
	pr.Tickets = nsw.TicketTitleIDs(c.Entries)
	if keys != nil {
		pr.CnmtTried, pr.CnmtKeys = true, keys.Fingerprint()
		present := map[string]bool{}
		for _, id := range pr.NCAs {
			present[id] = true
		}
		for _, e := range c.Entries {
			if !nsw.IsMetaNCA(e.Name) {
				continue
			}
			ct, err := keys.ReadCNMTInfo(c.ReaderAt(), e.Offset, e.Size)
			if err != nil {
				pr.CnmtErr = err.Error()
				continue
			}
			pr.Cnmt = append(pr.Cnmt, ct.Content)
			complete := len(ct.Required) > 0
			for _, id := range ct.Required {
				if !present[id] {
					complete = false
				}
			}
			if complete {
				pr.CnmtVerified = append(pr.CnmtVerified, ct.TitleID)
			}
		}
	}
	return pr
}

// derive 按可信度合并各来源，得出文件包含的内容
func derive(rf *RomFile, pr *probe, opt ScanOptions) {
	rf.NameVersion, rf.DlcMark = -1, -1
	ver, dlc, bundleBase := parseNameVersion(rf.Name)
	rf.NameVersion, rf.DlcMark = ver, dlc
	rf.Error = pr.Err
	rf.Complete = false

	set := map[nsw.TitleID]int64{}
	add := func(t nsw.TitleID, v int64) {
		if t.Kind() == nsw.KindUnknown {
			return
		}
		if old, ok := set[t]; !ok || v > old {
			set[t] = v
		}
	}

	verified := map[nsw.TitleID]bool{}
	for _, tid := range pr.CnmtVerified {
		verified[tid] = true
	}
	cnmtTIDs := map[nsw.TitleID]bool{}
	for _, c := range pr.Cnmt {
		add(c.TitleID, c.Version)
		cnmtTIDs[c.TitleID] = true
	}
	if len(set) > 0 {
		rf.Source = SrcCNMT
	}
	// 没有密钥，或部分 CNMT 解不开（例如需要更新的密钥）：用 NCA 比对补齐，已解密的以解密结果为准
	if cnmtOK := len(pr.Cnmt) > 0 && pr.CnmtErr == ""; !cnmtOK {
		if opt.DB != nil {
			for _, c := range opt.DB.MatchNCAs(pr.NCAs) {
				if !cnmtTIDs[c.TitleID] {
					add(c.TitleID, c.Version)
					verified[c.TitleID] = c.Version >= 0
				}
			}
		}
		if rf.Source == "" && len(set) > 0 {
			rf.Source = SrcNCA
		}
		for _, t := range pr.Tickets {
			if _, ok := set[t]; !ok {
				add(t, -1)
				if rf.Source == "" {
					rf.Source = SrcTicket
				}
			}
		}
		if len(set) == 0 {
			if t := nsw.TitleIDFromName(rf.Name); t != 0 {
				add(t, -1)
				rf.Source = SrcFilename
			}
		}
	}
	// Title 数吻合只是必要条件；每个 Title 还必须唯一识别、必需 NCA 全在且容器校验成功。
	if pr.Err == "" && (rf.Source == SrcCNMT || rf.Source == SrcNCA) && pr.Metas > 0 && len(set) == pr.Metas {
		rf.Complete = true
		for tid, v := range set {
			if v < 0 || !verified[tid] {
				rf.Complete = false
			}
		}
	}
	// 手动指定优先级最高：用户明确说了算
	if opt.Manual != nil {
		if m := opt.Manual.Get(rf.Name); m != "" {
			if t, ok := nsw.ParseTitleID(m); ok {
				set = map[nsw.TitleID]int64{}
				add(t, -1)
				rf.Source, rf.Complete = SrcManual, false
			}
		}
	}
	if len(set) == 0 {
		return
	}

	// 版本未知时用文件名标注补齐；[B+U…] 说明含本体
	var base nsw.TitleID
	for t := range set {
		if base == 0 || (t.Kind() == nsw.KindApp && base.Kind() != nsw.KindApp) ||
			(t.Kind() == nsw.KindPatch && base.Kind() == nsw.KindAddOn) ||
			(t.Kind() == base.Kind() && t < base) {
			base = t
		}
	}
	base = base.Base()
	if rf.Source != SrcCNMT && rf.Source != SrcNCA {
		// 文件名里写的是本体 ID（本工具整理后的命名即如此），说明文件含本体——
		// 从 XCI 本体合并 eShop 更新得到的包，票据里只有更新和 DLC 的。
		if nt := nsw.TitleIDFromName(rf.Name); nt == base && nt.Kind() == nsw.KindApp {
			if _, ok := set[base]; !ok {
				add(base, 0)
			}
		}
		if bundleBase {
			if _, ok := set[base]; !ok {
				add(base, 0)
			}
			if ver > 0 {
				add(base.PatchID(), ver)
			}
		}
		for t, v := range set {
			if v >= 0 || ver < 0 {
				continue
			}
			switch t.Kind() {
			case nsw.KindPatch:
				set[t] = ver
			case nsw.KindApp:
				if ver == 0 || !bundleBase {
					set[t] = ver
				}
			}
		}
		if ver > 0 && !bundleBase {
			if _, hasApp := set[base]; hasApp && set[base] == ver {
				// "[0100…000][v65536]" 这种写法：本体 ID + 更新版本，说明是合并包
				set[base] = 0
				add(base.PatchID(), ver)
			}
		}
	}
	rf.BaseID = base
	rf.Contents = rf.Contents[:0]
	for t, v := range set {
		rf.Contents = append(rf.Contents, nsw.Content{TitleID: t, Version: v})
	}
	sort.Slice(rf.Contents, func(i, j int) bool { return rf.Contents[i].TitleID < rf.Contents[j].TitleID })
}

// ============================ 按游戏聚合 ============================

type Game struct {
	Key          string               `json:"key"`
	BaseID       nsw.TitleID          `json:"-"`
	Files        []*RomFile           `json:"-"`
	HasApp       bool                 `json:"hasApp"`
	HasPatch     bool                 `json:"hasPatch"`
	LocalVersion int64                `json:"localVersion"`
	OwnedDLC     map[nsw.TitleID]bool `json:"-"`
	DlcMark      int                  `json:"dlcMark"`
	TotalSize    int64                `json:"totalSize"`
}

func Group(files []*RomFile) []*Game {
	m := map[string]*Game{}
	var order []string
	for _, f := range files {
		key := "?" + f.Rel
		if f.BaseID != 0 {
			key = f.BaseID.String()
		}
		g, ok := m[key]
		if !ok {
			g = &Game{Key: key, BaseID: f.BaseID, LocalVersion: -1, DlcMark: -1, OwnedDLC: map[nsw.TitleID]bool{}}
			m[key] = g
			order = append(order, key)
		}
		g.Files = append(g.Files, f)
		g.TotalSize += f.Size
		if f.DlcMark > g.DlcMark {
			g.DlcMark = f.DlcMark
		}
		for _, c := range f.Contents {
			switch c.Kind() {
			case nsw.KindApp:
				g.HasApp = true
			case nsw.KindPatch:
				g.HasPatch = true
			case nsw.KindAddOn:
				g.OwnedDLC[c.TitleID] = true
			}
			// 游戏版本只看本体/更新；旧版会把 DLC 文件名里的 [v65536] 误当成游戏版本
			if c.Kind() != nsw.KindAddOn && c.Version > g.LocalVersion {
				g.LocalVersion = c.Version
			}
		}
	}
	out := make([]*Game, 0, len(order))
	for _, k := range order {
		out = append(out, m[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ============================ 名称 ============================

type Namer struct {
	DB         *titledb.DB
	Custom     *StringMap
	Simplified bool
}

// Name 返回显示名与来源：custom（自定义）/ db（库）/ ""
func (n Namer) Name(tid nsw.TitleID) (string, string) {
	if tid == 0 {
		return "", ""
	}
	if n.Custom != nil {
		if v := n.Custom.Get(tid.String()); v != "" {
			return v, "custom"
		}
	}
	if n.DB != nil {
		if v := n.DB.Name(tid, n.Simplified); v != "" {
			return v, "db"
		}
	}
	return "", ""
}

// GuessTitle 从文件名猜原名（去掉 [] () 标记与扩展名）
func GuessTitle(name string) string {
	s := strings.TrimSuffix(name, filepath.Ext(name))
	s = bracketRe.ReplaceAllString(s, " ")
	s = parenRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("_", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return strings.TrimSuffix(name, filepath.Ext(name))
	}
	return s
}

var (
	bracketRe = regexp.MustCompile(`\[[^\]]*\]`)
	parenRe   = regexp.MustCompile(`\([^)]*\)`)
)

// DisplayName 为游戏选一个名字：自定义 > 库中名 > 文件名猜测
func (n Namer) DisplayName(g *Game) (string, string) {
	if name, src := n.Name(g.BaseID); name != "" {
		return name, src
	}
	if len(g.Files) > 0 {
		return GuessTitle(g.Files[0].Name), "file"
	}
	return g.Key, "file"
}

func withinRoot(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
