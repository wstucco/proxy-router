package router

import (
	"fmt"

	logger "github.com/wstucco/proxy-router/internal/log"
)

var pkgLog = logger.New(logger.LevelDebug, "router")
var dedupLog = logger.NewDedup(pkgLog)

// logEntry logs a router decision, deduplicating consecutive identical entries.
// ruleMatched controls whether host is included in the message for privacy.
func logEntry(host, ssid string, action string, ruleMatched bool) {
	var msg string
	if ruleMatched {
		msg = fmt.Sprintf("host=%s ssid=%q → %s", host, ssid, action)
	} else {
		msg = fmt.Sprintf("ssid=%q → %s", ssid, action)
	}
	dedupLog.Print(msg)
}

// FlushLog flushes any pending dedup repeat count.
// Call this during shutdown to ensure no repeat counts are lost.
func FlushLog() {
	dedupLog.Flush()
}