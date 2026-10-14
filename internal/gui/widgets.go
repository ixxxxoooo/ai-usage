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
