package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/service"
)

// newTestApp 创建一个注入了固定状态与时钟的 App（不依赖真实服务/窗口）。
func newTestApp(st service.State) *App {
	a := New()
	a.SetClock(func() time.Time { return testNow })
	a.stateFn = func() service.State { return st }
	return a
}

// fullState 返回 Cursor、DeepSeek、Antigravity 都正常的状态。
func fullState() service.State {
	return service.State{Cursor: cursorState(359), DeepSeek: dsState(), Antigravity: agState(), Settings: model.DefaultSettings()}
}

// agQuota 返回与真实 Antigravity 一致的额度：Gemini Flash 组剩余 72%，Claude / GPT-OSS 组满额度。
func agQuota() model.AntigravityQuota {
	gem := testNow.Add(2*time.Hour + 45*time.Minute)
	cl := testNow.Add(5 * time.Hour)
	return model.AntigravityQuota{Plan: "Google AI Pro", FetchedAt: testNow, Models: []model.AntigravityModel{
		{Label: "Claude Opus 4.6 (Thinking)", ID: "c1", HasQuota: true, Remaining: 1, ResetAt: cl},
		{Label: "GPT-OSS 120B (Medium)", ID: "g1", HasQuota: true, Remaining: 1, ResetAt: cl},
		{Label: "Gemini 3.8 Flash (High)", ID: "f1", HasQuota: true, Remaining: 0.72, ResetAt: gem},
		{Label: "Gemini 3.8 Flash (Low)", ID: "f2", HasQuota: true, Remaining: 0.72, ResetAt: gem},
		{Label: "Gemini 4 Argon", ID: "a1", Disabled: true},
	}}
}

// agState 返回 Antigravity 正常的状态。
func agState() service.AntigravityState {
	return service.AntigravityState{
		ProviderState: service.ProviderState{Status: service.StatusOK, UpdatedAt: testNow},
		Quota:         agQuota(), HasQuota: true,
	}
}

// TestPopoverShowsAntigravity 浮窗展示 Antigravity 各额度池的剩余比例与重置时间；满额度显示「额度充足」；点击卡片打开详情页。
func TestPopoverShowsAntigravity(t *testing.T) {
	st := fullState()
	st.Antigravity = agState()
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	for _, want := range []string{"Antigravity", "Google AI Pro", "Gemini", "剩余 72%", "Claude / GPT-OSS", "剩余 100%", "额度充足", "已同步"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q；实际: %q", want, tt.Texts())
		}
	}
	var gem string
	for _, x := range tt.Texts() {
		if strings.Contains(x, "还剩 2时45分") {
			gem = x
		}
	}
	if gem == "" {
		t.Errorf("Gemini 组应显示重置倒计时；实际: %q", tt.Texts())
	}
	var opened []string
	a.hk.openPage = func(page string) { opened = append(opened, page) }
	if err := tt.Click("剩余 72%"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != PageAntigravity {
		t.Errorf("点击卡片应打开 antigravity 页: %v", opened)
	}
}

// TestPopoverAntigravityStates 没运行、未取到数据、已关闭三种情况的文案。
func TestPopoverAntigravityStates(t *testing.T) {
	// 没运行但有旧数据
	st := fullState()
	st.Antigravity = agState()
	st.Antigravity.Status, st.Antigravity.Message = service.StatusNotRunning, "未检测到正在运行的 Antigravity，请先打开它"
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if !tt.HasText("未运行") || !tt.HasText("剩余 72%") {
		t.Errorf("未运行应显示状态并保留旧数据: %q", tt.Texts())
	}
	// 没有数据
	st = fullState()
	st.Antigravity = service.AntigravityState{}
	st.Antigravity.Status, st.Antigravity.Message = service.StatusNotRunning, "未检测到正在运行的 Antigravity，请先打开它"
	a = newTestApp(st)
	tt = ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if !tt.HasText("未检测到正在运行的 Antigravity，请先打开它") {
		t.Errorf("没有数据应说明原因: %q", tt.Texts())
	}
	// 已关闭：卡片不出现，浮窗高度回到原值
	st = fullState()
	st.Settings.AntigravityOff = true
	st.Antigravity.Disabled = true
	a = newTestApp(st)
	if a.popoverHeight() != PopoverHeight {
		t.Errorf("关闭后浮窗高度应为 %d，实际 %d", PopoverHeight, a.popoverHeight())
	}
	tt = ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if tt.HasText("Antigravity") {
		t.Errorf("关闭后不应显示 Antigravity: %q", tt.Texts())
	}
}

