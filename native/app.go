package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

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
	win *mygo.Window
	acp *acpClient

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
	usageUsed          int
	usageSize          int
	usageCost          float64
	usageCurrency      string
	usageCache         *usageCacheProjection

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

	providers  providerPanelState
	mcp        mcpPanelState
	knowledge  knowledgePanelState
	skills     skillsPanelState
	skillhub   skillHubPanelState
	experts    expertsPanelState
	acpWorkers acpWorkersPanelState
	env        envPanelState
	stats      statsPanelState
	memory     memoryPanelState
	cron       cronPanelState
	channels   channelsPanelState
	serve      servePanelState
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
		providers:        newProviderPanelState(),
		mcp:              newMCPPanelState(),
		knowledge:        newKnowledgePanelState(),
		skills:           newSkillsPanelState(),
		skillhub:         newSkillHubPanelState(),
		experts:          newExpertsPanelState(),
		acpWorkers:       newACPWorkersPanelState(),
		env:              newEnvPanelState(),
		stats:            newStatsPanelState(),
		memory:           newMemoryPanelState(),
		cron:             newCronPanelState(),
		channels:         newChannelsPanelState(),
		serve:            newServePanelState(),
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

func (a *nativeApp) dispatch(fn func()) {
	if a.win == nil {
		return
	}
	a.win.Update(fn)
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

// nativeAutoRestartDelay mirrors Desktop's 1/2/4/8/15 second backoff.
func nativeAutoRestartDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	delay := time.Second << (attempt - 1)
	if delay > 15*time.Second {
		delay = 15 * time.Second
	}
	return delay
}

// nativeNonRetryableStartupError mirrors Desktop: deterministic configuration
// failures must not enter a restart loop; the person has to fix them first.
func nativeNonRetryableStartupError(err nativeStartupError) bool {
	switch strings.TrimSpace(err.Code) {
	case "config_invalid", "provider_unusable":
		return true
	default:
		return false
	}
}

// scheduleAutoRestartOrRollback prefers the binary rollback reconnect when a
// user-selected runtime just failed; otherwise it enters the bounded restart
// scheduler. Never do both for one failure.
func (a *nativeApp) scheduleAutoRestartOrRollback() {
	if a.binaryRollbackPending {
		a.rollbackRuntimeBinaryIfPending()
		return
	}
	a.maybeScheduleAutoRestart()
}

// maybeScheduleAutoRestart is called on the UI thread after an unexpected ACP
// child exit. It keeps the Desktop parity rule: retry at most five times with
// bounded backoff, and never retry deterministic configuration failures.
func (a *nativeApp) maybeScheduleAutoRestart() {
	if a.closing.Load() || a.autoRestartPending {
		return
	}
	startup := a.loadStartupError()
	if nativeNonRetryableStartupError(startup) {
		addDiagnostic("error", "ACP restart disabled for non-retryable startup error: "+startup.Code)
		return
	}
	if a.autoRestartAttempts >= nativeMaxAutoRestartAttempts {
		addDiagnostic("warn", "ACP auto-restart attempts exhausted")
		return
	}
	a.autoRestartAttempts++
	attempt := a.autoRestartAttempts
	interval := nativeAutoRestartDelay(attempt)
	a.autoRestartPending = true
	a.status = fmt.Sprintf("ACP 已退出，%s后自动重连（%d/%d）", interval, attempt, nativeMaxAutoRestartAttempts)
	go func() {
		time.Sleep(interval)
		a.dispatch(func() {
			a.autoRestartPending = false
			if a.closing.Load() {
				return
			}
			if a.conn == connReady || a.conn == connStarting {
				return
			}
			extra := a.loadStartupError()
			if nativeNonRetryableStartupError(extra) {
				return
			}
			a.status = "ACP 已退出，正在自动重连…"
			go a.connect()
		})
	}()
}

// nativeACPEnvironment preserves the user's environment but extends the
// approval/question decision deadlines to 30 minutes, matching Desktop's ACP
// child launch. These are human-paced decisions; the Runtime defaults are much
// shorter and would expire a decision while a person is still reviewing it.
func nativeACPEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "MOTHX_ACP_PERMISSION_TIMEOUT=") ||
			strings.HasPrefix(entry, "MOTHX_ACP_QUESTION_TIMEOUT=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"MOTHX_ACP_PERMISSION_TIMEOUT=30m",
		"MOTHX_ACP_QUESTION_TIMEOUT=30m",
	)
}

