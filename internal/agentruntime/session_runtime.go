package agentruntime

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/browser"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/contextfiles"
	"github.com/oschina/mothx/internal/expert"
	"github.com/oschina/mothx/internal/mcp"
	"github.com/oschina/mothx/internal/provider"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/workflow"
)

// SessionRuntime is the front-end-neutral state required to construct and run
// an agent session. Adapters may wrap it with protocol-specific locks, approval
// state, and event delivery, but must not rebuild these shared resources.
type SessionRuntime struct {
	mu           sync.RWMutex
	closed       bool
	ID           string
	Source       RuntimeSource
	EntrySource  RuntimeSource
	Policy       ExecutionPolicy
	WorkDir      string
	Manager      *session.Manager
	Inputs       *InputMaterializer
	Attachments  *AttachmentService
	Registry     *tools.Registry
	SandboxMgr   *sandbox.Manager
	SkillsMgr    *skills.Manager
	MCPClients   []*mcp.Client
	ExtraContext string
	RuleContent  string
	LastUsed     time.Time
	Execution    *ExecutionRuntime
	Decisions    *DecisionService
	// Provider, Model, Mode, and ThinkingLevel are session-owned bindings used by
	// BuildAgent when adapters omit overrides. Providers contains the selectable
	// catalog and allows ACP sessions to switch credentials/models in-process.
	Provider              provider.Provider
	ProviderName          string
	Providers             ProviderCatalog
	Model                 *provider.Model
	Mode                  string
	ThinkingLevel         provider.ThinkingLevel
	AdditionalDirectories []string
	SandboxEnabled        bool
	BrowserEnabled        bool
	WebSearchEnabled      bool
	ArtifactEnabled       bool
	// Expert is the resolved expert binding (nil when the session has no
	// expert identity). Mailbox is the runtime-owned member completion queue
	// drained into steering at run input boundaries. ExpertCenter resolves
	// bundle names for this session's project directory.
	Expert       *ExpertBinding
	Mailbox      *agent.MemberMailbox
	ExpertCenter *expert.Center

	// resourceSettings and the two capability flags record the shared resource
	// assembly policy used to create this runtime. They let a lazily-bound
	// session rebuild context and package skills after its persisted identity is
	// known, without asking an adapter to grow a second resource loader.
	resourceSettings  *config.Settings
	resourceWorkflows bool
	resourceBrowser   bool
	// imageGenerationTool records whether this entry's registry policy exposed
	// the image-generation tool at build time, so refresh reconciles only its
	// settings gate.
	imageGenerationTool bool
}

// SetExecution attaches the session's canonical execution lifecycle.
func (r *SessionRuntime) SetExecution(execution *ExecutionRuntime) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.Execution = execution
	r.mu.Unlock()
}

// SetDecisions attaches the session's shared decision lifecycle. Adapters may
// keep protocol payload maps alongside it, but Runtime owns cleanup on close.
func (r *SessionRuntime) SetDecisions(decisions *DecisionService) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.Decisions = decisions
	r.mu.Unlock()
}

// Shutdown cancels the active execution, waits for its terminal transition, and
// then releases Runtime-owned MCP resources. A context bounds the wait.
func (r *SessionRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.RLock()
	execution := r.Execution
	decisions := r.Decisions
	r.mu.RUnlock()
	runID := ""
	var shutdownErr error
	if execution != nil {
		runID, _ = execution.Active()
		shutdownErr = execution.ShutdownContext(ctx, "session runtime shutdown")
	}
	if decisions != nil {
		if runID != "" {
			decisions.ClearRunWithValue(runID, "cancelled")
		} else {
			for _, request := range decisions.Pending() {
				decisions.ClearRunWithValue(request.RunID, "cancelled")
			}
		}
	}
	// Do not release MCP/resources while a loop is still active or durable
	// terminal persistence failed. Decision callbacks are still cleared above
	// so waiting agent code can observe cancellation and retry cleanup.
	if shutdownErr == nil {
		r.Close()
	}
	return shutdownErr
}

// Close releases resources owned by this runtime. It is safe to call more than
// once and prevents new resource mutations after the first close.
func (r *SessionRuntime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	clients := r.MCPClients
	r.MCPClients = nil
	r.mu.Unlock()
	mcp.CloseClients(clients)
}

func (r *SessionRuntime) ensureOpen() error {
	if r == nil {
		return fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return fmt.Errorf("agent runtime is closed")
	}
	return nil
}

func (r *SessionRuntime) resolvedExecutionPolicy(defaultMode string) (ExecutionPolicy, error) {
	if r == nil {
		return PolicyForSource(SourceUnknown, defaultMode), fmt.Errorf("agent runtime is nil")
	}
	r.mu.RLock()
	source := r.Source
	entrySource := r.EntrySource
	policySource := r.Policy.Source
	policyDefault := r.Policy.DefaultMode
	manager := r.Manager
	r.mu.RUnlock()
	if source == SourceUnknown && policySource != SourceUnknown {
		source = policySource
	}
	if policyDefault == "" {
		policyDefault = defaultMode
	}
	resolved, _, err := resolveManagerPolicy(manager, SourceResolutionInput{
		Current: source, Requested: entrySource,
	}, "", "", policyDefault)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	return PolicyForSource(resolved.Source, policyDefault), nil
}

