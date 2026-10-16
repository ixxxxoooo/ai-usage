package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/ixxxxoooo/ai-usage/internal/placement"
	"github.com/ixxxxoooo/ai-usage/internal/service"
)

// SelfTestReport 是运行期自检的输出（JSON），用于 E2E 脚本断言真实窗口位置。
//
// @author ixxxxoooo
type SelfTestReport struct {
	Mode      string           `json:"mode"`
	OK        bool             `json:"ok"`
	Problems  []string         `json:"problems,omitempty"`
	Displays  []placement.Rect `json:"displays"` // 各显示器工作区
	Tray      placement.Rect   `json:"tray"`
	Popover   placement.Rect   `json:"popover"` // 浮窗窗口矩形（含箭头与阴影边距）
	Content   placement.Rect   `json:"content"` // 去掉边距后的内容区
	Expected  placement.Rect   `json:"expected"`
	Panel     placement.Rect   `json:"panel"`
	PanelOpen bool             `json:"panelOpen"`
	Page      string           `json:"page,omitempty"`
	// deepseek 模式专用（不含任何凭据原文）
	DSStatus   string               `json:"dsStatus,omitempty"`
	DSMessage  string               `json:"dsMessage,omitempty"`
	DSTokenSrc string               `json:"dsTokenSrc,omitempty"`
	DSHasData  bool                 `json:"dsHasData,omitempty"`
	DSDiag     *service.DSDiagnosis `json:"dsDiag,omitempty"`
	CurStatus  string               `json:"curStatus,omitempty"`
	CurMessage string               `json:"curMessage,omitempty"`
	CurSource  string               `json:"curSource,omitempty"`
}

// toRect 把 MyGo 的矩形转成 placement.Rect。
func toRect(r mygo.Rectangle) placement.Rect {
	return placement.Rect{X: r.X, Y: r.Y, W: r.Width, H: r.Height}
}

// workAreas 返回全部显示器的工作区。
func workAreas() []placement.Rect {
	var out []placement.Rect
	for _, d := range mygo.Screen.Displays() {
		out = append(out, toRect(d.WorkArea))
	}
	return out
}

// onMain 在主线程执行 fn 并等待完成。
func onMain(fn func()) {
	done := make(chan struct{})
	mygo.RunOnMain(func() { fn(); close(done) })
	<-done
}

// parseBounds 解析 "x,y,w,h"。
func parseBounds(s string) (placement.Rect, error) {
	p := strings.Split(s, ",")
	if len(p) != 4 {
		return placement.Rect{}, fmt.Errorf("需要 x,y,w,h: %q", s)
	}
	var v [4]int
	for i := range p {
		n, err := strconv.Atoi(strings.TrimSpace(p[i]))
		if err != nil {
			return placement.Rect{}, err
		}
		v[i] = n
	}
	return placement.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}, nil
}

// RunSelfTest 在窗口环境里做一次真实自检并把 JSON 报告写到 out，然后退出应用。
//
// 模式：
//
//	position   打开托盘浮窗，比对它与托盘图标/显示器的真实位置关系
//	panel-set  打开大面板并设置为 bounds（"x,y,w,h"），用于验证窗口位置/尺寸记忆
//	panel-get  打开大面板并上报它此刻的位置/尺寸
//	deepseek   等首轮刷新结束，上报 DeepSeek 状态与各候选凭据（浏览器/手动/API Key）的检查结果
//	show:页面  打开大面板到指定页面并保持 30 秒（供截图），页面为 overview/cursor/deepseek/settings
//
// 参数 mode：自检模式；out：报告输出路径；bounds：panel-set 用的目标矩形。
// 必须在 App.Start 之后调用（它自己起 goroutine，不会阻塞）。
func (a *App) RunSelfTest(mode, out, bounds string) {
	go func() {
		rep := SelfTestReport{Mode: mode, OK: true}
		fail := func(f string, args ...any) {
			rep.OK = false
			rep.Problems = append(rep.Problems, fmt.Sprintf(f, args...))
		}
		time.Sleep(1500 * time.Millisecond) // 等托盘与窗口系统就绪
		switch mode {
		case "position":
			a.selfTestPosition(&rep, fail)
		case "panel-set":
			a.selfTestPanelSet(&rep, bounds, fail)
		case "panel-get":
			a.selfTestPanelGet(&rep, fail)
		case "deepseek":
			a.selfTestDeepSeek(&rep, fail)
		default:
			if page, ok := strings.CutPrefix(mode, "show:"); ok {
				a.selfTestShow(&rep, page, fail)
			} else {
				fail("未知自检模式 %q", mode)
			}
		}
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "写自检报告失败:", err)
		}
		mygo.App.Quit()
	}()
}

// popoverInset 是 NSPopover 窗口在内容区四周留出的边距（箭头、阴影），实测为 13 DIP。
const popoverInset = 13

