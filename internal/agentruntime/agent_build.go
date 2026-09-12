package agentruntime

import (
	"context"
	"fmt"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

// AgentBuildOptions are per-run inputs supplied by an adapter after Runtime has
// resolved its source, policy, session resources, and effective mode.
type AgentBuildOptions struct {
	ID                     agentpkg.AgentID
	ParentID               agentpkg.AgentID
	Provider               provider.Provider
	ProviderName           string
	Model                  *provider.Model
	Settings               *config.Settings
	Allow                  *config.AllowConfig
	Mode                   string
	ToolExecutionMode      string
	MaxToolConcurrency     int
	ExtraContext           string
	RuleContent            string
	ThinkingLevel          provider.ThinkingLevel
	SandboxMgr             *sandbox.Manager
	SandboxEnabled         *bool
	MaxTokens              int
	MaxTokensSet           bool
	MultiAgent             bool
	DelegateMode           bool
	Workflows              bool
	ApprovalHandler        func(string, string, map[string]any) bool
	ApprovalDecisionLookup func(string, string, map[string]any) (bool, bool)
	MaxIterations          int
	ContextPressure        float64
	BudgetPressure         float64
	BeforeToolCall         func(agent.BeforeToolCallContext) *agent.ToolCallBlockResult
	BeforeToolExecute      func(agent.BeforeToolExecuteContext) *agent.ToolCallBlockResult
	AfterToolCall          func(agent.AfterToolCallContext) *agent.ToolCallResult
	GetSteeringMessages    func() []provider.Message
	ConversationTurnID     string
	IntentID               string
	RunID                  string
	ConversationTurn       bool
	RuntimeOwnsTurnEnd     bool
	RuntimeOwnsUserEntry   bool
	UserEntryID            string
	// AuxiliaryRole marks a build that is not the session's conversational lead
	// even though it runs on the session's own manager (the legacy knowledge
	// librarian query bridge). Such a build never receives the team member
	// mailbox hooks: it must not wait for the session's members nor consume
	// member notifications that belong to the lead.
	AuxiliaryRole bool
}

// AgentBuildOptionsFromConfig converts the legacy Agent.Config shape used by
// provider-specific drivers into Runtime-owned build inputs.
func AgentBuildOptionsFromConfig(cfg agent.Config) AgentBuildOptions {
	return AgentBuildOptions{
		ID: cfg.ID, ParentID: cfg.ParentID, Provider: cfg.Provider, ProviderName: cfg.Vendor,
		Model: cfg.Model, Settings: cfg.Settings, Allow: cfg.Allow, Mode: cfg.Mode,
		RuleContent: cfg.RuleContent, ExtraContext: cfg.ExtraContext,
		ThinkingLevel: cfg.ThinkingLevel, MaxTokens: cfg.MaxTokens, MaxTokensSet: cfg.MaxTokensUserSet,
		MultiAgent: cfg.MultiAgent, DelegateMode: cfg.DelegateMode, Workflows: cfg.Workflows,
		ConversationTurnID: cfg.ConversationTurnID, IntentID: cfg.IntentID, RunID: cfg.RunID,
		ConversationTurn:     cfg.ConversationTurn,
		RuntimeOwnsTurnEnd:   cfg.RuntimeOwnsTurnEnd,
		RuntimeOwnsUserEntry: cfg.RuntimeOwnsUserEntry, UserEntryID: cfg.UserEntryID,
		ApprovalHandler: cfg.ApprovalHandler, ApprovalDecisionLookup: cfg.ApprovalDecisionLookup,
	}
}

// This preserves adapter-selected session tools and MCP clients while keeping
// provider/config/sandbox/context assembly out of adapters.
func (r *SessionRuntime) BuildAgent(opts AgentBuildOptions) (*agent.Agent, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	registry := r.Registry
	manager := r.Manager
	if opts.Provider == nil {
		opts.Provider = r.Provider
	}
	if opts.ProviderName == "" {
		opts.ProviderName = r.ProviderName
	}
	if opts.Model == nil {
		opts.Model = r.Model
	}
	if opts.Mode == "" {
		opts.Mode = r.Mode
	}
	if opts.ThinkingLevel == "" {
		opts.ThinkingLevel = r.ThinkingLevel
	}
	r.mu.RUnlock()
	return r.buildAgent(registry, manager, opts)
}

// BuildTransientAgent constructs a non-persisted agent over an adapter-provided
// registry. It is intended for temporary side queries such as TUI /btw. The
// shared Runtime still supplies provider-independent context and sandbox
// defaults, while the adapter retains ownership of the temporary registry.
func (r *SessionRuntime) BuildTransientAgent(registry *tools.Registry, opts AgentBuildOptions) (*agent.Agent, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("transient agent registry is required")
	}
	return r.buildAgent(registry, nil, opts)
}

