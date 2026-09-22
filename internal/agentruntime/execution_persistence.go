package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oschina/mothx/internal/session"
)

const (
	DefaultTerminalPersistenceTimeout = 10 * time.Second
	terminalPersistenceRetryInitial   = 50 * time.Millisecond
	terminalPersistenceRetryMaximum   = 500 * time.Millisecond
)

// BeginDurable starts an exclusive in-memory execution, creates its canonical
// durable run row, and records the initial event. Failures are compensated so a
// partially-started run is not left active or persisted as running.
func (r *ExecutionRuntime) BeginDurable(parent context.Context, run DurableRun, event RunEvent) (context.Context, error) {
	if run.ID == "" || run.SessionID == "" {
		return nil, fmt.Errorf("durable run ID and session ID are required")
	}
	store := r.runStore()
	if store == nil {
		return nil, fmt.Errorf("execution run store is not configured")
	}
	// A production store must admit the Run and its replay anchor in one
	// transaction. Keep the non-atomic callback only for embedded stores that
	// predate the extended lifecycle interface.
	if atomicStore, ok := store.(DurableRunEventStore); ok {
		return r.beginDurableWithStart(parent, run, event, "create durable run and start event", func() error {
			return nil
		}, func(startEvent RunEvent) (string, error) {
			if run.ConversationTurn {
				if turnStore, turnOK := store.(interface {
					CreateRunWithEventAndTurn(DurableRun, RunEvent) (string, error)
				}); turnOK {
					return turnStore.CreateRunWithEventAndTurn(withConversationTurnID(run), startEvent)
				}
			}
			return atomicStore.CreateRunWithEvent(run, startEvent)
		})
	}
	return r.beginDurable(parent, run, event, "create durable run", func() error {
		return store.Create(run)
	})
}

// BeginIntentDurable atomically admits the immutable original request and its
// first durable Run. A user-initiated retry must use BeginRetryDurable instead,
// which creates a linked Run without mutating the accepted intent.
func (r *ExecutionRuntime) BeginIntentDurable(parent context.Context, intent ExecutionIntent, run DurableRun, event RunEvent) (context.Context, error) {
	if intent.ID == "" || intent.SessionID == "" {
		return nil, fmt.Errorf("execution intent ID and session ID are required")
	}
	if run.ID == "" || run.SessionID == "" {
		return nil, fmt.Errorf("durable run ID and session ID are required")
	}
	if intent.SessionID != run.SessionID {
		return nil, fmt.Errorf("execution intent and durable run must belong to the same session")
	}
	if run.IntentID == "" {
		run.IntentID = intent.ID
	}
	if run.IntentID != intent.ID {
		return nil, fmt.Errorf("durable run intent ID does not match execution intent")
	}
	store, ok := r.runStore().(DurableIntentStore)
	if !ok || store == nil {
		return nil, fmt.Errorf("execution intent store is not configured")
	}
	if atomicStore, ok := store.(DurableIntentEventStore); ok {
		return r.beginDurableWithStart(parent, run, event, "create durable intent, run, and start event", func() error {
			return nil
		}, func(startEvent RunEvent) (string, error) {
			if run.ConversationTurn {
				if turnStore, turnOK := store.(interface {
					CreateIntentAndRunWithEventAndTurn(ExecutionIntent, DurableRun, RunEvent) (string, error)
				}); turnOK {
					return turnStore.CreateIntentAndRunWithEventAndTurn(intent, withConversationTurnID(run), startEvent)
				}
			}
			return atomicStore.CreateIntentAndRunWithEvent(intent, run, startEvent)
		})
	}
	return r.beginDurable(parent, run, event, "create durable intent and run", func() error {
		return store.CreateIntentAndRun(intent, run)
	})
}

