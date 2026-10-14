//go:build live

package live

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/provider/deepseek"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/service"
	"github.com/ixxxxoooo/ai-usage/internal/store"
)

// TestLiveManualTokenFlow 用真实 token 走完「手动粘贴」全流程：
// 以 "authorization: Bearer …" 形式保存 → 切到手动模式 → 刷新 → 余额与账单都应成功。
//
// token 取自本机 Chrome（只读），不写日志；若设置 AIUSAGE_E2E_DIR，则把数据留在该目录供应用复核
// （该目录里的 secrets.json 含 token，用完请删除）。
func TestLiveManualTokenFlow(t *testing.T) {
	toks, _ := deepseek.FindChromeTokens("")
	if len(toks) == 0 {
		t.Skip("本机浏览器里没有 DeepSeek 登录")
	}
	dir := os.Getenv("AIUSAGE_E2E_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	st, err := store.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := service.New(service.Config{
		Store: st, Secrets: &secret.File{Path: filepath.Join(dir, "secrets.json")},
		ChromeTokens: func() ([]deepseek.ChromeToken, error) { return nil, nil }, // 手动模式不应依赖浏览器
	})
	if err != nil {
		t.Fatal(err)
	}
	s := svc.Settings()
	s.DeepSeekAuthMode = model.AuthManual
	if err := svc.UpdateSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveSecret(secret.KeyDeepSeekToken, "authorization: Bearer "+toks[0].Token); err != nil {
		t.Fatal(err)
	}
	svc.RefreshAll(context.Background())
	d := svc.State().DeepSeek
	if d.Status != service.StatusOK || !d.HasBalance || !d.HasBill {
		t.Fatalf("手动模式刷新失败: %s %q hasBill=%v", d.Status.StatusText(), d.Message, d.HasBill)
	}
	t.Logf("手动模式成功：来源 %s，余额 %.2f，本月 %.2f", d.TokenSrc, d.Balance.Total, d.MonthCost)
}
