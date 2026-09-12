package agent

import (
	"fmt"
	"os"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/platform"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/tools"
)

// AgentFactory creates Agent instances with consistent configuration.
type AgentFactory struct {
	provider                 provider.Provider
	providerName             string
	model                    *provider.Model
	settings                 *config.Settings
	allow                    *config.AllowConfig
	sandboxMgr               *sandbox.Manager
	extraContext             string
	ruleContent              string
	skillsMgr                *skills.Manager
	compactionSettings       ctxpkg.CompactionSettings
	approvalHandler          func(toolCallID, toolName string, args map[string]any) bool
	multiAgentEnabled        bool
	delegateEnabled          bool
	workflowsEnabled         bool
	toolExecutionMode        string
	maxToolConcurrency       int
	beforeToolCall           func(ctx BeforeToolCallContext) *ToolCallBlockResult
	beforeToolExecute        func(ctx BeforeToolExecuteContext) *ToolCallBlockResult
	forcedMode               string
	resolveMode              func(manager *session.Manager, requestedMode string) (string, error)
	beforeToolCallForSession func(manager *session.Manager) func(ctx BeforeToolCallContext) *ToolCallBlockResult
	expertIdentity           string
	expertRoster             string
	// manager and memberMailbox are installed by AgentManager during shared
	// Runtime assembly. They are used only for manager-created top-level
	// agents (for example an ESM worker continuation), never for child agents.
	manager       *AgentManager
	memberMailbox *MemberMailbox
	// memberWaitEnabled records whether this manager's lead may hold its run open
	// for still-running members (a bound expert team). Sessions without a team
	// still receive member notifications through the steering drain, but their
	// runs may end while members are running: unattended entry points must not
	// inherit a multi-minute wait. Installed by AgentManager during assembly.
	memberWaitEnabled bool
}

// NewAgentFactory creates a factory with shared configuration.
func NewAgentFactory(
	provider provider.Provider,
	model *provider.Model,
	settings *config.Settings,
	sandboxMgr *sandbox.Manager,
	extraContext string,
	ruleContent string,
	skillsMgr *skills.Manager,
	compactionSettings ctxpkg.CompactionSettings,
	approvalHandler func(toolCallID, toolName string, args map[string]any) bool,
) *AgentFactory {
	return NewAgentFactoryWithOptions(provider, model, settings, sandboxMgr, extraContext, ruleContent, skillsMgr, compactionSettings, approvalHandler, AgentFactoryOptions{
		MultiAgentEnabled: true,
	})
}

// AgentFactoryOptions configures AgentFactory behavior.
type AgentFactoryOptions struct {
	MultiAgentEnabled        bool
	DelegateEnabled          bool
	WorkflowsEnabled         bool
	ProviderName             string
	Allow                    *config.AllowConfig
	BeforeToolCall           func(ctx BeforeToolCallContext) *ToolCallBlockResult
	BeforeToolExecute        func(ctx BeforeToolExecuteContext) *ToolCallBlockResult
	ForcedMode               string
	ResolveMode              func(manager *session.Manager, requestedMode string) (string, error)
	BeforeToolCallForSession func(manager *session.Manager) func(ctx BeforeToolCallContext) *ToolCallBlockResult
	// ExpertIdentity/ExpertRoster are injected into main agents only
	// (ParentID == ""); sub-agents never receive the lead persona overlay.
	ExpertIdentity string
	ExpertRoster   string
}

// NewAgentFactoryWithOptions creates a factory with explicit behavior flags.
func NewAgentFactoryWithOptions(
	provider provider.Provider,
	model *provider.Model,
	settings *config.Settings,
	sandboxMgr *sandbox.Manager,
	extraContext string,
	ruleContent string,
	skillsMgr *skills.Manager,
	compactionSettings ctxpkg.CompactionSettings,
	approvalHandler func(toolCallID, toolName string, args map[string]any) bool,
	opts AgentFactoryOptions,
) *AgentFactory {
	allow := opts.Allow
	if allow == nil {
		allow = config.LoadAllow()
	}
	return &AgentFactory{
		provider:                 provider,
		providerName:             opts.ProviderName,
		model:                    model,
		settings:                 settings,
		allow:                    allow,
		sandboxMgr:               sandboxMgr,
		extraContext:             extraContext,
		ruleContent:              ruleContent,
		skillsMgr:                skillsMgr,
		compactionSettings:       compactionSettings,
		approvalHandler:          approvalHandler,
		multiAgentEnabled:        opts.MultiAgentEnabled,
		delegateEnabled:          opts.DelegateEnabled,
		workflowsEnabled:         opts.WorkflowsEnabled,
		beforeToolCall:           opts.BeforeToolCall,
		beforeToolExecute:        opts.BeforeToolExecute,
		forcedMode:               opts.ForcedMode,
		resolveMode:              opts.ResolveMode,
		beforeToolCallForSession: opts.BeforeToolCallForSession,
		expertIdentity:           opts.ExpertIdentity,
		expertRoster:             opts.ExpertRoster,
	}
}

