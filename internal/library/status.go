package library

import (
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"my-switch-manager/internal/nsw"
	"my-switch-manager/internal/titledb"
)

// 游戏状态
const (
	StateUnidentified = "unidentified" // 识别不出 TitleID
	StateNoRecord     = "norecord"     // 库中无版本记录
	StateUnknown      = "unknown"      // 本地版本未知
	StateOutdated     = "outdated"     // 有新版本
	StateLatest       = "latest"       // 已最新
)

// Status 是一个游戏与数据库比对后的结果
type Status struct {
	Name       string   `json:"name"`
	NameSource string   `json:"nameSource"`
	State      string   `json:"state"`
	Latest     int64    `json:"latest"`
	LatestDate string   `json:"latestDate"`
	Behind     int      `json:"behind"`
	DLCTotal   int      `json:"dlcTotal"`
	DLCOwned   int      `json:"dlcOwned"`
	DLCApprox  bool     `json:"dlcApprox"` // 拥有数来自文件名标注，非精确
	MissingDLC []string `json:"missingDLC,omitempty"`
	IconURL    string   `json:"icon,omitempty"`
	Publisher  string   `json:"publisher,omitempty"`
	Release    string   `json:"release,omitempty"`
	SupportsZh bool     `json:"supportsZh"`
	Category   string   `json:"category,omitempty"`
}

func (s Status) DLCMissing() int { return max(0, s.DLCTotal-s.DLCOwned) }

// Evaluate 计算一个游戏的状态（db 可为空）
func Evaluate(g *Game, db *titledb.DB, namer Namer) Status {
	st := Status{Latest: -1}
	st.Name, st.NameSource = namer.DisplayName(g)
	if g.BaseID == 0 {
		st.State = StateUnidentified
		return st
	}
	if db == nil {
		st.State = StateNoRecord
		return st
	}
	if e, _ := db.Entry(g.BaseID); e != nil {
		st.IconURL = e.IconURL
		st.Publisher = e.Publisher
		st.Release = titledb.FormatDate(e.ReleaseDate)
		st.SupportsZh = slices.Contains(e.Languages, "zh")
		st.Category = strings.Join(e.Category, " / ")
		if namer.Simplified {
			st.Category = titledb.ToSimplified(st.Category)
		}
	}

	latest, date, ok := db.Latest(g.BaseID)
	switch {
	case !ok:
		// 从未发布过更新的游戏在 versions.json 里没有记录；库里认识它且本地有本体即视为最新
		if g.HasApp && db.Known(g.BaseID) {
			st.State = StateLatest
			st.Latest = 0
		} else {
			st.State = StateNoRecord
		}
	case g.LocalVersion < 0:
		st.State, st.Latest, st.LatestDate = StateUnknown, latest, date
	case g.LocalVersion < latest:
		st.State, st.Latest, st.LatestDate = StateOutdated, latest, date
		st.Behind = db.CountNewer(g.BaseID, g.LocalVersion)
	default:
		st.State, st.Latest, st.LatestDate = StateLatest, latest, date
	}

	all := db.DLCs(g.BaseID)
	st.DLCTotal = len(all)
	for _, d := range all {
		if g.OwnedDLC[d] {
			st.DLCOwned++
		} else {
			st.MissingDLC = append(st.MissingDLC, d.String())
		}
	}
	// 只靠文件名识别的合集包：DLC 的具体 ID 不可知，只能用「+6DLC」标注的数量估算
	if len(g.OwnedDLC) == 0 && g.DlcMark > 0 {
		st.DLCOwned = g.DlcMark
		if st.DLCTotal > 0 {
			st.DLCOwned = min(st.DLCOwned, st.DLCTotal)
		}
		st.DLCApprox = true
		st.MissingDLC = nil
	}
	return st
}

// ============================ 详情 ============================

type PatchRow struct {
	Version  int64  `json:"version"`
	Date     string `json:"date"`
	IsLatest bool   `json:"isLatest"`
	Included bool   `json:"included"` // 本地版本 >= 该版本
}

