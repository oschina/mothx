package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/stopwatch"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/startvibecoding/GoStreamingMarkdown/gsm"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	browserfeature "github.com/startvibecoding/mothx/internal/browser"
	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/provider"
	serviceruntime "github.com/startvibecoding/mothx/internal/serve/runtime"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skillhub"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/stats"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/tui/components/editor"
	"github.com/startvibecoding/mothx/internal/tui/components/suggest"
	"github.com/startvibecoding/mothx/internal/tui/i18n"
	"github.com/startvibecoding/mothx/internal/ua"
)

var (
	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("86")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15"))

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			Italic(true)

	toolModalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	thinkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			BorderTop(true).
			BorderForeground(lipgloss.Color("240"))

	footerSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("237"))

	footerTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	pasteMarkerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("214")).
				Bold(true)

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)
)

// InputEvent represents a queued input event
type InputEvent struct {
	msg     tea.Msg
	arrived time.Time
}

// queuedPrompt is a user submission received while the current foreground
// execution still owns the session. It intentionally retains Runtime-prepared
// attachments so delayed submission follows the same input contract.
type queuedPrompt struct {
	text      string
	resources []agentruntime.PreparedInput
}

type queuedPromptStartMsg struct{}

type toolResultStatus string

const (
	toolResultStatusRunning     toolResultStatus = "running"
	toolResultStatusCompleted   toolResultStatus = "completed"
	toolResultStatusInterrupted toolResultStatus = "interrupted"
)

// toolResult stores tool result information
type toolResult struct {
	toolCallID     string // Unique tool call ID for precise matching
	toolName       string
	toolArgs       map[string]any // Tool call arguments
	status         toolResultStatus
	summary        string // Short summary for collapsed view
	fullContent    string // Full content for expanded view
	diff           *tools.FileDiff
	toolError      string // Stable presentation error from the tool execution
	executionState string
	msgIndex       int // Index in a.messages where this tool message lives
	expanded       string
}

// App is the main TUI application.
type App struct {
	provider            provider.Provider
	backgroundSubmitter serviceruntime.BackgroundSubmitter
	backgroundRuns      map[string]int // run ID -> transcript message boundary
	providerName        string
	model               *provider.Model
	settings            *config.Settings
	translator          i18n.Translator
	tuiLangConfigured   i18n.ConfiguredLanguage
	tuiLangOffset       string
	tuiLangScope        string
	allow               *config.AllowConfig
	session             *session.Manager
	runtime             *agentruntime.SessionRuntime
	run                 *tuiRun
	registry            *tools.Registry
	sandboxInfo         string
	cwd                 string
	mode                string
	extraContext        string
	ruleContent         string
	skillsMgr           *skills.Manager
	skillHub            *skillhub.Service

	// Skills state: base extraContext (without skills) and active skill names
	baseExtraContext string            // extraContext without skill content
	activeSkills     map[string]string // skill name -> skill context string

	// UI Components
	input      editor.Model
	authInput  editor.Model
	envInput   editor.Model
	modelInput editor.Model
	suggest    suggest.Model
	timer      stopwatch.Model

	// State
	messages               []string
	hiddenEventIdx         map[int]bool // full-only event rows hidden by compact mode
	auth                   authDialogState
	envDialog              envDialogState
	modelDialog            modelDialogState
	defaultModelDialog     defaultModelDialogState
	sessionsDialog         sessionsDialogState
	toolResults            []toolResult // Store tool results for expansion
	isThinking             bool
	manualCompactionActive bool
	pendingAbortReason     string
	queuedPrompts          []queuedPrompt
	// runTerminalHandled marks that the current run's canonical terminal event
	// (EventRunFinished) was processed, so legacy EventDone/EventError that
	// follow it are not handled twice.
	runTerminalHandled bool
	agent              *agent.Agent
	eventCh            <-chan agent.Event
	width              int
	height             int
	ready              bool

	// Paste markers storage
	pasteCounter int
	pastes       map[int]string

	clipboardImageSaver   ClipboardImageSaver
	clipboardImageSource  ClipboardImageSource
	fileOpener            FileOpener
	lastPastedImagePath   string
	pendingInputResources []agentruntime.PreparedInput

	// Input queue for batching
	inputQueue           []InputEvent
	inputQueueMu         sync.Mutex
	inputQueueTickActive bool
	lastInputTime        time.Time
	lastTextInputTime    time.Time
	inputBatchSize       int
	inputDelay           time.Duration

	// Completed transcript blocks are printed above the live Bubble Tea view so
	// the terminal owns scrollback, mouse scrolling, and text selection.
	liveContent string
	printMu     sync.Mutex
	printCond   *sync.Cond
	printQueue  []string
	printOnce   sync.Once
	printStop   bool

	// Initial message to display
	initialMessage string

	// Auto-open auth dialog on startup (e.g. when settings.json was just created)
	autoOpenAuthDialog bool

	// Tool output modal
	toolModalOpen         bool
	toolModalOffset       int
	toolModalPinnedBottom bool
	toolModalActive       int
	toolModalCacheValid   bool
	toolModalCacheActive  int
	toolModalCacheVersion int
	toolModalCacheWidth   int
	toolModalVersion      int
	toolModalCacheLines   []string

	// SkillHub marketplace overlay.
	skillHubOpen           bool
	skillHubMarket         skillhub.Market
	skillHubView           skillHubView
	skillHubScope          string
	skillHubQuery          string
	skillHubSearchFocused  bool
	skillHubResults        []skillhub.SkillSummary
	skillHubSelected       int
	skillHubPage           int
	skillHubTotal          int64
	skillHubCursor         string
	skillHubNextCursor     string
	skillHubCursorHistory  []string
	skillHubCategories     []skillhub.Category
	skillHubCategory       string
	skillHubSort           string
	skillHubDetail         *skillhub.SkillDetail
	skillHubDetailExpanded bool
	skillHubDetailScroll   int
	skillHubLoading        bool
	skillHubInstalling     bool
	skillHubMessage        string

	// /btw side-question floating layer
	btwOpen       bool
	btwActive     bool   // a /btw sub-agent is currently running
	btwQuestion   string // the side question being answered
	btwAnswer     string // accumulated answer text (raw)
	btwThink      string // latest think summary (raw, tail-truncated)
	btwErr        error  // error if the side query failed
	btwScroll     int    // scroll offset within the overlay
	btwEventCh    <-chan agent.Event
	btwCancel     context.CancelFunc
	btwGeneration uint64

	// /btw streaming render optimization (mirrors main-agent assistant rendering):
	// accumulate deltas with a Builder, render markdown via a dedicated streaming
	// renderer, and cache the wrapped output so View does not re-wrap every frame.
	btwAnswerBuilder *strings.Builder
	btwThinkBuilder  *strings.Builder
	btwRenderer      *gsm.Stream
	btwRendered      string // cached rendered+wrapped answer body
	btwRenderWidth   int    // width the cache was built for
	btwAnswerDirty   bool   // answer changed since last render

	// /stats server and overlay state.
	statsServer        *stats.Server
	statsServerDB      *stats.DB
	statsServerURL     string
	statsOverlayOpen   bool
	statsOverlayLines  []string
	statsOverlayScroll int

	// Compact event display mode. The default transcript is intentionally
	// concise; Ctrl+G switches to the complete event projection.
	compactMode bool

	// Context usage
	contextUsage *ctxpkg.ContextUsage
	currentPlan  *tools.TaskPlan

	// Cache usage tracking (cumulative)
	totalInputTokens int
	totalCacheRead   int
	totalCacheWrite  int
	totalCostUSD     float64
	latestUsage      *provider.Usage

	// Enable Supervisor Mode (ESM)
	esmStore           *esm.Store
	esmStoreDir        string
	esmToolsRegistered bool
	esmFooter          string
	esmMu              sync.Mutex
	esmRunSeq          int64
	esmSteering        *esm.SteeringSource
	esmRunTracked      bool
	esmRunSessionID    string
	esmRunID           string
	esmActiveAgentID   agentpkg.AgentID
	// esmRunCancel cancels the supervisor-run context. Background-context
	// supervisors kept spawning roles after an abort because nothing owned their
	// lifetime; the App now does.
	esmRunCancel      context.CancelFunc
	esmRunTokens      int64
	esmSupervisorRun  bool
	esmRoleRunner     esmRoleRunner
	esmPanelOpen      bool
	esmPanelScroll    int
	esmPanelObjective *esm.Objective
	esmPanelErr       error

	// Spinner state
	spinnerIndex int
	requestStart time.Time
	lastDuration time.Duration

	// Session history
	sessionMu          sync.Mutex
	historyLoaded      bool
	agentHistoryLoaded bool

	// Prompt input history
	inputHistory         []string
	inputHistoryBrowsing bool
	inputHistoryIndex    int
	inputHistoryDraft    string

	// Render throttling
	lastRender     time.Time
	renderPending  bool
	renderMu       sync.Mutex
	renderInterval time.Duration

	// Approval state
	waitingForApproval bool
	pendingApprovalID  string
	currentApproval    pendingApproval
	currentApprovalIdx int
	approvalCursor     int
	approvalQueue      []pendingApproval

	// Question state
	waitingForQuestion bool
	pendingQuestionID  string
	currentQuestion    pendingQuestion
	questionQueue      []pendingQuestion

	// Multi-agent / delegate state
	multiAgent          bool
	delegateMode        bool
	workflows           bool
	browserEnabled      bool
	browserSkillInBase  bool
	browserSkillContext string
	activeAgent         agentpkg.AgentID
	agentMgr            *agent.AgentManager
	agentActivities     map[agentpkg.AgentID]*agentActivity
	agentActivityOrder  []agentpkg.AgentID

	// Cron state
	cronStore cron.CronStore
	scheduler *cron.Scheduler

	// Current streaming message indices (-1 = none)
	currentAssistantIdx int
	currentThinkIdx     int
	printedMessageIdx   map[int]bool
	thinkRaw            map[int]string // message index -> raw thinking content

	// Markdown rendering for assistant messages
	mdRenderer        *gsm.Stream
	assistantRaw      map[int]string // message index -> raw markdown content
	assistantBuilders map[int]*strings.Builder
	assistantRendered map[int]string // message index -> gsm-rendered content
	assistantDirty    map[int]bool   // message index -> needs re-render
	thinkBuilders     map[int]*strings.Builder

	// Bubble Tea program used to marshal deferred renders back onto the UI goroutine.
	program *tea.Program

	// External TUI-only status line state.
	statusLineOutput       string
	statusLineLastError    string
	statusLineInFlight     bool
	statusLineLastSuccess  string
	statusLineLastAttempt  string
	statusLinePending      *statusLineRequest
	statusLineIntervalInit bool
	statusLineGeneration   uint64
	statusLineCancel       context.CancelFunc

	// reloadRequested is set by /reload to ask main to re-exec the process after
	// the TUI exits, giving a clean "fresh process" with a brand-new session.
	reloadRequested bool
}