func (a *nativeApp) connect() {
	if a.closing.Load() {
		return
	}
	seq := atomic.AddInt64(&a.reloadSeq, 1)
	bin, source := resolveMothxBinarySource()
	addDiagnostic("info", "connect: binary="+bin+" source="+source)
	if bin == "" {
		addDiagnostic("error", "connect: no mothx binary resolved")
		a.dispatch(func() {
			a.setConn(connFailed, "找不到 mothx 可执行文件。请设置 MOTHX_BIN，或把 mothx 放进 PATH。")
		})
		return
	}
	a.dispatch(func() {
		a.setConn(connStarting, "")
		a.stderr = nil
		a.setStartupError(nativeStartupError{})
		a.featuresMu.Lock()
		a.features = make(map[string]bool)
		a.featuresReady = false
		a.featuresMu.Unlock()
		// A reconnect invalidates the management projection as well as ordinary
		// session requests. Leave the settings page so an in-flight response
		// can neither render stale values nor leave its spinner active.
		a.closeSettings()
		a.projects = nil
		a.projectsLoaded = false
		a.projectsErr = ""
		a.resetSessionPages()
		atomic.AddInt64(&a.projectsSeq, 1)
		a.invalidateRouterPanel()
		a.invalidateSubagentPanel()
		a.closeWorktreePanel()
	})

	var client *acpClient
	client = newACPClient(
		func(msg incoming) { a.handleIncoming(client, msg) },
		func(line string) {
			if parsed, ok := parseNativeStartupErrorLine(line); ok {
				a.setStartupError(parsed)
			}
			a.dispatch(func() {
				a.stderr = append(a.stderr, line)
				if len(a.stderr) > 40 {
					a.stderr = a.stderr[len(a.stderr)-40:]
				}
			})
		},
		func(err error) {
			if a.closing.Load() {
				return
			}
			text := "ACP 进程已退出"
			if err != nil {
				text += "：" + err.Error()
			}
			a.dispatch(func() {
				// A stale child (manual reconnect or binary rollback) must not
				// overwrite the connection state of the child that replaced it.
				if a.acp != client {
					return
				}
				if a.conn != connFailed {
					a.failConnection(text)
				}
				a.running = false
				a.creatingSession = false
				// The ACP child is gone, so a queued worktree open can no longer be
				// fulfilled. Drop it instead of leaving a resolved-looking promise.
				a.clearPendingWorktreeOpen()
				a.status = ""
				a.maybeScheduleAutoRestart()
			})
		},
	)
	if err := client.start(bin, a.cwd, nativeACPEnvironment()); err != nil {
		addDiagnostic("error", "ACP start failed: "+err.Error())
		a.dispatch(func() {
			a.setConn(connFailed, "无法启动 ACP："+err.Error())
			a.scheduleAutoRestartOrRollback()
		})
		return
	}
	a.dispatch(func() { a.acp = client })

	raw, err := client.call("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			"session":  map[string]any{"configOptions": map[string]any{"boolean": map[string]any{}}},
		},
		"clientInfo": map[string]any{
			"name":    "mothx-native",
			"title":   "MothX Native",
			"version": nativeBuildVersion,
		},
		"_meta": map[string]any{"mothx": map[string]any{"surface": "native"}},
	})
	if err != nil {
		client.close()
		addDiagnostic("error", "ACP initialize failed: "+err.Error())
		a.dispatch(func() {
			a.failConnection("ACP 初始化失败：" + err.Error())
			a.scheduleAutoRestartOrRollback()
		})
		return
	}
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		client.close()
		addDiagnostic("error", "ACP initialize response invalid: "+err.Error())
		a.dispatch(func() {
			a.failConnection("ACP initialize 响应无效：" + err.Error())
			a.scheduleAutoRestartOrRollback()
		})
		return
	}
	features := parseFeatures(init)
	a.dispatch(func() {
		if seq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		if version := strings.TrimSpace(init.AgentInfo.Version); version != "" {
			a.runtimeVersion = version
		}
		a.setConn(connReady, "")
		a.autoRestartAttempts = 0
		a.autoRestartPending = false
		a.setFeatures(features)
		a.binaryRollbackPending = false
		a.binaryRollbackPrevious = ""
	})
	if seq != atomic.LoadInt64(&a.reloadSeq) {
		client.close()
		return
	}
	// None of these projections is required for the handshake itself. Start
	// them together so the first list page does not wait behind draft config,
	// and let the session catalog render as soon as its own response arrives.
	go a.refreshDraftConfig(client, a.cwd, features)
	go a.refreshSessions(client, a.cwd, features)
	go a.refreshModelCatalog(client, features)
}

func (a *nativeApp) setFeatures(features map[string]bool) {
	a.featuresMu.Lock()
	a.features = features
	a.featuresReady = true
	a.featuresMu.Unlock()
	// A history view opened during initialize must resume once capabilities are
	// known instead of keeping the pre-init “not advertised” fallback forever.
	if a.acp != nil && a.historyOpen && !a.historyPanel.Loaded {
		a.historyPanel.Loading = false
		a.startHistoryLoad(false)
	}
}

func (a *nativeApp) featuresInitialized() bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	return a.featuresReady
}

func (a *nativeApp) featureSnapshot() map[string]bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	if a.features == nil {
		return nil
	}
	out := make(map[string]bool, len(a.features))
	for k, v := range a.features {
		out[k] = v
	}
	return out
}

func (a *nativeApp) featureEnabled(name string) bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	return a.features[name]
}

func parseFeatures(init initializeResult) map[string]bool {
	features := make(map[string]bool)
	raw, ok := init.Meta["mothx.dev"]
	if !ok {
		raw, ok = init.Meta["mothx"]
	}
	if !ok {
		return features
	}
	var meta struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return features
	}
	for _, f := range meta.Features {
		features[f] = true
	}
	return features
}

func (a *nativeApp) refreshDraftConfig(client *acpClient, cwd string, features map[string]bool) {
	if client == nil {
		return
	}
	if strings.TrimSpace(cwd) == "" {
		// An unset New Task draft has no cwd for draft-config-options; Desktop
		// still projects the provider/model menu from the provider catalog.
		a.refreshDraftProviderModelOptions(client, features)
		return
	}
	if len(features) > 0 && !features["sessionDraftConfigOptions"] {
		return
	}
	raw, err := client.call("mothx/session/draft-config-options", map[string]any{"cwd": cwd})
	if err != nil {
		return
	}
	var res draftConfigResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return
	}
	a.dispatch(func() {
		selectedCwd := a.workspace
		if selectedCwd == "" {
			selectedCwd = a.draftCwd
		}
		if a.currentSession != "" || selectedCwd != cwd || len(res.ConfigOptions) == 0 {
			return
		}
		a.configOptions = res.ConfigOptions
		for configID, value := range a.draftConfig {
			if option := a.configOptionByID(configID); option != nil {
				option.CurrentValue = value
			}
		}
		a.syncModelState()
		if provider := a.draftConfig["provider"]; provider != "" {
			a.refreshDraftProviderModels(provider)
		}
	})
}

