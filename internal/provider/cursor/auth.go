// Package cursor 实现 Cursor 用量数据源：本机凭据读取与 dashboard 接口调用。
//
// @author ixxxxoooo
package cursor

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，用于只读 Cursor 的 state.vscdb
)

// 预定义错误，供上层区分状态（未登录/过期/限流等）。
var (
	// ErrNoCredentials 表示没有找到任何可用凭据。
	ErrNoCredentials = errors.New("未找到 Cursor 登录凭据")
	// ErrUnauthorized 表示凭据无效或已过期（HTTP 401）。
	ErrUnauthorized = errors.New("Cursor 登录已过期")
	// ErrForbidden 表示无权访问（HTTP 403）。
	ErrForbidden = errors.New("Cursor 拒绝访问（403）")
	// ErrRateLimited 表示被限流（HTTP 429）。
	ErrRateLimited = errors.New("Cursor 请求过于频繁，已被限流")
	// ErrBadResponse 表示响应不是预期格式。
	ErrBadResponse = errors.New("Cursor 返回了无法识别的数据")
)

// Credentials 是调用 Cursor dashboard 接口所需的凭据。
//
// @author ixxxxoooo
type Credentials struct {
	UserID string // WorkOS 用户 ID（user_xxx）
	Token  string // 会话 JWT（敏感）
}

// Cookie 返回 Cookie 头的值：WorkosCursorSessionToken=<userId>%3A%3A<jwt>。
//
// 返回值：Cookie 头内容。
func (c Credentials) Cookie() string {
	return "WorkosCursorSessionToken=" + c.UserID + "%3A%3A" + c.Token
}

// Valid 判断凭据是否完整。
//
// 返回值：用户 ID 与 token 都非空返回 true。
func (c Credentials) Valid() bool { return c.UserID != "" && c.Token != "" }

// DefaultStatePath 返回本机 Cursor 的 state.vscdb 路径。
//
// 返回值：路径；无法确定用户目录时返回空串。
func DefaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
}

// ReadLocalCredentials 只读打开 Cursor 的 state.vscdb，取出 accessToken 并解析出用户 ID。
//
// 该库可能高达数 GB，因此不复制，直接以只读模式查询（失败时退回 immutable 模式，避免被写锁挡住）。
//
// 参数 dbPath：state.vscdb 路径，空则用默认路径。
// 返回值：凭据；找不到文件/键返回 ErrNoCredentials。
func ReadLocalCredentials(dbPath string) (Credentials, error) {
	if dbPath == "" {
		dbPath = DefaultStatePath()
	}
	if dbPath == "" {
		return Credentials{}, ErrNoCredentials
	}
	if _, err := os.Stat(dbPath); err != nil {
		return Credentials{}, fmt.Errorf("%w：%v", ErrNoCredentials, err)
	}
	var token string
	var lastErr error
	for _, q := range []string{"mode=ro", "mode=ro&immutable=1"} {
		t, err := queryAccessToken(dbPath, q)
		if err == nil {
			token, lastErr = t, nil
			break
		}
		lastErr = err
	}
	if lastErr != nil {
		return Credentials{}, fmt.Errorf("读取 Cursor 本地数据库失败：%w", lastErr)
	}
	if strings.TrimSpace(token) == "" {
		return Credentials{}, ErrNoCredentials
	}
	return CredentialsFromToken(strings.TrimSpace(token))
}

// queryAccessToken 以指定 URI 参数打开数据库并查询 accessToken。
func queryAccessToken(dbPath, params string) (string, error) {
	u := url.URL{Scheme: "file", Path: dbPath, RawQuery: params + "&_pragma=busy_timeout(3000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var v sql.NullString
	err = db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

// jwtClaims 是我们关心的 JWT 载荷字段。
type jwtClaims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

// ParseJWT 不验签地解析 JWT 载荷。
//
// 参数 token：JWT 字符串。
// 返回值：sub 与过期时间（Unix 秒）；格式不合法返回错误。
func ParseJWT(token string) (sub string, exp int64, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", 0, errors.New("不是合法的 JWT")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", 0, fmt.Errorf("JWT 载荷解码失败：%w", err)
	}
	var c jwtClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", 0, fmt.Errorf("JWT 载荷解析失败：%w", err)
	}
	return c.Sub, c.Exp, nil
}

// UserIDFromSub 从 sub（形如 github|user_xxx 或 google-oauth2|user_xxx）取出用户 ID。
//
// 参数 sub：JWT 的 sub。
// 返回值：用户 ID（取最后一个 | 之后的部分）。
func UserIDFromSub(sub string) string {
	if i := strings.LastIndex(sub, "|"); i >= 0 {
		return sub[i+1:]
	}
	return sub
}

// CredentialsFromToken 由 JWT 构造凭据；若 JWT 已过期返回 ErrUnauthorized。
//
// 参数 token：会话 JWT。
// 返回值：凭据。
func CredentialsFromToken(token string) (Credentials, error) {
	sub, exp, err := ParseJWT(token)
	if err != nil {
		return Credentials{}, err
	}
	uid := UserIDFromSub(sub)
	if uid == "" {
		return Credentials{}, errors.New("JWT 中没有用户标识")
	}
	if exp > 0 && time.Unix(exp, 0).Before(time.Now()) {
		return Credentials{}, fmt.Errorf("%w（本地 token 已于 %s 过期，请先打开一次 Cursor）", ErrUnauthorized, time.Unix(exp, 0).Format("2006-01-02 15:04"))
	}
	return Credentials{UserID: uid, Token: token}, nil
}

// ParseManualCookie 解析用户手动粘贴的内容，宽松支持多种写法：
//   - 完整 Cookie 头：WorkosCursorSessionToken=user_xxx%3A%3Aeyj...
//   - 只有值：user_xxx%3A%3Aeyj... 或 user_xxx::eyj...
//   - 只有 JWT：eyj...
//
// 参数 s：粘贴内容。
// 返回值：凭据；格式无法识别返回错误。
func ParseManualCookie(s string) (Credentials, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Cookie:")
	s = strings.TrimSpace(s)
	if s == "" {
		return Credentials{}, errors.New("内容为空")
	}
	// 在多个 cookie 中挑出目标项
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "WorkosCursorSessionToken=") {
			s = strings.TrimPrefix(part, "WorkosCursorSessionToken=")
			break
		}
	}
	s = strings.Trim(s, "\"'")
	if dec, err := url.QueryUnescape(s); err == nil {
		s = dec
	}
	if i := strings.Index(s, "::"); i > 0 {
		uid, token := s[:i], s[i+2:]
		if strings.Count(token, ".") != 2 {
			return Credentials{}, errors.New("Cookie 中的 token 不是合法 JWT")
		}
		return Credentials{UserID: UserIDFromSub(uid), Token: token}, nil
	}
	if strings.Count(s, ".") == 2 {
		return CredentialsFromToken(s)
	}
	return Credentials{}, errors.New("无法识别的 Cookie 格式，请粘贴 WorkosCursorSessionToken 的值")
}
