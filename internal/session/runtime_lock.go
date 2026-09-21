package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/startvibecoding/mothx/internal/dao"
	database "github.com/startvibecoding/mothx/internal/db"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	runtimeLeaseTTL       = 15 * time.Second
	runtimeHeartbeatEvery = 3 * time.Second
	// runtimeHeartbeatRetry bounds how long one heartbeat tick keeps retrying a
	// failed renewal before yielding to the next tick. It must absorb at least one
	// full SQLite busy_timeout stall (database.BusyTimeout, the longest a single
	// renewal statement can block) plus a follow-up attempt, so a transient timeout
	// is retried within the tick instead of spilling into the next one. Exhausting
	// it is NOT ownership loss: a database timeout is retried on the following
	// tick, and a lease is only ever marked lost when a renewal actually executes
	// and the fenced owner/epoch/token CAS no longer matches.
	runtimeHeartbeatRetry = database.BusyTimeout + 2*runtimeHeartbeatEvery
)

var (
	ErrRuntimeLeaseBusy         = errors.New("session runtime lease is held by another process")
	ErrRuntimeLeaseLost         = errors.New("session runtime lease was lost")
	ErrRuntimeSessionNotFound   = errors.New("session runtime lease requires an existing session")
	ErrSessionRunActive         = errors.New("session has an active durable run")
	ErrSessionRecoveryRequired  = errors.New("session has an active durable run that requires reconciliation")
	ErrSessionRecoveryNotNeeded = errors.New("session has no active durable run to recover")
	ErrRuntimeLeaseRunMismatch  = errors.New("session runtime lease run does not match the active durable run")
	ErrRuntimeLeasePurpose      = errors.New("session runtime lease purpose does not allow this operation")
)

var runtimeProcess = struct {
	sync.Once
	id string
}{}

func runtimeOwnerID() string {
	runtimeProcess.Do(func() {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			runtimeProcess.id = fmt.Sprintf("pid-%d-%d", os.Getpid(), time.Now().UnixNano())
			return
		}
		runtimeProcess.id = fmt.Sprintf("pid-%d-%s", os.Getpid(), hex.EncodeToString(nonce[:]))
	})
	return runtimeProcess.id
}

var runtimeLocks = newLockRegistry()

var sessionDataLocks = newLockRegistry()

var activeRuntimeLeases = struct {
	sync.Mutex
	leases map[string]*runtimeLease
}{leases: make(map[string]*runtimeLease)}

func runtimeLockKey(sessionDir, sessionID string) string {
	clean := filepath.Clean(sessionDir)
	if absolute, err := filepath.Abs(clean); err == nil {
		clean = absolute
	}
	return clean + "\x00" + sessionID
}

type runtimeLease struct {
	bindingMu  sync.RWMutex
	sessionDir string
	sessionID  string
	ownerID    string
	purpose    string
	runID      string
	tokenHash  string
	epoch      int64
	stop       chan struct{}
	lost       chan struct{}
	stopOnce   sync.Once
	lostOnce   sync.Once
	// refs includes the caller-owned RuntimeLeaseGuard plus any Runtime-owned
	// execution retention. The durable lease is released only after the last
	// reference drops, so an adapter cannot accidentally revoke authority while
	// terminal persistence is still retrying in the shared Runtime.
	refs     int
	released bool
}

type runtimeLeaseAcquireMode uint8

const (
	runtimeLeaseAcquireLegacy runtimeLeaseAcquireMode = iota
	runtimeLeaseAcquireNoActiveRun
	runtimeLeaseAcquireRecovery
)

type runtimeLeaseAcquireOptions struct {
	purpose             RuntimeLeasePurpose
	runID               string
	mode                runtimeLeaseAcquireMode
	allowMissingSession bool
}

func rememberRuntimeLease(lease *runtimeLease) {
	if lease == nil {
		return
	}
	activeRuntimeLeases.Lock()
	activeRuntimeLeases.leases[runtimeLockKey(lease.sessionDir, lease.sessionID)] = lease
	activeRuntimeLeases.Unlock()
	ensureLeaseHeartbeatScheduler(leaseDirKey(lease.sessionDir))
}

func forgetRuntimeLease(lease *runtimeLease) {
	if lease == nil {
		return
	}
	activeRuntimeLeases.Lock()
	key := runtimeLockKey(lease.sessionDir, lease.sessionID)
	if current := activeRuntimeLeases.leases[key]; current == lease {
		delete(activeRuntimeLeases.leases, key)
	}
	activeRuntimeLeases.Unlock()
}

func hasLiveRuntimeLease(sessionDir string, record *dao.RuntimeLeaseRecord) bool {
	if record == nil {
		return false
	}
	activeRuntimeLeases.Lock()
	lease := activeRuntimeLeases.leases[runtimeLockKey(sessionDir, record.SessionID)]
	activeRuntimeLeases.Unlock()
	if lease == nil {
		return false
	}
	lease.bindingMu.RLock()
	defer lease.bindingMu.RUnlock()
	return !lease.released && lease.ownerID == record.OwnerID && lease.epoch == record.Epoch && lease.tokenHash == record.TokenHash
}

