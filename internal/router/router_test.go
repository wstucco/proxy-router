package router

import (
	"testing"

	"github.com/wstucco/proxy-router/internal/config"
)

func TestIsAlwaysNoProxy(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"localhost:8080", true},
		{"127.0.0.1", true},
		{"127.0.0.1:3000", true},
		{"::1", true},
		{"[::1]:443", true},
		{"example.com", false},
		{"10.0.0.1", false},
	}
	for _, tt := range tests {
		got := isAlwaysNoProxy(tt.host)
		if got != tt.want {
			t.Errorf("isAlwaysNoProxy(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestMatchSSID(t *testing.T) {
	tests := []struct {
		current string
		list    []string
		want    bool
	}{
		{"OfficeWifi", []string{"OfficeWifi"}, true},
		{"OFFICEWIFI", []string{"OfficeWifi"}, true},
		{"OfficeWifi", []string{"*"}, true},
		{"HomeWifi", []string{"OfficeWifi", "OfficeWifi-5G"}, false},
		{"OfficeWifi-5G", []string{"OfficeWifi", "OfficeWifi-5G"}, true},
		{"", []string{"OfficeWifi"}, false},
		{"OfficeWifi", []string{}, false},
	}
	for _, tt := range tests {
		got := matchSSID(tt.current, tt.list)
		if got != tt.want {
			t.Errorf("matchSSID(%q, %v) = %v, want %v", tt.current, tt.list, got, tt.want)
		}
	}
}

func TestMatchIP(t *testing.T) {
	tests := []struct {
		host string
		list []string
		want bool
	}{
		{"10.0.0.5", []string{"10.0.0.0/8"}, true},
		{"10.0.0.5:1234", []string{"10.0.0.0/8"}, true},
		{"192.168.1.1", []string{"10.0.0.0/8"}, false},
		{"10.0.0.1", []string{"10.0.0.1"}, true},
		{"10.0.0.2", []string{"10.0.0.1"}, false},
		{"anything", []string{"*"}, true},
		{"192.168.1.1", []string{"192.168.0.0/16"}, true},
		{"172.16.0.1", []string{"192.168.0.0/16"}, false},
	}
	for _, tt := range tests {
		got := matchIP(tt.host, tt.list)
		if got != tt.want {
			t.Errorf("matchIP(%q, %v) = %v, want %v", tt.host, tt.list, got, tt.want)
		}
	}
}

func TestMatchesLocation(t *testing.T) {
	tests := []struct {
		name string
		loc  *config.Location
		host string
		ssid string
		want bool
	}{
		{
			name: "ssid match",
			loc:  &config.Location{SSIDs: []string{"OfficeWifi"}},
			host: "example.com", ssid: "OfficeWifi",
			want: true,
		},
		{
			name: "ssid no match",
			loc:  &config.Location{SSIDs: []string{"OfficeWifi"}},
			host: "example.com", ssid: "HomeWifi",
			want: false,
		},
		{
			name: "ssid wildcard",
			loc:  &config.Location{SSIDs: []string{"*"}},
			host: "example.com", ssid: "AnyWifi",
			want: true,
		},
		{
			name: "ip match",
			loc:  &config.Location{IPs: []string{"10.0.0.0/8"}},
			host: "10.1.2.3", ssid: "",
			want: true,
		},
		{
			name: "ip no match",
			loc:  &config.Location{IPs: []string{"10.0.0.0/8"}},
			host: "192.168.1.1", ssid: "",
			want: false,
		},
		{
			name: "domain match",
			loc:  &config.Location{Domains: []string{"corp.com"}},
			host: "internal.corp.com", ssid: "",
			want: true,
		},
		{
			name: "domain no match",
			loc:  &config.Location{Domains: []string{"corp.com"}},
			host: "example.com", ssid: "",
			want: false,
		},
		{
			name: "ssid AND ip both must match",
			loc:  &config.Location{SSIDs: []string{"OfficeWifi"}, IPs: []string{"10.0.0.0/8"}},
			host: "10.1.2.3", ssid: "HomeWifi",
			want: false,
		},
		{
			name: "ssid AND ip both match",
			loc:  &config.Location{SSIDs: []string{"OfficeWifi"}, IPs: []string{"10.0.0.0/8"}},
			host: "10.1.2.3", ssid: "OfficeWifi",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesLocation(tt.loc, tt.host, tt.ssid)
			if got != tt.want {
				t.Errorf("matchesLocation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func makeCfg(proxy, pac string, locs map[string]*config.Location) *config.Config {
	proxies := map[string]string{}
	pacs := map[string]string{}
	if proxy != "" && proxy != "direct" {
		proxies["corp"] = proxy
	}
	if pac != "" && pac != "direct" {
		pacs["autopac"] = pac
	}
	return &config.Config{
		Proxies:   proxies,
		PACs:      pacs,
		Defaults:  config.Defaults{Proxy: "corp", PAC: ""},
		Locations: locs,
	}
}

func TestDecide(t *testing.T) {
	corpProxy := "http://proxy.corp.com:8080"
	pacURL := "http://proxy.corp.com/proxy.pac"

	tests := []struct {
		name         string
		cfg          *config.Config
		host         string
		ssid         string
		wantProxy    string
		wantPAC      string
		wantLocation string
		wantDirect   bool
	}{
		{
			name: "always-no-proxy localhost",
			cfg: &config.Config{
				Proxies:   map[string]string{"corp": corpProxy},
				Defaults:  config.Defaults{Proxy: "corp"},
				Locations: map[string]*config.Location{},
			},
			host: "localhost", ssid: "OfficeWifi",
			wantDirect: true,
		},
		{
			name: "always-no-proxy 127.0.0.1",
			cfg: &config.Config{
				Proxies:   map[string]string{"corp": corpProxy},
				Defaults:  config.Defaults{Proxy: "corp"},
				Locations: map[string]*config.Location{},
			},
			host: "127.0.0.1:8080", ssid: "",
			wantDirect: true,
		},
		{
			name: "ssid match → location proxy",
			cfg: &config.Config{
				Proxies: map[string]string{"corp": corpProxy},
				Defaults: config.Defaults{Proxy: "direct"},
				Locations: map[string]*config.Location{
					"office": {Proxy: "corp", SSIDs: []string{"OfficeWifi"}},
				},
			},
			host: "example.com", ssid: "OfficeWifi",
			wantProxy: corpProxy, wantLocation: "office",
		},
		{
			name: "ssid no match → defaults direct",
			cfg: &config.Config{
				Proxies: map[string]string{"corp": corpProxy},
				Defaults: config.Defaults{Proxy: "direct"},
				Locations: map[string]*config.Location{
					"office": {Proxy: "corp", SSIDs: []string{"OfficeWifi"}},
				},
			},
			host: "example.com", ssid: "HomeWifi",
			wantDirect: true,
		},
		{
			name: "no location → defaults proxy",
			cfg: &config.Config{
				Proxies:   map[string]string{"corp": corpProxy},
				Defaults:  config.Defaults{Proxy: "corp"},
				Locations: map[string]*config.Location{},
			},
			host: "example.com", ssid: "",
			wantProxy: corpProxy,
		},
		{
			name: "no location → defaults PAC",
			cfg: &config.Config{
				PACs:      map[string]string{"autopac": pacURL},
				Defaults:  config.Defaults{PAC: "autopac"},
				Locations: map[string]*config.Location{},
			},
			host: "example.com", ssid: "",
			wantPAC: pacURL,
		},
		{
			name: "no_proxy bypass skips location proxy",
			cfg: &config.Config{
				Proxies: map[string]string{"corp": corpProxy},
				Defaults: config.Defaults{Proxy: "direct", NoProxy: []string{"internal.corp.com"}},
				Locations: map[string]*config.Location{
					"office": {Proxy: "corp", SSIDs: []string{"OfficeWifi"}},
				},
			},
			host: "internal.corp.com", ssid: "OfficeWifi",
			wantDirect: true,
		},
		{
			name: "IP CIDR match → location proxy",
			cfg: &config.Config{
				Proxies: map[string]string{"corp": corpProxy},
				Defaults: config.Defaults{Proxy: "direct"},
				Locations: map[string]*config.Location{
					"vpn": {Proxy: "corp", IPs: []string{"10.0.0.0/8"}},
				},
			},
			host: "10.5.6.7", ssid: "",
			wantProxy: corpProxy, wantLocation: "vpn",
		},
		{
			name: "domain match → location proxy",
			cfg: &config.Config{
				Proxies: map[string]string{"corp": corpProxy},
				Defaults: config.Defaults{Proxy: "direct"},
				Locations: map[string]*config.Location{
					"corp": {Proxy: "corp", Domains: []string{"corp.com"}},
				},
			},
			host: "mail.corp.com", ssid: "",
			wantProxy: corpProxy, wantLocation: "corp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decide(tt.cfg, tt.host, tt.ssid)

			if tt.wantDirect {
				if got.ProxyURL != "" || got.PAC != "" {
					t.Errorf("want direct, got ProxyURL=%q PAC=%q", got.ProxyURL, got.PAC)
				}
				return
			}
			if got.ProxyURL != tt.wantProxy {
				t.Errorf("ProxyURL = %q, want %q", got.ProxyURL, tt.wantProxy)
			}
			if got.PAC != tt.wantPAC {
				t.Errorf("PAC = %q, want %q", got.PAC, tt.wantPAC)
			}
			if got.LocationName != tt.wantLocation {
				t.Errorf("LocationName = %q, want %q", got.LocationName, tt.wantLocation)
			}
		})
	}
}
