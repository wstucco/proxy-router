// Package log provides a lightweight leveled logging system for proxy-router.
//
// Each package creates its own Logger with a prefix and level.
// A global level can be set at startup; individual loggers use the maximum
// of their own level and the global level.
//
// Usage:
//
//	var log = logger.New(log.Info, "proxy")
//	log.Info("started on %s", addr)
//	log.WithCorrelation("abc123").Debug("CONNECT %s", host)
package log

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
)

// Level represents a log severity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DBG",
	LevelInfo:  "INF",
	LevelWarn:  "WRN",
	LevelError: "ERR",
}

var levelValues = map[string]Level{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

// ParseLevel parses a case-insensitive level string.
// Returns an error if the string is not a valid level name.
func ParseLevel(s string) (Level, error) {
	l, ok := levelValues[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return LevelInfo, fmt.Errorf("unknown log level %q (valid: debug, info, warn, error)", s)
	}
	return l, nil
}

// globalLevel is the minimum level across all loggers.
// Can be set via SetLevel, env var PROXY_ROUTER_LOG_LEVEL, or config.
var globalLevel atomic.Int64

func init() {
	// Default global level is Debug — package-level loggers control their own filtering.
	// The global level can be raised at startup via SetLevel, env var, or config.
	globalLevel.Store(int64(LevelDebug))
}

// SetLevel sets the global minimum log level.
// All loggers will only emit messages at or above this level.
func SetLevel(l Level) {
	globalLevel.Store(int64(l))
}

// GetLevel returns the current global log level.
func GetLevel() Level {
	return Level(globalLevel.Load())
}

// Logger is a leveled logger with a prefix.
// Create one via New() for each package.
// Thread-safe.
type Logger struct {
	level       Level
	prefix      string
	out         io.Writer
	correlation string // optional, set via WithCorrelation
	logger      *log.Logger
}

// New creates a new Logger with the given minimum level and prefix.
// The prefix is displayed as [prefix] in log output.
func New(level Level, prefix string) *Logger {
	return &Logger{
		level:  level,
		prefix: prefix,
		out:    os.Stderr,
		logger: log.New(os.Stderr, "", log.LstdFlags),
	}
}

// SetOutput sets the output destination for the logger.
func (l *Logger) SetOutput(w io.Writer) {
	l.out = w
	l.logger = log.New(w, "", log.LstdFlags)
}

// WithCorrelation returns a copy of the logger with a correlation ID.
// Use this for tracing a single request across multiple log lines.
//
//	l.WithCorrelation("abc123").Info("CONNECT %s", host)
func (l *Logger) WithCorrelation(id string) *Logger {
	return &Logger{
		level:       l.level,
		prefix:      l.prefix,
		out:         l.out,
		correlation: id,
		logger:      l.logger,
	}
}

// Enabled reports whether messages at the given level will be emitted.
func (l *Logger) Enabled(lvl Level) bool {
	effective := l.level
	if g := Level(globalLevel.Load()); g > effective {
		effective = g
	}
	return lvl >= effective
}

// Debug logs a message at DEBUG level.
func (l *Logger) Debug(format string, args ...any) {
	if !l.Enabled(LevelDebug) {
		return
	}
	l.output(LevelDebug, format, args...)
}

// Info logs a message at INFO level.
func (l *Logger) Info(format string, args ...any) {
	if !l.Enabled(LevelInfo) {
		return
	}
	l.output(LevelInfo, format, args...)
}

// Warn logs a message at WARN level.
func (l *Logger) Warn(format string, args ...any) {
	if !l.Enabled(LevelWarn) {
		return
	}
	l.output(LevelWarn, format, args...)
}

// Error logs a message at ERROR level.
func (l *Logger) Error(format string, args ...any) {
	if !l.Enabled(LevelError) {
		return
	}
	l.output(LevelError, format, args...)
}

func (l *Logger) output(lvl Level, format string, args ...any) {
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	levelTag := levelNames[lvl]
	corr := ""
	if l.correlation != "" {
		corr = " req=" + l.correlation
	}

	l.logger.Printf("[%s]%s[%s] %s", l.prefix, corr, levelTag, msg)
}
