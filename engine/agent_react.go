package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RunToolFn gates and executes a tool call. Engine injects its own runTool
// as this function so the agent loop is decoupled from permission/approval/
// allowlist concerns.
type RunToolFn func(ctx context.Context, label string, call ToolCall, extraFns map[string]ToolFn, extraRisk map[string]RiskTier) ToolResult

// ReAct implements the Agent interface with a standard plan→act→observe loop:
//  1. Call the provider with the current message list (+ available tools)
//  2. If the model returns tool calls, execute them and append observations
//  3. Repeat until the model returns text (or submit_result)
//  4. Return the final text and the full updated conversation
//
// It is stateless: the provider, model, tools, budget and callbacks of a run
// come from its RunEnv.
type ReAct struct{}

// NewReAct returns the ReAct loop strategy (the Engine's default Agent).
func NewReAct() *ReAct { return &ReAct{} }

// Run implements Agent.Run. msgs already contain the user's prompt.
// Doom-loop state reset is handled by Engine before calling Run.
func (a *ReAct) Run(ctx context.Context, env RunEnv, req RunRequest, msgs []Message) (*RunResult, []Message, error) {
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 8192
	}

	// Validate extra tools early.
	for _, ct := range req.ExtraTools {
		if ct.Def.Name == "" {
			return nil, msgs, fmt.Errorf("ExtraTools contains a tool with empty Def.Name")
		}
		if ct.Fn == nil {
			return nil, msgs, fmt.Errorf("extra tool %q has nil Fn", ct.Def.Name)
		}
	}

	// Build the tool list the model sees: defaults + extras.
	useStructured := req.Schema != nil

	// Only tools the gate could let run are offered: one it always refuses
	// (write tier with no permission config, Deny, Ask with no one to ask)
	// only makes the model spend turns on calls that come back "denied".
	var toolDefs []ToolDef
	if req.UseTools {
		for _, d := range env.Tools.Defs() {
			if env.mayRun(d, false) {
				toolDefs = append(toolDefs, d)
			}
		}
	}
	for _, ct := range req.ExtraTools {
		if env.Tools.Has(ct.Def.Name) {
			env.Logger.Warn("extra tool shadows built-in", "name", ct.Def.Name)
		}
		if env.mayRun(ct.Def, true) {
			toolDefs = append(toolDefs, ct.Def)
		}
	}

	// When using structured output, avoid the two-phase slow path for simple
	// requests: if no tools are needed, skip Phase 1 entirely.
	if useStructured && len(toolDefs) == 0 {
		resp, span, err := a.callModel(ctx, env, "chat", req.Label, ChatRequest{
			System:    req.System,
			Messages:  msgs,
			Schema:    req.Schema,
			MaxTokens: maxTok,
			Model:     env.Model,
		})
		if err != nil {
			return nil, msgs, err
		}
		span.End()
		if js, ok := structuredAnswer(resp); ok {
			msgs = append(msgs, Message{Role: RoleAssistant, Content: js})
			return &RunResult{Text: js, Tools: nil}, msgs, nil
		}
		msgs = append(msgs, Message{Role: RoleAssistant, Content: resp.Text})
		return &RunResult{Text: resp.Text, Tools: nil}, msgs, nil
	}

	// The model needs a way to hand in the structured answer while it still
	// has tools: submit_result, whose input schema IS the requested schema.
	if useStructured {
		toolDefs = append(toolDefs, submitToolDef(req.Schema))
	}

	// Build lookup maps for extra tool dispatch.
	extraFns := make(map[string]ToolFn, len(req.ExtraTools))
	extraRisk := make(map[string]RiskTier, len(req.ExtraTools))
	for _, ct := range req.ExtraTools {
		extraFns[ct.Def.Name] = ct.Fn
		extraRisk[ct.Def.Name] = ct.Def.Risk
	}

	if req.Label != "" {
		env.Logger.Info("agent start", "label", req.Label)
	}

	nudges := 0
	var toolNames []string

	// ── Phase 1: tool-gathering loop ───────────────────────────────
	// Runs with tools but WITHOUT Schema (no response_format) so the
	// model can freely call tools to gather information.
	for {
		if err := ctx.Err(); err != nil {
			return nil, msgs, err
		}
		msgs = fitContext(compact(msgs), req.System, env.ContextWindow)

		// The span stays open over the turn: its tool calls become children.
		resp, chatSpan, err := a.callModel(ctx, env, "chat", req.Label, ChatRequest{
			System:    req.System,
			Messages:  msgs,
			Tools:     toolDefs,
			MaxTokens: maxTok,
			Model:     env.Model,
		})
		if err != nil {
			return nil, msgs, err
		}
		truncated := resp.StopReason == "length" || resp.StopReason == "max_tokens" // OpenAI / Anthropic
		if truncated {
			env.Logger.Warn("response truncated", "label", req.Label, "stop_reason", resp.StopReason)
		}

		if len(resp.ToolCalls) > 0 {
			// Deduplicate tool calls with identical name+input in the same batch.
			seen := make(map[string]bool)
			deduped := make([]ToolCall, 0, len(resp.ToolCalls))
			for _, call := range resp.ToolCalls {
				fp := fingerprint(call.Input)
				key := call.Name + ":" + fp
				if seen[key] {
					continue
				}
				seen[key] = true
				deduped = append(deduped, call)
			}
			resp.ToolCalls = deduped
			msgs = append(msgs, Message{Role: RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls})

			// Defer submit_result to last so other tools execute in the same
			// turn before the agent signals completion.
			var submitResult, rejected string

			// Collect all tool names for RunResult metadata.
			for _, call := range resp.ToolCalls {
				toolNames = append(toolNames, call.Name)
			}

			// Propagate the chat span via context so toolrunner creates child spans.
			toolCtx := WithSpan(ctx, chatSpan)

			// Run the calls in the model's order; consecutive read-only calls
			// run concurrently (see batchToolCalls).
			for _, batch := range batchToolCalls(resp.ToolCalls, extraRisk) {
				// A cancelled run stops here: the rest of the turn's calls
				// would still run (and prompt) after the user pressed Esc.
				if err := ctx.Err(); err != nil {
					chatSpan.End()
					return nil, msgs, err
				}
				if len(batch) > 1 {
					results := make([]ToolResult, len(batch))
					var wg sync.WaitGroup
					for i, call := range batch {
						wg.Go(func() { results[i] = env.RunTool(toolCtx, req.Label, call, extraFns, extraRisk) })
					}
					wg.Wait()
					// Results in call order, so the model sees a deterministic history.
					for i, call := range batch {
						msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: clampResult(results[i].String(), env.ContextWindow)})
					}
					continue
				}
				call := batch[0]
				if call.Name == SubmitTool {
					b, _ := json.Marshal(call.Input)
					// A cut-off reply arrives as {} or a partial object: taking it
					// as the answer gave totems an empty result (and task wrote
					// an empty plan over the real one). Refuse it; the model tries
					// again, within MaxNudges.
					if problem := submitProblem(req.Schema, b, truncated); problem != "" {
						rejected = problem
						msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: "ERROR: " + problem})
						continue
					}
					chatSpan.SetAttr("submit_result", "true")
					submitResult = string(b)
					// Answer the call so the history stays well-formed
					// (every tool_use has its tool_result) for a later Chat.
					msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: "submitted"})
					continue
				}
				out := env.RunTool(toolCtx, req.Label, call, extraFns, extraRisk)
				msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: clampResult(out.String(), env.ContextWindow)})
			}
			chatSpan.End()
			if submitResult != "" {
				return &RunResult{Text: submitResult, Tools: toolNames}, msgs, nil
			}
			if rejected != "" {
				if nudges >= maxNudgesOf(env) {
					return nil, msgs, fmt.Errorf("model did not return a complete structured answer after %d attempts: %s", nudges, rejected)
				}
				nudges++
			}
			continue
		}

		chatSpan.End()

		// No tool calls this turn.

		// ── Structured answer ───────────────────────────────────────
		// The model stopped calling tools without submit_result. In order:
		// JSON already in the text → native structured output (one more
		// call, when the provider supports it) → nudge back to submit_result.
		if useStructured {
			if js, ok := extractJSON(resp.Text); ok && submitProblem(req.Schema, []byte(js), truncated) == "" {
				msgs = append(msgs, Message{Role: RoleAssistant, Content: js})
				return &RunResult{Text: js, Tools: toolNames}, msgs, nil
			}
			msgs = append(msgs, Message{Role: RoleAssistant, Content: cmp.Or(resp.Text, "(no output)")})

			if env.Provider.Capabilities().StructuredOutput {
				// A user turn, not an assistant prefill: newer models reject
				// a conversation that ends on an assistant message.
				msgs = append(msgs, Message{Role: RoleUser, Content: "Now give the final answer as the structured object."})
				finalResp, phaseSpan, err := a.callModel(ctx, env, "phase2", req.Label, ChatRequest{
					System:   req.System,
					Messages: msgs,
					// The history holds this run's tool calls, so the tools
					// stay declared — but this call must answer, not call one.
					Tools:       toolDefs,
					NoToolCalls: true,
					Schema:      req.Schema, // native structured output in the provider
					MaxTokens:   maxTok,
					Model:       env.Model,
				})
				if err != nil {
					return nil, msgs, err
				}
				phaseSpan.End()
				phaseCut := finalResp.StopReason == "length" || finalResp.StopReason == "max_tokens"
				if js, ok := structuredAnswer(finalResp); ok && submitProblem(req.Schema, []byte(js), phaseCut) == "" {
					msgs = append(msgs, Message{Role: RoleAssistant, Content: js})
					return &RunResult{Text: js, Tools: toolNames}, msgs, nil
				}
				msgs = append(msgs, Message{Role: RoleAssistant, Content: cmp.Or(finalResp.Text, "(no output)")})
			}

			if nudges >= maxNudgesOf(env) {
				return nil, msgs, fmt.Errorf("model did not return structured output after %d attempts", nudges)
			}
			nudges++
			msgs = append(msgs, Message{Role: RoleUser, Content: "Reply by calling the " + SubmitTool + " tool with the final object (or a single JSON object)."})
			continue
		}

		// No schema → just return the text response.
		msgs = append(msgs, Message{Role: RoleAssistant, Content: resp.Text})
		return &RunResult{Text: resp.Text, Tools: toolNames}, msgs, nil
	}
}

