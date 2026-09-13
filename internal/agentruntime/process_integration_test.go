package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestRuntimeShutdownAcrossProcessBoundary(t *testing.T) {
	sessionDir := t.TempDir()
	runRuntimeProcessHelper(t, "shutdown", sessionDir, "process-shutdown-session", "process-shutdown-run")
	run, err := session.GetSessionRun(sessionDir, "process-shutdown-run")
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "cancelled" {
		t.Fatalf("run after child shutdown = %#v", run)
	}
}

func TestRuntimeOrphanRecoveryAcrossProcessBoundary(t *testing.T) {
	sessionDir := t.TempDir()
	runRuntimeProcessHelper(t, "orphan", sessionDir, "process-orphan-session", "process-orphan-run")
	before, err := session.GetSessionRun(sessionDir, "process-orphan-run")
	if err != nil || before == nil || before.Status != "running" {
		t.Fatalf("run before recovery = %#v, err=%v", before, err)
	}
	if _, err := RecoverOrphanedRuns(sessionDir, func(run session.SessionRun) RecoveryAction {
		return RecoveryFailLocal
	}, nil); err != nil {
		t.Fatal(err)
	}
	after, err := session.GetSessionRun(sessionDir, "process-orphan-run")
	if err != nil || after == nil || after.Status != "failed" {
		t.Fatalf("run after recovery = %#v, err=%v", after, err)
	}
	events, err := session.ListSessionRunEvents(sessionDir, "process-orphan-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventType != "started" || events[1].EventType != "recovered" || events[1].Status != "failed" {
		t.Fatalf("recovery events = %#v", events)
	}
}

func TestRuntimeKill9ConvergesWithoutUDP(t *testing.T) {
	sessionDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "owner-ready")
	cmd := startRuntimeProcessHelper(t, "hold", sessionDir, "kill9-session", "kill9-run", marker)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitForRuntimeMarker(t, marker)
	initial, err := session.GetSessionRun(sessionDir, "kill9-run")
	if err != nil || initial == nil || initial.Status != "running" {
		t.Fatalf("run before kill = %#v, err=%v", initial, err)
	}

	coordinator := NewRecoveryCoordinator(sessionDir, RecoveryCoordinatorOptions{ScanInterval: 10 * time.Millisecond, AttemptTimeout: 10 * time.Second, Policy: func(session.SessionRun) RecoveryAction { return RecoveryFailLocal }})
	if err := coordinator.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := coordinator.Stop(stopCtx); err != nil {
			t.Errorf("stop coordinator: %v", err)
		}
	}()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("kill -9 helper unexpectedly exited cleanly")
	}
	if err := expireRuntimeLease(sessionDir, "kill9-session"); err != nil {
		t.Fatal(err)
	}
	coordinator.Wake()
	waitForRuntimeRunStatus(t, sessionDir, "kill9-run", "failed")
}

func TestRecoveryOwnerKill9CanBeTakenOver(t *testing.T) {
	sessionDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "recovery-ready")
	cmd := startRuntimeProcessHelper(t, "hold_recovery", sessionDir, "recovery-kill-session", "recovery-kill-run", marker)
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitForRuntimeMarker(t, marker)
	coordinator := NewRecoveryCoordinator(sessionDir, RecoveryCoordinatorOptions{ScanInterval: 10 * time.Millisecond, AttemptTimeout: 10 * time.Second, Policy: func(session.SessionRun) RecoveryAction { return RecoveryFailLocal }})
	if err := coordinator.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := coordinator.Stop(stopCtx); err != nil {
			t.Errorf("stop coordinator: %v", err)
		}
	}()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("recovery helper unexpectedly exited cleanly")
	}
	if err := expireRuntimeLease(sessionDir, "recovery-kill-session"); err != nil {
		t.Fatal(err)
	}
	coordinator.Wake()
	waitForRuntimeRunStatus(t, sessionDir, "recovery-kill-run", "failed")
	recovery, err := session.GetSessionRunRecovery(sessionDir, "recovery-kill-run")
	if err != nil || recovery == nil || recovery.State != session.SessionRunRecoveryComplete {
		t.Fatalf("recovery after owner kill = %#v, err=%v", recovery, err)
	}
}

