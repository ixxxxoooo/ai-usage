// Package antigravity 实现 Antigravity（Google 的 AI IDE，反重力）额度数据源。
//
// Antigravity 运行时会拉起一个本机语言服务进程（language_server），它只监听 127.0.0.1，
// 并通过启动参数里的 --csrf_token 做校验。额度（各模型剩余比例与重置时间）可以通过它的
// GetUserStatus 接口读到，不需要用户的 Google 账号凭据，也不会访问外网。
//
// 流程：找到进程 → 取出 csrf token → 查它监听的端口 → 逐个端口（https 优先，再 http）调用接口。
// 因此只有 Antigravity 正在运行时才能取到数据；没运行时返回 ErrNotRunning，界面保留上次数据。
//
// @author ixxxxoooo
package antigravity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/model"
)

// 预定义错误，供上层区分状态。
var (
	// ErrNotRunning 表示没有找到正在运行的 Antigravity 语言服务。
	ErrNotRunning = errors.New("未检测到正在运行的 Antigravity，请先打开它")
	// ErrNotLoggedIn 表示 Antigravity 正在运行但没有登录账号。
	ErrNotLoggedIn = errors.New("Antigravity 尚未登录账号")
	// ErrBadResponse 表示响应不是预期格式。
	ErrBadResponse = errors.New("Antigravity 返回了无法识别的数据")
)

// statusPath 是语言服务的用户状态接口（Connect 协议，JSON）。
const statusPath = "/exa.language_server_pb.LanguageServerService/GetUserStatus"

// Process 是一个 Antigravity 语言服务进程。
//
// @author ixxxxoooo
type Process struct {
	PID  int
	CSRF string // 启动参数 --csrf_token（只用于访问本机接口，不落盘、不打印）
}

// Client 读取本机 Antigravity 的额度。零值可用；各函数字段留空时使用真实实现，测试里可注入。
//
// @author ixxxxoooo
type Client struct {
	HTTP  *http.Client                                      // 为空使用 6 秒超时的本机客户端
	Procs func(ctx context.Context) ([]Process, error)      // 列出语言服务进程
	Ports func(ctx context.Context, pid int) ([]int, error) // 列出进程监听的 TCP 端口
	Host  string                                            // 默认 127.0.0.1
}

// NewClient 创建默认客户端。
//
// 返回值：*Client。
func NewClient() *Client { return &Client{} }

// httpClient 返回访问本机语言服务的客户端：证书是自签的，只允许访问回环地址，所以跳过校验是安全的。
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout:   6 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, Proxy: nil},
	}
}

// host 返回要访问的主机（始终是回环地址，除非测试注入）。
func (c *Client) host() string {
	if c.Host != "" {
		return c.Host
	}
	return "127.0.0.1"
}

var csrfRe = regexp.MustCompile(`--csrf_token[= ]+([^\s]+)`)

// ParseProcesses 从 `ps -axo pid=,command=` 的输出里挑出 Antigravity 的语言服务进程。
//
// 参数 out：ps 输出。
// 返回值：进程列表（只含带 --csrf_token 的 language_server）。
func ParseProcesses(out string) []Process {
	var ps []Process
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if !strings.Contains(low, "language_server") || !strings.Contains(low, "antigravity") {
			continue
		}
		sp := strings.IndexAny(line, " \t")
		if sp < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:sp])
		if err != nil {
			continue
		}
		m := csrfRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ps = append(ps, Process{PID: pid, CSRF: m[1]})
	}
	return ps
}

// ParsePorts 从 `lsof -Fn` 的输出里取出监听端口。
//
// 参数 out：lsof 输出，监听行形如 n127.0.0.1:58635 或 n*:58635。
// 返回值：去重后的端口（升序）。
func ParsePorts(out string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "n") {
			continue
		}
		i := strings.LastIndex(line, ":")
		if i < 0 {
			continue
		}
		p, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(line[i+1:], "(LISTEN)")))
		if err != nil || p <= 0 || p > 65535 || seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// listProcesses 用 ps 列出语言服务进程。
func (c *Client) listProcesses(ctx context.Context) ([]Process, error) {
	if c.Procs != nil {
		return c.Procs(ctx)
	}
	out, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("读取进程列表失败：%w", err)
	}
	return ParseProcesses(string(out)), nil
}

// listPorts 用 lsof 列出进程监听的端口。
func (c *Client) listPorts(ctx context.Context, pid int) ([]int, error) {
	if c.Ports != nil {
		return c.Ports(ctx, pid)
	}
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	if err != nil && len(out) == 0 {
		return nil, nil // 进程刚退出或没有监听端口，lsof 会返回非 0
	}
	return ParsePorts(string(out)), nil
}

