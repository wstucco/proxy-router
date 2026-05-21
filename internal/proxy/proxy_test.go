package proxy

import (
	"testing"

	"github.com/wstucco/proxy-router/internal/config"
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
			got, err := applyRoute(tt.routes, tt.host, tt.path, tt.query)
			if err != nil {
				t.Fatalf("applyRoute() error = %v", err)
			}
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
	got, err := applyRoute(routes, "example.com", "/api/test", "q=1")
	if err != nil {
		t.Fatalf("applyRoute returned error: %v", err)
	}
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

func TestRouteTargetPortDefaults(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"https defaults to 443", "https://example.com", "example.com:443", false},
		{"http defaults to 80", "http://example.com", "example.com:80", false},
		{"no scheme defaults to 443", "example.com", "example.com:443", false},
		{"no scheme with port keeps port", "example.com:8443", "example.com:8443", false},
		{"invalid scheme errors", "ftp://example.com", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := parseRouteTarget(tt.raw)
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("parseRouteTarget(%q) error = %v", tt.raw, err)
				}
				return
			}
			if tt.wantErr {
				t.Fatalf("parseRouteTarget(%q) expected error", tt.raw)
			}
			got, err := addDefaultPort(u.Host, u.Scheme)
			if err != nil {
				t.Fatalf("addDefaultPort(%q, %q) error = %v", u.Host, u.Scheme, err)
			}
			if got != tt.want {
				t.Errorf("addDefaultPort(%q, %q) = %q, want %q", u.Host, u.Scheme, got, tt.want)
			}
		})
	}
}

func TestShouldUseUpstreamProxy(t *testing.T) {
	decision := &config.Decision{
		ProxyURL: "http://proxy.example:8080",
		NoProxy:  []string{".internal.example.com", "direct.example.com"},
	}

	tests := []struct {
		name    string
		host    string
		routed  bool
		wantUse bool
	}{
		{"routed target goes through proxy", "service.example.com", true, true},
		{"routed target in no_proxy goes direct", "direct.example.com", true, false},
		{"routed subdomain in no_proxy suffix goes direct", "api.internal.example.com", true, false},
		{"non routed host still uses proxy", "service.example.com", false, true},
		{"no proxy configured stays direct", "service.example.com", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := *decision
			if tt.name == "no proxy configured stays direct" {
				d.ProxyURL = ""
			}
			got := shouldUseUpstreamProxy(&d, tt.host, tt.routed)
			if got != tt.wantUse {
				t.Errorf("shouldUseUpstreamProxy(%q, routed=%v) = %v, want %v", tt.host, tt.routed, got, tt.wantUse)
			}
		})
	}
}

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
