package gui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
)

// 自动更新：基于 mygo 的签名更新（mygo.json 的 updates）。
//
// CI 用私钥给每个版本的 .app 签名，连同清单 update-darwin-universal.json 一起发布到 GitHub Release；
// 应用定时读取最新 Release 的清单，有新版本时弹出更新窗口，下载、校验签名后原地替换并重启。
//
// @author ixxxxoooo

// updateInterval 是后台检查更新的间隔。
const updateInterval = 6 * time.Hour

// EnableUpdates 启用自动更新（后台定时检查 + 更新窗口）。必须在 mygo.App.Run 之前调用。
//
// 没有调用时（测试、自检），设置页只显示版本号，菜单里也没有「检查更新」。
func (a *App) EnableUpdates() {
	mygo.Use(native.New(updater.Options{Interval: updateInterval, Language: "zh-Hans"}))
	a.updates, a.updateCan = true, mygo.Updater.Enabled()
	updater.OnChange(a.invalidate)
}

// CheckForUpdates 立即检查更新：弹出更新窗口，显示新版本、已是最新或失败原因。
func (a *App) CheckForUpdates() {
	if a.updates {
		updater.CheckForUpdates()
	}
}

// updateMenuItems 返回托盘菜单里的「检查更新…」；未启用自动更新时为空。
func (a *App) updateMenuItems() []*mygo.MenuItem {
	if !a.updates {
		return nil
	}
	return []*mygo.MenuItem{{Label: "检查更新…", Click: func(*mygo.MenuItem, *mygo.Window) { a.CheckForUpdates() }}}
}

// updateDesc 返回设置页「软件更新」一行的说明。
func updateDesc(can bool, last time.Time) string {
	if !can {
		return "当前安装位置无法自动更新（开发版，或应用不在可写目录，如直接从安装盘运行）"
	}
	if last.IsZero() {
		return "有新版本时会弹窗提示，可一键下载安装并重启"
	}
	return "上次检查：" + last.Local().Format("2006-01-02 15:04")
}
