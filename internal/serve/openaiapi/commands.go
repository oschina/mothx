package openaiapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/contextfiles"
	"github.com/startvibecoding/mothx/internal/provider"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/workflow"
)

// CommandResult holds the output of a slash command.
type CommandResult struct {
	Message string
	Error   bool
}

// handleCommand processes a /xxx slash command.
// Returns nil if the input is not a command (should go to agent).
func (s *Server) handleCommand(sess *APISession, input string, runIDs ...string) *CommandResult {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}

	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return nil
	}

	runID := ""
	if len(runIDs) > 0 {
		runID = runIDs[0]
	}
	cmd := parts[0]
	switch cmd {
	case "/clear":
		return s.cmdClear(sess)
	case "/mode":
		return s.cmdMode(sess, parts, runID)
	case "/model":
		return s.cmdModel(parts)
	case "/defaultModel":
		return s.cmdDefaultModel(parts)
	case "/models":
		return s.cmdModels()
	case "/sessions":
		return s.cmdSessionsForSession(sess, parts)
	case "/status":
		return s.cmdStatus(sess)
	case "/compact":
		return s.cmdCompact(sess)
	case "/delegate":
		return s.cmdDelegate(sess, parts, runID)
	case "/alloweditpath":
		return s.cmdAllowEditPath(parts)
	case "/allowautoedit":
		return s.cmdAllowAutoEdit(parts)
	case "/workflows":
		return s.cmdWorkflows(parts)
	case "/skill":
		return s.cmdSkill(sess, parts)
	case "/skills":
		return s.cmdSkills(sess)
	case "/rule":
		return s.cmdRule(sess, parts)
	case "/esm":
		return s.cmdESM(sess, trimmed)
	case "/help":
		return s.cmdHelp()
	default:
		return &CommandResult{Message: fmt.Sprintf("Unknown command: %s. Type /help for available commands.", cmd), Error: true}
	}
}

func (s *Server) getAllow() *config.AllowConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allow == nil {
		s.allow = config.LoadAllow()
	}
	return s.allow
}

func (s *Server) cmdClear(sess *APISession) *CommandResult {
	if sess == nil {
		return &CommandResult{Message: "No active session to clear.", Error: true}
	}
	// The session manager keeps persisted SQLite state, but we reset the in-memory state.
	// The caller will set agent=nil so the next request builds a fresh agent.
	return &CommandResult{Message: "✅ Conversation cleared"}
}

func (s *Server) cmdMode(sess *APISession, parts []string, runIDs ...string) *CommandResult {
	runID := ""
	if len(runIDs) > 0 {
		runID = runIDs[0]
	}
	if len(parts) > 1 {
		switch parts[1] {
		case "plan", "agent", "yolo", "os":
			if sess != nil {
				mode := parts[1]
				if _, err := s.patchActiveSessionCapabilities(sess, SessionCapabilityPatch{Mode: &mode}, "slash_mode", "user", runID, map[string]any{
					"command": "/mode",
				}); err != nil {
					return &CommandResult{Message: fmt.Sprintf("Failed to save mode: %v", err), Error: true}
				}
			}
			return &CommandResult{Message: fmt.Sprintf("Mode: %s", strings.ToUpper(parts[1]))}
		default:
			return &CommandResult{Message: "Invalid mode. Use: plan, agent, yolo, os", Error: true}
		}
	}
	mode := s.cfg.DefaultMode
	if sess != nil && sess.Mode != "" {
		mode = sess.Mode
	}
	return &CommandResult{Message: fmt.Sprintf("Current mode: %s", strings.ToUpper(mode))}
}