// ReloadRequested reports whether the user asked to reload via /reload.
func (a *App) ReloadRequested() bool { return a.reloadRequested }

// SetBackgroundSubmitter connects the TUI to the serve-owned durable
// Responses coordinator. Standalone TUI runs leave it unset and use the
// provider's synchronous availability fallback.
func (a *App) SetBackgroundSubmitter(submitter serviceruntime.BackgroundSubmitter) {
	a.backgroundSubmitter = submitter
	if submitter != nil {
		a.trackExistingBackgroundRuns()
		if a.program != nil && len(a.backgroundRuns) > 0 {
			a.program.Send(backgroundRunPollMsg(time.Now()))
		}
	}
}

// SetBrowserEnabled records browser-tool startup state. skillInBase is true
// when the browser skill context has already been included in baseExtraContext.
func (a *App) SetBrowserEnabled(enabled bool, skillInBase bool) {
	a.browserEnabled = enabled
	a.browserSkillInBase = skillInBase
	if enabled && a.skillsMgr != nil {
		a.browserSkillContext = a.skillsMgr.BuildSkillContext(browserfeature.SkillName)
	}
	a.markBuiltinActiveSkills()
}

// pendingApproval holds a queued approval request.
type pendingApproval struct {
	agentID    agentpkg.AgentID
	approvalID string
	toolName   string
	args       map[string]any
}

// pendingQuestion holds a queued question request.
type pendingQuestion struct {
	questionID string
	question   string
	options    []string
	context    string
}

type statusLineRequest struct {
	hash       string
	force      bool
	payload    []byte
	width      int
	generation uint64
	sessionID  string
	workingDir string
}

// NewApp creates a new TUI application.
func NewApp(p provider.Provider, model *provider.Model, settings *config.Settings, sess *session.Manager, registry *tools.Registry, sandboxInfo string, extraContext string, ruleContent string, skillsMgr *skills.Manager, initialMode string, multiAgent bool, delegateMode bool, agentMgr *agent.AgentManager, cronStore cron.CronStore, scheduler *cron.Scheduler) *App {
	return NewAppWithWorkflows(p, model, settings, sess, registry, sandboxInfo, extraContext, ruleContent, skillsMgr, initialMode, multiAgent, delegateMode, false, agentMgr, cronStore, scheduler)
}

func NewAppWithWorkflows(p provider.Provider, model *provider.Model, settings *config.Settings, sess *session.Manager, registry *tools.Registry, sandboxInfo string, extraContext string, ruleContent string, skillsMgr *skills.Manager, initialMode string, multiAgent bool, delegateMode bool, workflows bool, agentMgr *agent.AgentManager, cronStore cron.CronStore, scheduler *cron.Scheduler) *App {
	return NewAppWithWorkflowsAndAllow(p, model, settings, sess, registry, sandboxInfo, extraContext, ruleContent, skillsMgr, initialMode, multiAgent, delegateMode, workflows, agentMgr, cronStore, scheduler, "", config.LoadAllow())
}