// ResolvePolicy resolves one source/mode pair from Runtime-owned identity.
func (r *SessionRuntime) ResolvePolicy(sessionMode, requestedMode, defaultMode string) (SourceResolution, string, error) {
	if err := r.ensureOpen(); err != nil {
		return SourceResolution{}, "", err
	}
	r.mu.RLock()
	source := r.Source
	entrySource := r.EntrySource
	policySource := r.Policy.Source
	policyDefault := r.Policy.DefaultMode
	manager := r.Manager
	r.mu.RUnlock()
	if source == SourceUnknown && policySource != SourceUnknown {
		source = policySource
	}
	if policyDefault == "" {
		policyDefault = defaultMode
	}
	return resolveManagerPolicy(manager, SourceResolutionInput{
		Current: source, Requested: entrySource,
	}, sessionMode, requestedMode, policyDefault)
}

func resolveManagerSource(manager *session.Manager, input SourceResolutionInput) (SourceResolution, error) {
	if err := validateSourceCandidates(input); err != nil {
		return SourceResolution{}, err
	}
	if manager != nil {
		input.SessionHeader = manager.GetHeader()
		if header := input.SessionHeader; header != nil && header.ID != "" && manager.GetSessionDir() != "" {
			return ResolveSourceFromSession(manager.GetSessionDir(), header.ID, input)
		}
	}
	resolved := ResolveSource(input)
	if resolved.Conflicted {
		return resolved, &SourceConflictError{Diagnostics: append([]string(nil), resolved.Diagnostics...)}
	}
	return resolved, nil
}

func resolveManagerPolicy(manager *session.Manager, input SourceResolutionInput, sessionMode, requestedMode, defaultMode string) (SourceResolution, string, error) {
	resolved, err := resolveManagerSource(manager, input)
	if err != nil {
		return resolved, "", err
	}
	mode, err := PolicyForSource(resolved.Source, defaultMode).ResolveMode(sessionMode, requestedMode)
	return resolved, mode, err
}

// BindSession attaches or replaces the persisted session identity owned by this
// Runtime. It is used by frontends that create sessions lazily.
func (r *SessionRuntime) BindSession(manager *session.Manager, requested RuntimeSource) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if manager == nil || manager.GetHeader() == nil {
		return fmt.Errorf("initialized session manager is required")
	}
	header := manager.GetHeader()
	entrySource := requested
	if entrySource == SourceUnknown {
		r.mu.RLock()
		entrySource = r.EntrySource
		r.mu.RUnlock()
	}
	resolved, err := resolveManagerSource(manager, SourceResolutionInput{Requested: entrySource})
	if err != nil {
		return err
	}
	inputs, err := NewInputMaterializer(manager.GetSessionDir(), header.Cwd, DefaultInputPolicy())
	if err != nil {
		return err
	}
	attachments, err := NewAttachmentService(manager.GetSessionDir(), DefaultAttachmentPolicy())
	if err != nil {
		return err
	}
	prepared, err := r.prepareBoundSessionResources(manager)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("agent runtime is closed")
	}
	r.ID = header.ID
	r.Source = resolved.Source
	r.EntrySource = entrySource
	r.Policy.Source = resolved.Source
	r.WorkDir = header.Cwd
	r.Manager = manager
	r.Inputs = inputs
	r.Attachments = attachments
	r.ExpertCenter = &expert.Center{ProjectDir: header.Cwd}
	r.publishPreparedExpertResourcesLocked(prepared)
	return nil
}

// ConfigureSession installs the initial per-session provider, model, mode, and
// thinking bindings. It does not own provider construction.
func (r *SessionRuntime) ConfigureSession(p provider.Provider, providerName string, model *provider.Model, mode string, thinking provider.ThinkingLevel) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if p == nil || model == nil {
		return fmt.Errorf("session provider and model are required")
	}
	if providerName == "" {
		providerName = p.Name()
	}
	if p.GetModel(model.ID) == nil {
		return fmt.Errorf("model %q is not available for provider %q", model.ID, providerName)
	}
	if strings.TrimSpace(mode) == "" {
		mode = ModeYolo
	}
	_, effectiveMode, err := r.ResolvePolicy("", mode, ModeYolo)
	if err != nil {
		return err
	}
	thinking, err = ValidateThinkingLevel(string(thinking))
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("agent runtime is closed")
	}
	r.Provider = p
	r.ProviderName = providerName
	hasProvider := false
	for name := range r.Providers {
		if strings.EqualFold(name, providerName) {
			hasProvider = true
			break
		}
	}
	if !hasProvider {
		providers := cloneProviderCatalog(r.Providers)
		if providers == nil {
			providers = ProviderCatalog{}
		}
		providers[providerName] = p
		r.Providers = providers
	}
	r.Model = model
	r.Mode = effectiveMode
	r.ThinkingLevel = thinking
	r.LastUsed = time.Now()
	return nil
}

