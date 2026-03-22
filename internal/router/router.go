package router

import (
	"fmt"
	"net"
	"strings"

	"github.com/wstucco/proxy-router/internal/config"
)

// Decide evaluates locations top-to-bottom and returns a Decision for the given host.
// Defaults are applied first, then location matching.
func Decide(cfg *config.Config, host string) config.Decision {
	ssid := CurrentSSID()

	// Always-no-proxy check — hardcoded, cannot be overridden
	if isAlwaysNoProxy(host) {
		logEntry(host, ssid, "always no-proxy → direct", false)
		return config.Decision{}
	}

	// Find matching location
	var matched *config.Location
	var matchedName string
	for name, loc := range cfg.Locations {
		if matchesLocation(loc, host, ssid) {
			matched = loc
			matchedName = name
			break
		}
	}

	// Build no_proxy list
	var locationNoProxy []string
	if matched != nil {
		locationNoProxy = matched.NoProxy
	}
	noProxy := config.EffectiveNoProxy(cfg.Defaults.NoProxy, locationNoProxy)

	// Check no_proxy before routing
	if config.MatchNoProxy(host, noProxy) {
		if matched != nil {
			logEntry(host, ssid, fmt.Sprintf("location %q matched but host in no_proxy → direct", matchedName), false)
		} else {
			logEntry(host, ssid, "host in no_proxy → direct", false)
		}
		return config.Decision{}
	}

	// No location matched — use defaults
	if matched == nil {
		proxyURL := cfg.ResolveProxyURL(cfg.Defaults.Proxy)
		if proxyURL == "" {
			logEntry(host, ssid, "no location matched → default: direct", false)
		} else {
			logEntry(host, ssid, "no location matched → default proxy", false)
		}
		return config.Decision{
			ProxyURL: proxyURL,
			NoProxy:  noProxy,
		}
	}

	proxyURL := cfg.ResolveProxyURL(matched.Proxy)
	logEntry(host, ssid, fmt.Sprintf("location %q matched → %s", matchedName, matched.Proxy), true)
	return config.Decision{
		ProxyURL: proxyURL,
		Domain:   matched.Domain,
		DNS:      matched.DNS,
		NoProxy:  noProxy,
	}
}

// matchesLocation returns true if the location matches the given host and SSID.
// Matchers are OR within each array, AND across arrays.
func matchesLocation(loc *config.Location, host, ssid string) bool {
	// Each present matcher must match (AND logic across types)
	if len(loc.SSIDs) > 0 && !matchSSID(ssid, loc.SSIDs) {
		return false
	}
	if len(loc.IPs) > 0 && !matchIP(host, loc.IPs) {
		return false
	}
	if len(loc.Domains) > 0 && !config.MatchDomain(host, loc.Domains) {
		return false
	}
	return true
}

func isAlwaysNoProxy(host string) bool {
	h := host
	if idx := strings.LastIndex(h, ":"); idx != -1 {
		h = h[:idx]
	}
	h = strings.ToLower(h)
	for _, v := range []string{"localhost", "127.0.0.1", "::1"} {
		if h == v {
			return true
		}
	}
	return false
}

func matchSSID(current string, list []string) bool {
	current = strings.ToLower(strings.TrimSpace(current))
	for _, s := range list {
		if s == "*" || strings.ToLower(strings.TrimSpace(s)) == current {
			return true
		}
	}
	return false
}

func matchIP(host string, list []string) bool {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	ip := net.ParseIP(host)
	for _, entry := range list {
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err == nil && ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}
