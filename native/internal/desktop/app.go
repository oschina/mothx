package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschina/mothx/native/internal/feature/acpworkers"
	"github.com/oschina/mothx/native/internal/feature/channels"
	"github.com/oschina/mothx/native/internal/feature/cron"
	"github.com/oschina/mothx/native/internal/feature/env"
	"github.com/oschina/mothx/native/internal/feature/experts"
	"github.com/oschina/mothx/native/internal/feature/knowledge"
	"github.com/oschina/mothx/native/internal/feature/mcp"
	"github.com/oschina/mothx/native/internal/feature/memory"
	"github.com/oschina/mothx/native/internal/feature/providers"
	"github.com/oschina/mothx/native/internal/feature/serve"
	"github.com/oschina/mothx/native/internal/feature/skillhub"
	"github.com/oschina/mothx/native/internal/feature/skills"
	"github.com/oschina/mothx/native/internal/feature/stats"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// This file owns the composition shell's shared state and app-level lifecycle
// primitives. Connection, session catalog/flow, composer, decisions, and
// settings concerns live in app_*.go; rendering lives in view*.go.

type connState int

const (
	connIdle connState = iota
	connStarting
	connReady
	connFailed
)

const (
	nativeChineseSlogan = "MothX用中国词元赋能你的任何工作"
)

