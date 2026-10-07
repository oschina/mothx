package agentruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

func TestAcquireExecutionAdmissionRecoversOrphanBeforeReturningGuard(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "admission-recovery")
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "stale", SessionID: "admission-recovery", Source: "tui", Status: "running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireExecutionAdmission(t.Context(), sessionDir, "admission-recovery", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	if binding := guard.Binding(); binding.Purpose != session.RuntimeLeasePurposeAdmission || binding.RunID != "" {
		t.Fatalf("admission binding = %+v", binding)
	}
	stale, err := session.GetSessionRun(sessionDir, "stale")
	if err != nil || stale == nil || stale.Status != "failed" {
		t.Fatalf("stale run = %#v, err=%v", stale, err)
	}
}

func TestAcquireExecutionAdmissionDoesNotDisplaceLiveOwner(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "admission-owned")
	owner, err := session.AcquireExecutionAdmission(sessionDir, "admission-owned")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Release()
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "owned", SessionID: "admission-owned", Source: "acp", Status: "running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireExecutionAdmission(t.Context(), sessionDir, "admission-owned", ExecutionAdmissionOptions{}); !errors.Is(err, session.ErrRuntimeLeaseBusy) {
		t.Fatalf("second admission error = %v, want lease busy", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := AcquireExecutionAdmission(ctx, sessionDir, "admission-owned", ExecutionAdmissionOptions{Wait: true, PollInterval: time.Millisecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting admission error = %v, want deadline", err)
	}
}

func TestAcquireExecutionAdmissionRetainsVerifiedRemoteRun(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "admission-remote")
	now := time.Now()
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "remote-parent", SessionID: "admission-remote", Source: "responses_background", Status: "running", StartedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseRun(sessionDir, session.ResponseRun{
		SessionID: "admission-remote", LocalRunID: "remote", LocalTurnID: "remote-parent", ResponseID: "resp",
		Provider: "openai", API: "openai-responses", State: "queued", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireExecutionAdmission(t.Context(), sessionDir, "admission-remote", ExecutionAdmissionOptions{}); !errors.Is(err, ErrDetachedRemoteExecution) {
		t.Fatalf("remote admission error = %v, want detached remote", err)
	}
}

func TestAcquireExecutionAdmissionQueuesBehindSameProcessDrainingLease(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "admission-drain")

	// Simulate the drain window: the predecessor run reached its terminal
	// state, but the Runtime-owned terminal persistence retry still holds the
	// execution lease for this process.
	owner, err := session.AcquireExecutionAdmission(sessionDir, "admission-drain")
	if err != nil {
		t.Fatal(err)
	}
	binding := owner.Binding()
	binding.RunID = "drain-run"
	binding.Purpose = session.RuntimeLeasePurposeExecution
	draining := &ExecutionRuntime{runID: "drain-run"}
	draining.terminalEventSet = true
	key := executionRegistrationKey(binding)
	localExecutionRegistry.Lock()
	localExecutionRegistry.entries[key] = localExecutionRegistration{binding: binding, runtime: draining}
	localExecutionRegistry.Unlock()
	t.Cleanup(func() {
		localExecutionRegistry.Lock()
		delete(localExecutionRegistry.entries, key)
		localExecutionRegistry.Unlock()
		owner.Release()
	})

	// A queued same-process successor must not fail fast with a misleading
	// cross-process busy error while the lease is draining; it waits (here
	// bounded by the ctx deadline).
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireExecutionAdmission(ctx, sessionDir, "admission-drain", ExecutionAdmissionOptions{PollInterval: 5 * time.Millisecond}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("draining admission error = %v, want deadline while queued", err)
	}

	// Once the drain completes (registration retired, lease released), the
	// successor admission succeeds and the session stays in this process.
	localExecutionRegistry.Lock()
	delete(localExecutionRegistry.entries, key)
	localExecutionRegistry.Unlock()
	owner.Release()
	guard, err := AcquireExecutionAdmission(t.Context(), sessionDir, "admission-drain", ExecutionAdmissionOptions{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("successor admission after drain: %v", err)
	}
	guard.Release()
}

func TestAcquireSessionMutationUnlessHeldSkipsHeldLease(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "mutation-unless-held")

	// An in-run lease held by this process is the authority: the helper must
	// not attempt a second acquisition (which would report a false busy).
	owner, err := session.AcquireExecutionAdmission(sessionDir, "mutation-unless-held")
	if err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireSessionMutationUnlessHeld(t.Context(), sessionDir, "mutation-unless-held", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("unless-held while a local lease is live: %v", err)
	}
	if guard != nil {
		guard.Release()
		t.Fatal("expected a nil guard while this process holds the lease")
	}
	owner.Release()

	// With no held lease the helper acquires the shared mutation lease and
	// reclaims the released tombstone row through the fenced epoch bump.
	guard, err = AcquireSessionMutationUnlessHeld(t.Context(), sessionDir, "mutation-unless-held", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("unless-held after release: %v", err)
	}
	if guard == nil {
		t.Fatal("expected an acquired mutation guard after the held lease was released")
	}
	if binding := guard.Binding(); binding.Purpose != session.RuntimeLeasePurposeMutation {
		t.Fatalf("mutation binding = %+v", binding)
	}
	guard.Release()
}

func TestAcquireSessionMutationRecoversOrphanRun(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "mutation-orphan")
	if err := (RunStore{SessionDir: sessionDir}).Create(DurableRun{
		ID: "stale-mutation", SessionID: "mutation-orphan", Source: "tui", Status: "running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireSessionMutation(t.Context(), sessionDir, "mutation-orphan", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("mutation admission with an orphan run: %v", err)
	}
	defer guard.Release()
	stale, err := session.GetSessionRun(sessionDir, "stale-mutation")
	if err != nil || stale == nil || stale.Status != "failed" {
		t.Fatalf("stale run = %#v, err=%v", stale, err)
	}
}

func TestAcquireSessionMutationGroupDedupesSortsAndReleases(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "group-a")
	initRecoveryTestSession(t, sessionDir, "group-b")

	group, err := AcquireSessionMutationGroup(t.Context(), sessionDir, []string{"group-b", "", "group-a", "group-a"}, ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("group acquisition: %v", err)
	}
	if group.Guard("group-a") == nil || group.Guard("group-b") == nil {
		t.Fatal("expected one guard per deduplicated session")
	}
	if group.Guard("missing") != nil {
		t.Fatal("unexpected guard for an unknown session")
	}
	if _, err := AcquireSessionMutation(t.Context(), sessionDir, "group-b", ExecutionAdmissionOptions{}); !errors.Is(err, session.ErrRuntimeLeaseBusy) {
		t.Fatalf("competing mutation while grouped = %v, want busy", err)
	}
	group.Release()
	group.Release() // idempotent

	again, err := AcquireSessionMutation(t.Context(), sessionDir, "group-b", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("mutation after group release: %v", err)
	}
	again.Release()
}

func TestAcquireSessionMutationGroupReleasesEarlierSessionsOnFailure(t *testing.T) {
	sessionDir := t.TempDir()
	initRecoveryTestSession(t, sessionDir, "group-ok")
	initRecoveryTestSession(t, sessionDir, "group-blocked")
	blocker, err := session.AcquireExecutionAdmission(sessionDir, "group-blocked")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()

	if _, err := AcquireSessionMutationGroup(t.Context(), sessionDir, []string{"group-ok", "group-blocked"}, ExecutionAdmissionOptions{}); !errors.Is(err, session.ErrRuntimeLeaseBusy) {
		t.Fatalf("group error = %v, want busy", err)
	}
	// The earlier (sorted-first) group-ok lease must have been released by the
	// failed group instead of stranding the session.
	guard, err := AcquireSessionMutation(t.Context(), sessionDir, "group-ok", ExecutionAdmissionOptions{})
	if err != nil {
		t.Fatalf("reacquire group-ok after failed group: %v", err)
	}
	guard.Release()
}
