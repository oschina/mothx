package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	database "github.com/startvibecoding/mothx/internal/db"
	"github.com/startvibecoding/mothx/internal/provider"
)

// This file implements the load shapes of
// docs/proposal/sqlite-write-pressure-reduction-proposal.md §4.2 on top of
// the existing subprocess-helper pattern (see sqlite_robustness_test.go):
//
//   - Shape A: several writer processes on distinct sessions of one shared
//     session directory (the Serve+TUI+CLI shape), reporting contention
//     metrics for before/after comparisons.
//   - Shape B: two writer processes with mixed synchronous settings
//     (MOTHX_SQLITE_SYNCHRONOUS=FULL next to the NORMAL default), the
//     desktop-vendored-runtime version-skew shape.
//   - Shape C: one process with many concurrent sessions and leases (the
//     Serve-with-N-sessions shape), pinning the coalesced heartbeat and
//     batched entry writes at scale.
//
// Shape D (two-process same-session admission competition, kill -9 owner and
// bounded recovery convergence) is covered by the existing suites instead of
// being duplicated here: internal/agentruntime/process_integration_test.go
// (one winner, one duplicate/busy), TestSQLiteTwoProcessesCompetingForSameSession,
// TestRuntimeLeaseSurvivesProcessFailureUntilExpiry, and
// TestLeaseHeartbeatSchedulerBatchRenewDisplaceAndRetire.

const writePressureHelperEnv = "MOTHX_WRITE_PRESSURE_HELPER"

// writePressureMetrics is the JSON report every load helper process leaves
// for the parent test.
type writePressureMetrics struct {
	PID              int    `json:"pid"`
	Sessions         int    `json:"sessions"`
	Entries          int    `json:"entries"`
	ElapsedMs        int64  `json:"elapsedMs"`
	BusyRetryHits    uint64 `json:"busyRetryHits"`
	BusyRetryWaitMs  int64  `json:"busyRetryWaitMs"`
	BeginCount       uint64 `json:"beginCount"`
	BeginTotalWaitMs int64  `json:"beginTotalWaitMs"`
	BeginMaxWaitMs   int64  `json:"beginMaxWaitMs"`
}

func TestWritePressureProcessHelper(t *testing.T) {
	if os.Getenv(writePressureHelperEnv) != "1" {
		return
	}
	switch os.Getenv("MOTHX_WP_MODE") {
	case "write-load":
		runWriteLoadProcess(t)
	default:
		t.Fatalf("unknown write pressure helper mode %q", os.Getenv("MOTHX_WP_MODE"))
	}
}

