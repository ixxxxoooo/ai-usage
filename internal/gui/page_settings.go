package gui

import (
	"fmt"
	"os/exec"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/service"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// 设置页里各选项的显示文案与对应取值。
var (
	intervalLabels = []string{"1 分钟", "5 分钟", "10 分钟", "15 分钟", "30 分钟", "60 分钟"}
	intervalMins   = []int{1, 5, 10, 15, 30, 60}
	trayLabels     = []string{"仅图标", "Cursor", "DeepSeek", "两者"}
	trayModes      = []string{model.TrayIcon, model.TrayCursor, model.TrayDeepSeek, model.TrayBoth}
	notifyChoices  = []int{50, 70, 80, 90, 100}
	authLabelsC    = []string{"自动读取本机 Cursor 登录", "手动粘贴 Cookie"}
	authLabelsD    = []string{"自动读取 Chrome 登录", "手动粘贴 token"}
	importModes    = []string{"合并（去重，保留本机数据）", "覆盖（以备份为准）"}
)

// loadSettingsDraft 把服务里的当前设置载入设置页草稿，并刷新凭据状态。
// 进入设置页、导入、清除数据之后调用。
func (a *App) loadSettingsDraft() {
	if a.svc == nil {
		return
	}
	s := a.svc.Settings()
	u := &a.ui
	u.set, u.lastApplied, u.setLoaded = s, s, true
	u.cursorAuth = indexOf([]string{model.AuthAuto, model.AuthManual}, s.CursorAuthMode, 0)
	u.dsAuth = indexOf([]string{model.AuthAuto, model.AuthManual}, s.DeepSeekAuthMode, 0)
	u.trayMode = indexOf(trayModes, s.TrayDisplay, 1)
	u.interval = fmt.Sprintf("%d 分钟", s.RefreshMinutes)
	u.notifyPct = map[int]bool{}
	for _, p := range s.NotifyCursorPercent {
		u.notifyPct[p] = true
	}
	u.dsBelow = s.NotifyDeepSeekBelow
	u.agOn = !s.AntigravityOff
	u.loginItem = a.h().getLogin()
	u.cookieInput, u.keyInput, u.tokenInput = "", "", ""
	a.loadSecretFlags()
}

// indexOf 返回 v 在 list 中的位置，找不到返回 def。
func indexOf[T comparable](list []T, v T, def int) int {
	if i := slices.Index(list, v); i >= 0 {
		return i
	}
	return def
}

// draftSettings 由草稿控件的值组装出设置。
func (a *App) draftSettings() model.Settings {
	u := &a.ui
	s := u.set
	s.CursorAuthMode = []string{model.AuthAuto, model.AuthManual}[u.cursorAuth]
	s.DeepSeekAuthMode = []string{model.AuthAuto, model.AuthManual}[u.dsAuth]
	s.TrayDisplay = trayModes[u.trayMode]
	if i := indexOf(intervalLabels, u.interval, -1); i >= 0 {
		s.RefreshMinutes = intervalMins[i] // 不在预设里的自定义值（如导入的 2 分钟）保持原样，不被悄悄改掉
	}
	s.NotifyCursorPercent = nil
	for _, p := range notifyChoices {
		if u.notifyPct[p] {
			s.NotifyCursorPercent = append(s.NotifyCursorPercent, p)
		}
	}
	s.NotifyDeepSeekBelow = max(u.dsBelow, 0)
	s.AntigravityOff = !u.agOn
	return s
}

// applyDraft 草稿有变化时提交给服务（只在草稿与上次提交不同时才写库）。
func (a *App) applyDraft() {
	d := a.draftSettings()
	if fmt.Sprint(d) == fmt.Sprint(a.ui.lastApplied) {
		return
	}
	a.ui.lastApplied = d
	if err := a.svc.UpdateSettings(d); err != nil {
		a.say("保存设置失败：" + err.Error())
		return
	}
	a.updateTray()
	a.svc.Kick()
}

// setLoginItem 开关开机自启。
func (a *App) setLoginItem(on bool) {
	if err := a.h().setLogin(on); err != nil {
		a.ui.loginItem = a.h().getLogin()
		a.say("设置开机自启失败：" + err.Error())
	}
}

// settingsSection 画设置分组：标题 + 卡片。
func settingsSection(c *ui.Context, title string, fn func()) {
	ui.Text(c, title).Bold().FontSize(13).Margin(6, 2, 0, 2)
	card(c).Padding(4, 16, 4, 16).Gap(0).Children(fn)
}

// settingRow 画一行设置：左侧标题与说明，右侧控件；行间有分隔线。
func settingRow(c *ui.Context, title, desc string, control func()) {
	t := c.Theme()
	ui.Row(c).Gap(14).AlignItems(ui.Center).Padding(10, 0, 10, 0).Children(func() {
		ui.Column(c).Gap(2).Grow(1).Basis(0).Children(func() {
			ui.Text(c, title).FontSize(13)
			if desc != "" {
				ui.Text(c, desc).FontSize(11).TextColor(t.TextMuted)
			}
		})
		control()
	})
}

// settingBlock 画纵向的一块设置：标题说明在上，控件在下（输入框等宽控件）。
func settingBlock(c *ui.Context, title, desc string, control func()) {
	t := c.Theme()
	ui.Column(c).Gap(8).Padding(10, 0, 12, 0).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(13)
			if desc != "" {
				ui.Text(c, desc).FontSize(11).TextColor(t.TextMuted)
			}
		})
		control()
	})
}