func (a *nativeApp) refreshSessions(client *acpClient, cwd string, features map[string]bool) {
	if client == nil {
		return
	}
	// The task tree is one projection: keep the project catalog in step with
	// every session refresh, including refreshes that the sidebar coalesces.
	go a.refreshProjectsWith(features)
	if a.refreshing.Swap(true) {
		return
	}
	defer a.refreshing.Store(false)
	if cwd == "" {
		cwd = a.cwd
	}
	var raw json.RawMessage
	var err error
	if features["sessionListAll"] || len(features) == 0 {
		raw, err = client.call("mothx/session/listAll", map[string]any{"scope": "all", "limit": sidebarSessionPageSize})
	}
	if err != nil || len(raw) == 0 {
		raw, err = client.call("session/list", map[string]any{"cwd": cwd})
	}
	if err != nil {
		a.dispatch(func() {
			a.sessionsErr = err.Error()
			a.sessionsLoaded = true
		})
		return
	}
	var res sessionListResult
	if err := json.Unmarshal(raw, &res); err != nil {
		a.dispatch(func() {
			a.sessionsErr = err.Error()
			a.sessionsLoaded = true
		})
		return
	}
	allPage := newSessionPage(sessionPageAll, sessionPageAll, "")
	allPage.Sessions = res.Sessions
	allPage.NextCursor = res.NextCursor
	allPage.Loaded = true
	a.dispatch(func() {
		a.resetSessionPages()
		a.setSessionPage(allPage)
		a.rebuildSessionCatalog()
		a.sessionsErr = ""
		a.sessionsLoaded = true
		for _, listed := range res.Sessions {
			if listed.SessionID != a.currentSession {
				continue
			}
			a.workspace = listed.Cwd
			if a.draftCwd == "" {
				a.draftCwd = listed.Cwd
			}
			if a.currentTitle == "" || a.currentTitle == T("新会话") {
				a.currentTitle = listed.Title
			}
		}
		a.startFirstSidebarPages(features)
	})
}

func (a *nativeApp) handleIncoming(client *acpClient, msg incoming) {
	switch msg.Method {
	case "session/update":
		var n sessionUpdateNotification
		if err := json.Unmarshal(msg.Params, &n); err != nil {
			return
		}
		a.dispatch(func() { a.applyUpdate(n) })
	case "_mothx/session_event":
		var ev sessionEvent
		if err := json.Unmarshal(msg.Params, &ev); err != nil {
			return
		}
		a.dispatch(func() { a.handleSessionEvent(ev) })
	case "mothx/worktree/status":
		var payload worktreeStatusPayload
		if err := json.Unmarshal(msg.Params, &payload); err != nil {
			return
		}
		a.dispatch(func() { a.applyWorktreeStatus(payload) })
	case "$/cancel_request":
		return
	default:
		if msg.hasID() {
			a.handleReverse(client, msg)
		}
	}
}

func (a *nativeApp) handleReverse(client *acpClient, msg incoming) {
	switch msg.Method {
	case "session/request_permission":
		var p permissionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.respondError(msg.ID, -32602, "invalid permission params")
			}
			return
		}
		title := p.ToolCall.Title
		if title == "" {
			title = T("工具调用")
		}
		d := decision{
			ID:       append(json.RawMessage(nil), msg.ID...),
			Kind:     "permission",
			Session:  p.SessionID,
			Title:    title,
			Prompt:   permissionPrompt(p.ToolCall.RawInput),
			RawInput: p.ToolCall.RawInput,
		}
		for _, opt := range p.Options {
			d.Options = append(d.Options, decisionOption{ID: opt.OptionID, Label: permissionOptionLabel(opt.Name, opt.Kind), Kind: opt.Kind})
		}
		a.queueDecision(d)
	case "mothx/requestQuestion", "_mothx/request_question":
		var p questionParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.respondError(msg.ID, -32602, "invalid question params")
			}
			return
		}
		prompt := p.Prompt
		if prompt == "" {
			prompt = p.Question
		}
		title := p.Title
		if title == "" {
			title = "MothX 想确认一下"
		}
		d := decision{
			ID:      append(json.RawMessage(nil), msg.ID...),
			Kind:    "question",
			Session: p.SessionID,
			Title:   title,
			Prompt:  prompt,
			Multi:   p.Multi,
		}
		if p.TimeoutMs > 0 {
			d.Deadline = time.Now().Add(time.Duration(p.TimeoutMs) * time.Millisecond)
		}
		d.Options = parseQuestionOptions(p.Options)
		a.queueDecision(d)
	case "elicitation/create":
		var p elicitationParams
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			if client != nil {
				_ = client.respondError(msg.ID, -32602, "invalid elicitation params")
			}
			return
		}
		d := buildElicitationDecision(msg.ID, p)
		a.queueDecision(d)
	default:
		if client != nil && msg.hasID() {
			_ = client.respondError(msg.ID, -32601, "method not found: "+msg.Method)
		}
	}
}

