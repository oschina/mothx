package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/expert"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/workflow"
)

// handleExpertCommand is a thin TUI projection of Runtime-owned expert
// discovery and identity transitions. It never parses packages itself or
// writes session bindings directly: Runtime SetExpert and ForkWithExpert own
// those operations so every entry point observes the same session identity.
func (a *App) handleExpertCommand(parts []string) {
	if len(parts) < 2 {
		a.addCommandStatus(commandUsage(a.translator, "/expert list|show <id>|bind <id>|unbind|switch <id>"))
		return
	}

	switch strings.ToLower(parts[1]) {
	case "list", "ls":
		if len(parts) != 2 {
			a.addCommandError(commandUsage(a.translator, "/expert list"))
			return
		}
		a.listExperts()
	case "show":
		if len(parts) != 3 {
			a.addCommandError(commandUsage(a.translator, "/expert show <id>"))
			return
		}
		a.showExpert(parts[2])
	case "bind":
		if len(parts) != 3 {
			a.addCommandError(commandUsage(a.translator, "/expert bind <id>"))
			return
		}
		a.bindExpert(parts[2])
	case "unbind":
		if len(parts) != 2 {
			a.addCommandError(commandUsage(a.translator, "/expert unbind"))
			return
		}
		a.unbindExpert()
	case "switch":
		if len(parts) != 3 {
			a.addCommandError(commandUsage(a.translator, "/expert switch <id>"))
			return
		}
		a.switchExpert(parts[2])
	default:
		a.addCommandError("Unknown expert command: " + parts[1])
	}
}

func (a *App) expertRuntime() (*agentruntime.SessionRuntime, error) {
	if err := a.ensureRuntime(); err != nil {
		return nil, err
	}
	if a.runtime == nil {
		return nil, fmt.Errorf("expert runtime is unavailable")
	}
	return a.runtime, nil
}

func (a *App) listExperts() {
	runtime, err := a.expertRuntime()
	if err != nil {
		a.addCommandError("List experts failed: " + err.Error())
		return
	}
	summaries := runtime.ListExperts()
	if len(summaries) == 0 {
		a.addCommandStatus("Experts: none found")
		return
	}
	boundID := ""
	if binding, _ := runtime.ExpertState(); binding != nil {
		boundID = binding.ID
	}
	var out strings.Builder
	out.WriteString("Experts:\n\n")
	for _, summary := range summaries {
		marker := " "
		if summary.Name == boundID {
			marker = "*"
		}
		name := a.localizedExpertText(summary.DisplayName)
		if name == "" {
			name = summary.Name
		}
		line := fmt.Sprintf("  [%s] %s  %s (%s, %s)", marker, summary.Name, name, summary.ExpertType, summary.Source)
		if summary.Invalid {
			line += ": invalid — " + summary.InvalidReason
		}
		out.WriteString(line + "\n")
	}
	out.WriteString("\nUse /expert show <id> to inspect, or /expert bind <id> to bind this session.")
	a.addCommandStatus(out.String())
}

func (a *App) showExpert(expertID string) {
	runtime, err := a.expertRuntime()
	if err != nil {
		a.addCommandError("Show expert failed: " + err.Error())
		return
	}
	bundle, err := runtime.InspectExpert(expertID)
	if err != nil {
		a.addCommandError("Show expert failed: " + err.Error())
		return
	}
	a.addCommandStatus(a.formatExpertBundle(bundle))
}

func (a *App) bindExpert(expertID string) {
	if !a.expertControlIdle() {
		a.addCommandError("Cannot change expert while a run or member is active.")
		return
	}
	if err := a.ensureSession(); err != nil {
		a.addCommandError("Create session for expert binding failed: " + err.Error())
		return
	}
	runtime, err := a.expertRuntime()
	if err != nil {
		a.addCommandError("Bind expert failed: " + err.Error())
		return
	}
	bundle, err := runtime.InspectExpert(expertID)
	if err != nil {
		a.addCommandError("Bind expert failed: " + err.Error())
		return
	}
	if bundle.Invalid {
		a.addCommandError(fmt.Sprintf("Bind expert failed: expert bundle %q is invalid: %s", bundle.Name, bundle.InvalidReason))
		return
	}
	if err := runtime.SetExpert(expertID); err != nil {
		if errors.Is(err, agentruntime.ErrExpertSwitchRequiresFork) {
			a.addCommandError("This session already has an expert. Use /expert switch <id> to preserve its history in a fork.")
			return
		}
		a.addCommandError("Bind expert failed: " + err.Error())
		return
	}
	a.SetRuntime(runtime)
	a.resetAgent(fmt.Errorf("expert bound"))
	if err := a.refreshExpertAwareAgentManager(); err != nil {
		a.addCommandError("Bind expert failed: " + err.Error())
		return
	}
	a.addCommandStatus(fmt.Sprintf("Expert bound: %s (%s)", bundle.Name, a.localizedExpertText(bundle.Manifest.DisplayName)))
	if bundle.Manifest.ExpertType == expert.TypeTeam {
		a.addCommandStatus(a.teamExpertUsageHint())
	}
}

