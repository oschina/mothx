package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	browserfeature "github.com/startvibecoding/mothx/internal/browser"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/contextfiles"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/systeminit"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/tui/i18n"
	"github.com/startvibecoding/mothx/internal/workflow"
)

// handleAgentCommand handles /agent subcommands (multi-agent mode).
func (a *App) handleAgentCommand(parts []string) {
	if !a.agentManagementEnabled() {
		a.addCommandError(a.translator.Text(i18n.MsgMultiAgentDisabled))
		return
	}
	if len(parts) < 2 {
		a.addCommandStatus(commandUsage(a.translator, "/agent list|switch|destroy"))
		return
	}
	switch parts[1] {
	case "list":
		a.listAgents()
	case "switch":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/agent switch <id>"))
			return
		}
		a.switchAgent(agentpkg.AgentID(parts[2]))
	case "destroy":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/agent destroy <id>"))
			return
		}
		a.destroyAgent(agentpkg.AgentID(parts[2]))
	default:
		a.addCommandError(a.translator.Text(i18n.MsgCommandAgentUnknown, parts[1]))
	}
}

func (a *App) listAgents() {
	a.addCommandStatus(a.translator.Text(i18n.MsgCommandMultiAgentStatus, a.activeAgent))
	if a.agentMgr == nil {
		a.addCommandStatus("  " + a.translator.Text(i18n.MsgCommandAgentManagerUnavailable))
		return
	}

	ids := a.agentMgr.List()
	if len(ids) == 0 {
		a.addCommandStatus("  " + a.translator.Text(i18n.MsgCommandNoAgents))
		return
	}

	for _, id := range ids {
		parentID, hasParent := a.agentMgr.Parent(id)
		children := a.agentMgr.Children(id)
		status := "running"
		if id == a.activeAgent {
			status = "active"
		}

		info := fmt.Sprintf("  %s [%s]", id, status)
		if hasParent {
			info += fmt.Sprintf(" parent=%s", parentID)
		}
		if len(children) > 0 {
			info += fmt.Sprintf(" children=%d", len(children))
		}
		a.addCommandStatus(info)
	}
}

func (a *App) switchAgent(id agentpkg.AgentID) {
	if a.agentMgr == nil {
		a.addCommandError(a.translator.Text(i18n.MsgCommandAgentManagerUnavailable))
		return
	}

	_, ok := a.agentMgr.Get(id)
	if !ok {
		a.addCommandError(a.translator.Text(i18n.MsgCommandAgentNotFound, id))
		return
	}

	a.activeAgent = id
	a.addCommandStatus(a.translator.Text(i18n.MsgCommandAgentFocused, id), a.translator.Text(i18n.MsgCommandAgentInputHint))
}

func (a *App) handleDelegateCommand(parts []string) {
	if len(parts) < 2 || parts[1] == "status" {
		state := "OFF"
		if a.delegateMode {
			state = "ON"
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgCommandDelegateStatus, state))
		return
	}
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgCommandDelegateRunning))
		return
	}
	switch parts[1] {
	case "on":
		if a.agentMgr == nil {
			a.addCommandError(a.translator.Text(i18n.MsgCommandAgentManagerUnavailable))
			return
		}
		agent.RegisterDelegateSubAgentTool(a.registry, a.agentMgr)
		a.delegateMode = true
		a.resetAgent(fmt.Errorf("delegate mode changed"))
		a.addCommandStatus(a.translator.Text(i18n.MsgCommandDelegateChanged, "ON"))
	case "off":
		a.registry.Remove("delegate_subagent")
		a.resetAgent(fmt.Errorf("delegate mode changed"))
		a.delegateMode = false
		a.addCommandStatus(a.translator.Text(i18n.MsgCommandDelegateChanged, "OFF"))
	default:
		a.addCommandError(commandUsage(a.translator, "/delegate [on|off|status]"))
	}
}

