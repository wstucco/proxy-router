package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/wstucco/proxy-router/internal/certmanager"
	"github.com/wstucco/proxy-router/internal/config"
)

type installMode int

const (
	modeBrew   installMode = iota // running from /opt/homebrew or /usr/local/opt
	modeManual                    // manual install to /usr/local
)

type paths struct {
	mode       installMode
	prefix     string // /opt/homebrew or /usr/local
	bin        string
	cfgDir     string
	cfgFile    string
	logDir     string
	caCertFile string
	caKeyFile  string
	plist      string // only set for macOS manual installs
	svcFile    string // only set for Linux (systemd)
}

func detectPaths() paths {
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)

	var p paths

	if strings.HasPrefix(self, "/opt/homebrew") || strings.HasPrefix(self, "/usr/local/Cellar") || strings.HasPrefix(self, "/usr/local/opt") {
		p.mode = modeBrew
		if strings.HasPrefix(self, "/opt/homebrew") {
			p.prefix = "/opt/homebrew"
		} else {
			p.prefix = "/usr/local"
		}
		p.bin = filepath.Join(p.prefix, "bin", binaryName)
		p.cfgDir = filepath.Join(p.prefix, "etc", "proxy-router")
		p.logDir = filepath.Join(p.prefix, "var", "log")
	} else {
		p.mode = modeManual
		p.prefix = "/usr/local"
		p.bin = filepath.Join(p.prefix, "bin", binaryName)
		p.cfgDir = filepath.Join(p.prefix, "etc", "proxy-router")
		p.logDir = filepath.Join(p.prefix, "var", "log", "proxy-router")
		if runtime.GOOS == "darwin" {
			p.plist = filepath.Join("/Library", "LaunchAgents", plistFile)
		} else {
			home, _ := os.UserHomeDir()
			p.svcFile = filepath.Join(home, ".config", "systemd", "user", binaryName+".service")
		}
	}

	p.cfgFile = filepath.Join(p.cfgDir, "config.toml")
	p.caCertFile = filepath.Join(p.cfgDir, "cacert.pem")
	p.caKeyFile = filepath.Join(p.cfgDir, "cakey.pem")
	return p
}

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label}}</string>

  <key>ProgramArguments</key>
  <array>
    <string>{{.Bin}}</string>
    <string>run</string>
    <string>-config</string>
    <string>{{.CfgFile}}</string>
  </array>

  <key>RunAtLoad</key>
  <true/>

  <key>KeepAlive</key>
  <true/>

  <key>StandardOutPath</key>
  <string>{{.LogDir}}/proxy-router.log</string>

  <key>StandardErrorPath</key>
  <string>{{.LogDir}}/proxy-router.err</string>
