// Package titledb 加载 blawar/titledb 的元数据：版本历史、CNMT 内容表与中文名库。
package titledb

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liuzl/gocc"

	"my-switch-manager/internal/nsw"
)

// Files 是数据库需要的全部文件；versions.json 必需，其余缺失时对应功能降级
var Files = []string{"versions.json", "cnmts.json", "HK.zh.json", "CN.zh.json"}

// Entry 是中文名库（CN.zh.json / HK.zh.json）里的一条商品记录
type Entry struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Intro           string   `json:"intro"`
	Description     string   `json:"description"`
	Publisher       string   `json:"publisher"`
	Developer       string   `json:"developer"`
	ReleaseDate     int      `json:"releaseDate"`
	Category        []string `json:"category"`
	NumberOfPlayers int      `json:"numberOfPlayers"`
	Size            int64    `json:"size"`
	IconURL         string   `json:"iconUrl"`
	BannerURL       string   `json:"bannerUrl"`
	Screenshots     []string `json:"screenshots"`
	Languages       []string `json:"languages"`
	IsDemo          bool     `json:"isDemo"`
}

type cnmtEntry struct {
	TitleType      int `json:"titleType"` // 128 本体 / 129 更新 / 130 DLC
	Version        any `json:"version"`   // 库中有时是数字、有时是字符串
	ContentEntries []struct {
		NcaID string `json:"ncaId"`
	} `json:"contentEntries"`
}

type DB struct {
	Dir      string
	LoadedAt time.Time

	versions  map[nsw.TitleID]map[int64]string // 更新版本 -> 发布日期
	titleType map[nsw.TitleID]int
	dlcByBase map[nsw.TitleID][]nsw.TitleID
	ncaIndex  map[[16]byte][]nsw.Content // NCA 内容 ID -> 排序、去重的 Title/version 候选
	required  map[nsw.Content]ncaRequirements
	zhCN      map[nsw.TitleID]*Entry
	zhHK      map[nsw.TitleID]*Entry
	Warnings  []string
}

type ncaRequirements struct {
	ids   map[[16]byte]bool
	valid bool
}

type Stats struct {
	Versions int `json:"versions"`
	Cnmts    int `json:"cnmts"`
	NCAs     int `json:"ncas"`
	NamesCN  int `json:"namesCN"`
	NamesHK  int `json:"namesHK"`
}

func (db *DB) Stats() Stats {
	return Stats{len(db.versions), len(db.titleType), len(db.ncaIndex), len(db.zhCN), len(db.zhHK)}
}