func (a *App) handleBrowserCommand(parts []string) {
	if len(parts) > 2 {
		a.addCommandError(commandUsage(a.translator, "/browser [on|off|status]"))
		return
	}
	sub := "status"
	if len(parts) == 2 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "status":
		state := "OFF"
		if a.browserEnabled && browserfeature.IsToolRegistered(a.registry) {
			state = "ON"
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgCommandBrowserStatus, state), commandUsage(a.translator, "/browser [on|off|status]"))
	case "on":
		a.enableBrowserTool()
	case "off":
		a.disableBrowserTool()
	default:
		a.addCommandError(commandUsage(a.translator, "/browser [on|off|status]"))
	}
}

func (a *App) enableBrowserTool() {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgCommandBrowserRunning))
		return
	}
	if a.registry == nil {
		a.addCommandError(a.translator.Text(i18n.MsgCommandToolRegistryUnavailable))
		return
	}
	cwd := a.currentCwd()
	globalSkillsDir := ""
	if a.settings != nil {
		globalSkillsDir = a.settings.GetGlobalSkillsDir()
	}
	a.skillsMgr = skills.NewManagerWithProjectDirs(globalSkillsDir, skills.ProjectSkillDirs(cwd))
	if err := a.skillsMgr.Load(); err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgCommandBrowserSkillLoadFailed, err))
		return
	}

	a.registry.Register(tools.NewSkillRefTool(a.skillsMgr))
	browserfeature.RegisterTool(a.registry)
	a.browserEnabled = true
	a.browserSkillInBase = false
	a.browserSkillContext = a.skillsMgr.BuildSkillContext(browserfeature.SkillName)
	a.markBuiltinActiveSkills()
	a.rebuildExtraContext()
	a.resetAgent(fmt.Errorf("browser tool changed"))

	a.addCommandStatus(a.translator.Text(i18n.MsgCommandBrowserStatus, "ON"), "Using built-in browser skill")
}

func (a *App) disableBrowserTool() {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgCommandBrowserRunning))
		return
	}
	browserfeature.RemoveTool(a.registry)
	a.browserEnabled = false
	delete(a.activeSkills, browserfeature.SkillName)
	if a.browserSkillInBase && a.browserSkillContext != "" {
		a.baseExtraContext = strings.Replace(a.baseExtraContext, a.browserSkillContext, "", 1)
	}
	a.browserSkillInBase = false
	a.browserSkillContext = ""
	a.rebuildExtraContext()
	a.resetAgent(fmt.Errorf("browser tool changed"))
	a.addCommandStatus(a.translator.Text(i18n.MsgCommandBrowserStatus, "OFF"))
}

// handleAllowEditPathCommand manages the auto-edit path whitelist (allow.json).
func (a *App) handleAllowEditPathCommand(parts []string) {
	if a.allow == nil {
		a.allow = config.LoadAllow()
	}
	if len(parts) < 2 {
		paths := a.allow.EditPathList()
		if len(paths) == 0 {
			a.addCommandStatus(commandUsage(a.translator, "/alloweditpath add|remove <glob>|clear"))
			return
		}
		var sb strings.Builder
		sb.WriteString(a.translator.Text(i18n.MsgAllowEditPathTitle) + "\n")
		for _, p := range paths {
			sb.WriteString(fmt.Sprintf("  %s\n", p))
		}
		a.addCommandStatus(strings.TrimRight(sb.String(), "\n"))
		return
	}
	switch parts[1] {
	case "add":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/alloweditpath add <glob>"))
			return
		}
		glob := strings.Join(parts[2:], " ")
		if !a.allow.AddEditPath(glob) {
			a.addCommandStatus(a.translator.Text(i18n.MsgAllowEditPathAlready, glob))
			return
		}
		if err := a.allow.SaveProject(); err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgAllowEditPathSaveFailed, err))
			return
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgAllowEditPathSaved, a.translator.Text(i18n.MsgAllowEditPathAdded), glob))
	case "remove", "rm":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/alloweditpath remove <glob>"))
			return
		}
		glob := strings.Join(parts[2:], " ")
		if !a.allow.RemoveEditPath(glob) {
			a.addCommandStatus(a.translator.Text(i18n.MsgAllowEditPathNotFound, glob))
			return
		}
		if err := a.allow.SaveProject(); err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgAllowEditPathSaveFailed, err))
			return
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgAllowEditPathSaved, a.translator.Text(i18n.MsgAllowEditPathRemoved), glob))
	case "clear":
		a.allow.ClearEditPaths()
		if err := a.allow.SaveProject(); err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgAllowEditPathSaveFailed, err))
			return
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgAllowEditPathCleared))
	default:
		a.addCommandError(commandUsage(a.translator, "/alloweditpath [add <glob>|remove <glob>|clear]"))
	}
}

