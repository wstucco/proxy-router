# proxy-router

A lightweight local proxy (`localhost:32000`) that forwards connections to an upstream proxy or goes direct, based on configurable rules evaluated per-request.

## Rules

Rules are evaluated **top-to-bottom**; the first match wins. Each rule can match on:

- `ssids` — current Wi-Fi SSID (case-insensitive)
- `domains` — destination hostname suffix (e.g. `corp.com` matches `jira.corp.com`)
- `ips` — destination IP (exact match)

All specified matchers in a rule must match (AND logic). If no rule matches, `default` is used.

Actions: `"direct"` (connect directly) or `"upstream"` (route via the upstream proxy).

## Build

```bash
go build -o proxy-router ./cmd/proxy
```

## Run

```bash
./proxy-router -config config.json
```

Generate an example config:

```bash
./proxy-router -gen-config > config.json
```

## Hot reload

Send `SIGHUP` to reload config without restarting:

```bash
kill -HUP $(pgrep proxy-router)
```

## macOS system proxy

Point macOS at the proxy via System Settings → Network → (interface) → Proxies:
- **Web Proxy (HTTP):** `localhost` port `32000`
- **Secure Web Proxy (HTTPS):** `localhost` port `32000`

Or via CLI:
```bash
networksetup -setwebproxy Wi-Fi localhost 32000
networksetup -setsecurewebproxy Wi-Fi localhost 32000
```

## LaunchAgent (autostart)

```bash
# Build and install binary
go build -o /usr/local/bin/proxy-router ./cmd/proxy

# Install config
mkdir -p /usr/local/etc/proxy-router
cp config.json /usr/local/etc/proxy-router/config.json

# Install LaunchAgent
cp com.local.proxy-router.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.local.proxy-router.plist
```

## Upstream proxy formats

- HTTP proxy: `http://host:port`
- HTTP proxy with auth: `http://user:pass@host:port`
- SOCKS5: not yet supported natively (PRs welcome)