func (s *Server) cmdModel(parts []string) *CommandResult {
	if len(parts) > 1 {
		modelID := parts[1]
		newModel := s.provider.GetModel(modelID)
		if newModel == nil {
			return &CommandResult{Message: fmt.Sprintf("Model %q not found — available: %s", modelID, modelIDs(s.provider.Models())), Error: true}
		}
		s.mu.Lock()
		s.model = newModel
		s.mu.Unlock()
		return &CommandResult{Message: fmt.Sprintf("✅ Model switched to: %s (%s)", newModel.Name, newModel.ID)}
	}
	s.mu.RLock()
	m := s.model
	s.mu.RUnlock()
	return &CommandResult{Message: fmt.Sprintf("Current model: %s (%s)", m.Name, m.ID)}
}

func (s *Server) cmdDefaultModel(parts []string) *CommandResult {
	if len(parts) != 3 && len(parts) != 4 {
		return &CommandResult{Message: "Usage: /defaultModel <provider> <model> [project|global]", Error: true}
	}
	providerID := parts[1]
	modelID := parts[2]
	scope := "global"
	if len(parts) == 4 {
		switch parts[3] {
		case "project", "global":
			scope = parts[3]
		default:
			return &CommandResult{Message: "Usage: /defaultModel <provider> <model> [project|global]", Error: true}
		}
	}

	s.mu.RLock()
	runtime := *s.settings
	s.mu.RUnlock()
	runtime.DefaultProvider = providerID
	runtime.DefaultModel = modelID

	p, m, err := providerfactory.Create(&runtime, providerID, modelID)
	if err != nil {
		return &CommandResult{Message: fmt.Sprintf("Provider validation failed: %v", err), Error: true}
	}

	scoped, err := loadDefaultModelSettings(scope)
	if err != nil {
		return &CommandResult{Message: fmt.Sprintf("Failed to load %s settings: %v", scope, err), Error: true}
	}
	scoped.DefaultProvider = providerID
	scoped.DefaultModel = modelID
	if err := saveDefaultModelSettings(scope, scoped); err != nil {
		return &CommandResult{Message: fmt.Sprintf("Failed to save %s settings: %v", scope, err), Error: true}
	}

	s.mu.Lock()
	s.settings = &runtime
	s.provider = p
	s.model = m
	s.mu.Unlock()
	return &CommandResult{Message: fmt.Sprintf("✅ Default model saved (%s): %s / %s", scope, providerID, modelID)}
}

func loadDefaultModelSettings(scope string) (*config.Settings, error) {
	switch scope {
	case "global":
		return config.LoadGlobalSettingsSparse()
	default:
		return config.LoadProjectSettingsSparse()
	}
}

func saveDefaultModelSettings(scope string, settings *config.Settings) error {
	switch scope {
	case "global":
		return config.SaveGlobalSettings(settings)
	default:
		return config.SaveProjectSettings(settings)
	}
}