type DLCRow struct {
	TitleID string `json:"tid"`
	Name    string `json:"name"`
	Intro   string `json:"intro,omitempty"`
	Desc    string `json:"desc,omitempty"`
	Icon    string `json:"icon,omitempty"`
	Owned   bool   `json:"owned"`
	Latest  int64  `json:"latest"`
	Date    string `json:"date,omitempty"`
}

type Info struct {
	TitleID     string   `json:"tid"`
	Name        string   `json:"name"`
	NameSource  string   `json:"nameSource"`
	LibName     string   `json:"libName"`
	NameHK      string   `json:"nameHK"`
	Intro       string   `json:"intro"`
	Desc        string   `json:"desc"`
	Publisher   string   `json:"publisher"`
	Developer   string   `json:"developer"`
	Release     string   `json:"release"`
	Category    string   `json:"category"`
	Players     int      `json:"players"`
	Size        int64    `json:"size"`
	IconURL     string   `json:"icon"`
	BannerURL   string   `json:"banner"`
	Screenshots []string `json:"screenshots"`
	Languages   []string `json:"languages"`
}

func GameInfo(tid nsw.TitleID, db *titledb.DB, namer Namer) Info {
	in := Info{TitleID: tid.String()}
	in.Name, in.NameSource = namer.Name(tid)
	if db == nil {
		return in
	}
	in.LibName = db.Name(tid, namer.Simplified)
	in.NameHK = db.NameHK(tid)
	e, _ := db.Entry(tid)
	if e == nil {
		return in
	}
	conv := func(s string) string {
		if namer.Simplified {
			return titledb.ToSimplified(s)
		}
		return s
	}
	in.Intro, in.Desc = conv(e.Intro), conv(e.Description)
	in.Publisher, in.Developer = e.Publisher, e.Developer
	in.Release = titledb.FormatDate(e.ReleaseDate)
	in.Category = conv(strings.Join(e.Category, " / "))
	in.Players, in.Size = e.NumberOfPlayers, e.Size
	in.IconURL, in.BannerURL = e.IconURL, e.BannerURL
	in.Screenshots, in.Languages = e.Screenshots, e.Languages
	return in
}

func PatchHistory(g *Game, db *titledb.DB) []PatchRow {
	if db == nil || g.BaseID == 0 {
		return nil
	}
	vs := db.Versions(g.BaseID)
	out := make([]PatchRow, 0, len(vs))
	for i, p := range vs {
		out = append(out, PatchRow{Version: p.Version, Date: p.Date, IsLatest: i == 0,
			Included: g.LocalVersion >= 0 && g.LocalVersion >= p.Version})
	}
	return out
}

func DLCList(g *Game, db *titledb.DB, namer Namer) []DLCRow {
	if db == nil || g.BaseID == 0 {
		return nil
	}
	var out []DLCRow
	for _, tid := range db.DLCs(g.BaseID) {
		r := DLCRow{TitleID: tid.String(), Owned: g.OwnedDLC[tid], Latest: -1}
		r.Name, _ = namer.Name(tid)
		if e, _ := db.Entry(tid); e != nil {
			r.Intro, r.Desc, r.Icon = e.Intro, e.Description, e.IconURL
			if namer.Simplified {
				r.Intro, r.Desc = titledb.ToSimplified(r.Intro), titledb.ToSimplified(r.Desc)
			}
		}
		if v, d, ok := db.Latest(tid); ok {
			r.Latest, r.Date = v, d
		}
		out = append(out, r)
	}
	return out
}

// ============================ CSV 导出 ============================

// 带 UTF-8 BOM，否则 Excel 打开中文是乱码
func newCSV(w io.Writer) *csv.Writer {
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	return csv.NewWriter(w)
}

