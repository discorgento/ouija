package chat

import (
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
	{"/provider [name]", "Show or change provider (type /provider + Enter to open picker)"},
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
		modelNames := suggestedModels(m.eng.Config(), store.Models(m.eng.Config().Provider))
		items := make([]string, len(modelNames))
		values := make([]string, len(modelNames))
		for i, name := range modelNames {
			values[i] = name
			display := name
			if info, ok := store.ModelInfo(m.eng.Config().Provider, name); ok && info.Label != "" {
				display = info.Label
				if info.ContextWindow > 0 {
					display = fmt.Sprintf("%s  ·  %dk ctx", info.Label, info.ContextWindow/1000)
				}
			}
			items[i] = display
		}
		m.picker.Show(items, values, "Models")
	case "/provider":
		names, labels := store.ProvidersFor(providers.Names())
		m.picker.Show(labels, names, "Providers")
	}
	m.setViewportSize()
	return m, nil
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
		handled := m.handleSlash(input)
		if handled {
			m.input.Reset()
			m.input.SetHeight(1)
			return m, nil
		}
	}

	// ── Exit commands ───────────────────────────────────────────────
	if input == "exit" || input == "quit" {
		m.cancel()
		return m, tea.Quit
	}

	m.input.Reset()
	m.input.SetHeight(1)

	now := time.Now().Format("15:04")

	// ── If already waiting, enqueue instead of sending ──────────────
	if m.waiting {
		m.pendingQueue = append(m.pendingQueue, input)
		m.lines = append(m.lines,
			line{role: "user", text: input, ts: now},
		)
		m.msgCount++
		m.rebuildContent()
		if m.autoScroll {
			m.vp.GotoBottom()
		}
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

	m.lines = append(m.lines,
		line{role: "user", text: input, ts: now},
	)
	m.msgCount++
	m.waiting = true
	m.setViewportSize() // make room for persistent status line
	m.responseStart = time.Now()
	m.partialText = ""
	m.streamingTokens = 0
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}

	return m, tea.Batch(m.startChat(input), m.spinner.Tick)
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
			System:   m.eng.Config().SystemPrompt,
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
		m.lines = append(m.lines,
			line{role: "assistant", text: fmt.Sprintf("error: %v", msg.err), ts: now},
		)
		m.rebuildContent()
		if m.autoScroll {
			m.vp.GotoBottom()
		}
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

	// Show per-turn trace after each response, then reset for the next turn.
	// Resetting is safe: the tracer is not used anywhere else between Chat calls.
	if trace := m.eng.Tracer().RenderLastRootString(); trace != "" {
		m.lines = append(m.lines,
			line{role: "trace", text: trace, ts: now},
		)
		m.eng.Tracer().Reset()
	}

	// ── Show tool diffs as code blocks ───────────────────────────
	// Scan the last few history messages for write/edit_file results
	// containing diffs, and render them as readable diff code blocks.
	start := 0
	if len(m.history) > 20 {
		start = len(m.history) - 20
	}
	for i := start; i < len(m.history); i++ {
		if m.history[i].Role != engine.RoleTool {
			continue
		}
		content := m.history[i].Content
		if !strings.HasPrefix(content, "✓ edited") && !strings.HasPrefix(content, "✓ wrote") {
			continue
		}
		if idx := strings.IndexByte(content, '\n'); idx > 0 {
			diffText := content[idx+1:]
			if strings.TrimSpace(diffText) != "" {
				m.lines = append(m.lines,
					line{role: "codeblock", text: diffText, lang: "diff", ts: now},
				)
			}
		}
	}

	// ── Process pending queue ───────────────────────────────────────
	if len(m.pendingQueue) > 0 {
		next := m.pendingQueue[0]
		m.pendingQueue = m.pendingQueue[1:]
		m.waiting = true
		m.setViewportSize() // make room for persistent status line
		m.responseStart = time.Now()
		m.partialText = ""
		if m.autoScroll {
			m.vp.GotoBottom()
		}
		return m, tea.Batch(m.startChat(next), m.spinner.Tick)
	}

	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
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
		m.lines = nil
		m.history = nil
		m.msgCount = 0
		m.steps = 0
		m.tokens = 0
		m.usd = 0
		m.rejectedTools = make(map[string]bool)
		m.generationCancelled = false
		m.eng.ResetApprovals()
		if m.autoScroll {
			m.vp.GotoBottom()
		}
		m.rebuildContent()
		return true

	case "/help":
		return m.showHelp()

	case "/debug":
		// Toggle between slog.LevelInfo and slog.LevelDebug.
		level := engine.LogLevel()
		if level == slog.LevelDebug {
			engine.SetLogLevel(slog.LevelInfo)
			now := time.Now().Format("15:04")
			m.lines = append(m.lines,
				line{role: "assistant", text: "debug logging: off", ts: now},
			)
		} else {
			engine.SetLogLevel(slog.LevelDebug)
			now := time.Now().Format("15:04")
			m.lines = append(m.lines,
				line{role: "assistant", text: "debug logging: on", ts: now},
			)
		}
		m.rebuildContent()
		return true

	case "/model":
		if len(parts) < 2 {
			now := time.Now().Format("15:04")
			m.lines = append(m.lines,
				line{role: "assistant", text: fmt.Sprintf("current model: %s", m.eng.Config().Model), ts: now},
			)
		} else {
			newModel := parts[1]
			old := m.eng.Config().Model
			m.eng.SetModel(newModel)
			now := time.Now().Format("15:04")
			m.lines = append(m.lines,
				line{role: "assistant", text: fmt.Sprintf("✓ model changed: %s → %s", old, newModel), ts: now},
			)
			m.persistConfig()
		}
		m.rebuildContent()
		if m.autoScroll {
			m.vp.GotoBottom()
		}
		return true

	case "/provider":
		if len(parts) < 2 {
			now := time.Now().Format("15:04")
			m.lines = append(m.lines,
				line{role: "assistant", text: fmt.Sprintf("current provider: %s", m.eng.Config().Provider), ts: now},
			)
		} else {
			newProv := parts[1]
			p, err := m.buildProvider(newProv)
			if err != nil {
				now := time.Now().Format("15:04")
				m.lines = append(m.lines,
					line{role: "assistant", text: fmt.Sprintf("error: %v", err), ts: now},
				)
			} else {
				old := m.eng.Config().Provider
				m.eng.SetProvider(p)
				m.eng.SetModel(models.Load().DefaultModel(newProv))
				now := time.Now().Format("15:04")
				m.lines = append(m.lines,
					line{role: "assistant", text: fmt.Sprintf("✓ provider changed: %s → %s  (model: %s)", old, newProv, m.eng.Config().Model), ts: now},
				)
				m.persistConfig()
			}
		}
		m.rebuildContent()
		if m.autoScroll {
			m.vp.GotoBottom()
		}
		return true

	case "/permit":
		if m.eng == nil {
			now := time.Now().Format("15:04")
			m.lines = append(m.lines,
				line{role: "assistant", text: "engine not initialized", ts: now},
			)
			m.rebuildContent()
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

	now := time.Now().Format("15:04")
	m.lines = append(m.lines,
		line{role: "assistant", text: b.String(), ts: now},
	)
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
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
	now := time.Now().Format("15:04")

	if len(parts) == 1 {
		// Show current permission levels.
		var b strings.Builder
		b.WriteString("### Permission levels\n\n")
		b.WriteString(fmt.Sprintf("  default: **%s**\n", pc.Default))
		b.WriteString("\n  per-tool overrides:\n")

		// Show tools in sorted order.
		toolNames := make([]string, 0, len(pc.Tools))
		for name := range pc.Tools {
			toolNames = append(toolNames, name)
		}
		slices.Sort(toolNames)
		if len(toolNames) == 0 {
			b.WriteString("    (none)\n")
		}
		for _, name := range toolNames {
			b.WriteString(fmt.Sprintf("    %s → **%s**\n", name, pc.Tools[name]))
		}

		m.lines = append(m.lines,
			line{role: "assistant", text: b.String(), ts: now},
		)
	} else if len(parts) == 3 {
		tool := parts[1]
		level := engine.ParseLevel(parts[2])
		pc.Tools[tool] = level
		now := time.Now().Format("15:04")
		m.lines = append(m.lines,
			line{role: "assistant", text: fmt.Sprintf("✓ %s → %s", tool, level), ts: now},
		)
	} else {
		m.lines = append(m.lines,
			line{role: "assistant", text: "usage: /permit [tool] [allow|ask|deny]", ts: now},
		)
	}
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
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
		case "sep":
			b.WriteString("---\n\n")
		case "system":
			b.WriteString(fmt.Sprintf("_system_ (%s): %s\n\n", l.ts, l.text))
		}
	}

	if err := os.WriteFile(filename, []byte(b.String()), 0o644); err != nil {
		now := time.Now().Format("15:04")
		m.lines = append(m.lines,
			line{role: "assistant", text: fmt.Sprintf("error saving: %v", err), ts: now},
		)
	} else {
		now := time.Now().Format("15:04")
		m.lines = append(m.lines,
			line{role: "assistant", text: fmt.Sprintf("✓ saved to `%s`", filename), ts: now},
		)
	}
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
	return true
}
