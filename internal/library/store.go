package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ============================ 持久化的小型 JSON 存储 ============================

// saveJSON 写临时文件再改名，避免中途失败损坏原文件
func saveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	r, err := openRootFS(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer r.Close()
	return r.saveJSON(filepath.Base(path), v)
}

func loadJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

// StringMap 是一个线程安全、落盘的 string -> string 映射
// （自定义中文名：TitleID -> 名称；手动指定：文件名 -> TitleID）
type StringMap struct {
	path  string
	upper bool // 键是否统一转大写（TitleID）
	mu    sync.RWMutex
	m     map[string]string
}

func OpenStringMap(path string, upperKeys bool) *StringMap {
	s := &StringMap{path: path, upper: upperKeys, m: map[string]string{}}
	var raw map[string]string
	if loadJSON(path, &raw) == nil {
		for k, v := range raw {
			s.m[s.key(k)] = v
		}
	}
	return s
}

func (s *StringMap) key(k string) string {
	k = strings.TrimSpace(k)
	if s.upper {
		return strings.ToUpper(k)
	}
	return k
}

func (s *StringMap) Path() string { return s.path }

func (s *StringMap) Get(k string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m[s.key(k)]
}

// Set 设置并立即落盘；值为空表示删除
func (s *StringMap) Set(k, v string) error {
	return s.SetMany(map[string]string{k: v})
}

func (s *StringMap) SetMany(kv map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]string, len(s.m)+len(kv))
	for k, v := range s.m {
		next[k] = v
	}
	for k, v := range kv {
		v = strings.TrimSpace(v)
		if v == "" {
			delete(next, s.key(k))
		} else {
			next[s.key(k)] = v
		}
	}
	if err := saveJSON(s.path, next); err != nil {
		return err
	}
	s.m = next
	return nil
}

// renameWithAlias 在改名之前保存新键，保留旧键供撤销后使用。持锁覆盖整个改名，避免回滚覆盖并发修改。
func (s *StringMap) renameWithAlias(from, to string, action func() (bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from, to = s.key(from), s.key(to)
	value := s.m[from]
	if value == "" || from == to {
		_, err := action()
		return err
	}
	if old := s.m[to]; old != "" {
		if old != value {
			return fmt.Errorf("新文件名已有不同的手动匹配：%s", to)
		}
		_, err := action()
		return err
	}
	next := make(map[string]string, len(s.m)+1)
	for k, v := range s.m {
		next[k] = v
	}
	next[to] = value
	if err := saveJSON(s.path, next); err != nil {
		return fmt.Errorf("保存手动匹配失败，未改名：%w", err)
	}
	previous := s.m
	s.m = next
	moved, err := action()
	if err != nil && !moved {
		if rollbackErr := saveJSON(s.path, previous); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("清理新增匹配失败：%w", rollbackErr))
		}
		s.m = previous
	}
	return err
}

func (s *StringMap) All() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

func (s *StringMap) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks := make([]string, 0, len(s.m))
	for k := range s.m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (s *StringMap) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}