// buildElicitationDecision projects one elicitation/create request into the
// inline decision card. The Runtime's form projection uses a single "answer"
// property, but a request replayed from a form-capable client may carry other
// shapes: prefer "answer", else the first property in sorted order, and accept
// both string and {id,label} enum entries instead of failing the whole request
// on a mixed schema.
func buildElicitationDecision(id json.RawMessage, p elicitationParams) decision {
	d := decision{
		ID:      append(json.RawMessage(nil), id...),
		Kind:    "elicitation",
		Session: p.SessionID,
		Title:   T("MothX 想确认一下"),
		Prompt:  p.Message,
		Elicit:  true,
	}
	names := make([]string, 0, len(p.Schema.Properties))
	for name := range p.Schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 1 {
		// A real multi-property form: one field per property, answered as
		// content keyed by property name instead of a single free text.
		for _, name := range names {
			prop := p.Schema.Properties[name]
			field := decisionField{Name: name, Title: prop.Title, Description: prop.Description}
			if field.Title == "" {
				field.Title = name
			}
			for _, raw := range prop.Enum {
				if option, ok := parseElicitationEnum(raw); ok {
					field.Options = append(field.Options, option)
				}
			}
			d.Fields = append(d.Fields, field)
		}
		return d
	}
	chosen := ""
	for _, name := range names {
		if name == "answer" {
			chosen = name
			break
		}
	}
	if chosen == "" && len(names) > 0 {
		chosen = names[0]
	}
	if chosen != "" {
		prop := p.Schema.Properties[chosen]
		if prop.Title != "" {
			d.Title = prop.Title
		}
		for _, raw := range prop.Enum {
			if option, ok := parseElicitationEnum(raw); ok {
				d.Options = append(d.Options, option)
			}
		}
	}
	return d
}

// elicitationContent builds the accept payload: a multi-property form answers
// as content keyed by property name; the single-property projection keeps the
// Runtime's "answer" key. Empty forms decline instead of accepting blank data.
func elicitationContent(d *decision) map[string]any {
	content := make(map[string]any)
	if len(d.Fields) > 0 {
		for _, field := range d.Fields {
			if value := strings.TrimSpace(field.Value); value != "" {
				content[field.Name] = value
			}
		}
		return content
	}
	if answer := strings.TrimSpace(d.Answer); answer != "" {
		content["answer"] = answer
	}
	return content
}

func elicitationResolvedLabel(d *decision, content map[string]any) string {
	if len(d.Fields) == 0 {
		if value, ok := content["answer"].(string); ok {
			return value
		}
		return ""
	}
	parts := make([]string, 0, len(d.Fields))
	for _, field := range d.Fields {
		if value, ok := content[field.Name].(string); ok {
			parts = append(parts, field.Name+"="+value)
		}
	}
	return strings.Join(parts, ", ")
}

// parseElicitationEnum accepts both enum shapes used by ACP form schemas:
// plain strings and the {id,label} option objects the Runtime embeds when it
// projects a legacy question into elicitation/create.
func parseElicitationEnum(raw json.RawMessage) (decisionOption, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && strings.TrimSpace(text) != "" {
		return decisionOption{ID: text, Label: text}, true
	}
	var obj struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		id := obj.ID
		if id == "" {
			id = obj.Label
		}
		label := obj.Label
		if label == "" {
			label = id
		}
		if id != "" {
			return decisionOption{ID: id, Label: label}, true
		}
	}
	return decisionOption{}, false
}

// permissionOptionLabel keeps the Runtime-provided option name, which is the
// canonical user-facing label, and only falls back to a Native translation for
// older projections that omit it. Never overwrite an ACP name with a
// client-local string: the Runtime may already have localized it.
func permissionOptionLabel(name, kind string) string {
	if label := strings.TrimSpace(name); label != "" {
		return label
	}
	switch kind {
	case "allow_once", "allow":
		return T("允许一次")
	case "reject_once", "reject":
		return T("拒绝")
	default:
		return kind
	}
}

func permissionPrompt(input map[string]any) string {
	if input == nil {
		return ""
	}
	if command, ok := input["command"].(string); ok && command != "" {
		return command
	}
	if path, ok := input["path"].(string); ok && path != "" {
		return path
	}
	if query, ok := input["query"].(string); ok && query != "" {
		return query
	}
	return ""
}

func parseQuestionOptions(raw json.RawMessage) []decisionOption {
	if len(raw) == 0 {
		return nil
	}
	var objects []questionOption
	if err := json.Unmarshal(raw, &objects); err == nil {
		var out []decisionOption
		for _, opt := range objects {
			label := opt.Label
			if label == "" {
				label = opt.ID
			}
			out = append(out, decisionOption{ID: opt.ID, Label: label})
		}
		return out
	}
	var stringsOpt []string
	if err := json.Unmarshal(raw, &stringsOpt); err == nil {
		out := make([]decisionOption, 0, len(stringsOpt))
		for _, s := range stringsOpt {
			out = append(out, decisionOption{ID: s, Label: s})
		}
		return out
	}
	return nil
}

func (a *nativeApp) queueDecision(d decision) {
	a.dispatch(func() {
		add := func() {
			id := "decision:" + decisionIDString(d.ID)
			if id == "decision:" {
				id = a.nextLocalMessageID("decision")
			}
			message := a.upsertMessage(id, messageDecision)
			copy := d
			if copy.Multi {
				copy.Choices = make(map[string]*bool, len(copy.Options))
				for _, option := range copy.Options {
					copy.Choices[option.ID] = new(bool)
				}
			}
			message.Decision = &copy
			message.Done = false
		}
		if d.Session != "" && d.Session != a.currentSession && d.Session != a.loadingSession {
			a.withSessionState(d.Session, add)
			a.showErrorToast("后台会话需要确认，切换到该任务处理")
			return
		}
		add()
	})
}

func (a *nativeApp) decisionMessage(d *decision) *chatMessage {
	for _, message := range a.messages {
		if message.Decision == d {
			return message
		}
	}
	return nil
}