// selfTestPosition 打开浮窗并校验：水平对齐托盘图标、紧贴菜单栏下方、完整落在所在显示器内。
func (a *App) selfTestPosition(rep *SelfTestReport, fail func(string, ...any)) {
	onMain(func() { a.TogglePopover() })
	time.Sleep(1200 * time.Millisecond)
	onMain(func() {
		rep.Displays = workAreas()
		rep.Tray = toRect(a.tray.Bounds())
		if a.pop == nil {
			fail("浮窗没有打开")
			return
		}
		rep.Popover = toRect(a.pop.Bounds())
		p := rep.Popover
		rep.Content = placement.Rect{X: p.X + popoverInset, Y: p.Y + popoverInset, W: p.W - 2*popoverInset, H: p.H - 2*popoverInset}
		if exp, ok := placement.ForTray(rep.Tray, PopoverWidth, PopoverHeight, 4, rep.Displays); ok {
			rep.Expected = exp
		}
	})
	if rep.Tray.Empty() {
		fail("取不到托盘图标位置")
		return
	}
	work, _ := placement.DisplayFor(rep.Tray, rep.Displays)
	const tol = 24 // NSPopover 含箭头与阴影，允许一定误差
	// 水平：浮窗中心应与托盘中心对齐，除非被屏幕边缘挤压。
	exp := rep.Expected
	if abs(rep.Content.CenterX()-exp.CenterX()) > tol {
		fail("浮窗水平位置偏离期望：中心 %d，期望 %d", rep.Content.CenterX(), exp.CenterX())
	}
	if rep.Content.W != PopoverWidth || rep.Content.H != PopoverHeight {
		fail("浮窗内容尺寸 %dx%d 与设计的 %dx%d 不符", rep.Content.W, rep.Content.H, PopoverWidth, PopoverHeight)
	}
	// 垂直：内容区顶部应紧贴托盘图标下沿（中间是箭头）。
	if gap := rep.Content.Y - rep.Tray.Bottom(); gap < 0 || gap > 24 {
		fail("浮窗与托盘垂直间距异常：%d", gap)
	}
	if !rep.Content.Within(work, 2) {
		fail("浮窗内容 %+v 超出所在显示器工作区 %+v", rep.Content, work)
	}
	onMain(func() { a.ClosePopover() })
	time.Sleep(500 * time.Millisecond)
	onMain(func() {
		if a.pop != nil {
			fail("关闭后浮窗仍然存在")
		}
	})
}

// selfTestPanelSet 打开面板、设置位置尺寸并关闭，让 MyGo 保存窗口状态。
func (a *App) selfTestPanelSet(rep *SelfTestReport, bounds string, fail func(string, ...any)) {
	target, err := parseBounds(bounds)
	if err != nil {
		fail("%v", err)
		return
	}
	onMain(func() { a.OpenPanel(PageOverview) })
	time.Sleep(900 * time.Millisecond)
	onMain(func() {
		a.panel.SetBounds(mygo.Rectangle{X: target.X, Y: target.Y, Width: target.W, Height: target.H})
	})
	time.Sleep(900 * time.Millisecond)
	onMain(func() {
		rep.Displays = workAreas()
		rep.Panel = toRect(a.panel.Bounds())
		rep.PanelOpen = true
	})
	if abs(rep.Panel.X-target.X) > 2 || abs(rep.Panel.Y-target.Y) > 2 || abs(rep.Panel.W-target.W) > 2 || abs(rep.Panel.H-target.H) > 2 {
		fail("设置后窗口矩形 %+v 与目标 %+v 不一致", rep.Panel, target)
	}
	onMain(func() { a.panel.Close() })
	time.Sleep(600 * time.Millisecond)
}

// selfTestShow 打开面板到指定页面并保持 30 秒，供外部截图核对界面。
func (a *App) selfTestShow(rep *SelfTestReport, page string, fail func(string, ...any)) {
	switch page {
	case PageOverview, PageCursor, PageDeepSeek, PageSettings:
	default:
		fail("未知页面 %q", page)
		return
	}
	onMain(func() { a.OpenPanel(page) })
	time.Sleep(1200 * time.Millisecond)
	onMain(func() { a.panel.SetBounds(mygo.Rectangle{X: 40, Y: 60, Width: PanelWidth, Height: PanelHeight}) })
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		rep.Panel = toRect(a.panel.Bounds())
		rep.PanelOpen = a.panel != nil
		rep.Page = a.ui.page
	})
	time.Sleep(30 * time.Second)
}

// selfTestPanelGet 打开面板并上报其位置尺寸，同时校验它落在某个显示器内。
func (a *App) selfTestPanelGet(rep *SelfTestReport, fail func(string, ...any)) {
	onMain(func() { a.OpenPanel(PageOverview) })
	time.Sleep(900 * time.Millisecond)
	onMain(func() {
		rep.Displays = workAreas()
		rep.Panel = toRect(a.panel.Bounds())
		rep.PanelOpen = a.panel != nil
		rep.Page = a.ui.page
	})
	visible := false
	for _, w := range rep.Displays {
		if rep.Panel.Within(w, 40) || placement.Restore(rep.Panel, []placement.Rect{w}, 80) == rep.Panel {
			visible = true
		}
	}
	if !visible {
		fail("面板 %+v 不在任何显示器内", rep.Panel)
	}
}

// abs 返回绝对值。
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// selfTestDeepSeek 等首轮刷新结束后，上报 DeepSeek 状态，并逐个检查所有候选凭据。
//
// 在真实应用进程里运行，因此能反映"从 Finder/open 启动时"的真实权限与登录获取结果。
// 报告里只有脱敏信息，不含凭据原文。
func (a *App) selfTestDeepSeek(rep *SelfTestReport, fail func(string, ...any)) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, cs := a.svc.State().DeepSeek, a.svc.State().Cursor
		if !st.LastTry.IsZero() && st.Status != service.StatusLoading && !cs.LastTry.IsZero() && cs.Status != service.StatusLoading {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	st := a.svc.State().DeepSeek
	rep.DSStatus, rep.DSMessage, rep.DSTokenSrc, rep.DSHasData = st.Status.StatusText(), st.Message, st.TokenSrc, st.HasBalance
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	d := a.svc.DiagnoseDeepSeek(ctx)
	rep.DSDiag = &d
	cs := a.svc.State().Cursor
	rep.CurStatus, rep.CurMessage, rep.CurSource = cs.Status.StatusText(), cs.Message, cs.CredSource
	if st.Status != service.StatusOK {
		fail("DeepSeek 状态不是正常：%s（%s）", rep.DSStatus, st.Message)
	}
}
