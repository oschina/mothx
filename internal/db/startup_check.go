package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The startup integrity check reads every page of a database and re-verifies
// every index, so its cost grows with the file: a multi-gigabyte session store
// keeps the process that opens it from serving anything for tens of seconds.
// Letting the check finish after the open returns keeps the barrier for the
// databases where it is cheap and turns it into a reported finding for the rest.
//
// Nothing about the verdict changes. A stale secondary index is still rebuilt
// losslessly and table damage is still never touched; only the moment the
// caller learns the outcome moves, from "before the first query" to "when the
// check finishes".

// StartupCheckMode selects when a managed database runs its startup integrity
// check relative to the open that needs it.
type StartupCheckMode uint8

const (
	// StartupCheckBounded is the default. The open waits a short fixed budget
	// for the verdict and otherwise keeps the check running in the background.
	// A database whose check fits in the budget keeps the original
	// check-before-use behavior, while a large one delays the open by at most
	// that budget.
	StartupCheckBounded StartupCheckMode = iota
	// StartupCheckAsync never waits for the check.
	StartupCheckAsync
	// StartupCheckSync is the fail-closed behavior: the open returns only after
	// the check repaired what it could, or reports the damage as its error.
	StartupCheckSync
	// StartupCheckOff disables the check.
	StartupCheckOff
)

const (
	// backgroundIndexRepairBudget is how long a background check keeps retrying
	// the index rebuild while another process holds the writer lock. Unlike a
	// startup check it runs beside the live writers of this very process, so the
	// rebuild gets a wider window instead of reporting contention at once.
	backgroundIndexRepairBudget = time.Minute
)

// startupCheckWait bounds how long opening a database waits for a startup
// integrity check before handing it to the background. It is a variable so
// tests can exercise the handoff without a real slow check.
var startupCheckWait = time.Second

// startupCheckGate runs at the start of every startup integrity check when
// non-nil. Tests use it to hold a check open; production leaves it nil.
var startupCheckGate func(context.Context) error

var startupCheckCounters struct {
	handedOff atomic.Uint64
	cancelled atomic.Uint64
	passed    atomic.Uint64
	findings  atomic.Uint64
}

// StartupQuickCheckMode resolves MOTHX_SQLITE_QUICK_CHECK. An unset or
// unrecognized value selects the default mode; only an explicit disable value
// turns the check off, and "sync"/"async" select the two other behaviors.
func StartupQuickCheckMode() StartupCheckMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MOTHX_SQLITE_QUICK_CHECK"))) {
	case "off", "false", "0", "skip", "disabled":
		return StartupCheckOff
	case "sync":
		return StartupCheckSync
	case "async":
		return StartupCheckAsync
	default:
		return StartupCheckBounded
	}
}

// StartupQuickCheckEnabled reports whether opening a managed database runs the
// startup integrity check at all. It stays enabled in every mode but off;
// on-demand VerifyIntegrity checks such as `mothx doctor` never consult it.
func StartupQuickCheckEnabled() bool {
	return StartupQuickCheckMode() != StartupCheckOff
}

// startupCheck is one startup integrity check running on a pooled connection
// of its own.
//
// Exactly one side reports the outcome. A check that finishes before the opener
// stops waiting is handled by the opener like a synchronous check; once the
// opener hands it off, the goroutine publishes the finding. handedOff decides
// which side owns the report and is guarded by mu because the check finishing
// and the wait budget elapsing may coincide.
type startupCheck struct {
	path      string
	cancel    context.CancelFunc
	done      chan struct{}
	mu        sync.Mutex
	handedOff bool
	outcome   integrityOutcome
	err       error
}

// startupChecks indexes the in-flight checks by canonical database path. It has
// its own lock because a finishing check removes itself while Open may still
// hold the connection lock, and Close must be able to wait on a check without
// holding either.
var startupChecks = struct {
	sync.Mutex
	byPath map[string]*startupCheck
}{byPath: make(map[string]*startupCheck)}

// startStartupCheck begins the startup integrity check and returns as soon as
// it is running. The caller decides how long to wait for it; a caller that
// never will passes background=true, because the goroutine must then own the
// report from the start. Without it a check nobody waits for would finish,
// conclude that its caller is still waiting, and report nothing.
func startStartupCheck(sqlDB *sql.DB, path string, background bool) *startupCheck {
	ctx, cancel := context.WithCancel(context.Background())
	check := &startupCheck{path: path, cancel: cancel, done: make(chan struct{}), handedOff: background}
	startupChecks.Lock()
	startupChecks.byPath[path] = check
	startupChecks.Unlock()
	go func() {
		outcome, err := runStartupCheck(ctx, sqlDB, path, backgroundIndexRepairBudget)
		// A cancelled check is a shutdown, not a verdict: nothing is reported
		// and the next open repeats the check.
		cancelled := ctx.Err() != nil
		check.mu.Lock()
		check.outcome, check.err = outcome, err
		publish := !cancelled && check.handedOff
		check.mu.Unlock()
		recordStartupCheckOutcome(cancelled, err)
		startupChecks.Lock()
		if startupChecks.byPath[path] == check {
			delete(startupChecks.byPath, path)
		}
		startupChecks.Unlock()
		// done closes before anything is published, so a closing connection
		// never waits on a front-end callback.
		close(check.done)
		cancel()
		if publish {
			publishIntegrityFinding(integrityFindingFor(path, outcome, err))
		}
	}()
	return check
}

