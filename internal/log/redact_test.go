package log

import (
	"testing"
)

func TestRedactURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://user:pass@proxy:8080", "http://user:xxxxx@proxy:8080"},
		{"http://proxy:8080", "http://proxy:8080"},
		{"socks5://user:secret@proxy:1080", "socks5://user:xxxxx@proxy:1080"},
		{"", ""},
		{"not a url", "not a url"},
		{"http://user:@proxy:8080", "http://user:xxxxx@proxy:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := RedactURL(tt.input)
			if got != tt.want {
				t.Errorf("RedactURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactURLNoPasswordLeak(t *testing.T) {
	// Verify that a password never appears in the output
	urls := []string{
		"http://user:supersecret@proxy:8080",
		"http://admin:password123!@corp-proxy:3128",
		"socks5://bob:letmein@socks:1080",
	}
	for _, u := range urls {
		got := RedactURL(u)
		if containsAny(got, []string{"supersecret", "password123!", "letmein"}) {
			t.Errorf("RedactURL leaked password in: %s", got)
		}
	}
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if stringsContains(s, sub) {
			return true
		}
	}
	return false
}

func stringsContains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}