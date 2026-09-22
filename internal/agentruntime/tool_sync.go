package agentruntime

import (
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
	if SubAgentToolsEnabled(rt, policy.MultiAgent) {
		agent.RegisterSubAgentTools(registry, manager)
		if !team {
			trimToolSelection(registry, agent.SubAgentToolNames(), policy.SubAgentTools)
		}
	} else {
		removeSubAgentTools(registry)
	}
	if policy.Delegate {
		agent.RegisterDelegateSubAgentTool(registry, manager)
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

func removeSubAgentTools(registry *tools.Registry) {
	for _, name := range agent.SubAgentToolNames() {
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
