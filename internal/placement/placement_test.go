package placement

import "testing"

var (
	main1   = Rect{0, 25, 1512, 957}     // 主屏工作区（菜单栏 25）
	left    = Rect{-1920, 0, 1920, 1055} // 左侧副屏
	above   = Rect{0, -1080, 1920, 1055} // 上方副屏
	rightSc = Rect{1512, 25, 1920, 1055} // 右侧副屏
)

// TestBelow 表驱动：覆盖居中、左右贴边、翻转、缩小等场景。
func TestBelow(t *testing.T) {
	cases := []struct {
		name   string
		anchor Rect
		w, h   int
		gap    int
		work   Rect
		want   Rect
	}{
		{"居中", Rect{1000, 0, 24, 25}, 360, 480, 4, main1, Rect{832, 29, 360, 480}},
		{"贴右边缘滑回", Rect{1490, 0, 24, 25}, 360, 480, 4, main1, Rect{1152, 29, 360, 480}},
		{"贴左边缘滑回", Rect{2, 0, 24, 25}, 360, 480, 4, main1, Rect{0, 29, 360, 480}},
		{"下方放不下翻到上方(Dock 式底部托盘)", Rect{700, 960, 24, 22}, 360, 480, 4, Rect{0, 0, 1512, 982}, Rect{532, 476, 360, 480}},
		{"上下都放不下则贴顶并缩小", Rect{700, 5, 24, 25}, 360, 2000, 4, main1, Rect{532, 25, 360, 957}},
		{"比工作区还宽则缩小", Rect{100, 0, 24, 25}, 5000, 200, 0, main1, Rect{0, 25, 1512, 200}},
		{"左侧副屏(负坐标)", Rect{-300, 0, 24, 25}, 360, 480, 4, left, Rect{-468, 29, 360, 480}},
		{"左侧副屏贴右缘", Rect{-10, 0, 24, 25}, 360, 480, 4, left, Rect{-360, 29, 360, 480}},
		{"上方副屏(负 Y)", Rect{900, -1080, 24, 25}, 360, 480, 4, above, Rect{732, -1051, 360, 480}},
		{"右侧副屏", Rect{3000, 25, 24, 25}, 360, 480, 4, rightSc, Rect{2832, 54, 360, 480}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Below(c.anchor, c.w, c.h, c.gap, c.work)
			if got != c.want {
				t.Errorf("got %+v want %+v", got, c.want)
			}
			if !got.Within(c.work, 0) {
				t.Errorf("结果 %+v 超出工作区 %+v", got, c.work)
			}
		})
	}
}

// TestDisplayFor 验证显示器选择：重叠最大、无重叠取最近、空列表。
func TestDisplayFor(t *testing.T) {
	works := []Rect{main1, left, rightSc}
	cases := []struct {
		name   string
		anchor Rect
		want   Rect
	}{
		{"在主屏", Rect{1000, 0, 24, 25}, main1},
		{"在左屏", Rect{-100, 0, 24, 25}, left},
		{"跨屏取重叠大者", Rect{1505, 25, 24, 25}, rightSc},
		{"无重叠取最近(屏幕下方)", Rect{700, 2000, 24, 25}, main1},
		{"无重叠取最近(远左)", Rect{-5000, 100, 24, 25}, left},
	}
	for _, c := range cases {
		got, ok := DisplayFor(c.anchor, works)
		if !ok || got != c.want {
			t.Errorf("%s: got %+v ok=%v want %+v", c.name, got, ok, c.want)
		}
	}
	if _, ok := DisplayFor(Rect{}, nil); ok {
		t.Error("空显示器列表应返回 false")
	}
}

// TestForTray 综合：先选屏再定位，且结果必在选中的屏内。
func TestForTray(t *testing.T) {
	works := []Rect{main1, left, above}
	for _, a := range []Rect{{1000, 0, 24, 25}, {-300, 0, 24, 25}, {900, -1080, 24, 25}, {1511, 0, 24, 25}} {
		got, ok := ForTray(a, 360, 480, 4, works)
		if !ok {
			t.Fatal("应成功")
		}
		w, _ := DisplayFor(a, works)
		if !got.Within(w, 0) {
			t.Errorf("anchor %+v: 结果 %+v 不在 %+v 内", a, got, w)
		}
	}
	if _, ok := ForTray(Rect{}, 1, 1, 0, nil); ok {
		t.Error("无显示器应失败")
	}
}

// TestRestore 验证窗口位置恢复：可见保持、拔屏后回到可见区、过大被缩小。
func TestRestore(t *testing.T) {
	works := []Rect{main1}
	cases := []struct {
		name  string
		saved Rect
		want  Rect
	}{
		{"完全可见", Rect{100, 100, 800, 600}, Rect{100, 100, 800, 600}},
		{"部分可见保持", Rect{-700, 100, 800, 600}, Rect{-700, 100, 800, 600}},
		{"副屏已拔除", Rect{-1500, 100, 800, 600}, Rect{356, 203, 800, 600}},
		{"远在下方", Rect{100, 5000, 800, 600}, Rect{356, 203, 800, 600}},
		{"过大缩小并居中", Rect{-9000, 0, 5000, 3000}, Rect{0, 25, 1512, 957}},
	}
	for _, c := range cases {
		if got := Restore(c.saved, works, 80); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
	if got := Restore(Rect{1, 2, 3, 4}, nil, 80); got != (Rect{1, 2, 3, 4}) {
		t.Error("无显示器信息应原样返回")
	}
}

// TestRectHelpers 覆盖 Rect 的小方法。
func TestRectHelpers(t *testing.T) {
	r := Rect{10, 20, 30, 40}
	if r.Right() != 40 || r.Bottom() != 60 || r.CenterX() != 25 || r.CenterY() != 40 || r.Empty() || !(Rect{}).Empty() {
		t.Error("helpers")
	}
	if !r.Within(Rect{0, 0, 100, 100}, 0) || r.Within(Rect{0, 0, 30, 30}, 0) || !r.Within(Rect{11, 21, 28, 38}, 1) {
		t.Error("Within")
	}
}
