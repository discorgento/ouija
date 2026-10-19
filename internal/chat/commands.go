package chat

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/termsafe"
)

// ─── Slash commands ────────────────────────────────────────────────────

// slashCmd describes a built-in slash command.
type slashCmd struct {
	cmd  string
	desc string
}

// slashCmds is the canonical list of all available slash commands.
var slashCmds = []slashCmd{
	{"/clear", "Clear chat history"},
	{"/save [file]", "Save conversation to markdown"},
	{"/model [name]", "Show or change model (type /model + Enter to open picker)"},
	{"/provider [name] [model]", "Show or change provider and model (Enter opens a picker for each)"},
	{"/permit [tool] allow|ask|deny", "Permission: show, set default, or per-tool level"},
	{"/debug", "Toggle debug logs (written to a file; the path is shown)"},
	{"/help", "Show this help"},
}

// openPicker opens the interactive suggestion list for a slash command.
// It sets the input to the command with trailing space and populates suggestions
// with human-readable labels and parallel suggestionValues for the actual IDs.
func (m *model) openPicker(cmd string) (tea.Model, tea.Cmd) {
	m.input.SetValue(cmd + " ")
	store := models.Load()

	switch cmd {
	case "/model":
		m.showModelPicker(m.eng.Config().Provider)
	case "/provider":
		names, labels := store.ProvidersFor(providers.Names())
		m.picker.Show(labels, names, "Providers")
	}
	m.setViewportSize()
	return m, nil
}

// providerModels lists provider's models without blocking the UI: the list
// fetched from its API (built with the configured key, cached for a day) once
// it arrived, else the built-in suggestions while a fetch runs in the
// background (fetchModels; the result arrives as a modelListMsg). Fetching
// inline froze the whole TUI — Ctrl+C included — on a slow or dead API, and a
// failed fetch was kept as the provider's list for the rest of the session.
func (m *model) providerModels(provider string) []models.Model {
	if list, ok := m.modelLists[provider]; ok {
		return list
	}
	if !m.modelFetching[provider] && time.Since(m.modelFailedAt[provider]) > time.Minute {
		if m.modelFetching == nil {
			m.modelFetching = make(map[string]bool)
		}
		m.modelFetching[provider] = true
		m.pendingCmds = append(m.pendingCmds, m.fetchModels(provider))
	}
	return models.Load().Catalog(provider)
}

// modelListMsg carries a provider's fetched model list (err: it fell back).
type modelListMsg struct {
	provider string
	list     []models.Model
	err      error
}

// fetchModels fetches provider's model list off the event loop.
func (m *model) fetchModels(provider string) tea.Cmd {
	build := m.buildProvider
	return func() tea.Msg {
		var p engine.Provider
		if build != nil {
			p, _ = build(provider)
		}
		list, err := models.Load().Fetch(context.Background(), provider, p)
		return modelListMsg{provider: provider, list: list, err: err}
	}
}

// gotModelList stores a fetched list and refreshes an open picker with it.
func (m *model) gotModelList(msg modelListMsg) {
	delete(m.modelFetching, msg.provider)
	if msg.err != nil {
		if m.modelFailedAt == nil {
			m.modelFailedAt = make(map[string]time.Time)
		}
		m.modelFailedAt[msg.provider] = time.Now() // retried after a minute
		return
	}
	if m.modelLists == nil {
		m.modelLists = make(map[string][]models.Model)
	}
	m.modelLists[msg.provider] = msg.list
	if m.picker.Visible && m.picker.Label == modelPickerLabel {
		m.updateCmdList()
		m.setViewportSize()
	}
}

// modelPickerLabel heads the model list; selectCompletion keys on it.
const modelPickerLabel = "Models"

// showModelPicker lists provider's models — the project's suggestions first,
// then the catalog — with labels and context sizes. It reports whether there
// was anything to list.
func (m *model) showModelPicker(provider string) bool {
	list := m.providerModels(provider)
	byID := make(map[string]models.Model, len(list))
	for _, mod := range list {
		byID[mod.ID] = mod
	}
	modelNames := suggestedModelsFor(m.eng.Config(), provider, models.IDs(list))
	if len(modelNames) == 0 {
		return false
	}
	items := make([]string, len(modelNames))
	values := make([]string, len(modelNames))
	for i, name := range modelNames {
		values[i] = name
		display := name
		if info, ok := byID[name]; ok && info.Label != "" && info.Label != name {
			display = info.Label + "  ·  " + name
		}
		if info, ok := byID[name]; ok && info.ContextWindow > 0 {
			display = fmt.Sprintf("%s  ·  %dk ctx", display, info.ContextWindow/1000)
		}
		items[i] = display
	}
	m.picker.Show(items, values, modelPickerLabel)
	return true
}