// callModel makes one provider call inside a span named name (tagged with
// the run's label, so a totem's scout, passes and reconcile tell apart in the
// trace) and charges its
// usage to the budget. On success the span is returned open — the caller ends
// it, after the turn's tool calls when they should nest under it; on failure
// it is ended with the error.
func (a *ReAct) callModel(ctx context.Context, env RunEnv, name, label string, req ChatRequest) (ChatResponse, *Span, error) {
	span := env.Tracer.Start(name)
	if label != "" {
		span.SetAttr("label", label)
	}
	resp, err := a.streamChat(ctx, env, req)
	if err == nil {
		err = env.Budget.Add(resp.Usage)
	}
	if err != nil {
		span.SetError(err.Error())
		span.End()
		return resp, nil, err
	}
	span.SetAttr("input_tokens", strconv.Itoa(resp.Usage.InputTokens))
	span.SetAttr("output_tokens", strconv.Itoa(resp.Usage.OutputTokens))
	return resp, span, nil
}

// streamChat calls the provider's streaming Chat and accumulates events into
// a ChatResponse. Text tokens are forwarded to env.OnToken (if set) in real time.
func (a *ReAct) streamChat(ctx context.Context, env RunEnv, req ChatRequest) (ChatResponse, error) {
	for attempt := 1; ; attempt++ {
		resp, shown, err := a.streamOnce(ctx, env, req)
		if err == nil {
			return resp, nil
		}
		// A stream that broke mid-way is retried, so one dropped connection
		// does not throw away a whole multi-agent run — unless the text was
		// already on screen (a retry would show it twice).
		if attempt == maxStreamAttempts || shown || !retryableStreamError(ctx, err) {
			return resp, err
		}
		env.Logger.Warn("provider stream failed, retrying", "attempt", attempt, "error", err)
		select {
		case <-time.After(time.Duration(attempt) * streamRetryDelay):
		case <-ctx.Done():
			return resp, ctx.Err()
		}
	}
}

