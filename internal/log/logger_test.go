package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerLevels(t *testing.T) {
	tests := []struct {
		name      string
		level     Level
		callLevel Level
		format    string
		args      []any
		wantLog   bool
	}{
		{"debug at debug", LevelDebug, LevelDebug, "test %d", []any{1}, true},
		{"debug at info", LevelInfo, LevelDebug, "test %d", []any{2}, false},
		{"info at debug", LevelDebug, LevelInfo, "info msg", nil, true},
		{"info at info", LevelInfo, LevelInfo, "info msg", nil, true},
		{"info at warn", LevelWarn, LevelInfo, "info msg", nil, false},
		{"warn at warn", LevelWarn, LevelWarn, "warn msg", nil, true},
		{"warn at error", LevelError, LevelWarn, "warn msg", nil, false},
		{"error at error", LevelError, LevelError, "error msg", nil, true},
		{"error at debug", LevelDebug, LevelError, "error msg", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(tt.level, "test")
			l.SetOutput(&buf)

			switch tt.callLevel {
			case LevelDebug:
				l.Debug(tt.format, tt.args...)
			case LevelInfo:
				l.Info(tt.format, tt.args...)
			case LevelWarn:
				l.Warn(tt.format, tt.args...)
			case LevelError:
				l.Error(tt.format, tt.args...)
			}

			got := buf.String()
			if tt.wantLog && got == "" {
				t.Error("expected log output, got empty")
			}
			if !tt.wantLog && got != "" {
				t.Errorf("expected no log output, got: %s", got)
			}
		})
	}
}

func TestLoggerPrefix(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelInfo, "mypkg")
	l.SetOutput(&buf)

	l.Info("hello %s", "world")

	got := buf.String()
	if !strings.Contains(got, "[mypkg]") {
		t.Errorf("expected prefix [mypkg] in output, got: %s", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Errorf("expected message body, got: %s", got)
	}
}

func TestLoggerMultipleArgs(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelInfo, "test")
	l.SetOutput(&buf)

	l.Info("a=%d b=%s c=%v", 42, "hi", true)

	got := buf.String()
	if !strings.Contains(got, "a=42 b=hi c=true") {
		t.Errorf("expected formatted args, got: %s", got)
	}
}

func TestSetLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(LevelWarn, "test")
	l.SetOutput(&buf)

	l.Info("should be hidden")
	if buf.String() != "" {
		t.Errorf("info should be hidden at warn level, got: %s", buf.String())
	}

	l.Warn("visible")
	if !strings.Contains(buf.String(), "visible") {
		t.Errorf("warn should be visible, got: %s", buf.String())
	}
}

func TestLevelParsing(t *testing.T) {
	tests := []struct {
		input string
		want  Level
		err   bool
	}{
		{"debug", LevelDebug, false},
		{"DEBUG", LevelDebug, false},
		{"Debug", LevelDebug, false},
		{"info", LevelInfo, false},
		{"warn", LevelWarn, false},
		{"WARN", LevelWarn, false},
		{"error", LevelError, false},
		{"ERROR", LevelError, false},
		{"invalid", LevelInfo, true},
		{"", LevelInfo, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseLevel(tt.input)
			if tt.err && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.err && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoggerTypedMethods(t *testing.T) {
	tests := []struct {
		name    string
		level   Level
		method  func(l *Logger, msg string)
		prefix  string
		levelTag string
	}{
		{"Debug method", LevelDebug, func(l *Logger, msg string) { l.Debug("%s", msg) }, "[DBG]", "[DBG]"},
		{"Info method", LevelInfo, func(l *Logger, msg string) { l.Info("%s", msg) }, "[INF]", "[INF]"},
		{"Warn method", LevelWarn, func(l *Logger, msg string) { l.Warn("%s", msg) }, "[WRN]", "[WRN]"},
		{"Error method", LevelError, func(l *Logger, msg string) { l.Error("%s", msg) }, "[ERR]", "[ERR]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(tt.level, "pkg")
			l.SetOutput(&buf)

			tt.method(l, "test message")

			got := buf.String()
			if got == "" {
				t.Fatal("expected output, got empty")
			}
			if !strings.Contains(got, tt.levelTag) {
				t.Errorf("expected level tag %q, got: %s", tt.levelTag, got)
			}
			if !strings.Contains(got, "[pkg]") {
				t.Errorf("expected prefix [pkg], got: %s", got)
			}
			if !strings.Contains(got, "test message") {
				t.Errorf("expected message body, got: %s", got)
			}
		})
	}
}

func TestGlobalLevel(t *testing.T) {
	// Raise global level to Info to hide debug
	SetLevel(LevelInfo)

	var buf bytes.Buffer
	l := New(LevelDebug, "test")
	l.SetOutput(&buf)

	// With global LevelInfo, debug should be hidden
	if l.Enabled(LevelDebug) {
		t.Error("Debug should not be enabled when global level is Info")
	}
	l.Debug("hidden")
	if buf.String() != "" {
		t.Errorf("expected no debug output at global info level, got: %s", buf.String())
	}

	// Lower global level to debug
	SetLevel(LevelDebug)
	if !l.Enabled(LevelDebug) {
		t.Error("Debug should be enabled when global level is Debug")
	}
	buf.Reset()
	l.Debug("visible now")
	if !strings.Contains(buf.String(), "visible now") {
		t.Errorf("expected debug output after level change, got: %s", buf.String())
	}

	// Reset to default
	SetLevel(LevelDebug)
}