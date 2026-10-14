# 隐私与数据说明

AI Usage 是一个 **纯本地** 的桌面应用：没有自己的服务端，没有账号体系，没有统计、遥测或崩溃上报。

## 它会访问什么

| 对象 | 用途 | 方式 |
| --- | --- | --- |
| `https://cursor.com` | 读取你自己的用量与请求明细 | 用你本机 Cursor 的登录会话（或你手动粘贴的 Cookie）调用网页端接口 |
| `https://api.deepseek.com` | 用 API Key 查询余额 | 官方公开接口 |
| `https://platform.deepseek.com` | 用平台登录 token 读取汇总、每日账单、今日 Token | 网页端接口 |

除此之外应用不会连接任何其他主机。可以用 Little Snitch / `lsof -i` 自行核对。

## 它会读取本机什么（全部只读）

- **Cursor 登录状态**：以只读方式打开 `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb`，只取出访问令牌用于调用接口。该文件不会被复制或修改。
- **浏览器 localStorage**（仅「自动读取 DeepSeek 登录」时）：扫描 Chrome / Edge / Brave / Arc / Vivaldi / Chromium 各 Profile 里 `https://platform.deepseek.com` 的 `userToken`。为避免与浏览器抢文件锁，先把 leveldb 目录 **复制到系统临时目录**、只读打开、读完立即删除；浏览器原始数据从不被修改。
- 读取需要 macOS「完全磁盘访问权限」；未授权时读取会被系统拒绝，应用只会提示，不会绕过。详见 [DEEPSEEK_AUTH.md](DEEPSEEK_AUTH.md)。
- 若不想让应用读取浏览器，可在「设置 → DeepSeek」选择「手动粘贴 token」，应用就不会扫描浏览器。

## 它会保存什么

| 数据 | 位置 | 说明 |
| --- | --- | --- |
| 用量快照、请求明细、每日账单、设置 | `~/Library/Application Support/AI Usage/usage.db`（SQLite） | 只含用量数字、模型名、时间等，**不含任何凭据** |
| 手动粘贴的 Cookie / API Key / token | macOS 钥匙串 | 不写入数据库、日志、备份（默认） |
| 窗口位置与大小 | `window-state.json` | |
| 自动安全备份 | `backups/` | 覆盖导入、清除数据前自动生成，保留最近 5 份 |

内存中，从浏览器读到的 token 只缓存在进程内，不落盘。日志中所有凭据都会脱敏（只显示首尾几位与长度）。

## 备份文件

- 「导出备份」默认 **不包含** 凭据；仅当你勾选「同时导出登录凭据」才会以明文写入文件（界面有风险提示），请妥善保管该文件。
- 导入时即使文件含凭据，也要你再勾选「同时恢复登录凭据」才会写回钥匙串。

## 如何彻底删除

1. 退出并删除 `/Applications/AI Usage.app`。
2. 删除目录 `~/Library/Application Support/AI Usage/`。
3. 打开「钥匙串访问」，搜索 *AI Usage* 并删除相关条目。

## 反馈

隐私或安全方面的疑问与发现，请按 [SECURITY.md](../SECURITY.md) 联系。
