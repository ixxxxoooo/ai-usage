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

// DeepSeek 账单页的时间范围与指标。
var (
	deepseekRanges  = []string{"近 7 天", "近 30 天", "本月", "全部"}
	deepseekMetrics = []string{"花费", "Token"}
)

// deepseekView 是按范围筛选好的 DeepSeek 账单视图数据（带缓存）。
//
// @author ygw
type deepseekView struct {
	Rows     []model.DeepSeekDaily // 已筛选并按表格排序
	Days     []model.DeepSeekDayStat
	Cost     float64
	Tokens   int64
	Requests int64
	ByModel  []modelCost
	Since    string // 起点日期（空表示不限）
}

// modelCost 是按模型汇总的花费。
type modelCost struct {
	Model  string
	Cost   float64
	Tokens int64
}

// deepseekSince 计算范围起点日期键（空串表示不限）。
//
// 参数 rng：范围编号；now：当前时间。
// 返回值：YYYY-MM-DD 或空串。
func deepseekSince(rng int, now time.Time) string {
	switch rng {
	case 0:
		return now.AddDate(0, 0, -6).Format("2006-01-02")
	case 1:
		return now.AddDate(0, 0, -29).Format("2006-01-02")
	case 2:
		return now.Format("2006-01") + "-01"
	}
	return ""
}

// buildDeepSeekView 按起点日期筛选账单并汇总、排序。
//
// 参数 data：数据；since：起点日期（含，空表示不限）；o：表格排序。
// 返回值：deepseekView。
func buildDeepSeekView(data Data, since string, o ui.SortOrder) deepseekView {
	v := deepseekView{Since: since}
	byModel := map[string]*modelCost{}
	for _, r := range data.Daily {
		if since != "" && r.Date < since {
			continue
		}
		v.Rows = append(v.Rows, r)
		v.Cost += r.Cost
		v.Tokens += r.Tokens()
		v.Requests += r.Requests
		m := byModel[r.Model]
		if m == nil {
			m = &modelCost{Model: r.Model}
			byModel[r.Model] = m
		}
		m.Cost += r.Cost
		m.Tokens += r.Tokens()
	}
	v.Days = model.AggregateDeepSeekByDay(v.Rows)
	for _, m := range byModel {
		v.ByModel = append(v.ByModel, *m)
	}
	sort.Slice(v.ByModel, func(i, j int) bool {
		if v.ByModel[i].Cost != v.ByModel[j].Cost {
			return v.ByModel[i].Cost > v.ByModel[j].Cost
		}
		return v.ByModel[i].Model < v.ByModel[j].Model
	})
	sortDeepSeekRows(v.Rows, o)
	return v
}

// sortDeepSeekRows 按列排序账单行（稳定排序）。
//
// 参数 rows：待排序切片（原地）；o：排序方式。
func sortDeepSeekRows(rows []model.DeepSeekDaily, o ui.SortOrder) {
	var less func(i, j int) bool
	switch o.Column {
	case "date":
		less = func(i, j int) bool { return rows[i].Date < rows[j].Date }
	case "model":
		less = func(i, j int) bool { return rows[i].Model < rows[j].Model }
	case "requests":
		less = func(i, j int) bool { return rows[i].Requests < rows[j].Requests }
	case "hit":
		less = func(i, j int) bool { return rows[i].CacheHit < rows[j].CacheHit }
	case "miss":
		less = func(i, j int) bool { return rows[i].CacheMiss < rows[j].CacheMiss }
	case "output":
		less = func(i, j int) bool { return rows[i].Output < rows[j].Output }
	case "tokens":
		less = func(i, j int) bool { return rows[i].Tokens() < rows[j].Tokens() }
	case "cost":
		less = func(i, j int) bool { return rows[i].Cost < rows[j].Cost }
	default:
		return
	}
	if o.Descending {
		inner := less
		less = func(i, j int) bool { return inner(j, i) }
	}
	sort.SliceStable(rows, less)
}

