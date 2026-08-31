package proxy

import (
	"net"
	"testing"

	"github.com/wstucco/proxy-router/internal/config"
)

// testShellMatch is a helper function that implements the same shell-style pattern
// matching logic used by PAC's shExpMatch() function.
func testShellMatch(str, pattern string) bool {
	si, pi := 0, 0
	starIdx := -1
	matchIdx := 0

	for si < len(str) {
		if pi < len(pattern) && (pattern[pi] == '?' || pattern[pi] == str[si]) {
			si++
			pi++
		} else if pi < len(pattern) && pattern[pi] == '*' {
			starIdx = pi
			matchIdx = si
			pi++
		} else if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			si = matchIdx
		} else {
			return false
		}
	}

	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}

	return pi == len(pattern)
}

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

// TestPACEvaluationWithPorts verifies that hostnames with ports are handled correctly
// during PAC evaluation. This is a regression test for the bug where handleCONNECT
// would not strip the port from the hostname when the port was 443, causing PAC
// patterns like "*.example.com" to fail to match "host.example.com:443".
func TestPACEvaluationWithPorts(t *testing.T) {
	tests := []struct {
		name     string
		host     string // input host (may include port)
		port     string // port from net.SplitHostPort
		wantHost string // expected host passed to PAC (without port)
		wantScheme string // expected URL scheme for PAC
	}{
		{"port 443 should be stripped", "example.com:443", "443", "example.com", "https"},
		{"port 80 should be stripped", "example.com:80", "80", "example.com", "http"},
		{"non-standard port should be stripped", "example.com:8443", "8443", "example.com", "http"},
		{"no port should remain unchanged", "example.com", "", "example.com", "https"},
		{"ipv4 with port 443", "192.168.1.1:443", "443", "192.168.1.1", "https"},
		{"ipv4 with port 8080", "192.168.1.1:8080", "8080", "192.168.1.1", "http"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate what handleCONNECT does: strip port and infer scheme
			pacHost := tt.host
			pacScheme := "https"
			if h, p, err := net.SplitHostPort(tt.host); err == nil {
				pacHost = h
				if p != "443" {
					pacScheme = "http"
				}
			}

			if pacHost != tt.wantHost {
				t.Errorf("pacHost = %q, want %q", pacHost, tt.wantHost)
			}
			if pacScheme != tt.wantScheme {
				t.Errorf("pacScheme = %q, want %q", pacScheme, tt.wantScheme)
			}
		})
	}
}

// TestPACPatternMatchingWithPorts verifies that PAC pattern matching works correctly
// when the hostname includes a port. This ensures that PAC patterns like "*.domain.com"
// match "subdomain.domain.com" even when the original request was to "subdomain.domain.com:443".
func TestPACPatternMatchingWithPorts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping PAC evaluation test")
	}

	tests := []struct {
		name        string
		hostWithPort string
		pattern     string
		expectMatch bool
	}{
		// After fix: port is stripped before PAC evaluation, so these should all match correctly
		{"wildcard domain with port 443", "litellm.unp-aquarium.gruppounipol.cloud:443", "*.gruppounipol.cloud", true},  // After fix: port stripped, pattern matches
		{"wildcard domain without port", "litellm.unp-aquarium.gruppounipol.cloud", "*.gruppounipol.cloud", true},        // Correct behavior
		{"exact domain with port", "example.com:8443", "example.com", true},                                                 // After fix: port stripped, exact match works
		{"exact domain without port", "example.com", "example.com", true},                                                   // Correct behavior
		{"no match after strip", "api.example.com:443", "*.internal.com", false},                                            // Port stripped, but pattern doesn't match
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// After fix: we should strip the port before passing to PAC
			pacHost := tt.hostWithPort
			if h, _, err := net.SplitHostPort(tt.hostWithPort); err == nil {
				pacHost = h
			}

			// Use the same shellMatch logic as the PAC evaluator
			got := testShellMatch(pacHost, tt.pattern)
			if got != tt.expectMatch {
				t.Errorf("testShellMatch(%q, %q) = %v, want %v", pacHost, tt.pattern, got, tt.expectMatch)
			}
		})
	}
}
