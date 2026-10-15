package gui

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

// barDatum 是柱状图的一根柱子。
type barDatum struct {
	Label string  // X 轴短标签，如 "10/14"
	Value float64 // 数值
	Tip   string  // 悬停提示，如 "10月14日：12 次"
}

// barChart 画带网格线、Y 轴刻度、X 轴稀疏标签与悬停提示的柱状图。
//
// 参数 c：上下文；key：稳定标识；data：数据；col：柱色；height：高度；
// fmtY：Y 轴刻度格式化（nil 用默认）；label：无障碍名称。
func barChart(c *ui.Context, key string, data []barDatum, col ui.Color, height float32, fmtY func(float64) string, label string) {
	t := c.Theme()
	hi := 0.0
	for _, d := range data {
		hi = max(hi, d.Value)
	}
	if len(data) == 0 || hi <= 0 {
		ui.Column(c.Key(key)).Height(height).Center().Children(func() {
			hint(c, "暂无数据")
		})
		return
	}
	labels := make([]string, len(data))
	for i := range labels {
		labels[i] = strconv.Itoa(i)
	}
	x := chart.NewBand(labels...).Padding(0.22, 0.04)
	y := chart.NewLinear(0, hi).Nice(4)
	y.Format = fmtY
	yTicks := y.Ticks(4)
	step := (len(data) + 6) / 7 // X 轴最多约 7 个标签
	var xTicks []chart.Tick
	for i := 0; i < len(data); i += step {
		xTicks = append(xTicks, chart.Tick{At: x.At(i), Label: data[i].Label})
	}
	axisLabel := func(_ chart.Side, tick chart.Tick) {
		ui.Text(c, tick.Label).NoWrap().FontSize(10).TextColor(t.TextMuted)
	}
	hov := -1 // 鼠标正悬停的柱子
	var hovBar ui.Element
	chart.Plot(c.Key(key), chart.Axes{Left: yTicks, Bottom: xTicks, Label: axisLabel}, func(area ui.Element) {
		area.Draw(func(p *ui.Painter, r ui.Rect) {
			for _, tick := range yTicks {
				yy := chart.Y(r, tick.At)
				p.Line(r.X, yy, r.X+r.W, yy, 1, t.Text.Alpha(0.07))
			}
		})
		for i, d := range data {
			if d.Value <= 0 {
				continue
			}
			bar := chart.Rect(c.Key(fmt.Sprintf("%s-%d", key, i)), x.Band(i), y.Span(0, d.Value)).Radius(3).Description(d.Tip)
			if bar.Hovered() {
				bar.Background(col)
				hov, hovBar = i, bar
			} else {
				bar.Background(col.Alpha(0.78))
			}
		}
	}).Label(label).Height(height).Gap(6)
	if hov >= 0 && data[hov].Tip != "" {
		barTip(c, hovBar, data[hov].Tip)
	}
}

// barTip 在被悬停的柱子正上方立刻显示一个提示气泡（不像系统提示要等 0.6 秒），内容如 "10月15日：368 次 · 313 条"。
//
// 参数 c：上下文；bar：被悬停的柱子；text：提示文字。
func barTip(c *ui.Context, bar ui.Element, text string) {
	t := c.Theme()
	ui.Overlay(c, func() {
		tip := ui.Box(c).Absolute().PassThrough().Padding(5, 9).Radius(7).Background(t.Text.Alpha(0.92))
		tip.Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.25))
		tip.AttachTo(bar, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 6, 0)
		tip.Children(func() {
			ui.Text(c, text).FontSize(12).TextColor(t.Background).NoWrap()
		})
	})
}

// shareBar 画一行「名称 + 占比条 + 数值」，用于按模型占比。
//
// 参数 c：上下文；name：名称；frac：0~1 占比；col：颜色；value：右侧数值文案。
func shareBar(c *ui.Context, name string, frac float64, col ui.Color, value string) {
	t := c.Theme()
	ui.Column(c).Gap(3).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, name).FontSize(12).SingleLine().Grow(1)
			ui.Text(c, value).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})
		ui.Box(c).Height(6).Radius(3).Background(t.Text.Alpha(0.08)).Children(func() {
			ui.Box(c).Height(6).Radius(3).Background(col).WidthPercent(float32(max(0.02, min(frac, 1))) * 100)
		})
	})
}
