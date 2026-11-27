package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// holdStartupCheck makes every startup integrity check block until the returned
// release function is called or the check is cancelled. Each check that starts
// reports on entered, so a test can prove a check is in flight. The check is
// also released when the test ends, so a failed assertion never leaves one
// blocked.
func holdStartupCheck(t *testing.T, entered chan<- struct{}) (release func()) {
	t.Helper()
	unblock := make(chan struct{})
	previous := startupCheckGate
	startupCheckGate = func(ctx context.Context) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var once sync.Once
	release = func() { once.Do(func() { close(unblock) }) }
	t.Cleanup(func() {
		release()
		startupCheckGate = previous
	})
	return release
}

// waitForStartupCheckIdle blocks until no startup check of path is running and
// its outcome has been counted.
func waitForStartupCheckIdle(t *testing.T, path string) {
	t.Helper()
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		startupChecks.Lock()
		_, running := startupChecks.byPath[canonical]
		startupChecks.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup check of %s did not finish", canonical)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func startupCheckRunning(t *testing.T, path string) bool {
	t.Helper()
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	startupChecks.Lock()
	defer startupChecks.Unlock()
	_, running := startupChecks.byPath[canonical]
	return running
}

// waitForSignal returns the next value of ch, failing the test if none arrives.
func waitForSignal[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func assertNoSignal[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("unexpected %s", what)
	default:
	}
}

func quickCheckBudgetForTest(t *testing.T, budget time.Duration) {
	t.Helper()
	previous := startupCheckWait
	startupCheckWait = budget
	t.Cleanup(func() { startupCheckWait = previous })
}

func brokenTablePageDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := Open(path, staleIndexSchema())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO entries (session_id, type) VALUES ('session-1', 'message')`); err != nil {
		t.Fatal(err)
	}
	var rootPage, pageSize int
	if err := db.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'entries'`).Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Break the table b-tree page type, leaving the index mechanically valid.
	contents[(rootPage-1)*pageSize] = 0xf5
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

