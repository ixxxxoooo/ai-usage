package gui

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// brandCursor 返回 Cursor 的品牌色（随深浅色自适应）。
func brandCursor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#8E8CFF")
	}
	return ui.Hex("#5856D6")
}

// brandDeepSeek 返回 DeepSeek 的品牌色（随深浅色自适应）。
func brandDeepSeek(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#6F8BFF")
	}
	return ui.Hex("#4D6BFE")
}

// toneColor 把色调名转成主题色。
func toneColor(t *ui.Theme, tone string) ui.Color {
	switch tone {
	case "ok":
		return t.Success
	case "warn":
		return t.Warning
	case "bad":
		return t.Danger
	}
	return t.TextMuted
}

// levelColor 把额度级别转成颜色，正常时用 base。
func levelColor(t *ui.Theme, level int, base ui.Color) ui.Color {
	switch level {
	case LevelDanger:
		return t.Danger
	case LevelWarn:
		return t.Warning
	}
	return base
}

// card 创建一张卡片容器：在毛玻璃上是半透明填充，否则用主题 Surface。
//
// 参数 c：上下文。
// 返回值：卡片元素（调用方继续 Children）。
func card(c *ui.Context) ui.Element {
	t := c.Theme()
	bg := t.Surface
	if c.Vibrancy() {
		if t.Dark {
			bg = ui.RGBA(255, 255, 255, 0.07)
		} else {
			bg = ui.RGBA(255, 255, 255, 0.6)
		}
	}
	return ui.Column(c).Padding(12).Gap(8).Radius(12).Background(bg).Border(1, t.Border.Alpha(0.6))
}

// compactCard 创建紧凑卡片（托盘浮窗用）：内边距与间距比大面板的 card 更小。
//
// 参数 c：上下文。
// 返回值：卡片元素（调用方继续 Children）。
func compactCard(c *ui.Context) ui.Element {
	return card(c).Padding(9, 11, 9, 11).Gap(5).Radius(10)
}

// dot 画一个小圆点（状态指示）。
func dot(c *ui.Context, col ui.Color) ui.Element {
	return ui.Box(c).Size(7, 7).Radius(3.5).Background(col)
}

// pill 画一个带圆点的状态药丸。
//
// 参数 c：上下文；text：文字；col：颜色。
func pill(c *ui.Context, text string, col ui.Color) {
	ui.Row(c).Gap(5).Padding(2, 8).Radius(999).Background(col.Alpha(0.14)).Children(func() {
		dot(c, col)
		ui.Text(c, text).FontSize(11).TextColor(col).SingleLine()
	})
}

// iconButton 画一个扁平的图标按钮：悬停时出现底色，带无障碍名称与提示。
//
// 参数 c：上下文；key：稳定标识；icon：图标；label：名称与提示；spin：是否旋转（刷新中）。
// 返回值：点击后为 true。
func iconButton(c *ui.Context, key string, icon *ui.SVG, label string, spin bool) bool {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Size(28, 28).Radius(7).Center().Label(label).Tooltip(label).TextColor(t.TextMuted)
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.09)).TextColor(t.Text)
	}
	clicked := b.Clicked()
	b.Children(func() {
		ic := ui.Icon(c, icon).FontSize(15)
		if spin {
			ic.Rotate(ic.Loop("spin", time.Second, ui.Linear) * 360)
		}
	})
	return clicked
}

// arcPoints 在圆上生成从 from 到 to（弧度，自 12 点钟起顺时针）的折线路径。
func arcPath(cx, cy, r float32, from, to float64) *ui.Path {
	p := &ui.Path{}
	sweep := to - from
	steps := max(2, int(math.Ceil(math.Abs(sweep)/(math.Pi/90)))) // 约 2° 一段
	for i := 0; i <= steps; i++ {
		a := from + sweep*float64(i)/float64(steps)
		x := cx + r*float32(math.Sin(a))
		y := cy - r*float32(math.Cos(a))
		if i == 0 {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}
	return p
}

// ring 画一个环形进度，中间显示大字与小字；进度变化时平滑动画。
//
// 参数 c：上下文；key：稳定标识；frac：0~1 进度；size/width：直径与线宽；
// col：进度色；big/small：中心主/副文案。
func ring(c *ui.Context, key string, frac float64, size, width float32, col ui.Color, big, small string) {
	t := c.Theme()
	box := ui.Column(c.Key(key)).Size(size, size).Center().Label(big + " " + small)
	f := box.Animate("frac", float32(frac), 700*time.Millisecond)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := (min(r.W, r.H) - width) / 2
		p.StrokePath((&ui.Path{}).Circle(cx, cy, rad), width, t.Text.Alpha(0.1))
		if f > 0.001 {
			p.StrokePath(arcPath(cx, cy, rad, 0, float64(min(f, 1))*2*math.Pi), width, col)
		}
	})
	box.Children(func() {
		ui.Text(c, big).FontSize(size * 0.27).Bold().SingleLine()
		if small != "" {
			ui.Text(c, small).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
	})
}

