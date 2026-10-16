# 架构说明

> 作者：ixxxxoooo

AI Usage 是一个 Go 编写的 macOS 菜单栏应用，UI 使用 [MyGo](https://github.com/egoist/mygo) 的原生控件（无 WebView、无 cgo）。

## 分层

```
providers ──► service ──► store (SQLite)
 (cursor,      (刷新状态机、     ▲
  deepseek)     退避、通知)       │
                  │              │
                  ▼              │
                 gui ◄── transfer（导出/导入/CSV）
        (视图模型 · 托盘 · 浮窗 · 大面板)
```

| 目录 | 职责 |
| --- | --- |
| `main.go` | 装配：数据目录、存储、凭据、服务、界面；`--selftest` 自检入口 |
| `internal/provider/cursor` | 只读取本机 Cursor 登录；调用用量与明细接口；解析与分页 |
| `internal/provider/antigravity` | 读取本机 Antigravity 语言服务的模型额度（见 [ANTIGRAVITY.md](ANTIGRAVITY.md)） |
| `internal/provider/deepseek` | API Key 查余额；从浏览器 localStorage 取平台 token；汇总/月账单解析 |
| `internal/service` | 调度（默认 1 分钟）、三个数据源并发刷新、失败退避、离线保留旧数据、阈值通知、凭据保存与连通性测试、首次同步「先本周期再历史」 |
| `internal/store` | SQLite（modernc，纯 Go）：明细、账单、快照、设置、事务化数据集导入 |
| `internal/secret` | 凭据接口：钥匙串 / 文件（测试目录）/ 内存 |
| `internal/transfer` | 备份 JSON 的校验、迁移、导入（合并/覆盖/回滚）、安全备份、CSV |
| `internal/placement` | 浮窗相对托盘的位置、多显示器与窗口恢复（纯函数，表格测试） |
| `internal/gui` | 视图模型（纯函数）、托盘、浮窗、大面板四页、设置与导入导出动作、真实窗口自检 |
| `internal/model` / `util` / `httpx` | 领域模型与计算、通用工具、HTTP 公共（UA、超时、拦截页识别、Retry-After） |
| `internal/live` | `-tags live` 的真实数据只读联调 |

## 关键设计

- **状态机**：每个数据源有 `idle/loading/ok/offline/unauthorized/no-credentials/rate-limited` 等状态；失败时 **保留旧数据** 并标注「显示的是上次数据」，按基础间隔指数退避，被限流时遵守 `Retry-After`。
- **单线程 UI**：界面状态只在主线程读写；跨线程的明细数据整体替换并加锁读取；耗时操作（对话框、测试连接、导入导出）放 goroutine，通过 `Window.Update` 回主线程。
- **可测试性**：视图模型是纯函数；`gui` 通过 `hooks`（文件对话框、异步执行、开机自启）注入，测试里同步可控；`ui.NewTester` 做无头界面测试与截图。
- **安全**：凭据只进钥匙串；浏览器与 Cursor 数据只读；导出默认不含凭据；日志脱敏。详见 [PRIVACY.md](PRIVACY.md)。
- **导入**：整个写库在一个事务内，失败整体回滚；覆盖前自动安全备份。格式见 `.specstory/history/docs/20261014_导出导入.md`。

## 浮窗几何

托盘浮窗使用原生 NSPopover，窗口比内容四周各多 13 DIP（箭头与阴影）。内容 316×332（开启 Antigravity 时再加 130，即 316×462），窗口比内容四周各大 13。`--selftest=position` 在真实窗口里校验：水平对齐托盘图标、紧贴菜单栏、完整落在显示器内。

## 真实窗口自检

```bash
AIUSAGE_DATA_DIR=$(mktemp -d) "AI Usage.app/Contents/MacOS/AI Usage" \
  --selftest=position --selftest-out=/tmp/pos.json       # 浮窗位置
  # --selftest=panel-set --selftest-bounds=220,160,900,600 → 重启 → --selftest=panel-get
```

`AIUSAGE_DATA_DIR` 会让数据与凭据都落在该目录（凭据用文件而非钥匙串），避免污染真实环境。

## Cursor 账号类型与隔离（v1.1.0）

用量支持次数制与金额制两种口径，明细按账号隔离（schema v2），详见 [CURSOR_PLANS.md](CURSOR_PLANS.md)。

## Cursor 明细去重（v1.2.0）

明细按「时间 + 模型 + 会话 + 类型」去重，同一事件原地更新；同步时与远端对齐。详见 [CURSOR_DUPLICATES.md](CURSOR_DUPLICATES.md)。
