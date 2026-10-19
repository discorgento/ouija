package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
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
	mu        sync.Mutex
	roots     []*Span
	turnStart int // index of the first root of the current turn (NextTurn)
	active    bool
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
	t.turnStart = 0
	t.mu.Unlock()
}

// NextTurn starts a new turn: RenderLastRootString then shows only the spans
// recorded after this call, while Render and the Perfetto export still cover
// every span since the last Reset (the whole chat session).
func (t *Tracer) NextTurn() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.turnStart = len(t.roots)
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
func (t *Tracer) Render(w io.Writer) {
	if s := t.RenderString(); s != "" {
		fmt.Fprintln(w, s)
	}
}

// snapshot copies the recorded roots; nil when inactive or empty.
func (t *Tracer) snapshot() []*Span {
	if t == nil || !t.active {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.roots)
}

// wallTime is the time from the first root's start to the last root's end.
// The roots of a run are sequential ReAct iterations (or parallel passes), so
// neither the longest root nor the sum of their durations is the elapsed time.
func wallTime(roots []*Span) time.Duration {
	var first, last time.Time
	for _, r := range roots {
		d := r.Duration()
		if d == 0 {
			continue // never ended
		}
		if first.IsZero() || r.Start.Before(first) {
			first = r.Start
		}
		if end := r.Start.Add(d); end.After(last) {
			last = end
		}
	}
	return last.Sub(first)
}

// renderTree writes the header and every root's tree.
func renderTree(buf *strings.Builder, title string, roots []*Span, total time.Duration) {
	fmt.Fprintf(buf, "\n  ⭑  %s  (%s)\n", title, total.Round(time.Millisecond))
	for i, s := range roots {
		buf.WriteString(renderSpanString(s, "", i == len(roots)-1))
	}
}

// RenderLastRootString renders the current turn — the spans recorded since the
// last NextTurn or Reset, one root per ReAct iteration — into a compact tree.
// Useful for a per-turn trace in a TUI.
func (t *Tracer) RenderLastRootString() string {
	if t == nil || !t.active {
		return ""
	}
	t.mu.Lock()
	roots := slices.Clone(t.roots[t.turnStart:])
	t.mu.Unlock()
	if len(roots) == 0 {
		return ""
	}
	var buf strings.Builder
	renderTree(&buf, "last turn", roots, wallTime(roots))

	tools := make(map[string]int)
	gatherToolNames(roots, tools)
	if len(tools) == 0 {
		buf.WriteString("  💬 response\n")
		return buf.String()
	}
	var parts []string
	for _, n := range slices.Sorted(maps.Keys(tools)) {
		if c := tools[n]; c > 1 {
			parts = append(parts, fmt.Sprintf("%s(%d)", n, c))
		} else {
			parts = append(parts, n)
		}
	}
	fmt.Fprintf(&buf, "  🛠️ %s\n", strings.Join(parts, ", "))
	return buf.String()
}

// TurnTool is one tool call of a turn, as LastTurn reports it.
type TurnTool struct {
	Name     string        // tool name (read_file, bash…)
	Target   string        // its path, pattern or command; "" when none
	Duration time.Duration // 0 while it runs
	Error    string        // why it failed or was refused; "" on success
}

// TurnSummary is what the current turn did: its tool calls in the order they
// started, the model's token usage and the wall time.
type TurnSummary struct {
	Tools        []TurnTool
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
}

// LastTurn summarizes the current turn — the spans since the last NextTurn or
// Reset — for a UI that shows the tool calls rather than the span tree.
func (t *Tracer) LastTurn() TurnSummary {
	if t == nil || !t.active {
		return TurnSummary{}
	}
	t.mu.Lock()
	roots := slices.Clone(t.roots[t.turnStart:])
	t.mu.Unlock()
	sum := TurnSummary{Duration: wallTime(roots)}
	var walk func([]*Span)
	walk = func(spans []*Span) {
		for _, s := range spans {
			s.mu.Lock()
			name, attrs, errText := s.Name, maps.Clone(s.Attrs), s.Error
			children := slices.Clone(s.Children)
			s.mu.Unlock()
			if tool, ok := strings.CutPrefix(name, "tool."); ok {
				target := attrs["path"]
				for _, k := range []string{"pattern", "command"} {
					if v := attrs[k]; v != "" && target == "" {
						target = v
					}
				}
				sum.Tools = append(sum.Tools, TurnTool{Name: tool, Target: target, Duration: s.Duration(), Error: errText})
			}
			if n, err := strconv.Atoi(attrs["input_tokens"]); err == nil {
				sum.InputTokens += n
			}
			if n, err := strconv.Atoi(attrs["output_tokens"]); err == nil {
				sum.OutputTokens += n
			}
			walk(children)
		}
	}
	walk(roots) // roots in turn order, children in start order
	return sum
}

// RenderString returns the span tree as a string, or "" when there is nothing
// to render. The output has no ANSI codes — suitable for embedding in TUIs.
func (t *Tracer) RenderString() string {
	roots := t.snapshot()
	if len(roots) == 0 {
		return ""
	}
	total := wallTime(roots)
	var buf strings.Builder
	renderTree(&buf, "trace", roots, total)

	tools := make(map[string]int)
	gatherToolNames(roots, tools)
	calls := 0
	for _, c := range tools {
		calls += c
	}
	fmt.Fprintf(&buf, "  %d tool calls · %s total\n", calls, total.Round(time.Millisecond))
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
		if p, ok := attrs["pattern"]; ok {
			attrStr += "  /" + p + "/"
		}
	}

	var buf strings.Builder
	display := name
	if label := attrs["label"]; label != "" && label != name { // the chat's label is "chat"
		display += " " + label
	}
	line := fmt.Sprintf("%s%s%-30s %s%s  %s",
		prefix, connector, display, durStr(dur), attrStr, status)
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
	// 0600: a trace holds prompts and the file contents the tools read.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
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
	var events []perfettoEvent
	pid := 1
	for _, r := range t.snapshot() {
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
