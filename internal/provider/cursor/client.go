package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/httpx"
	"github.com/ixxxxoooo/ai-usage/internal/model"
)

// DefaultBaseURL 是 Cursor 网站地址。
const DefaultBaseURL = "https://cursor.com"

// Client 调用 Cursor dashboard 的只读接口。
//
// @author ixxxxoooo
type Client struct {
	BaseURL  string       // 默认 https://cursor.com，测试时指向假服务
	HTTP     *http.Client // 为空则使用 30 秒超时的默认客户端
	PageSize int          // 明细分页大小，默认 1000
	MaxPages int          // 明细最大页数（防御死循环），默认 200
}

// NewClient 创建默认客户端。
//
// 返回值：*Client。
func NewClient() *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: httpx.NewClient(30 * time.Second), PageSize: 1000, MaxPages: 200}
}

// base 返回去掉末尾斜杠的基础地址。
func (c *Client) base() string {
	b := c.BaseURL
	if b == "" {
		b = DefaultBaseURL
	}
	return strings.TrimRight(b, "/")
}

// httpClient 返回可用的 HTTP 客户端。
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return httpx.NewClient(30 * time.Second)
}

// newRequest 构造带凭据与浏览器头的请求。
func (c *Client) newRequest(method, path string, body []byte, cred Credentials) (*http.Request, error) {
	var rd *bytes.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	var req *http.Request
	var err error
	if rd != nil {
		req, err = http.NewRequest(method, c.base()+path, rd)
	} else {
		req, err = http.NewRequest(method, c.base()+path, nil)
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", cred.Cookie())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httpx.BrowserUA)
	req.Header.Set("Origin", "https://cursor.com") // 该站对 POST 做 CSRF 校验，需要匹配的 Origin
	req.Header.Set("Referer", "https://cursor.com/dashboard")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// checkStatus 把 HTTP 状态映射成预定义错误。
func checkStatus(r *httpx.Response) error {
	switch {
	case r.Status >= 200 && r.Status < 300:
		if httpx.IsHTMLBlock(r.Body) {
			return fmt.Errorf("%w：返回了网页而非 JSON", ErrBadResponse)
		}
		return nil
	case r.Status == http.StatusUnauthorized:
		return ErrUnauthorized
	case r.Status == http.StatusForbidden:
		return ErrForbidden
	case r.Status == http.StatusTooManyRequests:
		return &RateLimitError{RetryAfter: r.RetryAfter}
	}
	return fmt.Errorf("Cursor 接口错误：HTTP %d", r.Status)
}

// RateLimitError 表示被限流，附带建议的重试等待时间；errors.Is(err, ErrRateLimited) 为真。
type RateLimitError struct{ RetryAfter time.Duration }

// Error 实现 error。
func (e *RateLimitError) Error() string { return ErrRateLimited.Error() }

// Is 让 errors.Is 能匹配 ErrRateLimited。
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// usageResponse 是 /api/usage 的响应（只取老计划用到的 gpt-4 项）。
type usageResponse struct {
	GPT4 *struct {
		NumRequests      *int `json:"numRequests"`
		NumRequestsTotal *int `json:"numRequestsTotal"`
		MaxRequestUsage  *int `json:"maxRequestUsage"`
	} `json:"gpt-4"`
	StartOfMonth string `json:"startOfMonth"`
}

// ParseUsage 解析 /api/usage 响应体。
//
// 参数 body：响应 JSON；now：用于填写抓取时间。
// 返回值：用量快照；缺少 gpt-4 项返回 ErrBadResponse。
func ParseUsage(body []byte, now time.Time) (model.CursorUsage, error) {
	var r usageResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return model.CursorUsage{}, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	if r.GPT4 == nil {
		return model.CursorUsage{}, fmt.Errorf("%w：缺少 gpt-4 用量项", ErrBadResponse)
	}
	u := model.CursorUsage{Mode: model.CursorModeRequests, FetchedAt: now}
	switch {
	case r.GPT4.NumRequestsTotal != nil:
		u.Used = *r.GPT4.NumRequestsTotal
	case r.GPT4.NumRequests != nil:
		u.Used = *r.GPT4.NumRequests
	}
	if r.GPT4.MaxRequestUsage != nil && *r.GPT4.MaxRequestUsage > 0 {
		u.Limit = *r.GPT4.MaxRequestUsage
	}
	if r.StartOfMonth != "" {
		if t, err := time.Parse(time.RFC3339, r.StartOfMonth); err == nil {
			u.CycleStart = t
		}
	}
	return u, nil
}

// summaryPlan 是 usage-summary 里的一项额度（单位：美分）。
type summaryPlan struct {
	Enabled bool       `json:"enabled"`
	Used    *flexFloat `json:"used"`
	Limit   *flexFloat `json:"limit"`
}

// summaryResponse 是 /api/usage-summary 的响应（新计划/团队按美元额度计）。
type summaryResponse struct {
	BillingCycleStart string `json:"billingCycleStart"`
	BillingCycleEnd   string `json:"billingCycleEnd"`
	MembershipType    string `json:"membershipType"`
	LimitType         string `json:"limitType"`
	IsUnlimited       bool   `json:"isUnlimited"`
	IndividualUsage   struct {
		Plan     *summaryPlan `json:"plan"`
		OnDemand *summaryPlan `json:"onDemand"`
	} `json:"individualUsage"`
}

// ParseSummary 解析 /api/usage-summary 响应体（新计划/团队：按美元额度计，单位美分）。
//
// 参数 body：响应 JSON；now：用于填写抓取时间。
// 返回值：用量快照（Mode=usage）；缺少额度信息返回 ErrBadResponse。
func ParseSummary(body []byte, now time.Time) (model.CursorUsage, error) {
	var r summaryResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return model.CursorUsage{}, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	plan := r.IndividualUsage.Plan
	if plan == nil && r.BillingCycleEnd == "" && r.MembershipType == "" {
		return model.CursorUsage{}, fmt.Errorf("%w：缺少额度信息", ErrBadResponse)
	}
	u := model.CursorUsage{
		Mode: model.CursorModeUsage, FetchedAt: now,
		Membership: r.MembershipType, LimitType: r.LimitType, Unlimited: r.IsUnlimited,
	}
	if plan != nil {
		if plan.Used != nil {
			u.Used = int(float64(*plan.Used) + 0.5)
		}
		if plan.Limit != nil && *plan.Limit > 0 {
			u.Limit = int(float64(*plan.Limit) + 0.5)
		}
	}
	if od := r.IndividualUsage.OnDemand; od != nil && od.Used != nil {
		u.OnDemand = int(float64(*od.Used) + 0.5)
	}
	if t, err := time.Parse(time.RFC3339, r.BillingCycleStart); err == nil {
		u.CycleStart = t
	}
	if t, err := time.Parse(time.RFC3339, r.BillingCycleEnd); err == nil {
		u.CycleEndAt = t
	}
	return u, nil
}

// get 发 GET 请求并校验状态，返回响应体。
func (c *Client) get(ctx context.Context, path string, cred Credentials) ([]byte, error) {
	req, err := c.newRequest("GET", path, nil, cred)
	if err != nil {
		return nil, err
	}
	resp, err := httpx.Do(ctx, c.httpClient(), req)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// FetchUsage 获取当前周期的用量，自动识别计费口径：
//   - 老计划（/api/usage 给出 maxRequestUsage，如 500）：按次数计；
//   - 新计划/团队（没有次数上限）：按 /api/usage-summary 的美元额度计。
//
// 两种口径都会用 usage-summary 补上精确的重置时刻与套餐信息（取不到不影响主流程）。
//
// 参数 ctx：上下文；cred：凭据。
// 返回值：用量快照。
func (c *Client) FetchUsage(ctx context.Context, cred Credentials) (model.CursorUsage, error) {
	var legacy model.CursorUsage
	body, lerr := c.get(ctx, "/api/usage?user="+url.QueryEscape(cred.UserID), cred)
	if lerr == nil {
		legacy, lerr = ParseUsage(body, time.Now())
	}
	// 登录失效/限流时没必要再请求第二个接口
	if errors.Is(lerr, ErrUnauthorized) || errors.Is(lerr, ErrForbidden) || errors.Is(lerr, ErrRateLimited) {
		return model.CursorUsage{}, lerr
	}
	var sum model.CursorUsage
	sbody, serr := c.get(ctx, "/api/usage-summary", cred)
	if serr == nil {
		sum, serr = ParseSummary(sbody, time.Now())
	}
	var u model.CursorUsage
	switch {
	case lerr == nil && legacy.Limit > 0:
		u = legacy
		if serr == nil {
			u.CycleEndAt, u.Membership, u.LimitType = sum.CycleEndAt, sum.Membership, sum.LimitType
		}
	case serr == nil:
		u = sum
	case lerr != nil:
		return model.CursorUsage{}, lerr
	default:
		return model.CursorUsage{}, fmt.Errorf("%w：没有识别到额度信息", ErrBadResponse)
	}
	u.Account = cred.UserID
	return u, nil
}

// FetchEmail 获取账号邮箱（仅用于界面展示，失败不影响主流程）。
//
// 参数 ctx：上下文；cred：凭据。
// 返回值：邮箱。
func (c *Client) FetchEmail(ctx context.Context, cred Credentials) (string, error) {
	req, err := c.newRequest("GET", "/api/auth/me", nil, cred)
	if err != nil {
		return "", err
	}
	resp, err := httpx.Do(ctx, c.httpClient(), req)
	if err != nil {
		return "", err
	}
	if err := checkStatus(resp); err != nil {
		return "", err
	}
	var me struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(resp.Body, &me); err != nil {
		return "", fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	return me.Email, nil
}

// rawEvent 对应明细接口里的单条事件；数值字段兼容字符串/数字两种写法。
type rawEvent struct {
	Timestamp      flexInt64  `json:"timestamp"`
	Model          string     `json:"model"`
	Kind           string     `json:"kind"`
	RequestsCosts  flexFloat  `json:"requestsCosts"`
	IsTokenBased   bool       `json:"isTokenBasedCall"`
	ChargedCents   flexFloat  `json:"chargedCents"`
	ConversationID string     `json:"conversationId"`
	TokenUsage     *rawTokens `json:"tokenUsage"`
}

// rawTokens 是事件里的 Token 用量。
type rawTokens struct {
	InputTokens      flexInt64 `json:"inputTokens"`
	OutputTokens     flexInt64 `json:"outputTokens"`
	CacheWriteTokens flexInt64 `json:"cacheWriteTokens"`
	CacheReadTokens  flexInt64 `json:"cacheReadTokens"`
	TotalCents       flexFloat `json:"totalCents"`
}

// eventsPage 是一页明细。
type eventsPage struct {
	Total  *flexInt64 `json:"totalUsageEventsCount"`
	Events []rawEvent `json:"usageEventsDisplay"`
}

// flexInt64 兼容 JSON 数字与数字字符串。
type flexInt64 int64

// UnmarshalJSON 实现 json.Unmarshaler。
func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" || s == "-" {
		*f = 0
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*f = flexInt64(n)
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("无法解析数字 %q", s)
	}
	*f = flexInt64(v)
	return nil
}

// flexFloat 兼容 JSON 数字与数字字符串。
type flexFloat float64

// UnmarshalJSON 实现 json.Unmarshaler。
func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" || s == "-" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("无法解析数字 %q", s)
	}
	*f = flexFloat(v)
	return nil
}

