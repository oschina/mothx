package openaiapi

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
)

// TestRequireESMControlIdleAllowsOrphanedRun locks in the control-plane rule
// that an orphaned canonical Run (an active row with no valid lease) must not
// block an explicit pause/resume/clear. Such a Run has no live owner; it is
// waiting for the recovery coordinator, and treating it as busy would make the
// objective permanently uncontrollable after a failed local admission.
func TestRequireESMControlIdleAllowsOrphanedRun(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const id = "esm-orphaned-control"
	if _, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir()); err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}

	store := agentruntime.RunStore{SessionDir: srv.settings.GetSessionDir()}
	now := time.Now()
	if _, err := store.CreateRunWithEvent(agentruntime.DurableRun{
		ID: "orphaned-run", SessionID: id, Attempt: 1, Source: "webui", Model: "m1", Mode: "yolo",
		Status: "running", StartedAt: now,
	}, agentruntime.RunEvent{
		SessionID: id, RunID: "orphaned-run", EventType: "started", Source: "webui",
		Status: "running", Model: "m1", Mode: "yolo", Timestamp: now,
	}); err != nil {
		t.Fatalf("create orphaned run: %v", err)
	}

	snapshot, err := agentruntime.InspectSessionExecution(srv.settings.GetSessionDir(), id)
	if err != nil {
		t.Fatalf("InspectSessionExecution: %v", err)
	}
	if snapshot.State != agentruntime.SessionExecutionOrphaned {
		t.Fatalf("snapshot state = %s, want orphaned (%+v)", snapshot.State, snapshot)
	}
	if err := srv.requireESMControlIdle(id, false); err != nil {
		t.Fatalf("orphaned run must not block ESM control: %v", err)
	}
}
