package chat

import (
	"context"
	"image/color"
	"os"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/discorgento/ouija/internal/theme"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/art"
	"github.com/discorgento/ouija/internal/models"
)

// line is one rendered chat entry.
type line struct {
	role   string             // "user" | "assistant" | "banner" | "trace" | "diff"
	text   string             // the message; the span tree for "trace"; the diff for "diff"
	ts     string             // timestamp (HH:MM)
	meta   string             // assistant: how long the reply took, its cost; error: what failed
	path   string             // diff: the file changed
	hint   string             // error: what to do about it
	tools  engine.TurnSummary // trace, assistant: the turn's tool calls and tokens
	trace  string             // trace, assistant: the turn's span tree (Ctrl+T)
	cached string             // pre-rendered ANSI content (populated lazily, cleared on resize)
}

// model is the Bubble Tea model for the chat TUI.
type model struct {
	// Engine dependencies.
	eng           EngineAPI
	buildProvider ProviderFactory // /provider — injected by Run (a stub in tests)
	system        string
	ctx           context.Context // root context — never replaced, cancelled only on exit
	cancel        context.CancelFunc

	// Per-generation context — replaced on Esc cancel so the next
	// generation starts with a fresh context while m.ctx stays unchanged.
	// The engine callbacks guard on m.ctx (root, stable pointer), which
	// avoids any data race from a replaced context.
	chatCtx    context.Context
	chatCancel context.CancelFunc

	// Chat state.
	history  []engine.Message
	lines    []line
	msgCount int
	waiting  bool

	// UI components.
	spinner spinner.Model
	input   paddedInput
	vp      viewport.Model

	// Terminal dimensions.
	width  int
	height int

	// Footer stats.
	tokens       int
	steps        int
	usd          float64
	startTime    time.Time
	lastDuration time.Duration

	// Streaming token estimate — grows during streaming so the footer shows
	// live output token count before handleResponse finalises the total.
	streamingTokens int
	responseStart   time.Time

	// Smart scrolling.
	autoScroll bool

	// Input history.
	inputHistory []string
	inputHistIdx int
	inputSaved   string

	// Animated welcome — hue cycles via tick, freezes after 20 ticks (10s).
	hueShift      int
	tickCount     int
	welcomeBanner string      // cached rendered banner (card included)
	welcomeKey    string      // what welcomeBanner was rendered for
	welcome       welcomeInfo // git branch and memory, read at startup

	// Turn bookkeeping: the cost spent before the turn (its cost is the
	// difference), the live tools list's state (redrawn when it changes).
	usdAtTurn float64
	liveTools string

	// Ephemeral notice in the status bar (copied, mode switched…); it clears
	// itself when toastID's timer fires.
	toastText string
	toastID   int

	// seenLines is how many transcript lines the developer has seen: lines
	// past it arrived while they were scrolled up ("↓ 3 new").
	seenLines int

	// focused is false while the terminal window is in the background (when
	// the terminal reports focus): a long reply then ends with a notification.
	focused bool
	// selectMode releases the mouse so the terminal can select text (Ctrl+S).
	selectMode bool
	// search is the Ctrl+F transcript search; histSearch the Ctrl+R search
	// of the messages sent (nil when closed).
	search     search
	histSearch *search
	// files is the project's file list for @mentions (nil until first used).
	files []string
	// cmds are the slash commands: the chat's and one per totem.
	cmds []slashCmd
	// runTotem runs a totem for /task (set by Run; nil in tests); totem is
	// the one running, drawn live; totemSink forwards its run events.
	// sessionID names the file the conversation is saved in (sessions.go).
	sessionID string
	// restartTheme is the theme the chat quits to restart with ("" = none).
	restartTheme string

	runTotem  TotemRunner
	totem     *totemRun
	totemSink func(totemRunMsg)

	// Tool progress.
	currentTool string
	showTrace   bool // Ctrl+T: each turn's tools one per line, and the span tree

	// Streaming partial response.
	partialText string

	// Path to the project config file (.ouija/config.json) — set at startup
	// so /model and /provider can persist changes.
	configPath string

	// Render cache — caches are valid as long as the viewport width hasn't changed.
	cacheWidth int                       // width the lines' cached renders are for
	modelLists map[string][]models.Model // provider → models fetched from its API
	// modelFetching / modelFailedAt track background list fetches (see
	// providerModels); pendingCmds are commands queued outside a return
	// value, batched by Update.
	modelFetching map[string]bool
	modelFailedAt map[string]time.Time
	pendingCmds   []tea.Cmd
	lastRebuild   time.Time // throttles the streaming redraw (streamFrame)

	// Streaming redraw (rebuildStreaming): the settled transcript's lines, and
	// whether the viewport holds only their tail; the rendered complete
	// paragraphs of the streaming reply.
	staticLines   []string
	vpTail        bool
	streamHead    string
	streamHeadOut string
	streamWidth   int
	lastToken     time.Time

	// Slash command autocomplete.
	showCmdList bool    // true when showing the slash command list (7 items, small)
	selCmdIdx   int     // selected index in the slash command list
	picker      *Picker // reusable picker for argument-mode (model/provider selection)

	// Queue for messages sent while the model is generating.
	pendingQueue []string

	// Generation counter — incremented on each new Chat() call and on cancel.
	// Used to detect and ignore stale responses from cancelled requests.
	gen int

	// Pending approval request from the engine (non-nil when user must decide).
	approvalReq *approvalRequestMsg
	// approvalQueue holds requests that arrived while one was on screen
	// (parallel tools): each is shown in turn, none answered unseen.
	approvalQueue []approvalRequestMsg
	// debugLog is the file /debug sends the engine's logs to (nil = off).
	debugLog *os.File
	// tokenSink forwards a run's streamed tokens to the event loop (set by
	// Run; nil in tests).
	tokenSink   func(tokenMsg)
	approvalIdx int // which option is selected via arrow keys (0-4)

	// toolCallN is the sequential tool call number within the current generation.
	// Incremented on each approval request so the user sees "call #2", "#3", etc.
	// Reset on new chat or response end.
	toolCallN int

	// rejectedTools tracks which tools the user has rejected in the current
	// generation. Once a tool is rejected, ANY subsequent call to that tool
	// (regardless of arguments) is auto-rejected without prompting.
	rejectedTools map[string]bool

	// generationCancelled is set when the user cancels the current generation
	// (via Cancel button or Esc). Any approvalRequestMsg that arrives after
	// this flag is set is auto-rejected without prompting, because the
	// engine is already processing subsequent tool calls with a cancelled
	// context.
	generationCancelled bool

	// pressed holds the keys whose press was handled and whose release is
	// still due (see the KeyReleaseMsg case).
	pressed map[rune]bool

	// pendingEdit tracks an in-progress external editor session.
	pendingEdit *pendingEditFile
}