// AgentOptions specifies per-agent overrides.
type AgentOptions struct {
	ID       agentpkg.AgentID
	ParentID agentpkg.AgentID
	// Member metadata is a display snapshot for a named expert-team child.
	// AgentFactory does not interpret it; AgentManager retains it alongside the
	// child lifecycle so adapters can project the same canonical identity.
	MemberID          string
	ExpertID          string
	MemberDisplayName string
	MemberEmoji       string
	MemberRole        string
	Mode              string
	Model             *provider.Model
	WorkDir           string
	Tools             []string // optional: tool filter
	// ExcludeTools removes tools from the resolved child registry after the
	// standard mode/tool filtering. Used for children whose callers cannot answer
	// a tool's interactive contract (for example a blocking delegate child that
	// must not ask questions).
	ExcludeTools       []string
	SystemPromptExtra  string // extra context for this agent
	MaxIterations      int
	ToolExecutionMode  string
	MaxToolConcurrency int
	Session            *session.Manager
	IsSubAgent         bool                                                        // persist this agent outside the user-continuable session tables
	ApprovalHandler    func(toolCallID, toolName string, args map[string]any) bool // per-agent approval override
	// OwnsSessionMailbox opts an IsSubAgent role back into the session member
	// mailbox. The mailbox belongs to the conversational lead, so auxiliary roles
	// (ESM critic/audit/recovery) must not inherit it; a team-bound ESM worker
	// continuation is the session's lead in ESM mode and sets this explicitly.
	OwnsSessionMailbox bool
	// AuxiliaryRole marks an agent that is not the session's conversational lead
	// even though its shape matches one (a parentless, non-subagent run such as a
	// session-bound cron job). Auxiliary agents never drain or wait on the
	// session's members, and take precedence over OwnsSessionMailbox.
	AuxiliaryRole bool
	MultiAgent    *bool // optional prompt override
	DelegateMode  *bool // optional prompt override
	Workflows     *bool // optional prompt override
}