func (s *Server) cmdModels() *CommandResult {
	models := s.provider.Models()
	if len(models) == 0 {
		return &CommandResult{Message: "No models available."}
	}
	var sb strings.Builder
	sb.WriteString("Available models:\n")
	s.mu.RLock()
	currentID := s.model.ID
	s.mu.RUnlock()
	for _, m := range models {
		marker := " "
		if m.ID == currentID {
			marker = "*"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s (%s)\n", marker, m.Name, m.ID))
	}
	return &CommandResult{Message: sb.String()}
}

func (s *Server) cmdSessions(parts []string) *CommandResult {
	return s.cmdSessionsForSession(nil, parts)
}

func (s *Server) cmdSessionsForSession(sess *APISession, parts []string) *CommandResult {
	sub := "ls"
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "ls", "list":
		cwd := s.cfg.GetWorkDir()
		if sess != nil && sess.WorkDir != "" {
			cwd = sess.WorkDir
		}
		ids := s.pool.ListForWorkDir(cwd)
		if len(ids) == 0 {
			return &CommandResult{Message: "No active sessions."}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Active sessions (%d):\n", len(ids)))
		for _, id := range ids {
			sb.WriteString(fmt.Sprintf("  - %s\n", id))
		}
		return &CommandResult{Message: sb.String()}
	case "clear", "new":
		return &CommandResult{Message: "✅ Start a new request context to begin a fresh session."}
	case "del", "delete", "rm":
		if len(parts) < 3 {
			return &CommandResult{Message: "Usage: /sessions del <id>", Error: true}
		}
		id := strings.TrimSpace(parts[2])
		currentID := ""
		cwd := ""
		if sess != nil {
			currentID = sess.ID
			if sess.Manager != nil && sess.Manager.GetHeader() != nil {
				cwd = sess.Manager.GetHeader().Cwd
			}
		}
		if cwd == "" {
			cwd = s.cfg.GetWorkDir()
		}
		sessDir := s.settings.GetSessionDir()
		details, err := session.ListForDirDetailed(cwd, sessDir)
		if err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to list sessions: %v", err), Error: true}
		}
		var match *session.SessionDetail
		for i, d := range details {
			if strings.HasPrefix(d.ID, id) {
				if match != nil {
					return &CommandResult{Message: fmt.Sprintf("Ambiguous ID '%s'. Be more specific.", id), Error: true}
				}
				match = &details[i]
			}
		}
		if match != nil {
			if currentID != "" && match.ID == currentID {
				return &CommandResult{Message: "Cannot delete the current session. Switch to another session first, or use /clear to start fresh.", Error: true}
			}
			if err := agentruntime.DeleteSession(sessDir, match.ID); err != nil {
				return &CommandResult{Message: fmt.Sprintf("Failed to delete session: %v", err), Error: true}
			}
			s.pool.RemoveByWorkDir(cwd, match.ID)
			s.mu.Lock()
			if s.defaultSessionIDs != nil {
				for workDir, id := range s.defaultSessionIDs {
					if id == match.ID {
						delete(s.defaultSessionIDs, workDir)
					}
				}
			}
			s.mu.Unlock()
			return &CommandResult{Message: fmt.Sprintf("✅ Session %s deleted.", match.ID)}
		}
		return &CommandResult{Message: fmt.Sprintf("Session not found: %s", id), Error: true}
	default:
		return &CommandResult{Message: "Usage: /sessions [ls|clear|del <id>]", Error: true}
	}
}

func (s *Server) cmdStatus(sess *APISession) *CommandResult {
	if sess == nil {
		return &CommandResult{Message: "No active session.", Error: true}
	}
	mode := s.cfg.DefaultMode
	if sess.Mode != "" {
		mode = sess.Mode
	}
	s.mu.RLock()
	modelID := s.model.ID
	s.mu.RUnlock()
	msgCount := 0
	if sess.Manager != nil {
		msgCount = len(sess.Manager.GetMessages())
	}
	msg := fmt.Sprintf("Session: %s\nMode: %s\nModel: %s\nMessages: %d\nWorkDir: %s",
		sess.ID, strings.ToUpper(mode), modelID, msgCount, sess.WorkDir)
	return &CommandResult{Message: msg}
}

func (s *Server) cmdDelegate(sess *APISession, parts []string, runIDs ...string) *CommandResult {
	runID := ""
	if len(runIDs) > 0 {
		runID = runIDs[0]
	}
	if sess == nil {
		return &CommandResult{Message: "No active session.", Error: true}
	}
	if len(parts) < 2 || parts[1] == "status" {
		state := "OFF"
		if sess.DelegateMode {
			state = "ON"
		}
		return &CommandResult{Message: fmt.Sprintf("Delegation mode: %s", state)}
	}
	switch parts[1] {
	case "on", "off":
		enabled := parts[1] == "on"
		if _, err := s.patchActiveSessionCapabilities(sess, SessionCapabilityPatch{DelegateMode: &enabled}, "slash_delegate", "user", runID, map[string]any{
			"command": "/delegate " + parts[1],
		}); err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to save delegation mode: %v", err), Error: true}
		}
		state := "OFF"
		if enabled {
			state = "ON"
		}
		return &CommandResult{Message: "Delegation mode: " + state}
	default:
		return &CommandResult{Message: "Usage: /delegate [on|off|status]", Error: true}
	}
}

