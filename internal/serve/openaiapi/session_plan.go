package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

const (
	maxSessionPlanSteps     = 50
	maxSessionPlanTitleRune = 200
	maxSessionPlanNoteRune  = 4000
	maxSessionPlanStepRune  = 500
)

var (
	// ErrSessionPlanBusy means the session has a live Run or mutation lease, so
	// a user plan edit would race the Runtime-owned transcript.
	ErrSessionPlanBusy = errors.New("session is busy")
	// ErrSessionPlanInvalid means the submitted plan is missing content or has
	// an unsupported status.
	ErrSessionPlanInvalid = errors.New("invalid session plan")
)

// UpdateSessionPlan persists a user-authored plan update as a canonical plan
// tool call. The plan stays a projection of the session transcript: every
// entry point sees the same latest plan instead of an adapter-local override.
func (s *Server) UpdateSessionPlan(ctx context.Context, id string, plan SessionTaskPlan) (SessionTaskPlan, *SessionMessageEntry, error) {
	if s == nil || s.settings == nil || s.pool == nil {
		return SessionTaskPlan{}, nil, errors.New("server not ready")
	}
	if id == "" {
		return SessionTaskPlan{}, nil, ErrSessionNotFound
	}
	plan, err := normalizeSessionPlanUpdate(plan)
	if err != nil {
		return SessionTaskPlan{}, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// findSessionWorkDir also proves the session exists before we take the
	// mutation lease. The workdir itself is not needed for the write.
	if _, found, err := s.findSessionWorkDir(id); err != nil {
		return SessionTaskPlan{}, nil, err
	} else if !found {
		return SessionTaskPlan{}, nil, ErrSessionNotFound
	}

	// Fast local check keeps the common "editing while a run is streaming"
	// case out of the lease retry path. The shared mutation lease below is the
	// authority for cross-process ownership.
	if sess, err := s.pool.getExact(id); err != nil {
		return SessionTaskPlan{}, nil, err
	} else if sess != nil && sess.IsRunning() {
		return SessionTaskPlan{}, nil, ErrSessionPlanBusy
	}

	sessionDir := s.settings.GetSessionDir()
	guard, err := agentruntime.AcquireSessionMutation(ctx, sessionDir, id, agentruntime.ExecutionAdmissionOptions{})
	if err != nil {
		switch {
		case errors.Is(err, session.ErrRuntimeLeaseBusy),
			errors.Is(err, session.ErrSessionRunActive),
			errors.Is(err, agentruntime.ErrDetachedRemoteExecution):
			return SessionTaskPlan{}, nil, ErrSessionPlanBusy
		default:
			return SessionTaskPlan{}, nil, err
		}
	}
	defer guard.Release()

	mgr := (*session.Manager)(nil)
	if sess, err := s.pool.getExact(id); err == nil && sess != nil {
		mgr = sess.Manager
	}
	if mgr == nil {
		mgr, err = session.OpenByIDExact(sessionDir, id)
		if err != nil {
			return SessionTaskPlan{}, nil, fmt.Errorf("%w: %v", ErrSessionNotFound, err)
		}
	}

	args, err := json.Marshal(struct {
		Title string            `json:"title,omitempty"`
		Steps []SessionPlanStep `json:"steps"`
		Note  string            `json:"note,omitempty"`
	}{Title: plan.Title, Steps: plan.Steps, Note: plan.Note})
	if err != nil {
		return SessionTaskPlan{}, nil, fmt.Errorf("encode session plan: %w", err)
	}

	callID := "ui-plan-" + session.GenerateID()
	assistant := provider.NewAssistantMessage([]provider.ContentBlock{{
		Type: "toolCall",
		ToolCall: &provider.ToolCallBlock{
			ID:        callID,
			Name:      "plan",
			Arguments: json.RawMessage(args),
		},
	}})
	result := provider.NewToolResultMessage(callID, "plan", "Plan updated by user.", false)
	if _, err := mgr.AppendMessages([]provider.Message{assistant, result}); err != nil {
		return SessionTaskPlan{}, nil, err
	}

	// Return the persisted projection so the WebUI can upsert the exact entry
	// instead of rendering a client-local copy. A read failure after a
	// successful append must not make the caller retry the write.
	entry := &SessionMessageEntry{
		Role:       "toolCall",
		ToolCallID: callID,
		ToolName:   "plan",
		Plan:       &plan,
	}
	if entries, _, readErr := s.GetSessionMessagesLatest(id, 32); readErr == nil {
		for i := len(entries) - 1; i >= 0; i-- {
			candidate := entries[i]
			if candidate.Role == "toolCall" && candidate.ToolCallID == callID {
				entry = &candidate
				s.publishTranscriptEvent(id, messageTranscriptEvent(candidate))
				break
			}
		}
	}
	return plan, entry, nil
}

func normalizeSessionPlanUpdate(plan SessionTaskPlan) (SessionTaskPlan, error) {
	plan.Title = strings.TrimSpace(plan.Title)
	plan.Note = strings.TrimSpace(plan.Note)
	if len([]rune(plan.Title)) > maxSessionPlanTitleRune {
		return SessionTaskPlan{}, fmt.Errorf("%w: plan title is too long", ErrSessionPlanInvalid)
	}
	if len([]rune(plan.Note)) > maxSessionPlanNoteRune {
		return SessionTaskPlan{}, fmt.Errorf("%w: plan note is too long", ErrSessionPlanInvalid)
	}
	if len(plan.Steps) == 0 {
		return SessionTaskPlan{}, fmt.Errorf("%w: at least one step is required", ErrSessionPlanInvalid)
	}
	if len(plan.Steps) > maxSessionPlanSteps {
		return SessionTaskPlan{}, fmt.Errorf("%w: too many steps", ErrSessionPlanInvalid)
	}
	steps := make([]SessionPlanStep, 0, len(plan.Steps))
	for index, step := range plan.Steps {
		title := strings.TrimSpace(step.Title)
		if title == "" {
			return SessionTaskPlan{}, fmt.Errorf("%w: step %d is empty", ErrSessionPlanInvalid, index+1)
		}
		if len([]rune(title)) > maxSessionPlanStepRune {
			return SessionTaskPlan{}, fmt.Errorf("%w: step %d is too long", ErrSessionPlanInvalid, index+1)
		}
		status := normalizeSessionPlanStatus(step.Status)
		if status == "" {
			if strings.TrimSpace(step.Status) == "" {
				status = "pending"
			} else {
				return SessionTaskPlan{}, fmt.Errorf("%w: step %d has an unsupported status", ErrSessionPlanInvalid, index+1)
			}
		}
		steps = append(steps, SessionPlanStep{Title: title, Status: status})
	}
	plan.Steps = steps
	return plan, nil
}