type nativeApp struct {
	win    *mygo.Window
	acpPtr atomic.Pointer[acpClient]

	// post, when set, delivers work to the UI thread in place of the window
	// loop. Headless tests install it to drive asynchronous lifecycle code;
	// production always goes through the window.
	post func(func())

	// connectMu guards activeConnect: the connection attempt that owns the
	// handshake right now. A newer connect() supersedes it.
	connectMu     sync.Mutex
	activeConnect *connectAttempt

	conn            connState
	connErr         string
	connFix         string
	stderr          []string
	startupMu       sync.Mutex
	startupErrState nativeStartupError
	features        map[string]bool
	featuresMu      sync.RWMutex
	// featuresReady separates “initialize has not completed yet” from a real
	// runtime that advertises no optional capabilities. Panels must wait for
	// the handshake instead of permanently accepting a pre-init empty set.
	featuresReady bool
	closing       atomic.Bool
	reloadSeq     int64
	// autoRestartAttempts/Pending are UI-thread state mirroring Desktop's
	// bounded ACP restart scheduler.
	autoRestartAttempts int
	autoRestartPending  bool

	sessions            []listedSession
	sessionsLoaded      bool
	sessionsErr         string
	sessionPages        map[string]sessionPage
	sessionPageInFlight sync.Map
	sessionCatalogSeq   int64

	// projects is the shared task-library project catalog. The sidebar task
	// tree and the history filters both project it; neither keeps a second
	// copy or reads the project registry directly.
	projects       []projectView
	projectsLoaded bool
	projectsErr    string
	projectsSeq    int64

	currentSession string
	runtimeVersion string
	loadingSession string
	// sessionLoadSeq orders concurrent session/load requests so a slower
	// response for a session the user already left cannot clear or apply to
	// the session currently visible.
	sessionLoadSeq int64
	currentTitle   string
	workspace      string
	draftCwd       string
	cwd            string
	// draftSource mirrors Desktop's draft workspace provenance: quick-start
	// (launch scratch), new-task (unset until picked), selected (explicit).
	draftSource     string
	pickingDraftDir bool
	// lastWorkspace/recentWorkspaces are native-local picker conveniences
	// (Desktop's desktop-store boundary); they never become a session cwd.
	lastWorkspace    string
	recentWorkspaces []string

	messages             []*chatMessage
	messageIndex         map[string]*chatMessage
	planMessageID        string
	planCollapsed        bool
	planCollapsedID      string
	statusMessageID      string
	transcriptNextCursor string
	transcriptLoading    bool
	transcriptSeq        int64
	// replayingHistory suppresses wall-clock reasoning timing while canonical
	// history is replayed through the live reducer.
	replayingHistory bool
	localMessageSeq  int
	list             ui.ListState
	md               markdown.Options

	running            bool
	creatingSession    bool
	status             string
	awaitingUserEcho   bool
	localUserMessageID string
	// sendSeq numbers sends; promptToken is the send the visible task is
	// waiting on. It is snapshotted with the task, so a prompt answered while
	// another task is open is applied there rather than here.
	sendSeq     int64
	promptToken int64

	usageUsed     int
	usageSize     int
	usageCost     float64
	usageCurrency string
	usageCache    *usageCacheProjection

	draft       string
	attachments []attachment

	configOptions  []configOption
	modelLabel     string
	modelValue     string
	thinkingLabel  string
	thinkingValue  string
	mode           string
	availableModes []sessionMode
	commands       []command
	// draftConfig holds selections made before session/new. It is flushed
	// once through session/set_config_option; selecting a model must never
	// create an empty session by itself.
	draftConfig map[string]string

	search       string
	searching    bool
	history      bool
	historyOpen  bool
	historyQuery string
	page         string
	historyPanel historyState
	worktree     worktreePanelState
	router       routerPanelState
	subagent     subagentPanelState
	// subagentRosters is the per-session live child projection keyed by
	// session ID (background teams keep reporting). It is reconciled by
	// mothx/subagent/list and never becomes canonical state.
	subagentRosters   map[string][]subagentInfoView
	subagentRosterSeq map[string]int64
	sidebar           bool
	expandedProjects  map[string]bool
	sessionStates     map[string]sessionSnapshot
	backgroundRouting bool

	dark      bool
	themeSet  bool
	themeMode string

	prefsPath string
	home      homeAppearance

	// binaryRollbackPrevious holds the runtime binary preference to restore
	// when a user-selected binary fails to start/initialize. Empty means no
	// rollback is pending; the shell must stay usable after a bad switch.
	binaryRollbackPrevious string
	binaryRollbackPending  bool

	toasts   []toastItem
	toastSeq int64

	// services is the current window service handle, refreshed on every frame.
	// It only provides UI-thread clipboard/file-dialog access; it is never a
	// session or artifact authority.
	services ui.Services

	diagnosticsFilter string

	modelPickerOpen      bool
	modelPickerQuery     string
	modelPickerProvider  string
	modelPickerModels    []providerCatalogModel
	modelPickerLoading   bool
	modelPickerErr       string
	modelCatalog         []providerCatalogModel
	skillIconImages      map[string]ui.ImageSource
	skillIconLoading     map[string]bool
	skillIconFailed      map[string]bool
	configuredProviders  map[string]bool
	providerCatalogOrder []string
	skillsPageQuery      string

	previewOpen      bool
	previewTitle     string
	previewImage     ui.ImageSource
	previewData      []byte
	previewMediaType string
	previewErr       string
	previewLoading   bool
	previewSeq       int64

	refreshing atomic.Bool
	// sessionRefreshPending records a session-list refresh requested while one
	// was already in flight. The active refresh re-runs it with the current
	// connection instead of dropping the update.
	sessionRefreshPending atomic.Bool

	settingsOpen     bool
	settingsCategory string
	settingsTab      string
	settingsLoading  bool
	settingsSaving   bool
	settingsErr      string
	settingsStatus   string
	settingsSeq      int64
	settingsView     *applicationSettingsView
	settingsForm     applicationSettingsForm

	providers  providers.Panel
	mcp        mcp.Panel
	knowledge  knowledge.Panel
	skills     skills.Panel
	skillhub   skillhub.Panel
	experts    experts.Panel
	acpWorkers acpworkers.Panel
	env        env.Panel
	stats      stats.Panel
	memory     memory.Panel
	cron       cron.Panel
	channels   channels.Panel
	serve      serve.Panel
	runtime    runtimePanelState
}

