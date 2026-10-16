package gui

import (
	"testing"

	"github.com/egoist/mygo"
)

// TestAppMenuClosesWindow 应用菜单里必须有带 ⌘W 的「关闭窗口」项。
//
// 菜单栏应用默认没有系统菜单，⌘W 只能靠菜单项的快捷键（performClose:）触发；
// 少了这一项，浮窗/面板就关不掉。
//
// @author ixxxxoooo
func TestAppMenuClosesWindow(t *testing.T) {
	found := false
	var walk func(items []*mygo.MenuItem)
	walk = func(items []*mygo.MenuItem) {
		for _, it := range items {
			if it.Role == mygo.RoleClose {
				found = true
				if it.Accelerator != "CmdOrCtrl+W" {
					t.Errorf("关闭窗口的快捷键是 %q，期望 CmdOrCtrl+W", it.Accelerator)
				}
			}
			walk(it.Submenu)
		}
	}
	walk(appMenu().Items())
	if !found {
		t.Fatal("应用菜单缺少「关闭窗口」（CmdOrCtrl+W）项")
	}
}
