package tui

import (
	"fmt"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/skills"
	"github.com/oschina/mothx/internal/tools"
)

// tuiRuntime wraps CLI-prepared resources in the shared Runtime. Resource
// ownership remains a migration bridge until CLI construction moves into Builder.
func tuiRuntime(sess *session.Manager, registry *tools.Registry, sandboxInfo, extraContext, ruleContent string, skillsMgr *skills.Manager, settings *config.Settings) *agentruntime.SessionRuntime {
	_ = sandboxInfo
	if sess == nil || registry == nil {
		return nil
	}
	header := sess.GetHeader()
	if header == nil {
		return nil
	}
	runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		ID: header.ID, Source: agentruntime.SourceTUI, WorkDir: header.Cwd, Manager: sess, Registry: registry,
		ExtraContext: extraContext, RuleContent: ruleContent, SkillsMgr: skillsMgr, Settings: settings,
		ArtifactEnabled: settings.IsArtifactEnabled(),
	})
	if err != nil {
		return nil
	}
	return runtime
}

// SetRuntime attaches the shared front-end-neutral runtime to the TUI. The
// adapter aliases are synchronized for compatibility with existing commands.
func (a *App) SetRuntime(runtime *agentruntime.SessionRuntime) {
	if a == nil || runtime == nil {
		return
	}
	a.runtime = runtime
	if runtime.Manager != nil {
		a.session = runtime.Manager
	}
	if runtime.Registry != nil {
		a.registry = runtime.Registry
	}
	if runtime.SandboxMgr != nil {
		a.sandboxInfo = sandbox.FormatSandboxInfo(runtime.SandboxMgr.GetActive())
	}
	if runtime.SkillsMgr != nil {
		a.skillsMgr = runtime.SkillsMgr
	}
	if runtime.ExtraContext != "" {
		a.extraContext = runtime.ExtraContext
		a.baseExtraContext = runtime.ExtraContext
	}
	if runtime.RuleContent != "" {
		a.ruleContent = runtime.RuleContent
	}
}

func (a *App) bindRuntimeSession(manager *session.Manager) error {
	if a == nil || a.runtime == nil {
		return nil
	}
	if err := a.runtime.BindSession(manager, agentruntime.SourceTUI); err != nil {
		return err
	}
	// BindSession can change the Runtime-owned expert binding, skills, and
	// forced team capability. Keep the TUI aliases and manager-backed tool
	// projection aligned with that single resolved Runtime state.
	a.SetRuntime(a.runtime)
	return a.refreshExpertAwareAgentManager()
}
func (a *App) effectiveRuntimeMode() (string, error) {
	if a == nil || a.runtime == nil {
		return "", fmt.Errorf("tui session runtime is unavailable")
	}
	_, mode, err := a.runtime.ResolvePolicy(a.mode, "", a.settings.DefaultMode)
	return mode, err
}

func (a *App) buildRuntimeAgent() (*agent.Agent, error) {
	if a == nil {
		return nil, fmt.Errorf("tui app is nil")
	}
	if err := a.ensureRuntime(); err != nil {
		return nil, err
	}
	mode, err := a.effectiveRuntimeMode()
	if err != nil {
		return nil, err
	}
	if a.run != nil {
		// The durable record must use the exact Runtime-resolved mode that is
		// passed to BuildAgent, not the adapter's requested/display value.
		a.run.mode = mode
	}
	return a.runtime.BuildAgent(agentruntime.AgentBuildOptions{
		ID: agentpkg.AgentID("agent-master"), Provider: a.provider, ProviderName: a.providerName,
		Model: a.model, Settings: a.settings, Allow: a.allow, Mode: mode,
		ExtraContext: a.extraContext, ThinkingLevel: provider.ThinkingLevel(a.settings.DefaultThinkingLevel),
		MultiAgent: a.multiAgent, DelegateMode: a.delegateMode, Workflows: a.workflows,
		ConversationTurn:    true,
		GetSteeringMessages: a.nextESMSteeringMessages,
	})
}

// ensureRuntime establishes the TUI's adapter handle to the single shared
// Runtime before any input is normalized. It deliberately owns no input,
// attachment, or provider-content state itself.
func (a *App) ensureRuntime() error {
	if a == nil {
		return fmt.Errorf("tui app is nil")
	}
	if a.runtime != nil {
		return nil
	}
	runtime := tuiRuntime(a.session, a.registry, a.sandboxInfo, a.extraContext, a.ruleContent, a.skillsMgr, a.settings)
	if runtime == nil {
		return fmt.Errorf("tui session runtime is unavailable")
	}
	a.SetRuntime(runtime)
	return nil
}