// RuntimeLeaseLost returns the loss signal for the current process lease. It
// is intentionally read-only; callers use it to cancel work while every
// durable write still performs its own epoch/token fence check.
func RuntimeLeaseLost(sessionDir, sessionID string) <-chan struct{} {
	activeRuntimeLeases.Lock()
	lease := activeRuntimeLeases.leases[runtimeLockKey(sessionDir, sessionID)]
	activeRuntimeLeases.Unlock()
	if lease == nil {
		return nil
	}
	return lease.lost
}

func newLeaseTokenHash() string {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	hash := sha256.Sum256(token[:])
	return hex.EncodeToString(hash[:])
}

func sqliteNow(tx *dao.Tx) (int64, error) {
	return sqliteNowContext(tx, context.Background())
}

func sqliteNowContext(tx *dao.Tx, ctx context.Context) (int64, error) {
	return dao.NewRuntimeLeaseDAO(nil).Now(ctx, tx)
}

func acquireRuntimeLease(sessionDir, sessionID, purpose string) (*runtimeLease, error) {
	return acquireRuntimeLeaseWithOptions(sessionDir, sessionID, runtimeLeaseAcquireOptions{
		purpose: RuntimeLeasePurpose(purpose), allowMissingSession: true,
	})
}

func acquireRuntimeLeaseWithOptions(sessionDir, sessionID string, options runtimeLeaseAcquireOptions) (*runtimeLease, error) {
	return acquireRuntimeLeaseWithOptionsContext(context.Background(), sessionDir, sessionID, options)
}

func acquireRuntimeLeaseWithOptionsContext(ctx context.Context, sessionDir, sessionID string, options runtimeLeaseAcquireOptions) (*runtimeLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil, ErrRuntimeLeaseBusy
	}
	if sessionDir == "" {
		// Test and embedded in-memory adapters may intentionally omit a session
		// root. Production adapters resolve Settings.GetSessionDir before
		// admission, so only this compatibility path remains process-local.
		if options.allowMissingSession {
			return nil, nil
		}
		return nil, fmt.Errorf("session runtime directory is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now, err := sqliteNowContext(tx, ctx)
	if err != nil {
		return nil, err
	}
	sessionExists, err := dao.NewRuntimeLeaseDAO(nil).SessionExists(ctx, tx, sessionID)
	if err == nil && !sessionExists {
		if options.allowMissingSession {
			// Compatibility for legacy callers that reserve a client-side ID before
			// the first durable Session insert. New explicit acquisition APIs reject
			// this process-local-only path.
			return nil, nil
		}
		return nil, ErrRuntimeSessionNotFound
	} else if err != nil {
		return nil, err
	}
	expires := now + int64(runtimeLeaseTTL/time.Second)
	ownerID := runtimeOwnerID()
	tokenHash := newLeaseTokenHash()
	purpose := string(options.purpose)
	lease := &runtimeLease{sessionDir: sessionDir, sessionID: sessionID, ownerID: ownerID, purpose: purpose, runID: options.runID, tokenHash: tokenHash, stop: make(chan struct{}), lost: make(chan struct{}), refs: 1}

	current, err := dao.NewRuntimeLeaseDAO(nil).Find(ctx, tx, sessionID)
	if err != nil && err != dao.ErrNoRows {
		return nil, err
	}
	// A same-process row is reclaimable only when its exact fenced identity is
	// no longer registered locally. Retained execution references can outlive a
	// guard's process-local mutex, so owner_instance_id alone is not evidence
	// that an active row is stranded.
	allowSameOwnerReclaim := false
	if err == nil && current.State == "active" && current.ExpiresAt > now {
		if current.OwnerID != ownerID || hasLiveRuntimeLease(sessionDir, current) {
			return nil, ErrRuntimeLeaseBusy
		}
		allowSameOwnerReclaim = true
	}
	if options.mode != runtimeLeaseAcquireLegacy {
		activeRunIDs, activeErr := activeSessionRunIDsTxContext(ctx, tx, sessionID)
		if activeErr != nil {
			return nil, activeErr
		}
		switch options.mode {
		case runtimeLeaseAcquireNoActiveRun:
			if len(activeRunIDs) != 0 {
				if options.purpose == RuntimeLeasePurposeAdmission {
					return nil, ErrSessionRecoveryRequired
				}
				return nil, ErrSessionRunActive
			}
		case runtimeLeaseAcquireRecovery:
			if len(activeRunIDs) == 0 {
				return nil, ErrSessionRecoveryNotNeeded
			}
			if len(activeRunIDs) != 1 || activeRunIDs[0] != options.runID {
				return nil, ErrRuntimeLeaseRunMismatch
			}
		}
	}
	if err == dao.ErrNoRows {
		lease.epoch = 1
		err = dao.NewRuntimeLeaseDAO(nil).Insert(ctx, tx, &dao.RuntimeLeaseRecord{SessionID: sessionID, OwnerID: ownerID, OwnerPID: os.Getpid(), OwnerKind: "process", TokenHash: tokenHash, Epoch: lease.epoch, RunID: options.runID, Purpose: purpose, State: "active", AcquiredAt: now, HeartbeatAt: now, ExpiresAt: expires, UpdatedAt: now})
	} else {
		lease.epoch = current.Epoch + 1
		var count int64
		count, err = dao.NewRuntimeLeaseDAO(nil).Acquire(ctx, tx, &dao.RuntimeLeaseRecord{SessionID: sessionID, OwnerID: ownerID, OwnerPID: os.Getpid(), OwnerKind: "process", TokenHash: tokenHash, Epoch: lease.epoch, RunID: options.runID, Purpose: purpose, ExpiresAt: expires}, current.Epoch, now, allowSameOwnerReclaim)
		if err == nil && count != 1 {
			err = ErrRuntimeLeaseBusy
		}
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rememberRuntimeLease(lease)
	publishRuntimeLeaseNotification(RuntimeLeaseNotification{Type: "acquired", SessionID: lease.sessionID, Origin: lease.purpose, OwnerInstanceID: lease.ownerID, Epoch: lease.epoch, ExpiresAt: expires})
	return lease, nil
}

func activeSessionRunIDsTx(tx *dao.Tx, sessionID string) ([]string, error) {
	return activeSessionRunIDsTxContext(context.Background(), tx, sessionID)
}

func activeSessionRunIDsTxContext(ctx context.Context, tx *dao.Tx, sessionID string) ([]string, error) {
	return dao.NewRuntimeLeaseDAO(nil).ActiveRunIDs(ctx, tx, sessionID, NonTerminalSessionRunStatuses())
}

// leaseDirKey normalizes a session directory the same way runtimeLockKey does
// so every spelling of one directory shares a single heartbeat scheduler (and
// therefore a single batched renewal transaction).
func leaseDirKey(sessionDir string) string {
	clean := filepath.Clean(sessionDir)
	if absolute, err := filepath.Abs(clean); err == nil {
		clean = absolute
	}
	return clean
}

// leaseHeartbeatSchedulers holds one heartbeat loop per canonical session
// directory. A single loop renews every lease this process holds for that
// directory's database in one transaction, replacing the former per-lease
// goroutines: the steady-state heartbeat write cost is one commit per
// runtimeHeartbeatEvery per process and database instead of one per active
// lease. TTL, interval, retry budget, and the fenced CAS renewal semantics
// are unchanged, so the bounded orphan-recovery guarantees that depend on
// them (see
// docs/proposal/cross-process-session-execution-ownership-proposal.md) still
// hold.
var leaseHeartbeatSchedulers = struct {
	sync.Mutex
	schedulers map[string]*leaseHeartbeatScheduler
}{schedulers: make(map[string]*leaseHeartbeatScheduler)}

type leaseHeartbeatScheduler struct {
	dirKey   string
	stop     chan struct{}
	stopOnce sync.Once
}

func ensureLeaseHeartbeatScheduler(dirKey string) {
	leaseHeartbeatSchedulers.Lock()
	defer leaseHeartbeatSchedulers.Unlock()
	if leaseHeartbeatSchedulers.schedulers[dirKey] != nil {
		return
	}
	scheduler := &leaseHeartbeatScheduler{dirKey: dirKey, stop: make(chan struct{})}
	leaseHeartbeatSchedulers.schedulers[dirKey] = scheduler
	go scheduler.run()
}

func snapshotRuntimeLeasesForDir(dirKey string) []*runtimeLease {
	activeRuntimeLeases.Lock()
	defer activeRuntimeLeases.Unlock()
	var leases []*runtimeLease
	for _, lease := range activeRuntimeLeases.leases {
		if leaseDirKey(lease.sessionDir) == dirKey {
			leases = append(leases, lease)
		}
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].sessionID < leases[j].sessionID })
	return leases
}