// secretStatus 画「已保存 / 未保存」状态文案。
func secretStatus(c *ui.Context, has bool, mask string) {
	t := c.Theme()
	if has {
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconCheck).FontSize(12).TextColor(t.Success)
			ui.Text(c, "已保存到钥匙串 · "+mask).FontSize(11).TextColor(t.TextMuted)
		})
		return
	}
	ui.Text(c, "尚未保存").FontSize(11).TextColor(t.TextMuted)
}

// secretInput 画凭据输入框 + 保存/清除按钮。
func (a *App) secretInput(c *ui.Context, id, label, placeholder string, value *string, key string, has bool) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key(id), value).Password().Placeholder(placeholder).Label(label).Grow(1)
		if ui.Button(c.Key(id+"-save"), "保存").Disabled(util.IsEmpty(*value)).Clicked() {
			a.saveCredential(key, *value)
			*value = ""
		}
		if ui.Button(c.Key(id+"-clear"), "清除凭据").Disabled(!has).Clicked() {
			a.saveCredential(key, "")
		}
	})
}

// testLine 画「测试连接」按钮与结果文案。
func testLine(c *ui.Context, id string, busy bool, result string, onTest func()) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.Button(c.Key(id), "测试连接").Disabled(busy).Clicked() {
			onTest()
		}
		col := t.TextMuted
		if len(result) >= 6 && result[:6] == "失败" {
			col = t.Danger
		}
		ui.Text(c, result).FontSize(12).TextColor(col).Grow(1)
	})
}

// settingsPage 设置页。
func (a *App) settingsPage(c *ui.Context, st service.State) {
	if !a.ui.setLoaded {
		a.loadSettingsDraft()
	}
	u := &a.ui
	pageTitle(c, "设置", "凭据只保存在 macOS 钥匙串中；修改会立即生效", nil)
	pageBody(c, "set-scroll", func() {
		a.cursorSettings(c)
		a.deepseekSettings(c)
		a.antigravitySettings(c)
		a.generalSettings(c)
		a.notifySettings(c)
		a.dataSettings(c, st)
		ui.Text(c, fmt.Sprintf("AI Usage %s · © 2026 ixxxxoooo", AppVersion)).FontSize(11).TextColor(c.Theme().TextMuted).Margin(8, 0, 0, 2)
	})
	a.settingsDialogs(c)
	_ = u
	a.applyDraft()
}

// cursorSettings Cursor 分组。
func (a *App) cursorSettings(c *ui.Context) {
	u := &a.ui
	settingsSection(c, "Cursor", func() {
		settingBlock(c, "登录方式", "自动方式会只读地读取本机 Cursor 的登录状态，不需要任何额外操作", func() {
			ui.Segmented(c.Key("set-cauth"), &u.cursorAuth, authLabelsC...).Label("Cursor 登录方式")
		})
		if u.cursorAuth == 1 {
			ui.Divider(c)
			settingBlock(c, "Cookie", "在浏览器登录 cursor.com 后，复制请求头中的 Cookie（WorkosCursorSessionToken=…）粘贴到这里", func() {
				ui.Column(c).Gap(6).Children(func() {
					a.secretInput(c, "set-cookie", "Cursor Cookie", "粘贴 Cookie", &u.cookieInput, secret.KeyCursorCookie, u.hasCookie)
					secretStatus(c, u.hasCookie, u.maskCookie)
				})
			})
		}
		ui.Divider(c)
		settingBlock(c, "连接状态", "", func() {
			testLine(c, "set-test-cursor", u.testingC, u.testCursor, func() { a.testConnection("cursor") })
		})
	})
}

