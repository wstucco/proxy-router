# proxy-router

A lightweight local proxy (`localhost:1337`) that forwards connections to an upstream proxy or goes direct, based on configurable rules evaluated per-request.

## Changelog

See [CHANGELOG](CHANGELOG) for the full history.

## Features

- HTTP and HTTPS (`CONNECT`) proxying
- Rule-based routing: forward upstream or go direct based on SSID, hostname, or IP
- Authenticated upstream proxies (`http://username:pass@host:port`)
- Hot config reload — save the file and changes apply within 1 second (or send `SIGHUP`)
- macOS network change listener via `SCDynamicStore` — SSID cache updated on network events
- Brew service and manual LaunchAgent support

## Upstream proxy authentication

proxy-router automatically negotiates the correct authentication scheme (Basic, NTLM, Negotiate) by inspecting the upstream proxy's response — no manual configuration needed.

Credentials are specified in the upstream URL:

```json
"upstream": "http://username:password@proxy.corp.com:8080"
```

### Active Directory / NTLM

If the proxy requires NTLM authentication on an Active Directory network, set the domain separately via `upstream_domain`:

```json
{
  "upstream": "http://username:password@proxyu.corp.it:80",
  "upstream_domain": "DOMAIN"
}
```

Do not encode the domain in the username in the URL — this causes URL parsing failures. Use `upstream_domain` instead.

The domain is required for NTLM. Without it, Basic auth may work initially but fail after the session expires and the proxy switches to Negotiate.
## Install

### Homebrew (recommended)

```bash
brew tap wstucco/tap
brew install proxy-router
brew services start proxy-router
proxy-router install   # installs shell completions
```

### Manual

```bash
# Build
go build -o proxy-router ./cmd/proxy

# Install binary
sudo mv proxy-router /usr/local/bin/proxy-router

# Install config, completions, and register LaunchAgent
proxy-router install
```

## Commands

```
proxy-router run                        Start the proxy
proxy-router run -listen localhost:33000 -config ~/myconf.json
proxy-router install                    Write config, install completions, register LaunchAgent
proxy-router uninstall                  Deregister LaunchAgent, remove completions (keeps config)
proxy-router uninstall --prune          Remove everything including config
proxy-router completion <zsh|bash|fish> Print completion script
proxy-router version                    Print version
proxy-router help                       Show help
```

## Paths

### Homebrew install

| Path | Purpose |
|---|---|
| `/opt/homebrew/bin/proxy-router` | Binary |
| `/opt/homebrew/etc/proxy-router/config.json` | Config |
| `/opt/homebrew/var/log/proxy-router.log` | Log |
| managed by `brew services` | LaunchAgent |

### Manual install

| Path | Purpose |
|---|---|
| `/usr/local/bin/proxy-router` | Binary |
| `/usr/local/etc/proxy-router/config.json` | Config |
| `/usr/local/var/log/proxy-router/proxy-router.log` | Log |
| `/Library/LaunchAgents/com.wstucco.proxy-router.plist` | LaunchAgent |

## Local dev run

Run with a custom config and port without installing anything:

```bash
proxy-router run -config ~/myconf.json -listen localhost:33000
```

## Config

Rules are evaluated **top-to-bottom**; the first match wins. Each rule can match on:

- `ssids` — current Wi-Fi SSID (case-insensitive)
- `domains` — destination hostname suffix (`corp.com` matches `jira.corp.com`)
- `ips` — destination IP (exact match)

All matchers in a rule must match (AND logic). If no rule matches, `default` is used.

```json
{
  "listen": "localhost:1337",
  "upstream": "http://username:pass@corporate-proxy:8080",
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

## Shell completions

Installed automatically by `proxy-router install`. To install manually:

```bash
# zsh
proxy-router completion zsh > ~/.zsh/completions/_proxy-router
# add to ~/.zshrc: fpath=(~/.zsh/completions $fpath) && autoload -Uz compinit && compinit

# bash
proxy-router completion bash > ~/.local/share/bash-completion/completions/proxy-router

# fish
proxy-router completion fish > ~/.config/fish/completions/proxy-router.fish
```

## Releasing

Tag a commit to trigger a GitHub Actions build and release:

```bash
git tag v1.0.0
git push origin v1.0.0
```

The CI will build the binary, create a GitHub release, and automatically update the Homebrew formula in `wstucco/homebrew-tap`. Requires a `HOMEBREW_TAP_TOKEN` secret (GitHub PAT with repo write access to the tap).

## Build notes

Requires cgo (`SystemConfiguration.framework`, macOS only):

```bash
xcode-select --install
go build -o proxy-router ./cmd/proxy
```