func (s *Server) cmdAllowEditPath(parts []string) *CommandResult {
	allow := s.getAllow()
	if len(parts) < 2 {
		paths := allow.EditPathList()
		if len(paths) == 0 {
			return &CommandResult{Message: "Auto-edit path whitelist is empty. Usage: /alloweditpath add|remove <glob>|clear"}
		}
		var sb strings.Builder
		sb.WriteString("Auto-edit path whitelist (agent mode):\n")
		for _, p := range paths {
			sb.WriteString(fmt.Sprintf("  %s\n", p))
		}
		return &CommandResult{Message: strings.TrimRight(sb.String(), "\n")}
	}
	switch parts[1] {
	case "add":
		if len(parts) < 3 {
			return &CommandResult{Message: "Usage: /alloweditpath add <glob>", Error: true}
		}
		glob := strings.Join(parts[2:], " ")
		if !allow.AddEditPath(glob) {
			return &CommandResult{Message: fmt.Sprintf("Already in whitelist: %s", glob)}
		}
		if err := allow.SaveProject(); err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to save allow.json: %v", err), Error: true}
		}
		return &CommandResult{Message: fmt.Sprintf("✅ Added to auto-edit whitelist: %s", glob)}
	case "remove", "rm":
		if len(parts) < 3 {
			return &CommandResult{Message: "Usage: /alloweditpath remove <glob>", Error: true}
		}
		glob := strings.Join(parts[2:], " ")
		if !allow.RemoveEditPath(glob) {
			return &CommandResult{Message: fmt.Sprintf("Not in whitelist: %s", glob)}
		}
		if err := allow.SaveProject(); err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to save allow.json: %v", err), Error: true}
		}
		return &CommandResult{Message: fmt.Sprintf("✅ Removed from auto-edit whitelist: %s", glob)}
	case "clear":
		allow.ClearEditPaths()
		if err := allow.SaveProject(); err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to save allow.json: %v", err), Error: true}
		}
		return &CommandResult{Message: "✅ Auto-edit path whitelist cleared"}
	default:
		return &CommandResult{Message: "Usage: /alloweditpath [add <glob>|remove <glob>|clear]", Error: true}
	}
}

func (s *Server) cmdAllowAutoEdit(parts []string) *CommandResult {
	allow := s.getAllow()
	if len(parts) < 2 {
		state := "OFF"
		if allow.GetAutoEdit() {
			state = "ON"
		}
		return &CommandResult{Message: fmt.Sprintf("Auto-edit (agent mode): %s\nUsage: /allowautoedit [on|off] [global]", state)}
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
		return &CommandResult{Message: "Usage: /allowautoedit [on|off] [global]", Error: true}
	}
	var err error
	scope := "project"
	effective := enable
	if globalScope {
		scope = "global"
		effective = allow.SetGlobalAutoEdit(enable)
		err = allow.SaveGlobalAutoEditValue(enable)
	} else {
		allow.SetProjectAutoEdit(enable)
		err = allow.SaveProject()
	}
	if err != nil {
		return &CommandResult{Message: fmt.Sprintf("Failed to save allow.json: %v", err), Error: true}
	}
	state := "OFF"
	if enable {
		state = "ON"
	}
	msg := fmt.Sprintf("✅ Auto-edit (agent mode): %s [%s]", state, scope)
	if globalScope && effective != enable {
		effectiveState := "OFF"
		if effective {
			effectiveState = "ON"
		}
		msg += fmt.Sprintf(" (effective here: %s due to project override)", effectiveState)
	}
	return &CommandResult{Message: msg}
}

