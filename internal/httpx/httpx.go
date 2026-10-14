// Package httpx 封装各数据源共用的 HTTP 细节：浏览器 UA、超时、响应体大小限制。
//
// @author ygw
package httpx

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BrowserUA 是伪装成 Safari 的 User-Agent。
// DeepSeek 平台的 WAF 会拦截 Go 默认 UA（返回 429 的拦截页），所以必须带浏览器 UA。
const BrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"

// MaxBody 是单次响应体的读取上限（32MB），避免异常响应撑爆内存。
const MaxBody = 32 << 20

// NewClient 创建带整体超时的 HTTP 客户端。
//
// 参数 timeout：单次请求总超时。
// 返回值：*http.Client。
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        4,
			IdleConnTimeout:     60 * time.Second,
		},
	}
}

// Response 是读取完毕的响应。
type Response struct {
	Status     int
	Body       []byte
	RetryAfter time.Duration // 来自 Retry-After 头，没有则为 0
}

// Do 发送请求并读取（限制大小的）响应体。
//
// 参数 c：客户端；req：请求（调用方已设置好头）。
// 返回值：响应；网络错误返回 error（HTTP 非 2xx 不算 error，由调用方判断）。
func Do(ctx context.Context, c *http.Client, req *http.Request) (*Response, error) {
	resp, err := c.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	r := &Response{Status: resp.StatusCode, Body: b}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if sec, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && sec > 0 {
			r.RetryAfter = time.Duration(sec) * time.Second
		}
	}
	return r, nil
}

// IsHTMLBlock 判断响应体是不是 WAF 拦截页（HTML 而非 JSON）。
//
// 参数 b：响应体。
// 返回值：以 < 开头（HTML）返回 true。
func IsHTMLBlock(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return strings.HasPrefix(s, "<")
}
