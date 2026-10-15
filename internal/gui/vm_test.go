package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/service"
)

var testNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.Local)

// cursorState 构造一个 Cursor 状态。
func cursorState(used int) service.CursorState {
	return service.CursorState{
		ProviderState: service.ProviderState{Status: service.StatusOK, UpdatedAt: testNow.Add(-3 * time.Minute)},
		HasUsage:      true,
		Usage:         model.CursorUsage{Account: "acct", Used: used, Limit: 500, CycleStart: time.Date(2026, 9, 16, 8, 0, 0, 0, time.Local)},
		EventCount:    1234,
	}
}

// TestBuildCursorVM 验证各档位的文案与级别。
func TestBuildCursorVM(t *testing.T) {
	vm := BuildCursorVM(cursorState(359), testNow)
	if vm.Used != "359" || vm.Limit != "500" || vm.Remain != "剩余 141 次" || vm.Percent != "72%" || vm.Level != LevelOK {
		t.Errorf("%+v", vm)
	}
	if vm.Reset != "10/16 08:00 重置 · 还剩 1天17时" {
		t.Errorf("reset: %q", vm.Reset)
	}
	if vm.Updated != "3 分钟前" || vm.Events != "1,234 条明细" {
		t.Errorf("%q %q", vm.Updated, vm.Events)
	}
	if vm.WarnExhaust || vm.Exhaust != "" {
		t.Errorf("周期只剩 2 天，按日均 12.7 次不会提前用完: %+v", vm)
	}
	fast := cursorState(359)
	fast.Usage.CycleStart = time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local) // 13 天用了 359，日均约 27 次
	if v := BuildCursorVM(fast, testNow); !v.WarnExhaust || !strings.Contains(v.Exhaust, "10月19日") {
		t.Errorf("用得快应预警: %+v", v)
	}
	if v := BuildCursorVM(cursorState(400), testNow); v.Level != LevelWarn {
		t.Error("80% 应为警告")
	}
	if v := BuildCursorVM(cursorState(480), testNow); v.Level != LevelDanger {
		t.Error("96% 应为危险")
	}
	if v := BuildCursorVM(cursorState(500), testNow); v.Exhaust != "本周期额度已用完" || v.Remain != "剩余 0 次" {
		t.Errorf("%+v", v)
	}
	if v := BuildCursorVM(cursorState(550), testNow); v.Frac != 1 || v.Remain != "剩余 0 次" {
		t.Errorf("超额应封顶: %+v", v)
	}
	if v := BuildCursorVM(cursorState(10), testNow); v.WarnExhaust || v.Exhaust != "" {
		t.Errorf("低用量不应预警: %+v", v)
	}
}

// TestBuildCursorVMNoData 验证无数据与失败态。
func TestBuildCursorVMNoData(t *testing.T) {
	vm := BuildCursorVM(service.CursorState{ProviderState: service.ProviderState{Status: service.StatusNoCreds, Message: "未找到 Cursor 登录"}}, testNow)
	if vm.Has || vm.StatusText != "未登录" || vm.Message == "" || vm.Updated != "从未" {
		t.Errorf("%+v", vm)
	}
	st := cursorState(100)
	st.Status = service.StatusOffline
	if v := BuildCursorVM(st, testNow); !v.Stale || v.StatusText != "离线" {
		t.Errorf("%+v", v)
	}
}

// dsState 构造 DeepSeek 状态。
func dsState() service.DeepSeekState {
	return service.DeepSeekState{
		ProviderState: service.ProviderState{Status: service.StatusOK, UpdatedAt: testNow},
		HasBalance:    true,
		Balance:       model.DeepSeekBalance{Currency: "CNY", Total: 12.61, ToppedUp: 10.61, Granted: 2, TotalSpent: 17.5, HasSpent: true},
		HasBill:       true,
		Month:         "2026-10",
		MonthCost:     3.2,
		Today:         model.DeepSeekDayStat{Tokens: 1234567, Requests: 42, Cost: 0.55},
	}
}