// runStartupCheck is the check body shared by every mode, with the
// writer-lock budget its index rebuild may spend.
func runStartupCheck(ctx context.Context, sqlDB *sql.DB, path string, repairBudget time.Duration) (integrityOutcome, error) {
	if startupCheckGate != nil {
		if err := startupCheckGate(ctx); err != nil {
			return integrityOutcome{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return integrityOutcome{}, err
	}
	return checkIntegrityWithBudget(ctx, sqlDB, path, repairBudget)
}

func recordStartupCheckOutcome(cancelled bool, err error) {
	switch {
	case cancelled:
		startupCheckCounters.cancelled.Add(1)
	case err != nil:
		startupCheckCounters.findings.Add(1)
	default:
		startupCheckCounters.passed.Add(1)
	}
}

// wait blocks until the check finishes or the budget elapses. A finished check
// reports its outcome to the caller, which then owns the report; an unfinished
// one is handed off and reports for itself from the background.
//
// The outcome may be a cancellation instead of a verdict: Close may have
// interrupted the check while the caller was waiting. That says nothing about
// the database, so the caller treats it as "no verdict", never as a failure.
func (c *startupCheck) wait(budget time.Duration) (finished bool, err error) {
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-c.done:
		return true, c.err
	case <-timer.C:
	}
	c.mu.Lock()
	select {
	case <-c.done:
		// The check completed as the budget ran out and has not published.
		c.mu.Unlock()
		return true, c.err
	default:
		c.handedOff = true
	}
	c.mu.Unlock()
	return false, nil
}

// verdict waits for a check that is running in the background and returns its
// outcome. The check still reports for itself; a caller uses the answer only to
// make its own decision.
func (c *startupCheck) verdict() (integrityOutcome, error) {
	<-c.done
	return c.outcome, c.err
}

// stop cancels an in-flight check and waits until its statement has returned,
// so closing the connection never waits on it and the goroutine never outlives
// the handle. The driver interrupts a running statement on cancellation, so
// this returns promptly even for a check that would have run for minutes.
func (c *startupCheck) stop() {
	if c == nil {
		return
	}
	c.cancel()
	<-c.done
}

// stopStartupCheckFor cancels the in-flight check for path, if any.
func stopStartupCheckFor(path string) {
	startupChecks.Lock()
	check := startupChecks.byPath[path]
	delete(startupChecks.byPath, path)
	startupChecks.Unlock()
	check.stop()
}

// stopAllStartupChecks cancels every in-flight check and returns once each
// check statement has returned.
func stopAllStartupChecks() {
	startupChecks.Lock()
	checks := make([]*startupCheck, 0, len(startupChecks.byPath))
	for _, check := range startupChecks.byPath {
		checks = append(checks, check)
	}
	startupChecks.byPath = make(map[string]*startupCheck)
	startupChecks.Unlock()
	for _, check := range checks {
		check.stop()
	}
}

// journalModeIsWAL reports whether the database is already in WAL mode.
func journalModeIsWAL(sqlDB *sql.DB) (bool, error) {
	var mode string
	if err := sqlDB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(mode), "wal"), nil
}

// integrityFindingFor classifies a check that finished in the background. The
// second result is false for a healthy database, which is not a finding.
func integrityFindingFor(path string, outcome integrityOutcome, err error) (IntegrityFinding, bool) {
	finding := IntegrityFinding{Path: path, Report: outcome.report, Err: err, At: time.Now()}
	switch {
	case err == nil && outcome.repaired:
		finding.Kind = IntegrityIndexRepaired
	case err != nil && outcome.report != "":
		// The repairable condition was found, but the index could not be
		// rebuilt (writer contention, a read-only file, ...).
		finding.Kind = IntegrityIndexUnrepaired
	case err != nil:
		var integrity *IntegrityError
		if errors.As(err, &integrity) {
			finding.Kind = IntegrityCorrupt
			finding.Report = integrity.Report
		} else {
			finding.Kind = IntegrityCheckFailed
		}
	default:
		return IntegrityFinding{}, false
	}
	return finding, true
}
