package agentruntime

import (
	"fmt"
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
		// Each Router worker is one bounded external turn. The entire local
		// multi-agent surface is removed and replaced by the single management
		// signature, so a prior selection cannot leave a local-only tool alive.
		removeSubAgentTools(registry)
		registry.Remove(delegateSubAgentToolName)
		agent.RegisterRouterSubAgentTools(registry, manager)
	} else if SubAgentToolsEnabled(rt, policy.MultiAgent) {
		agent.RegisterSubAgentTools(registry, manager)
		if !team {
			trimToolSelection(registry, agent.SubAgentToolNames(), policy.SubAgentTools)
		}
	} else {
		removeSubAgentTools(registry)
	}
	// Router exposes only delegate_worker_mgr. The blocking local delegate tool is
	// independent and must never reappear in the Router surface.
	if mode != ModeRouter && policy.Delegate {
		agent.RegisterDelegateSubAgentTool(registry, manager)
	} else if mode != ModeRouter {
		registry.Remove(delegateSubAgentToolName)
	}
	if mode == ModeRouter {
		// Publish the activated worker to the management tool so its description
		// and `agent` schema carry the real profile ID instead of leaving the model
		// to guess a well-known product name.
		publishRouterTarget(rt, registry, mode)
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

// ManagerFactory constructs the session-scoped AgentManager used by the
// manager-backed tool groups. A factory may return (nil, nil) when the session
// deliberately has no usable manager (for example no configured provider); the
// Runtime then reconciles the registry to an empty manager-backed surface.
type ManagerFactory func() (*agent.AgentManager, error)

// ManagerBackedToolsOptions is the adapter-supplied input to one Runtime-owned
// manager/tool reconciliation.
//
// Adapters supply their resolved capability policy, the manager handle they
// currently hold, and a factory for a session-scoped manager. The Runtime owns
// whether a manager is required, when it must be rebuilt, and how the
// manager-backed tool groups are installed or removed. This is the single
// post-activation/refresh entry point for TUI, CLI, WebUI/API, ACP, and
// channels; adapters must not open-code the predicate plus installer pair.
type ManagerBackedToolsOptions struct {
	// Registry overrides the Runtime registry. Nil uses the Runtime's Registry.
	Registry *tools.Registry
	// Policy is the adapter's resolved capability/tool-selection policy.
	Policy ToolGroupPolicy
	// Manager is the adapter's current manager handle.
	Manager *agent.AgentManager
	// Build creates a session-scoped manager. It is called only when the
	// Runtime resolves that a manager-backed capability is active and either
	// Manager is nil or Rebuild is true.
	Build ManagerFactory
	// Rebuild discards Manager before Build so manager-owned session state
	// (expert roster/mailbox, Router worker binding, provider/model changes) is
	// recreated instead of reusing a stale roster. Build is required when
	// Rebuild is set and Manager is non-nil; the Runtime fails closed otherwise.
	Rebuild bool
	// RequireManager keeps a manager even when the capability predicate is
	// false. The TUI and CLI use it because root-agent bookkeeping, ESM
	// supervision, and the cron scheduler hold the manager handle independently
	// of which manager-backed tools the model can see.
	RequireManager bool
}

// ReconcileManagerBackedTools is the one Runtime entry point for the
// "ensure the session manager, then synchronize the manager-backed tool
// groups" sequence. It decides from resolved Runtime capability (including a
// bound expert team and Router mode) whether a manager is required, builds or
// reuses one, and installs/removes the tool groups against that exact manager.
//
// On a build error the current manager handle is preserved and returned with
// the error so the caller can keep its previous projection instead of
// silently discarding a manager that is still running children. Passing a nil
// factory with no current manager is valid: the tool groups are removed. A
// Rebuild with no factory is an error because silently reusing the handle
// would defeat the caller's explicit request to discard stale manager state.
//
// When the capability is off the tool groups are always removed, but a manager
// that still owns a running child is handed back instead of dropped so the
// adapter can keep observing and cancelling it.
func (r *SessionRuntime) ReconcileManagerBackedTools(opts ManagerBackedToolsOptions) (*agent.AgentManager, error) {
	registry := opts.Registry
	if registry == nil && r != nil {
		registry = r.Registry
	}
	required := ManagerBackedToolsEnabled(r, opts.Policy.MultiAgent, opts.Policy.Delegate, opts.Policy.Workflows) || opts.RequireManager
	if !required {
		SynchronizeToolGroups(r, registry, opts.Policy, nil)
		if opts.Manager != nil && opts.Manager.HasRunningChildren() {
			// No manager-backed capability is visible, but a live child still
			// needs its owning manager for status and cancellation. Return the
			// handle instead of orphaning it; the tool surface stays empty.
			return opts.Manager, nil
		}
		return nil, nil
	}
	manager := opts.Manager
	if manager == nil || opts.Rebuild {
		switch {
		case opts.Build != nil:
			built, err := opts.Build()
			if err != nil {
				return manager, err
			}
			manager = built
		case manager == nil:
			// A manager is required but the caller supplied no construction
			// path. Remove the manager-backed tools instead of leaving tools
			// that could only fail at spawn time.
			SynchronizeToolGroups(r, registry, opts.Policy, nil)
			return nil, nil
		default:
			// Rebuild without a factory cannot produce a fresh manager. Fail
			// closed instead of silently reusing the stale handle the caller
			// asked to discard.
			return manager, fmt.Errorf("manager-backed tool reconciliation: rebuild requires a manager factory")
		}
	}
	SynchronizeToolGroups(r, registry, opts.Policy, manager)
	return manager, nil
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

// RefreshRouterTarget republishes the current Router target from Runtime facts.
// The ACP child proxy calls it after a real worker initialize so the latest
// capability advertisement reaches delegate_worker_mgr without waiting for an
// adapter-side tool-group synchronization. It is a no-op outside Router mode,
// and the tool description is read lazily when the next prompt is frozen.
func (r *SessionRuntime) RefreshRouterTarget() {
	if r == nil {
		return
	}
	r.mu.RLock()
	mode := r.Mode
	r.mu.RUnlock()
	publishRouterTarget(r, r.Registry, mode)
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
			// Publish a short, model-facing capability hint derived from the same
			// resolver the invocation path uses. Bridge availability, remote/off
			// profiles, capabilityOverrides, and the latest initialize
			// advertisement all participate, so the prompt never advertises
			// report/ask for a worker that cannot reach a live bridge.
			target.Summary = map[string]string{}
			bridge := rt.workerBridge()
			bridgeAvailable := bridge != nil && bridge.Available()
			if settings := rt.SettingsSnapshot(); settings != nil {
				for _, profileID := range target.Accepted {
					profile, ok := settings.ACPAgentProfile(profileID)
					if !ok {
						continue
					}
					target.Summary[profileID] = publishedWorkerCapabilitySummary(
						publishedWorkerCapabilities(rt, profile), profile, bridgeAvailable, rt.workerAdvertisedCapabilities(profileID),
					)
				}
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
	if tool, ok := registry.Get("delegate_worker_mgr"); ok {
		if typed, ok := tool.(*agent.DelegateWorkerMgrTool); ok {
			typed.SetRouterTarget(target)
		}
	}
}
