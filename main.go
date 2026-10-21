// AI Usage：macOS 菜单栏 AI 账单用量工具（MyGo 原生 UI）。
//
// 环境变量：
//
//	AIUSAGE_DATA_DIR  指定数据目录（测试/E2E 用；设置后凭据改存该目录下的文件而不是钥匙串）
//
// @author ixxxxoooo
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/ixxxxoooo/ai-usage/internal/gui"
	"github.com/ixxxxoooo/ai-usage/internal/secret"
	"github.com/ixxxxoooo/ai-usage/internal/service"
	"github.com/ixxxxoooo/ai-usage/internal/store"
)

// dataDir 返回数据目录：优先环境变量，否则 ~/Library/Application Support/AI Usage。
//
// 返回值：目录路径与是否为测试目录（测试目录下凭据不进钥匙串）。
func dataDir() (string, bool) {
	if d := os.Getenv("AIUSAGE_DATA_DIR"); d != "" {
		return d, true
	}
	d, err := mygo.App.Path(mygo.PathUserData)
	if err != nil || d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, "Library", "Application Support", "AI Usage")
	}
	return d, false
}

// run 完成初始化并进入事件循环。
//
// 返回值：运行期错误。
func run() error {
	mygo.App.SetName("AI Usage")
	self := parseSelfTest(os.Args[1:])
	if self.mode == "" && !mygo.App.RequestSingleInstanceLock() {
		return nil // 已有实例在运行：参数已转交，本进程直接退出（自检模式不占用单实例锁）
	}
	dir, isTest := dataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dir, "usage.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	var secrets secret.Store = secret.Keychain{}
	if isTest {
		secrets = &secret.File{Path: filepath.Join(dir, "secrets.json")}
	}

	app := gui.New()
	if self.mode == "" {
		app.EnableUpdates() // 自检模式不检查更新，免得弹窗打断 E2E
	}
	svc, err := service.New(service.Config{
		Store: st, Secrets: secrets,
		OnChange: app.OnChange, Notify: app.Notify,
	})
	if err != nil {
		return err
	}
	app.Attach(svc)

	mygo.App.OnSecondInstance(func([]string, string) { app.OpenPanel(gui.PageOverview) })
	mygo.App.WhenReady(func() {
		if err := app.Start(); err != nil {
			log.Fatal("创建托盘失败: ", err)
		}
		svc.Start(context.Background())
		if self.mode != "" {
			app.RunSelfTest(self.mode, self.out, self.bounds)
		}
	})
	return mygo.App.Run()
}

// selfTest 是命令行自检参数（供 E2E 脚本使用）。
type selfTest struct{ mode, out, bounds string }

// parseSelfTest 解析 --selftest=模式 --selftest-out=路径 --selftest-bounds=x,y,w,h。
//
// 参数 args：命令行参数（不含程序名）。
// 返回值：解析结果；没有 --selftest 时 mode 为空。
func parseSelfTest(args []string) selfTest {
	var s selfTest
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--selftest="):
			s.mode = strings.TrimPrefix(a, "--selftest=")
		case strings.HasPrefix(a, "--selftest-out="):
			s.out = strings.TrimPrefix(a, "--selftest-out=")
		case strings.HasPrefix(a, "--selftest-bounds="):
			s.bounds = strings.TrimPrefix(a, "--selftest-bounds=")
		}
	}
	if s.mode != "" && s.out == "" {
		s.out = filepath.Join(os.TempDir(), "aiusage-selftest.json")
	}
	return s
}

// main 程序入口。
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
