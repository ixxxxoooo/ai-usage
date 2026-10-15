package cursor

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// makeJWT 构造一个未签名的测试 JWT。
func makeJWT(sub string, exp int64) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(map[string]any{"sub": sub, "exp": exp}) + ".sig"
}

// makeStateDB 创建一个模拟 Cursor state.vscdb 的 SQLite 文件。
func makeStateDB(t *testing.T, token string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	if token != "" {
		if _, err := db.Exec(`INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', ?)`, token); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// TestParseJWTAndUserID 验证 JWT 解析与用户 ID 提取。
func TestParseJWTAndUserID(t *testing.T) {
	tok := makeJWT("github|user_ABC", 1796743273)
	sub, exp, err := ParseJWT(tok)
	if err != nil || sub != "github|user_ABC" || exp != 1796743273 {
		t.Fatalf("ParseJWT: %v %v %v", sub, exp, err)
	}
	if UserIDFromSub(sub) != "user_ABC" || UserIDFromSub("plain") != "plain" {
		t.Error("UserIDFromSub")
	}
	for _, bad := range []string{"", "a.b", "a.!!!.c"} {
		if _, _, err := ParseJWT(bad); err == nil {
			t.Errorf("坏 JWT 应报错: %q", bad)
		}
	}
}

// TestCredentialsFromToken 验证过期判断与 Cookie 拼装。
func TestCredentialsFromToken(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Unix()
	c, err := CredentialsFromToken(makeJWT("github|user_X", future))
	if err != nil || !c.Valid() || c.UserID != "user_X" {
		t.Fatalf("%+v %v", c, err)
	}
	if !strings.HasPrefix(c.Cookie(), "WorkosCursorSessionToken=user_X%3A%3A") {
		t.Errorf("Cookie: %s", c.Cookie())
	}
	if _, err := CredentialsFromToken(makeJWT("github|user_X", time.Now().Add(-time.Hour).Unix())); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("过期应为 ErrUnauthorized: %v", err)
	}
	if _, err := CredentialsFromToken(makeJWT("", future)); err == nil {
		t.Error("空 sub 应报错")
	}
}

// TestReadLocalCredentials 用夹具验证从 state.vscdb 读取凭据。
func TestReadLocalCredentials(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Unix()
	tok := makeJWT("github|user_FIX", future)
	c, err := ReadLocalCredentials(makeStateDB(t, tok))
	if err != nil || c.UserID != "user_FIX" || c.Token != tok {
		t.Fatalf("%+v %v", c, err)
	}
	// 没有键 -> ErrNoCredentials
	if _, err := ReadLocalCredentials(makeStateDB(t, "")); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("无键: %v", err)
	}
	// 文件不存在 -> ErrNoCredentials
	if _, err := ReadLocalCredentials(filepath.Join(t.TempDir(), "none.vscdb")); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("无文件: %v", err)
	}
	// 过期 token
	if _, err := ReadLocalCredentials(makeStateDB(t, makeJWT("github|u", time.Now().Add(-time.Hour).Unix()))); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("过期: %v", err)
	}
}

// TestParseManualCookie 验证多种手动粘贴写法。
func TestParseManualCookie(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Unix()
	tok := makeJWT("github|user_M", future)
	inputs := []string{
		"WorkosCursorSessionToken=user_M%3A%3A" + tok,
		"Cookie: a=b; WorkosCursorSessionToken=user_M%3A%3A" + tok + "; c=d",
		"user_M%3A%3A" + tok,
		"user_M::" + tok,
		"\"user_M::" + tok + "\"",
		tok,
	}
	for _, in := range inputs {
		c, err := ParseManualCookie(in)
		if err != nil || c.UserID != "user_M" || c.Token != tok {
			t.Errorf("输入 %q: %+v %v", in[:20], c, err)
		}
	}
	for _, bad := range []string{"", "   ", "hello", "user::notajwt"} {
		if _, err := ParseManualCookie(bad); err == nil {
			t.Errorf("应报错: %q", bad)
		}
	}
}

// usageJSON 是真实接口的响应样例。
const usageJSON = `{"gpt-4":{"numRequests":359,"numRequestsTotal":359,"numTokens":0,"maxRequestUsage":500,"maxTokenUsage":null},"startOfMonth":"2026-09-16T02:24:49.000Z"}`