// ConfigSnapshot returns the current session configuration atomically.
func (r *SessionRuntime) ConfigSnapshot() (provider.Provider, string, *provider.Model, string, provider.ThinkingLevel) {
	if r == nil {
		return nil, "", nil, "", ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Provider, r.ProviderName, r.Model, r.Mode, r.ThinkingLevel
}

// SettingsSnapshot returns a copy of the Runtime resource settings for a
// Runtime-owned derived execution. The copy prevents a caller from mutating
// the active session's shared configuration while still letting derived roles
// inherit the same compaction and tool-execution defaults.
func (r *SessionRuntime) SettingsSnapshot() *config.Settings {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	settings := r.resourceSettings
	r.mu.RUnlock()
	if settings == nil {
		return nil
	}
	copy := *settings
	return &copy
}

// ResolveProviderModel resolves an optional derived-role provider/model pair
// from the Runtime-owned provider catalog. Empty values retain this session's
// configured provider and model. It is intentionally a Runtime method so
// adapters do not create providers or duplicate model-compatibility logic for
// role-specific executions.
func (r *SessionRuntime) ResolveProviderModel(providerName, modelID string) (provider.Provider, string, *provider.Model, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, "", nil, err
	}
	currentProvider, currentName, currentModel, _, _ := r.ConfigSnapshot()
	providerName = strings.TrimSpace(providerName)
	modelID = strings.TrimSpace(modelID)
	if providerName == "" && modelID == "" {
		if currentProvider == nil || currentModel == nil {
			return nil, "", nil, fmt.Errorf("session provider and model are required")
		}
		return currentProvider, currentName, currentModel, nil
	}
	if providerName == "" || modelID == "" {
		return nil, "", nil, fmt.Errorf("provider and model must be specified together")
	}
	p, err := r.providerByName(providerName)
	if err != nil {
		return nil, "", nil, err
	}
	model, err := providerfactory.ResolveModel(p, providerName, modelID)
	if err != nil {
		return nil, "", nil, err
	}
	return p, providerName, model, nil
}

// CapabilitySnapshot returns the mutable session capabilities used by the
// shared config-options contract.
func (r *SessionRuntime) CapabilitySnapshot() (sandboxEnabled, browserEnabled, webSearchEnabled bool) {
	if r == nil {
		return false, false, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.SandboxEnabled, r.BrowserEnabled, r.WebSearchEnabled
}

// ArtifactCapabilitySnapshot reports whether this entry point permits agents
// to publish generated files for the current session.
func (r *SessionRuntime) ArtifactCapabilitySnapshot() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ArtifactEnabled
}

// SetArtifactEnabled updates the entry-point policy used by subsequent runs.
// An already active collector retains ownership until that run terminates.
func (r *SessionRuntime) SetArtifactEnabled(enabled bool) error {
	if r == nil {
		return fmt.Errorf("agent runtime is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("agent runtime is closed")
	}
	r.ArtifactEnabled = enabled
	r.LastUsed = time.Now()
	return nil
}

// ConfigureCapabilities applies adapter-selected defaults and replays the
// persisted browser/web-search capability state when a session has one.
func (r *SessionRuntime) ConfigureCapabilities(sandboxEnabled, browserEnabled, webSearchEnabled bool) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	r.mu.Lock()
	manager := r.Manager
	r.mu.Unlock()
	if manager != nil {
		if caps, ok, err := session.LoadSessionCapabilities(manager.GetSessionDir(), r.ID); err != nil {
			return err
		} else if ok {
			browserEnabled = caps.Browser
			webSearchEnabled = caps.WebSearch
		}
	}
	r.mu.Lock()
	r.SandboxEnabled = sandboxEnabled
	r.BrowserEnabled = browserEnabled
	r.WebSearchEnabled = webSearchEnabled
	if r.Registry != nil && r.SandboxMgr != nil {
		active := r.SandboxMgr.GetActive()
		if !sandboxEnabled {
			if none, err := r.SandboxMgr.GetForLevel(sandbox.LevelNone); err == nil {
				active = none
			}
		} else if active == nil || active.Level() == sandbox.LevelNone {
			if standard, err := r.SandboxMgr.GetForLevel(sandbox.LevelStandard); err == nil {
				active = standard
			}
		}
		r.Registry.SetSandbox(active)
	}
	r.synchronizeCoreToolsLocked(browserEnabled, r.resourceSettings)
	r.LastUsed = time.Now()
	r.mu.Unlock()
	return nil
}

