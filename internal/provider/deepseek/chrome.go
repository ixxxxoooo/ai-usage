package deepseek

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// ChromeToken 表示从某个 Chromium 系浏览器配置里读到的 DeepSeek 平台登录 token。
//
// @author ygw
type ChromeToken struct {
	Browser string // 浏览器名称，如 Chrome
	Profile string // 配置目录名，如 Default / Profile 1
	Token   string // userToken 原文（敏感，禁止写日志）
}

// chromeBrowserDirs 是 macOS 下常见 Chromium 系浏览器的数据目录（相对 Application Support）。
var chromeBrowserDirs = []struct{ Name, Dir string }{
	{"Chrome", "Google/Chrome"},
	{"Chrome Beta", "Google/Chrome Beta"},
	{"Edge", "Microsoft Edge"},
	{"Brave", "BraveSoftware/Brave-Browser"},
	{"Chromium", "Chromium"},
	{"Arc", "Arc/User Data"},
	{"Vivaldi", "Vivaldi"},
}

// 平台站点在 Chromium localStorage 里的键：_<origin>\x00\x01<key>。
const (
	platformOrigin = "https://platform.deepseek.com"
	tokenKeyName   = "userToken"
)

// FindChromeTokens 扫描本机浏览器各 Profile 的 localStorage，找出 DeepSeek 平台 token。
//
// 为避免与浏览器抢锁，会先把 leveldb 目录复制到临时目录再只读打开；全程不改动浏览器数据。
//
// 参数 appSupport：Application Support 目录（为空则取 $HOME/Library/Application Support）。
// 返回值：找到的 token 列表（可能多个 Profile）；没有找到返回空切片，不视为错误。
func FindChromeTokens(appSupport string) ([]ChromeToken, error) {
	toks, _, err := ScanChromeTokens(appSupport)
	return toks, err
}

// ScanChromeTokens 同 FindChromeTokens，但额外返回每个 Profile 的扫描记录，方便排查"为什么没找到"。
//
// 记录形如 "Chrome/Default：找到 token"、"Chrome/Profile 1：没有 DeepSeek 登录"、
// "Chrome：无法读取目录（operation not permitted）"。记录里不含 token。
//
// 参数 appSupport：Application Support 目录（为空则取 $HOME/Library/Application Support）。
// 返回值：token 列表、扫描记录、错误（仅在拿不到用户目录时）。
func ScanChromeTokens(appSupport string) ([]ChromeToken, []string, error) {
	if appSupport == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, err
		}
		appSupport = filepath.Join(home, "Library", "Application Support")
	}
	var out []ChromeToken
	var notes []string
	for _, b := range chromeBrowserDirs {
		root := filepath.Join(appSupport, filepath.FromSlash(b.Dir))
		profiles, err := os.ReadDir(root)
		if err != nil {
			if !os.IsNotExist(err) {
				notes = append(notes, fmt.Sprintf("%s：无法读取目录（%v）", b.Name, err))
			}
			continue
		}
		for _, p := range profiles {
			if !p.IsDir() || !(p.Name() == "Default" || strings.HasPrefix(p.Name(), "Profile ")) {
				continue
			}
			label := b.Name + "/" + p.Name()
			dir := filepath.Join(root, p.Name(), "Local Storage", "leveldb")
			tok, err := ReadTokenFromLevelDB(dir)
			switch {
			case err != nil:
				notes = append(notes, fmt.Sprintf("%s：读取失败（%v）", label, err))
			case tok == "":
				notes = append(notes, label+"：没有 DeepSeek 登录")
			default:
				notes = append(notes, label+"：找到 token")
				out = append(out, ChromeToken{Browser: b.Name, Profile: p.Name(), Token: tok})
			}
		}
	}
	return out, notes, nil
}

// ReadTokenFromLevelDB 从一个 Chromium localStorage leveldb 目录里读取 DeepSeek userToken。
//
// 参数 dir：leveldb 目录路径（会被复制到临时目录后只读打开）。
// 返回值：token 原文；不存在返回空串与 nil 错误。
func ReadTokenFromLevelDB(dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "aiusage-ldb-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := copyDirFlat(dir, tmp); err != nil {
		return "", err
	}
	db, err := leveldb.OpenFile(tmp, &opt.Options{ReadOnly: true})
	if err != nil {
		// 日志文件可能写到一半，尝试自动恢复
		db, err = leveldb.RecoverFile(tmp, nil)
		if err != nil {
			return "", err
		}
	}
	defer db.Close()

	prefix := []byte("_" + platformOrigin + "\x00")
	it := db.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()
	for it.Next() {
		key := it.Key()[len(prefix):]
		// 键名前有 1 字节编码标记（0x01=Latin-1）
		if len(key) > 0 && (key[0] == 0x00 || key[0] == 0x01) {
			key = key[1:]
		}
		if string(key) != tokenKeyName {
			continue
		}
		if tok := ParseTokenValue(it.Value()); tok != "" {
			return tok, nil
		}
	}
	return "", it.Error()
}

// ParseTokenValue 解析 localStorage 值：首字节为编码标记（0x01 Latin-1 / 0x00 UTF-16LE），
// 内容是 JSON（{"value":"…"}）或裸 token。
//
// 参数 raw：leveldb 值原始字节。
// 返回值：token；无法解析返回空串。
func ParseTokenValue(raw []byte) string {
	if len(raw) < 2 {
		return ""
	}
	body := raw[1:]
	var s string
	if raw[0] == 0x00 {
		u := make([]uint16, 0, len(body)/2)
		for i := 0; i+1 < len(body); i += 2 {
			u = append(u, uint16(body[i])|uint16(body[i+1])<<8)
		}
		s = string(utf16.Decode(u))
	} else {
		s = string(body)
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		var v struct {
			Value string `json:"value"`
		}
		if json.Unmarshal([]byte(s), &v) == nil && v.Value != "" {
			return strings.TrimSpace(v.Value)
		}
		return ""
	}
	return strings.Trim(s, "\"")
}

// copyDirFlat 把 src 目录下的普通文件复制到 dst（跳过 LOCK），失败的单个文件会被忽略。
func copyDirFlat(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == "LOCK" {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err == nil {
			copied++
		}
	}
	if copied == 0 {
		return fmt.Errorf("目录为空或无法读取: %s", src)
	}
	return nil
}

// copyFile 复制单个文件。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
