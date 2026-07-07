package main

import "fmt"

func printHelp() {
	fmt.Printf("proxy-router version %s\n\n", version)
	fmt.Print(`A local proxy that routes connections to an upstream or direct based on configurable rules.

USAGE:
  proxy-router <command> [flags]

COMMANDS:
  run             Start the proxy
  connections     Live view of connections through the proxy (q to quit)
  install         Write default config, install completions, register service (LaunchAgent/systemd)
  install-certs   Generate and show how to install CA certificate for TLS MITM
  uninstall       Deregister service, remove completions (config kept by default)
  completion      Generate shell completion script (zsh, bash, fish)
  version         Print version
  help            Show this help

RUN FLAGS:
  -config <path>    Path to config file
                    default (brew):   /opt/homebrew/etc/proxy-router/config.toml
                    default (manual): /usr/local/etc/proxy-router/config.toml
  -listen <addr>    Override listen address (e.g. localhost:33000)
  -gen-config       Print an example config.toml and exit

CONNECTIONS FLAGS:
  -config <path>    Path to config file (to find the daemon's listen address)
  -listen <addr>    Daemon address (overrides config)
  -interval <dur>   Refresh interval for plain mode (default 1s)
  -once             Print one snapshot and exit (no TUI, scripting-friendly)
  -plain            Force the basic ANSI TUI
  -enhanced         Force the enhanced TUI (scroll, active/all filter, live SSE)

  Default: enhanced on an interactive terminal; piped output or TERM=dumb
  degrade to a plain snapshot (same as -once).

UNINSTALL FLAGS:
  --prune           Also delete the config directory

TLS MITM:
  When a location has route rules defined, proxy-router automatically performs
  TLS interception on HTTPS connections to enable path-based routing.
  Run 'proxy-router install-certs' to generate and install the CA certificate.

EXAMPLES:
  proxy-router install
  proxy-router install-certs
  proxy-router run -listen localhost:33000 -config ~/myconf.toml
  proxy-router connections
  proxy-router connections -once
  proxy-router uninstall --prune
  proxy-router completion zsh > ~/.zsh/completions/_proxy-router

PATHS (macOS manual install):
  Binary:      /usr/local/bin/proxy-router
  Config:      /usr/local/etc/proxy-router/config.toml
  CA cert:     /usr/local/etc/proxy-router/cacert.pem
  LaunchAgent: /Library/LaunchAgents/com.wstucco.proxy-router.plist
  Logs:        /usr/local/var/log/proxy-router/proxy-router.{log,err}

PATHS (Linux manual install):
  Binary:      /usr/local/bin/proxy-router
  Config:      /usr/local/etc/proxy-router/config.toml
  CA cert:     /usr/local/etc/proxy-router/cacert.pem
  Systemd:     ~/.config/systemd/user/proxy-router.service
  Logs:        journalctl --user -u proxy-router

PATHS (brew install):
  Binary:      /opt/homebrew/bin/proxy-router
  Config:      /opt/homebrew/etc/proxy-router/config.toml
  CA cert:     /opt/homebrew/etc/proxy-router/cacert.pem
  Service:     managed by brew services
  Logs:        /opt/homebrew/var/log/proxy-router.{log,err}

CONFIG RELOAD:
  Save the config file — changes apply within 1 second.
  Or manually: kill -HUP $(pgrep proxy-router)
`)
}