func (s *Server) cmdCompact(sess *APISession) *CommandResult {
	if sess == nil {
		return &CommandResult{Message: "No active session.", Error: true}
	}
	if sess.Manager == nil {
		return &CommandResult{Message: "No active session.", Error: true}
	}

	a, err := s.agentForCommandCompaction(sess)
	if err != nil {
		return &CommandResult{Message: fmt.Sprintf("Context compaction failed: %v", err), Error: true}
	}
	replayState := sess.Manager.GetReplayState()
	if len(replayState.Messages) > 0 {
		a.LoadHistoryState(replayState.Messages, replayState.EntryIDs)
	}
	eventCh := make(chan agent.Event, 16)
	if err := a.CompactForced(context.Background(), eventCh); err != nil {
		return &CommandResult{Message: fmt.Sprintf("Context compaction failed: %v", err), Error: true}
	}
	for len(eventCh) > 0 {
		<-eventCh
	}
	return &CommandResult{Message: "✅ Context compacted."}
}

func (s *Server) buildSessionRuntimeForCommand(sess *APISession) (*agentruntime.SessionRuntime, error) {
	if s == nil || sess == nil || sess.Manager == nil || sess.Registry == nil {
		return nil, fmt.Errorf("session runtime resources are unavailable")
	}
	return agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		ID:              sess.ID,
		Source:          agentruntime.SourceWebUI,
		WorkDir:         sess.WorkDir,
		Manager:         sess.Manager,
		Registry:        sess.Registry,
		SandboxMgr:      sess.SandboxMgr,
		SkillsMgr:       sess.SkillsMgr,
		MCPClients:      sess.MCPClients,
		ExtraContext:    sess.ExtraContext,
		RuleContent:     sess.RuleContent,
		Settings:        s.settings,
		Workflows:       sess.Workflows,
		Browser:         sess.Browser,
		ArtifactEnabled: s.cfg != nil && s.cfg.EnableArtifact,
	})
}

func (s *Server) agentForCommandCompaction(sess *APISession) (*agent.Agent, error) {
	if sess == nil {
		return nil, fmt.Errorf("session is unavailable")
	}
	if sess.Registry == nil {
		sess.Registry = tools.NewRegistry(sess.WorkDir, nil)
		sess.Registry.RegisterDefaults()
	}
	if sess.Runtime == nil {
		runtime, err := s.buildSessionRuntimeForCommand(sess)
		if err != nil {
			return nil, err
		}
		sess.Runtime = runtime
	}
	runtimeSettings := s.settingsForSession(sess)
	mode := sess.Mode
	if mode == "" {
		mode = runtimeSettings.DefaultMode
	}
	if mode == "" {
		mode = agentruntime.ModeYolo
	}
	return sess.Runtime.BuildAgent(agentruntime.AgentBuildOptions{
		Provider: s.provider, ProviderName: s.providerName, Model: s.model,
		Settings: runtimeSettings, Allow: s.getAllow(), Mode: mode,
		ThinkingLevel: provider.ThinkingLevel(s.cfg.DefaultThinkingLevel),
		MultiAgent:    sess.MultiAgent, DelegateMode: sess.DelegateMode, Workflows: sess.Workflows,
		GetSteeringMessages: s.esmSteeringMessages(sess.ID),
	})
}

