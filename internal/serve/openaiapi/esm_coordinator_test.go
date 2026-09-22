package openaiapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/esm"
	"github.com/oschina/mothx/internal/session"
)

func TestESMCoordinatorStopAllCancelsAndWaits(t *testing.T) {
	coordinator := newESMCoordinator()
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	done := make(chan struct{})
	coordinator.mu.Lock()
	coordinator.running["session-1"] = cancelWorker
	coordinator.done["session-1"] = done
	coordinator.mu.Unlock()
	go func() {
		<-workerCtx.Done()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.stopAll(ctx); err != nil {
		t.Fatalf("stopAll: %v", err)
	}
	coordinator.mu.Lock()
	closed := coordinator.closed
	coordinator.mu.Unlock()
	if !closed {
		t.Fatal("stopAll did not close the coordinator")
	}
}

func TestESMCoordinatorStopCancelsAndWaits(t *testing.T) {
	coordinator := newESMCoordinator()
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	done := make(chan struct{})
	coordinator.mu.Lock()
	coordinator.running["session-1"] = cancelWorker
	coordinator.done["session-1"] = done
	coordinator.mu.Unlock()
	go func() {
		<-workerCtx.Done()
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.stop(ctx, "session-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestESMCoordinatorWaitsForForegroundExecutionInsteadOfDroppingContinuation(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const sessionID = "webui-esm-wait-for-foreground"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.esmStore().Create(context.Background(), sessionID, "continue after the foreground run"); err != nil {
		t.Fatal(err)
	}
	lease, err := agentruntime.AcquireExecutionAdmission(context.Background(), srv.settings.GetSessionDir(), sessionID, agentruntime.ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("acquire foreground lease: %v", err)
	}
	defer lease.Release()

	srv.startESM(sessionID)
	coordinator := srv.ensureESMCoordinator()
	deadline := time.Now().Add(time.Second)
	for {
		coordinator.mu.Lock()
		_, waiting := coordinator.running[sessionID]
		coordinator.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ESM coordinator dropped the continuation while a foreground run owned the execution lease")
		}
		time.Sleep(time.Millisecond)
	}

	// Cancellation must unblock the admission wait so a pause/clear or shutdown
	// never leaves a coordinator goroutine behind.
	srv.stopESM(sessionID)
}

// The Serve half of the same idle-continuation contract asserted in TUI:
// only an auto-runnable objective may reach the role supervisor. A paused
// objective must return before durable Run creation, even though the
// coordinator itself is invoked directly by an adapter entry point.
func TestESMCoordinatorIdleGateRejectsPausedObjective(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const sessionID = "webui-esm-paused-idle-gate"
	if _, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir()); err != nil {
		t.Fatal(err)
	}
	store := srv.esmStore()
	if _, err := store.Create(context.Background(), sessionID, "do not continue while paused"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pause(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}

	srv.runESMCoordinator(context.Background(), sessionID)
	obj, err := store.Get(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if obj.CanAutoRun() {
		t.Fatalf("paused objective passed the auto-run gate: %#v", obj)
	}
	runs, err := session.ListSessionRuns(srv.settings.GetSessionDir(), sessionID, 10)
	if err != nil {
		t.Fatalf("list durable runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("paused objective created ESM role runs: %#v", runs)
	}
}

func TestWebSessionAgentOptionsInjectESMObjectiveVersions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	const sessionID = "webui-esm-steering-options"
	sess, err := srv.getOrCreateSession(sessionID, srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	opts := srv.buildAgentOptionsForSession(sess, srv.model, "yolo")
	if opts.GetSteeringMessages == nil {
		t.Fatal("normal WebUI agent options are missing ESM steering")
	}
	if _, err := srv.esmStore().Create(context.Background(), sessionID, "finish the first objective"); err != nil {
		t.Fatal(err)
	}
	messages := opts.GetSteeringMessages()
	if len(messages) != 1 || !messages[0].SystemInjected || !strings.Contains(messages[0].Content, "finish the first objective") {
		t.Fatalf("initial WebUI steering = %#v", messages)
	}
	if messages := opts.GetSteeringMessages(); len(messages) != 0 {
		t.Fatalf("duplicate WebUI steering = %#v, want none", messages)
	}
	if _, err := srv.esmStore().Edit(context.Background(), sessionID, "finish the revised objective"); err != nil {
		t.Fatal(err)
	}
	messages = opts.GetSteeringMessages()
	if len(messages) != 1 || !strings.Contains(messages[0].Content, "finish the revised objective") {
		t.Fatalf("revised WebUI steering = %#v", messages)
	}
}

func TestWebESMRuntimeAdapterHandlesClosedSessionRuntime(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	sess, err := srv.getOrCreateSession("webui-esm-closed-runtime", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	if sess.Runtime == nil {
		t.Fatal("test session has no runtime")
	}
	sess.Runtime.Close()

	adapter := &webESMRuntimeAdapter{
		server: srv, sess: sess, workDir: sess.WorkDir,
		source: "webui", mode: "agent",
	}
	_, err = adapter.RunRole(context.Background(), esm.RoleRequest{
		SessionID: sess.ID, RunID: "closed-runtime-role", Role: esm.RoleWorker,
		WorkDir: sess.WorkDir, Mode: "agent", Prompt: "should fail cleanly",
	})
	if err == nil || !strings.Contains(err.Error(), "agent manager is unavailable") {
		t.Fatalf("RunRole error = %v, want unavailable manager error", err)
	}
}

func TestApplyESMWorkerContinueResetsCompletionRejectionStreak(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	ctx := context.Background()
	const sessionID = "webui-esm-worker-continue"
	store := srv.esmStore()
	if _, err := store.Create(ctx, sessionID, "finish migration"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 1; i <= 4; i++ {
		runID := fmt.Sprintf("run-%d", i)
		obj, err := store.UpdateFromModelForRun(ctx, sessionID, esm.StatusComplete, "worker evidence", runID)
		if err != nil {
			t.Fatalf("candidate %d: %v", i, err)
		}
		if _, err := store.RejectCompletionCandidateForRun(ctx, sessionID, runID, "missing requirement", []string{"finish implementation"}); err != nil {
			t.Fatalf("reject %d: %v", i, err)
		}

		if !srv.applyESMWorker(ctx, store, obj, runID+"-continue", esm.RoleResult{
			Response:  `{"status":"continue","summary":"implemented missing requirement","evidence":["focused test passes"],"remaining_work":[],"blockers":[]}`,
			ToolCalls: 1,
			ToolError: map[string]bool{},
		}) {
			t.Fatalf("apply continue %d failed", i)
		}
		obj, err = store.Get(ctx, sessionID)
		if err != nil {
			t.Fatalf("Get after continue %d: %v", i, err)
		}
		if obj.Status != esm.StatusActive || obj.RejectionCount != 0 || obj.RejectionRunID != "" {
			t.Fatalf("continue %d did not reset rejection streak: %#v", i, obj)
		}
	}
}

func TestResolveESMRuntimePolicyDerivesUnattendedMode(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	sess, err := srv.getOrCreateSession("webui-esm-mode-policy", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("getOrCreateSession: %v", err)
	}
	if sess.Runtime == nil {
		t.Fatal("test session has no runtime")
	}

	for _, tt := range []struct{ session, want string }{
		{"agent", "yolo"},
		{"plan", "yolo"},
		{"yolo", "yolo"},
		{"os", "os"},
		{"", "yolo"},
	} {
		sess.Mode = tt.session
		_, mode, err := srv.resolveESMRuntimePolicy(sess)
		if err != nil {
			t.Fatalf("resolveESMRuntimePolicy(%q): %v", tt.session, err)
		}
		if mode != tt.want {
			t.Fatalf("resolveESMRuntimePolicy(%q) = %q, want %q", tt.session, mode, tt.want)
		}
	}
}
