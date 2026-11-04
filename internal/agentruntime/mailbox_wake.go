package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

const (
	defaultMailboxWakeDebounce = 25 * time.Millisecond
	maxMailboxWakeDebounce     = 500 * time.Millisecond
)

// MailboxWakeRun is the immutable identity an adapter uses to attach its
// existing renderer to a Runtime-owned automatic continuation.
type MailboxWakeRun struct {
	RunID     string
	SessionID string
	Source    string
	Model     string
	Mode      string
	StartedAt time.Time
}

// MailboxWakeOptions installs adapter policy hooks and projections around the
// one Runtime-owned continuation path. Build's RunID, turn identity, and
// ownership fields are overwritten per wake; adapters only supply policy such
// as Allow/approval handlers.
type MailboxWakeOptions struct {
	Build      AgentBuildOptions
	OnStarted  func(MailboxWakeRun)
	OnEvent    func(MailboxWakeRun, agent.Event)
	OnArtifact func(MailboxWakeRun, SessionAttachment)
	// OnAgent publishes the freshly built lead Agent so an adapter can answer
	// Runtime-owned questions through its existing protocol hook. The Agent is
	// already registered on the shared ExecutionRuntime and is never owned by
	// the adapter.
	OnAgent func(MailboxWakeRun, *agent.Agent)
	// ConfigureBuild applies entry-point policy that must be read at wake time
	// (capabilities, approvals, adapter hooks). It runs after the Runtime has
	// overwritten the run identity/provider snapshot, so it cannot fork the
	// canonical Run facts.
	ConfigureBuild func(*AgentBuildOptions)
	OnFinished     func(MailboxWakeRun, RunState, error)
	// Debounce coalesces a burst of completions into one admission attempt.
	Debounce time.Duration
}

