package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

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
    'connections:Live view of connections through the proxy'
    'install:Install config, completions, and systemd/LaunchAgent service'
    'install-certs:Generate and install CA certificate for TLS MITM'
    'uninstall:Stop and remove LaunchAgent and completions'
    'completion:Generate shell completion script'
    'version:Print version'
    'help:Show help'
  )

  local -a run_flags
  run_flags=(
    '-config[Path to config file]:file:_files'
    '-listen[Override listen address (e.g. localhost:33000)]:address'
    '-gen-config[Print example config.toml and exit]'
  )

  local -a connections_flags
  connections_flags=(
    '-config[Path to config file]:file:_files'
    '-listen[Daemon address (overrides config)]:address'
    '-interval[Refresh interval (default 1s)]:duration'
    '-once[Print one snapshot and exit]'
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
    run)         _arguments $run_flags ;;
    connections) _arguments $connections_flags ;;
    uninstall)   _arguments $uninstall_flags ;;
    completion)  _describe 'shell' shells ;;
  esac
}

_proxy_router "$@"
`

const bashCompletion = `_proxy_router() {
  local cur prev
  _init_completion || return

  local commands="run connections install install-certs uninstall completion version help"

  case "$prev" in
    proxy-router) COMPREPLY=($(compgen -W "$commands" -- "$cur")); return ;;
    -config)      COMPREPLY=($(compgen -f -- "$cur")); return ;;
    -listen)      return ;;
    completion)   COMPREPLY=($(compgen -W "zsh bash fish" -- "$cur")); return ;;
    uninstall)    COMPREPLY=($(compgen -W "--prune" -- "$cur")); return ;;
    run)          COMPREPLY=($(compgen -W "-config -listen -gen-config" -- "$cur")); return ;;
    connections)  COMPREPLY=($(compgen -W "-config -listen -interval -once" -- "$cur")); return ;;
  esac

  COMPREPLY=($(compgen -W "$commands" -- "$cur"))
}

complete -F _proxy_router proxy-router
`

const fishCompletion = `# proxy-router fish completion

complete -c proxy-router -f

complete -c proxy-router -n "__fish_use_subcommand" -a run          -d "Start the proxy"
complete -c proxy-router -n "__fish_use_subcommand" -a connections  -d "Live view of connections through the proxy"
complete -c proxy-router -n "__fish_use_subcommand" -a install      -d "Install config, completions, and service (LaunchAgent/systemd)"
complete -c proxy-router -n "__fish_use_subcommand" -a install-certs -d "Generate and install CA certificate for TLS MITM"
complete -c proxy-router -n "__fish_use_subcommand" -a uninstall    -d "Stop and remove LaunchAgent and completions"
complete -c proxy-router -n "__fish_use_subcommand" -a completion   -d "Generate shell completion script"
complete -c proxy-router -n "__fish_use_subcommand" -a version      -d "Print version"
complete -c proxy-router -n "__fish_use_subcommand" -a help         -d "Show help"

complete -c proxy-router -n "__fish_seen_subcommand_from run" -l config     -d "Path to config file" -r -F
complete -c proxy-router -n "__fish_seen_subcommand_from run" -l listen     -d "Override listen address" -r
complete -c proxy-router -n "__fish_seen_subcommand_from run" -l gen-config -d "Print example config.toml and exit"

complete -c proxy-router -n "__fish_seen_subcommand_from connections" -l config   -d "Path to config file" -r -F
complete -c proxy-router -n "__fish_seen_subcommand_from connections" -l listen   -d "Daemon address (overrides config)" -r
complete -c proxy-router -n "__fish_seen_subcommand_from connections" -l interval -d "Refresh interval (default 1s)" -r
complete -c proxy-router -n "__fish_seen_subcommand_from connections" -l once     -d "Print one snapshot and exit"

complete -c proxy-router -n "__fish_seen_subcommand_from uninstall" -l prune -d "Also delete the config directory"

complete -c proxy-router -n "__fish_seen_subcommand_from completion" -a "zsh bash fish"
`
