package log

import (
	"fmt"
	"sync"
)

// DedupLogger deduplicates consecutive identical log messages.
// When the same message is logged multiple times in a row, it is printed
// once followed by a "Last message repeated N times" notice when a
// different message arrives or when Flush() is called.
//
// This is useful for high-frequency log paths like router decisions
// where the same decision may repeat many times.
type DedupLogger struct {
	logger *Logger
	mu     sync.Mutex
	lastMsg string
	count   int
}

// NewDedup creates a DedupLogger that writes deduplicated messages
// through the given Logger.
func NewDedup(logger *Logger) *DedupLogger {
	return &DedupLogger{
		logger: logger,
	}
}

// Print logs a message at INFO level, deduplicating consecutive
// identical messages.
func (d *DedupLogger) Print(format string, args ...any) {
	d.PrintAt(LevelInfo, format, args...)
}

// PrintAt logs a message at the given level, deduplicating consecutive
// identical messages.
func (d *DedupLogger) PrintAt(lvl Level, format string, args ...any) {
	if !d.logger.Enabled(lvl) {
		return
	}

	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if msg == d.lastMsg {
		d.count++
		return
	}

	// Flush repeat count before printing new message
	if d.count > 0 {
		d.logger.output(lvl, "Last message repeated %d times", d.count)
	}

	d.lastMsg = msg
	d.count = 0
	d.logger.output(lvl, "%s", msg)
}

// Flush forces any pending repeat count to be written.
// Call this at the end of a request or before shutdown to ensure
// the last repeat notice is emitted.
func (d *DedupLogger) Flush() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.count > 0 {
		d.logger.output(LevelInfo, "Last message repeated %d times", d.count)
		d.count = 0
	}
}