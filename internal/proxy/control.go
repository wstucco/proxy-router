package proxy

import (
	"encoding/json"
	"net"
	"net/http"
)

// handleControl serves direct (non-proxied) requests hitting the listen
// port — they carry a relative URI, proxied requests an absolute one.
// Loopback only: the proxy may listen on a non-localhost address.
// Control requests are never tracked in the connection registry.
func (s *Server) handleControl(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodGet && r.URL.Path == "/_pr/connections" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Connections []ConnInfo `json:"connections"`
		}{SnapshotConnections()})
		return
	}

	http.Error(w, "proxy-router: unknown control path", http.StatusNotFound)
}