</dict>
</plist>
`))

var systemdServiceTemplate = template.Must(template.New("service").Parse(`[Unit]
Description=proxy-router — location-based proxy router
Documentation=https://github.com/wstucco/proxy-router
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.Bin}} run -config {{.CfgFile}}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`))

func cmdInstall() {
	p := detectPaths()

	// 1. Write default config if not present
	if err := os.MkdirAll(p.cfgDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error: install: creating config dir %s: %v\n", p.cfgDir, err)
		os.Exit(1)
	}
	if _, err := os.Stat(p.cfgFile); os.IsNotExist(err) {
		if err := os.WriteFile(p.cfgFile, []byte(config.DefaultConfig()), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error: install: writing config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ config    → %s (default, please edit)\n", p.cfgFile)
	} else {
		fmt.Printf("✓ config    → %s (already exists, skipped)\n", p.cfgFile)
	}

	// 2. Install completions
	installCompletions()

	// 3. Register systemd service (Linux) or LaunchAgent (macOS)
	if p.mode == modeManual {
		if p.svcFile != "" {
			// Linux — systemd user service
			if err := os.MkdirAll(filepath.Dir(p.svcFile), 0755); err != nil {
				fmt.Fprintf(os.Stderr, "error: install: creating systemd dir: %v\n", err)
				os.Exit(1)
			}
			f, err := os.Create(p.svcFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: install: creating systemd service: %v\n", err)
				os.Exit(1)
			}
			err = systemdServiceTemplate.Execute(f, map[string]string{
				"Bin":     p.bin,
				"CfgFile": p.cfgFile,
			})
			f.Close()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: install: writing systemd service: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✓ systemd   → %s\n", p.svcFile)
			fmt.Println()
			fmt.Println("To enable and start the service:")
			fmt.Println("  systemctl --user enable proxy-router")
			fmt.Println("  systemctl --user start proxy-router")
		} else {
			// macOS — LaunchAgent
			if err := os.MkdirAll(p.logDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "error: install: creating log dir: %v\n", err)
				os.Exit(1)
			}
			if err := os.MkdirAll(filepath.Dir(p.plist), 0755); err != nil {
				fmt.Fprintf(os.Stderr, "error: install: creating LaunchAgents dir: %v\n", err)
				os.Exit(1)
			}
			f, err := os.Create(p.plist)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: install: creating plist (try with sudo): %v\n", err)
				os.Exit(1)
			}
			err = plistTemplate.Execute(f, map[string]string{
				"Label":   plistLabel,
				"Bin":     p.bin,
				"CfgFile": p.cfgFile,
				"LogDir":  p.logDir,
			})
			f.Close()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: install: writing plist: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✓ plist     → %s\n", p.plist)

			out, err := exec.Command("launchctl", "load", "-w", p.plist).CombinedOutput()
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: install: launchctl load: %v\n%s", err, out)
				os.Exit(1)
			}
			fmt.Println("✓ launchctl load → proxy-router started")
			fmt.Printf("\nLogs: %s/proxy-router.log\n", p.logDir)
		}
	} else {
		fmt.Println()
		fmt.Println("Homebrew install detected — skipping service registration.")
		fmt.Println("To start as a service:")
		fmt.Println("  brew services start proxy-router")
	}

	fmt.Printf("\nEdit config: %s\n", p.cfgFile)
}

func cmdInstallCerts() {
	p := detectPaths()

	// Ensure cert directory exists
	if err := os.MkdirAll(p.cfgDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error: install-certs: creating config dir: %v\n", err)
		os.Exit(1)
	}

	// Initialize cert manager (generates CA if not present)
	if _, err := certmanager.NewManager(p.caCertFile, p.caKeyFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: install-certs: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ CA certificate → %s\n", p.caCertFile)
	fmt.Println()
	fmt.Println("To trust the certificate, run one of the following:")
	fmt.Println()
	fmt.Println("  macOS (system keychain, requires sudo):")
	fmt.Println("    sudo security add-trusted-cert -d -r trustRoot \\")
	fmt.Printf("      -k /Library/Keychains/System.keychain %s\n", p.caCertFile)
	fmt.Println()
	fmt.Println("  macOS (user keychain, no sudo):")
	fmt.Printf("    security add-trusted-cert -d -r trustRoot \\\n")
	fmt.Printf("      -k ~/Library/Keychains/login.keychain-db %s\n", p.caCertFile)
	fmt.Println()
	fmt.Println("  Firefox (NSS cert store):")
	fmt.Println("    certutil -A -n \"proxy-router\" -t \"TC,C,C\" \\")
	fmt.Printf("      -d ~/.mozilla/firefox/*.default-release -i %s\n", p.caCertFile)
	fmt.Println()
	fmt.Println("After trusting the CA, restart proxy-router.")
}

func cmdUninstall(prune bool) {
	p := detectPaths()

	if p.mode == modeManual {
		if p.svcFile != "" {
			exec.Command("systemctl", "--user", "stop", binaryName).Run()
			exec.Command("systemctl", "--user", "disable", binaryName).Run()
			fmt.Println("✓ systemctl stop/disable")
			removeFile(p.svcFile, "systemd service")
		} else {
			exec.Command("launchctl", "unload", "-w", p.plist).Run()
			fmt.Println("✓ launchctl unload")
			removeFile(p.plist, "plist")
		}
	} else {
		fmt.Println("Homebrew install detected — to stop the service:")
		fmt.Println("  brew services stop proxy-router")
	}

	removeCompletions()

	if prune {
		if err := os.RemoveAll(p.cfgDir); err != nil {
			log.Printf("uninstall: removing config dir: %v", err)
		} else {
			fmt.Printf("✓ config dir removed → %s\n", p.cfgDir)
		}
	} else {
		fmt.Printf("  config kept → %s (use --prune to delete)\n", p.cfgDir)
	}
}

func removeFile(path, label string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("uninstall: removing %s: %v", label, err)
	} else if err == nil {
		fmt.Printf("✓ %s removed → %s\n", label, path)
	}
}