// deepseekViewCached 返回缓存的账单视图。
func (a *App) deepseekViewCached(data Data, now time.Time) deepseekView {
	since := deepseekSince(a.ui.dRange, now)
	key := fmt.Sprintf("%d|%s|%t|%d|%d|%s", a.ui.dRange, a.ui.dSort.Column, a.ui.dSort.Descending, data.LoadedAt.UnixNano(), len(data.Daily), since)
	if a.ui.dCacheOK != key {
		a.ui.dCache = buildDeepSeekView(data, since, a.ui.dSort)
		a.ui.dCacheOK = key
	}
	return a.ui.dCache
}

// deepseekDayCount 估算图表要显示多少天。
func deepseekDayCount(rng int, now time.Time, view deepseekView) int {
	switch rng {
	case 0:
		return 7
	case 1:
		return 30
	case 2:
		return now.Day()
	}
	if len(view.Days) == 0 {
		return 30
	}
	first, err := time.ParseInLocation("2006-01-02", view.Days[0].Date, now.Location())
	if err != nil {
		return 30
	}
	return int(min(max(now.Sub(first).Hours()/24+1, 14), 120))
}

// deepseekPage DeepSeek 账单页。
func (a *App) deepseekPage(c *ui.Context, st service.State) {
	t := c.Theme()
	now := a.now()
	data := a.Data()
	vm := BuildDeepSeekVM(st.DeepSeek, st.Settings, now)
	view := a.deepseekViewCached(data, now)
	cur := currencyOf(st.DeepSeek.Balance, data.Daily)
	sub := "余额 " + orDash(vm.Balance)
	if vm.HasBill {
		sub += " · " + vm.MonthLabel + "已用 " + vm.MonthCost
	}
	pageTitle(c, "DeepSeek 账单", sub, func() {
		if ui.Button(c.Key("ds-export"), "导出 CSV").Disabled(len(view.Rows) == 0).Clicked() {
			a.exportDeepSeekCSV(view.Rows)
		}
		if ui.Button(c.Key("ds-refresh"), "刷新").Disabled(st.Refreshing).Clicked() {
			a.RefreshNow()
		}
	})
	pageBody(c, "ds-scroll", func() {
		if !vm.HasBalance && len(data.Daily) == 0 {
			card(c).Children(func() {
				emptyState(c, vm.Message, "尚未获取到 DeepSeek 数据", func() { a.openSettings() })
			})
			return
		}
		staleNote(c, vm.Stale, vm.Message)
		if vm.NeedToken && len(data.Daily) == 0 {
			card(c).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconWarn).FontSize(14).TextColor(t.Warning)
					ui.Text(c, "只配置了 API Key：能看到余额，但看不到账单与今日 Token。请到设置里让应用读取 Chrome 登录信息，或手动粘贴平台 token。").FontSize(12).Grow(1)
					if ui.Button(c.Key("ds-go-set"), "去设置").Clicked() {
						a.openSettings()
					}
				})
			})
		}
		ui.Row(c).Gap(10).AlignItems(ui.Stretch).Children(func() {
			summaryCard(c, "剩余余额", orDash(vm.Balance), brandDeepSeek(t))
			summaryCard(c, "范围内花费", util.FormatMoney(cur, view.Cost), ui.Color{})
			summaryCard(c, "范围内 Token", util.FormatTokens(view.Tokens), ui.Color{})
			summaryCard(c, "范围内请求", util.FormatInt(view.Requests), ui.Color{})
			if vm.TotalSpent != "" {
				summaryCard(c, "累计已用", vm.TotalSpent, ui.Color{})
			}
		})
		ui.Row(c).Gap(10).Children(func() {
			ui.Segmented(c.Key("ds-range"), &a.ui.dRange, deepseekRanges...).Label("时间范围")
			ui.Segmented(c.Key("ds-metric"), &a.ui.dMetric, deepseekMetrics...).Label("图表指标")
			ui.Spacer(c)
			hint(c, fmt.Sprintf("共 %d 行", len(view.Rows))).SingleLine()
		})
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			card(c).Grow(3).Basis(0).Children(func() {
				title := "每日" + deepseekMetrics[a.ui.dMetric]
				ui.Text(c, title).Bold().FontSize(13)
				fmtY := func(v float64) string { return util.FormatMoney(cur, v) }
				if a.ui.dMetric == 1 {
					fmtY = func(v float64) string { return util.FormatTokens(int64(v)) }
				}
				barChart(c, "ds-chart", DeepSeekBars(view.Days, cur, now, deepseekDayCount(a.ui.dRange, now, view), a.ui.dMetric),
					brandDeepSeek(t), 160, fmtY, "DeepSeek "+title)
			})
			card(c).Grow(2).Basis(0).Children(func() {
				ui.Text(c, "按模型花费").Bold().FontSize(13)
				if len(view.ByModel) == 0 {
					hint(c, "暂无数据")
				}
				for i, m := range view.ByModel {
					if i >= 6 {
						break
					}
					frac := 0.0
					if view.Cost > 0 {
						frac = m.Cost / view.Cost
					}
					shareBar(c, m.Model, frac, brandDeepSeek(t), fmt.Sprintf("%s · %d%%", util.FormatMoney(cur, m.Cost), int(frac*100+0.5)))
				}
			})
		})
		a.deepseekTable(c, view)
	})
}

