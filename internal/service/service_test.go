package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/provider/antigravity"
	"github.com/ixxxxoooo/ai-usage/internal/provider/cursor"
	"github.com/ixxxxoooo/ai-usage/internal/provider/deepseek"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/store"
)

// init 让测试默认认为本机没有运行 Antigravity，避免触碰开发机上真实运行的进程。
func init() {
	newAntigravityClient = func() *antigravity.Client {
		return &antigravity.Client{Procs: func(context.Context) ([]antigravity.Process, error) { return nil, nil }}
	}
}

// 固定“当前时间”：与真实数据一致的 2026-10-14。
var fixedNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.Local)

// cursorFake 模拟 Cursor 服务，可切换状态码。
type cursorFake struct {
	*httptest.Server
	mu        sync.Mutex
	status    int
	used      int
	eventBody []map[string]any // 记录每次明细请求体
	events    []string
	usageHits int32
	summary   string // 非空时 /api/usage-summary 返回它，且 /api/usage 不再给次数上限（模拟新计划）
}

// newCursorFake 创建假服务；默认返回 359/500。
func newCursorFake(t *testing.T) *cursorFake {
	f := &cursorFake{status: 200, used: 359}
	for i := 0; i < 3; i++ {
		f.events = append(f.events, fmt.Sprintf(`{"timestamp":"%d","model":"claude","kind":"USAGE_EVENT_KIND_INCLUDED_IN_PRO","requestsCosts":1,"tokenUsage":{"inputTokens":%d}}`, fixedNow.Add(-time.Duration(i)*time.Hour).UnixMilli(), i+1))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usage", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.usageHits, 1)
		f.mu.Lock()
		st, used := f.status, f.used
		f.mu.Unlock()
		if st != 200 {
			w.WriteHeader(st)
			return
		}
		f.mu.Lock()
		newPlan := f.summary != ""
		f.mu.Unlock()
		if newPlan {
			fmt.Fprint(w, `{"gpt-4":{"numRequests":0,"maxRequestUsage":null},"startOfMonth":"2026-09-16T02:24:49.000Z"}`)
			return
		}
		fmt.Fprintf(w, `{"gpt-4":{"numRequests":%d,"numRequestsTotal":%d,"maxRequestUsage":500},"startOfMonth":"2026-09-16T02:24:49.000Z"}`, used, used)
	})
	mux.HandleFunc("/api/usage-summary", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		sum := f.summary
		f.mu.Unlock()
		if sum == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, sum)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"email":"me@x.com"}`) })
	mux.HandleFunc("/api/dashboard/get-filtered-usage-events", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.eventBody = append(f.eventBody, body)
		st := f.status
		f.mu.Unlock()
		if st != 200 {
			w.WriteHeader(st)
			return
		}
		page := int(body["page"].(float64))
		if page > 1 {
			fmt.Fprint(w, `{"totalUsageEventsCount":3,"usageEventsDisplay":[]}`)
			return
		}
		// 按 startDate/endDate（毫秒）过滤，模拟真实接口。
		from, _ := strconv.ParseInt(fmt.Sprint(body["startDate"]), 10, 64)
		to, _ := strconv.ParseInt(fmt.Sprint(body["endDate"]), 10, 64)
		var picked []string
		for _, ev := range f.events {
			var one struct {
				Timestamp string `json:"timestamp"`
			}
			_ = json.Unmarshal([]byte(ev), &one)
			ts, _ := strconv.ParseInt(one.Timestamp, 10, 64)
			if ts >= from && ts <= to {
				picked = append(picked, ev)
			}
		}
		fmt.Fprintf(w, `{"totalUsageEventsCount":%d,"usageEventsDisplay":[%s]}`, len(picked), strings.Join(picked, ","))
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// setStatus 切换假服务状态码。
func (f *cursorFake) setStatus(s int) { f.mu.Lock(); f.status = s; f.mu.Unlock() }

// dsFake 模拟 DeepSeek 服务。
type dsFake struct {
	*httptest.Server
	mu        sync.Mutex
	status    int
	tokens    []string // 记录用过的 Bearer
	validTok  string   // 只有该 token 有效，空表示都有效
	balance   string   // 余额(summary)
	monthHits int32
}

// newDSFake 创建假服务。
func newDSFake(t *testing.T) *dsFake {
	f := &dsFake{status: 200, balance: "12.6"}
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		f.tokens = append(f.tokens, tok)
		st, valid := f.status, f.validTok
		f.mu.Unlock()
		if st != 200 {
			w.WriteHeader(st)
			return false
		}
		if valid != "" && tok != valid {
			fmt.Fprint(w, `{"code":40003,"msg":"Authorization Failed (invalid token)","data":null}`)
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/user/balance", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			fmt.Fprint(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"3.00","granted_balance":"0","topped_up_balance":"3.00"}]}`)
		}
	})
	mux.HandleFunc("/api/v0/users/get_user_summary", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			f.mu.Lock()
			bal := f.balance
			f.mu.Unlock()
			fmt.Fprintf(w, `{"code":0,"data":{"biz_code":0,"biz_data":{"normal_wallets":[{"currency":"CNY","balance":"%s"}],"bonus_wallets":[],"total_costs":[{"currency":"CNY","amount":"17.5"}]}}}`, bal)
		}
	})
	mux.HandleFunc("/api/v0/usage/amount", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			atomic.AddInt32(&f.monthHits, 1)
			m := r.URL.Query().Get("month")
			fmt.Fprintf(w, `{"code":0,"data":{"biz_code":0,"biz_data":{"days":[{"date":"2026-%s-14","data":[{"model":"deepseek-flash","usage":[{"type":"PROMPT_CACHE_MISS_TOKEN","amount":"1000"},{"type":"RESPONSE_TOKEN","amount":"234"},{"type":"REQUEST","amount":"5"}]}]}]}}}`, fmt.Sprintf("%02s", m))
		}
	})
	mux.HandleFunc("/api/v0/usage/cost", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			m := r.URL.Query().Get("month")
			fmt.Fprintf(w, `{"code":0,"data":{"biz_code":0,"biz_data":[{"currency":"CNY","days":[{"date":"2026-%s-14","data":[{"model":"deepseek-flash","usage":[{"type":"RESPONSE_TOKEN","amount":"0.25"}]}]}]}]}}`, fmt.Sprintf("%02s", m))
		}
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// harness 汇集一次测试的全部依赖。
type harness struct {
	svc         *Service
	cur         *cursorFake
	ds          *dsFake
	secrets     *secret.Memory
	st          *store.Store
	notices     []string
	changes     int32
	chromeCalls int32
	chrome      []deepseek.ChromeToken
	mu          sync.Mutex
}