func (s *Server) cmdRule(sess *APISession, parts []string) *CommandResult {
	if sess == nil {
		return &CommandResult{Message: "No active session.", Error: true}
	}
	if sess.AgentMgr != nil && sess.AgentMgr.HasRunning() {
		return &CommandResult{Message: "Cannot change rule while agents are running.", Error: true}
	}
	overwrite, ok := parseRuleForce(parts)
	if !ok {
		return &CommandResult{Message: "Usage: /rule [force|--force]", Error: true}
	}

	path, content, written, err := contextfiles.EnsureRuleFile(sess.WorkDir, overwrite)
	if err != nil {
		return &CommandResult{Message: fmt.Sprintf("Failed to write rule file: %v", err), Error: true}
	}
	sess.RuleContent = content
	if sess.AgentMgr != nil {
		sess.AgentMgr = s.newAgentManagerForSession(sess)
		if agentruntime.SubAgentToolsEnabled(sess.Runtime, sess.MultiAgent) && sess.AgentMgr != nil {
			agent.RegisterSubAgentTools(sess.Registry, sess.AgentMgr)
		}
		if sess.DelegateMode && sess.AgentMgr != nil {
			agent.RegisterDelegateSubAgentTool(sess.Registry, sess.AgentMgr)
		}
		if sess.Workflows && sess.AgentMgr != nil {
			workflow.RegisterTools(sess.Registry, sess.AgentMgr, nil)
		}
	}

	if written {
		action := "Created"
		if overwrite {
			action = "Overwrote"
		}
		return &CommandResult{Message: fmt.Sprintf("%s rule file: %s\nLoaded into the current session.", action, path)}
	}
	return &CommandResult{Message: fmt.Sprintf("Rule file already exists: %s\nNot overwritten. Use /rule force to replace it with the default template.\nLoaded existing rule into the current session.", path)}
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

func (s *Server) newAgentManagerForSession(sess *APISession) *agent.AgentManager {
	if sess == nil {
		return nil
	}
	runtimeSettings := s.settingsForSession(sess)
	if sess.Runtime == nil {
		// Compatibility for adapter-owned test fixtures while all production
		// sessions are created by agentruntime.Builder.
		sess.Runtime = &agentruntime.SessionRuntime{
			ID: sess.ID, Source: agentruntime.SourceWebUI, EntrySource: agentruntime.SourceWebUI,
			WorkDir: sess.WorkDir, Manager: sess.Manager, Registry: sess.Registry,
			SandboxMgr: sess.SandboxMgr, SkillsMgr: sess.SkillsMgr, MCPClients: sess.MCPClients,
			ExtraContext: sess.ExtraContext, RuleContent: sess.RuleContent,
			ArtifactEnabled: s.cfg != nil && s.cfg.EnableArtifact,
		}
	}
	if sess.Manager != nil {
		if err := sess.Runtime.BindSession(sess.Manager, agentruntime.SourceWebUI); err != nil {
			return nil
		}
	}
	if sess.Runtime.ExtraContext == "" {
		sess.Runtime.ExtraContext = s.extraContext
	}
	if sess.Runtime.SkillsMgr == nil {
		sess.Runtime.SkillsMgr = s.skillsMgr
	}
	if sess.Runtime.SandboxMgr == nil {
		sess.Runtime.SandboxMgr = s.sandboxMgr
	}
	mgr, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
		Runtime: sess.Runtime, Provider: s.provider, Model: s.model, Settings: runtimeSettings,
		ProviderName: s.providerName, Allow: s.getAllow(), MultiAgentEnabled: true,
		DelegateEnabled: sess.DelegateMode || s.cfg.EnableDelegate, WorkflowsEnabled: sess.Workflows,
	})
	if err != nil {
		return nil
	}
	return mgr
}

func (s *Server) cmdSkill(sess *APISession, parts []string) *CommandResult {
	skillsMgr := s.sessionSkills(sess)
	if skillsMgr == nil {
		return &CommandResult{Message: "No skills available.", Error: true}
	}
	if len(parts) < 2 {
		return s.cmdSkills(sess)
	}
	name := parts[1]
	skill := skillsMgr.Get(name)
	if skill == nil {
		return &CommandResult{Message: fmt.Sprintf("Skill not found: %s", name), Error: true}
	}
	if sess == nil {
		return &CommandResult{Message: "No active session.", Error: true}
	}
	if err := s.activateSkillForSession(sess, name); err != nil {
		return &CommandResult{Message: fmt.Sprintf("Failed to activate skill: %v", err), Error: true}
	}
	return &CommandResult{Message: fmt.Sprintf("✅ Skill '%s' activated: %s", name, skill.Description)}
}

func (s *Server) cmdSkills(sess *APISession) *CommandResult {
	skillsMgr := s.sessionSkills(sess)
	if skillsMgr == nil {
		return &CommandResult{Message: "No skills available."}
	}
	skillList := skillsMgr.List()
	if len(skillList) == 0 {
		return &CommandResult{Message: "No skills found."}
	}
	var sb strings.Builder
	sb.WriteString("Available skills:\n")
	for _, sk := range skillList {
		sb.WriteString(fmt.Sprintf("  - %s (%s): %s\n", sk.Name, sk.Source, sk.Description))
	}
	return &CommandResult{Message: sb.String()}
}

