# 更新日志

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [1.1.0] - 2026-10-15

### 新增

- Cursor 与 DeepSeek 账单页标题栏新增「在官网查看」，一键打开对应官网用量页。
- 柱状图鼠标悬停时立刻显示气泡（日期 / 小时 + 次数 + 条数 或 金额 + Token），不再等系统提示。
- 菜单栏浮窗里点击 Cursor 或 DeepSeek 卡片，会直接打开主窗口并跳到对应页面。
- 支持非 500 次的 Cursor 账号：新计划、团队账号按「金额额度」（美元）展示，无固定上限时只显示已用，不再误报 500 次。详见 [docs/CURSOR_PLANS.md](docs/CURSOR_PLANS.md)。
- 重置时间精确到分钟：优先使用接口给出的账期结束时刻，显示为「10/16 10:24 重置 · 还剩 12时5分」。

### 修复

- 切换 Cursor 账号后，明细里混着新旧两个账号的数据：现在每条明细都按账号隔离，切换账号立即清空旧账号的用量并只显示当前账号的明细。
- 模型下拉列表过长溢出窗口：改为限高并带滚动条。

### 升级注意

- 数据库升级到 v2：旧版本没有记录明细属于哪个账号，升级时会清除本地已缓存的 Cursor 明细并在首次刷新时重新同步当前账号（用量与 DeepSeek 账单不受影响）。

## [1.0.1] - 2026-10-15

### 修复

- DeepSeek 一直显示「未登录」：macOS 不允许从 Finder 启动的应用读取 Chrome 数据目录（`operation not permitted`），现在会明确提示并在设置页提供「打开隐私设置」入口。
- 多个浏览器 / Profile 里有过期登录时，只取第一个会误判为登录过期；现在会依次尝试所有候选，直到有一个可用。

### 改进

- 手动粘贴 token 更宽容：支持 `Bearer xxx`、整行 `authorization: …`、localStorage 的 JSON；粘成 Cookie 时给出明确提示。
- 设置页说明改为「不需要 Cookie，复制 authorization: Bearer 后的 token」。
- 新增 `--selftest=deepseek` 与 [DeepSeek 登录说明](docs/DEEPSEEK_AUTH.md)，可在真实应用进程里排查登录获取。

## [1.0.0] - 2026-10-15

首个正式版本。

### 新增

- 菜单栏托盘：可选显示仅图标 / Cursor 用量 / DeepSeek 余额 / 两者；左键弹出紧凑浮窗，右键菜单。
- Cursor：本周期已用 / 剩余 / 重置日期 / 日均 / 预测用完日期；逐条请求明细（筛选、排序、图表、CSV 导出）。
- DeepSeek：余额（充值 + 赠送）、今日 Token / 花费、本月与累计已用；每日、每模型账单与图表、CSV 导出。
- 大面板：概览、Cursor 明细、DeepSeek 账单、设置四页；窗口位置与大小记忆，拔掉显示器后自动回到可见区域。
- 自动刷新：默认每 1 分钟（1–60 分钟可选），唤醒后立即刷新；离线保留并标注上次数据；失败退避并遵守 `Retry-After`。
- 提醒：Cursor 用量阈值（50/70/80/90/100 %）与 DeepSeek 低余额系统通知。
- 凭据：Cursor 自动读取本机登录（只读）或手动 Cookie；DeepSeek API Key + 平台 token（自动读 Chrome 系浏览器 / 手动），手动凭据只存 macOS 钥匙串。
- 数据管理：JSON 备份导出 / 导入（预览、合并去重或覆盖、事务回滚、覆盖前自动备份、旧版迁移）、清除本地数据。
- 开机自启、浅色 / 深色自适应、毛玻璃。
- 通用（Intel + Apple 芯片）DMG 安装包；GitHub Actions 持续集成与自动发布。

[1.0.1]: https://github.com/ixxxxoooo/ai-usage/releases/tag/v1.0.1
[1.0.0]: https://github.com/ixxxxoooo/ai-usage/releases/tag/v1.0.0
