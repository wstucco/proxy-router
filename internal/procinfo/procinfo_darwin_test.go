//go:build darwin

package procinfo

import (
	"net"
	"os"
	"strconv"
	"testing"
)

// Self-test: dial a local listener and look up our own ephemeral port —
// must resolve to this test process.
func TestLookupSelf(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, portStr, _ := net.SplitHostPort(conn.LocalAddr().String())
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(p)

	res, err := Lookup(port)
	if err != nil {
		t.Fatalf("Lookup(%d): %v", port, err)
	}
	if res.PID != os.Getpid() {
		t.Errorf("Lookup returned pid %d, want %d (self)", res.PID, os.Getpid())
	}
	if res.Name == "" {
		t.Errorf("Lookup returned empty process name")
	}
	t.Logf("resolved self: pid=%d name=%q", res.PID, res.Name)
}