// TestPopoverShowsData 浮窗应展示用量、剩余、重置、余额、今日 Token。
func TestPopoverShowsData(t *testing.T) {
	a := newTestApp(fullState())
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	for _, want := range []string{"AI Usage", "Cursor", "359", "/ 500", "剩余 141 次", "DeepSeek", "¥12.61", "今日 Token", "1.23M", "¥0.55", "¥3.20", "已同步"} {
		if !tt.HasText(want) {
			t.Errorf("缺少文案 %q；实际: %q", want, tt.Texts())
		}
	}
}

// TestPopoverNoCredentials 未登录时应给出原因与「前往设置」入口，而不是空白。
func TestPopoverNoCredentials(t *testing.T) {
	st := service.State{Settings: model.DefaultSettings()}
	st.Cursor.Status, st.Cursor.Message = service.StatusNoCreds, "未找到本机 Cursor 登录"
	st.DeepSeek.Status, st.DeepSeek.Message = service.StatusNoCreds, "未配置 DeepSeek API Key"
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	for _, want := range []string{"未找到本机 Cursor 登录", "未配置 DeepSeek API Key", "未登录", "尚未获取数据"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q；实际: %q", want, tt.Texts())
		}
	}
	if tt.HasText("剩余 0 次") {
		t.Error("没有数据时不应显示 0 次")
	}
}

// TestPopoverStale 离线但有旧数据：仍显示数据，并明确标注是上次数据。
func TestPopoverStale(t *testing.T) {
	st := fullState()
	st.Cursor.Status, st.Cursor.Message = service.StatusOffline, "网络不可用，请检查连接后重试"
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if !tt.HasText("359") || !tt.HasText("离线") || !tt.HasText("网络不可用，请检查连接后重试（显示的是上次数据）") {
		t.Errorf("实际: %q", tt.Texts())
	}
}

// TestPopoverNeedToken 只有 API Key 没有平台 token：显示余额并提示去设置。
func TestPopoverNeedToken(t *testing.T) {
	st := fullState()
	st.DeepSeek.HasBill = false
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if !tt.HasText("¥12.61") || !tt.HasText("未读取到平台登录信息，暂无今日 Token") || !tt.HasText("去设置") {
		t.Errorf("实际: %q", tt.Texts())
	}
}

// TestPopoverLowBalanceAndExhaust 低余额与预测用完的提示文案出现。
func TestPopoverLowBalanceAndExhaust(t *testing.T) {
	st := fullState()
	st.DeepSeek.Balance.Total = 2
	st.Cursor.Usage.CycleStart = time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local)
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if !tt.HasText("¥2.00") || !tt.HasText("按当前速度约 10月19日 用完") {
		t.Errorf("实际: %q", tt.Texts())
	}
}

// TestPopoverDarkAndScale 深色与 2 倍缩放下都能正常构建。
func TestPopoverDarkAndScale(t *testing.T) {
	a := newTestApp(fullState())
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	tt.SetDark(true)
	tt.SetScale(2)
	tt.Frame()
	if !tt.HasText("359") {
		t.Error("深色/2x 下缺少数据")
	}
}

