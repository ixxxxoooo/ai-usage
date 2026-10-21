package gui

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/assets"
	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/placement"
	"github.com/ixxxxoooo/ai-usage/internal/service"
)

// 浮窗与面板的尺寸（DIP）。
const (
	PopoverWidth = 316
	// PopoverHeight 是只有 Cursor 与 DeepSeek 两张卡片时的浮窗高度；
	// 开启 Antigravity 额度查询时再加上 PopoverAntigravityExtra（见 popoverHeight）。
	PopoverHeight           = 332
	PopoverAntigravityExtra = 130
	PanelWidth              = 1020
	PanelHeight             = 680
)

// 面板页面标识。
const (
	PageOverview    = "overview"
	PageCursor      = "cursor"
	PageDeepSeek    = "deepseek"
	PageAntigravity = "antigravity"
	PageSettings    = "settings"
)

// App 是界面层的总控：托盘、浮窗、大面板，以及它们共享的状态。
//
// 视图只在主线程读取 App 的界面状态；跨线程的数据（Data）整体替换并加锁读取。
//
// @author ixxxxoooo
type App struct {
	svc     *service.Service
	now     func() time.Time
	stateFn func() service.State // 取当前状态；默认读 svc.State，测试可注入

	mu       sync.Mutex
	data     Data
	dataErr  error
	reloadCh chan struct{}

	// 以下字段只在主线程读写（托盘/菜单/视图回调都在主线程）。
	tray  *mygo.Tray
	pop   *mygo.Window
	panel *mygo.Window
	ui    uiState
	hk    hooks // 可替换的外部交互（文件对话框、异步执行），测试时注入

	updates   bool // 已启用自动更新（EnableUpdates）
	updateCan bool // 当前安装位置可以自动更新

	// 供 onChange 在其他 goroutine 里安全读取窗口指针。
	winMu sync.Mutex
	wins  []*mygo.Window
}

// New 创建 App。服务稍后通过 Attach 绑定（服务的 OnChange 需要引用 App，所以分两步）。
//
// 返回值：*App。
func New() *App {
	return &App{now: time.Now, reloadCh: make(chan struct{}, 1), ui: newUIState()}
}

// popoverHeight 返回浮窗内容高度：关闭 Antigravity 查询时不留空位。
func (a *App) popoverHeight() int {
	if a.state().Settings.AntigravityOff {
		return PopoverHeight
	}
	return PopoverHeight + PopoverAntigravityExtra
}

// state 返回当前整体状态。
func (a *App) state() service.State {
	if a.stateFn != nil {
		return a.stateFn()
	}
	return a.svc.State()
}

// SetClock 注入时钟（测试用）。
func (a *App) SetClock(now func() time.Time) { a.now = now }

// Attach 绑定服务并加载一次明细数据。
//
// 参数 svc：已创建的服务。
func (a *App) Attach(svc *service.Service) {
	a.svc = svc
	a.reloadNow()
	go a.reloadLoop()
}

// Service 返回绑定的服务。
func (a *App) Service() *service.Service { return a.svc }

// Data 返回当前明细数据快照（只读）。
func (a *App) Data() Data {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.data
}

// reloadNow 同步从存储重新加载明细。
func (a *App) reloadNow() {
	if a.svc == nil {
		return
	}
	d, err := LoadData(a.svc.Store(), a.now(), a.state().Cursor.Usage.Account)
	a.mu.Lock()
	a.data, a.dataErr = d, err
	a.mu.Unlock()
	if err != nil {
		log.Println("加载明细失败:", err)
	}
}

// reloadLoop 合并多次变更通知，串行重载明细后刷新窗口。
func (a *App) reloadLoop() {
	for range a.reloadCh {
		time.Sleep(150 * time.Millisecond) // 合并抖动
		select {
		case <-a.reloadCh:
		default:
		}
		a.reloadNow()
		a.invalidate()
	}
}

// OnChange 是 service.Config.OnChange 的实现：可能在任意 goroutine 调用。
// 它更新托盘文字，并异步重载明细、刷新已打开的窗口。
func (a *App) OnChange() {
	a.updateTray()
	select {
	case a.reloadCh <- struct{}{}:
	default:
	}
	a.invalidate() // 先用旧明细立刻刷新状态类文案
}

// invalidate 让所有已打开的窗口重绘。
func (a *App) invalidate() {
	a.winMu.Lock()
	ws := append([]*mygo.Window(nil), a.wins...)
	a.winMu.Unlock()
	for _, w := range ws {
		w.Update(func() {})
	}
}

// track 登记窗口（用于跨线程刷新），窗口关闭时自动移除。
func (a *App) track(w *mygo.Window) {
	a.winMu.Lock()
	a.wins = append(a.wins, w)
	a.winMu.Unlock()
	w.OnClosed(func() {
		a.winMu.Lock()
		defer a.winMu.Unlock()
		for i, x := range a.wins {
			if x == w {
				a.wins = append(a.wins[:i], a.wins[i+1:]...)
				break
			}
		}
	})
}

// Notify 发送系统通知；点击通知会打开大面板。供 service.Config.Notify 使用。
//
// 参数 id：通知标识（相同 id 会替换）；title/body：标题与正文。
func (a *App) Notify(id, title, body string) {
	if !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: title, Body: body})
	n.OnClick(func() { a.OpenPanel(PageOverview) })
	if err := n.Show(); err != nil {
		log.Println("通知失败:", err)
	}
}

// updateTray 按设置刷新托盘文字与悬停提示（可在任意 goroutine 调用）。
func (a *App) updateTray() {
	if a.tray == nil || (a.svc == nil && a.stateFn == nil) {
		return
	}
	st := a.state()
	a.tray.SetTitle(TrayTitle(st))
	a.tray.SetToolTip(TrayToolTip(st, a.now()))
}

