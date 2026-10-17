package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oschina/mothx/internal/session"
)

var (
	ErrSubmissionLocalBusy    = errors.New("session has a local execution in progress")
	ErrSubmissionExternalBusy = errors.New("session execution is owned elsewhere")
	ErrSubmissionClosed       = errors.New("session submission coordinator is closed")
	ErrSubmissionCancelled    = errors.New("session submission was cancelled")
)

type SubmissionState string

const (
	SubmissionAccepted    SubmissionState = "accepted"
	SubmissionQueued      SubmissionState = "queued"
	SubmissionAdmitting   SubmissionState = "admitting"
	SubmissionRunning     SubmissionState = "running"
	SubmissionCompleting  SubmissionState = "completing"
	SubmissionCompleted   SubmissionState = "completed"
	SubmissionFailed      SubmissionState = "failed"
	SubmissionCancelled   SubmissionState = "cancelled"
	SubmissionInterrupted SubmissionState = "interrupted"
)

type SubmissionEvent struct {
	SubmissionID string
	RunID        string
	State        SubmissionState
	Error        error
}

// SubmitPolicy controls whether a request may wait behind an execution owned
// by this Runtime. External ownership is never queued by this coordinator.
type SubmitPolicy struct {
	QueueBehindLocal bool
	WaitForExternal  bool
}

// SubmissionAdmission acquires the admission lease. The coordinator owns the
// returned release function and invokes it only after the runner has returned.
// This keeps adapter code from becoming a second lease owner.
type SubmissionAdmission func(context.Context) (release func(), err error)

// SubmissionRunner performs one complete execution, including its durable
// terminal transition. It must return only after the execution completion
// barrier is ready to close.
type SubmissionRunner func(context.Context) error

type ExecutionCompletion struct {
	SubmissionID string
	RunID        string
	Error        error
}

// SubmissionAdmissionHandle represents a Runtime-owned admission that keeps
// its lease until Release is called by the execution owner. It lets synchronous
// adapters migrate to the coordinator without taking ownership of the queue or
// implementing their own Wait:true polling loop.
type SubmissionAdmissionHandle struct {
	Submission  *ExecutionHandle
	Ready       <-chan struct{}
	ready       chan struct{}
	release     chan struct{}
	releaseOnce sync.Once

	admissionDone chan struct{}
	admissionOnce sync.Once
	admissionErr  error
}

// Wait blocks until the submission acquires its admission lease or reaches a
// terminal state. A failed, cancelled, or interrupted submission returns that
// terminal error instead of leaving the caller blocked forever.
func (h *SubmissionAdmissionHandle) Wait(ctx context.Context) error {
	if h == nil {
		return ErrSubmissionClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-h.Ready:
		return nil
	case <-h.admissionDone:
		if h.admissionErr != nil {
			return h.admissionErr
		}
		return ErrSubmissionCancelled
	case <-ctx.Done():
		return ctx.Err()
	}
}

// failAdmission records the terminal admission outcome. It is called only when
// the runner was never started; a successfully admitted submission closes Ready
// instead. The error is written before the channel is closed, so waiters that
// observe the close also observe the value.
func (h *SubmissionAdmissionHandle) failAdmission(err error) {
	if h == nil {
		return
	}
	h.admissionOnce.Do(func() {
		if err == nil {
			err = ErrSubmissionCancelled
		}
		h.admissionErr = err
		close(h.admissionDone)
	})
}

func (h *SubmissionAdmissionHandle) Release() {
	if h == nil {
		return
	}
	h.releaseOnce.Do(func() { close(h.release) })
}

type ExecutionHandle struct {
	SubmissionID string
	RunID        string
	Completed    <-chan ExecutionCompletion
	Events       <-chan SubmissionEvent
	cancel       func()
	completed    chan ExecutionCompletion
	events       chan SubmissionEvent
	completeOnce sync.Once
	state        atomic.Value
}