// BeginRetryDurable creates a new linked attempt for an existing immutable
// execution intent. It never reopens a terminal Run, preserving the attempt
// chain for all adapters and after process restart.
func (r *ExecutionRuntime) BeginRetryDurable(parent context.Context, run DurableRun, event RunEvent) (*ExecutionIntent, context.Context, error) {
	if run.ID == "" || run.SessionID == "" || run.IntentID == "" {
		return nil, nil, fmt.Errorf("durable retry requires run ID, session ID, and intent ID")
	}
	if run.RetryOf == "" {
		return nil, nil, fmt.Errorf("durable retry requires prior run ID")
	}
	if run.Attempt < 2 {
		return nil, nil, fmt.Errorf("durable retry attempt must be at least 2")
	}
	store, ok := r.runStore().(DurableIntentStore)
	if !ok || store == nil {
		return nil, nil, fmt.Errorf("execution intent store is not configured")
	}
	intent, err := store.GetIntent(run.IntentID)
	if err != nil {
		return nil, nil, fmt.Errorf("load execution intent: %w", err)
	}
	if intent == nil {
		return nil, nil, fmt.Errorf("execution intent not found: %s", run.IntentID)
	}
	if intent.SessionID != run.SessionID {
		return nil, nil, fmt.Errorf("execution intent and durable retry must belong to the same session")
	}
	if atomicStore, ok := r.runStore().(DurableRunEventStore); ok {
		ctx, err := r.beginDurableWithStart(parent, run, event, "create durable retry run and start event", func() error {
			return nil
		}, func(startEvent RunEvent) (string, error) {
			if run.ConversationTurn {
				if turnStore, turnOK := r.runStore().(interface {
					CreateRunWithEventAndTurn(DurableRun, RunEvent) (string, error)
				}); turnOK {
					return turnStore.CreateRunWithEventAndTurn(withConversationTurnID(run), startEvent)
				}
			}
			return atomicStore.CreateRunWithEvent(run, startEvent)
		})
		if err != nil {
			return nil, nil, err
		}
		return intent, ctx, nil
	}
	ctx, err := r.BeginDurable(parent, run, event)
	if err != nil {
		return nil, nil, err
	}
	return intent, ctx, nil
}

func withConversationTurnID(run DurableRun) DurableRun {
	if run.ConversationTurn && run.ConversationTurnID == "" {
		run.ConversationTurnID = "turn-" + run.ID
	}
	return run
}

func (r *ExecutionRuntime) beginDurable(parent context.Context, run DurableRun, event RunEvent, operation string, create func() error) (context.Context, error) {
	return r.beginDurableWithStart(parent, run, event, operation, create, nil)
}

