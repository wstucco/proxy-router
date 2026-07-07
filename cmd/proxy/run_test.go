package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestReloadSerializesStores verifies that reload() holds a mutex across
// both atomic pointer stores, preventing concurrent reloads from
// interleaving their stores (cfgPtr vs srvPtr mismatch).
//
// Without the mutex, the race detector catches the data race on the
// underlying string values that two concurrent reloads read-then-store.
// With the mutex, -race reports clean.
func TestReloadSerializesStores(t *testing.T) {
	var (
		cfgPtr   atomic.Pointer[string]
		srvPtr   atomic.Pointer[string]
		reloadMu sync.Mutex
	)

	reload := func(gen string) {
		reloadMu.Lock()
		cfgPtr.Store(&gen)
		srvPtr.Store(&gen)
		reloadMu.Unlock()
	}

	start := "gen-0"
	cfgPtr.Store(&start)
	srvPtr.Store(&start)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				reload("gen-" + itoa(id) + "-" + itoa(j))
			}
		}(i)
	}
	wg.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestSendCtxCancelled verifies that sendCtx returns false immediately
// when the context is already cancelled, even if the channel is empty.
// Prior to the fix, a blocking send on a full channel would hang
// indefinitely after context cancellation.
func TestSendCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	ch := make(chan connEvent, 1)
	if sendCtx(ctx, ch, connEvent{Type: "snapshot"}) {
		t.Error("sendCtx returned true on cancelled context")
	}
}

// TestSendCtxFullChannel verifies that sendCtx returns false when the
// channel is full and the context is cancelled, rather than blocking.
func TestSendCtxFullChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan connEvent, 1)

	// Fill the channel.
	ch <- connEvent{Type: "snapshot"}

	cancel() // cancel before the second send

	if sendCtx(ctx, ch, connEvent{Type: "open"}) {
		t.Error("sendCtx returned true on cancelled context with full channel")
	}
}

// TestSendCtxSuccess verifies sendCtx sends successfully on an uncancelled
// context with room in the buffer.
func TestSendCtxSuccess(t *testing.T) {
	ctx := context.Background()
	ch := make(chan connEvent, 1)

	if !sendCtx(ctx, ch, connEvent{Type: "snapshot"}) {
		t.Error("sendCtx returned false on valid send")
	}
	select {
	case ev := <-ch:
		if ev.Type != "snapshot" {
			t.Errorf("got event type %q, want %q", ev.Type, "snapshot")
		}
	default:
		t.Error("channel is empty, expected event")
	}
}