func (s *leaseHeartbeatScheduler) run() {
	ticker := time.NewTicker(runtimeHeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			leases := snapshotRuntimeLeasesForDir(s.dirKey)
			if len(leases) == 0 {
				// retire only reports success when it unregistered this scheduler
				// while the directory was still empty. A lease acquired in the
				// meantime must keep this scheduler running: returning here anyway
				// would leave a registered-but-stopped entry that no later acquire
				// could replace, so the live lease would never be renewed again.
				if s.retire() {
					return
				}
				continue
			}
			s.renew(leases)
		}
	}
}

// retire unregisters the scheduler once no lease remains for its directory and
// reports whether this scheduler was retired and must stop. The registry and
// the lease snapshot are rechecked while holding the scheduler map lock, so a
// concurrent acquire is never stranded without a heartbeat: either it is
// observed here (retire declines and the loop keeps ticking, picking up the new
// lease on the next tick) or this scheduler is unregistered first and the
// acquire's ensure call creates a fresh scheduler.
func (s *leaseHeartbeatScheduler) retire() bool {
	leaseHeartbeatSchedulers.Lock()
	if leaseHeartbeatSchedulers.schedulers[s.dirKey] != s {
		// Another path already removed or replaced this scheduler; stop it.
		leaseHeartbeatSchedulers.Unlock()
		return true
	}
	if len(snapshotRuntimeLeasesForDir(s.dirKey)) != 0 {
		leaseHeartbeatSchedulers.Unlock()
		return false
	}
	delete(leaseHeartbeatSchedulers.schedulers, s.dirKey)
	leaseHeartbeatSchedulers.Unlock()
	s.stopOnce.Do(func() { close(s.stop) })
	return true
}