func (s *Server) sessionSkills(sess *APISession) *skills.Manager {
	if sess != nil && sess.SkillsMgr != nil {
		return sess.SkillsMgr
	}
	return s.skillsMgr
}

func (s *Server) cmdWorkflows(parts []string) *CommandResult {
	store := workflow.DefaultStore()
	sub := "list"
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list", "ls":
		runs, err := store.List(context.Background())
		if err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to list workflows: %v", err), Error: true}
		}
		if len(runs) == 0 {
			return &CommandResult{Message: "Workflow runs: (none)"}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Workflow runs (%d):\n", len(runs)))
		for _, run := range runs {
			sb.WriteString(fmt.Sprintf("  [%s] %s %s\n", run.Status, run.ID, run.Name))
		}
		return &CommandResult{Message: strings.TrimRight(sb.String(), "\n")}
	case "show":
		if len(parts) < 3 {
			return &CommandResult{Message: "Usage: /workflows show <id>", Error: true}
		}
		run, err := store.Load(context.Background(), parts[2])
		if err != nil {
			return &CommandResult{Message: fmt.Sprintf("Failed to load workflow: %v", err), Error: true}
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Workflow %s: %s\n", run.ID, run.Status))
		if run.Name != "" {
			sb.WriteString(fmt.Sprintf("Name: %s\n", run.Name))
		}
		for _, phase := range run.Phases {
			sb.WriteString(fmt.Sprintf("Phase [%s] %s tasks=%d\n", phase.Status, phase.Name, len(phase.Tasks)))
		}
		for key, result := range run.Results {
			sb.WriteString(fmt.Sprintf("\n%s [%s]\n%s\n", key, result.Status, strings.TrimSpace(result.Result)))
		}
		if run.Error != "" {
			sb.WriteString("\nError: " + run.Error)
		}
		return &CommandResult{Message: strings.TrimRight(sb.String(), "\n")}
	case "cancel":
		if len(parts) < 3 {
			return &CommandResult{Message: "Usage: /workflows cancel <id>", Error: true}
		}
		id := strings.TrimSpace(parts[2])
		if !workflow.DefaultActiveRegistry().Cancel(id) {
			return &CommandResult{Message: fmt.Sprintf("Workflow run %s is not active.", id), Error: true}
		}
		return &CommandResult{Message: fmt.Sprintf("Workflow run %s cancellation requested.", id)}
	default:
		return &CommandResult{Message: "Usage: /workflows [list|show <id>|cancel <id>]", Error: true}
	}
}

func (s *Server) cmdHelp() *CommandResult {
	help := `Available commands:
  /clear                  - Clear conversation context
  /mode [plan|agent|yolo|os] - Show or switch mode
  /model [model_id]       - Show or switch model
  /defaultModel <provider> <model> [project|global] - Set default provider/model (default: global)
  /models                 - List available models
  /sessions               - List active sessions
  /sessions del <id>      - Delete a session
  /compact                - Trigger context compaction
  /delegate [on|off|status] - Toggle delegation mode
  /alloweditpath [add <glob>|remove <glob>|clear] - Auto-edit path whitelist
  /allowautoedit [on|off] [global] - Toggle full auto-edit in agent mode
  /workflows [list|show <id>|cancel <id>] - Inspect workflow runs
  /status                 - Show session status
  /skill <name>           - Activate a skill
  /skills                 - List available skills
  /rule [force]           - Create ` + contextfiles.RuleFile + ` with safe default project rules
  /esm <objective>        - Enable Supervisor Mode: create/status/edit/pause/resume/clear/guide
  /help                   - Show this help`
	return &CommandResult{Message: help}
}
