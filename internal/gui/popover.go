package gui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/liwenjiao/ai-usage/internal/service"
)

// popoverView 构建托盘浮窗：Cursor 与 DeepSeek 两张卡片 + 顶部/底部操作栏。
//
// 参数 c：上下文。
func (a *App) popoverView(c *ui.Context) {
	t := c.Theme()
	now := a.now()
	st := a.state()
	c.Root().Background(ui.Transparent)
	box := ui.Column(c).Fill()
	if !c.Vibrancy() { // 非 macOS 或没有材质时自己画面板
		box.Background(t.Surface).Radius(10).Border(1, t.Border)
	}
	box.Padding(14).Gap(10).Children(func() {
		a.popoverHeader(c, st)
		a.cursorCard(c, BuildCursorVM(st.Cursor, now), st)
		a.deepseekCard(c, BuildDeepSeekVM(st.DeepSeek, st.Settings, now), st)
		ui.Spacer(c)
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, RefreshedText(st, now)).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			if iconButton(c, "quit", iconPower, "退出 AI Usage", false) {
				mygo.App.Quit()
			}
		})
	})
}

// popoverHeader 顶部栏：标题与刷新/展开/设置三个图标按钮。
func (a *App) popoverHeader(c *ui.Context, st service.State) {
	ui.Row(c).Gap(2).Children(func() {
		ui.Text(c, "AI Usage").FontSize(15).Bold().SingleLine()
		ui.Spacer(c)
		if iconButton(c, "refresh", iconRefresh, "立即刷新", st.Refreshing) {
			a.RefreshNow()
		}
		if iconButton(c, "expand", iconExpand, "展开大面板", false) {
			a.OpenPanel(PageOverview)
		}
		if iconButton(c, "settings", iconSettings, "设置", false) {
			a.OpenPanel(PageSettings)
		}
	})
}

// statusPill 画状态药丸：正常时显示「已同步」，其余显示状态名。
func statusPill(c *ui.Context, text string, tone string) {
	pill(c, text, toneColor(c.Theme(), tone))
}

// cursorCard 画 Cursor 卡片：环形进度 + 剩余/重置/日均/预测。
func (a *App) cursorCard(c *ui.Context, vm CursorVM, st service.State) {
	t := c.Theme()
	brand := brandCursor(t)
	card(c).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			dot(c, brand)
			ui.Text(c, "Cursor").Bold().FontSize(13)
			ui.Spacer(c)
			label := vm.StatusText
			if vm.Status == service.StatusOK {
				label = "已同步"
			}
			statusPill(c, label, StatusTone(vm.Status, vm.Stale))
		})
		if !vm.Has {
			emptyState(c, vm.Message, "尚未获取到 Cursor 用量", func() { a.OpenPanel(PageSettings) })
			return
		}
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			ring(c, "cursor-ring", vm.Frac, 84, 9, levelColor(t, vm.Level, brand), vm.Used, "/ "+vm.Limit)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, vm.Remain).FontSize(16).Bold().SingleLine()
				hint(c, vm.Reset).SingleLine()
				hint(c, vm.Average+" · 已用 "+vm.Percent).SingleLine()
				if vm.Exhaust != "" {
					ui.Text(c, vm.Exhaust).FontSize(11).TextColor(t.Warning)
				}
			})
		})
		staleNote(c, vm.Stale, vm.Message)
	})
}

// deepseekCard 画 DeepSeek 卡片：余额大字 + 今日 Token/花费/本月已用。
func (a *App) deepseekCard(c *ui.Context, vm DeepSeekVM, st service.State) {
	t := c.Theme()
	brand := brandDeepSeek(t)
	card(c).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			dot(c, brand)
			ui.Text(c, "DeepSeek").Bold().FontSize(13)
			ui.Spacer(c)
			label := vm.StatusText
			if vm.Status == service.StatusOK {
				label = "已同步"
			}
			statusPill(c, label, StatusTone(vm.Status, vm.Stale))
		})
		if !vm.HasBalance {
			emptyState(c, vm.Message, "尚未获取到 DeepSeek 余额", func() { a.OpenPanel(PageSettings) })
			return
		}
		ui.Column(c).Gap(1).Children(func() {
			hint(c, "剩余余额")
			col := t.Text
			if vm.LowBalance {
				col = t.Danger
			}
			ui.Text(c, vm.Balance).FontSize(28).Bold().TextColor(col).SingleLine()
			hint(c, vm.Breakdown).SingleLine()
		})
		if vm.HasBill {
			ui.Row(c).Gap(18).Children(func() {
				stat(c, "今日 Token", vm.TodayTokens, ui.Color{})
				stat(c, "今日花费", vm.TodayCost, ui.Color{})
				stat(c, vm.MonthLabel+"已用", vm.MonthCost, ui.Color{})
			})
		} else if vm.NeedToken {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconWarn).FontSize(13).TextColor(t.Warning)
				hint(c, "未读取到平台登录信息，暂无今日 Token").Grow(1)
				if ui.Button(c.Key("ds-fix"), "去设置").Clicked() {
					a.OpenPanel(PageSettings)
				}
			})
		}
		staleNote(c, vm.Stale, vm.Message)
	})
}

// emptyState 画「没有数据」的占位：说明原因并给出「去设置」入口。
func emptyState(c *ui.Context, msg, fallback string, onSettings func()) {
	t := c.Theme()
	if msg == "" {
		msg = fallback
	}
	ui.Column(c).Gap(8).Padding(4, 0).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, iconWarn).FontSize(14).TextColor(t.Warning)
			ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted).Grow(1)
		})
		if ui.Button(c.Key("go-settings-"+fallback), "前往设置").Clicked() {
			onSettings()
		}
	})
}

// staleNote 在展示旧数据时给出一行说明（离线/过期等），避免用户误以为是最新值。
func staleNote(c *ui.Context, stale bool, msg string) {
	if !stale || msg == "" {
		return
	}
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, iconWarn).FontSize(12).TextColor(t.Warning)
		ui.Text(c, msg+"（显示的是上次数据）").FontSize(11).TextColor(t.Warning).Grow(1)
	})
}