// Cancel cancels a queued submission or requests cancellation of an active
// runner. The runner remains responsible for its durable terminal transition.
func (h *ExecutionHandle) Cancel() {
	if h != nil && h.cancel != nil {
		h.cancel()
	}
}

func (h *ExecutionHandle) State() SubmissionState {
	if h == nil {
		return SubmissionInterrupted
	}
	if state, ok := h.state.Load().(SubmissionState); ok && state != "" {
		return state
	}
	return SubmissionInterrupted
}

func (h *ExecutionHandle) setState(state SubmissionState, err error) {
	if h == nil {
		return
	}
	h.state.Store(state)
	select {
	case h.events <- SubmissionEvent{SubmissionID: h.SubmissionID, RunID: h.RunID, State: state, Error: err}:
	default:
	}
}

type submission struct {
	handle    *ExecutionHandle
	ctx       context.Context
	cancel    context.CancelFunc
	policy    SubmitPolicy
	admit     SubmissionAdmission
	run       SubmissionRunner
	admission *SubmissionAdmissionHandle
	admitted  atomic.Bool
}

func (s *submission) failAdmission(err error) {
	if s != nil && s.admission != nil {
		s.admission.failAdmission(err)
	}
}

// SubmissionCoordinator serializes submissions for one SessionRuntime. It is
// intentionally independent of Agent construction and Run persistence: those
// remain owned by the existing Runtime execution path.
type SubmissionCoordinator struct {
	mu             sync.Mutex
	closed         bool
	active         *submission
	queue          []*submission
	sequence       uint64
	closeCh        chan struct{}
	wg             sync.WaitGroup
	wake           chan struct{}
	identity       string
	session        string
	unsub          func()
	refs           int
	wakeSubscribed bool
}

var submissionWakeRegistry = struct {
	sync.Mutex
	entries map[string]map[*SubmissionCoordinator]struct{}
}{entries: make(map[string]map[*SubmissionCoordinator]struct{})}

var submissionWakeSubscription struct {
	sync.Mutex
	unsub func()
	refs  int
}

func retainSubmissionWakeSubscription() {
	submissionWakeSubscription.Lock()
	defer submissionWakeSubscription.Unlock()
	if submissionWakeSubscription.refs == 0 {
		submissionWakeSubscription.unsub = session.SubscribeRuntimeLeaseNotifications(func(notification session.RuntimeLeaseNotification) {
			if notification.SessionID == "" {
				return
			}
			submissionWakeRegistry.Lock()
			entries := make([]*SubmissionCoordinator, 0)
			for key, coordinators := range submissionWakeRegistry.entries {
				if notification.DatabaseIdentity != "" && key != submissionWakeKey(notification.DatabaseIdentity, notification.SessionID) {
					continue
				}
				for coordinator := range coordinators {
					if coordinator.session == notification.SessionID {
						entries = append(entries, coordinator)
					}
				}
			}
			submissionWakeRegistry.Unlock()
			for _, coordinator := range entries {
				coordinator.Wake()
			}
		})
	}
	submissionWakeSubscription.refs++
}

func releaseSubmissionWakeSubscription() {
	submissionWakeSubscription.Lock()
	if submissionWakeSubscription.refs > 0 {
		submissionWakeSubscription.refs--
	}
	if submissionWakeSubscription.refs == 0 && submissionWakeSubscription.unsub != nil {
		unsub := submissionWakeSubscription.unsub
		submissionWakeSubscription.unsub = nil
		submissionWakeSubscription.Unlock()
		unsub()
		return
	}
	submissionWakeSubscription.Unlock()
}

var closedSubmissionCoordinatorState struct {
	once  sync.Once
	value *SubmissionCoordinator
}

// closedSubmissionCoordinator is returned by an already closed SessionRuntime.
// It keeps every adapter's nil-free contract while making Submit/Acquire fail
// with ErrSubmissionClosed instead of reviving a new queue after shutdown.
func closedSubmissionCoordinator() *SubmissionCoordinator {
	closedSubmissionCoordinatorState.once.Do(func() {
		coordinator := NewSubmissionCoordinator()
		coordinator.Close()
		closedSubmissionCoordinatorState.value = coordinator
	})
	return closedSubmissionCoordinatorState.value
}