// newHarness 创建完整的被测服务。
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{cur: newCursorFake(t), ds: newDSFake(t), secrets: secret.NewMemory()}
	var err error
	h.st, err = store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.st.Close() })
	h.chrome = []deepseek.ChromeToken{{Browser: "Chrome", Profile: "Default", Token: "tok-chrome"}}
	h.svc = h.build(t)
	return h
}

// build 用当前依赖创建 Service（可重复调用以模拟重启）。
func (h *harness) build(t *testing.T) *Service {
	svc, err := New(Config{
		Store: h.st, Secrets: h.secrets,
		Cursor: &cursor.Client{BaseURL: h.cur.URL},
		DS:     &deepseek.Client{APIBase: h.ds.URL, PlatformBase: h.ds.URL},
		CursorLocalCreds: func() (cursor.Credentials, error) {
			return cursor.Credentials{UserID: "user_T", Token: "a.b.c"}, nil
		},
		ChromeTokens: func() ([]deepseek.ChromeToken, error) {
			atomic.AddInt32(&h.chromeCalls, 1)
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.chrome, nil
		},
		Notify: func(id, title, body string) {
			h.mu.Lock()
			h.notices = append(h.notices, id)
			h.mu.Unlock()
		},
		OnChange: func() { atomic.AddInt32(&h.changes, 1) },
		Now:      func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// noticeList 返回已发送通知的拷贝。
func (h *harness) noticeList() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.notices...)
}

// TestRefreshSuccess 验证两个数据源都成功刷新并持久化。
func TestRefreshSuccess(t *testing.T) {
	h := newHarness(t)
	h.svc.RefreshAll(context.Background())
	st := h.svc.State()
	if st.Cursor.Status != StatusOK || !st.Cursor.HasUsage || st.Cursor.Usage.Used != 359 || st.Cursor.Usage.Email != "me@x.com" {
		t.Fatalf("cursor: %+v", st.Cursor)
	}
	if st.Cursor.EventCount != 3 || st.Cursor.CredSource != "本机 Cursor" {
		t.Errorf("事件: %+v", st.Cursor)
	}
	d := st.DeepSeek
	if d.Status != StatusOK || !d.HasBalance || d.Balance.Total != 12.6 || !d.Balance.HasSpent || d.Balance.TotalSpent != 17.5 {
		t.Fatalf("deepseek: %+v", d)
	}
	if !d.HasBill || d.Today.Tokens != 1234 || d.Today.Requests != 5 || d.Today.Cost != 0.25 || d.MonthCost != 0.25 || d.Month != "2026-10" {
		t.Errorf("账单指标: %+v", d)
	}
	if d.TokenSrc != "Chrome / Default" || d.HasAPIKey {
		t.Errorf("来源: %+v", d)
	}
	if st.Refreshing {
		t.Error("刷新结束后 Refreshing 应为 false")
	}
	if atomic.LoadInt32(&h.changes) == 0 {
		t.Error("应触发 OnChange")
	}
	// 持久化：新 Service 在同一库上启动即有数据（重启场景）
	again := h.build(t).State()
	if !again.Cursor.HasUsage || again.Cursor.Usage.Used != 359 || again.Cursor.Status != StatusIdle || !again.DeepSeek.HasBalance || again.DeepSeek.Today.Tokens != 1234 {
		t.Errorf("重启恢复: %+v / %+v", again.Cursor, again.DeepSeek)
	}
}

// TestIncrementalEvents 验证二次刷新只请求增量区间。
func TestIncrementalEvents(t *testing.T) {
	h := newHarness(t)
	h.svc.RefreshAll(context.Background())
	if len(h.cur.eventBody) != 2 || h.cur.eventBody[0]["startDate"] == "0" || h.cur.eventBody[1]["startDate"] != "0" {
		t.Errorf("首次应先拉本周期、再补全部更早历史: %v", h.cur.eventBody)
	}
	h.svc.RefreshAll(context.Background())
	last := h.cur.eventBody[len(h.cur.eventBody)-1]
	if last["startDate"] == "0" || len(h.cur.eventBody) != 3 {
		t.Error("二次刷新应为单次增量请求")
	}
	if c := h.svc.State().Cursor.EventCount; c != 3 {
		t.Errorf("重复拉取不应重复入库: %d", c)
	}
}

// TestCursorFailuresKeepOldData 验证失败时保留旧数据并给出准确状态。
func TestCursorFailuresKeepOldData(t *testing.T) {
	h := newHarness(t)
	h.svc.RefreshAll(context.Background())
	cases := []struct {
		status int
		want   Status
	}{{401, StatusUnauthorized}, {429, StatusRateLimited}, {500, StatusError}}
	for _, c := range cases {
		h.cur.setStatus(c.status)
		h.svc.RefreshAll(context.Background())
		cs := h.svc.State().Cursor
		if cs.Status != c.want || cs.Message == "" {
			t.Errorf("status %d: %+v", c.status, cs.ProviderState)
		}
		if !cs.HasUsage || cs.Usage.Used != 359 || !cs.Stale() {
			t.Errorf("status %d: 旧数据应保留且标记过期: %+v", c.status, cs)
		}
	}
	h.cur.setStatus(200)
	h.svc.RefreshAll(context.Background())
	if cs := h.svc.State().Cursor; cs.Status != StatusOK || cs.Failures != 0 || cs.Message != "" {
		t.Errorf("恢复后: %+v", cs.ProviderState)
	}
}

// TestOfflineAndBackoff 验证离线状态与指数退避。
func TestOfflineAndBackoff(t *testing.T) {
	h := newHarness(t)
	h.cur.Close() // 服务关闭 => 连接被拒绝
	var prev time.Duration
	for i := 1; i <= 4; i++ {
		h.svc.RefreshAll(context.Background())
		cs := h.svc.State().Cursor
		if cs.Status != StatusOffline || cs.Failures != i {
			t.Fatalf("第 %d 次: %+v", i, cs.ProviderState)
		}
		gap := cs.NextTry.Sub(cs.LastTry)
		if i > 1 && gap <= prev {
			t.Errorf("退避应递增: %v -> %v", prev, gap)
		}
		prev = gap
	}
	if prev > 30*time.Minute {
		t.Errorf("退避上限 30 分钟: %v", prev)
	}
}

// TestBackoffFunc 直接验证退避函数。
func TestBackoffFunc(t *testing.T) {
	b := 5 * time.Minute
	if backoff(b, 0, 0) != 5*time.Minute || backoff(b, 1, 0) != 10*time.Minute || backoff(b, 2, 0) != 20*time.Minute {
		t.Error("倍增")
	}
	if backoff(b, 10, 0) != 30*time.Minute {
		t.Error("上限")
	}
	if backoff(b, 0, 40*time.Minute) != 40*time.Minute {
		t.Error("Retry-After 优先")
	}
}

// TestNoCredentials 验证没有任何凭据的状态。
func TestNoCredentials(t *testing.T) {
	h := newHarness(t)
	h.chrome = nil
	svc, _ := New(Config{Store: h.st, Secrets: h.secrets, Cursor: &cursor.Client{BaseURL: h.cur.URL}, DS: &deepseek.Client{PlatformBase: h.ds.URL},
		CursorLocalCreds: func() (cursor.Credentials, error) { return cursor.Credentials{}, cursor.ErrNoCredentials },
		ChromeTokens:     func() ([]deepseek.ChromeToken, error) { return nil, nil }, Now: func() time.Time { return fixedNow }})
	svc.RefreshAll(context.Background())
	st := svc.State()
	if st.Cursor.Status != StatusNoCreds || st.DeepSeek.Status != StatusNoCreds {
		t.Errorf("%v %v", st.Cursor.Status, st.DeepSeek.Status)
	}
	if st.DeepSeek.TokenSrc != "未配置" {
		t.Errorf("TokenSrc=%s", st.DeepSeek.TokenSrc)
	}
}

// TestManualModes 验证手动凭据模式与自动失败时的手动兜底。
func TestManualModes(t *testing.T) {
	h := newHarness(t)
	tok := "u.e.s"
	_ = tok
	// 手动模式但没保存 -> 无凭据
	s := h.svc.Settings()
	s.CursorAuthMode, s.DeepSeekAuthMode = model.AuthManual, model.AuthManual
	h.svc.UpdateSettings(s)
	h.svc.RefreshAll(context.Background())
	if st := h.svc.State(); st.Cursor.Status != StatusNoCreds || st.DeepSeek.Status != StatusNoCreds {
		t.Errorf("手动未保存: %v %v", st.Cursor.Status, st.DeepSeek.Status)
	}
	// 手动 Cookie 无效
	h.svc.SaveSecret(secret.KeyCursorCookie, "garbage")
	h.svc.RefreshAll(context.Background())
	if st := h.svc.State(); st.Cursor.Status != StatusError || !strings.Contains(st.Cursor.Message, "手动 Cookie 无效") {
		t.Errorf("无效 Cookie: %+v", st.Cursor.ProviderState)
	}
	// 合法手动凭据
	jwt := fakeJWT()
	h.svc.SaveSecret(secret.KeyCursorCookie, "WorkosCursorSessionToken=user_M%3A%3A"+jwt)
	h.svc.SaveSecret(secret.KeyDeepSeekToken, "tok-manual")
	h.svc.RefreshAll(context.Background())
	st := h.svc.State()
	if st.Cursor.Status != StatusOK || st.Cursor.CredSource != "手动 Cookie" {
		t.Errorf("手动 Cursor: %+v", st.Cursor)
	}
	if st.DeepSeek.Status != StatusOK || st.DeepSeek.TokenSrc != "手动粘贴" {
		t.Errorf("手动 DeepSeek: %+v", st.DeepSeek)
	}
	if atomic.LoadInt32(&h.chromeCalls) != 0 {
		t.Error("手动模式不应扫描浏览器")
	}
	// 自动模式失败 -> 回退手动
	s.CursorAuthMode, s.DeepSeekAuthMode = model.AuthAuto, model.AuthAuto
	h.svc.UpdateSettings(s)
	svc2, _ := New(Config{Store: h.st, Secrets: h.secrets, Cursor: &cursor.Client{BaseURL: h.cur.URL}, DS: &deepseek.Client{PlatformBase: h.ds.URL, APIBase: h.ds.URL},
		CursorLocalCreds: func() (cursor.Credentials, error) { return cursor.Credentials{}, cursor.ErrNoCredentials },
		ChromeTokens:     func() ([]deepseek.ChromeToken, error) { return nil, nil }, Now: func() time.Time { return fixedNow }})
	svc2.RefreshAll(context.Background())
	if st := svc2.State(); st.Cursor.Status != StatusOK || st.Cursor.CredSource != "手动 Cookie" || st.DeepSeek.TokenSrc != "手动粘贴" {
		t.Errorf("回退: %+v %+v", st.Cursor, st.DeepSeek)
	}
}

// fakeJWT 生成可被 ParseManualCookie 接受的假 JWT（三段式）。
func fakeJWT() string { return "aaa.bbb.ccc" }

// TestDeepSeekAPIKeyOnly 验证只有 API Key 时只显示余额，不拉账单。
func TestDeepSeekAPIKeyOnly(t *testing.T) {
	h := newHarness(t)
	h.chrome = nil
	h.svc.SaveSecret(secret.KeyDeepSeekKey, "sk-test")
	h.svc.RefreshAll(context.Background())
	d := h.svc.State().DeepSeek
	if d.Status != StatusOK || d.Balance.Source != "apikey" || d.Balance.Total != 3 || !d.HasAPIKey {
		t.Fatalf("%+v", d)
	}
	if d.HasBill || d.Balance.HasSpent || d.TokenSrc != "未配置" {
		t.Errorf("不应有账单: %+v", d)
	}
	if atomic.LoadInt32(&h.ds.monthHits) != 0 {
		t.Error("没有 token 不应请求账单")
	}
}

// TestDeepSeekKeyPlusToken 验证 API Key 余额 + token 的累计已用合并。
func TestDeepSeekKeyPlusToken(t *testing.T) {
	h := newHarness(t)
	h.svc.SaveSecret(secret.KeyDeepSeekKey, "sk-test")
	h.svc.RefreshAll(context.Background())
	d := h.svc.State().DeepSeek
	if d.Balance.Source != "apikey" || d.Balance.Total != 3 || !d.Balance.HasSpent || d.Balance.TotalSpent != 17.5 || !d.HasBill {
		t.Errorf("%+v", d)
	}
}

// TestChromeTokenCacheAndRetry 验证 token 缓存，以及过期后重扫并重试。
func TestChromeTokenCacheAndRetry(t *testing.T) {
	h := newHarness(t)
	h.svc.RefreshAll(context.Background())
	h.svc.RefreshAll(context.Background())
	if n := atomic.LoadInt32(&h.chromeCalls); n != 1 {
		t.Errorf("token 应缓存，扫描 %d 次", n)
	}
	// 浏览器里换了新 token，旧 token 失效
	h.mu.Lock()
	h.chrome = []deepseek.ChromeToken{{Browser: "Chrome", Profile: "Default", Token: "tok-new"}}
	h.mu.Unlock()
	h.ds.mu.Lock()
	h.ds.validTok = "tok-new"
	h.ds.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	d := h.svc.State().DeepSeek
	if d.Status != StatusOK || !d.Balance.HasSpent {
		t.Fatalf("应重扫后成功: %+v", d.ProviderState)
	}
	if n := atomic.LoadInt32(&h.chromeCalls); n != 2 {
		t.Errorf("应重扫 1 次，累计 %d", n)
	}
	// 再次失效且浏览器没有新 token -> 登录过期
	h.ds.mu.Lock()
	h.ds.validTok = "tok-other"
	h.ds.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	if d := h.svc.State().DeepSeek; d.Status != StatusUnauthorized || !d.HasBalance {
		t.Errorf("过期: %+v", d.ProviderState)
	}
}

// TestHistoryBackfillOnce 验证历史月份只回填一次。
func TestHistoryBackfillOnce(t *testing.T) {
	h := newHarness(t)
	s := h.svc.Settings()
	s.HistoryDays = 60 // 60/30+1 = 3 个月
	h.svc.UpdateSettings(s)
	h.svc.RefreshAll(context.Background())
	if n := atomic.LoadInt32(&h.ds.monthHits); n != 3 {
		t.Fatalf("首次应拉 3 个月，实际 %d", n)
	}
	h.svc.RefreshAll(context.Background())
	if n := atomic.LoadInt32(&h.ds.monthHits); n != 4 {
		t.Errorf("二次只应再拉本月，累计 %d", n)
	}
	rows, _ := h.st.DeepSeekDaily("", "")
	if len(rows) != 3 {
		t.Errorf("应有 3 个月的行: %d", len(rows))
	}
}

// TestCursorThresholdNotifications 验证阈值通知每个周期只发一次。
func TestCursorThresholdNotifications(t *testing.T) {
	h := newHarness(t)
	s := h.svc.Settings()
	s.NotifyCursorPercent = []int{50, 70, 90}
	h.svc.UpdateSettings(s)
	h.svc.RefreshAll(context.Background()) // 71.8% => 50、70
	n := h.noticeList()
	cursorN := 0
	for _, id := range n {
		if strings.HasPrefix(id, "cursor:") {
			cursorN++
		}
	}
	if cursorN != 2 {
		t.Fatalf("应通知 50/70 两次: %v", n)
	}
	h.svc.RefreshAll(context.Background())
	if len(h.noticeList()) != len(n) {
		t.Error("重复刷新不应重复通知")
	}
	h.cur.mu.Lock()
	h.cur.used = 460
	h.cur.mu.Unlock()
	h.svc.RefreshAll(context.Background()) // 92% => 90
	found90 := false
	for _, id := range h.noticeList() {
		if strings.Contains(id, ":90:") {
			found90 = true
		}
	}
	if !found90 {
		t.Errorf("应补发 90%% 通知: %v", h.noticeList())
	}
	// 关闭通知
	s.NotifyEnabled = false
	h.svc.UpdateSettings(s)
	before := len(h.noticeList())
	h.cur.mu.Lock()
	h.cur.used = 500
	h.cur.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	if len(h.noticeList()) != before {
		t.Error("关闭通知后不应再发")
	}
}

// TestDeepSeekLowBalanceNotification 验证余额不足通知每天一次。
func TestDeepSeekLowBalanceNotification(t *testing.T) {
	h := newHarness(t)
	h.ds.mu.Lock()
	h.ds.balance = "2.5" // 低于默认 5
	h.ds.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	h.svc.RefreshAll(context.Background())
	cnt := 0
	for _, id := range h.noticeList() {
		if strings.HasPrefix(id, "deepseek:low:") {
			cnt++
		}
	}
	if cnt != 1 {
		t.Errorf("应只通知 1 次: %v", h.noticeList())
	}
}

// TestRefreshManualThrottle 验证手动刷新 3 秒内合并。
func TestRefreshManualThrottle(t *testing.T) {
	h := newHarness(t)
	if !h.svc.RefreshManual(context.Background()) {
		t.Fatal("第一次应执行")
	}
	if h.svc.RefreshManual(context.Background()) {
		t.Error("3 秒内第二次应被合并")
	}
	if hits := atomic.LoadInt32(&h.cur.usageHits); hits != 1 {
		t.Errorf("应只请求 1 次: %d", hits)
	}
}

// TestConcurrentRefreshSerialized 验证并发调用不会产生并行刷新。
func TestConcurrentRefreshSerialized(t *testing.T) {
	h := newHarness(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.svc.RefreshAll(context.Background()) }()
	}
	wg.Wait()
	if hits := atomic.LoadInt32(&h.cur.usageHits); hits < 1 || hits > 8 {
		t.Errorf("hits=%d", hits)
	}
	if st := h.svc.State(); st.Refreshing || st.Cursor.Status != StatusOK {
		t.Errorf("%+v", st.Cursor.ProviderState)
	}
}

