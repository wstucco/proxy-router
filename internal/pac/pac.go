package pac

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// Result holds the parsed result of a PAC evaluation.
type Result struct {
	Direct bool
	Proxy  string
}

// IsDirect returns true if the PAC result is DIRECT.
func (r *Result) IsDirect() bool { return r.Direct }

// ProxyURL returns the proxy URL string (e.g. "http://host:port") or "" for DIRECT.
func (r *Result) ProxyURL() string {
	if r.Direct {
		return ""
	}
	return "http://" + r.Proxy
}

type scriptCacheEntry struct {
	vm      *goja.Runtime
	expires time.Time
	modTime time.Time // file mtime for file:// PACs, zero for remote
}

var (
	cacheMu sync.RWMutex
	cache   = map[string]*scriptCacheEntry{}
)

const remoteTTL = 5 * time.Minute

// Eval evaluates a PAC script loaded from pacURL for the given request URL and host.
func Eval(pacURL, reqURL, host string) (*Result, error) {
	vm, err := getRuntime(pacURL)
	if err != nil {
		return nil, fmt.Errorf("loading PAC script: %w", err)
	}

	fn, ok := goja.AssertFunction(vm.Get("FindProxyForURL"))
	if !ok {
		return nil, fmt.Errorf("PAC script does not define FindProxyForURL")
	}

	val, err := fn(goja.Undefined(), vm.ToValue(reqURL), vm.ToValue(host))
	if err != nil {
		return nil, fmt.Errorf("evaluating FindProxyForURL: %w", err)
	}

	return parseResult(val.String())
}

func parseResult(s string) (*Result, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return &Result{Direct: true}, nil
	}

	parts := strings.Split(s, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tokens := strings.Fields(part)
		if len(tokens) == 0 {
			continue
		}
		switch strings.ToUpper(tokens[0]) {
		case "DIRECT":
			return &Result{Direct: true}, nil
		case "PROXY":
			if len(tokens) >= 2 {
				return &Result{Direct: false, Proxy: tokens[1]}, nil
			}
		case "SOCKS":
			continue
		}
	}

	return &Result{Direct: true}, nil
}

func getRuntime(pacURL string) (*goja.Runtime, error) {
	cacheMu.RLock()
	entry, ok := cache[pacURL]
	cacheMu.RUnlock()

	if ok && entry.expires.After(time.Now()) {
		// For file-based PACs, check mtime to detect changes
		if !entry.modTime.IsZero() {
			if fi, err := os.Stat(pacURL); err == nil && !fi.ModTime().Equal(entry.modTime) {
				ok = false // file changed, reload
			}
		}
		if ok {
			return entry.vm, nil
		}
	}

	vm := goja.New()
	registerHelpers(vm)

	script, modTime, err := loadScript(pacURL)
	if err != nil {
		return nil, err
	}

	if _, err := vm.RunString(script); err != nil {
		return nil, fmt.Errorf("compiling PAC script: %w", err)
	}

	var expires time.Time
	if strings.HasPrefix(pacURL, "http://") || strings.HasPrefix(pacURL, "https://") {
		expires = time.Now().Add(remoteTTL)
	} else {
		expires = time.Now().Add(365 * 24 * time.Hour)
	}

	cacheMu.Lock()
	cache[pacURL] = &scriptCacheEntry{
		vm:      vm,
		expires: expires,
		modTime: modTime,
	}
	cacheMu.Unlock()

	return vm, nil
}

func loadScript(pacURL string) (string, time.Time, error) {
	var modTime time.Time
	u, err := url.Parse(pacURL)
	if err != nil {
		return "", modTime, fmt.Errorf("invalid PAC URL %q: %w", pacURL, err)
	}

	switch u.Scheme {
	case "file", "":
		path := u.Path
		if u.Scheme == "" {
			path = pacURL
		}
		if !filepath.IsAbs(path) {
			return "", modTime, fmt.Errorf("PAC path must be absolute: %q", pacURL)
		}
		if fi, err := os.Stat(filepath.Clean(path)); err == nil {
			modTime = fi.ModTime()
		}
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", modTime, fmt.Errorf("reading PAC file %q: %w", path, err)
		}
		return string(data), modTime, nil

	case "http", "https":
		body, err := fetchRemote(u)
		return body, modTime, err

	default:
		return "", modTime, fmt.Errorf("unsupported PAC URL scheme %q", u.Scheme)
	}
}