// indexVisibleRows scans the fixture index the way a real query would and
// counts the rows it finds. COUNT(*) is no use here: SQLite answers it from
// the table whatever INDEXED BY says, even through a subquery.
func indexVisibleRows(t *testing.T, q *sql.DB) int {
	t.Helper()
	rows, err := q.Query(`SELECT rowid, session_id, type FROM entries INDEXED BY idx_entries_session_type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestStartupQuickCheckModeValues(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  StartupCheckMode
	}{
		{"", StartupCheckBounded},
		{"on", StartupCheckBounded},
		{"unexpected", StartupCheckBounded},
		{"sync", StartupCheckSync},
		{"ASYNC", StartupCheckAsync},
		{"off", StartupCheckOff},
		{"0", StartupCheckOff},
		{"disabled", StartupCheckOff},
	} {
		t.Setenv("MOTHX_SQLITE_QUICK_CHECK", tc.value)
		if got := StartupQuickCheckMode(); got != tc.want {
			t.Fatalf("StartupQuickCheckMode() with %q = %v, want %v", tc.value, got, tc.want)
		}
		if want := tc.want != StartupCheckOff; StartupQuickCheckEnabled() != want {
			t.Fatalf("StartupQuickCheckEnabled() with %q = %v, want %v", tc.value, !want, want)
		}
	}
}

// TestOpenAppliesCheckFinishedWithinBudget pins the bounded mode for a database
// whose check is fast enough: the verdict is applied before the open returns,
// exactly like a synchronous check, and nothing is reported a second time.
func TestOpenAppliesCheckFinishedWithinBudget(t *testing.T) {
	TakeIndexRepairs()
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()

	path := filepath.Join(t.TempDir(), "sessions.db")
	staleIndexDatabase(t, path)
	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)

	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, nil)
		opened <- err
	}()
	waitForSignal(t, entered, "the startup check to start")
	release()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open did not return after the check was released")
	}
	defer CloseAll()

	// Open returns the cached handle, so the repair can be inspected through it.
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count := indexVisibleRows(t, db.DB); count != 2 {
		t.Fatalf("rows visible through the index before Open returned = %d, want 2: the repair must land inside the budget", count)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("quick_check after Open = %q, want ok", integrity)
	}
	if repairs := TakeIndexRepairs(); len(repairs) != 1 {
		t.Fatalf("index repairs = %#v, want exactly one", repairs)
	}
	waitForStartupCheckIdle(t, path)
	assertNoSignal(t, findings, "finding for a check that finished inside the budget")
	if failures := TakeIntegrityFailures(); len(failures) != 0 {
		t.Fatalf("a repaired database was reported as a failure: %#v", failures)
	}
}

// TestOpenHandsSlowCheckToBackground proves the handoff: the open returns once
// the budget elapses, the database is usable at once, and a healthy verdict
// reports nothing.
func TestOpenHandsSlowCheckToBackground(t *testing.T) {
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()
	quickCheckBudgetForTest(t, 40*time.Millisecond)

	path := filepath.Join(t.TempDir(), "healthy.db")
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS probe (value TEXT NOT NULL)`)
		return err
	}
	if _, err := Open(path, migrate); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)
	before := startupCheckCounters.handedOff.Load()

	started := time.Now()
	db, err := Open(path, nil)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer CloseAll()
	if elapsed > 2*time.Second {
		t.Fatalf("Open waited %v for a check that exceeds its budget", elapsed)
	}
	waitForSignal(t, entered, "the startup check to start")
	if !startupCheckRunning(t, path) {
		t.Fatal("the unfinished check was not left running in the background")
	}
	if handedOff := startupCheckCounters.handedOff.Load(); handedOff != before+1 {
		t.Fatalf("handed-off checks = %d, want %d", handedOff, before+1)
	}

	// The database serves queries while its check is still held.
	if _, err := db.Exec(`INSERT INTO probe (value) VALUES ('during-check')`); err != nil {
		t.Fatalf("write while the check is running: %v", err)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM probe`).Scan(&value); err != nil || value != "during-check" {
		t.Fatalf("read while the check is running = %q, %v", value, err)
	}

	release()
	waitForStartupCheckIdle(t, path)
	assertNoSignal(t, findings, "finding for a healthy database")
	if failures := TakeIntegrityFailures(); len(failures) != 0 {
		t.Fatalf("a healthy database was reported as a failure: %#v", failures)
	}
}

// TestBackgroundCheckRepairsStaleIndexAndSignals covers the repair signal: a
// stale index found after the open returns is rebuilt in the background and
// pushed to the subscribers, while the existing repair record still reports it.
func TestBackgroundCheckRepairsStaleIndexAndSignals(t *testing.T) {
	TakeIndexRepairs()
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()
	quickCheckBudgetForTest(t, 40*time.Millisecond)

	path := filepath.Join(t.TempDir(), "sessions.db")
	staleIndexDatabase(t, path)
	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)

	db, err := Open(path, staleIndexSchema())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer CloseAll()
	waitForSignal(t, entered, "the startup check to start")
	if repairs := TakeIndexRepairs(); len(repairs) != 0 {
		t.Fatalf("index repaired while its check was still held: %#v", repairs)
	}
	if count := indexVisibleRows(t, db.DB); count != 1 {
		t.Fatalf("rows visible through the stale index = %d, want 1: the index must stay stale until the check repairs it", count)
	}

	release()
	finding := waitForSignal(t, findings, "the index-repaired finding")
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if finding.Kind != IntegrityIndexRepaired || finding.Path != canonical || finding.At.IsZero() {
		t.Fatalf("finding = %#v, want an index-repaired finding for %s", finding, canonical)
	}
	if !strings.Contains(finding.Describe(), "wrong # of entries in index") {
		t.Fatalf("Describe() = %q, want it to name the condition that was repaired", finding.Describe())
	}
	repairs := TakeIndexRepairs()
	if len(repairs) != 1 || repairs[0].Path != canonical {
		t.Fatalf("index repairs = %#v, want exactly one for %s", repairs, canonical)
	}
	if failures := TakeIntegrityFailures(); len(failures) != 0 {
		t.Fatalf("a repaired index was reported as a failure: %#v", failures)
	}
	if count := indexVisibleRows(t, db.DB); count != 2 {
		t.Fatalf("rows visible through the index after the repair = %d, want 2", count)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("quick_check after the repair = %q, want ok", integrity)
	}
}

// TestBackgroundCheckReportsTableDamage covers the other signal: structural
// damage found after the open returns is never repaired, only reported, and
// the synchronous mode still refuses the very same database.
func TestBackgroundCheckReportsTableDamage(t *testing.T) {
	TakeIndexRepairs()
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()
	quickCheckBudgetForTest(t, 40*time.Millisecond)

	path := filepath.Join(t.TempDir(), "sessions.db")
	brokenTablePageDatabase(t, path)
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)
	if _, err := Open(path, nil); err != nil {
		t.Fatalf("Open with the check handed off: %v", err)
	}
	waitForSignal(t, entered, "the startup check to start")
	release()
	finding := waitForSignal(t, findings, "the corruption finding")
	if finding.Kind != IntegrityCorrupt || finding.Path != canonical || finding.Report == "" {
		t.Fatalf("finding = %#v, want a corruption finding for %s", finding, canonical)
	}
	if !IsIntegrityError(finding.Err) {
		t.Fatalf("finding error = %v, want an *IntegrityError", finding.Err)
	}
	failures := TakeIntegrityFailures()
	if len(failures) != 1 || failures[0].Kind != IntegrityCorrupt {
		t.Fatalf("drained failures = %#v, want exactly one corruption finding", failures)
	}
	if repairs := TakeIndexRepairs(); len(repairs) != 0 {
		t.Fatalf("table damage was reported as an index repair: %#v", repairs)
	}
	_ = CloseAll()

	// The same damage is still a hard failure when the check runs to completion.
	t.Setenv("MOTHX_SQLITE_QUICK_CHECK", "sync")
	entered = make(chan struct{}, 4)
	release = holdStartupCheck(t, entered)
	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, nil)
		opened <- err
	}()
	waitForSignal(t, entered, "the synchronous check to start")
	select {
	case err := <-opened:
		t.Fatalf("synchronous Open returned before the held check finished: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-opened:
		if err == nil || !strings.Contains(err.Error(), "sqlite integrity check failed") {
			t.Fatalf("synchronous Open error = %v, want the integrity failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("synchronous Open did not return after the check was released")
	}
	if failures := TakeIntegrityFailures(); len(failures) != 0 {
		t.Fatalf("a synchronous failure was also reported as a background finding: %#v", failures)
	}
	assertNoSignal(t, findings, "pushed finding for a synchronous failure")
	_ = CloseAll()
}

// TestIntegrityFindingClassification pins how a finished check maps to a
// finding without building each condition on disk.
func TestIntegrityFindingClassification(t *testing.T) {
	stale := "wrong # of entries in index idx_entries_session_type"
	damage := &IntegrityError{Path: "x", Report: "page 5 is never used"}
	for _, tc := range []struct {
		name    string
		outcome integrityOutcome
		err     error
		want    IntegrityFindingKind
		found   bool
	}{
		{name: "healthy", found: false},
		{name: "repaired", outcome: integrityOutcome{report: stale, repaired: true}, want: IntegrityIndexRepaired, found: true},
		{name: "unrepaired", outcome: integrityOutcome{report: stale}, err: errors.New("writer lock was unavailable"), want: IntegrityIndexUnrepaired, found: true},
		{name: "damage", err: damage, want: IntegrityCorrupt, found: true},
		{name: "check error", err: errors.New("disk I/O error"), want: IntegrityCheckFailed, found: true},
	} {
		finding, ok := integrityFindingFor("x", tc.outcome, tc.err)
		if ok != tc.found || finding.Kind != tc.want {
			t.Fatalf("%s: integrityFindingFor = (%#v, %v), want kind %q found %v", tc.name, finding, ok, tc.want, tc.found)
		}
		if ok && (finding.Path != "x" || finding.At.IsZero()) {
			t.Fatalf("%s: finding = %#v, want the path and a timestamp", tc.name, finding)
		}
	}
	finding, _ := integrityFindingFor("x", integrityOutcome{}, damage)
	if !errors.Is(finding.Err, damage) || finding.Report != damage.Report {
		t.Fatalf("corruption finding = %#v, want it to carry the IntegrityError and its report", finding)
	}
}

// TestCloseCancelsInFlightCheck proves a closing connection never waits for a
// check to finish on its own: the check is interrupted, reports nothing, and
// the next open repeats it.
func TestCloseCancelsInFlightCheck(t *testing.T) {
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()
	quickCheckBudgetForTest(t, 40*time.Millisecond)

	for _, tc := range []struct {
		name  string
		close func(path string) error
	}{
		{name: "Close", close: func(path string) error { return Close(path) }},
		{name: "CloseAll", close: func(string) error { return CloseAll() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sessions.db")
			staleIndexDatabase(t, path)
			entered := make(chan struct{}, 4)
			release := holdStartupCheck(t, entered)
			if _, err := Open(path, nil); err != nil {
				t.Fatalf("Open: %v", err)
			}
			waitForSignal(t, entered, "the startup check to start")
			before := startupCheckCounters.cancelled.Load()

			closed := make(chan error, 1)
			go func() { closed <- tc.close(path) }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s waited for the in-flight check instead of interrupting it", tc.name)
			}
			waitForStartupCheckIdle(t, path)
			if cancelled := startupCheckCounters.cancelled.Load(); cancelled != before+1 {
				t.Fatalf("cancelled checks = %d, want %d", cancelled, before+1)
			}
			assertNoSignal(t, findings, "finding for a cancelled check")
			if failures := TakeIntegrityFailures(); len(failures) != 0 {
				t.Fatalf("a cancelled check reported a failure: %#v", failures)
			}
			release()
			_ = CloseAll()
		})
	}
}

// TestAsyncAndSyncModes pins the two explicit modes against a held check: async
// never waits, sync waits for the verdict.
func TestAsyncAndSyncModes(t *testing.T) {
	quickCheckBudgetForTest(t, 40*time.Millisecond)
	// A table keeps CloseAll off the driver quirk where the first checkpoint of
	// a table-less database is reported as locked.
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS probe (value TEXT NOT NULL)`)
		return err
	}
	for _, tc := range []struct {
		mode     string
		wantWait bool
	}{
		{mode: "async", wantWait: false},
		{mode: "sync", wantWait: true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("MOTHX_SQLITE_QUICK_CHECK", tc.mode)
			path := filepath.Join(t.TempDir(), "sessions.db")
			if _, err := Open(path, migrate); err != nil {
				t.Fatal(err)
			}
			if err := CloseAll(); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{}, 4)
			release := holdStartupCheck(t, entered)

			opened := make(chan error, 1)
			go func() {
				_, err := Open(path, migrate)
				opened <- err
			}()
			waitForSignal(t, entered, "the startup check to start")
			returned := false
			select {
			case err := <-opened:
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				returned = true
			case <-time.After(150 * time.Millisecond):
			}
			if returned == tc.wantWait {
				t.Fatalf("mode %q returned while the check was held = %v, want %v", tc.mode, returned, !tc.wantWait)
			}
			release()
			if !returned {
				select {
				case err := <-opened:
					if err != nil {
						t.Fatalf("Open after release: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("Open did not return after the check was released")
				}
			}
			if err := CloseAll(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestAsyncModeKeepsCheckAheadOfWALSwitch covers the one database a background
// check cannot start on: switching the journal mode to WAL needs the exclusive
// access a running reader denies, so even the async mode waits for the verdict
// there and then performs the switch.
func TestAsyncModeKeepsCheckAheadOfWALSwitch(t *testing.T) {
	t.Setenv("MOTHX_SQLITE_QUICK_CHECK", "async")
	path := filepath.Join(t.TempDir(), "rollback-journal.db")
	raw, err := sql.Open("sqlite", testDSNWithBusyTimeout(path, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE probe (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if strings.EqualFold(mode, "wal") {
		t.Fatalf("fixture journal mode = %q, want a rollback-journal database", mode)
	}

	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)
	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, nil)
		opened <- err
	}()
	waitForSignal(t, entered, "the startup check to start")
	select {
	case err := <-opened:
		t.Fatalf("Open returned before the check finished on a non-WAL database: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("Open after release: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open did not return after the check was released")
	}
	defer CloseAll()
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal mode after Open = %q, want wal", mode)
	}
}

// TestCancelInterruptsRunningQuickCheck uses a real check slow enough to
// observe: cancelling the context must end the statement long before it would
// have finished by itself, which is what lets Close interrupt a background
// check instead of waiting for it.
func TestCancelInterruptsRunningQuickCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a full-text index")
	}
	path := filepath.Join(t.TempDir(), "fts.db")
	raw, err := sql.Open("sqlite", DSNForOS(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE VIRTUAL TABLE bodies USING fts5(body, tokenize='trigram')`); err != nil {
		t.Fatal(err)
	}
	// A trigram index makes quick_check re-tokenize every stored character. The
	// text repeats a short pattern so the index itself stays small: the cost is
	// re-tokenizing the rows, about 2 KiB each.
	pattern := make([]byte, 2048)
	for i := range pattern {
		pattern[i] = byte('a' + i%26)
	}
	text := string(pattern)
	insert := func(rows int) {
		t.Helper()
		tx, err := raw.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < rows; i++ {
			if _, err := tx.Exec(`INSERT INTO bodies (body) VALUES (?)`, text); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	// Grow the index until one uninterrupted check is long enough to observe.
	// The race detector slows the pure-Go driver down by more than an order of
	// magnitude, so a fixed size would be either too slow there or too fast
	// everywhere else.
	rows, full := 0, time.Duration(0)
	for step := 500; ; step *= 2 {
		insert(step)
		rows += step
		started := time.Now()
		if outcome, err := checkIntegrityWithBudget(context.Background(), raw, path, time.Second); err != nil || outcome.repaired {
			t.Fatalf("full check: outcome %#v, err %v", outcome, err)
		}
		full = time.Since(started)
		if full >= 400*time.Millisecond || rows >= 8000 {
			break
		}
	}
	if full < 300*time.Millisecond {
		t.Skipf("a check of %d rows took only %v, too little to observe an interruption", rows, full)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := checkIntegrityWithBudget(ctx, raw, path, time.Second)
		done <- err
	}()
	// Give the statement time to be running before it is interrupted.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		interrupted := time.Since(started)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted check error = %v, want context.Canceled", err)
		}
		if interrupted >= full/2 {
			t.Fatalf("cancelled check of %d rows returned after %v, want well before the uninterrupted %v", rows, interrupted, full)
		}
	case <-time.After(full):
		t.Fatalf("the cancelled check did not return before the uninterrupted duration %v", full)
	}
}

// schemaIncompatibleMigration stands in for a migration that cannot be applied
// in place: the one failure internal/db answers by backing the database up and
// starting an empty one.
func schemaIncompatibleMigration() Migrator {
	return func(*sql.DB) error {
		return SchemaIncompatible(errors.New("apply schema migration 16 (add_channel_binding_columns): no such table: main.sessions"))
	}
}

// TestFailedMigrationWaitsForBackgroundVerdict keeps the rebuild path behind the
// integrity verdict. A damaged database makes migrations fail for reasons a
// rebuild must never "fix", so when the startup check is still running the
// failure waits for it: damage fails the open and leaves the file alone,
// exactly as a synchronous check would have.
func TestFailedMigrationWaitsForBackgroundVerdict(t *testing.T) {
	TakeMigrationRecoveries()
	TakeIntegrityFailures()
	quickCheckBudgetForTest(t, 40*time.Millisecond)
	path := filepath.Join(t.TempDir(), "sessions.db")
	brokenTablePageDatabase(t, path)
	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)

	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, schemaIncompatibleMigration())
		opened <- err
	}()
	waitForSignal(t, entered, "the startup check to start")
	select {
	case err := <-opened:
		t.Fatalf("Open decided the fate of the database before the integrity verdict: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-opened:
		if !IsIntegrityError(err) {
			t.Fatalf("Open error = %v, want the integrity failure", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open did not return after the check was released")
	}
	if backups := migrationBackups(path); len(backups) != 0 {
		t.Fatalf("a damaged database was backed up for a rebuild: %#v", backups)
	}
	if recoveries := MigrationRecoveries(); len(recoveries) != 0 {
		t.Fatalf("a damaged database was rebuilt after a migration error: %#v", recoveries)
	}
	_ = CloseAll()

	// The file itself was left alone: its schema still names the table.
	raw, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var tables int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'entries'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("tables named entries = %d, want 1: the database must not have been replaced", tables)
	}
}

// TestFailedMigrationRetriesAfterBackgroundIndexRepair covers a migration that
// failed on the stale index the check then rebuilt: the verdict is a pass, so
// the migration gets one more try instead of a rebuild.
func TestFailedMigrationRetriesAfterBackgroundIndexRepair(t *testing.T) {
	TakeIndexRepairs()
	TakeMigrationRecoveries()
	quickCheckBudgetForTest(t, 40*time.Millisecond)
	path := filepath.Join(t.TempDir(), "sessions.db")
	staleIndexDatabase(t, path)
	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)

	calls := 0
	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, func(*sql.DB) error {
			calls++
			if calls == 1 {
				return SchemaIncompatible(errors.New("no query solution"))
			}
			return nil
		})
		opened <- err
	}()
	waitForSignal(t, entered, "the startup check to start")
	select {
	case err := <-opened:
		t.Fatalf("Open reported the migration before the integrity verdict: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("Open after the index repair: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open did not return after the check was released")
	}
	if calls != 2 {
		t.Fatalf("migration attempts = %d, want 2", calls)
	}
	if repairs := TakeIndexRepairs(); len(repairs) != 1 {
		t.Fatalf("index repairs = %#v, want exactly one", repairs)
	}
	if backups := migrationBackups(path); len(backups) != 0 {
		t.Fatalf("a repaired database was backed up for a rebuild: %#v", backups)
	}
	if recoveries := MigrationRecoveries(); len(recoveries) != 0 {
		t.Fatalf("a repaired database was rebuilt after a migration error: %#v", recoveries)
	}
	_ = CloseAll()
}

// TestFailedMigrationStillRebuildsAVerifiedDatabase pins the other side of the
// guard: once the background check has passed, a migration that cannot be
// applied keeps its existing recovery, because the failure is then about the
// schema and not about damage.
func TestFailedMigrationStillRebuildsAVerifiedDatabase(t *testing.T) {
	TakeMigrationRecoveries()
	quickCheckBudgetForTest(t, 40*time.Millisecond)
	path := filepath.Join(t.TempDir(), "sessions.db")
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS legacy (value TEXT NOT NULL)`)
		return err
	}
	if _, err := Open(path, migrate); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)

	// This "build" cannot upgrade the legacy table and says so, but initializes
	// an empty database normally, which is what it sees after the rebuild.
	current := func(sqlDB *sql.DB) error {
		var legacy int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'legacy'`).Scan(&legacy); err != nil {
			return err
		}
		if legacy > 0 {
			return SchemaIncompatible(errors.New("apply schema migration 16 (add_channel_binding_columns): no such table: main.sessions"))
		}
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS current (value TEXT NOT NULL)`)
		return err
	}
	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, current)
		opened <- err
	}()
	waitForSignal(t, entered, "the startup check to start")
	release()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("Open with a rebuildable schema failure: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open did not return after the check was released")
	}
	defer CloseAll()
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	recoveries := MigrationRecoveries()
	if len(recoveries) != 1 || recoveries[0].Path != canonical || recoveries[0].BackupPath == "" {
		t.Fatalf("recoveries = %#v, want one rebuild of %s with a backup", recoveries, canonical)
	}
	if backups := migrationBackups(path); len(backups) != 1 {
		t.Fatalf("backups = %#v, want exactly one", backups)
	}
}

