package engine

import (
	"encoding/json"
	"strings"
)

// Keeping a run within the model's context window. Without it, one 40 KB tool
// result overflowed a local model's 4k–8k window, and Ollama silently drops
// the start of the prompt — the system prompt and the task.

// minResultBytes is the smallest per-result cap, so a tiny window still
// shows something usable.
const minResultBytes = 2000

// resultCap is how many bytes one tool result may take: as many bytes as the
// window has tokens — at roughly 4 bytes a token, a quarter of the window —
// never above maxToolBytes. Unknown window: maxToolBytes.
func resultCap(window int) int {
	if window <= 0 {
		return maxToolBytes
	}
	return min(maxToolBytes, max(minResultBytes, window))
}

// clampResult cuts a tool result to resultCap(window), saying so.
func clampResult(s string, window int) string {
	limit := resultCap(window)
	if len(s) <= limit {
		return s
	}
	return strings.ToValidUTF8(s[:limit], "") +
		"\n…[cut to fit the model's context window — ask for a narrower range: read_file offset/limit, a more specific grep]"
}

// historyShare is the part of the window the conversation may fill; the rest
// is for the system prompt's growth, the tool definitions and the reply.
const historyShare = 0.6

// fitContext shrinks msgs until their estimated size, with system, fits the
// window: first the older tool results (never the newest turn's) are cut to
// a short stub, then the oldest messages leave, keeping the task. A window
// of 0 (unknown) changes nothing.
func fitContext(msgs []Message, system string, window int) []Message {
	if window <= 0 {
		return msgs
	}
	target := int(float64(window)*historyShare) - estimateTokens(system)
	if target <= 0 || conversationTokens(msgs) <= target {
		return msgs
	}
	out := append([]Message(nil), msgs...)
	cut := lastAssistant(out)
	for i := 0; i < cut; i++ {
		if out[i].Role == RoleTool && len(out[i].Content) > compactTrimAt/4 {
			out[i].Content = strings.ToValidUTF8(out[i].Content[:compactTrimAt/4], "") + "…[compacted]"
		}
	}
	for n := len(out) - 1; n > 2 && conversationTokens(out) > target; n -= 2 {
		out = slideWindowN(out, n)
	}
	return out
}

// conversationTokens estimates the tokens msgs take, tool-call inputs included.
func conversationTokens(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		n += estimateTokens(m.Content) + 4 // per-message overhead
		for _, tc := range m.ToolCalls {
			b, _ := json.Marshal(tc.Input)
			n += estimateTokens(tc.Name) + estimateTokens(string(b))
		}
	}
	return n
}
