// Package model 定义 AI Usage 的领域模型与纯计算逻辑（无 IO，便于单元测试）。
//
// @author ixxxxoooo
package model

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Cursor 计费口径（CursorUsage.Mode）。
const (
	// CursorModeRequests 是老计划：每周期固定次数（如 500 次），Used/Limit 单位是「次」。
	CursorModeRequests = "requests"
	// CursorModeUsage 是新计划/团队：按美元额度计，Used/Limit 单位是「美分」。
	CursorModeUsage = "usage"
)

// CursorUsage 是 Cursor 当前周期的用量快照，兼容老计划（按次数）与新计划/团队（按美元额度）。
//
// @author ixxxxoooo
type CursorUsage struct {
	Mode       string    `json:"mode,omitempty"`       // 计费口径，空值按老计划处理
	Used       int       `json:"used"`                 // 已用：次数（requests）或美分（usage）
	Limit      int       `json:"limit"`                // 周期总额度：次数或美分；0 表示没有固定上限
	Unlimited  bool      `json:"unlimited,omitempty"`  // 账号无固定上限（企业等）
	OnDemand   int       `json:"onDemand,omitempty"`   // 按需（超出套餐）已用美分
	CycleStart time.Time `json:"cycleStart"`           // 周期起点
	CycleEndAt time.Time `json:"cycleEndAt,omitempty"` // 周期终点（接口给出的精确重置时刻）
	Membership string    `json:"membership,omitempty"` // 套餐，如 pro / pro_plus / ultra / team / enterprise
	LimitType  string    `json:"limitType,omitempty"`  // user（个人额度）或 team（团队额度）
	Account    string    `json:"account,omitempty"`    // 账号标识（WorkOS 用户 ID）
	FetchedAt  time.Time `json:"fetchedAt"`            // 抓取时间
	Email      string    `json:"email,omitempty"`
}

// IsUsage 判断是否按美元额度计（新计划/团队）。
//
// 返回值：Mode 为 usage 返回 true。
func (u CursorUsage) IsUsage() bool { return u.Mode == CursorModeUsage }

// Amount 把额度数值（次数或美分）格式化成展示文案：老计划 "359 次"，新计划 "$16.72"。
//
// 参数 v：与 Used/Limit 同单位的数值。
// 返回值：带单位的文案。
func (u CursorUsage) Amount(v int) string {
	if u.IsUsage() {
		return fmt.Sprintf("$%.2f", float64(v)/100)
	}
	return fmt.Sprintf("%d 次", v)
}

// HasCap 判断是否有可计算比例的固定上限。
//
// 返回值：Limit>0 且不是无上限账号返回 true。
func (u CursorUsage) HasCap() bool { return u.Limit > 0 && !u.Unlimited }

// Remaining 返回剩余额度（不小于 0；单位同 Used）。
//
// 返回值：Limit-Used，最小为 0。
func (u CursorUsage) Remaining() int {
	if r := u.Limit - u.Used; r > 0 {
		return r
	}
	return 0
}

// Percent 返回已用比例 0~1（没有上限时为 0，超额时封顶 1）。
//
// 返回值：已用比例。
func (u CursorUsage) Percent() float64 {
	if !u.HasCap() {
		return 0
	}
	p := float64(u.Used) / float64(u.Limit)
	if p > 1 {
		return 1
	}
	if p < 0 {
		return 0
	}
	return p
}

// CycleEnd 返回周期重置时间：优先用接口给出的精确时刻，否则取周期起点后推一个月。
//
// 返回值：重置时间；都未知时返回零值。
func (u CursorUsage) CycleEnd() time.Time {
	if !u.CycleEndAt.IsZero() {
		return u.CycleEndAt
	}
	if u.CycleStart.IsZero() {
		return time.Time{}
	}
	return u.CycleStart.AddDate(0, 1, 0)
}

// UntilReset 返回距离重置还有多久（已过期或未知返回 0）。
//
// 参数 now：当前时间。
// 返回值：剩余时长。
func (u CursorUsage) UntilReset(now time.Time) time.Duration {
	end := u.CycleEnd()
	if end.IsZero() || !end.After(now) {
		return 0
	}
	return end.Sub(now)
}

