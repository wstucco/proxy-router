package proxy

import (
	"io"
	"net"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wstucco/proxy-router/internal/procinfo"
)

// Connection kinds shown in the connections view.
const (
	kindConnect = "connect"
	kindHTTP    = "http"
	kindMITM    = "mitm"
)

const recentCap = 32

// ConnEntry is a live connection tracked by the registry. Byte counters are
// atomics updated from the copy goroutines; the remaining mutable fields are
// guarded by mu.
type ConnEntry struct {
	id         uint64
	kind       string
	start      time.Time
	clientAddr string
	bytesUp    atomic.Int64
	bytesDown  atomic.Int64

	mu       sync.Mutex
	dest     string // host:port; MITM updates it per request
	location string // decision.RouteLoc()
	via      string // decision.RouteDest() — redacted by construction
	pid      int
	process  string
	end      time.Time // zero while active
}

// ConnInfo is the JSON-ready snapshot of a ConnEntry.
type ConnInfo struct {
	ID         uint64    `json:"id"`
	Kind       string    `json:"kind"`
	Active     bool      `json:"active"`
	Start      time.Time `json:"start"`
	DurationMS int64     `json:"duration_ms"`
	Client     string    `json:"client"`
	PID        int       `json:"pid,omitempty"`
	Process    string    `json:"process,omitempty"`
	Dest       string    `json:"dest"`
	Location   string    `json:"location"`
	Via        string    `json:"via"`
	BytesUp    int64     `json:"bytes_up"`
	BytesDown  int64     `json:"bytes_down"`
}

// ConnEvent is a registry change notification pushed to SSE subscribers.
// Type is "open", "close", or "dest"; Conn is the entry state at emit time
// (for "close" it carries the final byte counts and duration).
type ConnEvent struct {
	Type string   `json:"type"`
	Conn ConnInfo `json:"conn"`
}

// connRegistry is package-level: the Server is rebuilt and swapped on every
// config reload, but live connections outlast the swap (same pattern as the
// negotiate failure cache). Never cleared on reload.
var connReg = struct {
	sync.Mutex
	nextID uint64
	active map[uint64]*ConnEntry
	recent []*ConnEntry // ring buffer of recently closed entries
	subID  uint64
	subs   map[uint64]chan ConnEvent
}{
	active: make(map[uint64]*ConnEntry),
	subs:   make(map[uint64]chan ConnEvent),
}

// subscribeConns registers an event subscriber. The channel is buffered; a
// slow consumer loses events rather than blocking the data path — the
// periodic SSE stats event is authoritative on the active set, so lost
// events self-heal on the client.
func subscribeConns() (uint64, <-chan ConnEvent) {
	ch := make(chan ConnEvent, 128)
	connReg.Lock()
	connReg.subID++
	id := connReg.subID
	connReg.subs[id] = ch
	connReg.Unlock()
	return id, ch
}

func unsubscribeConns(id uint64) {
	connReg.Lock()
	delete(connReg.subs, id)
	connReg.Unlock()
}

// emitConnEvent fans out to subscribers. Callers must NOT hold connReg or
// e.mu is fine — snapshot() takes e.mu itself.
func emitConnEvent(typ string, e *ConnEntry) {
	ev := ConnEvent{Type: typ, Conn: e.snapshot()}
	connReg.Lock()
	for _, ch := range connReg.subs {
		select {
		case ch <- ev:
		default: // slow consumer: drop, stats will resync it
		}
	}
	connReg.Unlock()
}

// trackConn registers a new connection and resolves the owning client
// process asynchronously (the libproc scan must not block the data path).
func trackConn(kind, clientAddr, dest, location, via string) *ConnEntry {
	e := &ConnEntry{
		kind:       kind,
		start:      time.Now(),
		clientAddr: clientAddr,
		dest:       dest,
		location:   location,
		via:        via,
	}

	connReg.Lock()
	connReg.nextID++
	e.id = connReg.nextID
	connReg.active[e.id] = e
	connReg.Unlock()

	emitConnEvent("open", e)

	if _, port, err := net.SplitHostPort(clientAddr); err == nil {
		if p, err := strconv.ParseUint(port, 10, 16); err == nil {
			go func() {
				res, err := procinfo.Lookup(uint16(p))
				e.mu.Lock()
				if err != nil {
					e.process = "?"
				} else {
					e.pid = res.PID
					e.process = res.Name
				}
				e.mu.Unlock()
				// The "open" event predates resolution — push the name.
				emitConnEvent("proc", e)
			}()
		}
	}

	return e
}

// Close marks the entry as ended and moves it to the recent ring. Idempotent.
func (e *ConnEntry) Close() {
	e.mu.Lock()
	if !e.end.IsZero() {
		e.mu.Unlock()
		return
	}
	e.end = time.Now()
	e.mu.Unlock()

	connReg.Lock()
	delete(connReg.active, e.id)
	connReg.recent = append(connReg.recent, e)
	if len(connReg.recent) > recentCap {
		connReg.recent = connReg.recent[len(connReg.recent)-recentCap:]
	}
	connReg.Unlock()

	emitConnEvent("close", e)
}

// SetDest updates the destination (MITM tunnels carry per-request targets).
// Emits only on an actual change — MITM calls this on every request.
func (e *ConnEntry) SetDest(dest string) {
	e.mu.Lock()
	changed := e.dest != dest
	e.dest = dest
	e.mu.Unlock()
	if changed {
		emitConnEvent("dest", e)
	}
}

func (e *ConnEntry) snapshot() ConnInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	end := e.end
	active := end.IsZero()
	if active {
		end = time.Now()
	}
	return ConnInfo{
		ID:         e.id,
		Kind:       e.kind,
		Active:     active,
		Start:      e.start,
		DurationMS: end.Sub(e.start).Milliseconds(),
		Client:     e.clientAddr,
		PID:        e.pid,
		Process:    e.process,
		Dest:       e.dest,
		Location:   e.location,
		Via:        e.via,
		BytesUp:    e.bytesUp.Load(),
		BytesDown:  e.bytesDown.Load(),
	}
}

// SnapshotConnections returns active connections (newest first) followed by
// recently closed ones (newest first).
func SnapshotConnections() []ConnInfo {
	connReg.Lock()
	entries := make([]*ConnEntry, 0, len(connReg.active)+len(connReg.recent))
	for _, e := range connReg.active {
		entries = append(entries, e)
	}
	for i := len(connReg.recent) - 1; i >= 0; i-- {
		entries = append(entries, connReg.recent[i])
	}
	connReg.Unlock()

	out := make([]ConnInfo, len(entries))
	for i, e := range entries {
		out[i] = e.snapshot()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].ID > out[j].ID
	})
	return out
}

// countReader/countWriter feed the live byte counters on every Read/Write.
// Wrapping the conns disables io.Copy's ReadFrom/WriteTo fast path — fine
// for a local proxy, and the counters update while data flows instead of
// only at connection end.
type countReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

type countWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}
