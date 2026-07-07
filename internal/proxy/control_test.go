package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wstucco/proxy-router/internal/config"
)

func newControlReq(t *testing.T, method, path, remoteAddr string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = remoteAddr
	return r
}

func TestControlConnectionsJSON(t *testing.T) {
	resetConnReg()
	e := trackConn(kindConnect, "nohost","example.com:443", "office", "proxy:corp")
	defer e.Close()

	s := New(&config.Config{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, newControlReq(t, http.MethodGet, "/_pr/connections", "127.0.0.1:60000"))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type %q", ct)
	}
	var body struct {
		Connections []ConnInfo `json:"connections"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Connections) != 1 || body.Connections[0].Dest != "example.com:443" {
		t.Errorf("unexpected payload: %+v", body)
	}
}

// The via field comes from RouteDest() which never carries credentials —
// assert the JSON payload can't leak a password even for URL-only proxies.
func TestControlNoCredentialLeak(t *testing.T) {
	resetConnReg()
	d := config.Decision{ProxyURL: "http://user:secret@corp:8080"}
	e := trackConn(kindConnect, "nohost","example.com:443", d.RouteLoc(), d.RouteDest())
	defer e.Close()

	s := New(&config.Config{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, newControlReq(t, http.MethodGet, "/_pr/connections", "127.0.0.1:60000"))

	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("credentials leaked in control payload: %s", w.Body.String())
	}
}

func TestControlRejectsNonLoopback(t *testing.T) {
	s := New(&config.Config{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, newControlReq(t, http.MethodGet, "/_pr/connections", "192.168.1.20:60000"))
	if w.Code != http.StatusForbidden {
		t.Errorf("want 403 for non-loopback, got %d", w.Code)
	}
}

// readSSEEvent reads one "event:"/"data:" pair from the stream.
func readSSEEvent(t *testing.T, br *bufio.Reader) (event, data string) {
	t.Helper()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading SSE stream: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "" && event != "":
			return event, data
		}
	}
}

func TestEventsStream(t *testing.T) {
	resetConnReg()
	srv := httptest.NewServer(New(&config.Config{}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/_pr/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	br := bufio.NewReader(resp.Body)

	// First frame is always the snapshot.
	event, data := readSSEEvent(t, br)
	if event != "snapshot" {
		t.Fatalf("first event %q, want snapshot", event)
	}
	var snap struct {
		Connections []ConnInfo `json:"connections"`
	}
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Connections) != 0 {
		t.Errorf("expected empty snapshot, got %d", len(snap.Connections))
	}

	// A tracked connection must arrive as a push "open" event. Stray "proc"
	// events can leak in from PID-resolution goroutines of entries created
	// by earlier tests — skip anything that isn't ours.
	e := trackConn(kindConnect, "nohost", "example.com:443", "office", "proxy:corp")
	var ci ConnInfo
	for {
		event, data = readSSEEvent(t, br)
		if err := json.Unmarshal([]byte(data), &ci); err != nil {
			continue
		}
		if ci.Dest == "example.com:443" {
			break
		}
	}
	if event != "open" {
		t.Fatalf("event %q, want open", event)
	}
	if !ci.Active {
		t.Errorf("open payload: %+v", ci)
	}

	// While active, the periodic stats tick must include it.
	e.bytesUp.Add(42)
	for {
		event, data = readSSEEvent(t, br)
		if event != "stats" {
			continue
		}
		var stats []ConnStat
		if err := json.Unmarshal([]byte(data), &stats); err != nil {
			t.Fatal(err)
		}
		if len(stats) != 1 || stats[0].ID != ci.ID || stats[0].BytesUp != 42 {
			t.Errorf("stats payload: %+v", stats)
		}
		break
	}

	e.Close()
	for {
		event, data = readSSEEvent(t, br)
		if event == "stats" || event == "proc" {
			continue // late ticks or stray PID resolutions
		}
		break
	}
	if event != "close" {
		t.Fatalf("event %q, want close", event)
	}

	// Client disconnect must unsubscribe the handler.
	cancel()
	deadline := time.After(3 * time.Second)
	for {
		connReg.Lock()
		n := len(connReg.subs)
		connReg.Unlock()
		if n == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("handler did not unsubscribe after client disconnect")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestControlUnknownPath(t *testing.T) {
	s := New(&config.Config{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, newControlReq(t, http.MethodGet, "/whatever", "127.0.0.1:60000"))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}