// DaysUntilReset 返回距离重置还有多少天（向上取整，已过期返回 0）。
//
// 参数 now：当前时间。
// 返回值：天数。
func (u CursorUsage) DaysUntilReset(now time.Time) int {
	d := u.UntilReset(now)
	if d <= 0 {
		return 0
	}
	days := int(d / (24 * time.Hour))
	if d%(24*time.Hour) > 0 {
		days++
	}
	return days
}

// DailyAverage 返回本周期日均消耗（按已过去的天数，至少按 1 天算；单位同 Used）。
//
// 参数 now：当前时间。
// 返回值：日均消耗。
func (u CursorUsage) DailyAverage(now time.Time) float64 {
	if u.CycleStart.IsZero() {
		return 0
	}
	days := now.Sub(u.CycleStart).Hours() / 24
	if days < 1 {
		days = 1
	}
	return float64(u.Used) / days
}

// ProjectedExhaust 按日均速度预测额度耗尽时间。
//
// 参数 now：当前时间。
// 返回值：预测耗尽时间与是否有意义（已耗尽返回 now，没有上限或日均为 0 返回 false）。
func (u CursorUsage) ProjectedExhaust(now time.Time) (time.Time, bool) {
	if !u.HasCap() {
		return time.Time{}, false
	}
	if u.Used >= u.Limit {
		return now, true
	}
	avg := u.DailyAverage(now)
	if avg <= 0 {
		return time.Time{}, false
	}
	days := float64(u.Limit-u.Used) / avg
	return now.Add(time.Duration(days * float64(24*time.Hour))), true
}

// WillExhaustBeforeReset 判断按当前速度是否会在周期重置前用完。
//
// 参数 now：当前时间。
// 返回值：会提前用完返回 true。
func (u CursorUsage) WillExhaustBeforeReset(now time.Time) bool {
	t, ok := u.ProjectedExhaust(now)
	end := u.CycleEnd()
	return ok && !end.IsZero() && t.Before(end)
}

// CursorEvent 是 Cursor 一次请求的明细记录。
//
// @author ixxxxoooo
type CursorEvent struct {
	Key            string    `json:"key"`  // 去重键（时间+模型+会话+类型的摘要；不含 Token，进行中的事件 Token 会增长）
	Time           time.Time `json:"time"` // 请求时间
	Model          string    `json:"model"`
	Kind           string    `json:"kind"`     // 原始类型，如 USAGE_EVENT_KIND_INCLUDED_IN_PRO
	Requests       float64   `json:"requests"` // 本次计入的“次数”（老计划里高级模型可能 >1）
	InputTokens    int64     `json:"inputTokens"`
	OutputTokens   int64     `json:"outputTokens"`
	CacheWrite     int64     `json:"cacheWrite"`
	CacheRead      int64     `json:"cacheRead"`
	TotalCents     float64   `json:"totalCents"`   // 按 API 价折算的成本（美分）
	ChargedCents   float64   `json:"chargedCents"` // 实际扣费（美分）
	IsTokenBased   bool      `json:"isTokenBased"`
	ConversationID string    `json:"conversationId,omitempty"`
	Account        string    `json:"account,omitempty"` // 所属账号（WorkOS 用户 ID）；旧备份没有该字段
}

// EventKey 计算事件的稳定去重键。
//
// 接口没有事件 ID，用「时间(毫秒)+模型+会话+类型」代替。注意不能把 Token 数放进键里：
// Cursor 一次 Agent 运行里的事件在运行过程中 Token 会持续增长（时间戳不变），
// 若 Token 参与计算，每次刷新都会被当成新事件，造成重复累计。
//
// 参数 e：事件。
// 返回值：40 位十六进制摘要。
func EventKey(e CursorEvent) string {
	h := sha1.New()
	fmt.Fprintf(h, "%d|%s|%s|%s", e.Time.UnixMilli(), e.Model, e.ConversationID, e.Kind)
	return hex.EncodeToString(h.Sum(nil))
}

// KindLabel 把原始类型转成易读中文。
//
// 返回值：中文类型名。
func (e CursorEvent) KindLabel() string {
	switch e.Kind {
	case "USAGE_EVENT_KIND_INCLUDED_IN_PRO":
		return "套餐内"
	case "USAGE_EVENT_KIND_INCLUDED_IN_BUSINESS":
		return "团队套餐内"
	case "USAGE_EVENT_KIND_USAGE_BASED":
		return "按量付费"
	case "USAGE_EVENT_KIND_ERRORED_NOT_CHARGED":
		return "出错未扣费"
	case "USAGE_EVENT_KIND_FREE_CREDIT":
		return "免费额度"
	case "":
		return "-"
	}
	return e.Kind
}

