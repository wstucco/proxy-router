package router

import (
	"log"
	"strings"

	"github.com/local/proxy-router/internal/config"
)

// Decide evaluates rules top-to-bottom and returns the action for the given host.
func Decide(cfg *config.Config, host string) config.Action {
	ssid := CurrentSSID()
	log.Printf("[router] host=%s ssid=%q", host, ssid)

	for _, rule := range cfg.Rules {
		if matches(rule, host, ssid) {
			log.Printf("[router] rule matched → %s", rule.Action)
			return rule.Action
		}
	}

	log.Printf("[router] no rule matched → default: %s", cfg.Default)
	return cfg.Default
}

func matches(rule config.Rule, host, ssid string) bool {
	// Each matcher type is optional; if specified it must match.
	if len(rule.Domains) > 0 {
		if !config.MatchDomain(host, rule.Domains) {
			return false
		}
	}
	if len(rule.SSIDs) > 0 {
		if !matchSSID(ssid, rule.SSIDs) {
			return false
		}
	}
	// IP matching: straightforward string/CIDR check
	if len(rule.IPs) > 0 {
		if !matchIP(host, rule.IPs) {
			return false
		}
	}
	return true
}

func matchSSID(current string, list []string) bool {
	current = strings.ToLower(strings.TrimSpace(current))
	for _, s := range list {
		if strings.ToLower(strings.TrimSpace(s)) == current {
			return true
		}
	}
	return false
}

func matchIP(host string, list []string) bool {
	// strip port
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	for _, entry := range list {
		if host == entry {
			return true
		}
	}
	return false
}
