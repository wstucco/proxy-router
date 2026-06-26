package router

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/wstucco/proxy-router/internal/config"
)

var (
	activeLocationMu     sync.Mutex
	activeLocationName   string
	activeLocationConfig *config.Location
)

// OnLocationChange is set by main to execute hooks when the active location changes.
var OnLocationChange func(oldName, newName string, oldLoc, newLoc *config.Location)

// Decide evaluates locations top-to-bottom and returns a Decision for the given host.
// Routes from the matched location are carried in the Decision for per-request
// destination rewriting in the proxy handler.
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
		pacURL := cfg.Defaults.PAC
		proxyURL := cfg.ResolveProxyURL(cfg.Defaults.Proxy)
		if proxyURL == "" && pacURL == "" {
			logEntry(host, ssid, "no location matched → default: direct", false)
		} else if proxyURL != "" {
			logEntry(host, ssid, "no location matched → default proxy", false)
		} else {
			logEntry(host, ssid, "no location matched → default PAC", false)
		}

		fireLocationChange("", nil)

		return config.Decision{
			ProxyURL: proxyURL,
			NoProxy:  noProxy,
			Routes:   cfg.Defaults.Routes,
			PAC:      pacURL,
		}
	}

	proxyURL := ""
	if matched.Proxy != "" {
		proxyURL = cfg.ResolveProxyURL(matched.Proxy)
	}

	// Merge route maps: defaults.routes apply everywhere, location.routes override.
	routes := make(map[string]string, len(cfg.Defaults.Routes)+len(matched.Routes))
	for k, v := range cfg.Defaults.Routes {
		routes[k] = v
	}
	for k, v := range matched.Routes {
		routes[k] = v
	}

	logEntry(host, ssid, fmt.Sprintf("location %q matched → %s", matchedName, matched.Proxy), true)

	// Fire hook if the active location changed.
	fireLocationChange(matchedName, matched)

	return config.Decision{
		ProxyURL: proxyURL,
		Domain:   matched.Domain,
		DNS:      matched.DNS,
		NoProxy:  noProxy,
		Routes:   routes,
		PAC:      matched.PAC,
	}
}

// fireLocationChange detects transitions and calls the OnLocationChange callback.
func fireLocationChange(newName string, newLoc *config.Location) {
	activeLocationMu.Lock()
	oldName := activeLocationName
	oldLoc := activeLocationConfig
	if oldName == newName {
		activeLocationMu.Unlock()
		return
	}
	activeLocationName = newName
	activeLocationConfig = newLoc
	activeLocationMu.Unlock()

	if OnLocationChange != nil {
		OnLocationChange(oldName, newName, oldLoc, newLoc)
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
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		h = hostOnly
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
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
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