// maxStreamAttempts bounds the calls streamChat makes for one request.
const maxStreamAttempts = 3

// streamRetryDelay is the wait before the first retry; it grows linearly.
// A variable so tests can shorten it.
var streamRetryDelay = time.Second

// retryableStreamError reports whether a failed provider call is worth
// repeating: a reply that started and then broke off or went silent (STREAM).
// Never a cancelled run, nor any other failure — auth, config, budget, a
// refusal, an error status, a connection the client already retried.
func retryableStreamError(ctx context.Context, err error) bool {
	return ctx.Err() == nil && Is(err, ErrCodeStream)
}

// streamOnce makes one provider call. shown reports whether text already
// reached env.OnToken (a TUI), which makes a retry unsafe.
func (a *ReAct) streamOnce(ctx context.Context, env RunEnv, req ChatRequest) (ChatResponse, bool, error) {
	// An idle timeout, not a deadline: a long answer that keeps streaming is
	// never cut; only a provider that goes silent for streamTimeout is.
	timeout := time.Duration(env.Config.StreamTimeoutSec()) * time.Second
	chatCtx, idled, stop := idleWatchdog(ctx, timeout)
	defer stop()
	shown := false
	var onToken TokenCallback
	if env.OnToken != nil {
		onToken = func(t string) {
			shown = true
			env.OnToken(t)
		}
	}
	resp, err := CollectStream(withActivity(chatCtx, env.Provider.Chat(chatCtx, req)), onToken)
	if err != nil {
		if idled() && ctx.Err() == nil {
			return resp, shown, errStreamIdle(timeout)
		}
		return resp, shown, err
	}
	if resp.Usage.InputTokens == 0 && resp.Usage.OutputTokens == 0 && resp.Text != "" {
		resp.Usage.OutputTokens = estimateTokens(resp.Text)
		if resp.Usage.OutputTokens < 1 {
			resp.Usage.OutputTokens = 1
		}
		var inBuilder strings.Builder
		if req.System != "" {
			inBuilder.WriteString(req.System)
		}
		for _, m := range req.Messages {
			inBuilder.WriteString(m.Content)
		}
		if n := estimateTokens(inBuilder.String()); n > 0 {
			resp.Usage.InputTokens = n
		}
	}
	return resp, shown, nil
}

