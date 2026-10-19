package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestTracerNilSafety(t *testing.T) {
	// All methods on nil *Tracer should be no-ops.
	var tr *Tracer
	tr.Render(nil)        // should not panic
	tr.WritePerfetto(nil) // should not panic
	tr.WritePerfettoFile("/dev/null")
	tr.Reset() // should not panic
}

func TestSpanNilSafety(t *testing.T) {
	var s *Span
	s.End()             // should not panic
	s.Duration()        // should return 0
	s.SetAttr("k", "v") // should not panic
	s.SetError("err")   // should not panic
	s.StartChild("c")   // should return nil
}

func TestInactiveTracer(t *testing.T) {
	tr := NewTracer(false)
	s := tr.Start("chat")
	if s != nil {
		t.Fatal("expected nil span for inactive tracer")
	}
	var buf bytes.Buffer
	tr.Render(&buf)
	if buf.Len() != 0 {
		t.Fatal("inactive tracer should render nothing")
	}
}

func TestSpanLifecycle(t *testing.T) {
	s := &Span{Name: "test", Start: time.Now()}
	time.Sleep(time.Millisecond)
	s.End()

	d := s.Duration()
	if d < time.Millisecond {
		t.Fatalf("expected at least 1ms, got %v", d)
	}

	// End is idempotent.
	prev := d
	s.End()
	if s.Duration() != prev {
		t.Fatal("second End should be a no-op")
	}
}

func TestSpanEndSetsEnded(t *testing.T) {
	s := &Span{Name: "x"}
	s.End()
	// A never-started span has zero Start, so End sets end but Duration
	// still returns 0 (zero end minus zero start). That's fine.
	s.End() // second call shouldn't change anything
}

func TestSpanChildren(t *testing.T) {
	parent := &Span{Name: "parent", Start: time.Now()}
	c1 := parent.StartChild("child1")
	c2 := parent.StartChild("child2")

	if len(parent.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(parent.Children))
	}

	c1.End()
	c2.End()
	parent.End()

	if c1.Duration() == 0 {
		t.Fatal("child1 should have duration")
	}
}

func TestSpanAttrs(t *testing.T) {
	s := &Span{Name: "test"}
	s.SetAttr("key1", "val1")
	s.SetAttr("key2", "val2")

	if s.Attrs["key1"] != "val1" {
		t.Fatal("key1 not set")
	}
	if s.Attrs["key2"] != "val2" {
		t.Fatal("key2 not set")
	}
}

func TestSpanSetError(t *testing.T) {
	s := &Span{Name: "tool.read"}
	s.SetError("file not found")
	if s.Error != "file not found" {
		t.Fatal("error not set")
	}
}

func TestTracerStartAndReset(t *testing.T) {
	tr := NewTracer(true)
	s1 := tr.Start("chat")
	s2 := tr.Start("tool.read")
	s1.End()
	s2.End()

	if len(tr.roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tr.roots))
	}

	tr.Reset()
	if len(tr.roots) != 0 {
		t.Fatal("expected empty roots after reset")
	}
}

func TestTracerRenderOutput(t *testing.T) {
	tr := NewTracer(true)

	// Simulate a realistic trace.
	chat := tr.Start("chat")
	chat.SetAttr("input_tokens", "123")
	chat.SetAttr("output_tokens", "456")

	tool1 := chat.StartChild("tool.read_file")
	tool1.SetAttr("path", "src/main.go")
	time.Sleep(time.Millisecond)
	tool1.End()

	tool2 := chat.StartChild("tool.grep")
	tool2.SetError("not found")
	time.Sleep(time.Millisecond)
	tool2.End()

	chat.End()

	var buf bytes.Buffer
	tr.Render(&buf)

	output := buf.String()
	t.Logf("Render output:\n%s", output)

	// Check key parts exist.
	checks := []string{
		"chat",
		"tool.read_file",
		"tool.grep",
		"src/main.go",
		"not found",
		"123 in",
		"456 out",
		"2 tool calls",
	}
	for _, c := range checks {
		if !strings.Contains(output, c) {
			t.Errorf("expected Render output to contain %q", c)
		}
	}
}

