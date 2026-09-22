package agentruntime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/session"
)

const (
	DefaultRecoveryScanInterval   = 5 * time.Second
	DefaultRecoveryAttemptTimeout = 10 * time.Second
)

// RecoveryCoordinatorOptions supplies policy hooks owned by the shared
// Runtime host. Adapters may clean up their protocol projections in
// BeforeFail, but they do not decide whether a valid lease can be displaced.
type RecoveryCoordinatorOptions struct {
	ScanInterval   time.Duration
	AttemptTimeout time.Duration
	Policy         RunRecoveryPolicy
	BeforeFail     func(session.SessionRun) error
	OnResult       func(RunRecoveryResult)
	OnError        func(error)
}

// RecoveryCoordinator periodically drives lease-first orphan convergence for
// one canonical Session database. Multiple processes may scan the same DB;
// AcquireRecovery's SQLite CAS elects exactly one worker per Session.
type RecoveryCoordinator struct {
	sessionDir string
	options    RecoveryCoordinatorOptions
	wake       chan struct{}

	mu      sync.Mutex
	scanMu  sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	started bool
}

var recoveryCoordinators = struct {
	sync.RWMutex
	byDatabase map[string]map[*RecoveryCoordinator]struct{}
}{byDatabase: make(map[string]map[*RecoveryCoordinator]struct{})}

func NewRecoveryCoordinator(sessionDir string, options RecoveryCoordinatorOptions) *RecoveryCoordinator {
	if options.ScanInterval <= 0 || options.ScanInterval > DefaultRecoveryScanInterval {
		options.ScanInterval = DefaultRecoveryScanInterval
	}
	if options.AttemptTimeout <= 0 || options.AttemptTimeout > DefaultRecoveryAttemptTimeout {
		options.AttemptTimeout = DefaultRecoveryAttemptTimeout
	}
	return &RecoveryCoordinator{sessionDir: sessionDir, options: options, wake: make(chan struct{}, 1)}
}

// Start performs the mandatory startup scan synchronously, then begins the
// periodic and wake-driven loop. A startup error is returned for diagnostics,
// while the coordinator remains active so transient failures can converge.
func (c *RecoveryCoordinator) Start(parent context.Context) error {
	if c == nil {
		return fmt.Errorf("recovery coordinator is nil")
	}
	if parent == nil {
		parent = context.Background()
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel
	c.done = make(chan struct{})
	c.started = true
	c.mu.Unlock()
	registerRecoveryCoordinator(c)

	_, startupErr := c.scan(ctx, "startup")
	go c.loop(ctx)
	return startupErr
}

func (c *RecoveryCoordinator) loop(ctx context.Context) {
	ticker := time.NewTicker(c.options.ScanInterval)
	defer func() {
		ticker.Stop()
		unregisterRecoveryCoordinator(c)
		c.mu.Lock()
		c.started = false
		close(c.done)
		c.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = c.scan(ctx, "periodic")
		case <-c.wake:
			_, _ = c.scan(ctx, "periodic")
		}
	}
}

func (c *RecoveryCoordinator) scan(ctx context.Context, trigger string) (RunRecoveryResult, error) {
	c.scanMu.Lock()
	defer c.scanMu.Unlock()
	if err := ctx.Err(); err != nil {
		return RunRecoveryResult{}, err
	}
	result, err := recoverOrphanedRunsWithTrigger(ctx, c.sessionDir, trigger, c.options.AttemptTimeout, c.options.Policy, c.options.BeforeFail)
	if err != nil {
		if c.options.OnError != nil {
			c.options.OnError(err)
		}
		return result, err
	}
	if c.options.OnResult != nil {
		c.options.OnResult(result)
	}
	return result, nil
}

// ScanNow runs the same shared recovery path used by startup and the ticker.
func (c *RecoveryCoordinator) ScanNow(ctx context.Context) (RunRecoveryResult, error) {
	if c == nil {
		return RunRecoveryResult{}, fmt.Errorf("recovery coordinator is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.scan(ctx, "periodic")
}

// Wake coalesces notifications; SQLite remains the authority when the next
// scan runs, so lost or duplicated wake-ups do not affect correctness.
func (c *RecoveryCoordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Stop terminates the loop and waits for an in-progress scan to return.
func (c *RecoveryCoordinator) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func registerRecoveryCoordinator(c *RecoveryCoordinator) {
	identity := recoveryDatabaseIdentity(c.sessionDir)
	recoveryCoordinators.Lock()
	entries := recoveryCoordinators.byDatabase[identity]
	if entries == nil {
		entries = make(map[*RecoveryCoordinator]struct{})
		recoveryCoordinators.byDatabase[identity] = entries
	}
	entries[c] = struct{}{}
	recoveryCoordinators.Unlock()
}

func unregisterRecoveryCoordinator(c *RecoveryCoordinator) {
	identity := recoveryDatabaseIdentity(c.sessionDir)
	recoveryCoordinators.Lock()
	delete(recoveryCoordinators.byDatabase[identity], c)
	if len(recoveryCoordinators.byDatabase[identity]) == 0 {
		delete(recoveryCoordinators.byDatabase, identity)
	}
	recoveryCoordinators.Unlock()
}

func wakeRecoveryCoordinators(sessionDir string) {
	identity := recoveryDatabaseIdentity(sessionDir)
	recoveryCoordinators.RLock()
	entries := make([]*RecoveryCoordinator, 0, len(recoveryCoordinators.byDatabase[identity]))
	for coordinator := range recoveryCoordinators.byDatabase[identity] {
		entries = append(entries, coordinator)
	}
	recoveryCoordinators.RUnlock()
	for _, coordinator := range entries {
		coordinator.Wake()
	}
}

func recoveryDatabaseIdentity(sessionDir string) string {
	return session.RuntimeDatabaseIdentity(sessionDir)
}
