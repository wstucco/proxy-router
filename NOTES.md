# proxy-router — Development Notes

## Current state

Working branch: `feat/v0.3.0`

The new config schema (`locations`/`proxies`/`defaults`) is fully implemented and compiling. Auto-migration from legacy format is in place. Not yet released.

---

## Next immediate task

**Migrate config format from JSON to TOML** before releasing v0.3.0.

- Add `github.com/BurntSushi/toml` dependency
- Rewrite `internal/config/config.go` to use TOML instead of `encoding/json`
- Update `internal/config/migrate.go` to output TOML instead of JSON
- Migration path on startup:
  1. Detect legacy JSON (`upstream`/`rules` keys) → migrate to new TOML, write `config.json.bak`
  2. Detect new-format JSON (`locations`/`proxies` keys) → convert to TOML silently, write `config.json.bak`
  3. TOML → just load it
- Update `DefaultConfig()` to output TOML
- Update `config.json` example → `config.toml`
- File extension: `config.toml`

Example of what the TOML config should look like:

```toml
listen = "localhost:1337"

# Named upstream proxies
[proxies]
corp = "http://username:password@corp-proxy:8080"

# Default behavior when no location matches
[defaults]
proxy = "direct"
no_proxy = []

# Locations — first match wins
[locations.work]
proxy = "corp"
domain = "CORP"
ssids = ["OfficeWifi", "OfficeWifi-5G"]
ips = ["10.0.0.0/8"]
dns = ["10.0.0.1", "10.0.0.2"]
no_proxy = [".internal.corp.com"]

[locations.home]
proxy = "direct"
ssids = ["HomeWifi"]

# Routes: src => dst (host or host/path prefix)
[locations.home.routes]
"nexus.corp.com/repository/maven-public/" = "repo1.maven.org/maven2/"
"nexus.corp.com/repository/maven-snapshots/" = "oss.sonatype.org/content/repositories/snapshots/"
"nexus.corp.com" = "repo1.maven.org"
```

---

## Config schema (finalized)

```toml
listen = "localhost:1337"          # default port is 1337, never change this

[proxies]
name = "http://user:pass@host:port"  # named proxy URLs

[defaults]
proxy = "direct"                   # "direct" or a key in [proxies]
no_proxy = []                      # additional always-direct destinations
                                   # localhost, 127.0.0.1, ::1 are ALWAYS direct (hardcoded)

[locations.name]
proxy = "name"                     # key in [proxies] or raw URL (required)
domain = "CORP"                    # AD domain for NTLM auth
ssids = []                         # Wi-Fi SSIDs (OR logic, case-insensitive)
ips = []                           # IPs or CIDRs (OR logic)
domains = []                       # hostname suffixes (OR logic)
dns = []                           # custom DNS servers (does not affect system DNS)
no_proxy = []                      # bypass proxy for these destinations
                                   # supports: exact IP, CIDR, domain, .domain suffix, *

[locations.name.routes]            # host or host/path prefix rewrites (map[string]string)
"src.host/path/" = "dst.host/path/"
```

**Matching logic:**
- OR within each matcher array
- AND across matcher types (ssids AND ips AND domains must all match if specified)
- A location with no matchers → config error on startup
- `defaults` processed before location matching
- `localhost`, `127.0.0.1`, `::1` → always direct, cannot be overridden

**Proxy resolution:**
- `proxy = "direct"` or empty → direct connection
- `proxy = "name"` → looked up in `[proxies]` map
- `proxy = "http://..."` → used as raw URL

**Routes:**
- Key = src host or host/path prefix
- Value = dst host or host/path prefix
- HTTP: full URL rewriting (host + path)
- HTTPS: host-only rewriting (path invisible without TLS termination)
- Only active when the location is matched

---

## Architecture

```
cmd/proxy/main.go           CLI: run, install, uninstall, migrate, completion, version, help
internal/config/config.go   Schema, validation, Load()
internal/config/migrate.go  Auto-migration from legacy JSON and new JSON → TOML
internal/router/router.go   Location matching, Decide() → Decision
internal/router/log.go      Deduplicating syslog-style logger
internal/router/ssid.go     Wi-Fi SSID detection via ipconfig getsummary
internal/router/network_listener.go  macOS SCDynamicStore network change listener (cgo)
internal/proxy/proxy.go     HTTP/HTTPS proxy, upstream dialing via proxyplease
```

