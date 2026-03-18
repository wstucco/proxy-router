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
	"strings"
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

const plistLabel = "com.wstucco.proxy-router"
const plistFile = "com.wstucco.proxy-router.plist"
const binaryName = "proxy-router"

// ─── prefix detection ─────────────────────────────────────────────────────────

type installMode int

const (
	modeBrew   installMode = iota // running from /opt/homebrew or /usr/local/opt
	modeManual                    // manual install to /usr/local
)

type paths struct {
	mode    installMode
	prefix  string // /opt/homebrew or /usr/local
	bin     string
	cfgDir  string
	cfgFile string
	logDir  string
	plist   string // only set for manual installs
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
		p.plist = filepath.Join("/Library", "LaunchAgents", plistFile)
	}

	p.cfgFile = filepath.Join(p.cfgDir, "config.json")
	return p
}

// ─── plist template ───────────────────────────────────────────────────────────

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

// ─── install ──────────────────────────────────────────────────────────────────

func cmdInstall() {
	p := detectPaths()

	// 1. Write default config if not present
	if err := os.MkdirAll(p.cfgDir, 0755); err != nil {
		log.Fatalf("install: creating config dir %s: %v", p.cfgDir, err)
	}
	if _, err := os.Stat(p.cfgFile); os.IsNotExist(err) {
		if err := os.WriteFile(p.cfgFile, []byte(config.DefaultConfig()), 0644); err != nil {
			log.Fatalf("install: writing config: %v", err)
		}
		fmt.Printf("✓ config    → %s (default, please edit)\n", p.cfgFile)
	} else {
		fmt.Printf("✓ config    → %s (already exists, skipped)\n", p.cfgFile)
	}

	// 2. Install completions
	installCompletions()

	// 3. Register LaunchAgent (manual only — brew uses `brew services`)
	if p.mode == modeManual {
		if err := os.MkdirAll(p.logDir, 0755); err != nil {
			log.Fatalf("install: creating log dir: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(p.plist), 0755); err != nil {
			log.Fatalf("install: creating LaunchAgents dir: %v", err)
		}
		f, err := os.Create(p.plist)
		if err != nil {
			log.Fatalf("install: creating plist (try with sudo): %v", err)
		}
		err = plistTemplate.Execute(f, map[string]string{
			"Label":   plistLabel,
			"Bin":     p.bin,
			"CfgFile": p.cfgFile,
			"LogDir":  p.logDir,
		})
		f.Close()
		if err != nil {
			log.Fatalf("install: writing plist: %v", err)
		}
		fmt.Printf("✓ plist     → %s\n", p.plist)

		out, err := exec.Command("launchctl", "load", "-w", p.plist).CombinedOutput()
		if err != nil {
			log.Fatalf("install: launchctl load: %v\n%s", err, out)
		}
		fmt.Println("✓ launchctl load → proxy-router started")
		fmt.Printf("\nLogs: %s/proxy-router.log\n", p.logDir)
	} else {
		fmt.Println()
		fmt.Println("Homebrew install detected — skipping LaunchAgent.")
		fmt.Println("To start as a service:")
		fmt.Println("  brew services start proxy-router")
	}

	fmt.Printf("\nEdit config: %s\n", p.cfgFile)
}

// ─── uninstall ────────────────────────────────────────────────────────────────

func cmdUninstall(prune bool) {
	p := detectPaths()

	if p.mode == modeManual {
		exec.Command("launchctl", "unload", "-w", p.plist).Run()
		fmt.Println("✓ launchctl unload")
		removeFile(p.plist, "plist")
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

// ─── completion install/remove ────────────────────────────────────────────────

type shellCompletion struct {
	name    string
	binary  string
	dir     func() string
	file    string
	content string
	notice  string
}

func completionDefs() []shellCompletion {
	home, _ := os.UserHomeDir()
	return []shellCompletion{
		{
			name:    "zsh",
			binary:  "zsh",
			dir:     func() string { return filepath.Join(home, ".zsh", "completions") },
			file:    "_proxy-router",
			content: zshCompletion,
			notice: "  Add to ~/.zshrc if not already present:\n" +
				"    fpath=(~/.zsh/completions $fpath)\n" +
				"    autoload -Uz compinit && compinit",
		},
		{
			name:    "bash",
			binary:  "bash",
			dir:     func() string { return filepath.Join(home, ".local", "share", "bash-completion", "completions") },
			file:    "proxy-router",
			content: bashCompletion,
			notice: "  Requires bash-completion v2. Add to ~/.bash_profile if not already present:\n" +
				"    [[ -r \"$(brew --prefix)/etc/profile.d/bash_completion.sh\" ]] && \\\n" +
				"      . \"$(brew --prefix)/etc/profile.d/bash_completion.sh\"",
		},
		{
			name:    "fish",
			binary:  "fish",
			dir:     func() string { return filepath.Join(home, ".config", "fish", "completions") },
			file:    "proxy-router.fish",
			content: fishCompletion,
			notice:  "",
		},
	}
}

func installCompletions() {
	fmt.Println()
	any := false
	for _, s := range completionDefs() {
		if _, err := exec.LookPath(s.binary); err != nil {
			continue
		}
		dir := s.dir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("  ! %s completion: could not create dir: %v\n", s.name, err)
			continue
		}
		dest := filepath.Join(dir, s.file)
		if err := os.WriteFile(dest, []byte(s.content), 0644); err != nil {
			fmt.Printf("  ! %s completion: could not write: %v\n", s.name, err)
			continue
		}
		fmt.Printf("✓ %s completion → %s\n", s.name, dest)
		if s.notice != "" {
			fmt.Println(s.notice)
		}
		any = true
	}
	if !any {
		fmt.Println("  No supported shells detected, skipping completions.")
		fmt.Println("  Run `proxy-router completion <zsh|bash|fish>` to generate manually.")
	}
}

func removeCompletions() {
	for _, s := range completionDefs() {
		dest := filepath.Join(s.dir(), s.file)
		if err := os.Remove(dest); err == nil {
			fmt.Printf("✓ %s completion removed → %s\n", s.name, dest)
		}
	}
}

// ─── run ──────────────────────────────────────────────────────────────────────

func cmdRun(args []string) {
	p := detectPaths()

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgFile := fs.String("config", p.cfgFile, "path to config file")
	listen := fs.String("listen", "", "override listen address (e.g. localhost:33000)")
	genCfg := fs.Bool("gen-config", false, "print example config.json and exit")
	fs.Parse(args)

	if *genCfg {
		fmt.Println(config.DefaultConfig())
		os.Exit(0)
	}

	c, err := config.Load(*cfgFile)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if *listen != "" {
		c.Listen = *listen
	}
	log.Printf("loaded config: listen=%s upstream=%s default=%s rules=%d",
		c.Listen, c.Upstream, c.Default, len(c.Rules))

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(c)

	reload := func() {
		newCfg, err := config.Load(*cfgFile)
		if err != nil {
			log.Printf("[reload] error: %v — keeping current config", err)
			return
		}
		if *listen != "" {
			newCfg.Listen = *listen
		}
		cfgPtr.Store(newCfg)
		log.Printf("[reload] config reloaded: rules=%d", len(newCfg.Rules))
	}

	go func() {
		var lastMod time.Time
		if fi, err := os.Stat(*cfgFile); err == nil {
			lastMod = fi.ModTime()
		}
		for range time.Tick(time.Second) {
			fi, err := os.Stat(*cfgFile)
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

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		for range ch {
			log.Printf("[reload] SIGHUP received, reloading...")
			reload()
		}
	}()

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

// ─── help ─────────────────────────────────────────────────────────────────────

func printHelp() {
	fmt.Printf("proxy-router version %s\n\n", version)
	fmt.Print(`A local proxy that routes connections to an upstream or direct based on configurable rules.

USAGE:
  proxy-router <command> [flags]

COMMANDS:
  run         Start the proxy
  install     Write default config, install completions, register LaunchAgent (manual install only)
  uninstall   Deregister LaunchAgent, remove completions (config kept by default)
  completion  Generate shell completion script (zsh, bash, fish)
  version     Print version
  help        Show this help

RUN FLAGS:
  -config <path>    Path to config file
                    default (brew):   /opt/homebrew/etc/proxy-router/config.json
                    default (manual): /usr/local/etc/proxy-router/config.json
  -listen <addr>    Override listen address (e.g. localhost:33000)
  -gen-config       Print an example config.json and exit

UNINSTALL FLAGS:
  --prune           Also delete the config directory

EXAMPLES:
  proxy-router install
  proxy-router run -listen localhost:33000 -config ~/myconf.json
  proxy-router uninstall --prune
  proxy-router completion zsh > ~/.zsh/completions/_proxy-router

PATHS (manual install):
  Binary:      /usr/local/bin/proxy-router
  Config:      /usr/local/etc/proxy-router/config.json
  LaunchAgent: /Library/LaunchAgents/com.wstucco.proxy-router.plist
  Logs:        /usr/local/var/log/proxy-router/proxy-router.{log,err}

PATHS (brew install):
  Binary:      /opt/homebrew/bin/proxy-router
  Config:      /opt/homebrew/etc/proxy-router/config.json
  Service:     managed by brew services
  Logs:        /opt/homebrew/var/log/proxy-router.{log,err}

CONFIG RELOAD:
  Save the config file — changes apply within 1 second.
  Or manually: kill -HUP $(pgrep proxy-router)
`)
}

// ─── completion scripts ───────────────────────────────────────────────────────

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
    'install:Install config, completions, and LaunchAgent'
    'uninstall:Stop and remove LaunchAgent and completions'
    'completion:Generate shell completion script'
    'version:Print version'
    'help:Show help'
  )

  local -a run_flags
  run_flags=(
    '-config[Path to config file]:file:_files'
    '-listen[Override listen address (e.g. localhost:33000)]:address'
    '-gen-config[Print example config.json and exit]'
  )

  local -a uninstall_flags
  uninstall_flags=('--prune[Also delete the config directory]')

  local -a shells
  shells=(zsh bash fish)

  if (( CURRENT == 2 )); then
    _describe 'command' commands
    return
  fi

  case ${words[2]} in
    run)        _arguments $run_flags ;;
    uninstall)  _arguments $uninstall_flags ;;
    completion) _describe 'shell' shells ;;
  esac
}

