package proxy

import (
	"net"
	"net/url"
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
