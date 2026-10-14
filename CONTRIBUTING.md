# 贡献指南

感谢你愿意参与 AI Usage！无论是报 Bug、提想法、改文档还是提交代码都欢迎。参与前请阅读 [行为准则](CODE_OF_CONDUCT.md)。

## 报告问题

- Bug：请用 [Bug 模板](https://github.com/ixxxxoooo/ai-usage/issues/new?template=bug_report.yml)，附上版本号、macOS 版本、复现步骤。**不要粘贴 Cookie / token / API Key**。
- 想法：用 [功能建议模板](https://github.com/ixxxxoooo/ai-usage/issues/new?template=feature_request.yml)。
- 安全问题：**不要** 公开提 Issue，见 [SECURITY.md](SECURITY.md)。

## 开发环境

- macOS 13+，Go（`go.mod` 指定版本，`GOTOOLCHAIN=auto` 自动下载）。无需 cgo、Node 或 Xcode 工程。
- 常用命令（见 `Makefile`）：

```bash
make test    # go test -race ./...
make vet     # go vet + mygo vet（检查原生 UI 生命周期）
make fmt     # gofmt 检查
make run     # 热重载运行
make build   # 构建 .app / .dmg
```

- 调试请设置 `AIUSAGE_DATA_DIR=/某个临时目录`：数据和凭据都落在该目录，不会碰你真实的钥匙串与数据。
- 想用真实账号做只读联调：`make live`（需要本机已登录 Cursor / Chrome 里登录过 DeepSeek 平台）。该测试只调用只读接口，不会打印凭据。

## 提交代码

1. Fork 并从 `main` 创建分支：`feat/xxx`、`fix/xxx`、`docs/xxx`。
2. 保持改动聚焦；新增行为要有测试，修 Bug 先补一个能复现的测试。
3. 提交前确保：`make fmt vet test` 全部通过。
4. 提交信息使用 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)，描述写中文或英文均可，例如：
   - `feat: 设置页增加通知阈值`
   - `fix: 修复离线时浮窗显示 0 次`
   - `docs: 补充隐私说明`
5. 发起 PR，按模板填写；CI（格式、vet、测试、构建 DMG）必须通过。

## 代码约定

- 每个导出与关键方法写中文注释，说明用途、参数、返回值；单个方法不超过 300 行。
- 判空统一用 `util.IsEmpty`；对外输入做合法性校验。
- 视图模型保持纯函数以便单测；界面里的阻塞操作放 goroutine，通过 `Window.Update` 回主线程。
- 凭据只能进钥匙串（`internal/secret`），日志与测试输出必须脱敏（`util.MaskSecret`）；读取浏览器 / Cursor 数据一律只读。
- 改了界面请顺手运行 `make shots` 看一眼浅色 / 深色截图。
- 新增依赖前请先在 Issue 里讨论，尽量保持纯 Go、零 cgo。

## 发布流程（维护者）

1. 更新 `mygo.json` 的 `version` 与 `CHANGELOG.md`。
2. 合并到 `main`，CI 通过。
3. 打 tag：`git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z`。
4. `Release` 工作流会校验 tag 与 `mygo.json` 版本一致、构建通用 DMG、生成 `SHA256SUMS` 并创建 GitHub Release（说明取自 CHANGELOG）。
