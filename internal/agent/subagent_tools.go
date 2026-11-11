package agent

import "github.com/oschina/mothx/internal/tools"

// SubAgentToolNames returns the canonical async sub-agent toolset, in
// registration order. Every adapter that installs or uninstalls these tools
// (factory child registries, expert bind/unbind, channel tool catalogs) must
// derive its list from here instead of repeating the names: a hand-copied list
// silently keeps a removed tool alive in one entry point.
//
// A fresh slice is returned so callers cannot mutate the shared definition.
func SubAgentToolNames() []string {
	return []string{"subagent_spawn", "subagent_status", "subagent_send", "subagent_answer", "subagent_destroy", "subagent_wait"}
}

// RouterSubAgentToolNames returns the tools that are valid for an external
// Router worker. Router has one complete management signature: every lifecycle
// action (delegate/spawn/status/list/send/wait/destroy/integrate/answer) is an
// action of delegate_worker_mgr, not a separate tool. answer is intentionally
// absent from the local surface: ACP-worker questions resolve through the
// parent DecisionService, not the local member question registry.
func RouterSubAgentToolNames() []string {
	return []string{delegateWorkerMgrToolName}
}

// RouterOnlySubAgentToolNames are tools installed only for an external Router
// worker surface. Local multi-agent sessions must never see them.
func RouterOnlySubAgentToolNames() []string {
	return []string{delegateWorkerMgrToolName}
}

// RegisterSubAgentTools registers the built-in sub-agent tools when multi-agent
// mode is enabled. It is safe to call more than once; Registry.Register replaces
// existing tools without duplicating their order.
func RegisterSubAgentTools(registry *tools.Registry, manager *AgentManager) {
	if registry == nil || manager == nil {
		return
	}
	registry.Register(NewSubAgentSpawnTool(manager))
	registry.Register(NewSubAgentStatusTool(manager))
	registry.Register(NewSubAgentSendTool(manager))
	registry.Register(NewSubAgentAnswerTool(manager))
	registry.Register(NewSubAgentDestroyTool(manager))
	registry.Register(NewSubAgentWaitTool(manager))
}

// RegisterRouterSubAgentTools installs the single Router management tool. The
// local delegate_subagent / subagent_* surface is deliberately not installed:
// Router differences are expressed by the management tool's action contract,
// not by two parallel tool sets.
func RegisterRouterSubAgentTools(registry *tools.Registry, manager *AgentManager) {
	if registry == nil || manager == nil {
		return
	}
	registry.Register(NewDelegateWorkerMgrTool(manager))
}

// RegisterDelegateSubAgentTool registers the blocking single sub-agent
// delegation tool. It is independent from the async multi-agent toolset and is
// never installed in Router mode (Router uses delegate_worker_mgr).
func RegisterDelegateSubAgentTool(registry *tools.Registry, manager *AgentManager) {
	if registry == nil || manager == nil {
		return
	}
	registry.Register(NewDelegateSubAgentTool(manager))
}
