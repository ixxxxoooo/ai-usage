// Package service 负责调度刷新、状态机、阈值提醒，并把结果持久化。
//
// @author ygw
package service

import (
	"context"
	"errors"
	"net"
	"net/url"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/provider/cursor"
	"github.com/ixxxxoooo/ai-usage/internal/provider/deepseek"
)

// Status 是单个数据源的状态。
type Status string

// 状态取值。
const (
	StatusIdle         Status = "idle"         // 尚未刷新
	StatusLoading      Status = "loading"      // 刷新中
	StatusOK           Status = "ok"           // 正常
	StatusNoCreds      Status = "nocreds"      // 没有凭据
	StatusUnauthorized Status = "unauthorized" // 凭据过期/无效
	StatusOffline      Status = "offline"      // 网络不通
	StatusRateLimited  Status = "ratelimited"  // 被限流/拦截
	StatusError        Status = "error"        // 其他错误
)

// ProviderState 是数据源通用的状态信息。
//
// @author ygw
type ProviderState struct {
	Status    Status    // 当前状态
	Message   string    // 面向用户的中文说明（失败时有值）
	UpdatedAt time.Time // 最近一次成功时间
	LastTry   time.Time // 最近一次尝试时间
	NextTry   time.Time // 下次计划尝试时间（退避后）
	Failures  int       // 连续失败次数
}

// Stale 判断数据是否是旧数据（当前状态不是 OK，但有历史数据）。
//
// 返回值：状态非 OK/Loading 且曾经成功过返回 true。
func (p ProviderState) Stale() bool {
	return p.Status != StatusOK && p.Status != StatusLoading && !p.UpdatedAt.IsZero()
}

// CursorState 是 Cursor 在界面上展示所需的全部状态。
type CursorState struct {
	ProviderState
	Usage      model.CursorUsage
	HasUsage   bool
	EventCount int    // 本地已存明细条数
	CredSource string // 凭据来源说明：本机 Cursor / 手动 Cookie
	EventsErr  string // 明细同步失败原因（不影响次数显示）
}

// DeepSeekState 是 DeepSeek 在界面上展示所需的全部状态。
type DeepSeekState struct {
	ProviderState
	Balance    model.DeepSeekBalance
	HasBalance bool
	Today      model.DeepSeekDayStat // 今日 Token/请求/花费（需平台 token）
	MonthCost  float64               // 本月已用金额
	Month      string                // 本月 YYYY-MM
	HasBill    bool                  // 是否拿到了账单数据
	TokenSrc   string                // 平台 token 来源说明（Chrome Default / 手动粘贴 / 未配置）
	HasAPIKey  bool                  // 是否配置了 API Key
	BillErr    string                // 账单同步失败原因（不影响余额显示）
}

// State 是提供给 UI 的整体快照（值拷贝，可安全读取）。
type State struct {
	Cursor     CursorState
	DeepSeek   DeepSeekState
	Settings   model.Settings
	Refreshing bool
}

// classify 把底层错误归类为状态与中文说明。
//
// 参数 err：底层错误。
// 返回值：状态、面向用户的说明。
func classify(err error) (Status, string) {
	switch {
	case err == nil:
		return StatusOK, ""
	case errors.Is(err, cursor.ErrNoCredentials), errors.Is(err, deepseek.ErrNoCredentials):
		return StatusNoCreds, err.Error()
	case errors.Is(err, cursor.ErrUnauthorized), errors.Is(err, deepseek.ErrUnauthorized):
		return StatusUnauthorized, err.Error()
	case errors.Is(err, cursor.ErrRateLimited), errors.Is(err, deepseek.ErrRateLimited):
		return StatusRateLimited, err.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return StatusOffline, "请求超时，请检查网络"
	}
	var ne net.Error
	var ue *url.Error
	var de *net.DNSError
	if errors.As(err, &ne) || errors.As(err, &ue) || errors.As(err, &de) {
		return StatusOffline, "网络不可用，请检查连接后重试"
	}
	return StatusError, err.Error()
}

// StatusText 返回状态的简短中文名。
//
// 返回值：中文状态名。
func (s Status) StatusText() string {
	switch s {
	case StatusIdle:
		return "未刷新"
	case StatusLoading:
		return "刷新中"
	case StatusOK:
		return "正常"
	case StatusNoCreds:
		return "未登录"
	case StatusUnauthorized:
		return "登录过期"
	case StatusOffline:
		return "离线"
	case StatusRateLimited:
		return "被限流"
	}
	return "出错"
}

// backoff 计算连续失败后的下次尝试间隔：基础间隔 × 2^失败次数，上限 30 分钟。
//
// 参数 base：正常刷新间隔；failures：连续失败次数；retryAfter：服务端建议等待时间（可为 0）。
// 返回值：等待时长。
func backoff(base time.Duration, failures int, retryAfter time.Duration) time.Duration {
	d := base
	for i := 0; i < failures && d < 30*time.Minute; i++ {
		d *= 2
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	if retryAfter > d {
		d = retryAfter
	}
	return d
}
