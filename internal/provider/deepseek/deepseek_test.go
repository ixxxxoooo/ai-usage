package deepseek

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
)

// TestParseBalance 验证 API Key 余额解析、多币种优先级与异常。
func TestParseBalance(t *testing.T) {
	body := `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"3.00","granted_balance":"1.00","topped_up_balance":"2.00"},{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`
	b, err := ParseBalance([]byte(body), time.Now())
	if err != nil || b.Currency != "CNY" || b.Total != 110 || b.Granted != 10 || b.ToppedUp != 100 || !b.Available || b.Source != "apikey" {
		t.Fatalf("%+v %v", b, err)
	}
	b, _ = ParseBalance([]byte(`{"is_available":false,"balance_infos":[{"currency":"usd","total_balance":"0.00","granted_balance":"0","topped_up_balance":"0"}]}`), time.Now())
	if b.Currency != "USD" || b.Available {
		t.Errorf("USD: %+v", b)
	}
	for _, bad := range []string{`{}`, `{"balance_infos":[]}`, `x`} {
		if _, err := ParseBalance([]byte(bad), time.Now()); !errors.Is(err, ErrBadResponse) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// 真实结构的 summary 样例。
const summaryJSON = `{"code":0,"msg":"","data":{"biz_code":0,"biz_msg":"","biz_data":{"normal_wallets":[{"currency":"CNY","balance":"12.6121173400000000","token_estimation":"0"}],"bonus_wallets":[{"currency":"CNY","balance":"3","token_estimation":"0"}],"total_costs":[{"currency":"CNY","amount":"17.5011996600000000"}]}}}`

// TestParseSummary 验证平台 summary：余额拆分与累计已用。
func TestParseSummary(t *testing.T) {
	b, err := ParseSummary([]byte(summaryJSON), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if b.Currency != "CNY" || b.ToppedUp < 12.61 || b.ToppedUp > 12.62 || b.Granted != 3 || b.Total < 15.61 || b.Total > 15.62 {
		t.Errorf("余额: %+v", b)
	}
	if !b.HasSpent || b.TotalSpent < 17.50 || b.TotalSpent > 17.51 || b.Source != "platform" || !b.Available {
		t.Errorf("累计: %+v", b)
	}
	// 过期业务码
	for _, body := range []string{`{"code":40003,"msg":"Authorization Failed","data":null}`, `{"code":0,"data":{"biz_code":40002,"biz_msg":"x"}}`} {
		if _, err := ParseSummary([]byte(body), time.Now()); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("过期: %v", err)
		}
	}
	if _, err := ParseSummary([]byte(`{"code":500,"msg":"boom"}`), time.Now()); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Errorf("其他错误: %v", err)
	}
	if _, err := ParseSummary([]byte(`{"code":0,"data":{"biz_code":0,"biz_data":{}}}`), time.Now()); !errors.Is(err, ErrBadResponse) {
		t.Errorf("无钱包: %v", err)
	}
}

// amountJSON / costJSON 为两个接口的响应样例（含全 0 模型行）。
const amountJSON = `{"code":0,"msg":"","data":{"biz_code":0,"biz_msg":"","biz_data":{"total":[],"days":[
{"date":"2026-10-13","data":[{"model":"deepseek-flash","usage":[{"type":"PROMPT_TOKEN","amount":"0"},{"type":"PROMPT_CACHE_HIT_TOKEN","amount":"4561408"},{"type":"PROMPT_CACHE_MISS_TOKEN","amount":"246848"},{"type":"RESPONSE_TOKEN","amount":"22826"},{"type":"REQUEST","amount":"68"}]},{"model":"deepseek-v4-pro","usage":[{"type":"REQUEST","amount":"0"}]}]},
{"date":"2026-10-14","data":[{"model":"deepseek-flash","usage":[{"type":"PROMPT_CACHE_MISS_TOKEN","amount":"103"},{"type":"RESPONSE_TOKEN","amount":"118"},{"type":"REQUEST","amount":"2"}]}]}]}}}`

const costJSON = `{"code":0,"msg":"","data":{"biz_code":0,"biz_msg":"","biz_data":[{"currency":"CNY","total":[],"days":[
{"date":"2026-10-13","data":[{"model":"deepseek-flash","usage":[{"type":"PROMPT_CACHE_HIT_TOKEN","amount":"0.2"},{"type":"PROMPT_CACHE_MISS_TOKEN","amount":"0.1"},{"type":"RESPONSE_TOKEN","amount":"0.129380160000"}]}]},
{"date":"2026-10-14","data":[{"model":"deepseek-flash","usage":[{"type":"RESPONSE_TOKEN","amount":"0.000575"}]}]}]},
{"currency":"USD","days":[{"date":"2026-10-14","data":[{"model":"deepseek-flash","usage":[{"type":"RESPONSE_TOKEN","amount":"999"}]}]}]}]}}`

// TestParseMonth 验证 Token 与费用合并、零行过滤、多币种只取主币种。
func TestParseMonth(t *testing.T) {
	rows, err := ParseMonth([]byte(amountJSON), []byte(costJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("应过滤掉全 0 的模型行，得到 %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Date != "2026-10-13" || r.CacheHit != 4561408 || r.CacheMiss != 246848 || r.Output != 22826 || r.Requests != 68 || r.Currency != "CNY" {
		t.Errorf("行0: %+v", r)
	}
	if r.Cost < 0.4293 || r.Cost > 0.4294 {
		t.Errorf("行0 费用: %v", r.Cost)
	}
	if rows[1].Cost != 0.000575 {
		t.Errorf("行1 费用不应混入 USD: %v", rows[1].Cost)
	}
	if rows[0].Tokens() != 4561408+246848+22826 {
		t.Error("Tokens")
	}
	if _, err := ParseMonth([]byte(`{"code":40003}`), []byte(costJSON)); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("amount 过期: %v", err)
	}
	if _, err := ParseMonth([]byte(amountJSON), []byte(`oops`)); !errors.Is(err, ErrBadResponse) {
		t.Errorf("cost 坏: %v", err)
	}
	// 只有费用没有用量，也应产生一行
	rows, _ = ParseMonth([]byte(`{"code":0,"data":{"biz_code":0,"biz_data":{"days":[]}}}`), []byte(costJSON))
	if len(rows) != 2 {
		t.Errorf("仅费用: %+v", rows)
	}
}

// fakePlatform 模拟 DeepSeek 平台与 API。
func fakePlatform(t *testing.T, status int, seen *http.Header) *httptest.Server {
	mux := http.NewServeMux()
	handler := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if seen != nil {
				*seen = r.Header.Clone()
			}
			if status != 200 {
				w.WriteHeader(status)
				return
			}
			fmt.Fprint(w, body)
		}
	}
	mux.HandleFunc("/user/balance", handler(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"8.80","granted_balance":"0","topped_up_balance":"8.80"}]}`))
	mux.HandleFunc("/api/v0/users/get_user_summary", handler(summaryJSON))
	mux.HandleFunc("/api/v0/usage/amount", handler(amountJSON))
	mux.HandleFunc("/api/v0/usage/cost", handler(costJSON))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// TestClientFetch 验证请求头（Bearer、浏览器 UA）与各接口。
func TestClientFetch(t *testing.T) {
	var hdr http.Header
	s := fakePlatform(t, 200, &hdr)
	c := &Client{APIBase: s.URL, PlatformBase: s.URL}
	ctx := context.Background()
	b, err := c.FetchBalanceByKey(ctx, " sk-key ")
	if err != nil || b.Total != 8.8 {
		t.Fatalf("%+v %v", b, err)
	}
	if hdr.Get("Authorization") != "Bearer sk-key" || !strings.Contains(hdr.Get("User-Agent"), "Safari") {
		t.Errorf("头: %v", hdr)
	}
	sum, err := c.FetchSummary(ctx, "tok")
	if err != nil || sum.TotalSpent < 17.5 {
		t.Fatalf("%+v %v", sum, err)
	}
	if hdr.Get("x-client-platform") != "web" || hdr.Get("Origin") != "https://platform.deepseek.com" {
		t.Errorf("平台头: %v", hdr)
	}
	rows, err := c.FetchMonth(ctx, "tok", 2026, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	if _, err := c.FetchBalanceByKey(ctx, ""); !errors.Is(err, ErrNoCredentials) {
		t.Error("空 key")
	}
	if _, err := c.FetchSummary(ctx, " "); !errors.Is(err, ErrNoCredentials) {
		t.Error("空 token")
	}
	if _, err := c.FetchMonth(ctx, "", 2026, 10); !errors.Is(err, ErrNoCredentials) {
		t.Error("空 token 月度")
	}
}

// TestClientErrors 验证状态码、WAF 拦截页与业务码错误映射。
func TestClientErrors(t *testing.T) {
	for status, want := range map[int]error{401: ErrUnauthorized, 403: ErrUnauthorized, 429: ErrRateLimited} {
		s := fakePlatform(t, status, nil)
		c := &Client{APIBase: s.URL, PlatformBase: s.URL}
		if _, err := c.FetchSummary(context.Background(), "t"); !errors.Is(err, want) {
			t.Errorf("status %d: %v", status, err)
		}
	}
	if _, err := (&Client{PlatformBase: fakePlatform(t, 500, nil).URL}).FetchSummary(context.Background(), "t"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500: %v", err)
	}
	waf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!DOCTYPE html><title>Error - Request Blocked</title>")
	}))
	defer waf.Close()
	if _, err := (&Client{PlatformBase: waf.URL}).FetchSummary(context.Background(), "t"); !errors.Is(err, ErrRateLimited) {
		t.Errorf("WAF 页: %v", err)
	}
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":40003,"msg":"Authorization Failed (invalid token)","data":null}`)
	}))
	defer expired.Close()
	if _, err := (&Client{PlatformBase: expired.URL}).FetchSummary(context.Background(), "t"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("业务码过期: %v", err)
	}
}

