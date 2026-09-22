package esm

import (
	"context"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// AddGuidance queues user guidance for the session's objective. The guidance
// is stamped with the objective's current version so adapters can detect
// stale submissions, and the Supervisor injects it into the next role prompts.
func (s *Store) AddGuidance(ctx context.Context, sessionID, text string) (*Objective, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("guidance cannot be empty")
	}
	obj, err := s.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	g := session.ESMGuidance{
		ID:               "guidance-" + session.GenerateID(),
		SessionID:        sessionID,
		ObjectiveVersion: formatTime(obj.UpdatedAt),
		Guidance:         text,
	}
	if err := session.SaveESMGuidance(s.sessionDir, g); err != nil {
		return nil, err
	}
	return obj, nil
}

// PendingGuidance returns queued guidance not yet injected into a role run.
func (s *Store) PendingGuidance(ctx context.Context, sessionID string) ([]session.ESMGuidance, error) {
	_ = ctx
	return session.ListESMGuidance(s.sessionDir, sessionID, "pending", 100)
}

// ConsumeGuidance marks queued guidance as applied after a role run used it.
func (s *Store) ConsumeGuidance(ctx context.Context, sessionID string, ids []string) error {
	_ = ctx
	return session.ConsumeESMGuidance(s.sessionDir, sessionID, ids)
}

// FormatGuidanceSuffix renders queued user guidance as a prompt section. The
// guidance is user data: it is listed verbatim and never treated as system or
// developer instructions by the role prompts.
func FormatGuidanceSuffix(items []session.ESMGuidance) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nUser guidance queued for this objective:\n")
	for _, item := range items {
		b.WriteString("- " + item.Guidance + "\n")
	}
	return b.String()
}
