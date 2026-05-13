package proxy

import (
	"net/url"
	"testing"
)

func TestApplyRoute(t *testing.T) {
	tests := []struct {
		name   string
		routes map[string]string
		host   string
		path   string
		query  string
		want   string
		wantOK bool
	}{
		{"host prefix", map[string]string{"httpbin.org": "https://localhost:4321"}, "httpbin.org", "/anything", "", "https://localhost:4321/anything", true},
		{"host+path prefix", map[string]string{"httpbin.org/api": "https://localhost:4321"}, "httpbin.org", "/api/users", "", "https://localhost:4321/users", true},
		{"no match different host", map[string]string{"httpbin.org": "https://localhost:4321"}, "example.com", "/x", "", "", false},
		{"empty routes", map[string]string{}, "httpbin.org", "/x", "", "", false},
		{"nil routes", nil, "httpbin.org", "/x", "", "", false},
		{"preserves query", map[string]string{"example.com": "https://localhost:8080"}, "example.com", "/search", "q=hello", "https://localhost:8080/search?q=hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyRoute(tt.routes, tt.host, tt.path, tt.query)
			if (got != nil) != tt.wantOK {
				t.Errorf("applyRoute() ok = %v, want %v", got != nil, tt.wantOK)
			}
			if got != nil {
				if got.String() != tt.want {
					t.Errorf("applyRoute() = %q, want %q", got.String(), tt.want)
				}
			}
		})
	}
}

func TestApplyRouteRoundTrip(t *testing.T) {
	routes := map[string]string{"example.com": "https://localhost:8080"}
	got := applyRoute(routes, "example.com", "/api/test", "q=1")
	if got == nil {
		t.Fatal("applyRoute returned nil")
	}
	if got.Scheme != "https" {
		t.Errorf("Scheme = %q, want 'https'", got.Scheme)
	}
	if got.Host != "localhost:8080" {
		t.Errorf("Host = %q, want 'localhost:8080'", got.Host)
	}
	if got.Path != "/api/test" {
		t.Errorf("Path = %q, want '/api/test'", got.Path)
	}
	if got.RawQuery != "q=1" {
		t.Errorf("RawQuery = %q, want 'q=1'", got.RawQuery)
	}
}

type urlStruct url.URL

func TestStripPort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"example.com:443", "example.com"},
		{"example.com", "example.com"},
		{"192.168.1.1:8080", "192.168.1.1"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripPort(tt.input)
			if got != tt.want {
				t.Errorf("stripPort(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
