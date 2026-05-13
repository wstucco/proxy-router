package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRouteMatch(t *testing.T) {
	tests := []struct {
		name     string
		routes   map[string]string
		path     string
		want     string
		wantOK   bool
	}{
		{"exact match", map[string]string{"/api/health": "direct"}, "/api/health", "direct", true},
		{"exact no match", map[string]string{"/api/health": "direct"}, "/api/other", "", false},
		{"glob suffix", map[string]string{"/api/*": "direct"}, "/api/health", "direct", true},
		{"glob suffix nested", map[string]string{"/api/*": "direct"}, "/api/v1/health", "", false},
		{"glob prefix", map[string]string{"/*.html": "direct"}, "/index.html", "direct", true},
		{"single char", map[string]string{"/api/?" : "direct"}, "/api/h", "direct", true},
		{"empty routes", map[string]string{}, "/api/health", "", false},
		{"multiple routes first match", map[string]string{"/api/*": "direct", "/health": "corp"}, "/health", "corp", true},
		{"no match among several", map[string]string{"/a": "direct", "/b": "corp"}, "/c", "", false},
		{"root path", map[string]string{"/": "direct"}, "/", "direct", true},
		{"trailing slash", map[string]string{"/api/": "direct"}, "/api/", "direct", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := &Location{Routes: tt.routes}
			got, ok := loc.RouteMatch(tt.path)
			if ok != tt.wantOK {
				t.Errorf("RouteMatch() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("RouteMatch() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRouteMatchOrderPreservation(t *testing.T) {
	// Go map iteration is non-deterministic, so routes with overlapping
	// patterns may match unpredictably. Test that all registered patterns
	// are at least reachable.
	loc := &Location{
		Routes: map[string]string{
			"/api/*":    "direct",
			"/api/health": "corp",
		},
	}

	got, ok := loc.RouteMatch("/api/health")
	if !ok {
		t.Error("RouteMatch(/api/health) should match one of the patterns")
	}
	if got != "direct" && got != "corp" {
		t.Errorf("RouteMatch(/api/health) = %q, want one of 'direct', 'corp'", got)
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
