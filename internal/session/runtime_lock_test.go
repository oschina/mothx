package session

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRuntimeLeaseSubprocessHelper is executed by the parent test in a
// separate OS process. The helper intentionally keeps the lease until it is
// killed, modelling a process crash without running any cleanup code.
func TestRuntimeLeaseSubprocessHelper(t *testing.T) {
	if os.Getenv("MOTHX_RUNTIME_LEASE_HELPER") != "1" {
		return
	}
	release, ok := TryLockRuntime(os.Getenv("MOTHX_RUNTIME_LEASE_DIR"), os.Getenv("MOTHX_RUNTIME_LEASE_SESSION"))
	if !ok {
		t.Fatal("helper could not acquire runtime lease")
	}
	defer release()
	_, _ = fmt.Fprintln(os.Stdout, "acquired")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestRuntimeLeaseSurvivesProcessFailureUntilExpiry(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("lease-process"); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRuntimeLeaseSubprocessHelper", "-test.v")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"MOTHX_RUNTIME_LEASE_HELPER=1",
		"MOTHX_RUNTIME_LEASE_DIR="+sessionDir,
		"MOTHX_RUNTIME_LEASE_SESSION=lease-process",
	)
	// The helper reads stdin until EOF, so keep a pipe open while it owns the
	// lease and close it only after the parent has observed acquisition.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	acquired := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "acquired" {
				acquired <- true
				return
			}
		}
		acquired <- false
	}()
	select {
	case ok := <-acquired:
		if !ok {
			t.Fatal("lease helper exited before acquiring")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for helper lease")
	}

	if release, ok := TryLockRuntime(sessionDir, "lease-process"); ok {
		release()
		t.Fatal("second process acquired an unexpired runtime lease")
	}
	mgrB := New(filepath.Join(t.TempDir(), "work-b"), sessionDir)
	if err := mgrB.InitWithID("lease-process-b"); err != nil {
		t.Fatal(err)
	}
	releaseB, ok := TryLockRuntime(sessionDir, "lease-process-b")
	if !ok {
		t.Fatal("session B was blocked by session A's lease")
	}
	releaseB()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`UPDATE session_runtime_leases SET expires_at = CAST(strftime('%s','now') AS INTEGER) - 1 WHERE session_id = ?`, "lease-process"); err != nil {
		t.Fatal(err)
	}
	release, ok := TryLockRuntime(sessionDir, "lease-process")
	if !ok {
		t.Fatal("expired runtime lease was not reclaimable")
	}
	var epoch int64
	if err := db.Bun().QueryRow(`SELECT epoch FROM session_runtime_leases WHERE session_id = ?`, "lease-process").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if epoch < 2 {
		t.Fatalf("reclaimed runtime lease epoch = %d, want fencing epoch >= 2", epoch)
	}
	release()
	var state string
	if err := db.Bun().QueryRow(`SELECT epoch, state FROM session_runtime_leases WHERE session_id = ?`, "lease-process").Scan(&epoch, &state); err != nil {
		t.Fatalf("released runtime lease tombstone missing: %v", err)
	}
	if state != "released" {
		t.Fatalf("released runtime lease state = %q, want released", state)
	}
}

func TestReleasedLeaseFencesDelayedOwnerWrite(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("lease-tombstone"); err != nil {
		t.Fatal(err)
	}
	oldLease, err := acquireRuntimeLease(sessionDir, "lease-tombstone", "run")
	if err != nil || oldLease == nil {
		t.Fatalf("acquire old lease = %v, lease=%v", err, oldLease)
	}
	oldLease.release()
	newLease, err := acquireRuntimeLease(sessionDir, "lease-tombstone", "run")
	if err != nil || newLease == nil {
		t.Fatalf("acquire new lease = %v, lease=%v", err, newLease)
	}
	newLease.release()

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = validateRuntimeLeaseTx(tx, sessionDir, "lease-tombstone")
	_ = tx.Rollback()
	if !errors.Is(err, ErrRuntimeLeaseLost) {
		t.Fatalf("delayed owner validation error = %v, want ErrRuntimeLeaseLost", err)
	}
	if _, err := SaveSessionRunEvent(sessionDir, SessionRunEvent{SessionID: "lease-tombstone", RunID: "stale-run", EventType: "late"}); !errors.Is(err, ErrRuntimeLeaseLost) {
		t.Fatalf("delayed run event error = %v, want ErrRuntimeLeaseLost", err)
	}
}