// TestSettingsUpdate 验证设置保存、规范化与持久化。
func TestSettingsUpdate(t *testing.T) {
	h := newHarness(t)
	s := h.svc.Settings()
	s.RefreshMinutes, s.TrayDisplay = 0, "weird"
	if err := h.svc.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	got := h.svc.Settings()
	if got.RefreshMinutes != 1 || got.TrayDisplay != model.TrayCursor {
		t.Errorf("应规范化: %+v", got)
	}
	s.RefreshMinutes = 15
	h.svc.UpdateSettings(s)
	if db, _ := h.st.GetSettings(); db.RefreshMinutes != 15 {
		t.Error("应已持久化")
	}
}

// TestSchedulerStartAndKick 验证后台循环：启动即刷新，Kick 触发再次刷新，Stop 后退出。
func TestSchedulerStartAndKick(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.svc.Start(ctx)
	waitFor(t, func() bool { return atomic.LoadInt32(&h.cur.usageHits) >= 1 && h.svc.State().Cursor.Status == StatusOK }, "启动刷新")
	h.svc.Kick()
	h.svc.Kick() // 合并
	waitFor(t, func() bool { return atomic.LoadInt32(&h.cur.usageHits) >= 2 }, "Kick 刷新")
	h.svc.Stop()
	time.Sleep(50 * time.Millisecond)
	n := atomic.LoadInt32(&h.cur.usageHits)
	h.svc.Kick()
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&h.cur.usageHits) != n {
		t.Error("Stop 后不应再刷新")
	}
}

