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

	"github.com/local/proxy-router/internal/config"
	"github.com/local/proxy-router/internal/proxy"
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

	// Atomic pointer for hot reload
	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(cfg)

	// Hot reload on SIGHUP
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGHUP)
		for range ch {
			newCfg, err := config.Load(*cfgPath)
			if err != nil {
				log.Printf("[reload] error: %v — keeping current config", err)
				continue
			}
			cfgPtr.Store(newCfg)
			log.Printf("[reload] config reloaded: rules=%d", len(newCfg.Rules))
		}
	}()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always read latest config atomically
		current := cfgPtr.Load()
		srv := proxy.New(current)
		srv.ServeHTTP(w, r)
	})

	log.Printf("proxy-router listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
