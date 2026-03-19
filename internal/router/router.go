package router

import (
	"fmt"
	"strings"

	"github.com/wstucco/proxy-router/internal/config"
)

// Decide evaluates rules top-to-bottom and returns a Decision for the given host.
func Decide(cfg *config.Config, host string) config.Decision {
	ssid := CurrentSSID()

	for _, rule := range cfg.Rules {
		if matches(rule, host, ssid) {
			logEntry(host, ssid, fmt.Sprintf("rule matched → %s", rule.Action), true)
			return config.Decision{Action: rule.Action, DNS: rule.DNS}
		}
	}

	logEntry(host, ssid, fmt.Sprintf("no rule matched → default: %s", cfg.Default), false)
	return config.Decision{Action: cfg.Default}
}

func matches(rule config.Rule, host, ssid string) bool {
	if len(rule.Domains) > 0 {
		if !config.MatchDomain(host, rule.Domains) {
			return false
		}
	}
	if len(rule.SSIDs) > 0 {
		if !matchSSID(ssid, rule.SSIDs) {
			return false
		}
	}
	if len(rule.IPs) > 0 {
		if !matchIP(host, rule.IPs) {
			return false
		}
	}
	return true
}

func matchSSID(current string, list []string) bool {
	current = strings.ToLower(strings.TrimSpace(current))
	for _, s := range list {
		if strings.ToLower(strings.TrimSpace(s)) == current {
			return true
		}
	}
	return false
}

func matchIP(host string, list []string) bool {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	for _, entry := range list {
		if host == entry {
			return true
		}
	}
	return false
}
