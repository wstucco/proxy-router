package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wstucco/proxy-router/internal/hooks"
)

// ─── config load / migration ──────────────────────────────────────────────────

func TestLoadTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `version = "1"
listen = "localhost:1337"

[proxies]
corp = "http://user:pass@proxy:8080"

[defaults]
proxy = "direct"

[locations.work]
proxy = "corp"
ssids = ["OfficeWifi"]
`
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Listen != "localhost:1337" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "localhost:1337")
	}
	if cfg.Version != "1" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1")
	}
	if cfg.Defaults.Proxy != "direct" {
		t.Errorf("Defaults.Proxy = %q, want %q", cfg.Defaults.Proxy, "direct")
	}
	if cfg.Locations["work"] == nil {
		t.Fatal("location 'work' not found")
	}
	if cfg.Locations["work"].Proxy != "corp" {
		t.Errorf("location work.Proxy = %q, want %q", cfg.Locations["work"].Proxy, "corp")
	}
}

func TestLoadLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	json := `{
		"listen": "localhost:1337",
		"upstream": "http://user:pass@proxy:8080",
		"default": "upstream",
		"rules": [
			{"ssids": ["OfficeWifi"], "action": "upstream"}
		]
	}`
	if err := os.WriteFile(jsonPath, []byte(json), 0644); err != nil {
		t.Fatal(err)
	}

	// Load with .json path — should migrate to TOML
	cfg, err := Load(jsonPath)
	if err != nil {
		t.Fatalf("Load(legacy.json) failed: %v", err)
	}

	if cfg.Listen != "localhost:1337" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "localhost:1337")
	}
	if cfg.Version != "1" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1")
	}

	// Should have created config.toml
	tomlPath := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		t.Error("config.toml was not created after migration")
	}

	// Load again by .toml — should work without re-migrating
	cfg2, err := Load(tomlPath)
	if err != nil {
		t.Fatalf("Load(migrated.toml) failed: %v", err)
	}
	if cfg2.Listen != "localhost:1337" {
		t.Errorf("after reload Listen = %q", cfg2.Listen)
	}
}

func TestLoadNewJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	json := `{
		"listen": "localhost:1337",
		"proxies": {"corp": "http://user:pass@proxy:8080"},
		"defaults": {"proxy": "direct"},
		"locations": {
			"office": {
				"proxy": "corp",
				"ssids": ["OfficeWifi"]
			}
		}
	}`
	if err := os.WriteFile(jsonPath, []byte(json), 0644); err != nil {
		t.Fatal(err)
	}

	// Load with .json path — should migrate to TOML
	cfg, err := Load(jsonPath)
	if err != nil {
		t.Fatalf("Load(new.json) failed: %v", err)
	}

	if cfg.Listen != "localhost:1337" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "localhost:1337")
	}
	if cfg.Version != "1" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1")
	}
	if cfg.Locations["office"] == nil {
		t.Fatal("location 'office' not found")
	}

	// Should have created config.toml
	tomlPath := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		t.Error("config.toml was not created after migration")
	}
}

func TestLoadTOMLFallbackToJSON(t *testing.T) {
	dir := t.TempDir()

	// Write only config.json (new format)
	json := `{
		"listen": "localhost:9999",
		"proxies": {},
		"defaults": {"proxy": "direct"},
		"locations": {}
	}`
	jsonPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(jsonPath, []byte(json), 0644); err != nil {
		t.Fatal(err)
	}

	// Load with non-existent .toml — should fall back to .json and migrate
	tomlPath := filepath.Join(dir, "config.toml")
	cfg, err := Load(tomlPath)
	if err != nil {
		t.Fatalf("Load(missing.toml) with fallback failed: %v", err)
	}

	if cfg.Listen != "localhost:9999" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "localhost:9999")
	}

	// Should have created config.toml
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		t.Error("config.toml was not created after fallback migration")
	}
}

func TestLoadDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `[defaults]
proxy = "direct"
`
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Listen != "localhost:1337" {
		t.Errorf("Listen = %q, want default %q", cfg.Listen, "localhost:1337")
	}
	if cfg.Version != "1" {
		t.Errorf("Version = %q, want %q", cfg.Version, "1")
	}
	if cfg.Proxies == nil {
		t.Error("Proxies should not be nil")
	}
	if cfg.Locations == nil {
		t.Error("Locations should not be nil")
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("garbage [[["), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() should fail on invalid TOML")
	}
}

// ─── PAC config ──────────────────────────────────────────────────────────────

func TestLoadTOMLWithPACs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `version = "1"
listen = "localhost:1337"

[pacs]
corporate = "/etc/proxy-router/corporate.pac"
auto = "http://proxy:8080/proxy.pac"

[defaults]
pac = "corporate"

[locations.office]
pac = "corporate"
ssids = ["OfficeWifi"]
`
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if len(cfg.PACs) != 2 {
		t.Errorf("PACs = %d entries, want 2", len(cfg.PACs))
	}
	if cfg.PACs["corporate"] != "/etc/proxy-router/corporate.pac" {
		t.Errorf(`PACs["corporate"] = %q, want %q`, cfg.PACs["corporate"], "/etc/proxy-router/corporate.pac")
	}
	if cfg.Defaults.PAC != "corporate" {
		t.Errorf("Defaults.PAC = %q, want %q", cfg.Defaults.PAC, "corporate")
	}
	if cfg.Locations["office"].PAC != "corporate" {
		t.Errorf("location office.PAC = %q, want %q", cfg.Locations["office"].PAC, "corporate")
	}
}

