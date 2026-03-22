package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

const eventWidth = 15

// prettyHandler formats log records as:
//
//	2006/01/02 15:04:05 [LEVEL] [component] [EVENT              ] key=value ...
//
// The message is expected to follow the "[component] event text" convention.
// If a single attr is present its value is printed bare; multiple attrs use key=value pairs.
type prettyHandler struct {
	mu  sync.Mutex
	out io.Writer
}

func newPrettyHandler(out io.Writer) slog.Handler {
	return &prettyHandler{out: out}
}

func (h *prettyHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	ts := r.Time.Format("2006/01/02 15:04:05")
	lvl := fmt.Sprintf("[%s]", r.Level.String())

	// Split "[component] event text" → component="[component]", event="EVENT TEXT"
	component, event := "", ""
	msg := r.Message

	if strings.HasPrefix(msg, "[") {
		if i := strings.Index(msg, "] "); i != -1 {
			component = msg[:i+1]
			event = strings.TrimSpace(msg[i+2:])
		}
	}

	// Collect attrs; single → bare value, multiple → key=value pairs
	var attrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	attrStr := ""
	switch len(attrs) {
	case 1:
		attrStr = fmt.Sprint(attrs[0].Value.Any())
	default:
		parts := make([]string, len(attrs))
		for i, a := range attrs {
			parts[i] = fmt.Sprintf("%s=%v", a.Key, a.Value.Any())
		}
		attrStr = strings.Join(parts, " ")
	}

	var b strings.Builder
	b.WriteString(ts)
	b.WriteByte(' ')
	b.WriteString(lvl)
	if component != "" {
		b.WriteByte(' ')
		b.WriteString(component)
	}

	//fmt.Println("width:", eventWidth-utf8.RuneCountInString(component))
	//width := int(math.Max(float64(eventWidth-utf8.RuneCountInString(component)), 0))
	//fmt.Fprintf(&b, "%-*s", width, " ")
	fmt.Fprintf(&b, " %s", event)
	if attrStr != "" {
		b.WriteByte(' ')
		b.WriteString(attrStr)
	}
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, b.String())
	return err
}

func (h *prettyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *prettyHandler) WithGroup(_ string) slog.Handler      { return h }
