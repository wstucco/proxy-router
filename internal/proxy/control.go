package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
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

	if r.Method == http.MethodGet && r.URL.Path == "/_pr/events" {
		s.handleEvents(w, r)
		return
	}

	http.Error(w, "proxy-router: unknown control path", http.StatusNotFound)
}

// connStat is the per-tick byte counter update for one active connection.
type connStat struct {
	ID        uint64 `json:"id"`
	BytesUp   int64  `json:"bytes_up"`
	BytesDown int64  `json:"bytes_down"`
}

// handleEvents streams registry changes as Server-Sent Events: an initial
// "snapshot", then push "open"/"close"/"dest"/"proc" events, plus a periodic
// "stats" event with the byte counters of active connections. Stats lists
// the whole active set, so a client that missed a dropped event resyncs on
// the next tick. Try it: curl -N localhost:1337/_pr/events
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	send := func(event string, data any) bool {
		payload, err := json.Marshal(data)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Subscribe before the snapshot so no event in between is lost; the
	// client upserts by id, so an event duplicating snapshot state is fine.
	subID, events := subscribeConns()
	defer unsubscribeConns(subID)

	if !send("snapshot", struct {
		Connections []ConnInfo `json:"connections"`
	}{SnapshotConnections()}) {
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			if !send(ev.Type, ev.Conn) {
				return
			}
		case <-ticker.C:
			stats := activeStats()
			if len(stats) == 0 {
				continue
			}
			if !send("stats", stats) {
				return
			}
		}
	}
}

// activeStats returns the byte counters of all active connections.
func activeStats() []connStat {
	connReg.Lock()
	defer connReg.Unlock()
	out := make([]connStat, 0, len(connReg.active))
	for _, e := range connReg.active {
		out = append(out, connStat{ID: e.id, BytesUp: e.bytesUp.Load(), BytesDown: e.bytesDown.Load()})
	}
	return out
}
