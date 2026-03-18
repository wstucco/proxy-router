package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"text/template"
	"time"

	"github.com/wstucco/proxy-router/internal/config"
	"github.com/wstucco/proxy-router/internal/proxy"
	"github.com/wstucco/proxy-router/internal/router"
)

// version is set at build time via -ldflags "-X main.version=x.y.z"
var version = "dev"

const plistName = "com.local.proxy-router.plist"
const binaryName = "proxy-router"

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func binPath() string { return filepath.Join(userHome(), ".local", "bin", binaryName) }
func cfgDir() string  { return filepath.Join(userHome(), ".config", "proxy-router") }
func cfgPath() string { return filepath.Join(cfgDir(), "config.json") }
func plistPath() string {
	return filepath.Join(userHome(), "Library", "LaunchAgents", plistName)
}

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.local.proxy-router</string>

  <key>ProgramArguments</key>
  <array>
    <string>{{.BinPath}}</string>
    <string>-config</string>
    <string>{{.CfgPath}}</string>
  </array>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>StandardOutPath</key>
  <string>{{.Home}}/Library/Logs/proxy-router.log</string>

  <key>StandardErrorPath</key>
  <string>{{.Home}}/Library/Logs/proxy-router.err</string>
</dict>
</plist>
`))

func cmdInstall() {
	home := userHome()

	// 1. Copy binary
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("install: cannot determine executable path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binPath()), 0755); err != nil {
		log.Fatalf("install: creating bin dir: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		log.Fatalf("install: reading binary: %v", err)
	}
	if err := os.WriteFile(binPath(), data, 0755); err != nil {
		log.Fatalf("install: writing binary: %v", err)
	}
	fmt.Printf("✓ binary    → %s\n", binPath())

	// 2. Write default config if not already present
	if err := os.MkdirAll(cfgDir(), 0755); err != nil {
		log.Fatalf("install: creating config dir: %v", err)
	}
	if _, err := os.Stat(cfgPath()); os.IsNotExist(err) {
		if err := os.WriteFile(cfgPath(), []byte(config.DefaultConfig()), 0644); err != nil {
			log.Fatalf("install: writing config: %v", err)
		}
		fmt.Printf("✓ config    → %s (default, please edit)\n", cfgPath())
	} else {
		fmt.Printf("✓ config    → %s (already exists, skipped)\n", cfgPath())
	}

	// 3. Write plist
	if err := os.MkdirAll(filepath.Dir(plistPath()), 0755); err != nil {
		log.Fatalf("install: creating LaunchAgents dir: %v", err)
	}
	f, err := os.Create(plistPath())
	if err != nil {
		log.Fatalf("install: creating plist: %v", err)
	}
	err = plistTemplate.Execute(f, map[string]string{
		"BinPath": binPath(),
		"CfgPath": cfgPath(),
		"Home":    home,
	})
	f.Close()
	if err != nil {
		log.Fatalf("install: writing plist: %v", err)
	}
	fmt.Printf("✓ plist     → %s\n", plistPath())

	// 4. Set GOTOOLCHAIN=local to avoid toolchain download attempts
	exec.Command("go", "env", "-w", "GOTOOLCHAIN=local").Run()

	// 5. Load LaunchAgent
	out, err := exec.Command("launchctl", "load", "-w", plistPath()).CombinedOutput()
	if err != nil {
		log.Fatalf("install: launchctl load: %v\n%s", err, out)
	}
	fmt.Println("✓ launchctl load → proxy-router started")
	fmt.Printf("\nProxy is running at %s\n", "localhost:32000")
	fmt.Printf("Edit config: %s\n", cfgPath())
	fmt.Printf("View logs:   %s\n", filepath.Join(home, "Library", "Logs", "proxy-router.log"))
}

func cmdUninstall(prune bool) {
	// 1. Unload LaunchAgent (ignore errors — may not be loaded)
	exec.Command("launchctl", "unload", "-w", plistPath()).Run()
	fmt.Println("✓ launchctl unload")

	// 2. Remove plist
	removeFile(plistPath(), "plist")

	// 3. Remove binary
	removeFile(binPath(), "binary")

	// 4. Optionally remove config
	if prune {
		if err := os.RemoveAll(cfgDir()); err != nil {
			log.Printf("uninstall: removing config dir: %v", err)
		} else {
			fmt.Printf("✓ config dir removed → %s\n", cfgDir())
		}
	} else {
		fmt.Printf("  config kept → %s (use --prune to delete)\n", cfgDir())
	}
}

func removeFile(path, label string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("uninstall: removing %s: %v", label, err)
	} else {
		fmt.Printf("✓ %s removed → %s\n", label, path)
	}
}

func printHelp() {
	fmt.Printf("proxy-router version %s\n\n", version)
	fmt.Print(`proxy-router — a local proxy that routes connections based on configurable rules.

