package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// alwaysNoProxy are destinations that are never proxied, regardless of config.
var alwaysNoProxy = []string{"localhost", "127.0.0.1", "::1"}

// Config is the top-level configuration for proxy-router.
type Config struct {
	Listen    string               `json:"listen"`              // e.g. "localhost:1337"
	Proxies   map[string]string    `json:"proxies,omitempty"`   // named proxy URLs
	Defaults  Defaults             `json:"defaults"`            // default behavior
	Locations map[string]*Location `json:"locations,omitempty"` // named locations
}

// Defaults defines the fallback behavior when no location matches.
type Defaults struct {
	Proxy   string   `json:"proxy,omitempty"`    // "direct" or a key in Proxies
	NoProxy []string `json:"no_proxy,omitempty"` // additional always-direct destinations
}

// Location defines a named network context with matchers and proxy settings.
type Location struct {
	// Proxy config
	Proxy  string `json:"proxy"`            // key in Proxies map or a raw URL
	Domain string `json:"domain,omitempty"` // AD domain for NTLM auth

	// Matchers — OR within each array, AND across arrays
	SSIDs   []string `json:"ssids,omitempty"`   // Wi-Fi SSID (case-insensitive)
	IPs     []string `json:"ips,omitempty"`     // exact IP or CIDR
	Domains []string `json:"domains,omitempty"` // hostname suffix match

	// Options
	DNS     []string `json:"dns,omitempty"`      // custom DNS servers for this location
	NoProxy []string `json:"no_proxy,omitempty"` // destinations to bypass proxy for
}

// Decision is the result of location matching.
type Decision struct {
	ProxyURL string   // resolved upstream proxy URL, "" means direct
	Domain   string   // AD domain for NTLM
	DNS      []string // custom DNS servers, nil means system default
	NoProxy  []string // combined no_proxy list for this decision
}

// Load reads and validates the config file, detecting legacy format automatically.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	// Detect legacy format
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if _, isLegacy := raw["upstream"]; isLegacy {
		return nil, fmt.Errorf("legacy config format detected — please run `proxy-router migrate` or see https://github.com/wstucco/proxy-router/wiki/configuration")
	}
	if _, isLegacy := raw["rules"]; isLegacy {
		return nil, fmt.Errorf("legacy config format detected — please run `proxy-router migrate` or see https://github.com/wstucco/proxy-router/wiki/configuration")
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

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

	return &cfg, nil
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
		if _, ok := c.Proxies[loc.Proxy]; !ok {
			if _, err := url.Parse(loc.Proxy); err != nil {
				return fmt.Errorf("location %q proxy %q is not a known proxy name or valid URL", name, loc.Proxy)
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

// DefaultConfig returns an example config as JSON string.
func DefaultConfig() string {
	cfg := Config{
		Listen: "localhost:1337",
		Proxies: map[string]string{
			"corp": "http://username:password@corp-proxy:8080",
		},
		Defaults: Defaults{
			Proxy:   "direct",
			NoProxy: []string{},
		},
		Locations: map[string]*Location{
			"work": {
				Proxy:   "corp",
				Domain:  "CORP",
				SSIDs:   []string{"OfficeWifi", "OfficeWifi-5G"},
				IPs:     []string{"10.0.0.0/8"},
				DNS:     []string{"10.0.0.1", "10.0.0.2"},
				NoProxy: []string{".internal.corp.com"},
			},
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b)
}