func (a *nativeApp) answerDecision(d *decision, optionID string) {
	if d == nil || d.Resolved {
		return
	}
	client := a.acp
	answer := strings.TrimSpace(d.Answer)
	if optionID != "" {
		answer = optionID
		if !d.Multi {
			d.Answer = optionID
		}
	}
	resolved := ""
	switch d.Kind {
	case "permission":
		if client != nil {
			if optionID == "" {
				_ = client.respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
			} else {
				_ = client.respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}})
			}
		}
		resolved = T("已取消")
		if optionID != "" {
			for _, option := range d.Options {
				if option.ID == optionID {
					resolved = Tf("已选择：%s", option.Label)
					break
				}
			}
		}
	case "elicitation":
		content := elicitationContent(d)
		if client != nil {
			if len(content) == 0 {
				_ = client.respond(d.ID, map[string]any{"action": "decline"})
			} else {
				_ = client.respond(d.ID, map[string]any{"action": "accept", "content": content})
			}
		}
		resolved = T("已取消")
		if len(content) > 0 {
			resolved = Tf("已提交：%s", elicitationResolvedLabel(d, content))
		}
	default:
		if client != nil {
			if d.Multi {
				var answers []string
				for _, option := range d.Options {
					if ptr := d.Choices[option.ID]; ptr != nil && *ptr {
						answers = append(answers, option.Label)
					}
				}
				if answer != "" {
					answers = append(answers, answer)
				}
				if len(answers) == 0 {
					_ = client.respond(d.ID, map[string]any{"cancelled": true})
				} else {
					_ = client.respond(d.ID, map[string]any{"answers": answers})
				}
			} else if answer == "" {
				_ = client.respond(d.ID, map[string]any{"cancelled": true})
			} else {
				_ = client.respond(d.ID, map[string]any{"answer": answer})
			}
		}
		resolved = T("已取消")
		if answer != "" {
			resolved = Tf("已回答：%s", answer)
		}
	}
	d.Resolved = true
	d.ResolvedValue = resolved
	if message := a.decisionMessage(d); message != nil {
		message.Done = true
	}
}

func (a *nativeApp) cancelDecision(d *decision) {
	if d == nil || d.Resolved {
		return
	}
	client := a.acp
	switch d.Kind {
	case "permission":
		if client != nil {
			_ = client.respond(d.ID, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
		}
	case "elicitation":
		if client != nil {
			_ = client.respond(d.ID, map[string]any{"action": "decline"})
		}
	default:
		if client != nil {
			_ = client.respond(d.ID, map[string]any{"cancelled": true})
		}
	}
	d.Resolved = true
	d.ResolvedValue = T("已取消")
	if message := a.decisionMessage(d); message != nil {
		message.Done = true
	}
}

func (a *nativeApp) newChat() {
	if a.creatingSession {
		a.status = "请等待当前会话创建完成"
		return
	}
	if a.settingsOpen {
		a.closeSettings()
	}
	a.saveActiveSession()
	a.invalidateRouterPanel()
	a.invalidateSubagentPanel()
	a.closeArtifactPreview()
	a.page = ""
	a.clearSessionState("")
	a.currentTitle = ""
	a.draft = ""
	a.attachments = nil
	a.draftConfig = make(map[string]string)
	a.resetTranscript()
	// Desktop's New Task draft has no inherited directory: it stays unset
	// until the native picker (or a worktree action) supplies one, and send
	// asks again if it is still empty. Only the launch quick-start seeds a
	// scratch directory without asking.
	a.draftSource = "new-task"
	a.workspace = ""
	a.draftCwd = ""
	go a.refreshDraftConfig(a.acp, "", a.featureSnapshot())
}

// startNewTask mirrors Desktop's New Task: reset to an unset draft and ask
// for the directory immediately; cancelling leaves it unset for send time.
func (a *nativeApp) startNewTask() {
	a.newChat()
	a.requestDraftDirectory(func(picked string) {
		if picked == "" {
			return
		}
		a.applyWorkspaceChoice(picked)
	})
}

// requestDraftDirectory opens the native directory picker once at a time and
// delivers the choice (or "" on cancel) on the UI thread. With no window it
// resolves to cancel so headless tests stay deterministic.
func (a *nativeApp) requestDraftDirectory(onPick func(string)) {
	if a.pickingDraftDir {
		return
	}
	if a.win == nil {
		onPick("")
		return
	}
	a.pickingDraftDir = true
	win := a.win
	defaultPath := a.lastWorkspace
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T("选择新会话工作区"),
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		picked := ""
		if err == nil && len(paths) > 0 {
			picked = paths[0]
		}
		a.dispatch(func() {
			a.pickingDraftDir = false
			onPick(picked)
		})
	}()
}

func (a *nativeApp) newChatIn(cwd string) {
	if a.creatingSession {
		a.status = "请等待当前会话创建完成"
		return
	}
	a.newChat()
	if cwd != "" {
		a.recordWorkspace(cwd)
		a.workspace = cwd
		a.draftCwd = cwd
		a.draftSource = "selected"
		go a.refreshDraftConfig(a.acp, cwd, a.featureSnapshot())
	}
}

func (a *nativeApp) chooseWorkspace() {
	// An open session owns its cwd: the picker changes that session through
	// the canonical setWorkDir path. Only a draft shell changes the directory
	// for the next new session; it must never silently replace the chat.
	if a.currentSession != "" {
		session, ok := a.sessionByID(a.currentSession)
		if !ok {
			session = listedSession{SessionID: a.currentSession, Cwd: a.workspace}
		} else if a.sessionRunActive(session) {
			a.status = "该会话正在运行，请先停止"
			return
		}
		if !a.historyWorkDirCapable() {
			a.status = "当前运行时不支持修改会话工作目录"
			return
		}
		a.chooseHistoryWorkDir(session)
		return
	}
	win := a.win
	defaultPath := a.lastWorkspace
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T("选择新会话工作区"),
			DefaultPath:       defaultPath,
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		a.dispatch(func() {
			a.applyWorkspaceChoice(path)
		})
	}()
}

