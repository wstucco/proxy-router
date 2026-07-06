package config

import "testing"

// Regression test: RouteDest is used in proxy log lines (debug and error) —
// it must show proxy/PAC names, never resolved URLs with credentials.
func TestRouteDest(t *testing.T) {
	tests := []struct {
		name string
		d    Decision
		want string
	}{
		{"direct", Decision{}, "direct"},
		{"proxy by name", Decision{ProxyURL: "http://user:secret@corp:8080", ProxyName: "corp"}, "proxy:corp"},
		{"proxy without name is redacted", Decision{ProxyURL: "http://user:secret@corp:8080"}, "proxy:http://user:xxxxx@corp:8080"},
		{"pac by name", Decision{PAC: "http://wpad.example.com/wpad.dat", PACName: "wpad"}, "pac:wpad"},
		{"pac without name shows url", Decision{PAC: "http://wpad.example.com/wpad.dat"}, "pac:http://wpad.example.com/wpad.dat"},
		{"pac wins over proxy", Decision{PAC: "file:///w.dat", PACName: "w", ProxyURL: "http://p:1", ProxyName: "p"}, "pac:w"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.RouteDest(); got != tt.want {
				t.Errorf("RouteDest() = %q, want %q", got, tt.want)
			}
			if tt.d.ProxyURL != "" && tt.d.ProxyName == "" {
				if got := tt.d.RouteDest(); got == "proxy:"+tt.d.ProxyURL {
					t.Errorf("RouteDest() leaks credentials: %q", got)
				}
			}
		})
	}
}