// renew batch-renews one snapshot of leases. A transient SQLite failure (a busy
// or unreachable database) is retried within the runtimeHeartbeatRetry budget
// and, if still failing, simply left for the next heartbeat tick: a database
// timeout is not proof of ownership loss and must never interrupt a live run.
// Only a renewal that actually executes and reports zero affected rows is
// treated as loss, because the fenced owner/epoch/token CAS matching nothing is
// the one definitive sign that another process took over or the lease was
// released. Each attempt is bounded by a context deadline so the managed
// transaction's busy-begin retry can never outlive the budget.
func (s *leaseHeartbeatScheduler) renew(leases []*runtimeLease) {
	deadline := time.Now().Add(runtimeHeartbeatRetry)
	current := leases
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), remaining)
		results, err := renewLeaseBatchOnce(ctx, s.dirKey, current)
		cancel()
		if err == nil {
			for _, lease := range current {
				if results[lease.sessionID] != 1 {
					markRuntimeLeaseLost(lease, "renewal fenced out by another owner")
				}
			}
			return
		}
		select {
		case <-s.stop:
			return
		case <-time.After(200 * time.Millisecond):
		}
		refreshed := snapshotRuntimeLeasesForDir(s.dirKey)
		if len(refreshed) == 0 {
			return
		}
		current = refreshed
	}
}

func renewLeaseBatchOnce(ctx context.Context, sessionDir string, leases []*runtimeLease) (map[string]int64, error) {
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	records := make([]dao.RuntimeLeaseRecord, 0, len(leases))
	for _, lease := range leases {
		records = append(records, dao.RuntimeLeaseRecord{SessionID: lease.sessionID, OwnerID: lease.ownerID, Epoch: lease.epoch, TokenHash: lease.tokenHash})
	}
	results, err := dao.NewRuntimeLeaseDAO(nil).RenewBatch(ctx, tx, records, int64(runtimeLeaseTTL/time.Second))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func markRuntimeLeaseLost(lease *runtimeLease, reason string) {
	if lease == nil {
		return
	}
	lease.bindingMu.Lock()
	if lease.released {
		lease.bindingMu.Unlock()
		return
	}
	lease.released = true
	lease.refs = 0
	purpose := lease.purpose
	ownerID := lease.ownerID
	epoch := lease.epoch
	sessionID := lease.sessionID
	lease.bindingMu.Unlock()
	log.Printf("[session] runtime lease lost for %s (owner=%s epoch=%d): %s", sessionID, ownerID, epoch, reason)
	forgetRuntimeLease(lease)
	lease.lostOnce.Do(func() {
		close(lease.lost)
		publishRuntimeLeaseNotification(RuntimeLeaseNotification{
			Type: "lost", SessionID: sessionID, Origin: purpose, OwnerInstanceID: ownerID, Epoch: epoch,
		})
	})
}

func (lease *runtimeLease) release() {
	if lease == nil {
		return
	}
	lease.bindingMu.Lock()
	if lease.released {
		lease.bindingMu.Unlock()
		return
	}
	if lease.refs > 1 {
		lease.refs--
		lease.bindingMu.Unlock()
		return
	}
	lease.refs = 0
	lease.released = true
	sessionID := lease.sessionID
	ownerID := lease.ownerID
	epoch := lease.epoch
	tokenHash := lease.tokenHash
	purpose := lease.purpose
	lease.bindingMu.Unlock()
	lease.stopOnce.Do(func() { close(lease.stop) })
	forgetRuntimeLease(lease)
	// A voluntary release ends this process's authority just as decisively as
	// heartbeat loss. ExecutionRuntime watches this signal to retire a local
	// registration that could not persist its terminal transition.
	defer lease.lostOnce.Do(func() { close(lease.lost) })
	db, err := OpenRootDB(lease.sessionDir)
	if err != nil {
		return
	}
	// Keep a released tombstone. Removing the row would make a delayed write
	// from an old owner indistinguishable from a legacy cold write after the
	// new owner has finished and released its lease.
	count, _ := dao.NewRuntimeLeaseDAO(db.Bun()).Release(context.Background(), &dao.RuntimeLeaseRecord{SessionID: sessionID, OwnerID: ownerID, Epoch: epoch, TokenHash: tokenHash})
	if count == 1 {
		publishRuntimeLeaseNotification(RuntimeLeaseNotification{
			Type: "released", SessionID: sessionID, Origin: purpose, OwnerInstanceID: ownerID, Epoch: epoch,
		})
	}
}

// bindRuntimeLeaseToRunTx transitions the caller's admission/legacy lease to
// an execution lease in the same transaction that creates the durable Run.
// A missing lease row remains a temporary compatibility path for embedded
// stores; once a row exists, exact owner/token/epoch fencing is mandatory.
func bindRuntimeLeaseToRunTx(tx *dao.Tx, sessionDir, sessionID, runID string) (*runtimeLease, error) {
	if tx == nil || sessionID == "" || runID == "" {
		return nil, fmt.Errorf("runtime lease binding requires transaction, session ID, and run ID")
	}
	activeRuntimeLeases.Lock()
	lease := activeRuntimeLeases.leases[runtimeLockKey(sessionDir, sessionID)]
	activeRuntimeLeases.Unlock()
	if lease == nil {
		exists, err := dao.NewRuntimeLeaseDAO(nil).Exists(context.Background(), tx, sessionID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, nil
		}
		return nil, ErrRuntimeLeaseLost
	}
	lease.bindingMu.RLock()
	existingRunID := lease.runID
	purpose := lease.purpose
	lease.bindingMu.RUnlock()
	if purpose != string(RuntimeLeasePurposeAdmission) && purpose != string(RuntimeLeasePurposeLegacyRun) && purpose != string(RuntimeLeasePurposeExecution) && purpose != string(RuntimeLeasePurposeRecovery) {
		return nil, ErrRuntimeLeasePurpose
	}
	if existingRunID != "" && existingRunID != runID {
		return nil, ErrRuntimeLeaseRunMismatch
	}
	count, err := dao.NewRuntimeLeaseDAO(nil).Bind(context.Background(), tx, sessionID, lease.ownerID, lease.epoch, lease.tokenHash, runID,
		[]string{string(RuntimeLeasePurposeAdmission), string(RuntimeLeasePurposeLegacyRun), string(RuntimeLeasePurposeExecution), string(RuntimeLeasePurposeRecovery)})
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrRuntimeLeaseLost
	}
	return lease, nil
}

