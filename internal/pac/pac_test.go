package pac

import (
	"os"
	"path/filepath"
	"testing"
)

func writePac(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.pac")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEvalDirect(t *testing.T) {
	path := writePac(t, `function FindProxyForURL(url, host) { return "DIRECT"; }`)
	result, err := Eval(path, "http://example.com/foo", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsDirect() {
		t.Errorf("expected DIRECT, got PROXY %s", result.Proxy)
	}
	if result.ProxyURL() != "" {
		t.Errorf("expected empty ProxyURL, got %q", result.ProxyURL())
	}
}

func TestEvalProxy(t *testing.T) {
	path := writePac(t, `function FindProxyForURL(url, host) { return "PROXY proxy.corp.com:8080"; }`)
	result, err := Eval(path, "http://example.com/foo", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if result.IsDirect() {
		t.Fatal("expected PROXY, got DIRECT")
	}
	if result.Proxy != "proxy.corp.com:8080" {
		t.Errorf("expected proxy.corp.com:8080, got %q", result.Proxy)
	}
	if result.ProxyURL() != "http://proxy.corp.com:8080" {
		t.Errorf("expected http://proxy.corp.com:8080, got %q", result.ProxyURL())
	}
}

func TestEvalFallback(t *testing.T) {
	path := writePac(t, `function FindProxyForURL(url, host) { return "PROXY proxy1:3128; PROXY proxy2:3128; DIRECT"; }`)
	result, err := Eval(path, "http://example.com", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if result.Proxy != "proxy1:3128" {
		t.Errorf("expected first proxy, got %q", result.Proxy)
	}
}

func TestEvalByHost(t *testing.T) {
	path := writePac(t, `function FindProxyForURL(url, host) {
		if (host == "internal.corp.com") return "DIRECT";
		return "PROXY proxy.corp.com:8080";
	}`)

	// Internal host → DIRECT
	result, err := Eval(path, "http://internal.corp.com", "internal.corp.com")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsDirect() {
		t.Errorf("expected DIRECT for internal host, got PROXY %s", result.Proxy)
	}

	// External host → PROXY
	result, err = Eval(path, "http://example.com", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if result.IsDirect() || result.Proxy != "proxy.corp.com:8080" {
		t.Errorf("expected PROXY proxy.corp.com:8080 for external host, got %v", result)
	}
}

func TestEvalByHostPattern(t *testing.T) {
	path := writePac(t, `function FindProxyForURL(url, host) {
		if (shExpMatch(host, "*.corp.com")) return "DIRECT";
		return "PROXY proxy:80";
	}`)

	result, err := Eval(path, "http://foo.corp.com", "foo.corp.com")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsDirect() {
		t.Errorf("expected DIRECT for *.corp.com, got PROXY %s", result.Proxy)
	}
}

func TestEvalDNSResolve(t *testing.T) {
	path := writePac(t, `function FindProxyForURL(url, host) {
		if (isPlainHostName(host)) return "DIRECT";
		return "PROXY proxy:80";
	}`)

	result, err := Eval(path, "http://localhost", "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsDirect() {
		t.Errorf("expected DIRECT for plain hostname, got PROXY %s", result.Proxy)
	}
}

func TestParseResult(t *testing.T) {
	tests := []struct {
		input  string
		direct bool
		proxy  string
	}{
		{"DIRECT", true, ""},
		{"PROXY host:8080", false, "host:8080"},
		{"PROXY host:8080; DIRECT", false, "host:8080"},
		{"SOCKS host:1080; DIRECT", true, ""},
		{"SOCKS host:1080; PROXY proxy:80", false, "proxy:80"},
		{"", true, ""},
	}
	for _, tt := range tests {
		r, err := parseResult(tt.input)
		if err != nil {
			t.Errorf("parseResult(%q): %v", tt.input, err)
			continue
		}
		if r.Direct != tt.direct {
			t.Errorf("parseResult(%q).Direct = %v, want %v", tt.input, r.Direct, tt.direct)
		}
		if r.Proxy != tt.proxy {
			t.Errorf("parseResult(%q).Proxy = %q, want %q", tt.input, r.Proxy, tt.proxy)
		}
	}
}

func TestShellMatch(t *testing.T) {
	tests := []struct {
		str     string
		pattern string
		match   bool
	}{
		{"foo.corp.com", "*.corp.com", true},
		{"bar.corp.com", "*.corp.com", true},
		{"corp.com", "*.corp.com", false},
		{"foo.corp.com", "foo.*", true},
		{"foo.corp.com", "foo.corp.*", true},
		{"foo.corp.com", "*", true},
		{"foo.corp.com", "foo.corp.com", true},
		{"foo.corp.com", "bar.corp.com", false},
		{"hello", "h*o", true},
		{"hello", "h*lo", true},
		{"hello", "h*l?", true},
	}
	for _, tt := range tests {
		got := shellMatch(tt.str, tt.pattern)
		if got != tt.match {
			t.Errorf("shellMatch(%q, %q) = %v, want %v", tt.str, tt.pattern, got, tt.match)
		}
	}
}
