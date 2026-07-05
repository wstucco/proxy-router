package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchRoute(t *testing.T) {
	tests := []struct {
		name   string
		routes map[string]string
		host   string
		path   string
		want   string
		wantOK bool
	}{
		{"host prefix", map[string]string{"httpbin.org": "https://localhost:4321"}, "httpbin.org", "/anything", "https://localhost:4321/anything", true},
		{"host+path prefix", map[string]string{"httpbin.org/api": "https://localhost:4321"}, "httpbin.org", "/api/users", "https://localhost:4321/users", true},
		{"no match different host", map[string]string{"httpbin.org": "https://localhost:4321"}, "example.com", "/", "", false},
		{"no match different path", map[string]string{"httpbin.org/api": "https://localhost:4321"}, "httpbin.org", "/other", "", false},
		{"empty routes", map[string]string{}, "httpbin.org", "/x", "", false},
		{"trailing slash normalization", map[string]string{"example.com/": "https://localhost:8080"}, "example.com", "/foo", "https://localhost:8080/foo", true},
		{"root path", map[string]string{"example.com": "https://localhost:9999"}, "example.com", "/", "https://localhost:9999", true},
		{"multiple routes longest prefix wins", map[string]string{"httpbin.org": "https://a:1", "httpbin.org/api": "https://b:2"}, "httpbin.org", "/api/users", "https://b:2/users", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := &Location{Routes: tt.routes}
			got, ok := loc.MatchRoute(tt.host, tt.path)
			if ok != tt.wantOK {
				t.Errorf("MatchRoute() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("MatchRoute() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchRouteOrderPreservation(t *testing.T) {
	loc := &Location{
		Routes: map[string]string{
			"httpbin.org": "https://localhost:4321",
		},
	}

	got, ok := loc.MatchRoute("httpbin.org", "/anything")
	if !ok {
		t.Error("MatchRoute should match")
	}
	if got != "https://localhost:4321/anything" {
		t.Errorf("MatchRoute = %q, want %q", got, "https://localhost:4321/anything")
	}
}

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