// TotalTokens 返回该事件的全部 Token（输入+输出+缓存读写）。
//
// 返回值：Token 总数。
func (e CursorEvent) TotalTokens() int64 {
	return e.InputTokens + e.OutputTokens + e.CacheWrite + e.CacheRead
}

// CursorDayStat 是 Cursor 按日汇总。
type CursorDayStat struct {
	Date     string  `json:"date"` // 本地日期 YYYY-MM-DD
	Requests float64 `json:"requests"`
	Events   int     `json:"events"`
}

// CursorModelStat 是 Cursor 按模型汇总。
type CursorModelStat struct {
	Model    string  `json:"model"`
	Requests float64 `json:"requests"`
	Events   int     `json:"events"`
}

// SumRequests 汇总事件的“次数”。
//
// 参数 evs：事件列表。
// 返回值：次数合计。
func SumRequests(evs []CursorEvent) float64 {
	var s float64
	for _, e := range evs {
		s += e.Requests
	}
	return s
}

// AggregateCursorByDay 按本地日期汇总，结果按日期升序。
//
// 参数 evs：事件列表；loc：用于划分日期的时区。
// 返回值：按日统计。
func AggregateCursorByDay(evs []CursorEvent, loc *time.Location) []CursorDayStat {
	if loc == nil {
		loc = time.Local
	}
	m := map[string]*CursorDayStat{}
	for _, e := range evs {
		d := e.Time.In(loc).Format("2006-01-02")
		s := m[d]
		if s == nil {
			s = &CursorDayStat{Date: d}
			m[d] = s
		}
		s.Requests += e.Requests
		s.Events++
	}
	out := make([]CursorDayStat, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// AggregateCursorByModel 按模型汇总，结果按次数降序。
//
// 参数 evs：事件列表。
// 返回值：按模型统计。
func AggregateCursorByModel(evs []CursorEvent) []CursorModelStat {
	m := map[string]*CursorModelStat{}
	for _, e := range evs {
		s := m[e.Model]
		if s == nil {
			s = &CursorModelStat{Model: e.Model}
			m[e.Model] = s
		}
		s.Requests += e.Requests
		s.Events++
	}
	out := make([]CursorModelStat, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// DeepSeekBalance 是 DeepSeek 账户余额快照。
//
// @author ixxxxoooo
type DeepSeekBalance struct {
	Currency   string    `json:"currency"`
	Total      float64   `json:"total"`      // 总可用余额
	ToppedUp   float64   `json:"toppedUp"`   // 充值余额
	Granted    float64   `json:"granted"`    // 赠送余额
	TotalSpent float64   `json:"totalSpent"` // 累计已用金额（来自平台 total_costs，无 token 时为 0）
	HasSpent   bool      `json:"hasSpent"`   // TotalSpent 是否有效
	Available  bool      `json:"available"`  // 是否可用于调用
	Source     string    `json:"source"`     // apikey / platform
	FetchedAt  time.Time `json:"fetchedAt"`
}

// DeepSeekDaily 是 DeepSeek 一天一个模型的用量与费用（账单明细的最小单位）。
//
// @author ixxxxoooo
type DeepSeekDaily struct {
	Date      string  `json:"date"` // YYYY-MM-DD
	Model     string  `json:"model"`
	Currency  string  `json:"currency"`
	CacheHit  int64   `json:"cacheHit"`  // 缓存命中输入 Token
	CacheMiss int64   `json:"cacheMiss"` // 缓存未命中输入 Token
	Output    int64   `json:"output"`    // 输出 Token
	Requests  int64   `json:"requests"`  // 请求数
	Cost      float64 `json:"cost"`      // 当日该模型花费
}

// Tokens 返回该条记录的总 Token（输入+输出）。
//
// 返回值：Token 总数。
func (d DeepSeekDaily) Tokens() int64 { return d.CacheHit + d.CacheMiss + d.Output }

// DeepSeekDayStat 是 DeepSeek 按日汇总。
type DeepSeekDayStat struct {
	Date     string  `json:"date"`
	Tokens   int64   `json:"tokens"`
	Requests int64   `json:"requests"`
	Cost     float64 `json:"cost"`
}

// AggregateDeepSeekByDay 按日期汇总，结果按日期升序。
//
// 参数 rows：账单明细。
// 返回值：按日统计。
func AggregateDeepSeekByDay(rows []DeepSeekDaily) []DeepSeekDayStat {
	m := map[string]*DeepSeekDayStat{}
	for _, r := range rows {
		s := m[r.Date]
		if s == nil {
			s = &DeepSeekDayStat{Date: r.Date}
			m[r.Date] = s
		}
		s.Tokens += r.Tokens()
		s.Requests += r.Requests
		s.Cost += r.Cost
	}
	out := make([]DeepSeekDayStat, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// DeepSeekToday 汇总指定日期的 Token、请求数与花费。
//
// 参数 rows：账单明细；date：YYYY-MM-DD。
// 返回值：当日统计（无数据时全 0）。
func DeepSeekToday(rows []DeepSeekDaily, date string) DeepSeekDayStat {
	s := DeepSeekDayStat{Date: date}
	for _, r := range rows {
		if r.Date == date {
			s.Tokens += r.Tokens()
			s.Requests += r.Requests
			s.Cost += r.Cost
		}
	}
	return s
}

// DeepSeekMonthCost 汇总指定月份（YYYY-MM）的总花费。
//
// 参数 rows：账单明细；month：YYYY-MM。
// 返回值：当月花费。
func DeepSeekMonthCost(rows []DeepSeekDaily, month string) float64 {
	var c float64
	for _, r := range rows {
		if len(r.Date) >= 7 && r.Date[:7] == month {
			c += r.Cost
		}
	}
	return c
}

// AntigravityModel 是 Antigravity 里一个模型的额度状态。
//
// @author ixxxxoooo
type AntigravityModel struct {
	Label     string    `json:"label"`     // 展示名，如 "Claude Opus 4.6 (Thinking)"
	ID        string    `json:"id"`        // 模型标识
	Disabled  bool      `json:"disabled"`  // 当前套餐不可用
	HasQuota  bool      `json:"hasQuota"`  // 是否有额度信息
	Remaining float64   `json:"remaining"` // 剩余比例 0~1
	ResetAt   time.Time `json:"resetAt"`   // 额度重置时刻（零值未知）
}

// Used 返回已用比例 0~1（没有额度信息时为 0）。
//
// 返回值：已用比例。
func (m AntigravityModel) Used() float64 {
	if !m.HasQuota {
		return 0
	}
	return 1 - m.Remaining
}

// AntigravityQuota 是 Antigravity（反重力）的额度快照。
//
// @author ixxxxoooo
type AntigravityQuota struct {
	Plan      string             `json:"plan"` // 套餐，如 "Google AI Pro"
	Models    []AntigravityModel `json:"models"`
	FetchedAt time.Time          `json:"fetchedAt"`
}

// AntigravityPool 是共用同一份额度的一组模型（剩余比例与重置时间相同的模型，如 Gemini Flash 的 High/Medium/Low）。
//
// @author ixxxxoooo
type AntigravityPool struct {
	Name      string    // 组名：各模型厂商名去重后用 " / " 连接，如 "Claude / GPT-OSS"
	Labels    []string  // 组内模型的完整展示名
	Remaining float64   // 剩余比例
	ResetAt   time.Time // 重置时刻
}

// Used 返回已用比例 0~1。
//
// 返回值：已用比例。
func (p AntigravityPool) Used() float64 { return 1 - p.Remaining }

// Pools 把有额度信息且可用的模型按（剩余比例、重置时间）归并成额度池，按剩余比例升序（最紧张的在前）。
//
// 返回值：额度池列表；没有可用额度返回 nil。
func (q AntigravityQuota) Pools() []AntigravityPool {
	type key struct {
		rem   int64 // 万分位，避免浮点比较误差
		reset int64 // 分钟，容忍同一组里秒级的差异
	}
	idx := map[key]int{}
	var pools []AntigravityPool
	for _, m := range q.Models {
		if m.Disabled || !m.HasQuota {
			continue
		}
		k := key{int64(m.Remaining*10000 + 0.5), m.ResetAt.Unix() / 60}
		i, ok := idx[k]
		if !ok {
			i = len(pools)
			idx[k] = i
			pools = append(pools, AntigravityPool{Remaining: m.Remaining, ResetAt: m.ResetAt})
		}
		pools[i].Labels = append(pools[i].Labels, m.Label)
	}
	for i := range pools {
		var fams []string
		seen := map[string]bool{}
		for _, l := range pools[i].Labels {
			f := antigravityFamily(l)
			if !seen[f] {
				seen[f] = true
				fams = append(fams, f)
			}
		}
		pools[i].Name = strings.Join(fams, " / ")
	}
	sort.SliceStable(pools, func(i, j int) bool { return pools[i].Remaining < pools[j].Remaining })
	return pools
}

// antigravityFamily 从模型展示名里取厂商/系列名（第一个单词）：Claude Opus 4.6 → Claude，Gemini 3.8 Flash → Gemini。
func antigravityFamily(label string) string {
	if f := strings.Fields(label); len(f) > 0 {
		return f[0]
	}
	return label
}

// Tightest 返回剩余比例最低的额度池。
//
// 返回值：额度池与是否存在。
func (q AntigravityQuota) Tightest() (AntigravityPool, bool) {
	ps := q.Pools()
	if len(ps) == 0 {
		return AntigravityPool{}, false
	}
	return ps[0], true
}

// Settings 是用户可调的应用设置（会随备份导出；凭据不在其中）。
//
// @author ixxxxoooo
type Settings struct {
	RefreshMinutes      int     `json:"refreshMinutes"`      // 自动刷新间隔（分钟）
	TrayDisplay         string  `json:"trayDisplay"`         // icon / cursor / deepseek / both
	CursorAuthMode      string  `json:"cursorAuthMode"`      // auto / manual
	DeepSeekAuthMode    string  `json:"deepseekAuthMode"`    // auto / manual
	NotifyEnabled       bool    `json:"notifyEnabled"`       // 是否开启阈值提醒
	NotifyCursorPercent []int   `json:"notifyCursorPercent"` // Cursor 提醒阈值（百分比）
	NotifyDeepSeekBelow float64 `json:"notifyDeepseekBelow"` // DeepSeek 余额低于该值提醒（0 关闭）
	HistoryDays         int     `json:"historyDays"`         // DeepSeek 账单拉取月数相关的回溯天数
	AntigravityOff      bool    `json:"antigravityOff"`      // 关闭 Antigravity 额度查询（默认开启；用「关闭」而非「开启」是为了让旧备份/旧设置默认保持开启）
}

// 托盘显示模式常量。
const (
	TrayIcon     = "icon"
	TrayCursor   = "cursor"
	TrayDeepSeek = "deepseek"
	TrayBoth     = "both"
)

// 凭据获取方式常量。
const (
	AuthAuto   = "auto"
	AuthManual = "manual"
)

// DefaultSettings 返回默认设置。
//
// 返回值：带合理默认值的 Settings。
func DefaultSettings() Settings {
	return Settings{
		RefreshMinutes:      1,
		TrayDisplay:         TrayCursor,
		CursorAuthMode:      AuthAuto,
		DeepSeekAuthMode:    AuthAuto,
		NotifyEnabled:       true,
		NotifyCursorPercent: []int{80, 90},
		NotifyDeepSeekBelow: 5,
		HistoryDays:         90,
	}
}

// Normalize 把设置里的非法值修正为合法值（导入或读取旧数据时调用）。
//
// 返回值：修正后的副本。
func (s Settings) Normalize() Settings {
	d := DefaultSettings()
	if s.RefreshMinutes < 1 || s.RefreshMinutes > 720 {
		s.RefreshMinutes = d.RefreshMinutes
	}
	switch s.TrayDisplay {
	case TrayIcon, TrayCursor, TrayDeepSeek, TrayBoth:
	default:
		s.TrayDisplay = d.TrayDisplay
	}
	if s.CursorAuthMode != AuthAuto && s.CursorAuthMode != AuthManual {
		s.CursorAuthMode = d.CursorAuthMode
	}
	if s.DeepSeekAuthMode != AuthAuto && s.DeepSeekAuthMode != AuthManual {
		s.DeepSeekAuthMode = d.DeepSeekAuthMode
	}
	var ps []int
	seen := map[int]bool{}
	for _, p := range s.NotifyCursorPercent {
		if p >= 1 && p <= 100 && !seen[p] {
			seen[p] = true
			ps = append(ps, p)
		}
	}
	sort.Ints(ps)
	s.NotifyCursorPercent = ps
	if s.NotifyDeepSeekBelow < 0 {
		s.NotifyDeepSeekBelow = 0
	}
	if s.HistoryDays < 7 || s.HistoryDays > 3650 {
		s.HistoryDays = d.HistoryDays
	}
	return s
}