func markRuntimeLeaseBound(lease *runtimeLease, runID string) {
	if lease == nil {
		return
	}
	lease.bindingMu.Lock()
	lease.runID = runID
	lease.purpose = string(RuntimeLeasePurposeExecution)
	lease.bindingMu.Unlock()
}

// BindRuntimeLeaseToExistingRun promotes a recovery/legacy lease to execution
// only while the expected Run is still the Session's sole non-terminal Run.
// Reattach paths call this before registering an in-memory execution.
func BindRuntimeLeaseToExistingRun(sessionDir, sessionID, runID string) (RuntimeLeaseBinding, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(runID) == "" {
		return RuntimeLeaseBinding{}, ErrRuntimeLeaseRunMismatch
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return RuntimeLeaseBinding{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return RuntimeLeaseBinding{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return RuntimeLeaseBinding{}, err
	}
	activeRunIDs, err := activeSessionRunIDsTx(tx, sessionID)
	if err != nil {
		return RuntimeLeaseBinding{}, err
	}
	if len(activeRunIDs) != 1 || activeRunIDs[0] != runID {
		return RuntimeLeaseBinding{}, ErrRuntimeLeaseRunMismatch
	}
	lease, err := bindRuntimeLeaseToRunTx(tx, sessionDir, sessionID, runID)
	if err != nil {
		return RuntimeLeaseBinding{}, err
	}
	if lease == nil {
		return RuntimeLeaseBinding{}, nil
	}
	if err := tx.Commit(); err != nil {
		return RuntimeLeaseBinding{}, err
	}
	markRuntimeLeaseBound(lease, runID)
	binding, ok := CurrentRuntimeLeaseBinding(sessionDir, sessionID)
	if !ok {
		return RuntimeLeaseBinding{}, ErrRuntimeLeaseLost
	}
	return binding, nil
}

// validateRuntimeLeaseTx fences transcript writes from a stale process. A
// session with no lease is a cold/manual mutation; once a lease row exists,
// only the current owner and epoch may append entries.
//
// Coverage policy: execution-path writes (entries, run/capability events,
// response turns, ESM guidance) run this fence inside their transaction.
// Short administrative writes (channel bindings, channel tools, projects and
// session metadata, cron jobs) intentionally skip the fence and do not take
// AcquireMutation: they are single-statement SQLite transactions whose
// correctness comes from the database, and they must stay usable while a Run
// lease is held. If an administrative write ever needs cross-statement
// isolation, route it through AcquireMutation instead of relaxing this fence.
func validateRuntimeLeaseTx(tx *dao.Tx, sessionDir, sessionID string) error {
	return validateRuntimeLeaseTxContext(context.Background(), tx, sessionDir, sessionID)
}

func validateRuntimeLeaseTxContext(ctx context.Context, tx *dao.Tx, sessionDir, sessionID string) error {
	record, err := dao.NewRuntimeLeaseDAO(nil).Find(ctx, tx, sessionID)
	if err == dao.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	activeRuntimeLeases.Lock()
	lease := activeRuntimeLeases.leases[runtimeLockKey(sessionDir, sessionID)]
	activeRuntimeLeases.Unlock()
	// Ownership is proven by the fenced identity (state/owner/epoch/token), not by
	// wall-clock freshness. A competing process can only take over through
	// Acquire, which bumps the epoch, so an expired row that still carries our
	// identity is still ours (mirroring Renew's contract). Failing on expiry would
	// let a transient database stall that delays the heartbeat kill a live run
	// even though no other process ever took the session.
	if record.State != "active" || lease == nil || lease.ownerID != record.OwnerID || lease.tokenHash != record.TokenHash || lease.epoch != record.Epoch {
		return ErrRuntimeLeaseLost
	}
	return nil
}

// validateRuntimeLeaseBindingTx verifies that the current process owns the
// exact purpose/run binding required by a control operation. Adapter input is
// never used as lease authority; expected identity comes from the process-local
// lease handle and is checked again against SQLite under the caller's tx.
func validateRuntimeLeaseBindingTx(tx *dao.Tx, sessionDir, sessionID, runID string, purpose RuntimeLeasePurpose) (RuntimeLeaseBinding, error) {
	return validateRuntimeLeaseBindingTxContext(context.Background(), tx, sessionDir, sessionID, runID, purpose)
}

func validateRuntimeLeaseBindingTxContext(ctx context.Context, tx *dao.Tx, sessionDir, sessionID, runID string, purpose RuntimeLeasePurpose) (RuntimeLeaseBinding, error) {
	if err := validateRuntimeLeaseTxContext(ctx, tx, sessionDir, sessionID); err != nil {
		return RuntimeLeaseBinding{}, err
	}
	binding, ok := CurrentRuntimeLeaseBinding(sessionDir, sessionID)
	if !ok {
		return RuntimeLeaseBinding{}, ErrRuntimeLeaseLost
	}
	record, err := dao.NewRuntimeLeaseDAO(nil).Binding(ctx, tx, sessionID, binding.OwnerInstanceID, binding.Epoch, binding.TokenHash)
	if err != nil {
		if err == dao.ErrNoRows {
			return RuntimeLeaseBinding{}, ErrRuntimeLeaseLost
		}
		return RuntimeLeaseBinding{}, err
	}
	persistedRunID, persistedPurpose := record.RunID, record.Purpose
	if RuntimeLeasePurpose(persistedPurpose) != purpose || binding.Purpose != purpose {
		return RuntimeLeaseBinding{}, ErrRuntimeLeasePurpose
	}
	if persistedRunID != runID || binding.RunID != runID {
		return RuntimeLeaseBinding{}, ErrRuntimeLeaseRunMismatch
	}
	return binding, nil
}

// ValidateRuntimeLeaseContext rechecks the process-owned lease binding in a
// fresh SQLite transaction. It is the final Runtime fence before a side
// effect; callers must not treat client-provided identity as authority.
func ValidateRuntimeLeaseContext(ctx context.Context, sessionDir, sessionID, runID string, purpose RuntimeLeasePurpose) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionDir) == "" {
		return ErrRuntimeLeaseLost
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := validateRuntimeLeaseBindingTxContext(ctx, tx, sessionDir, sessionID, runID, purpose); err != nil {
		return err
	}
	status, err := dao.NewRuntimeLeaseDAO(nil).RunStatus(ctx, tx, runID, sessionID)
	if err != nil {
		if err == dao.ErrNoRows {
			return ErrRuntimeLeaseRunMismatch
		}
		return err
	}
	if isTerminalSessionRunStatus(status) {
		return ErrRuntimeLeaseLost
	}
	return nil
}

// ValidateRuntimeLease is the context-free compatibility wrapper.
func ValidateRuntimeLease(sessionDir, sessionID, runID string, purpose RuntimeLeasePurpose) error {
	return ValidateRuntimeLeaseContext(context.Background(), sessionDir, sessionID, runID, purpose)
}

func isTerminalSessionRunStatus(status string) bool {
	return IsTerminalSessionRunStatus(strings.ToLower(strings.TrimSpace(status)))
}

// RuntimeLeaseBinding is the Runtime-owned identity of an acquired Session
// lease. It is safe to expose to trusted in-process code for diagnostics and
// matching, but must never be accepted from an adapter or client as authority.
type RuntimeLeaseBinding struct {
	DatabaseIdentity string
	SessionID        string
	RunID            string
	OwnerInstanceID  string
	TokenHash        string
	Epoch            int64
	Purpose          RuntimeLeasePurpose
}

// CurrentRuntimeLeaseBinding returns the lease identity held by this process.
// It is intended for internal/agentruntime registration and diagnostics only;
// durable control operations must still revalidate the database row.
func CurrentRuntimeLeaseBinding(sessionDir, sessionID string) (RuntimeLeaseBinding, bool) {
	activeRuntimeLeases.Lock()
	lease := activeRuntimeLeases.leases[runtimeLockKey(sessionDir, sessionID)]
	activeRuntimeLeases.Unlock()
	if lease == nil {
		return RuntimeLeaseBinding{}, false
	}
	lease.bindingMu.RLock()
	runID := lease.runID
	purpose := lease.purpose
	lease.bindingMu.RUnlock()
	return RuntimeLeaseBinding{
		DatabaseIdentity: runtimeDatabaseIdentity(lease.sessionDir),
		SessionID:        lease.sessionID,
		RunID:            runID,
		OwnerInstanceID:  lease.ownerID,
		TokenHash:        lease.tokenHash,
		Epoch:            lease.epoch,
		Purpose:          RuntimeLeasePurpose(purpose),
	}, true
}

// RetainRuntimeLease adds a Runtime-owned reference to the current execution
// lease. The caller-owned RuntimeLeaseGuard may be released independently; the
// durable lease remains active until the returned release function is called.
// This is intentionally an in-process handoff primitive, never a client-facing
// authorization mechanism.
func RetainRuntimeLease(sessionDir, sessionID, runID string) (RuntimeLeaseBinding, func(), bool, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return RuntimeLeaseBinding{}, nil, false, nil
	}
	activeRuntimeLeases.Lock()
	lease := activeRuntimeLeases.leases[runtimeLockKey(sessionDir, sessionID)]
	activeRuntimeLeases.Unlock()
	if lease == nil {
		// A missing local lease is retained as a compatibility path for embedded
		// stores that predate Runtime admission.
		return RuntimeLeaseBinding{}, nil, false, nil
	}
	lease.bindingMu.Lock()
	if lease.released {
		lease.bindingMu.Unlock()
		return RuntimeLeaseBinding{}, nil, false, ErrRuntimeLeaseLost
	}
	if lease.purpose != string(RuntimeLeasePurposeExecution) {
		lease.bindingMu.Unlock()
		return RuntimeLeaseBinding{}, nil, false, ErrRuntimeLeasePurpose
	}
	if lease.runID != runID {
		lease.bindingMu.Unlock()
		return RuntimeLeaseBinding{}, nil, false, ErrRuntimeLeaseRunMismatch
	}
	lease.refs++
	binding := RuntimeLeaseBinding{
		DatabaseIdentity: runtimeDatabaseIdentity(lease.sessionDir),
		SessionID:        lease.sessionID,
		RunID:            lease.runID,
		OwnerInstanceID:  lease.ownerID,
		TokenHash:        lease.tokenHash,
		Epoch:            lease.epoch,
		Purpose:          RuntimeLeasePurpose(lease.purpose),
	}
	lease.bindingMu.Unlock()
	var once sync.Once
	return binding, func() { once.Do(lease.release) }, true, nil
}

