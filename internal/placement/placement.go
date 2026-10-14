// Package placement 提供浮窗相对托盘图标的定位纯函数。
//
// 真正的定位由 MyGo 的 Flyout/NSPopover 完成；这里的纯函数描述「期望」的位置，
// 既用于表驱动单元测试，也用于运行期自检（--selftest）去比对真实浮窗位置。
// 坐标系与 MyGo 一致：DIP，原点在主屏左上角，Y 向下增长，可为负（副屏在主屏左/上方）。
//
// @author ygw
package placement

// Rect 是屏幕坐标系下的矩形。
type Rect struct{ X, Y, W, H int }

// Right 返回右边界（不含）。
func (r Rect) Right() int { return r.X + r.W }

// Bottom 返回下边界（不含）。
func (r Rect) Bottom() int { return r.Y + r.H }

// CenterX 返回水平中心。
func (r Rect) CenterX() int { return r.X + r.W/2 }

// CenterY 返回垂直中心。
func (r Rect) CenterY() int { return r.Y + r.H/2 }

// Empty 判断矩形是否为空（宽或高不为正）。
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Within 判断 r 是否完整落在 outer 内（允许 tol 像素误差）。
func (r Rect) Within(outer Rect, tol int) bool {
	return r.X >= outer.X-tol && r.Y >= outer.Y-tol &&
		r.Right() <= outer.Right()+tol && r.Bottom() <= outer.Bottom()+tol
}

// overlap 返回两个矩形重叠区域的面积。
func overlap(a, b Rect) int {
	w := min(a.Right(), b.Right()) - max(a.X, b.X)
	h := min(a.Bottom(), b.Bottom()) - max(a.Y, b.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// dist2 返回两个矩形中心点的平方距离。
func dist2(a, b Rect) int {
	dx, dy := a.CenterX()-b.CenterX(), a.CenterY()-b.CenterY()
	return dx*dx + dy*dy
}

// DisplayFor 选出锚点所在的显示器工作区：重叠面积最大者；都不重叠则取中心最近者。
//
// 参数 anchor：托盘图标的屏幕矩形；works：各显示器的工作区（不含菜单栏/Dock）。
// 返回值：选中的工作区；works 为空时返回零值与 false。
func DisplayFor(anchor Rect, works []Rect) (Rect, bool) {
	if len(works) == 0 {
		return Rect{}, false
	}
	best, bestArea := works[0], -1
	for _, w := range works {
		if a := overlap(anchor, w); a > bestArea {
			best, bestArea = w, a
		}
	}
	if bestArea > 0 {
		return best, true
	}
	best, bestD := works[0], dist2(anchor, works[0])
	for _, w := range works[1:] {
		if d := dist2(anchor, w); d < bestD {
			best, bestD = w, d
		}
	}
	return best, true
}

// Below 计算浮窗应放置的位置：水平居中于锚点，垂直紧贴锚点下方（间距 gap）。
//
// 规则（与 MyGo Flyout 文档一致）：
//  1. 下方放不下则翻到锚点上方；
//  2. 水平/垂直方向滑动回工作区内；
//  3. 仍放不下则缩小尺寸以适配工作区。
//
// 参数 anchor：托盘图标矩形；w、h：期望尺寸；gap：与锚点的间距；work：所在显示器工作区。
// 返回值：期望的浮窗矩形。
func Below(anchor Rect, w, h, gap int, work Rect) Rect {
	w, h = min(w, work.W), min(h, work.H)
	r := Rect{X: anchor.CenterX() - w/2, Y: anchor.Bottom() + gap, W: w, H: h}
	if r.Bottom() > work.Bottom() { // 下方放不下，翻到上方
		if up := anchor.Y - gap - h; up >= work.Y {
			r.Y = up
		}
	}
	r.X = clamp(r.X, work.X, work.Right()-w)
	r.Y = clamp(r.Y, work.Y, work.Bottom()-h)
	return r
}

// ForTray 综合选择显示器并计算浮窗位置。
//
// 参数 anchor：托盘图标矩形；w、h：期望尺寸；gap：间距；works：全部显示器工作区。
// 返回值：期望矩形；没有显示器信息时返回 false。
func ForTray(anchor Rect, w, h, gap int, works []Rect) (Rect, bool) {
	work, ok := DisplayFor(anchor, works)
	if !ok {
		return Rect{}, false
	}
	return Below(anchor, w, h, gap, work), true
}

// Restore 校正一个被记忆的窗口矩形，使其至少有一部分落在某个显示器内：
// 显示器被拔掉后窗口不会跑到看不见的地方。
//
// 参数 saved：记忆的矩形；works：当前各显示器工作区；minVisible：至少要露出的像素边长。
// 返回值：校正后的矩形（完全不可见时居中到第一个工作区，并缩到不超过工作区）。
func Restore(saved Rect, works []Rect, minVisible int) Rect {
	if len(works) == 0 {
		return saved
	}
	for _, w := range works {
		vis := Rect{X: max(saved.X, w.X), Y: max(saved.Y, w.Y)}
		vis.W = min(saved.Right(), w.Right()) - vis.X
		vis.H = min(saved.Bottom(), w.Bottom()) - vis.Y
		if vis.W >= minVisible && vis.H >= minVisible {
			return saved
		}
	}
	w := works[0]
	out := Rect{W: min(saved.W, w.W), H: min(saved.H, w.H)}
	out.X = w.X + (w.W-out.W)/2
	out.Y = w.Y + (w.H-out.H)/2
	return out
}

// clamp 把 v 限制在 [lo,hi]；hi<lo 时取 lo。
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}