func Load(dir string) (*DB, error) {
	db := &DB{
		Dir:       dir,
		versions:  map[nsw.TitleID]map[int64]string{},
		titleType: map[nsw.TitleID]int{},
		dlcByBase: map[nsw.TitleID][]nsw.TitleID{},
		ncaIndex:  map[[16]byte][]nsw.Content{},
		required:  map[nsw.Content]ncaRequirements{},
		zhCN:      map[nsw.TitleID]*Entry{},
		zhHK:      map[nsw.TitleID]*Entry{},
	}
	read := func(name string, v any) error {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		defer f.Close()
		return json.NewDecoder(f).Decode(v)
	}

	// 四个文件互不依赖，并行解析（HK.zh.json 与 cnmts.json 各约 50 MB）
	var (
		wg                     sync.WaitGroup
		rawV                   map[string]map[string]string
		rawC                   map[string]map[string]cnmtEntry
		rawCN, rawHK           map[string]*Entry
		errV, errC, errN, errH error
	)
	wg.Add(4)
	go func() { defer wg.Done(); errV = read("versions.json", &rawV) }()
	go func() { defer wg.Done(); errC = read("cnmts.json", &rawC) }()
	go func() { defer wg.Done(); errN = read("CN.zh.json", &rawCN) }()
	go func() { defer wg.Done(); errH = read("HK.zh.json", &rawHK) }()
	wg.Wait()

	if errV != nil {
		return nil, fmt.Errorf("读取 versions.json 失败: %w", errV)
	}
	for k, m := range rawV {
		tid, ok := nsw.ParseTitleID(k)
		if !ok {
			continue
		}
		vm := make(map[int64]string, len(m))
		for vs, date := range m {
			if v, err := strconv.ParseInt(vs, 10, 64); err == nil {
				vm[v] = date
			}
		}
		db.versions[tid] = vm
	}

	if errC != nil {
		db.Warnings = append(db.Warnings, "未加载 cnmts.json：DLC 清单与按 NCA 识别不可用")
	}
	for k, vers := range rawC {
		tid, ok := nsw.ParseTitleID(k)
		if !ok {
			continue
		}
		for _, e := range vers {
			// 冲突的类型也固定选择，不能依赖 map 遍历顺序。
			if old, exists := db.titleType[tid]; !exists || e.TitleType < old {
				db.titleType[tid] = e.TitleType
			}
			ver := anyToInt(e.Version)
			c := nsw.Content{TitleID: tid, Version: ver}
			req := ncaRequirements{ids: map[[16]byte]bool{}, valid: ver >= 0 && len(e.ContentEntries) > 0}
			for _, ce := range e.ContentEntries {
				id, ok := parseNCAID(ce.NcaID)
				if !ok {
					req.valid = false
					continue
				}
				req.ids[id] = true
			}
			if old, exists := db.required[c]; exists {
				// 同一版本有冲突清单时保留所有证据，但不能宣称完整。
				if len(old.ids) != len(req.ids) {
					req.valid = false
				}
				for id := range old.ids {
					if !req.ids[id] {
						req.valid = false
					}
					req.ids[id] = true
				}
				req.valid = req.valid && old.valid
			}
			db.required[c] = req
		}
		if db.titleType[tid] == 130 && tid.Kind() == nsw.KindAddOn {
			db.dlcByBase[tid.Base()] = append(db.dlcByBase[tid.Base()], tid)
		}
	}
	for c, req := range db.required {
		for id := range req.ids {
			db.ncaIndex[id] = append(db.ncaIndex[id], c)
		}
	}
	for _, candidates := range db.ncaIndex {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].TitleID != candidates[j].TitleID {
				return candidates[i].TitleID < candidates[j].TitleID
			}
			return candidates[i].Version < candidates[j].Version
		})
	}
	for b := range db.dlcByBase {
		s := db.dlcByBase[b]
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	}

	// 同一 TitleID 可能有多条记录（原版与 NS2 版等）：固定取发售日最早的那条，保证结果稳定
	fill := func(raw map[string]*Entry, dst map[nsw.TitleID]*Entry) {
		for _, e := range raw {
			if e == nil {
				continue
			}
			tid, ok := nsw.ParseTitleID(e.ID)
			if !ok {
				continue
			}
			if old, ok := dst[tid]; ok && old.ReleaseDate != 0 && (e.ReleaseDate == 0 || old.ReleaseDate <= e.ReleaseDate) {
				continue
			}
			dst[tid] = e
		}
	}
	if errN == nil {
		fill(rawCN, db.zhCN)
	}
	if errH == nil {
		fill(rawHK, db.zhHK)
	}
	if errN != nil && errH != nil {
		db.Warnings = append(db.Warnings, "未加载中文名库（CN.zh.json / HK.zh.json），中文名将留空")
	}
	db.LoadedAt = time.Now()
	return db, nil
}

func anyToInt(v any) int64 {
	switch x := v.(type) {
	case float64:
		if x >= 0 && x <= 0xffffffff && x == float64(uint32(x)) {
			return int64(x)
		}
	case string:
		if n, err := strconv.ParseUint(x, 10, 32); err == nil {
			return int64(n)
		}
	}
	return -1
}

// ============================ 查询 ============================

func parseNCAID(id string) ([16]byte, bool) {
	var k [16]byte
	b, err := hex.DecodeString(id)
	if err != nil || len(b) != len(k) {
		return k, false
	}
	copy(k[:], b)
	return k, true
}