func fetchRemote(u *url.URL) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return "", fmt.Errorf("fetching remote PAC %q: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching remote PAC %q: HTTP %d", u.String(), resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading remote PAC %q: %w", u.String(), err)
	}

	return string(body), nil
}

func registerHelpers(vm *goja.Runtime) {
	_ = vm.Set("isPlainHostName", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return goja.Undefined()
		}
		return vm.ToValue(!strings.Contains(call.Arguments[0].String(), "."))
	})

	_ = vm.Set("dnsDomainIs", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		host := call.Arguments[0].String()
		domain := call.Arguments[1].String()
		dotDomain := "." + strings.TrimPrefix(domain, ".")
		return vm.ToValue(host == domain || strings.HasSuffix(host, dotDomain))
	})

	_ = vm.Set("localHostOrDomainIs", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		host := call.Arguments[0].String()
		hostdom := call.Arguments[1].String()
		return vm.ToValue(host == hostdom || strings.HasPrefix(host, hostdom+"."))
	})

	_ = vm.Set("isResolvable", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return goja.Undefined()
		}
		_, err := net.LookupHost(call.Arguments[0].String())
		return vm.ToValue(err == nil)
	})

	_ = vm.Set("isInNet", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			return goja.Undefined()
		}
		host := call.Arguments[0].String()
		pattern := call.Arguments[1].String()
		mask := call.Arguments[2].String()

		ip := net.ParseIP(host)
		if ip == nil {
			addrs, err := net.LookupHost(host)
			if err != nil || len(addrs) == 0 {
				return vm.ToValue(false)
			}
			ip = net.ParseIP(addrs[0])
		}
		if ip == nil {
			return vm.ToValue(false)
		}

		_, cidr, err := net.ParseCIDR(pattern + "/" + mask)
		if err != nil {
			maskIP := net.ParseIP(mask)
			if maskIP != nil {
				ones := maskOnes(maskIP)
				if ones != 0 {
					_, cidr, err = net.ParseCIDR(fmt.Sprintf("%s/%d", pattern, ones))
				}
			}
		}
		if err != nil || cidr == nil {
			return vm.ToValue(false)
		}
		return vm.ToValue(cidr.Contains(ip))
	})

	_ = vm.Set("dnsResolve", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return goja.Undefined()
		}
		addrs, err := net.LookupHost(call.Arguments[0].String())
		if err != nil || len(addrs) == 0 {
			return goja.Undefined()
		}
		return vm.ToValue(addrs[0])
	})

	_ = vm.Set("myIpAddress", func(call goja.FunctionCall) goja.Value {
		conn, err := net.Dial("udp", "8.8.8.8:80")
		if err != nil {
			return goja.Undefined()
		}
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		return vm.ToValue(localAddr.IP.String())
	})

	_ = vm.Set("dnsDomainLevels", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return goja.Undefined()
		}
		return vm.ToValue(strings.Count(call.Arguments[0].String(), "."))
	})

	_ = vm.Set("shExpMatch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		return vm.ToValue(shellMatch(call.Arguments[0].String(), call.Arguments[1].String()))
	})

	_ = vm.Set("weekdayRange", func(call goja.FunctionCall) goja.Value {
		args := make([]string, len(call.Arguments))
		for i, a := range call.Arguments {
			args[i] = a.String()
		}
		now := time.Now()
		isGMT := false

		for i := 2; i < len(args); i++ {
			if strings.ToUpper(args[i]) == "GMT" {
				isGMT = true
				args = append(args[:i], args[i+1:]...)
				break
			}
		}
		if isGMT {
			now = now.UTC()
		}

		wd := now.Weekday().String()[:3]

		if len(args) == 1 {
			return vm.ToValue(strings.EqualFold(wd, args[0]))
		}

		if len(args) >= 2 {
			days := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
			startIdx, endIdx := -1, -1
			for i, d := range days {
				if strings.EqualFold(d, args[0]) {
					startIdx = i
				}
				if strings.EqualFold(d, args[1]) {
					endIdx = i
				}
			}
			if startIdx == -1 || endIdx == -1 {
				return vm.ToValue(false)
			}

			nowIdx := -1
			for i, d := range days {
				if strings.EqualFold(d, wd) {
					nowIdx = i
					break
				}
			}

			if startIdx <= endIdx {
				return vm.ToValue(nowIdx >= startIdx && nowIdx <= endIdx)
			}
			return vm.ToValue(nowIdx >= startIdx || nowIdx <= endIdx)
		}

		return vm.ToValue(false)
	})

	_ = vm.Set("dateRange", func(call goja.FunctionCall) goja.Value {
		strArgs := make([]string, len(call.Arguments))
		for i, a := range call.Arguments {
			strArgs[i] = a.String()
		}
		now := time.Now()
		isGMT := false

		for i := 0; i < len(strArgs); i++ {
			if strings.ToUpper(strArgs[i]) == "GMT" {
				isGMT = true
				strArgs = append(strArgs[:i], strArgs[i+1:]...)
				break
			}
		}
		if isGMT {
			now = now.UTC()
		}

		args := make([]int, 0, len(strArgs))
		for _, s := range strArgs {
			n, _ := strconv.Atoi(s)
			args = append(args, n)
		}

		switch len(args) {
		case 1:
			return vm.ToValue(int(now.Month()) == args[0])
		case 2:
			if args[0] <= 12 && args[1] <= 12 {
				m := int(now.Month())
				return vm.ToValue(m >= args[0] && m <= args[1])
			}
			d := now.Day()
			return vm.ToValue(d >= args[0] && d <= args[1])
		case 3:
			return vm.ToValue(int(now.Month()) == args[0] && now.Day() >= args[1] && now.Day() <= args[2])
		case 4:
			return vm.ToValue(int(now.Month()) >= args[0] && int(now.Month()) <= args[1] && now.Day() >= args[2] && now.Day() <= args[3])
		case 5:
			t := time.Date(args[4], time.Month(args[0]), args[1], 0, 0, 0, 0, now.Location())
			return vm.ToValue(now.After(t) || now.Equal(t))
		case 6:
			t1 := time.Date(args[4], time.Month(args[0]), args[1], 0, 0, 0, 0, now.Location())
			t2 := time.Date(args[5], time.Month(args[2]), args[3], 0, 0, 0, 0, now.Location())
			return vm.ToValue((now.After(t1) || now.Equal(t1)) && (now.Before(t2) || now.Equal(t2)))
		}
		return vm.ToValue(false)
	})

	_ = vm.Set("timeRange", func(call goja.FunctionCall) goja.Value {
		args := make([]int, 0, len(call.Arguments))
		for _, a := range call.Arguments {
			args = append(args, int(a.ToInteger()))
		}
		now := time.Now()
		isGMT := false

		for i := 0; i < len(args); i++ {
			if i < len(call.Arguments) && strings.ToUpper(call.Arguments[i].String()) == "GMT" {
				isGMT = true
				args = append(args[:i], args[i+1:]...)
				break
			}
		}
		if isGMT {
			now = now.UTC()
		}

		hour, min := now.Hour(), now.Minute()

		switch len(args) {
		case 1:
			return vm.ToValue(hour == args[0])
		case 2:
			return vm.ToValue(hour >= args[0] && hour <= args[1])
		case 4:
			start := args[0]*60 + args[1]
			end := args[2]*60 + args[3]
			nowMin := hour*60 + min
			if start <= end {
				return vm.ToValue(nowMin >= start && nowMin <= end)
			}
			return vm.ToValue(nowMin >= start || nowMin <= end)
		}
		return vm.ToValue(false)
	})
}

func shellMatch(str, pattern string) bool {
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

func maskOnes(maskIP net.IP) int {
	maskIP = maskIP.To4()
	if maskIP == nil {
		return 32
	}
	ones := 0
	for _, b := range maskIP {
		for b > 0 {
			ones++
			b <<= 1
		}
	}
	return ones
}
