// Package deepseek 实现 DeepSeek 数据源：API Key 余额、平台账单（需网页登录 token）、浏览器 token 读取。
//
// @author ixxxxoooo
package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/httpx"
	"github.com/ixxxxoooo/ai-usage/internal/model"
)

// 预定义错误，供上层区分状态。
var (
	// ErrNoCredentials 表示没有可用的 API Key 或平台 token。
	ErrNoCredentials = errors.New("未配置 DeepSeek 凭据")
	// ErrUnauthorized 表示凭据无效或过期。
	ErrUnauthorized = errors.New("DeepSeek 登录已过期或 Key 无效")
	// ErrRateLimited 表示被限流或被 WAF 拦截。
	ErrRateLimited = errors.New("DeepSeek 请求被限流或拦截")
	// ErrBadResponse 表示响应不是预期格式。
	ErrBadResponse = errors.New("DeepSeek 返回了无法识别的数据")
)

// 默认地址。
const (
	DefaultAPIBase      = "https://api.deepseek.com"
	DefaultPlatformBase = "https://platform.deepseek.com"
)

// Client 调用 DeepSeek 接口。
//
// @author ixxxxoooo
type Client struct {
	APIBase      string       // 官方 API 地址（测试可替换）
	PlatformBase string       // 平台网站地址（测试可替换）
	HTTP         *http.Client // 为空使用默认客户端
}

// NewClient 创建默认客户端。
//
// 返回值：*Client。
func NewClient() *Client {
	return &Client{APIBase: DefaultAPIBase, PlatformBase: DefaultPlatformBase, HTTP: httpx.NewClient(30 * time.Second)}
}

// httpClient 返回可用的 HTTP 客户端。
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return httpx.NewClient(30 * time.Second)
}

// apiBase / platformBase 返回去掉末尾斜杠的地址。
func (c *Client) apiBase() string {
	if c.APIBase == "" {
		return DefaultAPIBase
	}
	return strings.TrimRight(c.APIBase, "/")
}

func (c *Client) platformBase() string {
	if c.PlatformBase == "" {
		return DefaultPlatformBase
	}
	return strings.TrimRight(c.PlatformBase, "/")
}

// get 发送带 Bearer 的 GET 请求；platform 为 true 时附带网页端请求头。
func (c *Client) get(ctx context.Context, fullURL, bearer string, platform bool) (*httpx.Response, error) {
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", httpx.BrowserUA)
	if platform {
		req.Header.Set("x-client-platform", "web")
		req.Header.Set("Origin", "https://platform.deepseek.com")
		req.Header.Set("Referer", "https://platform.deepseek.com/usage")
	}
	resp, err := httpx.Do(ctx, c.httpClient(), req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden:
		return nil, ErrUnauthorized
	case resp.Status == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case resp.Status < 200 || resp.Status >= 300:
		return nil, fmt.Errorf("DeepSeek 接口错误：HTTP %d", resp.Status)
	case httpx.IsHTMLBlock(resp.Body):
		return nil, fmt.Errorf("%w：返回了拦截网页", ErrRateLimited)
	}
	return resp, nil
}

// num 兼容字符串/数字的数值。
type num float64

// UnmarshalJSON 实现 json.Unmarshaler。
func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("无法解析数字 %q", s)
	}
	*n = num(v)
	return nil
}