// TestBuildDeepSeekVM 验证金额、Token、低余额与缺 token 提示。
func TestBuildDeepSeekVM(t *testing.T) {
	st := model.DefaultSettings()
	vm := BuildDeepSeekVM(dsState(), st, testNow)
	if vm.Balance != "¥12.61" || vm.MonthCost != "¥3.20" || vm.TodayTokens != "1.23M" || vm.TodayCost != "¥0.55" || vm.TodayReqs != "42" {
		t.Errorf("%+v", vm)
	}
	if vm.MonthLabel != "10 月" || vm.TotalSpent != "¥17.50" || vm.LowBalance || vm.NeedToken {
		t.Errorf("%+v", vm)
	}
	if !strings.Contains(vm.Breakdown, "充值 ¥10.61") {
		t.Errorf("%q", vm.Breakdown)
	}
	s := dsState()
	s.Balance.Total = 3
	if v := BuildDeepSeekVM(s, st, testNow); !v.LowBalance {
		t.Error("低于 5 应为低余额")
	}
	st.NotifyDeepSeekBelow = 0
	if v := BuildDeepSeekVM(s, st, testNow); v.LowBalance {
		t.Error("阈值 0 表示关闭")
	}
	s = dsState()
	s.HasBill = false
	if v := BuildDeepSeekVM(s, model.DefaultSettings(), testNow); !v.NeedToken || v.TodayTokens != "" {
		t.Errorf("%+v", v)
	}
	s.Month = "2026-03"
	s.HasBill = true
	if v := BuildDeepSeekVM(s, model.DefaultSettings(), testNow); v.MonthLabel != "3 月" {
		t.Errorf("%q", v.MonthLabel)
	}
}

// TestTrayTitle 验证四种托盘显示模式及缺数据场景。
func TestTrayTitle(t *testing.T) {
	st := service.State{Cursor: cursorState(359), DeepSeek: dsState(), Settings: model.DefaultSettings()}
	cases := map[string]string{
		model.TrayIcon: "", model.TrayCursor: "359/500", model.TrayDeepSeek: "¥12.61", model.TrayBoth: "359/500 · ¥12.61",
	}
	for mode, want := range cases {
		st.Settings.TrayDisplay = mode
		if got := TrayTitle(st); got != want {
			t.Errorf("%s: %q want %q", mode, got, want)
		}
	}
	st.Settings.TrayDisplay = model.TrayBoth
	st.DeepSeek.HasBalance = false
	if got := TrayTitle(st); got != "359/500" {
		t.Errorf("both 缺 DeepSeek: %q", got)
	}
	st.Cursor.HasUsage = false
	if got := TrayTitle(st); got != "" {
		t.Errorf("both 全缺: %q", got)
	}
	st.DeepSeek = dsState()
	if got := TrayTitle(st); got != "¥12.61" {
		t.Errorf("both 缺 Cursor: %q", got)
	}
	st.Settings.TrayDisplay = model.TrayCursor
	if got := TrayTitle(st); got != "" {
		t.Errorf("cursor 模式缺数据: %q", got)
	}
}

// TestTrayToolTipAndRefreshed 验证悬停提示与更新文案。
func TestTrayToolTipAndRefreshed(t *testing.T) {
	st := service.State{Cursor: cursorState(359), DeepSeek: dsState(), Settings: model.DefaultSettings()}
	tip := TrayToolTip(st, testNow)
	if !strings.Contains(tip, "359/500") || !strings.Contains(tip, "¥12.61") {
		t.Errorf("%q", tip)
	}
	if tip := TrayToolTip(service.State{Settings: model.DefaultSettings()}, testNow); !strings.Contains(tip, "Cursor：") {
		t.Errorf("%q", tip)
	}
	if got := RefreshedText(st, testNow); got != "更新于 刚刚" {
		t.Errorf("%q", got)
	}
	st.Refreshing = true
	if got := RefreshedText(st, testNow); got != "正在刷新…" {
		t.Errorf("%q", got)
	}
	if got := RefreshedText(service.State{}, testNow); got != "尚未获取数据" {
		t.Errorf("%q", got)
	}
}

// TestStatusTone 验证状态色调映射。
func TestStatusTone(t *testing.T) {
	for s, want := range map[service.Status]string{
		service.StatusOK: "ok", service.StatusLoading: "muted", service.StatusIdle: "muted",
		service.StatusNoCreds: "bad", service.StatusUnauthorized: "bad", service.StatusError: "bad",
		service.StatusOffline: "warn", service.StatusRateLimited: "warn",
	} {
		if got := StatusTone(s, true); got != want {
			t.Errorf("%s: %s want %s", s, got, want)
		}
	}
}