// applyWorkspaceChoice applies one explicitly picked directory with the same
// semantics as the native picker: an open session changes its canonical cwd
// through setWorkDir; a draft only changes the next new-session directory.
// Recent-directory menu entries reuse this path instead of forking it.
func (a *nativeApp) applyWorkspaceChoice(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if a.currentSession != "" {
		session, ok := a.sessionByID(a.currentSession)
		if !ok {
			session = listedSession{SessionID: a.currentSession, Cwd: a.workspace}
		} else if a.sessionRunActive(session) {
			a.status = "该会话正在运行，请先停止"
			return
		}
		if !a.historyWorkDirCapable() {
			a.status = "当前运行时不支持修改会话工作目录"
			return
		}
		a.recordWorkspace(path)
		a.runHistoryWorkDir(session, path)
		return
	}
	a.recordWorkspace(path)
	a.workspace = path
	a.draftCwd = path
	a.draftSource = "selected"
	a.status = "新会话工作区已切换"
	go a.refreshDraftConfig(a.acp, path, a.featureSnapshot())
}

func (a *nativeApp) attachFiles() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   win,
			Title:    T("附加文件"),
			Multiple: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.dispatch(func() {
			root := a.workspace
			if root == "" {
				root = a.draftCwd
			}
			for _, p := range paths {
				abs, err := filepath.Abs(p)
				if err != nil {
					abs = p
				}
				entry := attachment{Name: filepath.Base(abs), Path: abs, MimeType: mime.TypeByExtension(filepath.Ext(abs))}
				if strings.HasPrefix(strings.ToLower(entry.MimeType), "image/") {
					if info, statErr := os.Stat(abs); statErr == nil && info.Size() <= nativeMaxEmbeddedAttachment {
						if data, readErr := os.ReadFile(abs); readErr == nil {
							if image, decodeErr := ui.DecodeImage(data); decodeErr == nil {
								entry.Preview = image
							}
						}
					}
				}
				if !pathWithinDirectory(root, abs) {
					info, statErr := os.Stat(abs)
					if statErr != nil {
						a.status = "无法读取附件：" + entry.Name
						continue
					}
					if info.Size() > nativeMaxEmbeddedAttachment {
						a.status = "附件过大，未附加：" + entry.Name
						continue
					}
					data, readErr := os.ReadFile(abs)
					if readErr != nil {
						a.status = "无法读取附件：" + entry.Name
						continue
					}
					entry.Data = base64.StdEncoding.EncodeToString(data)
					entry.Size = info.Size()
					entry.Embedded = true
				}
				a.attachments = append(a.attachments, entry)
			}
		})
	}()
}

func (a *nativeApp) removeAttachment(i int) {
	if i < 0 || i >= len(a.attachments) {
		return
	}
	a.attachments = append(a.attachments[:i], a.attachments[i+1:]...)
}

func (a *nativeApp) selectSession(s listedSession) {
	if a.currentSession == s.SessionID {
		return
	}
	if a.settingsOpen {
		a.closeSettings()
	}
	a.invalidateRouterPanel()
	a.invalidateSubagentPanel()
	a.closeArtifactPreview()
	a.page = ""
	a.selectSessionState(s.SessionID)
	a.loadingSession = s.SessionID
	a.currentTitle = s.Title
	a.workspace = s.Cwd
	a.draftConfig = make(map[string]string)
	if a.draftCwd == "" {
		a.draftCwd = s.Cwd
	}
	if !a.running {
		a.status = "正在载入会话…"
	} else {
		a.status = "该会话正在运行…"
	}
	client := a.acp
	if client == nil {
		a.loadingSession = ""
		a.running = false
		a.status = ""
		a.settleWorktreeOpen()
		return
	}
	features := a.featureSnapshot()
	params := map[string]any{
		"sessionId": s.SessionID,
		"cwd":       s.Cwd,
	}
	if a.transcriptPagingEnabled() || features["sessionHistoryPaging"] {
		params["historyLimit"] = transcriptPageSize
	}
	go func() {
		raw, err := client.call("session/load", params)
		if err != nil {
			a.dispatch(func() { a.failSessionLoad(err) })
			return
		}
		var res sessionResult
		_ = json.Unmarshal(raw, &res)
		a.dispatch(func() { a.finishSessionLoad(s.SessionID, res, s.Title) })
		go a.refreshSessions(client, s.Cwd, features)
	}()
}

// finishSessionLoad applies a completed session/load and then gives a deferred
// worktree open intent the chance to run now that loadingSession is clear.
func (a *nativeApp) finishSessionLoad(sessionID string, res sessionResult, title string) {
	if a.currentSession != sessionID {
		return
	}
	a.loadingSession = ""
	a.status = ""
	busy := false
	if res.Execution != nil {
		busy = res.Execution.Busy
	} else if session, ok := a.sessionByID(sessionID); ok {
		busy = a.sessionRunActive(session)
	}
	a.running = busy
	a.applySessionResult(res)
	if busy {
		a.status = "该会话正在运行…"
		for i := 0; i < len(a.messages)-1; i++ {
			a.messages[i].Done = true
		}
	} else {
		for _, m := range a.messages {
			m.Done = true
		}
	}
	if a.currentTitle == "" && res.SessionID != "" {
		a.currentTitle = title
	}
	a.refreshSubagentRoster(sessionID)
	a.settleWorktreeOpen()
}

