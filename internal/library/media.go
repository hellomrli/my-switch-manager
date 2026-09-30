package library

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ============================ 媒体文件与 gamelist.xml ============================
//
// ES-DE / EmulationStation 的目录结构：
//
//	Switch/
//	├── gamelist.xml     <path> 指 ROM，<image> 等指媒体
//	├── images/          集中存放封面：<ROM名>-thumb.jpg
//	└── 游戏/游戏.nsp
//
// ROM 改名后 gamelist 与图片名都会失联。这里按「旧 ROM 名 -> 新 ROM」映射同步修正。

const (
	MediaNone       = "none"        // 不处理
	MediaKeep       = "keep"        // 留在原目录，只改名（ES-DE 标准布局）
	MediaIntoFolder = "into-folder" // 移进各游戏文件夹
)

var mediaSuffixes = []string{
	"-thumb", "-marquee", "-image", "-boxart", "-screenshot",
	"-wheel", "-fanart", "-title", "-video", "-mix", "-logo",
}

var mediaExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".mp4": true, ".webm": true,
}

var centralMediaDirs = map[string]bool{"images": true, "media": true, "videos": true, "covers": true}

// RenameMap：旧 ROM 基名（不含扩展名）-> 新 ROM 绝对路径（含扩展名）
type RenameMap map[string]string

// RenameMapFrom 由执行结果（旧绝对路径 -> 新绝对路径）生成
func RenameMapFrom(renames map[string]string) RenameMap {
	m := RenameMap{}
	for o, n := range renames {
		m[stripExt(filepath.Base(o))] = n
	}
	return m
}

