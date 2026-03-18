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

	// 6. Install shell completions
	installCompletions()
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

	// 4. Remove completion files
	removeCompletions()

	// 5. Optionally remove config
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

func removeCompletions() {
	home := userHome()
	files := []string{
		filepath.Join(home, ".zsh", "completions", "_proxy-router"),
		filepath.Join(home, ".local", "share", "bash-completion", "completions", "proxy-router"),
		filepath.Join(home, ".config", "fish", "completions", "proxy-router.fish"),
	}
	for _, f := range files {
		if err := os.Remove(f); err == nil {
			fmt.Printf("✓ completion removed → %s\n", f)
		}
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
  completion  Generate shell completion script (zsh, bash, fish)
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
  proxy-router completion zsh > $(brew --prefix)/share/zsh/site-functions/_proxy-router
  proxy-router completion bash > $(brew --prefix)/etc/bash_completion.d/proxy-router
  proxy-router completion fish > ~/.config/fish/completions/proxy-router.fish

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
	case "completion":
		cmdCompletion(os.Args[2:])
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

// ─── shell completion ─────────────────────────────────────────────────────────

func cmdCompletion(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: proxy-router completion <zsh|bash|fish>")
		os.Exit(1)
	}
	switch args[0] {
	case "zsh":
		fmt.Print(zshCompletion)
	case "bash":
		fmt.Print(bashCompletion)
	case "fish":
		fmt.Print(fishCompletion)
	default:
		fmt.Fprintf(os.Stderr, "unknown shell %q — supported: zsh, bash, fish\n", args[0])
		os.Exit(1)
	}
}

const zshCompletion = `#compdef proxy-router

_proxy_router() {
  local -a commands
  commands=(
    'run:Start the proxy'
    'install:Install binary, config, and LaunchAgent'
    'uninstall:Stop and remove binary and LaunchAgent'
    'completion:Generate shell completion script'
    'version:Print version'
    'help:Show help'
  )

  local -a run_flags
  run_flags=(
    '-config[Path to config file]:file:_files'
    '-gen-config[Print example config.json and exit]'
  )

  local -a uninstall_flags
  uninstall_flags=(
    '--prune[Also delete the config directory]'
  )

  local -a shells
  shells=(zsh bash fish)

  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi

  case ${words[2]} in
    run)
      _arguments $run_flags ;;
    uninstall)
      _arguments $uninstall_flags ;;
    completion)
      _describe 'shell' shells ;;
  esac
}

_proxy_router "$@"
`

const bashCompletion = `_proxy_router() {
  local cur prev words
  _init_completion || return

  local commands="run install uninstall completion version help"

  case "$prev" in
    proxy-router)
      COMPREPLY=($(compgen -W "$commands" -- "$cur"))
      return ;;
    -config)
      COMPREPLY=($(compgen -f -- "$cur"))
      return ;;
    completion)
      COMPREPLY=($(compgen -W "zsh bash fish" -- "$cur"))
      return ;;
    uninstall)
      COMPREPLY=($(compgen -W "--prune" -- "$cur"))
      return ;;
    run)
      COMPREPLY=($(compgen -W "-config -gen-config" -- "$cur"))
      return ;;
  esac

  COMPREPLY=($(compgen -W "$commands" -- "$cur"))
}

complete -F _proxy_router proxy-router
`

const fishCompletion = `# proxy-router fish completion

set -l commands run install uninstall completion version help

# disable file completion by default
complete -c proxy-router -f

# subcommands
complete -c proxy-router -n "__fish_use_subcommand" -a run        -d "Start the proxy"
complete -c proxy-router -n "__fish_use_subcommand" -a install    -d "Install binary, config, and LaunchAgent"
complete -c proxy-router -n "__fish_use_subcommand" -a uninstall  -d "Stop and remove binary and LaunchAgent"
complete -c proxy-router -n "__fish_use_subcommand" -a completion -d "Generate shell completion script"
complete -c proxy-router -n "__fish_use_subcommand" -a version    -d "Print version"
complete -c proxy-router -n "__fish_use_subcommand" -a help       -d "Show help"

# run flags
complete -c proxy-router -n "__fish_seen_subcommand_from run" -l config     -d "Path to config file" -r -F
complete -c proxy-router -n "__fish_seen_subcommand_from run" -l gen-config -d "Print example config.json and exit"

# uninstall flags
complete -c proxy-router -n "__fish_seen_subcommand_from uninstall" -l prune -d "Also delete the config directory"

# completion shells
complete -c proxy-router -n "__fish_seen_subcommand_from completion" -a "zsh bash fish"
`

// ─── completion install ───────────────────────────────────────────────────────

type shellCompletion struct {
	name    string
	binary  string // binary to check for existence
	dir     func() string
	file    string
	content string
	notice  string // what to add to shell config, if anything
}

func installCompletions() {
	home := userHome()

	shells := []shellCompletion{
		{
			name:   "zsh",
			binary: "zsh",
			dir: func() string {
				// Use XDG-friendly user completions dir
				return filepath.Join(home, ".zsh", "completions")
			},
			file:    "_proxy-router",
			content: zshCompletion,
			notice: "  Add to ~/.zshrc if not already present:\n" +
				"    fpath=(~/.zsh/completions $fpath)\n" +
				"    autoload -Uz compinit && compinit",
		},
		{
			name:   "bash",
			binary: "bash",
			dir: func() string {
				// XDG user bash-completion dir (bash-completion v2 picks this up automatically)
				return filepath.Join(home, ".local", "share", "bash-completion", "completions")
			},
			file:    "proxy-router",
			content: bashCompletion,
			notice: "  Requires bash-completion v2. Add to ~/.bash_profile if not already present:\n" +
				"    [[ -r \"$(brew --prefix)/etc/profile.d/bash_completion.sh\" ]] && \\\n" +
				"      . \"$(brew --prefix)/etc/profile.d/bash_completion.sh\"",
		},
		{
			name:   "fish",
			binary: "fish",
			dir: func() string {
				return filepath.Join(home, ".config", "fish", "completions")
			},
			file:    "proxy-router.fish",
			content: fishCompletion,
			notice:  "", // fish picks up completions automatically, no config needed
		},
	}

	fmt.Println()
	anyInstalled := false

	for _, s := range shells {
		// Skip if shell is not installed
		if _, err := exec.LookPath(s.binary); err != nil {
			continue
		}

		dir := s.dir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("  ! %s completion: could not create dir %s: %v\n", s.name, dir, err)
			continue
		}

		dest := filepath.Join(dir, s.file)
		if err := os.WriteFile(dest, []byte(s.content), 0644); err != nil {
			fmt.Printf("  ! %s completion: could not write %s: %v\n", s.name, dest, err)
			continue
		}

		fmt.Printf("✓ %s completion → %s\n", s.name, dest)
		if s.notice != "" {
			fmt.Println(s.notice)
		}
		anyInstalled = true
	}

	if !anyInstalled {
		fmt.Println("  No supported shells detected, skipping completions.")
		fmt.Println("  Run `proxy-router completion <zsh|bash|fish>` to generate manually.")
	}
}