// stat 画一个「标题 + 数值」的小块，用于汇总条。
//
// 参数 c：上下文；title：小标题；value：数值文案；col：数值颜色（零值用默认文字色）。
func stat(c *ui.Context, title, value string, col ui.Color) {
	t := c.Theme()
	ui.Column(c).Gap(2).Children(func() {
		ui.Text(c, title).FontSize(11).TextColor(t.TextMuted).SingleLine()
		v := ui.Text(c, value).FontSize(15).Bold().SingleLine()
		if col != (ui.Color{}) {
			v.TextColor(col)
		}
	})
}

// hint 画一行弱化的提示文字（可多行）。
func hint(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).FontSize(11).TextColor(c.Theme().TextMuted)
}

// 官网页面地址：账单页的「在官网查看」按钮会用系统默认浏览器打开。
const (
	CursorUsageURL   = "https://cursor.com/dashboard?tab=usage"
	DeepSeekUsageURL = "https://platform.deepseek.com/usage"
)

// linkButton 画一个「在官网查看」按钮：点击后用系统默认浏览器打开 url。
//
// 参数 c：上下文；key：稳定标识；label：按钮文案；url：要打开的地址。
func linkButton(c *ui.Context, key, label, url string) {
	if ui.Button(c.Key(key), label).Description("在浏览器打开 " + url).Clicked() {
		c.OpenURL(url)
	}
}

// clickableCard 让卡片可点击：鼠标变手型、悬停时加深底色，点击时执行 onClick。
//
// 参数 cd：已创建好内容的卡片；onClick：点击回调。
func clickableCard(c *ui.Context, cd ui.Element, onClick func()) {
	t := c.Theme()
	cd.Cursor(ui.CursorPointer)
	if cd.Hovered() {
		cd.Border(1, t.Accent.Alpha(0.55))
	}
	cd.OnClick(onClick)
}

// maxSelectHeight 是下拉列表的最大高度，超出时出现滚动条。
const maxSelectHeight = 260

// scrollSelect 画一个选项很多时也不会撑出窗口的下拉框：列表最高 maxSelectHeight，超出可滚动。
//
// 外观与 ui.Select 一致，键盘上下键与回车照常可用。
//
// 参数 c：上下文（调用方已 c.Key）；selected：当前选中；options：全部选项；width：触发按钮宽度；label：无障碍名称。
// 返回值：触发按钮元素。
func scrollSelect(c *ui.Context, selected *string, options []string, width float32, label string) ui.Element {
	t := c.Theme()
	sel := ui.SelectBase(c, selected)
	trig := sel.Trigger.Width(width).Label(label)
	trig.Padding(6, 10).Gap(6).Radius(t.Radius).Background(t.Surface).Border(1, t.Border).TextColor(t.Text).Justify(ui.SpaceBetween)
	if trig.Hovered() {
		trig.Background(t.SurfaceHover)
	}
	trig.Children(func() {
		ui.Text(c, *selected).SingleLine()
		ui.Box(c).Size(10, 10).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			var path ui.Path
			path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
			p.StrokePath(&path, 1.5, t.TextMuted)
		})
	})
	sel.Popup(func(panel ui.Element) {
		panel.Margin(4, 0, 0, 0).Padding(4).Radius(t.Radius+2).Background(t.Background).Border(1, t.Border).MinWidth(width)
		panel.Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		ui.Scroll(c.Key("select-scroll")).MaxHeight(maxSelectHeight).Children(func() {
			ui.Column(c).Children(func() {
				for _, opt := range options {
					item := sel.Item(opt).Padding(6, 10).Radius(t.Radius)
					switch {
					case item.Highlighted():
						item.Background(t.Accent).TextColor(t.AccentText)
					case opt == *selected:
						item.Background(t.Surface)
					}
					item.Children(func() { ui.Text(c, opt).SingleLine() })
				}
			})
		})
	})
	return trig
}