func (r *SessionRuntime) buildAgent(registry *tools.Registry, manager *session.Manager, opts AgentBuildOptions) (*agent.Agent, error) {
	if registry == nil {
		return nil, fmt.Errorf("agent runtime registry is required")
	}
	if opts.Provider == nil || opts.Model == nil {
		return nil, fmt.Errorf("agent provider and model are required")
	}
	if opts.ConversationTurn && opts.RuntimeOwnsTurnEnd && opts.RunID != "" {
		opts.RuntimeOwnsUserEntry = true
		if opts.UserEntryID == "" {
			opts.UserEntryID = session.RunUserEntryID(opts.RunID)
		}
	}
	r.mu.RLock()
	sandboxMgr := r.SandboxMgr
	if opts.SandboxMgr != nil {
		sandboxMgr = opts.SandboxMgr
	}
	if opts.SandboxEnabled != nil && !*opts.SandboxEnabled {
		sandboxMgr = nil
	}
	extraContext := r.ExtraContext
	ruleContent := r.RuleContent
	expertBinding := r.Expert
	mailbox := r.Mailbox
	r.mu.RUnlock()
	expertIdentity, expertRoster := projectExpertBuild(expertBinding, &opts)
	// Only the session's conversational lead owns the team mailbox. Transient
	// builds (side questions, knowledge-base indexing) pass a nil session
	// manager, and auxiliary roles built over the session manager opt out, so
	// neither blocks on the session's members nor consumes member notifications
	// that belong to the lead. Every session drains member notifications; only a
	// bound expert team holds its run open for members.
	teamBound := expertBinding != nil && expertBinding.Team
	steeringMessages := opts.GetSteeringMessages
	var followUpMessages func(context.Context) []provider.Message
	if manager != nil && !opts.AuxiliaryRole {
		steeringMessages = composeSteering(mailbox, opts.GetSteeringMessages)
		if teamBound {
			// A team lead must not end its run (and cancel the members it is still
			// waiting for) just because a turn produced no tool calls: the follow-up
			// hook waits for members and keeps adapter steering responsive while it
			// waits.
			followUpMessages = agent.ComposeFollowUps(mailbox, opts.GetSteeringMessages)
		}
	}
	settings := opts.Settings
	if settings == nil {
		settings = &config.Settings{}
	}
	// The persisted session manager is Runtime-owned. Keep the agent's settings
	// aligned with that manager so descendants created through AgentManager
	// inherit the same session database instead of silently falling back to the
	// machine-global default directory.
	settingsValue := *settings
	if manager != nil && manager.GetSessionDir() != "" {
		settingsValue.SessionDir = manager.GetSessionDir()
	}
	settings = &settingsValue
	mode := opts.Mode
	if mode == "" {
		mode = ModeYolo
	}
	if opts.ExtraContext != "" {
		extraContext = opts.ExtraContext
	}
	if opts.RuleContent != "" {
		ruleContent = opts.RuleContent
	}
	toolExecutionMode := opts.ToolExecutionMode
	maxToolConcurrency := opts.MaxToolConcurrency
	if toolExecutionMode == "" {
		toolExecutionMode = settings.ToolExecution.EffectiveMode()
	}
	if maxToolConcurrency <= 0 {
		maxToolConcurrency = settings.ToolExecution.EffectiveMaxConcurrency()
	}
	policy, err := r.resolvedExecutionPolicy(ModeYolo)
	if err != nil {
		return nil, err
	}
	// Resolve the effective mode at the shared construction boundary. Adapters
	// may pass a requested mode, but a persisted source policy (for example a
	// channel-bound forced-yolo session) must determine the actual Agent config
	// before its prompt and tool set are frozen.
	mode, err = policy.ResolveMode("", mode)
	if err != nil {
		return nil, err
	}
	beforeToolCall := beforeToolCallForPolicy(policy, opts.BeforeToolCall)
	beforeToolExecute := beforeToolExecuteForRuntime(r)
	if opts.BeforeToolExecute != nil {
		beforeToolExecute = composeBeforeToolExecute(beforeToolExecute, opts.BeforeToolExecute)
	}
	maxTokens := agent.ResolveMaxTokens(opts.Model)
	if opts.MaxTokensSet {
		maxTokens = opts.MaxTokens
	}
	return agent.NewWithLoopConfig(agent.AgentLoopConfig{
		Config: agent.Config{
			ID: opts.ID, ParentID: opts.ParentID, Provider: opts.Provider, Vendor: opts.ProviderName, Model: opts.Model, Mode: mode,
			ThinkingLevel: opts.ThinkingLevel, MaxTokens: maxTokens,
			SandboxMgr: sandboxMgr, Settings: settings, Allow: opts.Allow, Session: manager,
			ExtraContext: extraContext, RuleContent: ruleContent,
			ExpertIdentity: expertIdentity, ExpertRoster: expertRoster,
			CompactionSettings: agent.CompactionSettingsFromConfig(settings.Compaction),
			ApprovalHandler:    opts.ApprovalHandler, ApprovalDecisionLookup: opts.ApprovalDecisionLookup, MultiAgent: opts.MultiAgent,
			DelegateMode: opts.DelegateMode, Workflows: opts.Workflows,
			ConversationTurnID: opts.ConversationTurnID, IntentID: opts.IntentID, RunID: opts.RunID,
			ConversationTurn:     opts.ConversationTurn,
			RuntimeOwnsTurnEnd:   opts.RuntimeOwnsTurnEnd,
			RuntimeOwnsUserEntry: opts.RuntimeOwnsUserEntry, UserEntryID: opts.UserEntryID,
		},
		ToolExecutionMode: toolExecutionMode, MaxToolConcurrency: maxToolConcurrency,
		MaxIterations: opts.MaxIterations, ContextPressureThreshold: opts.ContextPressure,
		BudgetPressureThreshold: opts.BudgetPressure, BeforeToolCall: beforeToolCall, BeforeToolExecute: beforeToolExecute,
		AfterToolCall:       opts.AfterToolCall,
		GetSteeringMessages: steeringMessages,
		GetFollowUpMessages: followUpMessages,
		ForcedMode:          policy.ForcedMode(),
	}, registry), nil
}

func composeBeforeToolExecute(first, second func(agent.BeforeToolExecuteContext) *agent.ToolCallBlockResult) func(agent.BeforeToolExecuteContext) *agent.ToolCallBlockResult {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func(ctx agent.BeforeToolExecuteContext) *agent.ToolCallBlockResult {
		if result := first(ctx); result != nil && result.Block {
			return result
		}
		return second(ctx)
	}
}