// SetCapabilityOption persists and applies one boolean session capability.
func (r *SessionRuntime) SetCapabilityOption(id string, enabled bool) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	r.mu.Lock()
	manager := r.Manager
	sandboxEnabled, browserEnabled, webSearchEnabled := r.SandboxEnabled, r.BrowserEnabled, r.WebSearchEnabled
	switch id {
	case ConfigOptionSandbox:
		sandboxEnabled = enabled
	case ConfigOptionBrowser:
		browserEnabled = enabled
	case ConfigOptionWebSearch:
		webSearchEnabled = enabled
	default:
		r.mu.Unlock()
		return fmt.Errorf("unknown capability config option %q", id)
	}
	r.SandboxEnabled, r.BrowserEnabled, r.WebSearchEnabled = sandboxEnabled, browserEnabled, webSearchEnabled
	if r.Registry != nil && r.SandboxMgr != nil {
		active := r.SandboxMgr.GetActive()
		if !sandboxEnabled {
			if none, err := r.SandboxMgr.GetForLevel(sandbox.LevelNone); err == nil {
				active = none
			}
		} else if active == nil || active.Level() == sandbox.LevelNone {
			if standard, err := r.SandboxMgr.GetForLevel(sandbox.LevelStandard); err == nil {
				active = standard
			}
		}
		r.Registry.SetSandbox(active)
	}
	r.synchronizeCoreToolsLocked(browserEnabled, r.resourceSettings)
	r.LastUsed = time.Now()
	r.mu.Unlock()
	if manager != nil {
		caps, _, err := session.LoadSessionCapabilities(manager.GetSessionDir(), r.ID)
		if err != nil {
			return err
		}
		persisted := session.SessionCapabilities{SessionID: r.ID}
		if caps != nil {
			persisted = *caps
		}
		persisted.SessionID = r.ID
		persisted.Browser = browserEnabled
		persisted.WebSearch = webSearchEnabled
		if err := session.SaveSessionCapabilities(manager.GetSessionDir(), persisted); err != nil {
			return err
		}
	}
	return nil
}

// AdditionalDirectoriesSnapshot returns a copy of the current session roots.
func (r *SessionRuntime) AdditionalDirectoriesSnapshot() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.AdditionalDirectories...)
}

// SetAdditionalDirectories persists and applies a complete replacement of
// the session's additional directory roots.
func (r *SessionRuntime) SetAdditionalDirectories(directories []string) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	normalized, err := NormalizeAdditionalDirectories(directories)
	if err != nil {
		return err
	}
	r.mu.RLock()
	manager := r.Manager
	previous := append([]string(nil), r.AdditionalDirectories...)
	r.mu.RUnlock()
	if manager != nil && (len(normalized) > 0 || len(previous) > 0) {
		if err := manager.Reload(); err != nil {
			return err
		}
		if _, err := manager.AppendAdditionalDirectories(normalized); err != nil {
			return err
		}
	}
	r.mu.Lock()
	r.AdditionalDirectories = append([]string(nil), normalized...)
	r.LastUsed = time.Now()
	r.mu.Unlock()
	return nil
}

// ReloadAdditionalDirectories applies the latest persisted directory binding.
func (r *SessionRuntime) ReloadAdditionalDirectories(manager *session.Manager) error {
	if manager == nil {
		return nil
	}
	entry, ok := manager.GetLatestAdditionalDirectories()
	if !ok {
		return nil
	}
	normalized, err := NormalizeAdditionalDirectories(entry.Directories)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.AdditionalDirectories = append([]string(nil), normalized...)
	r.mu.Unlock()
	return nil
}

// ConfigOptions returns the standard mutable configuration catalog for this
// session. An empty catalog means the runtime has not been bound to a provider.
func (r *SessionRuntime) ConfigOptions() []SessionConfigOption {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	p, providerName, model, mode, thinking := r.Provider, r.ProviderName, r.Model, r.Mode, r.ThinkingLevel
	providers := cloneProviderCatalog(r.Providers)
	r.mu.RUnlock()
	if p == nil {
		return nil
	}
	if len(providers) == 0 {
		providers = ProviderCatalog{providerName: p}
	}
	options := SessionConfigOptionsWithProviders(providerName, providers, p.Models(), model, mode, thinking)
	options = append(options, r.expertConfigOption())
	r.mu.RLock()
	browserEnabled, webSearchEnabled := r.BrowserEnabled, r.WebSearchEnabled
	r.mu.RUnlock()
	// Sandbox remains process-policy-owned until session_capabilities gains a
	// durable sandbox column; do not advertise a toggle whose state would be
	// lost across reloads.
	options = append(options,
		SessionConfigOption{Type: "boolean", ID: ConfigOptionBrowser, Name: "Browser", Category: "browser", CurrentValue: strconv.FormatBool(browserEnabled)},
		SessionConfigOption{Type: "boolean", ID: ConfigOptionWebSearch, Name: "Web search", Category: "web_search", CurrentValue: strconv.FormatBool(webSearchEnabled)},
	)
	return options
}

func cloneProviderCatalog(src ProviderCatalog) ProviderCatalog {
	if len(src) == 0 {
		return nil
	}
	dst := make(ProviderCatalog, len(src))
	for name, p := range src {
		dst[name] = p
	}
	return dst
}