// EnableMailboxWake starts the Runtime-owned continuation scheduler. Pending
// mailbox items are drained only after submission admission; a burst observed
// before admission therefore becomes one lead Run carrying every item.
// Repeated calls while the scheduler is active are idempotent.
func (r *SessionRuntime) EnableMailboxWake(ctx context.Context, options MailboxWakeOptions) error {
	if err := r.ensureOpen(); err != nil {
		return err
	}
	if r.Mailbox == nil {
		return fmt.Errorf("session mailbox is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	debounce := options.Debounce
	if debounce <= 0 {
		debounce = defaultMailboxWakeDebounce
	}
	if debounce > maxMailboxWakeDebounce {
		debounce = maxMailboxWakeDebounce
	}
	options.Debounce = debounce

	r.mailboxWakeMu.Lock()
	if r.mailboxWakeCancel != nil {
		r.mailboxWakeMu.Unlock()
		return nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	removeListener := r.Mailbox.AddActivityListener(func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	r.mailboxWakeCancel = cancel
	r.mailboxWakeDone = done
	r.mailboxWakeMu.Unlock()
	if r.Mailbox.HasPending() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}

	go r.runMailboxWake(loopCtx, options, done, wake, removeListener)
	return nil
}

// DisableMailboxWake stops the scheduler and waits for its current admission
// attempt to unwind.
func (r *SessionRuntime) DisableMailboxWake() {
	_ = r.stopMailboxWake(context.Background())
}

func (r *SessionRuntime) cancelMailboxWake() {
	if r == nil {
		return
	}
	r.mailboxWakeMu.Lock()
	cancel := r.mailboxWakeCancel
	r.mailboxWakeCancel = nil
	r.mailboxWakeDone = nil
	r.mailboxWakeMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *SessionRuntime) stopMailboxWake(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mailboxWakeMu.Lock()
	cancel := r.mailboxWakeCancel
	done := r.mailboxWakeDone
	r.mailboxWakeCancel = nil
	r.mailboxWakeDone = nil
	r.mailboxWakeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *SessionRuntime) runMailboxWake(ctx context.Context, options MailboxWakeOptions, done chan struct{}, wake <-chan struct{}, removeListener func()) {
	defer close(done)
	defer removeListener()

	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
		if !waitMailboxWakeDebounce(ctx, options.Debounce) {
			return
		}
		if err := r.tryMailboxWake(ctx, options); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrSubmissionClosed) {
			log.Printf("[runtime] automatic mailbox continuation failed: %v", err)
		}
	}
}

func waitMailboxWakeDebounce(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *SessionRuntime) tryMailboxWake(ctx context.Context, options MailboxWakeOptions) error {
	if r == nil || r.Mailbox == nil || !r.Mailbox.HasPending() {
		return nil
	}
	sessionDir, sessionID := r.mailboxWakeSession()
	if sessionID == "" {
		return nil
	}
	snapshot, err := InspectSessionExecution(sessionDir, sessionID)
	if err != nil {
		return err
	}
	// A local Run keeps the session's mailbox as steering input; the
	// coordinator queues this continuation behind it. Remote/unknown ownership
	// must not be raced from this process.
	switch snapshot.State {
	case SessionExecutionIdle, SessionExecutionLocal:
	default:
		return nil
	}

	runID := "wake_" + session.GenerateID()
	coordinator := r.SubmissionCoordinator()
	if coordinator == nil {
		return ErrSubmissionClosed
	}
	handle, err := coordinator.Submit(ctx, runID, SubmitPolicy{QueueBehindLocal: true}, func(admissionCtx context.Context) (func(), error) {
		guard, acquireErr := AcquireExecutionAdmission(admissionCtx, sessionDir, sessionID, ExecutionAdmissionOptions{})
		if acquireErr != nil {
			return nil, acquireErr
		}
		return guard.Release, nil
	}, func(runCtx context.Context) error {
		return r.executeMailboxWake(runCtx, runID, options)
	})
	if err != nil {
		if errors.Is(err, ErrSubmissionLocalBusy) || errors.Is(err, ErrSubmissionExternalBusy) {
			return nil
		}
		return err
	}
	select {
	case completion := <-handle.Completed:
		return completion.Error
	case <-ctx.Done():
		handle.Cancel()
		return ctx.Err()
	}
}

func (r *SessionRuntime) executeMailboxWake(ctx context.Context, runID string, options MailboxWakeOptions) (runErr error) {
	// Re-check after admission: an active predecessor may already have drained
	// the mailbox, in which case this wake must not create an empty Run.
	if !r.Mailbox.HasPending() {
		return nil
	}
	pending := r.Mailbox.DrainSteering()
	if len(pending) == 0 {
		return nil
	}
	seedText := strings.TrimSpace(joinMailboxWakeMessages(pending))
	if seedText == "" {
		return nil
	}
	seed := provider.NewSystemInjectedUserMessage(seedText)

	_, sessionID := r.mailboxWakeSession()
	if sessionID == "" {
		return fmt.Errorf("mailbox continuation requires a bound session")
	}
	p, providerName, model, mode, thinking := r.ConfigSnapshot()
	if p == nil || model == nil {
		return fmt.Errorf("mailbox continuation requires a session provider and model")
	}
	settings := r.SettingsSnapshot()
	source := string(r.Source)
	if source == "" {
		source = "runtime"
	}
	startedAt := time.Now()
	turnID := "turn-" + runID
	runInfo := MailboxWakeRun{
		RunID: runID, SessionID: sessionID, Source: source,
		Model: model.ID, Mode: mode, StartedAt: startedAt,
	}

	execution, err := r.mailboxWakeExecution()
	if err != nil {
		return err
	}
	runCtx, err := execution.BeginDurable(ctx, DurableRun{
		ID: runID, SessionID: sessionID, WorkDir: r.WorkDir, Source: source,
		Model: model.ID, Mode: mode, Status: "running", StartedAt: startedAt,
		ConversationTurnID: turnID, ConversationTurn: true,
	}, RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "started", Source: source,
		Status: "running", Model: model.ID, Mode: mode, Timestamp: startedAt,
	})
	if err != nil {
		return err
	}

	finish := func(state RunState, cause error) error {
		message := ""
		if cause != nil {
			message = cause.Error()
		}
		finishErr := execution.FinishDurableWithRetry(context.Background(), runID, state, message, RunEvent{
			SessionID: sessionID, RunID: runID, EventType: "finished", Source: source,
			Status: string(state), Model: model.ID, Mode: mode, Timestamp: time.Now(),
		})
		if finishErr != nil && cause == nil {
			cause = finishErr
		}
		callMailboxWakeFinished(options.OnFinished, runInfo, state, cause)
		return finishErr
	}
	callMailboxWakeStarted(options.OnStarted, runInfo)

	collector, artifactErr := r.BeginArtifactCollection(runID)
	if artifactErr != nil {
		_ = finish(RunStateFailed, artifactErr)
		return artifactErr
	}
	if collector != nil {
		collector.SetObserver(func(record SessionAttachment) {
			callMailboxWakeArtifact(options.OnArtifact, runInfo, record)
		})
		defer collector.Close()
	}

	build := options.Build
	build.Provider = p
	build.ProviderName = providerName
	build.Model = model
	build.Mode = mode
	build.ThinkingLevel = thinking
	build.Settings = settings
	build.RunID = runID
	build.IntentID = ""
	build.ConversationTurnID = turnID
	build.ConversationTurn = true
	build.RuntimeOwnsTurnEnd = true
	build.RuntimeOwnsUserEntry = false
	build.UserEntryID = ""
	build.SyntheticContinuation = true
	build.AuxiliaryRole = false
	callMailboxWakeConfigure(options.ConfigureBuild, &build)

	a, buildErr := r.BuildAgent(build)
	if buildErr != nil {
		_ = finish(RunStateFailed, buildErr)
		return buildErr
	}
	execution.SetAgent(a)
	callMailboxWakeAgent(options.OnAgent, runInfo, a)

	terminal := agent.Event{}
	terminalSeen := false
	for ev := range a.RunWithUserMessage(runCtx, seed) {
		if ev.AgentID == "" || (ev.Type != agent.EventRunFinished && ev.Type != agent.EventError) {
			if _, observeErr := execution.ObserveAgentEvent(ev); observeErr != nil {
				log.Printf("[runtime] observe mailbox continuation event for %s: %v", runID, observeErr)
			}
		}
		callMailboxWakeEvent(options.OnEvent, runInfo, ev)
		if ev.AgentID != "" {
			continue
		}
		switch ev.Type {
		case agent.EventRunFinished:
			terminal = ev
			terminalSeen = true
		case agent.EventError:
			if !terminalSeen {
				terminal = ev
			}
		}
	}

	state, cause := mailboxWakeTerminalState(terminal, terminalSeen, runCtx)
	event := RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "finished", Source: source,
		Status: string(state), Model: model.ID, Mode: mode, Timestamp: time.Now(),
	}
	if terminal.AssistantEntryID != "" {
		event.AssistantEntryID = terminal.AssistantEntryID
	}
	if terminal.AssistantMessage.Role == "assistant" {
		event.AssistantMessage = terminal.AssistantMessage
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	finishErr := execution.FinishDurableWithRetry(context.Background(), runID, state, message, event)
	if finishErr != nil && cause == nil {
		cause = finishErr
	}
	callMailboxWakeFinished(options.OnFinished, runInfo, state, cause)
	return finishErr
}