func NewSubmissionCoordinator(sessionArgs ...string) *SubmissionCoordinator {
	var sessionDir, sessionID string
	if len(sessionArgs) > 0 {
		sessionDir = sessionArgs[0]
	}
	if len(sessionArgs) > 1 {
		sessionID = sessionArgs[1]
	}
	c := &SubmissionCoordinator{
		closeCh:  make(chan struct{}),
		wake:     make(chan struct{}, 1),
		identity: session.RuntimeDatabaseIdentity(sessionDir),
		session:  sessionID,
		refs:     1,
	}
	if c.identity != "" && sessionID != "" {
		retainSubmissionWakeSubscription()
		c.wakeSubscribed = true
		submissionWakeRegistry.Lock()
		key := submissionWakeKey(c.identity, sessionID)
		if submissionWakeRegistry.entries[key] == nil {
			submissionWakeRegistry.entries[key] = make(map[*SubmissionCoordinator]struct{})
		}
		submissionWakeRegistry.entries[key][c] = struct{}{}
		submissionWakeRegistry.Unlock()
	}
	return c
}

var sharedSubmissionCoordinators = struct {
	sync.Mutex
	entries map[string]*SubmissionCoordinator
}{entries: make(map[string]*SubmissionCoordinator)}

func acquireSubmissionCoordinator(sessionDir, sessionID string) *SubmissionCoordinator {
	identity := session.RuntimeDatabaseIdentity(sessionDir)
	if identity == "" || sessionID == "" {
		return NewSubmissionCoordinator(sessionDir, sessionID)
	}
	key := submissionWakeKey(identity, sessionID)
	sharedSubmissionCoordinators.Lock()
	defer sharedSubmissionCoordinators.Unlock()
	if existing := sharedSubmissionCoordinators.entries[key]; existing != nil {
		existing.mu.Lock()
		if !existing.closed {
			existing.refs++
			existing.mu.Unlock()
			return existing
		}
		existing.mu.Unlock()
		delete(sharedSubmissionCoordinators.entries, key)
	}
	coordinator := NewSubmissionCoordinator(sessionDir, sessionID)
	sharedSubmissionCoordinators.entries[key] = coordinator
	return coordinator
}

func releaseSubmissionCoordinator(c *SubmissionCoordinator) {
	if c == nil || c.identity == "" || c.session == "" {
		if c != nil {
			c.Close()
		}
		return
	}
	key := submissionWakeKey(c.identity, c.session)
	sharedSubmissionCoordinators.Lock()
	c.mu.Lock()
	if c.refs > 0 {
		c.refs--
	}
	last := c.refs == 0
	c.mu.Unlock()
	if last {
		delete(sharedSubmissionCoordinators.entries, key)
	}
	sharedSubmissionCoordinators.Unlock()
	if last {
		c.Close()
	}
}

// AcquireExecutionSubmission is the synchronous adapter bridge for background
// executions that already own their Agent/Run lifecycle. The coordinator owns
// FIFO ordering, lease admission, external ownership waiting, and release;
// the caller only runs its existing durable execution between the returned
// admission and release functions.
func AcquireExecutionSubmission(ctx context.Context, sessionDir, sessionID, runID string, waitForExternal bool) (func(), error) {
	coordinator := acquireSubmissionCoordinator(sessionDir, sessionID)
	policy := SubmitPolicy{QueueBehindLocal: true, WaitForExternal: waitForExternal}
	admission, err := coordinator.Acquire(ctx, runID, policy, func(admissionCtx context.Context) (func(), error) {
		guard, acquireErr := AcquireExecutionAdmission(admissionCtx, sessionDir, sessionID, ExecutionAdmissionOptions{})
		if acquireErr != nil {
			return nil, acquireErr
		}
		return guard.Release, nil
	})
	if err != nil {
		releaseSubmissionCoordinator(coordinator)
		return nil, err
	}
	if err := admission.Wait(ctx); err != nil {
		admission.Release()
		releaseSubmissionCoordinator(coordinator)
		return nil, err
	}
	return func() {
		admission.Release()
		releaseSubmissionCoordinator(coordinator)
	}, nil
}