// TestPopoverFitsHeight 各种状态下浮窗内容都不能超出固定高度（否则底部的「更新于」和退出按钮会被裁掉）。
func TestPopoverFitsHeight(t *testing.T) {
	stale := fullState()
	stale.Cursor.Status, stale.Cursor.Message = service.StatusOffline, "网络不可用，请检查连接后重试"
	stale.DeepSeek.Status, stale.DeepSeek.Message = service.StatusOffline, "网络不可用，请检查连接后重试"
	worst := stale
	worst.Cursor.Usage.CycleStart = time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local) // 触发「按当前速度约…用完」
	worst.DeepSeek.Balance.Total = 2
	noCreds := service.State{Settings: model.DefaultSettings()}
	noCreds.Cursor.Status, noCreds.Cursor.Message = service.StatusNoCreds, "未找到本机 Cursor 登录，请先登录 Cursor 或在设置里手动粘贴 Cookie"
	noCreds.DeepSeek.Status, noCreds.DeepSeek.Message = service.StatusNoCreds, "未配置 DeepSeek API Key，也没有在浏览器里找到登录信息"
	needToken := fullState()
	needToken.DeepSeek.HasBill = false
	withAG := func(st service.State) service.State { st.Antigravity = agState(); return st }
	agStale := withAG(stale)
	agStale.Antigravity.Status, agStale.Antigravity.Message = service.StatusNotRunning, "未检测到正在运行的 Antigravity，请先打开它"
	agNone := noCreds
	agNone.Antigravity.Status, agNone.Antigravity.Message = service.StatusNotRunning, "未检测到正在运行的 Antigravity，请先打开它"
	off := fullState()
	off.Settings.AntigravityOff, off.Antigravity.Disabled = true, true
	cases := map[string]service.State{
		"正常": withAG(fullState()), "离线": withAG(stale), "最坏": withAG(worst), "未登录": withAG(noCreds), "缺 token": withAG(needToken),
		"AG 未运行有旧数据": agStale, "AG 无数据": agNone, "已关闭 AG": off,
	}
	for name, st := range cases {
		a := newTestApp(st)
		limit := a.popoverHeight()
		// 找到「底栏不再被挤出窗口」的最小高度，即内容的自然高度。
		need := 0
		for h := 200; h <= 800; h += 2 {
			tt := ui.NewTester(a.popoverView, PopoverWidth, h)
			if r, ok := tt.Find(RefreshedText(st, testNow)); ok && r.Y+r.H <= float32(h)-10 {
				need = h
				break
			}
		}
		t.Logf("%s: 内容自然高度约 %d（浮窗高 %d）", name, need, limit)
		if need == 0 || need > limit {
			t.Errorf("%s: 内容超出浮窗高度，需要 %d > %d", name, need, limit)
		}
	}
}

// TestPopoverCardsOpenPanelPages 点浮窗里的 Cursor / DeepSeek 卡片，应打开大面板并跳到对应页面。
func TestPopoverCardsOpenPanelPages(t *testing.T) {
	a := newTestApp(fullState())
	var opened []string
	a.hk.openPage = func(page string) { opened = append(opened, page) }
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	if err := tt.Click("剩余 141 次"); err != nil { // Cursor 卡片里的文字
		t.Fatal(err)
	}
	if err := tt.Click("剩余余额"); err != nil { // DeepSeek 卡片里的文字
		t.Fatal(err)
	}
	if len(opened) != 2 || opened[0] != PageCursor || opened[1] != PageDeepSeek {
		t.Errorf("点击卡片应依次打开 cursor / deepseek 页，实际 %v", opened)
	}
}

// TestPopoverUsageModeAndPlan 新计划按美元额度显示：环里显示百分比，并标注套餐与精确重置时刻。
func TestPopoverUsageModeAndPlan(t *testing.T) {
	st := fullState()
	st.Cursor.Usage = model.CursorUsage{
		Mode: model.CursorModeUsage, Account: "acct", Used: 1672, Limit: 2000, Membership: "pro", LimitType: "team",
		CycleStart: time.Date(2026, 9, 16, 8, 0, 0, 0, time.Local), CycleEndAt: time.Date(2026, 10, 16, 10, 24, 0, 0, time.Local),
	}
	a := newTestApp(st)
	tt := ui.NewTester(a.popoverView, PopoverWidth, a.popoverHeight())
	for _, want := range []string{"84%", "剩余 $3.28", "Pro · 团队额度", "10/16 10:24 重置 · 还剩 1天19时"} {
		if !tt.HasText(want) {
			t.Errorf("缺少 %q；实际: %q", want, tt.Texts())
		}
	}
}
