package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/wstucco/proxy-router/internal/config"
)

// version is set at build time via -ldflags "-X main.version=x.y.z"
var version = "dev"

const plistLabel = "com.wstucco.proxy-router"
const plistFile = "com.wstucco.proxy-router.plist"
const binaryName = "proxy-router"

func cmdMigrate() {
	p := detectPaths()
	jsonPath := strings.TrimSuffix(p.cfgFile, ".toml") + ".json"

	data, err := os.ReadFile(jsonPath)
	if os.IsNotExist(err) {
		fmt.Println("✓ no legacy config.json found, nothing to migrate")
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: migrate: reading legacy config: %v\n", err)
		os.Exit(1)
	}

	// A config.toml already in place is the source of truth: never overwrite
	// it from a stale config.json (post_install runs migrate on every upgrade).
	// Archive the leftover JSON so it can't re-trigger a migration.
	if _, err := os.Stat(p.cfgFile); err == nil {
		if err := os.Rename(jsonPath, jsonPath+".bak"); err != nil {
			fmt.Fprintf(os.Stderr, "error: migrate: archiving legacy config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ config.toml already exists — leftover config.json archived as %s.bak\n", jsonPath)
		return
	}

	_, err = config.MigrateIfLegacy(jsonPath, p.cfgFile, data)
	if err != nil {
		// Unsupported format or other non-fatal migration error — skip
		fmt.Printf("⚠ migrate: %v\n", err)
		fmt.Println("⚠ config.json is not a legacy config, skipping migration")
		return
	}
	fmt.Printf("✓ config migrated → %s\n", p.cfgFile)
}

func main() {
	if len(os.Args) < 2 {
		cmdRun(nil)
		return
	}

	switch os.Args[1] {
	case "migrate":
		cmdMigrate()

	case "install":
		cmdInstall()
	case "install-certs":
		cmdInstallCerts()
	case "uninstall":
		prune := len(os.Args) > 2 && os.Args[2] == "--prune"
		cmdUninstall(prune)
	case "run":
		cmdRun(os.Args[2:])
	case "completion":
		cmdCompletion(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("proxy-router version %s\n", version)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nRun 'proxy-router help' for usage.\n", os.Args[1])
		os.Exit(1)
	}
}
