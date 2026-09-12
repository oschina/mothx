package agentruntime

import (
	"fmt"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

// AgentManagerOptions binds provider-specific execution dependencies to a
// shared SessionRuntime. Adapter-specific event and approval handling remains
// outside this type.
type AgentManagerOptions struct {
	Runtime           *SessionRuntime
	Provider          provider.Provider
	Model             *provider.Model
	Settings          *config.Settings
	ProviderName      string
	Allow             *config.AllowConfig
	MultiAgentEnabled bool
	DelegateEnabled   bool
	WorkflowsEnabled  bool
}

// NewAgentManager constructs an AgentFactory and AgentManager using the shared
// runtime's sandbox, context, rules and skills. All entry points should use
// this path instead of assembling AgentFactory arguments independently.
func NewAgentManager(opts AgentManagerOptions) (*agent.AgentManager, error) {
	if opts.Runtime == nil {
		return nil, fmt.Errorf("agent runtime is required")
	}
	if opts.Settings == nil {
		return nil, fmt.Errorf("agent runtime settings are required")
	}
	if opts.Provider == nil {
		return nil, fmt.Errorf("agent provider is required")
	}
	if opts.Model == nil {
		return nil, fmt.Errorf("agent model is required")
	}
	policy, err := opts.Runtime.resolvedExecutionPolicy(ModeYolo)
	if err != nil {
		return nil, err
	}
	opts.Runtime.mu.RLock()
	runtimeManager := opts.Runtime.Manager
	entrySource := opts.Runtime.EntrySource
	opts.Runtime.mu.RUnlock()
	// The Runtime's session manager is authoritative. Do not let a caller's
	// settings pointer redirect sub-agents to the platform-default session DB:
	// that would split child execution and durable state from the parent run.
	effectiveSettingsValue := *opts.Settings
	if runtimeManager != nil && runtimeManager.GetSessionDir() != "" {
		effectiveSettingsValue.SessionDir = runtimeManager.GetSessionDir()
	}
	effectiveSettings := &effectiveSettingsValue
	// A team expert binding forces multi-agent capability for the session at
	// the shared manager boundary; adapters may not downgrade it.
	expertBinding, mailbox := opts.Runtime.ExpertState()
	multiAgentEnabled := opts.MultiAgentEnabled || (expertBinding != nil && expertBinding.Team)
	expertIdentity, expertRoster := "", ""
	if expertBinding != nil {
		expertIdentity, expertRoster = expertBinding.IdentityPrompt, expertBinding.RosterPrompt
	}
	currentSourceFor := func(manager *session.Manager) RuntimeSource {
		if manager != nil && manager == runtimeManager {
			return policy.Source
		}
		return SourceUnknown
	}
	compaction := agent.CompactionSettingsFromConfig(effectiveSettings.Compaction)
	factory := agent.NewAgentFactoryWithOptions(
		opts.Provider,
		opts.Model,
		effectiveSettings,
		opts.Runtime.SandboxMgr,
		opts.Runtime.ExtraContext,
		opts.Runtime.RuleContent,
		opts.Runtime.SkillsMgr,
		compaction,
		nil,
		agent.AgentFactoryOptions{
			MultiAgentEnabled: multiAgentEnabled,
			DelegateEnabled:   opts.DelegateEnabled,
			WorkflowsEnabled:  opts.WorkflowsEnabled,
			ExpertIdentity:    expertIdentity,
			ExpertRoster:      expertRoster,
			ProviderName:      opts.ProviderName,
			Allow:             opts.Allow,
			BeforeToolCall:    beforeToolCallForPolicy(policy, nil),
			BeforeToolExecute: beforeToolExecuteForRuntime(opts.Runtime),
			ForcedMode:        policy.ForcedMode(),
			ResolveMode: func(manager *session.Manager, requestedMode string) (string, error) {
				if manager == nil {
					return policy.ResolveMode("", requestedMode)
				}
				_, mode, err := resolveManagerPolicy(manager, SourceResolutionInput{
					Current: currentSourceFor(manager), Requested: entrySource,
				}, "", requestedMode, ModeYolo)
				return mode, err
			},
			BeforeToolCallForSession: func(manager *session.Manager) func(agent.BeforeToolCallContext) *agent.ToolCallBlockResult {
				if manager == nil {
					return nil
				}
				resolved, err := resolveManagerSource(manager, SourceResolutionInput{
					Current: currentSourceFor(manager), Requested: entrySource,
				})
				if err != nil {
					return func(agent.BeforeToolCallContext) *agent.ToolCallBlockResult {
						return &agent.ToolCallBlockResult{Block: true, Reason: err.Error()}
					}
				}
				return beforeToolCallForPolicy(PolicyForSource(resolved.Source, ModeYolo), nil)
			},
		},
	)
	manager := agent.NewAgentManager(factory)
	// The session mailbox is installed for every session, not only for teams: a
	// member's question or completion must reach an active lead wherever members
	// can run (multi-agent, delegate, or workflow modes), and subagent_wait must
	// not be a dead tool outside a team binding. Only a bound team may hold its
	// run open for members, so the wrap-up wait stays team-only and unattended
	// entry points never gain a multi-minute wait.
	var members *agent.MemberDefRegistry
	expertID := ""
	teamBound := false
	if expertBinding != nil {
		members = agent.NewMemberDefRegistry(expertBinding.MemberDefs)
		expertID = expertBinding.ID
		teamBound = expertBinding.Team
	}
	if mailbox != nil || expertBinding != nil {
		manager.SetMemberContext(members, mailbox, expertID)
		manager.SetMemberWaitEnabled(teamBound)
	}
	return manager, nil
}
