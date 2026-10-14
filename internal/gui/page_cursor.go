package gui

import (
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/liwenjiao/ai-usage/internal/model"
	"github.com/liwenjiao/ai-usage/internal/service"
	"github.com/liwenjiao/ai-usage/internal/util"
)

// Cursor 明细页的时间范围。
var cursorRanges = []string{"今天", "近 7 天", "本周期", "全部"}

// cursorView 是按筛选条件算好的 Cursor 明细视图数据（带缓存）。
//
// @author ygw
type cursorView struct {
	Rows    []model.CursorEvent // 已筛选并按表格排序
	Days    []model.CursorDayStat
	ByModel []model.CursorModelStat
	Hours   [24]float64 // 「今天」按小时的次数
	Total   float64
	Tokens  int64
	EstUSD  float64
	Since   time.Time
}

// cursorSince 计算时间范围的起点（零值表示不限）。
//
// 参数 rng：范围编号；now：当前时间；cycleStart：本周期起点（可为零值）。
// 返回值：起点。
func cursorSince(rng int, now, cycleStart time.Time) time.Time {
	switch rng {
	case 0:
		return util.StartOfDay(now)
	case 1:
		return util.StartOfDay(now.AddDate(0, 0, -6))
	case 2:
		if !cycleStart.IsZero() {
			return cycleStart
		}
		return util.StartOfDay(now.AddDate(0, 0, -30))
	}
	return time.Time{}
}

// buildCursorView 按范围、模型与排序筛选并汇总明细。
//
// 参数 data：明细数据；since：起点（零值不限）；modelName：模型名（allModels 或空表示不限）；sortBy：排序；now：当前时间（决定「今天」与时区）。
// 返回值：cursorView。
func buildCursorView(data Data, since time.Time, modelName string, sortBy ui.SortOrder, now time.Time) cursorView {
	loc := now.Location()
	today := now.Format("2006-01-02")
	v := cursorView{Since: since}
	for _, e := range data.Events {
		if !since.IsZero() && e.Time.Before(since) {
			continue
		}
		if modelName != "" && modelName != allModels && e.Model != modelName {
			continue
		}
		v.Rows = append(v.Rows, e)
		v.Total += e.Requests
		v.Tokens += e.TotalTokens()
		v.EstUSD += e.TotalCents / 100
		if lt := e.Time.In(loc); lt.Format("2006-01-02") == today {
			v.Hours[lt.Hour()] += e.Requests
		}
	}
	v.Days = model.AggregateCursorByDay(v.Rows, loc)
	v.ByModel = model.AggregateCursorByModel(v.Rows)
	sortCursorRows(v.Rows, sortBy)
	return v
}

// sortCursorRows 按列排序明细（稳定排序；相同值保持时间倒序）。
//
// 参数 rows：待排序切片（原地）；o：排序方式。
func sortCursorRows(rows []model.CursorEvent, o ui.SortOrder) {
	less := func(i, j int) bool { return rows[i].Time.After(rows[j].Time) }
	switch o.Column {
	case "time":
		less = func(i, j int) bool { return rows[i].Time.Before(rows[j].Time) }
	case "model":
		less = func(i, j int) bool { return rows[i].Model < rows[j].Model }
	case "kind":
		less = func(i, j int) bool { return rows[i].KindLabel() < rows[j].KindLabel() }
	case "requests":
		less = func(i, j int) bool { return rows[i].Requests < rows[j].Requests }
	case "tokens":
		less = func(i, j int) bool { return rows[i].TotalTokens() < rows[j].TotalTokens() }
	case "cost":
		less = func(i, j int) bool { return rows[i].TotalCents < rows[j].TotalCents }
	default:
		return
	}
	if o.Descending {
		inner := less
		less = func(i, j int) bool { return inner(j, i) }
	}
	sort.SliceStable(rows, less)
}

