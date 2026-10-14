package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/liwenjiao/ai-usage/internal/service"
	"github.com/liwenjiao/ai-usage/internal/util"
)

// overviewPage 概览页：两张大卡片 + 近 14 天趋势图。
func (a *App) overviewPage(c *ui.Context, st service.State) {
	t := c.Theme()
	now := a.now()
	data := a.Data()
	cv := BuildCursorVM(st.Cursor, now)
	dv := BuildDeepSeekVM(st.DeepSeek, st.Settings, now)
	pageTitle(c, "概览", RefreshedText(st, now), func() {
		if ui.Button(c.Key("ov-refresh"), "立即刷新").Disabled(st.Refreshing).Clicked() {
			a.RefreshNow()
		}
	})
	pageBody(c, "ov-scroll", func() {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			a.bigCursorCard(c, cv, st)
			a.bigDeepSeekCard(c, dv, st)
		})
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			card(c).Grow(1).Basis(0).Children(func() {
				ui.Text(c, "Cursor · 近 14 天请求次数").Bold().FontSize(13)
				barChart(c, "ov-cursor-chart", CursorBars(data.Days, now, 14), brandCursor(t), 170,
					func(v float64) string { return trimFloat(v) }, "Cursor 近 14 天每日请求次数")
			})
			card(c).Grow(1).Basis(0).Children(func() {
				ui.Text(c, "DeepSeek · 近 14 天花费").Bold().FontSize(13)
				cur := currencyOf(st.DeepSeek.Balance, data.Daily)
				barChart(c, "ov-ds-chart", DeepSeekBars(data.DSDays, cur, now, 14, 0), brandDeepSeek(t), 170,
					func(v float64) string { return util.FormatMoney(cur, v) }, "DeepSeek 近 14 天每日花费")
				if !dv.HasBill {
					hint(c, "需要平台登录信息才能看到每日账单，请到「设置」配置")
				}
			})
		})
	})
}

// bigCursorCard 概览页的 Cursor 大卡片。
func (a *App) bigCursorCard(c *ui.Context, vm CursorVM, st service.State) {
	t := c.Theme()
	brand := brandCursor(t)
	card(c).Grow(1).Basis(0).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			dot(c, brand)
			ui.Text(c, "Cursor").Bold().FontSize(14)
			ui.Spacer(c)
			label := vm.StatusText
			if vm.Status == service.StatusOK {
				label = "已同步"
			}
			statusPill(c, label, StatusTone(vm.Status, vm.Stale))
		})
		if !vm.Has {
			emptyState(c, vm.Message, "尚未获取到 Cursor 用量", func() { a.openSettings() })
			return
		}
		ui.Row(c).Gap(18).AlignItems(ui.Center).Children(func() {
			ring(c, "ov-ring", vm.Frac, 116, 11, levelColor(t, vm.Level, brand), vm.Used, "/ "+vm.Limit+" 次")
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, vm.Remain).FontSize(20).Bold().SingleLine()
				hint(c, vm.Reset).SingleLine()
				hint(c, vm.Average+" · 已用 "+vm.Percent).SingleLine()
				if vm.Exhaust != "" {
					ui.Text(c, vm.Exhaust).FontSize(12).TextColor(t.Warning)
				}
			})
		})
		hint(c, vm.Events+"  ·  来源："+orDash(vm.Source))
		staleNote(c, vm.Stale, vm.Message)
		if ui.Button(c.Key("ov-open-cursor"), "查看使用明细").Clicked() {
			a.ui.page = PageCursor
		}
	})
}

// bigDeepSeekCard 概览页的 DeepSeek 大卡片。
func (a *App) bigDeepSeekCard(c *ui.Context, vm DeepSeekVM, st service.State) {
	t := c.Theme()
	brand := brandDeepSeek(t)
	card(c).Grow(1).Basis(0).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			dot(c, brand)
			ui.Text(c, "DeepSeek").Bold().FontSize(14)
			ui.Spacer(c)
			label := vm.StatusText
			if vm.Status == service.StatusOK {
				label = "已同步"
			}
			statusPill(c, label, StatusTone(vm.Status, vm.Stale))
		})
		if !vm.HasBalance {
			emptyState(c, vm.Message, "尚未获取到 DeepSeek 余额", func() { a.openSettings() })
			return
		}
		ui.Column(c).Gap(2).Children(func() {
			hint(c, "剩余余额")
			col := t.Text
			if vm.LowBalance {
				col = t.Danger
			}
			ui.Text(c, vm.Balance).FontSize(38).Bold().TextColor(col).SingleLine()
			hint(c, vm.Breakdown)
		})
		if vm.HasBill {
			ui.Row(c).Gap(22).Children(func() {
				stat(c, "今日 Token", vm.TodayTokens, ui.Color{})
				stat(c, "今日花费", vm.TodayCost, ui.Color{})
				stat(c, vm.MonthLabel+"已用", vm.MonthCost, ui.Color{})
				if vm.TotalSpent != "" {
					stat(c, "累计已用", vm.TotalSpent, ui.Color{})
				}
			})
		} else if vm.NeedToken {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconWarn).FontSize(13).TextColor(t.Warning)
				hint(c, "未读取到平台登录信息，暂无今日 Token 与账单").Grow(1)
			})
		}
		staleNote(c, vm.Stale, vm.Message)
		if ui.Button(c.Key("ov-open-ds"), "查看账单").Clicked() {
			a.ui.page = PageDeepSeek
		}
	})
}

// orDash 空文本显示为 "-"。
func orDash(s string) string {
	if util.IsEmpty(s) {
		return "-"
	}
	return s
}

// openSettings 切到设置页并载入设置草稿。
func (a *App) openSettings() {
	a.ui.page = PageSettings
	a.loadSettingsDraft()
}