**Key dependencies:**
- `github.com/aus/proxyplease v0.1.0` — transparent Basic/NTLM/Negotiate upstream auth
- `github.com/BurntSushi/toml` — to be added for TOML support

---

## Decisions log

- **Default port: 1337** (never use 32000)
- **TOML config** — chosen over JSON (comments) and YAML (indentation fragility)
- **Named proxies map** — define once, reference by name from locations
- **`localhost`/`127.0.0.1`/`::1`** — hardcoded always-direct, cannot be proxied
- **`domain` field on location** — AD domain for NTLM, not encoded in URL username
- **`proxyplease`** — handles Basic/NTLM/Negotiate negotiation transparently
- **Routes as `map[string]string`** — `src => dst`, cleaner than array of objects
- **HTTPS routes = host-only** — path rewriting requires TLS MITM (future feature)
- **TLS MITM reference**: `github.com/MMNetworks/myproxy` — good reference implementation
- **Linux support** — planned for v1.0, split platform-specific files with build tags
- **CHANGELOG** — no extension, Elixir-style format, entries grouped by component in [brackets]

---

## Planned features (not yet started)

### v0.3.0 (current)
- [x] New config schema (locations/proxies/defaults)
- [x] Auto-migration from legacy format
- [x] `migrate` CLI command
- [ ] TOML config format
- [ ] Routes (host/path rewriting per location)
- [ ] CLI commands: `proxy add/update/remove`, `location add/update/remove`

### Future
- TLS MITM for full path rewriting on HTTPS (requires local CA, cert generation)
- Linux support (build tags, systemd, nmcli/iwgetid for SSID)
- `enable`/`disable` commands for system proxy management
- `status` command
- Web UI for config management
- Multiple upstreams per location
- Gateway MAC / default gateway as location matchers
- Kerberos support (blocked: proxyplease only supports it on Windows)

---

## CI / Release

- GitHub Actions on `macos-15` (macos-14 has SDK bug with AuthorizationRef)
- Release triggered by tag push: `git tag vX.Y.Z && git push origin vX.Y.Z`
- Auto-updates `wstucco/homebrew-tap` Formula via `HOMEBREW_TAP_TOKEN` secret
- Changelog section extracted from `CHANGELOG` file for release body

## Repo

- GitHub: `github.com/wstucco/proxy-router`
- Homebrew tap: `github.com/wstucco/homebrew-tap`
- Module: `github.com/wstucco/proxy-router`

---

## Kerberos / Negotiate auth — solving the 90-day password change problem

### Problem

Corporate proxies use Negotiate (Kerberos) or NTLM. Credentials are static (stored in config). When the domain password expires (typically every 90 days), the proxy stops working until the config is updated and the process restarted.

Alpaca (`github.com/samuong/alpaca`) also has this problem — it's NTLM-only and caches the MD4 password hash at startup.

### Current state

`proxyplease` (v0.1.0) handles auth via the upstream CONNECT handshake:

- **NTLM**: cross-platform (via `git-lfs/go-ntlm`), but uses static username/password
- **Negotiate/Kerberos**: Windows-only via SSPI (`alexbrainman/sspi`). On macOS/Linux it returns "only available on Windows"
- **Basic**: cross-platform, but static credentials
- **Per-connection**: each CONNECT is a fresh auth handshake, no token/session caching

The proxy-router integration creates a fresh `proxyplease` dialer per request in `dialViaUpstream()` (`internal/proxy/proxy.go:300-327`). Credentials come from URL parsing in the config — fully static.

### Solution: use the OS credential cache instead of static passwords

Instead of storing passwords, authenticate using the **current OS user's already-established credentials**. The OS manages ticket renewal, password changes, etc. The proxy just needs to get a service ticket for `HTTP/<proxy-host>` from the system cache.

### Option A: Pure Go (gokrb5)

`github.com/jcmturner/gokrb5` — full Kerberos implementation in Go.

| Pro | Con |
|-----|-----|
| Cross-platform (same code everywhere) | Heavy dependency (~50+ files) |
| No cgo required | Need to read/parse the system credential cache file |
| Can read MIT/Heimdal credential caches | Must implement SPNEGO token wrapping (not in gokrb5) |
| Can also use keytab | Cache file path differs per OS (`/tmp/krb5cc_*` vs Windows) |
| | KDC configuration must be read from `/etc/krb5.conf` or Windows registry |
| | On Windows, may conflict with built-in SSPI |

