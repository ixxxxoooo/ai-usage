<div align="center">

<img src="resources/icon.png" width="96" alt="AI Usage icon" />

# AI Usage

**A macOS menu-bar app for AI billing usage** — see at a glance how many Cursor requests are left and how much DeepSeek balance remains.

[![CI](https://github.com/ixxxxoooo/ai-usage/actions/workflows/ci.yml/badge.svg)](https://github.com/ixxxxoooo/ai-usage/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ixxxxoooo/ai-usage?display_name=tag)](https://github.com/ixxxxoooo/ai-usage/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[简体中文](README.md) · English

</div>

<p align="center">
  <img src="docs/images/popover-light.png" width="300" alt="Menu-bar popover (light)" />
  <img src="docs/images/popover-dark.png" width="300" alt="Menu-bar popover (dark)" />
</p>

## Why

Cursor's legacy **500-request plan** and DeepSeek's balance both live behind web dashboards. AI Usage sits in the menu bar so the answer is one click away, and expands into a full panel with per-request details and daily bills. It is written in pure Go with the [MyGo](https://github.com/egoist/mygo) native UI toolkit: no WebView, no Electron, a ~12 MB download, and a look that follows the system (light/dark, accent color, vibrancy).

## Features

- **Menu bar**: choose what the tray shows (icon only / Cursor `359/500` / DeepSeek balance / both); a compact popover on click.
- **Cursor**: used / remaining / reset date / daily average / projected exhaustion date; per-request table with filtering, sorting and charts.
- **DeepSeek**: balance (top-up + granted), today's tokens and cost, this month, lifetime spend; daily and per-model bills.
- **Refresh**: every **1 minute** by default (1–60 min), refreshes on wake, keeps and labels stale data when offline, backs off on failure and honors `Retry-After`.
- **Alerts**: system notifications at 50/70/80/90/100 % of the Cursor quota and when DeepSeek balance gets low.
- **Data**: local SQLite; **backup export/import** (merge or replace, preview before import, automatic safety backup) and CSV export.
- **Polish**: light/dark, vibrancy, window position/size memory (recovers from unplugged displays), open at login.

## Install

1. Download `AI-Usage-<version>.dmg` (universal: Intel + Apple silicon) from [Releases](https://github.com/ixxxxoooo/ai-usage/releases/latest) and drag **AI Usage** to Applications. Requires macOS 13+.
2. The app is **ad-hoc signed** (no Apple Developer ID, not notarized). On first launch right-click → Open → Open, or run `xattr -dr com.apple.quarantine "/Applications/AI Usage.app"`.
3. Optional: verify with `shasum -a 256 -c SHA256SUMS`.

Build from source: `make build` (or `make build-universal`). Only Go is required; MyGo needs no cgo.

## Setup

- **Cursor**: read automatically from the local, logged-in Cursor app (read-only). If that fails, paste a cookie in *Settings → Cursor*.
- **DeepSeek**: add an API key for the balance; the platform token (for bills and today's tokens) is read read-only from Chrome-family browsers' localStorage, or pasted manually.

## Privacy

Everything runs locally; there is no server, analytics or telemetry. Requests only go to `cursor.com`, `platform.deepseek.com` and `api.deepseek.com`. Manually entered credentials live only in the macOS Keychain and are excluded from backups by default. See [docs/PRIVACY.md](docs/PRIVACY.md) (Chinese) and [SECURITY.md](SECURITY.md).

## Development

`make test`, `make vet`, `make run`, `make live`. See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Disclaimer

Not affiliated with Cursor (Anysphere) or DeepSeek. It uses the same unofficial endpoints the dashboards use with your own session; they may change at any time.

## License

[MIT](LICENSE) © 2026 ygw

### Cursor accounts that are not "500 requests"

Team and new-plan accounts are measured by a dollar allowance; accounts without a fixed cap only show usage. The reset time is accurate to the minute, and switching accounts only shows the current account's events. See [docs/CURSOR_PLANS.md](docs/CURSOR_PLANS.md) (Chinese).
