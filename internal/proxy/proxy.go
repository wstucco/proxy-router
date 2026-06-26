package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
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

func shortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%08x", b)
}

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
	log := pkgLog.WithCorrelation(shortID())
	if r.Method == http.MethodConnect {
		s.handleCONNECT(w, r, log)
	} else {
		s.handleHTTP(w, r, log)
	}
}

func (s *Server) handleCONNECT(w http.ResponseWriter, r *http.Request, log *logger.Logger) {
	decision := router.Decide(s.cfg, r.Host)

	// Evaluate PAC script if configured for this location.
	// For CONNECT we construct a URL from the target host.
	pacURL := "https://" + r.Host
	if err := applyPAC(&decision, pacURL, r.Host, log); err != nil {
		log.Warn("PAC eval failed for CONNECT %s: %v — using static config", r.Host, err)
	}

	// MITM mode: only trigger when the CONNECT host matches at least one route prefix.
	// Non-matching hosts use the normal tunnel (faster, no TLS overhead).
	if len(decision.Routes) > 0 && s.certMgr != nil {
		host := stripPort(r.Host)
		for prefix := range decision.Routes {
			if strings.HasPrefix(host, prefix) {
				s.handleMITM(w, r, &decision, log)
				return
			}
		}
	}

	dialer := makeDialer(decision.DNS, log)

	var targetConn net.Conn
	var err error

	if decision.ProxyURL != "" {
		log.Info("CONNECT %s via upstream", r.Host)
		targetConn, err = dialViaUpstream(decision.ProxyURL, decision.Domain, r.Host, dialer, log)
	} else {
		log.Info("CONNECT %s direct", r.Host)
		targetConn, err = dialer.DialContext(context.Background(), "tcp", r.Host)
	}

	if err != nil {
		log.Error("CONNECT %s failed: %v", r.Host, err)
		http.Error(w, fmt.Sprintf("failed to connect: %v", err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Error("CONNECT %s: hijacking not supported", r.Host)
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Error("CONNECT %s: hijack error: %v", r.Host, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	log.Debug("CONNECT %s tunnel open", r.Host)

	done := make(chan struct{}, 2)
	go func() { io.Copy(targetConn, clientConn); done <- struct{}{} }()
	go func() { io.Copy(clientConn, targetConn); done <- struct{}{} }()
	<-done
	log.Debug("CONNECT %s tunnel closed", r.Host)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request, log *logger.Logger) {
	decision := router.Decide(s.cfg, r.Host)

	// Evaluate PAC script if configured for this location.
	pacURL := r.URL.String()
	if err := applyPAC(&decision, pacURL, r.Host, log); err != nil {
		log.Warn("PAC eval failed for HTTP %s: %v — using static config", r.Host, err)
	}

	dialer := makeDialer(decision.DNS, log)

	// Apply route rewriting: if host+path matches a route prefix,
	// redirect this request to the route's target URL directly.
	routed := false
	if targetURL, err := applyRoute(decision.Routes, r.Host, r.URL.Path, r.URL.RawQuery); err != nil {
		log.Error("HTTP %s %s route error: %v", r.Method, r.Host+requestPath(r), err)
		http.Error(w, fmt.Sprintf("invalid route target: %v", err), http.StatusBadGateway)
		return
	} else if targetURL != nil {
		routed = true
		log.Info("HTTP %s %s route → %s", r.Method, r.Host+requestPath(r), targetURL.String())
		r.URL = targetURL
		r.Host = targetURL.Host
	}

	var transport http.RoundTripper

	if shouldUseUpstreamProxy(&decision, r.Host, routed) {
		log.Info("HTTP %s %s via upstream", r.Method, r.Host)
		upstreamURL, err := url.Parse(decision.ProxyURL)
		if err != nil {
			log.Error("HTTP %s: invalid upstream URL: %v", r.Host, err)
			http.Error(w, "invalid upstream URL", http.StatusInternalServerError)
			return
		}
		transport = &http.Transport{
			Proxy:       http.ProxyURL(upstreamURL),
			DialContext: dialer.DialContext,
		}
	} else {
		log.Debug("HTTP %s %s direct", r.Method, r.Host)
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
		log.Error("HTTP %s %s error: %v", r.Method, r.Host, err)
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Debug("HTTP %s %s → %d", r.Method, r.Host, resp.StatusCode)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) handleMITM(w http.ResponseWriter, r *http.Request, decision *config.Decision, log *logger.Logger) {
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
		log.Error("MITM %s: cert generation failed: %v", r.Host, err)
		return
	}

	_, _ = clientRaw.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	clientTLS := tls.Server(clientRaw, &tls.Config{Certificates: []tls.Certificate{*cert}})
	if err := clientTLS.Handshake(); err != nil {
		log.Error("MITM %s: client TLS handshake failed: %v", r.Host, err)
		return
	}
	defer clientTLS.Close()

	log.Info("MITM %s tunnel open (routes: %d)", r.Host, len(decision.Routes))
	s.mitmProxy(clientTLS, hostname, decision, log)
}

func (s *Server) mitmProxy(clientTLS *tls.Conn, origHost string, decision *config.Decision, log *logger.Logger) {
	dialer := makeDialer(decision.DNS, log)
	for {
		req, err := http.ReadRequest(bufio.NewReader(clientTLS))
		if err != nil {
			break
		}

		// Determine target: apply route rewriting or fall back to original host
		routed := false
		targetHost := origHost
		if targetURL, err := applyRoute(decision.Routes, origHost, req.URL.Path, req.URL.RawQuery); err != nil {
			log.Error("MITM: route error for %s: %v", req.URL.String(), err)
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
				log.Error("MITM: %v", err)
				_ = sendHTTPError(clientTLS, http.StatusBadGateway, err.Error())
				break
			}
		}

		if routed {
			log.Info("MITM %s route → %s", req.URL.String(), targetHost)
		}

		log.Debug("MITM %s %s", req.Method, req.URL.String())

		// Evaluate PAC per-request in MITM mode — each decrypted request
		// may target a different host, so the PAC decision may differ.
		perReqDecision := *decision
		mitmURL := req.URL.String()
		if err := applyPAC(&perReqDecision, mitmURL, req.Host, log); err != nil {
			log.Warn("MITM PAC eval for %s: %v — using location config", req.Host, err)
		}

		// Dial target — routed requests still honor the location's upstream
		// proxy unless the destination matches no_proxy.
		var targetConn net.Conn
		if shouldUseUpstreamProxy(&perReqDecision, targetHost, routed) {
			targetConn, err = dialViaUpstream(perReqDecision.ProxyURL, perReqDecision.Domain, targetHost, dialer, log)
		} else {
			targetConn, err = dialer.DialContext(context.Background(), "tcp", targetHost)
		}
		if err != nil {
			log.Error("MITM: dial %s failed: %v", targetHost, err)
			break
		}

		targetTLS := tls.Client(targetConn, &tls.Config{ServerName: stripPort(targetHost)})
		if err := targetTLS.Handshake(); err != nil {
			targetConn.Close()
			log.Error("MITM: TLS handshake to %s failed: %v", targetHost, err)
			break
		}

		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authenticate")
		req.Header.Del("Proxy-Authorization")

		if err := req.Write(targetTLS); err != nil {
			targetTLS.Close()
			log.Error("MITM: write request to %s failed: %v", targetHost, err)
			break
		}

		resp, err := http.ReadResponse(bufio.NewReader(targetTLS), req)
		targetTLS.Close()
		if err != nil {
			log.Error("MITM: read response from %s failed: %v", targetHost, err)
			break
		}

		if err := resp.Write(clientTLS); err != nil {
			resp.Body.Close()
			log.Error("MITM: write response to client failed: %v", err)
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
	for prefix, target := range routes {
		prefix = strings.TrimRight(prefix, "/")
		if strings.HasPrefix(matchKey, prefix) {
			base, err := parseRouteTarget(target)
			if err != nil {
				return nil, fmt.Errorf("parse target %q: %w", target, err)
			}
			suffix := strings.TrimPrefix(matchKey, prefix)
			base.Path = strings.TrimRight(base.Path, "/") + suffix
			if query != "" {
				base.RawQuery = query
			}
			return base, nil
		}
	}
	return nil, nil
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
func applyPAC(decision *config.Decision, reqURL, host string, log *logger.Logger) error {
	if decision.PAC == "" {
		return nil
	}

	result, err := pac.Eval(decision.PAC, reqURL, host)
	if err != nil {
		return err
	}

	if result.IsDirect() {
		log.Debug("PAC → DIRECT for %s", host)
		decision.ProxyURL = ""
	} else {
		log.Debug("PAC → PROXY %s for %s", result.Proxy, host)
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

func dialViaUpstream(proxyURL, domain, target string, dialer *net.Dialer, log *logger.Logger) (net.Conn, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	// Try Negotiate/Kerberos auth first (macOS GSS.framework, others TBD).
	// Uses the system credential cache — no config needed, survives password
	// changes within the TGT renewal window.
	negotiateTried := false
	if negotiateDial != nil {
		if cachedErr, skip := skipNegotiate(u.Host); skip {
			log.Debug("auth Negotiate skipped for %s (cached: %s)", u.Host, cachedErr)
		} else {
			negotiateTried = true
			conn, err := negotiateDial(u, target, dialer)
			if err == nil {
				clearNegotiateFailure(u.Host)
				log.Info("auth Negotiate for %s", u.Host)
				return conn, nil
			}
			recordNegotiateFailure(u.Host, err.Error())
			log.Debug("auth Negotiate failed for %s: %v", u.Host, err)
		}
	}

	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	log.Debug("auth NTLM/Basic for %s via proxyplease", u.Host)

	dialCtx := proxyplease.NewDialContext(proxyplease.Proxy{
		URL:      u,
		Username: user,
		Password: pass,
		Domain:   domain,
	})

	conn, err := dialCtx(context.Background(), "tcp", target)
	if err != nil {
		if user == "" && pass == "" && negotiateTried {
			log.Warn("no credentials for %s — Kerberos TGT or proxy URL password required", u.Host)
		}
		return nil, fmt.Errorf("dialing upstream %s: %w", u.Host, err)
	}

	return conn, nil
}

func makeDialer(dnsServers []string, log *logger.Logger) *net.Dialer {
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

	log.Debug("using custom DNS: %v", dnsServers)

	return &net.Dialer{
		Timeout:  10 * time.Second,
		Resolver: resolver,
	}
}
