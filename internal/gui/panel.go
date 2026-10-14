package gui

import "github.com/egoist/mygo/ui"

// panelView 构建大面板（临时占位，M4 完整实现）。
func (a *App) panelView(c *ui.Context) {
	ui.Column(c).Fill().Center().Children(func() {
		ui.Text(c, "AI Usage 大面板："+a.ui.page).FontSize(20).Bold()
	})
}