// TestParseUsage 验证用量解析与缺字段处理。
func TestParseUsage(t *testing.T) {
	u, err := ParseUsage([]byte(usageJSON), time.Now())
	if err != nil || u.Used != 359 || u.Limit != 500 || u.CycleStart.Year() != 2026 || u.CycleStart.Month() != 9 {
		t.Fatalf("%+v %v", u, err)
	}
	// 缺 maxRequestUsage：额度未知（0），不能臆测成 500；只有 numRequests
	u, err = ParseUsage([]byte(`{"gpt-4":{"numRequests":7}}`), time.Now())
	if err != nil || u.Used != 7 || u.Limit != 0 {
		t.Errorf("缺字段: %+v %v", u, err)
	}
	for _, bad := range []string{`{}`, `not json`, `{"gpt-4":null}`} {
		if _, err := ParseUsage([]byte(bad), time.Now()); !errors.Is(err, ErrBadResponse) {
			t.Errorf("%q 应为 ErrBadResponse: %v", bad, err)
		}
	}
}

// eventJSON 生成一条事件 JSON。
func eventJSON(ts int64, model string, reqs float64) string {
	return fmt.Sprintf(`{"timestamp":"%d","model":"%s","kind":"USAGE_EVENT_KIND_INCLUDED_IN_PRO","requestsCosts":%g,"usageBasedCosts":"-","isTokenBasedCall":false,"tokenUsage":{"inputTokens":42,"outputTokens":9554,"cacheWriteTokens":151862,"cacheReadTokens":2693901,"totalCents":74.46},"chargedCents":4,"conversationId":"conv-1"}`, ts, model, reqs)
}

// TestParseEventsPage 验证事件解析（字符串时间戳、缺 tokenUsage、重复键稳定）。
func TestParseEventsPage(t *testing.T) {
	body := `{"totalUsageEventsCount":2,"usageEventsDisplay":[` + eventJSON(1791991841155, "claude", 1) + `,{"timestamp":1791991590340,"model":"gpt","requestsCosts":"2.5"}]}`
	evs, total, err := ParseEventsPage([]byte(body))
	if err != nil || total != 2 || len(evs) != 2 {
		t.Fatalf("%v %v %v", evs, total, err)
	}
	if evs[0].Time.UnixMilli() != 1791991841155 || evs[0].InputTokens != 42 || evs[0].CacheRead != 2693901 || evs[0].Requests != 1 || evs[0].ChargedCents != 4 {
		t.Errorf("事件0: %+v", evs[0])
	}
	if evs[1].Requests != 2.5 || evs[1].InputTokens != 0 || evs[1].Time.UnixMilli() != 1791991590340 {
		t.Errorf("事件1: %+v", evs[1])
	}
	if evs[0].Key == "" || evs[0].Key == evs[1].Key || evs[0].Key != EventKey(evs[0]) {
		t.Error("Key 应稳定且互异")
	}
	// 空页：没有任何字段
	evs, total, err = ParseEventsPage([]byte(`{}`))
	if err != nil || len(evs) != 0 || total != -1 {
		t.Errorf("空页: %v %v %v", evs, total, err)
	}
	if _, _, err := ParseEventsPage([]byte(`[`)); !errors.Is(err, ErrBadResponse) {
		t.Error("坏 JSON")
	}
}

// fakeServer 构造模拟 Cursor 的服务，并记录请求。
type fakeServer struct {
	*httptest.Server
	usageHits, eventHits   int32
	lastCookie, lastOrigin string
	lastBody               map[string]any
}