func (r *ExecutionRuntime) beginDurableWithStart(parent context.Context, run DurableRun, event RunEvent, operation string, create func() error, atomicStart func(RunEvent) (string, error)) (context.Context, error) {
	if create == nil {
		return nil, fmt.Errorf("durable run create operation is required")
	}
	store := r.runStore()
	if store == nil {
		return nil, fmt.Errorf("execution run store is not configured")
	}
	run = withConversationTurnID(run)
	ctx, err := r.Begin(parent, run.ID)
	if err != nil {
		return nil, err
	}
	if leaseStore, ok := store.(interface{ LeaseLost(string) <-chan struct{} }); ok {
		r.mu.Lock()
		done := r.done
		r.mu.Unlock()
		r.watchLeaseLost(done, leaseStore.LeaseLost(run.SessionID))
	}
	r.mu.Lock()
	runCopy := run
	r.durable = &runCopy
	r.durablePersisted = false
	r.startEvent = event
	r.mu.Unlock()
	event.SessionID = run.SessionID
	event.RunID = run.ID
	event.Data = withRunAttemptData(event.Data, run)
	if event.Status == "" {
		event.Status = string(RunStateRunning)
	}
	if atomicStart != nil {
		startID, err := atomicStart(event)
		if err != nil {
			_ = r.FinishWithState(run.ID, RunStateFailed)
			return nil, fmt.Errorf("%s: %w", operation, err)
		}
		event.ID = startID
		r.mu.Lock()
		if r.activeLocked(run.ID) {
			r.durablePersisted = true
			r.startEvent = event
		}
		r.mu.Unlock()
		if err := r.registerLocalExecution(run); err != nil {
			return nil, r.failDurableRegistration(run, err)
		}
		if projector, ok := r.eventSink().(RunEventProjector); ok {
			_ = projector.Project(event, event.ID)
		}
		session.NotifyRuntimeStateChanged(run.SessionID, run.Source)
		return ctx, nil
	}
	if err := create(); err != nil {
		_ = r.FinishWithState(run.ID, RunStateFailed)
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	r.mu.Lock()
	if r.activeLocked(run.ID) {
		r.durablePersisted = true
	}
	r.mu.Unlock()
	r.mu.Lock()
	if r.activeLocked(run.ID) {
		r.startEvent = event
	}
	r.mu.Unlock()
	if err := r.registerLocalExecution(run); err != nil {
		return nil, r.failDurableRegistration(run, err)
	}
	if _, err := r.RecordEvent(event); err != nil {
		message := "record run start event: " + err.Error()
		// The start event failure must not leave either an active in-memory run or
		// a running durable row. Try the canonical terminal path first; if the
		// same failing sink prevents that path from completing, force the
		// in-memory transition and finish the row as a best effort.
		if finishErr := r.FinishDurable(run.ID, RunStateFailed, message, RunEvent{
			SessionID: run.SessionID, RunID: run.ID, EventType: "failed",
			Source: run.Source, Status: string(RunStateFailed), Model: run.Model,
			Mode: run.Mode, Timestamp: time.Now(),
		}); finishErr != nil {
			_ = store.Finish(run.ID, RunStateFailed, message)
			r.transitionMu.Lock()
			_, _ = r.finishInMemory(run.ID, RunStateFailed, true)
			r.transitionMu.Unlock()
		}
		return nil, fmt.Errorf("record run start event: %w", err)
	}
	session.NotifyRuntimeStateChanged(run.SessionID, run.Source)
	return ctx, nil
}

func (r *ExecutionRuntime) failDurableRegistration(run DurableRun, registrationErr error) error {
	message := "register local execution binding: " + registrationErr.Error()
	finishErr := r.FinishDurable(run.ID, RunStateFailed, message, RunEvent{
		SessionID: run.SessionID, RunID: run.ID, EventType: "failed",
		Source: run.Source, Status: string(RunStateFailed), Model: run.Model,
		Mode: run.Mode, Timestamp: time.Now(),
	})
	if finishErr != nil {
		r.Cancel()
		return fmt.Errorf("%s (terminalize failed: %v)", message, finishErr)
	}
	return fmt.Errorf("%s", message)
}

// ReattachDurable restores in-memory ownership of an already persisted,
// non-terminal Run without creating a duplicate canonical row.
func (r *ExecutionRuntime) ReattachDurable(parent context.Context, runID string, state RunState) (context.Context, error) {
	if r == nil {
		return nil, fmt.Errorf("execution runtime is nil")
	}
	if runID == "" {
		return nil, fmt.Errorf("run ID is required")
	}
	if r.runStore() == nil {
		return nil, fmt.Errorf("execution run store is not configured")
	}
	if state == "" {
		state = RunStateRunning
	}
	if isTerminalRunState(state) {
		return nil, fmt.Errorf("cannot reattach terminal execution state: %s", state)
	}
	ctx, err := r.Begin(parent, runID)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.state = state
	r.durable = &DurableRun{ID: runID, Status: string(state)}
	r.durablePersisted = true
	r.mu.Unlock()
	return ctx, nil
}

// ReattachDurableRun restores an existing row with its full identity. The
// metadata is required so a later shutdown or FinishWithState can emit a
// valid terminal event without relying on adapter-local fallbacks.
func (r *ExecutionRuntime) ReattachDurableRun(parent context.Context, run DurableRun, state RunState, startEvent RunEvent) (context.Context, error) {
	if r == nil {
		return nil, fmt.Errorf("execution runtime is nil")
	}
	if run.ID == "" || run.SessionID == "" {
		return nil, fmt.Errorf("durable run ID and session ID are required")
	}
	store := r.runStore()
	if store == nil {
		return nil, fmt.Errorf("execution run store is not configured")
	}
	if state == "" {
		state = RunStateRunning
	}
	if isTerminalRunState(state) {
		return nil, fmt.Errorf("cannot reattach terminal execution state: %s", state)
	}
	ctx, err := r.Begin(parent, run.ID)
	if err != nil {
		return nil, err
	}
	if ownershipStore, ok := store.(durableExecutionOwnershipStore); ok {
		if err := ownershipStore.PrepareExistingExecution(run.SessionID, run.ID); err != nil {
			_, _ = r.finishInMemory(run.ID, RunStateFailed, true)
			return nil, fmt.Errorf("prepare durable execution reattach: %w", err)
		}
	}
	if run.Status == "" {
		run.Status = string(state)
	}
	if startEvent.SessionID == "" {
		startEvent.SessionID = run.SessionID
	}
	if startEvent.RunID == "" {
		startEvent.RunID = run.ID
	}
	if startEvent.Source == "" {
		startEvent.Source = run.Source
	}
	if startEvent.Model == "" {
		startEvent.Model = run.Model
	}
	if startEvent.Mode == "" {
		startEvent.Mode = run.Mode
	}
	r.mu.Lock()
	r.state = state
	r.durable = &run
	r.durablePersisted = true
	r.startEvent = startEvent
	r.mu.Unlock()
	if leaseStore, ok := store.(interface{ LeaseLost(string) <-chan struct{} }); ok {
		r.mu.Lock()
		done := r.done
		r.mu.Unlock()
		r.watchLeaseLost(done, leaseStore.LeaseLost(run.SessionID))
	}
	if err := r.registerLocalExecution(run); err != nil {
		_, _ = r.finishInMemory(run.ID, RunStateFailed, true)
		return nil, fmt.Errorf("register reattached execution: %w", err)
	}
	return ctx, nil
}

// UpdateDurable persists a non-terminal state for the active canonical run.
func (r *ExecutionRuntime) UpdateDurable(runID string, state RunState, message string) error {
	if r == nil {
		return fmt.Errorf("execution runtime is nil")
	}
	if isTerminalRunState(state) || state == "" {
		return fmt.Errorf("execution non-terminal state is invalid: %s", state)
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	activeID, active := r.Active()
	if !active || activeID != runID {
		return fmt.Errorf("execution is not active: %s", runID)
	}
	store := r.runStore()
	if store == nil {
		return fmt.Errorf("execution run store is not configured")
	}
	r.mu.Lock()
	previous := r.state
	r.mu.Unlock()
	if err := store.Update(runID, state, message); err != nil {
		return fmt.Errorf("persist run update: %w", err)
	}
	r.mu.Lock()
	if r.activeLocked(runID) {
		r.state = state
	} else if r.state != state {
		r.mu.Unlock()
		return fmt.Errorf("execution ended while updating %s (previous state %s)", state, previous)
	}
	r.mu.Unlock()
	return nil
}

// RecordUsage persists the latest provider and context-window usage for the
// active Run. Usage is durable metadata, not a terminal-event-only projection,
// so reconnects and recovery can inspect it before terminalization.
func (r *ExecutionRuntime) RecordUsage(runID string, usage, contextUsage json.RawMessage) error {
	if r == nil {
		return fmt.Errorf("execution runtime is nil")
	}
	activeID, active := r.Active()
	if !active || activeID != runID {
		return fmt.Errorf("execution is not active: %s", runID)
	}
	store, ok := r.runStore().(DurableRunUsageStore)
	if !ok || store == nil {
		return fmt.Errorf("execution run metadata store is not configured")
	}
	if err := store.UpdateUsage(runID, usage, contextUsage); err != nil {
		return fmt.Errorf("persist run usage: %w", err)
	}
	r.mu.Lock()
	if r.durable != nil && r.activeLocked(runID) {
		r.durable.Usage = append(json.RawMessage(nil), usage...)
		r.durable.ContextUsage = append(json.RawMessage(nil), contextUsage...)
	}
	r.mu.Unlock()
	return nil
}

// CancelDurable requests cancellation and persists the canonical cancelling
// state. Final terminalization remains the responsibility of FinishDurable.
func (r *ExecutionRuntime) CancelDurable(message string) (bool, error) {
	store := r.runStore()
	if store == nil {
		return false, fmt.Errorf("execution run store is not configured")
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	runID, active := r.Active()
	if !active {
		return false, nil
	}
	if err := store.Update(runID, RunStateCancelling, message); err != nil {
		return false, fmt.Errorf("persist run cancellation: %w", err)
	}
	r.notifyDurableStateChanged()
	if !r.Cancel() {
		return false, nil
	}
	return true, nil
}

// FinishDurable performs one canonical terminal transition, records its final
// event, and updates the durable run row. Persistence happens before releasing
// in-memory ownership so failed writes remain retryable and concurrent finishers
// cannot emit duplicate terminal events.
func (r *ExecutionRuntime) FinishDurable(runID string, state RunState, message string, event RunEvent) error {
	if r == nil {
		return fmt.Errorf("execution runtime is nil")
	}
	if !isTerminalRunState(state) {
		return fmt.Errorf("execution terminal state is invalid: %s", state)
	}
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	return r.finishDurableLocked(runID, state, message, event)
}

// FinishDurableWithRetry keeps the execution lease and local registration
// active while a transient terminal write is retried. The supplied context is
// bounded to the Runtime maximum so adapters cannot invent divergent retry
// policies or silently clear their projection after the first storage error.
func (r *ExecutionRuntime) FinishDurableWithRetry(ctx context.Context, runID string, state RunState, message string, event RunEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > DefaultTerminalPersistenceTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTerminalPersistenceTimeout)
		defer cancel()
	}
	delay := terminalPersistenceRetryInitial
	var lastErr error
	for {
		if err := r.FinishDurable(runID, state, message, event); err == nil {
			return nil
		} else {
			lastErr = err
			if errors.Is(err, session.ErrRuntimeLeaseLost) || !r.canRetryTerminalPersistence(runID, state) {
				return err
			}
		}
		lost := (<-chan struct{})(nil)
		if store, ok := r.runStore().(interface{ LeaseLost(string) <-chan struct{} }); ok {
			r.mu.Lock()
			sessionID := ""
			if r.durable != nil {
				sessionID = r.durable.SessionID
			}
			r.mu.Unlock()
			lost = store.LeaseLost(sessionID)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			// The bounded foreground attempt is only a responsiveness limit. The
			// Runtime keeps the execution lease/registration alive and continues
			// retrying in the background until the fenced terminal transaction
			// succeeds or the lease is lost.
			r.startTerminalPersistenceRetry(runID, state, message, event)
			return errors.Join(lastErr, ctx.Err())
		case <-lost:
			if !timer.Stop() {
				<-timer.C
			}
			return errors.Join(lastErr, session.ErrRuntimeLeaseLost)
		case <-timer.C:
		}
		if delay < terminalPersistenceRetryMaximum {
			delay *= 2
			if delay > terminalPersistenceRetryMaximum {
				delay = terminalPersistenceRetryMaximum
			}
		}
	}
}

func (r *ExecutionRuntime) canRetryTerminalPersistence(runID string, state RunState) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeLocked(runID) && r.terminalEventSet && r.terminalState == state
}