// NewAppWithWorkflowsAndAllow creates a new TUI application. providerKey is the
// user-configured settings.json provider key (e.g. "xiaomi", "doubao"); when
// empty the resolved vendor name from p.Name() is used as a fallback.
func NewAppWithWorkflowsAndAllow(p provider.Provider, model *provider.Model, settings *config.Settings, sess *session.Manager, registry *tools.Registry, sandboxInfo string, extraContext string, ruleContent string, skillsMgr *skills.Manager, initialMode string, multiAgent bool, delegateMode bool, workflows bool, agentMgr *agent.AgentManager, cronStore cron.CronStore, scheduler *cron.Scheduler, providerKey string, allow *config.AllowConfig) *App {
	if settings == nil {
		settings = config.DefaultSettings()
	}
	configured, valid := i18n.ParseConfigured(settings.TUILang)
	if !valid {
		fmt.Fprintf(os.Stderr, "Warning: invalid tuilang %q; using auto\n", settings.TUILang)
	}
	now := time.Now()
	translator := i18n.New(i18n.Resolve(configured, now, time.Local))
	input := editor.New(80).SetPlaceholder(translator.Text(i18n.MsgInputPlaceholder)).SetMaxLines(5)

	// Determine initial mode: use provided mode, fall back to settings default
	mode := initialMode
	if mode == "" {
		mode = settings.DefaultMode
	}
	if mode == "" {
		mode = "yolo"
	}
	if allow == nil {
		allow = config.LoadAllow()
	}

	providerName := providerKey
	if providerName == "" {
		providerName = safeProviderName(p)
	}
	globalSkillsDir := ""
	officialSkillHandles := []string{config.DefaultSkillHubOfficialHandle}
	if settings != nil {
		globalSkillsDir = settings.GetGlobalSkillsDir()
		if len(settings.SkillHub.OfficialHandles) > 0 {
			officialSkillHandles = settings.SkillHub.OfficialHandles
		}
	}

	app := &App{
		provider:          p,
		providerName:      providerName,
		model:             model,
		settings:          settings,
		translator:        translator,
		tuiLangConfigured: configured,
		tuiLangOffset:     i18n.UTCOffset(now, time.Local),
		tuiLangScope:      configuredTUILangScope(),
		allow:             allow,
		session:           sess,
		registry:          registry,
		sandboxInfo:       sandboxInfo,
		cwd:               currentWorkingDir(sess),
		mode:              mode,
		extraContext:      extraContext,
		ruleContent:       ruleContent,
		baseExtraContext:  extraContext,
		activeSkills:      make(map[string]string),
		skillsMgr:         skillsMgr,
		skillHub: skillhub.NewServiceForWorkDir(globalSkillsDir, currentWorkingDir(sess), officialSkillHandles, skillhub.ClientsForSettings(func() config.SkillHubSettings {
			if settings == nil {
				return config.SkillHubSettings{}
			}
			return settings.SkillHub
		}())...),
		input:               input,
		authInput:           editor.New(80).SetPlaceholder("").SetMaxLines(3),
		suggest:             suggest.New(80).SetItems(commandSuggestionItems(translator)),
		timer:               stopwatch.NewWithInterval(time.Second),
		printQueue:          make([]string, 0, 256),
		pastes:              make(map[int]string),
		fileOpener:          systemFileOpener{},
		inputQueue:          make([]InputEvent, 0, 100),
		inputBatchSize:      10,
		inputDelay:          16 * time.Millisecond, // ~60fps
		renderInterval:      16 * time.Millisecond, // ~60fps
		currentAssistantIdx: -1,
		currentThinkIdx:     -1,
		currentApprovalIdx:  -1,
		compactMode:         true,
		printedMessageIdx:   make(map[int]bool),
		hiddenEventIdx:      make(map[int]bool),
		thinkRaw:            make(map[int]string),
		thinkBuilders:       make(map[int]*strings.Builder),
		assistantRaw:        make(map[int]string),
		assistantBuilders:   make(map[int]*strings.Builder),
		assistantRendered:   make(map[int]string),
		assistantDirty:      make(map[int]bool),
		multiAgent:          multiAgent,
		delegateMode:        delegateMode,
		workflows:           workflows,
		agentMgr:            agentMgr,
		agentActivities:     make(map[agentpkg.AgentID]*agentActivity),
		cronStore:           cronStore,
		scheduler:           scheduler,
	}
	app.markBuiltinActiveSkills()

	app.configureMarkdownRenderer()

	return app
}

// SetInitialMessage sets an initial message to display when the TUI starts.
func (a *App) SetInitialMessage(msg string) {
	a.initialMessage = msg
}

// SetAutoOpenAuthDialog tells the TUI to open the /auth dialog automatically
// on startup. This is used when settings.json was just created.
func (a *App) SetAutoOpenAuthDialog(v bool) {
	a.autoOpenAuthDialog = v
}

// SetProgram stores the Bubble Tea program used for deferred UI updates.
func (a *App) SetProgram(p *tea.Program) {
	a.program = p
	if a.printCond == nil {
		a.printCond = sync.NewCond(&a.printMu)
	}
	a.printOnce.Do(func() {
		go func() {
			for {
				a.printMu.Lock()
				for len(a.printQueue) == 0 && !a.printStop {
					a.printCond.Wait()
				}
				if a.printStop && len(a.printQueue) == 0 {
					a.printMu.Unlock()
					return
				}
				line := a.printQueue[0]
				a.printQueue[0] = ""
				a.printQueue = a.printQueue[1:]
				program := a.program
				a.printMu.Unlock()

				if program != nil {
					// Print directly to native terminal scrollback; do not route
					// completed transcript output back through the update loop.
					program.Println(line)
				}
			}
		}()
	})
}

// stopPrintLoop asks the deferred print goroutine to drain and exit. Any
// already-queued transcript lines are still flushed before the goroutine
// returns; callers should use it when the Bubble Tea program is about to quit
// so the background loop does not outlive the process teardown. Note that
// printMessageOnce calls made after the pump exits are queued but never
// printed (and their index is still marked done), so only quit/reload paths
// may stop the loop.
func (a *App) stopPrintLoop() {
	a.printMu.Lock()
	a.printStop = true
	if a.printCond != nil {
		a.printCond.Broadcast()
	}
	a.printMu.Unlock()
}

// LoadHistoryMessages loads messages from session history into TUI display.
func (a *App) LoadHistoryMessages() {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()

	if a.session == nil {
		return
	}

	historyMessages := a.session.GetMessages()
	if len(historyMessages) == 0 {
		return
	}

	a.historyLoaded = true

	// Display history messages in TUI
	for _, msg := range historyMessages {
		switch msg.Role {
		case "user":
			if msg.SystemInjected {
				continue
			}
			a.messages = append(a.messages, userStyle.Render(a.translator.Text(i18n.MsgYouPrefix))+msg.Content)
		case "assistant":
			// Extract text content from assistant message
			var textContent string
			if msg.Content != "" {
				textContent = msg.Content
			} else if len(msg.Contents) > 0 {
				for _, block := range msg.Contents {
					if block.Type == "text" && block.Text != "" {
						textContent += block.Text
					}
				}
			}
			if textContent != "" {
				a.messages = append(a.messages, assistantStyle.Render(a.translator.Text(i18n.MsgAssistantPrefix)+textContent))
			}
		}
	}
}