// reloadPersistedConfig applies the latest session bindings after a manager
// reload. A session may be opened by more than one adapter/process, so the
// manager's optimistic-lock cursor and the Runtime's in-memory binding must be
// refreshed together before another option is changed.
func (r *SessionRuntime) reloadPersistedConfig(manager *session.Manager) error {
	if manager == nil {
		return nil
	}
	if err := r.ReloadAdditionalDirectories(manager); err != nil {
		return err
	}
	p, providerName, model, mode, thinking := r.ConfigSnapshot()
	if p == nil {
		return fmt.Errorf("session provider is unavailable")
	}
	if entry, ok := manager.GetLatestModelChange(); ok {
		if entry.Provider != "" && !strings.EqualFold(entry.Provider, providerName) {
			var switchErr error
			p, switchErr = r.providerByName(entry.Provider)
			if switchErr != nil {
				return switchErr
			}
			providerName = entry.Provider
		}
		resolved, err := providerfactory.ResolveModel(p, providerName, entry.ModelID)
		if err != nil {
			return err
		}
		model = resolved
	}
	if entry, ok := manager.GetLatestModeChange(); ok && strings.TrimSpace(entry.Mode) != "" {
		_, resolved, err := r.ResolvePolicy("", entry.Mode, ModeYolo)
		if err != nil {
			return err
		}
		mode = resolved
	}
	if entry, ok := manager.GetLatestThinkingLevelChange(); ok && strings.TrimSpace(entry.ThinkingLevel) != "" {
		resolved, err := ValidateThinkingLevel(entry.ThinkingLevel)
		if err != nil {
			return err
		}
		thinking = resolved
	}
	r.mu.Lock()
	r.Provider = p
	r.ProviderName = providerName
	r.Model = model
	r.Mode = mode
	r.ThinkingLevel = thinking
	r.LastUsed = time.Now()
	r.mu.Unlock()
	return nil
}

// SetConfigOption validates, persists, and applies one mutable session option.
// Persistence happens before the in-memory binding changes so failed requests
// cannot leave a runtime ahead of its session history.
func (r *SessionRuntime) SetConfigOption(id, value string) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	value = strings.TrimSpace(value)
	if id == "" || (value == "" && id != ConfigOptionExpert) {
		return fmt.Errorf("config option id and value are required")
	}
	if id == ConfigOptionExpert {
		return r.SetExpert(value)
	}
	p, providerName, currentModel, currentMode, currentThinking := r.ConfigSnapshot()
	if p == nil {
		return fmt.Errorf("session provider is unavailable")
	}
	r.mu.RLock()
	manager := r.Manager
	r.mu.RUnlock()
	if manager != nil {
		// A session can be opened by more than one thin adapter. Refresh the
		// optimistic-lock leaf before appending a new binding so a prior write
		// from another adapter is preserved rather than reported as a conflict.
		if err := manager.Reload(); err != nil {
			return err
		}
		if err := r.reloadPersistedConfig(manager); err != nil {
			return err
		}
		p, providerName, currentModel, currentMode, currentThinking = r.ConfigSnapshot()
	}
	switch id {
	case ConfigOptionProvider:
		target, err := r.providerByName(value)
		if err != nil {
			return err
		}
		targetName := value
		matchedCatalog := false
		r.mu.RLock()
		catalog := cloneProviderCatalog(r.Providers)
		r.mu.RUnlock()
		for name := range catalog {
			if strings.EqualFold(name, value) {
				targetName = name
				matchedCatalog = true
				break
			}
		}
		if !matchedCatalog && target.Name() != "" {
			targetName = target.Name()
		}
		var currentModelID string
		if currentModel != nil {
			currentModelID = currentModel.ID
		}
		model := target.GetModel(currentModelID)
		if model == nil {
			models := target.Models()
			if len(models) > 0 {
				model = models[0]
			}
		}
		if model == nil {
			return fmt.Errorf("provider %q has no usable model", targetName)
		}
		if manager != nil {
			if _, err := manager.AppendModelChange(targetName, model.ID); err != nil {
				return err
			}
		}
		r.mu.Lock()
		r.Provider = target
		r.ProviderName = targetName
		r.Model = model
		r.LastUsed = time.Now()
		r.mu.Unlock()
		return nil
	case ConfigOptionModel:
		if strings.Contains(value, "/") {
			qualifiedProvider, _, err := providerfactory.ParseQualifiedModel(value)
			if err != nil {
				return err
			}
			if !strings.EqualFold(qualifiedProvider, providerName) {
				target, switchErr := r.providerByName(qualifiedProvider)
				if switchErr != nil {
					return switchErr
				}
				p = target
				providerName = qualifiedProvider
			}
		}
		model, err := providerfactory.ResolveModel(p, providerName, value)
		if err != nil {
			return err
		}
		if manager != nil {
			if _, err := manager.AppendModelChange(providerName, model.ID); err != nil {
				return err
			}
		}
		r.mu.Lock()
		r.Provider = p
		r.ProviderName = providerName
		r.Model = model
		r.LastUsed = time.Now()
		r.mu.Unlock()
		return nil
	case ConfigOptionMode:
		resolution, mode, err := r.ResolvePolicy(currentMode, value, ModeYolo)
		if err != nil {
			return err
		}
		_ = resolution
		if manager != nil {
			if _, err := manager.AppendModeChange(mode); err != nil {
				return err
			}
		}
		r.mu.Lock()
		r.Mode = mode
		r.LastUsed = time.Now()
		r.mu.Unlock()
		return nil
	case ConfigOptionThinkingLevel:
		if currentModel == nil || !currentModel.Reasoning {
			return fmt.Errorf("config option %q is unavailable for model %q", ConfigOptionThinkingLevel, providerfactory.QualifiedModel(providerName, currentModel))
		}
		thinking, err := ValidateThinkingLevel(value)
		if err != nil {
			return err
		}
		if manager != nil {
			if _, err := manager.AppendThinkingLevelChange(string(thinking)); err != nil {
				return err
			}
		}
		r.mu.Lock()
		r.ThinkingLevel = thinking
		r.LastUsed = time.Now()
		r.mu.Unlock()
		return nil
	default:
		_ = currentModel
		_ = currentThinking
		return fmt.Errorf("unsupported config option %q", id)
	}
}