// deepseekSettings DeepSeek 分组。
func (a *App) deepseekSettings(c *ui.Context) {
	u := &a.ui
	settingsSection(c, "DeepSeek", func() {
		settingBlock(c, "API Key", "用于查询余额。在 platform.deepseek.com 创建的 Key，只需要查询权限", func() {
			ui.Column(c).Gap(6).Children(func() {
				a.secretInput(c, "set-apikey", "DeepSeek API Key", "sk-…", &u.keyInput, secret.KeyDeepSeekKey, u.hasKey)
				secretStatus(c, u.hasKey, u.maskKey)
			})
		})
		ui.Divider(c)
		settingBlock(c, "平台登录（账单与今日 Token）", "账单接口需要网页登录信息。自动方式会只读地从 Chrome 读取，不会修改浏览器数据", func() {
			ui.Segmented(c.Key("set-dsauth"), &u.dsAuth, authLabelsD...).Label("DeepSeek 平台登录方式")
		})
		if u.dsAuth == 0 {
			settingBlock(c, "自动读取需要授权", "macOS 默认不允许应用读取 Chrome 的数据。若这里一直显示「未登录」，请在「完全磁盘访问权限」里允许 AI Usage 并重启；也可以改用「手动粘贴 token」", func() {
				if ui.Button(c.Key("set-open-fda"), "打开隐私设置").Clicked() {
					openPrivacySettings()
				}
			})
		}
		if u.dsAuth == 1 {
			settingBlock(c, "平台 token", "不需要 Cookie。登录 platform.deepseek.com 后，按 F12 → Network，点开任一 api/v0 请求，复制请求头 authorization: Bearer 后面的那一串（或 Application → Local Storage 里 userToken 的 value）。直接粘贴整行 authorization 也可以", func() {
				ui.Column(c).Gap(6).Children(func() {
					a.secretInput(c, "set-dstoken", "DeepSeek 平台 token", "粘贴 token", &u.tokenInput, secret.KeyDeepSeekToken, u.hasToken)
					secretStatus(c, u.hasToken, u.maskToken)
				})
			})
		}
		ui.Divider(c)
		settingBlock(c, "连接状态", "", func() {
			testLine(c, "set-test-ds", u.testingD, u.testDS, func() { a.testConnection("deepseek") })
		})
	})
}

// antigravitySettings Antigravity 分组：开关与连接测试。
func (a *App) antigravitySettings(c *ui.Context) {
	u := &a.ui
	settingsSection(c, "Antigravity（反重力）", func() {
		settingRow(c, "查询额度", "读取本机正在运行的 Antigravity 的模型额度，不需要任何登录凭据，也不会访问外网。关闭后浮窗与面板里不再显示", func() {
			ui.Switch(c.Key("set-ag"), &u.agOn).Label("查询 Antigravity 额度")
		})
		if u.agOn {
			ui.Divider(c)
			settingBlock(c, "连接状态", "需要先打开 Antigravity 应用；没运行时会显示上次的数据", func() {
				testLine(c, "set-test-ag", u.testingA, u.testAG, func() { a.testConnection("antigravity") })
			})
		}
	})
}

// generalSettings 通用分组：刷新间隔、托盘显示、开机自启。
func (a *App) generalSettings(c *ui.Context) {
	u := &a.ui
	settingsSection(c, "通用", func() {
		settingRow(c, "自动刷新间隔", "合上盖子唤醒后会立即刷新一次", func() {
			ui.Select(c.Key("set-interval"), &u.interval, intervalLabels).Label("自动刷新间隔").Width(120)
		})
		ui.Divider(c)
		settingRow(c, "菜单栏显示", "托盘图标旁的文字", func() {
			ui.Segmented(c.Key("set-tray"), &u.trayMode, trayLabels...).Label("菜单栏显示内容")
		})
		ui.Divider(c)
		settingRow(c, "开机自启", "登录 macOS 后自动在菜单栏运行", func() {
			if ui.Switch(c.Key("set-login"), &u.loginItem).Label("开机自启").Changed() {
				a.setLoginItem(u.loginItem)
			}
		})
	})
}

// notifySettings 通知分组。
func (a *App) notifySettings(c *ui.Context) {
	u := &a.ui
	settingsSection(c, "通知", func() {
		settingRow(c, "用量提醒", "达到阈值时发送系统通知，每个周期每个阈值只提醒一次", func() {
			ui.Switch(c.Key("set-notify"), &u.set.NotifyEnabled).Label("开启用量提醒")
		})
		if u.set.NotifyEnabled {
			ui.Divider(c)
			settingBlock(c, "Cursor 用量阈值", "已用请求数达到套餐额度的百分比时提醒", func() {
				ui.Row(c).Gap(16).Children(func() {
					for _, p := range notifyChoices {
						v := u.notifyPct[p]
						if ui.Checkbox(c.Key(fmt.Sprint("set-pct-", p)), &v, fmt.Sprintf("%d%%", p)).Changed() {
							u.notifyPct[p] = v
						}
					}
				})
			})
			ui.Divider(c)
			settingRow(c, "DeepSeek 余额不足", "余额低于该数值时提醒，0 表示关闭", func() {
				ui.NumberInput(c.Key("set-below"), &u.dsBelow, 0, 100000, 1).Label("DeepSeek 余额提醒线").Width(120)
			})
		}
	})
}