// RuntimeLeaseGuard owns both the process-local mutex and the durable SQLite
// lease. Release is idempotent and must be called after every successful
// acquisition.
type RuntimeLeaseGuard struct {
	lease  *runtimeLease
	unlock func()
	once   sync.Once
}

// RuntimeLeaseGroup owns an ordered set of Session leases acquired for one
// multi-Session mutation.
type RuntimeLeaseGroup struct {
	guards []*RuntimeLeaseGuard
	once   sync.Once
}

// Guard returns the mutation guard this group holds for sessionID. It is used
// by one shared multi-session operation to fence each per-session write inside
// its own transaction without attempting to reacquire the process-local lock.
func (g *RuntimeLeaseGroup) Guard(sessionID string) *RuntimeLeaseGuard {
	if g == nil {
		return nil
	}
	for _, guard := range g.guards {
		if guard != nil && guard.Binding().SessionID == sessionID {
			return guard
		}
	}
	return nil
}

// Release relinquishes grouped leases in reverse acquisition order.
func (g *RuntimeLeaseGroup) Release() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		for i := len(g.guards) - 1; i >= 0; i-- {
			g.guards[i].Release()
		}
	})
}

// Release relinquishes the durable lease and then the process-local mutex.
func (g *RuntimeLeaseGuard) Release() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		if g.lease != nil {
			g.lease.release()
		}
		if g.unlock != nil {
			g.unlock()
		}
	})
}

