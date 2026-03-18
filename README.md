# proxy-router

A lightweight local proxy (`localhost:32000`) that forwards connections to an upstream proxy or goes direct, based on configurable rules evaluated per-request.

## Features

- HTTP and HTTPS (`CONNECT`) proxying
- Rule-based routing: forward upstream or go direct based on SSID, hostname, or IP
- Authenticated upstream proxies (`http://user:pass@host:port`)
- Hot config reload — save the file and changes apply within 1 second (or send `SIGHUP`)
- macOS network change listener via `SCDynamicStore` — SSID cache updated on network events, zero per-request shell-outs
- Per-user install with LaunchAgent autostart

## Commands

```
proxy-router install              Install and start at login
proxy-router uninstall            Stop and remove (keeps config)
proxy-router uninstall --prune    Stop, remove everything including config
proxy-router run                  Start the proxy
proxy-router run -config <path>   Start with a custom config path
proxy-router help                 Show help
```

## Install

```bash
# Build
go build -o proxy-router ./cmd/proxy

# Install (copies binary, writes default config, registers LaunchAgent)
./proxy-router install
```

Installed paths (all per-user, no sudo required):

| Path | Purpose |
|---|---|
| `~/.local/bin/proxy-router` | Binary |
| `~/.config/proxy-router/config.json` | Config |
| `~/Library/LaunchAgents/com.local.proxy-router.plist` | LaunchAgent |
| `~/Library/Logs/proxy-router.{log,err}` | Logs |

After install, edit `~/.config/proxy-router/config.json` — changes are picked up automatically within 1 second.

## Uninstall

```bash
# Remove everything except config
proxy-router uninstall

# Remove everything including config
proxy-router uninstall --prune
```

## macOS system proxy

Point macOS at the proxy via System Settings → Network → (interface) → Proxies, or via CLI:

```bash
networksetup -setwebproxy Wi-Fi localhost 32000
networksetup -setsecurewebproxy Wi-Fi localhost 32000
```

## Config

Rules are evaluated **top-to-bottom**; the first match wins. Each rule can match on:

- `ssids` — current Wi-Fi SSID (case-insensitive)
- `domains` — destination hostname suffix (`corp.com` matches `jira.corp.com`)
- `ips` — destination IP (exact match)

All matchers in a rule must match (AND logic). If no rule matches, `default` is used (`"direct"` or `"upstream"`).

```json
{
  "listen": "localhost:32000",
  "upstream": "http://user:pass@corporate-proxy:8080",
  "default": "direct",
  "rules": [
    {
      "ssids": ["OfficeWifi", "CorpVPN"],
      "action": "upstream"
    },
    {
      "domains": ["internal.corp.com", "jira.corp.com"],
      "action": "upstream"
    },
    {
      "domains": ["localhost"],
      "ips": ["127.0.0.1", "::1"],
      "action": "direct"
    }
  ]
}
```

## Hot reload

Changes to the config file are picked up automatically within 1 second. You can also trigger a manual reload:

```bash
kill -HUP $(pgrep proxy-router)
```

## Build notes

This project uses cgo (`SCDynamicStore` via `SystemConfiguration.framework`) and is macOS-only. Requires Xcode Command Line Tools:

```bash
xcode-select --install
```