// pendingEditFile holds state while the user edits a file externally.
type pendingEditFile struct {
	path    string // real file path
	tmpFile string // temp file path (opened in editor)
	respCh  chan<- approvalResponseMsg
}

// newModel creates the chat model.
func newModel(eng EngineAPI, buildProvider ProviderFactory, system, configPath string) *model {
	s := spinner.New()
	// spinner.Dot's frames end in a space, which pushed every line it
	// starts one column right: the same dots, one cell each.
	s.Spinner = spinner.Spinner{Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, FPS: spinner.Dot.FPS}
	s.Style = spinnerS

	pi := newPaddedInput()

	ctx, cancel := context.WithCancel(context.Background())
	chatCtx, chatCancel := context.WithCancel(ctx)

	now := time.Now()
	var welcome []line

	vp := viewport.New(viewport.WithWidth(1), viewport.WithHeight(1))
	// Search hits are highlighted.
	vp.HighlightStyle = searchHit
	vp.SelectedHighlightStyle = searchHitFocus
	// SoftWrap false: content is already pre-wrapped (glamour / writeWrapped).
	vp.SoftWrap = false

	m := &model{
		eng: eng, buildProvider: buildProvider, system: system, ctx: ctx, cancel: cancel,
		chatCtx: chatCtx, chatCancel: chatCancel,
		spinner: s, input: pi, vp: vp,
		width: 80, height: 24,
		startTime:     now,
		lines:         welcome,
		autoScroll:    true,
		configPath:    configPath,
		inputHistory:  nil,
		inputHistIdx:  -1,
		hueShift:      0,
		rejectedTools: make(map[string]bool),
		picker:        &Picker{},
		focused:       true,
		cmds:          slashCommands(),
	}
	m.welcome = loadWelcomeInfo(m.eng.Config().ProjectRoot)
	linkRoot = m.eng.Config().ProjectRoot
	m.rebuildContent()
	return m
}

// ─── Colors & Styles ───────────────────────────────────────────────────

var (
	purple = theme.Purple // deep purple — ouija (AI)
	pink   = theme.Pink   // bright warm pink — user
	violet = theme.Violet // rich violet — headers
	gray   = theme.Muted
	white  = theme.Text

	roleYou = lipgloss.NewStyle().Bold(true).Foreground(pink)
	roleOui = lipgloss.NewStyle().Bold(true).Foreground(purple)

	txtDim = lipgloss.NewStyle().Foreground(gray)

	bannerTag    = lipgloss.NewStyle().Italic(true).Foreground(theme.Lavender)
	bannerAction = lipgloss.NewStyle().Foreground(theme.Purple)

	pillNew      = lipgloss.NewStyle().Bold(true).Foreground(theme.Surface).Background(theme.Pink)
	pillSelect   = lipgloss.NewStyle().Bold(true).Foreground(theme.Surface).Background(theme.Amber)
	slashTyped   = lipgloss.NewStyle().Bold(true).Foreground(theme.Pink)
	pickerCursor = lipgloss.NewStyle().Bold(true).Foreground(theme.Pink)
	pickerLabel  = lipgloss.NewStyle().Bold(true).Foreground(theme.Lavender)

	hdrOui    = lipgloss.NewStyle().Bold(true).Foreground(violet)
	hdrMeta   = lipgloss.NewStyle().Faint(true).Foreground(white)
	hdrModel  = lipgloss.NewStyle().Bold(true).Foreground(theme.Lavender)
	hdrBranch = lipgloss.NewStyle().Foreground(theme.Pink)

	ftDim   = lipgloss.NewStyle().Faint(true).Foreground(white)
	ftScope = lipgloss.NewStyle().Foreground(gray)
	ftVal   = lipgloss.NewStyle().Foreground(violet)

	pipeYou   = lipgloss.NewStyle().SetString("▎").Foreground(pink).String()
	pipeOui   = lipgloss.NewStyle().SetString("▎").Foreground(purple).String()
	pipeTrace = lipgloss.NewStyle().SetString("▎").Foreground(theme.Gray).String()
	sepLine   = lipgloss.NewStyle().Faint(true).Foreground(gray)
	spinnerS  = lipgloss.NewStyle().Foreground(purple)

	// No background of ouija's own: the terminal's shows through, so the UI
	// matches the terminal theme instead of patching it with two purples.
	cardBg    color.Color = lipgloss.NoColor{}
	cardBgOui color.Color = lipgloss.NoColor{}

	// inputCardFrame is the total horizontal frame of the input card:
	// border (2) + Padding(0,1) (2).
	inputCardFrame = 4
)

// ouijaArt references the shared figlet rendering.
var ouijaArt = art.OuijaArt