// cursorViewCached 返回缓存的视图；筛选/排序/数据任一变化时重算。
func (a *App) cursorViewCached(st service.State, data Data, now time.Time) cursorView {
	cycle := time.Time{}
	if st.Cursor.HasUsage {
		cycle = st.Cursor.Usage.CycleStart
	}
	since := cursorSince(a.ui.cRange, now, cycle)
	key := fmt.Sprintf("%d|%s|%s|%t|%d|%d|%d", a.ui.cRange, a.ui.cModel, a.ui.cSort.Column, a.ui.cSort.Descending,
		data.LoadedAt.UnixNano(), len(data.Events), since.Unix())
	if a.ui.cCacheOK != key {
		a.ui.cCache = buildCursorView(data, since, a.ui.cModel, a.ui.cSort, now)
		a.ui.cCacheOK = key
	}
	return a.ui.cCache
}

// cursorHourBars 把今天按小时的次数转成 24 根柱子。
func cursorHourBars(hours [24]float64) []barDatum {
	out := make([]barDatum, 24)
	for h, v := range hours {
		out[h] = barDatum{Label: fmt.Sprintf("%d时", h), Value: v, Tip: fmt.Sprintf("%02d:00–%02d:59：%s 次", h, h, trimFloat(v))}
	}
	return out
}

// cursorDayCount 估算图表要显示多少天。
func cursorDayCount(rng int, now, since time.Time, data Data) int {
	switch rng {
	case 0, 1:
		return 7
	case 2:
		return int(min(max(now.Sub(since).Hours()/24+1, 7), 45))
	}
	if len(data.Events) == 0 {
		return 14
	}
	oldest := data.Events[len(data.Events)-1].Time
	return int(min(max(now.Sub(oldest).Hours()/24+1, 14), 90))
}

// cursorPage Cursor 明细页。
func (a *App) cursorPage(c *ui.Context, st service.State) {
	t := c.Theme()
	now := a.now()
	data := a.Data()
	vm := BuildCursorVM(st.Cursor, now)
	view := a.cursorViewCached(st, data, now)

	sub := vm.Events
	if vm.Has {
		sub = fmt.Sprintf("本周期已用 %s / %s 次 · %s", vm.Used, vm.Limit, vm.Reset)
	}
	pageTitle(c, "Cursor 明细", sub, func() {
		if ui.Button(c.Key("cu-export"), "导出 CSV").Disabled(len(view.Rows) == 0).Clicked() {
			a.exportCursorCSV(view.Rows)
		}
		if ui.Button(c.Key("cu-refresh"), "刷新").Disabled(st.Refreshing).Clicked() {
			a.RefreshNow()
		}
	})
	pageBody(c, "cu-scroll", func() {
		if !vm.Has && len(data.Events) == 0 {
			card(c).Children(func() {
				emptyState(c, vm.Message, "尚未获取到 Cursor 数据", func() { a.openSettings() })
			})
			return
		}
		staleNote(c, vm.Stale, vm.Message)
		ui.Row(c).Gap(10).Children(func() {
			ui.Segmented(c.Key("cu-range"), &a.ui.cRange, cursorRanges...).Label("时间范围")
			models := append([]string{allModels}, data.Models...)
			ui.Select(c.Key("cu-model"), &a.ui.cModel, models).Label("模型").Width(210)
			ui.Spacer(c)
			hint(c, fmt.Sprintf("共 %s 条", util.FormatInt(int64(len(view.Rows))))).SingleLine()
		})
		ui.Row(c).Gap(10).AlignItems(ui.Stretch).Children(func() {
			summaryCard(c, "筛选范围内次数", trimFloat(view.Total)+" 次", brandCursor(t))
			summaryCard(c, "请求条数", util.FormatInt(int64(len(view.Rows))), ui.Color{})
			summaryCard(c, "Token 合计", util.FormatTokens(view.Tokens), ui.Color{})
			summaryCard(c, "API 估值", fmt.Sprintf("$%.2f", view.EstUSD), ui.Color{})
		})
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			card(c).Grow(3).Basis(0).Children(func() {
				title := "每日请求次数"
				bars := CursorBars(view.Days, now, cursorDayCount(a.ui.cRange, now, view.Since, data))
				if a.ui.cRange == 0 {
					title, bars = "今日按小时请求次数", cursorHourBars(view.Hours)
				}
				ui.Text(c, title).Bold().FontSize(13)
				barChart(c, "cu-chart", bars, brandCursor(t), 160, func(v float64) string { return trimFloat(v) }, "Cursor "+title)
			})
			card(c).Grow(2).Basis(0).Children(func() {
				ui.Text(c, "按模型").Bold().FontSize(13)
				if len(view.ByModel) == 0 {
					hint(c, "暂无数据")
				}
				for i, m := range view.ByModel {
					if i >= 6 {
						break
					}
					frac := 0.0
					if view.Total > 0 {
						frac = m.Requests / view.Total
					}
					shareBar(c, m.Model, frac, brandCursor(t), fmt.Sprintf("%s 次 · %d%%", trimFloat(m.Requests), int(frac*100+0.5)))
				}
			})
		})
		a.cursorTable(c, view)
	})
}

