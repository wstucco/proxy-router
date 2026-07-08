package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/wstucco/proxy-router/internal/hooks"
	logger "github.com/wstucco/proxy-router/internal/log"
)

var pkgLog = logger.New(logger.LevelDebug, "migrate")

// alwaysNoProxy are destinations that are never proxied, regardless of config.
var alwaysNoProxy = []string{"localhost", "127.0.0.1", "::1"}

const CurrentVersion = "1"

// Config is the top-level configuration for proxy-router.
type Config struct {
	Version   string               `toml:"version,omitempty"   json:"version,omitempty"`
	Listen    string               `toml:"listen"              json:"listen"`
	Proxies   map[string]string    `toml:"proxies,omitempty"   json:"proxies,omitempty"`
	PACs      map[string]string    `toml:"pacs,omitempty"      json:"pacs,omitempty"`
	Defaults  Defaults             `toml:"defaults"            json:"defaults"`
	Locations map[string]*Location `toml:"locations,omitempty" json:"locations,omitempty"`
	Log       LogConfig            `toml:"log"                 json:"log"`
}

// LogConfig controls logging behavior.
type LogConfig struct {
	Level       string `toml:"level,omitempty"        json:"level,omitempty"`
	SilenceLibs *bool  `toml:"silence_libs,omitempty" json:"silence_libs,omitempty"`
}

// Defaults defines the fallback behavior when no location matches.
type Defaults struct {
	Proxy   string            `toml:"proxy,omitempty"    json:"proxy,omitempty"`
	NoProxy []string          `toml:"no_proxy,omitempty" json:"no_proxy,omitempty"`
	Routes  map[string]string `toml:"routes,omitempty"   json:"routes,omitempty"`
	PAC     string            `toml:"pac,omitempty"      json:"pac,omitempty"`
}

// Location defines a named network context with matchers and proxy settings.
type Location struct {
	// Proxy config — at least one of Proxy or PAC must be set
	Proxy  string `toml:"proxy,omitempty"  json:"proxy,omitempty"`
	PAC    string `toml:"pac,omitempty"    json:"pac,omitempty"`
	Domain string `toml:"domain,omitempty" json:"domain,omitempty"`

	// Matchers — OR within each array, AND across arrays
	SSIDs   []string `toml:"ssids,omitempty"   json:"ssids,omitempty"`
	IPs     []string `toml:"ips,omitempty"     json:"ips,omitempty"`
	Domains []string `toml:"domains,omitempty" json:"domains,omitempty"`

	// Options
	DNS     []string             `toml:"dns,omitempty"      json:"dns,omitempty"`
	NoProxy []string             `toml:"no_proxy,omitempty" json:"no_proxy,omitempty"`
	Routes  map[string]string    `toml:"routes,omitempty"   json:"routes,omitempty"`
	Hooks   *hooks.LocationHooks `toml:"hooks,omitempty"    json:"hooks,omitempty"`
}

// Decision is the result of location matching.
type Decision struct {
	ProxyURL     string   // resolved upstream proxy URL, "" means direct
	ProxyName    string   // proxy name from [proxies], for logging
	Domain       string   // AD domain for NTLM
	DNS          []string // custom DNS servers, nil means system default
	NoProxy      []string // combined no_proxy list for this decision
	Routes       map[string]string // routes from matched location, nil if none
	PAC          string   // PAC script URL, evaluated per-request in proxy handler
	PACName      string   // PAC name from [pacs], for logging
	LocationName string   // matched location name, empty if no location matched
}

// RouteLoc returns the location label: the matched location name, or
// "default" when no location matched (the [defaults] section applies).
func (d *Decision) RouteLoc() string {
	if d.LocationName != "" {
		return d.LocationName
	}
	return "default"
}

// RouteDest returns the routing destination: "proxy:<name>", "pac:<name>",
// or "direct". Falls back to the redacted URL when no name is set —
// credentials must never reach the logs.
func (d *Decision) RouteDest() string {
	switch {
	case d.PAC != "":
		if d.PACName != "" {
			return "pac:" + d.PACName
		}
		return "pac:" + logger.RedactURL(d.PAC)
	case d.ProxyURL != "":
		if d.ProxyName != "" {
			return "proxy:" + d.ProxyName
		}
		return "proxy:" + logger.RedactURL(d.ProxyURL)
	default:
		return "direct"
	}
}

// RouteString returns a compact routing summary: "loc dest", collapsed to
// just "dest" when no location matched.
func (d *Decision) RouteString() string {
	loc := d.RouteLoc()
	dest := d.RouteDest()
	if loc == "default" && dest == "direct" {
		return "direct"
	}
	if loc == "default" {
		return dest
	}
	return loc + " " + dest
}