// ParseEventsPage 解析一页明细响应。
//
// 参数 body：响应 JSON。
// 返回值：事件列表（已转换为领域模型）、接口给出的总数（缺失为 -1）。
func ParseEventsPage(body []byte) ([]model.CursorEvent, int, error) {
	var p eventsPage
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, 0, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	total := -1
	if p.Total != nil {
		total = int(*p.Total)
	}
	out := make([]model.CursorEvent, 0, len(p.Events))
	for _, r := range p.Events {
		out = append(out, toEvent(r))
	}
	return out, total, nil
}

// toEvent 把原始事件转换为领域模型并计算去重键。
func toEvent(r rawEvent) model.CursorEvent {
	e := model.CursorEvent{
		Time:           time.UnixMilli(int64(r.Timestamp)),
		Model:          r.Model,
		Kind:           r.Kind,
		Requests:       float64(r.RequestsCosts),
		ChargedCents:   float64(r.ChargedCents),
		IsTokenBased:   r.IsTokenBased,
		ConversationID: r.ConversationID,
	}
	if r.TokenUsage != nil {
		e.InputTokens = int64(r.TokenUsage.InputTokens)
		e.OutputTokens = int64(r.TokenUsage.OutputTokens)
		e.CacheWrite = int64(r.TokenUsage.CacheWriteTokens)
		e.CacheRead = int64(r.TokenUsage.CacheReadTokens)
		e.TotalCents = float64(r.TokenUsage.TotalCents)
	}
	e.Key = EventKey(e)
	return e
}