// TestNextWait 验证下次等待时长取退避计划与间隔的较小者，且不低于 5 秒。
func TestNextWait(t *testing.T) {
	h := newHarness(t)
	if w := h.svc.nextWait(); w != time.Minute {
		t.Errorf("默认: %v", w)
	}
	h.svc.update(func(x *State) { x.Cursor.NextTry = fixedNow.Add(30 * time.Second) })
	if w := h.svc.nextWait(); w != 30*time.Second {
		t.Errorf("取较早: %v", w)
	}
	h.svc.update(func(x *State) { x.Cursor.NextTry = fixedNow.Add(-time.Minute) })
	if w := h.svc.nextWait(); w != 5*time.Second {
		t.Errorf("下限: %v", w)
	}
}

// TestTestConnections 验证“测试连接”。
func TestTestConnections(t *testing.T) {
	h := newHarness(t)
	msg, err := h.svc.TestCursor(context.Background())
	if err != nil || !strings.Contains(msg, "359 次 / 500 次") {
		t.Errorf("%q %v", msg, err)
	}
	msg, err = h.svc.TestDeepSeek(context.Background())
	if err != nil || !strings.Contains(msg, "网页 token 有效") {
		t.Errorf("%q %v", msg, err)
	}
	h.cur.setStatus(401)
	if _, err := h.svc.TestCursor(context.Background()); err == nil {
		t.Error("401 应报错")
	}
	// 只有 API Key
	h.mu.Lock()
	h.chrome = nil
	h.mu.Unlock()
	h.svc.invalidateToken()
	h.svc.SaveSecret(secret.KeyDeepSeekKey, "sk")
	msg, err = h.svc.TestDeepSeek(context.Background())
	if err != nil || !strings.Contains(msg, "API Key 有效") {
		t.Errorf("%q %v", msg, err)
	}
	h.svc.SaveSecret(secret.KeyDeepSeekKey, "")
	if _, err := h.svc.TestDeepSeek(context.Background()); err == nil {
		t.Error("无凭据应报错")
	}
}