// LookupNCACandidates 返回全部排序、去重的候选，调用者不能修改索引。
func (db *DB) LookupNCACandidates(id string) []nsw.Content {
	k, ok := parseNCAID(id)
	if !ok {
		return nil
	}
	return append([]nsw.Content(nil), db.ncaIndex[k]...)
}

// LookupNCA 仅在单个 NCA 恰好对应一个候选时返回结果；这不代表整包完整。
func (db *DB) LookupNCA(id string) (nsw.Content, bool) {
	cs := db.LookupNCACandidates(id)
	if len(cs) != 1 {
		return nsw.Content{}, false
	}
	return cs[0], true
}

// MatchNCAs 按每个 Title 分别使用整包证据消歧，不对多 Title 合集做全局交集。
// 只有唯一候选且其必需内容全部存在，才给出版本；否则保留 Title，版本为未知。
func (db *DB) MatchNCAs(ids []string) []nsw.Content {
	present := map[[16]byte]bool{}
	evidence := map[nsw.TitleID]map[[16]byte]bool{}
	candidates := map[nsw.TitleID]map[nsw.Content]bool{}
	for _, s := range ids {
		id, ok := parseNCAID(s)
		if !ok {
			continue
		}
		present[id] = true
		for _, c := range db.ncaIndex[id] {
			if evidence[c.TitleID] == nil {
				evidence[c.TitleID] = map[[16]byte]bool{}
				candidates[c.TitleID] = map[nsw.Content]bool{}
			}
			evidence[c.TitleID][id] = true
			candidates[c.TitleID][c] = true
		}
	}
	var out []nsw.Content
	for tid, cs := range candidates {
		var compatible []nsw.Content
		for c := range cs {
			req := db.required[c]
			matches := true
			for id := range evidence[tid] {
				if !req.ids[id] {
					matches = false
					break
				}
			}
			if matches {
				compatible = append(compatible, c)
			}
		}
		result := nsw.Content{TitleID: tid, Version: -1}
		if len(compatible) == 1 {
			c := compatible[0]
			req := db.required[c]
			complete := req.valid
			for id := range req.ids {
				if !present[id] {
					complete = false
					break
				}
			}
			if complete {
				result = c
			}
		}
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TitleID < out[j].TitleID })
	return out
}

// Latest 返回某 Title 的最新版本与发布日期
func (db *DB) Latest(tid nsw.TitleID) (int64, string, bool) {
	m := db.versions[tid]
	if len(m) == 0 {
		return 0, "", false
	}
	var best int64 = -1
	for v := range m {
		if v > best {
			best = v
		}
	}
	return best, m[best], true
}