// failSessionLoad restores the idle shell after a failed load. The deferred
// worktree intent is consumed first so its new session does not hide the error.
func (a *nativeApp) failSessionLoad(err error) {
	a.loadingSession = ""
	a.currentSession = ""
	a.currentTitle = ""
	a.status = ""
	a.settleWorktreeOpen()
	a.appendErrorFromErr("载入会话失败：", err)
}

func (a *nativeApp) send() {
	text := strings.TrimSpace(a.draft)
	if text == "" || a.running || a.creatingSession {
		return
	}
	if a.conn != connReady {
		a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "尚未连接到 MothX ACP。", Done: true})
		return
	}
	if a.currentSession == "" && a.draftSource == "new-task" &&
		strings.TrimSpace(a.workspace) == "" && strings.TrimSpace(a.draftCwd) == "" {
		// Desktop asks again at send time when the New Task draft still has
		// no directory; cancelling keeps the draft instead of sending nowhere.
		a.requestDraftDirectory(func(picked string) {
			if picked == "" {
				a.status = "未选择工作目录，新任务未发送"
				return
			}
			a.applyWorkspaceChoice(picked)
			a.send()
		})
		return
	}
	if a.acp == nil {
		a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "尚未连接到 MothX ACP。", Done: true})
		return
	}
	attachments := append([]attachment(nil), a.attachments...)
	sessionID := a.currentSession
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	client := a.acp
	features := a.featureSnapshot()
	draftConfig := a.cloneDraftConfig()

	a.draft = ""
	a.attachments = nil
	a.running = true
	if a.page == "home" {
		// Desktop's home composer returns to the chat view once a prompt is
		// sent; the transcript then shows the same session's run.
		a.page = ""
	}
	if sessionID == "" {
		a.creatingSession = true
	}
	a.status = "正在生成…"
	a.setStatusLine("正在生成…")
	display := text
	if len(attachments) > 0 {
		var names []string
		for _, f := range attachments {
			names = append(names, f.Name)
		}
		display += "\n\n" + T("附件：") + strings.Join(names, T("、"))
	}
	localUserID := a.nextLocalMessageID("user")
	a.appendMessage(&chatMessage{ID: localUserID, Kind: messageUser, Text: display, Done: true, Images: attachmentChatImages(attachments)})
	a.awaitingUserEcho = true
	a.localUserMessageID = localUserID

	go func() {
		if sessionID == "" {
			raw, err := client.call("session/new", map[string]any{"cwd": cwd})
			if err != nil {
				a.promptFailed(err)
				return
			}
			var res sessionResult
			if err := json.Unmarshal(raw, &res); err != nil {
				a.promptFailed(err)
				return
			}
			sessionID = res.SessionID
			a.dispatch(func() {
				a.applySessionResult(res)
				a.currentTitle = "新会话"
				a.draftConfig = make(map[string]string)
				a.finishSessionCreation()
			})
			if warning := a.flushDraftConfig(client, sessionID, draftConfig); warning != "" {
				a.dispatch(func() { a.status = warning })
			}
			go a.refreshSessions(client, cwd, features)
		}
		blocks := []contentBlock{{Type: "text", Text: text}}
		for _, f := range attachments {
			if f.Embedded && f.Data != "" {
				blocks = append(blocks, contentBlock{Type: "resource", Name: f.Name, MimeType: f.MimeType, Data: f.Data})
				continue
			}
			uri := fileURI(f.Path)
			blocks = append(blocks, contentBlock{Type: "resource_link", Name: f.Name, URI: uri, MimeType: f.MimeType})
		}
		addDiagnostic("info", "session/prompt session="+sessionID)
		raw, err := client.call("session/prompt", map[string]any{
			"sessionId": sessionID,
			"prompt":    blocks,
			"_meta": map[string]any{
				"mothx": map[string]any{"surface": "native"},
			},
		})
		if err != nil {
			a.promptFailed(err)
			return
		}
		var res promptResult
		_ = json.Unmarshal(raw, &res)
		a.dispatch(func() {
			a.markRunDone(res.StopReason)
			if res.StopReason != "" && res.StopReason != "end_turn" && res.StopReason != "cancelled" {
				a.appendRunFinished(res.StopReason)
			}
		})
		go a.refreshSessions(client, cwd, features)
	}()
}

// appendRunFinished records a non-normal terminal stop reason as a transcript
// system line. The canonical Chinese text stays the dictionary key; the
// transcript renderer projects it through T, so both locales read naturally and
// a later locale switch re-renders existing history consistently.
func (a *nativeApp) appendRunFinished(stopReason string) {
	a.appendMessage(&chatMessage{
		ID:   a.nextLocalMessageID("system"),
		Kind: messageSystem,
		Text: "运行结束：" + stopReason,
		Done: true,
	})
}

func (a *nativeApp) promptFailed(err error) {
	a.dispatch(func() { a.applyPromptFailure(err) })
}

// applyPromptFailure restores the idle shell after a failed send. Any deferred
// worktree intent is consumed before the error is appended, so the new chat
// keeps the failure visible instead of silently swallowing it.
func (a *nativeApp) applyPromptFailure(err error) {
	addDiagnostic("error", "prompt failed: "+err.Error())
	a.showErrorToast("发送失败：" + err.Error())
	a.running = false
	a.creatingSession = false
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.status = ""
	a.settleWorktreeOpen()
	a.appendErrorFromErr("发送失败：", err)
}

func pathWithinDirectory(root, path string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}

// cancelSessionRun stops one projected session, active or background. The
// Runtime decides whether the cancel is accepted; the client only forwards the
// canonical session/cancel request and projects the immediate status.
func (a *nativeApp) cancelSessionRun(sessionID string) {
	client := a.acp
	if client == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	a.setSessionRunLocal(sessionID, "cancelling", true)
	if a.currentSession == sessionID {
		a.status = "正在停止…"
	}
	go func() {
		_, _ = client.call("session/cancel", map[string]any{"sessionId": sessionID})
	}()
}