func (r *SessionRuntime) providerByName(name string) (provider.Provider, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("provider is required")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for catalogName, p := range r.Providers {
		if strings.EqualFold(catalogName, name) && p != nil {
			return p, nil
		}
	}
	if strings.EqualFold(r.ProviderName, name) && r.Provider != nil {
		return r.Provider, nil
	}
	return nil, fmt.Errorf("provider %q is not available", name)
}

// UnbindSession clears persisted session identity while retaining reusable
// Runtime-owned resources for a frontend that will lazily create another session.
func (r *SessionRuntime) UnbindSession() error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("agent runtime is closed")
	}
	r.ID = ""
	r.Source = r.EntrySource
	r.Policy.Source = r.Source
	r.Manager = nil
	r.LastUsed = time.Now()
	r.mu.Unlock()
	return r.rehydrateBoundResources()
}

type Builder struct {
	Settings     *config.Settings
	SandboxLevel sandbox.Level
}

// RegistryHook injects adapter-specific tools into a fully initialized shared
// runtime. Hooks run after core tools are registered and before MCP connects,
// so MCP tools see the final registry without owning Runtime lifecycle.
type RegistryHook func(*SessionRuntime) error

// BuildOptions are the resource-affecting session capabilities. They are kept
// separate from adapter-specific presentation and approval options.
type BuildOptions struct {
	ID              string
	Source          RuntimeSource
	WorkDir         string
	Manager         *session.Manager
	Workflows       bool
	Browser         bool
	ArtifactEnabled bool
	RegistryHooks   []RegistryHook
}

// RefreshOptions are mutable resource-affecting session capabilities.
type RefreshOptions struct {
	Workflows    bool
	Browser      bool
	ActiveSkills map[string]bool
}

// ContextResources are the shared context/skill inputs used by a session runtime.
type ContextResources struct {
	SkillsMgr    *skills.Manager
	ExtraContext string
	RuleContent  string
}

