package antigravity

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// 与真实 Antigravity 返回结构一致的响应（已去掉个人信息与无关字段）。
const statusJSON = `{"userStatus":{"name":"x","email":"x@example.com",
"planStatus":{"planInfo":{"teamsTier":"TEAMS_TIER_PRO","planName":"Pro"},"availablePromptCredits":500},
"cascadeModelConfigData":{"clientModelConfigs":[
 {"label":"Claude Opus 4.6 (Thinking)","modelOrAlias":{"model":"MODEL_PLACEHOLDER_M26"},"quotaInfo":{"remainingFraction":1,"resetTime":"2026-10-16T23:07:38Z"},"modelId":"claude-opus-4-6-thinking"},
 {"label":"GPT-OSS 120B (Medium)","quotaInfo":{"remainingFraction":1,"resetTime":"2026-10-16T23:07:38Z"},"modelId":"gpt-oss-120b-medium"},
 {"label":"Gemini 4 Argon","disabled":true,"quotaInfo":{},"modelId":"gemini-4-argon-disabled"},
 {"label":"Gemini 3.8 Flash (High)","quotaInfo":{"remainingFraction":0.720279,"resetTime":"2026-10-16T20:52:52Z"},"modelId":"gemini-3.8-flash-high"},
 {"label":"Gemini 3.8 Flash (Low)","quotaInfo":{"remainingFraction":0.720279,"resetTime":"2026-10-16T20:52:52Z"},"modelId":"gemini-3.8-flash-low"},
 {"label":"Claude Sonnet 4.6 (Thinking)","quotaInfo":{"resetTime":"2026-10-16T23:07:38Z"},"modelId":"claude-sonnet-4-6"}
]}, "userTier":{"id":"g1-pro-tier","name":"Google AI Pro"}}}`

// TestParseQuota 验证套餐、模型、剩余比例与重置时间的解析，以及额度池归并。
func TestParseQuota(t *testing.T) {
	now := time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC)
	q, err := ParseQuota([]byte(statusJSON), now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Plan != "Google AI Pro" || len(q.Models) != 6 || !q.FetchedAt.Equal(now) {
		t.Fatalf("基本信息: %+v", q)
	}
	m := q.Models[3]
	if m.ID != "gemini-3.8-flash-high" || !m.HasQuota || m.Remaining != 0.720279 || !m.ResetAt.Equal(time.Date(2026, 10, 16, 20, 52, 52, 0, time.UTC)) {
		t.Errorf("Gemini Flash: %+v", m)
	}
	if d := q.Models[2]; !d.Disabled || d.HasQuota {
		t.Errorf("不可用的模型应标记 Disabled 且无额度: %+v", d)
	}
	// 有重置时间但没有剩余比例：额度已耗尽
	if e := q.Models[5]; !e.HasQuota || e.Remaining != 0 || e.Used() != 1 {
		t.Errorf("缺省剩余比例应视为耗尽: %+v", e)
	}
	pools := q.Pools()
	if len(pools) != 3 {
		t.Fatalf("应归并为 3 个额度池（耗尽的 Claude Sonnet / Gemini Flash / Claude+GPT-OSS），实际 %d: %+v", len(pools), pools)
	}
	if pools[0].Remaining != 0 || pools[0].Name != "Claude" {
		t.Errorf("最紧张的在前: %+v", pools[0])
	}
	if pools[1].Name != "Gemini" || len(pools[1].Labels) != 2 {
		t.Errorf("Gemini Flash 的两档应归为一组: %+v", pools[1])
	}
	if pools[2].Name != "Claude / GPT-OSS" || pools[2].Remaining != 1 {
		t.Errorf("满额度的组: %+v", pools[2])
	}
	if p, ok := q.Tightest(); !ok || p.Remaining != 0 {
		t.Errorf("Tightest: %+v %v", p, ok)
	}
}

// TestParseQuotaErrors 验证未登录与坏数据。
func TestParseQuotaErrors(t *testing.T) {
	now := time.Now()
	if _, err := ParseQuota([]byte(`{}`), now); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("没有 userStatus 应为未登录: %v", err)
	}
	if _, err := ParseQuota([]byte(`[`), now); !errors.Is(err, ErrBadResponse) {
		t.Errorf("坏 JSON: %v", err)
	}
	if _, err := ParseQuota([]byte(`{"userStatus":{"cascadeModelConfigData":{"clientModelConfigs":[]}}}`), now); !errors.Is(err, ErrBadResponse) {
		t.Errorf("没有模型: %v", err)
	}
}