// compactKeepRecent keeps the N most recent messages verbatim; older tool
// results get their content trimmed to bound context growth (cheap compaction).
const compactKeepRecent = 6
const compactTrimAt = 800

// compactMaxMsgs is the maximum number of conversation messages kept in the
// sliding window. Older messages beyond this limit are dropped to prevent
// unbounded context growth across long chat sessions.
const compactMaxMsgs = 60

// compact trims old tool results and applies a sliding window to keep the
// conversation size bounded. It returns a new slice and never modifies the
// caller's messages. Message order is preserved; the most recent messages are
// kept verbatim.
func compact(msgs []Message) []Message {
	if len(msgs) <= compactKeepRecent {
		return msgs
	}
	out := slices.Clone(msgs)
	// 1. Trim content of older tool-result messages — never the newest turn's:
	// counted in messages, a batch of 8 parallel reads had its first results
	// cut before the model ever saw them (and it re-read them in a loop).
	cut := len(out) - compactKeepRecent
	if last := lastAssistant(out); last >= 0 && last < cut {
		cut = last
	}
	for i := range cut {
		if out[i].Role == RoleTool && len(out[i].Content) > compactTrimAt {
			out[i].Content = strings.ToValidUTF8(out[i].Content[:compactTrimAt], "") + "…[compacted]"
		}
	}
	// 2. Sliding window: drop oldest messages to stay within limit.
	if len(out) > compactMaxMsgs {
		return slideWindow(out)
	}
	return out
}