**SPNEGO complexity**: Kerberos tickets are wrapped in SPNEGO tokens (`RFC 4178`). A `Proxy-Authorization: Negotiate <base64>` header contains a SPNEGO wrapper with the Kerberos AP-REQ inside. Same for the server's response. This needs:
- `GSSAPI_Wrap`-like encoding of the Kerberos AP-REQ into a SPNEGO token (ASN.1 DER)
- Parsing the server's SPNEGO response (which may contain a challenge or an error)

The ASN.1 libraries in Go's stdlib can handle this, but it's fiddly.

### Option B: macOS GSS.framework (cgo)

Built-in macOS framework, same approach as the existing `network_listener.go` (uses `Security.framework`).

| Pro | Con |
|-----|-----|
| Uses system credential cache automatically | macOS only |
| Handles TGT renewal transparently | Requires cgo |
| GSSAPI is standard, well-documented | Heimdal implementation (not MIT) |
| No external deps | Different API from Linux (MIT krb5) |
| SPNEGO is part of GSSAPI (`GSS_C_NO_OID` with `GSS_KRB5_NEGOID`) | Must compile on macOS CI |

**API flow**:
1. `gss_acquire_cred` — get credentials from system cache (GSS_C_NO_CREDENTIAL = use default)
2. `gss_init_sec_context` — create security context, get Kerberos ticket for `HTTP@proxy-host`
3. `gss_wrap` or direct SPNEGO encoding of the output token
4. Base64 encode and send as `Proxy-Authorization: Negotiate <token>`
5. On 407 with challenge: feed challenge back to `gss_init_sec_context` for mutual auth
6. `gss_release_cred`, `gss_delete_sec_context` cleanup

### Option C: Windows SSPI (existing in proxyplease)

Already works via `alexbrainman/sspi`. Uses the Windows LSA credential cache automatically when `AcquireCurrentUserCredentials()` is called (no explicit username/password). The OS manages TGT renewal transparently.

Only issue: proxyplease doesn't expose the "current user" path without building from source.

### Option D: Linux — use GSSAPI via cgo or `krb5-config`

| Approach | Pros | Cons |
|----------|------|------|
| **cgo + `krb5.h` / `gssapi.h`** | Native, well-tested, system-managed cache | Linux-only, cgo, needs `libkrb5-dev` at build time |
| **gokrb5** (pure Go) | Cross-platform, no deps | Must handle credential cache parsing + SPNEGO |

### Cross-platform recommendation

```
Layer           macOS              Windows             Linux
─────────────────────────────────────────────────────────────
Auth source     GSS.framework      SSPI (secur32.dll)  gokrb5 / libgssapi
                (cgo)              (via proxyplease)   (pure Go or cgo)
```

**For a single codebase targeting all three:**

**Recommended: gokrb5 + manual SPNEGO** — the only approach that works uniformly across all platforms without cgo. The trade-off is the heavy dependency and the need to reimplement SPNEGO wrapping (Go's `encoding/asn1` can handle the DER encoding).

**Hybrid approach** (more work, better UX):
- macOS: GSS.framework (cgo) — system-managed, best integration
- Windows: SSPI via proxyplease — already works, just wire it up
- Linux: gokrb5 (pure Go) — no cgo needed, reads MIT krb5 ccache

This gives the best native experience on each platform at the cost of maintaining three code paths (with build tags, like the existing SSID detection pattern).

### Implementation plan for v0.4.0 (target: macOS first)

1. **Replace proxyplease for Negotiate auth** — write a new `negotiate_darwin.go` using GSS.framework (cgo)
2. **Keep proxyplease for NTLM/Basic** — fall back if Negotiate fails
3. **Add `Negotiate` to the 407 scheme dispatch** — in the proxy handler, when upstream returns 407 with `Negotiate`, use the system Kerberos cache instead of proxyplease's static-creds path
4. **Add test** — verify against a test KDC or mock 407 handler

### Estimated effort

- **GSS.framework bindings**: ~150 lines, moderate (cgo + GSSAPI is well documented)
- **Integration into proxy handler**: ~50 lines
- **gokrb5 path** (if chosen instead): ~300 lines, lighter on cgo but heavier on ASN.1
- **proxyplease fork/replacement**: optional — can call GSSAPI alongside proxyplease

Blockers:
- macOS CI must have GSS.framework (it does, it's part of the OS)
- No way to test against a real KDC in CI without setting one up
