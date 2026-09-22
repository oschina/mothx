package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/expert"
	"github.com/oschina/mothx/internal/session"
)

// ErrSessionExpertMutationBusy means that a session has an active or
// externally owned run and its identity cannot safely change yet.
var ErrSessionExpertMutationBusy = errors.New("session expert identity cannot change while a run is active")

// ExpertSummary is the WebUI-safe projection of one discoverable expert
// bundle. Persona prompt bodies are intentionally excluded from this catalog.
type ExpertSummary struct {
	ID          string               `json:"id"`
	ExpertType  string               `json:"expertType"`
	DisplayName expert.LocalizedText `json:"displayName"`
	Source      string               `json:"source"`
	Invalid     bool                 `json:"invalid"`
	Reason      string               `json:"reason,omitempty"`
}

// ExpertMember is metadata used by a WebUI member card. Runtime definitions
// remain authoritative for prompts and execution capability overrides.
type ExpertMember struct {
	ID         string               `json:"id"`
	Name       expert.LocalizedText `json:"name"`
	Profession expert.LocalizedText `json:"profession,omitempty"`
	Avatar     string               `json:"avatar,omitempty"`
	Role       string               `json:"role"`
}

// ExpertDetail is the inspect response for a single bundle. It deliberately
// exposes manifest metadata only, never persona Markdown or tool prompts.
type ExpertDetail struct {
	ID                string                 `json:"id"`
	ExpertType        string                 `json:"expertType"`
	DisplayName       expert.LocalizedText   `json:"displayName"`
	CategoryID        string                 `json:"categoryId,omitempty"`
	QuickPrompts      []expert.LocalizedText `json:"quickPrompts,omitempty"`
	DefaultInitPrompt expert.LocalizedText   `json:"defaultInitPrompt,omitempty"`
	Members           []ExpertMember         `json:"members,omitempty"`
	Invalid           bool                   `json:"invalid"`
	Reason            string                 `json:"reason,omitempty"`
}

// SessionExpertState is the current session identity projection. A nil Expert
// represents an explicitly unbound session.
type SessionExpertState struct {
	SessionID string        `json:"sessionId"`
	Expert    *ExpertDetail `json:"expert,omitempty"`
}

func expertSummaryFromRuntime(summary expert.Summary) ExpertSummary {
	return ExpertSummary{
		ID: summary.Name, ExpertType: summary.ExpertType, DisplayName: summary.DisplayName,
		Source: summary.Source, Invalid: summary.Invalid, Reason: summary.InvalidReason,
	}
}

func expertDetailFromBundle(bundle *expert.Bundle) *ExpertDetail {
	if bundle == nil {
		return nil
	}
	detail := &ExpertDetail{
		ID: bundle.Name, ExpertType: bundle.Manifest.ExpertType, DisplayName: bundle.Manifest.DisplayName,
		CategoryID: bundle.Manifest.CategoryID, QuickPrompts: append([]expert.LocalizedText(nil), bundle.Manifest.QuickPrompts...),
		DefaultInitPrompt: bundle.Manifest.DefaultInitPrompt, Invalid: bundle.Invalid, Reason: bundle.InvalidReason,
	}
	for _, member := range bundle.Manifest.Members {
		detail.Members = append(detail.Members, ExpertMember{
			ID: member.ID, Name: member.Name, Profession: member.Profession, Avatar: member.Avatar, Role: member.Role,
		})
	}
	return detail
}

func (s *Server) expertWorkDir(sessionID string) (string, error) {
	if s == nil || s.cfg == nil {
		return "", ErrSessionNotFound
	}
	if strings.TrimSpace(sessionID) == "" {
		return s.cfg.GetWorkDir(), nil
	}
	workDir, found, err := s.findSessionWorkDir(sessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrSessionNotFound
	}
	return workDir, nil
}

// ListExperts returns the discoverable bundles for either the default work
// directory or the authoritative work directory of sessionID.
func (s *Server) ListExperts(sessionID string) ([]ExpertSummary, error) {
	workDir, err := s.expertWorkDir(sessionID)
	if err != nil {
		return nil, err
	}
	summaries := agentruntime.ListExperts(workDir)
	result := make([]ExpertSummary, 0, len(summaries))
	for _, summary := range summaries {
		result = append(result, expertSummaryFromRuntime(summary))
	}
	return result, nil
}

// InspectExpert returns one validated/displayable bundle for the same work
// directory resolution as ListExperts. It does not bind the session.
func (s *Server) InspectExpert(sessionID, expertID string) (*ExpertDetail, error) {
	workDir, err := s.expertWorkDir(sessionID)
	if err != nil {
		return nil, err
	}
	bundle, err := agentruntime.InspectExpert(workDir, expertID)
	if err != nil {
		return nil, err
	}
	return expertDetailFromBundle(bundle), nil
}

func (s *Server) isAllocatedSessionID(id string) bool {
	if s == nil || id == "" {
		return false
	}
	s.mu.RLock()
	_, ok := s.allocatedSessionIDs[id]
	s.mu.RUnlock()
	return ok
}