func TestFirstSessionAdmissionCompetesAcrossProcesses(t *testing.T) {
	sessionDir := t.TempDir()
	controlDir := t.TempDir()
	gate := filepath.Join(controlDir, "start")
	markerA := filepath.Join(controlDir, "ready-a")
	markerB := filepath.Join(controlDir, "ready-b")
	resultA := filepath.Join(controlDir, "result-a")
	resultB := filepath.Join(controlDir, "result-b")
	const sessionID = "first-session-race"
	const runID = "first-session-race-run"

	cmdA := startRuntimeProcessHelperWithFiles(t, "compete_session", sessionDir, sessionID, runID, markerA, gate, resultA)
	cmdB := startRuntimeProcessHelperWithFiles(t, "compete_session", sessionDir, sessionID, runID, markerB, gate, resultB)
	if err := waitForRuntimeMarkerResult(markerA, 5*time.Second); err != nil {
		_ = cmdA.Process.Kill()
		_ = cmdB.Process.Kill()
		t.Fatal(err)
	}
	if err := waitForRuntimeMarkerResult(markerB, 5*time.Second); err != nil {
		_ = cmdA.Process.Kill()
		_ = cmdB.Process.Kill()
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, []byte("go"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdA.Wait(); err != nil {
		t.Fatalf("first admission process A: %v", err)
	}
	if err := cmdB.Wait(); err != nil {
		t.Fatalf("first admission process B: %v", err)
	}
	statusA, err := os.ReadFile(resultA)
	if err != nil {
		t.Fatal(err)
	}
	statusB, err := os.ReadFile(resultB)
	if err != nil {
		t.Fatal(err)
	}
	statuses := []string{string(statusA), string(statusB)}
	winners, blocked := 0, 0
	for _, status := range statuses {
		switch status {
		case "winner":
			winners++
		case "duplicate", "busy":
			blocked++
		default:
			t.Fatalf("first session competition result = %q, want winner and duplicate/busy", status)
		}
	}
	if winners != 1 || blocked != 1 {
		t.Fatalf("first session competition results = %#v, want one winner and one duplicate/busy", statuses)
	}
	run, err := session.GetSessionRun(sessionDir, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "completed" {
		t.Fatalf("admitted run = %#v, want one completed canonical run", run)
	}
	events, err := session.ListSessionRunEvents(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventType != "started" || events[1].EventType != "finished" {
		t.Fatalf("first session run events = %#v, want started and finished", events)
	}
}

func TestOldOwnerIsFencedBeforeSideEffectAfterCrossProcessTakeover(t *testing.T) {
	sessionDir := t.TempDir()
	controlDir := t.TempDir()
	ready := filepath.Join(controlDir, "ready")
	gate := filepath.Join(controlDir, "resume")
	result := filepath.Join(controlDir, "result")
	const sessionID = "tool-fence-process-session"
	const runID = "tool-fence-process-run"

	cmd := startRuntimeProcessHelperWithFiles(t, "tool_fence_hold", sessionDir, sessionID, runID, ready, gate, result)
	// Start-up (process exec, migrations, admission, durable begin) is setup, not
	// the invariant under test, so it gets a load-tolerant budget.
	if err := waitForRuntimeMarkerResult(ready, 30*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	recovered, recoverErr := takeoverExpiredRun(sessionDir, sessionID, runID)
	if recoverErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(recoverErr)
	}
	if len(recovered.Failed) != 1 || recovered.Failed[0].ID != runID {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("takeover recovery = %#v, want the orphaned run failed", recovered)
	}
	if err := os.WriteFile(gate, []byte("resume"), 0600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("old owner process: %v", err)
	}
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "blocked" {
		t.Fatalf("old owner side-effect fence result = %q, want blocked", data)
	}
}

func runRuntimeProcessHelper(t *testing.T, action, sessionDir, sessionID, runID string) {
	t.Helper()
	cmd := startRuntimeProcessHelper(t, action, sessionDir, sessionID, runID, "")
	if err := cmd.Wait(); err != nil {
		t.Fatalf("process helper: %v", err)
	}
}

func startRuntimeProcessHelper(t *testing.T, action, sessionDir, sessionID, runID, marker string) *exec.Cmd {
	return startRuntimeProcessHelperWithFiles(t, action, sessionDir, sessionID, runID, marker, "", "")
}

func startRuntimeProcessHelperWithFiles(t *testing.T, action, sessionDir, sessionID, runID, marker, gate, result string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeProcessHelper$")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"MOTHX_RUNTIME_PROCESS_HELPER=1",
		"MOTHX_RUNTIME_PROCESS_ACTION="+action,
		"MOTHX_RUNTIME_PROCESS_SESSION_DIR="+sessionDir,
		"MOTHX_RUNTIME_PROCESS_SESSION_ID="+sessionID,
		"MOTHX_RUNTIME_PROCESS_RUN_ID="+runID,
	)
	if marker != "" {
		cmd.Env = append(cmd.Env, "MOTHX_RUNTIME_PROCESS_MARKER="+marker)
	}
	if gate != "" {
		cmd.Env = append(cmd.Env, "MOTHX_RUNTIME_PROCESS_GATE="+gate)
	}
	if result != "" {
		cmd.Env = append(cmd.Env, "MOTHX_RUNTIME_PROCESS_RESULT="+result)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process helper: %v", err)
	}
	return cmd
}

func TestRuntimeProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_RUNTIME_PROCESS_HELPER") != "1" {
		return
	}
	sessionDir := os.Getenv("MOTHX_RUNTIME_PROCESS_SESSION_DIR")
	sessionID := os.Getenv("MOTHX_RUNTIME_PROCESS_SESSION_ID")
	runID := os.Getenv("MOTHX_RUNTIME_PROCESS_RUN_ID")
	marker := os.Getenv("MOTHX_RUNTIME_PROCESS_MARKER")
	gate := os.Getenv("MOTHX_RUNTIME_PROCESS_GATE")
	result := os.Getenv("MOTHX_RUNTIME_PROCESS_RESULT")
	manager := session.New(t.TempDir(), sessionDir)
	action := os.Getenv("MOTHX_RUNTIME_PROCESS_ACTION")
	if action == "compete_session" {
		if marker != "" {
			if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := waitForRuntimeGate(gate, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := manager.InitWithID(sessionID); err != nil {
			if errors.Is(err, session.ErrSessionIDExists) {
				if writeErr := os.WriteFile(result, []byte("duplicate"), 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
				return
			}
			if errors.Is(err, session.ErrRuntimeLeaseBusy) || errors.Is(err, session.ErrRuntimeLeaseLost) {
				if writeErr := os.WriteFile(result, []byte("busy"), 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
				return
			}
			if writeErr := os.WriteFile(result, []byte("session-error:"+err.Error()), 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			return
		}
		guard, err := session.AcquireExecutionAdmission(sessionDir, sessionID)
		if err != nil {
			if writeErr := os.WriteFile(result, []byte("admission-error:"+err.Error()), 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			return
		}
		defer guard.Release()
		execution := &ExecutionRuntime{}
		execution.SetRunStore(RunStore{SessionDir: sessionDir})
		execution.SetEventSink(SessionRunEventSink{SessionDir: sessionDir})
		startedAt := time.Now()
		intent := ExecutionIntent{ID: "intent_" + runID, SessionID: sessionID, Source: "acp", Model: "process-model", Mode: "agent", WorkDir: manager.GetHeader().Cwd, Request: []byte(`{}`), Policy: []byte(`{}`), CreatedAt: startedAt}
		if _, err := execution.BeginIntentDurable(context.Background(), intent, DurableRun{ID: runID, SessionID: sessionID, IntentID: intent.ID, WorkDir: manager.GetHeader().Cwd, Source: "acp", Model: "process-model", Mode: "agent", Status: "running", StartedAt: startedAt}, RunEvent{SessionID: sessionID, RunID: runID, EventType: "started", Source: "acp", Status: "running", Timestamp: startedAt}); err != nil {
			if writeErr := os.WriteFile(result, []byte("begin-error:"+err.Error()), 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			return
		}
		if err := execution.FinishDurable(runID, RunStateCompleted, "", RunEvent{SessionID: sessionID, RunID: runID, EventType: "finished", Source: "acp", Status: "completed", Timestamp: time.Now()}); err != nil {
			if writeErr := os.WriteFile(result, []byte("finish-error:"+err.Error()), 0600); writeErr != nil {
				t.Fatal(writeErr)
			}
			return
		}
		if err := os.WriteFile(result, []byte("winner"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if action == "tool_fence_hold" {
		if err := manager.InitWithID(sessionID); err != nil {
			t.Fatal(err)
		}
		guard, err := session.AcquireExecutionAdmission(sessionDir, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		defer guard.Release()
		execution := &ExecutionRuntime{}
		execution.SetRunStore(RunStore{SessionDir: sessionDir})
		execution.SetEventSink(SessionRunEventSink{SessionDir: sessionDir})
		now := time.Now()
		if _, err := execution.BeginDurable(context.Background(), DurableRun{ID: runID, SessionID: sessionID, Source: "tui", Status: "running", StartedAt: now}, RunEvent{SessionID: sessionID, RunID: runID, EventType: "started", Source: "tui", Status: "running", Timestamp: now}); err != nil {
			t.Fatal(err)
		}
		runtime := &SessionRuntime{ID: sessionID, Manager: manager, Execution: execution}
		hook := beforeToolExecuteForRuntime(runtime)
		if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := waitForRuntimeGate(gate, 60*time.Second); err != nil {
			t.Fatal(err)
		}
		decision := hook(agent.BeforeToolExecuteContext{RunID: runID, ExecutionContext: context.Background(), SideEffecting: true})
		status := "allowed"
		if decision != nil && decision.Block {
			status = "blocked"
		}
		if err := os.WriteFile(result, []byte(status), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if action == "hold_recovery" {
		if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{ID: runID, SessionID: sessionID, Source: "tui", Status: "running", StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		guard, err := session.AcquireRecovery(sessionDir, sessionID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.BeginSessionRunRecovery(sessionDir, sessionID, runID, "startup", "owner_lost", 0); err != nil {
			guard.Release()
			t.Fatal(err)
		}
		if marker != "" {
			if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
				guard.Release()
				t.Fatal(err)
			}
		}
		select {}
	}
	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: sessionDir})
	execution.SetEventSink(SessionRunEventSink{SessionDir: sessionDir})
	runtime := &SessionRuntime{ID: sessionID, Source: SourceWebUI, WorkDir: manager.GetHeader().Cwd, Manager: manager, Execution: execution}
	if _, err := execution.BeginDurable(context.Background(), DurableRun{
		ID: runID, SessionID: sessionID, WorkDir: manager.GetHeader().Cwd,
		Source: "webui", Model: "process-model", Mode: "agent", Status: "running", StartedAt: time.Now(),
	}, RunEvent{SessionID: sessionID, RunID: runID, EventType: "started", Source: "webui", Status: "running", Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if action == "shutdown" {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if action == "hold" {
		if marker != "" {
			if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		select {}
	}
}

func waitForRuntimeMarker(t *testing.T, marker string) {
	t.Helper()
	// Helper process startup (exec, migrations, admission, durable begin) is
	// setup, not the invariant under test, so it gets a load-tolerant budget.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runtime helper marker %q was not created", marker)
}

func waitForRuntimeMarkerResult(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("runtime process marker %q was not created", path)
}

func waitForRuntimeGate(path string, timeout time.Duration) error {
	if path == "" {
		return fmt.Errorf("runtime process gate is required")
	}
	return waitForRuntimeMarkerResult(path, timeout)
}

// takeoverExpiredRun expires the old owner's lease and retries the takeover
// until it lands.
//
// The old owner keeps a live heartbeat, and renewing one's own expired row is
// deliberate (internal/session fences renewal on owner/epoch/token so only a
// real takeover can stop it, see RuntimeLeaseDAO.Renew). Expiring the row
// therefore only holds until the next renewal (runtimeHeartbeatEvery, 3s); on a
// loaded machine the recovery can be delayed past that point and would correctly
// refuse to take over a lease it can still observe as valid. Re-expiring and
// retrying keeps the scenario deterministic without weakening the invariant
// under test: recovery must never take over a lease that still looks valid.
func takeoverExpiredRun(sessionDir, sessionID, runID string) (RunRecoveryResult, error) {
	deadline := time.Now().Add(30 * time.Second)
	var last RunRecoveryResult
	for {
		if err := expireRuntimeLease(sessionDir, sessionID); err != nil {
			return last, err
		}
		recovered, err := RecoverOrphanedSessionRun(sessionDir, sessionID, nil, nil)
		if err == nil && len(recovered.Failed) == 1 && recovered.Failed[0].ID == runID {
			return recovered, nil
		}
		if err != nil {
			return recovered, err
		}
		last = recovered
		if time.Now().After(deadline) {
			return last, fmt.Errorf("takeover recovery = %#v, want the orphaned run failed (the old owner kept renewing its own expired lease)", last)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func expireRuntimeLease(sessionDir, sessionID string) error {
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	_, err = db.Bun().Exec(`UPDATE session_runtime_leases SET expires_at = CAST(strftime('%s','now') AS INTEGER) - 1 WHERE session_id = ?`, sessionID)
	return err
}

func waitForRuntimeRunStatus(t *testing.T, sessionDir, runID, status string) {
	t.Helper()
	// The coordinator's recovery attempt is bounded by its own timeout, so this
	// wait only needs to outlast a slow (fsync-heavy) attempt, not a healthy one.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		run, err := session.GetSessionRun(sessionDir, runID)
		if err != nil {
			t.Fatal(err)
		}
		if run != nil && run.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := session.GetSessionRun(sessionDir, runID)
	t.Fatalf("run %s did not reach %s: %#v", runID, status, run)
}
