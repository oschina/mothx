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
