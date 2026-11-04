package acp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
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
		return rt.runtime.SetMode(agentruntime.ModePlan)
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