// Create creates a new Agent with per-agent Registry.
// Each agent gets its own Registry (with its own workDir, sandbox, JobManager).
func (f *AgentFactory) Create(opts AgentOptions) agentpkg.Agent {
	workDir := opts.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	// Determine session before mode and sandbox so Runtime policy can use
	// persisted identity for manager-created background agents.
	sess := opts.Session
	if sess == nil {
		sess = f.defaultSession(workDir, opts.IsSubAgent || opts.ParentID != "")
	}

	mode := opts.Mode
	if mode == "" {
		mode = "yolo"
	}
	if resolvedMode, err := f.resolveAgentMode(sess, mode); err == nil {
		mode = resolvedMode
	}

	model := opts.Model
	if model == nil {
		model = f.model
		if sess != nil {
			if entry, ok := sess.GetLatestModelChange(); ok && entry.ModelID != "" && f.provider != nil {
				if persisted := f.provider.GetModel(entry.ModelID); persisted != nil {
					model = persisted
				}
			}
		}
	}

	maxIterations := opts.MaxIterations
	if maxIterations == 0 {
		maxIterations = 200
	}

	toolExecMode := opts.ToolExecutionMode
	if toolExecMode == "" {
		toolExecMode = "parallel"
		if f.toolExecutionMode != "" {
			toolExecMode = f.toolExecutionMode
		} else if f.settings != nil {
			toolExecMode = f.settings.ToolExecution.EffectiveMode()
		}
	}
	maxToolConcurrency := opts.MaxToolConcurrency
	if maxToolConcurrency <= 0 {
		maxToolConcurrency = f.maxToolConcurrency
	}
	if maxToolConcurrency <= 0 {
		if f.settings != nil {
			maxToolConcurrency = f.settings.ToolExecution.EffectiveMaxConcurrency()
		} else {
			maxToolConcurrency = config.DefaultToolExecutionMaxConcurrency
		}
	}

	// Create per-agent Registry with isolated workDir/sandbox/JobManager
	sb := f.sandboxForMode(mode)
	registry := tools.NewRegistryWithConfig(tools.RegistryConfig{
		WorkDir:        workDir,
		Sandbox:        sb,
		ToolFilter:     opts.Tools,
		SkillsMgr:      f.skillsMgr,
		EnablePlanTool: config.BoolPtr(f.settings == nil || f.settings.IsPlanToolEnabled()),
		EnvVars:        config.LoadEnv().List(),
	})

	// Decision 5: Sub-agents cannot spawn sub-agents
	// Remove subagent_* tools from sub-agent registries
	if opts.ParentID != "" {
		for _, name := range SubAgentToolNames() {
			registry.Remove(name)
		}
		registry.Remove("delegate_subagent")
	}
	for _, name := range opts.ExcludeTools {
		registry.Remove(name)
	}

	// Build extra context: factory-level + per-agent
	extraContext := f.extraContext
	if opts.ParentID != "" {
		extraContext += "\n" + BuildSubAgentContext()
	}
	if opts.SystemPromptExtra != "" {
		extraContext += "\n" + opts.SystemPromptExtra
	}

	multiAgent := f.multiAgentEnabled && opts.ParentID == ""
	if opts.MultiAgent != nil {
		multiAgent = *opts.MultiAgent
	}
	delegateMode := f.delegateEnabled && opts.ParentID == ""
	if opts.DelegateMode != nil {
		delegateMode = *opts.DelegateMode
	}
	workflows := f.workflowsEnabled && opts.ParentID == ""
	if opts.Workflows != nil {
		workflows = *opts.Workflows
	}
	if opts.ParentID != "" {
		delegateMode = false
		workflows = false
	}
	// Manager-created top-level agents normally have an isolated Registry. If
	// the shared Runtime resolved team capability for this run, reattach the
	// canonical manager-owned sub-agent tools here. This is what lets an ESM
	// worker continue as the team lead while preserving critic/audit isolation
	// (those roles keep MultiAgent=false).
	if opts.ParentID == "" && multiAgent && f.manager != nil {
		RegisterSubAgentTools(registry, f.manager)
	}

	thinkingLevel := provider.ThinkingLevel(agentpkg.ThinkingMedium)
	if f.settings != nil {
		thinkingLevel = provider.ThinkingLevel(f.settings.DefaultThinkingLevel)
	}
	if sess != nil {
		if entry, ok := sess.GetLatestThinkingLevelChange(); ok && entry.ThinkingLevel != "" {
			thinkingLevel = provider.ThinkingLevel(entry.ThinkingLevel)
		}
	}
	expertIdentity, expertRoster := "", ""
	if opts.ParentID == "" {
		expertIdentity, expertRoster = f.expertIdentity, f.expertRoster
	}
	cfg := Config{
		ID:            opts.ID,
		ParentID:      opts.ParentID,
		Provider:      f.provider,
		Vendor:        f.providerName,
		Model:         model,
		Mode:          mode,
		ThinkingLevel: thinkingLevel,
		MaxTokens: func() int {
			return ResolveMaxTokens(model)
		}(),
		SandboxMgr:         f.sandboxMgr,
		Settings:           f.settings,
		Allow:              f.allow,
		Session:            sess,
		ExtraContext:       extraContext,
		RuleContent:        f.ruleContent,
		CompactionSettings: f.compactionSettings,
		ApprovalHandler: func() func(toolCallID, toolName string, args map[string]any) bool {
			if opts.ApprovalHandler != nil {
				return opts.ApprovalHandler
			}
			return f.approvalHandler
		}(),
		MultiAgent:     multiAgent,
		DelegateMode:   delegateMode,
		Workflows:      workflows,
		ExpertIdentity: expertIdentity,
		ExpertRoster:   expertRoster,
	}

	beforeToolCall := f.beforeToolCall
	if f.beforeToolCallForSession != nil {
		beforeToolCall = composeBeforeToolCall(f.beforeToolCallForSession(sess), beforeToolCall)
	}
	loopCfg := AgentLoopConfig{
		Config:             cfg,
		ForcedMode:         f.forcedMode,
		ToolExecutionMode:  toolExecMode,
		MaxToolConcurrency: maxToolConcurrency,
		MaxIterations:      maxIterations,
		BeforeToolCall:     beforeToolCall,
		BeforeToolExecute:  f.beforeToolExecute,
	}
	// The session mailbox belongs to the conversational lead: an auxiliary role
	// (ESM critic/audit/recovery runs are created with IsSubAgent and no parent)
	// must neither block on the session's members nor drain their notifications.
	// A team-bound ESM worker continuation acts as the lead and opts back in.
	if opts.ParentID == "" && !opts.AuxiliaryRole && f.memberMailbox != nil && (!opts.IsSubAgent || opts.OwnsSessionMailbox) {
		loopCfg.GetSteeringMessages = f.memberMailbox.DrainSteering
		// Member notifications that arrive while the lead is producing its last
		// turn must reach it before the run may end: without this hook the lead
		// would stop (and cancel the members it is still waiting for) before ever
		// seeing their completions or questions. Only a bound expert team holds
		// the run open for members; elsewhere the notification is delivered at the
		// next iteration or the next run instead of extending this one.
		if f.memberWaitEnabled {
			loopCfg.GetFollowUpMessages = ComposeFollowUps(f.memberMailbox, nil)
		}
	}

	a := NewWithLoopConfig(loopCfg, registry)
	return NewAgentAdapter(a)
}