// computeContextUsage estimates context usage from the current session's
// messages and the active model. Returns nil when usage cannot be computed
// (no model, no context window, or no session/messages).
func (a *App) computeContextUsage() *ctxpkg.ContextUsage {
	if a.model == nil || a.model.ContextWindow <= 0 || a.session == nil {
		return nil
	}
	messages := a.session.GetMessages()
	if len(messages) == 0 {
		return nil
	}
	compactionSettings := agent.CompactionSettingsFromConfig(a.settings.Compaction)
	estimator := ctxpkg.ResolveTokenEstimator(compactionSettings, a.model)
	usage := ctxpkg.ContextUsageFromMessages(messages, estimator)
	usage.ContextWindow = a.model.ContextWindow
	percent := float64(usage.TotalTokens) / float64(a.model.ContextWindow) * 100
	usage.Percent = &percent
	return &usage
}

// Init implements tea.Model.
func (a *App) Init() tea.Cmd {
	var cmds []tea.Cmd

	// Render header as first message (use width 80 as default before WindowSizeMsg)
	headerW := a.width
	if headerW <= 0 {
		headerW = 80
	}
	providerName := ""
	modelName := "unknown"
	if a.model != nil {
		providerName = a.model.Provider
		modelName = a.model.Name
	}
	cwd := a.currentCwd()
	header := renderHeader(headerW, ua.Version, providerName, modelName, cwd)
	if header != "" {
		a.messages = append(a.messages, header)
	}

	// Show initial message if set
	if a.initialMessage != "" {
		a.messages = append(a.messages, statusStyle.Render(a.initialMessage))
	}

	// Load history messages from session
	a.LoadHistoryMessages()
	a.trackExistingBackgroundRuns()
	a.updateViewportContent()
	for idx := range a.messages {
		a.printMessageOnce(idx)
	}

	if inputCmd := a.input.Init(); inputCmd != nil {
		cmds = append(cmds, inputCmd)
	}

	if a.autoOpenAuthDialog {
		a.openAuthDialog()
	}
	if a.backgroundSubmitter != nil && len(a.backgroundRuns) > 0 {
		cmds = append(cmds, a.backgroundRunPoll())
	}

	return tea.Batch(cmds...)
}

func currentWorkingDir(sess *session.Manager) string {
	if sess != nil && sess.GetHeader() != nil && sess.GetHeader().Cwd != "" {
		return sess.GetHeader().Cwd
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		return cwd
	}
	return "."
}

func (a *App) currentCwd() string {
	if a.session != nil && a.session.GetHeader() != nil && a.session.GetHeader().Cwd != "" {
		return a.session.GetHeader().Cwd
	}
	if a.cwd != "" {
		return a.cwd
	}
	return "."
}

// isAgentActive returns true when the agent is actively working — thinking or
// waiting for approval. Tool execution is covered by isThinking while the
// request is active; do not infer activity from historical tool entries,
// because an aborted stream can leave a running entry until its late event is
// drained.
func (a *App) isAgentActive() bool {
	return a.isThinking || a.waitingForApproval
}

// processInputQueue returns a command that processes queued input events
func (a *App) processInputQueue() tea.Cmd {
	return tea.Tick(a.inputDelay, func(t time.Time) tea.Msg {
		return inputQueueTickMsg(t)
	})
}

// inputQueueTickMsg is sent when the input queue should be processed
type inputQueueTickMsg time.Time

// spinnerTickMsg is sent to update the spinner animation
type spinnerTickMsg time.Time

// Spinner characters for the thinking animation
var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond
const mouseWheelScrollLines = 3
const splitPasteIdleDelay = 120 * time.Millisecond

var splitPasteCoalescingEnabled = func() bool {
	return true
}

