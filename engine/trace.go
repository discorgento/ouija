package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// ─── Context propagation ─────────────────────────────────────────────

type spanKey struct{}

// WithSpan returns a new context containing the given span as the active
// parent for tool-runner child spans.
func WithSpan(ctx context.Context, s *Span) context.Context {
	return context.WithValue(ctx, spanKey{}, s)
}

// SpanFromContext extracts the active parent span from the context, or nil
// if none is set.
func SpanFromContext(ctx context.Context) *Span {
	s, _ := ctx.Value(spanKey{}).(*Span)
	return s
}

// ─── Span ─────────────────────────────────────────────────────────────

// Span is a single traced operation with timing, hierarchy, and attributes.
// Safe for concurrent use: StartChild and End are goroutine-safe.
// A nil *Span is a no-op — callers never need to nil-check.
type Span struct {
	mu       sync.Mutex
	Name     string
	Start    time.Time
	end      time.Time
	ended    bool
	Children []*Span
	Attrs    map[string]string
	Error    string
}

// End marks the span as finished. Idempotent — safe to call multiple times.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.ended {
		s.end = time.Now()
		s.ended = true
	}
	s.mu.Unlock()
}

// Duration returns the elapsed time. Returns 0 for never-ended spans.
func (s *Span) Duration() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return s.end.Sub(s.Start)
	}
	return 0
}

// SetAttr sets a key-value attribute on the span.
func (s *Span) SetAttr(key, value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.Attrs == nil {
		s.Attrs = make(map[string]string)
	}
	s.Attrs[key] = value
	s.mu.Unlock()
}

// SetError records an error on the span.
func (s *Span) SetError(err string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Error = err
	s.mu.Unlock()
}

// StartChild creates a new span as a child of this span.
func (s *Span) StartChild(name string) *Span {
	if s == nil {
		return nil
	}
	child := &Span{Name: name, Start: time.Now()}
	s.mu.Lock()
	s.Children = append(s.Children, child)
	s.mu.Unlock()
	return child
}

// ─── Tracer ───────────────────────────────────────────────────────────

// Tracer is the root factory for spans. It collects a tree of spans that
// can be rendered as a console tree or exported as Perfetto JSON.
type Tracer struct {
	mu     sync.Mutex
	roots  []*Span
	active bool
}

// NewTracer creates a Tracer. When active is false, all Start calls return
// a no-op span (nil) and nothing is recorded.
func NewTracer(active bool) *Tracer {
	return &Tracer{active: active}
}

// Start creates a new root span.
func (t *Tracer) Start(name string) *Span {
	if t == nil || !t.active {
		return nil
	}
	s := &Span{Name: name, Start: time.Now()}
	t.mu.Lock()
	t.roots = append(t.roots, s)
	t.mu.Unlock()
	return s
}

// Reset clears all recorded spans. Called between runs when the Tracer is
// reused across multiple agent invocations.
func (t *Tracer) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.roots = nil
	t.mu.Unlock()
}

// ─── Console tree rendering ──────────────────────────────────────────

// Render prints the span tree to w as a formatted tree with durations.
// The output is designed for stderr (or any io.Writer) so the dev can
// see what happened after every run.
//
//	⭑ trace                                     2.3s
//	├─ read_file   src/User.php                0.8s  ✓
//	├─ grep        getUsers()                  1.2s  ✓
//	└─ edit_file   src/User.php                0.3s  ✗
//
// Render prints the span tree to w as a formatted tree with durations.
func (t *Tracer) Render(w io.Writer) {
	if s := t.RenderString(); s != "" {
		fmt.Fprintln(w, s)
	}
}

