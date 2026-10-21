package secret

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
)

// exercise 对任意 Store 实现跑一遍通用契约测试。
func exercise(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在应返回 ErrNotFound: %v", err)
	}
	if err := s.Set("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Get("k"); err != nil || v != "v1" {
		t.Errorf("读回: %q %v", v, err)
	}
	s.Set("k", "v2") // 覆盖
	if v, _ := s.Get("k"); v != "v2" {
		t.Errorf("覆盖: %q", v)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Error("删除后应不存在")
	}
	if err := s.Delete("never"); err != nil {
		t.Errorf("删除不存在的键不应报错: %v", err)
	}
}

// TestMemory 验证内存实现。
func TestMemory(t *testing.T) { exercise(t, NewMemory()) }

// fakeKeeper 是 secretKeeper 的内存实现。
type fakeKeeper struct {
	m    map[string]string
	fail error // 不为空时 Get 一律返回它（模拟钥匙串被拒绝）
}

// Get 读取。
func (f *fakeKeeper) Get(key string) ([]byte, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	v, ok := f.m[key]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return []byte(v), nil
}

// TestKeychainGet 读到凭据原样返回；没有时返回 ErrNotFound；其他错误（如被拒绝）原样上报，不伪装成没有凭据。
func TestKeychainGet(t *testing.T) {
	k := &fakeKeeper{m: map[string]string{KeyDeepSeekKey: "sk-1"}}
	if v, err := keychainGet(k, KeyDeepSeekKey); err != nil || v != "sk-1" {
		t.Errorf("应读到凭据: %q %v", v, err)
	}
	if _, err := keychainGet(k, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("应返回 ErrNotFound: %v", err)
	}
	k.fail = errors.New("钥匙串被拒绝")
	if _, err := keychainGet(k, KeyDeepSeekKey); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("应上报真实错误: %v", err)
	}
}

// TestFile 验证文件实现，并检查文件权限为 0600、可跨实例读取。
func TestFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "secrets.json")
	f := &File{Path: p}
	exercise(t, f)
	f.Set("a", "1")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("权限应为 0600: %v", info.Mode().Perm())
	}
	if v, err := (&File{Path: p}).Get("a"); err != nil || v != "1" {
		t.Errorf("跨实例: %q %v", v, err)
	}
	os.WriteFile(p, []byte("{bad"), 0o600)
	if _, err := f.Get("a"); err == nil {
		t.Error("损坏文件应报错")
	}
}
