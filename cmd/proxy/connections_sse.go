package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/wstucco/proxy-router/internal/proxy"
)

// connEvent is the unified message the enhanced TUI consumes, from either
// the SSE stream or the polling fallback.
type connEvent struct {
	Type     string // snapshot | open | close | dest | proc | stats | status
	Conn     *proxy.ConnInfo
	Snapshot []proxy.ConnInfo
	Stats    []proxy.ConnStat
	Status   string // human message for the status bar (Type == "status")
}

// sendCtx sends ev to ch, aborting and returning false if ctx is cancelled.
func sendCtx(ctx context.Context, ch chan<- connEvent, ev connEvent) bool {
	// Prefer ctx cancellation over send even when both are ready
	// (e.g. channel buffer is empty but context is already cancelled).
	if ctx.Err() != nil {
		return false
	}
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// streamEvents feeds ch from the daemon's /_pr/events SSE stream,
// reconnecting with a fixed backoff. If the daemon predates the SSE
// endpoint (404 — e.g. the service wasn't restarted after an upgrade),
// it degrades to polling /_pr/connections. Runs until ctx is cancelled.
func streamEvents(ctx context.Context, addr string, ch chan<- connEvent) {
	for {
		err := readEventStream(ctx, addr, ch)
		if ctx.Err() != nil {
			return
		}
		if err == errNoSSE {
			pollEvents(ctx, addr, ch)
			return
		}
		if !sendCtx(ctx, ch, connEvent{Type: "status", Status: "disconnected from " + addr + " — retrying"}) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

type sseError string

func (e sseError) Error() string { return string(e) }

const errNoSSE = sseError("daemon does not support /_pr/events")

func readEventStream(ctx context.Context, addr string, ch chan<- connEvent) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/_pr/events", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNoSSE
	}
	if resp.StatusCode != http.StatusOK {
		return sseError("daemon returned " + resp.Status)
	}

	if !sendCtx(ctx, ch, connEvent{Type: "status", Status: "live — " + addr}) {
		return nil
	}

	br := bufio.NewReader(resp.Body)
	var event, data string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && event != "":
			if ev, ok := parseSSEEvent(event, data); ok {
				if !sendCtx(ctx, ch, ev) {
					return nil
				}
			}
			event, data = "", ""
		}
	}
}

func parseSSEEvent(event, data string) (connEvent, bool) {
	switch event {
	case "snapshot":
		var body struct {
			Connections []proxy.ConnInfo `json:"connections"`
		}
		if json.Unmarshal([]byte(data), &body) != nil {
			return connEvent{}, false
		}
		return connEvent{Type: event, Snapshot: body.Connections}, true
	case "open", "close", "dest", "proc":
		var c proxy.ConnInfo
		if json.Unmarshal([]byte(data), &c) != nil {
			return connEvent{}, false
		}
		return connEvent{Type: event, Conn: &c}, true
	case "stats":
		var s []proxy.ConnStat
		if json.Unmarshal([]byte(data), &s) != nil {
			return connEvent{}, false
		}
		return connEvent{Type: event, Stats: s}, true
	}
	return connEvent{}, false
}

// pollEvents is the degraded mode for daemons without SSE: a periodic full
// snapshot via /_pr/connections.
func pollEvents(ctx context.Context, addr string, ch chan<- connEvent) {
	if !sendCtx(ctx, ch, connEvent{Type: "status", Status: "polling — " + addr + " (daemon has no SSE, restart it after upgrading)"}) {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		conns, err := fetchConnections(addr)
		if err != nil {
			if !sendCtx(ctx, ch, connEvent{Type: "status", Status: "disconnected from " + addr + " — retrying"}) {
				return
			}
			continue
		}
		if !sendCtx(ctx, ch, connEvent{Type: "snapshot", Snapshot: conns}) {
			return
		}
	}
}
