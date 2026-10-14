package deepseek

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrBadToken 表示粘贴的内容不是有效的平台 token（例如粘成了 Cookie）。
var ErrBadToken = errors.New("这不是有效的平台 token")

// cookieRe 匹配 name=value 形式的 Cookie 片段（token 末尾的 base64 填充 "=" 不会匹配）。
var cookieRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]*=[^=]`)

var bearerRe = regexp.MustCompile(`(?i)bearer\s+([A-Za-z0-9+/=_\-.~]+)`)

// NormalizeToken 把用户粘贴的各种形态整理成纯 token。
//
// 平台接口的登录凭据是请求头 `authorization: Bearer <token>` 里的 <token>，
// 也就是 localStorage 里 userToken 的 value；它不是 Cookie。为了少踩坑，下面这些写法都认：
//   - 纯 token
//   - Bearer <token>
//   - authorization: Bearer <token>（含 curl 里的 -H '…' 整行，甚至整条 curl 命令）
//   - localStorage 里的 JSON：{"value":"<token>","__version":"0"}
//   - 带引号的 token
//
// 参数 raw：用户粘贴的原始内容。
// 返回值：整理后的 token；内容为空返回空串与 nil；明显不是 token（如 Cookie）返回 ErrBadToken。
func NormalizeToken(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "{") {
		var v struct {
			Value string `json:"value"`
		}
		if json.Unmarshal([]byte(s), &v) == nil && strings.TrimSpace(v.Value) != "" {
			s = strings.TrimSpace(v.Value)
		}
	}
	if m := bearerRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = strings.Trim(s, "\"' \t\r\n")
	if len(s) < 8 || strings.ContainsAny(s, " \t\r\n;,{}") || cookieRe.MatchString(s) {
		return "", ErrBadToken
	}
	return s, nil
}

// ErrBrowserDenied 表示 macOS 拒绝本应用读取浏览器数据目录（缺少「完全磁盘访问权限」）。
// 它包装 ErrNoCredentials，因此界面仍显示「未登录」，但说明里会告诉用户怎么解决。
var ErrBrowserDenied = fmt.Errorf("%w：macOS 不允许 AI Usage 读取浏览器数据。请在「系统设置 → 隐私与安全性 → 完全磁盘访问权限」中允许 AI Usage 后重启，或到设置里改为手动粘贴 token", ErrNoCredentials)

// BrowserAccessDenied 判断是否存在被 macOS 拒绝读取（operation not permitted）的浏览器数据目录。
//
// 只做一次 ReadDir，开销很小。
//
// 参数 appSupport：Application Support 目录（为空取 $HOME/Library/Application Support）。
// 返回值：任一已安装浏览器的数据目录因权限被拒返回 true。
func BrowserAccessDenied(appSupport string) bool {
	if appSupport == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		appSupport = filepath.Join(home, "Library", "Application Support")
	}
	for _, b := range chromeBrowserDirs {
		_, err := os.ReadDir(filepath.Join(appSupport, filepath.FromSlash(b.Dir)))
		if err != nil && errors.Is(err, os.ErrPermission) {
			return true
		}
	}
	return false
}
