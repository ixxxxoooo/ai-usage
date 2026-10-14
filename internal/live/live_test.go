//go:build live

// Package live 是用真实账号数据做的只读联调测试，只在用户本机用 `go test -tags live ./internal/live` 运行。
// 所有请求都是只读接口；任何凭据都不会被打印（只打印脱敏后的摘要）。
//
// @author ygw
package live

import (
	"context"
	"testing"
	"time"

	"github.com/ixxxxoooo/ai-usage/internal/provider/cursor"
	"github.com/ixxxxoooo/ai-usage/internal/provider/deepseek"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// TestLiveCursor 读取本机 Cursor 登录，拉取用量与最近明细，核对两者的次数一致。
func TestLiveCursor(t *testing.T) {
	cred, err := cursor.ReadLocalCredentials("")
	if err != nil {
		t.Skipf("本机没有可用的 Cursor 登录: %v", err)
	}
	t.Logf("凭据来源：本机 Cursor，用户 %s，token %s", util.MaskSecret(cred.UserID), util.MaskSecret(cred.Token))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := cursor.NewClient()
	u, err := c.FetchUsage(ctx, cred)
	if err != nil {
		t.Fatalf("拉取用量失败: %v", err)
	}
	if u.Limit <= 0 || u.Used < 0 || u.CycleStart.IsZero() {
		t.Fatalf("用量数据异常: %+v", u)
	}
	t.Logf("用量 %d / %d，周期起点 %s", u.Used, u.Limit, u.CycleStart.Format("2006-01-02"))
	evs, err := c.FetchEvents(ctx, cred, u.CycleStart, time.Now(), nil)
	if err != nil {
		t.Fatalf("拉取明细失败: %v", err)
	}
	var sum float64
	for _, e := range evs {
		sum += e.Requests
	}
	t.Logf("本周期明细 %d 条，合计 %.0f 次", len(evs), sum)
	if diff := float64(u.Used) - sum; diff < -3 || diff > 3 {
		t.Errorf("明细合计 %.0f 与用量 %d 不一致", sum, u.Used)
	}
}

// TestLiveDeepSeek 从 Chrome 读取平台 token，拉取汇总与当月账单。
func TestLiveDeepSeek(t *testing.T) {
	toks, err := deepseek.FindChromeTokens("")
	if err != nil || len(toks) == 0 {
		t.Skipf("没有在浏览器里找到 DeepSeek 登录: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := deepseek.NewClient()
	var ok bool
	for _, tk := range toks {
		t.Logf("候选 token：%s/%s %s", tk.Browser, tk.Profile, util.MaskSecret(tk.Token))
		b, err := c.FetchSummary(ctx, tk.Token)
		if err != nil {
			t.Logf("  该 token 不可用: %v", err)
			continue
		}
		ok = true
		t.Logf("余额 %.2f %s，累计已用 %.2f", b.Total, b.Currency, b.TotalSpent)
		now := time.Now()
		rows, err := c.FetchMonth(ctx, tk.Token, now.Year(), int(now.Month()))
		if err != nil {
			t.Fatalf("拉取当月账单失败: %v", err)
		}
		var cost float64
		for _, r := range rows {
			if r.Date == "" || r.Model == "" {
				t.Errorf("账单行缺字段: %+v", r)
			}
			cost += r.Cost
		}
		t.Logf("当月账单 %d 行，合计 %.2f", len(rows), cost)
		break
	}
	if !ok {
		t.Fatal("所有候选 token 都不可用（登录可能已过期）")
	}
}

// TestLiveCursorFullHistory 首次同步用的「从最早开始」查询必须真的返回历史明细。
func TestLiveCursorFullHistory(t *testing.T) {
	cred, err := cursor.ReadLocalCredentials("")
	if err != nil {
		t.Skipf("本机没有可用的 Cursor 登录: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	evs, err := cursor.NewClient().FetchEvents(ctx, cred, time.Time{}, time.Time{}, nil)
	if err != nil {
		t.Fatalf("拉取全部历史失败: %v", err)
	}
	t.Logf("全部历史明细 %d 条", len(evs))
	if len(evs) == 0 {
		t.Error("全部历史查询返回 0 条（startDate=0 可能不被接口接受）")
	}
}