// writeLevelDB 用 goleveldb 构造 Chromium localStorage 夹具。
func writeLevelDB(t *testing.T, dir string, kv map[string][]byte) {
	t.Helper()
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range kv {
		if err := db.Put([]byte(k), v, nil); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
}

// utf16Value 构造 UTF-16LE 编码的 localStorage 值（首字节 0x00）。
func utf16Value(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := []byte{0x00}
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// TestParseTokenValue 验证多种值编码。
func TestParseTokenValue(t *testing.T) {
	cases := map[string][]byte{
		"tok-latin": append([]byte{0x01}, []byte(`{"value":"tok-latin","__version":"0"}`)...),
		"tok-raw":   append([]byte{0x01}, []byte(`tok-raw`)...),
		"tok-quote": append([]byte{0x01}, []byte(`"tok-quote"`)...),
		"tok-utf16": utf16Value(`{"value":"tok-utf16"}`),
	}
	for want, raw := range cases {
		if got := ParseTokenValue(raw); got != want {
			t.Errorf("want %s got %q", want, got)
		}
	}
	for _, bad := range [][]byte{nil, {0x01}, append([]byte{0x01}, []byte(`{"value":""}`)...), append([]byte{0x01}, []byte(`{bad`)...)} {
		if got := ParseTokenValue(bad); got != "" {
			t.Errorf("应为空: %q", got)
		}
	}
}

// TestReadTokenFromLevelDB 验证从 leveldb 夹具读取 token，并忽略别的站点和别的键。
func TestReadTokenFromLevelDB(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "leveldb")
	writeLevelDB(t, dir, map[string][]byte{
		"_https://platform.deepseek.com\x00\x01userToken": append([]byte{0x01}, []byte(`{"value":"REAL-TOKEN","__version":"0"}`)...),
		"_https://platform.deepseek.com\x00\x01other":     append([]byte{0x01}, []byte(`x`)...),
		"_https://example.com\x00\x01userToken":           append([]byte{0x01}, []byte(`{"value":"WRONG"}`)...),
		"META:https://platform.deepseek.com":              []byte("meta"),
	})
	tok, err := ReadTokenFromLevelDB(dir)
	if err != nil || tok != "REAL-TOKEN" {
		t.Fatalf("%q %v", tok, err)
	}
	// 没有 token
	dir2 := filepath.Join(t.TempDir(), "leveldb")
	writeLevelDB(t, dir2, map[string][]byte{"_https://example.com\x00\x01userToken": append([]byte{0x01}, []byte(`x`)...)})
	if tok, err := ReadTokenFromLevelDB(dir2); tok != "" || err != nil {
		t.Errorf("无 token: %q %v", tok, err)
	}
	if _, err := ReadTokenFromLevelDB(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("目录不存在应报错")
	}
}

// TestFindChromeTokens 验证扫描多个浏览器/Profile，且不修改原目录。
func TestFindChromeTokens(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, token string) string {
		dir := filepath.Join(root, filepath.FromSlash(rel), "Local Storage", "leveldb")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		kv := map[string][]byte{}
		if token != "" {
			kv["_https://platform.deepseek.com\x00\x01userToken"] = append([]byte{0x01}, []byte(`{"value":"`+token+`"}`)...)
		}
		writeLevelDB(t, dir, kv)
		return dir
	}
	d1 := mk("Google/Chrome/Default", "T1")
	mk("Google/Chrome/Profile 2", "T2")
	mk("Google/Chrome/Profile 3", "")
	mk("Microsoft Edge/Default", "T3")
	os.MkdirAll(filepath.Join(root, "Google/Chrome/Crashpad"), 0o755)
	before, _ := os.ReadDir(d1)
	toks, err := FindChromeTokens(root)
	if err != nil || len(toks) != 3 {
		t.Fatalf("%+v %v", toks, err)
	}
	got := map[string]string{}
	for _, tk := range toks {
		got[tk.Browser+"/"+tk.Profile] = tk.Token
	}
	if got["Chrome/Default"] != "T1" || got["Chrome/Profile 2"] != "T2" || got["Edge/Default"] != "T3" {
		t.Errorf("%v", got)
	}
	after, _ := os.ReadDir(d1)
	if len(before) != len(after) {
		t.Errorf("原目录被改动: %d -> %d", len(before), len(after))
	}
	// 没有任何浏览器目录
	if toks, err := FindChromeTokens(t.TempDir()); err != nil || len(toks) != 0 {
		t.Errorf("空: %v %v", toks, err)
	}
}