// handleSend sends or enqueues the current input as a chat message.
func (m *model) handleSend() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input.Value())
	if input == "" {
		return m, nil
	}

	// ── Slash commands: /model and /provider without args → open picker ──
	if strings.HasPrefix(input, "/") {
		parts := strings.Fields(input)
		if len(parts) == 1 && (parts[0] == "/model" || parts[0] == "/provider") {
			return m.openPicker(parts[0])
		}
		// `/provider <name>`: the developer picks the model too — offer this
		// provider's models; picking one completes `/provider <name> <model>`.
		if len(parts) == 2 && parts[0] == "/provider" {
			m.input.SetValue("/provider " + parts[1] + " ")
			if m.showModelPicker(parts[1]) {
				m.setViewportSize()
				return m, nil
			}
		}
		handled := m.handleSlash(input)
		if handled {
			m.input.Reset()
			m.setViewportSize()
			return m, nil
		}
	}

	// ── Exit commands ───────────────────────────────────────────────
	if input == "exit" || input == "quit" {
		m.cancel()
		return m, tea.Quit
	}

	m.input.Reset()
	m.setViewportSize()

	// ── If already waiting, enqueue instead of sending ──────────────
	// It joins the transcript when it is sent (dispatchQueued), so each reply
	// sits under its own question; until then the queue preview shows it.
	if m.waiting {
		m.pendingQueue = append(m.pendingQueue, input)
		m.setViewportSize() // the preview grows
		return m, nil
	}

	// ── Save to input history ───────────────────────────────────────
	if tail := len(m.inputHistory) - 1; tail < 0 || m.inputHistory[tail] != input {
		m.inputHistory = append(m.inputHistory, input)
		if len(m.inputHistory) > 50 {
			m.inputHistory = m.inputHistory[1:]
		}
	}
	m.inputHistIdx = -1
	m.inputSaved = ""

	m.lines = append(m.lines, line{role: "user", text: input, ts: time.Now().Format("15:04")})
	m.msgCount++
	return m, m.beginGeneration(input) // re-renders with the status line
}

// ─── Response handling ────────────────────────────────────────────────

// startChat starts a new generation with the given input.
// Captures m.history and m.chatCtx at creation time so the goroutine
// always sees the exact state from when startChat was called — not
// whatever m.history/m.chatCtx may have been replaced to by a cancel or
// queued dispatch before the closure executes.
func (m *model) startChat(input string) tea.Cmd {
	m.gen++
	gen := m.gen // capture before goroutine — cancel can't change this local
	m.toolCallN = 0
	m.generationCancelled = false // fresh generation starts with a clean slate
	// A rejection blocks the tool for the generation it was given in, not for
	// the rest of the session.
	m.rejectedTools = make(map[string]bool)
	msgs := m.history
	if msgs == nil {
		msgs = []engine.Message{}
	}
	chatCtx := m.chatCtx
	sink := m.tokenSink
	return func() tea.Msg {
		text, updated, err := m.eng.Chat(chatCtx, engine.RunRequest{
			Label:    "chat",
			Prompt:   input,
			Messages: msgs,
			UseTools: true,
			System:   m.system,
			// Tagged with this generation, so its tokens still in flight
			// after a cancel are dropped instead of joining the next reply.
			OnToken: func(text string) {
				if sink != nil {
					sink(tokenMsg{text: text, gen: gen})
				}
			},
		})
		if err != nil {
			return chatResponse{text: "", msgs: updated, err: err, gen: gen}
		}
		return chatResponse{text: text, msgs: updated, err: nil, gen: gen}
	}
}

