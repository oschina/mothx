package channels

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/a2a"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/mcp"
	"github.com/startvibecoding/mothx/internal/memory"
	"github.com/startvibecoding/mothx/internal/messaging"
	"github.com/startvibecoding/mothx/internal/provider"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/serve/hooks"
	serviceruntime "github.com/startvibecoding/mothx/internal/serve/runtime"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/util"
	"github.com/startvibecoding/mothx/internal/workflow"
)

// ToolCatalogItem describes a tool that may be configured for channel sessions.
type ToolCatalogItem struct {
	Name              string `json:"name"`
	Available         bool   `json:"available"`
	Default           bool   `json:"default"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

// ChannelToolDefinition is the single runtime definition used by the
// catalog, persisted-tool validator and session registry builder.
type ChannelToolDefinition struct {
	Name              string
	Default           bool
	Available         bool
	UnavailableReason string
}

func isMultiAgentToolName(name string) bool {
	return name == "delegate_subagent" || strings.HasPrefix(name, "subagent_") || strings.HasPrefix(name, "workflow_")
}

// esmSteeringMessages provides one channel Agent run with the same persisted,
// version-aware objective steering used by the TUI and WebUI. It deliberately
// has no scheduling side effects: channel delivery remains a projection of the
// shared Runtime lifecycle.
func (d *Dispatcher) esmSteeringMessages(sess *ChannelSession) func() []provider.Message {
	if d == nil || sess == nil || sess.ID == "" {
		return nil
	}
	sessionDir := d.sessionDir
	if sessionDir == "" && sess.Manager != nil {
		sessionDir = sess.Manager.GetSessionDir()
	}
	if sessionDir == "" {
		return nil
	}
	return esm.NewSteeringSource(esm.NewStore(sessionDir), sess.ID).Next
}

type ChannelToolState struct {
	Name              string `json:"name"`
	RequestedEnabled  bool   `json:"requestedEnabled"`
	Available         bool   `json:"available"`
	EffectiveEnabled  bool   `json:"effectiveEnabled"`
	Registered        bool   `json:"registered"`
	WillRegister      bool   `json:"willRegister"`
	UnavailableReason string `json:"reason,omitempty"`
}

// BackgroundRequest and BackgroundSubmitter remain available from the
// channels package for compatibility while their contract lives in the
// neutral serve/runtime package.
type BackgroundRequest = serviceruntime.BackgroundRequest
type BackgroundSubmitter = serviceruntime.BackgroundSubmitter

// ToolCatalog returns the complete channel tool catalog. Startup options determine
// each dynamic tool's default selection; every catalog item remains selectable per session.
func (d *Dispatcher) ToolCatalog(platform string) []ToolCatalogItem {
	definitions := d.channelToolDefinitions(platform)
	result := make([]ToolCatalogItem, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, ToolCatalogItem{
			Name: definition.Name, Default: definition.Default,
			Available: definition.Available, UnavailableReason: definition.UnavailableReason,
		})
	}
	return result
}

func (d *Dispatcher) channelToolDefinitions(platform string) []ChannelToolDefinition {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.channelToolDefinitionsLocked(platform)
}

func (d *Dispatcher) channelToolDefinitionsLocked(platform string) []ChannelToolDefinition {
	cfg := d.cfg
	browserEnabled := d.browser
	a2aEnabled := d.a2aMaster
	multiAgentEnabled := d.multiAgent
	cronAvailable := d.cronStore != nil
	if cfg == nil {
		cfg = DefaultConfig()
	}
	workDir := cfg.GetPlatformWorkDir(platform)
	reg, err := agentruntime.BuildRegistry(workDir, nil, d.settings, agentruntime.RegistryPolicy{RegisterDefaults: true})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	result := make([]ChannelToolDefinition, 0)
	add := func(name string, available, defaultEnabled bool, reason ...string) {
		if seen[name] {
			return
		}
		seen[name] = true
		item := ChannelToolDefinition{Name: name, Available: available, Default: defaultEnabled}
		if len(reason) > 0 && !available {
			item.UnavailableReason = reason[0]
		}
		result = append(result, item)
	}
	for _, item := range reg.All() {
		add(item.Name(), true, true)
	}
	// The browser runtime can be enabled on demand, so the browser tool stays
	// selectable regardless of the feature flag; browserEnabled only decides
	// the default checked state.
	add("browser", true, browserEnabled)
	add("memory", true, true)
	add("cron", cronAvailable, cronAvailable, "cron scheduler is disabled")
	a2aAvailable, a2aReason := a2aToolAvailability(a2aEnabled)
	add("a2a_dispatch", a2aAvailable, a2aAvailable, a2aReason)
	// The multi-agent runtime (agent manager) is always available, so these
	// tools stay selectable regardless of the multiAgent flag. multiAgent only
	// decides the default checked state; the user can still enable/disable
	// them per session from the WebUI.
	add("delegate_subagent", true, multiAgentEnabled)
	// Every sub-agent tool is listed so the WebUI can project the same surface the
	// session registry actually registers (multiAgentEnabled only decides the
	// default checked state).
	for _, name := range agent.SubAgentToolNames() {
		add(name, true, multiAgentEnabled)
	}
	for _, name := range []string{"workflow_lint", "workflow_run", "workflow_status", "workflow_cancel"} {
		add(name, true, multiAgentEnabled)
	}
	return result
}

func (d *Dispatcher) SessionToolStates(sessionID, platform string) ([]ChannelToolState, int64, error) {
	catalog := d.ToolCatalog(platform)
	configured, err := session.ListChannelTools(d.sessionDir, sessionID)
	if err != nil {
		return nil, 0, err
	}
	generation, err := session.GetChannelToolGeneration(d.sessionDir, sessionID)
	if err != nil {
		return nil, 0, err
	}
	requested := make(map[string]bool, len(configured))
	for _, item := range configured {
		requested[item.ToolName] = item.Enabled
	}
	hasConfig := len(configured) > 0
	registered := d.registeredTools(sessionID)
	states := make([]ChannelToolState, 0, len(catalog))
	for _, item := range catalog {
		value, ok := requested[item.Name]
		if !ok {
			value = !hasConfig && item.Default
		}
		effective := value && item.Available
		isRegistered := false
		willRegister := effective
		if registered != nil {
			isRegistered = registered[item.Name]
		}
		states = append(states, ChannelToolState{
			Name: item.Name, RequestedEnabled: value, Available: item.Available,
			EffectiveEnabled: effective, Registered: isRegistered, WillRegister: willRegister, UnavailableReason: item.UnavailableReason,
		})
	}
	return states, generation, nil
}

func (d *Dispatcher) registeredTools(sessionID string) map[string]bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, sess := range d.sessions {
		if sess == nil || sess.ID != sessionID || sess.Registry == nil {
			continue
		}
		result := make(map[string]bool)
		for _, tool := range sess.Registry.All() {
			result[tool.Name()] = true
		}
		return result
	}
	return nil
}

type agentApprovalHandler func(toolCallID, toolName string, args map[string]any) bool

// Dispatcher routes messages to per-user agent sessions.
type Dispatcher struct {
	mu         sync.RWMutex
	cfg        *Config
	settings   *config.Settings
	allow      *config.AllowConfig
	version    string
	sessionDir string
	security   *Security
	hooksMgr   *hooks.Manager

	// Cached provider/model for creating agent instances
	provider     provider.Provider
	providerName string // user-configured vendor name
	model        *provider.Model

	// Multi-agent mode
	multiAgent bool
	agentMgr   *agent.AgentManager

	// Cron
	cronStore cron.CronStore
	scheduler *cron.Scheduler

	// Sandbox mode
	sandbox    bool
	sandboxMgr *sandbox.Manager
	browser    bool
	artifact   bool
	a2aMaster  bool

	// Active sessions: key = "<platform-channel>/<user_id>"
	sessions map[string]*ChannelSession
	// agentSessions maps a channel run's root agent ID to its channel session ID.
	// It lets the manager status listener route terminal child-agent events to
	// the right session observer even after the parent event stream has closed.
	agentSessions map[string]string
	// Optional callback invoked when a channel sub-agent emits an event. It lets
	// the WebUI display channel-owned sub-agent progress without sharing runtime
	// ownership of the channel AgentManager.
	subAgentObserver    func(string, agent.Event)
	questionObserver    func(string, agent.Event)
	runObserver         func(string)
	rotateHandler       func(string, string, bool) error
	backgroundSubmitter BackgroundSubmitter
	runRootCtx          context.Context
	runRootStop         context.CancelFunc
	identityLocks       *session.IdentityLocks
	watchdogFired       map[string]struct{}
}

// ChannelSession holds state for a single channel user session.
type ChannelSession struct {
	// Runtime owns the front-end-neutral session resources. The fields below
	// remain transition aliases while Channel-specific tool policy migrates.
	Runtime    *agentruntime.SessionRuntime
	Execution  *agentruntime.ExecutionRuntime
	Decisions  *agentruntime.DecisionService
	ID         string // e.g. "channels/wechat/wxid_user1"
	Platform   string // "wechat", "feishu", "ws"
	UserID     string
	WorkDir    string
	Manager    *session.Manager
	SandboxMgr *sandbox.Manager
	Registry   *tools.Registry
	// AgentMgr is the session-scoped manager that owns this session's member
	// mailbox (and a bound team's roster). The dispatcher-wide manager stays as a
	// process-level fallback for tools that do not carry session member state.
	AgentMgr   *agent.AgentManager
	MCPClients []*mcp.Client // connected MCP clients (nil if none)
	Mode       string
	LastUsed   time.Time
	mu         sync.Mutex // serializes requests within this session
	// ForceCompact is a legacy/session flag consumed by the next agent run.
	// The /compact command now executes compaction immediately.
	ForceCompact    bool
	runStateMu      sync.Mutex
	runID           string
	runCancel       context.CancelFunc
	runAgent        *agent.Agent
	runStartedAt    time.Time
	lastEventAt     time.Time
	invalidated     bool
	generation      int64
	pendingEntrants int
	activeRuns      int
}

// ChannelSessionLease keeps a resolved session alive while a message waits for
// the shared runtime lock. This prevents refresh/invalidation from closing its
// Registry or MCP clients underneath the waiting request.
type ChannelSessionLease struct {
	d          *Dispatcher
	key        string
	platform   string
	userID     string
	session    *ChannelSession
	generation int64
	promoted   bool
	released   bool
}

type dispatcherRuntimeSnapshot struct {
	cfg          *Config
	settings     *config.Settings
	provider     provider.Provider
	providerName string
	model        *provider.Model
	allow        *config.AllowConfig
	security     *Security
	hooksMgr     *hooks.Manager
	multiAgent   bool
	sandbox      bool
	browser      bool
	artifact     bool
	a2aMaster    bool
	cronStore    cron.CronStore
	scheduler    *cron.Scheduler
	agentMgr     *agent.AgentManager
}

func (d *Dispatcher) runtimeSnapshot() dispatcherRuntimeSnapshot {
	if d == nil {
		return dispatcherRuntimeSnapshot{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return dispatcherRuntimeSnapshot{
		cfg: d.cfg, settings: d.settings, provider: d.provider, providerName: d.providerName, model: d.model,
		allow: d.allow, security: d.security, hooksMgr: d.hooksMgr,
		multiAgent: d.multiAgent, sandbox: d.sandbox, browser: d.browser, artifact: d.artifact, a2aMaster: d.a2aMaster,
		cronStore: d.cronStore, scheduler: d.scheduler, agentMgr: d.agentMgr,
	}
}

func (d *Dispatcher) acquireSessionLease(key, platform, userID string, sess *ChannelSession) (*ChannelSessionLease, bool) {
	if d == nil || sess == nil {
		return nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sessions[key] != sess || sess.invalidated {
		return nil, false
	}
	sess.pendingEntrants++
	return &ChannelSessionLease{d: d, key: key, platform: platform, userID: userID, session: sess, generation: sess.generation}, true
}

func (l *ChannelSessionLease) promoteAfterRuntimeLock() bool {
	if l == nil || l.d == nil || l.session == nil || l.released {
		return false
	}
	l.d.mu.Lock()
	if l.d.sessions[l.key] != l.session || l.session.invalidated || l.session.generation != l.generation {
		l.releaseLocked()
		l.d.mu.Unlock()
		return false
	}
	l.d.mu.Unlock()

	if (l.platform == "wechat" || l.platform == "feishu") && l.d.identityLocks != nil {
		releaseIdentity := l.d.identityLocks.Lock(l.platform, l.userID)
		bound, err := session.FindBinding(l.d.sessionDir, l.platform, l.userID)
		releaseIdentity()
		if err != nil || bound == nil || l.session.Manager == nil || l.session.Manager.GetHeader() == nil || bound.SessionID != l.session.Manager.GetHeader().ID {
			l.d.mu.Lock()
			l.releaseLocked()
			l.d.mu.Unlock()
			return false
		}
	}

	l.d.mu.Lock()
	defer l.d.mu.Unlock()
	if l.d.sessions[l.key] != l.session || l.session.invalidated || l.session.generation != l.generation {
		l.releaseLocked()
		return false
	}
	if l.session.pendingEntrants > 0 {
		l.session.pendingEntrants--
	}
	l.session.activeRuns++
	l.promoted = true
	return true
}

func (l *ChannelSessionLease) release() {
	if l == nil || l.d == nil || l.session == nil || l.released {
		return
	}
	l.d.mu.Lock()
	defer l.d.mu.Unlock()
	l.releaseLocked()
}

func (l *ChannelSessionLease) releaseLocked() {
	if l.released {
		return
	}
	if l.promoted && l.session.activeRuns > 0 {
		l.session.activeRuns--
	} else if !l.promoted && l.session.pendingEntrants > 0 {
		l.session.pendingEntrants--
	}
	l.released = true
	if l.session.invalidated && l.session.pendingEntrants == 0 && l.session.activeRuns == 0 && l.d.sessions[l.key] == l.session {
		l.d.closeAndDeleteLocked(l.key, l.session)
	}
}

// Lock acquires the session lock.
func (s *ChannelSession) Lock() { s.mu.Lock() }

// Unlock releases the session lock.
func (s *ChannelSession) Unlock() { s.mu.Unlock() }

// Touch updates the last-used timestamp.
func (s *ChannelSession) Touch() { s.LastUsed = time.Now() }

// NewDispatcher creates a dispatcher with the given configuration.
func NewDispatcher(cfg *Config, settings *config.Settings, version string, cronStore cron.CronStore, scheduler *cron.Scheduler) (*Dispatcher, error) {
	if cfg.WebSearch {
		settings.WebSearch.Enabled = config.BoolPtr(true)
	}
	providerName := cfg.GetDefaultProvider(settings.DefaultProvider)
	modelID := cfg.GetDefaultModel(settings.DefaultModel)

	p, model, err := providerfactory.Create(settings, providerName, modelID)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}

	runRootCtx, runRootStop := context.WithCancel(context.Background())
	d := &Dispatcher{
		cfg:           cfg,
		settings:      settings,
		allow:         config.LoadAllow(),
		version:       version,
		sessionDir:    settings.GetSessionDir(),
		security:      NewSecurity(cfg),
		hooksMgr:      hooks.NewManager(cfg.Hooks.PreToolCall, cfg.Hooks.PostToolCall),
		provider:      p,
		providerName:  providerName,
		model:         model,
		multiAgent:    cfg.MultiAgent,
		sandbox:       cfg.Sandbox,
		sandboxMgr:    sandbox.NewManagerWithOptions(cfg.GetWorkDir(), settings.Sandbox.Options()),
		browser:       cfg.Browser,
		artifact:      cfg.Artifact,
		a2aMaster:     cfg.A2AMaster,
		cronStore:     cronStore,
		scheduler:     scheduler,
		sessions:      make(map[string]*ChannelSession),
		agentSessions: make(map[string]string),
		runRootCtx:    runRootCtx,
		runRootStop:   runRootStop,
		identityLocks: session.NewIdentityLocks(),
	}

	if cfg.MultiAgent || cronStore != nil {
		d.ensureAgentManager()
	}
	d.startWatchdog()

	return d, nil
}

// SetIdentityLocks injects the shared identity lock set used by serve
// lifecycle management. It should be called before the first inbound message.
func (d *Dispatcher) SetIdentityLocks(locks *session.IdentityLocks) {
	if d == nil || locks == nil {
		return
	}
	d.mu.Lock()
	d.identityLocks = locks
	d.mu.Unlock()
}

// ApplyConfig updates runtime channel settings and drops cached sessions so the next
// inbound message is built from the new configuration.
func (d *Dispatcher) ApplyConfig(cfg *Config) error {
	if d == nil || cfg == nil {
		return fmt.Errorf("dispatcher config is required")
	}
	if cfg.WebSearch {
		d.settings.WebSearch.Enabled = config.BoolPtr(true)
	}
	providerName := cfg.GetDefaultProvider(d.settings.DefaultProvider)
	modelID := cfg.GetDefaultModel(d.settings.DefaultModel)
	d.mu.RLock()
	currentProvider := d.provider
	currentModel := d.model
	currentProviderName := d.providerName
	previousCfg := d.cfg
	d.mu.RUnlock()
	p, model := currentProvider, currentModel
	if currentProvider == nil || currentProviderName != providerName || currentModel == nil || currentModel.ID != modelID {
		var err error
		p, model, err = providerfactory.Create(d.settings, providerName, modelID)
		if err != nil {
			return fmt.Errorf("create provider: %w", err)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg
	d.provider = p
	d.providerName = providerName
	d.model = model
	d.security = NewSecurity(cfg)
	d.hooksMgr = hooks.NewManager(cfg.Hooks.PreToolCall, cfg.Hooks.PostToolCall)
	d.multiAgent = cfg.MultiAgent
	d.sandbox = cfg.Sandbox
	d.browser = cfg.Browser
	d.artifact = cfg.Artifact
	d.a2aMaster = cfg.A2AMaster
	for key, sess := range d.sessions {
		if shouldInvalidateSession(previousCfg, cfg, key) {
			d.invalidateSessionLocked(key, sess)
		}
	}
	if !cfg.MultiAgent && d.agentMgr != nil {
		d.agentMgr = nil
	}
	return nil
}

// ApplySettings rebuilds the provider from settings.json so future channel
// runs and sub-agents use the same runtime configuration as the WebUI.
func (d *Dispatcher) ApplySettings(settings *config.Settings) error {
	if d == nil || settings == nil {
		return fmt.Errorf("dispatcher settings are required")
	}

	d.mu.RLock()
	cfg := d.cfg
	allow := d.allow
	d.mu.RUnlock()
	if cfg == nil {
		return fmt.Errorf("dispatcher config is required")
	}

	runtimeSettings := *settings
	if cfg.WebSearch {
		runtimeSettings.WebSearch.Enabled = config.BoolPtr(true)
	}
	providerName := cfg.GetDefaultProvider(runtimeSettings.DefaultProvider)
	modelID := cfg.GetDefaultModel(runtimeSettings.DefaultModel)
	p, model, err := providerfactory.Create(&runtimeSettings, providerName, modelID)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	d.mu.Lock()
	d.settings = &runtimeSettings
	d.provider = p
	d.providerName = providerName
	d.model = model
	manager := d.agentMgr
	for key, sess := range d.sessions {
		d.invalidateSessionLocked(key, sess)
	}
	d.mu.Unlock()

	if manager != nil {
		manager.UpdateRuntimeConfig(p, providerName, model, &runtimeSettings, allow)
	}
	return nil
}

func shouldInvalidateSession(previous, next *Config, key string) bool {
	if previous == nil || next == nil {
		return true
	}
	globalChanged := previous.WorkDir != next.WorkDir ||
		previous.MultiAgent != next.MultiAgent ||
		previous.Sandbox != next.Sandbox ||
		previous.Browser != next.Browser ||
		previous.Artifact != next.Artifact ||
		previous.A2AMaster != next.A2AMaster ||
		!reflect.DeepEqual(previous.Security, next.Security) ||
		!reflect.DeepEqual(previous.Memory, next.Memory) ||
		!reflect.DeepEqual(previous.Cron, next.Cron) ||
		!reflect.DeepEqual(previous.Hooks, next.Hooks) ||
		!reflect.DeepEqual(previous.Agent, next.Agent)
	if globalChanged {
		return true
	}
	if strings.HasPrefix(key, "channels/wechat/") {
		return !reflect.DeepEqual(previous.Wechat, next.Wechat)
	}
	if strings.HasPrefix(key, "channels/feishu/") {
		return !reflect.DeepEqual(previous.Feishu, next.Feishu)
	}
	return true
}

// forwardChildTerminalStatus routes terminal child-agent lifecycle transitions
// to the channel session observer. It is the delivery path of last resort for
// children whose parent event stream already closed: the stream-forwarded copy
// is dropped in that case, and the sink deduplicates when both paths deliver.
func (d *Dispatcher) forwardChildTerminalStatus(st agent.ManagedAgentStatus) {
	if d == nil {
		return
	}
	if !isTerminalChildState(st.State) {
		return
	}
	root := st.ParentID
	if root == "" {
		// Top-level channel agents finishing is run bookkeeping, not sub-agent
		// activity.
		return
	}
	mgr := d.AgentManager()
	if mgr != nil {
		for depth := 0; depth < 8; depth++ {
			parent, ok := mgr.Parent(root)
			if !ok {
				break
			}
			root = parent
		}
	}
	d.mu.RLock()
	sessionID := d.agentSessions[string(root)]
	d.mu.RUnlock()
	if sessionID == "" {
		return
	}
	ev := agent.Event{
		AgentID:       st.ID,
		Type:          agent.EventRunFinished,
		Status:        agent.TaskSuccess,
		StatusMessage: st.Result,
	}
	switch st.State {
	case "error":
		ev.Status = agent.TaskFailed
		if st.Error != "" {
			ev.Error = errors.New(st.Error)
			log.Printf("[channels] deferred sub-agent %s failed: %s", st.ID, st.Error)
		}
	case "incomplete":
		ev.Status = agent.TaskIncomplete
	case "canceled":
		ev.Status = agent.TaskCanceled
		if st.Error != "" {
			ev.Error = errors.New(st.Error)
			log.Printf("[channels] deferred sub-agent %s cancelled: %s", st.ID, st.Error)
		}
	}
	d.notifySubAgentObserver(sessionID, channelSafeSubAgentEvent(ev))
	d.notifyRunObserver(sessionID)
}

func isTerminalChildState(state string) bool {
	return state == "done" || state == "incomplete" || state == "error" || state == "canceled"
}

// releaseAgentSession drops the root-agent → session mapping once the run has
// no non-terminal children left. Mappings must survive the run while children
// are still active so their late terminal events can be routed.
func (d *Dispatcher) releaseAgentSession(id agentpkg.AgentID) {
	if d == nil {
		return
	}
	if mgr := d.AgentManager(); mgr != nil {
		for _, st := range mgr.Statuses() {
			if st.ParentID == id && !isTerminalChildState(st.State) {
				return
			}
		}
	}
	d.mu.Lock()
	delete(d.agentSessions, string(id))
	d.mu.Unlock()
}

// AgentManager returns the dispatcher agent manager used by sub-agents and cron.
func (d *Dispatcher) AgentManager() *agent.AgentManager {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.agentMgr
}

// EnsureAgentManager creates the dispatcher agent manager if it is not already available.
func (d *Dispatcher) EnsureAgentManager() *agent.AgentManager {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ensureAgentManager()
}

func (d *Dispatcher) ensureAgentManager() *agent.AgentManager {
	if d.agentMgr != nil {
		return d.agentMgr
	}
	if d.sandboxMgr != nil {
		if d.sandbox {
			if err := d.sandboxMgr.SetLevel(sandbox.LevelStandard); err != nil {
				return nil
			}
			if err := d.sandboxMgr.FallbackError(); err != nil {
				log.Printf("[channels] sandbox unavailable; using direct execution: %v", err)
			}
		} else {
			_ = d.sandboxMgr.SetLevel(sandbox.LevelNone)
		}
	}
	runtime := &agentruntime.SessionRuntime{
		Source: agentruntime.SourceUnknown, EntrySource: agentruntime.SourceUnknown, SandboxMgr: d.sandboxMgr,
	}
	mgr, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
		Runtime: runtime, Provider: d.provider, Model: d.model, Settings: d.settings,
		ProviderName: d.providerName, Allow: d.allow, MultiAgentEnabled: true,
	})
	if err != nil {
		log.Printf("[channels] create agent manager: %v", err)
		return nil
	}
	d.agentMgr = mgr
	// The manager is the authoritative source of terminal child-agent states:
	// an asynchronously spawned child can outlive the parent event stream, and
	// events forwarded through that stream are dropped once it closes.
	d.agentMgr.AddStatusListener(d.forwardChildTerminalStatus)
	return d.agentMgr
}

// selectedSubAgentTools returns the canonical sub-agent tools that this
// session's tool selection actually registered. Re-registering the whole
// canonical set must not resurrect a tool the user switched off, so the caller
// re-applies this selection afterwards.
func selectedSubAgentTools(reg *tools.Registry) map[string]bool {
	selected := make(map[string]bool)
	if reg == nil {
		return selected
	}
	for _, name := range agent.SubAgentToolNames() {
		if _, ok := reg.Get(name); ok {
			selected[name] = true
		}
	}
	return selected
}

// newSessionAgentManager creates the session-scoped manager that owns this
// session's member mailbox (and a bound team's roster). It intentionally does
// not reuse d.agentMgr: the latter is shared across sessions, predates the
// session's Runtime, and has no session mailbox, so a member's question or
// completion would have no wake path (N8).
func (d *Dispatcher) newSessionAgentManager(runtime *agentruntime.SessionRuntime, snapshot dispatcherRuntimeSnapshot) *agent.AgentManager {
	if runtime == nil || snapshot.provider == nil || snapshot.model == nil || snapshot.settings == nil {
		return nil
	}
	manager, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
		Runtime: runtime, Provider: snapshot.provider, ProviderName: snapshot.providerName,
		Model: snapshot.model, Settings: snapshot.settings, Allow: snapshot.allow,
		MultiAgentEnabled: snapshot.multiAgent || runtime.TeamExpertActive(),
	})
	if err != nil {
		log.Printf("[channels] create session agent manager: %v", err)
		return nil
	}
	manager.AddStatusListener(d.forwardChildTerminalStatus)
	return manager
}

// SetCronStore updates the cron store used by newly created channel sessions.
func (d *Dispatcher) SetCronStore(store cron.CronStore) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cronStore = store
}

// SetCronScheduler updates the scheduler used by cron tools created for sessions.
func (d *Dispatcher) SetCronScheduler(s *cron.Scheduler) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.scheduler = s
	d.mu.Unlock()
}

// SetSubAgentObserver installs a callback for sub-agent events emitted during
// channel execution. The session ID identifies the channel-bound WebUI session.
func (d *Dispatcher) SetSubAgentObserver(observer func(string, agent.Event)) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.subAgentObserver = observer
	d.mu.Unlock()
}

// SubAgentObserverConfigured reports whether channel sub-agent events have a sink.
// It is used by serve integration tests to verify runtime wiring.
func (d *Dispatcher) SubAgentObserverConfigured() bool {
	if d == nil {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.subAgentObserver != nil
}

// SetQuestionObserver installs a callback for channel-owned interactive questions.
// The callback is optional; without a protocol-level responder, Channel runs
// remain unattended and the dispatcher resolves the question with an empty
// answer after notifying the observer.
func (d *Dispatcher) SetQuestionObserver(observer func(string, agent.Event)) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.questionObserver = observer
	d.mu.Unlock()
}

// The callback is session-ID based so the WebUI can reuse its canonical runtime
// snapshot and event broker.
func (d *Dispatcher) SetRunObserver(observer func(string)) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.runObserver = observer
	d.mu.Unlock()
}

// SetBackgroundSubmitter routes channel messages to the serve-owned durable
// Responses runtime when the configured provider enables background mode.
func (d *Dispatcher) SetBackgroundSubmitter(submitter BackgroundSubmitter) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.backgroundSubmitter = submitter
	d.mu.Unlock()
}

func (d *Dispatcher) responsesBackgroundEnabled() bool {
	if d == nil {
		return false
	}
	d.mu.RLock()
	p := d.provider
	d.mu.RUnlock()
	enabled, ok := p.(interface{ ResponsesBackgroundEnabled() bool })
	return ok && enabled.ResponsesBackgroundEnabled()
}

// SetRotateHandler lets the serve runtime route channel /new and /clear
// through the shared lifecycle coordinator. The bool argument requests a
// forced rotation (cancel the active run, wait a grace period, then rotate
// even if the run ignored cancellation).
func (d *Dispatcher) SetRotateHandler(handler func(string, string, bool) error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.rotateHandler = handler
	d.mu.Unlock()
}

func (d *Dispatcher) PlatformWorkDir(platform string) string {
	if d == nil {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.cfg == nil {
		return ""
	}
	return d.cfg.GetPlatformWorkDir(platform)
}

func (d *Dispatcher) notifyQuestionObserver(sessionID string, ev agent.Event) {
	if d == nil || sessionID == "" {
		return
	}
	d.mu.RLock()
	observer := d.questionObserver
	d.mu.RUnlock()
	if observer != nil {
		observer(sessionID, ev)
	}
}

func (d *Dispatcher) notifyRunObserver(sessionID string) {
	if d == nil || sessionID == "" {
		return
	}
	d.mu.RLock()
	observer := d.runObserver
	d.mu.RUnlock()
	if observer != nil {
		observer(sessionID)
	}
}

func (d *Dispatcher) notifySubAgentObserver(sessionID string, ev agent.Event) {
	if d == nil || sessionID == "" || ev.AgentID == "" {
		return
	}
	d.mu.RLock()
	observer := d.subAgentObserver
	d.mu.RUnlock()
	if observer != nil {
		observer(sessionID, ev)
	}
}

// HandleMessage processes an inbound message from any platform.
type incompleteRunError struct{}

func (incompleteRunError) Error() string { return "agent run incomplete" }

// channelRunFailure carries the Runtime-owned, safe error projection while
// retaining the original cause for lifecycle checks such as errors.Is. Its
// Error string is what the messaging transport presents to users.
type channelRunFailure struct {
	cause error
	info  agentruntime.ErrorInfo
}

func (e *channelRunFailure) Error() string {
	if e == nil {
		return "The run could not be completed."
	}
	if message := strings.TrimSpace(agentruntime.DisplayErrorMessage(e.info)); message != "" {
		return message
	}
	return "The run could not be completed."
}

func (e *channelRunFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *channelRunFailure) ErrorInfo() agentruntime.ErrorInfo {
	if e == nil {
		return agentruntime.ErrorInfo{}
	}
	return e.info
}

func newChannelRunFailure(err error, observed *agentruntime.ErrorInfo, phase agentruntime.RunPhase) error {
	info := channelFailureInfo(err, observed, phase)
	return &channelRunFailure{cause: err, info: info}
}

func channelFailureInfo(err error, observed *agentruntime.ErrorInfo, phase agentruntime.RunPhase) agentruntime.ErrorInfo {
	if observed != nil && strings.TrimSpace(agentruntime.DisplayErrorMessage(*observed)) != "" {
		return *observed
	}
	var failure *channelRunFailure
	if errors.As(err, &failure) && failure != nil && strings.TrimSpace(agentruntime.DisplayErrorMessage(failure.info)) != "" {
		return failure.info
	}
	return agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{Phase: phase})
}

// channelSafeSubAgentEvent keeps observer-facing terminal failures within the
// same safe Runtime contract as main channel runs. The original error remains
// available to the dispatcher for logging before this projection is emitted.
func channelSafeSubAgentEvent(ev agent.Event) agent.Event {
	switch ev.Type {
	case agent.EventError:
		info := channelFailureInfo(ev.Error, nil, agentruntime.PhaseModel)
		ev.Error = errors.New(agentruntime.DisplayErrorMessage(info))
	case agent.EventRunFinished:
		if !ev.Status.IsSuccessful() {
			info := channelFailureInfo(ev.Error, nil, agentruntime.PhaseModel)
			ev.Error = errors.New(agentruntime.DisplayErrorMessage(info))
		}
	}
	return ev
}

// HandleMessage is retained for text-only callers such as existing SDK and
// webhook integrations. Channel transports use HandleDelivery so native media
// operations are projected from the same canonical run result.
func (d *Dispatcher) HandleMessage(ctx context.Context, msg messaging.InboundMessage) (string, error) {
	response, err := d.HandleDelivery(ctx, msg)
	// Text-only embedding callers predate MessageResponse. They cannot execute
	// native media operations, so terminalize those pending records explicitly
	// instead of leaving a replayable delivery in limbo.
	if len(response.Attachments) > 0 {
		if response.Text != "" {
			response.Text += "\n\n"
		}
		response.Text += "Generated attachments are available in the MothX WebUI session. This text-only caller cannot send media attachments."
		for _, attachment := range response.Attachments {
			if attachment.Complete != nil {
				attachment.Complete(ctx, "unsupported", "", "text_only_adapter")
			}
		}
	}
	return response.Text, err
}

// HandleDelivery processes an inbound message through the channel Runtime and
// returns the transport projection of the canonical text/artifact result.
func (d *Dispatcher) HandleDelivery(ctx context.Context, msg messaging.InboundMessage) (response messaging.MessageResponse, runErr error) {
	log.Printf("[channels] HandleMessage: platform=%s userID=%s chatID=%s text=%q", msg.Platform, msg.UserID, msg.ChatID, truncate(msg.Text, 80))

	runtime := d.runtimeSnapshot()
	// Feishu's open_id identifies the sender, while chat_id identifies the
	// conversation used for session binding and outbound delivery. Messaging
	// channels accept all users by default.
	msg.UserID = channelRouteID(msg)

	// Check if command
	if strings.HasPrefix(msg.Text, "/") {
		text, err := d.handleCommand(msg)
		return messaging.MessageResponse{Text: text}, err
	}

	var sess *ChannelSession
	var lease *ChannelSessionLease
	var releaseRuntime func()
	var leaseOK bool
	for attempt := 0; attempt < 3; attempt++ {
		var err error
		sess, err = d.resolveSession(msg.Platform, msg.UserID)
		if err != nil {
			return messaging.MessageResponse{}, fmt.Errorf("resolve session: %w", err)
		}
		key := sessionKey(msg.Platform, msg.UserID)
		lease, leaseOK = d.acquireSessionLease(key, msg.Platform, msg.UserID, sess)
		if !leaseOK {
			continue
		}
		// Admission below blocks until any in-flight run for this session
		// finishes. Tell the user their message is queued instead of leaving them
		// to guess whether the agent stopped.
		sess.runStateMu.Lock()
		queuedBehind := sess.runID
		sess.runStateMu.Unlock()
		if queuedBehind != "" && msg.ProgressFunc != nil {
			msg.ProgressFunc("⏳ 上一条消息仍在执行，本条消息将排队等待…")
		}
		runtimeGuard, admissionErr := agentruntime.AcquireExecutionAdmission(ctx, d.sessionDir, sess.Manager.GetHeader().ID, agentruntime.ExecutionAdmissionOptions{Wait: true})
		if admissionErr != nil {
			lease.release()
			return messaging.MessageResponse{}, fmt.Errorf("acquire channel execution admission: %w", admissionErr)
		}
		releaseRuntime = runtimeGuard.Release
		if lease.promoteAfterRuntimeLock() {
			break
		}
		releaseRuntime()
		releaseRuntime = nil
		lease.release()
		lease = nil
	}
	if sess == nil || lease == nil || releaseRuntime == nil || !lease.promoted {
		return messaging.MessageResponse{}, fmt.Errorf("session changed while message was waiting for runtime lock")
	}
	// This is intentionally resolved at execution time as well as on creation
	// and /mode: old bindings, recovery, and external submissions must not
	// inherit a downgraded persisted mode.
	sess.Mode = effectiveChannelMode(msg.Platform, sess.Mode)
	runSource := channelRunSource(sess)
	// A process restart can outlive the progress callback that belonged to the
	// original inbound message. Reconcile completed durable background output
	// before accepting the next message for this session.
	d.reconcileCompletedBackgroundRun(sess, msg.ProgressFunc)

	d.mu.RLock()
	backgroundSubmitter := d.backgroundSubmitter
	d.mu.RUnlock()
	if backgroundSubmitter != nil && d.responsesBackgroundEnabled() {
		backgroundRunID := "channel_" + session.GenerateID()
		input, err := sess.Runtime.AcceptInput(ctx, backgroundRunID, msg.Text, channelAttachmentIngresses(msg))
		if err != nil {
			lease.release()
			releaseRuntime()
			return messaging.MessageResponse{}, fmt.Errorf("accept channel input: %w", err)
		}
		backgroundReq := BackgroundRequest{
			Context: ctx, SessionID: sess.Manager.GetHeader().ID, WorkDir: sess.WorkDir,
			Platform: msg.Platform, UserID: msg.UserID, IdempotencyKey: channelMessageIdempotencyKey(msg), IdempotencyScope: "channel", ModelID: func() string {
				if runtime.model == nil {
					return ""
				}
				return runtime.model.ID
			}(),
			Mode: sess.Mode, RunID: backgroundRunID, Input: input, Progress: msg.ProgressFunc,
		}
		lease.release()
		releaseRuntime()
		runID, err := backgroundSubmitter(backgroundReq)
		if err != nil {
			sess.Runtime.DiscardInput(ctx, input)
			d.notifyRunObserver(sess.Manager.GetHeader().ID)
			return messaging.MessageResponse{}, err
		}
		d.notifyRunObserver(sess.Manager.GetHeader().ID)
		return messaging.MessageResponse{Text: fmt.Sprintf("Responses background run queued: %s", runID)}, nil
	}

	sessionID := sess.Manager.GetHeader().ID
	d.notifyRunObserver(sessionID)
	defer func() {
		lease.release()
		releaseRuntime()
		d.notifyRunObserver(sessionID)
	}()
	sess.Lock()
	defer sess.Unlock()
	sess.Touch()
	if err := sess.Manager.Reload(); err != nil {
		return messaging.MessageResponse{}, fmt.Errorf("reload session before channel run: %w", err)
	}
	runID := "channel_" + session.GenerateID()
	runBase := d.runRootCtx
	if runBase == nil {
		runBase = context.Background()
	}
	if sess.Execution == nil {
		sess.Execution = &agentruntime.ExecutionRuntime{}
	}
	if sess.Runtime == nil {
		return messaging.MessageResponse{}, fmt.Errorf("channel session runtime is unavailable")
	}
	input, err := sess.Runtime.AcceptInput(ctx, runID, msg.Text, channelAttachmentIngresses(msg))
	if err != nil {
		return messaging.MessageResponse{}, fmt.Errorf("accept channel attachments: %w", err)
	}
	requestSnapshot, snapshotErr := json.Marshal(map[string]any{
		"platform": msg.Platform, "userId": msg.UserID, "chatId": msg.ChatID, "message": input.Text,
		"resources": input.Resources,
	})
	if snapshotErr != nil {
		return messaging.MessageResponse{}, snapshotErr
	}
	requestDigest := sha256.Sum256(requestSnapshot)
	requestFP := fmt.Sprintf("sha256:%x", requestDigest[:])
	submissionKey := channelMessageIdempotencyKey(msg)
	if existing, err := agentruntime.FindIdempotentRun(ctx, d.sessionDir, sessionID, submissionKey, requestFP, "channel"); err != nil {
		return messaging.MessageResponse{}, err
	} else if existing != nil {
		// The original inbound event already has a canonical Run. A retry is
		// acknowledged without sending a second channel response.
		return messaging.MessageResponse{}, nil
	}
	sess.Execution.SetRunStore(agentruntime.RunStore{SessionDir: d.sessionDir})
	sess.Execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: d.sessionDir})
	runStartedAt := time.Now()
	modelID := ""
	if runtime.model != nil {
		modelID = runtime.model.ID
	}
	toolNames := make([]string, 0)
	if sess.Registry != nil {
		for _, definition := range sess.Registry.Definitions() {
			if definition.Name != "" {
				toolNames = append(toolNames, definition.Name)
			}
		}
	}
	sort.Strings(toolNames)
	policySnapshot, snapshotErr := json.Marshal(map[string]any{
		"source": runSource, "mode": sess.Mode, "workDir": sess.WorkDir,
		"tools": toolNames, "skills": []string{},
		"capabilities":   map[string]any{"multiAgent": runtime.multiAgent, "browser": runtime.browser, "a2aMaster": runtime.a2aMaster},
		"sandbox":        map[string]any{"enabled": d.sandbox},
		"approvalPolicy": "runtime", "questionPolicy": "runtime",
	})
	if snapshotErr != nil {
		return messaging.MessageResponse{}, snapshotErr
	}
	digest := sha256.Sum256(requestSnapshot)
	intent := agentruntime.ExecutionIntent{ID: "intent_" + session.GenerateID(), SessionID: sessionID, Source: runSource, Model: modelID, Mode: sess.Mode, WorkDir: sess.WorkDir, RequestFingerprint: fmt.Sprintf("sha256:%x", digest[:]), Request: requestSnapshot, Policy: policySnapshot, CreatedAt: runStartedAt}
	userMessage, err := sess.Runtime.BuildUserMessage(runBase, input)
	if err != nil {
		return messaging.MessageResponse{}, err
	}
	startData, _ := json.Marshal(map[string]any{
		"intentId": intent.ID, "attempt": 1,
		"idempotencyKeyHash": agentruntime.IdempotencyKeyFingerprint(submissionKey),
		"idempotencyScope":   "channel", "requestFingerprint": requestFP,
	})
	runCtx, err := sess.Execution.BeginIntentDurable(runBase, intent, agentruntime.DurableRun{
		ID: runID, SessionID: sessionID, IntentID: intent.ID, Attempt: 1, WorkDir: sess.WorkDir,
		Source: runSource, Model: modelID, Mode: sess.Mode,
		InputResourceIDs: input.ResourceIDs(), SubmissionKeyHash: agentruntime.IdempotencyKeyFingerprint(submissionKey),
		SubmissionScope: "channel", SubmissionFingerprint: requestFP,
		UserEntryID: session.RunUserEntryID(runID), UserMessage: &userMessage,
		Status: "running", StartedAt: runStartedAt, ConversationTurnID: "turn-" + intent.ID, ConversationTurn: true,
	}, agentruntime.RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "started", Source: runSource,
		Status: "running", Model: modelID, Mode: sess.Mode, Timestamp: runStartedAt, Data: startData,
	})
	if err != nil {
		return messaging.MessageResponse{}, err
	}
	cancelRun := func() { sess.Execution.Cancel() }
	sess.runStateMu.Lock()
	sess.runID = runID
	sess.runCancel = cancelRun
	sess.runAgent = nil
	sess.runStartedAt = runStartedAt
	sess.lastEventAt = runStartedAt
	sess.runStateMu.Unlock()
	defer func() {
		sess.runStateMu.Lock()
		if sess.runID == runID {
			sess.runID = ""
			sess.runCancel = nil
			sess.runAgent = nil
		}
		sess.runStateMu.Unlock()
		lease.release()
	}()
	result, err := d.runAgent(runCtx, sess, userMessage, msg.ProgressFunc)
	finish := func(runErr error, plan *agentruntime.DeliveryPlan) error {
		if plan != nil {
			if err := sess.Execution.SetDeliveryPlan(runID, *plan); err != nil {
				return err
			}
		}
		status := "completed"
		message := ""
		var errorInfo agentruntime.ErrorInfo
		var incompleteErr incompleteRunError
		if errors.As(runErr, &incompleteErr) {
			status = "incomplete"
		} else if runErr != nil {
			status = "failed"
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				status = "canceled"
			}
			errorInfo = channelFailureInfo(runErr, nil, agentruntime.PhaseModel)
			message = errorInfo.Message
		}
		eventType := "finished"
		if status == "failed" {
			eventType = "failed"
		} else if status == "incomplete" {
			eventType = "incomplete"
		} else if status == "canceled" {
			eventType = "canceled"
		}
		var eventData json.RawMessage
		if message != "" {
			eventData, _ = json.Marshal(map[string]any{"error": message, "errorInfo": errorInfo})
		}
		return sess.Execution.FinishDurableWithRetry(context.Background(), runID, channelRunState(runErr), message, agentruntime.RunEvent{
			SessionID: sessionID, RunID: runID, EventType: eventType,
			Source: runSource, Status: status, Model: modelID, Mode: sess.Mode,
			Timestamp: time.Now(), Data: eventData,
		})
	}
	if err != nil {
		if finishErr := finish(err, nil); finishErr != nil {
			log.Printf("[channels] finish failed Run %s: %v", runID, finishErr)
		}
		return messaging.MessageResponse{}, err
	}
	capability := channelDeliveryCapability(msg.Platform)
	transportContext, _ := json.Marshal(map[string]string{
		"replyMessageId": strings.TrimSpace(msg.MessageID), "replyContext": msg.ReplyContext,
		// Captions are retained in the frozen Runtime projection so a process
		// restart can replay a text operation without reconstructing provider
		// content from adapter-local state. The value is still opaque to the
		// provider and is never included in prompts.
		"caption": result.Text,
	})
	plan, fallbackText, planErr := agentruntime.PlanDelivery(agentruntime.DeliveryPlanRequest{
		SessionID: sessionID, RunID: runID, Platform: msg.Platform, TargetID: msg.ChatID,
		ReplyMessageID: msg.MessageID, TransportContext: transportContext, Caption: result.Text,
		Attachments: result.Artifacts, Capability: capability,
	})
	if planErr != nil {
		if finishErr := finish(planErr, nil); finishErr != nil {
			log.Printf("[channels] finish failed Run %s: %v", runID, finishErr)
		}
		return messaging.MessageResponse{}, planErr
	}
	if fallbackText != "" {
		var frozen map[string]string
		if err := json.Unmarshal(plan.Intent.TransportContext, &frozen); err == nil {
			frozen["fallback"] = fallbackText
			if encoded, marshalErr := json.Marshal(frozen); marshalErr == nil {
				plan.Intent.TransportContext = encoded
			}
		}
	}
	var planPtr *agentruntime.DeliveryPlan
	if len(plan.Operations) > 0 {
		planPtr = &plan
	}
	if finishErr := finish(nil, planPtr); finishErr != nil {
		return messaging.MessageResponse{}, finishErr
	}
	return d.projectDelivery(ctx, sess, msg, result, plan, fallbackText)
}

// channelAttachmentIngresses translates only authenticated transport streams
// into Runtime-owned ingress. It deliberately rejects unknown kinds and never
// treats a user-supplied URL as an attachment download instruction.
func channelAttachmentIngresses(msg messaging.InboundMessage) []agentruntime.InputIngress {
	ingresses := make([]agentruntime.InputIngress, 0, len(msg.Attachments))
	for index, attachment := range msg.Attachments {
		attachment := attachment
		kind := agentruntime.AttachmentKind(attachment.Kind)
		eventID := msg.MessageID
		if eventID == "" {
			eventID = attachment.MessageID
		}
		ingresses = append(ingresses, agentruntime.InputIngress{
			Origin: "channel:" + msg.Platform, EventID: eventID, ItemIndex: index,
			Reference: attachment.Reference, Kind: kind, FilenameHint: attachment.Filename,
			MediaTypeHint: attachment.MediaType, SizeHint: attachment.SizeHint,
			Open: func(ctx context.Context) (agentruntime.InputStream, error) {
				if attachment.Reference == "" || attachment.Open == nil {
					return agentruntime.InputStream{}, fmt.Errorf("channel attachment is missing an authenticated platform reader")
				}
				stream, err := attachment.Open(ctx)
				if err != nil {
					return agentruntime.InputStream{}, err
				}
				return agentruntime.InputStream{
					Reader: stream.Reader, Filename: stream.Filename,
					MediaType: stream.MediaType, ContentSize: stream.ContentSize,
				}, nil
			},
		})
	}
	return ingresses
}

func (d *Dispatcher) projectDelivery(ctx context.Context, sess *ChannelSession, inbound messaging.InboundMessage, result channelRunResult, plan agentruntime.DeliveryPlan, fallbackText string) (messaging.MessageResponse, error) {
	response := messaging.MessageResponse{Text: result.Text}
	if fallbackText != "" {
		if response.Text != "" {
			response.Text += "\n\n"
		}
		response.Text += fallbackText
	}
	if sess == nil || sess.Runtime == nil || sess.Runtime.Attachments == nil || len(plan.Operations) == 0 {
		return response, nil
	}
	controller := newChannelDeliveryController(d.sessionDir)
	var textOperations []session.DeliveryOperation
	for _, operation := range plan.Operations {
		if operation.OperationKind == "send_text" || operation.OperationKind == "send_fallback_text" {
			textOperations = append(textOperations, session.DeliveryOperation{ID: operation.ID, IntentID: plan.Intent.ID, OperationKey: operation.OperationKey, OperationKind: operation.OperationKind, Sequence: operation.Sequence, DependsOn: operation.DependsOn, IdempotencyKey: operation.IdempotencyKey, PayloadDigest: operation.PayloadDigest, Status: operation.Status})
		}
	}
	for _, operation := range textOperations {
		projection := controller.textProjection(operation, plan.Intent, agentruntime.DeliveryOperationText(plan.Intent.TransportContext, operation.OperationKind))
		response.TextDeliveries = append(response.TextDeliveries, *projection)
	}
	if len(response.TextDeliveries) > 0 {
		response.TextDelivery = &response.TextDeliveries[0]
	}
	artifacts := make(map[string]agentruntime.SessionAttachment, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	for _, operation := range plan.Operations {
		if operation.OperationKind != "send_artifact" || operation.ArtifactID == "" {
			continue
		}
		artifact, ok := artifacts[operation.ArtifactID]
		if !ok {
			var err error
			artifact, err = sess.Runtime.Attachments.Get(ctx, plan.Intent.SessionID, operation.ArtifactID)
			if err != nil {
				return response, fmt.Errorf("resolve delivery artifact %s: %w", operation.ArtifactID, err)
			}
		}
		uploadID := operation.DependsOn
		sendOperation := session.DeliveryOperation{ID: operation.ID, IntentID: plan.Intent.ID, OperationKey: operation.OperationKey, OperationKind: operation.OperationKind, Sequence: operation.Sequence, DependsOn: operation.DependsOn, IdempotencyKey: operation.IdempotencyKey, PayloadDigest: operation.PayloadDigest, Status: operation.Status}
		uploadOperation := session.DeliveryOperation{ID: uploadID, IntentID: plan.Intent.ID, OperationKind: "upload_artifact", Status: "pending"}
		for _, candidate := range plan.Operations {
			if candidate.ID == uploadID {
				uploadOperation = session.DeliveryOperation{ID: candidate.ID, IntentID: plan.Intent.ID, OperationKey: candidate.OperationKey, OperationKind: candidate.OperationKind, Sequence: candidate.Sequence, DependsOn: candidate.DependsOn, IdempotencyKey: candidate.IdempotencyKey, PayloadDigest: candidate.PayloadDigest, Status: candidate.Status}
				break
			}
		}
		response.Attachments = append(response.Attachments, controller.attachmentProjection(sess.Runtime, artifact, uploadOperation, sendOperation, plan.Intent))
	}
	return response, nil
}

type channelDeliveryController struct {
	coordinator *agentruntime.DeliveryCoordinator
	mu          sync.Mutex
	claims      map[string]*session.DeliveryOperation
}

func newChannelDeliveryController(sessionDir string) *channelDeliveryController {
	return &channelDeliveryController{coordinator: agentruntime.NewDeliveryCoordinator(sessionDir, "channel-delivery-"+session.GenerateID()), claims: make(map[string]*session.DeliveryOperation)}
}

func (c *channelDeliveryController) claim(ctx context.Context, operationID string) (*session.DeliveryOperation, error) {
	if strings.TrimSpace(operationID) == "" {
		return nil, nil
	}
	c.mu.Lock()
	if existing := c.claims[operationID]; existing != nil {
		copy := *existing
		c.mu.Unlock()
		return &copy, nil
	}
	c.mu.Unlock()
	claimed, err := c.coordinator.Claim(ctx, operationID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, session.ErrDeliveryOperationBusy) {
			current, getErr := session.GetDeliveryOperation(ctx, c.coordinator.SessionDir, operationID)
			if getErr == nil && current != nil && (current.Status == "delivered" || current.Status == "unsupported") {
				return current, nil
			}
		}
		return nil, err
	}
	c.mu.Lock()
	c.claims[operationID] = claimed
	c.mu.Unlock()
	return claimed, nil
}

func (c *channelDeliveryController) complete(ctx context.Context, operation *session.DeliveryOperation, status, providerMessageID, failureCode string) {
	if operation == nil || c.coordinator == nil || operation.LeaseOwner != c.coordinator.Owner || operation.LeaseEpoch <= 0 {
		return
	}
	if err := c.coordinator.Complete(ctx, operation, agentruntime.DeliveryResult{Status: status, ProviderAssetID: operation.ProviderAssetID, ProviderMessageID: providerMessageID, ProviderState: operation.ProviderState, FailureCode: failureCode}); err != nil {
		log.Printf("[channels] update delivery operation %s: %v", operation.ID, err)
	}
	c.mu.Lock()
	delete(c.claims, operation.ID)
	c.mu.Unlock()
}

func (c *channelDeliveryController) progress(ctx context.Context, operation *session.DeliveryOperation, status, providerAssetID, providerMessageID, providerState, failureCode string) {
	if operation == nil || c.coordinator == nil || operation.LeaseOwner != c.coordinator.Owner || operation.LeaseEpoch <= 0 {
		return
	}
	if err := c.coordinator.Progress(ctx, operation, agentruntime.DeliveryResult{
		Status: status, ProviderAssetID: providerAssetID, ProviderMessageID: providerMessageID,
		ProviderState: json.RawMessage(providerState), FailureCode: failureCode,
	}); err != nil {
		log.Printf("[channels] checkpoint delivery operation %s: %v", operation.ID, err)
	}
	operation.Status = status
	operation.ProviderAssetID = providerAssetID
	operation.ProviderMessageID = providerMessageID
	operation.ProviderState = json.RawMessage(providerState)
}

func (c *channelDeliveryController) textProjection(operation session.DeliveryOperation, intent agentruntime.DeliveryIntentPlan, text string) *messaging.OutboundText {
	var transport struct {
		ReplyContext string `json:"replyContext"`
	}
	_ = json.Unmarshal(intent.TransportContext, &transport)
	return &messaging.OutboundText{
		ID: operation.ID, RunID: intent.RunID, TargetID: intent.TargetID,
		ReplyMessageID: intent.ReplyMessageID, ReplyContext: transport.ReplyContext, Text: text,
		Prepare: func(ctx context.Context) error {
			_, err := c.claim(ctx, operation.ID)
			return err
		},
		Complete: func(ctx context.Context, status, providerMessageID, failureCode string) {
			claimed, err := c.claim(ctx, operation.ID)
			if err != nil {
				log.Printf("[channels] claim text operation %s: %v", operation.ID, err)
				return
			}
			c.complete(ctx, claimed, status, providerMessageID, failureCode)
		},
	}
}

func (c *channelDeliveryController) attachmentProjection(runtime *agentruntime.SessionRuntime, artifact agentruntime.SessionAttachment, upload, send session.DeliveryOperation, intent agentruntime.DeliveryIntentPlan) messaging.OutboundAttachment {
	prepared := false
	var mu sync.Mutex
	var transport struct {
		ReplyContext string `json:"replyContext"`
	}
	_ = json.Unmarshal(intent.TransportContext, &transport)
	prepare := func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if prepared {
			return nil
		}
		if upload.ID != "" {
			if _, err := c.claim(ctx, upload.ID); err != nil {
				return err
			}
		}
		prepared = true
		return nil
	}
	return messaging.OutboundAttachment{
		ID: artifact.ID, RunID: intent.RunID, TargetID: intent.TargetID, ReplyContext: transport.ReplyContext,
		UploadOperationID: upload.ID, SendOperationID: send.ID,
		Kind: messaging.AttachmentKind(artifact.Kind), Filename: artifact.Filename, MediaType: artifact.MediaType,
		ProviderAssetID: upload.ProviderAssetID, ProviderState: append(json.RawMessage(nil), upload.ProviderState...),
		Prepare: prepare,
		ProgressUpload: func(ctx context.Context, status, providerAssetID, providerState, failureCode string) {
			if uploadClaim, err := c.claim(ctx, upload.ID); err == nil {
				c.progress(ctx, uploadClaim, status, providerAssetID, "", providerState, failureCode)
			}
		},
		CompleteUpload: func(ctx context.Context, status, providerAssetID, providerState, failureCode string) {
			if uploadClaim, err := c.claim(ctx, upload.ID); err == nil {
				uploadClaim.ProviderAssetID = providerAssetID
				uploadClaim.ProviderState = json.RawMessage(providerState)
				c.complete(ctx, uploadClaim, status, "", failureCode)
			}
		},
		PrepareSend: func(ctx context.Context) error {
			_, err := c.claim(ctx, send.ID)
			return err
		},
		CompleteSend: func(ctx context.Context, status, providerMessageID, providerState, failureCode string) {
			if sendClaim, err := c.claim(ctx, send.ID); err == nil {
				sendClaim.ProviderState = json.RawMessage(providerState)
				c.complete(ctx, sendClaim, status, providerMessageID, failureCode)
			}
		},
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			_, reader, err := runtime.Attachments.Open(ctx, artifact.SessionID, artifact.ID)
			return reader, err
		},
		Complete: func(ctx context.Context, status, providerMessageID, failureCode string) {
			if uploadClaim, err := c.claim(ctx, upload.ID); err == nil {
				c.complete(ctx, uploadClaim, status, "", failureCode)
			}
			if sendClaim, err := c.claim(ctx, send.ID); err == nil {
				c.complete(ctx, sendClaim, status, providerMessageID, failureCode)
			}
		},
	}
}

func channelDeliveryCapability(platform string) agentruntime.DeliveryCapability {
	capability := agentruntime.DeliveryCapability{Text: true}
	if strings.EqualFold(strings.TrimSpace(platform), "wechat") {
		capability.SendImage, capability.SendFile, capability.SendVideo = true, true, true
	}
	if strings.EqualFold(strings.TrimSpace(platform), "feishu") {
		capability.SendImage, capability.SendFile = true, true
	}
	return capability
}

func channelRunState(runErr error) agentruntime.RunState {
	if errors.Is(runErr, context.DeadlineExceeded) {
		return agentruntime.RunStateTimedOut
	}
	if errors.Is(runErr, context.Canceled) {
		return agentruntime.RunStateCancelled
	}
	var incompleteErr incompleteRunError
	if errors.As(runErr, &incompleteErr) {
		return agentruntime.RunStateIncomplete
	}
	if runErr != nil {
		return agentruntime.RunStateFailed
	}
	return agentruntime.RunStateCompleted
}
func channelMessageIdempotencyKey(msg messaging.InboundMessage) string {
	// Scope provider event IDs to the channel identity; different platforms can
	// legitimately reuse the same native ID.
	platform := strings.TrimSpace(msg.Platform)
	userID := strings.TrimSpace(msg.UserID)
	if messageID := strings.TrimSpace(msg.MessageID); messageID != "" {
		return "channel:" + platform + ":" + userID + ":" + messageID
	}
	// Some transports omit a native event ID. Hash the stable envelope and
	// attachment references so retries share one submission reservation without
	// persisting or logging opaque transport values.
	var b strings.Builder
	b.WriteString(platform)
	b.WriteByte(0)
	b.WriteString(userID)
	b.WriteByte(0)
	b.WriteString(strings.TrimSpace(msg.ChatID))
	b.WriteByte(0)
	b.WriteString(msg.Text)
	b.WriteByte(0)
	if !msg.Timestamp.IsZero() {
		b.WriteString(msg.Timestamp.UTC().Format(time.RFC3339Nano))
	}
	for _, attachment := range msg.Attachments {
		b.WriteByte(0)
		b.WriteString(strings.TrimSpace(attachment.MessageID))
		b.WriteByte(0)
		b.WriteString(strings.TrimSpace(attachment.Reference))
	}
	if b.Len() == 0 {
		return ""
	}
	digest := sha256.Sum256([]byte(b.String()))
	return "channel:" + platform + ":" + userID + ":fallback:" + hex.EncodeToString(digest[:])
}

func (d *Dispatcher) acquireCommandSession(platform, userID string) (*ChannelSession, func(), error) {
	for attempt := 0; attempt < 3; attempt++ {
		sess, err := d.resolveSession(platform, userID)
		if err != nil {
			return nil, nil, err
		}
		lease, ok := d.acquireSessionLease(sessionKey(platform, userID), platform, userID, sess)
		if !ok {
			continue
		}
		runtimeGuard, err := agentruntime.AcquireSessionMutation(context.Background(), d.sessionDir, sess.Manager.GetHeader().ID, agentruntime.ExecutionAdmissionOptions{Wait: true})
		if err != nil {
			lease.release()
			return nil, nil, err
		}
		releaseRuntime := runtimeGuard.Release
		if lease.promoteAfterRuntimeLock() {
			return sess, func() {
				releaseRuntime()
				lease.release()
			}, nil
		}
		releaseRuntime()
		lease.release()
	}
	return nil, nil, fmt.Errorf("session changed while waiting for runtime lock")
}

// RequestSessionStop projects the shared Runtime stop operation for channel
// commands. Channel-local maps are notification state only and do not decide
// ownership or whether a Run exists.
func (d *Dispatcher) RequestSessionStop(ctx context.Context, sessionID string) (agentruntime.SessionStopResult, error) {
	if d == nil || sessionID == "" {
		return agentruntime.SessionStopResult{}, fmt.Errorf("session ID is required")
	}
	result, err := agentruntime.RequestSessionStop(ctx, d.sessionDir, sessionID, agentruntime.SessionStopOptions{
		LegacyLocalCancel: d.legacyLocalCancelHook(sessionID),
	})
	d.notifyRunObserver(sessionID)
	return result, err
}

// legacyLocalCancelHook is the only remaining bridge for channel fixtures and
// older embedded integrations that kept a run solely in process memory. The
// Runtime has already inspected the durable facts before invoking this hook;
// a durable/external Run therefore cannot be cancelled through this path.
func (d *Dispatcher) legacyLocalCancelHook(sessionID string) func() bool {
	return func() bool {
		if d == nil || sessionID == "" {
			return false
		}
		d.mu.RLock()
		var target *ChannelSession
		for _, sess := range d.sessions {
			if sess != nil && sess.ID == sessionID {
				target = sess
				break
			}
		}
		d.mu.RUnlock()
		if target == nil {
			return false
		}
		target.runStateMu.Lock()
		runID, cancel, runningAgent := target.runID, target.runCancel, target.runAgent
		target.runStateMu.Unlock()
		if runID == "" || cancel == nil {
			return false
		}
		cancel()
		if runningAgent != nil {
			runningAgent.Abort()
		}
		return true
	}
}

// CancelChannelSessionRun is retained for compatibility with callers that
// only need an accepted/not-accepted answer.
func (d *Dispatcher) CancelChannelSessionRun(sessionID string) bool {
	result, err := d.RequestSessionStop(context.Background(), sessionID)
	if err != nil {
		return false
	}
	switch result.Code {
	case agentruntime.SessionStopAccepted, agentruntime.SessionStopRemoteAccepted, agentruntime.SessionStopRecoveryStarted:
		return true
	default:
		return false
	}
}

// resolveSession finds or creates the active session for a platform user.
func (d *Dispatcher) resolveSession(platform, userID string) (*ChannelSession, error) {
	key := sessionKey(platform, userID)

	d.mu.RLock()
	if sess, ok := d.sessions[key]; ok {
		d.mu.RUnlock()
		log.Printf("[channels] session reused: %s", key)
		return sess, nil
	}
	d.mu.RUnlock()

	log.Printf("[channels] session not found in cache, creating: %s", key)

	// Create or load session
	d.mu.Lock()
	defer d.mu.Unlock()

	// Double-check after acquiring write lock
	if sess, ok := d.sessions[key]; ok {
		log.Printf("[channels] session found after write lock: %s", key)
		return sess, nil
	}

	cfg := d.cfg
	security := d.security
	sandboxEnabled := d.sandbox
	browserEnabled := d.browser
	artifactEnabled := d.artifact
	a2aEnabled := d.a2aMaster
	multiAgentEnabled := d.multiAgent
	cronStore := d.cronStore
	scheduler := d.scheduler
	workDir := cfg.GetPlatformWorkDir(platform)
	if security != nil {
		if err := security.CheckWorkDirAllowed(workDir); err != nil {
			return nil, err
		}
	}
	var mgr *session.Manager
	var bound *session.Binding
	var err error
	if platform == "wechat" || platform == "feishu" {
		bound, err = session.FindBinding(d.sessionDir, platform, userID)
		if err != nil {
			return nil, fmt.Errorf("find channel binding: %w", err)
		}
	}
	if bound != nil {
		mgr, err = agentruntime.OpenSession(d.sessionDir, bound.SessionID)
		if err != nil {
			return nil, fmt.Errorf("open bound session: %w", err)
		}
	} else if platform == "wechat" || platform == "feishu" {
		mgr, err = agentruntime.CreateSession(agentruntime.CreateSessionOptions{
			WorkDir: workDir, SessionDir: d.sessionDir, ChannelType: platform, ChannelID: userID,
		})
		if err != nil {
			return nil, fmt.Errorf("create bound session: %w", err)
		}
	} else {
		mgr, err = agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: workDir, SessionDir: d.sessionDir})
		if err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		}
	}

	sbMgr := sandbox.NewManagerWithOptions(workDir, d.settings.Sandbox.Options())
	if sandboxEnabled {
		if err := sbMgr.SetLevel(sandbox.LevelStandard); err != nil {
			return nil, fmt.Errorf("enable sandbox: %w", err)
		}
		if err := sbMgr.FallbackError(); err != nil {
			log.Printf("[channels] sandbox unavailable; using direct execution: %v", err)
		}
	} else {
		_ = sbMgr.SetLevel(sandbox.LevelNone)
	}
	configured, toolErr := session.ListChannelTools(d.sessionDir, mgr.GetHeader().ID)
	if toolErr != nil {
		return nil, fmt.Errorf("load channel tools: %w", toolErr)
	}
	enabled := make(map[string]bool, len(configured))
	for _, item := range configured {
		enabled[item.ToolName] = item.Enabled
	}
	hasToolConfig := len(configured) > 0
	definitions := make(map[string]ChannelToolDefinition)
	for _, definition := range d.channelToolDefinitionsLocked(platform) {
		definitions[definition.Name] = definition
	}
	toolEnabled := func(name string, defaultEnabled bool) bool {
		if definition, ok := definitions[name]; ok {
			if !definition.Available {
				return false
			}
			defaultEnabled = definition.Default
		}
		if value, ok := enabled[name]; ok {
			return value
		}
		return !hasToolConfig && defaultEnabled
	}
	// Resolve the browser capability once from the channel session selection.
	// The same value must drive both the initial registry and the Runtime
	// resource attachment; otherwise AttachSessionResources rehydrates with the
	// process default and can remove an explicitly selected browser tool.
	selectedBrowser := toolEnabled("browser", browserEnabled)

	resources, err := agentruntime.LoadContextResources(d.settings, workDir, false, selectedBrowser)
	if err != nil {
		return nil, fmt.Errorf("load channel context resources: %w", err)
	}
	reg, err := agentruntime.BuildRegistry(workDir, sbMgr, d.settings, agentruntime.RegistryPolicy{
		RegisterDefaults: true,
		Browser:          selectedBrowser,
		Mutators: []agentruntime.RegistryMutator{func(reg *tools.Registry) error {
			for _, item := range reg.All() {
				if !toolEnabled(item.Name(), true) {
					reg.Remove(item.Name())
				}
			}
			if toolEnabled("a2a_dispatch", a2aEnabled) {
				if err := d.registerA2AMasterTool(reg); err != nil {
					return err
				}
			}
			if toolEnabled("memory", true) {
				reg.Register(memory.NewMemoryTool(memory.NewStore(cfg.Memory.Path, workDir)))
			}
			registerMultiAgent := false
			for name := range definitions {
				if isMultiAgentToolName(name) && toolEnabled(name, multiAgentEnabled) {
					registerMultiAgent = true
					break
				}
			}
			if registerMultiAgent || agentruntime.SessionHasTeamExpert(workDir, mgr) {
				manager := d.ensureAgentManager()
				agent.RegisterSubAgentTools(reg, manager)
				agent.RegisterDelegateSubAgentTool(reg, manager)
				workflow.RegisterTools(reg, manager, nil)
			}
			if cronStore != nil && toolEnabled("cron", true) {
				sessionID := ""
				if header := mgr.GetHeader(); header != nil {
					sessionID = header.ID
				}
				reg.Register(cron.NewCronTool(cron.NewSessionScopedStoreWithWorkDir(cronStore, sessionID, workDir), scheduler))
			}
			return nil
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("build channel registry: %w", err)
	}

	sessionRuntime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		Source: agentruntime.SourceFromChannelType(platform), WorkDir: workDir, Manager: mgr, Registry: reg,
		SandboxMgr: sbMgr, SkillsMgr: resources.SkillsMgr, ExtraContext: resources.ExtraContext,
		RuleContent: resources.RuleContent, Settings: d.settings, Browser: selectedBrowser,
		ArtifactEnabled: artifactEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("attach channel session runtime: %w", err)
	}
	sessionAgentMgr := d.newSessionAgentManager(sessionRuntime, dispatcherRuntimeSnapshot{
		settings: d.settings, provider: d.provider, providerName: d.providerName, model: d.model, allow: d.allow,
		multiAgent: multiAgentEnabled,
	})
	if err := sessionRuntime.ConnectConfiguredMCP(context.Background(), agentruntime.MCPPolicy{
		Optional: true,
		OnError:  func(err error) { log.Printf("[channels] connect MCP servers: %v", err) },
	}); err != nil {
		return nil, fmt.Errorf("connect channel MCP servers: %w", err)
	}
	sessionRuntime.SetExecution(nil)
	mcpClients := sessionRuntime.MCPClients
	if len(mcpClients) > 0 {
		log.Printf("[channels] connected %d MCP server(s) for %s/%s", len(mcpClients), platform, userID)
	}

	if platform == "wechat" || platform == "feishu" {
		for _, item := range reg.All() {
			if value, ok := enabled[item.Name()]; ok && !value {
				reg.Remove(item.Name())
			}
		}
	}
	if sessionAgentMgr != nil {
		// The session-scoped manager owns this session's member mailbox, so member
		// questions and completions reach this session's lead (and subagent_wait
		// observes the same mailbox). Re-register after channel-specific removals so
		// the sub-agent tools never stay attached to the dispatcher-wide manager,
		// which is shared across sessions and has no session mailbox.
		//
		// A team binding is a Runtime policy capability (not an adapter-local
		// toggle), so its full toolset is authoritative. An ordinary multi-agent
		// selection is re-pointed one by one instead: RegisterSubAgentTools replaces
		// the whole canonical set, and resurrecting a tool the user switched off
		// would override an explicit per-tool choice.
		if sessionRuntime.TeamExpertActive() {
			agent.RegisterSubAgentTools(reg, sessionAgentMgr)
		} else if selected := selectedSubAgentTools(reg); len(selected) > 0 {
			agent.RegisterSubAgentTools(reg, sessionAgentMgr)
			for _, name := range agent.SubAgentToolNames() {
				if !selected[name] {
					reg.Remove(name)
				}
			}
		}
	}
	sess := &ChannelSession{
		Execution:  &agentruntime.ExecutionRuntime{},
		Decisions:  &agentruntime.DecisionService{},
		Runtime:    sessionRuntime,
		ID:         mgr.GetHeader().ID,
		Platform:   platform,
		UserID:     userID,
		WorkDir:    workDir,
		Manager:    mgr,
		Registry:   reg,
		AgentMgr:   sessionAgentMgr,
		SandboxMgr: sbMgr,
		MCPClients: mcpClients,
		Mode:       "yolo",
		LastUsed:   time.Now(),
	}
	sessionRuntime.SetExecution(sess.Execution)
	sess.Execution.SetRunStore(agentruntime.RunStore{SessionDir: d.sessionDir})
	sess.Execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: d.sessionDir})
	if generation, generationErr := session.GetChannelToolGeneration(d.sessionDir, mgr.GetHeader().ID); generationErr == nil {
		sess.generation = generation
	} else {
		log.Printf("[channels] load tool generation for %s: %v", key, generationErr)
	}

	d.sessions[key] = sess
	log.Printf("[channels] session created: %s (workDir=%s)", key, workDir)
	return sess, nil
}

// RotateSession archives the current session and creates a new one.
// Called when user sends /new. force requests cancellation of the active run
// and allows rotating past a run that ignored cancellation.
func (d *Dispatcher) RotateSession(platform, userID string, force bool) error {
	key := sessionKey(platform, userID)
	log.Printf("[channels] rotating session: %s (force=%v)", key, force)
	if platform != "wechat" && platform != "feishu" {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.sessions, key)
		return nil
	}
	for {
		bound, err := session.FindBinding(d.sessionDir, platform, userID)
		if err != nil {
			return fmt.Errorf("find channel binding: %w", err)
		}
		if bound == nil {
			return nil
		}
		releaseRuntime, err := d.AcquireRuntimeForRotate(context.Background(), d.sessionDir, bound.SessionID, force)
		if err != nil {
			return err
		}
		releaseIdentity := func() {}
		if d.identityLocks != nil {
			releaseIdentity = d.identityLocks.Lock(platform, userID)
		}
		current, readErr := session.FindBinding(d.sessionDir, platform, userID)
		if readErr != nil {
			releaseIdentity()
			releaseRuntime()
			return fmt.Errorf("recheck channel binding: %w", readErr)
		}
		if current == nil {
			releaseIdentity()
			releaseRuntime()
			return nil
		}
		if current.SessionID != bound.SessionID {
			releaseIdentity()
			releaseRuntime()
			continue
		}
		workDir := d.PlatformWorkDir(platform)
		if _, rotateErr := session.RotateBoundSession(workDir, d.sessionDir, platform, userID, current.SessionID); rotateErr != nil {
			releaseIdentity()
			releaseRuntime()
			return fmt.Errorf("rotate bound session: %w", rotateErr)
		}
		d.mu.Lock()
		if sess, ok := d.sessions[key]; ok {
			d.invalidateSessionLocked(key, sess)
		}
		d.mu.Unlock()
		releaseIdentity()
		releaseRuntime()
		return nil
	}
}

// RotateForceGrace is how long a forced rotation waits for the active run to
// release the runtime lock after cancellation was requested.
const RotateForceGrace = 10 * time.Second

// ErrSessionRunBusy reports that a session runtime lock is held by an active
// run. It is shared by the channel rotation paths so callers get a consistent
// busy signal (and a consistent hint about /stop and /new force).
var ErrSessionRunBusy = errors.New("session has an active run")

// AcquireRuntimeForRotate takes an explicit mutation lease for a rotation.
// sessionDir is the lifecycle owner's authoritative session directory; an
// empty value falls back to the dispatcher's configured directory.
// With force it requests cancellation of a local channel run and waits a
// bounded grace period. It never mutates an externally-owned Session without
// the durable lease.
func (d *Dispatcher) AcquireRuntimeForRotate(ctx context.Context, sessionDir, sessionID string, force bool) (func(), error) {
	if strings.TrimSpace(sessionDir) == "" {
		sessionDir = d.sessionDir
	}
	guard, err := agentruntime.AcquireSessionMutation(ctx, sessionDir, sessionID, agentruntime.ExecutionAdmissionOptions{})
	if err == nil {
		return guard.Release, nil
	}
	if !force {
		return nil, ErrSessionRunBusy
	}
	d.CancelChannelSessionRun(sessionID)
	release, ok := AwaitRuntimeRelease(ctx, sessionDir, sessionID, RotateForceGrace)
	if ok {
		return release, nil
	}
	return nil, ErrSessionRunBusy
}

// AwaitRuntimeRelease waits for an explicit mutation lease until the grace
// period elapses. Orphaned local runs are reconciled before it returns.
func AwaitRuntimeRelease(ctx context.Context, sessionDir, sessionID string, grace time.Duration) (func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	waitCtx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	guard, err := agentruntime.AcquireSessionMutation(waitCtx, sessionDir, sessionID, agentruntime.ExecutionAdmissionOptions{Wait: true, PollInterval: 200 * time.Millisecond})
	if err != nil {
		return nil, false
	}
	return guard.Release, true
}

// GetSession returns a session by key, or nil if not found.
func (d *Dispatcher) GetSession(key string) *ChannelSession {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sessions[key]
}

// ListSessions returns all active session keys.
func (d *Dispatcher) ListSessions() []*ChannelSession {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]*ChannelSession, 0, len(d.sessions))
	for _, s := range d.sessions {
		result = append(result, s)
	}
	return result
}

// RefreshBinding invalidates the cached runtime route for a channel identity.
// The next inbound message will resolve the identity from the canonical binding
// stored in the root sessions database.
func (d *Dispatcher) RefreshBinding(platform, userID string) {
	if d == nil || userID == "" {
		return
	}
	d.RemoveSession(sessionKey(platform, userID))
}

// RefreshSessionTools drops the cached channel session so its registry is rebuilt
// from the latest persisted tool configuration on the next message.
func (d *Dispatcher) RefreshSessionTools(sessionID string) {
	if d == nil || sessionID == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, sess := range d.sessions {
		if sess.Manager != nil && sess.Manager.GetHeader() != nil && sess.Manager.GetHeader().ID == sessionID {
			d.invalidateSessionLocked(key, sess)
			return
		}
	}
}

// RemoveSession removes a session from the pool.
func (d *Dispatcher) RemoveSession(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sess, ok := d.sessions[key]; ok {
		d.invalidateSessionLocked(key, sess)
	}
}

func (d *Dispatcher) invalidateSessionLocked(key string, sess *ChannelSession) {
	if sess == nil {
		delete(d.sessions, key)
		return
	}
	busy := sess.pendingEntrants > 0 || sess.activeRuns > 0
	if busy {
		sess.invalidated = true
	}
	if busy {
		return
	}
	d.closeAndDeleteLocked(key, sess)
}

func (d *Dispatcher) closeAndDeleteLocked(key string, sess *ChannelSession) {
	if sess == nil {
		delete(d.sessions, key)
		return
	}
	if sess.Runtime != nil {
		sess.Runtime.Close()
		sess.MCPClients = nil // legacy alias is released by Runtime.
	} else if len(sess.MCPClients) > 0 {
		agentruntime.CloseMCPClients(sess.MCPClients)
		sess.MCPClients = nil
	}
	for agentID, sessionID := range d.agentSessions {
		if sessionID == sess.ID {
			delete(d.agentSessions, agentID)
		}
	}
	delete(d.sessions, key)
}

func (d *Dispatcher) evictInvalidated(key string, target *ChannelSession) {
	d.mu.Lock()
	defer d.mu.Unlock()
	current := d.sessions[key]
	if current != target {
		return
	}
	d.closeAndDeleteLocked(key, target)
}

// Close cancels all channel runs and releases idle session resources.
func (d *Dispatcher) Close() {
	if d == nil {
		return
	}
	if d.runRootStop != nil {
		d.runRootStop()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, sess := range d.sessions {
		d.invalidateSessionLocked(key, sess)
	}
}

// buildAgent creates and configures an agent for a session.
// Returns the agent and a cleanup function to call with the run error.
func (d *Dispatcher) buildAgent(ctx context.Context, sess *ChannelSession, approvalHandler agentApprovalHandler) (*agent.Agent, func(error)) {
	runtime := d.runtimeSnapshot()
	cfg := runtime.cfg
	settings := runtime.settings
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if settings == nil {
		settings = config.DefaultSettings()
	}
	if sess.Runtime == nil {
		// Compatibility for adapter-owned test fixtures during the transition.
		resources, resourceErr := agentruntime.LoadContextResources(settings, sess.WorkDir, false, runtime.browser)
		if resourceErr != nil {
			return nil, func(error) {}
		}
		runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
			ID: sess.ID, Source: agentruntime.SourceFromChannelType(sess.Platform), WorkDir: sess.WorkDir,
			Manager: sess.Manager, Registry: sess.Registry, SandboxMgr: sess.SandboxMgr, MCPClients: sess.MCPClients,
			SkillsMgr: resources.SkillsMgr, ExtraContext: resources.ExtraContext, RuleContent: resources.RuleContent,
			Settings: settings, Browser: runtime.browser, ArtifactEnabled: runtime.artifact,
		})
		if err != nil {
			return nil, func(error) {}
		}
		sess.Runtime = runtime
	}

	// Prompt gating flags must reflect the tools actually present in the
	// session registry. Per-session tool config can enable or disable
	// sub-agent/delegate/workflow tools individually (and wechat/feishu
	// sessions drop explicitly disabled tools), so derive the flags from the
	// registry instead of the dispatcher-level multiAgent flag alone.
	hasTool := func(name string) bool {
		if sess.Registry == nil {
			return false
		}
		_, ok := sess.Registry.Get(name)
		return ok
	}

	sess.runStateMu.Lock()
	activeRunID := sess.runID
	sess.runStateMu.Unlock()
	intentID := ""
	if activeRunID != "" {
		if run, err := agentruntime.GetDurableRun(ctx, d.sessionDir, activeRunID); err == nil && run != nil {
			intentID = run.IntentID
		}
	}
	a, err := sess.Runtime.BuildAgent(agentruntime.AgentBuildOptions{
		Provider: runtime.provider, ProviderName: runtime.providerName, Model: runtime.model,
		Settings: settings, Allow: runtime.allow, Mode: sess.Mode,
		ThinkingLevel: provider.ThinkingLevel(settings.DefaultThinkingLevel),
		MultiAgent:    hasTool("subagent_spawn"), DelegateMode: hasTool("delegate_subagent"),
		Workflows: hasTool("workflow_run"), ApprovalHandler: approvalHandler,
		GetSteeringMessages: d.esmSteeringMessages(sess),
		ConversationTurnID:  "turn-" + intentID, IntentID: intentID, RunID: activeRunID,
		ConversationTurn: true, RuntimeOwnsTurnEnd: true,
		MaxIterations: cfg.Agent.MaxTurns, ContextPressure: cfg.Agent.ContextPressureThreshold,
		BudgetPressure: cfg.Agent.BudgetPressureThreshold,
		BeforeToolCall: func(toolCtx agent.BeforeToolCallContext) *agent.ToolCallBlockResult {
			current := d.runtimeSnapshot()
			if current.hooksMgr == nil || !current.hooksMgr.HasPreHook() {
				return nil
			}
			args, _ := toolCtx.Args.(map[string]any)
			allowed, reason, hookErr := current.hooksMgr.PreToolCall(ctx, toolCtx.ToolCall.Name, args, sess.Platform, sess.UserID)
			if hookErr != nil || allowed {
				return nil
			}
			if reason == "" {
				reason = "Tool execution blocked by channel pre-tool hook"
			}
			return &agent.ToolCallBlockResult{Block: true, Reason: reason}
		},
		AfterToolCall: func(ctx2 agent.AfterToolCallContext) *agent.ToolCallResult {
			if runtime.hooksMgr != nil && runtime.hooksMgr.HasPostHook() {
				argsMap, _ := ctx2.Args.(map[string]any)
				errMsg := ""
				if ctx2.IsError {
					errMsg = ctx2.Result.Content
				}
				runtime.hooksMgr.PostToolCall(ctx, ctx2.ToolCall.Name, argsMap, ctx2.Result.Content, errMsg, sess.Platform, sess.UserID)
			}
			return nil
		},
	})
	if err != nil {
		return nil, func(error) {}
	}

	var runErr error
	agentMgr := sess.AgentMgr
	if agentMgr == nil {
		agentMgr = runtime.agentMgr
	}
	if agentMgr != nil {
		agentMgr.Register(agent.NewAgentAdapter(a))
		d.mu.Lock()
		if d.agentSessions == nil {
			d.agentSessions = make(map[string]string)
		}
		d.agentSessions[string(a.ID())] = sess.ID
		d.mu.Unlock()
	}
	cleanup := func(err error) {
		runErr = err
		if agentMgr != nil {
			// Finish first: terminal child transitions fired from it must still
			// resolve this root agent to its session.
			agentMgr.Finish(a.ID(), runErr)
			d.releaseAgentSession(a.ID())
		}
	}

	if sess.ForceCompact {
		a.SetForceCompact()
		sess.ForceCompact = false
	}

	if replayState := sess.Manager.GetReplayState(); len(replayState.Messages) > 0 {
		a.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
	}

	return a, cleanup
}

func (d *Dispatcher) compactSession(ctx context.Context, sess *ChannelSession) error {
	a, cleanup := d.buildAgent(ctx, sess, nil)
	defer cleanup(nil)

	eventCh := make(chan agent.Event, 16)
	if err := a.CompactForced(ctx, eventCh); err != nil {
		return err
	}
	for len(eventCh) > 0 {
		<-eventCh
	}
	return nil
}

func (d *Dispatcher) channelDecisionService(sess *ChannelSession) *agentruntime.DecisionService {
	if sess == nil {
		return nil
	}
	if sess.Decisions == nil {
		sess.Decisions = &agentruntime.DecisionService{}
		if sess.Runtime != nil {
			sess.Runtime.SetDecisions(sess.Decisions)
		}
	}
	return sess.Decisions
}

func (d *Dispatcher) registerChannelDecision(sess *ChannelSession, id string, kind agentruntime.DecisionKind) {
	if sess == nil || id == "" {
		return
	}
	_ = d.channelDecisionService(sess).Register(agentruntime.DecisionRequest{
		ID: id, RunID: sess.runID, SessionID: sess.ID, Kind: kind,
	})
}

func (d *Dispatcher) clearChannelDecisions(sess *ChannelSession) {
	if sess == nil || sess.Decisions == nil {
		return
	}
	for _, request := range sess.Decisions.ClearRunWithValue(sess.runID, "") {
		d.persistChannelDecision(sess, request.ID, request.Kind, "cancelled", "", map[string]any{
			"reason": "channel run ended before the decision was resolved",
		})
	}
}

// messagingApprovalHandler returns an ApprovalHandler for messaging platforms.
// Medium risk → auto-approve + notify; high risk → auto-reject + notify.
func (d *Dispatcher) messagingApprovalHandler(ctx context.Context, sess *ChannelSession, progress func(string)) agentApprovalHandler {
	runtime := d.runtimeSnapshot()
	return func(toolCallID, toolName string, args map[string]any) bool {
		d.registerChannelDecision(sess, toolCallID, agentruntime.DecisionApproval)
		defer func() {
			if sess.Decisions != nil {
				if _, err := sess.Decisions.ResolveWith(agentruntime.DecisionResolution{ID: toolCallID, Kind: agentruntime.DecisionApproval, Status: "resolved"}, func(_ agentruntime.DecisionRequest) error {
					return d.persistChannelDecision(sess, toolCallID, agentruntime.DecisionApproval, "resolved", "", nil)
				}); err != nil {
					log.Printf("[channels] resolve decision %s: %v", toolCallID, err)
				}
			}
		}()
		if toolName == "git_access" {
			if progress != nil {
				progress("⛔ Git metadata access is not available in unattended channel sessions")
			}
			return false
		}
		if runtime.security != nil && runtime.security.ShouldAutoApprove(toolName, args, sess.Mode) {
			return true
		}

		risk := "medium"
		if toolName == "bash" {
			if cmd, ok := args["command"]; ok {
				risk = CommandRiskLevel(fmt.Sprintf("%v", cmd))
			}
		}

		if risk == "medium" {
			if progress != nil {
				progress(FormatApprovalNotification(toolName, args, risk, true))
			}
			return true
		}

		if progress != nil {
			progress(FormatApprovalNotification(toolName, args, risk, false))
		}
		return false
	}
}

type channelRunResult struct {
	Text      string
	Artifacts []agentruntime.SessionAttachment
}

// runAgent executes the agent loop synchronously (for messaging platforms).
func (d *Dispatcher) runAgent(ctx context.Context, sess *ChannelSession, userMessage provider.Message, progress func(string)) (channelRunResult, error) {
	if sess == nil || sess.Runtime == nil {
		return channelRunResult{}, fmt.Errorf("channel session runtime is unavailable")
	}
	artifacts, err := sess.Runtime.BeginArtifactCollection(sess.runID)
	if err != nil {
		return channelRunResult{}, fmt.Errorf("begin runtime artifact collection: %w", err)
	}
	defer artifacts.Close()
	a, cleanup := d.buildAgent(ctx, sess, d.messagingApprovalHandler(ctx, sess, progress))
	if a == nil {
		return channelRunResult{}, fmt.Errorf("build channel agent failed")
	}
	var runErr error
	defer cleanup(runErr)
	defer d.clearChannelDecisions(sess)

	if sess.Execution != nil {
		sess.Execution.SetAgent(a)
	}
	// Publish the agent handle so cancellation and the watchdog can abort waits
	// that do not observe the run context.
	sess.runStateMu.Lock()
	if sess.runID != "" && sess.runAgent == nil {
		sess.runAgent = a
	}
	sess.runStateMu.Unlock()

	eventCh := a.RunWithUserMessage(ctx, userMessage)

	var response strings.Builder
	var thinkBuf strings.Builder
	var eventCount int
	var toolCount int
	var attachments []provider.Attachment
	terminalSeen := false
	var terminalInfo *agentruntime.ErrorInfo
	pendingToolArgs := make(map[string]map[string]any) // ToolCallID → args
	flushThink := func() {
		if progress != nil && thinkBuf.Len() > 0 {
			text := thinkBuf.String()
			if len(text) > 500 {
				text = text[:500] + "..."
			}
			progress("💭 " + text)
			thinkBuf.Reset()
		}
	}
	for ev := range eventCh {
		eventCount++
		sess.runStateMu.Lock()
		sess.lastEventAt = time.Now()
		sess.runStateMu.Unlock()
		// Child-agent events are progress notifications, not events from the
		// channel's main agent. Never append child text to the main response or
		// treat a child timeout as a failure of the parent run.
		if ev.AgentID != "" {
			sessionID := sess.Manager.GetHeader().ID
			if ev.Error != nil && (ev.Type == agent.EventError || ev.Type == agent.EventRunFinished) {
				log.Printf("[channels] Sub-agent %s for %s/%s failed: %v", ev.AgentID, sess.Platform, sess.UserID, ev.Error)
			}
			d.notifySubAgentObserver(sessionID, channelSafeSubAgentEvent(ev))
			d.notifyRunObserver(sessionID)
			if ev.Type == agent.EventError && progress != nil && ev.Error != nil {
				info := channelFailureInfo(ev.Error, nil, agentruntime.PhaseModel)
				progress(fmt.Sprintf("⚠️ Sub-agent %s: %s", ev.AgentID, agentruntime.DisplayErrorMessage(info)))
			}
			continue
		}
		if sess.Execution != nil {
			observation, observeErr := sess.Execution.ObserveAgentEvent(ev)
			if observeErr != nil {
				log.Printf("[channels] observe agent event for %s: %v", sess.runID, observeErr)
			} else if observation.Error != nil {
				info := *observation.Error
				terminalInfo = &info
			}
		}
		switch ev.Type {
		case agent.EventAgentStart:
			// RunWithUserMessage persists the inbound message before entering the
			// loop, so this is the first safe point to sync it to WebUI.
			d.notifyRunObserver(sess.Manager.GetHeader().ID)
		case agent.EventThinkDelta:
			thinkBuf.WriteString(ev.ThinkDelta)
		case agent.EventTextDelta:
			flushThink()
			response.WriteString(ev.TextDelta)
		case agent.EventHostedItem:
			if progress != nil && ev.HostedItem != nil {
				typeName := strings.TrimSpace(ev.HostedItem.Type)
				status := strings.TrimSpace(ev.HostedItem.Status)
				if typeName != "" || status != "" {
					progress(fmt.Sprintf("Hosted tool %s: %s", typeName, status))
				}
			}
		case agent.EventTurnEnd:
			// The assistant turn has been appended to SQLite before this event is
			// emitted. Publish here so WebUI subscribers see each channel turn,
			// including tool-call turns, instead of waiting for EventDone.
			d.notifyRunObserver(sess.Manager.GetHeader().ID)
		case agent.EventQuestionRequest:
			questionID := ev.QuestionID
			d.registerChannelDecision(sess, questionID, agentruntime.DecisionQuestion)
			d.persistChannelDecisionRequestWithDeadline(sess, questionID, agentruntime.DecisionQuestion, map[string]any{"question": ev.QuestionText, "options": ev.QuestionOptions, "context": ev.QuestionContext}, time.Now())
			if sess.Execution != nil {
				_ = sess.Execution.WaitForQuestion(sess.runID)
			}
			if sess.Decisions != nil {
				_ = sess.Decisions.Bind(questionID, func(answer string) error {
					if qh, ok := any(a).(agentpkg.QuestionHandler); ok {
						qh.HandleQuestionResponse(questionID, answer)
					}
					return nil
				})
			}
			d.notifyQuestionObserver(sess.Manager.GetHeader().ID, ev)
			if sess.Decisions != nil {
				if _, err := sess.Decisions.ResolveWith(agentruntime.DecisionResolution{ID: questionID, Kind: agentruntime.DecisionQuestion, Status: "cancelled", Value: ""}, func(_ agentruntime.DecisionRequest) error {
					return d.persistChannelDecision(sess, questionID, agentruntime.DecisionQuestion, "cancelled", "", nil)
				}); err != nil {
					log.Printf("[channels] resolve question %s: %v", questionID, err)
				}
			}
			if sess.Execution != nil {
				_ = sess.Execution.Resume(sess.runID)
			}

		case agent.EventToolExecutionStart:
			if ev.ToolCallID != "" && ev.ToolArgs != nil {
				pendingToolArgs[ev.ToolCallID] = ev.ToolArgs
			}
		case agent.EventToolExecutionEnd:
			flushThink()
			toolCount++
			if progress != nil {
				args := pendingToolArgs[ev.ToolCallID]
				delete(pendingToolArgs, ev.ToolCallID)
				line := formatToolProgress(ev, args)
				if line != "" {
					progress(line)
				}
			}
		case agent.EventContextPressure, agent.EventBudgetPressure:
			// Forward pressure warnings to messaging platform
			if progress != nil && ev.PressureMessage != "" {
				progress("\n" + ev.PressureMessage)
			}
			log.Printf("[channels] %s pressure event for %s/%s: %s", ev.PressureType, sess.Platform, sess.UserID, ev.PressureMessage)
		case agent.EventCompactionStart:
			if progress != nil {
				progress("🗜️ Compacting context...")
			}
		case agent.EventCompactionEnd:
			if progress != nil {
				if ev.Error != nil {
					info := channelFailureInfo(ev.Error, nil, agentruntime.PhaseContext)
					log.Printf("[channels] Context compaction for %s/%s failed: %v", sess.Platform, sess.UserID, ev.Error)
					progress("⚠️ Context compaction failed: " + agentruntime.DisplayErrorMessage(info))
				} else if ev.StatusMessage != "" {
					progress("🗜️ " + ev.StatusMessage)
				}
			}
		case agent.EventStatus:
			// Surface context-recovery notices (overflow compaction/truncation)
			// so unattended channel users can see why a reply was delayed. Retry
			// state is emitted separately as EventRetry with stable metadata.
			if ev.RetryStatus {
				continue
			}
			if progress != nil {
				if strings.HasPrefix(ev.StatusMessage, "Context recovery:") {
					progress("🗜️ " + ev.StatusMessage)
				} else if strings.HasPrefix(ev.StatusMessage, "⚠️") {
					progress(ev.StatusMessage)
				}
			}
		case agent.EventRetry:
			if progress != nil {
				progress(formatRetryProgress(ev))
			}
		case agent.EventRunFinished:
			terminalSeen = true
			switch ev.Status {
			case agent.TaskFailed, agent.TaskCanceled:
				flushThink()
				if ev.Error != nil {
					runErr = ev.Error
				} else if ev.Status == agent.TaskCanceled {
					runErr = context.Canceled
				} else {
					runErr = errors.New("agent run failed")
				}
				d.notifyRunObserver(sess.Manager.GetHeader().ID)
				log.Printf("[channels] Agent run %s for %s/%s: %v", ev.Status, sess.Platform, sess.UserID, runErr)
				runErr = newChannelRunFailure(runErr, terminalInfo, agentruntime.PhaseModel)
				return channelRunResult{}, runErr
			case agent.TaskIncomplete:
				d.notifyRunObserver(sess.Manager.GetHeader().ID)
				attachments = append(attachments, ev.Attachments...)
				return channelRunResult{Text: response.String(), Artifacts: d.collectChannelArtifacts(ctx, sess, artifacts, attachments)}, incompleteRunError{}
			case agent.TaskSuccess:
				d.notifyRunObserver(sess.Manager.GetHeader().ID)
				attachments = append(attachments, ev.Attachments...)
			}
		case agent.EventError:
			if terminalSeen {
				continue
			}
			flushThink()
			if ev.Error != nil {
				runErr = ev.Error
				d.notifyRunObserver(sess.Manager.GetHeader().ID)
				log.Printf("[channels] Agent error for %s/%s: %v", sess.Platform, sess.UserID, runErr)
				runErr = newChannelRunFailure(runErr, terminalInfo, agentruntime.PhaseModel)
				return channelRunResult{}, runErr
			}
			// An error event without an error payload is a protocol violation,
			// never a successful completion.
			runErr = errors.New("error event without error detail")
			d.notifyRunObserver(sess.Manager.GetHeader().ID)
			log.Printf("[channels] Agent error event without detail for %s/%s", sess.Platform, sess.UserID)
			runErr = newChannelRunFailure(runErr, terminalInfo, agentruntime.PhaseTransport)
			return channelRunResult{}, runErr
		case agent.EventDone:
			if terminalSeen {
				continue
			}
			d.notifyRunObserver(sess.Manager.GetHeader().ID)
			attachments = append(attachments, ev.Attachments...)
		}
	}

	if !terminalSeen {
		// Channel closed without a terminal event — protocol failure, never success.
		log.Printf("[channels] Agent event stream closed without terminal result for %s/%s", sess.Platform, sess.UserID)
		runErr = errors.New("event stream closed without terminal result")
		info := agentruntime.ClassifyError(runErr, agentruntime.ErrorClassificationOptions{
			Code: "event_stream_interrupted", Type: "transport_error", Phase: agentruntime.PhaseTransport,
			MessageKey: "run.error.streamInterrupted", Message: "The run stopped before it could finish.",
		})
		if sess.Execution != nil {
			var observeErr error
			info, observeErr = sess.Execution.RecordFailure(runErr, agentruntime.ErrorClassificationOptions{
				Code: "event_stream_interrupted", Type: "transport_error", Phase: agentruntime.PhaseTransport,
				MessageKey: "run.error.streamInterrupted", Message: "The run stopped before it could finish.",
			})
			if observeErr != nil {
				log.Printf("[channels] record interrupted stream for %s: %v", sess.runID, observeErr)
			}
		}
		terminalInfo = &info
		runErr = newChannelRunFailure(runErr, terminalInfo, agentruntime.PhaseTransport)
		return channelRunResult{}, runErr
	}

	result := response.String()
	log.Printf("[channels] Agent completed for %s/%s: events=%d, tools=%d, response_len=%d", sess.Platform, sess.UserID, eventCount, toolCount, len(result))

	// If agent produced no text but executed tools, provide a fallback summary
	if result == "" && toolCount > 0 {
		result = fmt.Sprintf("✅ Done (%d tool calls completed)", toolCount)
	}
	if attachmentText := FormatAttachmentSummary(nonDeliverableAttachments(attachments)); attachmentText != "" {
		if result != "" {
			result += "\n\n"
		}
		result += attachmentText
	}

	return channelRunResult{Text: result, Artifacts: d.collectChannelArtifacts(ctx, sess, artifacts, attachments)}, nil
}

func (d *Dispatcher) collectChannelArtifacts(ctx context.Context, sess *ChannelSession, collector *agentruntime.ArtifactCollector, items []provider.Attachment) []agentruntime.SessionAttachment {
	if sess == nil || sess.Runtime == nil || !sess.Runtime.ArtifactCapabilitySnapshot() {
		return nil
	}
	artifacts := collector.Artifacts()
	return append(artifacts, d.materializeChannelArtifacts(ctx, sess, items)...)
}

func (d *Dispatcher) materializeChannelArtifacts(ctx context.Context, sess *ChannelSession, items []provider.Attachment) []agentruntime.SessionAttachment {
	if sess == nil || sess.Runtime == nil || !sess.Runtime.ArtifactCapabilitySnapshot() || sess.runID == "" || len(items) == 0 {
		return nil
	}
	runtime := d.runtimeSnapshot()
	if runtime.provider == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	artifacts := make([]agentruntime.SessionAttachment, 0)
	for _, item := range items {
		if item.Kind != "image" && item.Kind != "file" {
			continue
		}
		key := item.Kind + ":" + item.ProviderRef
		if item.ProviderRef == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		record, err := sess.Runtime.AcceptProviderAttachment(ctx, sess.runID, runtime.provider, item)
		if err != nil {
			// Keep the model response successful: an optional artifact delivery
			// must not retroactively fail the canonical Agent run. The raw
			// provider reference is deliberately not exposed to the user.
			log.Printf("[channels] materialize provider attachment %s: %v", item.Kind, err)
			continue
		}
		artifacts = append(artifacts, record)
	}
	return artifacts
}

func nonDeliverableAttachments(items []provider.Attachment) []provider.Attachment {
	result := make([]provider.Attachment, 0, len(items))
	for _, item := range items {
		if item.Kind == "image" || item.Kind == "file" {
			continue
		}
		result = append(result, item)
	}
	return result
}

// formatRetryProgress is an adapter presentation of Agent Core's retry event.
// It intentionally excludes RetryReason because that field may carry provider
// diagnostics unsuitable for an end-user messaging channel.
func formatRetryProgress(ev agent.Event) string {
	message := "↻ Retrying"
	if ev.RetryContinue {
		message = "↻ Continuing response"
	}
	if ev.RetryAttempt > 0 && ev.RetryMaxAttempts > 0 {
		message += fmt.Sprintf(" (%d/%d)", ev.RetryAttempt, ev.RetryMaxAttempts)
	}
	if ev.RetryAfterMS > 0 {
		message += fmt.Sprintf("; waiting %s", time.Duration(ev.RetryAfterMS)*time.Millisecond)
	}
	return message + "..."
}

// FormatAttachmentSummary renders provider-neutral attachment references for
// channel messages and background completion callbacks.
func FormatAttachmentSummary(items []provider.Attachment) string {
	return serviceruntime.FormatAttachmentSummary(items)
}

// formatToolProgress formats a tool execution event into a concise one-line progress string.
func formatToolProgress(ev agent.Event, args map[string]any) string {
	name := ev.ToolName
	if name == "" && ev.ToolCall != nil {
		name = ev.ToolCall.Name
	}
	if name == "" {
		return ""
	}

	var icon string
	if ev.ToolError != nil {
		icon = "❌"
	} else {
		icon = "✅"
	}

	// Build a concise summary per tool type
	switch name {
	case "read", "write", "edit", "insert":
		if path, ok := args["path"].(string); ok {
			return fmt.Sprintf("[%s]: %s %s", name, path, icon)
		}
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > 60 {
				cmd = cmd[:60] + "..."
			}
			return fmt.Sprintf("[bash]: %s %s", cmd, icon)
		}
	case "grep":
		if pat, ok := args["pattern"].(string); ok {
			return fmt.Sprintf("[grep]: %s %s", pat, icon)
		}
	case "find":
		if pat, ok := args["pattern"].(string); ok {
			return fmt.Sprintf("[find]: %s %s", pat, icon)
		}
	case "ls":
		if path, ok := args["path"].(string); ok {
			return fmt.Sprintf("[ls]: %s %s", path, icon)
		}
	}

	return fmt.Sprintf("[%s] %s", name, icon)
}

func (d *Dispatcher) registerA2AMasterTool(registry *tools.Registry) error {
	if !d.a2aMaster {
		return nil
	}
	a2aListCfg, err := loadA2AAgentList()
	if err != nil {
		return fmt.Errorf("load a2a-list.json: %w", err)
	}
	a2aMgr := a2a.NewA2AManager(a2aListCfg)
	registry.Register(tools.NewA2ADispatchTool(&a2aDispatcherAdapter{mgr: a2aMgr}))
	return nil
}

func loadA2AAgentList() (*a2a.AgentListConfig, error) {
	a2aListPath := a2a.ProjectAgentListConfigPath()
	if _, err := os.Stat(a2aListPath); err != nil {
		a2aListPath = a2a.AgentListConfigPath()
	}
	return a2a.LoadAgentList(a2aListPath)
}

func a2aToolAvailability(enabled bool) (bool, string) {
	if !enabled {
		return false, "A2A master is disabled"
	}
	if _, err := loadA2AAgentList(); err != nil {
		return false, fmt.Sprintf("A2A agent list is unavailable: %v", err)
	}
	return true, ""
}

type a2aDispatcherAdapter struct {
	mgr *a2a.A2AManager
}

func (a *a2aDispatcherAdapter) List() []tools.AgentEntry {
	entries := a.mgr.List()
	result := make([]tools.AgentEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, tools.AgentEntry{Name: e.Name, URL: e.URL})
	}
	return result
}

func (a *a2aDispatcherAdapter) Dispatch(ctx context.Context, name, message string) (string, error) {
	return a.mgr.Dispatch(ctx, name, message)
}

const channelCommandHelp = `可用聊天命令：
/new [force]            - 创建新的会话（force 强制中断正在执行的任务）
/clear [force]          - 清空当前会话并创建新会话
/stop                   - 停止当前正在执行的任务
/status                 - 查看当前会话状态
/sessions               - 查看当前活跃会话
/mode [plan|agent|yolo|os] - 查看或切换会话模式
/compact                - 压缩当前会话上下文
/help                   - 显示此帮助
/more                   - 继续接收微信未发送完的消息`

// handleCommand processes slash commands from messaging platforms.
func (d *Dispatcher) handleCommand(msg messaging.InboundMessage) (string, error) {
	parts := strings.Fields(msg.Text)
	if len(parts) == 0 {
		return "", nil
	}

	cmd := strings.ToLower(parts[0])
	force := len(parts) > 1 && strings.EqualFold(parts[1], "force")
	switch cmd {
	case "/help":
		return channelCommandHelp, nil
	case "/new":
		handler := d.rotateHandlerForCommand()
		if err := handler(msg.Platform, msg.UserID, force); err != nil {
			if errors.Is(err, ErrSessionRunBusy) {
				return "⏳ 上一个任务仍在执行。可先发送 /stop，或使用 /new force 强制创建新会话。", nil
			}
			return "❌ Failed to create new session: " + channelCommandFailureMessage(err), nil
		}
		return "✅ New session created.", nil
	case "/clear":
		handler := d.rotateHandlerForCommand()
		if err := handler(msg.Platform, msg.UserID, force); err != nil {
			if errors.Is(err, ErrSessionRunBusy) {
				return "⏳ 上一个任务仍在执行。可先发送 /stop，或使用 /clear force 强制清空。", nil
			}
			return "❌ Failed to clear session: " + channelCommandFailureMessage(err), nil
		}
		return "✅ Session cleared.", nil
	case "/stop":
		sessionID := ""
		if sess := d.GetSession(sessionKey(msg.Platform, msg.UserID)); sess != nil {
			sessionID = sess.ID
		} else if msg.Platform == "wechat" || msg.Platform == "feishu" {
			if bound, err := session.FindBinding(d.sessionDir, msg.Platform, msg.UserID); err == nil && bound != nil {
				sessionID = bound.SessionID
			}
		}
		if sessionID == "" {
			return "No active session.", nil
		}
		result, err := d.RequestSessionStop(context.Background(), sessionID)
		if err != nil {
			return "❌ Unable to stop the current run: " + channelCommandFailureMessage(err), nil
		}
		switch result.Code {
		case agentruntime.SessionStopAccepted, agentruntime.SessionStopRemoteAccepted:
			return "🛑 Stop requested.", nil
		case agentruntime.SessionStopRecoveryStarted:
			return "🛑 Stale execution recovery requested.", nil
		case agentruntime.SessionStopOwnedElsewhere:
			return "⏳ This session is running in another MothX process and cannot be stopped here.", nil
		case agentruntime.SessionStopRemoteUnsupported:
			return "⏳ The detached provider run cannot be stopped from this channel.", nil
		case agentruntime.SessionStopReserved:
			return "⏳ This session is currently reserved by another operation.", nil
		case agentruntime.SessionStopNoActiveRun:
			return "No active run to stop.", nil
		default:
			return "❌ Unable to confirm the current execution state.", nil
		}
	case "/status":
		sess := d.GetSession(sessionKey(msg.Platform, msg.UserID))
		sessionID := ""
		if sess != nil {
			sessionID = sess.ID
		} else if msg.Platform == "wechat" || msg.Platform == "feishu" {
			if bound, err := session.FindBinding(d.sessionDir, msg.Platform, msg.UserID); err == nil && bound != nil {
				sessionID = bound.SessionID
			}
		}
		if sessionID == "" {
			return "No active session.", nil
		}
		mode, workDir, messageCount := "", "", 0
		if sess != nil {
			mode, workDir = sess.Mode, sess.WorkDir
			if sess.Manager != nil {
				messageCount = len(sess.Manager.GetMessages())
			}
		}
		reply := fmt.Sprintf("Session: %s\nMode: %s\nMessages: %d\nWorkDir: %s",
			sessionID, mode, messageCount, workDir)
		execution, inspectErr := agentruntime.InspectSessionExecution(d.sessionDir, sessionID)
		var localRunID string
		var startedAt, lastEventAt time.Time
		if sess != nil {
			sess.runStateMu.Lock()
			localRunID, startedAt, lastEventAt = sess.runID, sess.runStartedAt, sess.lastEventAt
			sess.runStateMu.Unlock()
		}
		if inspectErr != nil {
			reply += "\nRun: state unavailable (retryable)"
			return reply, nil
		}
		if execution.ActiveRun != nil {
			runID := execution.ActiveRun.ID
			status := "running"
			if !execution.Running {
				status = string(execution.State)
			}
			reply += fmt.Sprintf("\nRun: %s (%s, owner=%s", runID, status, execution.DisplayOwnerScope)
			if sess != nil && localRunID == runID && !startedAt.IsZero() {
				now := time.Now()
				reply += fmt.Sprintf(", running %s, last event %s ago", now.Sub(startedAt).Round(time.Second), now.Sub(lastEventAt).Round(time.Second))
			}
			reply += ")"
		} else if execution.State == agentruntime.SessionExecutionReserved {
			reply += fmt.Sprintf("\nRun: reserved (owner=%s)", execution.DisplayOwnerScope)
		} else if execution.State == agentruntime.SessionExecutionIdle {
			// A hand-built embedded ChannelSession can have no matching durable
			// Session row. Preserve its legacy local status until it is persisted;
			// production sessions always take the canonical branch above.
			if !execution.SessionExists && localRunID != "" {
				now := time.Now()
				reply += fmt.Sprintf("\nRun: %s (running %s, last event %s ago)", localRunID, now.Sub(startedAt).Round(time.Second), now.Sub(lastEventAt).Round(time.Second))
			} else {
				reply += "\nRun: idle"
			}
		} else if execution.ActiveRun == nil && !execution.SessionExists {
			// Keep the compatibility projection for an embedded or transitional
			// session when there is no persisted Session row yet. A real persisted
			// Session that cannot be classified must remain visibly unknown.
			if localRunID != "" {
				now := time.Now()
				reply += fmt.Sprintf("\nRun: %s (running %s, last event %s ago)", localRunID, now.Sub(startedAt).Round(time.Second), now.Sub(lastEventAt).Round(time.Second))
			} else {
				reply += "\nRun: idle"
			}
		} else {
			reply += fmt.Sprintf("\nRun: %s", string(execution.State))
		}
		return reply, nil
	case "/sessions":
		sessions := d.ListSessions()
		if len(sessions) == 0 {
			return "No active sessions.", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Active sessions (%d):\n", len(sessions)))
		for _, s := range sessions {
			msgs := s.Manager.GetMessages()
			sb.WriteString(fmt.Sprintf("  • %s (%d msgs, %s)\n", s.ID, len(msgs), s.WorkDir))
		}
		return sb.String(), nil
	case "/mode":
		if len(parts) < 2 {
			sess := d.GetSession(sessionKey(msg.Platform, msg.UserID))
			if sess != nil {
				return fmt.Sprintf("Current mode: %s", effectiveChannelMode(sess.Platform, sess.Mode)), nil
			}
			return "No active session.", nil
		}
		mode := strings.ToLower(parts[1])
		switch mode {
		case "plan", "agent", "yolo", "os":
			sess, release, err := d.acquireCommandSession(msg.Platform, msg.UserID)
			if err != nil {
				return "❌ No active session.", nil
			}
			defer release()
			sess.Lock()
			defer sess.Unlock()
			resolved := effectiveChannelMode(sess.Platform, mode)
			sess.Mode = resolved
			if resolved != mode {
				return "Channel sessions always run in yolo mode.", nil
			}
			return fmt.Sprintf("✅ Mode set to %s.", resolved), nil
		default:
			return "Invalid mode. Use: plan, agent, yolo, os", nil
		}
	case "/compact":
		sess, release, err := d.acquireCommandSession(msg.Platform, msg.UserID)
		if err != nil {
			return "❌ No active session.", nil
		}
		defer release()
		sess.Lock()
		defer sess.Unlock()
		if err := sess.Manager.Reload(); err != nil {
			return fmt.Sprintf("Session reload failed: %v", err), nil
		}
		if err := d.compactSession(context.Background(), sess); err != nil {
			return fmt.Sprintf("Context compaction failed: %v", err), nil
		}
		return "✅ Context compacted.", nil
	default:
		return fmt.Sprintf("Unknown command: %s\n%s", cmd, channelCommandHelp), nil
	}
}

func channelCommandFailureMessage(err error) string {
	info := channelFailureInfo(err, nil, agentruntime.PhasePersistence)
	if message := strings.TrimSpace(agentruntime.DisplayErrorMessage(info)); message != "" {
		return message
	}
	return "The operation could not be completed."
}

func (d *Dispatcher) rotateHandlerForCommand() func(string, string, bool) error {
	d.mu.RLock()
	handler := d.rotateHandler
	d.mu.RUnlock()
	if handler != nil {
		return handler
	}
	return d.RotateSession
}

// channelSessionDir returns the directory for a platform user's sessions.
func (d *Dispatcher) channelSessionDir(platform, userID string) string {
	return filepath.Join(d.sessionDir, "channels", safeSessionPathComponent(platform), safeSessionPathComponent(userID))
}

// sessionKey builds a session pool key.
func effectiveChannelMode(platform, requestedMode string) string {
	source := agentruntime.SourceFromChannelType(platform)
	_, mode, err := agentruntime.ResolvePolicy(agentruntime.SourceResolutionInput{
		Current: source, Requested: source,
	}, "", requestedMode, agentruntime.ModeYolo)
	if err != nil {
		// Channel mode resolution is fail-closed. A malformed persisted value
		// must never be returned as an executable mode.
		if policy := agentruntime.PolicyForSource(source, agentruntime.ModeYolo); policy.HasForcedMode() {
			return policy.ForcedMode()
		}
		return agentruntime.ModeYolo
	}
	return mode
}

func channelRunSource(sess *ChannelSession) string {
	if sess != nil && sess.Runtime != nil {
		if resolved, _, err := sess.Runtime.ResolvePolicy(sess.Mode, "", agentruntime.ModeYolo); err == nil && resolved.Source != agentruntime.SourceUnknown {
			return string(resolved.Source)
		}
	}
	if sess != nil {
		if source := agentruntime.SourceFromChannelType(sess.Platform); source != agentruntime.SourceUnknown {
			return string(source)
		}
		if strings.TrimSpace(sess.Platform) != "" {
			return "channel:" + strings.TrimSpace(sess.Platform)
		}
	}
	return string(agentruntime.SourceUnknown)
}

func sessionKey(platform, userID string) string {
	return fmt.Sprintf("channels/%s/%s", platform, userID)
}

// channelRouteID returns the stable conversation identity used for channel
// sessions and outbound delivery. Feishu provides both an open_id (sender)
// and a chat_id (conversation); bindings must use the latter because the
// Feishu API sends with receive_id_type=chat_id. WeChat currently uses the
// same value for both fields, and other transports retain their user ID.
func channelRouteID(msg messaging.InboundMessage) string {
	if (msg.Platform == "feishu" || msg.Platform == "wechat") && msg.ChatID != "" {
		return msg.ChatID
	}
	return msg.UserID
}

func safeSessionPathComponent(s string) string {
	if s == "" || s == "." || s == ".." {
		return "b64_" + base64.RawURLEncoding.EncodeToString([]byte(s))
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '-', '_', '.', '@':
			continue
		default:
			return "b64_" + base64.RawURLEncoding.EncodeToString([]byte(s))
		}
	}
	return s
}

// archiveCorrupt renames a corrupt session file.
func (d *Dispatcher) archiveCorrupt(path string) {
	dir := filepath.Dir(path)
	archived := filepath.Join(dir, fmt.Sprintf("%s_corrupt.db",
		time.Now().Format("20060102-150405")))
	os.Rename(path, archived)
}

// ResolveQuestion sends an answer to a currently running agent question.
func (d *Dispatcher) ResolveQuestion(questionID, answer string) bool {
	var found bool
	activeAgents.Range(func(_, value any) bool {
		ag, ok := value.(*agent.Agent)
		if !ok {
			return true
		}
		ag.HandleQuestionResponse(questionID, answer)
		found = true
		return false
	})
	return found
}

// activeAgents tracks running agents by ID for question resolution.
// Key: agent ID (string), Value: *agent.Agent
var activeAgents sync.Map

// RegisterActiveAgent registers a running agent for question resolution.
func RegisterActiveAgent(id string, a *agent.Agent) {
	activeAgents.Store(id, a)
}

// UnregisterActiveAgent removes an agent from the registry.
func UnregisterActiveAgent(id string) {
	activeAgents.Delete(id)
}

func truncate(s string, maxLen int) string {
	return util.TruncateWithSuffix(s, maxLen, "...")
}
