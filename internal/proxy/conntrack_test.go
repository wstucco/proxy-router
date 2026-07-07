package proxy

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func resetConnReg() {
	connReg.Lock()
	connReg.active = make(map[uint64]*ConnEntry)
	connReg.recent = nil
	connReg.subs = make(map[uint64]chan ConnEvent)
	connReg.Unlock()
}

func TestTrackCloseSnapshot(t *testing.T) {
	resetConnReg()

	e := trackConn(kindConnect, "nohost", "example.com:443", "office", "proxy:corp")
	e.bytesUp.Add(100)
	e.bytesDown.Add(2000)

	snap := SnapshotConnections()
	if len(snap) != 1 {
		t.Fatalf("want 1 entry, got %d", len(snap))
	}
	c := snap[0]
	if !c.Active || c.Dest != "example.com:443" || c.Location != "office" || c.Via != "proxy:corp" {
		t.Errorf("unexpected snapshot: %+v", c)
	}
	if c.BytesUp != 100 || c.BytesDown != 2000 {
		t.Errorf("bytes not tracked: up=%d down=%d", c.BytesUp, c.BytesDown)
	}

	e.Close()
	e.Close() // idempotent

	snap = SnapshotConnections()
	if len(snap) != 1 {
		t.Fatalf("closed entry must remain in recent: got %d entries", len(snap))
	}
	if snap[0].Active {
		t.Error("entry still active after Close")
	}
	if snap[0].DurationMS < 0 {
		t.Error("negative duration")
	}
}

func TestSetDest(t *testing.T) {
	resetConnReg()
	e := trackConn(kindMITM, "nohost", "orig.example.com:443", "office", "direct")
	defer e.Close()
	e.SetDest("routed.example.com:443")
	if got := SnapshotConnections()[0].Dest; got != "routed.example.com:443" {
		t.Errorf("SetDest not visible: %q", got)
	}
}

func TestRecentRingCap(t *testing.T) {
	resetConnReg()
	for i := 0; i < recentCap+10; i++ {
		e := trackConn(kindHTTP, "nohost", fmt.Sprintf("host%d:80", i), "", "direct")
		e.Close()
	}
	snap := SnapshotConnections()
	if len(snap) != recentCap {
		t.Fatalf("ring cap: want %d, got %d", recentCap, len(snap))
	}
	// Newest first: the last closed entry (host<recentCap+9>) leads.
	if want := fmt.Sprintf("host%d:80", recentCap+9); snap[0].Dest != want {
		t.Errorf("order: first is %q, want %q", snap[0].Dest, want)
	}
}

func TestSnapshotActiveBeforeRecent(t *testing.T) {
	resetConnReg()
	closed := trackConn(kindHTTP, "nohost", "closed:80", "", "direct")
	closed.Close()
	open := trackConn(kindConnect, "nohost", "open:443", "", "direct")
	defer open.Close()

	snap := SnapshotConnections()
	if len(snap) != 2 || !snap[0].Active || snap[1].Active {
		t.Fatalf("active must sort before recent: %+v", snap)
	}
}

func TestCountReaderWriter(t *testing.T) {
	resetConnReg()
	e := trackConn(kindConnect, "nohost", "x:443", "", "direct")
	defer e.Close()

	src := strings.NewReader(strings.Repeat("a", 1234))
	var sink bytes.Buffer
	n, err := io.Copy(countWriter{&sink, &e.bytesDown}, countReader{src, &e.bytesUp})
	if err != nil || n != 1234 {
		t.Fatalf("copy: n=%d err=%v", n, err)
	}
	if e.bytesUp.Load() != 1234 || e.bytesDown.Load() != 1234 {
		t.Errorf("counters: up=%d down=%d, want 1234/1234", e.bytesUp.Load(), e.bytesDown.Load())
	}
}

// Test client addresses use an unparseable host:port ("nohost") so trackConn
// skips the async PID-resolution goroutine and no "proc" event interleaves.
func TestSubscribeEvents(t *testing.T) {
	resetConnReg()
	id, ch := subscribeConns()
	defer unsubscribeConns(id)

	e := trackConn(kindMITM, "nohost", "orig.example.com:443", "office", "proxy:corp")
	e.SetDest("routed.example.com:443")
	e.SetDest("routed.example.com:443") // no change → no event
	e.Close()

	want := []struct{ typ, dest string }{
		{"open", "orig.example.com:443"},
		{"dest", "routed.example.com:443"},
		{"close", "routed.example.com:443"},
	}
	for _, w := range want {
		select {
		case ev := <-ch:
			if ev.Type != w.typ || ev.Conn.Dest != w.dest {
				t.Errorf("got %s/%s, want %s/%s", ev.Type, ev.Conn.Dest, w.typ, w.dest)
			}
			if w.typ == "close" && ev.Conn.Active {
				t.Error("close event marked active")
			}
		default:
			t.Fatalf("missing %s event", w.typ)
		}
	}
	select {
	case ev := <-ch:
		t.Errorf("unexpected extra event: %+v", ev)
	default:
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	resetConnReg()
	id, ch := subscribeConns()
	defer unsubscribeConns(id)

	// Overflow the buffer without draining: emitters must never block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			trackConn(kindHTTP, "nohost", "h:80", "", "direct").Close()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("emitters blocked on a slow subscriber")
	}
	if len(ch) != cap(ch) {
		t.Errorf("expected full buffer (%d), got %d", cap(ch), len(ch))
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	resetConnReg()
	id, ch := subscribeConns()
	unsubscribeConns(id)
	trackConn(kindHTTP, "nohost", "h:80", "", "direct").Close()
	if len(ch) != 0 {
		t.Errorf("received %d events after unsubscribe", len(ch))
	}
}

func TestConcurrentTrackCloseSnapshot(t *testing.T) {
	resetConnReg()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := trackConn(kindHTTP, "nohost", fmt.Sprintf("h%d:80", i), "", "direct")
			e.bytesUp.Add(1)
			SnapshotConnections()
			e.Close()
		}(i)
	}
	wg.Wait()
	for _, c := range SnapshotConnections() {
		if c.Active {
			t.Errorf("entry %d still active", c.ID)
		}
	}
}