// handleResponse processes the engine reply.
func (m *model) handleResponse(msg chatResponse) (tea.Model, tea.Cmd) {
	// Ignore stale responses from cancelled requests.
	if msg.gen < m.gen {
		return m, nil
	}

	m.waiting = false
	m.setViewportSize() // restore viewport height when status line disappears
	m.currentTool = ""
	m.lastDuration = time.Since(m.responseStart)
	tokens, steps, usd := m.eng.Spent()
	m.tokens = tokens
	m.steps = steps
	m.usd = usd
	now := time.Now().Format("15:04")

	// ── Handle error ────────────────────────────────────────────────
	if msg.err != nil {
		m.say("assistant", fmt.Sprintf("error: %v", msg.err))
		return m, m.dispatchQueued() // a failed reply must not strand the queue
	}

	// Save updated conversation history.
	m.history = msg.msgs

	// ── Show response ───────────────────────────────────────────────
	// Model and tool text reaches the screen only without terminal controls
	// (an escape sequence could set the clipboard or redraw the transcript).
	msg.text = termsafe.Clean(msg.text)
	if msg.text != "" {
		m.lines = append(m.lines,
			line{role: "assistant", text: msg.text, ts: now},
		)
	}

	// This turn's trace (beginGeneration started the turn). The tracer keeps
	// every turn, for the session trace and --trace-file.
	if trace := m.eng.Tracer().RenderLastRootString(); trace != "" {
		m.lines = append(m.lines, line{role: "trace", text: termsafe.Clean(trace), ts: now})
	}

	// ── Show this turn's write/edit diffs as code blocks ────────────
	for _, diff := range turnDiffs(m.history) {
		m.lines = append(m.lines, line{role: "codeblock", text: termsafe.Clean(diff), lang: "diff", ts: now})
	}

	// Out of the tool output, where it would be easy to miss: a command
	// changed what git runs on its own.
	for _, notice := range turnGitNotices(m.history) {
		m.lines = append(m.lines, line{role: "banner", text: termsafe.Clean(notice)})
	}

	if cmd := m.dispatchQueued(); cmd != nil {
		return m, cmd // re-renders with the status line back
	}
	m.rebuildContent()
	return m, nil
}

// ─── Slash command dispatch ──────────────────────────────────────────

// handleSlash processes a slash command. Returns true if handled.
func (m *model) handleSlash(input string) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return false
	}
	cmd := parts[0]

	switch cmd {
	case "/clear":
		// A response still in flight would write the old conversation back
		// into m.history: cancel it (and drop the queue) first.
		if m.waiting {
			m.cancelGeneration()
		}
		m.pendingQueue = nil
		m.toolCallN = 0
		m.streamingTokens = 0
		m.lines = nil
		m.history = nil
		m.msgCount = 0
		m.steps = 0
		m.tokens = 0
		m.usd = 0
		m.rejectedTools = make(map[string]bool)
		m.generationCancelled = false
		m.eng.ResetApprovals()
		m.rebuildContent()
		return true

	case "/help":
		return m.showHelp()

	case "/debug":
		m.toggleDebugLog()
		return true

	case "/model":
		if len(parts) < 2 {
			m.say("assistant", fmt.Sprintf("current model: %s", m.eng.Config().Model))
		} else {
			newModel := parts[1]
			old := m.eng.Config().Model
			m.eng.SetModel(newModel)
			m.say("assistant", fmt.Sprintf("✓ model changed: %s → %s", old, newModel))
			m.persistConfig()
		}
		return true

	case "/provider":
		if len(parts) < 2 {
			m.say("assistant", fmt.Sprintf("current provider: %s", m.eng.Config().Provider))
		} else if len(parts) < 3 {
			// The developer picks the model; ouija never chooses one for them.
			// (A provider with catalog models gets a picker before this.)
			m.say("assistant", fmt.Sprintf("usage: /provider %s <model>", parts[1]))
		} else {
			newProv, newModel := parts[1], parts[2]
			p, err := m.buildProvider(newProv)
			if err != nil {
				m.say("assistant", fmt.Sprintf("error: %v", err))
			} else {
				old := m.eng.Config().Provider
				m.eng.SetProvider(p)
				m.eng.SetModel(newModel)
				m.say("assistant", fmt.Sprintf("✓ provider changed: %s → %s  (model: %s)", old, newProv, m.eng.Config().Model))
				m.persistConfig()
			}
		}
		return true

	case "/permit":
		if m.eng == nil {
			m.say("assistant", "engine not initialized")
			return true
		}
		return m.handlePermit(parts)

	case "/save":
		if len(parts) < 2 {
			return m.handleSave("")
		}
		return m.handleSave(parts[1])

	default:
		return false
	}
}

// toggleDebugLog sends the engine's debug logs to a file, or stops. The TUI
// discards them otherwise (stderr would draw over the screen), so the old
// level switch alone showed nothing.
func (m *model) toggleDebugLog() {
	if m.debugLog != nil {
		m.eng.SetLogger(slog.New(slog.DiscardHandler))
		m.debugLog.Close()
		m.debugLog = nil
		m.say("assistant", "debug logging: off")
		return
	}
	path := filepath.Join(os.TempDir(), "ouija-chat-debug.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // prompts and file contents: owner only
	if err != nil {
		m.say("assistant", "debug logging: "+err.Error())
		return
	}
	m.debugLog = f
	m.eng.SetLogger(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})))
	m.say("assistant", "debug logging: on → "+path)
}

// ─── /help ────────────────────────────────────────────────────────────

