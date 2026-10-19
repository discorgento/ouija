package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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

	var toolDefs []ToolDef
	if req.UseTools {
		toolDefs = append(toolDefs, env.Tools.Defs()...)
	}
	for _, ct := range req.ExtraTools {
		if env.Tools.Has(ct.Def.Name) {
			env.Logger.Warn("extra tool shadows built-in", "name", ct.Def.Name)
		}
		toolDefs = append(toolDefs, ct.Def)
	}

	// When using structured output, avoid the two-phase slow path for simple
	// requests: if no tools are needed, skip Phase 1 entirely.
	if useStructured && len(toolDefs) == 0 {
		span := env.Tracer.Start("chat")
		resp, err := a.streamChat(ctx, env, ChatRequest{
			System:    req.System,
			Messages:  msgs,
			Schema:    req.Schema,
			MaxTokens: maxTok,
			Model:     env.Model,
		})
		if err != nil {
			span.SetError(err.Error())
			span.End()
			return nil, msgs, err
		}
		if berr := env.Budget.Add(resp.Usage); berr != nil {
			span.SetError(berr.Error())
			span.End()
			return nil, msgs, berr
		}
		span.SetAttr("input_tokens", fmt.Sprintf("%d", resp.Usage.InputTokens))
		span.SetAttr("output_tokens", fmt.Sprintf("%d", resp.Usage.OutputTokens))
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
		msgs = compact(msgs)

		chatSpan := env.Tracer.Start("chat")
		resp, err := a.streamChat(ctx, env, ChatRequest{
			System:    req.System,
			Messages:  msgs,
			Tools:     toolDefs,
			MaxTokens: maxTok,
			Model:     env.Model,
		})
		if err != nil {
			chatSpan.SetError(err.Error())
			chatSpan.End()
			return nil, msgs, err
		}
		if berr := env.Budget.Add(resp.Usage); berr != nil {
			chatSpan.SetError(berr.Error())
			chatSpan.End()
			return nil, msgs, berr
		}
		if resp.StopReason == "length" {
			env.Logger.Warn("response truncated", "label", req.Label, "stop_reason", "length")
		}
		chatSpan.SetAttr("input_tokens", fmt.Sprintf("%d", resp.Usage.InputTokens))
		chatSpan.SetAttr("output_tokens", fmt.Sprintf("%d", resp.Usage.OutputTokens))

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
			var submitResult string

			// Collect all tool names for RunResult metadata.
			for _, call := range resp.ToolCalls {
				toolNames = append(toolNames, call.Name)
			}

			// Propagate the chat span via context so toolrunner creates child spans.
			toolCtx := WithSpan(ctx, chatSpan)

			// Classify tools: parallel-safe (read-only) run concurrently;
			// sequential (write, bash) run one at a time.
			parCalls, seqCalls := classifyToolCalls(resp.ToolCalls, extraRisk)

			// ── Parallel execution ──────────────────────────────────────
			if len(parCalls) > 0 {
				type callResult struct {
					idx    int
					result ToolResult
				}
				ch := make(chan callResult, len(parCalls))
				for i, call := range parCalls {
					i, call := i, call
					go func() {
						out := env.RunTool(toolCtx, req.Label, call, extraFns, extraRisk)
						ch <- callResult{i, out}
					}()
				}
				results := make([]ToolResult, len(parCalls))
				for range len(parCalls) {
					cr := <-ch
					results[cr.idx] = cr.result
				}
				close(ch)
				// Append results in call order so the model sees deterministic order.
				for i, call := range parCalls {
					msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: results[i].String()})
				}
			}

			// ── Sequential execution ────────────────────────────────────
			for _, call := range seqCalls {
				if call.Name == submitTool {
					chatSpan.SetAttr("submit_result", "true")
					b, _ := json.Marshal(call.Input)
					submitResult = string(b)
					// Answer the call so the history stays well-formed
					// (every tool_use has its tool_result) for a later Chat.
					msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: "submitted"})
					continue
				}
				out := env.RunTool(toolCtx, req.Label, call, extraFns, extraRisk)
				msgs = append(msgs, Message{Role: RoleTool, ToolCallID: call.ID, Content: out.String()})
			}
			chatSpan.End()
			if submitResult != "" {
				return &RunResult{Text: submitResult, Tools: toolNames}, msgs, nil
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
			if js, ok := extractJSON(resp.Text); ok {
				msgs = append(msgs, Message{Role: RoleAssistant, Content: js})
				return &RunResult{Text: js, Tools: toolNames}, msgs, nil
			}
			msgs = append(msgs, Message{Role: RoleAssistant, Content: cmp.Or(resp.Text, "(no output)")})

			if env.Provider.Capabilities().StructuredOutput {
				// A user turn, not an assistant prefill: newer models reject
				// a conversation that ends on an assistant message.
				msgs = append(msgs, Message{Role: RoleUser, Content: "Now give the final answer as the structured object."})
				phaseSpan := env.Tracer.Start("phase2")
				finalResp, err := a.streamChat(ctx, env, ChatRequest{
					System:    req.System,
					Messages:  msgs,
					Schema:    req.Schema, // native structured output in the provider
					MaxTokens: maxTok,
					Model:     env.Model,
				})
				if err != nil {
					phaseSpan.SetError(err.Error())
					phaseSpan.End()
					return nil, msgs, err
				}
				if berr := env.Budget.Add(finalResp.Usage); berr != nil {
					phaseSpan.SetError(berr.Error())
					phaseSpan.End()
					return nil, msgs, berr
				}
				phaseSpan.SetAttr("input_tokens", fmt.Sprintf("%d", finalResp.Usage.InputTokens))
				phaseSpan.SetAttr("output_tokens", fmt.Sprintf("%d", finalResp.Usage.OutputTokens))
				phaseSpan.End()
				if js, ok := structuredAnswer(finalResp); ok {
					msgs = append(msgs, Message{Role: RoleAssistant, Content: js})
					return &RunResult{Text: js, Tools: toolNames}, msgs, nil
				}
				msgs = append(msgs, Message{Role: RoleAssistant, Content: cmp.Or(finalResp.Text, "(no output)")})
			}

			maxNudges := env.Config.MaxNudges
			if maxNudges <= 0 {
				maxNudges = 3
			}
			if nudges >= maxNudges {
				return nil, msgs, fmt.Errorf("model did not return structured output after %d attempts", nudges)
			}
			nudges++
			msgs = append(msgs, Message{Role: RoleUser, Content: "Reply by calling the " + submitTool + " tool with the final object (or a single JSON object)."})
			continue
		}

		// No schema → just return the text response.
		msgs = append(msgs, Message{Role: RoleAssistant, Content: resp.Text})
		return &RunResult{Text: resp.Text, Tools: toolNames}, msgs, nil
	}
}