func newNativeApp() *nativeApp {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cwd, _ = filepath.Abs(cwd)
	a := &nativeApp{
		conn:             connIdle,
		cwd:              cwd,
		workspace:        cwd,
		draftCwd:         cwd,
		draftSource:      "quick-start",
		sidebar:          true,
		expandedProjects: make(map[string]bool),
		sessionStates:    make(map[string]sessionSnapshot),
		draftConfig:      make(map[string]string),
		messageIndex:     make(map[string]*chatMessage),
		dark:             true,
		themeMode:        "system",
		home:             defaultHomeAppearance(),
		providers:        providers.NewPanel(),
		mcp:              mcp.NewPanel(),
		knowledge:        knowledge.NewPanel(),
		skills:           skills.NewPanel(),
		skillhub:         skillhub.NewPanel(),
		experts:          experts.NewPanel(),
		acpWorkers:       acpworkers.NewPanel(),
		env:              env.NewPanel(),
		stats:            stats.NewPanel(),
		memory:           memory.NewPanel(),
		cron:             cron.NewPanel(),
		channels:         channels.NewPanel(),
		serve:            serve.NewPanel(),
		sessionPages:     make(map[string]sessionPage),
		historyPanel:     newHistoryState(),
		worktree:         newWorktreePanelState(),
		router:           newRouterPanelState(),
		subagentRosters:  make(map[string][]subagentInfoView),
	}
	a.md = markdown.Options{WrapCode: true}
	a.list.FollowEnd = true
	a.list.FollowAnimation = &ui.Smooth
	return a
}

// dispatch runs fn on the UI thread. Without a window or a test hook there is
// no UI thread to run it on, so the work is dropped: headless tests that only
// exercise synchronous reducers rely on that.
func (a *nativeApp) dispatch(fn func()) {
	switch {
	case a.post != nil:
		a.post(fn)
	case a.win == nil:
		return
	default:
		a.win.Update(fn)
	}
}

func (a *nativeApp) setConn(state connState, errText string) {
	if state != connFailed {
		a.connFix = ""
	}
	a.conn = state
	a.connErr = errText
	if state == connFailed {
		// The sidebar task tree is an ACP projection. Once the child cannot be
		// started there will be no initial page, so keep the catalogs idle
		// instead of leaving a perpetual "loading tasks" placeholder.
		a.sessionsLoaded = true
		a.projectsLoaded = true
		if a.sessionsErr == "" {
			a.sessionsErr = errText
		}
		if a.historyPanel.Loading && !a.historyPanel.Loaded {
			a.historyPanel.Loading = false
			a.historyPanel.Loaded = true
			a.historyPanel.Err = errText
		}
	}
}

func (a *nativeApp) setStartupError(err nativeStartupError) {
	a.startupMu.Lock()
	a.startupErrState = err
	a.startupMu.Unlock()
}

func (a *nativeApp) loadStartupError() nativeStartupError {
	a.startupMu.Lock()
	defer a.startupMu.Unlock()
	return a.startupErrState
}

// failConnection prefers the structured MOTHX_ACP_ERROR the ACP child printed
// (message + fix) over the generic process-exit text, matching Desktop.
func (a *nativeApp) failConnection(fallback string) {
	text := fallback
	startup := a.loadStartupError()
	if strings.TrimSpace(startup.Message) != "" {
		text = startup.Message
		a.connFix = startup.Fix
	} else {
		a.connFix = ""
	}
	a.setConn(connFailed, text)
}

const nativeMaxAutoRestartAttempts = 5

// nativeACPInitializeTimeout bounds the ACP handshake. A child that starts but
// never answers initialize must fail visibly instead of leaving the shell in
// connStarting forever. Prompt requests intentionally stay unbounded.
const nativeACPInitializeTimeout = 60 * time.Second

func (a *nativeApp) shutdown() {
	a.closing.Store(true)
	if client := a.client(); client != nil {
		client.Close()
	}
}

// Settings lifecycle is implemented in settings_nav.go. The methods below
// handle the application-settings load/save/reconnect path only.
