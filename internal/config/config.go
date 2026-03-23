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
)

// alwaysNoProxy are destinations that are never proxied, regardless of config.
var alwaysNoProxy = []string{"localhost", "127.0.0.1", "::1"}

// Config is the top-level configuration for proxy-router.
type Config struct {
	Listen    string               `toml:"listen"              json:"listen"`
	Proxies   map[string]string    `toml:"proxies,omitempty"   json:"proxies,omitempty"`
	Defaults  Defaults             `toml:"defaults"            json:"defaults"`
	Locations map[string]*Location `toml:"locations,omitempty" json:"locations,omitempty"`
}

// Defaults defines the fallback behavior when no location matches.
type Defaults struct {
	Proxy   string   `toml:"proxy,omitempty"    json:"proxy,omitempty"`
	NoProxy []string `toml:"no_proxy,omitempty" json:"no_proxy,omitempty"`
}

// Location defines a named network context with matchers and proxy settings.
type Location struct {
	// Proxy config
	Proxy  string `toml:"proxy"            json:"proxy"`
	Domain string `toml:"domain,omitempty" json:"domain,omitempty"`

	// Matchers — OR within each array, AND across arrays
	SSIDs   []string `toml:"ssids,omitempty"   json:"ssids,omitempty"`
	IPs     []string `toml:"ips,omitempty"     json:"ips,omitempty"`
	Domains []string `toml:"domains,omitempty" json:"domains,omitempty"`

	// Options
	DNS     []string          `toml:"dns,omitempty"      json:"dns,omitempty"`
	NoProxy []string          `toml:"no_proxy,omitempty" json:"no_proxy,omitempty"`
	Routes  map[string]string `toml:"routes,omitempty"   json:"routes,omitempty"`
}

// Decision is the result of location matching.
type Decision struct {
	ProxyURL string   // resolved upstream proxy URL, "" means direct
	Domain   string   // AD domain for NTLM
	DNS      []string // custom DNS servers, nil means system default
	NoProxy  []string // combined no_proxy list for this decision
}

// Load reads and validates the config file.
//
// Detection order:
//  1. Path ends in .toml and file exists → parse as TOML
//  2. Path ends in .toml but file missing → look for <same-base>.json, migrate it
//  3. Path ends in .json → parse as JSON, detect legacy vs new format, convert to .toml
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
	// Only legacy JSON format (upstream/rules fields) is supported for migration
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	_, hasUpstream := raw["upstream"]
	_, hasRules := raw["rules"]
	if !hasUpstream && !hasRules {
		return nil, fmt.Errorf("unsupported JSON config format — migrate to TOML (see https://github.com/wstucco/proxy-router/wiki/configuration)")
	}
	return MigrateIfLegacy(jsonPath, tomlPath, data)
}

func finalize(cfg *Config) (*Config, error) {
	if cfg.Listen == "" {
		cfg.Listen = "localhost:1337"
	}
	if cfg.Proxies == nil {
		cfg.Proxies = map[string]string{}
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

	// Validate defaults.proxy
	if p := c.Defaults.Proxy; p != "" && p != "direct" {
		if _, ok := c.Proxies[p]; !ok {
			if _, err := url.Parse(p); err != nil {
				return fmt.Errorf("defaults.proxy %q is not 'direct', a known proxy name, or a valid URL", p)
			}
		}
	}

	// Validate locations
	for name, loc := range c.Locations {
		if len(loc.SSIDs) == 0 && len(loc.IPs) == 0 && len(loc.Domains) == 0 {
			return fmt.Errorf("location %q has no matchers (ssids, ips, or domains required)", name)
		}
		if loc.Proxy == "" {
			return fmt.Errorf("location %q is missing proxy field", name)
		}
		if loc.Proxy != "direct" {
			if _, ok := c.Proxies[loc.Proxy]; !ok {
				if _, err := url.Parse(loc.Proxy); err != nil {
					return fmt.Errorf("location %q proxy %q is not a known proxy name or valid URL", name, loc.Proxy)
				}
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
	if idx := strings.LastIndex(h, ":"); idx != -1 {
		h = h[:idx]
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
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
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

// DefaultConfig returns an example config as a TOML string.
func DefaultConfig() string {
	return `listen = "localhost:1337"

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
`
}
