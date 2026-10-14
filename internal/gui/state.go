package gui

import "github.com/egoist/mygo/ui"

// uiState 保存界面本身的状态（当前页、筛选、表格状态等），只在主线程读写。
//
// @author ygw
type uiState struct {
	page string

	sidebarSel string // 侧边栏当前选中项（与 page 同步）
	_          ui.Element
}

// newUIState 创建默认界面状态。
func newUIState() uiState { return uiState{page: PageOverview, sidebarSel: PageOverview} }