func (a *App) unbindExpert() {
	if !a.expertControlIdle() {
		a.addCommandError("Cannot change expert while a run or member is active.")
		return
	}
	if err := a.ensureSession(); err != nil {
		a.addCommandError("Create session for expert unbind failed: " + err.Error())
		return
	}
	runtime, err := a.expertRuntime()
	if err != nil {
		a.addCommandError("Unbind expert failed: " + err.Error())
		return
	}
	binding, _ := runtime.ExpertState()
	if binding == nil {
		a.addCommandStatus("No expert is bound to this session.")
		return
	}
	if err := runtime.SetExpert(""); err != nil {
		a.addCommandError("Unbind expert failed: " + err.Error())
		return
	}
	a.SetRuntime(runtime)
	a.resetAgent(fmt.Errorf("expert unbound"))
	if err := a.refreshExpertAwareAgentManager(); err != nil {
		a.addCommandError("Unbind expert failed: " + err.Error())
		return
	}
	a.addCommandStatus("Expert unbound: " + binding.ID)
}

func (a *App) switchExpert(expertID string) {
	if !a.expertControlIdle() {
		a.addCommandError("Cannot switch expert while a run or member is active.")
		return
	}
	if err := a.ensureSession(); err != nil {
		a.addCommandError("Create session for expert switch failed: " + err.Error())
		return
	}
	runtime, err := a.expertRuntime()
	if err != nil {
		a.addCommandError("Switch expert failed: " + err.Error())
		return
	}
	binding, _ := runtime.ExpertState()
	if binding == nil {
		a.addCommandError("No expert is bound. Use /expert bind <id> instead.")
		return
	}
	bundle, err := runtime.InspectExpert(expertID)
	if err != nil {
		a.addCommandError("Switch expert failed: " + err.Error())
		return
	}
	if bundle.Invalid {
		a.addCommandError(fmt.Sprintf("Switch expert failed: expert bundle %q is invalid: %s", bundle.Name, bundle.InvalidReason))
		return
	}
	if bundle.Name == binding.ID {
		a.addCommandStatus("Expert is already bound: " + binding.ID)
		return
	}
	if a.session == nil || a.session.GetHeader() == nil {
		a.addCommandError("No active session to fork.")
		return
	}
	result, err := agentruntime.ForkWithExpert(context.Background(), a.getSessionDir(), agentruntime.ForkOptions{
		SourceSessionID: a.session.GetHeader().ID,
		RequestID:       "tui-expert-switch-" + session.GenerateID(),
	}, bundle.Name)
	if err != nil {
		a.addCommandError("Switch expert failed: " + err.Error())
		return
	}
	child, err := session.OpenByIDExact(a.getSessionDir(), result.SessionID)
	if err != nil || child.GetHeader() == nil {
		if err == nil {
			err = fmt.Errorf("forked session header is unavailable")
		}
		a.addCommandError("Open forked expert session failed: " + err.Error())
		return
	}
	header := child.GetHeader()
	detail := session.SessionDetail{
		SessionInfo:  session.SessionInfo{Path: child.GetFile(), ModTime: header.Timestamp, Cwd: header.Cwd, ParentSession: header.ParentSession, ForkBoundarySeq: header.ForkBoundarySeq, SeedLength: header.SeedLength, ForkKind: header.ForkKind},
		ID:           result.SessionID,
		MessageCount: len(child.GetMessages()),
	}
	if err := a.switchToSession(detail); err != nil {
		a.addCommandError("Open forked expert session failed: " + err.Error())
		return
	}
	a.addCommandStatus(fmt.Sprintf("Expert switched: %s → %s (forked session %s)", binding.ID, bundle.Name, result.SessionID))
}