// handleAllowAutoEditCommand toggles full auto-edit in agent mode (allow.json).
// With a trailing "global" argument the autoEdit flag is persisted to the
// global allow.json instead of the project one.
func (a *App) handleAllowAutoEditCommand(parts []string) {
	if a.allow == nil {
		a.allow = config.LoadAllow()
	}
	if len(parts) < 2 {
		state := "OFF"
		if a.allow.GetAutoEdit() {
			state = "ON"
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgAllowAutoEditStatus, state))
		a.addCommandStatus(commandUsage(a.translator, "/allowautoedit [on|off] [global]"))
		return
	}
	globalScope := false
	for _, p := range parts[2:] {
		if p == "global" {
			globalScope = true
		}
	}
	var enable bool
	switch parts[1] {
	case "on":
		enable = true
	case "off":
		enable = false
	default:
		a.addCommandError(commandUsage(a.translator, "/allowautoedit [on|off] [global]"))
		return
	}
	var err error
	scope := "project"
	effective := enable
	if globalScope {
		scope = "global"
		effective = a.allow.SetGlobalAutoEdit(enable)
		err = a.allow.SaveGlobalAutoEditValue(enable)
	} else {
		a.allow.SetProjectAutoEdit(enable)
		err = a.allow.SaveProject()
	}
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgAllowEditPathSaveFailed, err))
		return
	}
	state := "OFF"
	if enable {
		state = "ON"
	}
	msg := fmt.Sprintf("\u2705 Auto-edit (agent mode): %s [%s]", state, scope)
	if globalScope && effective != enable {
		effectiveState := "OFF"
		if effective {
			effectiveState = "ON"
		}
		msg += fmt.Sprintf(" (effective here: %s due to project override)", effectiveState)
	}
	a.addCommandStatus(msg)
}

func (a *App) destroyAgent(id agentpkg.AgentID) {
	if a.agent != nil && id == a.agent.ID() {
		a.addCommandError(a.translator.Text(i18n.MsgAgentCannotDestroyMain))
		return
	}

	if a.agentMgr == nil {
		a.addCommandError(a.translator.Text(i18n.MsgCommandAgentManagerUnavailable))
		return
	}

	if err := a.agentMgr.Destroy(id); err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgAgentDestroyFailed, id, err))
		return
	}

	// If we destroyed the active agent, switch to main
	if a.activeAgent == id {
		a.activeAgent = "main"
	}

	a.addCommandStatus(a.translator.Text(i18n.MsgAgentDestroyed, id))
}

