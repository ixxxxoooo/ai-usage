package gui

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/ixxxxoooo/ai-usage/internal/model"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/transfer"
	"github.com/ixxxxoooo/ai-usage/internal/util"
)

// AppVersion 是应用版本号，显示在「关于」并写入备份文件。
const AppVersion = "1.1.0"

// hooks 是界面对外部世界的交互入口：文件对话框与异步执行。
// 默认走真实实现，测试时可整体替换，避免弹窗与 goroutine。
//
// @author ygw
type hooks struct {
	saveFile func(title, name string) (string, error) // 保存对话框；取消返回空串
	openFile func(title string) (string, error)       // 打开对话框；取消返回空串
	async    func(a *App, work func() func())         // 后台执行 work，返回的回调在主线程执行
	getLogin func() bool                              // 读取开机自启状态
	setLogin func(on bool) error                      // 设置开机自启
	openPage func(page string)                        // 打开大面板并跳到指定页面（测试里替换为记录）
}

// h 返回带默认值的 hooks。
func (a *App) h() *hooks {
	if a.hk.saveFile == nil {
		a.hk.saveFile = func(title, name string) (string, error) {
			return mygo.Dialog.Save(mygo.SaveDialogOptions{
				Parent: a.panel, Title: title, DefaultPath: name, CreateDirectories: true,
			})
		}
	}
	if a.hk.openFile == nil {
		a.hk.openFile = func(title string) (string, error) {
			ps, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent: a.panel, Title: title,
				Filters: []mygo.FileFilter{{Name: "AI Usage 备份 (JSON)", Extensions: []string{"json"}}},
			})
			if err != nil || len(ps) == 0 {
				return "", err
			}
			return ps[0], nil
		}
	}
	if a.hk.getLogin == nil {
		a.hk.getLogin = func() bool { return mygo.App.OpenAtLogin() }
	}
	if a.hk.setLogin == nil {
		a.hk.setLogin = func(on bool) error { return mygo.App.SetOpenAtLogin(on) }
	}
	if a.hk.async == nil {
		a.hk.async = func(a *App, work func() func()) {
			win := a.panel
			go func() {
				done := work()
				if done == nil {
					return
				}
				if win == nil {
					return
				}
				win.Update(done)
			}()
		}
	}
	return &a.hk
}

// run 在后台执行 work，并把返回的回调切回主线程执行。
func (a *App) run(work func() func()) { a.h().async(a, work) }

// say 显示一条提示（toast）。
func (a *App) say(msg string) { a.ui.toast = msg }

// setDataMsg 设置数据管理区的结果文案。
func (a *App) setDataMsg(msg string, bad bool) {
	a.ui.dataMsg, a.ui.dataMsgBad = msg, bad
	a.say(msg)
}

// dataDir 返回数据目录（数据库所在目录）。
func (a *App) dataDir() string { return filepath.Dir(a.svc.Store().Path()) }

// today 返回 yyyyMMdd 形式的当前日期。
func (a *App) today() string { return a.now().Format("20060102") }

// exportCursorCSV 把当前筛选后的 Cursor 明细导出为 CSV（弹出保存对话框）。
//
// 参数 rows：要导出的明细。
func (a *App) exportCursorCSV(rows []model.CursorEvent) {
	if util.IsEmpty(rows) || a.ui.busy {
		return
	}
	a.ui.busy = true
	name := transfer.SuggestName("cursor-usage", "csv", a.today())
	a.run(func() func() {
		path, err := a.h().saveFile("导出 Cursor 明细", name)
		if err == nil && path != "" {
			err = transfer.SaveCursorCSV(path, rows)
		}
		return func() {
			a.ui.busy = false
			switch {
			case err != nil:
				a.setDataMsg("导出失败："+err.Error(), true)
			case path != "":
				a.setDataMsg(fmt.Sprintf("已导出 %d 条 Cursor 明细：%s", len(rows), path), false)
			}
		}
	})
}