// tickSpinner returns a command that updates the spinner
func (a *App) tickSpinner() tea.Cmd {
	return tea.Tick(spinnerInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		oldWidth := a.width
		oldSkillHubPageSize := a.skillHubPageSize()
		a.width = msg.Width
		a.height = msg.Height
		a.ready = true

		a.input = a.input.SetWidth(msg.Width)
		a.authInput = a.authInput.SetWidth(msg.Width - 8)
		a.suggest = a.suggest.SetWidth(msg.Width - 2)
		if oldWidth != a.width {
			a.configureMarkdownRenderer()
			a.markAssistantRenderedDirty()
			a.invalidateToolModalCache()
		}

		a.updateViewportContent()
		if a.skillHubOpen && oldSkillHubPageSize != a.skillHubPageSize() {
			a.resetSkillHubPaging()
			if cmd := a.loadSkillHub(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if a.statusLineEnabled() {
			if !a.statusLineIntervalInit {
				a.statusLineIntervalInit = true
				if cmd := a.tickStatusLine(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			a.requestStatusLineRefresh(true)
		}
		return a, tea.Batch(cmds...)

	case inputQueueTickMsg:
		// Process queued input events after a short quiet period so
		// terminals that split paste into key events can be coalesced.
		if a.inputQueueIdle() {
			cmd := a.flushInputQueue()
			cmds = append(cmds, cmd)
			if a.stopInputQueueTickIfEmpty() {
				return a, tea.Batch(cmds...)
			}
			cmds = append(cmds, a.processInputQueue())
			return a, tea.Batch(cmds...)
		}
		// Schedule next tick
		cmds = append(cmds, a.processInputQueue())
		return a, tea.Batch(cmds...)

	case spinnerTickMsg:
		// Update timed UI affordances while the agent is active or user input is pending.
		if a.isAgentActive() {
			a.spinnerIndex = (a.spinnerIndex + 1) % len(spinnerChars)
			cmds = append(cmds, a.tickSpinner())
		}
		return a, tea.Batch(cmds...)

	case backgroundRunPollMsg:
		a.pollBackgroundRuns()
		if len(a.backgroundRuns) > 0 {
			cmds = append(cmds, a.backgroundRunPoll())
		}
		return a, tea.Batch(cmds...)

	case stopwatch.TickMsg, stopwatch.StartStopMsg, stopwatch.ResetMsg:
		var timerCmd tea.Cmd
		a.timer, timerCmd = a.timer.Update(msg)
		if timerCmd != nil {
			cmds = append(cmds, timerCmd)
		}
		return a, tea.Batch(cmds...)

	case renderRequestMsg:
		// Trailing throttled renders land here so the status line refresh runs
		// on the tea loop instead of scheduleRender's timer goroutine.
		a.requestStatusLineRefresh(false)
		a.updateViewportContent()
		return a, nil

	case updateNoticeMsg:
		a.addCommandStatus(string(msg))
		a.updateViewportContent()
		return a, nil

	case backgroundSubmittedMsg:
		if msg.Err != nil {
			a.runtime.DiscardInput(context.Background(), msg.Submission)
			a.addMessage(errorStyle.Render(a.translator.Text(i18n.MsgBackgroundStartFailed)) + activityFailureMessage(msg.Err))
			return a, nil
		}
		if msg.Input != "" {
			a.addMessage(userStyle.Render(a.translator.Text(i18n.MsgYouPrefix)) + msg.Input)
		}
		if a.backgroundRuns == nil {
			a.backgroundRuns = make(map[string]int)
		}
		if msg.RunID != "" {
			boundary := 0
			if a.session != nil {
				boundary = len(a.session.GetMessages())
			}
			a.backgroundRuns[msg.RunID] = boundary
		}
		a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgBackgroundSubmitted, msg.RunID)))
		a.scheduleRender()
		return a, a.backgroundRunPoll()

	case backgroundProgressMsg:
		if strings.TrimSpace(msg.Text) != "" {
			a.addMessage(statusStyle.Render(msg.Text))
			a.scheduleRender()
		}
		return a, nil

	case statusLineRenderedMsg:
		return a, a.handleStatusLineRendered(msg)

	case statusLineTickMsg:
		if a.statusLineEnabled() {
			a.requestStatusLineRefresh(true)
			if cmd := a.tickStatusLine(); cmd != nil {
				return a, cmd
			}
		}
		return a, nil

	case tea.MouseMsg:
		return a, a.handleMouse(msg)

	case tea.KeyMsg:
		if handled, cmd := a.handleApprovalKey(msg); handled {
			return a, cmd
		}
		if handled, cmd := a.handleSessionsKey(msg); handled {
			return a, cmd
		}
		if handled, cmd := a.handleDefaultModelKey(msg); handled {
			return a, cmd
		}
		if handled, cmd := a.handleModelKey(msg); handled {
			return a, cmd
		}
		if handled, cmd := a.handleEnvKey(msg); handled {
			return a, cmd
		}
		if handled, cmd := a.handleAuthKey(msg); handled {
			return a, cmd
		}
		if a.skillHubOpen {
			return a, a.handleSkillHubKey(msg)
		}
		if a.esmPanelOpen {
			switch {
			case msg.Type == tea.KeyCtrlC:
				a.finalizeForQuit()
				a.stopPrintLoop()
				return a, tea.Quit
			case msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlE || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
				a.closeESMPanel()
				a.scheduleRender()
				return a, nil
			case msg.Type == tea.KeyUp:
				a.scrollESMPanel(-1)
				return a, nil
			case msg.Type == tea.KeyDown:
				a.scrollESMPanel(1)
				return a, nil
			case msg.Type == tea.KeyPgUp:
				a.scrollESMPanel(-a.esmPanelPageSize())
				return a, nil
			case msg.Type == tea.KeyPgDown:
				a.scrollESMPanel(a.esmPanelPageSize())
				return a, nil
			case msg.Type == tea.KeyHome:
				a.esmPanelScroll = 0
				return a, nil
			case msg.Type == tea.KeyEnd:
				a.esmPanelScroll = a.maxESMPanelOffset()
				return a, nil
			}
			return a, nil
		}
		if a.btwOpen {
			switch {
			case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
				a.closeBtw()
				a.scheduleRender()
				return a, nil
			case msg.Type == tea.KeyUp:
				a.scrollBtw(-1)
				return a, nil
			case msg.Type == tea.KeyDown:
				a.scrollBtw(1)
				return a, nil
			case msg.Type == tea.KeyPgUp:
				a.scrollBtw(-10)
				return a, nil
			case msg.Type == tea.KeyPgDown:
				a.scrollBtw(10)
				return a, nil
			}
			return a, nil
		}
		if a.statsOverlayOpen {
			switch {
			case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
				a.closeStatsOverlay()
				a.scheduleRender()
				return a, nil
			case msg.Type == tea.KeyUp:
				a.scrollStatsOverlay(-1)
				return a, nil
			case msg.Type == tea.KeyDown:
				a.scrollStatsOverlay(1)
				return a, nil
			case msg.Type == tea.KeyPgUp:
				a.scrollStatsOverlay(-10)
				return a, nil
			case msg.Type == tea.KeyPgDown:
				a.scrollStatsOverlay(10)
				return a, nil
			}
			return a, nil
		}
		if a.toolModalOpen {
			switch {
			case msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlO || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
				a.closeToolModal()
				return a, nil
			case msg.Type == tea.KeyUp:
				a.scrollToolModal(-1)
				return a, nil
			case msg.Type == tea.KeyDown:
				a.scrollToolModal(1)
				return a, nil
			case msg.Type == tea.KeyLeft:
				a.switchToolModalTarget(-1)
				return a, nil
			case msg.Type == tea.KeyRight:
				a.switchToolModalTarget(1)
				return a, nil
			case msg.Type == tea.KeyPgUp:
				a.scrollToolModal(-a.toolModalPageSize())
				return a, nil
			case msg.Type == tea.KeyPgDown:
				a.scrollToolModal(a.toolModalPageSize())
				return a, nil
			case msg.Type == tea.KeyHome:
				a.toolModalOffset = 0
				a.toolModalPinnedBottom = false
				return a, nil
			case msg.Type == tea.KeyEnd:
				a.toolModalOffset = a.maxToolModalOffset()
				a.toolModalPinnedBottom = true
				return a, nil
			}
			return a, nil
		}

		// Special keys are processed immediately; regular text input is batched.
		switch msg.Type {
		case tea.KeyCtrlC:
			a.finalizeForQuit()
			return a, tea.Quit
		case tea.KeyEsc:
			if a.isThinking || a.waitingForApproval || a.waitingForQuestion {
				return a, a.abortPendingRequest("user pressed Esc")
			} else {
				a.input.Reset()
				a.resetInputHistoryNavigation()
			}
			return a, nil
		case tea.KeyEnter:
			if msg.Alt {
				flushCmd := a.flushInputQueue()
				a.input, _ = a.input.Update(msg)
				a.scheduleRender()
				return a, flushCmd
			}
			if splitPasteCoalescingEnabled() && a.shouldQueueEnterForSplitPaste() {
				cmd := a.queueInput(msg)
				a.resetInputHistoryNavigation()
				return a, cmd
			}

			flushCmd := a.flushInputQueue()
			submitCmd := a.handleInputSubmit()
			return a, tea.Batch(flushCmd, submitCmd)
		case tea.KeyTab:
			flushCmd := a.flushInputQueue()
			if a.commandSuggestionsVisible() && a.applySelectedCommandSuggestion() {
				return a, flushCmd
			}
			if a.commandInputActive() {
				return a, flushCmd
			}
			a.cycleMode()
			return a, flushCmd
		case tea.KeyUp:
			flushCmd := a.flushInputQueue()
			if a.handleCommandSuggestionKey(msg) {
				return a, flushCmd
			}
			if a.inputHistoryBrowsing || a.input.AtFirstLine() {
				if a.navigateInputHistory(-1) {
					return a, flushCmd
				}
			}
			if !a.input.AtFirstLine() {
				a.input, _ = a.input.Update(msg)
				a.scheduleRender()
				return a, flushCmd
			}
		case tea.KeyDown:
			flushCmd := a.flushInputQueue()
			if a.handleCommandSuggestionKey(msg) {
				return a, flushCmd
			}
			if a.inputHistoryBrowsing {
				if a.navigateInputHistory(1) {
					return a, flushCmd
				}
			}
			if !a.input.AtLastLine() {
				a.input, _ = a.input.Update(msg)
				a.scheduleRender()
				return a, flushCmd
			}
		case tea.KeyCtrlO:
			if a.openLatestToolModal() {
				return a, nil
			}
			a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgNoConversationDetails)))
			return a, nil
		case tea.KeyCtrlE:
			a.openESMPanel()
			a.scheduleRender()
			return a, nil
		case tea.KeyCtrlR:
			return a, a.previewLastPastedImage()
		case tea.KeyCtrlG:
			a.compactMode = !a.compactMode
			if !a.compactMode {
				a.printUnrenderedTranscript()
			}
			if a.compactMode {
				a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgCompactToolsOn)))
			} else {
				a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgCompactToolsOff)))
			}
			a.updateViewportContent()
			return a, nil
		case tea.KeyCtrlP:
			a.addCommandStatus(a.translator.Text(i18n.MsgMultiAgentConfigured))
			return a, nil
		}

		// Check for paste (multi-line input in a single key event)
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			input := string(msg.Runes)
			if msg.Paste || strings.Contains(input, "\n") {
				a.handlePaste(input)
				return a, nil
			}
		}

		cmd := a.queueInput(msg)
		a.resetInputHistoryNavigation()
		return a, cmd

	case agentStreamStartMsg:
		if msg.run != nil && msg.run != a.run {
			return a, nil
		}
		a.eventCh = msg.eventCh
		if msg.err != nil || msg.eventCh == nil {
			a.eventCh = nil
			a.isThinking = false
			a.manualCompactionActive = false
			a.finishRequestTimer()
			if msg.run != nil && a.run == msg.run {
				a.runtime.DiscardInput(context.Background(), msg.submission)
				msg.run.finish(agentruntime.RunStateFailed)
				a.run = nil
			}
			if msg.err != nil {
				a.addCommandError(fmt.Sprintf("Failed to start agent run: %v", msg.err))
			} else {
				a.addCommandError("Failed to start agent run: no event stream")
			}
			return a, a.timer.Stop()
		}
		a.isThinking = true
		a.manualCompactionActive = msg.compacting
		a.pendingAbortReason = ""
		a.runTerminalHandled = false
		// A running tool row can only belong to the run that is being replaced:
		// its terminal result can no longer reach this transcript. Commit it now
		// so the new run never renders underneath a stale running row.
		a.finalizeInterruptedTools()
		a.spinnerIndex = 0
		a.requestStart = time.Now()
		a.lastDuration = 0
		if !msg.compacting && msg.input != "" {
			a.addMessage(userStyle.Render(a.translator.Text(i18n.MsgYouPrefix)) + msg.input)
		}
		return a, tea.Batch(a.listenAgentEvents(), a.tickSpinner(), a.timer.Reset(), a.timer.Start())

	case queuedPromptStartMsg:
		return a, a.startNextQueuedPrompt()

	case agentEventMsg:
		// Esc can cancel a run while its event channel still has terminal
		// events buffered. Do not let those stale events mutate the next run
		// after it has installed a new channel.
		if msg.eventCh != nil && msg.eventCh != a.eventCh {
			return a, nil
		}
		return a, a.handleAgentEvent(msg.event)

	case btwStreamStartMsg:
		if msg.generation != a.btwGeneration {
			return a, nil
		}
		a.btwEventCh = msg.eventCh
		return a, a.listenBtwEvents(msg.generation)

	case btwEventMsg:
		if msg.generation != a.btwGeneration {
			return a, nil
		}
		return a, a.handleBtwEvent(msg.event)

	case btwDoneMsg:
		if msg.generation != a.btwGeneration {
			return a, nil
		}
		a.btwActive = false
		a.btwEventCh = nil
		if msg.err != nil {
			a.btwErr = msg.err
		}
		a.scheduleRender()
		return a, nil

	case statsServerStartedMsg:
		return a, a.handleStatsServerStarted(msg)

	case statsServerStartFailedMsg:
		a.addCommandError(a.translator.Text(i18n.MsgStatsStartFailed, msg.err))
		return a, nil

	case statsServerStoppedMsg:
		a.handleStatsServerStopped(msg)
		return a, nil

	case statsOverlayLoadedMsg:
		a.handleStatsOverlayLoaded(msg)
		a.scheduleRender()
		return a, nil

	case skillHubLoadedMsg:
		a.handleSkillHubLoaded(msg)
		a.scheduleRender()
		return a, nil

	case skillHubDetailLoadedMsg:
		a.handleSkillHubDetailLoaded(msg)
		a.scheduleRender()
		return a, nil

	case skillHubCategoriesLoadedMsg:
		a.handleSkillHubCategoriesLoaded(msg)
		a.scheduleRender()
		return a, nil

	case skillHubInstalledMsg:
		a.handleSkillHubInstalled(msg)
		a.scheduleRender()
		return a, nil

	case onlineModelsLoadedMsg:
		a.handleOnlineModelsLoaded(msg)
		a.scheduleRender()
		return a, nil

	case agentDoneMsg:
		if msg.eventCh != nil && msg.eventCh != a.eventCh {
			return a, nil
		}
		// The event stream ended without a terminal run event, so no tool result
		// can reach the transcript anymore. The run must still be terminalized:
		// leaving a.run set would swallow every later submission into the queue and
		// keep the session's execution lease alive indefinitely.
		a.finalizeInterruptedTools()
		if a.run != nil {
			a.finalizeAbortedRun()
			a.run.finish(agentruntime.RunStateFailed)
			a.run = nil
		}
		a.isThinking = false
		a.manualCompactionActive = false
		a.finishRequestTimer()
		if msg.err != nil {
			a.addMessage(errorStyle.Render(a.translator.Text(i18n.MsgErrorPrefix)) + activityFailureMessage(msg.err))
		} else if msg.stopReason != "" {
			a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgSessionEndedPrefix)) + msg.stopReason)
		}
		return a, tea.Batch(a.timer.Stop(), a.scheduleNextQueuedPrompt())
	}

	// Update components
	var inputCmd tea.Cmd
	a.input, inputCmd = a.input.Update(msg)

	if inputCmd != nil {
		cmds = append(cmds, inputCmd)
	}

	return a, tea.Batch(cmds...)
}

