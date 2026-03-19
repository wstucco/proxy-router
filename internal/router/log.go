package router

import (
	"fmt"
	"log"
	"sync"
)

const dedupThreshold = 100

type dedupLogger struct {
	mu      sync.Mutex
	lastKey string
	count   int
}

var logger = &dedupLogger{}

func logEntry(host, ssid string, action string, ruleMatched bool) {
	key := fmt.Sprintf("ssid=%q → %s", ssid, action)

	logger.mu.Lock()
	defer logger.mu.Unlock()

	if key == logger.lastKey {
		logger.count++
		if logger.count%dedupThreshold == 0 {
			log.Printf("[router] ... %d times: %s", logger.count, key)
		}
		return
	}

	// Flush leftover count for previous key
	if logger.count > 0 && logger.count%dedupThreshold != 0 {
		log.Printf("[router] ... %d times: %s", logger.count, logger.lastKey)
	}

	logger.lastKey = key
	logger.count = 0

	if ruleMatched {
		log.Printf("[router] host=%s %s", host, key)
	} else {
		log.Printf("[router] %s", key)
	}
}
