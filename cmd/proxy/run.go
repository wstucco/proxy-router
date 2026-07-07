package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wstucco/proxy-router/internal/certmanager"
	"github.com/wstucco/proxy-router/internal/config"
	"github.com/wstucco/proxy-router/internal/hooks"
	pkgLog "github.com/wstucco/proxy-router/internal/log"
	"github.com/wstucco/proxy-router/internal/proxy"
	"github.com/wstucco/proxy-router/internal/router"
)

var mainLog = pkgLog.New(pkgLog.LevelInfo, "main")

// startConfigWatcher polls for config file changes and fires reload.
// Returns immediately; the goroutine runs for the lifetime of the process.
func startConfigWatcher(cfgFile string, reload func()) {
	hashFile := func() string {
		data, err := os.ReadFile(cfgFile)
		if err != nil {
			return ""
		}
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:])
	}
	go func() {
		var lastMod time.Time
		var lastHash string
		if fi, err := os.Stat(cfgFile); err == nil {
			lastMod = fi.ModTime()
			lastHash = hashFile()
		}
		for range time.Tick(time.Second) {
			fi, err := os.Stat(cfgFile)
			if err != nil {
				continue
			}
			if fi.ModTime().After(lastMod) {
				lastMod = fi.ModTime()
				hash := hashFile()
				if hash == lastHash {
					continue
				}
				lastHash = hash
				mainLog.Info("config file changed, reloading...")
				reload()
			}
		}
	}()
}

// wireSIGHUP installs a SIGHUP handler that calls reload.
func wireSIGHUP(reload func()) {
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		for range ch {
			mainLog.Info("SIGHUP received, reloading...")
			reload()
		}
	}()
}

func cmdRun(args []string) {
	p := detectPaths()

	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgFile := fs.String("config", p.cfgFile, "path to config file")
	listen := fs.String("listen", "", "override listen address (e.g. localhost:33000)")
	genCfg := fs.Bool("gen-config", false, "print example config.toml and exit")
	fs.Parse(args)

	if *genCfg {
		fmt.Println(config.DefaultConfig())
		os.Exit(0)
	}

	c, err := config.Load(*cfgFile)
	if err != nil {
		mainLog.Error("failed to load config: %v", err)
		os.Exit(1)
	}
	if *listen != "" {
		c.Listen = *listen
	}

	// Apply log level from config
	if c.Log.Level != "" {
		lvl, err := pkgLog.ParseLevel(c.Log.Level)
		if err != nil {
			mainLog.Error("invalid log level in config: %v", err)
			os.Exit(1)
		}
		pkgLog.SetLevel(lvl)
	}

	// Silence library log output (proxyplease etc.) unless opted out.
	if c.Log.SilenceLibs == nil || *c.Log.SilenceLibs {
		log.SetOutput(io.Discard)
	}

	router.SetConfig(c)

	// Initialize certificate manager for TLS MITM (generates CA on first run)
	mgr, err := certmanager.NewManager(p.caCertFile, p.caKeyFile)
	if err != nil {
		mainLog.Error("certmanager: %v", err)
		os.Exit(1)
	}

	newProxy := func(cfg *config.Config) *proxy.Server {
		s := proxy.New(cfg)
		s.SetCertManager(mgr)
		return s
	}

	var (
		cfgPtr  atomic.Pointer[config.Config]
		srvPtr  atomic.Pointer[proxy.Server]
		reloadMu sync.Mutex
	)
	cfgPtr.Store(c)
	srvPtr.Store(newProxy(c))

	reload := func() {
		reloadMu.Lock()
		defer reloadMu.Unlock()
		oldCfg := cfgPtr.Load()
		newCfg, err := config.Load(*cfgFile)
		if err != nil {
			mainLog.Warn("reload error: %v — keeping current config", err)
			return
		}
		if *listen != "" {
			newCfg.Listen = *listen
		}

		// Apply log settings from the new config.
		if newCfg.Log.Level != "" {
			if lvl, err := pkgLog.ParseLevel(newCfg.Log.Level); err == nil {
				pkgLog.SetLevel(lvl)
			}
		}
		if newCfg.Log.SilenceLibs == nil || *newCfg.Log.SilenceLibs {
			log.SetOutput(io.Discard)
		} else {
			log.SetOutput(os.Stderr)
		}

		cfgPtr.Store(newCfg)
		srvPtr.Store(newProxy(newCfg))
		router.SetConfig(newCfg)
		proxy.ClearNegotiateCache()
		diff := config.ConfigDiff(oldCfg, newCfg)
		mainLog.Info("config reloaded:%s", diff)
	}

	startConfigWatcher(*cfgFile, reload)
	wireSIGHUP(reload)

	go router.StartNetworkListener()

	// Wire location change hooks.
	router.OnLocationChange = func(oldName, newName string, oldLoc, newLoc *config.Location) {
		cfg := cfgPtr.Load()

		// Look up hooks: first try location-specific, fall back to global.
		var oldHooks, newHooks *hooks.LocationHooks

		if oldLoc != nil {
			oldHooks = oldLoc.Hooks
		} else if l, ok := cfg.Locations[oldName]; ok {
			oldHooks = l.Hooks
		}

		if newLoc != nil {
			newHooks = newLoc.Hooks
		} else if l, ok := cfg.Locations[newName]; ok {
			newHooks = l.Hooks
		}

		if oldHooks != nil {
			env := map[string]string{
				"LOCATION":     oldName,
				"ACTION":       "leave",
				"NEW_LOCATION": newName,
			}
			hooks.Execute(oldHooks.OnLeave, env)
		}

		if newHooks != nil {
			env := map[string]string{
				"LOCATION":     newName,
				"ACTION":       "enter",
				"OLD_LOCATION": oldName,
			}
			hooks.Execute(newHooks.OnEnter, env)
		}
	}

	server := &http.Server{
		Addr: c.Listen,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			srvPtr.Load().ServeHTTP(w, r)
		}),
		// Only guard the header read: CONNECT tunnels are long-lived, so
		// read/write deadlines on the whole connection would break them.
		ReadHeaderTimeout: 10 * time.Second,
	}

	mainLog.Info("proxy-router listening on %s", c.Listen)

	// Graceful shutdown on SIGINT/SIGTERM.
	shutdown := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		mainLog.Info("shutting down gracefully...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			mainLog.Error("shutdown error: %v", err)
		}
		close(shutdown)
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		mainLog.Error("server error: %v", err)
		os.Exit(1)
	}

	<-shutdown
	mainLog.Info("proxy-router stopped")
}
