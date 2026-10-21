package gui

import (
	"strings"
	"testing"
	"time"
)

// TestUpdateDesc 设置页「软件更新」说明：不可更新、从未检查、已检查三种情况。
func TestUpdateDesc(t *testing.T) {
	if got := updateDesc(false, time.Now()); !strings.Contains(got, "无法自动更新") {
		t.Errorf("不可更新时应说明原因: %q", got)
	}
	if got := updateDesc(true, time.Time{}); !strings.Contains(got, "新版本") {
		t.Errorf("从未检查时应介绍功能: %q", got)
	}
	last := time.Date(2026, 10, 21, 8, 30, 0, 0, time.Local)
	if got := updateDesc(true, last); got != "上次检查：2026-10-21 08:30" {
		t.Errorf("已检查时应显示时间: %q", got)
	}
}

// TestUpdateMenuItems 未启用自动更新（测试、自检）时托盘菜单不出现「检查更新」，也不会调用更新插件。
func TestUpdateMenuItems(t *testing.T) {
	a := New()
	if items := a.updateMenuItems(); len(items) != 0 {
		t.Errorf("未启用时不应有菜单项: %d", len(items))
	}
	a.CheckForUpdates() // 未启用时是空操作，不能 panic
}