// sessionForExpertMutation opens an existing session, or materializes a
// server-issued deferred WebUI session. Arbitrary unknown IDs are rejected so
// an identity mutation cannot create an untracked session namespace.
func (s *Server) sessionForExpertMutation(id string) (*APISession, error) {
	workDir, found, err := s.findSessionWorkDir(id)
	if err != nil {
		return nil, err
	}
	if !found {
		if !s.isAllocatedSessionID(id) || s.cfg == nil {
			return nil, ErrSessionNotFound
		}
		workDir = s.cfg.GetWorkDir()
	}
	return s.getOrCreateSession(id, workDir)
}

// GetSessionExpert returns the Runtime-resolved identity of a persisted or
// active WebUI session. It intentionally reuses the session Runtime instead
// of reading expert_id or package files in this adapter.
func (s *Server) GetSessionExpert(id string) (*SessionExpertState, error) {
	sess, err := s.sessionForExpertMutation(id)
	if err != nil {
		return nil, err
	}
	state := &SessionExpertState{SessionID: sess.ID}
	if sess.Runtime == nil {
		return state, nil
	}
	binding, _ := sess.Runtime.ExpertState()
	if binding != nil {
		state.Expert = expertDetailFromBundle(binding.Bundle)
	}
	return state, nil
}

// SetSessionExpert is the Serve/WebUI identity transition. The Runtime owns
// validation, session persistence and resource rehydration; the adapter only
// rebuilds its manager/tool projection from the new Runtime state.
func (s *Server) SetSessionExpert(ctx context.Context, id, expertID string) (*SessionExpertState, error) {
	sess, err := s.sessionForExpertMutation(id)
	if err != nil {
		return nil, err
	}
	if sess.Runtime == nil || sess.Manager == nil || s.settings == nil {
		return nil, ErrSessionNotFound
	}
	if strings.TrimSpace(expertID) != "" {
		bundle, inspectErr := sess.Runtime.InspectExpert(expertID)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if bundle.Invalid {
			return nil, fmt.Errorf("expert bundle %q is invalid: %s", bundle.Name, bundle.InvalidReason)
		}
	}
	guard, err := agentruntime.AcquireSessionMutation(ctx, s.settings.GetSessionDir(), sess.ID, agentruntime.ExecutionAdmissionOptions{})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionExpertMutationBusy, err)
	}
	defer guard.Release()
	if !s.pool.Pin(sess) {
		return nil, ErrSessionNotFound
	}
	defer s.pool.Unpin(sess)

	sess.Lock()
	if sess.IsRunning() {
		sess.Unlock()
		return nil, ErrSessionExpertMutationBusy
	}
	if err := sess.Runtime.SetExpert(expertID); err != nil {
		sess.Unlock()
		return nil, err
	}
	// SetExpert rehydrates Runtime resources. Rebuild all adapter aliases and
	// discard the old manager so roster/mailbox context cannot leak across an
	// identity change.
	sess.SkillsMgr = sess.Runtime.SkillsMgr
	sess.ExtraContext = sess.Runtime.ExtraContext
	sess.RuleContent = sess.Runtime.RuleContent
	sess.AgentMgr = nil
	if err := s.syncSessionTools(sess, false); err != nil {
		sess.Unlock()
		return nil, err
	}
	state := &SessionExpertState{SessionID: sess.ID}
	if binding, _ := sess.Runtime.ExpertState(); binding != nil {
		state.Expert = expertDetailFromBundle(binding.Bundle)
	}
	sess.Touch()
	sess.Unlock()
	s.publishSessionStreamEvent(sess.ID, "expert_changed", state)
	return state, nil
}

// ForkSessionWithExpert validates the requested child identity through the
// source Runtime, then delegates the durable branch operation to the canonical
// Runtime fork boundary. The source identity/history remains immutable.
func (s *Server) ForkSessionWithExpert(ctx context.Context, id string, options agentruntime.ForkOptions, expertID string) (agentruntime.ForkResult, error) {
	sess, err := s.sessionForExpertMutation(id)
	if err != nil {
		return agentruntime.ForkResult{}, err
	}
	if sess.Runtime == nil {
		return agentruntime.ForkResult{}, ErrSessionNotFound
	}
	if strings.TrimSpace(expertID) != "" {
		bundle, inspectErr := sess.Runtime.InspectExpert(expertID)
		if inspectErr != nil {
			return agentruntime.ForkResult{}, inspectErr
		}
		if bundle.Invalid {
			return agentruntime.ForkResult{}, fmt.Errorf("expert bundle %q is invalid: %s", bundle.Name, bundle.InvalidReason)
		}
	}
	options.SourceSessionID = id
	return agentruntime.ForkWithExpert(ctx, s.settings.GetSessionDir(), options, expertID)
}

// IsSessionExpertMutationBusy lets the HTTP adapter map Runtime lease
// contention to the same 409 semantic used by other session mutations.
func IsSessionExpertMutationBusy(err error) bool {
	return errors.Is(err, ErrSessionExpertMutationBusy) ||
		errors.Is(err, session.ErrSessionRunActive) ||
		errors.Is(err, session.ErrRuntimeLeaseBusy) ||
		errors.Is(err, agentruntime.ErrDetachedRemoteExecution)
}