// Versions 按版本降序返回全部补丁版本
func (db *DB) Versions(tid nsw.TitleID) []Patch {
	m := db.versions[tid]
	out := make([]Patch, 0, len(m))
	for v, d := range m {
		out = append(out, Patch{Version: v, Date: d})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

type Patch struct {
	Version int64  `json:"version"`
	Date    string `json:"date"`
}

// CountNewer 返回比 local 新的已发布版本数
func (db *DB) CountNewer(tid nsw.TitleID, local int64) int {
	n := 0
	for v := range db.versions[tid] {
		if v > local {
			n++
		}
	}
	return n
}

// DLCs 返回某游戏在库中的全部 DLC（按 TitleID 升序）
func (db *DB) DLCs(base nsw.TitleID) []nsw.TitleID { return db.dlcByBase[base] }

// Known 表示库里有该 Title 的任何记录
func (db *DB) Known(tid nsw.TitleID) bool {
	if _, ok := db.titleType[tid]; ok {
		return true
	}
	e, _ := db.Entry(tid)
	return e != nil
}

func (db *DB) IsDLCProduct(tid nsw.TitleID) bool { return db.titleType[tid] == 130 }

// Entry 取商品记录：简体官方库优先，其次港区
func (db *DB) Entry(tid nsw.TitleID) (*Entry, string) {
	if e, ok := db.zhCN[tid]; ok {
		return e, "CN"
	}
	if e, ok := db.zhHK[tid]; ok {
		return e, "HK"
	}
	return nil, ""
}

// Name 返回库中名称：国行简体 > 港区繁体（simplified 时转简体）。更新归位到本体查询。
func (db *DB) Name(tid nsw.TitleID, simplified bool) string {
	if tid.Kind() == nsw.KindPatch {
		tid = tid.Base()
	}
	if e, ok := db.zhCN[tid]; ok && e.Name != "" {
		return e.Name
	}
	if e, ok := db.zhHK[tid]; ok && e.Name != "" {
		if simplified {
			return ToSimplified(e.Name)
		}
		return e.Name
	}
	return ""
}

// NameHK 返回港区繁体原名（未转换）
func (db *DB) NameHK(tid nsw.TitleID) string {
	if e, ok := db.zhHK[tid]; ok {
		return e.Name
	}
	return ""
}

// ============================ 搜索 ============================

type Hit struct {
	TitleID string `json:"tid"`
	Name    string `json:"name"`
	Release string `json:"release"`
	IconURL string `json:"icon"`
	IsDLC   bool   `json:"isDLC"`
}

func normName(s string) string {
	r := strings.NewReplacer("™", "", "®", "", "　", "", " ", "", "-", "", "：", "", ":", "", "·", "", "・", "")
	return strings.ToLower(r.Replace(s))
}

// Search 按中文名片段或 TitleID 搜索；游戏本体优先，DLC 商品排后。
func (db *DB) Search(q string, simplified bool, limit int) []Hit {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	nq := normName(q)
	ql := strings.ToLower(q)
	byTID := map[nsw.TitleID]Hit{}
	consider := func(tid nsw.TitleID, e *Entry) {
		if _, ok := byTID[tid]; ok {
			return
		}
		name := e.Name
		if simplified {
			name = ToSimplified(name)
		}
		hitTID := len(ql) >= 4 && strings.Contains(strings.ToLower(tid.String()), ql)
		if !hitTID && !strings.Contains(normName(name), nq) && !strings.Contains(normName(e.Name), nq) {
			return
		}
		byTID[tid] = Hit{TitleID: tid.String(), Name: name, Release: FormatDate(e.ReleaseDate),
			IconURL: e.IconURL, IsDLC: db.IsDLCProduct(tid) || tid.Kind() == nsw.KindAddOn}
	}
	for tid, e := range db.zhCN {
		consider(tid, e)
	}
	for tid, e := range db.zhHK {
		consider(tid, e)
	}
	out := make([]Hit, 0, len(byTID))
	for _, h := range byTID {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDLC != out[j].IsDLC {
			return !out[i].IsDLC
		}
		// 名字越短越接近完整匹配
		li, lj := len([]rune(out[i].Name)), len([]rune(out[j].Name))
		if li != lj {
			return li < lj
		}
		return out[i].TitleID < out[j].TitleID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func FormatDate(d int) string {
	s := strconv.Itoa(d)
	if len(s) != 8 {
		return ""
	}
	return s[:4] + "-" + s[4:6] + "-" + s[6:]
}

// ============================ 繁 -> 简 ============================

var (
	t2sOnce sync.Once
	t2sConv *gocc.OpenCC
	t2sMu   sync.Mutex
	t2sMemo = map[string]string{}
)

// ToSimplified 用 OpenCC 做港繁转简；转换器不可用时原样返回
func ToSimplified(s string) string {
	if s == "" {
		return s
	}
	t2sOnce.Do(func() {
		if c, err := gocc.New("hk2s"); err == nil {
			t2sConv = c
		} else if c, err := gocc.New("t2s"); err == nil {
			t2sConv = c
		}
	})
	if t2sConv == nil {
		return s
	}
	t2sMu.Lock()
	defer t2sMu.Unlock()
	if v, ok := t2sMemo[s]; ok {
		return v
	}
	out, err := t2sConv.Convert(s)
	if err != nil {
		out = s
	}
	if len(t2sMemo) < 200000 {
		t2sMemo[s] = out
	}
	return out
}
