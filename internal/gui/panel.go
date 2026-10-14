package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/service"
)

// 侧边栏宽度（DIP）。
const sidebarWidth = 212

// panelView 构建大面板：左侧毛玻璃侧边栏 + 右侧内容页。
//
// 参数 c：上下文。
func (a *App) panelView(c *ui.Context) {
	t := c.Theme()
	st := a.state()
	vib := c.Vibrancy()
	if vib {
		c.Root().Background(ui.Transparent)
	}
	if a.ui.toast != "" {
		c.Toast(a.ui.toast)
		a.ui.toast = ""
	}
	bar := c.TitleBar()
	top := max(bar.Height, 38)
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(sidebarWidth).Padding(0, 10, 12, 10)
		if !vib {
			side.Background(t.Surface)
		}
		side.Children(func() { a.sidebar(c, st, top) })
		ui.Column(c).Grow(1).Background(t.Background).Children(func() {
			a.pageHeaderBar(c, top)
			switch a.ui.page {
			case PageCursor:
				a.cursorPage(c, st)
			case PageDeepSeek:
				a.deepseekPage(c, st)
			case PageSettings:
				a.settingsPage(c, st)
			default:
				a.overviewPage(c, st)
			}
		})
	})
}

// pageHeaderBar 内容区顶部的可拖动空白条（给红绿灯留位置，并允许拖动窗口）。
func (a *App) pageHeaderBar(c *ui.Context, h float32) {
	ui.Box(c).Height(h - 14).DragWindow()
}

// sidebar 画侧边栏：标题、导航项、底部同步状态。
func (a *App) sidebar(c *ui.Context, st service.State, top float32) {
	t := c.Theme()
	ui.Box(c).Height(top).DragWindow()
	ui.Row(c).Gap(8).Padding(0, 8, 10, 8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconOverview).FontSize(16).TextColor(brandCursor(t))
		ui.Text(c, "AI Usage").FontSize(15).Bold()
	})
	sel := a.ui.page
	if ui.Sidebar(c.Key("nav"), &sel, func() {
		ui.SidebarItem(c, PageOverview, iconOverview, "概览")
		ui.SidebarItem(c, PageCursor, iconList, "Cursor 明细")
		ui.SidebarItem(c, PageDeepSeek, iconWallet, "DeepSeek 账单")
		ui.SidebarItem(c, PageSettings, iconSettings, "设置")
	}).Grow(1).Changed() {
		a.ui.page = sel
		if sel == PageSettings {
			a.loadSettingsDraft()
		}
	}
	now := a.now()
	ui.Column(c).Gap(6).Padding(8).Children(func() {
		statusLine(c, "Cursor", BuildCursorVM(st.Cursor, now).StatusText, StatusTone(st.Cursor.Status, st.Cursor.Stale()))
		statusLine(c, "DeepSeek", BuildDeepSeekVM(st.DeepSeek, st.Settings, now).StatusText, StatusTone(st.DeepSeek.Status, st.DeepSeek.Stale()))
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			hint(c, RefreshedText(st, now)).SingleLine().Grow(1)
			if iconButton(c, "side-refresh", iconRefresh, "立即刷新", st.Refreshing) {
				a.RefreshNow()
			}
		})
	})
}

// statusLine 侧边栏底部的一行状态：圆点 + 名称 + 状态。
func statusLine(c *ui.Context, name, text, tone string) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		dot(c, toneColor(t, tone))
		ui.Text(c, name).FontSize(12).SingleLine()
		ui.Spacer(c)
		ui.Text(c, text).FontSize(11).TextColor(t.TextMuted).SingleLine()
	})
}

// pageTitle 画页面标题与副标题，右侧可放操作按钮。
//
// 参数 c：上下文；title：标题；sub：副标题；actions：右侧操作（可为 nil）。
func pageTitle(c *ui.Context, title, sub string, actions func()) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.End).Padding(0, 24, 0, 24).Children(func() {
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, title).FontSize(22).Bold().SingleLine()
			if sub != "" {
				ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted).SingleLine()
			}
		})
		if actions != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(actions)
		}
	})
}

// pageBody 创建带统一内边距的可滚动页面主体。
func pageBody(c *ui.Context, key string, fn func()) {
	ui.Scroll(c.Key(key)).Grow(1).Children(func() {
		ui.Column(c).Padding(16, 24, 24, 24).Gap(14).Children(fn)
	})
}