// TestNormalizeToken 验证各种粘贴形态都能整理成纯 token，Cookie 会被拒绝。
func TestNormalizeToken(t *testing.T) {
	tok := "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/abcdefgh=="
	ok := map[string]string{
		tok:                            tok,
		"  " + tok + "\n":              tok,
		"Bearer " + tok:                tok,
		"authorization: Bearer " + tok: tok,
		"-H 'authorization: Bearer " + tok + "' \\": tok,
		`{"value":"` + tok + `","__version":"0"}`:   tok,
		`"` + tok + `"`: tok,
		"curl 'https://x' -H 'accept: */*' -H 'authorization: Bearer " + tok + "' -b 'a=b'": tok,
	}
	for in, want := range ok {
		if got, err := NormalizeToken(in); err != nil || got != want {
			t.Errorf("%q → %q, %v", in, got, err)
		}
	}
	if got, err := NormalizeToken("  "); got != "" || err != nil {
		t.Errorf("空串: %q %v", got, err)
	}
	for _, bad := range []string{
		"deviceId=0123456789abcdef0123456789abcdef0123456789abcdef; SESSTIME=1700000000000",
		"deviceId=0123456789abcdef0123456789abcdef0123456789abcdef",
		"short1", "has space inside the token value 1234567890",
	} {
		if _, err := NormalizeToken(bad); !errors.Is(err, ErrBadToken) {
			t.Errorf("应拒绝 %q：%v", bad, err)
		}
	}
}
