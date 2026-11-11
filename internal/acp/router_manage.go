package acp

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// handleManageRouterActivate binds the session to the requested external ACP
// workers and enters Router mode.
//
// The adapter only names the workers. Profile validation, the non-blocking
// approval strategy, the idle fence, the source policy, and the durable mode
// entry all stay in the shared Runtime activation path, so Desktop cannot reach
// a Router state the TUI, CLI, or WebUI could not.
func (s *server) handleManageRouterActivate(req rpcRequest) {
	var in struct {
		SessionID  string   `json:"sessionId"`
		ProfileIDs []string `json:"profileIds"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"sessionId and profileIds are required", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	profileIDs := make([]string, 0, len(in.ProfileIDs))
	for _, id := range in.ProfileIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			profileIDs = append(profileIDs, trimmed)
		}
	}
	if sessionID == "" || len(profileIDs) == 0 {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"sessionId and at least one profileId are required", nil))
		return
	}
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.runtime == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "unknown_session",
			"unknown session", nil))
		return
	}
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable",
			err.Error(), nil))
		return
	}

	binding := agentruntime.RouterBinding{ACPProfileID: profileIDs[0]}
	for _, id := range profileIDs {
		binding.Profiles = append(binding.Profiles, agentruntime.RouterProfileBinding{ProfileID: id})
	}
	sessionDir := ""
	if rt.mgr != nil {
		sessionDir = rt.mgr.GetSessionDir()
	}
	// ActivateRouter performs its own idle check and durable write. It
	// deliberately does not take an execution admission: holding one would
	// make the session look busy to every other adapter for a user-interface
	// action. The TUI and WebUI activate the same way.
	if err := rt.runtime.ActivateRouter(context.Background(), sessionDir, settings, binding); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "router_activation_failed",
			err.Error(), nil))
		return
	}

	// Router installs the delegation surface for this session, so the session
	// needs its own manager (the activated worker binding is per session) and
	// the manager-backed tool groups must point at it. Without this the lead
	// agent is registered into a manager that does not own the session, and
	// every spawn fails with "parent agent <id> not found".
	if rt.registry != nil {
		manager, err := s.registerTeamExpertTools(rt.runtime, rt.registry)
		if err != nil {
			// The binding and mode are already persisted, so this is a
			// projection failure, not a failed activation. Report it without
			// rolling back the durable Router selection.
			log.Printf("[acp] router activation could not build the session manager for %s: %v", sessionID, err)
		} else {
			rt.setAgentMgr(manager)
		}
	}

	// The Runtime persisted Router mode; project the resolved state so the
	// Desktop renderer observes the canonical value instead of an optimistic one.
	s.notify(sessionID, sessionUpdate{SessionUpdate: "current_mode_update", CurrentModeID: agentruntime.ModeRouter})
	s.notify(sessionID, sessionUpdate{SessionUpdate: "config_option_update", ConfigOptions: s.sessionConfigOptions(sessionID)})
	s.notifySessionInfo(sessionID)
	s.writeResponse(req.ID, s.routerBindingProjection(rt), nil)
}

// handleManageRouterDeactivate leaves Router mode and keeps the session usable.
// The binding stays persisted so the session can re-enter Router, but the
// effective mode returns to the ordinary read-only plan surface.
func (s *server) handleManageRouterDeactivate(req rpcRequest) {
	var in struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"sessionId is required", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"sessionId is required", nil))
		return
	}
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.runtime == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "unknown_session",
			"unknown session", nil))
		return
	}
	// Leaving Router is a mode write, so it follows the same lease-fenced path
	// the other ACP session mutations use.
	if err := s.withSessionMutationLease(sessionID, func() error {
		if err := rt.runtime.SetMode(agentruntime.ModePlan); err != nil {
			return err
		}
		// Leaving Router removes the Router-only delegation surface. Reconcile the
		// tool groups through the same Runtime-owned installer activation uses;
		// otherwise the next prompt keeps subagent_spawn/delegate_subagent with a
		// manager whose lead is no longer registered.
		if rt.registry != nil {
			if err := s.refreshSessionExpertTools(rt); err != nil {
				log.Printf("[acp] router deactivation could not reconcile tools for %s: %v", sessionID, err)
			}
		}
		return nil
	}); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "router_deactivation_failed",
			err.Error(), nil))
		return
	}
	_, _, _, effectiveMode, _ := rt.runtime.ConfigSnapshot()
	s.notify(sessionID, sessionUpdate{SessionUpdate: "current_mode_update", CurrentModeID: effectiveMode})
	s.notify(sessionID, sessionUpdate{SessionUpdate: "config_option_update", ConfigOptions: s.sessionConfigOptions(sessionID)})
	s.notifySessionInfo(sessionID)
	s.writeResponse(req.ID, s.routerBindingProjection(rt), nil)
}

// handleManageRouterState projects the session's current Router selection.
func (s *server) handleManageRouterState(req rpcRequest) {
	var in struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params",
			"sessionId is required", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.runtime == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "unknown_session",
			"unknown session", nil))
		return
	}
	s.writeResponse(req.ID, s.routerBindingProjection(rt), nil)
}

// routerBindingProjection is the session's Router selection. It never carries
// profile argv, environment names, or credentials.
func (s *server) routerBindingProjection(rt *sessionRuntime) map[string]any {
	projection := map[string]any{"enabled": false}
	if rt == nil || rt.runtime == nil {
		return projection
	}
	_, _, _, mode, _ := rt.runtime.ConfigSnapshot()
	projection["mode"] = mode
	binding, ok := rt.runtime.RouterBinding()
	if !ok {
		return projection
	}
	projection["enabled"] = true
	projection["primaryProfile"] = binding.ACPProfileID
	projection["profileIds"] = binding.ProfileIDs()
	return projection
}

// handleManageACPProfilePatch applies the narrow behavior-metadata edit surface
// for one existing ACP worker profile. It cannot change argv, URLs, environment,
// trust flags, or authorization; those remain operator-owned settings.json
// fields. Validation and persistence stay in internal/config.
func (s *server) handleManageACPProfilePatch(req rpcRequest) {
	var in struct {
		ID                  string           `json:"id"`
		Driver              *string          `json:"driver"`
		Model               *string          `json:"model"`
		MCPBridge           *string          `json:"mcpBridge"`
		CapabilityOverrides *map[string]bool `json:"capabilityOverrides"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id is required", nil))
		return
	}
	if _, err := config.PatchACPAgentProfile(in.ID, config.ACPAgentProfilePatch{
		Driver: in.Driver, Model: in.Model, MCPBridge: in.MCPBridge, CapabilityOverrides: in.CapabilityOverrides,
	}); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "acp_profile_patch_failed", err.Error(), nil))
		return
	}
	// Publish the patched profile set into the live configuration plane. A
	// Desktop/WebUI edit otherwise only changed settings.json: currentSettings()
	// (and therefore every later session/new/load/worker delegation) kept the
	// startup snapshot until the process restarted.
	settings, err := s.manageSettings()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil))
		return
	}
	updated := settings
	if current := s.currentSettings(); current != nil {
		copy := *current
		copy.ACPAgents = settings.ACPAgents
		updated = &copy
	}
	s.publishSettings(updated)
	// A Router session's session-scoped AgentManager captured the settings
	// snapshot at construction, so re-resolve idle Router sessions against the
	// published profile without disturbing an in-flight prompt.
	s.refreshRouterWorkerManagers()
	s.writeResponse(req.ID, map[string]any{"profiles": agentruntime.ListACPWorkerProfiles(s.currentSettings())}, nil)
}

