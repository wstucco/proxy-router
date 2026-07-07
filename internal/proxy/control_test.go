package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	e := trackConn(kindConnect, "127.0.0.1:50000", "example.com:443", "office", "proxy:corp")
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
	e := trackConn(kindConnect, "127.0.0.1:50000", "example.com:443", d.RouteLoc(), d.RouteDest())
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

func TestControlUnknownPath(t *testing.T) {
	s := New(&config.Config{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, newControlReq(t, http.MethodGet, "/whatever", "127.0.0.1:60000"))
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}