// handleCronCommand handles /cron subcommands (multi-agent mode).
func (a *App) handleCronCommand(parts []string) {
	if !a.multiAgent {
		a.addCommandError(a.translator.Text(i18n.MsgCronRequiresMultiAgent))
		return
	}
	if a.cronStore == nil {
		a.addCommandError(a.translator.Text(i18n.MsgCronStoreUnavailable))
		return
	}
	if len(parts) < 2 {
		a.addCommandStatus(commandUsage(a.translator, "/cron add|list|enable|disable|remove|run"))
		return
	}
	switch parts[1] {
	case "add":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/cron add <description>"))
			return
		}
		desc := strings.Join(parts[2:], " ")
		job, err := a.cronStore.Create(cron.CronJob{
			Name:    desc,
			Prompt:  desc,
			Enabled: true,
			Mode:    a.mode,
		})
		if err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgCronCreateFailed, err))
			return
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgCronCreated, job.Name, job.ID))
	case "list":
		jobs, err := a.cronStore.List()
		if err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgCronListFailed, err))
			return
		}
		if len(jobs) == 0 {
			a.addCommandStatus(a.translator.Text(i18n.MsgCronListEmpty))
			return
		}
		var sb strings.Builder
		sb.WriteString(a.translator.Text(i18n.MsgCronListTitle, len(jobs)) + "\n")
		for _, j := range jobs {
			status := "✅"
			if !j.Enabled {
				status = "⏸"
			}
			if j.LastStatus == "failed" {
				status = "❌"
			}
			sb.WriteString(a.translator.Text(i18n.MsgCronEntry, status, j.ID, j.Name, j.RunCount) + "\n")
		}
		a.addCommandStatus(sb.String())
	case "enable":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/cron enable <id>"))
			return
		}
		job, err := a.cronStore.Get(parts[2])
		if err != nil {
			a.addCommandError(fmt.Sprintf("%v", err))
			return
		}
		job.Enabled = true
		a.cronStore.Update(*job)
		a.addCommandStatus(a.translator.Text(i18n.MsgCronChanged, job.ID, a.translator.Text(i18n.MsgCronChangedEnabled)))
	case "disable":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/cron disable <id>"))
			return
		}
		job, err := a.cronStore.Get(parts[2])
		if err != nil {
			a.addCommandError(fmt.Sprintf("%v", err))
			return
		}
		job.Enabled = false
		a.cronStore.Update(*job)
		a.addCommandStatus(a.translator.Text(i18n.MsgCronChanged, job.ID, a.translator.Text(i18n.MsgCronChangedDisabled)))
	case "remove":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/cron remove <id>"))
			return
		}
		if err := a.cronStore.Delete(parts[2]); err != nil {
			a.addCommandError(fmt.Sprintf("%v", err))
			return
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgCronChanged, parts[2], a.translator.Text(i18n.MsgCronChangedRemoved)))
	case "run":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/cron run <id>"))
			return
		}
		job, err := a.cronStore.Get(parts[2])
		if err != nil {
			a.addCommandError(fmt.Sprintf("%v", err))
			return
		}
		if a.scheduler == nil {
			a.addCommandError(a.translator.Text(i18n.MsgCronSchedulerUnavailable))
			return
		}
		// Trigger immediate run by resetting LastRun
		job.LastRun = time.Time{}
		a.cronStore.Update(*job)
		a.addCommandStatus(a.translator.Text(i18n.MsgCronTriggered, job.ID))
	default:
		a.addCommandError(a.translator.Text(i18n.MsgCronUnknownCommand, parts[1]))
	}
}