// TestReloadFromStore 验证导入数据后重新载入状态。
func TestReloadFromStore(t *testing.T) {
	h := newHarness(t)
	st := model.DefaultSettings()
	st.RefreshMinutes = 45
	h.st.SaveSettings(st)
	h.st.SetJSON("cursor_usage", model.CursorUsage{Used: 100, Limit: 500, FetchedAt: fixedNow})
	h.svc.ReloadFromStore()
	s := h.svc.State()
	if s.Settings.RefreshMinutes != 45 || s.Cursor.Usage.Used != 100 || !s.Cursor.HasUsage {
		t.Errorf("%+v", s)
	}
}

// TestClassify 验证错误归类。
func TestClassify(t *testing.T) {
	if s, _ := classify(nil); s != StatusOK {
		t.Error("nil")
	}
	if s, _ := classify(context.DeadlineExceeded); s != StatusOffline {
		t.Error("deadline")
	}
	if s, _ := classify(fmt.Errorf("wrap: %w", cursor.ErrForbidden)); s != StatusError {
		t.Error("forbidden 视为一般错误")
	}
	if StatusOffline.StatusText() != "离线" || StatusOK.StatusText() != "正常" || Status("zzz").StatusText() != "出错" {
		t.Error("StatusText")
	}
	if (ProviderState{Status: StatusOK, UpdatedAt: fixedNow}).Stale() || !(ProviderState{Status: StatusOffline, UpdatedAt: fixedNow}).Stale() || (ProviderState{Status: StatusOffline}).Stale() {
		t.Error("Stale")
	}
}

// waitFor 轮询等待条件成立。
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// TestFirstSyncCycleThenHistory 首次同步先拉本周期再补更早历史，两段都要落库。
func TestFirstSyncCycleThenHistory(t *testing.T) {
	h := newHarness(t)
	old := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC) // 早于周期起点 9/16
	h.cur.events = append(h.cur.events, fmt.Sprintf(`{"timestamp":"%d","model":"old-model","kind":"USAGE_EVENT_KIND_INCLUDED_IN_PRO","requestsCosts":1,"tokenUsage":{"inputTokens":9}}`, old.UnixMilli()))
	h.svc.RefreshAll(context.Background())
	st := h.svc.State()
	if st.Cursor.EventCount != 4 || st.Cursor.EventsErr != "" {
		t.Fatalf("首次同步应拿到本周期 3 条 + 历史 1 条，得到 %d 条，错误 %q", st.Cursor.EventCount, st.Cursor.EventsErr)
	}
	h.cur.mu.Lock()
	calls := len(h.cur.eventBody)
	h.cur.mu.Unlock()
	if calls < 2 {
		t.Errorf("应分两段请求，实际 %d 次", calls)
	}
	// 之后的增量刷新只拉重叠区间，不重复拉全部历史。
	h.cur.mu.Lock()
	h.cur.eventBody = nil
	h.cur.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	h.cur.mu.Lock()
	n := len(h.cur.eventBody)
	h.cur.mu.Unlock()
	if n != 1 {
		t.Errorf("增量刷新应只请求 1 次，实际 %d", n)
	}
}

// TestMultiCandidateTokens 浏览器里第一个 Profile 的 token 已过期时，应继续尝试后面的候选。
func TestMultiCandidateTokens(t *testing.T) {
	h := newHarness(t)
	h.mu.Lock()
	h.chrome = []deepseek.ChromeToken{
		{Browser: "Chrome", Profile: "Default", Token: "tok-stale"},
		{Browser: "Chrome", Profile: "Profile 1", Token: "tok-chrome"},
	}
	h.mu.Unlock()
	h.ds.mu.Lock()
	h.ds.validTok = "tok-chrome"
	h.ds.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	d := h.svc.State().DeepSeek
	if d.Status != StatusOK || d.TokenSrc != "Chrome / Profile 1" {
		t.Fatalf("应改用第二个候选: %s src=%q", d.Status.StatusText(), d.TokenSrc)
	}
}