func (f *AgentFactory) withParentRuntimeConfig(cfg AgentLoopConfig) *AgentFactory {
	if f == nil {
		return nil
	}
	clone := *f
	clone.provider = cfg.Provider
	clone.providerName = cfg.Vendor
	clone.model = cfg.Model
	clone.settings = cfg.Settings
	clone.allow = cfg.Allow
	clone.extraContext = cfg.ExtraContext
	clone.ruleContent = cfg.RuleContent
	clone.compactionSettings = cfg.CompactionSettings
	clone.approvalHandler = cfg.ApprovalHandler
	clone.beforeToolCall = cfg.BeforeToolCall
	clone.beforeToolExecute = cfg.BeforeToolExecute
	clone.forcedMode = cfg.ForcedMode
	clone.toolExecutionMode = cfg.ToolExecutionMode
	clone.maxToolConcurrency = cfg.MaxToolConcurrency
	return &clone
}

func (f *AgentFactory) resolveAgentMode(manager *session.Manager, requestedMode string) (string, error) {
	if f == nil {
		return requestedMode, nil
	}
	if f.forcedMode != "" {
		return f.forcedMode, nil
	}
	if f.resolveMode != nil {
		return f.resolveMode(manager, requestedMode)
	}
	return requestedMode, nil
}

func composeBeforeToolCall(first, second func(BeforeToolCallContext) *ToolCallBlockResult) func(BeforeToolCallContext) *ToolCallBlockResult {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(ctx BeforeToolCallContext) *ToolCallBlockResult {
		if result := first(ctx); result != nil && result.Block {
			return result
		}
		return second(ctx)
	}
}

func (f *AgentFactory) withRuntimeConfig(p provider.Provider, providerName string, model *provider.Model, settings *config.Settings, allow *config.AllowConfig) *AgentFactory {
	if f == nil {
		return nil
	}
	clone := *f
	if p != nil {
		clone.provider = p
	}
	clone.providerName = providerName
	if model != nil {
		clone.model = model
	}
	if settings != nil {
		clone.settings = settings
		// Keep sub-agent compaction settings in sync with the new runtime
		// settings so future agents compact with the same policy.
		clone.compactionSettings = CompactionSettingsFromConfig(settings.Compaction)
	}
	if allow != nil {
		clone.allow = allow
	}
	return &clone
}

// CreateFromPublicOptions creates an agent from public Builder options.
func (f *AgentFactory) CreateFromPublicOptions(b *agentpkg.Builder) agentpkg.Agent {
	if b == nil {
		return nil
	}
	agent, err := buildFromPublicBuilder(b)
	if err != nil {
		return nil
	}
	return agent
}

// sandboxForMode returns the appropriate sandbox for the given mode.
func (f *AgentFactory) sandboxForMode(mode string) sandbox.Sandbox {
	if f.sandboxMgr == nil {
		return sandbox.NewNoneSandbox()
	}
	switch mode {
	case "plan":
		return f.sandboxMgr.GetActive()
	case "agent":
		return f.sandboxMgr.GetActive()
	case "yolo", "os":
		return sandbox.NewNoneSandbox()
	default:
		return f.sandboxMgr.GetActive()
	}
}