// showHelp renders a formatted list of slash commands.
func (m *model) showHelp() bool {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# ouija chat — %s\n\n", time.Now().Format("2006-01-02 15:04")))
	b.WriteString("## Slash commands\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---------|-------------|\n")
	for _, sc := range slashCmds {
		b.WriteString(fmt.Sprintf("| `%s` | %s |\n", sc.cmd, sc.desc))
	}
	b.WriteString("\n## Shortcuts\n\n")
	b.WriteString("| Key | Action |\n")
	b.WriteString("|-----|--------|\n")
	b.WriteString("| `↑` | Edit last message (empty input) |\n")
	b.WriteString("| `Alt+↑` / `Alt+↓` | Cycle input history |\n")
	b.WriteString("| `Shift+Enter` / `Alt+Enter` | Insert a newline |\n")
	b.WriteString("| `Tab` | Accept the highlighted suggestion |\n")
	b.WriteString("| `PgUp` / `PgDn` / `Home` / `End` | Scroll the transcript |\n")
	b.WriteString("| `Ctrl+Y` | Copy last assistant response to clipboard |\n")
	b.WriteString("| `Esc` | Cancel generation / clear pending queue |\n")
	b.WriteString("| `Ctrl+C` / `Ctrl+D` | Quit |\n")

	m.say("assistant", b.String())
	return true
}

// persistConfig writes the current config back to the project config file.
// It delegates to the engine (SaveConfigAt) so the "temporarily blank the
// project root before saving" trick lives in exactly one place. A failure is
// shown in the transcript: slog would write over the TUI, and the change
// would silently last only this session.
func (m *model) persistConfig() {
	if m.configPath == "" {
		return
	}
	if err := app.SaveProjectConfig(m.configPath, m.eng.SaveConfigAt); err != nil {
		m.addBanner("⚠ not saved (this session only): " + err.Error())
	}
}

// ─── /permit ──────────────────────────────────────────────────────────

// handlePermit processes the /permit command: show the levels, set the
// default (`/permit ask`), or set one tool's (`/permit bash allow`).
func (m *model) handlePermit(parts []string) bool {
	const usage = "usage: /permit [tool] allow|ask|deny"
	pc := m.eng.PermissionConfig()
	if pc == nil {
		m.say("assistant", "permissions are not configured")
		return true
	}

	switch len(parts) {
	case 1:
		var b strings.Builder
		b.WriteString("### Permission levels\n\n")
		def, tools := pc.Levels()
		fmt.Fprintf(&b, "  default: **%s**\n", def)
		b.WriteString("\n  per-tool overrides:\n")
		if len(tools) == 0 {
			b.WriteString("    (none)\n")
		}
		for _, name := range slices.Sorted(maps.Keys(tools)) {
			fmt.Fprintf(&b, "    %s → **%s**\n", name, tools[name])
		}
		m.say("assistant", b.String())
	case 2, 3:
		// The level is checked here, not by ParseLevel: a typo ("alow")
		// silently became ask.
		word := parts[len(parts)-1]
		if !slices.Contains([]string{"allow", "ask", "deny"}, strings.ToLower(word)) {
			m.say("assistant", fmt.Sprintf("unknown level %q — %s", word, usage))
			return true
		}
		level := engine.ParseLevel(word)
		if len(parts) == 2 {
			pc.SetDefault(level)
			m.say("assistant", fmt.Sprintf("✓ default → %s", level))
		} else {
			pc.SetTool(parts[1], level) // a run may be reading the config right now
			m.say("assistant", fmt.Sprintf("✓ %s → %s", parts[1], level))
		}
	default:
		m.say("assistant", usage)
	}
	return true
}

// ─── /save ───────────────────────────────────────────────────────────

// handleSave saves the chat history to a markdown file.
func (m *model) handleSave(filename string) bool {
	if filename == "" {
		filename = fmt.Sprintf("ouija-chat-%s.md", time.Now().Format("2006-01-02-150405"))
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# ouija chat — %s\n\n", time.Now().Format("2006-01-02 15:04")))
	b.WriteString(fmt.Sprintf("model: %s · provider: %s\n\n", m.eng.Config().Model, m.eng.Config().Provider))
	b.WriteString("---\n\n")

	for _, l := range m.lines {
		switch l.role {
		case "user":
			b.WriteString(fmt.Sprintf("**user** (%s): %s\n\n", l.ts, l.text))
		case "assistant":
			b.WriteString(fmt.Sprintf("**ouija** (%s): %s\n\n", l.ts, l.text))
		}
	}

	if err := os.WriteFile(filename, []byte(b.String()), 0o644); err != nil {
		m.say("assistant", fmt.Sprintf("error saving: %v", err))
	} else {
		m.say("assistant", fmt.Sprintf("✓ saved to `%s`", filename))
	}
	return true
}
