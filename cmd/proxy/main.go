package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/local/proxy-router/internal/config"
	"github.com/local/proxy-router/internal/proxy"
	"github.com/local/proxy-router/internal/router"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	genConfig := flag.Bool("gen-config", false, "print example config.json and exit")
	flag.Parse()

	if *genConfig {
		fmt.Println(config.DefaultConfig())
		os.Exit(0)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	log.Printf("loaded config: listen=%s upstream=%s default=%s rules=%d",
		cfg.Listen, cfg.Upstream, cfg.Default, len(cfg.Rules))

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(cfg)

	reload := func() {
		newCfg, err := config.Load(*cfgPath)
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
		if fi, err := os.Stat(*cfgPath); err == nil {
			lastMod = fi.ModTime()
		}
		for range time.Tick(500 * time.Millisecond) {
			fi, err := os.Stat(*cfgPath)
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

	log.Printf("proxy-router listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
