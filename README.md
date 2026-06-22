# proxy-router

A lightweight local proxy (`localhost:1337`) that forwards connections to an upstream proxy or goes direct, based on configurable locations evaluated per-request. Supports destination rewriting (routes) for both HTTP and HTTPS (via TLS interception).

## Changelog

See [CHANGELOG](CHANGELOG) for the full history.

## Features

- HTTP and HTTPS (`CONNECT`) proxying
- Location-based routing: forward to a named proxy or go direct based on SSID, hostname, or IP
- **Destination rewriting (routes)** — redirect matching `host+path` requests to a different URL, like an API gateway
- **TLS interception (MITM)** — enables path-based routing on HTTPS connections; automatic when routes are defined
- Authenticated upstream proxies with automatic Basic/NTLM/Negotiate negotiation
- **macOS-native Kerberos/Negotiate auth** via GSS.framework (no password needed with valid TGT)
- **Negotiate failure cache** (30s TTL) — avoids hammering KDC/proxy on repeated failures
- **TOML config** — auto-migrated from JSON on first run (supports both legacy `upstream/rules` and current `proxies/locations` formats)
- **`version` field** in config for future schema upgrades
- **Structured logging** with configurable levels (`debug`, `info`, `warn`, `error`) — `[log]` section in config
- **Configurable routes in `[defaults]`** — apply regardless of location; location routes override same-key defaults
- **PAC (Proxy Auto-Config)** — per-location PAC scripts (file:// or http://) evaluated via JS runtime
- **Location hooks** (`on_enter` / `on_leave`) — execute shell commands when the active location changes
- Hot config reload — save the file and changes apply within 1 second (or send `SIGHUP`); content-hash guard avoids spurious reloads
- macOS network change listener via `SCDynamicStore` — SSID cache updated on network events
- Brew service and manual LaunchAgent support

## How it works

proxy-router sits at `localhost:1337` and intercepts all HTTP/HTTPS traffic routed through it. For each connection it evaluates the configured locations and decides whether to forward the connection to a named upstream proxy or connect directly.

Locations match on the current Wi-Fi SSID, destination hostname, or destination IP. This makes it ideal for automatically switching between a corporate proxy at the office and a direct connection at home, without changing any system settings manually.

**Routes** extend locations with destination rewriting: if a request's `host+path` matches a route prefix, the request is redirected to the route's target URL. Routes work on HTTP directly and on HTTPS via automatic TLS interception (MITM). In MITM mode, both routed and non-routed requests still respect the location's upstream proxy.

## Upstream proxy authentication

proxy-router automatically negotiates the correct authentication scheme (Basic, NTLM, Negotiate) by inspecting the upstream proxy's response — no manual configuration needed.

Credentials are specified in the proxy URL inside the `proxies` table:

```toml
[proxies]
corp = "http://username:password@proxy.corp.com:8080"
```

### Kerberos / Negotiate (macOS)

On macOS, proxy-router uses the native GSS.framework for Kerberos/Negotiate authentication — no password is needed when a valid TGT exists in the system credential cache. The SPN is constructed as `HTTP@<proxy-canonical-fqdn>` via forward+reverse DNS lookup, handling short names and CNAMEs commonly used in proxy URLs.

Negotiate is tried first; if it fails (no TGT, wrong realm, or proxy doesn't support it), proxy-router automatically falls back to NTLM/Basic via `proxyplease`. Failed Negotiate attempts are cached for 30s to avoid hammering the KDC or proxy, and the cache is cleared on config reload and on successful auth.

### Active Directory / NTLM

If the proxy requires NTLM authentication on an Active Directory network, set the domain on the location via `domain`:

```toml
[proxies]
corp = "http://username:password@proxyu.corp.it:80"

[locations.work]
proxy = "corp"
domain = "CORP"
ssids = ["OfficeWifi"]
```

Do not encode the domain in the username in the URL — this causes URL parsing failures. Use the `domain` field on the location instead.

The domain is required for NTLM. Without it, Basic auth may work initially but fail after the session expires and the proxy switches to Negotiate.

## Install

### Homebrew (recommended)

```bash
brew tap wstucco/tap
brew install proxy-router
brew services start proxy-router
proxy-router install          # installs shell completions
proxy-router install-certs    # generates CA cert for HTTPS routes (see output for trust instructions)
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
proxy-router run -listen localhost:1337 -config ~/myconf.toml
proxy-router migrate                     Migrate config from legacy format
proxy-router install                     Write config, install completions, register LaunchAgent
proxy-router install-certs               Generate CA certificate for TLS MITM and print trust instructions
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
| `/opt/homebrew/etc/proxy-router/config.toml` | Config |
| `/opt/homebrew/etc/proxy-router/cacert.pem` | CA certificate (TLS MITM) |
| `/opt/homebrew/var/log/proxy-router.log` | Log |
| managed by `brew services` | LaunchAgent |

### Manual install

| Path | Purpose |
|---|---|
| `/usr/local/bin/proxy-router` | Binary |
| `/usr/local/etc/proxy-router/config.toml` | Config |
| `/usr/local/etc/proxy-router/cacert.pem` | CA certificate (TLS MITM) |
| `/usr/local/var/log/proxy-router/proxy-router.log` | Log |
| `/Library/LaunchAgents/com.wstucco.proxy-router.plist` | LaunchAgent |

## Local dev run

Run with a custom config and port without installing anything:

```bash
proxy-router run -config ~/myconf.toml -listen localhost:1338
```

## Config

Locations are matched by SSID, IP, and/or domain (OR within each array, AND across arrays). The first matching location wins. If no location matches, `defaults` is used.

`localhost`, `127.0.0.1`, and `::1` are always direct — they cannot be proxied regardless of config.

```toml
version = "1"
listen = "localhost:1337"

[log]
level = "info"

[proxies]
corp = "http://username:password@corp-proxy:8080"

[defaults]
proxy = "direct"
no_proxy = []

# Routes in [defaults] apply regardless of which location matches.
# Location routes override same-key defaults.
[defaults.routes]
# "httpbin.org" = "https://localhost:4321"

[locations.work]
proxy = "corp"
domain = "CORP"
ssids = ["OfficeWifi", "OfficeWifi-5G"]
ips = ["10.0.0.0/8"]
dns = ["10.0.0.1", "10.0.0.2"]
no_proxy = [".internal.corp.com", "192.168.0.0/24"]

# Routes rewrite matching requests to a different URL
[locations.work.routes]
"repo1.maven.org/maven2/com/company/" = "https://nexus.internal/repo/"

[locations.co-working]
proxy = "corp"
ssids = ["Barista"]
```

### Fields

**Top-level:**
- `version` — schema version (for future migration support); do not change
- `listen` — address to listen on
- `proxies` — named proxy URL map; referenced by locations and defaults
- `log.level` — log verbosity: `"debug"`, `"info"`, `"warn"`, `"error"`
- `defaults.proxy` — `"direct"` or a key in `proxies`; used when no location matches
- `defaults.no_proxy` — additional destinations that always bypass the proxy
- `defaults.routes` — destination rewriting rules applied regardless of location; location routes override same-key defaults

**Location:**
- `proxy` — key in `proxies` or a raw URL; at least one of `proxy` or `pac` required
- `pac` — PAC script URL (`file://` path or `http(s)://` URL); alternative to static proxy
- `domain` — Active Directory domain for NTLM auth
- `ssids` — Wi-Fi SSID list (case-insensitive, OR logic)
- `ips` — IP or CIDR list (OR logic)
- `domains` — hostname suffix list (OR logic); `.corp.com` matches all subdomains
- `dns` — custom DNS servers for this location (does not affect system DNS)
- `no_proxy` — destinations to bypass proxy within this location; supports exact IP, CIDR, domain, `.domain` suffix, `*`
- `routes` — destination rewriting rules (table); key is URL prefix (`host` or `host/path`), value is the target base URL
- `hooks.on_enter` — shell command executed when this location becomes active
- `hooks.on_leave` — shell command executed when this location is no longer active

### Routes

Routes rewrite requests whose `host+path` starts with the route key to the route's target URL. The unmatched path suffix is appended to the target.

```toml
[locations.work.routes]
# httpbin.org/anything → localhost:4321/anything
"httpbin.org" = "https://localhost:4321"

# repo1.maven.org/maven2/com/company/foo/1.0/foo.pom
#   → nexus.internal/repo/foo/1.0/foo.pom
"repo1.maven.org/maven2/com/company/" = "https://nexus.internal/repo/"
```

- **HTTP**: routes are applied inline before forwarding
- **HTTPS**: routes require TLS interception (MITM), triggered per-connection only when the CONNECT host matches a route prefix (non-matching hosts use the normal tunnel). Run `proxy-router install-certs` to generate the CA certificate, then trust it on your machine:
  ```bash
  sudo security add-trusted-cert -d -r trustRoot \
    -k /Library/Keychains/System.keychain /opt/homebrew/etc/proxy-router/cacert.pem
  ```
- Route targets without an explicit scheme default to `https` and port `443`; explicit `http` targets use port `80`. Any other scheme is rejected.
- Requests in MITM mode still respect the location's upstream proxy setting, including routed requests, unless the destination matches `no_proxy`

### PAC (Proxy Auto-Config)

Locations can use a PAC script instead of (or alongside) a static proxy. The script is evaluated per-request via a built-in JavaScript runtime.

```toml
[locations.office]
pac = "/etc/proxy-router/corporate.pac"
ssids = ["OfficeWifi"]
```

Or with a fallback proxy in case the PAC script fails:

```toml
[locations.office]
proxy = "corp"
pac = "http://proxy.company.com/proxy.pac"
ssids = ["OfficeWifi"]
```

Supported helpers: `dnsResolve`, `isInNet`, `isPlainHostName`, `shExpMatch`, `myIpAddress`, `isResolvable`, `dnsDomainIs`, `dnsDomainLevels`, `localHostOrDomainIs`, `weekdayRange`, `dateRange`, `timeRange`.

### Hooks (on_enter / on_leave)

Hooks execute shell commands when the active location changes — e.g. when you switch Wi-Fi networks. This is useful for triggering side effects like connecting or disconnecting a VPN.

```toml
[locations.office]
proxy = "corp"
ssids = ["OfficeWifi"]

[locations.office.hooks]
  [locations.office.hooks.on_enter]
  exec = "/usr/local/bin/disconnect-vpn"

  [locations.office.hooks.on_leave]
  exec = "/usr/local/bin/connect-vpn"
```

Hooks run via `sh -c` (supports multi-line scripts with TOML `"""`), asynchronously with a configurable timeout (default 10s). Failures are logged but never block the proxy.

**Env vars** passed to the hook: `LOCATION`, `ACTION` (enter/leave), `OLD_LOCATION`, `NEW_LOCATION`.

```toml
[locations.office.hooks]
  [locations.office.hooks.on_enter]
  exec = """
    echo "Entered $LOCATION (was $OLD_LOCATION)" >> /tmp/proxy-hooks.log
  """
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
git tag v0.4.0
git push origin v0.4.0
```

The CI will build the binary, create a GitHub release, and automatically update the Homebrew formula in `wstucco/homebrew-tap`. Requires a `HOMEBREW_TAP_TOKEN` secret (GitHub PAT with repo write access to the tap).

## Build notes

Requires cgo (`SystemConfiguration.framework`, macOS only):

```bash
xcode-select --install
go build -o proxy-router ./cmd/proxy
```