// lastAssistant is the index of the last assistant message, -1 when none.
func lastAssistant(msgs []Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant {
			return i
		}
	}
	return -1
}

// slideWindow keeps at most compactMaxMsgs of the most recent messages, cut
// where the conversation stays valid for every provider:
//   - never on a tool result, whose tool call would be dropped (Anthropic
//     rejects an orphan tool_result with a 400);
//   - when the window opens on an assistant turn, the first message (the
//     user's task) is pinned in front, so the conversation still starts with
//     a user turn and keeps its goal.
func slideWindow(msgs []Message) []Message { return slideWindowN(msgs, compactMaxMsgs) }

// slideWindowN is slideWindow keeping at most n messages.
func slideWindowN(msgs []Message, n int) []Message {
	start := len(msgs) - n
	skipToolResults := func() {
		for start < len(msgs) && msgs[start].Role == RoleTool {
			start++
		}
	}
	skipToolResults()
	if start >= len(msgs) || msgs[start].Role == RoleUser || msgs[0].Role != RoleUser {
		return slices.Clone(msgs[start:])
	}
	// Make room for the pinned task without exceeding the limit.
	start++
	skipToolResults()
	if start < len(msgs) && msgs[start].Role == RoleUser {
		return slices.Clone(msgs[start:])
	}
	return append([]Message{msgs[0]}, msgs[start:]...)
}

// compressConversation collapses tool-heavy turns into a compact form:
// user question → final assistant response, skipping intermediate tool calls
// and results that are useless for future turns. This prevents context bloat
// from large file reads while preserving the model's synthesized answer.
func compressConversation(msgs []Message) []Message {
	n := len(msgs)
	if n < 4 {
		return msgs
	}

	var out []Message
	i := 0
	for i < n {
		if msgs[i].Role != RoleUser {
			out = append(out, msgs[i])
			i++
			continue
		}
		// Found a user message. Walk forward to find the turn boundaries and
		// identify the final assistant response with actual text content.
		turnStart := i
		var finalContent *Message
		var hasTools bool
		j := i + 1
		for j < n {
			if msgs[j].Role == RoleUser {
				break // next turn
			}
			if msgs[j].Role == RoleAssistant && len(msgs[j].ToolCalls) > 0 {
				hasTools = true
			}
			if msgs[j].Role == RoleAssistant && msgs[j].Content != "" && len(msgs[j].ToolCalls) == 0 {
				finalContent = &msgs[j]
			}
			j++
		}
		if hasTools && finalContent != nil {
			// Compress: keep the user question, any edit/write diffs (small,
			// useful for the model and TUI), and the final response. Each kept
			// result keeps the assistant call it answers, pruned to the kept
			// calls: providers reject a tool result without its tool call.
			kept := make(map[string]bool)
			for k := turnStart; k < j; k++ {
				if isEditResult(msgs[k]) {
					kept[msgs[k].ToolCallID] = true
				}
			}
			out = append(out, msgs[turnStart])
			for k := turnStart + 1; k < j; k++ {
				m := msgs[k]
				switch {
				case m.Role == RoleAssistant && len(m.ToolCalls) > 0:
					var calls []ToolCall
					for _, c := range m.ToolCalls {
						if kept[c.ID] {
							calls = append(calls, c)
						}
					}
					if len(calls) > 0 {
						m.ToolCalls = calls
						out = append(out, m)
					}
				case m.Role == RoleTool && kept[m.ToolCallID]:
					out = append(out, m)
				}
			}
			out = append(out, *finalContent)
		} else {
			// No tool calls or no final content — keep verbatim.
			for k := turnStart; k < j; k++ {
				out = append(out, msgs[k])
			}
		}
		i = j
	}
	return out
}

