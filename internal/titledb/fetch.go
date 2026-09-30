package titledb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mirrors 下载源：国内访问 raw.githubusercontent.com 常失败，按顺序回退
var Mirrors = []string{
	"https://raw.githubusercontent.com/blawar/titledb/master/",
	"https://cdn.jsdelivr.net/gh/blawar/titledb@master/",
	"https://ghproxy.net/https://raw.githubusercontent.com/blawar/titledb/master/",
	"https://gh-proxy.com/https://raw.githubusercontent.com/blawar/titledb/master/",
	"https://ghfast.top/https://raw.githubusercontent.com/blawar/titledb/master/",
}

// Progress 回调：文件名、已下载字节、总字节（未知为 -1）
type Progress func(name string, done, total int64)

// Fetch 依次下载全部数据库文件。单个文件在所有源都失败时继续下一个，最后汇总错误。
func Fetch(ctx context.Context, dir string, logf func(string, ...any), prog Progress) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var failed []string
	for _, name := range Files {
		logf("下载 %s …", name)
		var lastErr error
		ok := false
		for _, base := range Mirrors {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, err := downloadJSON(ctx, base+name, filepath.Join(dir, name), func(d, t int64) {
				if prog != nil {
					prog(name, d, t)
				}
			})
			if err != nil {
				lastErr = err
				logf("  %s 失败：%v", hostOf(base), err)
				continue
			}
			src := "官方源"
			if base != Mirrors[0] {
				src = "镜像 " + hostOf(base)
			}
			logf("  完成 %s  [%s]", humanBytes(n), src)
			ok = true
			break
		}
		if !ok {
			failed = append(failed, name)
			logf("  [失败] 所有下载源都不可用：%v", lastErr)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d 个文件下载失败：%s（可手动下载放入 %s）", len(failed), strings.Join(failed, "、"), dir)
	}
	return nil
}

func hostOf(u string) string {
	u = strings.TrimPrefix(u, "https://")
	if i := strings.IndexByte(u, '/'); i > 0 {
		return u[:i]
	}
	return u
}

// downloadJSON 下载到临时文件，校验确实是完整 JSON 后才替换旧文件。
// 旧版不做校验：镜像站返回 200 的错误页时会直接覆盖掉可用的数据库。
func downloadJSON(ctx context.Context, url, dst string, prog func(done, total int64)) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "my-switch-manager")
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".download"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	pr := &progressReader{r: resp.Body, total: resp.ContentLength, cb: prog}
	n, err := io.Copy(f, pr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = validateJSON(tmp)
	}
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, dst)
}

func validateJSON(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(f, 1<<20))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return fmt.Errorf("内容不是 JSON 对象（可能是镜像错误页）")
	}
	// 逐个跳过键值，直到对象结束；能走完说明文件完整
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("JSON 不完整: %w", err)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return fmt.Errorf("JSON 不完整: %w", err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("JSON 不完整: %w", err)
	}
	return nil
}

type progressReader struct {
	r     io.Reader
	done  int64
	total int64
	last  time.Time
	cb    func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.cb != nil && (time.Since(p.last) > 300*time.Millisecond || err != nil) {
		p.last = time.Now()
		p.cb(p.done, p.total)
	}
	return n, err
}

func humanBytes(b int64) string {
	const u = 1024
	switch {
	case b >= u*u*u:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(u*u*u))
	case b >= u*u:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(u*u))
	}
	return fmt.Sprintf("%d KB", b/u)
}
