package session

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRuntimeLeaseBusConvergenceHelper is the subprocess peer for the
// ownership-snapshot convergence test. It subscribes (which starts the bus and
// its snapshot loop), registers a synthetic execution lease, and then stays
// alive. Because no directed event is ever published for the synthetic lease,
// the periodic anti-entropy snapshot is the only way a peer can learn about it.
func TestRuntimeLeaseBusConvergenceHelper(t *testing.T) {
	if os.Getenv("MOTHX_RUNTIME_BUS_CONVERGENCE_HELPER") != "1" {
		return
	}
	dir := os.Getenv("MOTHX_RUNTIME_BUS_HELPER_DIR")
	sessionID := os.Getenv("MOTHX_RUNTIME_BUS_HELPER_SESSION")
	if dir == "" || sessionID == "" {
		fmt.Println("missing-helper-env")
		return
	}
	unsubscribe := SubscribeRuntimeLeaseNotifications(func(RuntimeLeaseNotification) {})
	defer unsubscribe()

	// The snapshot loop announces immediately on start, before the lease below
	// is registered; the peer can therefore only converge via a later periodic
	// tick, which is exactly the anti-entropy path under test.
	activeRuntimeLeases.Lock()
	activeRuntimeLeases.leases[runtimeLockKey(dir, sessionID)] = &runtimeLease{
		sessionDir: dir,
		sessionID:  sessionID,
		ownerID:    "peer-owner",
		purpose:    string(RuntimeLeasePurposeExecution),
		runID:      "run-snapshot",
		epoch:      7,
		expiresAt:  time.Now().Add(time.Minute),
	}
	activeRuntimeLeases.Unlock()
	fmt.Println("registered")
	select {}
}

// TestRuntimeLeaseBusOwnershipSnapshotConvergesAcrossProcesses proves that the
// process-wide ownership view converges from the periodic `ownership_snapshot`
// alone: a peer process holds a lease but never sends a directed acquire event,
// and this process learns about it purely through anti-entropy.
func TestRuntimeLeaseBusOwnershipSnapshotConvergesAcrossProcesses(t *testing.T) {
	port := pickConvergenceBusPort(t)
	t.Setenv("MOTHX_RUNTIME_BUS_PORT", strconv.Itoa(port))
	t.Setenv("MOTHX_RUNTIME_SNAPSHOT_INTERVAL", "150ms")

	dir := t.TempDir()
	sessionID := "sess-converge-" + strconv.Itoa(os.Getpid())
	identity := runtimeDatabaseIdentity(dir)
	if lookup := LookupRuntimeOwnership(identity, sessionID); lookup.Present {
		t.Fatalf("unexpected pre-existing ownership entry: %+v", lookup.Entry)
	}

	before := runtimeLeaseMetrics.received.Load()
	unsubscribe := SubscribeRuntimeLeaseNotifications(func(RuntimeLeaseNotification) {})
	defer unsubscribe()
	if !waitForRuntimeLeaseBusListener(2 * time.Second) {
		t.Fatal("parent lease bus listener did not start")
	}

	helper := startRuntimeLeaseBusConvergenceHelper(t,
		"MOTHX_RUNTIME_BUS_HELPER_DIR="+dir,
		"MOTHX_RUNTIME_BUS_HELPER_SESSION="+sessionID,
	)
	defer helper.stop()
	if line := helper.nextLine(t); line != "registered" {
		t.Fatalf("unexpected helper line %q", line)
	}

	deadline := time.Now().Add(5 * time.Second)
	var entry RuntimeOwnershipEntry
	for time.Now().Before(deadline) {
		lookup := LookupRuntimeOwnership(identity, sessionID)
		if lookup.Present && lookup.Fresh && !lookup.Uncertain {
			entry = lookup.Entry
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if entry.SessionID == "" {
		t.Fatal("ownership view did not converge from the periodic ownership_snapshot")
	}
	if entry.Purpose != string(RuntimeLeasePurposeExecution) || entry.RunStatus != "running" || entry.Epoch != 7 {
		t.Fatalf("unexpected converged entry: %+v", entry)
	}
	if entry.OwnerPID == os.Getpid() {
		t.Fatalf("converged entry must come from the peer process, got pid %d", entry.OwnerPID)
	}
	if got := runtimeLeaseMetrics.received.Load(); got <= before {
		t.Fatalf("expected received counter to grow, before=%d after=%d", before, got)
	}
}

func startRuntimeLeaseBusConvergenceHelper(t *testing.T, extraEnv ...string) *runtimeLeaseBusHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestRuntimeLeaseBusConvergenceHelper")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "MOTHX_RUNTIME_BUS_CONVERGENCE_HELPER=1")
	cmd.Env = append(cmd.Env, extraEnv...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- strings.TrimSpace(scanner.Text())
		}
		close(lines)
	}()
	return &runtimeLeaseBusHelper{cmd: cmd, lines: lines}
}

func pickConvergenceBusPort(t *testing.T) int {
	t.Helper()
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()
	return port
}