// Load reads and validates the config file.
//
// Detection order:
//  1. Path ends in .toml and file exists → parse as TOML
//  2. Path ends in .toml but file missing → look for <same-base>.json, auto-migrate it to .toml
//  3. Path ends in .json → auto-migrate to .toml (supports both legacy and new JSON formats)
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)

	if os.IsNotExist(err) && strings.HasSuffix(path, ".toml") {
		// config.toml missing: try config.json in same directory
		jsonPath := strings.TrimSuffix(path, ".toml") + ".json"
		return migrateFromJSONFile(jsonPath, path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if strings.HasSuffix(path, ".toml") {
		return parseTOML(data)
	}

	// JSON file passed explicitly
	tomlPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".toml"
	return migrateFromJSONData(path, tomlPath, data)
}

func parseTOML(data []byte) (*Config, error) {
	var cfg Config
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return finalize(&cfg)
}

func migrateFromJSONFile(jsonPath, tomlPath string) (*Config, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return migrateFromJSONData(jsonPath, tomlPath, data)
}

func migrateFromJSONData(jsonPath, tomlPath string, data []byte) (*Config, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	_, hasUpstream := raw["upstream"]
	_, hasRules := raw["rules"]
	if hasUpstream || hasRules {
		return MigrateIfLegacy(jsonPath, tomlPath, data)
	}

	// New JSON format — unmarshal directly
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if _, err := finalize(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Archive the original JSON so it can't re-trigger a migration later;
	// fall back to a copy if the rename fails.
	backupPath := jsonPath + ".bak"
	if err := os.Rename(jsonPath, backupPath); err != nil {
		if werr := os.WriteFile(backupPath, data, 0644); werr != nil {
			pkgLog.Warn("could not write backup to %s: %v", backupPath, werr)
		}
	}
	pkgLog.Info("legacy config archived → %s", backupPath)
	if err := writeTOML(tomlPath, &cfg); err != nil {
		return nil, fmt.Errorf("writing migrated config: %w", err)
	}
	pkgLog.Info("config migrated from JSON to TOML → %s", tomlPath)
	return &cfg, nil
}

func finalize(cfg *Config) (*Config, error) {
	if cfg.Version == "" {
		cfg.Version = CurrentVersion
	}
	if cfg.Listen == "" {
		cfg.Listen = "localhost:1337"
	}
	if cfg.Proxies == nil {
		cfg.Proxies = map[string]string{}
	}
	if cfg.PACs == nil {
		cfg.PACs = map[string]string{}
	}
	if cfg.Locations == nil {
		cfg.Locations = map[string]*Location{}
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// writeTOML encodes cfg as TOML and writes it to path.
func writeTOML(path string, cfg *Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// validate checks the config for errors.
func (c *Config) validate() error {
	// Validate proxy names in proxies map
	for name, rawURL := range c.Proxies {
		if _, err := url.Parse(rawURL); err != nil {
			return fmt.Errorf("proxy %q has invalid URL: %w", name, err)
		}
	}

	// Validate PAC names in pacs map
	for name, rawURL := range c.PACs {
		if _, err := url.Parse(rawURL); err != nil {
			return fmt.Errorf("pac %q has invalid URL: %w", name, err)
		}
	}

	// Validate defaults — exactly one of proxy or pac
	if err := validateProxyOrPAC("defaults", c.Defaults.Proxy, c.Defaults.PAC, c); err != nil {
		return err
	}

	// Validate locations
	for name, loc := range c.Locations {
		if len(loc.SSIDs) == 0 && len(loc.IPs) == 0 && len(loc.Domains) == 0 {
			return fmt.Errorf("location %q has no matchers (ssids, ips, or domains required)", name)
		}
		if err := validateProxyOrPAC(name, loc.Proxy, loc.PAC, c); err != nil {
			return fmt.Errorf("location %q: %w", name, err)
		}
	}

	return nil
}

// validateProxyOrPAC checks that exactly one of proxy or pac is set,
// and that the value is valid (known name, raw URL, or "direct").
func validateProxyOrPAC(name, proxy, pac string, cfg *Config) error {
	hasProxy := proxy != ""
	hasPAC := pac != ""

	if !hasProxy && !hasPAC {
		return fmt.Errorf("must have a proxy or pac field")
	}
	if hasProxy && hasPAC {
		return fmt.Errorf("cannot have both proxy and pac, choose one")
	}

	if proxy != "" && proxy != "direct" {
		if _, ok := cfg.Proxies[proxy]; !ok {
			if _, err := url.Parse(proxy); err != nil {
				return fmt.Errorf("proxy %q is not 'direct', a known proxy name, or a valid URL", proxy)
			}
		}
	}

	if pac != "" && pac != "direct" {
		if _, ok := cfg.PACs[pac]; !ok {
			if _, err := url.Parse(pac); err != nil {
				return fmt.Errorf("pac %q is not 'direct', a known pac name, or a valid URL/path", pac)
			}
		}
	}

	return nil
}

// ResolveProxyURL returns the actual proxy URL for a proxy name or raw URL.
// Returns "" for "direct" or empty string.
func (c *Config) ResolveProxyURL(proxy string) string {
	if proxy == "" || proxy == "direct" {
		return ""
	}
	if u, ok := c.Proxies[proxy]; ok {
		return u
	}
	return proxy // treat as raw URL
}

// ResolvePACURL returns the actual PAC URL/path for a PAC name or raw value.
// Returns "" for "direct" or empty string.
func (c *Config) ResolvePACURL(pac string) string {
	if pac == "" || pac == "direct" {
		return ""
	}
	if u, ok := c.PACs[pac]; ok {
		return u
	}
	return pac // treat as raw URL/path
}

// EffectiveNoProxy returns the combined no_proxy list for a decision:
// always-no-proxy + defaults.no_proxy + location no_proxy.
func EffectiveNoProxy(defaultNoProxy, locationNoProxy []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, v := range alwaysNoProxy {
		seen[v] = true
		result = append(result, v)
	}
	for _, v := range defaultNoProxy {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	for _, v := range locationNoProxy {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result
}

// MatchNoProxy returns true if host should bypass the proxy.
func MatchNoProxy(host string, noProxy []string) bool {
	// strip port
	h := host
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		h = hostOnly
	}
	h = strings.ToLower(h)

	for _, entry := range noProxy {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "*" {
			return true
		}
		// CIDR match
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err == nil {
				if ip := net.ParseIP(h); ip != nil && cidr.Contains(ip) {
					return true
				}
			}
			continue
		}
		// Leading dot = subdomain match
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(h, entry) || h == entry[1:] {
				return true
			}
			continue
		}
		// Exact match (IP or hostname)
		if h == entry {
			return true
		}
	}
	return false
}

// MatchDomain returns true if host matches any of the domain suffixes.
func MatchDomain(host string, domains []string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	for _, d := range domains {
		d = strings.ToLower(d)
		if d == "*" {
			return true
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// ConfigDiff returns a human-readable summary of changes between two configs.
// Returns "(no changes)" if they are identical at the compared fields.
func ConfigDiff(old, new *Config) string {
	if old == nil || new == nil {
		return ""
	}
	var b strings.Builder

	if old.Listen != new.Listen {
		fmt.Fprintf(&b, "\n  listen: %q → %q", old.Listen, new.Listen)
	}

	// proxies
	for k, v := range new.Proxies {
		if oldV, ok := old.Proxies[k]; !ok {
			fmt.Fprintf(&b, "\n  [proxies] +%s = %s", k, logger.RedactURL(v))
		} else if oldV != v {
			fmt.Fprintf(&b, "\n  [proxies] ~%s: %s → %s", k, logger.RedactURL(oldV), logger.RedactURL(v))
		}
	}
	for k := range old.Proxies {
		if _, ok := new.Proxies[k]; !ok {
			fmt.Fprintf(&b, "\n  [proxies] -%s", k)
		}
	}

	// pacs
	for k := range new.PACs {
		if _, ok := old.PACs[k]; !ok {
			fmt.Fprintf(&b, "\n  [pacs] +%s", k)
		}
	}
	for k := range old.PACs {
		if _, ok := new.PACs[k]; !ok {
			fmt.Fprintf(&b, "\n  [pacs] -%s", k)
		}
	}

	// defaults
	if old.Defaults.Proxy != new.Defaults.Proxy {
		fmt.Fprintf(&b, "\n  defaults.proxy: %q → %q", old.Defaults.Proxy, new.Defaults.Proxy)
	}
	if old.Defaults.PAC != new.Defaults.PAC {
		fmt.Fprintf(&b, "\n  defaults.pac: %q → %q", old.Defaults.PAC, new.Defaults.PAC)
	}

	// locations
	for k, newLoc := range new.Locations {
		oldLoc, exists := old.Locations[k]
		if !exists {
			fmt.Fprintf(&b, "\n  [locations] +%s", k)
			continue
		}
		if d := locationDiff(k, oldLoc, newLoc); d != "" {
			b.WriteString(d)
		}
	}
	for k := range old.Locations {
		if _, ok := new.Locations[k]; !ok {
			fmt.Fprintf(&b, "\n  [locations] -%s", k)
		}
	}

	// log
	if old.Log.Level != new.Log.Level {
		fmt.Fprintf(&b, "\n  log.level: %q → %q", old.Log.Level, new.Log.Level)
	}
	if silenceLibsDefault(old.Log.SilenceLibs) != silenceLibsDefault(new.Log.SilenceLibs) {
		fmt.Fprintf(&b, "\n  log.silence_libs: %v → %v", silenceLibsDefault(old.Log.SilenceLibs), silenceLibsDefault(new.Log.SilenceLibs))
	}

	if b.Len() == 0 {
		return " (no changes)"
	}
	return b.String()
}

// locationDiff returns a diff string for two versions of the same location,
// or "" if they are identical.
func locationDiff(name string, old, new *Location) string {
	var b strings.Builder
	prefix := "\n  [locations." + name + "]"

	if old.Proxy != new.Proxy {
		fmt.Fprintf(&b, "%s proxy: %q → %q", prefix, old.Proxy, new.Proxy)
	}
	if old.PAC != new.PAC {
		fmt.Fprintf(&b, "%s pac: %q → %q", prefix, old.PAC, new.PAC)
	}
	if old.Domain != new.Domain {
		fmt.Fprintf(&b, "%s domain: %q → %q", prefix, old.Domain, new.Domain)
	}
	if !stringSliceEqual(old.SSIDs, new.SSIDs) {
		fmt.Fprintf(&b, "%s ssids: %v → %v", prefix, old.SSIDs, new.SSIDs)
	}
	if !stringSliceEqual(old.IPs, new.IPs) {
		fmt.Fprintf(&b, "%s ips: %v → %v", prefix, old.IPs, new.IPs)
	}
	if !stringSliceEqual(old.Domains, new.Domains) {
		fmt.Fprintf(&b, "%s domains: %v → %v", prefix, old.Domains, new.Domains)
	}
	if !stringSliceEqual(old.DNS, new.DNS) {
		fmt.Fprintf(&b, "%s dns: %v → %v", prefix, old.DNS, new.DNS)
	}
	if !stringSliceEqual(old.NoProxy, new.NoProxy) {
		fmt.Fprintf(&b, "%s no_proxy: %v → %v", prefix, old.NoProxy, new.NoProxy)
	}
	if !routesEqual(old.Routes, new.Routes) {
		fmt.Fprintf(&b, "%s routes: %v → %v", prefix, old.Routes, new.Routes)
	}
	if !hooksEqual(old.Hooks, new.Hooks) {
		fmt.Fprintf(&b, "%s hooks: changed", prefix)
	}

	return b.String()
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func routesEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func hooksEqual(a, b *hooks.LocationHooks) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return hookConfigEqual(a.OnEnter, b.OnEnter) && hookConfigEqual(a.OnLeave, b.OnLeave)
	}
}

func hookConfigEqual(a, b *hooks.HookConfig) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Exec == b.Exec && a.Timeout == b.Timeout
	}
}

// silenceLibsDefault returns true if the SilenceLibs field effectively means
// "silence libraries" — nil (unset) or *true both mean silence.
func silenceLibsDefault(p *bool) bool {
	return p == nil || *p
}

// DefaultConfig returns an example config as a TOML string.
func DefaultConfig() string {
	return `version = "1"
listen = "localhost:1337"

[log]
level = "info"
# silence_libs = false  # set to true to suppress library log output (proxyplease)

# Named upstream proxies
[proxies]
corp = "http://username:password@corp-proxy:8080"

# Named PAC scripts
# pacs = { corporate = "/etc/proxy-router/corporate.pac" }
# Reference by name in a location: pac = "corporate"

# Default behavior when no location matches
[defaults]
proxy = "direct"
# or use a PAC instead:
# pac = "corporate"
no_proxy = []

# Routes in defaults apply regardless of which location matches.
# They can be overridden per-location by defining the same key.
[defaults.routes]
# "httpbin.org" = "https://localhost:4321"

# Locations — first match wins
[locations.work]
proxy = "corp"
domain = "CORP"
ssids = ["OfficeWifi", "OfficeWifi-5G"]
ips = ["10.0.0.0/8"]
dns = ["10.0.0.1", "10.0.0.2"]
no_proxy = [".internal.corp.com"]

# Locations can use a PAC script instead of a static proxy.
# [locations.pac-routed]
# pac = "corporate"
# ssids = ["OfficeWifi"]
`
}