// RenderLastRootString renders the latest turn (all root spans from one
// Chat() call) into a compact tree. The ReAct loop creates one root per
// iteration — tool-calling iterations first, then a final text-only response.
// Useful for showing per-turn trace in a TUI.
func (t *Tracer) RenderLastRootString() string {
	if t == nil || !t.active {
		return ""
	}
	t.mu.Lock()
	n := len(t.roots)
	if n == 0 {
		t.mu.Unlock()
		return ""
	}
	// Copy all roots that belong to the current turn. The tracer is never
	// reset between Chat() calls (only Close()), so the full accumulated
	// list is the current turn's span tree.
	roots := append([]*Span(nil), t.roots...)
	t.mu.Unlock()

	// Calculate total duration from the last root (final response).
	last := roots[n-1]
	totalDur := last.Duration()

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("\n  ⭑  last turn  (%s)\n", totalDur.Round(time.Millisecond)))

	for i, s := range roots {
		isLast := i == len(roots)-1
		buf.WriteString(renderSpanString(s, "", isLast))
	}

	// Summarize tools used across ALL root spans (the last root is just
	// the final text-only response — tools live in earlier roots).
	tools := make(map[string]int)
	for _, r := range roots {
		gatherToolNames([]*Span{r}, tools)
	}
	if len(tools) > 0 {
		names := make([]string, 0, len(tools))
		for n := range tools {
			names = append(names, n)
		}
		slices.Sort(names)
		var parts []string
		for _, n := range names {
			c := tools[n]
			if c == 1 {
				parts = append(parts, n)
			} else {
				parts = append(parts, fmt.Sprintf("%s(%d)", n, c))
			}
		}
		buf.WriteString(fmt.Sprintf("  🛠️ %s\n", strings.Join(parts, ", ")))
	} else {
		buf.WriteString("  💬 response\n")
	}
	return buf.String()
}

// RenderString returns the span tree as a string (no trailing newline).
// Returns empty string when there is nothing to render.
// The output has no ANSI codes — suitable for embedding in GUIs/TUIs.
func (t *Tracer) RenderString() string {
	if t == nil || !t.active {
		return ""
	}
	t.mu.Lock()
	roots := append([]*Span(nil), t.roots...)
	t.mu.Unlock()

	if len(roots) == 0 {
		return ""
	}

	// Calculate total duration.
	var total time.Duration
	for _, r := range roots {
		d := r.Duration()
		if d > total {
			total = d
		}
	}

	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("\n  ⭑  trace  (%s)\n", total.Round(time.Millisecond)))

	for i, s := range roots {
		isLast := i == len(roots)-1
		buf.WriteString(renderSpanString(s, "", isLast))
	}

	toolCount := countTools(roots)
	buf.WriteString(fmt.Sprintf("  %d spans · %s total\n", toolCount, total.Round(time.Millisecond)))

	return buf.String()
}

// renderSpanString renders a single span and its children into a string.
// No ANSI codes, suitable for embedding.
func renderSpanString(s *Span, prefix string, isLast bool) string {
	if s == nil {
		return ""
	}

	s.mu.Lock()
	name := s.Name
	err := s.Error
	dur := time.Duration(0)
	if s.ended {
		dur = s.end.Sub(s.Start)
	}
	attrs := make(map[string]string, len(s.Attrs))
	for k, v := range s.Attrs {
		attrs[k] = v
	}
	children := append([]*Span(nil), s.Children...)
	s.mu.Unlock()

	// Build connector.
	connector := "├─ "
	if isLast {
		connector = "└─ "
	}

	// Status indicator.
	status := "✓"
	if err != "" {
		status = "✗"
	}

	// Attrs summary (show important ones inline).
	attrStr := ""
	if name == "chat" || name == "phase2" {
		if tok, ok := attrs["input_tokens"]; ok {
			attrStr = fmt.Sprintf("  [%s in]", tok)
			if out, ok2 := attrs["output_tokens"]; ok2 {
				attrStr = fmt.Sprintf("  [%s in · %s out]", tok, out)
			}
		}
	} else if strings.HasPrefix(name, "tool.") {
		if p, ok := attrs["tool"]; ok && p != name {
			attrStr = "  " + p
		}
		if p, ok := attrs["path"]; ok {
			attrStr = "  " + p
		}
	}

	var buf strings.Builder
	line := fmt.Sprintf("%s%s%-30s %s%s  %s",
		prefix, connector, name, durStr(dur), attrStr, status)
	if err != "" {
		line += "  " + err
	}
	buf.WriteString(line)
	buf.WriteByte('\n')

	// Render children.
	childPrefix := prefix + "  "
	if !isLast {
		childPrefix = prefix + "│ "
	}
	for i, child := range children {
		buf.WriteString(renderSpanString(child, childPrefix, i == len(children)-1))
	}

	return buf.String()
}