func TestTracerPerfettoExport(t *testing.T) {
	tr := NewTracer(true)

	chat := tr.Start("chat")
	tool1 := chat.StartChild("tool.read_file")
	tool1.SetAttr("path", "src/main.go")
	time.Sleep(100 * time.Microsecond)
	tool1.End()
	chat.End()

	var buf bytes.Buffer
	err := tr.WritePerfetto(&buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	t.Logf("Perfetto output:\n%s", output)

	if !strings.Contains(output, `"name": "chat"`) {
		t.Error("expected chat span in Perfetto output")
	}
	if !strings.Contains(output, `"name": "tool.read_file"`) {
		t.Error("expected tool span in Perfetto output")
	}
	if !strings.Contains(output, `"ph": "X"`) {
		t.Error("expected complete events (ph: X)")
	}
}

func TestContextPropagation(t *testing.T) {
	ctx := context.Background()
	span := &Span{Name: "test"}

	// Before propagation, SpanFromContext returns nil.
	if s := SpanFromContext(ctx); s != nil {
		t.Fatal("expected nil before WithSpan")
	}

	ctx = WithSpan(ctx, span)
	if s := SpanFromContext(ctx); s != span {
		t.Fatal("expected same span after WithSpan")
	}

	// Chain: child context should still propagate.
	type extraKey struct{}
	childCtx := context.WithValue(ctx, extraKey{}, "value")
	if s := SpanFromContext(childCtx); s != span {
		t.Fatal("expected span in child context")
	}
}

func TestContextNilSafety(t *testing.T) {
	ctx := context.Background()
	if s := SpanFromContext(ctx); s != nil {
		t.Fatal("expected nil from empty context")
	}

	// WithSpan with nil span.
	ctx = WithSpan(ctx, nil)
	if s := SpanFromContext(ctx); s != nil {
		t.Fatal("expected nil after WithSpan with nil span")
	}
}

func TestDurStr(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "<1ms"},
		{500 * time.Microsecond, "<1ms"},
		{time.Millisecond, "1ms"},
		{500 * time.Millisecond, "500ms"},
		{time.Second, "1.0s"},
		{1500 * time.Millisecond, "1.5s"},
		{10 * time.Second, "10.0s"},
	}
	for _, tc := range tests {
		got := durStr(tc.d)
		if got != tc.want {
			t.Errorf("durStr(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestCategory(t *testing.T) {
	tests := []struct {
		name string
		cat  string
	}{
		{"chat", "chat"},
		{"phase2", "chat"},
		{"tool.read_file", "tool"},
		{"tool.grep", "tool"},
		{"thinking", "agent"},
		{"other", "agent"},
	}
	for _, tc := range tests {
		got := category(tc.name)
		if got != tc.cat {
			t.Errorf("category(%q) = %q, want %q", tc.name, got, tc.cat)
		}
	}
}

func TestInactivePerfetto(t *testing.T) {
	tr := NewTracer(false)
	var buf bytes.Buffer
	err := tr.WritePerfetto(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatal("inactive tracer should produce no Perfetto output")
	}
}

func TestTracerRootCount(t *testing.T) {
	tr := NewTracer(true)
	_ = tr.Start("r1")
	_ = tr.Start("r2")
	_ = tr.Start("r3")
	tr.roots[0].End()
	tr.roots[1].End()
	tr.roots[2].End()

	if len(tr.roots) != 3 {
		t.Fatalf("expected 3 roots, got %d", len(tr.roots))
	}
}

func TestSpanConcurrentStartChild(t *testing.T) {
	// Verify StartChild is goroutine-safe.
	parent := &Span{Name: "parent", Start: time.Now()}
	n := 50
	done := make(chan struct{}, n)
	for range n {
		go func() {
			child := parent.StartChild("child")
			child.End()
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}
	parent.End()
	if len(parent.Children) != n {
		t.Fatalf("expected %d children, got %d", n, len(parent.Children))
	}
}

func TestTracerConcurrentStart(t *testing.T) {
	// Verify that Start (which appends to roots) is goroutine-safe.
	tr := NewTracer(true)
	n := 50
	done := make(chan struct{}, n)
	for range n {
		go func() {
			s := tr.Start("s")
			s.End()
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}
	if len(tr.roots) != n {
		t.Fatalf("expected %d roots, got %d", n, len(tr.roots))
	}
}

func TestWritePerfettoFile(t *testing.T) {
	tr := NewTracer(true)
	s := tr.Start("chat")
	s.End()

	path := t.TempDir() + "/trace.json"
	if err := tr.WritePerfettoFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestWritePerfettoFileInactive(t *testing.T) {
	tr := NewTracer(false)
	if err := tr.WritePerfettoFile("/dev/null"); err != nil {
		t.Fatal(err)
	}
}

func TestTracerRenderEmpty(t *testing.T) {
	tr := NewTracer(true)
	var buf bytes.Buffer
	tr.Render(&buf)
	if buf.Len() != 0 {
		t.Fatal("empty tracer should render nothing")
	}
}

func TestTracerNilRender(t *testing.T) {
	var tr *Tracer
	var buf bytes.Buffer
	tr.Render(&buf) // should not panic
	tr.Reset()      // should not panic
}

// The total is wall time — first start to last end — not the longest root.
func TestWallTime(t *testing.T) {
	t0 := time.Now()
	mk := func(start, dur time.Duration) *Span {
		return &Span{Start: t0.Add(start), end: t0.Add(start + dur), ended: true}
	}
	roots := []*Span{mk(0, 2*time.Second), mk(2*time.Second, 3*time.Second), {Start: t0.Add(9 * time.Second)}}
	if got := wallTime(roots); got != 5*time.Second {
		t.Fatalf("wallTime = %v; want 5s (two sequential roots, one never ended)", got)
	}
}

// NextTurn narrows the per-turn render without dropping spans from the
// session render (the chat's --trace-file used to export only the last turn).
func TestTracerNextTurn(t *testing.T) {
	tr := NewTracer(true)
	first := tr.Start("chat")
	first.StartChild("tool.read_file").End()
	first.End()
	tr.NextTurn()
	if got := tr.RenderLastRootString(); got != "" {
		t.Fatalf("new turn with no spans rendered %q", got)
	}
	s := tr.Start("chat")
	s.StartChild("tool.grep").End()
	s.End()
	if got := tr.RenderLastRootString(); !strings.Contains(got, "grep") || strings.Contains(got, "read_file") {
		t.Fatalf("turn render = %q; want only this turn's grep", got)
	}
	if got := tr.RenderString(); !strings.Contains(got, "read_file") || !strings.Contains(got, "grep") {
		t.Fatalf("session render lost a turn: %q", got)
	}
}

// A model call shows the run's label, so a totem's phases tell apart.
func TestRenderShowsRunLabel(t *testing.T) {
	tr := NewTracer(true)
	for _, label := range []string{"scout", "chat"} {
		s := tr.Start("chat")
		s.SetAttr("label", label)
		s.End()
	}
	out := tr.RenderString()
	if !strings.Contains(out, "chat scout") || strings.Contains(out, "chat chat") {
		t.Fatalf("render = %q; want the scout label, and no repeated chat", out)
	}
}

// LastTurn lists the turn's tool calls in order with their target, and sums the
// model's token usage; earlier turns are left out.
func TestTracerLastTurn(t *testing.T) {
	tr := NewTracer(true)
	old := tr.Start("chat")
	old.StartChild("tool.read_file").End()
	old.End()
	tr.NextTurn()

	chat := tr.Start("chat")
	chat.SetAttr("input_tokens", "1200")
	chat.SetAttr("output_tokens", "300")
	read := chat.StartChild("tool.read_file")
	read.SetAttr("path", "a.go")
	read.End()
	bash := chat.StartChild("tool.bash")
	bash.SetAttr("command", "go test ./...")
	bash.SetError("exit status 1")
	bash.End()
	chat.End()

	sum := tr.LastTurn()
	if len(sum.Tools) != 2 {
		t.Fatalf("tools = %+v, want this turn's two", sum.Tools)
	}
	if sum.Tools[0].Name != "read_file" || sum.Tools[0].Target != "a.go" || sum.Tools[0].Error != "" {
		t.Errorf("tools[0] = %+v", sum.Tools[0])
	}
	if sum.Tools[1].Name != "bash" || sum.Tools[1].Target != "go test ./..." || sum.Tools[1].Error != "exit status 1" {
		t.Errorf("tools[1] = %+v", sum.Tools[1])
	}
	if sum.InputTokens != 1200 || sum.OutputTokens != 300 {
		t.Errorf("tokens = %d in / %d out, want 1200 / 300", sum.InputTokens, sum.OutputTokens)
	}
	if (*Tracer)(nil).LastTurn().Tools != nil {
		t.Error("a nil tracer reports tools")
	}
}