// defaultSession creates a default session manager for the given work directory.
func (f *AgentFactory) defaultSession(workDir string, subAgent bool) *session.Manager {
	sessionDir := ""
	if f.settings != nil {
		sessionDir = f.settings.GetSessionDir()
	}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	if subAgent {
		return session.NewSubAgent(workDir, sessionDir)
	}
	return session.New(workDir, sessionDir)
}

// Provider returns the factory's provider (for Builder integration).
func (f *AgentFactory) Provider() provider.Provider { return f.provider }

// Settings returns the factory's settings.
func (f *AgentFactory) Settings() *config.Settings { return f.settings }

// --- Register the internal builder with the public agent package ---

func init() {
	agentpkg.SetBuilderFunc(buildFromPublicBuilder)
}

// buildFromPublicBuilder converts a public Builder into an internal Agent.
// This bridges the public agent.Builder API to the internal Agent implementation.
func buildFromPublicBuilder(b *agentpkg.Builder) (agentpkg.Agent, error) {
	cfg := b.Config()

	// Adapt the public Provider to the internal provider.Provider interface
	internalProvider := NewProviderAdapter(cfg.Provider)

	// Resolve the model from the provider
	model := internalProvider.GetModel(cfg.ModelID)
	if model == nil {
		// If the model is not found, create a minimal model entry
		model = &provider.Model{
			ID:   cfg.ModelID,
			Name: cfg.ModelID,
		}
	}

	// Build compaction settings
	compactionSettings := ctxpkg.CompactionSettings{
		Enabled:       cfg.CompactionEnabled,
		ReserveTokens: cfg.CompactionReserve,
	}
	if compactionSettings.ReserveTokens == 0 {
		compactionSettings.ReserveTokens = 16384
	}

	// Build sandbox
	var sandboxMgr *sandbox.Manager
	if cfg.SandboxEnabled {
		sandboxMgr = sandbox.NewManagerWithOptions(cfg.WorkDir, sandbox.Options{ProtectGit: true})
		level := sandbox.LevelStandard
		if err := sandboxMgr.SetLevel(level); err != nil {
			return nil, fmt.Errorf("strict sandbox enabled but unavailable: %w", err)
		}
	}

	// Build session. An empty session directory keeps the platform default,
	// which session.New resolves itself; the public builder no longer imports
	// internal packages to pre-resolve it.
	sess := session.New(cfg.WorkDir, cfg.SessionDir)

	// Build the tool registry
	var sb sandbox.Sandbox
	if sandboxMgr != nil {
		sb = sandboxMgr.GetActive()
	} else {
		sb = sandbox.NewNoneSandbox()
	}
	var registry *tools.Registry
	if cfg.DisableBuiltinTools {
		// External-only mode: start with an empty registry so the agent may
		// use ONLY the host-provided external tools.
		registry = tools.NewRegistry(cfg.WorkDir, sb)
	} else {
		registry = tools.NewRegistryWithConfig(tools.RegistryConfig{
			WorkDir:    cfg.WorkDir,
			Sandbox:    sb,
			ToolFilter: cfg.Tools,
		})
	}
	// Register host-provided external tools.
	for _, et := range cfg.ExternalTools {
		if et == nil {
			continue
		}
		registry.Register(newExternalToolAdapter(et))
	}

	agentCfg := Config{
		Provider:           internalProvider,
		Model:              model,
		Mode:               cfg.Mode,
		ThinkingLevel:      provider.ThinkingLevel(cfg.ThinkingLevel),
		MaxTokens:          ResolveMaxTokensValue(cfg.MaxTokens, model),
		MaxTokensUserSet:   cfg.MaxTokens > 0,
		SandboxMgr:         sandboxMgr,
		Session:            sess,
		ExtraContext:       cfg.SystemPromptExtra,
		CompactionSettings: compactionSettings,
		ApprovalHandler:    cfg.ApprovalHandler,
		MultiAgent:         cfg.MultiAgent,
		DelegateMode:       cfg.DelegateMode,
	}

	loopCfg := AgentLoopConfig{
		Config:             agentCfg,
		ToolExecutionMode:  cfg.ToolExecutionMode,
		MaxToolConcurrency: cfg.MaxToolConcurrency,
		MaxIterations:      cfg.MaxIterations,
	}

	a := NewWithLoopConfig(loopCfg, registry)
	return NewAgentAdapter(a), nil
}
