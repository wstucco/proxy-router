package router

import (
	"fmt"
	"log"
	"sync"
)

type dedupLogger struct {
	mu      sync.Mutex
	lastMsg string
	count   int
}

var logger = &dedupLogger{}

func logEntry(host, ssid string, action string, ruleMatched bool) {
	var msg string
	if ruleMatched {
		msg = fmt.Sprintf("[router] host=%s ssid=%q → %s", host, ssid, action)
	} else {
		msg = fmt.Sprintf("[router] ssid=%q → %s", ssid, action)
	}

	logger.mu.Lock()
	defer logger.mu.Unlock()

	if msg == logger.lastMsg {
		logger.count++
		return
	}

	// Flush repeat count before printing new message
	if logger.count > 0 {
		log.Printf("[router] Last log repeated %d times", logger.count)
	}

	logger.lastMsg = msg
	logger.count = 0
	log.Print(msg)
}
