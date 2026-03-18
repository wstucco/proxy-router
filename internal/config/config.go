package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is the top-level configuration for proxy-router.
type Config struct {
	Listen   string     `json:"listen"`    // e.g. "localhost:32000"
	Upstream string     `json:"upstream"`  // e.g. "http://corporate:8080" or "socks5://..."
	Rules    []Rule     `json:"rules"`     // evaluated top-to-bottom, first match wins
	Default  Action     `json:"default"`   // "direct" or "upstream"
}

// Rule matches traffic and decides what to do with it.
type Rule struct {
	// Matchers (all present matchers must match — AND logic)
	Domains []string `json:"domains,omitempty"` // suffix match: "example.com" matches "foo.example.com"
	IPs     []string `json:"ips,omitempty"`     // exact or CIDR match
	SSIDs   []string `json:"ssids,omitempty"`   // current Wi-Fi SSID (case-insensitive)

	Action Action `json:"action"` // "direct" or "upstream"
}

type Action string

const (
	ActionDirect   Action = "direct"
	ActionUpstream Action = "upstream"
)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = "localhost:32000"
	}
	if cfg.Default == "" {
		cfg.Default = ActionDirect
	}
	return &cfg, nil
}

// DefaultConfig returns a commented example config as JSON string.
func DefaultConfig() string {
	cfg := Config{
		Listen:   "localhost:32000",
		Upstream: "http://corporate-proxy:8080",
		Default:  ActionDirect,
		Rules: []Rule{
			{
				SSIDs:  []string{"OfficeWifi", "CorpVPN"},
				Action: ActionUpstream,
			},
			{
				Domains: []string{"internal.corp.com", "jira.corp.com"},
				Action:  ActionUpstream,
			},
			{
				Domains: []string{"localhost", "127.0.0.1"},
				Action:  ActionDirect,
			},
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b)
}

// MatchDomain returns true if host matches any of the domain suffixes.
func MatchDomain(host string, domains []string) bool {
	// strip port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)
	for _, d := range domains {
		d = strings.ToLower(d)
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