USAGE:
  proxy-router <command> [flags]

COMMANDS:
  run         Start the proxy (default if no command given)
  install     Install binary, config, and LaunchAgent; start automatically at login
  uninstall   Stop and remove binary and LaunchAgent (config is kept by default)
  help        Show this help

RUN FLAGS:
  -config <path>    Path to config file (default: ~/.config/proxy-router/config.json)
  -gen-config       Print an example config.json and exit

UNINSTALL FLAGS:
  --prune           Also delete the config directory (~/.config/proxy-router/)

EXAMPLES:
  proxy-router install
  proxy-router run -config /tmp/test.json
  proxy-router uninstall
  proxy-router uninstall --prune

PATHS (per-user install):
  Binary:      ~/.local/bin/proxy-router
  Config:      ~/.config/proxy-router/config.json
  LaunchAgent: ~/Library/LaunchAgents/com.local.proxy-router.plist
  Logs:        ~/Library/Logs/proxy-router.{log,err}

CONFIG:
  Rules are evaluated top-to-bottom; first match wins.
  Reload config at runtime by saving the file or sending SIGHUP:
    kill -HUP $(pgrep proxy-router)
`)
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfg := fs.String("config", cfgPath(), "path to config file")
	genCfg := fs.Bool("gen-config", false, "print example config.json and exit")
	fs.Parse(args)

	if *genCfg {
		fmt.Println(config.DefaultConfig())
		os.Exit(0)
	}

	c, err := config.Load(*cfg)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	log.Printf("loaded config: listen=%s upstream=%s default=%s rules=%d",
		c.Listen, c.Upstream, c.Default, len(c.Rules))

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(c)

	reload := func() {
		newCfg, err := config.Load(*cfg)
		if err != nil {
			log.Printf("[reload] error: %v — keeping current config", err)
			return
		}
		cfgPtr.Store(newCfg)
		log.Printf("[reload] config reloaded: rules=%d", len(newCfg.Rules))
	}

	// Watch config file for changes (poll mtime every second)
	go func() {
		var lastMod time.Time
		if fi, err := os.Stat(*cfg); err == nil {
			lastMod = fi.ModTime()
		}
		for range time.Tick(time.Second) {
			fi, err := os.Stat(*cfg)
			if err != nil {
				continue
			}
			if fi.ModTime().After(lastMod) {
				lastMod = fi.ModTime()
				log.Printf("[reload] config file changed, reloading...")
				reload()
			}
		}
	}()

	// Also support manual SIGHUP
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		for range ch {
			log.Printf("[reload] SIGHUP received, reloading...")
			reload()
		}
	}()

	// Start network change listener (keeps SSID cache up to date)
	go router.StartNetworkListener()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := cfgPtr.Load()
		srv := proxy.New(current)
		srv.ServeHTTP(w, r)
	})

	log.Printf("proxy-router listening on %s", c.Listen)
	if err := http.ListenAndServe(c.Listen, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func main() {
	if len(os.Args) < 2 {
		cmdRun(os.Args[1:])
		return
	}

	switch os.Args[1] {
	case "install":
		cmdInstall()
	case "uninstall":
		prune := len(os.Args) > 2 && os.Args[2] == "--prune"
		cmdUninstall(prune)
	case "run":
		cmdRun(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("proxy-router version %s\n", version)
	case "help", "-h", "--help":
		printHelp()
	default:
		// Treat unknown first arg as a flag for backwards compat (e.g. -config)
		cmdRun(os.Args[1:])
	}
}
