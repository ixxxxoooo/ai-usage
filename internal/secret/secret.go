// Package secret 抽象凭据存储：生产环境用 macOS 钥匙串，测试用内存/文件实现。
//
// @author ixxxxoooo
package secret

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
)

// 凭据键名。
const (
	KeyCursorCookie  = "cursor-cookie"   // 手动粘贴的 Cursor Cookie
	KeyDeepSeekKey   = "deepseek-apikey" // DeepSeek API Key
	KeyDeepSeekToken = "deepseek-token"  // DeepSeek 平台登录 token（手动粘贴）
)

// ErrNotFound 表示没有这个凭据。
var ErrNotFound = errors.New("凭据不存在")

// Store 是凭据存储接口。
//
// @author ixxxxoooo
type Store interface {
	// Get 读取凭据，不存在返回 ErrNotFound。
	Get(key string) (string, error)
	// Set 写入凭据（覆盖）。
	Set(key, value string) error
	// Delete 删除凭据，不存在不报错。
	Delete(key string) error
}

// secretKeeper 是 mygo.Secrets 的读取能力，便于测试注入。
type secretKeeper interface {
	Get(key string) ([]byte, error)
}

// Keychain 使用 mygo.Secrets（macOS 登录钥匙串）。
type Keychain struct{}

// Get 读取钥匙串项。
func (Keychain) Get(key string) (string, error) { return keychainGet(mygo.Secrets, key) }

// keychainGet 读取钥匙串项，把 mygo 的「不存在」换成 ErrNotFound。
//
// 参数 k：钥匙串；key：凭据键名。
// 返回值：凭据内容；没有时返回 ErrNotFound，其他错误（如被拒绝）原样返回。
func keychainGet(k secretKeeper, key string) (string, error) {
	b, err := k.Get(key)
	switch {
	case err == nil:
		return string(b), nil
	case errors.Is(err, mygo.ErrSecretNotFound):
		return "", ErrNotFound
	default:
		return "", err
	}
}

// Set 写入钥匙串项（单项不超过 mygo.MaxSecretSize 字节）。
func (Keychain) Set(key, value string) error {
	return mygo.Secrets.Set(key, []byte(value))
}

// Delete 删除钥匙串项。
func (Keychain) Delete(key string) error { return mygo.Secrets.Delete(key) }

// Memory 是内存实现，供单元测试使用。
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory 创建内存存储。
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

// Get 读取。
func (s *Memory) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set 写入。
func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

// Delete 删除。
func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// File 把凭据存到一个 0600 权限的 JSON 文件，仅用于端到端测试（避免反复弹钥匙串授权）。
type File struct {
	mu   sync.Mutex
	Path string
}

// load 读取文件内容。
func (s *File) load() (map[string]string, error) {
	m := map[string]string{}
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// save 原子写回文件。
func (s *File) save(m map[string]string) error {
	b, _ := json.Marshal(m)
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Get 读取。
func (s *File) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set 写入。
func (s *File) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[key] = value
	return s.save(m)
}

// Delete 删除。
func (s *File) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	delete(m, key)
	return s.save(m)
}