// TestBrowserDeniedMessage 被 macOS 拒绝读取浏览器目录时，提示里要说明如何授权。
func TestBrowserDeniedMessage(t *testing.T) {
	h := newHarness(t)
	h.mu.Lock()
	h.chrome = nil
	h.mu.Unlock()
	svc, err := New(Config{
		Store: h.st, Secrets: h.secrets,
		Cursor:        &cursor.Client{BaseURL: h.cur.URL},
		DS:            &deepseek.Client{APIBase: h.ds.URL, PlatformBase: h.ds.URL},
		ChromeTokens:  func() ([]deepseek.ChromeToken, error) { return nil, nil },
		BrowserDenied: func() bool { return true },
		Now:           func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.RefreshAll(context.Background())
	d := svc.State().DeepSeek
	if d.Status != StatusNoCreds || !strings.Contains(d.Message, "完全磁盘访问权限") {
		t.Errorf("提示不对: %s %q", d.Status.StatusText(), d.Message)
	}
}

// TestSaveTokenNormalizes 手动保存 token 时兼容 Bearer/请求头写法，并拒绝 Cookie。
func TestSaveTokenNormalizes(t *testing.T) {
	h := newHarness(t)
	long := "tok-chrome-0123456789abcdef"
	if err := h.svc.SaveSecret(secret.KeyDeepSeekToken, "authorization: Bearer "+long); err != nil {
		t.Fatal(err)
	}
	if v, _ := h.secrets.Get(secret.KeyDeepSeekToken); v != long {
		t.Errorf("应只保存纯 token: %q", v)
	}
	err := h.svc.SaveSecret(secret.KeyDeepSeekToken, "deviceId=0123456789abcdef0123456789abcdef; HWWAFSESID=a7e49a")
	if err == nil || !strings.Contains(err.Error(), "Cookie") {
		t.Errorf("Cookie 应被拒绝: %v", err)
	}
	if v, _ := h.secrets.Get(secret.KeyDeepSeekToken); v != long {
		t.Errorf("被拒绝后不应覆盖原值: %q", v)
	}
}

// newAccountService 创建一个可切换 Cursor 登录账号的服务（共用同一个数据库）。
func (h *harness) newAccountService(t *testing.T, user *atomic.Value) *Service {
	svc, err := New(Config{
		Store: h.st, Secrets: h.secrets,
		Cursor: &cursor.Client{BaseURL: h.cur.URL},
		DS:     &deepseek.Client{APIBase: h.ds.URL, PlatformBase: h.ds.URL},
		CursorLocalCreds: func() (cursor.Credentials, error) {
			return cursor.Credentials{UserID: user.Load().(string), Token: "a.b.c"}, nil
		},
		ChromeTokens: func() ([]deepseek.ChromeToken, error) { return nil, nil },
		Now:          func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// TestAccountSwitchIsolatesEvents 切换 Cursor 账号后，用量与明细必须跟着切换，不能混在一起。
func TestAccountSwitchIsolatesEvents(t *testing.T) {
	h := newHarness(t)
	var user atomic.Value
	user.Store("user_A")
	svc := h.newAccountService(t, &user)
	ctx := context.Background()

	svc.RefreshAll(ctx)
	if c := svc.State().Cursor; c.Usage.Account != "user_A" || c.EventCount != 3 {
		t.Fatalf("A 账号: account=%q events=%d", c.Usage.Account, c.EventCount)
	}
	// 换成 B 账号：服务端对 B 返回另外 2 条明细
	h.cur.mu.Lock()
	aEvents := append([]string(nil), h.cur.events...)
	h.cur.events = h.cur.events[:0]
	for i := 0; i < 2; i++ {
		h.cur.events = append(h.cur.events, fmt.Sprintf(`{"timestamp":"%d","model":"gpt-5","kind":"K","requestsCosts":1}`, fixedNow.Add(-time.Duration(i+10)*time.Hour).UnixMilli()))
	}
	h.cur.mu.Unlock()
	user.Store("user_B")
	svc.RefreshAll(ctx)
	c := svc.State().Cursor
	if c.Usage.Account != "user_B" || c.EventCount != 2 {
		t.Fatalf("B 账号: account=%q events=%d", c.Usage.Account, c.EventCount)
	}
	a, _ := h.st.CursorEvents(store.EventQuery{Account: "user_A"})
	b, _ := h.st.CursorEvents(store.EventQuery{Account: "user_B"})
	if len(a) != 3 || len(b) != 2 {
		t.Errorf("按账号隔离: A=%d B=%d", len(a), len(b))
	}
	for _, e := range b {
		if e.Model != "gpt-5" {
			t.Errorf("B 里混入了 A 的明细: %+v", e)
		}
	}
	if models, _ := h.st.CursorModels("user_B"); len(models) != 1 || models[0] != "gpt-5" {
		t.Errorf("模型列表应只含当前账号: %v", models)
	}
	// 再换回 A：A 的历史仍在，不会重复拉取为 B 的数据（服务端此时返回 A 的 3 条）
	h.cur.mu.Lock()
	h.cur.events = aEvents
	h.cur.mu.Unlock()
	user.Store("user_A")
	svc.RefreshAll(ctx)
	if c := svc.State().Cursor; c.EventCount != 3 {
		t.Errorf("切回 A: %d", c.EventCount)
	}
}

// TestAccountSwitchClearsStaleUsage 切换账号后新账号取数失败时，不能把旧账号的用量显示在新账号下。
func TestAccountSwitchClearsStaleUsage(t *testing.T) {
	h := newHarness(t)
	var user atomic.Value
	user.Store("user_A")
	svc := h.newAccountService(t, &user)
	svc.RefreshAll(context.Background())
	if !svc.State().Cursor.HasUsage {
		t.Fatal("前置：A 应有用量")
	}
	user.Store("user_B")
	h.cur.setStatus(500)
	svc.RefreshAll(context.Background())
	if c := svc.State().Cursor; c.HasUsage || c.EventCount != 0 {
		t.Errorf("新账号失败时不应沿用旧账号数据: %+v", c)
	}
}

// TestNewPlanUsageMode 新计划/团队没有次数上限时，按美元额度计，并能给出精确的重置时刻。
func TestNewPlanUsageMode(t *testing.T) {
	h := newHarness(t)
	h.cur.mu.Lock()
	h.cur.summary = `{"billingCycleStart":"2026-09-16T02:24:49.000Z","billingCycleEnd":"2026-10-16T02:24:49.000Z","membershipType":"pro","limitType":"team","isUnlimited":false,"individualUsage":{"plan":{"used":1672,"limit":2000},"onDemand":{"used":0}}}`
	h.cur.mu.Unlock()
	h.svc.RefreshAll(context.Background())
	u := h.svc.State().Cursor.Usage
	if !u.IsUsage() || u.Used != 1672 || u.Limit != 2000 || u.LimitType != "team" {
		t.Fatalf("%+v", u)
	}
	if want := time.Date(2026, 10, 16, 2, 24, 49, 0, time.UTC); !u.CycleEnd().Equal(want) {
		t.Errorf("重置时刻: %v", u.CycleEnd())
	}
	if msg, err := h.svc.TestCursor(context.Background()); err != nil || !strings.Contains(msg, "$16.72 / $20.00") {
		t.Errorf("测试连接文案: %q %v", msg, err)
	}
}

// setEvents 替换假服务端的明细（每项是一条事件 JSON）。
func (f *cursorFake) setEvents(evs ...string) {
	f.mu.Lock()
	f.events = evs
	f.mu.Unlock()
}

// progressEvent 构造一条「进行中」的事件：时间戳、会话固定，只有 Token 在变。
func progressEvent(ts time.Time, conv string, cacheRead int) string {
	return fmt.Sprintf(`{"timestamp":"%d","model":"claude-sonnet-5-5-high","kind":"USAGE_EVENT_KIND_INCLUDED_IN_PRO","requestsCosts":1,"conversationId":"%s","tokenUsage":{"inputTokens":10,"outputTokens":20,"cacheWriteTokens":30,"cacheReadTokens":%d,"totalCents":%d}}`,
		ts.UnixMilli(), conv, cacheRead, cacheRead/1000)
}

// TestInProgressEventNotDuplicated 回归：进行中的事件（时间戳不变、Token 逐次增长）每次刷新只能更新同一行，不能累加出新行。
func TestInProgressEventNotDuplicated(t *testing.T) {
	h := newHarness(t)
	svc := h.svc
	ctx := context.Background()
	ts := fixedNow.Add(-30 * time.Minute)
	for i, cr := range []int{1000, 5000, 20000, 90000} {
		h.cur.setEvents(progressEvent(ts, "conv-1", cr))
		svc.RefreshAll(ctx)
		if n := svc.State().Cursor.EventCount; n != 1 {
			t.Fatalf("第 %d 次刷新后应只有 1 条明细，实际 %d", i+1, n)
		}
	}
	got, _ := h.st.CursorEvents(store.EventQuery{})
	if len(got) != 1 || got[0].CacheRead != 90000 {
		t.Fatalf("应保留最新的 Token 值: %+v", got)
	}
}

// TestSyncRemovesRemoteMissing 完整拉取后，本地在同一区间内多出来的旧记录（如历史重复）会被清掉；区间之外的不动。
func TestSyncRemovesRemoteMissing(t *testing.T) {
	h := newHarness(t)
	svc := h.svc
	ctx := context.Background()
	ts := fixedNow.Add(-30 * time.Minute)
	h.cur.setEvents(progressEvent(ts, "conv-1", 1000))
	svc.RefreshAll(ctx)
	// 本地混入一条本周期内的陈旧重复记录（模拟旧版本留下的快照），以及一条早于本周期起点的历史
	recent := model.CursorEvent{Time: ts, Model: "claude-sonnet-5-5-high", Kind: "K", Requests: 1, ConversationID: "conv-1", InputTokens: 7, Account: "u"}
	recent.Key = "legacy-dup"
	old := model.CursorEvent{Time: fixedNow.AddDate(0, 0, -40), Model: "m", Kind: "K", Requests: 1, Account: "u"}
	old.Key = "old-history"
	acc := svc.State().Cursor.Usage.Account
	recent.Account, old.Account = acc, acc
	if _, err := h.st.UpsertCursorEvents([]model.CursorEvent{recent, old}); err != nil {
		t.Fatal(err)
	}
	svc.RefreshAll(ctx)
	got, _ := h.st.CursorEvents(store.EventQuery{Account: acc})
	keys := map[string]bool{}
	for _, e := range got {
		keys[e.Key] = true
	}
	if keys["legacy-dup"] {
		t.Errorf("区间内远端已不存在的记录应被清理: %+v", got)
	}
	if !keys["old-history"] {
		t.Errorf("区间之外的历史不应被动: %+v", got)
	}
}

// TestIncompleteFetchKeepsLocal 拉取不完整（接口总数大于实际拿到）时不能删除本地数据。
func TestIncompleteFetchKeepsLocal(t *testing.T) {
	if completeFetch(0, 5) || completeFetch(3, -1) {
		t.Error("空结果或没有总数不应视为完整")
	}
	if !completeFetch(3, 3) || !completeFetch(996, 1000) {
		t.Error("数量吻合应视为完整")
	}
	if completeFetch(50, 100) {
		t.Error("只拿到一半不应视为完整")
	}
}

// agFake 模拟本机 Antigravity 语言服务。
type agFake struct {
	*httptest.Server
	mu   sync.Mutex
	body string
	hits int32
	port int
	on   bool // 是否「运行中」
}

// newAGFake 创建假服务；默认运行中，返回两个额度池。
func newAGFake(t *testing.T) *agFake {
	f := &agFake{on: true, body: agBody(0.72, 1)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.hits, 1)
		if r.Header.Get("X-Codeium-Csrf-Token") != "tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.mu.Lock()
		b := f.body
		f.mu.Unlock()
		fmt.Fprint(w, b)
	}))
	t.Cleanup(f.Close)
	u, _ := url.Parse(f.URL)
	_, p, _ := net.SplitHostPort(u.Host)
	f.port, _ = strconv.Atoi(p)
	return f
}

// agBody 构造响应：Gemini 组剩余 gem，Claude 组剩余 cl。
func agBody(gem, cl float64) string {
	return fmt.Sprintf(`{"userStatus":{"userTier":{"name":"Google AI Pro"},"cascadeModelConfigData":{"clientModelConfigs":[
{"label":"Gemini 3.8 Flash (High)","modelId":"g-h","quotaInfo":{"remainingFraction":%g,"resetTime":"2026-10-14T09:00:00Z"}},
{"label":"Claude Opus 4.6 (Thinking)","modelId":"c-o","quotaInfo":{"remainingFraction":%g,"resetTime":"2026-10-14T11:00:00Z"}}]}}}`, gem, cl)
}

// client 返回指向该假服务的客户端；on 为 false 时表示进程不存在。
func (f *agFake) client() *antigravity.Client {
	return &antigravity.Client{
		Procs: func(context.Context) ([]antigravity.Process, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !f.on {
				return nil, nil
			}
			return []antigravity.Process{{PID: 1, CSRF: "tok"}}, nil
		},
		Ports: func(context.Context, int) ([]int, error) { return []int{f.port}, nil },
	}
}

// newAGService 创建使用假 Antigravity 的服务。
func (h *harness) newAGService(t *testing.T, f *agFake) *Service {
	svc, err := New(Config{
		Store: h.st, Secrets: h.secrets, Antigravity: f.client(),
		Cursor: &cursor.Client{BaseURL: h.cur.URL},
		DS:     &deepseek.Client{APIBase: h.ds.URL, PlatformBase: h.ds.URL},
		CursorLocalCreds: func() (cursor.Credentials, error) {
			return cursor.Credentials{UserID: "user_T", Token: "a.b.c"}, nil
		},
		ChromeTokens: func() ([]deepseek.ChromeToken, error) { return h.chrome, nil },
		Now:          func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// TestAntigravityRefresh 刷新后能读到额度池，并落库；重启后立即可见。
func TestAntigravityRefresh(t *testing.T) {
	h := newHarness(t)
	f := newAGFake(t)
	svc := h.newAGService(t, f)
	svc.RefreshAll(context.Background())
	ag := svc.State().Antigravity
	if ag.Status != StatusOK || !ag.HasQuota || ag.Quota.Plan != "Google AI Pro" || len(ag.Quota.Models) != 2 {
		t.Fatalf("状态: %+v", ag)
	}
	if p, ok := ag.Quota.Tightest(); !ok || p.Remaining != 0.72 || p.Name != "Gemini" {
		t.Errorf("最紧张的额度池: %+v", p)
	}
	// 重启：不刷新也能立刻看到上次的额度
	svc2 := h.newAGService(t, f)
	if a := svc2.State().Antigravity; !a.HasQuota || a.Quota.Plan != "Google AI Pro" {
		t.Errorf("重启后应恢复上次额度: %+v", a)
	}
}

// TestAntigravityNotRunningKeepsData Antigravity 没运行时标记「未运行」并保留旧数据；运行后恢复；失败后不退避拉长。
func TestAntigravityNotRunningKeepsData(t *testing.T) {
	h := newHarness(t)
	f := newAGFake(t)
	svc := h.newAGService(t, f)
	ctx := context.Background()
	svc.RefreshAll(ctx)
	f.mu.Lock()
	f.on = false
	f.mu.Unlock()
	for i := 0; i < 4; i++ {
		svc.RefreshAll(ctx)
	}
	ag := svc.State().Antigravity
	if ag.Status != StatusNotRunning || !ag.HasQuota || !ag.Stale() || ag.Message == "" {
		t.Fatalf("未运行应保留旧数据并标记: %+v", ag)
	}
	if want := fixedNow.Add(time.Minute); !ag.NextTry.Equal(want) {
		t.Errorf("未运行时不应指数退避，下次尝试应为 1 分钟后: %v", ag.NextTry.Sub(fixedNow))
	}
	f.mu.Lock()
	f.on = true
	f.body = agBody(0.5, 1)
	f.mu.Unlock()
	svc.RefreshAll(ctx)
	ag = svc.State().Antigravity
	if ag.Status != StatusOK || ag.Stale() {
		t.Fatalf("恢复运行后应回到正常: %+v", ag)
	}
	if p, _ := ag.Quota.Tightest(); p.Remaining != 0.5 {
		t.Errorf("应更新为最新额度: %+v", p)
	}
}

// TestAntigravityDisabled 在设置里关闭后不发任何请求，打开后立即取一次。
func TestAntigravityDisabled(t *testing.T) {
	h := newHarness(t)
	f := newAGFake(t)
	svc := h.newAGService(t, f)
	s := svc.Settings()
	s.AntigravityOff = true
	if err := svc.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	svc.RefreshAll(context.Background())
	if n := atomic.LoadInt32(&f.hits); n != 0 {
		t.Errorf("关闭后不应访问 Antigravity，实际 %d 次", n)
	}
	if st := svc.State().Antigravity; !st.Disabled || st.HasQuota {
		t.Errorf("状态应为已关闭: %+v", st)
	}
	s.AntigravityOff = false
	if err := svc.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	svc.RefreshAll(context.Background())
	if st := svc.State().Antigravity; st.Disabled || !st.HasQuota || st.Status != StatusOK {
		t.Errorf("重新打开后应取到额度: %+v", st)
	}
}

// TestAntigravityDoesNotAffectOthers Antigravity 失败不影响 Cursor / DeepSeek 刷新。
func TestAntigravityDoesNotAffectOthers(t *testing.T) {
	h := newHarness(t)
	f := newAGFake(t)
	f.on = false
	svc := h.newAGService(t, f)
	svc.RefreshAll(context.Background())
	st := svc.State()
	if st.Cursor.Status != StatusOK || st.DeepSeek.Status != StatusOK || st.Antigravity.Status != StatusNotRunning {
		t.Errorf("各数据源应独立: cursor=%s deepseek=%s ag=%s", st.Cursor.Status, st.DeepSeek.Status, st.Antigravity.Status)
	}
}

// TestTestAntigravity 设置页「测试连接」的文案。
func TestTestAntigravity(t *testing.T) {
	h := newHarness(t)
	f := newAGFake(t)
	svc := h.newAGService(t, f)
	msg, err := svc.TestAntigravity(context.Background())
	if err != nil || !strings.Contains(msg, "Google AI Pro") || !strings.Contains(msg, "2 个模型") {
		t.Errorf("%q %v", msg, err)
	}
	f.on = false
	if _, err := svc.TestAntigravity(context.Background()); !errors.Is(err, antigravity.ErrNotRunning) {
		t.Errorf("没运行应返回 ErrNotRunning: %v", err)
	}
}