// startTerminalPersistenceRetry launches at most one Runtime-owned retry loop
// for a terminal transition that outlived its foreground context. Keeping this
// loop in agentruntime prevents each adapter from inventing its own finalizer
// or releasing a still-authoritative execution lease too early.
func (r *ExecutionRuntime) startTerminalPersistenceRetry(runID string, state RunState, message string, event RunEvent) {
	if r == nil {
		return
	}
	event.Data = append(json.RawMessage(nil), event.Data...)
	r.mu.Lock()
	if r.terminalRetryRunning || !r.activeLocked(runID) || !r.terminalEventSet || r.terminalState != state {
		r.mu.Unlock()
		return
	}
	r.terminalRetryRunning = true
	done := r.done
	store := r.store
	sessionID := ""
	if r.durable != nil {
		sessionID = r.durable.SessionID
	}
	r.mu.Unlock()
	var lost <-chan struct{}
	if leaseStore, ok := store.(interface{ LeaseLost(string) <-chan struct{} }); ok && sessionID != "" {
		lost = leaseStore.LeaseLost(sessionID)
	}
	go func() {
		defer func() {
			r.mu.Lock()
			r.terminalRetryRunning = false
			r.mu.Unlock()
		}()
		delay := terminalPersistenceRetryInitial
		for {
			if !r.canRetryTerminalPersistence(runID, state) {
				return
			}
			if err := r.FinishDurable(runID, state, message, event); err == nil {
				return
			} else if errors.Is(err, session.ErrRuntimeLeaseLost) || !r.canRetryTerminalPersistence(runID, state) {
				return
			}
			timer := time.NewTimer(delay)
			select {
			case <-done:
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-lost:
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
			if delay < terminalPersistenceRetryMaximum {
				delay *= 2
				if delay > terminalPersistenceRetryMaximum {
					delay = terminalPersistenceRetryMaximum
				}
			}
		}
	}()
}

// finishDurableLocked is the single durable terminal transition owner. The
// caller must hold transitionMu; keeping the lock across event/store I/O
// prevents a competing adapter callback from selecting another terminal state.
func (r *ExecutionRuntime) finishDurableLocked(runID string, state RunState, message string, event RunEvent) error {
	r.mu.Lock()
	if !r.activeLocked(runID) {
		finished := r.finished && r.runID == runID
		terminalState, terminalErr := r.state, r.terminalErr
		r.mu.Unlock()
		if finished && terminalState == state && terminalErr == nil {
			return nil
		}
		return fmt.Errorf("execution is not active: %s", runID)
	}
	if r.terminalEventSet && r.terminalState != state {
		selected := r.terminalState
		r.mu.Unlock()
		return fmt.Errorf("execution terminal state already selected: %s", selected)
	}
	r.mu.Unlock()

	// Normalize and persist the event before the row becomes terminal. The
	// event identity is retained across retries, so a transient row failure
	// does not append duplicate terminal events.
	event.RunID = runID
	if event.ID == "" {
		event.ID = session.RunTerminalEventID(runID, event.EventType)
	}
	if event.Status == "" {
		event.Status = string(state)
	}
	var (
		durableRun   DurableRun
		terminalInfo ErrorInfo
	)
	r.mu.Lock()
	if !r.terminalEventSet {
		durable := r.durable
		startEvent := r.startEvent
		if durable != nil {
			if event.SessionID == "" {
				event.SessionID = durable.SessionID
			}
			if event.Source == "" {
				event.Source = durable.Source
			}
			if event.Model == "" {
				event.Model = durable.Model
			}
			if event.Mode == "" {
				event.Mode = durable.Mode
			}
		}
		if event.SessionID == "" {
			event.SessionID = startEvent.SessionID
		}
		if event.Source == "" {
			event.Source = startEvent.Source
		}
		if event.Model == "" {
			event.Model = startEvent.Model
		}
		if event.Mode == "" {
			event.Mode = startEvent.Mode
		}
		if durable != nil {
			durableRun = *durable
			if event.AssistantMessage.Role == "assistant" {
				if event.AssistantEntryID == "" {
					event.AssistantEntryID = session.RunAssistantEntryID(runID)
				}
				durableRun.AssistantEntryID = event.AssistantEntryID
				message := event.AssistantMessage
				durableRun.AssistantMessage = &message
				durable.AssistantEntryID = event.AssistantEntryID
				durable.AssistantMessage = &message
			}
			event.Data = withRunAttemptData(event.Data, *durable)
			event.Data = withAssistantEntryData(event.Data, durableRun.AssistantEntryID)
		}
		if state != RunStateCompleted {
			terminalInfo = terminalErrorInfoFor(state, message, r.facts, durableRun)
			message = terminalInfo.Message
			event.Data = withTerminalErrorInfo(event.Data, terminalInfo)
			r.facts.lastError = terminalInfo
			r.terminalErrorInfo = terminalInfo
		}
		r.terminalEvent = event
		r.terminalEventSet = true
		r.terminalState = state
		r.terminalMessage = message
	} else {
		event = r.terminalEvent
		if r.durable != nil {
			durableRun = *r.durable
		}
		if r.terminalErrorInfo.Code != "" {
			terminalInfo = r.terminalErrorInfo
			message = terminalInfo.Message
		} else {
			message = r.terminalMessage
		}
	}
	prepared := r.terminalPrepared
	r.terminalizing = true
	r.terminalErr = nil
	recorded := r.terminalEventRecorded
	r.mu.Unlock()
	store := r.runStore()
	if !prepared {
		if terminalStore, ok := store.(DurableTerminalPersistenceStore); ok {
			if err := terminalStore.MarkTerminalizing(runID, message); err != nil {
				r.finishTerminalAttempt(err)
				return fmt.Errorf("mark durable run terminalizing: %w", err)
			}
		}
		r.mu.Lock()
		r.terminalPrepared = true
		if r.activeLocked(runID) {
			r.state = RunStateTerminalizing
		}
		r.mu.Unlock()
		r.notifyDurableStateChanged()
	}
	atomicFinisher, atomicFinish := store.(DurableConversationTurnEventFinisher)
	atomicFinish = atomicFinish && durableRun.ConversationTurn

	if !recorded && !atomicFinish {
		if sink := r.eventSink(); sink != nil {
			id, err := sink.Record(event)
			if err != nil {
				r.finishTerminalAttempt(err)
				return fmt.Errorf("record run terminal event: %w", err)
			}
			r.mu.Lock()
			if r.terminalEvent.ID == "" {
				r.terminalEvent.ID = id
			}
			r.terminalEventRecorded = true
			r.mu.Unlock()
		} else {
			r.mu.Lock()
			r.terminalEventRecorded = true
			r.mu.Unlock()
		}
	}
	if terminalInfo.Code != "" {
		if err := r.persistErrorInfo(durableRun, terminalInfo); err != nil {
			r.finishTerminalAttempt(err)
			return fmt.Errorf("persist run terminal error: %w", err)
		}
	}
	if err := r.clearRetryProgress(durableRun); err != nil {
		r.finishTerminalAttempt(err)
		return fmt.Errorf("clear run retry progress: %w", err)
	}
	if store == nil {
		err := fmt.Errorf("execution run store is not configured")
		r.finishTerminalAttempt(err)
		return err
	}
	if atomicFinish {
		id, err := atomicFinisher.FinishRunAndConversationTurn(durableRun, state, message, event)
		if err != nil {
			r.finishTerminalAttempt(err)
			return fmt.Errorf("finish durable run and conversation turn: %w", err)
		}
		r.mu.Lock()
		if r.terminalEvent.ID == "" {
			r.terminalEvent.ID = id
		}
		r.terminalEventRecorded = true
		r.mu.Unlock()
		if projector, ok := r.eventSink().(RunEventProjector); ok {
			_ = projector.Project(event, id)
		}
	} else if turnStore, ok := store.(DurableConversationTurnFinisher); ok && durableRun.ConversationTurn {
		if err := turnStore.FinishConversationTurn(durableRun, state, message); err != nil && !errors.Is(err, session.ErrConversationTurnNotOpen) {
			r.finishTerminalAttempt(err)
			return fmt.Errorf("finish conversation turn: %w", err)
		}
		if err := store.Finish(runID, state, message); err != nil {
			r.finishTerminalAttempt(err)
			return fmt.Errorf("finish durable run: %w", err)
		}
	} else {
		if err := store.Finish(runID, state, message); err != nil {
			r.finishTerminalAttempt(err)
			return fmt.Errorf("finish durable run: %w", err)
		}
	}
	done, err := r.finishInMemory(runID, state, false)
	if err != nil {
		r.finishTerminalAttempt(err)
		return err
	}
	r.mu.Lock()
	r.terminalErr = nil
	r.terminalizing = false
	wait := r.terminalDone
	r.terminalDone = nil
	if wait != nil {
		close(wait)
	}
	r.mu.Unlock()
	r.closeDone(done)
	if durableRun.SessionID != "" {
		session.NotifyRuntimeStateChanged(durableRun.SessionID, durableRun.Source)
	}
	r.notifyTerminalObserver(runID, state)
	return nil
}

func (r *ExecutionRuntime) finishTerminalAttempt(err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.terminalErr = err
	r.terminalizing = false
	wait := r.terminalDone
	r.terminalDone = nil
	if wait != nil {
		close(wait)
	}
	r.mu.Unlock()
}
