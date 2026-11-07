package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// ACPWorkerProfileSummary is the safe WebUI-facing view of one configured
// external ACP worker. It never carries argv, environment names, credentials,
// or the remote endpoint's query and fragment.
type ACPWorkerProfileSummary struct {
	ID                    string                     `json:"id"`
	Title                 string                     `json:"title,omitempty"`
	Transport             config.ACPAgentTransport   `json:"transport"`
	Enabled               bool                       `json:"enabled"`
	RouterEligible        bool                       `json:"routerEligible"`
	PermissionPolicy      config.ACPPermissionPolicy `json:"permissionPolicy"`
	MaxConcurrentChildren int                        `json:"maxConcurrentChildren"`
	ParallelBaseWorkspace bool                       `json:"parallelBaseWorkspaceWrites,omitempty"`
	InvalidReason         string                     `json:"invalidReason,omitempty"`
}

// ListACPWorkerProfiles projects the shared Runtime catalog for the WebUI.
// The HTTP adapter never reads acpAgents itself: eligibility and the validation
// verdict all stay behind the Runtime catalog.
func (s *Server) ListACPWorkerProfiles() []ACPWorkerProfileSummary {
	if s == nil || s.settings == nil {
		return nil
	}
	descriptors := agentruntime.ListACPWorkerProfiles(s.settings)
	result := make([]ACPWorkerProfileSummary, 0, len(descriptors))
	for _, descriptor := range descriptors {
		result = append(result, ACPWorkerProfileSummary{
			ID:                    descriptor.ID,
			Title:                 descriptor.Title,
			Transport:             descriptor.Transport,
			Enabled:               descriptor.Enabled,
			RouterEligible:        descriptor.RouterEligible,
			PermissionPolicy:      descriptor.PermissionPolicy,
			MaxConcurrentChildren: descriptor.MaxConcurrentChildren,
			ParallelBaseWorkspace: descriptor.ParallelBaseWorkspaceWrites,
			InvalidReason:         descriptor.InvalidReason,
		})
	}
	return result
}

// RouterBindingSummary is the session's current Router selection.
type RouterBindingSummary struct {
	Enabled        bool     `json:"enabled"`
	ProfileIDs     []string `json:"profileIds,omitempty"`
	PrimaryProfile string   `json:"primaryProfile,omitempty"`
	Mode           string   `json:"mode,omitempty"`
}

// GetSessionRouter returns the session's Router selection, if any.
func (s *Server) GetSessionRouter(sessionID string) (*RouterBindingSummary, error) {
	sess, err := s.routerSession(sessionID)
	if err != nil {
		return nil, err
	}
	return s.routerSummary(sess), nil
}

// ActivateSessionRouter binds the session to the requested external ACP
// workers and enters Router mode.
//
// The adapter only names the workers. Profile validation, the approval
// strategy, the idle fence, the source policy, and the durable mode entry all
// stay in the shared Runtime activation path, so the WebUI cannot reach a
// Router state the TUI or CLI could not.
func (s *Server) ActivateSessionRouter(sessionID string, profileIDs []string) (*RouterBindingSummary, error) {
	if s == nil || s.settings == nil {
		return nil, fmt.Errorf("server settings are unavailable")
	}
	ids := make([]string, 0, len(profileIDs))
	for _, id := range profileIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one ACP worker profile is required")
	}

	sess, err := s.routerSession(sessionID)
	if err != nil {
		return nil, err
	}
	sess.Lock()
	defer sess.Unlock()

	if sess.Registry == nil {
		registry, buildErr := agentruntime.BuildRegistry(sess.WorkDir, nil, s.settings, agentruntime.RegistryPolicy{RegisterDefaults: true})
		if buildErr != nil {
			return nil, buildErr
		}
		sess.Registry = registry
	}
	if sess.Runtime == nil {
		runtime, buildErr := s.buildSessionRuntimeForCommand(sess)
		if buildErr != nil {
			return nil, buildErr
		}
		sess.Runtime = runtime
	}

	binding := agentruntime.RouterBinding{ACPProfileID: ids[0]}
	for _, id := range ids {
		binding.Profiles = append(binding.Profiles, agentruntime.RouterProfileBinding{ProfileID: id})
	}
	sessionDir := ""
	if sess.Manager != nil {
		sessionDir = sess.Manager.GetSessionDir()
	}
	if err := sess.Runtime.ActivateRouter(context.Background(), sessionDir, s.settings, binding); err != nil {
		return nil, err
	}
	// Activation persisted Router mode through the Runtime; mirror the resolved
	// value instead of writing the adapter field.
	if _, _, _, mode, _ := sess.Runtime.ConfigSnapshot(); strings.TrimSpace(mode) != "" {
		sess.Mode = mode
	}
	// Router installs the delegation surface for this session, so the session
	// needs its own manager even when the session's capability flags are all
	// false. The Runtime-owned reconciliation decides that from the resolved
	// mode, builds or reuses the manager, and installs the matching tool group.
	if err := s.reconcileSessionAgentManager(sess, false); err != nil {
		return nil, err
	}

	return s.routerSummary(sess), nil
}

// DeactivateSessionRouter leaves Router mode and keeps the session usable.
// The binding stays persisted so the session can re-enter Router, but the
// effective mode returns to the ordinary read-only plan surface.
func (s *Server) DeactivateSessionRouter(sessionID string) (*RouterBindingSummary, error) {
	sess, err := s.routerSession(sessionID)
	if err != nil {
		return nil, err
	}
	sess.Lock()
	defer sess.Unlock()
	if sess.Runtime == nil {
		return nil, fmt.Errorf("session runtime is unavailable")
	}
	if err := sess.Runtime.SetMode(agentruntime.ModePlan); err != nil {
		return nil, err
	}
	if _, _, _, mode, _ := sess.Runtime.ConfigSnapshot(); strings.TrimSpace(mode) != "" {
		sess.Mode = mode
	}
	if err := s.reconcileSessionAgentManager(sess, false); err != nil {
		return nil, err
	}
	return s.routerSummary(sess), nil
}

// routerSummary projects the session's Router state without re-entering the
// session lookup, so a caller that already holds the session lock does not
// deadlock on it.
func (s *Server) routerSummary(sess *APISession) *RouterBindingSummary {
	summary := &RouterBindingSummary{}
	if sess == nil {
		return summary
	}
	summary.Mode = sess.Mode
	if sess.Runtime == nil {
		return summary
	}
	binding, ok := sess.Runtime.RouterBinding()
	if !ok {
		return summary
	}
	summary.Enabled = true
	summary.ProfileIDs = binding.ProfileIDs()
	summary.PrimaryProfile = binding.ACPProfileID
	return summary
}

// routerSession resolves the session the WebUI addressed.
func (s *Server) routerSession(sessionID string) (*APISession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrSessionNotFound
	}
	workDir, found, err := s.findSessionWorkDir(sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}
	sess, err := s.getOrCreateSession(sessionID, workDir)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, ErrSessionNotFound
	}
	return sess, nil
}

// IsSessionRouterMutationBusy reports that the session has an active or
// remotely owned execution, so its Router selection cannot change.
func IsSessionRouterMutationBusy(err error) bool {
	return errors.Is(err, session.ErrSessionRunActive) ||
		errors.Is(err, session.ErrRuntimeLeaseBusy) ||
		errors.Is(err, agentruntime.ErrDetachedRemoteExecution)
}