// TestAsyncModeReportsItsVerdict pins the mode that never waits: the goroutine
// owns the report from the start, so a verdict still reaches the record and the
// subscribers instead of being dropped as "the caller is waiting for it".
func TestAsyncModeReportsItsVerdict(t *testing.T) {
	TakeIndexRepairs()
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()
	t.Setenv("MOTHX_SQLITE_QUICK_CHECK", "async")

	path := filepath.Join(t.TempDir(), "sessions.db")
	brokenTablePageDatabase(t, path)
	canonical, err := CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	// No gate holds the check: this is the asynchronous open as it happens.
	if _, err := Open(path, nil); err != nil {
		t.Fatalf("Open in async mode on a damaged database: %v", err)
	}
	defer CloseAll()
	finding := waitForSignal(t, findings, "the corruption finding in async mode")
	if finding.Kind != IntegrityCorrupt || finding.Path != canonical {
		t.Fatalf("finding = %#v, want a corruption finding for %s", finding, canonical)
	}
	if failures := TakeIntegrityFailures(); len(failures) != 1 || failures[0].Kind != IntegrityCorrupt {
		t.Fatalf("drained failures = %#v, want exactly one corruption finding", failures)
	}
}

// TestOpenSurvivesACheckCancelledByClose pins the one outcome of a check that
// is not a verdict: Close interrupting it while another goroutine is still
// waiting for it. The open must not mistake that for a failed database.
func TestOpenSurvivesACheckCancelledByClose(t *testing.T) {
	TakeIntegrityFailures()
	findings := make(chan IntegrityFinding, 4)
	unsubscribe := SubscribeIntegrityFindings(func(finding IntegrityFinding) { findings <- finding })
	defer unsubscribe()
	quickCheckBudgetForTest(t, 5*time.Second)

	path := filepath.Join(t.TempDir(), "sessions.db")
	migrate := func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS probe (value TEXT NOT NULL)`)
		return err
	}
	// A database that is already in WAL mode is the one whose check can run
	// beside the open; a fresh file is checked before it is switched to WAL.
	if _, err := Open(path, migrate); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 4)
	release := holdStartupCheck(t, entered)

	opened := make(chan error, 1)
	go func() {
		_, err := Open(path, migrate)
		opened <- err
	}()
	waitForSignal(t, entered, "the startup check to start")
	// A shutdown elsewhere in the process closes every database while this
	// open is still inside its wait budget.
	stopAllStartupChecks()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("Open failed on a check that Close interrupted: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Open did not return after its check was cancelled")
	}
	assertNoSignal(t, findings, "finding for a cancelled check")
	if failures := TakeIntegrityFailures(); len(failures) != 0 {
		t.Fatalf("a cancelled check was reported as a failure: %#v", failures)
	}
	release()
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
}
