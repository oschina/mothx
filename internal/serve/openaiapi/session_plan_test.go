package openaiapi

import (
	"errors"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestUpdateSessionPlanPersistsCanonicalPlan(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.InitWithID("plan-edit"); err != nil {
		t.Fatalf("init session: %v", err)
	}
	if _, err := mgr.AppendMessage(provider.NewUserMessage("start")); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	updated, entry, err := srv.UpdateSessionPlan(t.Context(), "plan-edit", SessionTaskPlan{
		Title: "Edited plan",
		Note:  "Edited from the WebUI",
		Steps: []SessionPlanStep{
			{Title: "Keep the existing step", Status: "done"},
			{Title: "Add a new step", Status: ""},
			{Title: "Check status mapping", Status: "running"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateSessionPlan: %v", err)
	}
	if updated.Title != "Edited plan" || updated.Note != "Edited from the WebUI" {
		t.Fatalf("updated plan = %#v", updated)
	}
	if len(updated.Steps) != 3 || updated.Steps[1].Status != "pending" {
		t.Fatalf("updated steps = %#v", updated.Steps)
	}
	if entry == nil || entry.Plan == nil || entry.ToolCallID == "" {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Role != "toolCall" || entry.ToolName != "plan" {
		t.Fatalf("entry role = %q tool = %q", entry.Role, entry.ToolName)
	}

	entries, err := srv.GetSessionMessages("plan-edit")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	var persisted *SessionMessageEntry
	for i := range entries {
		if entries[i].ToolCallID == entry.ToolCallID && entries[i].Plan != nil {
			persisted = &entries[i]
			break
		}
	}
	if persisted == nil {
		t.Fatalf("persisted plan call not found: %#v", entries)
	}
	if persisted.Plan.Title != "Edited plan" || len(persisted.Plan.Steps) != 3 {
		t.Fatalf("persisted plan = %#v", persisted.Plan)
	}
	if persisted.Plan.Steps[0].Title != "Keep the existing step" || persisted.Plan.Steps[0].Status != "done" {
		t.Fatalf("persisted first step = %#v", persisted.Plan.Steps[0])
	}
}

func TestUpdateSessionPlanRejectsInvalidPlans(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	tests := []struct {
		name string
		plan SessionTaskPlan
	}{
		{name: "no steps", plan: SessionTaskPlan{Title: "Empty"}},
		{name: "empty step", plan: SessionTaskPlan{Steps: []SessionPlanStep{{Title: "  "}}}},
		{name: "invalid status", plan: SessionTaskPlan{Steps: []SessionPlanStep{{Title: "Step", Status: "blocked"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := srv.UpdateSessionPlan(t.Context(), "missing", tt.plan)
			if !errors.Is(err, ErrSessionPlanInvalid) {
				t.Fatalf("error = %v, want ErrSessionPlanInvalid", err)
			}
		})
	}
}

func TestUpdateSessionPlanRejectsRunningSession(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	sess, err := srv.getOrCreateSession("plan-busy", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	sess.SetRunning(true)
	defer sess.SetRunning(false)

	_, _, err = srv.UpdateSessionPlan(t.Context(), "plan-busy", SessionTaskPlan{
		Steps: []SessionPlanStep{{Title: "Should not be written", Status: "pending"}},
	})
	if !errors.Is(err, ErrSessionPlanBusy) {
		t.Fatalf("error = %v, want ErrSessionPlanBusy", err)
	}
}