func (a *App) handleWorkflowsCommand(parts []string) {
	store := workflow.DefaultStore()
	sub := "list"
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list", "ls":
		runs, err := store.List(context.Background())
		if err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgWorkflowListFailed, err))
			return
		}
		if len(runs) == 0 {
			a.addCommandStatus(a.translator.Text(i18n.MsgWorkflowListEmpty))
			return
		}
		var sb strings.Builder
		sb.WriteString(a.translator.Text(i18n.MsgWorkflowListTitle, len(runs)) + "\n")
		for _, run := range runs {
			sb.WriteString(a.translator.Text(i18n.MsgWorkflowListEntry, run.Status, run.ID, run.Name, run.UpdatedAt.Format(time.RFC3339)) + "\n")
		}
		a.addCommandStatus(strings.TrimRight(sb.String(), "\n"))
	case "show":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/workflows show <id>"))
			return
		}
		run, err := store.Load(context.Background(), parts[2])
		if err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgWorkflowShowFailed, err))
			return
		}
		var sb strings.Builder
		sb.WriteString(a.translator.Text(i18n.MsgWorkflowTitle, run.ID, run.Status) + "\n")
		if run.Name != "" {
			sb.WriteString(a.translator.Text(i18n.MsgWorkflowName, run.Name) + "\n")
		}
		for _, phase := range run.Phases {
			sb.WriteString(a.translator.Text(i18n.MsgWorkflowPhase, phase.Status, phase.Name, len(phase.Tasks)) + "\n")
		}
		for key, result := range run.Results {
			sb.WriteString(a.translator.Text(i18n.MsgWorkflowResultEntry, key, result.Status, strings.TrimSpace(result.Result)))
		}
		if run.Error != "" {
			sb.WriteString("\n" + a.translator.Text(i18n.MsgWorkflowError, run.Error))
		}
		a.addCommandStatus(strings.TrimRight(sb.String(), "\n"))
	case "cancel":
		if len(parts) < 3 {
			a.addCommandStatus(commandUsage(a.translator, "/workflows cancel <id>"))
			return
		}
		id := strings.TrimSpace(parts[2])
		if !workflow.DefaultActiveRegistry().Cancel(id) {
			a.addCommandError(a.translator.Text(i18n.MsgWorkflowNotActive, id))
			return
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgWorkflowCancelRequested, id))
	default:
		a.addCommandError(commandUsage(a.translator, "/workflows [list|show <id>|cancel <id>]"))
	}
}

// handleSystemInitCommand generates (or refreshes) a project AGENTS.md. In
// plan mode it first switches to agent mode (AGENTS.md needs write access). In
// interactive modes (plan/agent) the agent is told to use the question tool to
// clarify project conventions with the user before writing the file.
func (a *App) handleSystemInitCommand(cmd string) tea.Cmd {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgSystemInitRunning))
		return nil
	}
	if a.manualCompactionActive {
		a.addCommandError(a.translator.Text(i18n.MsgSystemInitCompacting))
		return nil
	}
	extra := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cmd), "/systeminit"))
	if a.mode == "plan" {
		a.mode = "agent"
		a.resetAgent(fmt.Errorf("systeminit requires write access"))
		a.addCommandStatus(a.translator.Text(i18n.MsgSystemInitSwitchedMode))
	}
	// The question tool is only available in plan/agent modes, so only request
	// interactive clarification when not in yolo mode.
	interactive := a.mode != "yolo" && a.mode != "os"
	if interactive {
		a.addCommandStatus(a.translator.Text(i18n.MsgSystemInitInteractive))
	} else {
		a.addCommandStatus(a.translator.Text(i18n.MsgSystemInitAutomatic))
	}
	return a.submitAgentPrompt(systeminit.Prompt(interactive, extra))
}

func (a *App) handleRuleCommand(parts []string) {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgRuleRunning))
		return
	}
	overwrite, ok := parseRuleForce(parts)
	if !ok {
		a.addCommandError(commandUsage(a.translator, "/rule [force|--force]"))
		return
	}

	path, content, written, err := contextfiles.EnsureRuleFile(a.currentCwd(), overwrite)
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgRuleWriteFailed, err))
		return
	}
	a.ruleContent = content
	a.resetAgent(fmt.Errorf("rule changed"))

	if written {
		action := "Created"
		if overwrite {
			action = "Overwrote"
		}
		a.addCommandStatus(a.translator.Text(i18n.MsgRuleCreated, action, path), a.translator.Text(i18n.MsgRuleLoaded))
		return
	}
	a.addCommandStatus(a.translator.Text(i18n.MsgRuleExists, path), a.translator.Text(i18n.MsgRuleNotOverwritten), a.translator.Text(i18n.MsgRuleLoadedExisting))
}

