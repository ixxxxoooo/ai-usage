package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRenderScreenshots 把各页面渲染成 PNG（浅色/深色），用于人工核对外观。
// 只在设置了 AIUSAGE_SHOTS=输出目录 时运行，不截取真实屏幕。
func TestRenderScreenshots(t *testing.T) {
	dir := os.Getenv("AIUSAGE_SHOTS")
	if dir == "" {
		t.Skip("未设置 AIUSAGE_SHOTS")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(name string, tt *ui.Tester) {
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	e := newPanelEnv(t)
	for _, dark := range []bool{false, true} {
		suffix := "light"
		if dark {
			suffix = "dark"
		}
		for _, p := range []string{PageOverview, PageCursor, PageDeepSeek} {
			tt := ui.NewTester(func(c *ui.Context) { e.a.ui.page = p; e.a.panelView(c) }, PanelWidth, PanelHeight)
			tt.SetDark(dark)
			tt.SetScale(1)
			tt.Frame()
			save(p+"-"+suffix, tt)
		}
		e.a.loadSettingsDraft()
		tt := ui.NewTester(func(c *ui.Context) { e.a.ui.page = PageSettings; e.a.panelView(c) }, PanelWidth, 1500)
		tt.SetDark(dark)
		tt.Frame()
		save("settings-"+suffix, tt)
		pop := ui.NewTester(e.a.popoverView, PopoverWidth, PopoverHeight)
		pop.SetDark(dark)
		pop.Frame()
		save("popover-"+suffix, pop)
	}
}