func TestResolvePACURL(t *testing.T) {
	cfg := &Config{
		PACs: map[string]string{
			"corporate": "/etc/proxy-router/corporate.pac",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"direct", ""},
		{"corporate", "/etc/proxy-router/corporate.pac"},
		{"/path/to/custom.pac", "/path/to/custom.pac"},
		{"http://example.com/proxy.pac", "http://example.com/proxy.pac"},
	}

	for _, tt := range tests {
		got := cfg.ResolvePACURL(tt.input)
		if got != tt.want {
			t.Errorf("ResolvePACURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoadBothProxyAndPAC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `version = "1"
listen = "localhost:1337"

[defaults]
proxy = "direct"
pac = "direct"

[locations.test]
proxy = "direct"
pac = "direct"
ssids = ["Test"]
`
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() should fail when a location has both proxy and pac")
	}
}

func TestLoadDefaultsBothProxyAndPAC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `version = "1"
listen = "localhost:1337"

[defaults]
proxy = "direct"
pac = "/path/to/pac"
`
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() should fail when defaults has both proxy and pac")
	}
}

func TestLoadDefaultsPACDirect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	toml := `version = "1"
listen = "localhost:1337"

[defaults]
pac = "direct"

[locations.test]
proxy = "direct"
ssids = ["Test"]
`
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if cfg.Defaults.PAC != "direct" {
		t.Errorf("Defaults.PAC = %q, want %q", cfg.Defaults.PAC, "direct")
	}
}

// ─── ConfigDiff ─────────────────────────────────────────────────────────────────

func TestConfigDiffLocationHooks(t *testing.T) {
	oldCfg := &Config{
		Locations: map[string]*Location{
			"office": {
				Proxy: "corp",
				SSIDs: []string{"OfficeWifi"},
			},
		},
	}
	newCfg := &Config{
		Locations: map[string]*Location{
			"office": {
				Proxy: "corp",
				SSIDs: []string{"OfficeWifi"},
				Hooks: &hooks.LocationHooks{
					OnEnter: &hooks.HookConfig{Exec: "echo entered"},
				},
			},
		},
	}

	diff := ConfigDiff(oldCfg, newCfg)
	if diff == " (no changes)" {
		t.Error("ConfigDiff: expected location hooks change to be detected")
	}
}

func TestConfigDiffLocationProxy(t *testing.T) {
	oldCfg := &Config{
		Locations: map[string]*Location{
			"office": {Proxy: "corp", SSIDs: []string{"OfficeWifi"}},
		},
	}
	newCfg := &Config{
		Locations: map[string]*Location{
			"office": {Proxy: "direct", SSIDs: []string{"OfficeWifi"}},
		},
	}

	diff := ConfigDiff(oldCfg, newCfg)
	if diff == " (no changes)" {
		t.Error("ConfigDiff: expected location proxy change to be detected")
	}
}

func TestConfigDiffLocationAdded(t *testing.T) {
	oldCfg := &Config{Locations: map[string]*Location{}}
	newCfg := &Config{
		Locations: map[string]*Location{
			"office": {Proxy: "corp", SSIDs: []string{"OfficeWifi"}},
		},
	}

	diff := ConfigDiff(oldCfg, newCfg)
	if diff == " (no changes)" {
		t.Error("ConfigDiff: expected new location to be detected")
	}
}

func TestConfigDiffLocationRemoved(t *testing.T) {
	oldCfg := &Config{
		Locations: map[string]*Location{
			"office": {Proxy: "corp", SSIDs: []string{"OfficeWifi"}},
		},
	}
	newCfg := &Config{Locations: map[string]*Location{}}

	diff := ConfigDiff(oldCfg, newCfg)
	if diff == " (no changes)" {
		t.Error("ConfigDiff: expected removed location to be detected")
	}
}

// ─── helper comparisons ─────────────────────────────────────────────────────────

func TestStringSliceEqual(t *testing.T) {
	if !stringSliceEqual(nil, nil) {
		t.Error("nil slices should be equal")
	}
	if !stringSliceEqual([]string{}, []string{}) {
		t.Error("empty slices should be equal")
	}
	if stringSliceEqual([]string{"a"}, []string{"b"}) {
		t.Error("different values should not be equal")
	}
	if stringSliceEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different lengths should not be equal")
	}
}

func TestRoutesEqual(t *testing.T) {
	if !routesEqual(nil, nil) {
		t.Error("nil maps should be equal")
	}
	if !routesEqual(map[string]string{}, map[string]string{}) {
		t.Error("empty maps should be equal")
	}
	if routesEqual(map[string]string{"a": "1"}, map[string]string{"a": "2"}) {
		t.Error("different values should not be equal")
	}
	if routesEqual(map[string]string{"a": "1"}, map[string]string{"b": "1"}) {
		t.Error("different keys should not be equal")
	}
}

func TestHooksEqual(t *testing.T) {
	if !hooksEqual(nil, nil) {
		t.Error("nil hooks should be equal")
	}
	if hooksEqual(&hooks.LocationHooks{}, nil) {
		t.Error("empty vs nil should not be equal")
	}
	if !hooksEqual(&hooks.LocationHooks{}, &hooks.LocationHooks{}) {
		t.Error("empty hooks should be equal")
	}
	a := &hooks.LocationHooks{OnEnter: &hooks.HookConfig{Exec: "echo hello"}}
	b := &hooks.LocationHooks{OnEnter: &hooks.HookConfig{Exec: "echo bye"}}
	if hooksEqual(a, b) {
		t.Error("different hook exec should not be equal")
	}
}