// dataSettings 数据分组：导出、导入、清除。
func (a *App) dataSettings(c *ui.Context, st service.State) {
	t := c.Theme()
	u := &a.ui
	settingsSection(c, "数据", func() {
		settingBlock(c, "导出备份", "把本机全部用量数据与设置保存为一个 JSON 文件，可在另一台 Mac 上导入", func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				if ui.Button(c.Key("set-export"), "导出备份…").Disabled(u.busy).Clicked() {
					a.exportBackup(u.exportCreds)
				}
				ui.Checkbox(c.Key("set-export-creds"), &u.exportCreds, "同时导出登录凭据（明文，不建议）")
			})
		})
		ui.Divider(c)
		settingBlock(c, "导入备份", "支持本应用导出的备份文件；导入前会先预览内容，不会改动数据", func() {
			a.importBlock(c)
		})
		ui.Divider(c)
		settingBlock(c, "清除本地数据", "删除本机保存的 Cursor 明细与 DeepSeek 账单（钥匙串里的凭据会保留）。清除前会自动备份", func() {
			if ui.Button(c.Key("set-wipe"), "清除本地数据…").Disabled(u.busy).Clicked() {
				u.confirmWipe = true
			}
		})
		if u.dataMsg != "" {
			col := t.TextMuted
			if u.dataMsgBad {
				col = t.Danger
			}
			ui.Divider(c)
			ui.Text(c, u.dataMsg).FontSize(12).TextColor(col).Margin(10, 0, 10, 0)
		}
		ui.Divider(c)
		settingRow(c, "数据位置", a.dataDir(), func() {
			if ui.Button(c.Key("set-reveal"), "在访达中显示").Clicked() {
				mygo.Shell.ShowItemInFolder(a.svc.Store().Path())
			}
		})
	})
	_ = st
}

// importBlock 画导入区：选择文件 → 预览 → 选项 → 开始导入。
func (a *App) importBlock(c *ui.Context) {
	t := c.Theme()
	u := &a.ui
	if u.importBackup == nil {
		if ui.Button(c.Key("set-import-pick"), "选择备份文件…").Disabled(u.busy).Clicked() {
			a.pickImportFile()
		}
		return
	}
	ui.Column(c).Gap(10).Children(func() {
		card(c).Background(t.Surface).Gap(4).Children(func() {
			ui.Text(c, u.importPath).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Text(c, u.importInfo).FontSize(12)
		})
		ui.Segmented(c.Key("set-import-mode"), &u.importMode, importModes...).Label("导入方式")
		ui.Checkbox(c.Key("set-import-set"), &u.importSet, "同时采用备份里的设置")
		if u.importPrev.HasCredentials {
			ui.Checkbox(c.Key("set-import-creds"), &u.importCreds, "同时恢复备份里的登录凭据")
		}
		if u.importMode == 1 {
			ui.Text(c, "覆盖会先清空本机数据，之后以备份为准；清空前会自动留一份安全备份。").FontSize(11).TextColor(t.Warning)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("set-import-go"), "开始导入").Disabled(u.busy).Clicked() {
				if u.importMode == 1 {
					u.confirmImport = true
				} else {
					a.doImport()
				}
			}
			if ui.Button(c.Key("set-import-cancel"), "取消").Disabled(u.busy).Clicked() {
				a.cancelImport()
			}
		})
	})
}

// settingsDialogs 画需要二次确认的对话框（覆盖导入、清除数据）。
func (a *App) settingsDialogs(c *ui.Context) {
	u := &a.ui
	if ui.AlertDialog(c.Key("dlg-import"), &u.confirmImport, "覆盖本机数据？",
		"将用备份文件里的数据替换本机数据。覆盖前会自动备份当前数据，可随时再导入恢复。", "取消", "覆盖导入") == 1 {
		a.doImport()
	}
	if ui.AlertDialog(c.Key("dlg-wipe"), &u.confirmWipe, "清除全部本地数据？",
		"将删除本机保存的 Cursor 明细与 DeepSeek 账单。清除前会自动备份，钥匙串里的凭据不会被删除。", "取消", "清除") == 1 {
		a.wipeData()
	}
}

// openPrivacySettings 打开系统设置里的「完全磁盘访问权限」页面。
func openPrivacySettings() {
	go func() {
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles").Run()
	}()
}
