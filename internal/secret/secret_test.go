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

// fakeKeeper 是 secretKeeper 的内存实现，用来验证旧服务名的凭据迁移。
type fakeKeeper struct {
	m    map[string]string
	fail error // 不为空时 Get 一律返回它（模拟读不到/被拒绝）
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

// Set 写入。
func (f *fakeKeeper) Set(key string, value []byte) error {
	f.m[key] = string(value)
	return nil
}

// TestKeychainMigratesLegacyService 换 bundle id 前存在旧服务名下的凭据，读取时应自动迁移到新服务名。
func TestKeychainMigratesLegacyService(t *testing.T) {
	cur := &fakeKeeper{m: map[string]string{}}
	old := &fakeKeeper{m: map[string]string{KeyDeepSeekKey: "sk-old", "stale": "v"}}
	got, err := keychainGet(cur, old, KeyDeepSeekKey)
	if err != nil || got != "sk-old" {
		t.Fatalf("应读到旧服务名的凭据: %q %v", got, err)
	}
	if cur.m[KeyDeepSeekKey] != "sk-old" {
		t.Errorf("命中的旧凭据应写回新服务名: %v", cur.m)
	}
	if _, ok := cur.m["stale"]; ok {
		t.Error("只迁移被读取的那个键")
	}
	// 新服务名里已有（或已迁移过）时，直接返回，不再碰旧项。
	old.fail = errors.New("不应再访问旧服务名")
	if v, err := keychainGet(cur, old, KeyDeepSeekKey); err != nil || v != "sk-old" {
		t.Errorf("已有新凭据时应直接返回: %q %v", v, err)
	}
}

// TestKeychainNotFound 新旧服务名都没有该凭据时返回 ErrNotFound。
func TestKeychainNotFound(t *testing.T) {
	cur := &fakeKeeper{m: map[string]string{}}
	old := &fakeKeeper{m: map[string]string{}}
	if _, err := keychainGet(cur, old, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("应返回 ErrNotFound: %v", err)
	}
}

// TestKeychainLegacyError 读旧服务名出错（非「不存在」）时上报该错误，而不是伪装成没有凭据。
func TestKeychainLegacyError(t *testing.T) {
	cur := &fakeKeeper{m: map[string]string{}}
	old := &fakeKeeper{m: map[string]string{}, fail: errors.New("钥匙串被拒绝")}
	if _, err := keychainGet(cur, old, "k"); err == nil || errors.Is(err, ErrNotFound) {
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