func submissionWakeKey(identity, sessionID string) string { return identity + "\x00" + sessionID }

// Wake is advisory. It never changes admission state; it only causes a
// waiting submission to re-read SQLite and retry its fenced admission.
func (c *SubmissionCoordinator) Wake() {
	if c == nil {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *SubmissionCoordinator) nextID() string {
	return fmt.Sprintf("submission_%d", atomic.AddUint64(&c.sequence, 1))
}

// Submit accepts a request and starts it immediately when the coordinator is
// idle. A request accepted behind a local execution is never dropped because
// the admission lease is temporarily busy.
func (c *SubmissionCoordinator) Submit(ctx context.Context, runID string, policy SubmitPolicy, admit SubmissionAdmission, run SubmissionRunner) (*ExecutionHandle, error) {
	return c.submit(ctx, runID, policy, admit, run, nil)
}

func (c *SubmissionCoordinator) submit(ctx context.Context, runID string, policy SubmitPolicy, admit SubmissionAdmission, run SubmissionRunner, admission *SubmissionAdmissionHandle) (*ExecutionHandle, error) {
	if c == nil {
		return nil, ErrSubmissionClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if admit == nil || run == nil {
		return nil, errors.New("submission admission and runner are required")
	}
	ctx, cancel := context.WithCancel(ctx)
	completed := make(chan ExecutionCompletion, 1)
	events := make(chan SubmissionEvent, 16)
	handle := &ExecutionHandle{SubmissionID: c.nextID(), RunID: runID, Completed: completed, Events: events, completed: completed, events: events}
	handle.setState(SubmissionAccepted, nil)
	s := &submission{handle: handle, ctx: ctx, cancel: cancel, policy: policy, admit: admit, run: run, admission: admission}
	handle.cancel = func() {
		cancel()
		c.cancelQueued(s)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		s.failAdmission(ErrSubmissionClosed)
		return nil, ErrSubmissionClosed
	}
	if c.active != nil && !policy.QueueBehindLocal {
		c.mu.Unlock()
		cancel()
		s.failAdmission(ErrSubmissionLocalBusy)
		return nil, ErrSubmissionLocalBusy
	}
	if c.active != nil {
		handle.setState(SubmissionQueued, nil)
		c.queue = append(c.queue, s)
		c.mu.Unlock()
		return handle, nil
	}
	c.active = s
	handle.setState(SubmissionAdmitting, nil)
	c.wg.Add(1)
	c.mu.Unlock()
	go c.execute(s)
	return handle, nil
}

// Acquire queues an admission and holds its lease until the returned handle is
// released. The caller may wait for Ready, perform its existing execution path,
// and then Release; all ordering and external ownership decisions stay in the
// shared coordinator.
func (c *SubmissionCoordinator) Acquire(ctx context.Context, runID string, policy SubmitPolicy, admit SubmissionAdmission) (*SubmissionAdmissionHandle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ready := make(chan struct{})
	release := make(chan struct{})
	admission := &SubmissionAdmissionHandle{
		Ready:         ready,
		ready:         ready,
		release:       release,
		admissionDone: make(chan struct{}),
	}
	submissionHandle, err := c.submit(ctx, runID, policy, admit, func(runCtx context.Context) error {
		close(ready)
		select {
		case <-release:
			return nil
		case <-runCtx.Done():
			return runCtx.Err()
		}
	}, admission)
	if err != nil {
		return nil, err
	}
	admission.Submission = submissionHandle
	return admission, nil
}

func (c *SubmissionCoordinator) cancelQueued(target *submission) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, item := range c.queue {
		if item != target {
			continue
		}
		c.queue = append(c.queue[:i], c.queue[i+1:]...)
		c.completeLocked(item, ErrSubmissionCancelled)
		return
	}
}

func (c *SubmissionCoordinator) execute(s *submission) {
	defer c.wg.Done()
	release, err := c.admit(s)
	if err == nil {
		s.admitted.Store(true)
		s.handle.setState(SubmissionRunning, nil)
		err = s.run(s.ctx)
	}
	s.handle.setState(SubmissionCompleting, err)
	if release != nil {
		release()
	}

	c.mu.Lock()
	c.completeLocked(s, err)
	if c.active == s {
		c.active = nil
	}
	if !c.closed && len(c.queue) > 0 {
		next := c.queue[0]
		c.queue = c.queue[1:]
		c.active = next
		c.wg.Add(1)
		go c.execute(next)
	}
	c.mu.Unlock()
}

func (c *SubmissionCoordinator) admit(s *submission) (func(), error) {
	for {
		release, err := s.admit(s.ctx)
		if err == nil {
			return release, nil
		}
		if !errors.Is(err, session.ErrRuntimeLeaseBusy) {
			return release, err
		}
		if !s.policy.WaitForExternal {
			return nil, ErrSubmissionExternalBusy
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-s.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, s.ctx.Err()
		case <-c.closeCh:
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ErrSubmissionClosed
		case <-c.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func (c *SubmissionCoordinator) completeLocked(s *submission, err error) {
	if s == nil || s.handle == nil {
		return
	}
	// A submission that never reached its runner has no Ready signal; report
	// its terminal admission error to a synchronous Acquire waiter instead.
	if !s.admitted.Load() {
		s.failAdmission(err)
	}
	s.handle.completeOnce.Do(func() {
		state := SubmissionCompleted
		if err != nil {
			state = SubmissionFailed
			if errors.Is(err, ErrSubmissionCancelled) || errors.Is(err, context.Canceled) {
				state = SubmissionCancelled
			}
		}
		s.handle.setState(state, err)
		select {
		case s.handle.completed <- ExecutionCompletion{SubmissionID: s.handle.SubmissionID, RunID: s.handle.RunID, Error: err}:
		default:
		}
		close(s.handle.completed)
		close(s.handle.events)
	})
}

// Close cancels the active runner and deterministically completes all queued
// submissions. The active runner is allowed to finish its own durable cleanup.
func (c *SubmissionCoordinator) Close() {
	c.close(context.Background())
}

// CloseContext cancels the active runner, completes queued submissions, and
// waits for the active runner only as long as ctx permits. The runner remains
// responsible for its own durable cleanup if the wait expires.
func (c *SubmissionCoordinator) CloseContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return c.close(ctx)
}

func (c *SubmissionCoordinator) close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	wakeSubscribed := c.wakeSubscribed
	c.wakeSubscribed = false
	close(c.closeCh)
	active := c.active
	queued := c.queue
	c.queue = nil
	for _, item := range queued {
		// The coordinator lock is held; cancel the context directly instead of
		// calling the public handle callback, which also scans the queue.
		item.cancel()
		c.completeLocked(item, ErrSubmissionCancelled)
	}
	if active != nil {
		active.cancel()
	}
	c.mu.Unlock()
	if c.identity != "" && c.session != "" {
		submissionWakeRegistry.Lock()
		key := submissionWakeKey(c.identity, c.session)
		if entries := submissionWakeRegistry.entries[key]; entries != nil {
			delete(entries, c)
			if len(entries) == 0 {
				delete(submissionWakeRegistry.entries, key)
			}
		}
		submissionWakeRegistry.Unlock()
	}
	if wakeSubscribed {
		releaseSubmissionWakeSubscription()
	}
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *SubmissionCoordinator) QueueLen() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}