// Start 创建托盘与菜单，并设置为菜单栏常驻应用。必须在 mygo.App 就绪后（WhenReady 内）调用。
//
// 返回值：创建托盘失败时的错误。
func (a *App) Start() error {
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {}) // 浮窗/面板都关闭后继续常驻
	mygo.App.SetMenu(appMenu())
	var err error
	a.tray, err = mygo.NewTray(mygo.TrayOptions{Icon: assets.Tray(), IconIsTemplate: true, ToolTip: "AI Usage"})
	if err != nil {
		return err
	}
	a.tray.OnClick(a.TogglePopover)
	a.tray.OnRightClick(a.showTrayMenu)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		a.tray.Destroy()
		if a.svc != nil {
			a.svc.Stop()
		}
	})
	mygo.App.OnActivate(func(hasVisible bool) {
		if !hasVisible {
			a.OpenPanel(PageOverview)
		}
	})
	mygo.Power.OnResume(func() {
		if a.svc != nil {
			a.svc.Kick()
		}
	})
	a.updateTray()
	return nil
}

// appMenu 构建应用菜单。
//
// macOS 的 ⌘W 由菜单项提供：File 菜单里的「关闭窗口」绑定 performClose:，
// 传给当前主窗口。菜单栏应用默认没有菜单，漏掉 File 菜单就等于 ⌘W 没人处理。
//
// 返回值：应用菜单。
func appMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Role: mygo.RoleFileMenu}, // 关闭窗口（⌘W）
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

// showTrayMenu 在右键时弹出菜单；菜单只在这次弹出期间设置，这样左键仍然用来开关浮窗。
func (a *App) showTrayMenu() {
	if a.pop != nil {
		a.pop.Close()
	}
	items := []*mygo.MenuItem{
		{Label: "打开面板", Click: func(*mygo.MenuItem, *mygo.Window) { a.OpenPanel(PageOverview) }},
		{Label: "立即刷新", Click: func(*mygo.MenuItem, *mygo.Window) { a.RefreshNow() }},
		{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) { a.OpenPanel(PageSettings) }},
	}
	items = append(items, a.updateMenuItems()...)
	items = append(items, mygo.Separator(), &mygo.MenuItem{Role: mygo.RoleQuit, Label: "退出 AI Usage"})
	menu := mygo.NewMenu(items)
	a.tray.SetMenu(menu)
	defer a.tray.SetMenu(nil)
	a.tray.PopUpMenu()
}

// RefreshNow 在后台触发一次手动刷新（服务内部带节流）。
func (a *App) RefreshNow() {
	if a.svc == nil {
		return
	}
	go a.svc.RefreshManual(context.Background())
}

// TogglePopover 打开或关闭托盘浮窗。
func (a *App) TogglePopover() {
	if a.pop != nil {
		a.pop.Close()
		return
	}
	a.pop = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      a.tray,
		Placement: mygo.PlacementBottom,
		Gap:       4,
		Width:     PopoverWidth,
		Height:    a.popoverHeight(),
		Focusable: true,
		Shadow:    true,
		Popover:   true,
		Content:   ui.View(a.popoverView),
	})
	a.track(a.pop)
	pop := a.pop
	a.pop.OnClosed(func() {
		if a.pop == pop {
			a.pop = nil
		}
	})
	a.RefreshNow() // 打开即刷新一次，让数据尽量新鲜
}

// ClosePopover 关闭浮窗（若已打开）。
func (a *App) ClosePopover() {
	if a.pop != nil {
		a.pop.Close()
	}
}

// OpenPanel 打开（或激活）大面板并切到指定页面。
//
// 参数 page：PageOverview / PageCursor / PageDeepSeek / PageAntigravity / PageSettings。
func (a *App) OpenPanel(page string) {
	a.ClosePopover()
	if page != "" {
		a.ui.page = page
	}
	if a.ui.page == PageSettings {
		a.loadSettingsDraft()
	}
	if a.panel != nil {
		a.panel.Show()
		a.panel.Focus()
		mygo.App.Focus()
		a.panel.Update(func() {})
		return
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	a.panel = mygo.NewWindow(mygo.WindowOptions{
		Title:         "AI Usage",
		Width:         PanelWidth,
		Height:        PanelHeight,
		MinWidth:      820,
		MinHeight:     520,
		StateKey:      "panel",
		TitleBarStyle: mygo.TitleBarHidden,
		Vibrancy:      mygo.VibrancySidebar,
		Content:       ui.View(a.panelView),
	})
	a.fitPanel(a.panel)
	a.track(a.panel)
	panel := a.panel
	a.panel.OnClosed(func() {
		if a.panel == panel {
			a.panel = nil
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		}
	})
	mygo.App.Focus()
}

// Settings 返回当前设置（便捷方法）。
func (a *App) Settings() model.Settings { return a.svc.Settings() }

// fitPanel 修正恢复出来的面板窗口：过小放大、过大缩小、落在已拔掉的显示器上则拉回可见区域。
//
// 参数 w：刚创建（已恢复上次位置）的面板窗口。
func (a *App) fitPanel(w *mygo.Window) {
	cur := toRect(w.Bounds())
	want := placement.EnsureUsable(cur, workAreas(), 820, 520)
	if want != cur {
		log.Printf("面板窗口位置已修正：%+v -> %+v", cur, want)
		w.SetBounds(mygo.Rectangle{X: want.X, Y: want.Y, Width: want.W, Height: want.H})
	}
}