// LoadManifest 解析整理时写下的 _before_*.txt（两列制表符分隔，Windows 或 Unix 路径均可）。
// 旧版工具写的清单也能读：右列绝对路径只取「库根之后」的部分重新拼到当前库根上。
func LoadManifest(path, root string) (RenameMap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var oldRoot string
	m := RenameMap{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if strings.HasPrefix(line, "# 扫描根目录:") {
			oldRoot = strings.TrimSpace(strings.TrimPrefix(line, "# 扫描根目录:"))
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		oldBase := stripExt(baseNameAny(parts[0]))
		newRel := toSlash(parts[1])
		if oldRoot != "" {
			newRel = strings.TrimPrefix(newRel, strings.TrimRight(toSlash(oldRoot), "/")+"/")
		}
		if oldBase == "" || newRel == "" {
			continue
		}
		newAbs := newRel
		if !filepath.IsAbs(newRel) && !strings.Contains(newRel, ":") {
			newAbs = filepath.Join(root, filepath.FromSlash(newRel))
		}
		m[oldBase] = newAbs
	}
	return m, sc.Err()
}

func LatestManifest(root string) string {
	ms, _ := filepath.Glob(filepath.Join(root, "_before_*.txt"))
	if len(ms) == 0 {
		return ""
	}
	sort.Strings(ms)
	return ms[len(ms)-1]
}

type MediaPlan struct {
	Moves        []Move   `json:"moves"`      // 相对库根
	PathFixes    int      `json:"pathFixes"`  // gamelist.xml <path> 修正数
	MediaFixes   int      `json:"mediaFixes"` // gamelist.xml 媒体引用修正数
	Unmatched    []string `json:"unmatched"`  // 对不上任何 ROM 的媒体
	Orphans      []string `json:"orphans"`    // gamelist 里指向不存在文件的条目
	newGamelist  string
	oldGamelist  string
	pathGamelist string
	mediaTargets map[string]string
	hasGamelist  bool
}

// BuildMediaPlan 规划媒体改名与 gamelist.xml 修正
func BuildMediaPlan(root string, rm RenameMap, mode string) (*MediaPlan, error) {
	root = filepath.Clean(root)
	pl := &MediaPlan{Moves: []Move{}, mediaTargets: map[string]string{}}
	if mode == MediaNone || len(rm) == 0 {
		return pl, nil
	}
	into := mode == MediaIntoFolder
	rel := func(p string) string { return relOf(root, p) }
	planned := map[string]bool{}
	addMove := func(from, to string) {
		if from == to || planned[strings.ToLower(to)] {
			return
		}
		if exists(to) && !sameFile(from, to) {
			pl.Unmatched = append(pl.Unmatched, rel(from)+"（目标已存在："+rel(to)+"）")
			return
		}
		planned[strings.ToLower(to)] = true
		pl.Moves = append(pl.Moves, Move{From: rel(from), To: rel(to)})
	}
	target := func(dir, newRom, suffix, ext string) string {
		base := stripExt(filepath.Base(newRom)) + suffix + ext
		if into {
			return filepath.Join(filepath.Dir(newRom), base)
		}
		return filepath.Join(dir, base)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	central := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() && centralMediaDirs[strings.ToLower(e.Name())] {
			central[filepath.Join(root, e.Name())] = true
		}
	}
	// 集中式媒体目录（可能有 images/covers/ 这种子目录，递归处理）
	newNames := rmValues(rm)
	for d := range central {
		filepath.WalkDir(d, func(p string, de os.DirEntry, err error) error {
			if err != nil || de.IsDir() || !mediaExts[strings.ToLower(filepath.Ext(p))] {
				return nil
			}
			romKey, suffix, ext := splitMediaName(de.Name())
			newRom, ok := rm[romKey]
			if !ok {
				if !newNames[romKey] {
					pl.Unmatched = append(pl.Unmatched, rel(p))
				}
				return nil
			}
			if !exists(newRom) {
				pl.Unmatched = append(pl.Unmatched, rel(p)+"（对应的新 ROM 不存在）")
				return nil
			}
			addMove(p, target(filepath.Dir(p), newRom, suffix, ext))
			return nil
		})
	}
	// 游戏文件夹里的媒体
	filepath.WalkDir(root, func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if de.IsDir() {
			if central[p] || (p != root && skipDir(de.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Dir(p) == root || !mediaExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		romKey, suffix, ext := splitMediaName(de.Name())
		if newRom, ok := rm[romKey]; ok {
			addMove(p, target(filepath.Dir(p), newRom, suffix, ext))
		}
		return nil
	})

	// gamelist.xml
	glPath := filepath.Join(root, "gamelist.xml")
	data, err := os.ReadFile(glPath)
	if err == nil {
		pl.hasGamelist = true
		pl.oldGamelist = string(data)
		content := pl.oldGamelist
		movedTo := map[string]string{}
		for _, mv := range pl.Moves {
			movedTo[strings.ToLower(mv.From)] = mv.To
		}
		content = glPathRe.ReplaceAllStringFunc(content, func(s string) string {
			m := glPathRe.FindStringSubmatch(s)
			old := strings.TrimPrefix(toSlash(xmlUnescape(m[1])), "./")
			if newRom, ok := rm[stripExt(baseNameAny(old))]; ok {
				nv := xmlEscape("./" + rel(newRom))
				if nv != m[1] {
					pl.PathFixes++
					return "<path>" + nv + "</path>"
				}
				return s
			}
			if !exists(filepath.Join(root, filepath.FromSlash(old))) {
				pl.Orphans = append(pl.Orphans, old)
			}
			return s
		})
		pl.pathGamelist = content
		content = glMediaRe.ReplaceAllStringFunc(content, func(s string) string {
			m := glMediaRe.FindStringSubmatch(s)
			if m[1] != m[3] {
				return s
			}
			old := strings.TrimPrefix(toSlash(xmlUnescape(m[2])), "./")
			var nv string
			if to, ok := movedTo[strings.ToLower(old)]; ok {
				nv = to
			} else if romKey, suffix, ext := splitMediaName(baseNameAny(old)); rm[romKey] != "" {
				nv = rel(target(filepath.Join(root, filepath.Dir(filepath.FromSlash(old))), rm[romKey], suffix, ext))
			} else {
				if !exists(filepath.Join(root, filepath.FromSlash(old))) {
					pl.Orphans = append(pl.Orphans, old)
				}
				return s
			}
			pl.mediaTargets[strings.ToLower(old)] = toSlash(nv)
			nv = xmlEscape("./" + toSlash(nv))
			if nv == m[2] {
				return s
			}
			pl.MediaFixes++
			return "<" + m[1] + ">" + nv + "</" + m[1] + ">"
		})
		pl.newGamelist = content
	}
	sort.Slice(pl.Moves, func(i, j int) bool { return pl.Moves[i].From < pl.Moves[j].From })
	return pl, nil
}

var (
	glPathRe  = regexp.MustCompile(`<path>([^<]*)</path>`)
	glMediaRe = regexp.MustCompile(`<(image|marquee|thumbnail|video|fanart|boxart|screenshot|titlescreen|wheel)>([^<]*)</(image|marquee|thumbnail|video|fanart|boxart|screenshot|titlescreen|wheel)>`)
)

// rmValues 返回新 ROM 基名集合（媒体已经是新名字时不算“对不上”）
func rmValues(rm RenameMap) map[string]bool {
	out := make(map[string]bool, len(rm))
	for _, v := range rm {
		out[stripExt(filepath.Base(v))] = true
	}
	return out
}

// ExecuteMediaPlan 执行媒体移动并改写 gamelist.xml（先备份，备份路径记入撤销日志）
func ExecuteMediaPlan(root string, pl *MediaPlan, j *Journal, logf Logf) (ok, failed int) {
	root = filepath.Clean(root)
	succeeded := map[string]bool{}
	planned := map[string]bool{}
	for _, mv := range pl.Moves {
		planned[strings.ToLower(mv.From)] = true
	}
	for _, mv := range pl.Moves {
		if j.Err() != nil {
			logf("[失败] %v", j.Err())
			return ok, failed + 1
		}
		from := filepath.Join(root, filepath.FromSlash(mv.From))
		to := filepath.Join(root, filepath.FromSlash(mv.To))
		if err := j.mkdirAll(filepath.Dir(to)); err != nil {
			failed++
			logf("[失败] 创建目录 %s：%v", filepath.Dir(mv.To), err)
			continue
		}
		if err := j.move(from, to); err != nil {
			failed++
			logf("[失败] %s：%v", mv.From, err)
			continue
		}
		succeeded[strings.ToLower(mv.From)] = true
		ok++
	}
	if j.Err() != nil {
		return ok, failed
	}
	if pl.hasGamelist && pl.PathFixes+pl.MediaFixes > 0 {
		gl := filepath.Join(root, "gamelist.xml")
		name, err := j.fs.relative(gl)
		if err != nil {
			logf("[失败] %v", err)
			return ok, failed + 1
		}
		data, err := j.fs.ReadFile(name)
		if err != nil || string(data) != pl.oldGamelist {
			logf("[失败] gamelist.xml 已变化或无法读取，请重新生成方案：%v", err)
			return ok, failed + 1
		}
		mediaFixes := 0
		content := glMediaRe.ReplaceAllStringFunc(pl.pathGamelist, func(s string) string {
			m := glMediaRe.FindStringSubmatch(s)
			if m[1] != m[3] {
				return s
			}
			old := strings.ToLower(strings.TrimPrefix(toSlash(xmlUnescape(m[2])), "./"))
			to := pl.mediaTargets[old]
			if to == "" || (planned[old] && !succeeded[old]) {
				return s
			}
			target, err := j.fs.relative(filepath.Join(root, filepath.FromSlash(to)))
			if err != nil {
				return s
			}
			if fi, err := j.fs.Stat(target); err != nil || !fi.Mode().IsRegular() {
				return s
			}
			nv := xmlEscape("./" + toSlash(to))
			if nv == m[2] {
				return s
			}
			mediaFixes++
			return "<" + m[1] + ">" + nv + "</" + m[1] + ">"
		})
		if content != string(data) {
			if err := j.writeFile(gl, []byte(content)); err != nil {
				logf("[失败] 更新 gamelist.xml：%v", err)
				return ok, failed + 1
			}
			ok++
			logf("已更新 gamelist.xml：修正 %d 个 <path>、%d 个媒体引用（已备份并记录撤销）", pl.PathFixes, mediaFixes)
		}
	}
	return ok, failed
}

// ============================ 小工具 ============================

func xmlUnescape(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", "\"", "&apos;", "'", "&amp;", "&").Replace(s)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(s)
}

func toSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

func baseNameAny(p string) string {
	p = toSlash(p)
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func stripExt(s string) string {
	if i := strings.LastIndex(s, "."); i > 0 {
		return s[:i]
	}
	return s
}

// splitMediaName 把媒体文件名拆成「ROM 名 + 后缀 + 扩展名」，后缀取最长匹配
func splitMediaName(name string) (rom, suffix, ext string) {
	ext = filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	low := strings.ToLower(stem)
	best := ""
	for _, s := range mediaSuffixes {
		if strings.HasSuffix(low, s) && len(s) > len(best) {
			best = s
		}
	}
	return stem[:len(stem)-len(best)], stem[len(stem)-len(best):], ext
}
