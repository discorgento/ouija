package chat

import (
	"context"
	"image/color"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/art"
	"github.com/discorgento/ouija/internal/models"
)

// line is one rendered chat entry.
type line struct {
	role   string // "user" | "assistant" | "banner" | "codeblock"
	text   string
	ts     string // timestamp (HH:MM)
	lang   string // language hint for code blocks (e.g. "php", "js")
	cached string // pre-rendered ANSI content (populated lazily, cleared on resize)
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
	welcomeBanner string // cached rendered banner, refreshed on tick

	// Tool progress.
	currentTool string

	// Streaming partial response.
	partialText string

	// Path to the project config file (.ouija/config.json) — set at startup
	// so /model and /provider can persist changes.
	configPath string

	// Render cache — caches are valid as long as the viewport width hasn't changed.
	cacheWidth  int                       // width the lines' cached renders are for
	modelLists  map[string][]models.Model // provider → models, resolved once per session
	lastRebuild time.Time                 // throttles rebuildContent during streaming (~30fps)

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

	// lastWasPress tracks whether the last key event was a KeyPressMsg.
	// Some terminals send both press AND release for Enter; others (Kitty)
	// send only release. This flag prevents double-processing: when a
	// KeyReleaseMsg arrives, if lastWasPress is true (press was already
	// processed), the release is ignored.
	lastWasPress bool

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
	s.Spinner = spinner.Dot
	s.Style = spinnerS

	pi := newPaddedInput()

	ctx, cancel := context.WithCancel(context.Background())
	chatCtx, chatCancel := context.WithCancel(ctx)

	now := time.Now()
	var welcome []line

	vp := viewport.New(viewport.WithWidth(1), viewport.WithHeight(1))
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
	}
	m.welcomeBanner = renderWelcomeBanner(0, m.eng.Config().ProjectRoot)
	m.rebuildContent()
	return m
}

// ─── Colors & Styles ───────────────────────────────────────────────────

var (
	purple = lipgloss.Color("93")  // deep purple — ouija (AI)
	pink   = lipgloss.Color("213") // bright warm pink — user
	violet = lipgloss.Color("99")  // rich violet — headers
	gray   = lipgloss.Color("60")
	white  = lipgloss.Color("#ffffff")

	roleYou = lipgloss.NewStyle().Bold(true).Foreground(pink)
	roleOui = lipgloss.NewStyle().Bold(true).Foreground(purple)

	txtDim = lipgloss.NewStyle().Foreground(gray)

	bannerColors = []color.Color{
		lipgloss.Color("213"),
		lipgloss.Color("141"),
		lipgloss.Color("99"),
		lipgloss.Color("93"),
		lipgloss.Color("140"),
		lipgloss.Color("225"),
	}
	bannerTag    = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("141"))
	bannerAction = lipgloss.NewStyle().Foreground(lipgloss.Color("93"))

	hdrOui  = lipgloss.NewStyle().Bold(true).Foreground(violet)
	hdrMeta = lipgloss.NewStyle().Faint(true).Foreground(white)

	ftDim   = lipgloss.NewStyle().Faint(true).Foreground(white)
	ftScope = lipgloss.NewStyle().Foreground(gray)
	ftVal   = lipgloss.NewStyle().Foreground(violet)

	pipeYou   = lipgloss.NewStyle().SetString("▎").Foreground(pink).String()
	pipeOui   = lipgloss.NewStyle().SetString("▎").Foreground(purple).String()
	pipeTrace = lipgloss.NewStyle().SetString("▎").Foreground(lipgloss.Color("243")).String()
	sepLine   = lipgloss.NewStyle().Faint(true).Foreground(gray)
	spinnerS  = lipgloss.NewStyle().Foreground(purple)

	cardBg    = lipgloss.Color("#1e1930") // lighter dark purple — user card
	cardBgOui = lipgloss.Color("#12101e") // darker dark purple — AI card

	// inputCardFrame is the total horizontal frame of the input card:
	// Padding(1,1) = 2 (left + right).
	inputCardFrame = 2
)

// ouijaArt references the shared figlet rendering.
var ouijaArt = art.OuijaArt