// Build constructs context, skills, sandbox, tools and MCP connections for one
// session. The caller owns the returned runtime and must call Close on failures
// after successful construction or when the session is evicted.
func (b Builder) Build(ctx context.Context, opts BuildOptions) (*SessionRuntime, error) {
	if b.Settings == nil {
		return nil, fmt.Errorf("agent runtime settings are required")
	}
	if opts.WorkDir == "" {
		return nil, fmt.Errorf("agent runtime work directory is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	expertBundle, err := resolveBoundExpertBundle(opts.WorkDir, opts.Manager)
	if err != nil {
		return nil, err
	}
	resources, err := LoadContextResourcesWithExpert(b.Settings, opts.WorkDir, opts.Workflows, opts.Browser, expertBundle)
	if err != nil {
		return nil, err
	}
	skillsMgr, extraContext := resources.SkillsMgr, resources.ExtraContext

	sandboxMgr := sandbox.NewManagerWithOptions(opts.WorkDir, b.Settings.Sandbox.Options())
	if err := sandboxMgr.SetLevel(b.SandboxLevel); err != nil {
		return nil, fmt.Errorf("sandbox for work directory: %w", err)
	}
	registry, err := BuildRegistry(opts.WorkDir, sandboxMgr, b.Settings, RegistryPolicy{
		RegisterDefaults: true,
		EnablePlanTool:   DefaultPlanToolPolicy(b.Settings),
		SkillsMgr:        skillsMgr,
		Browser:          opts.Browser,
		ImageGeneration:  true,
	})
	if err != nil {
		return nil, err
	}

	resolved, err := resolveManagerSource(opts.Manager, SourceResolutionInput{Requested: opts.Source})
	if err != nil {
		return nil, err
	}
	var inputs *InputMaterializer
	var attachments *AttachmentService
	if opts.Manager != nil {
		inputs, err = NewInputMaterializer(opts.Manager.GetSessionDir(), opts.WorkDir, DefaultInputPolicy())
		if err != nil {
			return nil, err
		}
		attachments, err = NewAttachmentService(opts.Manager.GetSessionDir(), DefaultAttachmentPolicy())
		if err != nil {
			return nil, err
		}
	}
	runtime := &SessionRuntime{
		ID:                  opts.ID,
		Source:              resolved.Source,
		EntrySource:         opts.Source,
		Policy:              PolicyForSource(resolved.Source, ""),
		WorkDir:             opts.WorkDir,
		Manager:             opts.Manager,
		Inputs:              inputs,
		Attachments:         attachments,
		Registry:            registry,
		SandboxMgr:          sandboxMgr,
		SkillsMgr:           skillsMgr,
		ExtraContext:        extraContext,
		RuleContent:         resources.RuleContent,
		LastUsed:            time.Now(),
		ArtifactEnabled:     opts.ArtifactEnabled,
		imageGenerationTool: registryExposesImageGeneration(registry),
		resourceSettings:    b.Settings,
		resourceWorkflows:   opts.Workflows,
		resourceBrowser:     opts.Browser,
	}
	runtime.Mailbox = agent.NewMemberMailbox()
	runtime.ExpertCenter = &expert.Center{ProjectDir: opts.WorkDir}
	if err := runtime.refreshExpertBinding(); err != nil {
		return nil, err
	}
	if err := runtime.ApplyRegistryHooks(opts.RegistryHooks); err != nil {
		return nil, err
	}
	servers, err := mcp.LoadConfiguredServers(opts.WorkDir)
	if err != nil {
		return nil, err
	}
	clients, err := mcp.ConnectServers(ctx, servers, registry, mcp.Callbacks{})
	if err != nil {
		return nil, fmt.Errorf("connect MCP servers: %w", err)
	}
	runtime.MCPClients = clients
	return runtime, nil
}

// ApplyRegistryHooks injects adapter-owned tools into this Runtime. It is
// intentionally limited to Registry mutation; policy resolution, sandbox,
// allow rules, session ownership and run lifecycle remain Runtime-owned.
func (r *SessionRuntime) ApplyRegistryHooks(hooks []RegistryHook) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	// Hooks commonly need Runtime-owned facts (for example the effective
	// multi-agent capability). Do not invoke third-party adapter callbacks
	// while holding r.mu: those helpers correctly take r.mu.RLock and would
	// otherwise self-deadlock the build path.
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return fmt.Errorf("agent runtime is closed")
	}
	r.mu.RUnlock()
	for _, hook := range hooks {
		if hook == nil {
			continue
		}
		if err := hook(r); err != nil {
			return err
		}
	}
	return nil
}

// RefreshResources reloads context files and skills, synchronizes the shared
// skill_ref/browser tools, and updates the Runtime fields atomically after all
// validation succeeds. It always resolves the current persisted expert binding
// before loading resources, so lazy session attachment and expert bind/unbind
// cannot leave package skills or prompts from a previous session behind.
// Adapter-specific AgentManager and optional tools are deliberately outside
// this method.
func (r *SessionRuntime) RefreshResources(settings *config.Settings, opts RefreshOptions) error {
	if r == nil {
		return fmt.Errorf("agent runtime is nil")
	}
	if settings == nil {
		return fmt.Errorf("agent runtime settings are required")
	}
	if err := r.ensureOpen(); err != nil {
		return err
	}
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return fmt.Errorf("agent runtime is closed")
	}
	workDir := r.WorkDir
	manager := r.Manager
	r.mu.RUnlock()

	expertBundle, err := resolveBoundExpertBundle(workDir, manager)
	if err != nil {
		return err
	}
	resources, err := LoadContextResourcesWithExpert(settings, workDir, opts.Workflows, opts.Browser, expertBundle)
	if err != nil {
		return err
	}
	skillsMgr, extraContext := resources.SkillsMgr, resources.ExtraContext
	activeContext, err := activeSkillsContext(skillsMgr, opts.ActiveSkills)
	if err != nil {
		return err
	}
	var binding *ExpertBinding
	if expertBundle != nil {
		binding = newExpertBinding(expertBundle)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("agent runtime is closed")
	}
	// A concurrent BindSession/UnbindSession changed the authoritative manager
	// while the potentially expensive context load was in progress. Do not
	// publish resources resolved for the wrong session.
	if r.WorkDir != workDir || r.Manager != manager {
		return fmt.Errorf("session identity changed while refreshing runtime resources")
	}
	if r.Registry != nil {
		r.Registry.Register(tools.NewSkillRefTool(skillsMgr))
	}
	r.synchronizeCoreToolsLocked(opts.Browser, settings)
	r.Expert = binding
	r.SkillsMgr = skillsMgr
	r.ExtraContext = extraContext + activeContext
	r.RuleContent = resources.RuleContent
	r.resourceSettings = settings
	r.resourceWorkflows = opts.Workflows
	r.resourceBrowser = opts.Browser
	r.LastUsed = time.Now()
	return nil
}

