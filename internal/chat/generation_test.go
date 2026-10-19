package chat

import (
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/models"
)

func newSwitchModel(t *testing.T) (*model, *switchEngine) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	eng := &switchEngine{cfg: &engine.Config{Provider: "ollama", Model: "llama3"}}
	return newModel(eng, func(name string) (engine.Provider, error) { return namedProvider{name}, nil }, "", ""), eng
}

// Cancelling an approval with a message queued starts that message as a real
// generation: status line on, fresh counters (it used to start with waiting
// false, so every token of the queued reply was dropped).
func TestApprovalCancelStartsTheQueuedMessage(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.waiting = true
	m.streamingTokens = 99
	m.pendingQueue = []string{"next question"}
	resp := make(chan approvalResponseMsg, 1)
	m.approvalReq = &approvalRequestMsg{tool: "bash", respCh: resp}
	genBefore := m.gen

	_, cmd := m.approvalCancel()
	if cmd == nil || !m.waiting || len(m.pendingQueue) != 0 {
		t.Fatalf("cmd=%v waiting=%v queue=%v; want the queued message started", cmd != nil, m.waiting, m.pendingQueue)
	}
	if m.streamingTokens != 0 || m.gen <= genBefore {
		t.Fatalf("stale per-turn state: streamingTokens=%d gen=%d (was %d)", m.streamingTokens, m.gen, genBefore)
	}
	if r := <-resp; r.allowed || !r.cancelled {
		t.Fatalf("approval answer = %+v; want cancelled", r)
	}
}

// Only the last turn's diffs are shown, never ones from earlier turns still
// in the history.
func TestTurnDiffs(t *testing.T) {
	msgs := []engine.Message{
		{Role: engine.RoleUser, Content: "first"},
		{Role: engine.RoleTool, ToolCallID: "1", Content: "✓ edited a.go\n-old\n+new"},
		{Role: engine.RoleAssistant, Content: "done"},
		{Role: engine.RoleUser, Content: "second"},
		{Role: engine.RoleTool, ToolCallID: "2", Content: "file contents"},
		{Role: engine.RoleTool, ToolCallID: "3", Content: "✓ wrote b.go (3 bytes)\n+abc"},
		{Role: engine.RoleAssistant, Content: "done again"},
	}
	got := turnDiffs(msgs)
	if len(got) != 1 || got[0] != "+abc" {
		t.Fatalf("turnDiffs = %q; want only this turn's diff", got)
	}
}

// /clear during a generation cancels it first, so its response cannot write
// the old conversation back.
func TestClearCancelsTheGenerationInFlight(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.waiting = true
	m.pendingQueue = []string{"queued"}
	gen := m.gen
	m.handleSlash("/clear")
	if m.waiting || len(m.pendingQueue) != 0 || m.gen == gen {
		t.Fatalf("waiting=%v queue=%v gen %d→%d; want the generation cancelled and the queue dropped", m.waiting, m.pendingQueue, gen, m.gen)
	}
	_, _ = m.handleResponse(chatResponse{text: "late", msgs: []engine.Message{{Role: engine.RoleUser, Content: "old"}}, gen: gen})
	if len(m.history) != 0 {
		t.Fatalf("a stale response restored the history: %+v", m.history)
	}
}

// A provider's model list is resolved once per session, not on every key.
func TestProviderModelsIsResolvedOnce(t *testing.T) {
	m, _ := newSwitchModel(t)
	builds := 0
	m.buildProvider = func(name string) (engine.Provider, error) {
		builds++
		return namedProvider{name}, nil
	}
	first := m.providerModels("anthropic")
	m.providerModels("anthropic")
	if builds != 1 || len(first) == 0 {
		t.Fatalf("builds=%d list=%v; want one resolution per provider", builds, models.IDs(first))
	}
}

// The chat sends the system prompt it was started with — project rules and
// learned facts included — not the bare configured systemPrompt.
func TestChatSendsItsSystemPrompt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	eng := &switchEngine{cfg: &engine.Config{SystemPrompt: "bare"}}
	m := newModel(eng, nil, "assembled prompt", "")
	m.startChat("hi")()
	if eng.lastReq.System != "assembled prompt" {
		t.Fatalf("System = %q; want the prompt chat.Run was given", eng.lastReq.System)
	}
}