func parseRuleForce(parts []string) (bool, bool) {
	if len(parts) == 1 {
		return false, true
	}
	if len(parts) != 2 {
		return false, false
	}
	switch parts[1] {
	case "force", "--force":
		return true, true
	default:
		return false, false
	}
}

// handleReloadCommand starts a brand-new session and re-execs the process so
// the next run behaves exactly like a freshly started program (config, context
// files, skills, and MCP are all reloaded).
func (a *App) handleReloadCommand() tea.Cmd {
	if a.isThinking && a.agent != nil {
		a.abortAndResetAgent("reload")
		a.isThinking = false
		a.finishRequestTimer()
	}
	a.reloadRequested = true
	a.addCommandStatus(a.translator.Text(i18n.MsgReloading))
	a.stopPrintLoop()
	return tea.Quit
}

func (a *App) handleCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	if strings.HasPrefix(parts[0], "/skill:") {
		skillName := strings.TrimPrefix(parts[0], "/skill:")
		if skillName != "" {
			a.activateSkill(skillName)
		} else {
			a.listSkills()
		}
		return nil
	}

	command := parts[0]

	switch command {
	case "/mode":
		if len(parts) > 1 {
			switch parts[1] {
			case "plan", "agent", "yolo", "os":
				a.mode = parts[1]
				// If agent is currently running, abort it so the new mode takes effect immediately
				if a.isThinking && a.agent != nil {
					a.pendingAbortReason = "mode change"
					a.abortAndResetAgent("mode changed")
					a.clearQueuedInput()
					a.isThinking = false
					a.finishRequestTimer()
					a.addCommandStatus(a.translator.Text(i18n.MsgModeChangeAborted))
				} else {
					a.resetAgent(fmt.Errorf("mode changed"))
				}
				a.addCommandStatus(a.translator.Text(i18n.MsgCommandMode, strings.ToUpper(a.mode)))
			default:
				a.addCommandError(a.translator.Text(i18n.MsgCommandInvalidMode))
			}
		} else {
			a.addCommandStatus(a.translator.Text(i18n.MsgCommandCurrentMode, strings.ToUpper(a.mode)))
			switch a.mode {
			case "plan":
				a.addCommandStatus(a.translator.Text(i18n.MsgCommandPermissionsPlan))
			case "agent":
				a.addCommandStatus(a.translator.Text(i18n.MsgCommandPermissionsAgent))
			case "yolo":
				a.addCommandStatus(a.translator.Text(i18n.MsgCommandPermissionsYolo))
			case "os":
				a.addCommandStatus(a.translator.Text(i18n.MsgCommandPermissionsOS))
			}
		}
	case "/model":
		if len(parts) > 1 {
			// Switch model directly
			modelID := parts[1]
			newModel := a.provider.GetModel(modelID)
			if newModel == nil {
				models := a.provider.Models()
				ids := make([]string, len(models))
				for i, m := range models {
					ids[i] = m.ID
				}
				a.addCommandError(a.translator.Text(i18n.MsgCommandModelNotFound, modelID, strings.Join(ids, ", ")))
				return nil
			}
			a.model = newModel
			a.syncAgentManagerRuntime()
			// Reset agent so next message uses the new model
			a.resetAgent(fmt.Errorf("model changed"))
			a.addCommandStatus(a.translator.Text(i18n.MsgCommandModelSwitched, newModel.Name, newModel.ID))
		} else {
			if a.isThinking {
				a.addCommandError(a.translator.Text(i18n.MsgCommandRunningCannotOpen, "/model"))
			} else {
				a.openModelDialog()
			}
		}
	case "/defaultModel":
		scope := "global"
		if len(parts) > 2 {
			a.addCommandError(commandUsage(a.translator, "/defaultModel [project|global]"))
			return nil
		}
		if len(parts) == 2 {
			switch parts[1] {
			case "project", "global":
				scope = parts[1]
			default:
				a.addCommandError(commandUsage(a.translator, "/defaultModel [project|global]"))
				return nil
			}
		}
		if a.isThinking {
			a.addCommandError(a.translator.Text(i18n.MsgCommandRunningCannotOpen, "/defaultModel"))
		} else {
			a.openDefaultModelDialog(scope)
		}
	case "/auth":
		if a.isThinking {
			a.addCommandError(a.translator.Text(i18n.MsgCommandRunningCannotOpen, "/auth"))
		} else {
			a.openAuthDialog()
		}
	case "/settings":
		if a.isThinking {
			a.addCommandError(a.translator.Text(i18n.MsgSettingsRunning))
		} else {
			a.openSettingsDialog(parts[1:])
		}
	case "/tuilang":
		a.handleTUILangCommand(parts)
	case "/skills":
		a.listSkills()
	case "/env":
		a.handleEnvCommand(parts)
	case "/skillhub":
		return a.handleSkillHubCommand(parts)
	case "/esm":
		return a.handleESMCommand(cmd)
	case "/skill":
		if len(parts) > 1 {
			a.activateSkill(parts[1])
		} else {
			a.listSkills()
		}
	case "/paste-image":
		a.handlePasteImageCommand()
	case "/compact":
		if a.isThinking {
			a.addCommandError(a.translator.Text(i18n.MsgCompactRunning))
		} else if a.agent == nil {
			a.addCommandError(a.translator.Text(i18n.MsgCommandNothingToCompact))
		} else {
			return a.startManualCompaction()
		}
	case "/clear":
		a.discardPendingInput()
		a.resetTranscriptState()
		a.resetAgent(fmt.Errorf("conversation cleared"))
		a.contextUsage = nil
		a.totalInputTokens = 0
		a.totalCacheRead = 0
		a.totalCacheWrite = 0
		a.pastes = make(map[int]string)
		a.pasteCounter = 0
		a.activeSkills = make(map[string]string)
		a.markBuiltinActiveSkills()
		a.rebuildExtraContext()
		a.updateViewportContent()
		a.printedMessageIdx = make(map[int]bool)
		a.addCommandStatus(a.translator.Text(i18n.MsgConversationCleared))

	case "/quit":
		a.finalizeForQuit()
		a.stopPrintLoop()
		return tea.Quit
	case "/sessions":
		a.handleSessionsCommand(parts)
	case "/init_mcp":
		a.handleInitMCPCommand(parts)
	case "/mcps":
		a.handleMCPsCommand()
	case "/agent":
		a.handleAgentCommand(parts)
	case "/expert":
		a.handleExpertCommand(parts)
	case "/delegate":
		a.handleDelegateCommand(parts)
	case "/browser":
		a.handleBrowserCommand(parts)
	case "/alloweditpath":
		a.handleAllowEditPathCommand(parts)
	case "/allowautoedit":
		a.handleAllowAutoEditCommand(parts)
	case "/btw":
		return a.handleBtwCommand(cmd)
	case "/systeminit":
		return a.handleSystemInitCommand(cmd)
	case "/rule":
		a.handleRuleCommand(parts)
	case "/reload":
		return a.handleReloadCommand()
	case "/cron":
		a.handleCronCommand(parts)
	case "/workflows":
		a.handleWorkflowsCommand(parts)
	case "/statusline":
		a.handleStatusLineCommand(parts)
	case "/stats":
		return a.handleStatsCommand(parts)
	case "/help":
		a.addCommandStatus(commandHelpText(a.translator))
	default:
		a.addCommandError(a.translator.Text(i18n.MsgUnknownCommand, command))
	}

	return nil
}