// rehydrateBoundResources refreshes the resolved expert binding after a
// runtime is attached to, detached from, or explicitly rebound to a session.
// Older compatibility paths without assembly settings still refresh the
// binding; production builders and AttachSessionResources provide settings so
// expert package skills share the normal Runtime-owned resource path.
func (r *SessionRuntime) rehydrateBoundResources() error {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	settings := r.resourceSettings
	workflows := r.resourceWorkflows
	browserEnabled := r.resourceBrowser
	r.mu.RUnlock()
	if settings == nil {
		return r.refreshExpertBinding()
	}
	return r.RefreshResources(settings, RefreshOptions{Workflows: workflows, Browser: browserEnabled})
}

// SynchronizeCoreTools applies mutable registry tools that have no adapter
// dependency. It is safe to call when context content itself is unchanged.
func (r *SessionRuntime) SynchronizeCoreTools(browserEnabled bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.synchronizeCoreToolsLocked(browserEnabled, r.resourceSettings)
}

// synchronizeCoreToolsLocked reconciles the conditional core tools whose
// availability is capability/settings driven. It is the single refresh surface
// for these tools so build, capability changes, and resource refresh cannot
// drift apart.
func (r *SessionRuntime) synchronizeCoreToolsLocked(browserEnabled bool, settings *config.Settings) {
	if r.Registry == nil {
		return
	}
	if browserEnabled {
		browser.RegisterTool(r.Registry)
	} else {
		browser.RemoveTool(r.Registry)
	}
	if r.imageGenerationTool {
		if settings != nil && settings.IsImageGenerationEnabled() {
			r.Registry.Register(tools.NewImageGenerationTool(settings))
		} else {
			r.Registry.Remove("image_generation")
		}
	}
}

func activeSkillsContext(manager *skills.Manager, active map[string]bool) (string, error) {
	if len(active) == 0 {
		return "", nil
	}
	names := make([]string, 0, len(active))
	for name, enabled := range active {
		if enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var context strings.Builder
	for _, name := range names {
		if manager == nil || manager.Get(name) == nil {
			return "", fmt.Errorf("skill not found: %s", name)
		}
		context.WriteString(manager.BuildSkillContext(name))
	}
	return context.String(), nil
}

// LoadContextResources loads context files, project/global skills, and rules.
// It is shared by all adapters; adapters choose only the requested capabilities.
func LoadContextResources(settings *config.Settings, workDir string, workflows, browserEnabled bool) (*ContextResources, error) {
	return LoadContextResourcesWithExpert(settings, workDir, workflows, browserEnabled, nil)
}

// LoadContextResourcesWithExpert additionally loads skills packaged with a
// resolved expert bundle. Expert skills are session-scoped Runtime resources:
// they shadow ordinary project/global skills of the same name without creating
// an adapter-owned skills path. Embedded bundles are read directly from their
// fs.FS, so packaged binaries do not depend on a source checkout.
func LoadContextResourcesWithExpert(settings *config.Settings, workDir string, workflows, browserEnabled bool, expertBundle *expert.Bundle) (*ContextResources, error) {
	if workflows {
		if _, _, err := workflow.EnsureProjectSkill(workDir); err != nil {
			return nil, fmt.Errorf("create workflow skill: %w", err)
		}
	}
	projectSkillDirs := skills.ProjectSkillDirs(workDir)
	if expertBundle != nil && expertBundle.SkillsDir != "" && expertBundle.SkillsFS == nil {
		// NewManagerWithProjectDirs loads the first directory last, giving the
		// session-bound expert package its documented highest local precedence.
		projectSkillDirs = append([]string{expertBundle.SkillsDir}, projectSkillDirs...)
	}
	skillsMgr := skills.NewManagerWithProjectDirs(settings.GetGlobalSkillsDir(), projectSkillDirs)
	_ = skillsMgr.Load()
	if expertBundle != nil && expertBundle.SkillsDir != "" && expertBundle.SkillsFS != nil {
		if err := skillsMgr.LoadFS(expertBundle.SkillsFS, expertBundle.SkillsDir, "expert"); err != nil {
			return nil, fmt.Errorf("load expert package skills: %w", err)
		}
	}

	var extraContext string
	if settings.ContextFiles.Enabled {
		result := contextfiles.LoadContextFiles(workDir, config.ConfigDir(), settings.ContextFiles.ExtraFiles)
		extraContext = contextfiles.BuildContextString(result)
	}
	extraContext += skillsMgr.BuildAllSkillsContext()
	if workflows {
		extraContext += skillsMgr.BuildSkillContext(workflow.SkillName)
	}
	if browserEnabled {
		extraContext += skillsMgr.BuildSkillContext(browser.SkillName)
	}
	return &ContextResources{SkillsMgr: skillsMgr, ExtraContext: extraContext, RuleContent: contextfiles.LoadRuleFile(workDir)}, nil
}
