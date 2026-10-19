package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/registry"
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
	if len(got) != 1 || got[0] != (turnDiff{path: "b.go", diff: "+abc"}) {
		t.Fatalf("turnDiffs = %+v; want only this turn's diff, of b.go", got)
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

// Ctrl+Y and Alt+Y say what happened instead of failing silently — in the
// status bar, not the transcript.
func TestCopyFeedback(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.handleCopyMessage()
	if m.toastText != "nothing to copy yet" || len(m.lines) != 0 {
		t.Fatalf("empty transcript: toast %q, %d lines", m.toastText, len(m.lines))
	}
	m.handleCopyCode()
	if m.toastText != "no code block to copy yet" {
		t.Fatalf("no code block: toast %q", m.toastText)
	}
}

// The last fenced block of the latest reply that has one is what Alt+Y copies.
func TestLastCodeBlock(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.lines = []line{
		{role: "assistant", text: "```go\nold()\n```"},
		{role: "assistant", text: "run:\n```sh\ngo test ./...\n```\nthen\n```sh\nmake lint\n```"},
		{role: "assistant", text: "no code here"},
	}
	if got := m.lastCodeBlock(); got != "make lint" {
		t.Fatalf("lastCodeBlock = %q, want make lint", got)
	}
}

// A failed turn becomes an error card naming the failure and the next step.
func TestErrorCard(t *testing.T) {
	m, _ := newSwitchModel(t)
	err := &engine.OError{Code: engine.ErrCodeAuth, Message: "401 unauthorized"}
	m.handleResponse(chatResponse{err: err, gen: m.gen})
	l := m.lines[len(m.lines)-1]
	if l.role != "error" || l.meta != "the API key was refused" || !strings.Contains(l.hint, "ouija setup") {
		t.Fatalf("error line = %+v", l)
	}
	if out := ansi.Strip(renderErrorBlock(l.meta, l.text, l.hint, 60)); !strings.Contains(out, "✕ the API key was refused") || !strings.Contains(out, "→ run `ouija setup`") {
		t.Fatalf("error card:\n%s", out)
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

// A git-guard notice in the last turn becomes a banner in the transcript.
func TestGitGuardNoticeShown(t *testing.T) {
	m, _ := newSwitchModel(t)
	msgs := []engine.Message{
		{Role: engine.RoleUser, Content: "run tests"},
		{Role: engine.RoleTool, ToolCallID: "1", Content: "ok\n\n" + engine.GitGuardMarker + " this command changed .git/hooks/pre-commit — review"},
		{Role: engine.RoleAssistant, Content: "done"},
	}
	m.handleResponse(chatResponse{text: "done", msgs: msgs, gen: m.gen})
	found := false
	for _, l := range m.lines {
		found = found || (l.role == "banner" && strings.Contains(l.text, ".git/hooks/pre-commit"))
	}
	if !found {
		t.Fatalf("no git-guard banner in %+v", m.lines)
	}
}

// A long reply that ends in the background asks for a desktop notification;
// one in the foreground, or a quick one, does not.
func TestNotifyWhenALongReplyEndsInTheBackground(t *testing.T) {
	m, _ := newSwitchModel(t)
	m.waiting = true
	m.responseStart = time.Now().Add(-notifyAfter - time.Second)
	if _, cmd := m.handleResponse(chatResponse{text: "done", gen: m.gen}); cmd != nil {
		t.Fatal("notified while focused")
	}
	m.focused = false
	m.waiting = true
	m.responseStart = time.Now().Add(-notifyAfter - time.Second)
	_, cmd := m.handleResponse(chatResponse{text: "done", gen: m.gen})
	if cmd == nil {
		t.Fatal("no notification in the background")
	}
	raw, ok := cmd().(tea.RawMsg)
	if !ok || !strings.Contains(fmt.Sprint(raw.Msg), "\x1b]9;ouija: reply ready\x07") {
		t.Fatalf("notification = %#v", cmd())
	}
}

// What the model wrote cannot end the notification's escape sequence early.
func TestNotifyStripsControls(t *testing.T) {
	seq := fmt.Sprint(notifyCmd("a\x07b\x1b]0;x")().(tea.RawMsg).Msg)
	if n := strings.Count(seq, "\x07"); n != 2 { // the two terminators only
		t.Fatalf("%d BELs in %q", n, seq)
	}
	if n := strings.Count(seq, "\x1b"); n != 2 { // the two introducers only
		t.Fatalf("%d ESCs in %q", n, seq)
	}
}

// /undo says what it took back, or why it could not.
func TestUndoCommand(t *testing.T) {
	m, eng := newSwitchModel(t)
	m.handleSlash("/undo")
	if got := m.lines[len(m.lines)-1].text; got != "⚠ nothing to undo" {
		t.Fatalf("empty journal: %q", got)
	}
	eng.undo = func() (string, error) { return "restored a.go", nil }
	m.handleSlash("/undo")
	if got := m.lines[len(m.lines)-1].text; got != "✓ ↶ restored a.go" {
		t.Fatalf("undo: %q", got)
	}
}

// /task runs the totem with the description, draws its runs live and shows
// the plan it returns.
func TestTaskTotemInTheChat(t *testing.T) {
	m, _ := newSwitchModel(t)
	var gotArgs registry.Args
	m.runTotem = func(_ context.Context, name string, args registry.Args, onRun engine.RunCallback) (string, error) {
		gotArgs = args
		return "## Plan\n\n- [ ] step one", nil
	}
	m.input.SetValue("/task add a cache")
	_, cmd := m.handleSend()
	if !m.waiting || m.totem == nil || cmd == nil {
		t.Fatalf("not running: waiting=%v totem=%v", m.waiting, m.totem)
	}
	m.Update(totemRunMsg{ev: engine.RunEvent{Label: "scout"}, gen: m.gen})
	m.Update(totemRunMsg{ev: engine.RunEvent{Label: "scout", Done: true, Duration: time.Second}, gen: m.gen})
	m.Update(totemRunMsg{ev: engine.RunEvent{Label: "p1:risks"}, gen: m.gen})
	board := ansi.Strip(strings.Join(m.renderTotemBoard(80), "\n"))
	if !strings.Contains(board, "séance · task") || !strings.Contains(board, "✓ scout") || !strings.Contains(board, "p1:risks") {
		t.Fatalf("board:\n%s", board)
	}
	text, err := m.runTotem(context.Background(), "task", registry.Args{Task: "add a cache"}, nil)
	m.Update(totemDoneMsg{text: text, err: err, gen: m.gen})
	if gotArgs.Task != "add a cache" {
		t.Fatalf("task = %q", gotArgs.Task)
	}
	last := m.lines[len(m.lines)-1]
	if m.waiting || m.totem != nil || last.role != "assistant" || !strings.Contains(last.text, "step one") || !strings.HasPrefix(last.meta, "task · ") {
		t.Fatalf("after: waiting=%v last=%+v", m.waiting, last)
	}
}

// /theme lists the themes with their colors; choosing one quits to restart
// the chat with it, the conversation resumed (where exec exists).
func TestThemePickerRestarts(t *testing.T) {
	m, _ := newSwitchModel(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	m.handleSlash("/theme")
	if !m.picker.Visible || m.picker.Label != themePickerLabel || !strings.Contains(ansi.Strip(m.picker.Items[1]), "■") {
		t.Fatalf("picker: %v", m.picker.Items)
	}
	m.picker.selected = 2 // dracula
	m.selectCompletion()
	if !canRestart {
		return
	}
	if m.restartTheme != "dracula" || len(m.pendingCmds) == 0 {
		t.Fatalf("restartTheme=%q pending=%d", m.restartTheme, len(m.pendingCmds))
	}
	if got := strings.Join(restartArgs([]string{"ouija"}, true), " "); got != "ouija chat --continue" {
		t.Fatalf("bare ouija restarts as %q", got)
	}
	if got := strings.Join(restartArgs([]string{"ouija", "chat", "-c"}, true), " "); got != "ouija chat -c" {
		t.Fatalf("restart args = %q", got)
	}
	if got := strings.Join(restartArgs([]string{"ouija", "chat"}, false), " "); got != "ouija chat" {
		t.Fatalf("a new conversation resumed: %q", got)
	}
}
