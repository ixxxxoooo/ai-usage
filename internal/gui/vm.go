// Package gui 是 AI Usage 的原生界面：托盘浮窗、大面板与设置页。
//
// 本文件是「视图模型」层：把 service.State 转成界面直接展示的文案与数值，
// 全部是纯函数，便于单元测试，也让视图代码保持简洁。
//
// @author ixxxxoooo
package gui

import (
	"fmt"
	"strings"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/service"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// 额度提示级别。
const (
	LevelOK     = 0 // 正常
	LevelWarn   = 1 // 用量 ≥ 80%
	LevelDanger = 2 // 用量 ≥ 95%
)

// CursorVM 是 Cursor 卡片的展示数据。
//
// @author ixxxxoooo
type CursorVM struct {
	Has         bool    // 是否有用量数据
	Used        string  // 已用，如 "359"（老计划）或 "$16.72"（新计划）
	Limit       string  // 总量，如 "500" / "$20.00" / "无上限"
	RingMain    string  // 环中间的大字：老计划为已用次数，新计划为已用百分比
	RingSub     string  // 环中间的小字，如 "/ 500" 或 "已用"
	RingSubFull string  // 概览页环里的小字，如 "/ 500 次"
	Plan        string  // 套餐说明，如 "Pro · 个人额度"；未知为空
	OnDemand    string  // 按需（超出套餐）已用，如 "$1.20"；没有为空
	IsUsage     bool    // 是否按美元额度计（新计划/团队）
	Remain      string  // 剩余文案，如 "剩余 141 次"
	Frac        float64 // 已用比例 0~1
	Percent     string  // 如 "72%"
	Level       int     // 提示级别
	Reset       string  // 如 "10月16日重置 · 2 天后"
	Average     string  // 如 "日均 12.0 次"
	Exhaust     string  // 预测耗尽文案；为空表示无需提示
	WarnExhaust bool    // 是否会在重置前用完
	Status      service.Status
	StatusText  string
	Message     string // 失败说明（空表示正常）
	Updated     string // 如 "3 分钟前"
	Stale       bool
	Source      string
	Events      string // 明细条数文案
}

// BuildCursorVM 构造 Cursor 卡片展示数据。
//
// 参数 s：Cursor 状态；now：当前时间。
// 返回值：CursorVM。
func BuildCursorVM(s service.CursorState, now time.Time) CursorVM {
	vm := CursorVM{
		Has: s.HasUsage, Status: s.Status, StatusText: s.Status.StatusText(),
		Message: s.Message, Stale: s.Stale(), Source: s.CredSource,
		Updated: util.FormatRelative(s.UpdatedAt, now),
		Events:  fmt.Sprintf("%s 条明细", util.FormatInt(int64(s.EventCount))),
	}
	if !s.HasUsage {
		return vm
	}
	u := s.Usage
	vm.IsUsage = u.IsUsage()
	vm.Plan = planLabel(u)
	if u.OnDemand > 0 {
		vm.OnDemand = fmt.Sprintf("$%.2f", float64(u.OnDemand)/100)
	}
	if vm.IsUsage {
		vm.Used = fmt.Sprintf("$%.2f", float64(u.Used)/100)
		switch {
		case u.HasCap():
			vm.Limit = fmt.Sprintf("$%.2f", float64(u.Limit)/100)
			vm.Remain = "剩余 " + u.Amount(u.Remaining())
		case u.Unlimited:
			vm.Limit, vm.Remain = "无上限", "无固定上限"
		default:
			vm.Limit, vm.Remain = "—", "未设固定上限"
		}
		vm.Average = fmt.Sprintf("日均 %s", u.Amount(int(u.DailyAverage(now)+0.5)))
	} else {
		vm.Used = fmt.Sprintf("%d", u.Used)
		vm.Limit = fmt.Sprintf("%d", u.Limit)
		vm.Remain = fmt.Sprintf("剩余 %d 次", u.Remaining())
		vm.Average = fmt.Sprintf("日均 %.1f 次", u.DailyAverage(now))
	}
	vm.Frac = u.Percent()
	vm.Percent = fmt.Sprintf("%d%%", int(vm.Frac*100+0.5))
	vm.Level = LevelOK
	switch {
	case vm.Frac >= 0.95:
		vm.Level = LevelDanger
	case vm.Frac >= 0.8:
		vm.Level = LevelWarn
	}
	vm.Reset = ResetText(u, now)
	switch {
	case !vm.IsUsage:
		vm.RingMain, vm.RingSub, vm.RingSubFull = vm.Used, "/ "+vm.Limit, "/ "+vm.Limit+" 次"
	case u.HasCap():
		vm.RingMain, vm.RingSub, vm.RingSubFull = vm.Percent, "已用", "已用 · 共 "+vm.Limit
	default:
		vm.RingMain, vm.RingSub, vm.RingSubFull = vm.Used, "已用", "已用 · "+vm.Limit
	}
	if u.HasCap() && u.Used >= u.Limit {
		vm.Exhaust = "本周期额度已用完"
		vm.WarnExhaust = true
	} else if t, ok := u.ProjectedExhaust(now); ok && u.WillExhaustBeforeReset(now) {
		vm.Exhaust = fmt.Sprintf("按当前速度约 %d月%d日 用完", int(t.Month()), t.Day())
		vm.WarnExhaust = true
	}
	return vm
}

// DeepSeekVM 是 DeepSeek 卡片的展示数据。
//
// @author ixxxxoooo
type DeepSeekVM struct {
	HasBalance  bool
	Balance     string // 剩余余额，如 "¥12.61"
	Breakdown   string // 如 "充值 ¥12.61 · 赠送 ¥0.00"
	LowBalance  bool   // 低于提醒阈值
	HasBill     bool
	MonthLabel  string // 如 "10 月"
	MonthCost   string // 本月已用
	TotalSpent  string // 累计已用（有才显示）
	TodayTokens string // 今日 Token
	TodayCost   string // 今日花费
	TodayReqs   string // 今日请求数
	Status      service.Status
	StatusText  string
	Message     string
	BillErr     string
	TokenSrc    string
	Updated     string
	Stale       bool
	NeedToken   bool // 余额可用但缺少平台 token，无法显示账单
}

// BuildDeepSeekVM 构造 DeepSeek 卡片展示数据。
//
// 参数 s：DeepSeek 状态；st：设置（用于低余额阈值）；now：当前时间。
// 返回值：DeepSeekVM。
func BuildDeepSeekVM(s service.DeepSeekState, st model.Settings, now time.Time) DeepSeekVM {
	vm := DeepSeekVM{
		HasBalance: s.HasBalance, HasBill: s.HasBill, Status: s.Status, StatusText: s.Status.StatusText(),
		Message: s.Message, BillErr: s.BillErr, TokenSrc: s.TokenSrc,
		Updated: util.FormatRelative(s.UpdatedAt, now), Stale: s.Stale(),
	}
	if s.HasBalance {
		b := s.Balance
		vm.Balance = util.FormatMoney(b.Currency, b.Total)
		vm.Breakdown = fmt.Sprintf("充值 %s · 赠送 %s", util.FormatMoney(b.Currency, b.ToppedUp), util.FormatMoney(b.Currency, b.Granted))
		if b.HasSpent {
			vm.TotalSpent = util.FormatMoney(b.Currency, b.TotalSpent)
		}
		vm.LowBalance = st.NotifyDeepSeekBelow > 0 && b.Total < st.NotifyDeepSeekBelow
	}
	cur := s.Balance.Currency
	if cur == "" {
		cur = "CNY"
	}
	if s.HasBill {
		if len(s.Month) == 7 {
			vm.MonthLabel = fmt.Sprintf("%s 月", s.Month[5:])
			if s.Month[5] == '0' {
				vm.MonthLabel = fmt.Sprintf("%s 月", s.Month[6:])
			}
		}
		vm.MonthCost = util.FormatMoney(cur, s.MonthCost)
		vm.TodayTokens = util.FormatTokens(s.Today.Tokens)
		vm.TodayCost = util.FormatMoney(cur, s.Today.Cost)
		vm.TodayReqs = util.FormatInt(s.Today.Requests)
	}
	vm.NeedToken = s.HasBalance && !s.HasBill
	return vm
}

// AntigravityPoolVM 是 Antigravity 一组共用额度的模型的展示数据。
//
// @author ixxxxoooo
type AntigravityPoolVM struct {
	Name   string  // 组名，如 "Claude / GPT-OSS"
	Models string  // 组内模型，如 "Claude Opus 4.6 (Thinking)、GPT-OSS 120B (Medium)"
	Remain string  // 如 "剩余 72%"
	Frac   float64 // 已用比例 0~1
	Level  int     // 提示级别
	Reset  string  // 重置说明；额度充足时为 "额度充足"
}

// AntigravityModelVM 是 Antigravity 单个模型的展示数据。
//
// @author ixxxxoooo
type AntigravityModelVM struct {
	Label       string
	Remain      string // 如 "72%"；不可用时为 "不可用"
	Frac        float64
	Level       int
	Reset       string
	Unavailable bool
}

// AntigravityVM 是 Antigravity 卡片与页面的展示数据。
//
// @author ixxxxoooo
type AntigravityVM struct {
	Disabled   bool // 用户关闭了查询，界面不显示
	Has        bool // 是否有额度数据
	Plan       string
	Pools      []AntigravityPoolVM
	Models     []AntigravityModelVM
	Summary    string // 一句话概括，如 "最低剩余 72%（Gemini）"
	Status     service.Status
	StatusText string
	Message    string
	Updated    string
	Stale      bool
}

// antigravityResetText 生成某个额度的重置说明。
//
// 剩余 100% 时服务端给的是「现在 + 滚动窗口」的占位时刻，没有意义，直接说额度充足。
//
// 参数 remaining：剩余比例；at：重置时刻；now：当前时间。
// 返回值：说明文案。
func antigravityResetText(remaining float64, at, now time.Time) string {
	if remaining >= 0.999 {
		return "额度充足"
	}
	if at.IsZero() {
		return "重置时间未知"
	}
	at = at.In(now.Location())
	left := at.Sub(now)
	if left <= 0 {
		return "已到重置时间"
	}
	clock := fmt.Sprintf("%02d:%02d", at.Hour(), at.Minute())
	if at.YearDay() != now.YearDay() || at.Year() != now.Year() {
		clock = fmt.Sprintf("%d/%d %s", int(at.Month()), at.Day(), clock)
	}
	return fmt.Sprintf("%s 重置 · 还剩 %s", clock, util.FormatCountdown(left))
}

// levelOfUsed 把已用比例转成提示级别。
func levelOfUsed(used float64) int {
	switch {
	case used >= 0.95:
		return LevelDanger
	case used >= 0.8:
		return LevelWarn
	}
	return LevelOK
}

// BuildAntigravityVM 构造 Antigravity 展示数据。
//
// 参数 s：Antigravity 状态；now：当前时间。
// 返回值：AntigravityVM。
func BuildAntigravityVM(s service.AntigravityState, now time.Time) AntigravityVM {
	vm := AntigravityVM{
		Disabled: s.Disabled, Has: s.HasQuota, Status: s.Status, StatusText: s.Status.StatusText(),
		Message: s.Message, Updated: util.FormatRelative(s.UpdatedAt, now), Stale: s.Stale(),
	}
	if !s.HasQuota {
		return vm
	}
	q := s.Quota
	vm.Plan = q.Plan
	for _, p := range q.Pools() {
		used := p.Used()
		vm.Pools = append(vm.Pools, AntigravityPoolVM{
			Name: p.Name, Models: strings.Join(p.Labels, "、"),
			Remain: fmt.Sprintf("剩余 %d%%", int(p.Remaining*100+0.5)),
			Frac:   used, Level: levelOfUsed(used), Reset: antigravityResetText(p.Remaining, p.ResetAt, now),
		})
	}
	for _, m := range q.Models {
		mv := AntigravityModelVM{Label: m.Label, Frac: m.Used(), Level: levelOfUsed(m.Used())}
		switch {
		case m.Disabled:
			mv.Unavailable, mv.Remain, mv.Reset = true, "不可用", "当前套餐暂不可用"
		case !m.HasQuota:
			mv.Remain, mv.Reset = "—", "没有额度信息"
		default:
			mv.Remain = fmt.Sprintf("%d%%", int(m.Remaining*100+0.5))
			mv.Reset = antigravityResetText(m.Remaining, m.ResetAt, now)
		}
		vm.Models = append(vm.Models, mv)
	}
	if len(vm.Pools) > 0 {
		low := vm.Pools[0]
		vm.Summary = fmt.Sprintf("最低%s（%s）", low.Remain, low.Name)
	}
	return vm
}

// TrayTitle 计算托盘图标旁边显示的文字。
//
// 参数 st：整体状态。
// 返回值：文字；图标模式或没有数据时为空。
func TrayTitle(st service.State) string {
	cur, ds := "", ""
	if st.Cursor.HasUsage {
		cv := BuildCursorVM(st.Cursor, time.Now())
		cur = cv.Used + "/" + cv.Limit
		if cv.IsUsage && !st.Cursor.Usage.HasCap() {
			cur = cv.Used
		}
	}
	if st.DeepSeek.HasBalance {
		b := st.DeepSeek.Balance
		ds = util.FormatMoney(b.Currency, b.Total)
	}
	switch st.Settings.TrayDisplay {
	case model.TrayCursor:
		return cur
	case model.TrayDeepSeek:
		return ds
	case model.TrayBoth:
		switch {
		case cur != "" && ds != "":
			return cur + " · " + ds
		case cur != "":
			return cur
		}
		return ds
	}
	return ""
}

// TrayToolTip 计算托盘图标的悬停提示。
//
// 参数 st：整体状态；now：当前时间。
// 返回值：多行提示文案。
func TrayToolTip(st service.State, now time.Time) string {
	cur := BuildCursorVM(st.Cursor, now)
	ds := BuildDeepSeekVM(st.DeepSeek, st.Settings, now)
	ag := BuildAntigravityVM(st.Antigravity, now)
	out := "AI Usage"
	if cur.Has {
		out += fmt.Sprintf("\nCursor：%s/%s（%s）", cur.Used, cur.Limit, cur.Remain)
	} else {
		out += "\nCursor：" + cur.StatusText
	}
	if ds.HasBalance {
		out += "\nDeepSeek：余额 " + ds.Balance
	} else {
		out += "\nDeepSeek：" + ds.StatusText
	}
	if !ag.Disabled {
		switch {
		case ag.Has && ag.Summary != "":
			out += "\nAntigravity：" + ag.Summary
		case ag.Has:
			out += "\nAntigravity：已同步"
		default:
			out += "\nAntigravity：" + ag.StatusText
		}
	}
	return out
}

// StatusTone 把状态映射为展示色调：ok / warn / bad / muted。
//
// 参数 s：状态；stale：是否在展示旧数据。
// 返回值：色调名。
func StatusTone(s service.Status, stale bool) string {
	switch s {
	case service.StatusOK:
		return "ok"
	case service.StatusLoading, service.StatusIdle:
		return "muted"
	case service.StatusNoCreds, service.StatusUnauthorized, service.StatusError:
		return "bad"
	}
	if stale {
		return "warn"
	}
	return "warn"
}

// RefreshedText 返回底部栏的「更新于…」文案。
//
// 参数 st：整体状态；now：当前时间。
// 返回值：文案。
func RefreshedText(st service.State, now time.Time) string {
	if st.Refreshing {
		return "正在刷新…"
	}
	var latest time.Time
	for _, t := range []time.Time{st.Cursor.UpdatedAt, st.DeepSeek.UpdatedAt, st.Antigravity.UpdatedAt} {
		if t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return "尚未获取数据"
	}
	return "更新于 " + util.FormatRelative(latest, now)
}

// ResetText 生成精确到分钟的重置文案，如 "10/16 10:24 重置 · 还剩 12时5分"。
//
// 重置时刻取接口给出的精确值（取不到时用周期起点 + 1 个月），按本机时区显示。
//
// 参数 u：用量；now：当前时间。
// 返回值：文案；重置时刻未知返回空串。
func ResetText(u model.CursorUsage, now time.Time) string {
	end := u.CycleEnd()
	if end.IsZero() {
		return ""
	}
	end = end.In(now.Location())
	left := u.UntilReset(now)
	if left <= 0 {
		return fmt.Sprintf("%d/%d %02d:%02d 已到重置时间", int(end.Month()), end.Day(), end.Hour(), end.Minute())
	}
	return fmt.Sprintf("%d/%d %02d:%02d 重置 · 还剩 %s", int(end.Month()), end.Day(), end.Hour(), end.Minute(), util.FormatCountdown(left))
}

// planLabel 生成套餐说明，如 "Pro · 个人额度"、"Team · 团队额度"。
func planLabel(u model.CursorUsage) string {
	names := map[string]string{"free": "Free", "free_trial": "试用", "pro": "Pro", "pro_plus": "Pro+", "ultra": "Ultra", "business": "Business", "team": "Team", "enterprise": "Enterprise"}
	name := names[u.Membership]
	if name == "" {
		name = u.Membership
	}
	scope := ""
	switch u.LimitType {
	case "team":
		scope = "团队额度"
	case "user":
		scope = "个人额度"
	}
	switch {
	case name != "" && scope != "":
		return name + " · " + scope
	case name != "":
		return name
	}
	return scope
}
