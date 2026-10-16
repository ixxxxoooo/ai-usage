package gui

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/service"
)

// AntigravityURL 是 Antigravity 官网地址（「在官网查看」使用）。
const AntigravityURL = "https://antigravity.google"

// antigravityPage Antigravity 额度页：按额度池展示，再列出全部模型。
//
// 参数 c：上下文；st：整体状态。
func (a *App) antigravityPage(c *ui.Context, st service.State) {
	t := c.Theme()
	vm := BuildAntigravityVM(st.Antigravity, a.now())
	sub := "读取本机正在运行的 Antigravity，不需要登录凭据"
	if vm.Has {
		sub = orDash(vm.Plan) + " · 更新于 " + vm.Updated
	}
	pageTitle(c, "Antigravity 额度", sub, func() {
		linkButton(c, "ag-web", "官网", AntigravityURL)
		if ui.Button(c.Key("ag-refresh"), "刷新").Disabled(st.Refreshing).Clicked() {
			a.RefreshNow()
		}
	})
	pageBody(c, "ag-scroll", func() {
		if vm.Disabled {
			card(c).Children(func() {
				emptyState(c, "已在设置中关闭 Antigravity 额度查询", "Antigravity 额度查询已关闭", func() { a.openSettings() })
			})
			return
		}
		if !vm.Has {
			card(c).Children(func() {
				msg := vm.Message
				if msg == "" {
					msg = "尚未获取到 Antigravity 额度"
				}
				ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, iconWarn).FontSize(14).TextColor(t.Warning)
					ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted).Grow(1)
				})
			})
			return
		}
		staleNote(c, vm.Stale, vm.Message)
		brand := brandAntigravity(t)
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			for i, p := range vm.Pools {
				card(c).Grow(1).Basis(0).Padding(16).Gap(10).Children(func() {
					ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
						ring(c, "ag-ring-"+strconv.Itoa(i), p.Frac, 84, 9, levelColor(t, p.Level, brand), pctText(p.Frac), "已用")
						ui.Column(c).Grow(1).Gap(3).Children(func() {
							ui.Text(c, p.Name).Bold().FontSize(14).SingleLine()
							ui.Text(c, p.Remain).FontSize(16).Bold().SingleLine()
							hint(c, p.Reset).SingleLine()
						})
					})
					hint(c, p.Models)
				})
			}
		})
		card(c).Gap(10).Children(func() {
			ui.Text(c, "全部模型").Bold().FontSize(13)
			for _, m := range vm.Models {
				col := levelColor(t, m.Level, brand)
				if m.Unavailable {
					col = t.TextMuted
				}
				shareBar(c, m.Label, 1-m.Frac, col, m.Remain+" · "+m.Reset)
			}
			hint(c, "额度按时间窗口自动恢复；同一组的模型共用一份额度。数据来自本机 Antigravity，需要它正在运行。")
		})
	})
}

// pctText 把 0~1 的比例转成 "72%"。
func pctText(f float64) string { return strconv.Itoa(int(f*100+0.5)) + "%" }

// antigravityCard 托盘浮窗里的 Antigravity 紧凑卡片：每个额度池一行（名称、剩余、进度条、重置）。
func (a *App) antigravityCard(c *ui.Context, vm AntigravityVM) {
	t := c.Theme()
	brand := brandAntigravity(t)
	cd := compactCard(c)
	cd.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			dot(c, brand)
			ui.Text(c, "Antigravity").Bold().FontSize(12)
			if vm.Plan != "" {
				hint(c, vm.Plan).SingleLine()
			}
			ui.Spacer(c)
			label := vm.StatusText
			if vm.Status == service.StatusOK {
				label = "已同步"
			}
			statusPill(c, label, StatusTone(vm.Status, vm.Stale))
		})
		if !vm.Has {
			msg := vm.Message
			if msg == "" {
				msg = "尚未获取到 Antigravity 额度"
			}
			ui.Row(c).Gap(5).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, iconWarn).FontSize(12).TextColor(t.Warning)
				ui.Text(c, msg).FontSize(11).TextColor(t.TextMuted).Grow(1)
			})
			return
		}
		for i, p := range vm.Pools {
			if i >= 2 { // 浮窗里最多两行，其余在详情页看
				break
			}
			ui.Column(c).Gap(2).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, p.Name).FontSize(12).SingleLine().Grow(1)
					ui.Text(c, p.Remain).FontSize(12).Bold().SingleLine().TextColor(levelColor(t, p.Level, t.Text))
				})
				ui.Box(c).Height(5).Radius(2.5).Background(t.Text.Alpha(0.08)).Children(func() {
					ui.Box(c).Height(5).Radius(2.5).Background(levelColor(t, p.Level, brand)).WidthPercent(float32(max(0.02, min(1-p.Frac, 1))) * 100)
				})
				hint(c, p.Reset).SingleLine()
			})
		}
	})
	clickableCard(c, cd, func() { a.openPage(PageAntigravity) })
}

// bigAntigravityCard 概览页的 Antigravity 大卡片。
func (a *App) bigAntigravityCard(c *ui.Context, vm AntigravityVM) {
	t := c.Theme()
	brand := brandAntigravity(t)
	card(c).Grow(1).Basis(0).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			dot(c, brand)
			ui.Text(c, "Antigravity").Bold().FontSize(14)
			if vm.Plan != "" {
				hint(c, vm.Plan).SingleLine()
			}
			ui.Spacer(c)
			label := vm.StatusText
			if vm.Status == service.StatusOK {
				label = "已同步"
			}
			statusPill(c, label, StatusTone(vm.Status, vm.Stale))
		})
		if !vm.Has {
			msg := vm.Message
			if msg == "" {
				msg = "尚未获取到 Antigravity 额度"
			}
			ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
				ui.Icon(c, iconWarn).FontSize(14).TextColor(t.Warning)
				ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted).Grow(1)
			})
			return
		}
		ui.Row(c).Gap(24).AlignItems(ui.Start).Children(func() {
			for _, p := range vm.Pools {
				ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
					shareBar(c, p.Name, 1-p.Frac, levelColor(t, p.Level, brand), p.Remain)
					hint(c, p.Reset).SingleLine()
				})
			}
		})
		staleNote(c, vm.Stale, vm.Message)
		if ui.Button(c.Key("ov-open-ag"), "查看额度详情").Clicked() {
			a.ui.page = PageAntigravity
		}
	})
}
