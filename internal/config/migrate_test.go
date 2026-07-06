package config

import (
	"os"
	"path/filepath"
	"testing"
)

const legacyJSON = `{
	"listen": "localhost:1337",
	"upstream": "http://user:pass@corp-proxy:8080",
	"default": "upstream",
	"rules": [
		{"ssids": ["office"], "action": "upstream"}
	]
}`

// Regression test: after migration the legacy JSON must be archived (renamed
// to .bak), otherwise a later `migrate` run — brew post_install runs it on
// every upgrade — would re-migrate the stale JSON and overwrite the TOML.
func TestMigrateArchivesLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")
	tomlPath := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(jsonPath, []byte(legacyJSON), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := MigrateIfLegacy(jsonPath, tomlPath, []byte(legacyJSON))
	if err != nil {
		t.Fatalf("MigrateIfLegacy: %v", err)
	}
	if cfg.Proxies["default"] != "http://user:pass@corp-proxy:8080" {
		t.Errorf("upstream not migrated: %v", cfg.Proxies)
	}

	if _, err := os.Stat(tomlPath); err != nil {
		t.Errorf("migrated TOML not written: %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("legacy config.json still present after migration — would re-trigger on next migrate run")
	}
	bak, err := os.ReadFile(jsonPath + ".bak")
	if err != nil {
		t.Fatalf("backup not written: %v", err)
	}
	if string(bak) != legacyJSON {
		t.Errorf("backup content differs from original JSON")
	}
}
