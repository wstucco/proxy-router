package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wstucco/proxy-router/internal/proxy"
)

func TestDecideMode(t *testing.T) {
	tests := []struct {
		name                   string
		isTTY                  bool
		term                   string
		plain, enhanced, once  bool
		want                   connMode
	}{
		{"interactive default is enhanced", true, "xterm-256color", false, false, false, modeEnhanced},
		{"pipe degrades to snapshot", false, "xterm-256color", false, false, false, modeOnce},
		{"dumb term degrades to snapshot", true, "dumb", false, false, false, modeOnce},
		{"empty term degrades to snapshot", true, "", false, false, false, modeOnce},
		{"-plain wins over detection", false, "dumb", true, false, false, modePlain},
		{"-enhanced wins over detection", false, "", false, true, false, modeEnhanced},
		{"-once wins over everything", true, "xterm", true, true, true, modeOnce},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideMode(tt.isTTY, tt.term, tt.plain, tt.enhanced, tt.once)
			if got != tt.want {
				t.Errorf("decideMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTuiModelFilterAndScroll(t *testing.T) {
	m := &tuiModel{rows: map[uint64]*tuiRow{}, height: 8} // bodyHeight = 4

	for i := uint64(1); i <= 10; i++ {
		m.upsert(proxy.ConnInfo{ID: i, Active: true, Dest: fmt.Sprintf("h%d:443", i)})
	}
	m.apply(connEvent{Type: "close", Conn: &proxy.ConnInfo{ID: 1, Dest: "h1:443"}})
	m.apply(connEvent{Type: "close", Conn: &proxy.ConnInfo{ID: 2, Dest: "h2:443"}})

	if got := len(m.visibleRows()); got != 10 {
		t.Errorf("filter all: %d rows, want 10", got)
	}
	m.activeOnly = true
	if got := len(m.visibleRows()); got != 8 {
		t.Errorf("filter active: %d rows, want 8", got)
	}
	// Active rows sort newest first.
	if first := m.visibleRows()[0].info.ID; first != 10 {
		t.Errorf("first row ID %d, want 10", first)
	}

	// Scroll clamps to the filtered set.
	m.scroll = 100
	m.clampScroll()
	if want := 8 - m.bodyHeight(); m.scroll != want {
		t.Errorf("scroll clamped to %d, want %d", m.scroll, want)
	}
	m.scroll = -5
	m.clampScroll()
	if m.scroll != 0 {
		t.Errorf("negative scroll not clamped: %d", m.scroll)
	}
}

// A polling snapshot must merge, not replace: existing rows keep their
// identity and rows missing from the snapshot get closed.
func TestTuiModelSnapshotMerge(t *testing.T) {
	m := &tuiModel{rows: map[uint64]*tuiRow{}}
	m.apply(connEvent{Type: "snapshot", Snapshot: []proxy.ConnInfo{
		{ID: 1, Active: true, Dest: "a:443"},
		{ID: 2, Active: true, Dest: "b:443"},
	}})
	firstSeen := m.rows[1].seenAt

	m.apply(connEvent{Type: "snapshot", Snapshot: []proxy.ConnInfo{
		{ID: 1, Active: true, Dest: "a:443", BytesUp: 10},
	}})
	if m.rows[1].seenAt != firstSeen {
		t.Error("snapshot merge re-flashed an existing row")
	}
	if m.rows[1].info.BytesUp != 10 {
		t.Error("snapshot merge did not update counters")
	}
	if m.rows[2].closedAt.IsZero() {
		t.Error("row missing from snapshot not marked closed")
	}
}

func TestNormalizeListen(t *testing.T) {
	if got := normalizeListen(":1337"); got != "localhost:1337" {
		t.Errorf("normalizeListen(\":1337\") = %q", got)
	}
	if got := normalizeListen("127.0.0.1:9999"); got != "127.0.0.1:9999" {
		t.Errorf("normalizeListen passthrough broken: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0B"}, {512, "512B"}, {2048, "2.0KB"},
		{5 << 20, "5.0MB"}, {3 << 30, "3.0GB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.n); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestHumanAge(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "5s"},
		{90 * time.Second, "1m30s"},
		{2*time.Hour + 5*time.Minute, "2h5m"},
	}
	for _, tt := range tests {
		if got := humanAge(tt.d); got != tt.want {
			t.Errorf("humanAge(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestPad(t *testing.T) {
	if got := pad("abc", 5); got != "abc  " {
		t.Errorf("pad right: %q", got)
	}
	if got := pad("abcdefgh", 5); got != "abcd…" {
		t.Errorf("pad truncate: %q", got)
	}
}

func TestRenderTable(t *testing.T) {
	conns := []proxy.ConnInfo{
		{ID: 1, Active: false, Process: "curl", PID: 42, Dest: "closed.example.com:80", Location: "default", Via: "direct", DurationMS: 100},
		{ID: 2, Active: true, Process: "Safari", PID: 7, Dest: "example.com:443", Location: "office", Via: "proxy:corp", BytesUp: 1024, BytesDown: 2 << 20, DurationMS: 65000},
	}

	plain := renderTable(conns, 120, 24, false)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got %d lines:\n%s", len(lines), plain)
	}
	if !strings.HasPrefix(lines[0], "PROCESS") {
		t.Errorf("missing header: %q", lines[0])
	}
	// Active row must come first regardless of input order.
	if !strings.Contains(lines[1], "Safari") || !strings.Contains(lines[1], "proxy:corp") {
		t.Errorf("active row not first: %q", lines[1])
	}
	if !strings.Contains(lines[1], "1.0KB") || !strings.Contains(lines[1], "2.0MB") || !strings.Contains(lines[1], "1m5s") {
		t.Errorf("bytes/age not humanized: %q", lines[1])
	}

	ansi := renderTable(conns, 120, 24, true)
	if !strings.Contains(ansi, "\x1b[2m") {
		t.Error("closed row not dimmed in ansi mode")
	}
	if !strings.HasPrefix(ansi, "\x1b[H") || !strings.HasSuffix(ansi, "\x1b[J") {
		t.Error("ansi frame missing home/clear sequences")
	}

	// Height clamp: with height 3 only header + 1 row fit.
	clamped := renderTable(conns, 120, 3, true)
	if strings.Contains(clamped, "curl") {
		t.Error("height clamp not applied: dimmed row should be cut")
	}

	// Narrow width: DEST truncated but line length bounded.
	narrow := renderTable(conns, 60, 24, false)
	for _, l := range strings.Split(strings.TrimRight(narrow, "\n"), "\n") {
		if len(l) > 100 {
			t.Errorf("line too long at width 60: %d chars", len(l))
		}
	}
}
