package gui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestScrollSelectCapsHeight 选项很多的下拉：列表高度受限，只显示前面一部分，滚动后才能看到后面的选项。
func TestScrollSelectCapsHeight(t *testing.T) {
	var opts []string
	for i := 0; i < 80; i++ {
		opts = append(opts, fmt.Sprintf("model-%02d", i))
	}
	sel := opts[0]
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(10).Children(func() {
			scrollSelect(c.Key("sel"), &sel, opts, 210, "模型")
		})
	}, 400, 520)
	if err := tt.Click("model-00"); err != nil { // 点触发按钮（当前选中项）打开列表
		t.Fatal(err)
	}
	if !tt.HasText("model-01") {
		t.Fatalf("列表应已展开: %q", tt.Texts())
	}
	first, _ := tt.Find("model-01")
	if last, ok := tt.Find("model-79"); ok && last.W > 0 && last.H > 0 {
		t.Errorf("最后一项应被裁在列表之外，实际可见: %+v", last)
	}
	tt.Move(first.X+first.W/2, first.Y+first.H/2)
	for i := 0; i < 60; i++ { // 正值向下滚动
		tt.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 400)
	}
	last, ok := tt.Find("model-79")
	if !ok || last.W <= 0 || last.Y > first.Y+maxSelectHeight {
		t.Fatalf("滚动后应能看到最后一项: %+v", last)
	}
	if err := tt.Click("model-79"); err != nil {
		t.Fatal(err)
	}
	if sel != "model-79" {
		t.Errorf("选择后应更新选中项: %q", sel)
	}
}

// TestBarChartHoverShowsTip 鼠标移到柱子上，应立刻出现带日期和数量的提示。
func TestBarChartHoverShowsTip(t *testing.T) {
	data := []barDatum{
		{Label: "10/13", Value: 12, Tip: "10月13日：12 次 · 10 条"},
		{Label: "10/14", Value: 30, Tip: "10月14日：30 次 · 25 条"},
		{Label: "10/15", Value: 368, Tip: "10月15日：368 次 · 313 条"},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			barChart(c, "chart", data, ui.RGBA(90, 80, 220, 1), 160, func(v float64) string { return trimFloat(v) }, "测试图")
		})
	}, 500, 260)
	if tt.HasText("10月15日：368 次 · 313 条") {
		t.Fatal("没悬停时不应显示提示")
	}
	shown := map[string]bool{}
	for x := float32(30); x < 480; x += 5 {
		for y := float32(60); y < 200; y += 5 {
			tt.Move(x, y)
			tt.Frame()
			for _, d := range data {
				if tt.HasText(d.Tip) {
					shown[d.Tip] = true
				}
			}
		}
	}
	for _, d := range data {
		if !shown[d.Tip] {
			t.Errorf("悬停时没有出现提示 %q", d.Tip)
		}
	}
}

// TestOfficialSiteLinks 两个账单页都有「在官网查看」，点击用默认浏览器打开对应官网页面。
func TestOfficialSiteLinks(t *testing.T) {
	for page, want := range map[string]string{PageCursor: CursorUsageURL, PageDeepSeek: DeepSeekUsageURL} {
		e := newPanelEnv(t)
		tt := e.view(page)
		if err := tt.Click("在官网查看"); err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		if urls := tt.OpenedURLs(); len(urls) != 1 || urls[0] != want {
			t.Errorf("%s 打开的地址 %v，期望 %s", page, urls, want)
		}
	}
}
