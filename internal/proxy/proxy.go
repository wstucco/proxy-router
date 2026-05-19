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
	"github.com/wstucco/proxy-router/internal/router"
)

var pkgLog = logger.New(logger.LevelInfo, "proxy")

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

	// MITM mode for locations with routes (enables path-based HTTPS routing)
	if len(decision.Routes) > 0 && s.certMgr != nil {
		s.handleMITM(w, r, &decision, log)
		return
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
	dialer := makeDialer(decision.DNS, log)

	// Apply route rewriting: if host+path matches a route prefix,
	// redirect this request to the route's target URL directly.
	if targetURL := applyRoute(decision.Routes, r.Host, r.URL.Path, r.URL.RawQuery); targetURL != nil {
		log.Info("HTTP %s %s route → %s", r.Method, r.Host+requestPath(r), targetURL.String())
		r.URL = targetURL
		r.Host = targetURL.Host
		// Route matched — go direct to the target, bypass location proxy
		decision.ProxyURL = ""
	}

	var transport http.RoundTripper

	if decision.ProxyURL != "" {
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

	_, _ = clientRaw.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	cert, err := s.certMgr.CertForHost(hostname)
	if err != nil {
		log.Error("MITM %s: cert generation failed: %v", r.Host, err)
		return
	}

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
		if targetURL := applyRoute(decision.Routes, origHost, req.URL.Path, req.URL.RawQuery); targetURL != nil {
			routed = true
			targetHost = targetURL.Host
			req.URL = targetURL
			req.Host = targetURL.Host
		} else {
			req.URL.Scheme = "https"
			req.URL.Host = origHost
		}

		if routed {
			log.Info("MITM %s route → %s", req.URL.String(), targetHost)
		}

		log.Debug("MITM %s %s", req.Method, req.URL.String())

		// Dial target — routed requests always go direct, non-routed may use upstream proxy
		var targetConn net.Conn
		if !routed && decision.ProxyURL != "" {
			targetConn, err = dialViaUpstream(decision.ProxyURL, decision.Domain, targetHost, dialer, log)
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

func applyRoute(routes map[string]string, host, path, query string) *url.URL {
	matchKey := strings.TrimRight(host+path, "/")
	for prefix, target := range routes {
		prefix = strings.TrimRight(prefix, "/")
		if strings.HasPrefix(matchKey, prefix) {
			base, err := url.Parse(target)
			if err != nil {
				continue
			}
			suffix := strings.TrimPrefix(matchKey, prefix)
			base.Path = strings.TrimRight(base.Path, "/") + suffix
			if query != "" {
				base.RawQuery = query
			}
			return base
		}
	}
	return nil
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

	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	log.Debug("dialing upstream %s", u.Host)

	dialCtx := proxyplease.NewDialContext(proxyplease.Proxy{
		URL:      u,
		Username: user,
		Password: pass,
		Domain:   domain,
	})

	conn, err := dialCtx(context.Background(), "tcp", target)
	if err != nil {
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
