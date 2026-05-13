package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aus/proxyplease"

	"github.com/wstucco/proxy-router/internal/config"
	"github.com/wstucco/proxy-router/internal/router"
)

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
	} else {
		s.handleHTTP(w, r)
	}
}

func (s *Server) handleCONNECT(w http.ResponseWriter, r *http.Request) {
	decision := router.Decide(s.cfg, r.Host)

	// MITM mode for locations with routes (enables path-based HTTPS routing)
	if len(decision.Routes) > 0 && s.certMgr != nil {
		s.handleMITM(w, r, &decision)
		return
	}

	dialer := makeDialer(decision.DNS)

	var targetConn net.Conn
	var err error

	if decision.ProxyURL != "" {
		log.Printf("[proxy] CONNECT %s via upstream", r.Host)
		targetConn, err = dialViaUpstream(decision.ProxyURL, decision.Domain, r.Host, dialer)
	} else {
		log.Printf("[proxy] CONNECT %s direct", r.Host)
		targetConn, err = dialer.DialContext(context.Background(), "tcp", r.Host)
	}

	if err != nil {
		log.Printf("[proxy] CONNECT %s failed: %v", r.Host, err)
		http.Error(w, fmt.Sprintf("failed to connect: %v", err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("[proxy] CONNECT %s: hijacking not supported", r.Host)
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("[proxy] CONNECT %s: hijack error: %v", r.Host, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	log.Printf("[proxy] CONNECT %s tunnel open", r.Host)

	done := make(chan struct{}, 2)
	go func() { io.Copy(targetConn, clientConn); done <- struct{}{} }()
	go func() { io.Copy(clientConn, targetConn); done <- struct{}{} }()
	<-done
	log.Printf("[proxy] CONNECT %s tunnel closed", r.Host)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	decision := router.Decide(s.cfg, r.Host)
	dialer := makeDialer(decision.DNS)

	// Apply route rewriting: if host+path matches a route prefix,
	// redirect this request to the route's target URL directly.
	if targetURL := applyRoute(decision.Routes, r.Host, r.URL.Path, r.URL.RawQuery); targetURL != nil {
		log.Printf("[proxy] HTTP %s %s route → %s", r.Method, r.Host+requestPath(r), targetURL.String())
		r.URL = targetURL
		r.Host = targetURL.Host
		// Route matched — go direct to the target, bypass location proxy
		decision.ProxyURL = ""
	}

	var transport http.RoundTripper

	if decision.ProxyURL != "" {
		log.Printf("[proxy] HTTP %s %s via upstream", r.Method, r.Host)
		upstreamURL, err := url.Parse(decision.ProxyURL)
		if err != nil {
			log.Printf("[proxy] HTTP %s: invalid upstream URL: %v", r.Host, err)
			http.Error(w, "invalid upstream URL", http.StatusInternalServerError)
			return
		}
		transport = &http.Transport{
			Proxy:       http.ProxyURL(upstreamURL),
			DialContext: dialer.DialContext,
		}
	} else {
		log.Printf("[proxy] HTTP %s %s direct", r.Method, r.Host)
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
		log.Printf("[proxy] HTTP %s %s error: %v", r.Method, r.Host, err)
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("[proxy] HTTP %s %s → %d", r.Method, r.Host, resp.StatusCode)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
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

	_, _ = clientRaw.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	cert, err := s.certMgr.CertForHost(hostname)
	if err != nil {
		log.Printf("[proxy] MITM %s: cert generation failed: %v", r.Host, err)
		return
	}

	clientTLS := tls.Server(clientRaw, &tls.Config{Certificates: []tls.Certificate{*cert}})
	if err := clientTLS.Handshake(); err != nil {
		log.Printf("[proxy] MITM %s: client TLS handshake failed: %v", r.Host, err)
		return
	}
	defer clientTLS.Close()

	log.Printf("[proxy] MITM %s tunnel open (routes: %d)", r.Host, len(decision.Routes))
	s.mitmProxy(clientTLS, hostname, decision)
}

func (s *Server) mitmProxy(clientTLS *tls.Conn, origHost string, decision *config.Decision) {
	dialer := makeDialer(decision.DNS)
	for {
		req, err := http.ReadRequest(bufio.NewReader(clientTLS))
		if err != nil {
			break
		}

		// Determine target: apply route rewriting or fall back to original host
		targetHost := origHost
		if targetURL := applyRoute(decision.Routes, origHost, req.URL.Path, req.URL.RawQuery); targetURL != nil {
			targetHost = targetURL.Host
			req.URL = targetURL
			req.Host = targetURL.Host
		} else {
			// Reconstruct full URL for the original host
			req.URL.Scheme = "https"
			req.URL.Host = origHost
		}

		log.Printf("[proxy] MITM %s %s", req.Method, req.URL.String())

		// Dial target (always a fresh connection per request)
		targetConn, err := dialer.DialContext(context.Background(), "tcp", targetHost)
		if err != nil {
			log.Printf("[proxy] MITM: dial %s failed: %v", targetHost, err)
			break
		}

		targetTLS := tls.Client(targetConn, &tls.Config{ServerName: stripPort(targetHost)})
		if err := targetTLS.Handshake(); err != nil {
			targetConn.Close()
			log.Printf("[proxy] MITM: TLS handshake to %s failed: %v", targetHost, err)
			break
		}

		req.RequestURI = ""
		req.Header.Del("Proxy-Connection")
		req.Header.Del("Proxy-Authenticate")
		req.Header.Del("Proxy-Authorization")

		if err := req.Write(targetTLS); err != nil {
			targetTLS.Close()
			log.Printf("[proxy] MITM: write request to %s failed: %v", targetHost, err)
			break
		}

		resp, err := http.ReadResponse(bufio.NewReader(targetTLS), req)
		targetTLS.Close()
		if err != nil {
			log.Printf("[proxy] MITM: read response from %s failed: %v", targetHost, err)
			break
		}

		if err := resp.Write(clientTLS); err != nil {
			resp.Body.Close()
			log.Printf("[proxy] MITM: write response to client failed: %v", err)
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

func dialViaUpstream(proxyURL, domain, target string, dialer *net.Dialer) (net.Conn, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}

	log.Printf("[proxy] dialing upstream %s", u.Host)

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

	log.Printf("[proxy] using custom DNS: %v", dnsServers)

	return &net.Dialer{
		Timeout:  10 * time.Second,
		Resolver: resolver,
	}
}