// streamChat calls the provider's streaming Chat and accumulates events into
// a ChatResponse. Text tokens are forwarded to env.OnToken (if set) in real time.
func (a *ReAct) streamChat(ctx context.Context, env RunEnv, req ChatRequest) (ChatResponse, error) {
	timeout := time.Duration(env.Config.StreamTimeoutSec()) * time.Second
	chatCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		chatCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	resp, err := CollectStream(env.Provider.Chat(chatCtx, req), env.OnToken)
	if err != nil {
		return resp, err
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
	return resp, nil
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
	// 1. Trim content of older tool-result messages.
	for i := range len(out) - compactKeepRecent {
		if out[i].Role == RoleTool && len(out[i].Content) > compactTrimAt {
			out[i].Content = out[i].Content[:compactTrimAt] + "…[compacted]"
		}
	}
	// 2. Sliding window: drop oldest messages to stay within limit.
	if len(out) > compactMaxMsgs {
		return slideWindow(out)
	}
	return out
}

// slideWindow keeps at most compactMaxMsgs of the most recent messages, cut
// where the conversation stays valid for every provider:
//   - never on a tool result, whose tool call would be dropped (Anthropic
//     rejects an orphan tool_result with a 400);
//   - when the window opens on an assistant turn, the first message (the
//     user's task) is pinned in front, so the conversation still starts with
//     a user turn and keeps its goal.
func slideWindow(msgs []Message) []Message {
	start := len(msgs) - compactMaxMsgs
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
		(strings.HasPrefix(m.Content, "✓ edited") || strings.HasPrefix(m.Content, "✓ wrote"))
}

// submitToolDef is the tool the model calls to hand in a structured answer.
// Its input schema is the run's Schema, so the call's input is the answer.
func submitToolDef(schema map[string]any) ToolDef {
	return ToolDef{
		Name:        submitTool,
		Description: "Submit the final answer. Call it exactly once, when you are done, with the complete result object.",
		Schema:      schema,
	}
}

// structuredAnswer extracts the JSON answer from a structured-output reply:
// a submit_result call (how providers without a native JSON mode answer, e.g.
// Anthropic's forced tool), else a JSON object in the text.
func structuredAnswer(resp ChatResponse) (string, bool) {
	for _, tc := range resp.ToolCalls {
		if tc.Name != submitTool {
			continue
		}
		if b, err := json.Marshal(tc.Input); err == nil {
			return string(b), true
		}
	}
	return extractJSON(resp.Text)
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
