package chat

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/models"
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
	{"/debug", "Toggle debug log level (info ↔ debug)"},
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

// providerModels lists provider's models: its own list from its API (built
// with the configured key, cached for a day), else the built-in suggestions.
//
// The answer is kept for the session: the inline list asks on every
// keystroke, and a provider whose API is down (not cached on disk) would
// otherwise stall the UI on every key.
func (m *model) providerModels(provider string) []models.Model {
	if list, ok := m.modelLists[provider]; ok {
		return list
	}
	var p engine.Provider
	if m.buildProvider != nil {
		p, _ = m.buildProvider(provider)
	}
	list := models.Load().List(context.Background(), provider, p)
	if m.modelLists == nil {
		m.modelLists = make(map[string][]models.Model)
	}
	m.modelLists[provider] = list
	return list
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
	if m.waiting {
		m.pendingQueue = append(m.pendingQueue, input)
		m.msgCount++
		m.say("user", input)
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
	msgs := m.history
	if msgs == nil {
		msgs = []engine.Message{}
	}
	chatCtx := m.chatCtx
	return func() tea.Msg {
		text, updated, err := m.eng.Chat(chatCtx, engine.RunRequest{
			Label:    "chat",
			Prompt:   input,
			Messages: msgs,
			UseTools: true,
			System:   m.system,
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
		return m, nil
	}

	// Save updated conversation history.
	m.history = msg.msgs

	// ── Show response ───────────────────────────────────────────────
	if msg.text != "" {
		m.lines = append(m.lines,
			line{role: "assistant", text: msg.text, ts: now},
		)
	}

	// This turn's trace (beginGeneration started the turn). The tracer keeps
	// every turn, for the session trace and --trace-file.
	if trace := m.eng.Tracer().RenderLastRootString(); trace != "" {
		m.lines = append(m.lines, line{role: "trace", text: trace, ts: now})
	}

	// ── Show this turn's write/edit diffs as code blocks ────────────
	for _, diff := range turnDiffs(m.history) {
		m.lines = append(m.lines, line{role: "codeblock", text: diff, lang: "diff", ts: now})
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
		// Toggle between slog.LevelInfo and slog.LevelDebug.
		level := engine.LogLevel()
		if level == slog.LevelDebug {
			engine.SetLogLevel(slog.LevelInfo)
			m.say("assistant", "debug logging: off")
		} else {
			engine.SetLogLevel(slog.LevelDebug)
			m.say("assistant", "debug logging: on")
		}
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
// project root before saving" trick lives in exactly one place.
func (m *model) persistConfig() {
	if m.configPath == "" {
		return
	}
	if err := app.SaveProjectConfig(m.configPath, m.eng.SaveConfigAt); err != nil {
		slog.Warn("save config", "path", m.configPath, "err", err)
	}
}

// ─── /permit ──────────────────────────────────────────────────────────

// handlePermit processes the /permit command.
func (m *model) handlePermit(parts []string) bool {
	pc := m.eng.PermissionConfig()

	if len(parts) == 1 {
		// Show current permission levels.
		var b strings.Builder
		b.WriteString("### Permission levels\n\n")
		def, tools := pc.Levels()
		b.WriteString(fmt.Sprintf("  default: **%s**\n", def))
		b.WriteString("\n  per-tool overrides:\n")

		// Show tools in sorted order.
		toolNames := make([]string, 0, len(tools))
		for name := range tools {
			toolNames = append(toolNames, name)
		}
		slices.Sort(toolNames)
		if len(toolNames) == 0 {
			b.WriteString("    (none)\n")
		}
		for _, name := range toolNames {
			b.WriteString(fmt.Sprintf("    %s → **%s**\n", name, tools[name]))
		}

		m.say("assistant", b.String())
	} else if len(parts) == 3 {
		tool := parts[1]
		level := engine.ParseLevel(parts[2])
		pc.SetTool(tool, level) // a run may be reading the config right now
		m.say("assistant", fmt.Sprintf("✓ %s → %s", tool, level))
	} else {
		m.say("assistant", "usage: /permit [tool] [allow|ask|deny]")
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
