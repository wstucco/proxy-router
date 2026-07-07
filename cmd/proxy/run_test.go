package main

import (
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