// runWriteLoadProcess writes messages to S distinct sessions in batches of B
// through the production AppendMessages path, then reports the process-wide
// contention metrics. The parent controls the durability mix by passing
// MOTHX_SQLITE_SYNCHRONOUS into this environment.
func runWriteLoadProcess(t *testing.T) {
	sessionDir := os.Getenv("MOTHX_WP_SESSION_DIR")
	idPrefix := os.Getenv("MOTHX_WP_ID_PREFIX")
	sessions, err := strconv.Atoi(os.Getenv("MOTHX_WP_SESSIONS"))
	if err != nil {
		t.Fatal(err)
	}
	messages, err := strconv.Atoi(os.Getenv("MOTHX_WP_MESSAGES"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := strconv.Atoi(os.Getenv("MOTHX_WP_BATCH"))
	if err != nil || batch < 1 {
		t.Fatalf("invalid MOTHX_WP_BATCH: %v", err)
	}

	started := time.Now()
	entries := 0
	for s := 0; s < sessions; s++ {
		m := New("/tmp/write-pressure", sessionDir)
		sessionID := fmt.Sprintf("%s-%d", idPrefix, s)
		if err := m.InitWithID(sessionID); err != nil {
			t.Fatal(err)
		}
		for start := 0; start < messages; start += batch {
			end := min(start+batch, messages)
			bulk := make([]provider.Message, 0, end-start)
			for i := start; i < end; i++ {
				bulk = append(bulk, provider.NewUserMessage(fmt.Sprintf("%s-message-%d", sessionID, i)))
			}
			ids, err := m.AppendMessages(bulk)
			if err != nil {
				t.Fatal(err)
			}
			entries += len(ids)
		}
	}

	hits, retryWait := database.BusyRetryStats()
	beginCount, beginTotal, beginMax := database.BeginWaitStats()
	encoded, err := json.Marshal(writePressureMetrics{
		PID: os.Getpid(), Sessions: sessions, Entries: entries,
		ElapsedMs:        time.Since(started).Milliseconds(),
		BusyRetryHits:    hits,
		BusyRetryWaitMs:  retryWait.Milliseconds(),
		BeginCount:       beginCount,
		BeginTotalWaitMs: beginTotal.Milliseconds(),
		BeginMaxWaitMs:   beginMax.Milliseconds(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("MOTHX_WP_METRICS_FILE"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}

type writePressureProcess struct {
	cmd         *exec.Cmd
	output      *bytes.Buffer
	metricsFile string
}

func startWritePressureProcess(t *testing.T, env ...string) writePressureProcess {
	t.Helper()
	metricsFile := filepath.Join(t.TempDir(), "metrics.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWritePressureProcessHelper$")
	cmd.Env = append(os.Environ(), append([]string{
		writePressureHelperEnv + "=1",
		"MOTHX_WP_METRICS_FILE=" + metricsFile,
	}, env...)...)
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return writePressureProcess{cmd: cmd, output: output, metricsFile: metricsFile}
}

func waitWritePressureProcess(t *testing.T, process writePressureProcess) writePressureMetrics {
	t.Helper()
	if err := process.cmd.Wait(); err != nil {
		t.Fatalf("write pressure helper failed: %v\n%s", err, process.output.String())
	}
	encoded, err := os.ReadFile(process.metricsFile)
	if err != nil {
		t.Fatalf("read metrics file: %v\nhelper output:\n%s", err, process.output.String())
	}
	var metrics writePressureMetrics
	if err := json.Unmarshal(encoded, &metrics); err != nil {
		t.Fatalf("decode metrics %q: %v", encoded, err)
	}
	return metrics
}

// writePressureScale returns the workload multiplier. The default keeps the
// shapes fast enough for the regular test run; setting
// MOTHX_WRITE_PRESSURE_SCALE (for example "8") enlarges the workload for the
// baseline/benchmark runs described in the proposal.
func writePressureScale(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("MOTHX_WRITE_PRESSURE_SCALE")
	if raw == "" {
		return 1
	}
	scale, err := strconv.Atoi(raw)
	if err != nil || scale < 1 {
		t.Fatalf("invalid MOTHX_WRITE_PRESSURE_SCALE %q", raw)
	}
	return scale
}

func logWritePressureMetrics(t *testing.T, shape string, all ...writePressureMetrics) {
	t.Helper()
	var totalEntries int
	var wallMs int64
	var busyHits uint64
	var beginMaxMs int64
	for _, m := range all {
		totalEntries += m.Entries
		if m.ElapsedMs > wallMs {
			wallMs = m.ElapsedMs
		}
		busyHits += m.BusyRetryHits
		if m.BeginMaxWaitMs > beginMaxMs {
			beginMaxMs = m.BeginMaxWaitMs
		}
		t.Logf("[shape %s] pid=%d entries=%d elapsed=%dms busyHits=%d busyWaitMs=%d begins=%d beginTotalMs=%d beginMaxMs=%d",
			shape, m.PID, m.Entries, m.ElapsedMs, m.BusyRetryHits, m.BusyRetryWaitMs, m.BeginCount, m.BeginTotalWaitMs, m.BeginMaxWaitMs)
	}
	t.Logf("[shape %s] aggregate entries=%d wallMs=%d busyHits=%d beginMaxMs=%d", shape, totalEntries, wallMs, busyHits, beginMaxMs)
}

func writeLoadEnv(sessionDir, idPrefix string, sessions, messages, batch int) []string {
	return []string{
		"MOTHX_WP_MODE=write-load",
		"MOTHX_WP_SESSION_DIR=" + sessionDir,
		"MOTHX_WP_ID_PREFIX=" + idPrefix,
		fmt.Sprintf("MOTHX_WP_SESSIONS=%d", sessions),
		fmt.Sprintf("MOTHX_WP_MESSAGES=%d", messages),
		fmt.Sprintf("MOTHX_WP_BATCH=%d", batch),
	}
}

// assertWriteLoadRows verifies the sessions and entries one helper prefix
// must have produced: every session keeps its header entry plus all messages.
func assertWriteLoadRows(t *testing.T, sessionDir, idPrefix string, sessions, messages int) {
	t.Helper()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	for s := 0; s < sessions; s++ {
		sessionID := fmt.Sprintf("%s-%d", idPrefix, s)
		var count int
		if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ?", sessionID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if want := messages + 1; count != want { // header plus messages
			t.Fatalf("entries for %s = %d, want %d", sessionID, count, want)
		}
	}
}

// TestWritePressureShapeAMultiProcessWriters runs proposal shape A: several
// writer processes on distinct sessions of one shared directory. Every write
// must succeed (the busy-retry policy absorbs lock contention), every row
// must be present, and the database must stay integrity-clean; the
// contention metrics are logged for before/after comparisons.
func TestWritePressureShapeAMultiProcessWriters(t *testing.T) {
	sessionDir := t.TempDir()
	scale := writePressureScale(t)
	const (
		processes          = 3
		sessionsPerProcess = 2
		batch              = 8
	)
	messagesPerSession := 24 * scale

	launched := make([]writePressureProcess, 0, processes)
	for i := 0; i < processes; i++ {
		launched = append(launched, startWritePressureProcess(t,
			writeLoadEnv(sessionDir, fmt.Sprintf("shapeA-p%d", i), sessionsPerProcess, messagesPerSession, batch)...))
	}
	all := make([]writePressureMetrics, 0, processes)
	for _, process := range launched {
		metrics := waitWritePressureProcess(t, process)
		if metrics.Entries != sessionsPerProcess*messagesPerSession {
			t.Fatalf("helper pid=%d entries = %d, want %d", metrics.PID, metrics.Entries, sessionsPerProcess*messagesPerSession)
		}
		all = append(all, metrics)
	}
	logWritePressureMetrics(t, "A", all...)

	for i := 0; i < processes; i++ {
		assertWriteLoadRows(t, sessionDir, fmt.Sprintf("shapeA-p%d", i), sessionsPerProcess, messagesPerSession)
	}
	assertDatabaseIntegrity(t, filepath.Join(sessionDir, "sessions.db"))
}

// TestWritePressureShapeBMixedSynchronousModes runs proposal shape B: two
// writer processes share one database while one runs with
// MOTHX_SQLITE_SYNCHRONOUS=FULL (the desktop vendored-runtime version-skew
// shape). synchronous is a per-connection setting, so both processes must
// write successfully into one consistent database.
func TestWritePressureShapeBMixedSynchronousModes(t *testing.T) {
	sessionDir := t.TempDir()
	scale := writePressureScale(t)
	const (
		sessionsPerProcess = 1
		batch              = 8
	)
	messagesPerSession := 24 * scale

	full := startWritePressureProcess(t, append(writeLoadEnv(sessionDir, "shapeB-full", sessionsPerProcess, messagesPerSession, batch),
		"MOTHX_SQLITE_SYNCHRONOUS=FULL")...)
	normal := startWritePressureProcess(t, writeLoadEnv(sessionDir, "shapeB-normal", sessionsPerProcess, messagesPerSession, batch)...)

	fullMetrics := waitWritePressureProcess(t, full)
	normalMetrics := waitWritePressureProcess(t, normal)
	logWritePressureMetrics(t, "B", fullMetrics, normalMetrics)

	assertWriteLoadRows(t, sessionDir, "shapeB-full", sessionsPerProcess, messagesPerSession)
	assertWriteLoadRows(t, sessionDir, "shapeB-normal", sessionsPerProcess, messagesPerSession)
	assertDatabaseIntegrity(t, filepath.Join(sessionDir, "sessions.db"))
}

// TestWritePressureShapeCSingleProcessManySessions runs proposal shape C: one
// process with many concurrent sessions that all hold leases (the
// Serve-with-N-sessions shape). It pins the phase-3 coalescing claim at
// scale: exactly one heartbeat scheduler for the directory, and one coalesced
// tick renews every lease while the batched entry writes keep every session's
// chain intact.
func TestWritePressureShapeCSingleProcessManySessions(t *testing.T) {
	sessionDir := t.TempDir()
	scale := writePressureScale(t)
	sessionCount := 4 * scale
	messages := 16 * scale

	managers := make([]*Manager, sessionCount)
	guards := make([]*RuntimeLeaseGuard, sessionCount)
	for i := 0; i < sessionCount; i++ {
		managers[i] = New(filepath.Join(t.TempDir(), fmt.Sprintf("work-%d", i)), sessionDir)
		if err := managers[i].InitWithID(fmt.Sprintf("shapeC-%d", i)); err != nil {
			t.Fatal(err)
		}
		guard, err := AcquireExecutionAdmission(sessionDir, fmt.Sprintf("shapeC-%d", i))
		if err != nil {
			t.Fatalf("acquire lease %d: %v", i, err)
		}
		guards[i] = guard
	}
	defer func() {
		for _, guard := range guards {
			guard.Release()
		}
	}()

	dirKey := leaseDirKey(sessionDir)
	leaseHeartbeatSchedulers.Lock()
	_, scheduled := leaseHeartbeatSchedulers.schedulers[dirKey]
	leaseHeartbeatSchedulers.Unlock()
	if !scheduled {
		t.Fatal("no heartbeat scheduler was started for the directory")
	}

	before := make([]int64, sessionCount)
	for i := 0; i < sessionCount; i++ {
		before[i] = leaseHeartbeatAt(t, sessionDir, fmt.Sprintf("shapeC-%d", i))
		bulk := make([]provider.Message, messages)
		for j := range bulk {
			bulk[j] = provider.NewUserMessage(fmt.Sprintf("shapeC-%d-message-%d", i, j))
		}
		if _, err := managers[i].AppendMessages(bulk); err != nil {
			t.Fatalf("batch append session %d: %v", i, err)
		}
	}

	// One coalesced tick must renew every lease of the directory.
	deadline := time.Now().Add(2*runtimeHeartbeatEvery + 2*time.Second)
	for time.Now().Before(deadline) {
		renewed := 0
		for i := 0; i < sessionCount; i++ {
			if leaseHeartbeatAt(t, sessionDir, fmt.Sprintf("shapeC-%d", i)) > before[i] {
				renewed++
			}
		}
		if renewed == sessionCount {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for i := 0; i < sessionCount; i++ {
		if leaseHeartbeatAt(t, sessionDir, fmt.Sprintf("shapeC-%d", i)) <= before[i] {
			t.Fatalf("lease shapeC-%d was not renewed by the coalesced heartbeat", i)
		}
		select {
		case <-guards[i].Lost():
			t.Fatalf("lease shapeC-%d was marked lost while its owner is alive", i)
		default:
		}
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if want := sessionCount * (messages + 1); count != want { // header plus messages per session
		t.Fatalf("entries = %d, want %d", count, want)
	}
	assertDatabaseIntegrity(t, filepath.Join(sessionDir, "sessions.db"))
}