// Lost reports when the durable lease can no longer be renewed.
func (g *RuntimeLeaseGuard) Lost() <-chan struct{} {
	if g == nil || g.lease == nil {
		return nil
	}
	return g.lease.lost
}

// Binding returns the exact identity acquired by this process.
func (g *RuntimeLeaseGuard) Binding() RuntimeLeaseBinding {
	if g == nil || g.lease == nil {
		return RuntimeLeaseBinding{}
	}
	g.lease.bindingMu.RLock()
	runID := g.lease.runID
	purpose := g.lease.purpose
	g.lease.bindingMu.RUnlock()
	return RuntimeLeaseBinding{
		DatabaseIdentity: runtimeDatabaseIdentity(g.lease.sessionDir),
		SessionID:        g.lease.sessionID,
		RunID:            runID,
		OwnerInstanceID:  g.lease.ownerID,
		TokenHash:        g.lease.tokenHash,
		Epoch:            g.lease.epoch,
		Purpose:          RuntimeLeasePurpose(purpose),
	}
}

func runtimeDatabaseIdentity(sessionDir string) string {
	path := rootDBPath(sessionDir)
	if absolute, err := filepath.Abs(filepath.Clean(path)); err == nil {
		return absolute
	}
	return filepath.Clean(path)
}

// RuntimeDatabaseIdentity returns the normalized SQLite identity used to
// scope process-local execution and recovery registries.
func RuntimeDatabaseIdentity(sessionDir string) string {
	return runtimeDatabaseIdentity(sessionDir)
}

func acquireRuntimeLeaseGuard(sessionDir, sessionID string, options runtimeLeaseAcquireOptions) (*RuntimeLeaseGuard, error) {
	return acquireRuntimeLeaseGuardContext(context.Background(), sessionDir, sessionID, options)
}

func acquireRuntimeLeaseGuardContext(ctx context.Context, sessionDir, sessionID string, options runtimeLeaseAcquireOptions) (*RuntimeLeaseGuard, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrRuntimeLeaseBusy
	}
	key := runtimeLockKey(sessionDir, sessionID)
	lock := runtimeLocks.acquire(key)
	if !lock.TryLock() {
		runtimeLocks.drop(key, lock)
		return nil, ErrRuntimeLeaseBusy
	}
	lease, err := acquireRuntimeLeaseWithOptionsContext(ctx, sessionDir, sessionID, options)
	if err != nil {
		lock.Unlock()
		runtimeLocks.drop(key, lock)
		return nil, err
	}
	return &RuntimeLeaseGuard{lease: lease, unlock: func() {
		lock.Unlock()
		runtimeLocks.drop(key, lock)
	}}, nil
}

// AcquireExecutionAdmission reserves an existing idle Session for a new Run.
// The durable admission transaction must subsequently bind the new run ID and
// transition this same lease to purpose=execution.
func AcquireExecutionAdmission(sessionDir, sessionID string) (*RuntimeLeaseGuard, error) {
	return acquireRuntimeLeaseGuard(sessionDir, sessionID, runtimeLeaseAcquireOptions{
		purpose:             RuntimeLeasePurposeAdmission,
		mode:                runtimeLeaseAcquireNoActiveRun,
		allowMissingSession: strings.TrimSpace(sessionDir) == "",
	})
}

