package hooks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	logger "github.com/wstucco/proxy-router/internal/log"
)

var pkgLog = logger.New(logger.LevelDebug, "hooks")

// HookConfig defines a single hook command.
type HookConfig struct {
	Exec    string `toml:"exec"    json:"exec"`
	Timeout string `toml:"timeout,omitempty" json:"timeout,omitempty"`
}

// LocationHooks holds the hooks for a location.
type LocationHooks struct {
	OnEnter *HookConfig `toml:"on_enter,omitempty" json:"on_enter,omitempty"`
	OnLeave *HookConfig `toml:"on_leave,omitempty" json:"on_leave,omitempty"`
}

// Equal returns true if a and b describe the same hooks.
func Equal(a, b *LocationHooks) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return hookConfigEqual(a.OnEnter, b.OnEnter) && hookConfigEqual(a.OnLeave, b.OnLeave)
	}
}

func hookConfigEqual(a, b *HookConfig) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Exec == b.Exec && a.Timeout == b.Timeout
	}
}

// Execute runs a hook command asynchronously. Logs errors but never blocks.
func Execute(hook *HookConfig, env map[string]string) {
	if hook == nil || hook.Exec == "" {
		return
	}

	timeout := 10 * time.Second
	if hook.Timeout != "" {
		if d, err := time.ParseDuration(hook.Timeout); err == nil {
			timeout = d
		} else {
			pkgLog.Warn("invalid hook timeout %q, using 10s", hook.Timeout)
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", hook.Exec)
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				pkgLog.Warn("hook timed out after %v: %s", timeout, hook.Exec)
			} else {
				pkgLog.Warn("hook failed: %s — %v", hook.Exec, err)
			}
			if len(out) > 0 {
				pkgLog.Debug("hook output: %s", strings.TrimSpace(string(out)))
			}
			return
		}
		if len(out) > 0 {
			pkgLog.Info("hook OK: %s", strings.TrimSpace(string(out)))
		}
	}()
}