// exportDeepSeekCSV 把当前筛选后的 DeepSeek 账单导出为 CSV。
//
// 参数 rows：要导出的账单行。
func (a *App) exportDeepSeekCSV(rows []model.DeepSeekDaily) {
	if util.IsEmpty(rows) || a.ui.busy {
		return
	}
	a.ui.busy = true
	name := transfer.SuggestName("deepseek-bill", "csv", a.today())
	a.run(func() func() {
		path, err := a.h().saveFile("导出 DeepSeek 账单", name)
		if err == nil && path != "" {
			err = transfer.SaveDeepSeekCSV(path, rows)
		}
		return func() {
			a.ui.busy = false
			switch {
			case err != nil:
				a.setDataMsg("导出失败："+err.Error(), true)
			case path != "":
				a.setDataMsg(fmt.Sprintf("已导出 %d 行 DeepSeek 账单：%s", len(rows), path), false)
			}
		}
	})
}

// exportBackup 导出完整备份（JSON）。
//
// 参数 withCreds：是否包含登录凭据（明文，需要用户明确勾选）。
func (a *App) exportBackup(withCreds bool) {
	if a.ui.busy {
		return
	}
	a.ui.busy = true
	name := transfer.SuggestName("ai-usage-backup", "json", a.today())
	now := a.now()
	a.run(func() func() {
		path, err := a.h().saveFile("导出备份", name)
		var b *transfer.Backup
		if err == nil && path != "" {
			b, err = transfer.BuildBackup(a.svc.Store(), transfer.ExportOptions{
				AppVersion: AppVersion, IncludeCredentials: withCreds, Secrets: a.svc.Secrets(), Now: now,
			})
		}
		if err == nil && b != nil {
			err = transfer.WriteFile(path, b)
		}
		return func() {
			a.ui.busy = false
			switch {
			case err != nil:
				a.setDataMsg("导出失败："+err.Error(), true)
			case b != nil:
				msg := fmt.Sprintf("备份已保存：%s（Cursor %d 条，DeepSeek %d 行）", path, len(b.CursorEvents), len(b.DeepSeekDaily))
				if withCreds {
					msg += "。文件含明文凭据，请妥善保管"
				}
				a.setDataMsg(msg, false)
			}
		}
	})
}

// pickImportFile 选择备份文件并读取预览（不修改任何数据）。
func (a *App) pickImportFile() {
	if a.ui.busy {
		return
	}
	a.ui.busy = true
	a.run(func() func() {
		path, err := a.h().openFile("选择备份文件")
		var b *transfer.Backup
		var pv transfer.Preview
		if err == nil && path != "" {
			b, pv, err = transfer.ReadFile(path)
		}
		return func() {
			a.ui.busy = false
			switch {
			case err != nil:
				a.ui.importBackup = nil
				a.setDataMsg("无法导入："+err.Error(), true)
			case b != nil:
				a.ui.importBackup, a.ui.importPrev, a.ui.importPath = b, pv, path
				a.ui.importInfo = pv.Summary()
				a.ui.importSet, a.ui.importCreds = false, false
				a.ui.dataMsg = ""
			}
		}
	})
}

// cancelImport 放弃已选择的备份文件。
func (a *App) cancelImport() {
	a.ui.importBackup, a.ui.importPath, a.ui.importInfo = nil, "", ""
	a.ui.importPrev = transfer.Preview{}
}

// doImport 执行导入（调用前界面已完成二次确认）。
func (a *App) doImport() {
	b := a.ui.importBackup
	if b == nil || a.ui.busy {
		return
	}
	a.ui.busy = true
	opt := transfer.ImportOptions{
		Replace:            a.ui.importMode == 1,
		ApplySettings:      a.ui.importSet,
		RestoreCredentials: a.ui.importCreds && a.ui.importPrev.HasCredentials,
		Secrets:            a.svc.Secrets(),
		BackupDir:          filepath.Join(a.dataDir(), "backups"),
		AppVersion:         AppVersion,
	}
	a.run(func() func() {
		res, err := transfer.Import(a.svc.Store(), b, opt)
		return func() {
			a.ui.busy = false
			if err != nil {
				a.setDataMsg("导入失败（本机数据保持不变）："+err.Error(), true)
				return
			}
			a.svc.ReloadFromStore()
			a.reloadNow()
			a.invalidate()
			a.loadSettingsDraft()
			msg := fmt.Sprintf("导入完成：新增 Cursor %d 条（跳过重复 %d）、DeepSeek 账单 %d 行", res.EventsAdded, res.EventsSkipped, res.DailyUpserted)
			if res.CredentialsRestored > 0 {
				msg += fmt.Sprintf("，已恢复 %d 项凭据", res.CredentialsRestored)
			}
			if res.SafetyBackup != "" {
				msg += "。覆盖前已自动备份到 " + res.SafetyBackup
			}
			a.cancelImport()
			a.setDataMsg(msg, false)
		}
	})
}