// refreshRouterWorkerManagers refreshes the session-scoped manager snapshot for
// idle Router sessions so a profile edit (notably the default model) reaches the
// next worker delegation. Running prompts are skipped: their manager must not
// be mutated mid-run and already resolve the profile captured at admission.
func (s *server) refreshRouterWorkerManagers() {
	if s == nil {
		return
	}
	s.mu.Lock()
	targets := make([]*sessionRuntime, 0, len(s.sessions))
	for _, rt := range s.sessions {
		if rt == nil || rt.runtime == nil || rt.registry == nil || !rt.runtime.RouterActive() {
			continue
		}
		rt.cancelMu.Lock()
		running := rt.promptID != ""
		rt.cancelMu.Unlock()
		if running {
			continue
		}
		targets = append(targets, rt)
	}
	s.mu.Unlock()
	settings := s.currentSettings()
	if settings == nil {
		return
	}
	for _, rt := range targets {
		if manager := rt.getAgentMgr(); manager != nil {
			// Refresh the manager that already owns this session's binding and
			// child bookkeeping; rebuilding would orphan live worker handles.
			p, providerName, model, _, _ := rt.runtime.ConfigSnapshot()
			if p == nil || model == nil {
				log.Printf("[acp] refresh Router worker profile for %s: session provider and model are unavailable", rt.id)
				continue
			}
			manager.UpdateRuntimeConfig(p, providerName, model, settings, s.allow)
			continue
		}
		manager, err := s.registerTeamExpertTools(rt.runtime, rt.registry)
		if err != nil {
			log.Printf("[acp] refresh Router worker profile for %s: %v", rt.id, err)
			continue
		}
		rt.setAgentMgr(manager)
	}
}
