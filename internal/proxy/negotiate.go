package proxy

import (
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// negotiateDial is set by platform-specific init() functions.
// It attempts Kerberos/Negotiate auth to the upstream proxy, performing the
// full CONNECT+407 handshake. Returns the established conn on success, or
// a non-nil error on failure (no TGT, proxy doesn't support Negotiate, etc.)
// so the caller can fall back to NTLM/Basic.
var negotiateDial func(proxyURL *url.URL, target string, dialer *net.Dialer) (net.Conn, error)

// canonicalizeHostname resolves host to its canonical FQDN for use as a
// Kerberos SPN. It does a forward lookup (host→IP) then a reverse lookup
// (IP→name). If either step fails it returns the original hostname.
//
// Example: "proxy" → "proxy.corp.company.com." → "HTTP/proxy.corp.company.com"
func canonicalizeHostname(host string) string {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}

	ips, err := net.LookupIP(h)
	if err != nil || len(ips) == 0 {
		return h
	}

	names, err := net.LookupAddr(ips[0].String())
	if err != nil || len(names) == 0 {
		return h
	}

	return names[0]
}

// negotiateFailCache caches Negotiate failures per proxy hostname.
// After negotiation fails, subsequent attempts are skipped for a while
// to avoid hammering the KDC/proxy. The cache is invalidated on config
// reload or after a TTL.
type negotiateFailCache struct {
	mu       sync.Mutex
	entries  map[string]*negotiateCacheEntry
}

type negotiateCacheEntry struct {
	err     string
	expires time.Time
}

var negCache = &negotiateFailCache{
	entries: make(map[string]*negotiateCacheEntry),
}

var negCacheTTL atomic.Int64

func init() {
	negCacheTTL.Store(int64(30 * time.Second))
}

// SetNegotiateCacheTTL sets how long a Negotiate failure is cached.
func SetNegotiateCacheTTL(d time.Duration) {
	negCacheTTL.Store(int64(d))
}

// skipNegotiate checks whether Negotiate should be skipped for the given
// proxy host because it has failed recently. Returns the cached error
// message (empty if no cached failure).
func skipNegotiate(proxyHost string) (string, bool) {
	negCache.mu.Lock()
	defer negCache.mu.Unlock()

	e, ok := negCache.entries[proxyHost]
	if !ok {
		return "", false
	}
	if time.Now().Before(e.expires) {
		return e.err, true
	}
	delete(negCache.entries, proxyHost)
	return "", false
}

// recordNegotiateFailure records that Negotiate failed for proxyHost.
func recordNegotiateFailure(proxyHost, errMsg string) {
	ttl := time.Duration(negCacheTTL.Load())

	negCache.mu.Lock()
	defer negCache.mu.Unlock()

	negCache.entries[proxyHost] = &negotiateCacheEntry{
		err:     errMsg,
		expires: time.Now().Add(ttl),
	}
}

// clearNegotiateFailure removes a cached failure for proxyHost.
func clearNegotiateFailure(proxyHost string) {
	negCache.mu.Lock()
	defer negCache.mu.Unlock()
	delete(negCache.entries, proxyHost)
}

// ClearNegotiateCache clears all cached Negotiate failures.
// Called on config reload so that changed proxy settings are re-evaluated.
func ClearNegotiateCache() {
	negCache.mu.Lock()
	defer negCache.mu.Unlock()
	negCache.entries = make(map[string]*negotiateCacheEntry)
}

func hasNegotiateScheme(headers []string) bool {
	for _, h := range headers {
		scheme := strings.SplitN(h, " ", 2)[0]
		if strings.EqualFold(strings.TrimSpace(scheme), "negotiate") {
			return true
		}
	}
	return false
}