// AcquireMutation reserves an idle Session for a short non-execution change.
func AcquireMutation(sessionDir, sessionID string) (*RuntimeLeaseGuard, error) {
	return acquireRuntimeLeaseGuard(sessionDir, sessionID, runtimeLeaseAcquireOptions{
		purpose: RuntimeLeasePurposeMutation,
		mode:    runtimeLeaseAcquireNoActiveRun,
		// Embedded/test adapters may not have a shared root. Keep this
		// process-local no-op explicit and isolated; production callers resolve
		// a non-empty Settings.SessionDir before admission.
		allowMissingSession: strings.TrimSpace(sessionDir) == "",
	})
}

// AcquireMutations reserves multiple idle Sessions in stable order so a
// cross-Session mutation cannot deadlock another caller taking the same set.
func AcquireMutations(sessionDir string, sessionIDs []string) (*RuntimeLeaseGroup, error) {
	ids := append([]string(nil), sessionIDs...)
	sort.Strings(ids)
	ordered := ids[:0]
	for _, id := range ids {
		if id == "" || (len(ordered) > 0 && ordered[len(ordered)-1] == id) {
			continue
		}
		ordered = append(ordered, id)
	}
	group := &RuntimeLeaseGroup{guards: make([]*RuntimeLeaseGuard, 0, len(ordered))}
	for _, id := range ordered {
		guard, err := AcquireMutation(sessionDir, id)
		if err != nil {
			group.Release()
			return nil, err
		}
		group.guards = append(group.guards, guard)
	}
	return group, nil
}

// AcquireFork reserves an idle source Session while a child snapshot is made.
func AcquireFork(sessionDir, sessionID string) (*RuntimeLeaseGuard, error) {
	return acquireRuntimeLeaseGuard(sessionDir, sessionID, runtimeLeaseAcquireOptions{
		purpose: RuntimeLeasePurposeFork,
		mode:    runtimeLeaseAcquireNoActiveRun,
	})
}

// AcquireRecovery claims an unowned or expired active Run for fenced recovery.
func AcquireRecovery(sessionDir, sessionID, expectedRunID string) (*RuntimeLeaseGuard, error) {
	return AcquireRecoveryContext(context.Background(), sessionDir, sessionID, expectedRunID)
}

// AcquireRecoveryContext bounds the lease acquisition transaction by the
// recovery attempt deadline.
func AcquireRecoveryContext(ctx context.Context, sessionDir, sessionID, expectedRunID string) (*RuntimeLeaseGuard, error) {
	if strings.TrimSpace(expectedRunID) == "" {
		return nil, ErrRuntimeLeaseRunMismatch
	}
	if ctx == nil {
		ctx = context.Background()
	}
	guard, err := acquireRuntimeLeaseGuardContext(ctx, sessionDir, sessionID, runtimeLeaseAcquireOptions{
		purpose: RuntimeLeasePurposeRecovery,
		runID:   expectedRunID,
		mode:    runtimeLeaseAcquireRecovery,
	})
	return guard, err
}

// TryLockRuntime serializes one session across all processes. The process-local
// mutex remains a fast path, while the SQLite lease is the authority and is
// automatically renewed until release or lease loss.
func TryLockRuntime(sessionDir, sessionID string) (func(), bool) {
	return tryLockRuntimePurpose(sessionDir, sessionID, "run")
}

func tryLockRuntimePurpose(sessionDir, sessionID, purpose string) (func(), bool) {
	guard, err := acquireRuntimeLeaseGuard(sessionDir, sessionID, runtimeLeaseAcquireOptions{
		purpose:             RuntimeLeasePurpose(purpose),
		mode:                runtimeLeaseAcquireLegacy,
		allowMissingSession: true,
	})
	if err != nil {
		return func() {}, false
	}
	return guard.Release, true
}

// LockRuntime waits for the single-session lease. It is intentionally
// implemented as retrying TryLockRuntime so no database transaction remains
// open while an execution is running.
func LockRuntime(sessionDir, sessionID string) func() {
	for {
		if release, ok := TryLockRuntime(sessionDir, sessionID); ok {
			return release
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// LockSessionData serializes short persistence mutations inside one process.
// Cross-process data consistency still comes from SQLite transactions.
func LockSessionData(sessionDir, sessionID string) func() {
	if sessionID == "" {
		return func() {}
	}
	key := runtimeLockKey(sessionDir, sessionID)
	lock := sessionDataLocks.acquire(key)
	lock.Lock()
	return func() {
		lock.Unlock()
		sessionDataLocks.drop(key, lock)
	}
}

// TryLockRuntimes acquires multiple session leases in sorted order. Different
// sessions remain independently concurrent; ordering only applies to an
// operation that explicitly spans more than one session.
func TryLockRuntimes(sessionDir string, sessionIDs []string) (func(), bool) {
	ids := append([]string(nil), sessionIDs...)
	sort.Strings(ids)
	ordered := ids[:0]
	for _, id := range ids {
		if id == "" || (len(ordered) > 0 && ordered[len(ordered)-1] == id) {
			continue
		}
		ordered = append(ordered, id)
	}
	releases := make([]func(), 0, len(ordered))
	for _, id := range ordered {
		release, ok := TryLockRuntime(sessionDir, id)
		if !ok {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}
			return func() {}, false
		}
		releases = append(releases, release)
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, true
}