func TestAcquireExecutionAdmissionRequiresExistingIdleSession(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("admission-idle"); err != nil {
		t.Fatal(err)
	}

	guard, err := AcquireExecutionAdmission(sessionDir, "admission-idle")
	if err != nil {
		t.Fatalf("acquire admission: %v", err)
	}
	if binding := guard.Binding(); binding.Purpose != RuntimeLeasePurposeAdmission || binding.RunID != "" || binding.SessionID != "admission-idle" {
		t.Fatalf("admission binding = %+v", binding)
	}
	guard.Release()
	guard.Release()

	if _, err := AcquireExecutionAdmission(sessionDir, "missing-session"); !errors.Is(err, ErrRuntimeSessionNotFound) {
		t.Fatalf("missing-session admission error = %v, want ErrRuntimeSessionNotFound", err)
	}
}

func TestAcquireExecutionAdmissionRequiresRecoveryForActiveRun(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("admission-active"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-active", SessionID: "admission-active", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := AcquireExecutionAdmission(sessionDir, "admission-active"); !errors.Is(err, ErrSessionRecoveryRequired) {
		t.Fatalf("active-run admission error = %v, want ErrSessionRecoveryRequired", err)
	}
	if _, err := AcquireMutation(sessionDir, "admission-active"); !errors.Is(err, ErrSessionRunActive) {
		t.Fatalf("active-run mutation error = %v, want ErrSessionRunActive", err)
	}
	if _, err := AcquireFork(sessionDir, "admission-active"); !errors.Is(err, ErrSessionRunActive) {
		t.Fatalf("active-run fork error = %v, want ErrSessionRunActive", err)
	}
}

func TestAcquireRecoveryBindsExpectedActiveRun(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("recovery-bind"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-recovery", SessionID: "recovery-bind", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := AcquireRecovery(sessionDir, "recovery-bind", "other-run"); !errors.Is(err, ErrRuntimeLeaseRunMismatch) {
		t.Fatalf("mismatched recovery error = %v, want ErrRuntimeLeaseRunMismatch", err)
	}
	guard, err := AcquireRecovery(sessionDir, "recovery-bind", "run-recovery")
	if err != nil {
		t.Fatalf("acquire recovery: %v", err)
	}
	defer guard.Release()
	binding := guard.Binding()
	if binding.Purpose != RuntimeLeasePurposeRecovery || binding.RunID != "run-recovery" || binding.Epoch != 1 {
		t.Fatalf("recovery binding = %+v", binding)
	}
	facts, err := ReadSessionExecutionFacts(sessionDir, "recovery-bind")
	if err != nil {
		t.Fatal(err)
	}
	if facts.Lease == nil || !facts.Lease.Valid || facts.Lease.RunID != "run-recovery" || facts.Lease.Purpose != RuntimeLeasePurposeRecovery {
		t.Fatalf("recovery lease facts = %+v", facts.Lease)
	}
}

func TestAcquireRecoveryRequiresActiveRun(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("recovery-idle"); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireRecovery(sessionDir, "recovery-idle", "run-missing"); !errors.Is(err, ErrSessionRecoveryNotNeeded) {
		t.Fatalf("idle recovery error = %v, want ErrSessionRecoveryNotNeeded", err)
	}
}

func TestRunAdmissionRejectsMutationLeaseWithoutPartialRun(t *testing.T) {
	sessionDir := t.TempDir()
	mgr := New(filepath.Join(t.TempDir(), "work"), sessionDir)
	if err := mgr.InitWithID("mutation-cannot-run"); err != nil {
		t.Fatal(err)
	}
	guard, err := AcquireMutation(sessionDir, "mutation-cannot-run")
	if err != nil {
		t.Fatalf("acquire mutation lease: %v", err)
	}
	defer guard.Release()
	now := time.Now()
	err = CreateSessionRun(sessionDir, SessionRun{
		ID: "run-not-created", SessionID: "mutation-cannot-run", Status: "running", StartedAt: now, UpdatedAt: now,
	})
	if !errors.Is(err, ErrRuntimeLeasePurpose) {
		t.Fatalf("create run with mutation lease error = %v, want ErrRuntimeLeasePurpose", err)
	}
	run, err := GetSessionRun(sessionDir, "run-not-created")
	if err != nil {
		t.Fatal(err)
	}
	if run != nil {
		t.Fatalf("run persisted despite failed lease binding: %+v", run)
	}
	binding := guard.Binding()
	if binding.Purpose != RuntimeLeasePurposeMutation || binding.RunID != "" {
		t.Fatalf("mutation binding changed after rolled-back run: %+v", binding)
	}
}