func (a *nativeApp) stop() {
	if !a.running {
		return
	}
	client := a.acp
	sessionID := a.currentSession
	a.status = "正在停止…"
	if client != nil && sessionID != "" {
		go func() {
			_, _ = client.call("session/cancel", map[string]any{"sessionId": sessionID})
		}()
		return
	}
	a.markRunDone("cancelled")
}

func (a *nativeApp) setConfigOption(configID, value string) {
	if a.running || a.creatingSession {
		return
	}
	// A draft shell has no session to mutate. Keep the selection local and
	// flush it once after session/new; browsing menus must not create rows.
	if a.currentSession == "" {
		a.applyDraftConfigOption(configID, value)
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	sessionID := a.currentSession
	go func() {
		raw, err := client.call("session/set_config_option", map[string]any{
			"sessionId": sessionID,
			"configId":  configID,
			"value":     value,
		})
		if err != nil {
			a.dispatch(func() { a.status = "切换失败：" + err.Error() })
			return
		}
		var res sessionResult
		if err := json.Unmarshal(raw, &res); err == nil && len(res.ConfigOptions) > 0 {
			a.dispatch(func() {
				if a.currentSession != sessionID {
					return
				}
				a.configOptions = res.ConfigOptions
				a.syncModelState()
			})
		}
	}()
}

// finishSessionCreation clears the transient create guard and consumes a
// deferred worktree open intent now that the shell can accept a new chat.
func (a *nativeApp) finishSessionCreation() {
	a.creatingSession = false
	a.settleWorktreeOpen()
}

func (a *nativeApp) shutdown() {
	a.closing.Store(true)
	if a.acp != nil {
		a.acp.close()
	}
}

// Settings lifecycle is implemented in settings_nav.go. The methods below
// handle the application-settings load/save/reconnect path only.

func (a *nativeApp) loadApplicationSettings(seq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
				return
			}
			a.settingsLoading = false
			a.settingsErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	raw, err := client.call("mothx/manage/application/get", map[string]any{})
	a.dispatch(func() {
		if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
			return
		}
		if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		a.settingsLoading = false
		if err != nil {
			a.settingsErr = "读取应用设置失败：" + err.Error()
			return
		}
		var view applicationSettingsView
		if err := json.Unmarshal(raw, &view); err != nil {
			a.settingsErr = "应用设置响应无效：" + err.Error()
			return
		}
		a.settingsView = &view
		a.settingsForm = applicationSettingsFormFromView(&view)
		a.settingsErr = ""
	})
}

func (a *nativeApp) saveApplicationSettings() {
	if a.settingsSaving || a.settingsLoading {
		return
	}
	if a.settingsView == nil {
		a.settingsStatus = "保存失败：设置尚未加载。"
		return
	}
	patch := applicationSettingsPatch(a.settingsView, &a.settingsForm)
	if len(patch) == 0 {
		a.settingsStatus = "没有需要保存的变更"
		return
	}
	client := a.acp
	if client == nil {
		a.settingsStatus = "保存失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&a.settingsSeq, 1)
	a.settingsSaving = true
	a.settingsStatus = "正在保存…"
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("mothx/manage/application/patch", map[string]any{"patch": patch})
		a.dispatch(func() {
			if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
				return
			}
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.settingsSaving = false
			if err != nil {
				a.settingsStatus = "保存失败：" + err.Error()
				return
			}
			var view applicationSettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				a.settingsStatus = "保存后响应无效：" + err.Error()
				return
			}
			a.settingsView = &view
			a.settingsForm = applicationSettingsFormFromView(&view)
			a.settingsStatus = "应用设置已保存"
		})
	}()
}

func (a *nativeApp) onWebSearchBackendChanged(backend string) {
	if a.settingsForm.WebSearchBackend == backend {
		return
	}
	a.settingsForm.WebSearchBackend = backend
	a.settingsForm.WebSearchBaseURL = ""
	a.settingsForm.WebSearchToken = ""
	a.settingsForm.ClearWebSearchToken = true
}

// small helpers used by the view.

func (a *nativeApp) currentModelOption() *configOption {
	for i := range a.configOptions {
		if a.configOptions[i].ID == "model" {
			return &a.configOptions[i]
		}
	}
	return nil
}

func (a *nativeApp) currentThinkingOption() *configOption {
	for i := range a.configOptions {
		switch a.configOptions[i].ID {
		case "thinking_level", "thought_level", "thinking":
			return &a.configOptions[i]
		}
	}
	return nil
}

func (a *nativeApp) connectionLabel() string {
	switch a.conn {
	case connStarting:
		return T("连接中")
	case connReady:
		return T("已连接")
	case connFailed:
		return T("离线")
	default:
		return T("未连接")
	}
}

func (a *nativeApp) usageLabel() string {
	if a.usageUsed <= 0 {
		return ""
	}
	label := fmt.Sprintf("%d tokens", a.usageUsed)
	if a.usageSize > 0 {
		percent := int(float64(a.usageUsed) / float64(a.usageSize) * 100)
		label = fmt.Sprintf("%d / %d (%d%%)", a.usageUsed, a.usageSize, percent)
	}
	if a.usageCost > 0 {
		currency := a.usageCurrency
		if currency == "" {
			currency = "$"
		}
		if currency == "USD" || currency == "usd" {
			currency = "$"
		}
		label += fmt.Sprintf(" · %s%.4f", currency, a.usageCost)
	}
	return label
}

func (a *nativeApp) workspaceLabel() string {
	return a.workspaceName()
}
