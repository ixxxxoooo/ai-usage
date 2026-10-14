package gui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/service"
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
	box.Padding(10).Gap(6).Children(func() {
		a.popoverHeader(c, st)
		a.cursorCard(c, BuildCursorVM(st.Cursor, now), st)
		dv := BuildDeepSeekVM(st.DeepSeek, st.Settings, now)
		a.deepseekCard(c, dv, st)
		ui.Spacer(c)
		popoverNote(c, BuildCursorVM(st.Cursor, now), dv)
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, RefreshedText(st, now)).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			if iconButton(c, "quit", iconPower, "退出 AI Usage", false) {
				mygo.App.Quit()
			}
		})
	})
}

// popoverNote 在底栏上方用一行说明「显示的是上次数据」的原因；两个数据源原因相同只显示一次，保持浮窗紧凑。
func popoverNote(c *ui.Context, cv CursorVM, dv DeepSeekVM) {
	var msg string
	switch {
	case cv.Stale && dv.Stale && cv.Message == dv.Message:
		msg = cv.Message
	case cv.Stale && dv.Stale:
		msg = "Cursor：" + cv.Message
	case cv.Stale:
		msg = cv.Message
	case dv.Stale:
		msg = dv.Message
	}
	if msg == "" {
		return
	}
	t := c.Theme()
	ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconWarn).FontSize(12).TextColor(t.Warning)
		ui.Text(c, msg+"（显示的是上次数据）").FontSize(11).TextColor(t.Warning).SingleLine().Grow(1)
	})
}

// popoverHeader 顶部栏：标题与刷新/展开/设置三个图标按钮。
func (a *App) popoverHeader(c *ui.Context, st service.State) {
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "AI Usage").FontSize(14).Bold().SingleLine()
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
	compactCard(c).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			dot(c, brand)
			ui.Text(c, "Cursor").Bold().FontSize(12)
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
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ring(c, "cursor-ring", vm.Frac, 66, 7, levelColor(t, vm.Level, brand), vm.Used, "/ "+vm.Limit)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, vm.Remain).FontSize(14).Bold().SingleLine()
				hint(c, vm.Reset).SingleLine()
				if vm.Exhaust != "" {
					ui.Text(c, vm.Exhaust).FontSize(11).TextColor(t.Warning).SingleLine() // 预警比日均更重要，占用同一行
				} else {
					hint(c, vm.Average+" · 已用 "+vm.Percent).SingleLine()
				}
			})
		})
	})
}

// deepseekCard 画 DeepSeek 卡片：余额大字 + 今日 Token/花费/本月已用。
func (a *App) deepseekCard(c *ui.Context, vm DeepSeekVM, st service.State) {
	t := c.Theme()
	brand := brandDeepSeek(t)
	compactCard(c).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			dot(c, brand)
			ui.Text(c, "DeepSeek").Bold().FontSize(12)
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
		ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
			col := t.Text
			if vm.LowBalance {
				col = t.Danger
			}
			ui.Text(c, vm.Balance).FontSize(22).Bold().TextColor(col).SingleLine()
			ui.Column(c).Grow(1).Gap(0).Padding(0, 0, 3, 0).Children(func() {
				hint(c, "剩余余额").SingleLine()
				hint(c, vm.Breakdown).SingleLine()
			})
		})
		if vm.HasBill {
			ui.Row(c).Gap(16).Children(func() {
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
	})
}

// emptyState 画「没有数据」的占位：说明原因并给出「去设置」入口。
func emptyState(c *ui.Context, msg, fallback string, onSettings func()) {
	t := c.Theme()
	if msg == "" {
		msg = fallback
	}
	ui.Column(c).Gap(6).Padding(2, 0).Children(func() {
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
