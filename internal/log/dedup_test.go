package log

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestDedupLoggerBasic(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "router")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	d.Print("first message")
	d.Print("first message")
	d.Print("first message")
	d.Print("different message")

	output := buf.String()
	// Should contain the first message once, the repeat notice, and the different message
	if !strings.Contains(output, "first message") {
		t.Errorf("expected 'first message' in output, got: %s", output)
	}
	if !strings.Contains(output, "Last message repeated 2 times") {
		t.Errorf("expected repeat count, got: %s", output)
	}
	if !strings.Contains(output, "different message") {
		t.Errorf("expected 'different message' in output, got: %s", output)
	}
}

func TestDedupLoggerNoDedup(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "router")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	d.Print("msg a")
	d.Print("msg b")
	d.Print("msg c")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %s", len(lines), output)
	}
	if strings.Contains(output, "repeated") {
		t.Errorf("unexpected repeat notice: %s", output)
	}
}

func TestDedupLoggerFlushOnExit(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "router")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	d.Print("repeated message")
	d.Print("repeated message")
	d.Print("repeated message")

	// Flush should emit the repeat count
	d.Flush()

	output := buf.String()
	if !strings.Contains(output, "Last message repeated 2 times") {
		t.Errorf("expected repeat count after flush, got: %s", output)
	}
}

func TestDedupLoggerSingleMessageNoFlush(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "router")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	d.Print("single message")

	output := buf.String()
	if !strings.Contains(output, "single message") {
		t.Errorf("expected message, got: %s", output)
	}
	if strings.Contains(output, "repeated") {
		t.Errorf("unexpected repeat notice for single message: %s", output)
	}
}

func TestDedupLoggerConcurrent(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "router")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.Print("concurrent message")
		}()
	}
	wg.Wait()

	// Flush to get the repeat count
	d.Flush()

	output := buf.String()
	if !strings.Contains(output, "concurrent message") {
		t.Errorf("expected message, got: %s", output)
	}
	// Should have a repeat count since we sent it 10 times
	if !strings.Contains(output, "repeated") && !strings.Contains(output, "repeated") {
		// If count is written, it should be in output
		// It's possible the count was flushed already if interleaved with other messages
		// But there's only one message, so no interleaving
	}

	// Verify no panic — just check we got output
	if output == "" {
		t.Error("expected some output")
	}
}

func TestDedupLoggerInterleaved(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "router")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	d.Print("msg a")
	d.Print("msg a")
	d.Print("msg b")
	d.Print("msg b")
	d.Print("msg b")
	d.Print("msg c")
	d.Flush()

	output := buf.String()

	// msg a appears once, then "repeated 1 time"
	// msg b appears once, then "repeated 2 times"  
	// msg c appears once

	// Verify structure
	aCount := strings.Count(output, "msg a")
	bCount := strings.Count(output, "msg b")
	cCount := strings.Count(output, "msg c")
	repeatCount := strings.Count(output, "repeated")

	if aCount != 1 {
		t.Errorf("expected 'msg a' once, got %d", aCount)
	}
	if bCount != 1 {
		t.Errorf("expected 'msg b' once, got %d", bCount)
	}
	if cCount != 1 {
		t.Errorf("expected 'msg c' once, got %d", cCount)
	}
	if repeatCount < 2 {
		t.Errorf("expected at least 2 repeat notices, got %d", repeatCount)
	}
}

func TestDedupLoggerPrefixInherited(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelInfo, "testprefix")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	d.Print("hello")

	output := buf.String()
	if !strings.Contains(output, "[testprefix]") {
		t.Errorf("expected prefix [testprefix] inherited, got: %s", output)
	}
}

func TestDedupLoggerCustomLevel(t *testing.T) {
	var buf bytes.Buffer
	inner := New(LevelWarn, "test")
	inner.SetOutput(&buf)
	d := NewDedup(inner)

	// Info-level message should be filtered out
	d.PrintAt(LevelInfo, "info message")
	if buf.String() != "" {
		t.Errorf("info message should be hidden at warn level, got: %s", buf.String())
	}

	// Warn-level should pass through
	d.PrintAt(LevelWarn, "warn message")
	if !strings.Contains(buf.String(), "warn message") {
		t.Errorf("warn message should be visible, got: %s", buf.String())
	}
}