// queueInput adds an input event to the queue and starts the queue tick on demand.
func (a *App) queueInput(msg tea.Msg) tea.Cmd {
	a.inputQueueMu.Lock()
	defer a.inputQueueMu.Unlock()

	now := time.Now()
	a.inputQueue = append(a.inputQueue, InputEvent{
		msg:     msg,
		arrived: now,
	})
	a.lastInputTime = now
	if isTextInputMsg(msg) {
		a.lastTextInputTime = now
	}
	if a.inputQueueTickActive {
		return nil
	}
	a.inputQueueTickActive = true
	return a.processInputQueue()
}

func (a *App) hasQueuedInput() bool {
	a.inputQueueMu.Lock()
	defer a.inputQueueMu.Unlock()
	return len(a.inputQueue) > 0
}

func (a *App) shouldQueueEnterForSplitPaste() bool {
	if a.commandSuggestionsVisible() {
		return false
	}
	if a.hasQueuedInput() {
		return true
	}
	if strings.TrimSpace(a.input.Value()) == "" || a.lastTextInputTime.IsZero() {
		return false
	}
	return time.Since(a.lastTextInputTime) < splitPasteIdleDelay
}

func (a *App) stopInputQueueTickIfEmpty() bool {
	a.inputQueueMu.Lock()
	defer a.inputQueueMu.Unlock()
	if len(a.inputQueue) > 0 {
		return false
	}
	a.inputQueueTickActive = false
	return true
}