func TestAcquireMutationsReleasesEarlierSessionsOnConflict(t *testing.T) {
	sessionDir := t.TempDir()
	for _, id := range []string{"mutation-a", "mutation-b"} {
		mgr := New(filepath.Join(t.TempDir(), id), sessionDir)
		if err := mgr.InitWithID(id); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := SaveSessionRun(sessionDir, SessionRun{
		ID: "run-b", SessionID: "mutation-b", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireMutations(sessionDir, []string{"mutation-b", "mutation-a"}); !errors.Is(err, ErrSessionRunActive) {
		t.Fatalf("multi-mutation error = %v, want ErrSessionRunActive", err)
	}
	guard, err := AcquireMutation(sessionDir, "mutation-a")
	if err != nil {
		t.Fatalf("first mutation lease remained held after rollback: %v", err)
	}
	guard.Release()
}

// leaseHeartbeatAt reads the persisted heartbeat timestamp of one lease row.
func leaseHeartbeatAt(t *testing.T, sessionDir, sessionID string) int64 {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var heartbeat int64
	if err := db.Bun().QueryRow("SELECT heartbeat_at FROM session_runtime_leases WHERE session_id = ?", sessionID).Scan(&heartbeat); err != nil {
		t.Fatal(err)
	}
	return heartbeat
}

// TestLeaseHeartbeatSchedulerBatchRenewDisplaceAndRetire pins the coalesced
// heartbeat contract in one timeline: a single per-directory scheduler renews
// every lease of that directory (both rows advance within one tick), a lease
// displaced by an epoch bump is the only one marked lost, the surviving lease
// keeps being renewed, and the scheduler retires once the last lease is
// released.
func TestLeaseHeartbeatSchedulerBatchRenewDisplaceAndRetire(t *testing.T) {
	sessionDir := t.TempDir()
	first := New(filepath.Join(t.TempDir(), "work-a"), sessionDir)
	if err := first.InitWithID("hb-batch-1"); err != nil {
		t.Fatal(err)
	}
	second := New(filepath.Join(t.TempDir(), "work-b"), sessionDir)
	if err := second.InitWithID("hb-batch-2"); err != nil {
		t.Fatal(err)
	}

	guardA, err := AcquireExecutionAdmission(sessionDir, "hb-batch-1")
	if err != nil {
		t.Fatalf("acquire lease A: %v", err)
	}
	defer guardA.Release()
	guardB, err := AcquireExecutionAdmission(sessionDir, "hb-batch-2")
	if err != nil {
		t.Fatalf("acquire lease B: %v", err)
	}
	defer guardB.Release()

	dirKey := leaseDirKey(sessionDir)
	leaseHeartbeatSchedulers.Lock()
	_, scheduled := leaseHeartbeatSchedulers.schedulers[dirKey]
	leaseHeartbeatSchedulers.Unlock()
	if !scheduled {
		t.Fatal("no heartbeat scheduler was started for the directory")
	}

	// Simulate a competing process taking over lease B: bumping the epoch is
	// the only takeover path, and the fenced renewal must stop matching B
	// while A stays renewable.
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec("UPDATE session_runtime_leases SET epoch = epoch + 1 WHERE session_id = ?", "hb-batch-2"); err != nil {
		t.Fatalf("displace lease B: %v", err)
	}

	beforeA := leaseHeartbeatAt(t, sessionDir, "hb-batch-1")
	deadline := time.Now().Add(2*runtimeHeartbeatEvery + 2*time.Second)
	renewed := false
	for time.Now().Before(deadline) && !renewed {
		select {
		case <-guardB.Lost():
		default:
			time.Sleep(100 * time.Millisecond)
			continue
		}
		// Lease B was marked lost; the same coalesced tick must have renewed A.
		if leaseHeartbeatAt(t, sessionDir, "hb-batch-1") <= beforeA {
			t.Fatal("surviving lease A was not renewed by the batch that detected B's displacement")
		}
		renewed = true
	}
	if !renewed {
		t.Fatal("displaced lease B was not marked lost within two heartbeat intervals")
	}

	guardA.Release()
	guardB.Release()
	retired := false
	deadline = time.Now().Add(2*runtimeHeartbeatEvery + 2*time.Second)
	for time.Now().Before(deadline) && !retired {
		leaseHeartbeatSchedulers.Lock()
		_, exists := leaseHeartbeatSchedulers.schedulers[dirKey]
		leaseHeartbeatSchedulers.Unlock()
		retired = !exists
		if !retired {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !retired {
		t.Fatal("heartbeat scheduler was not retired after the last lease was released")
	}
}
