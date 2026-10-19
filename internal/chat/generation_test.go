package chat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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

// A provider's model list is fetched once, off the event loop: the catalog
// shows at once, the fetched list replaces it, and later keystrokes reuse it.
func TestProviderModelsFetchedInBackground(t *testing.T) {
	m, _ := newSwitchModel(t)
	builds := 0
	m.buildProvider = func(name string) (engine.Provider, error) {
		builds++
		return namedProvider{name}, nil
	}
	if first := m.providerModels("anthropic"); len(first) == 0 || builds != 0 || len(m.pendingCmds) != 1 {
		t.Fatalf("first call: list=%d builds=%d cmds=%d; want the catalog now, one fetch queued", len(first), builds, len(m.pendingCmds))
	}
	m.providerModels("anthropic") // a fetch is in flight: not queued again
	if len(m.pendingCmds) != 1 {
		t.Fatalf("%d fetches queued, want 1", len(m.pendingCmds))
	}
	if msg, ok := m.pendingCmds[0]().(modelListMsg); !ok || msg.provider != "anthropic" || builds != 1 {
		t.Fatalf("the fetch command returned %T, builds=%d", msg, builds)
	}
	m.pendingCmds = nil
	m.Update(modelListMsg{provider: "anthropic", list: []models.Model{{ID: "fetched"}}})
	if got := m.providerModels("anthropic"); len(got) != 1 || got[0].ID != "fetched" || len(m.pendingCmds) != 0 {
		t.Fatalf("after the fetch: %v, cmds=%d", models.IDs(got), len(m.pendingCmds))
	}
	// A failed fetch is not kept as the list, and is retried only later.
	m.Update(modelListMsg{provider: "openai", err: errors.New("down")})
	m.providerModels("openai")
	if _, kept := m.modelLists["openai"]; kept || len(m.pendingCmds) != 0 {
		t.Fatalf("failed fetch kept=%v or retried at once (cmds=%d)", kept, len(m.pendingCmds))
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

// type sets the input as if typed and refreshes the suggestion lists.
func typeInput(m *model, s string) {
	m.input.SetValue(s)
	m.updateCmdList()
}

// A command with arguments is sent as typed: the list used to fall back to
// its first item, so Enter on `/save notes.md` ran /clear.
func TestSlashWithArgsIsSentAsTyped(t *testing.T) {
	m, _ := newSwitchModel(t)
	t.Chdir(t.TempDir())
	m.lines = []line{{role: "user", text: "keep me"}}
	typeInput(m, "/save notes.md")
	if m.showCmdList || m.picker.Visible {
		t.Fatalf("a list is open for a complete command: cmd=%v picker=%v", m.showCmdList, m.picker.Visible)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.lines) == 0 || m.lines[0].text != "keep me" {
		t.Fatal("the conversation was cleared")
	}
	if _, err := os.Stat("notes.md"); err != nil {
		t.Fatalf("/save notes.md did not save: %v", err)
	}
}

// The command list completes a partial name, and hides when nothing matches.
func TestSlashCommandList(t *testing.T) {
	m, _ := newSwitchModel(t)
	typeInput(m, "/he")
	if !m.showCmdList || slashCmds[m.selCmdIdx].cmd != "/help" {
		t.Fatalf("/he: list=%v sel=%d", m.showCmdList, m.selCmdIdx)
	}
	typeInput(m, "/nope")
	if m.showCmdList {
		t.Fatal("/nope: the list stayed open with no match")
	}
}

// /provider's second word lists that provider's models, and a model the list
// does not know is kept as typed.
func TestProviderModelStep(t *testing.T) {
	m, eng := newSwitchModel(t)
	typeInput(m, "/provider openai ")
	if !m.picker.Visible || m.picker.Label != modelPickerLabel {
		t.Fatalf("after `/provider openai `: picker=%v label=%q, want the model list", m.picker.Visible, m.picker.Label)
	}
	typeInput(m, "/provider openai zzz-custom")
	if m.picker.Visible {
		t.Fatal("an unknown model kept the picker open (Enter would replace it)")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if eng.provider != "openai" || eng.model != "zzz-custom" {
		t.Fatalf("switched to %s/%s, want openai/zzz-custom", eng.provider, eng.model)
	}
}

// Esc closes an open list without cancelling the reply in flight.
func TestEscClosesListFirst(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.waiting = true
	typeInput(m, "/mo")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.showCmdList || !m.waiting {
		t.Fatalf("Esc: list=%v waiting=%v; want the list closed and the reply running", m.showCmdList, m.waiting)
	}
}

// A rejection lasts for its generation only.
func TestRejectionResetsPerGeneration(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.rejectedTools["bash"] = true
	m.startChat("next")
	if m.rejectedTools["bash"] {
		t.Fatal("a rejected tool stayed blocked in the next generation")
	}
}

// A queued message joins the transcript when it is sent — also after a
// failed reply, which used to strand the queue.
func TestQueueAfterErrorAndTranscriptOrder(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.waiting = true
	m.input.SetValue("Q2")
	m.handleSend()
	for _, l := range m.lines {
		if l.text == "Q2" {
			t.Fatal("a queued message entered the transcript before its turn")
		}
	}
	_, cmd := m.handleResponse(chatResponse{err: errors.New("boom"), gen: m.gen})
	if cmd == nil || !m.waiting || len(m.pendingQueue) != 0 {
		t.Fatalf("after a failed reply: cmd=%v waiting=%v queue=%v; want Q2 started", cmd != nil, m.waiting, m.pendingQueue)
	}
	last := m.lines[len(m.lines)-1]
	if last.role != "user" || last.text != "Q2" {
		t.Fatalf("last line = %+v; want Q2, after the error", last)
	}
}

func TestPermit(t *testing.T) {
	m, eng := newSwitchModel(t)
	m.handlePermit([]string{"/permit"}) // nil config: a message, no panic
	eng.perm = engine.NewPermissionConfig(engine.Ask)

	m.handlePermit([]string{"/permit", "deny"})
	if def, _ := eng.perm.Levels(); def != engine.Deny {
		t.Fatalf("/permit deny: default = %v", def)
	}
	m.handlePermit([]string{"/permit", "bash", "allow"})
	if _, tools := eng.perm.Levels(); tools["bash"] != engine.Allow {
		t.Fatalf("/permit bash allow: %v", tools)
	}
	m.handlePermit([]string{"/permit", "bash", "alow"})
	if _, tools := eng.perm.Levels(); tools["bash"] != engine.Allow {
		t.Fatalf("a typo changed the level: %v", tools)
	}
	if last := m.lines[len(m.lines)-1].text; !strings.Contains(last, "unknown level") {
		t.Fatalf("typo message = %q", last)
	}
}

// Edit in $EDITOR goes through the sandbox: a path out of the project is
// refused (the prompt stays up) instead of being written by the editor.
func TestApprovalEditStaysInSandbox(t *testing.T) {
	m, eng := newSwitchModel(t)
	root := t.TempDir()
	eng.cfg.ProjectRoot = root
	resp := make(chan approvalResponseMsg, 1)
	m.approvalReq = &approvalRequestMsg{tool: "edit_file", respCh: resp,
		input: map[string]any{"path": "../outside.txt", "oldString": "a", "newString": "b"}}
	m.handleApprovalEdit()
	if m.pendingEdit != nil || m.approvalReq == nil {
		t.Fatal("an edit outside the project was opened in the editor")
	}
}

// After the editor: the edit is written and the tool skipped; a failed write
// is reported as not applied.
func TestEditorDone(t *testing.T) {
	m, _ := newSwitchModel(t)
	dir := t.TempDir()
	target, tmp := filepath.Join(dir, "a.txt"), filepath.Join(dir, "tmp")
	os.WriteFile(tmp, []byte("edited"), 0o600)
	resp := make(chan approvalResponseMsg, 1)
	m.pendingEdit = &pendingEditFile{path: target, tmpFile: tmp, respCh: resp}
	m.handleEditorDone(editorDoneMsg{})
	if r := <-resp; !r.allowed || !r.skipExecution {
		t.Fatalf("answer = %+v", r)
	}
	if b, _ := os.ReadFile(target); string(b) != "edited" {
		t.Fatalf("target = %q", b)
	}

	os.WriteFile(tmp, []byte("x"), 0o600)
	m.pendingEdit = &pendingEditFile{path: filepath.Join(dir, "missing", "a.txt"), tmpFile: tmp, respCh: resp}
	m.handleEditorDone(editorDoneMsg{})
	if r := <-resp; r.allowed || r.skipExecution {
		t.Fatalf("failed write answered %+v; want not applied", r)
	}
}

// /debug writes the engine's logs to a file and says where; again turns it off.
func TestDebugToggle(t *testing.T) {
	m, _ := newSwitchModel(t)
	t.Setenv("TMPDIR", t.TempDir())
	m.handleSlash("/debug")
	if m.debugLog == nil || !strings.Contains(m.lines[len(m.lines)-1].text, "ouija-chat-debug.log") {
		t.Fatalf("on: file=%v msg=%q", m.debugLog, m.lines[len(m.lines)-1].text)
	}
	m.handleSlash("/debug")
	if m.debugLog != nil {
		t.Fatal("off: the log file stayed open")
	}
}

// Ctrl+Y says what happened instead of failing silently.
func TestCopyFeedback(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.handleCopyMessage()
	if got := m.lines[len(m.lines)-1].text; got != "nothing to copy yet" {
		t.Fatalf("empty transcript: %q", got)
	}
}

// Terminal controls in a reply never reach the screen.
func TestReplyIsTerminalSafe(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.handleResponse(chatResponse{text: "hi\x1b]52;c;ZXZpbA==\x07", gen: m.gen})
	for _, l := range m.lines {
		if strings.ContainsRune(l.text, 0x1b) {
			t.Fatalf("escape sequence kept in %q", l.text)
		}
	}
}