// TestParseProcesses 验证只挑出 Antigravity 的语言服务，并取出 csrf token。
func TestParseProcesses(t *testing.T) {
	out := `  101 /Applications/Antigravity.app/Contents/MacOS/Antigravity
33351 /Applications/Antigravity.app/Contents/Resources/bin/language_server --standalone --override_ide_name antigravity --https_server_port 0 --csrf_token abc-123 --app_data_dir antigravity --api_server_url https://x
  202 /Applications/Windsurf.app/Contents/Resources/language_server_macos_arm --csrf_token windsurf-token
  303 /Applications/Antigravity.app/Contents/Resources/bin/language_server --app_data_dir antigravity
  404 /usr/bin/grep language_server antigravity --csrf_token=eq-token
`
	ps := ParseProcesses(out)
	if len(ps) != 2 || ps[0].PID != 33351 || ps[0].CSRF != "abc-123" || ps[1].PID != 404 || ps[1].CSRF != "eq-token" {
		t.Errorf("进程解析: %+v", ps)
	}
	if len(ParseProcesses("")) != 0 {
		t.Error("空输出应没有进程")
	}
}

// TestParsePorts 验证 lsof -Fn 输出的端口解析与去重。
func TestParsePorts(t *testing.T) {
	out := "p33351\nf7\nn127.0.0.1:58635\nf8\nn127.0.0.1:58636\nf9\nn*:58636\nnlocalhost:abc\nn[::1]:61000 (LISTEN)\n"
	got := ParsePorts(out)
	want := []int{58635, 58636, 61000}
	if len(got) != len(want) {
		t.Fatalf("端口: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("端口 %d: %v", i, got)
		}
	}
}

// fakeServer 启动一个模拟语言服务；校验 csrf 头与路径，返回它的端口。
func fakeServer(t *testing.T, tls bool, csrf, body string) int {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != statusPath || r.Header.Get("X-Codeium-Csrf-Token") != csrf || r.Header.Get("Connect-Protocol-Version") != "1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, p, _ := net.SplitHostPort(u.Host)
	n, _ := strconv.Atoi(p)
	return n
}

// newTestClient 创建注入了假进程与假端口的客户端。
func newTestClient(procs []Process, ports ...int) *Client {
	return &Client{
		Procs: func(context.Context) ([]Process, error) { return procs, nil },
		Ports: func(context.Context, int) ([]int, error) { return ports, nil },
	}
}

// TestFetchQuotaHTTPSAndHTTP 验证 https 端口、http 端口都能读到，且会跳过连不上的端口。
func TestFetchQuotaHTTPSAndHTTP(t *testing.T) {
	ctx := context.Background()
	for _, tls := range []bool{true, false} {
		port := fakeServer(t, tls, "tok", statusJSON)
		c := newTestClient([]Process{{PID: 1, CSRF: "tok"}}, 1 /* 连不上的端口 */, port)
		q, err := c.FetchQuota(ctx)
		if err != nil || q.Plan != "Google AI Pro" || len(q.Models) != 6 {
			t.Errorf("tls=%v: %+v %v", tls, q, err)
		}
	}
}

// TestFetchQuotaErrors 验证没运行、csrf 错误、未登录的错误归类。
func TestFetchQuotaErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := newTestClient(nil).FetchQuota(ctx); !errors.Is(err, ErrNotRunning) {
		t.Errorf("没有进程应为 ErrNotRunning: %v", err)
	}
	if _, err := newTestClient([]Process{{PID: 1, CSRF: "t"}}).FetchQuota(ctx); !errors.Is(err, ErrNotRunning) {
		t.Errorf("没有监听端口应为 ErrNotRunning: %v", err)
	}
	port := fakeServer(t, true, "right", statusJSON)
	_, err := newTestClient([]Process{{PID: 1, CSRF: "wrong"}}, port).FetchQuota(ctx)
	if err == nil || errors.Is(err, ErrNotRunning) {
		t.Errorf("csrf 错误应报连接失败而不是未运行: %v", err)
	}
	port = fakeServer(t, true, "t", `{}`)
	if _, err := newTestClient([]Process{{PID: 1, CSRF: "t"}}, port).FetchQuota(ctx); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("未登录: %v", err)
	}
	// 多个进程：第一个不可用时用第二个
	good := fakeServer(t, false, "t2", statusJSON)
	c := &Client{
		Procs: func(context.Context) ([]Process, error) {
			return []Process{{PID: 1, CSRF: "t1"}, {PID: 2, CSRF: "t2"}}, nil
		},
		Ports: func(_ context.Context, pid int) ([]int, error) { return []int{good}, nil },
	}
	if q, err := c.FetchQuota(ctx); err != nil || len(q.Models) == 0 {
		t.Errorf("应回退到第二个进程: %v", err)
	}
}
