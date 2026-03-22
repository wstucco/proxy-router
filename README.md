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

## How it works

proxy-router sits at `localhost:1337` and intercepts all HTTP/HTTPS traffic routed through it. For each connection it evaluates the configured rules top-to-bottom and decides whether to forward the connection to an upstream proxy or connect directly.

Rules can match on the current Wi-Fi SSID, destination hostname, or destination IP. This makes it ideal for automatically switching between a corporate proxy at the office and a direct connection at home, without changing any system settings manually.

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
proxy-router run                         Start the proxy
proxy-router run -listen localhost:1337 -config ~/myconf.json
proxy-router install                     Write config, install completions, register LaunchAgent
proxy-router uninstall                   Deregister LaunchAgent, remove completions (keeps config)
proxy-router uninstall --prune           Remove everything including config
proxy-router completion <zsh|bash|fish>  Print completion script
proxy-router version                     Print version
proxy-router help                        Show help
```

## Setting up the proxy

### System-wide (recommended)

Point macOS system proxy at proxy-router so all applications use it automatically.

Via System Settings → Network → (your interface) → Details → Proxies:
- Enable **Web Proxy (HTTP)**: `localhost` port `1337`
- Enable **Secure Web Proxy (HTTPS)**: `localhost` port `1337`

Or via the command line:
```bash
# Wi-Fi
networksetup -setwebproxy Wi-Fi localhost 1337
networksetup -setsecurewebproxy Wi-Fi localhost 1337

# To disable
networksetup -setwebproxystate Wi-Fi off
networksetup -setsecurewebproxystate Wi-Fi off
```

### Per-application

Some applications allow configuring a proxy independently of the system settings.

**curl:**
```bash
curl --proxy http://localhost:1337 https://example.com
# or set permanently
export http_proxy=http://localhost:1337
export https_proxy=http://localhost:1337
```

**git:**
```bash
git config --global http.proxy http://localhost:1337
git config --global https.proxy http://localhost:1337
# to remove
git config --global --unset http.proxy
git config --global --unset https.proxy
```

**npm:**
```bash
npm config set proxy http://localhost:1337
npm config set https-proxy http://localhost:1337
```

**Java / Maven** (`~/.m2/settings.xml`):
```xml
<proxies>
    <proxy>
        <active>true</active>
        <protocol>http</protocol>
        <host>localhost</host>
        <port>1337</port>
    </proxy>
</proxies>
```

**IntelliJ IDEA / GoLand:**
Settings → Appearance & Behavior → System Settings → HTTP Proxy → Manual proxy configuration:
- Host: `localhost`, Port: `1337`

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
proxy-router run -config ~/myconf.json -listen localhost:1338
```

## Config

Locations are matched by SSID, IP, and/or domain (OR within each array, AND across arrays). The first matching location wins. If no location matches, `defaults` is used.

`localhost`, `127.0.0.1`, and `::1` are always direct — they cannot be proxied.

```json
{
  "listen": "localhost:1337",
  "proxies": {
    "corp": "http://username:password@corp-proxy:8080"
  },
  "defaults": {
    "proxy": "direct",
    "no_proxy": []
  },
  "locations": {
    "work": {
      "proxy": "corp",
      "domain": "CORP",
      "ssids": ["OfficeWifi", "OfficeWifi-5G"],
      "ips": ["10.0.0.0/8"],
      "dns": ["10.0.0.1", "10.0.0.2"],
      "no_proxy": [".internal.corp.com", "192.168.0.0/24"]
    },
    "co-working": {
      "proxy": "corp",
      "ssids": ["Barista"]
    }
  }
}
```

### Fields

**Top-level:**
- `listen` — address to listen on
- `proxies` — named proxy URL map; referenced by locations and defaults
- `defaults.proxy` — `"direct"` or a key in `proxies`; used when no location matches
- `defaults.no_proxy` — additional destinations that always bypass the proxy

**Location:**
- `proxy` — key in `proxies` or a raw URL (required)
- `domain` — Active Directory domain for NTLM auth
- `ssids` — Wi-Fi SSID list (case-insensitive, OR logic)
- `ips` — IP or CIDR list (OR logic)
- `domains` — hostname suffix list (OR logic); `.corp.com` matches all subdomains
- `dns` — custom DNS servers for this location (does not affect system DNS)
- `no_proxy` — destinations to bypass proxy within this location; supports exact IP, CIDR, domain, `.domain` suffix, `*`

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
git tag v0.2.0
git push origin v0.2.0
```

The CI will build the binary, create a GitHub release, and automatically update the Homebrew formula in `wstucco/homebrew-tap`. Requires a `HOMEBREW_TAP_TOKEN` secret (GitHub PAT with repo write access to the tap).

## Build notes

Requires cgo (`SystemConfiguration.framework`, macOS only):

```bash
xcode-select --install
go build -o proxy-router ./cmd/proxy
```