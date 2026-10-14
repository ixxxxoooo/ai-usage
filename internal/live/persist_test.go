//go:build live

package live

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/service"
	"github.com/ixxxxoooo/ai-usage/internal/store"
)

// TestPersistedByApp 检查真实应用进程跑过一次后留下的数据：重新打开数据库并创建服务，
// 界面状态应能直接恢复（用量、余额、明细条数）。需要先用 AIUSAGE_DATA_DIR 运行一次应用。
func TestPersistedByApp(t *testing.T) {
	dir := os.Getenv("AIUSAGE_E2E_DIR")
	if dir == "" {
		t.Skip("未设置 AIUSAGE_E2E_DIR")
	}
	st, err := store.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := service.New(service.Config{Store: st, Secrets: secret.NewMemory()})
	if err != nil {
		t.Fatal(err)
	}
	s := svc.State()
	if !s.Cursor.HasUsage || s.Cursor.Usage.Limit != 500 || s.Cursor.Usage.Used <= 0 {
		t.Errorf("重启后没有恢复 Cursor 用量: %+v", s.Cursor.Usage)
	}
	if s.Cursor.EventCount <= 0 {
		t.Errorf("重启后没有 Cursor 明细")
	}
	rows, _ := st.DeepSeekDaily("", "")
	t.Logf("恢复：Cursor %d/%d，明细 %d 条；DeepSeek 余额 %.2f，账单 %d 行", s.Cursor.Usage.Used, s.Cursor.Usage.Limit, s.Cursor.EventCount, s.DeepSeek.Balance.Total, len(rows))
	if s.DeepSeek.Balance.Total <= 0 || len(rows) == 0 {
		t.Errorf("重启后没有恢复 DeepSeek 数据: %+v rows=%d", s.DeepSeek.Balance, len(rows))
	}
}