// deepseekTable 画账单明细表格。
func (a *App) deepseekTable(c *ui.Context, view deepseekView) {
	t := c.Theme()
	cols := []ui.TableColumn{
		{Title: "日期", ID: "date", Width: 100, Sortable: true},
		{Title: "模型", ID: "model", Sortable: true},
		{Title: "请求", ID: "requests", Width: 64, Align: ui.End, Sortable: true},
		{Title: "缓存命中", ID: "hit", Width: 86, Align: ui.End, Sortable: true},
		{Title: "缓存未命中", ID: "miss", Width: 96, Align: ui.End, Sortable: true},
		{Title: "输出", ID: "output", Width: 80, Align: ui.End, Sortable: true},
		{Title: "总 Token", ID: "tokens", Width: 86, Align: ui.End, Sortable: true},
		{Title: "费用", ID: "cost", Width: 86, Align: ui.End, Sortable: true},
	}
	a.ui.dList.Selected = &a.ui.dSel
	a.ui.dList.Sort = &a.ui.dSort
	rows := view.Rows
	a.ui.dList.Key = func(i int) any {
		if i >= 0 && i < len(rows) {
			return rows[i].Date + "|" + rows[i].Model
		}
		return i
	}
	a.ui.dList.Label = func(i int) string {
		if i >= 0 && i < len(rows) {
			return rows[i].Date + " " + rows[i].Model
		}
		return ""
	}
	box := ui.Column(c).Height(420).Radius(10).Border(1, t.Border.Alpha(0.7)).Clip()
	box.Children(func() {
		if len(rows) == 0 {
			ui.Column(c).Fill().Center().Children(func() { hint(c, "这个范围内没有账单") })
			return
		}
		ui.Table(c.Key("ds-table"), &a.ui.dList, cols, len(rows), func(row, col int) {
			if row < 0 || row >= len(rows) {
				return
			}
			r := rows[row]
			var s string
			switch col {
			case 0:
				s = r.Date
			case 1:
				s = r.Model
			case 2:
				s = util.FormatInt(r.Requests)
			case 3:
				s = util.FormatTokens(r.CacheHit)
			case 4:
				s = util.FormatTokens(r.CacheMiss)
			case 5:
				s = util.FormatTokens(r.Output)
			case 6:
				s = util.FormatTokens(r.Tokens())
			case 7:
				s = util.FormatMoney(r.Currency, r.Cost)
			}
			ui.Text(c, s).SingleLine().FontSize(12)
		}).Grow(1).Label("DeepSeek 账单明细")
	})
}