// newFake 创建假服务；events 为全量事件（倒序），按 page/pageSize 切片返回。
func newFake(t *testing.T, events []string, status int) *fakeServer {
	f := &fakeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.usageHits, 1)
		f.lastCookie = r.Header.Get("Cookie")
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		fmt.Fprint(w, usageJSON)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"email":"a@b.c"}`) })
	mux.HandleFunc("/api/dashboard/get-filtered-usage-events", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.eventHits, 1)
		f.lastOrigin = r.Header.Get("Origin")
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.lastBody = body
		page, size := int(body["page"].(float64)), int(body["pageSize"].(float64))
		lo, hi := (page-1)*size, page*size
		if lo > len(events) {
			lo = len(events)
		}
		if hi > len(events) {
			hi = len(events)
		}
		fmt.Fprintf(w, `{"totalUsageEventsCount":%d,"usageEventsDisplay":[%s]}`, len(events), strings.Join(events[lo:hi], ","))
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// testCred 是测试凭据。
var testCred = Credentials{UserID: "user_T", Token: "a.b.c"}

// TestFetchUsage 验证请求头与解析。
func TestFetchUsage(t *testing.T) {
	f := newFake(t, nil, 200)
	c := &Client{BaseURL: f.URL}
	u, err := c.FetchUsage(context.Background(), testCred)
	if err != nil || u.Used != 359 {
		t.Fatalf("%+v %v", u, err)
	}
	if f.lastCookie != "WorkosCursorSessionToken=user_T%3A%3Aa.b.c" {
		t.Errorf("Cookie: %s", f.lastCookie)
	}
	if email, err := c.FetchEmail(context.Background(), testCred); err != nil || email != "a@b.c" {
		t.Errorf("email: %v %v", email, err)
	}
}

// TestFetchErrors 验证各类 HTTP 错误映射。
func TestFetchErrors(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{{401, ErrUnauthorized}, {403, ErrForbidden}, {429, ErrRateLimited}}
	for _, c := range cases {
		f := newFake(t, nil, c.status)
		cl := &Client{BaseURL: f.URL}
		if _, err := cl.FetchUsage(context.Background(), testCred); !errors.Is(err, c.want) {
			t.Errorf("status %d: %v", c.status, err)
		}
	}
	f := newFake(t, nil, 500)
	if _, err := (&Client{BaseURL: f.URL}).FetchUsage(context.Background(), testCred); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500: %v", err)
	}
	// 限流携带 Retry-After
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).FetchUsage(context.Background(), testCred)
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter: %v", err)
	}
	// HTML 拦截页
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>blocked</html>") }))
	defer html.Close()
	if _, err := (&Client{BaseURL: html.URL}).FetchUsage(context.Background(), testCred); !errors.Is(err, ErrBadResponse) {
		t.Errorf("HTML: %v", err)
	}
	// 畸形 JSON
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"gpt-4":`) }))
	defer bad.Close()
	if _, err := (&Client{BaseURL: bad.URL}).FetchUsage(context.Background(), testCred); !errors.Is(err, ErrBadResponse) {
		t.Errorf("坏 JSON: %v", err)
	}
}

// TestFetchEventsPagination 验证分页、去重、Origin 头与时间范围。
func TestFetchEventsPagination(t *testing.T) {
	var evs []string
	for i := 0; i < 25; i++ {
		evs = append(evs, eventJSON(int64(1791991841155-i*1000), "claude", 1))
	}
	evs = append(evs, evs[24]) // 页边界上的重复行
	f := newFake(t, evs, 200)
	c := &Client{BaseURL: f.URL, PageSize: 10, MaxPages: 10}
	var progress []int
	got, err := c.FetchEvents(context.Background(), testCred, time.UnixMilli(1000), time.Time{}, func(g, total int) { progress = append(progress, g) })
	if err != nil || len(got) != 25 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	if atomic.LoadInt32(&f.eventHits) != 3 {
		t.Errorf("应请求 3 页，实际 %d", f.eventHits)
	}
	if f.lastOrigin != "https://cursor.com" {
		t.Errorf("Origin: %s", f.lastOrigin)
	}
	if f.lastBody["startDate"] != "1000" {
		t.Errorf("startDate: %v", f.lastBody["startDate"])
	}
	if len(progress) != 3 {
		t.Errorf("进度回调: %v", progress)
	}
	// 刚好满页再来一页空页
	exact := evs[:20]
	f2 := newFake(t, exact, 200)
	got, err = (&Client{BaseURL: f2.URL, PageSize: 10}).FetchEvents(context.Background(), testCred, time.Time{}, time.Time{}, nil)
	if err != nil || len(got) != 20 {
		t.Errorf("满页: %d %v", len(got), err)
	}
	// 超过最大页数被截断并报错，但仍返回已取到的数据
	f3 := newFake(t, evs[:25], 200)
	got, err = (&Client{BaseURL: f3.URL, PageSize: 5, MaxPages: 2}).FetchEvents(context.Background(), testCred, time.Time{}, time.Time{}, nil)
	if err == nil || len(got) != 10 {
		t.Errorf("截断: %d %v", len(got), err)
	}
	// 401 在分页中途
	f4 := newFake(t, nil, 401)
	if _, err := (&Client{BaseURL: f4.URL}).FetchEvents(context.Background(), testCred, time.Time{}, time.Time{}, nil); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401: %v", err)
	}
}

