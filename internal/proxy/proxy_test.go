package proxy

import (
	"testing"
)

func TestMatchRoute(t *testing.T) {
	tests := []struct {
		name   string
		routes map[string]string
		path   string
		want   string
		wantOK bool
	}{
		{"exact", map[string]string{"/health": "direct"}, "/health", "direct", true},
		{"glob suffix", map[string]string{"/api/*": "direct"}, "/api/health", "direct", true},
		{"glob prefix", map[string]string{"/*.html": "corp"}, "/page.html", "corp", true},
		{"no match", map[string]string{"/api/*": "direct"}, "/other", "", false},
		{"empty routes", map[string]string{}, "/x", "", false},
		{"nil routes", nil, "/x", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchRoute(tt.routes, tt.path)
			if ok != tt.wantOK {
				t.Errorf("matchRoute() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("matchRoute() = %q, want %q", got, tt.want)
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