func (a *App) handleTUILangCommand(parts []string) {
	if len(parts) == 1 {
		a.addCommandStatus(a.translator.Text(i18n.MsgTUILangStatus, a.effectiveSettings().TUILang, a.translator.Language(), a.tuiLangOffset, a.languageSourceLabel()))
		a.addCommandStatus(commandUsage(a.translator, "/tuilang [global|project] [auto|zh|en]"))
		return
	}

	scope := "global"
	value := ""
	if len(parts) == 2 {
		value = parts[1]
	} else if len(parts) == 3 {
		scope = strings.ToLower(parts[1])
		value = parts[2]
	} else {
		a.addCommandError(commandUsage(a.translator, "/tuilang [global|project] [auto|zh|en]"))
		return
	}
	if scope != "global" && scope != "project" {
		a.addCommandError(commandUsage(a.translator, "/tuilang [global|project] [auto|zh|en]"))
		return
	}
	configured, valid := i18n.ParseConfigured(value)
	if !valid || strings.TrimSpace(value) == "" {
		a.addCommandError(commandUsage(a.translator, "/tuilang [global|project] [auto|zh|en]"))
		return
	}
	if err := a.saveTUILangForScope(string(configured), scope); err != nil {
		return
	}
}

func (a *App) handleEnvCommand(parts []string) {
	a.openEnvDialog()
}

