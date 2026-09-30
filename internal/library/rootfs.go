package library

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// rootFS 用目录句柄约束实际文件操作；仅检查路径字符串不能阻止符号链接逃逸。
type rootFS struct {
	root string
	*os.Root
}

func openRootFS(root string) (*rootFS, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	return &rootFS{root: filepath.Clean(abs), Root: r}, nil
}

func (r *rootFS) relative(path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.root, path)
	}
	rel, err := filepath.Rel(r.root, filepath.Clean(path))
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("路径不在库目录内：%s", path)
	}
	return rel, nil
}

func (r *rootFS) rename(from, to string) error {
	a, err := r.relative(from)
	if err != nil {
		return err
	}
	b, err := r.relative(to)
	if err != nil {
		return err
	}
	if a == "." || b == "." {
		return fmt.Errorf("不允许移动库根目录")
	}
	fi, err := r.Lstat(a)
	if err != nil {
		return fmt.Errorf("源不存在：%w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("不移动符号链接文件：%s", from)
	}
	// Stat 还会检查父目录中的链接，不能用普通 os.Stat 代替。
	if _, err := r.Stat(a); err != nil {
		return err
	}
	if target, err := r.Lstat(b); err == nil {
		if !os.SameFile(fi, target) {
			return fmt.Errorf("目标已存在，拒绝覆盖：%s", to)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return r.Rename(a, b)
}

func uniqueSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (r *rootFS) atomicWrite(path string, data []byte, mode fs.FileMode) error {
	name, err := r.relative(path)
	if err != nil {
		return err
	}
	if name == "." {
		return fmt.Errorf("不能覆盖库根目录")
	}
	tmp := filepath.Join(filepath.Dir(name), ".switchmgr-"+uniqueSuffix()+".tmp")
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := r.Rename(tmp, name); err != nil {
		return err
	}
	return syncRootDir(r.Root, filepath.Dir(name))
}

func (r *rootFS) saveJSON(path string, v any) error {
	data, err := encodeJSON(v)
	if err != nil {
		return err
	}
	return r.atomicWrite(path, data, 0o600)
}

func (r *rootFS) readDir(path string) ([]os.DirEntry, error) {
	name, err := r.relative(path)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// FileStamp 在恢复未提交操作时确认文件身份，避免把后来出现的同名文件移走。
type FileStamp struct {
	ID      string `json:"id,omitempty"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	Dir     bool   `json:"dir"`
}

func (r *rootFS) stamp(path string) (*FileStamp, error) {
	name, err := r.relative(path)
	if err != nil {
		return nil, err
	}
	li, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("不操作符号链接文件：%s", path)
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &FileStamp{ID: fileIdentity(f, info), Size: info.Size(), ModTime: info.ModTime().UnixNano(), Dir: info.IsDir()}, nil
}

func (s *FileStamp) matches(other *FileStamp) bool {
	if s == nil || other == nil || s.Dir != other.Dir {
		return false
	}
	if s.ID != "" && other.ID != "" && s.ID != other.ID {
		return false
	}
	if s.Dir {
		return s.ID != "" && s.ID == other.ID
	}
	return s.Size == other.Size && s.ModTime == other.ModTime
}
