package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wstucco/proxy-router/internal/proxy"
)

const (
	flashFor = 2 * time.Second  // new rows glow green
	keepDead = 10 * time.Second // closed rows linger dimmed, then vanish
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Padding(0, 1)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("240")).Underline(true)
	rowStyle    = lipgloss.NewStyle()
	flashStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	deadStyle   = lipgloss.NewStyle().Faint(true)
	scrollStyle = lipgloss.NewStyle().Faint(true)
	statusStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250")).Padding(0, 1)
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)

type tuiRow struct {
	info     proxy.ConnInfo
	seenAt   time.Time // when the row first appeared (drives the flash)
	closedAt time.Time // zero while active
}

type tuiModel struct {
	rows       map[uint64]*tuiRow
	status     string
	width      int
	height     int
	scroll     int
	activeOnly bool
	upRate     float64
	dnRate     float64
	lastUp     int64
	lastDown   int64
	lastTick   time.Time
	events     chan connEvent
}

type evMsg connEvent
type frameMsg time.Time

func waitEvent(ch chan connEvent) tea.Cmd {
	return func() tea.Msg { return evMsg(<-ch) }
}

func frameTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return frameMsg(t) })
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(waitEvent(m.events), frameTick())
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "a":
			m.activeOnly = !m.activeOnly
			m.scroll = 0
		case "up", "k":
			m.scroll--
		case "down", "j":
			m.scroll++
		case "pgup":
			m.scroll -= m.bodyHeight()
		case "pgdown":
			m.scroll += m.bodyHeight()
		case "home":
			m.scroll = 0
		case "end":
			m.scroll = len(m.visibleRows())
		}
		m.clampScroll()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
	case frameMsg:
		for id, r := range m.rows {
			if !r.closedAt.IsZero() && time.Since(r.closedAt) > keepDead {
				delete(m.rows, id)
			}
		}
		m.clampScroll()
		return m, frameTick()
	case evMsg:
		m.apply(connEvent(msg))
		// Also cleanup expired rows and schedule the next frame tick
		// so stale entries don't linger during high event traffic.
		for id, r := range m.rows {
			if !r.closedAt.IsZero() && time.Since(r.closedAt) > keepDead {
				delete(m.rows, id)
			}
		}
		m.clampScroll()
		return m, tea.Batch(waitEvent(m.events), frameTick())
	}
	return m, nil
}

func (m *tuiModel) apply(ev connEvent) {
	switch ev.Type {
	case "status":
		m.status = ev.Status
	case "snapshot":
		// Merge, don't replace: the polling fallback sends a snapshot every
		// second and a naive replace would re-flash every row.
		seen := make(map[uint64]bool, len(ev.Snapshot))
		for _, c := range ev.Snapshot {
			seen[c.ID] = true
			m.upsert(c)
		}
		for id, r := range m.rows {
			if !seen[id] && r.closedAt.IsZero() {
				r.closedAt = time.Now() // vanished from an authoritative snapshot
			}
		}
	case "open", "dest", "proc":
		m.upsert(*ev.Conn)
	case "close":
		if r, ok := m.rows[ev.Conn.ID]; ok {
			r.info = *ev.Conn
			r.closedAt = time.Now()
		} else {
			m.upsert(*ev.Conn)
		}
	case "stats":
		var up, down int64
		for _, s := range ev.Stats {
			if r, ok := m.rows[s.ID]; ok {
				r.info.BytesUp, r.info.BytesDown = s.BytesUp, s.BytesDown
			}
			up += s.BytesUp
			down += s.BytesDown
		}
		now := time.Now()
		if !m.lastTick.IsZero() {
			dt := now.Sub(m.lastTick).Seconds()
			if dt > 0 && up >= m.lastUp && down >= m.lastDown {
				m.upRate = float64(up-m.lastUp) / dt
				m.dnRate = float64(down-m.lastDown) / dt
			}
		}
		m.lastUp, m.lastDown, m.lastTick = up, down, now
	}
}

func (m *tuiModel) upsert(c proxy.ConnInfo) {
	if r, ok := m.rows[c.ID]; ok {
		r.info = c
		if !c.Active && r.closedAt.IsZero() {
			r.closedAt = time.Now()
		}
		return
	}
	r := &tuiRow{info: c, seenAt: time.Now()}
	if !c.Active {
		r.seenAt = r.seenAt.Add(-flashFor) // never flash already-closed rows
		r.closedAt = time.Now()
	}
	m.rows[c.ID] = r
}