// wipeData 清空本机业务数据（凭据仍留在钥匙串）。
func (a *App) wipeData() {
	if a.ui.busy {
		return
	}
	a.ui.busy = true
	a.run(func() func() {
		_, berr := transfer.SafetyBackup(a.svc.Store(), filepath.Join(a.dataDir(), "backups"), AppVersion)
		var err error
		if berr == nil {
			err = a.svc.Store().Wipe()
		} else {
			err = fmt.Errorf("清空前自动备份失败，已取消：%w", berr)
		}
		return func() {
			a.ui.busy = false
			if err != nil {
				a.setDataMsg("清除失败："+err.Error(), true)
				return
			}
			a.svc.ReloadFromStore()
			a.reloadNow()
			a.invalidate()
			a.loadSettingsDraft()
			a.setDataMsg("本地数据已清除（清除前已自动备份）。可立即刷新重新拉取", false)
			a.RefreshNow()
		}
	})
}

// testConnection 后台测试某个服务的连接。
//
// 参数 which：secret 之外的服务名 "cursor" 或 "deepseek"。
func (a *App) testConnection(which string) {
	if which == "cursor" {
		if a.ui.testingC {
			return
		}
		a.ui.testingC, a.ui.testCursor = true, "测试中…"
	} else {
		if a.ui.testingD {
			return
		}
		a.ui.testingD, a.ui.testDS = true, "测试中…"
	}
	a.run(func() func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var msg string
		var err error
		if which == "cursor" {
			msg, err = a.svc.TestCursor(ctx)
		} else {
			msg, err = a.svc.TestDeepSeek(ctx)
		}
		if err != nil {
			msg = "失败：" + err.Error()
		}
		return func() {
			if which == "cursor" {
				a.ui.testingC, a.ui.testCursor = false, msg
			} else {
				a.ui.testingD, a.ui.testDS = false, msg
			}
		}
	})
}

// saveCredential 保存/清除一个手动凭据并刷新「已保存」状态。
//
// 参数 key：secret.Key*；value：内容，空串表示清除。
// 返回值：无；结果通过提示反馈。
func (a *App) saveCredential(key, value string) {
	if err := a.svc.SaveSecret(key, value); err != nil {
		a.say("保存失败：" + err.Error())
		return
	}
	a.loadSecretFlags()
	if util.IsEmpty(value) {
		a.say("已清除")
		return
	}
	a.say("已保存到钥匙串")
	a.svc.Kick()
}

// loadSecretFlags 读取各凭据是否已保存及脱敏文本（只在进入设置页/保存后调用）。
func (a *App) loadSecretFlags() {
	s := a.svc.Secrets()
	read := func(key string) (bool, string) {
		if s == nil {
			return false, ""
		}
		v, err := s.Get(key)
		if err != nil || util.IsEmpty(v) {
			return false, ""
		}
		return true, util.MaskSecret(v)
	}
	a.ui.hasCookie, a.ui.maskCookie = read(secret.KeyCursorCookie)
	a.ui.hasKey, a.ui.maskKey = read(secret.KeyDeepSeekKey)
	a.ui.hasToken, a.ui.maskToken = read(secret.KeyDeepSeekToken)
}

// openPage 打开大面板并跳到指定页面（浮窗里点卡片用）。
//
// 参数 page：PageOverview / PageCursor / PageDeepSeek / PageSettings。
func (a *App) openPage(page string) {
	if a.hk.openPage != nil {
		a.hk.openPage(page)
		return
	}
	a.OpenPanel(page)
}
