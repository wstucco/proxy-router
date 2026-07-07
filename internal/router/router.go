package router

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wstucco/proxy-router/internal/config"
)

var (
	activeLocationMu     sync.Mutex
	activeLocationName   string
	activeLocationConfig *config.Location
	currentConfig        atomic.Pointer[config.Config]
)

// SetConfig stores the current config for use by the network listener
// to log location details on SSID changes.
func SetConfig(cfg *config.Config) {
	currentConfig.Store(cfg)
}

// OnLocationChange is set by main to execute hooks when the active location changes.
var OnLocationChange func(oldName, newName string, oldLoc, newLoc *config.Location)

// Decide evaluates locations top-to-bottom and returns a Decision for the given host.
// Routes from the matched location are carried in the Decision for per-request
// destination rewriting in the proxy handler.
func Decide(cfg *config.Config, host string) config.Decision {
	return decide(cfg, host, CurrentSSID())
}

func decide(cfg *config.Config, host, ssid string) config.Decision {
	// Always-no-proxy check — hardcoded, cannot be overridden
	if isAlwaysNoProxy(host) {
		logEntry(host, ssid, "direct (always)", false)
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
			logEntry(host, ssid, "direct (no_proxy)", true)
		} else {
			logEntry(host, ssid, "direct (no_proxy)", false)
		}
		return config.Decision{}
	}

	// No location matched — use defaults
	if matched == nil {
		pacURL := cfg.ResolvePACURL(cfg.Defaults.PAC)
		proxyURL := cfg.ResolveProxyURL(cfg.Defaults.Proxy)
		if proxyURL == "" && pacURL == "" {
			logEntry(host, ssid, "default direct", false)
		} else if proxyURL != "" {
			logEntry(host, ssid, fmt.Sprintf("default proxy:%s", cfg.Defaults.Proxy), false)
		} else {
			logEntry(host, ssid, fmt.Sprintf("default pac:%s", cfg.Defaults.PAC), false)
		}

		fireLocationChange("", nil)

		return config.Decision{
			ProxyURL:  proxyURL,
			ProxyName: cfg.Defaults.Proxy,
			NoProxy:   noProxy,
			Routes:    cfg.Defaults.Routes,
			PAC:       pacURL,
			PACName:   cfg.Defaults.PAC,
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

	logEntry(host, ssid, destString(matched.Proxy, matched.PAC), true)

	// Fire hook if the active location changed.
	fireLocationChange(matchedName, matched)

	return config.Decision{
		ProxyURL:     proxyURL,
		ProxyName:    matched.Proxy,
		Domain:       matched.Domain,
		DNS:          matched.DNS,
		NoProxy:      noProxy,
		Routes:       routes,
		PAC:          cfg.ResolvePACURL(matched.PAC),
		PACName:      matched.PAC,
		LocationName: matchedName,
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

// SSIDLocationInfo returns the location name and proxy/pac description that
// would be selected for the given SSID with a probe hostname.
func SSIDLocationInfo(cfg *config.Config, ssid string) (name, proxyOrPAC string) {
	for n, loc := range cfg.Locations {
		if matchesLocation(loc, "probe.local", ssid) {
			if loc.Proxy != "" && loc.Proxy != "direct" {
				return n, "proxy: " + loc.Proxy
			}
			if loc.PAC != "" && loc.PAC != "direct" {
				return n, "pac: " + loc.PAC
			}
			return n, "direct"
		}
	}
	// No location matched — use defaults
	if cfg.Defaults.Proxy != "" && cfg.Defaults.Proxy != "direct" {
		return "", "default proxy: " + cfg.Defaults.Proxy
	}
	if cfg.Defaults.PAC != "" && cfg.Defaults.PAC != "direct" {
		return "", "default pac: " + cfg.Defaults.PAC
	}
	return "", "default: direct"
}

// destString returns a compact description of a location's destination.
func destString(proxy, pac string) string {
	switch {
	case proxy != "" && proxy != "direct":
		return "proxy:" + proxy
	case pac != "" && pac != "direct":
		return "pac:" + pac
	default:
		return "direct"
	}
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