func (a *App) listSkills() {
	if a.skillsMgr == nil {
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillsUnavailable))
		return
	}
	skillList := a.skillsMgr.List()
	if len(skillList) == 0 {
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillsEmpty))
		return
	}

	var sb strings.Builder
	sb.WriteString("Available skills:\n")
	for _, s := range skillList {
		marker := " "
		if _, ok := a.activeSkills[s.Name]; ok {
			marker = "*"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s (%s): %s\n", marker, s.Name, s.Source, s.Description))
	}
	sb.WriteString("\nUse /skill <name> or /skill:<name> to activate a skill.")
	a.addCommandStatus(sb.String())
}

// activateSkill loads a skill's content into the extra context.
func (a *App) activateSkill(name string) {
	if a.skillsMgr == nil {
		a.addCommandError(a.translator.Text(i18n.MsgSkillsUnavailable))
		return
	}
	skill := a.skillsMgr.Get(name)
	if skill == nil {
		a.addCommandError(a.translator.Text(i18n.MsgSkillNotFound, name))
		return
	}

	// Check if already active
	if _, ok := a.activeSkills[name]; ok {
		a.addCommandStatus(a.translator.Text(i18n.MsgSkillAlreadyActive, name))
		return
	}

	// Add skill content to active skills
	skillCtx := a.skillsMgr.BuildSkillContext(name)
	a.activeSkills[name] = skillCtx

	// Rebuild extraContext from base + all active skills
	a.rebuildExtraContext()

	// Reset agent so next message uses the updated context
	a.resetAgent(fmt.Errorf("skill activated"))

	a.addCommandStatus(a.translator.Text(i18n.MsgSkillActivated, name, skill.Source, skill.Description))
}

// rebuildExtraContext rebuilds extraContext from base context + all active skills.
func (a *App) rebuildExtraContext() {
	sb := strings.Builder{}
	sb.WriteString(a.baseExtraContext)
	for _, ctx := range a.activeSkills {
		sb.WriteString(ctx)
	}
	a.extraContext = sb.String()
}

func (a *App) markBuiltinActiveSkills() {
	if a.skillsMgr == nil {
		return
	}
	if a.activeSkills == nil {
		a.activeSkills = make(map[string]string)
	}
	if a.workflows && a.skillsMgr.Get(workflow.SkillName) != nil {
		a.activeSkills[workflow.SkillName] = ""
	}
	if a.browserEnabled && a.skillsMgr.Get(browserfeature.SkillName) != nil {
		if a.browserSkillInBase {
			a.activeSkills[browserfeature.SkillName] = ""
		} else {
			a.activeSkills[browserfeature.SkillName] = a.skillsMgr.BuildSkillContext(browserfeature.SkillName)
		}
	}
}

// getSessionDir returns the session directory path.
