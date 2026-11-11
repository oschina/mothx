package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	// Model/Driver/MCPBridge/CapabilityOverrides are the profile's configured
	// external model, ACP flavor, bridge intent, and tightened capabilities.
	// They are behavior metadata only, never credentials or argv.
	Model               string   `json:"model,omitempty"`
	Driver              string   `json:"driver"`
	MCPBridge           string   `json:"mcpBridge"`
	CapabilityOverrides []string `json:"capabilityOverrides,omitempty"`
	InvalidReason       string   `json:"invalidReason,omitempty"`
}

// ListACPWorkerProfiles projects the shared Runtime catalog for the WebUI.
// The HTTP adapter never reads acpAgents itself: eligibility and the validation
// verdict all stay behind the Runtime catalog.
func (s *Server) ListACPWorkerProfiles() []ACPWorkerProfileSummary {
	if s == nil || s.settings == nil {
		return nil
	}
	return projectACPWorkerProfiles(s.settings.ACPAgents)
}

// projectACPWorkerProfiles resolves the shared Runtime catalog for one profile
// list. Both the read and patch projections use it so a patch response cannot
// drift from the list response.
func projectACPWorkerProfiles(profiles []config.ACPAgentProfile) []ACPWorkerProfileSummary {
	descriptors := agentruntime.ListACPWorkerProfiles(&config.Settings{ACPAgents: profiles})
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
			Model:                 descriptor.Model,
			Driver:                descriptor.Driver,
			MCPBridge:             descriptor.MCPBridge,
			CapabilityOverrides:   descriptor.CapabilityOverrides,
			InvalidReason:         descriptor.InvalidReason,
		})
	}
	return result
}

// PatchACPWorkerProfile applies the narrow behavior-metadata patch and returns
// the refreshed catalog. It never exposes or writes argv, URLs, environment, or
// trust flags, and validation stays in internal/config.
func (s *Server) PatchACPWorkerProfile(id string, patch config.ACPAgentProfilePatch) ([]ACPWorkerProfileSummary, error) {
	profiles, err := config.PatchACPAgentProfile(id, patch)
	if err != nil {
		return nil, err
	}
	// Publish the patched profile set into the live server snapshot. Without
	// this the catalog returned below would reflect the edit while every later
	// ListACPWorkerProfiles call and Router activation kept reading the startup
	// snapshot until the process restarted or another settings save replaced it.
	s.publishACPAgentProfiles(profiles)
	return projectACPWorkerProfiles(profiles), nil
}

// publishACPAgentProfiles swaps the ACP worker profiles inside the server's
// settings pointer using copy-on-write. The previous struct is never mutated, so
// concurrent readers that retained it keep a consistent view.
func (s *Server) publishACPAgentProfiles(profiles []config.ACPAgentProfile) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.settings != nil {
		runtime := *s.settings
		runtime.ACPAgents = profiles
		s.settings = &runtime
	}
	s.mu.Unlock()
	s.refreshRouterSessionManagers()
}

// refreshRouterSessionManagers refreshes the session-scoped manager snapshot for
// idle Router sessions so a profile edit (notably the default model) reaches the
// next worker delegation. Running or busy sessions are skipped: their manager
// must not be mutated mid-run. TryLock keeps this patch RPC from blocking on an
// unrelated long request.
func (s *Server) refreshRouterSessionManagers() {
	if s == nil || s.pool == nil {
		return
	}
	for _, sess := range s.pool.Snapshot() {
		if sess == nil || sess.Runtime == nil || sess.Registry == nil {
			continue
		}
		if !sess.Runtime.RouterActive() || sess.IsRunning() {
			continue
		}
		if !sess.mu.TryLock() {
			continue
		}
		running := sess.IsRunning()
		var err error
		if !running {
			if sess.AgentMgr != nil {
				// Refresh in place so manager-owned child bookkeeping survives a
				// profile edit. The manager's child backend holds the live ACP
				// profile snapshot used by the next delegation.
				s.refreshSessionAgentManagerSettings(sess)
			} else {
				err = s.reconcileSessionAgentManager(sess, false)
			}
		}
		sess.mu.Unlock()
		if err != nil {
			log.Printf("serve: refresh Router worker profile for %s: %v", sess.ID, err)
		}
	}
}

// refreshSessionAgentManagerSettings publishes the current live settings
// snapshot into an already-built session manager. It is used when a profile
// edit must reach the next external delegation without rebuilding the manager
// and losing its child tracking.
func (s *Server) refreshSessionAgentManagerSettings(sess *APISession) {
	if s == nil || sess == nil || sess.AgentMgr == nil {
		return
	}
	sess.AgentMgr.UpdateRuntimeConfig(s.provider, s.providerName, s.model, s.settingsForSession(sess), s.getAllow())
}

// RouterBindingSummary is the session's current Router selection plus the
// read-only worker message and pending-question projection. The projection is
// never an answer surface: only the lead's delegate_worker_mgr can resolve a
// worker question.
type RouterBindingSummary struct {
	Enabled          bool                              `json:"enabled"`
	ProfileIDs       []string                          `json:"profileIds,omitempty"`
	PrimaryProfile   string                            `json:"primaryProfile,omitempty"`
	Mode             string                            `json:"mode,omitempty"`
	WorkerMessages   []agentruntime.WorkerMessage      `json:"workerMessages,omitempty"`
	PendingQuestions []agentruntime.WorkerQuestionView `json:"pendingQuestions,omitempty"`
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
	// Activation installs the delegation surface for this session, so the session
	// needs its own manager even when the session's capability flags are all
	// false. Refresh an existing manager's settings snapshot first: its ACP child
	// backend captured the pre-edit profile catalog and would otherwise delegate
	// the old/empty model to the next worker.
	s.refreshSessionAgentManagerSettings(sess)
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
	if snapshot, err := sess.Runtime.WorkerSnapshot(); err == nil {
		summary.WorkerMessages = snapshot.Messages
		summary.PendingQuestions = snapshot.PendingQuestions
	}
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
