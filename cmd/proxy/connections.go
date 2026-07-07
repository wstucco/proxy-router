package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/wstucco/proxy-router/internal/config"
	"github.com/wstucco/proxy-router/internal/proxy"
)

func cmdConnections(args []string) {
	p := detectPaths()

	fs := flag.NewFlagSet("connections", flag.ExitOnError)
	cfgFile := fs.String("config", p.cfgFile, "path to config file (to find the listen address)")
	listen := fs.String("listen", "", "daemon address (overrides config)")
	interval := fs.Duration("interval", time.Second, "refresh interval")
	once := fs.Bool("once", false, "print one snapshot and exit (no TUI)")
	fs.Parse(args)

	addr := *listen
	if addr == "" {
		c, err := config.Load(*cfgFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot load config to find listen address: %v\n(use -listen to specify the daemon address)\n", err)
			os.Exit(1)
		}
		addr = c.Listen
	}
	addr = normalizeListen(addr)

	if *once {
		conns, err := fetchConnections(addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot reach proxy-router at %s — is the daemon running? (%v)\n", addr, err)
			os.Exit(1)
		}
		fmt.Print(renderTable(conns, 120, len(conns)+2, false))
		return
	}

	runConnectionsTUI(addr, *interval)
}

// normalizeListen turns ":1337" into "localhost:1337" for dialing.
func normalizeListen(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

var connClient = &http.Client{Timeout: 2 * time.Second}

func fetchConnections(addr string) ([]proxy.ConnInfo, error) {
	resp, err := connClient.Get("http://" + addr + "/_pr/connections")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daemon returned %s", resp.Status)
	}
	var body struct {
		Connections []proxy.ConnInfo `json:"connections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Connections, nil
}

func runConnectionsTUI(addr string, interval time.Duration) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: not a terminal (use -once for plain output): %v\n", err)
		os.Exit(1)
	}
	restore := func() {
		_ = term.Restore(fd, oldState)
		fmt.Print("\x1b[?25h") // show cursor
	}
	defer restore()
	fmt.Print("\x1b[?25l\x1b[2J") // hide cursor, clear screen

	quit := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				return
			}
			// In raw mode Ctrl-C arrives as 0x03, not SIGINT.
			if buf[0] == 'q' || buf[0] == 'Q' || buf[0] == 0x03 {
				close(quit)
				return
			}
		}
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	draw := func() {
		width, height, err := term.GetSize(fd)
		if err != nil {
			width, height = 80, 24
		}
		conns, ferr := fetchConnections(addr)
		var out string
		if ferr != nil {
			out = fmt.Sprintf("\x1b[H\x1b[2mcannot reach proxy-router at %s — is the daemon running? (retrying)\x1b[0m\x1b[K\x1b[J", addr)
		} else {
			out = renderTable(conns, width, height, true)
		}
		fmt.Print(out)
	}

	draw()
	for {
		select {
		case <-quit:
			return
		case <-sigCh:
			return
		case <-ticker.C:
			draw()
		}
	}
}

// renderTable produces the full frame. Pure function: testable without a
// terminal. ansi=false yields plain text for -once. Rows must be pre-sorted
// (active first, newest first) — SnapshotConnections already guarantees it.
func renderTable(conns []proxy.ConnInfo, width, height int, ansi bool) string {
	sort.SliceStable(conns, func(i, j int) bool {
		if conns[i].Active != conns[j].Active {
			return conns[i].Active
		}
		return conns[i].ID > conns[j].ID
	})

	type col struct {
		name  string
		width int
	}
	cols := []col{
		{"PROCESS", 16}, {"PID", 6}, {"DEST", 0 /* flexible */},
		{"LOCATION", 12}, {"VIA", 14}, {"UP", 8}, {"DOWN", 8}, {"AGE", 6},
	}
	fixed := 0
	for _, c := range cols {
		if c.width > 0 {
			fixed += c.width + 2
		}
	}
	destW := width - fixed - 2
	if destW < 12 {
		destW = 12
	}
	cols[2].width = destW

	var b strings.Builder
	nl := "\n"
	if ansi {
		b.WriteString("\x1b[H")
		nl = "\x1b[K\r\n"
	}

	line := func(cells []string, dim bool) {
		var row strings.Builder
		for i, c := range cols {
			row.WriteString(pad(cells[i], c.width))
			if i < len(cols)-1 {
				row.WriteString("  ")
			}
		}
		s := row.String()
		if len(s) > width && width > 0 {
			s = s[:width]
		}
		if ansi && dim {
			s = "\x1b[2m" + s + "\x1b[0m"
		}
		b.WriteString(s)
		b.WriteString(nl)
	}

	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.name
	}
	line(header, false)

	maxRows := len(conns)
	if ansi && height > 2 && maxRows > height-2 {
		maxRows = height - 2
	}
	for _, c := range conns[:maxRows] {
		process := c.Process
		if process == "" {
			process = "?"
		}
		pid := ""
		if c.PID > 0 {
			pid = fmt.Sprintf("%d", c.PID)
		}
		line([]string{
			process, pid, c.Dest, c.Location, c.Via,
			humanBytes(c.BytesUp), humanBytes(c.BytesDown),
			humanAge(time.Duration(c.DurationMS) * time.Millisecond),
		}, !c.Active)
	}
	if ansi {
		b.WriteString("\x1b[J") // clear rest of screen
	}
	return b.String()
}

// pad truncates or right-pads s to exactly w characters.
func pad(s string, w int) string {
	if len(s) > w {
		if w > 1 {
			return s[:w-1] + "…"
		}
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func humanAge(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