// balanceResp 对应 /user/balance。
type balanceResp struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		Currency string `json:"currency"`
		Total    num    `json:"total_balance"`
		Granted  num    `json:"granted_balance"`
		ToppedUp num    `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

// ParseBalance 解析 API Key 余额响应；多币种时优先 CNY，其次 USD，再取第一个。
//
// 参数 body：响应 JSON；now：抓取时间。
// 返回值：余额快照。
func ParseBalance(body []byte, now time.Time) (model.DeepSeekBalance, error) {
	var r balanceResp
	if err := json.Unmarshal(body, &r); err != nil {
		return model.DeepSeekBalance{}, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	if len(r.BalanceInfos) == 0 {
		return model.DeepSeekBalance{}, fmt.Errorf("%w：没有 balance_infos", ErrBadResponse)
	}
	pick := 0
	rank := func(c string) int {
		switch strings.ToUpper(c) {
		case "CNY":
			return 0
		case "USD":
			return 1
		}
		return 2
	}
	for i, b := range r.BalanceInfos {
		if rank(b.Currency) < rank(r.BalanceInfos[pick].Currency) {
			pick = i
		}
	}
	b := r.BalanceInfos[pick]
	return model.DeepSeekBalance{
		Currency: strings.ToUpper(b.Currency), Total: float64(b.Total), Granted: float64(b.Granted),
		ToppedUp: float64(b.ToppedUp), Available: r.IsAvailable, Source: "apikey", FetchedAt: now,
	}, nil
}

// FetchBalanceByKey 用 API Key 查询余额。
//
// 参数 ctx：上下文；apiKey：DeepSeek API Key。
// 返回值：余额快照。
func (c *Client) FetchBalanceByKey(ctx context.Context, apiKey string) (model.DeepSeekBalance, error) {
	if strings.TrimSpace(apiKey) == "" {
		return model.DeepSeekBalance{}, ErrNoCredentials
	}
	resp, err := c.get(ctx, c.apiBase()+"/user/balance", strings.TrimSpace(apiKey), false)
	if err != nil {
		return model.DeepSeekBalance{}, err
	}
	return ParseBalance(resp.Body, time.Now())
}

// envelope 是平台接口的外层包装：{code,msg,data:{biz_code,biz_msg,biz_data}}。
type envelope[T any] struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		BizCode int    `json:"biz_code"`
		BizMsg  string `json:"biz_msg"`
		BizData T      `json:"biz_data"`
	} `json:"data"`
}

// checkBiz 检查平台业务码，40002/40003 视为登录过期。
func checkBiz(code, bizCode int, msg string) error {
	if code == 40002 || code == 40003 || bizCode == 40002 || bizCode == 40003 {
		return ErrUnauthorized
	}
	if code != 0 || bizCode != 0 {
		return fmt.Errorf("DeepSeek 平台错误：code=%d biz_code=%d %s", code, bizCode, msg)
	}
	return nil
}

// summaryData 对应 get_user_summary 的 biz_data。
type summaryData struct {
	Normal []walletItem `json:"normal_wallets"`
	Bonus  []walletItem `json:"bonus_wallets"`
	Costs  []struct {
		Currency string `json:"currency"`
		Amount   num    `json:"amount"`
	} `json:"total_costs"`
}

// walletItem 是一个钱包（充值/赠送）。
type walletItem struct {
	Currency string `json:"currency"`
	Balance  num    `json:"balance"`
}

// ParseSummary 解析平台 get_user_summary 响应：余额（充值+赠送）与累计已用。
//
// 参数 body：响应 JSON；now：抓取时间。
// 返回值：余额快照（Source=platform，HasSpent=true）。
func ParseSummary(body []byte, now time.Time) (model.DeepSeekBalance, error) {
	var e envelope[summaryData]
	if err := json.Unmarshal(body, &e); err != nil {
		return model.DeepSeekBalance{}, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	if err := checkBiz(e.Code, e.Data.BizCode, e.Msg+e.Data.BizMsg); err != nil {
		return model.DeepSeekBalance{}, err
	}
	d := e.Data.BizData
	cur := ""
	for _, w := range append(append([]walletItem{}, d.Normal...), d.Bonus...) {
		if cur == "" || strings.EqualFold(w.Currency, "CNY") {
			cur = strings.ToUpper(w.Currency)
		}
	}
	if cur == "" && len(d.Costs) > 0 {
		cur = strings.ToUpper(d.Costs[0].Currency)
	}
	if cur == "" {
		return model.DeepSeekBalance{}, fmt.Errorf("%w：没有钱包信息", ErrBadResponse)
	}
	b := model.DeepSeekBalance{Currency: cur, Source: "platform", FetchedAt: now}
	for _, w := range d.Normal {
		if strings.EqualFold(w.Currency, cur) {
			b.ToppedUp += float64(w.Balance)
		}
	}
	for _, w := range d.Bonus {
		if strings.EqualFold(w.Currency, cur) {
			b.Granted += float64(w.Balance)
		}
	}
	b.Total = b.ToppedUp + b.Granted
	b.Available = b.Total > 0
	for _, c := range d.Costs {
		if strings.EqualFold(c.Currency, cur) {
			b.TotalSpent += float64(c.Amount)
			b.HasSpent = true
		}
	}
	return b, nil
}

// FetchSummary 用平台 token 查询余额与累计已用。
//
// 参数 ctx：上下文；token：平台 userToken。
// 返回值：余额快照。
func (c *Client) FetchSummary(ctx context.Context, token string) (model.DeepSeekBalance, error) {
	if strings.TrimSpace(token) == "" {
		return model.DeepSeekBalance{}, ErrNoCredentials
	}
	resp, err := c.get(ctx, c.platformBase()+"/api/v0/users/get_user_summary", strings.TrimSpace(token), true)
	if err != nil {
		return model.DeepSeekBalance{}, err
	}
	return ParseSummary(resp.Body, time.Now())
}

// usageItem 是某个用量类别及数值。
type usageItem struct {
	Type   string `json:"type"`
	Amount num    `json:"amount"`
}

// modelUsage 是某个模型的用量项列表。
type modelUsage struct {
	Model string      `json:"model"`
	Usage []usageItem `json:"usage"`
}

// dayUsage 是某一天各模型的用量。
type dayUsage struct {
	Date string       `json:"date"`
	Data []modelUsage `json:"data"`
}

// amountData 对应 usage/amount 的 biz_data。
type amountData struct {
	Days []dayUsage `json:"days"`
}

// costItem 是 usage/cost 的 biz_data 数组元素（按币种）。
type costItem struct {
	Currency string     `json:"currency"`
	Days     []dayUsage `json:"days"`
}

// ParseMonth 合并同一月份的 amount（Token）与 cost（费用）响应，生成逐日逐模型账单。
//
// 参数 amountBody/costBody：两个接口的响应体。
// 返回值：账单明细（全 0 的行会被丢弃，按日期、模型排序）。
func ParseMonth(amountBody, costBody []byte) ([]model.DeepSeekDaily, error) {
	var a envelope[amountData]
	if err := json.Unmarshal(amountBody, &a); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	if err := checkBiz(a.Code, a.Data.BizCode, a.Msg+a.Data.BizMsg); err != nil {
		return nil, err
	}
	var c envelope[[]costItem]
	if err := json.Unmarshal(costBody, &c); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	if err := checkBiz(c.Code, c.Data.BizCode, c.Msg+c.Data.BizMsg); err != nil {
		return nil, err
	}
	currency := "CNY"
	if len(c.Data.BizData) > 0 && c.Data.BizData[0].Currency != "" {
		currency = strings.ToUpper(c.Data.BizData[0].Currency)
	}
	rows := map[string]*model.DeepSeekDaily{}
	get := func(date, mdl string) *model.DeepSeekDaily {
		k := date + "|" + mdl
		r := rows[k]
		if r == nil {
			r = &model.DeepSeekDaily{Date: date, Model: mdl, Currency: currency}
			rows[k] = r
		}
		return r
	}
	for _, d := range a.Data.BizData.Days {
		for _, m := range d.Data {
			r := get(d.Date, m.Model)
			for _, u := range m.Usage {
				n := int64(u.Amount)
				switch u.Type {
				case "PROMPT_CACHE_HIT_TOKEN":
					r.CacheHit += n
				case "PROMPT_CACHE_MISS_TOKEN":
					r.CacheMiss += n
				case "RESPONSE_TOKEN":
					r.Output += n
				case "REQUEST":
					r.Requests += n
				}
			}
		}
	}
	// 只取主币种的费用，避免多币种相加出错
	for _, ci := range c.Data.BizData {
		if !strings.EqualFold(ci.Currency, currency) {
			continue
		}
		for _, d := range ci.Days {
			for _, m := range d.Data {
				var sum float64
				for _, u := range m.Usage {
					sum += float64(u.Amount)
				}
				get(d.Date, m.Model).Cost += sum
			}
		}
	}
	out := make([]model.DeepSeekDaily, 0, len(rows))
	for _, r := range rows {
		if r.Tokens() == 0 && r.Requests == 0 && r.Cost == 0 {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Model < out[j].Model
	})
	return out, nil
}

// FetchMonth 拉取指定年月的账单明细（amount 与 cost 两个接口）。
//
// 参数 ctx：上下文；token：平台 userToken；year/month：年月。
// 返回值：该月逐日逐模型明细。
func (c *Client) FetchMonth(ctx context.Context, token string, year, month int) ([]model.DeepSeekDaily, error) {
	if strings.TrimSpace(token) == "" {
		return nil, ErrNoCredentials
	}
	q := url.Values{"month": {strconv.Itoa(month)}, "year": {strconv.Itoa(year)}}.Encode()
	ar, err := c.get(ctx, c.platformBase()+"/api/v0/usage/amount?"+q, strings.TrimSpace(token), true)
	if err != nil {
		return nil, err
	}
	cr, err := c.get(ctx, c.platformBase()+"/api/v0/usage/cost?"+q, strings.TrimSpace(token), true)
	if err != nil {
		return nil, err
	}
	return ParseMonth(ar.Body, cr.Body)
}