func (a *App) expertControlIdle() bool {
	return a != nil && !a.isThinking && (a.agentMgr == nil || !a.agentMgr.HasRunning())
}

// agentManagementEnabled includes the Runtime-owned forced team capability;
// an adapter flag alone must not hide a bound team's manager or its status.
func (a *App) agentManagementEnabled() bool {
	return a != nil && (a.multiAgent || a.delegateMode || a.workflows || (a.runtime != nil && a.runtime.TeamExpertActive()))
}

// refreshExpertAwareAgentManager rebuilds the manager from the authoritative
// Runtime after a session bind or expert transition. The manager owns the
// resolved roster/mailbox context; retaining an old manager would leak a
// previous session's team into this session's tool surface.
func (a *App) refreshExpertAwareAgentManager() error {
	if a == nil || a.runtime == nil || a.provider == nil || a.model == nil || a.settings == nil {
		return nil
	}
	manager, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
		Runtime: a.runtime, Provider: a.provider, ProviderName: a.activeProviderName(), Model: a.model,
		Settings: a.settings, Allow: a.allow, MultiAgentEnabled: a.multiAgent,
		DelegateEnabled: a.delegateMode, WorkflowsEnabled: a.workflows,
	})
	if err != nil {
		return err
	}
	a.agentMgr = manager
	a.activeAgent = ""
	if a.registry == nil {
		return nil
	}
	if agentruntime.SubAgentToolsEnabled(a.runtime, a.multiAgent) {
		agent.RegisterSubAgentTools(a.registry, manager)
	} else {
		removeTUISubAgentTools(a.registry)
	}
	if a.delegateMode {
		agent.RegisterDelegateSubAgentTool(a.registry, manager)
	} else {
		a.registry.Remove("delegate_subagent")
	}
	if a.workflows {
		workflow.RegisterTools(a.registry, manager, nil)
	}
	return nil
}

func removeTUISubAgentTools(registry interface{ Remove(string) }) {
	if registry == nil {
		return
	}
	for _, name := range agent.SubAgentToolNames() {
		registry.Remove(name)
	}
}

func (a *App) localizedExpertText(text expert.LocalizedText) string {
	if a != nil && a.translator.Language() == "zh" && strings.TrimSpace(text.Zh) != "" {
		return text.Zh
	}
	if strings.TrimSpace(text.En) != "" {
		return text.En
	}
	return text.Zh
}

// teamExpertUsageHint is intentionally presented at the bind boundary, where
// a user first opts into fan-out. It is advisory only: budget and rate-limit
// enforcement remain the existing Runtime/provider policy, not a TUI fork.
func (a *App) teamExpertUsageHint() string {
	if a != nil && a.translator.Language() == "zh" {
		return "消耗提示：团队主角通常会并行调用多个成员，Token 与工具调用消耗可能是单主角的数倍。"
	}
	return "Usage note: team experts may run several members in parallel, so token and tool-call usage can be several times higher than a single expert."
}

func (a *App) formatExpertBundle(bundle *expert.Bundle) string {
	if bundle == nil {
		return "Expert: unavailable"
	}
	var out strings.Builder
	out.WriteString("Expert: " + bundle.Name + "\n")
	out.WriteString("Name: " + a.localizedExpertText(bundle.Manifest.DisplayName) + "\n")
	out.WriteString("Type: " + bundle.Manifest.ExpertType + "\n")
	if bundle.Invalid {
		out.WriteString("Status: invalid — " + bundle.InvalidReason)
		return out.String()
	}
	out.WriteString("Status: available\n")
	if bundle.Manifest.ExpertType == expert.TypeTeam {
		out.WriteString("Members:\n")
		for _, member := range bundle.Manifest.Members {
			line := fmt.Sprintf("  - %s", member.ID)
			if name := a.localizedExpertText(member.Name); name != "" && name != member.ID {
				line += " (" + name + ")"
			}
			if profession := a.localizedExpertText(member.Profession); profession != "" {
				line += ": " + profession
			}
			if member.Role != "" {
				line += " [" + member.Role + "]"
			}
			out.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(out.String(), "\n")
}