// EventKey 计算事件的稳定去重键（实现见 model.EventKey：不含 Token，避免进行中的事件被重复累计）。
//
// 参数 e：事件。
// 返回值：40 位十六进制摘要。
func EventKey(e model.CursorEvent) string { return model.EventKey(e) }

// FetchEvents 分页拉取 [since, until] 内的全部明细，并按 Key 去重。
//
// 参数 ctx：上下文；cred：凭据；since/until：时间范围（since 为零值表示从最早，until 为零值表示到现在）；
// onPage：每拉完一页的进度回调（可为 nil），参数为已累计条数与接口给出的总数。
// 返回值：按时间倒序（最新在前）的明细。
func (c *Client) FetchEvents(ctx context.Context, cred Credentials, since, until time.Time, onPage func(got, total int)) ([]model.CursorEvent, error) {
	pageSize, maxPages := c.PageSize, c.MaxPages
	if pageSize <= 0 {
		pageSize = 1000
	}
	if maxPages <= 0 {
		maxPages = 200
	}
	if until.IsZero() {
		until = time.Now().Add(time.Minute)
	}
	startMs := int64(0)
	if !since.IsZero() && since.UnixMilli() > 0 {
		startMs = since.UnixMilli()
	}
	seen := map[string]bool{}
	var out []model.CursorEvent
	for page := 1; page <= maxPages; page++ {
		body, _ := json.Marshal(map[string]any{
			"page":      page,
			"pageSize":  pageSize,
			"startDate": strconv.FormatInt(startMs, 10),
			"endDate":   strconv.FormatInt(until.UnixMilli(), 10),
		})
		req, err := c.newRequest("POST", "/api/dashboard/get-filtered-usage-events", body, cred)
		if err != nil {
			return nil, err
		}
		resp, err := httpx.Do(ctx, c.httpClient(), req)
		if err != nil {
			return nil, err
		}
		if err := checkStatus(resp); err != nil {
			return nil, err
		}
		evs, total, err := ParseEventsPage(resp.Body)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if !seen[e.Key] {
				seen[e.Key] = true
				out = append(out, e)
			}
		}
		if onPage != nil {
			onPage(len(out), total)
		}
		if len(evs) < pageSize {
			return out, nil
		}
	}
	return out, fmt.Errorf("明细分页超过上限 %d 页，已截断", maxPages)
}