// visibleRows returns the filtered rows in display order: active first
// (newest first), then recently closed (newest first).
func (m *tuiModel) visibleRows() []*tuiRow {
	rows := make([]*tuiRow, 0, len(m.rows))
	for _, r := range m.rows {
		if m.activeOnly && !r.closedAt.IsZero() {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		ai, aj := rows[i].closedAt.IsZero(), rows[j].closedAt.IsZero()
		if ai != aj {
			return ai
		}
		return rows[i].info.ID > rows[j].info.ID
	})
	return rows
}

// bodyHeight is the number of table rows that fit: total minus title,
// header, scroll-indicator line, and status bar.
func (m *tuiModel) bodyHeight() int {
	h := m.height - 4
	if h < 1 {
		return 1
	}
	return h
}

func (m *tuiModel) clampScroll() {
	max := len(m.visibleRows()) - m.bodyHeight()
	if max < 0 {
		max = 0
	}
	if m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *tuiModel) View() string {
	width := m.width
	if width == 0 {
		width = 100
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("proxy-router connections"))
	b.WriteString("\n")

	cols := []struct {
		name  string
		width int
	}{
		{"PROCESS", 17}, {"PID", 6}, {"DEST", 0}, {"LOCATION", 12},
		{"VIA", 14}, {"UP", 9}, {"DOWN", 9}, {"AGE", 6},
	}
	fixed := 0
	for _, c := range cols {
		if c.width > 0 {
			fixed += c.width + 2
		}
	}
	if destW := width - fixed - 2; destW >= 12 {
		cols[2].width = destW
	} else {
		cols[2].width = 12
	}

	format := func(cells []string) string {
		var l strings.Builder
		for i, c := range cols {
			l.WriteString(pad(cells[i], c.width))
			if i < len(cols)-1 {
				l.WriteString("  ")
			}
		}
		return l.String()
	}

	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.name
	}
	b.WriteString(headerStyle.Render(format(header)))
	b.WriteString("\n")

	rows := m.visibleRows()
	bodyH := m.bodyHeight()
	start := m.scroll
	end := start + bodyH
	if end > len(rows) {
		end = len(rows)
	}

	active := 0
	for _, r := range rows {
		if r.closedAt.IsZero() {
			active++
		}
	}

	for _, r := range rows[start:end] {
		c := r.info
		process := c.Process
		if process == "" {
			process = "?"
		}
		pid := ""
		if c.PID > 0 {
			pid = fmt.Sprintf("%d", c.PID)
		}
		age := time.Since(c.Start)
		if !r.closedAt.IsZero() {
			age = time.Duration(c.DurationMS) * time.Millisecond
		}
		line := format([]string{
			process, pid, c.Dest, c.Location, c.Via,
			humanBytes(c.BytesUp), humanBytes(c.BytesDown), humanAge(age),
		})
		style := rowStyle
		switch {
		case !r.closedAt.IsZero():
			style = deadStyle
		case time.Since(r.seenAt) < flashFor:
			style = flashStyle
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	// Scroll indicator line (always reserved so the layout doesn't jump).
	indicator := ""
	if start > 0 {
		indicator += fmt.Sprintf("▲ %d above  ", start)
	}
	if end < len(rows) {
		indicator += fmt.Sprintf("▼ %d more", len(rows)-end)
	}
	b.WriteString(scrollStyle.Render(indicator))
	b.WriteString("\n")

	filter := "all"
	if m.activeOnly {
		filter = "active"
	}
	status := fmt.Sprintf("%s  •  %d active  •  filter: %s  •  ↑ %s/s  ↓ %s/s  •  a filter · ↑↓ scroll · q quit",
		m.status, active, filter, humanBytes(int64(m.upRate)), humanBytes(int64(m.dnRate)))
	b.WriteString(statusStyle.Width(width).Render(accentStyle.Render("●") + " " + status))
	return b.String()
}

// runConnectionsEnhanced runs the bubbletea TUI fed by the SSE stream.
func runConnectionsEnhanced(addr string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan connEvent, 64)
	go streamEvents(ctx, addr, events)

	m := &tuiModel{
		rows:   map[uint64]*tuiRow{},
		status: "connecting…",
		events: events,
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
