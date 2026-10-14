# 更新日志

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

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

[1.0.0]: https://github.com/ixxxxoooo/ai-usage/releases/tag/v1.0.0