// summaryCard 画汇总小卡片：小标题 + 数值。
func summaryCard(c *ui.Context, title, value string, col ui.Color) {
	card(c).Grow(1).Basis(0).Gap(2).Children(func() {
		stat(c, title, value, col)
	})
}

// cursorTable 画明细表格（虚拟列表，只构建可见行）。
func (a *App) cursorTable(c *ui.Context, view cursorView) {
	t := c.Theme()
	cols := []ui.TableColumn{
		{Title: "时间", ID: "time", Width: 150, Sortable: true},
		{Title: "模型", ID: "model", Sortable: true},
		{Title: "类型", ID: "kind", Width: 96, Sortable: true},
		{Title: "次数", ID: "requests", Width: 64, Align: ui.End, Sortable: true},
		{Title: "Token", ID: "tokens", Width: 84, Align: ui.End, Sortable: true},
		{Title: "API 估值", ID: "cost", Width: 90, Align: ui.End, Sortable: true},
	}
	a.ui.cList.Selected = &a.ui.cSel
	a.ui.cList.Sort = &a.ui.cSort
	rows := view.Rows
	a.ui.cList.Key = func(i int) any {
		if i >= 0 && i < len(rows) {
			return rows[i].Key
		}
		return i
	}
	a.ui.cList.Label = func(i int) string {
		if i >= 0 && i < len(rows) {
			return rows[i].Model
		}
		return ""
	}
	box := ui.Column(c).Height(420).Radius(10).Border(1, t.Border.Alpha(0.7)).Clip()
	box.Children(func() {
		if len(rows) == 0 {
			ui.Column(c).Fill().Center().Children(func() { hint(c, "这个范围内没有明细") })
			return
		}
		ui.Table(c.Key("cu-table"), &a.ui.cList, cols, len(rows), func(row, col int) {
			if row < 0 || row >= len(rows) {
				return
			}
			e := rows[row]
			switch col {
			case 0:
				ui.Text(c, e.Time.Local().Format("01-02 15:04:05")).SingleLine().FontSize(12)
			case 1:
				ui.Text(c, e.Model).SingleLine().FontSize(12)
			case 2:
				ui.Text(c, e.KindLabel()).SingleLine().FontSize(12).TextColor(t.TextMuted)
			case 3:
				ui.Text(c, trimFloat(e.Requests)).SingleLine().FontSize(12)
			case 4:
				ui.Text(c, util.FormatTokens(e.TotalTokens())).SingleLine().FontSize(12)
			case 5:
				ui.Text(c, fmt.Sprintf("$%.3f", e.TotalCents/100)).SingleLine().FontSize(12).TextColor(t.TextMuted)
			}
		}).Grow(1).Label("Cursor 使用明细")
	})
}