func durStr(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	d = d.Round(time.Millisecond)
	if d < time.Second {
		return d.String()
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func countTools(spans []*Span) int {
	n := 0
	for _, s := range spans {
		if strings.HasPrefix(s.Name, "tool.") {
			n++
		}
		n += countTools(s.Children)
	}
	return n
}

// gatherToolNames collects tool names and their counts from all spans.
func gatherToolNames(spans []*Span, out map[string]int) {
	for _, s := range spans {
		if strings.HasPrefix(s.Name, "tool.") {
			name := strings.TrimPrefix(s.Name, "tool.")
			out[name]++
		}
		gatherToolNames(s.Children, out)
	}
}

// WritePerfettoFile is a convenience wrapper that writes Perfetto JSON
// to a file. It creates the file (or truncates) and encodes the trace tree.
func (t *Tracer) WritePerfettoFile(path string) error {
	if t == nil || !t.active {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.WritePerfetto(f)
}

// ─── Perfetto / Chrome trace export ──────────────────────────────────

// perfettoEvent is a single trace event in Chrome Trace Event format.
// See https://docs.google.com/document/d/1CvAClvFfyA5R-PhYUmn5OOQtYMH4h6I0nSsKchNAySU
type perfettoEvent struct {
	Name string            `json:"name"`
	Cat  string            `json:"cat"`
	Ph   string            `json:"ph"` // "X" = complete, "b"/"e" = async start/end
	Ts   int64             `json:"ts"` // microseconds since epoch
	Dur  int64             `json:"dur,omitempty"`
	Pid  int               `json:"pid"`
	Tid  int               `json:"tid"`
	Args map[string]string `json:"args,omitempty"`
}

// WritePerfetto writes a Chrome Trace Event format JSON file that can be
// opened in chrome://tracing or ui.perfetto.dev.
func (t *Tracer) WritePerfetto(w io.Writer) error {
	if t == nil || !t.active {
		return nil
	}
	t.mu.Lock()
	roots := append([]*Span(nil), t.roots...)
	t.mu.Unlock()

	var events []perfettoEvent
	pid := 1
	for _, r := range roots {
		flattenPerfetto(r, pid, 0, &events)
	}

	if len(events) == 0 {
		return nil
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(events)
}

func flattenPerfetto(s *Span, pid, tid int, events *[]perfettoEvent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	name := s.Name
	ts := s.Start.UnixMicro()
	var dur int64
	if s.ended {
		dur = s.end.Sub(s.Start).Microseconds()
	}
	attrs := make(map[string]string, len(s.Attrs))
	for k, v := range s.Attrs {
		attrs[k] = v
	}
	if s.Error != "" {
		attrs["error"] = s.Error
	}
	children := append([]*Span(nil), s.Children...)
	s.mu.Unlock()

	*events = append(*events, perfettoEvent{
		Name: name,
		Cat:  category(name),
		Ph:   "X",
		Ts:   ts,
		Dur:  dur,
		Pid:  pid,
		Tid:  tid,
		Args: attrs,
	})

	for i, child := range children {
		flattenPerfetto(child, pid, tid*100+i+1, events)
	}
}

func category(name string) string {
	switch {
	case name == "chat" || name == "phase2":
		return "chat"
	case strings.HasPrefix(name, "tool."):
		return "tool"
	default:
		return "agent"
	}
}
