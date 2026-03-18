package proxy

import (
	"bufio"
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
	action := router.Decide(s.cfg, r.Host)

	var targetConn net.Conn
	var err error

	if action == config.ActionUpstream && s.cfg.Upstream != "" {
		targetConn, err = dialViaUpstream(s.cfg.Upstream, r.Host)
	} else {
		targetConn, err = net.DialTimeout("tcp", r.Host, 10*time.Second)
	}

	if err != nil {
		http.Error(w, fmt.Sprintf("failed to connect: %v", err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Signal success to client
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Bidirectional copy
	done := make(chan struct{}, 2)
	go func() { io.Copy(targetConn, clientConn); done <- struct{}{} }()
	go func() { io.Copy(clientConn, targetConn); done <- struct{}{} }()
	<-done
}

// handleHTTP handles plain HTTP proxy requests.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	action := router.Decide(s.cfg, r.Host)

	var transport http.RoundTripper

	if action == config.ActionUpstream && s.cfg.Upstream != "" {
		upstreamURL, err := url.Parse(s.cfg.Upstream)
		if err != nil {
			http.Error(w, "invalid upstream URL", http.StatusInternalServerError)
			return
		}
		transport = &http.Transport{Proxy: http.ProxyURL(upstreamURL)}
	} else {
		transport = &http.Transport{}
	}

	// Clean up request for forwarding
	r.RequestURI = ""
	r.Header.Del("Proxy-Connection")
	r.Header.Del("Proxy-Authenticate")
	r.Header.Del("Proxy-Authorization")

	resp, err := transport.RoundTrip(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// dialViaUpstream opens a TCP tunnel through an HTTP CONNECT upstream proxy.
func dialViaUpstream(upstream, target string) (net.Conn, error) {
	u, err := url.Parse(upstream)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream: %w", err)
	}

	proxyHost := u.Host
	conn, err := net.DialTimeout("tcp", proxyHost, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dialing upstream proxy %s: %w", proxyHost, err)
	}

	// Build CONNECT request, adding auth header if credentials are present
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	if u.User != nil {
		pass, _ := u.User.Password()
		creds := base64.StdEncoding.EncodeToString([]byte(u.User.Username() + ":" + pass))
		req += "Proxy-Authorization: Basic " + creds + "\r\n"
	}
	req += "\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sending CONNECT: %w", err)
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
		return nil, fmt.Errorf("upstream proxy returned %d", resp.StatusCode)
	}

	log.Printf("[proxy] tunnel established via upstream to %s", target)
	return conn, nil
}
