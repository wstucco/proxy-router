package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// legacyConfig represents the old configuration format.
type legacyConfig struct {
	Listen         string       `json:"listen"`
	Upstream       string       `json:"upstream"`
	UpstreamDomain string       `json:"upstream_domain,omitempty"`
	Rules          []legacyRule `json:"rules"`
	Default        string       `json:"default"`
}

type legacyRule struct {
	Domains []string `json:"domains,omitempty"`
	IPs     []string `json:"ips,omitempty"`
	SSIDs   []string `json:"ssids,omitempty"`
	Action  string   `json:"action"`
	DNS     []string `json:"dns,omitempty"`
}

// MigrateIfLegacy detects legacy format, migrates automatically, writes
// a backup of jsonPath and a new TOML config to tomlPath, logs a warning,
// and returns the migrated config.
func MigrateIfLegacy(jsonPath, tomlPath string, data []byte) (*Config, error) {
	var legacy legacyConfig
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("parsing legacy config: %w", err)
	}

	cfg := &Config{
		Listen:    legacy.Listen,
		Proxies:   map[string]string{},
		Locations: map[string]*Location{},
		Defaults: Defaults{
			Proxy: "direct",
		},
	}
	if cfg.Listen == "" {
		cfg.Listen = "localhost:1337"
	}

	// Register upstream as a named proxy if present
	if legacy.Upstream != "" {
		cfg.Proxies["default"] = legacy.Upstream
		if legacy.Default == "upstream" {
			cfg.Defaults.Proxy = "default"
		}
	}

	// Migrate rules to locations
	for i, rule := range legacy.Rules {
		if rule.Action == "direct" {
			// direct rules become no_proxy entries on defaults
			cfg.Defaults.NoProxy = append(cfg.Defaults.NoProxy, rule.Domains...)
			cfg.Defaults.NoProxy = append(cfg.Defaults.NoProxy, rule.IPs...)
			continue
		}

		// Generate a name from the first SSID, domain, or fallback to index
		name := locationName(rule, i)

		proxy := "default"
		if legacy.Upstream == "" {
			proxy = ""
		}

		loc := &Location{
			Proxy:   proxy,
			Domain:  legacy.UpstreamDomain,
			SSIDs:   rule.SSIDs,
			IPs:     rule.IPs,
			Domains: rule.Domains,
			DNS:     rule.DNS,
		}

		// Skip locations with no matchers (would fail validation)
		if len(loc.SSIDs) == 0 && len(loc.IPs) == 0 && len(loc.Domains) == 0 {
			pkgLog.Warn("skipping rule %d: no matchers", i+1)
			continue
		}

		cfg.Locations[name] = loc
	}

	cfg.Version = CurrentVersion

	// Archive the original JSON so a later migrate run can't overwrite the
	// TOML again; fall back to a copy if the rename fails.
	backupPath := jsonPath + ".bak"
	if err := os.Rename(jsonPath, backupPath); err != nil {
		if werr := os.WriteFile(backupPath, data, 0644); werr != nil {
			pkgLog.Warn("could not write backup to %s: %v", backupPath, werr)
		}
	}
	pkgLog.Info("legacy config archived → %s", backupPath)

	// Write migrated config as TOML
	if err := writeTOML(tomlPath, cfg); err != nil {
		return nil, fmt.Errorf("writing migrated config: %w", err)
	}

	pkgLog.Info("config automatically migrated to TOML → %s", tomlPath)
	pkgLog.Info("see https://github.com/wstucco/proxy-router/wiki/configuration for details")

	return cfg, nil
}

// locationName generates a meaningful name for a migrated location.
func locationName(rule legacyRule, index int) string {
	if len(rule.SSIDs) > 0 {
		return sanitizeName(rule.SSIDs[0])
	}
	if len(rule.Domains) > 0 {
		return sanitizeName(rule.Domains[0])
	}
	if len(rule.IPs) > 0 {
		return sanitizeName(rule.IPs[0])
	}
	return fmt.Sprintf("location_%d", index+1)
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimPrefix(s, ".")
	replacer := strings.NewReplacer(
		" ", "_", ".", "_", "/", "_", ":", "_", "*", "any",
	)
	return replacer.Replace(s)
}