func (a *App) inputQueueIdle() bool {
	a.inputQueueMu.Lock()
	defer a.inputQueueMu.Unlock()
	if len(a.inputQueue) == 0 || a.lastInputTime.IsZero() {
		return true
	}

	delay := a.inputDelay
	if splitPasteCoalescingEnabled() && queuedInputNeedsPasteWindow(a.inputQueue) && delay < splitPasteIdleDelay {
		delay = splitPasteIdleDelay
	}
	return time.Since(a.lastInputTime) >= delay
}

// flushInputQueue processes all queued input events
func (a *App) flushInputQueue() tea.Cmd {
	a.inputQueueMu.Lock()
	events := make([]InputEvent, len(a.inputQueue))
	copy(events, a.inputQueue)
	a.inputQueue = a.inputQueue[:0]
	a.lastInputTime = time.Time{}
	a.inputQueueMu.Unlock()

	if len(events) == 0 {
		return nil
	}

	if splitPasteCoalescingEnabled() {
		if text, ok := coalescedSplitPaste(events); ok {
			a.handlePaste(text)
			a.updateCommandSuggestions()
			return nil
		}
	}

	// Process events in batch
	var cmds []tea.Cmd
	for _, event := range events {
		// Update input component
		if keyMsg, ok := event.msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyEnter && !keyMsg.Alt {
				a.updateCommandSuggestions()
				if cmd := a.handleInputSubmit(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				continue
			}
			var cmd tea.Cmd
			a.input, cmd = a.input.Update(keyMsg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// Schedule render
	a.updateCommandSuggestions()
	a.scheduleRender()

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}

func coalescedSplitPaste(events []InputEvent) (string, bool) {
	var b strings.Builder
	enterCount := 0
	sawEnter := false
	textAfterEnter := false

	for _, event := range events {
		msg, ok := event.msg.(tea.KeyMsg)
		if !ok {
			return "", false
		}
		switch msg.Type {
		case tea.KeyRunes:
			text := string(msg.Runes)
			if text == "" {
				continue
			}
			text = strings.ReplaceAll(text, "\r\n", "\n")
			text = strings.ReplaceAll(text, "\r", "\n")
			if sawEnter {
				textAfterEnter = true
			}
			enterCount += strings.Count(text, "\n")
			if strings.Contains(text, "\n") {
				sawEnter = true
				textAfterEnter = true
			}
			b.WriteString(text)
		case tea.KeySpace:
			if sawEnter {
				textAfterEnter = true
			}
			b.WriteRune(' ')
		case tea.KeyEnter, tea.KeyCtrlJ:
			if msg.Type == tea.KeyEnter && msg.Alt {
				return "", false
			}
			enterCount++
			sawEnter = true
			b.WriteRune('\n')
		default:
			return "", false
		}
	}

	if enterCount == 0 || !textAfterEnter {
		return "", false
	}
	return b.String(), true
}

func isTextInputMsg(msg tea.Msg) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}
	switch keyMsg.Type {
	case tea.KeyRunes, tea.KeySpace:
		return true
	default:
		return false
	}
}

// queuedInputNeedsPasteWindow reports whether the queued events still look
// like an in-flight split paste that needs the extended idle window before
// flushing. Newline-bearing rune chunks and Enter keys adjacent to text are
// paste evidence; a lone deferred Enter (or a run of Enters) carries none —
// any preceding chunks were already flushed into the input — so it keeps the
// normal idle window and submits without the extra paste delay.
func queuedInputNeedsPasteWindow(events []InputEvent) bool {
	hasEnter := false
	hasText := false
	for _, event := range events {
		msg, ok := event.msg.(tea.KeyMsg)
		if !ok {
			continue
		}
		switch msg.Type {
		case tea.KeyEnter, tea.KeyCtrlJ:
			if msg.Type == tea.KeyEnter && msg.Alt {
				continue
			}
			hasEnter = true
		case tea.KeyRunes:
			text := string(msg.Runes)
			if strings.ContainsAny(text, "\r\n") {
				return true
			}
			if text != "" {
				hasText = true
			}
		case tea.KeySpace:
			hasText = true
		}
	}
	return hasEnter && hasText
}

// scheduleRender schedules a render update with throttling.
// If called inside the throttle window, a delayed render is scheduled
// so the pending update is not lost.
func (a *App) scheduleRender() {
	a.renderMu.Lock()
	defer a.renderMu.Unlock()

	now := time.Now()
	if now.Sub(a.lastRender) < a.renderInterval {
		if !a.renderPending {
			a.renderPending = true
			remaining := a.renderInterval - now.Sub(a.lastRender)
			time.AfterFunc(remaining, func() {
				a.renderMu.Lock()
				wasPending := a.renderPending
				if wasPending {
					a.lastRender = time.Now()
					a.renderPending = false
				}
				a.renderMu.Unlock()
				if wasPending {
					// The status line footer is only refreshed when an actual render
					// is emitted, so event-dense bursts coalesce into a single
					// request instead of re-arming the external status command on
					// every scheduleRender call. The refresh itself runs on the tea
					// loop via renderRequestMsg; this timer goroutine must never
					// touch statusLine* state directly.
					if a.program != nil {
						a.program.Send(renderRequestMsg{})
					}
				}
			})
		}
		return
	}

	// Render now
	a.lastRender = now
	a.renderPending = false
	a.requestStatusLineRefresh(false)
	a.updateViewportContent()
}

// View implements tea.Model.
func (a *App) View() string {
	if !a.ready {
		return "\n  " + a.translator.Text(i18n.MsgLoading) + "\n"
	}

	footer := a.renderFooter()
	if !a.waitingForApproval && a.toolModalOpen {
		return a.renderFixedHeight(lipgloss.JoinVertical(lipgloss.Left, a.renderToolModal(), footer))
	}
	if !a.waitingForApproval && a.btwOpen {
		return a.renderFixedHeight(lipgloss.JoinVertical(lipgloss.Left, a.renderBtwOverlay(), footer))
	}
	if !a.waitingForApproval && a.statsOverlayOpen {
		return a.renderFixedHeight(lipgloss.JoinVertical(lipgloss.Left, a.renderStatsOverlay(), footer))
	}
	if !a.waitingForApproval && a.skillHubOpen {
		return a.renderFixedHeight(lipgloss.JoinVertical(lipgloss.Left, a.renderSkillHub(), footer))
	}
	if !a.waitingForApproval && a.esmPanelOpen {
		panelFooter := footer
		if a.height > 0 && a.height < 8 {
			panelFooter = ""
		}
		panel := a.renderESMPanel()
		if panelFooter != "" {
			panel = lipgloss.JoinVertical(lipgloss.Left, panel, panelFooter)
		}
		return a.renderFixedHeight(panel)
	}

	var parts []string

	// 1. Live transcript (streaming content only when unmanaged output is active)
	if live := a.renderLiveTranscriptContent(); live != "" {
		parts = append(parts, live)
	}

	// 2. Sticky todo list (non-done tasks)
	maxTodoVisible := 5
	if a.height > 0 {
		maxTodoVisible = a.height / 8
		if maxTodoVisible < 3 {
			maxTodoVisible = 3
		}
	}
	if todoList := renderStickyTodoList(a.currentPlan, a.width, maxTodoVisible); todoList != "" {
		parts = append(parts, todoList)
	}

	// 3. Loading indicator (spinner + timer + tokens when thinking)
	if loading := renderLoadingIndicator(a.translator, a.isThinking, a.spinnerIndex, a.timer.Elapsed(), 0, a.width); loading != "" {
		parts = append(parts, loading)
	}
	if activity := a.renderActivitySummary(a.width); activity != "" {
		parts = append(parts, activity)
	}

	// 4. Dialog or input field
	if a.waitingForApproval {
		parts = append(parts, a.renderApprovalDialog())
	} else if a.sessionsDialog.Open {
		parts = append(parts, a.renderSessionsDialog())
	} else if a.defaultModelDialog.Open {
		parts = append(parts, a.renderDefaultModelDialog())
	} else if a.modelDialog.Open {
		parts = append(parts, a.renderModelDialog())
	} else if a.envDialog.Open {
		parts = append(parts, a.renderEnvDialog())
	} else if a.auth.Open {
		parts = append(parts, a.renderAuthDialog())
	} else {
		inputGap := lipgloss.NewStyle().
			Foreground(lipgloss.Color("236")).
			Render(strings.Repeat("▄", a.width))
		parts = append(parts, inputGap)
		parts = append(parts, a.input.View())
		if suggestions := a.suggest.View(); suggestions != "" {
			parts = append(parts, suggestions)
		}
	}

	// 5. Footer (status bar)
	parts = append(parts, footer)

	// 6. Agent tab bar (multi-agent mode)
	if tabBar := renderAgentTabBar(a.translator, a.agentMgr, string(a.activeAgent), a.width); tabBar != "" {
		parts = append(parts, tabBar)
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// retireEventStream detaches the UI from the current run's event stream and
// keeps draining it in the background until the producer closes it. Retiring
// (rather than dropping) the channel matters because terminal events
// (EventRunFinished/EventDone/EventAgentEnd) are always sent unconditionally: a
// stream abandoned with a full buffer could still park the aborted run forever,
// leaking its goroutine and leaving its conversation turn open.
func (a *App) retireEventStream() {
	ch := a.eventCh
	a.eventCh = nil
	if ch == nil {
		return
	}
	go func() {
		for range ch {
		}
	}()
}

// finalizeForQuit terminalizes an in-flight run before the process exits. The
// Runtime shutdown that follows only requests cancellation; the loop owner (this
// adapter) must still write the terminal state, otherwise the run is later
// recovered as failed instead of the cancellation the user asked for.
func (a *App) finalizeForQuit() {
	a.abortActiveESMAgent()
	if a.run != nil {
		a.finalizeAbortedRun()
		run := a.run
		run.cancel()
		run.finish(agentruntime.RunStateCancelled)
		a.run = nil
	}
	a.isThinking = false
	a.manualCompactionActive = false
	a.finishRequestTimer()
}

func (a *App) abortPendingRequest(reason string) tea.Cmd {
	a.pendingAbortReason = reason
	a.abortActiveESMAgent()
	if a.agent != nil {
		a.abortAndResetAgent("aborted")
	} else {
		a.finalizeAbortedRun()
		a.resetAgent(fmt.Errorf("aborted"))
	}
	// No further events from the cancelled stream belong to the active UI run,
	// but the stream must still be consumed: every ordinary send (including the
	// approval/question requests) stops once the run context is done, while the
	// terminal events (EventRunFinished/EventDone/EventError/agentEndEvent) keep
	// their unconditional send. Abandoning a full buffer would park the aborted
	// run on one of those and it could never close its turn.
	a.retireEventStream()
	a.clearApprovalState()
	a.clearQuestionState()
	a.clearQueuedInput()
	a.input.Reset()
	a.resetInputHistoryNavigation()
	a.isThinking = false
	a.manualCompactionActive = false
	a.finishRequestTimer()
	a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgAborted)))
	// Prompts queued while the aborted run was active must still run: nothing else
	// restarts the queue because the aborted run's terminal event is retired.
	return tea.Batch(a.timer.Stop(), a.scheduleNextQueuedPrompt())
}