func (r *SessionRuntime) mailboxWakeExecution() (*ExecutionRuntime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("agent runtime is closed")
	}
	if r.Execution == nil {
		execution := &ExecutionRuntime{}
		if r.Manager != nil {
			sessionDir := r.Manager.GetSessionDir()
			execution.SetRunStore(RunStore{SessionDir: sessionDir})
			execution.SetEventSink(SessionRunEventSink{SessionDir: sessionDir})
		}
		r.Execution = execution
	}
	return r.Execution, nil
}

func (r *SessionRuntime) mailboxWakeSession() (sessionDir, sessionID string) {
	if r == nil {
		return "", ""
	}
	r.mu.RLock()
	manager := r.Manager
	sessionID = r.ID
	r.mu.RUnlock()
	if manager != nil {
		sessionDir = manager.GetSessionDir()
		if header := manager.GetHeader(); header != nil && header.ID != "" {
			sessionID = header.ID
		}
	}
	return sessionDir, sessionID
}

func joinMailboxWakeMessages(messages []provider.Message) string {
	var b strings.Builder
	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(text)
	}
	return b.String()
}

func mailboxWakeTerminalState(terminal agent.Event, seen bool, runCtx context.Context) (RunState, error) {
	if !seen && terminal.Type == agent.EventError && terminal.Error != nil {
		return RunStateFailed, terminal.Error
	}
	if !seen {
		if err := runCtx.Err(); err != nil {
			return RunStateCancelled, err
		}
		return RunStateFailed, fmt.Errorf("mailbox continuation event stream closed without a terminal result")
	}
	switch terminal.Status {
	case agent.TaskFailed:
		if terminal.Error != nil {
			return RunStateFailed, terminal.Error
		}
		return RunStateFailed, fmt.Errorf("mailbox continuation failed")
	case agent.TaskCanceled:
		return RunStateCancelled, nil
	case agent.TaskIncomplete:
		return RunStateIncomplete, terminal.Error
	default:
		return RunStateCompleted, terminal.Error
	}
}

func callMailboxWakeStarted(callback func(MailboxWakeRun), run MailboxWakeRun) {
	if callback == nil {
		return
	}
	defer func() { _ = recover() }()
	callback(run)
}

func callMailboxWakeEvent(callback func(MailboxWakeRun, agent.Event), run MailboxWakeRun, ev agent.Event) {
	if callback == nil {
		return
	}
	defer func() { _ = recover() }()
	callback(run, ev)
}

func callMailboxWakeArtifact(callback func(MailboxWakeRun, SessionAttachment), run MailboxWakeRun, record SessionAttachment) {
	if callback == nil {
		return
	}
	defer func() { _ = recover() }()
	callback(run, record)
}

func callMailboxWakeAgent(callback func(MailboxWakeRun, *agent.Agent), run MailboxWakeRun, a *agent.Agent) {
	if callback == nil {
		return
	}
	defer func() { _ = recover() }()
	callback(run, a)
}

func callMailboxWakeConfigure(callback func(*AgentBuildOptions), build *AgentBuildOptions) {
	if callback == nil {
		return
	}
	defer func() { _ = recover() }()
	callback(build)
}

func callMailboxWakeFinished(callback func(MailboxWakeRun, RunState, error), run MailboxWakeRun, state RunState, err error) {
	if callback == nil {
		return
	}
	defer func() { _ = recover() }()
	callback(run, state, err)
}