// WriteTodoCSV 导出「待补清单」：只列需要更新或缺 DLC 的游戏。
// 工具不下载任何内容，这份清单用于在自己主机上走官方渠道更新后回来核对。
func WriteTodoCSV(w io.Writer, games []*Game, db *titledb.DB, namer Namer) (int, error) {
	cw := newCSV(w)
	cw.Write([]string{"游戏", "TitleID", "本地版本", "最新版本", "落后版本数", "最新发布日期", "缺DLC数", "缺失的DLC TitleID", "文件"})
	n := 0
	for _, g := range games {
		if g.BaseID == 0 {
			continue
		}
		st := Evaluate(g, db, namer)
		if st.State != StateOutdated && st.DLCMissing() == 0 {
			continue
		}
		latest := "未收录"
		if st.Latest >= 0 {
			latest = strconv.FormatInt(st.Latest, 10)
		}
		behind := ""
		if st.State == StateOutdated {
			behind = strconv.Itoa(st.Behind)
		}
		cw.Write([]string{st.Name, g.Key, verStr(g.LocalVersion), latest, behind, st.LatestDate,
			strconv.Itoa(st.DLCMissing()), strings.Join(st.MissingDLC, " "), g.Files[0].Rel})
		n++
	}
	cw.Flush()
	return n, cw.Error()
}

// WriteLibraryCSV 导出完整库清单
func WriteLibraryCSV(w io.Writer, games []*Game, db *titledb.DB, namer Namer) error {
	cw := newCSV(w)
	cw.Write([]string{"游戏", "TitleID", "状态", "本地版本", "最新版本", "DLC", "支持中文", "发行商", "发售日", "文件数", "总大小(字节)", "文件"})
	for _, g := range games {
		st := Evaluate(g, db, namer)
		dlc := ""
		if st.DLCTotal > 0 || st.DLCOwned > 0 {
			dlc = fmt.Sprintf("%d/%d", st.DLCOwned, st.DLCTotal)
		}
		zh := ""
		if st.SupportsZh {
			zh = "是"
		}
		var files []string
		for _, f := range g.Files {
			files = append(files, f.Rel)
		}
		latest := ""
		if st.Latest >= 0 {
			latest = strconv.FormatInt(st.Latest, 10)
		}
		cw.Write([]string{st.Name, g.BaseID.String(), StateLabel(st.State), verStr(g.LocalVersion), latest, dlc, zh,
			st.Publisher, st.Release, strconv.Itoa(len(g.Files)), strconv.FormatInt(g.TotalSize, 10), strings.Join(files, " | ")})
	}
	cw.Flush()
	return cw.Error()
}

// WriteNamesCSV 导出中文名对照表：TitleID, 自定义中文名, 库中名称, 文件
func WriteNamesCSV(w io.Writer, games []*Game, custom *StringMap, namer Namer) error {
	cw := newCSV(w)
	cw.Write([]string{"TitleID", "自定义中文名", "库中名称", "文件"})
	for _, g := range games {
		if g.BaseID == 0 {
			continue
		}
		lib := ""
		if namer.DB != nil {
			lib = namer.DB.Name(g.BaseID, namer.Simplified)
		}
		cw.Write([]string{g.BaseID.String(), custom.Get(g.BaseID.String()), lib, g.Files[0].Rel})
	}
	cw.Flush()
	return cw.Error()
}

// ImportNamesCSV 读回中文名表（只取前两列；忽略表头与非 TitleID 行；第 2 列留空不改动）
func ImportNamesCSV(r io.Reader, custom *StringMap) (int, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	rows, err := cr.ReadAll()
	if err != nil {
		return 0, err
	}
	changes := map[string]string{}
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		tid, ok := nsw.ParseTitleID(strings.TrimPrefix(strings.TrimSpace(row[0]), "\ufeff"))
		name := strings.TrimSpace(row[1])
		if !ok || name == "" || custom.Get(tid.String()) == name {
			continue
		}
		changes[tid.String()] = name
	}
	if len(changes) == 0 {
		return 0, nil
	}
	return len(changes), custom.SetMany(changes)
}

func verStr(v int64) string {
	if v < 0 {
		return "未知"
	}
	return strconv.FormatInt(v, 10)
}

func StateLabel(s string) string {
	switch s {
	case StateUnidentified:
		return "未识别"
	case StateNoRecord:
		return "库中无记录"
	case StateUnknown:
		return "本地版本未知"
	case StateOutdated:
		return "可更新"
	case StateLatest:
		return "已最新"
	}
	return s
}