// call 向某个端口发起 GetUserStatus，https 优先，失败再试 http。
func (c *Client) call(ctx context.Context, port int, csrf string) ([]byte, error) {
	body := []byte(`{"metadata":{"ideName":"antigravity","extensionName":"antigravity","locale":"en"}}`)
	var lastErr error
	for _, scheme := range []string{"https", "http"} {
		url := fmt.Sprintf("%s://%s:%d%s", scheme, c.host(), port, statusPath)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set("X-Codeium-Csrf-Token", csrf)
		resp, err := c.httpClient().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if rerr != nil {
			lastErr = rerr
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return data, nil
		}
		// 对 https 端口发 http（或相反）会得到 400，换协议重试；其他状态直接记录
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil, lastErr
}

// FetchQuota 读取当前 Antigravity 的套餐与各模型额度。
//
// 参数 ctx：上下文。
// 返回值：额度快照；没有运行返回 ErrNotRunning，未登录返回 ErrNotLoggedIn。
func (c *Client) FetchQuota(ctx context.Context) (model.AntigravityQuota, error) {
	procs, err := c.listProcesses(ctx)
	if err != nil {
		return model.AntigravityQuota{}, err
	}
	if len(procs) == 0 {
		return model.AntigravityQuota{}, ErrNotRunning
	}
	var firstErr error
	for _, p := range procs {
		ports, _ := c.listPorts(ctx, p.PID)
		for _, port := range ports {
			data, err := c.call(ctx, port, p.CSRF)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			q, err := ParseQuota(data, time.Now())
			if err != nil {
				return model.AntigravityQuota{}, err
			}
			return q, nil
		}
	}
	if firstErr != nil {
		return model.AntigravityQuota{}, fmt.Errorf("连接 Antigravity 本机服务失败：%w", firstErr)
	}
	return model.AntigravityQuota{}, ErrNotRunning
}

// statusResponse 是 GetUserStatus 响应里用到的部分。
type statusResponse struct {
	UserStatus *struct {
		PlanStatus struct {
			PlanInfo struct {
				PlanName string `json:"planName"`
			} `json:"planInfo"`
		} `json:"planStatus"`
		UserTier struct {
			Name string `json:"name"`
		} `json:"userTier"`
		CascadeModelConfigData struct {
			Configs []struct {
				Label        string `json:"label"`
				ModelID      string `json:"modelId"`
				Disabled     bool   `json:"disabled"`
				ModelOrAlias struct {
					Model string `json:"model"`
				} `json:"modelOrAlias"`
				QuotaInfo *struct {
					RemainingFraction *float64 `json:"remainingFraction"`
					ResetTime         string   `json:"resetTime"`
				} `json:"quotaInfo"`
			} `json:"clientModelConfigs"`
		} `json:"cascadeModelConfigData"`
	} `json:"userStatus"`
}

// ParseQuota 解析 GetUserStatus 响应。
//
// 参数 body：响应 JSON；now：抓取时间。
// 返回值：额度快照；没有 userStatus 返回 ErrNotLoggedIn，格式不对返回 ErrBadResponse。
func ParseQuota(body []byte, now time.Time) (model.AntigravityQuota, error) {
	var r statusResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return model.AntigravityQuota{}, fmt.Errorf("%w：%v", ErrBadResponse, err)
	}
	if r.UserStatus == nil {
		return model.AntigravityQuota{}, ErrNotLoggedIn
	}
	q := model.AntigravityQuota{FetchedAt: now}
	q.Plan = r.UserStatus.UserTier.Name
	if q.Plan == "" {
		q.Plan = r.UserStatus.PlanStatus.PlanInfo.PlanName
	}
	for _, m := range r.UserStatus.CascadeModelConfigData.Configs {
		id := m.ModelID
		if id == "" {
			id = m.ModelOrAlias.Model
		}
		am := model.AntigravityModel{Label: m.Label, ID: id, Disabled: m.Disabled}
		if am.Label == "" {
			am.Label = id
		}
		if qi := m.QuotaInfo; qi != nil && (qi.RemainingFraction != nil || qi.ResetTime != "") {
			// 有重置时间却没有剩余比例，表示额度已耗尽（服务端省略了 0）
			f := 0.0
			if qi.RemainingFraction != nil {
				f = *qi.RemainingFraction
			}
			f = min(max(f, 0), 1)
			am.HasQuota, am.Remaining = true, f
			if t, err := time.Parse(time.RFC3339, qi.ResetTime); err == nil {
				am.ResetAt = t
			}
		}
		q.Models = append(q.Models, am)
	}
	if len(q.Models) == 0 {
		return model.AntigravityQuota{}, fmt.Errorf("%w：没有模型额度信息", ErrBadResponse)
	}
	return q, nil
}