// isEditResult reports whether m is a write/edit result, whose diff is worth
// keeping in a compressed conversation (the chat TUI renders it).
func isEditResult(m Message) bool {
	return m.Role == RoleTool && m.ToolCallID != "" &&
		(strings.HasPrefix(m.Content, "✓ edited") || strings.HasPrefix(m.Content, "✓ wrote") ||
			strings.Contains(m.Content, GitGuardMarker)) // the user must still see it
}

// submitToolDef is the tool the model calls to hand in a structured answer.
// Its input schema is the run's Schema, so the call's input is the answer.
func submitToolDef(schema map[string]any) ToolDef {
	return ToolDef{
		Name:        SubmitTool,
		Description: "Submit the final answer. Call it exactly once, when you are done, with the complete result object.",
		Schema:      schema,
	}
}

// structuredAnswer extracts the JSON answer from a structured-output reply:
// a submit_result call (how providers without a native JSON mode answer, e.g.
// Anthropic's forced tool), else a JSON object in the text.
func structuredAnswer(resp ChatResponse) (string, bool) {
	for _, tc := range resp.ToolCalls {
		if tc.Name != SubmitTool {
			continue
		}
		if b, err := json.Marshal(tc.Input); err == nil {
			return string(b), true
		}
	}
	return extractJSON(resp.Text)
}

// maxNudgesOf is how many times a run asks again for the structured answer.
func maxNudgesOf(env RunEnv) int {
	if n := env.Config.MaxNudges; n > 0 {
		return n
	}
	return 3
}

// submitProblem says why js cannot be the run's structured answer, "" when
// it can: the reply was cut off by the output limit, it is not an object, or
// it lacks a field the schema requires (a code example in prose, or the {}
// a provider makes of an unparsable cut-off tool input).
func submitProblem(schema map[string]any, js []byte, truncated bool) string {
	if truncated {
		return "the reply was cut off by the output limit — call " + SubmitTool + " again with the complete object, written more concisely"
	}
	var obj map[string]any
	if json.Unmarshal(js, &obj) != nil || obj == nil {
		return "the answer must be one JSON object matching the schema"
	}
	var missing []string
	for _, k := range requiredKeys(schema) {
		if v, ok := obj[k]; !ok || v == nil {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return "the answer lacks required field(s) " + strings.Join(missing, ", ") + " — call " + SubmitTool + " again with the complete object"
	}
	return ""
}

// requiredKeys reads a schema's top-level "required" list ([]string from the
// schema package, []any from decoded JSON).
func requiredKeys(schema map[string]any) []string {
	switch r := schema["required"].(type) {
	case []string:
		return r
	case []any:
		var out []string
		for _, v := range r {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// extractJSON pulls the first balanced-looking JSON object out of free text.
func extractJSON(s string) (string, bool) {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return "", false
	}
	cand := s[i : j+1]
	var v any
	if json.Unmarshal([]byte(cand), &v) != nil {
		return "", false
	}
	return cand, true
}

// estimateTokens estimates the number of tokens in a text string when the
// provider does not report usage. Uses a simple heuristic based on character
// composition:
//   - Pure ASCII text averages ~4 chars/token
//   - Mixed/Latin-script text (Portuguese, French, etc.) averages ~3-3.5
//   - CJK/non-Latin text averages ~1.5-2 chars/token
//
// This is a rough estimate — real tokenization depends on the model's
// tokenizer. The function errs towards overestimation for non-ASCII text
// to avoid surprise budget breaches.
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return len(text) / 4
	}
	asciiCount := 0
	for _, r := range runes {
		if r <= 0x7f {
			asciiCount++
		}
	}
	nonASCII := len(runes) - asciiCount
	ratio := float64(nonASCII) / float64(len(runes))
	switch {
	case ratio > 0.5:
		// Mostly non-Latin (CJK, emoji, etc.) — ~2 chars/token.
		return len(runes) / 2
	case ratio > 0.15:
		// Mixed with significant non-ASCII (accented Latin, etc.) — ~3 chars/token.
		return len(runes) / 3
	default:
		// Mostly ASCII — ~4 chars/token.
		return len(runes) / 4
	}
}