// handlePaste handles large pastes by creating markers
func (a *App) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if a.esmPanelOpen {
		switch {
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
			a.scrollESMPanel(-mouseWheelScrollLines)
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
			a.scrollESMPanel(mouseWheelScrollLines)
		}
		return nil
	}
	if a.statsOverlayOpen {
		switch {
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
			a.scrollStatsOverlay(-mouseWheelScrollLines)
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
			a.scrollStatsOverlay(mouseWheelScrollLines)
		}
		return nil
	}
	if a.skillHubOpen {
		return nil
	}
	if a.toolModalOpen {
		switch {
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
			a.scrollToolModal(-mouseWheelScrollLines)
		case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
			a.scrollToolModal(mouseWheelScrollLines)
		}
		return nil
	}

	return nil
}

func (a *App) markAssistantRenderedDirty() {
	if a.assistantDirty == nil {
		a.assistantDirty = make(map[int]bool)
	}
	for idx := range a.assistantRendered {
		a.assistantDirty[idx] = true
	}
}

// Message types
type agentStreamStartMsg struct {
	input      string
	submission agentruntime.InputSubmission
	eventCh    <-chan agent.Event
	err        error
	run        *tuiRun
	compacting bool
}
type renderRequestMsg struct{}

type backgroundSubmittedMsg struct {
	Input      string
	Submission agentruntime.InputSubmission
	RunID      string
	Err        error
}

type backgroundProgressMsg struct{ Text string }

func safeProviderName(p provider.Provider) string {
	if p == nil {
		return ""
	}
	return p.Name()
}

func (a *App) activeProviderName() string {
	if a.providerName != "" {
		return a.providerName
	}
	if a.model != nil && a.model.Provider != "" {
		return a.model.Provider
	}
	return safeProviderName(a.provider)
}

func (a *App) syncAgentManagerRuntime() {
	if a == nil || a.agentMgr == nil {
		return
	}
	a.agentMgr.UpdateRuntimeConfig(a.provider, a.activeProviderName(), a.model, a.settings, a.allow)
}