// TestFetchContextCancel 验证超时/取消会返回错误而不是卡死。
func TestFetchContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (&Client{BaseURL: srv.URL}).FetchUsage(ctx, testCred); err == nil || time.Since(start) > time.Second {
		t.Errorf("应快速超时: %v %v", err, time.Since(start))
	}
}

// summaryJSON 是 /api/usage-summary 的真实响应样例（个人 Pro，按美元额度，单位美分）。
const summaryJSON = `{"billingCycleStart":"2026-09-16T02:24:49.000Z","billingCycleEnd":"2026-10-16T02:24:49.000Z","membershipType":"pro","limitType":"user","isUnlimited":false,"individualUsage":{"plan":{"enabled":true,"used":1672,"limit":2000,"remaining":328},"onDemand":{"enabled":false,"used":0,"limit":null,"remaining":null}}}`

// TestParseSummary 验证新计划/团队的美元额度解析，以及精确重置时刻。
func TestParseSummary(t *testing.T) {
	u, err := ParseSummary([]byte(summaryJSON), time.Now())
	if err != nil || !u.IsUsage() || u.Used != 1672 || u.Limit != 2000 || u.Membership != "pro" || u.LimitType != "user" {
		t.Fatalf("%+v %v", u, err)
	}
	if want := time.Date(2026, 10, 16, 2, 24, 49, 0, time.UTC); !u.CycleEnd().Equal(want) {
		t.Errorf("重置时刻应取接口给出的精确值: %v", u.CycleEnd())
	}
	// 团队无上限：Limit 为空 + isUnlimited
	u, err = ParseSummary([]byte(`{"membershipType":"enterprise","limitType":"team","isUnlimited":true,"individualUsage":{"plan":{"used":530.4,"limit":null},"onDemand":{"used":120}}}`), time.Now())
	if err != nil || !u.Unlimited || u.HasCap() || u.Used != 530 || u.OnDemand != 120 || u.LimitType != "team" {
		t.Errorf("团队无上限: %+v %v", u, err)
	}
	for _, bad := range []string{`{}`, `not json`} {
		if _, err := ParseSummary([]byte(bad), time.Now()); !errors.Is(err, ErrBadResponse) {
			t.Errorf("%q 应为 ErrBadResponse: %v", bad, err)
		}
	}
}

// TestFetchUsageModes 验证 FetchUsage 自动识别口径：老计划按次数，新计划按美元，两者都带精确重置时刻。
func TestFetchUsageModes(t *testing.T) {
	serve := func(legacy, summary string) *Client {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
			if legacy == "" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, legacy)
		})
		mux.HandleFunc("/api/usage-summary", func(w http.ResponseWriter, r *http.Request) {
			if summary == "" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, summary)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return &Client{BaseURL: srv.URL}
	}
	cred := Credentials{UserID: "user_A", Token: "a.b.c"}
	ctx := context.Background()

	// 1) 老计划 + summary：按次数，重置时刻来自 summary，记录账号
	u, err := serve(usageJSON, summaryJSON).FetchUsage(ctx, cred)
	if err != nil || u.IsUsage() || u.Used != 359 || u.Limit != 500 || u.Account != "user_A" || u.Membership != "pro" {
		t.Fatalf("老计划: %+v %v", u, err)
	}
	if u.CycleEndAt.IsZero() {
		t.Error("老计划也应带精确重置时刻")
	}
	// 2) 老计划，summary 取不到：仍可用，重置时刻回退为起点+1 月
	u, err = serve(usageJSON, "").FetchUsage(ctx, cred)
	if err != nil || u.Used != 359 || !u.CycleEnd().Equal(u.CycleStart.AddDate(0, 1, 0)) {
		t.Fatalf("summary 缺失: %+v %v", u, err)
	}
	// 3) 新计划：/api/usage 没有次数上限 -> 用美元额度
	u, err = serve(`{"gpt-4":{"numRequests":0,"maxRequestUsage":null}}`, summaryJSON).FetchUsage(ctx, cred)
	if err != nil || !u.IsUsage() || u.Used != 1672 || u.Limit != 2000 {
		t.Fatalf("新计划: %+v %v", u, err)
	}
	// 4) 两个接口都没有额度信息 -> 明确报错
	if _, err := serve(`{"gpt-4":{"numRequests":3}}`, "").FetchUsage(ctx, cred); !errors.Is(err, ErrBadResponse) {
		t.Errorf("无额度信息应报错: %v", err)
	}
}
