package model

import (
	"testing"
	"time"
)

// 构造一个与真实数据一致的用量：359/500，周期 9-16 起。
func sampleUsage() CursorUsage {
	return CursorUsage{Used: 359, Limit: 500, CycleStart: time.Date(2026, 9, 16, 2, 24, 49, 0, time.UTC)}
}

// TestCursorUsageBasics 验证剩余、比例、重置时间。
func TestCursorUsageBasics(t *testing.T) {
	u := sampleUsage()
	if u.Remaining() != 141 {
		t.Errorf("Remaining=%d", u.Remaining())
	}
	if p := u.Percent(); p < 0.717 || p > 0.719 {
		t.Errorf("Percent=%v", p)
	}
	if !u.CycleEnd().Equal(time.Date(2026, 10, 16, 2, 24, 49, 0, time.UTC)) {
		t.Errorf("CycleEnd=%v", u.CycleEnd())
	}
	now := time.Date(2026, 10, 14, 15, 39, 0, 0, time.UTC)
	if d := u.DaysUntilReset(now); d != 2 {
		t.Errorf("DaysUntilReset=%d", d)
	}
	if d := u.DaysUntilReset(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)); d != 0 {
		t.Errorf("过期应为 0: %d", d)
	}
	// 超额与边界
	over := CursorUsage{Used: 600, Limit: 500}
	if over.Remaining() != 0 || over.Percent() != 1 {
		t.Error("超额处理错误")
	}
	if (CursorUsage{}).Percent() != 0 || !(CursorUsage{}).CycleEnd().IsZero() {
		t.Error("零值处理错误")
	}
}

// TestProjectedExhaust 验证日均与预计用尽。
func TestProjectedExhaust(t *testing.T) {
	u := sampleUsage()
	now := time.Date(2026, 10, 14, 2, 24, 49, 0, time.UTC) // 周期第 28 天
	avg := u.DailyAverage(now)
	if avg < 12.8 || avg > 12.9 {
		t.Errorf("DailyAverage=%v", avg)
	}
	ex, ok := u.ProjectedExhaust(now)
	if !ok || !ex.After(now) {
		t.Fatalf("应有预测: %v %v", ex, ok)
	}
	// 剩余 141 / 12.82 ≈ 11 天后，晚于重置日(2 天后)，不会提前用完
	if u.WillExhaustBeforeReset(now) {
		t.Error("不应提前用完")
	}
	fast := CursorUsage{Used: 480, Limit: 500, CycleStart: now.AddDate(0, 0, -10)}
	if !fast.WillExhaustBeforeReset(now) {
		t.Error("应提前用完")
	}
	done := CursorUsage{Used: 500, Limit: 500, CycleStart: now.AddDate(0, 0, -10)}
	if e, ok := done.ProjectedExhaust(now); !ok || !e.Equal(now) {
		t.Error("已耗尽应返回 now")
	}
	if _, ok := (CursorUsage{Used: 0, Limit: 500, CycleStart: now.AddDate(0, 0, -3)}).ProjectedExhaust(now); ok {
		t.Error("零消耗不应有预测")
	}
}

// TestCursorAggregation 验证按日/按模型汇总。
func TestCursorAggregation(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	evs := []CursorEvent{
		{Time: time.Date(2026, 10, 14, 1, 0, 0, 0, loc), Model: "a", Requests: 1},
		{Time: time.Date(2026, 10, 14, 23, 0, 0, 0, loc), Model: "b", Requests: 2},
		{Time: time.Date(2026, 10, 13, 12, 0, 0, 0, loc), Model: "a", Requests: 1},
	}
	days := AggregateCursorByDay(evs, loc)
	if len(days) != 2 || days[0].Date != "2026-10-13" || days[1].Requests != 3 || days[1].Events != 2 {
		t.Errorf("按日: %+v", days)
	}
	models := AggregateCursorByModel(evs)
	if len(models) != 2 || models[0].Model != "a" && models[0].Requests != 2 {
		t.Errorf("按模型: %+v", models)
	}
	if SumRequests(evs) != 4 {
		t.Error("SumRequests")
	}
}

// TestKindLabel 验证类型中文名。
func TestKindLabel(t *testing.T) {
	if (CursorEvent{Kind: "USAGE_EVENT_KIND_INCLUDED_IN_PRO"}).KindLabel() != "套餐内" {
		t.Error("套餐内")
	}
	if (CursorEvent{Kind: "X"}).KindLabel() != "X" || (CursorEvent{}).KindLabel() != "-" {
		t.Error("兜底")
	}
}

// TestDeepSeekAggregation 验证账单汇总。
func TestDeepSeekAggregation(t *testing.T) {
	rows := []DeepSeekDaily{
		{Date: "2026-10-13", Model: "m1", CacheHit: 100, CacheMiss: 10, Output: 5, Requests: 2, Cost: 0.5},
		{Date: "2026-10-14", Model: "m1", CacheHit: 1, CacheMiss: 2, Output: 3, Requests: 1, Cost: 0.1},
		{Date: "2026-10-14", Model: "m2", Output: 7, Requests: 1, Cost: 0.2},
		{Date: "2026-09-30", Model: "m1", Cost: 9},
	}
	today := DeepSeekToday(rows, "2026-10-14")
	if today.Tokens != 13 || today.Requests != 2 || today.Cost < 0.299 || today.Cost > 0.301 {
		t.Errorf("今日: %+v", today)
	}
	if c := DeepSeekMonthCost(rows, "2026-10"); c < 0.799 || c > 0.801 {
		t.Errorf("本月: %v", c)
	}
	days := AggregateDeepSeekByDay(rows)
	if len(days) != 3 || days[0].Date != "2026-09-30" {
		t.Errorf("按日: %+v", days)
	}
	if (DeepSeekDaily{CacheHit: 1, CacheMiss: 2, Output: 3}).Tokens() != 6 {
		t.Error("Tokens")
	}
}

// TestSettingsNormalize 验证非法设置被修正。
func TestSettingsNormalize(t *testing.T) {
	d := DefaultSettings()
	if d.RefreshMinutes != 1 || d.TrayDisplay != TrayCursor || !d.NotifyEnabled {
		t.Errorf("默认值: %+v", d)
	}
	bad := Settings{RefreshMinutes: 0, TrayDisplay: "x", CursorAuthMode: "y", DeepSeekAuthMode: "", NotifyCursorPercent: []int{90, 0, 80, 80, 101}, NotifyDeepSeekBelow: -1, HistoryDays: 1}
	n := bad.Normalize()
	if n.RefreshMinutes != 1 || n.TrayDisplay != TrayCursor || n.CursorAuthMode != AuthAuto || n.DeepSeekAuthMode != AuthAuto {
		t.Errorf("枚举修正: %+v", n)
	}
	if len(n.NotifyCursorPercent) != 2 || n.NotifyCursorPercent[0] != 80 || n.NotifyCursorPercent[1] != 90 {
		t.Errorf("阈值去重排序: %v", n.NotifyCursorPercent)
	}
	if n.NotifyDeepSeekBelow != 0 || n.HistoryDays != 90 {
		t.Errorf("数值修正: %+v", n)
	}
	ok := Settings{RefreshMinutes: 15, TrayDisplay: TrayBoth, CursorAuthMode: AuthManual, DeepSeekAuthMode: AuthManual, HistoryDays: 30}.Normalize()
	if ok.RefreshMinutes != 15 || ok.TrayDisplay != TrayBoth || ok.CursorAuthMode != AuthManual || ok.HistoryDays != 30 {
		t.Errorf("合法值不应被改: %+v", ok)
	}
}
