package agent

import "github.com/startvibecoding/mothx/internal/tools"

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

// RegisterDelegateSubAgentTool registers the blocking single sub-agent
// delegation tool. It is independent from the async multi-agent toolset.
func RegisterDelegateSubAgentTool(registry *tools.Registry, manager *AgentManager) {
	if registry == nil || manager == nil {
		return
	}
	registry.Register(NewDelegateSubAgentTool(manager))
}
