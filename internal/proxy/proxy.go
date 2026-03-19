package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/wstucco/proxy-router/internal/config"
	"github.com/wstucco/proxy-router/internal/router"
)

type Server struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleCONNECT(w, r)
	} else {
		s.handleHTTP(w, r)
	}
}

// handleCONNECT handles HTTPS tunneling (CONNECT method).
func (s *Server) handleCONNECT(w http.ResponseWriter, r *http.Request) {
	decision := router.Decide(s.cfg, r.Host)
	dialer := makeDialer(decision.DNS)

	var targetConn net.Conn
	var err error

	if decision.Action == config.ActionUpstream && s.cfg.Upstream != "" {
		log.Printf("[proxy] CONNECT %s via upstream", r.Host)
		targetConn, err = dialViaUpstream(s.cfg.Upstream, r.Host, dialer)
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

// handleHTTP handles plain HTTP proxy requests.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	decision := router.Decide(s.cfg, r.Host)
	dialer := makeDialer(decision.DNS)

	var transport http.RoundTripper

	if decision.Action == config.ActionUpstream && s.cfg.Upstream != "" {
		log.Printf("[proxy] HTTP %s %s via upstream", r.Method, r.Host)
		upstreamURL, err := url.Parse(s.cfg.Upstream)
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

// makeDialer returns a dialer using custom DNS servers if provided,
// otherwise uses the system default resolver.
func makeDialer(dnsServers []string) *net.Dialer {
	if len(dnsServers) == 0 {
		return &net.Dialer{Timeout: 10 * time.Second}
	}

	// Build DNS addresses with port 53
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
			// Try each DNS server in order
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

// dialViaUpstream opens a TCP tunnel through an HTTP CONNECT upstream proxy.
func dialViaUpstream(upstream, target string, dialer *net.Dialer) (net.Conn, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL: %w", err)
	}

	proxyHost := u.Host
	log.Printf("[proxy] dialing upstream %s", proxyHost)
	conn, err := dialer.DialContext(context.Background(), "tcp", proxyHost)
	if err != nil {
		return nil, fmt.Errorf("dialing upstream %s: %w", proxyHost, err)
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if u.User != nil {
		pass, _ := u.User.Password()
		creds := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + pass))
		req += "Proxy-Authorization: Basic " + creds + "\r\n"
	}
	req += "\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sending CONNECT to upstream: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading upstream CONNECT response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("upstream returned %d %s", resp.StatusCode, resp.Status)
	}

	return conn, nil
}
