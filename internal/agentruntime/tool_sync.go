package agentruntime

import (
	"strings"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/tools"
	"github.com/oschina/mothx/internal/workflow"
)

// ToolGroupPolicy describes the optional tool groups resolved from session
// capabilities. Adapters supply policy and the current manager handle;
// installation, removal, and manager-swap re-installation are Runtime-owned so
// every entry point (TUI, CLI, WebUI/API, ACP, Channel) shares one behavior.
type ToolGroupPolicy struct {
	// MultiAgent enables the async sub-agent toolset. A bound expert team
	// forces it on regardless (see SubAgentToolsEnabled).
	MultiAgent bool
	// SubAgentTools optionally narrows the sub-agent set by tool name for
	// ordinary multi-agent sessions. It is ignored while a team is bound: the
	// team capability is authoritative and never drops tools. Nil means the
	// full canonical set.
	SubAgentTools map[string]bool
	// Delegate enables the blocking single sub-agent delegation tool.
	Delegate bool
	// Workflows enables the workflow toolset.
	Workflows bool
	// WorkflowTools optionally narrows the workflow set by tool name with the
	// same team semantics as SubAgentTools. Nil means the full canonical set.
	WorkflowTools map[string]bool
	// Mode optionally overrides the mode used for Router-only availability. An
	// adapter that projects a locally edited mode (for example the TUI /mode
	// command) passes it here instead of duplicating registration logic. Empty
	// means use the Runtime's persisted/resolved mode.
	Mode string
}

// SynchronizeToolGroups reconciles the registry's optional tool groups against
// the policy and the current AgentManager. It is idempotent and owns the whole
// lifecycle: installing a group, removing a disabled group, re-installing after
// a manager replacement (so tool instances always reference the current
// manager), and trimming per-tool selections that an ordinary multi-agent
// session switched off. A nil manager (or runtime with no team binding)
// removes every group tool.
func SynchronizeToolGroups(rt *SessionRuntime, registry *tools.Registry, policy ToolGroupPolicy, manager *agent.AgentManager) {
	if registry == nil {
		return
	}
	team := rt.TeamExpertActive()
	if manager == nil {
		removeSubAgentTools(registry)
		registry.Remove(delegateSubAgentToolName)
		workflow.RemoveTools(registry)
		return
	}
	mode := strings.TrimSpace(policy.Mode)
	if mode == "" {
		_, _, _, mode, _ = rt.ConfigSnapshot()
	}
	if mode == ModeRouter {
		// Each Router worker is one bounded external turn. Reconcile the entire
		// async group so a prior multi-agent selection cannot leave send/answer
		// (or a local-only tool) alive in the Router surface.
		removeSubAgentTools(registry)
		agent.RegisterRouterSubAgentTools(registry, manager)
	} else if SubAgentToolsEnabled(rt, policy.MultiAgent) {
		agent.RegisterSubAgentTools(registry, manager)
		if !team {
			trimToolSelection(registry, agent.SubAgentToolNames(), policy.SubAgentTools)
		}
	} else {
		removeSubAgentTools(registry)
	}
	// Router always exposes the blocking delegate tool. The async spawn surface is
	// installed above; both target the same activated external worker.
	if policy.Delegate || mode == ModeRouter {
		agent.RegisterDelegateSubAgentTool(registry, manager)
		// Router has exactly one activated worker. Publish it to the tool so the
		// model-facing description and the `agent` schema carry the real profile ID
		// instead of leaving the model to guess a well-known product name.
		publishRouterTarget(rt, registry, mode)
	} else {
		registry.Remove(delegateSubAgentToolName)
	}
	if policy.Workflows {
		workflow.RegisterTools(registry, manager, nil)
		trimToolSelection(registry, workflow.ToolNames(), policy.WorkflowTools)
	} else {
		workflow.RemoveTools(registry)
	}
}

const delegateSubAgentToolName = "delegate_subagent"

// RouterActive reports whether this session currently resolves to Router mode.
// It is the Runtime-owned mode predicate adapters must use instead of
// re-deriving Router from their own mode field: Router is a Runtime fact
// resolved from the persisted mode entry plus the activated worker binding.
func (r *SessionRuntime) RouterActive() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	mode := r.Mode
	r.mu.RUnlock()
	return strings.TrimSpace(mode) == ModeRouter
}

// ManagerBackedToolsEnabled reports whether this session exposes the
// manager-backed tool groups (sub-agent, delegate, workflow) and therefore
// needs an AgentManager plus a registered lead agent.
//
// The lead agent must be registered into the manager before it can spawn a
// child: AgentManager.Create validates the parent ID against the registered
// agents and fails with "parent agent <id> not found" otherwise. Router mode
// installs the same delegation surface as a multi-agent session, so it must
// count here even when the adapter's multi-agent/delegate/workflow flags are
// all false. Every entry point must use this predicate so a session can never
// expose a delegation tool whose parent is unknown to the manager.
func ManagerBackedToolsEnabled(rt *SessionRuntime, multiAgent, delegate, workflows bool) bool {
	if SubAgentToolsEnabled(rt, multiAgent) || delegate || workflows {
		return true
	}
	return rt.RouterActive()
}

func removeSubAgentTools(registry *tools.Registry) {
	for _, name := range agent.SubAgentToolNames() {
		registry.Remove(name)
	}
	for _, name := range agent.RouterOnlySubAgentToolNames() {
		registry.Remove(name)
	}
}

// trimToolSelection re-applies an explicit per-tool selection after a full
// group install. A nil selection keeps the full set; a non-nil selection is
// authoritative so re-registering never resurrects a tool the user switched
// off.
func trimToolSelection(registry *tools.Registry, names []string, selection map[string]bool) {
	if selection == nil {
		return
	}
	for _, name := range names {
		if !selection[name] {
			registry.Remove(name)
		}
	}
}

// publishRouterTarget pushes the session's activated ACP worker into every tool
// that can start an external child. It runs on every tool-group synchronization,
// so activation, binding changes, and leaving Router mode are all reflected
// without rebuilding the tool instance.
func publishRouterTarget(rt *SessionRuntime, registry *tools.Registry, mode string) {
	if registry == nil {
		return
	}
	var target *agent.RouterTarget
	if mode == ModeRouter {
		if binding, hasBinding := rt.RouterBinding(); hasBinding && strings.TrimSpace(binding.ACPProfileID) != "" {
			target = &agent.RouterTarget{
				ID:       binding.ACPProfileID,
				Accepted: binding.ProfileIDs(),
			}
			// Other configured-but-unbound profiles appear only as correction
			// context; the bound allowlist is the accepted schema surface.
			if settings := rt.SettingsSnapshot(); settings != nil {
				for _, descriptor := range ListACPWorkerProfiles(settings) {
					if descriptor.RouterEligible {
						target.Available = append(target.Available, descriptor.ID)
					}
				}
			}
		}
	}
	for _, name := range []string{delegateSubAgentToolName, "subagent_spawn"} {
		tool, ok := registry.Get(name)
		if !ok {
			continue
		}
		switch typed := tool.(type) {
		case *agent.DelegateSubAgentTool:
			typed.SetRouterTarget(target)
		case *agent.SubAgentSpawnTool:
			typed.SetRouterTarget(target)
		}
	}
}