_proxy_router "$@"
`

const bashCompletion = `_proxy_router() {
  local cur prev
  _init_completion || return

  local commands="run install uninstall completion version help"

  case "$prev" in
    proxy-router) COMPREPLY=($(compgen -W "$commands" -- "$cur")); return ;;
    -config)      COMPREPLY=($(compgen -f -- "$cur")); return ;;
    -listen)      return ;;
    completion)   COMPREPLY=($(compgen -W "zsh bash fish" -- "$cur")); return ;;
    uninstall)    COMPREPLY=($(compgen -W "--prune" -- "$cur")); return ;;
    run)          COMPREPLY=($(compgen -W "-config -listen -gen-config" -- "$cur")); return ;;
  esac

  COMPREPLY=($(compgen -W "$commands" -- "$cur"))
}

complete -F _proxy_router proxy-router
`

const fishCompletion = `# proxy-router fish completion

complete -c proxy-router -f

complete -c proxy-router -n "__fish_use_subcommand" -a run        -d "Start the proxy"
complete -c proxy-router -n "__fish_use_subcommand" -a install    -d "Install config, completions, and LaunchAgent"
complete -c proxy-router -n "__fish_use_subcommand" -a uninstall  -d "Stop and remove LaunchAgent and completions"
complete -c proxy-router -n "__fish_use_subcommand" -a completion -d "Generate shell completion script"
complete -c proxy-router -n "__fish_use_subcommand" -a version    -d "Print version"
complete -c proxy-router -n "__fish_use_subcommand" -a help       -d "Show help"

complete -c proxy-router -n "__fish_seen_subcommand_from run" -l config     -d "Path to config file" -r -F
complete -c proxy-router -n "__fish_seen_subcommand_from run" -l listen     -d "Override listen address" -r
complete -c proxy-router -n "__fish_seen_subcommand_from run" -l gen-config -d "Print example config.json and exit"

complete -c proxy-router -n "__fish_seen_subcommand_from uninstall" -l prune -d "Also delete the config directory"

complete -c proxy-router -n "__fish_seen_subcommand_from completion" -a "zsh bash fish"
`

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		cmdRun(nil)
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
	case "completion":
		cmdCompletion(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("proxy-router version %s\n", version)
	case "help", "-h", "--help":
		printHelp()
	default:
		cmdRun(os.Args[1:])
	}
}
