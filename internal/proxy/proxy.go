package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aus/proxyplease"

	"github.com/wstucco/proxy-router/internal/config"
	logger "github.com/wstucco/proxy-router/internal/log"
	"github.com/wstucco/proxy-router/internal/pac"
	"github.com/wstucco/proxy-router/internal/router"
)

var pkgLog = logger.New(logger.LevelDebug, "proxy")

type Server struct {
	cfg     *config.Config
	certMgr interface {
		CertForHost(hostname string) (*tls.Certificate, error)
	}
}

func New(cfg *config.Config) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) SetCertManager(mgr interface {
	CertForHost(hostname string) (*tls.Certificate, error)
}) {
	s.certMgr = mgr
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleCONNECT(w, r)
		return
	}
	// Proxied HTTP requests carry an absolute URI; a relative one means a
	// direct hit on the listen port (control endpoint, not proxy traffic).
	if !r.URL.IsAbs() {
		s.handleControl(w, r)
		return
	}
	s.handleHTTP(w, r)
}

func (s *Server) handleCONNECT(w http.ResponseWriter, r *http.Request) {
	decision := router.Decide(s.cfg, r.Host)

	// Evaluate PAC script if configured for this location.
	// For CONNECT we construct a URL from the target host.
	// Strip port from host for PAC evaluation; detect scheme from port (443 → https, others → http).
	pacHost := r.Host
	pacScheme := "https"
	if h, p, err := net.SplitHostPort(r.Host); err == nil {
		pacHost = h
		if p != "443" {
			pacScheme = "http"
		}
	}
	pacURL := pacScheme + "://" + pacHost
	if err := applyPAC(&decision, pacURL, pacHost); err != nil {
		pkgLog.Warn("PAC eval failed for CONNECT %s: %v — using static config", r.Host, err)
	}

	// MITM mode: only trigger when the CONNECT host matches at least one route prefix.
	// Non-matching hosts use the normal tunnel (faster, no TLS overhead).
	if len(decision.Routes) > 0 && s.certMgr != nil {
		host := stripPort(r.Host)
		for prefix := range decision.Routes {
			if strings.HasPrefix(host, prefix) {
				s.handleMITM(w, r, &decision)
				return
			}
		}
	}

	dialer := makeDialer(decision.DNS)

	var targetConn net.Conn
	var err error

	if decision.ProxyURL != "" {
		targetConn, err = dialViaUpstream(decision.ProxyURL, decision.Domain, r.Host, dialer)
	} else {
		targetConn, err = dialer.DialContext(context.Background(), "tcp", r.Host)
	}

	if err != nil {
		pkgLog.Error("%s %s failed: %v", r.Host, decision.RouteString(), err)
		http.Error(w, fmt.Sprintf("failed to connect: %v", err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		pkgLog.Error("CONNECT %s: hijacking not supported", r.Host)
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		pkgLog.Error("CONNECT %s: hijack error: %v", r.Host, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	pkgLog.Debug("%s %s", r.Host, decision.RouteString())

	e := trackConn(kindConnect, r.RemoteAddr, r.Host, decision.RouteLoc(), decision.RouteDest())
	defer e.Close()

	done := make(chan struct{}, 2)
	go func() { io.Copy(targetConn, countReader{clientConn, &e.bytesUp}); done <- struct{}{} }()
	go func() { io.Copy(clientConn, countReader{targetConn, &e.bytesDown}); done <- struct{}{} }()
	<-done
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	decision := router.Decide(s.cfg, r.Host)

	// Evaluate PAC script if configured for this location.
	// Strip port from host for PAC evaluation: PAC patterns typically match hostnames only,
	// not host:port combinations. For example, "*.example.com" won't match "host.example.com:8080".
	pacHost := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		pacHost = h
	}
	pacURL := r.URL.String()
	if err := applyPAC(&decision, pacURL, pacHost); err != nil {
		pkgLog.Warn("PAC eval failed for HTTP %s: %v — using static config", r.Host, err)
	}

	e := trackConn(kindHTTP, r.RemoteAddr, r.Host, decision.RouteLoc(), decision.RouteDest())
	defer e.Close()
	if r.Body != nil {
		r.Body = struct {
			io.Reader
			io.Closer
		}{countReader{r.Body, &e.bytesUp}, r.Body}
	}

	dialer := makeDialer(decision.DNS)

	// Apply route rewriting: if host+path matches a route prefix,
	// redirect this request to the route's target URL directly.
	routed := false
	if targetURL, err := applyRoute(decision.Routes, r.Host, r.URL.Path, r.URL.RawQuery); err != nil {
		pkgLog.Error("HTTP %s %s route error: %v", r.Method, r.Host+requestPath(r), err)
		http.Error(w, fmt.Sprintf("invalid route target: %v", err), http.StatusBadGateway)
		return
	} else if targetURL != nil {
		routed = true
		pkgLog.Info("HTTP %s %s route → %s", r.Method, r.Host+requestPath(r), targetURL.String())
		r.URL = targetURL
		r.Host = targetURL.Host
		e.SetDest(r.Host)
	}

	var transport http.RoundTripper

	if shouldUseUpstreamProxy(&decision, r.Host, routed) {
		pkgLog.Info("HTTP %s %s via upstream", r.Method, r.Host)
		upstreamURL, err := url.Parse(decision.ProxyURL)
		if err != nil {
			pkgLog.Error("HTTP %s: invalid upstream URL: %v", r.Host, err)
			http.Error(w, "invalid upstream URL", http.StatusInternalServerError)
			return
		}
		transport = &http.Transport{
			Proxy:       http.ProxyURL(upstreamURL),
			DialContext: dialer.DialContext,
		}
	} else {
		pkgLog.Debug("HTTP %s %s direct", r.Method, r.Host)
		transport = &http.Transport{
			DialContext: dialer.DialContext,
		}
	}

	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authenticate")
	r.Header.Del("Proxy-Authorization")

	resp, err := transport.RoundTrip(r)
	if err != nil {
		pkgLog.Error("%s %s failed: %v", r.Host, decision.RouteString(), err)
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	pkgLog.Debug("HTTP %s %s → %d", r.Method, r.Host, resp.StatusCode)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, countReader{resp.Body, &e.bytesDown})
}

func (s *Server) handleMITM(w http.ResponseWriter, r *http.Request, decision *config.Decision) {
	hostname := stripPort(r.Host)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientRaw, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientRaw.Close()

	cert, err := s.certMgr.CertForHost(hostname)
	if err != nil {
		_ = sendHTTPError(clientRaw, http.StatusBadGateway, "certificate generation failed")
		pkgLog.Error("MITM %s: cert generation failed: %v", r.Host, err)
		return
	}

	_, _ = clientRaw.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	clientTLS := tls.Server(clientRaw, &tls.Config{Certificates: []tls.Certificate{*cert}})
	if err := clientTLS.Handshake(); err != nil {
		pkgLog.Error("MITM %s: client TLS handshake failed: %v", r.Host, err)
		return
	}
	defer clientTLS.Close()

	pkgLog.Info("MITM %s tunnel open (routes: %d)", r.Host, len(decision.Routes))

	// One entry per client TLS tunnel; dest is updated per decrypted request.
	// The via string stays tunnel-level even if per-request PAC differs —
	// documented simplification.
	e := trackConn(kindMITM, r.RemoteAddr, r.Host, decision.RouteLoc(), decision.RouteDest())
	defer e.Close()

	s.mitmProxy(clientTLS, hostname, decision, e)
}

func (s *Server) mitmProxy(clientTLS *tls.Conn, origHost string, decision *config.Decision, e *ConnEntry) {
	dialer := makeDialer(decision.DNS)
	for {
		req, err := http.ReadRequest(bufio.NewReader(clientTLS))
		if err != nil {
			break
		}

		// Determine target: apply route rewriting or fall back to original host
		routed := false
		targetHost := origHost
		if targetURL, err := applyRoute(decision.Routes, origHost, req.URL.Path, req.URL.RawQuery); err != nil {
			pkgLog.Error("MITM: route error for %s: %v", req.URL.String(), err)
			_ = sendHTTPError(clientTLS, http.StatusBadGateway, fmt.Sprintf("invalid route target: %v", err))
			break
		} else if targetURL != nil {
			routed = true
			targetHost = targetURL.Host
			req.URL = targetURL
			req.Host = targetURL.Host
		} else {
			req.URL.Scheme = "https"
			req.URL.Host = origHost
		}

		// Ensure the target has an explicit port for upstream proxy dialing.
		// Route URLs often omit the default port, but the upstream proxy
		// requires it in the CONNECT request.
		if _, _, err := net.SplitHostPort(targetHost); err != nil {
			targetHost, err = addDefaultPort(targetHost, req.URL.Scheme)
			if err != nil {
				pkgLog.Error("MITM: %v", err)
				_ = sendHTTPError(clientTLS, http.StatusBadGateway, err.Error())
				break
			}
		}

		if routed {
			pkgLog.Info("MITM %s route → %s", req.URL.String(), targetHost)
		}
		e.SetDest(targetHost)

		pkgLog.Debug("MITM %s %s", req.Method, req.URL.String())

		// Evaluate PAC per-request in MITM mode — each decrypted request
		// may target a different host, so the PAC decision may differ.
		// Strip port from host for PAC evaluation (see handleHTTP for details).
		perReqDecision := *decision
		mitmURL := req.URL.String()
		pacHost := req.Host
		if h, _, err := net.SplitHostPort(req.Host); err == nil {
			pacHost = h
		}
		if err := applyPAC(&perReqDecision, mitmURL, pacHost); err != nil {
			pkgLog.Warn("MITM PAC eval for %s: %v — using location config", req.Host, err)
		}

		// Dial target — routed requests still honor the location's upstream
		// proxy unless the destination matches no_proxy.
		var targetConn net.Conn
		if shouldUseUpstreamProxy(&perReqDecision, targetHost, routed) {
			targetConn, err = dialViaUpstream(perReqDecision.ProxyURL, perReqDecision.Domain, targetHost, dialer)
		} else {
			targetConn, err = dialer.DialContext(context.Background(), "tcp", targetHost)
		}
		if err != nil {
			pkgLog.Error("MITM: dial %s failed: %v", targetHost, err)
			break
		}

		targetTLS := tls.Client(targetConn, &tls.Config{ServerName: stripPort(targetHost)})
		if err := targetTLS.Handshake(); err != nil {
			targetConn.Close()
			pkgLog.Error("MITM: TLS handshake to %s failed: %v", targetHost, err)
			break
		}

		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authenticate")
		req.Header.Del("Proxy-Authorization")

		if err := req.Write(countWriter{targetTLS, &e.bytesUp}); err != nil {
			targetTLS.Close()
			pkgLog.Error("MITM: write request to %s failed: %v", targetHost, err)
			break
		}

		resp, err := http.ReadResponse(bufio.NewReader(targetTLS), req)
		if err != nil {
			targetTLS.Close()
			pkgLog.Error("MITM: read response from %s failed: %v", targetHost, err)
			break
		}

		if err := resp.Write(countWriter{clientTLS, &e.bytesDown}); err != nil {
			resp.Body.Close()
			pkgLog.Error("MITM: write response to client failed: %v", err)
			break
		}
		resp.Body.Close()

		if req.Close {
			break
		}
	}
}

func applyRoute(routes map[string]string, host, path, query string) (*url.URL, error) {
	matchKey := strings.TrimRight(host+path, "/")
	var bestPrefix string
	var bestTarget string
	for prefix := range routes {
		p := strings.TrimRight(prefix, "/")
		if strings.HasPrefix(matchKey, p) && len(p) > len(bestPrefix) {
			bestPrefix = p
			bestTarget = routes[prefix]
		}
	}
	if bestPrefix == "" {
		return nil, nil
	}
	base, err := parseRouteTarget(bestTarget)
	if err != nil {
		return nil, fmt.Errorf("parse target %q: %w", bestTarget, err)
	}
	suffix := strings.TrimPrefix(matchKey, bestPrefix)
	base.Path = strings.TrimRight(base.Path, "/") + suffix
	if query != "" {
		base.RawQuery = query
	}
	return base, nil
}

func parseRouteTarget(target string) (*url.URL, error) {
	if target == "" {
		return nil, fmt.Errorf("empty route target")
	}

	if strings.Contains(target, "://") {
		u, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		if _, err := defaultPortForScheme(u.Scheme); err != nil {
			return nil, err
		}
		return u, nil
	}

	u, err := url.Parse("https://" + strings.TrimPrefix(target, "//"))
	if err != nil {
		return nil, err
	}
	return u, nil
}

func addDefaultPort(host, scheme string) (string, error) {
	port, err := defaultPortForScheme(scheme)
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", fmt.Errorf("route target is missing host")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host, nil
	}
	return net.JoinHostPort(host, port), nil
}

// applyPAC evaluates the PAC script from decision.PAC and updates the decision
// with the resulting proxy URL or direct connection.
func applyPAC(decision *config.Decision, reqURL, host string) error {
	if decision.PAC == "" {
		return nil
	}

	result, err := pac.Eval(decision.PAC, reqURL, host)
	if err != nil {
		return err
	}

	if result.IsDirect() {
		pkgLog.Debug("PAC → DIRECT for %s", host)
		decision.ProxyURL = ""
	} else {
		pkgLog.Debug("PAC → PROXY %s for %s", result.Proxy, host)
		decision.ProxyURL = result.ProxyURL()
	}
	return nil
}

func shouldUseUpstreamProxy(decision *config.Decision, host string, routed bool) bool {
	if decision.ProxyURL == "" {
		return false
	}
	if routed && config.MatchNoProxy(stripPort(host), decision.NoProxy) {
		return false
	}
	return true
}

func defaultPortForScheme(scheme string) (string, error) {
	switch scheme {
	case "", "https":
		return "443", nil
	case "http":
		return "80", nil
	default:
		return "", fmt.Errorf("unsupported route target scheme %q", scheme)
	}
}

func sendHTTPError(w io.Writer, status int, msg string) error {
	body := msg + "\n"
	_, err := fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, http.StatusText(status), len(body), body)
	return err
}

func requestPath(r *http.Request) string {
	if r.URL.Path != "" {
		return r.URL.Path
	}
	return "/"
}

func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func dialViaUpstream(proxyURL, domain, target string, dialer *net.Dialer) (net.Conn, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	// Try Negotiate/Kerberos auth first (macOS GSS.framework, others TBD).
	// Uses the system credential cache — no config needed, survives password
	// changes within the TGT renewal window.
	var negotiateErr error
	if negotiateDial != nil {
		if cachedErr, skip := skipNegotiate(u.Host); skip {
			pkgLog.Debug("auth Negotiate skipped for %s (cached: %s)", u.Host, cachedErr)
		} else {
			conn, err := negotiateDial(u, target, dialer)
			if err == nil {
				clearNegotiateFailure(u.Host)
				pkgLog.Info("auth Negotiate for %s", u.Host)
				return conn, nil
			}
			recordNegotiateFailure(u.Host, err.Error())
			pkgLog.Debug("auth Negotiate failed for %s: %v", u.Host, err)
			negotiateErr = err
		}
	}

	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	// If the proxy URL came from PAC (no embedded credentials) and
	// Negotiate was already attempted, don't waste time falling
	// through to NTLM/Basic — it will also fail without credentials.
	if negotiateErr != nil && user == "" && pass == "" {
		return nil, fmt.Errorf("auth Negotiate failed for %s: %w", u.Host, negotiateErr)
	}

	pkgLog.Debug("auth NTLM/Basic for %s via proxyplease", u.Host)

	dialCtx := proxyplease.NewDialContext(proxyplease.Proxy{
		URL:      u,
		Username: user,
		Password: pass,
		Domain:   domain,
	})

	conn, err := dialCtx(context.Background(), "tcp", target)
	if err != nil {
		if user == "" && pass == "" && negotiateErr == nil {
			pkgLog.Warn("no credentials for %s — Kerberos TGT or proxy URL password required", u.Host)
		}
		return nil, fmt.Errorf("dialing upstream %s: %w", u.Host, err)
	}

	return conn, nil
}

func makeDialer(dnsServers []string) *net.Dialer {
	if len(dnsServers) == 0 {
		return &net.Dialer{Timeout: 10 * time.Second}
	}

	addrs := make([]string, len(dnsServers))
	for i, s := range dnsServers {
		if _, _, err := net.SplitHostPort(s); err != nil {
			addrs[i] = net.JoinHostPort(s, "53")
		} else {
			addrs[i] = s
		}
	}

	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 5 * time.Second}
			var lastErr error
			for _, addr := range addrs {
				conn, err := d.DialContext(ctx, "udp", addr)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}

	pkgLog.Debug("using custom DNS: %v", dnsServers)

	return &net.Dialer{
		Timeout:  10 * time.Second,
		Resolver: resolver,
	}
}
