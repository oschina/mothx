package tui

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
)

func TestTUIRunLifecycle(t *testing.T) {
	run := newTUIRun()
	if run == nil || run.id == "" {
		t.Fatal("newTUIRun returned an incomplete run")
	}

	ctx, err := run.execution.Begin(context.Background(), run.id)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if ctx == nil {
		t.Fatal("Begin() returned nil context")
	}
	if got := run.execution.State(); got != agentruntime.RunStateRunning {
		t.Fatalf("State() = %q, want running", got)
	}

	run.finish(agentruntime.RunStateFailed)
	if got := run.execution.State(); got != agentruntime.RunStateFailed {
		t.Fatalf("State() = %q, want failed", got)
	}
	if err := run.execution.FinishWithState(run.id, agentruntime.RunStateCompleted); err == nil {
		t.Fatal("FinishWithState() after terminal state should fail")
	}
}

func TestTUIRunApprovalLifecycle(t *testing.T) {
	run := newTUIRun()
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatal(err)
	}
	if err := run.waitForApproval(); err != nil {
		t.Fatalf("waitForApproval() error = %v", err)
	}
	if got := run.execution.State(); got != agentruntime.RunStateWaitingApproval {
		t.Fatalf("State() = %q, want waiting_for_approval", got)
	}
	if err := run.resume(); err != nil {
		t.Fatalf("resume() error = %v", err)
	}
	if got := run.execution.State(); got != agentruntime.RunStateRunning {
		t.Fatalf("State() = %q, want running", got)
	}
}

func TestTUIRunQuestionLifecycle(t *testing.T) {
	run := newTUIRun()
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatal(err)
	}
	if err := run.waitForQuestion(); err != nil {
		t.Fatalf("waitForQuestion() error = %v", err)
	}
	if got := run.execution.State(); got != agentruntime.RunStateWaitingQuestion {
		t.Fatalf("State() = %q, want waiting_for_question", got)
	}
	if err := run.resume(); err != nil {
		t.Fatalf("resume() error = %v", err)
	}
	if got := run.execution.State(); got != agentruntime.RunStateRunning {
		t.Fatalf("State() = %q, want running", got)
	}
}
func TestTUIRunDecisionService(t *testing.T) {
	run := newTUIRun("session-1")
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatal(err)
	}
	if err := run.registerDecision("approval-1", agentruntime.DecisionApproval); err != nil {
		t.Fatalf("register approval: %v", err)
	}
	if err := run.waitForApproval(); err != nil {
		t.Fatal(err)
	}
	if err := run.resolveDecision("approval-1", agentruntime.DecisionApproval, "true"); err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	if err := run.resume(); err != nil {
		t.Fatalf("resume after approval: %v", err)
	}
	if got := run.execution.State(); got != agentruntime.RunStateRunning {
		t.Fatalf("State() after approval = %q, want running", got)
	}

	if err := run.registerDecision("question-1", agentruntime.DecisionQuestion); err != nil {
		t.Fatalf("register question: %v", err)
	}
	if got := len(run.decisions.Pending()); got != 1 {
		t.Fatalf("pending decisions = %d, want 1", got)
	}
	run.clearDecisions("cancelled")
	if got := len(run.decisions.Pending()); got != 0 {
		t.Fatalf("pending decisions after clear = %d, want 0", got)
	}
}
func TestTUIRunCancel(t *testing.T) {
	run := newTUIRun()
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatal(err)
	}
	if !run.cancel() {
		t.Fatal("cancel() = false, want true")
	}
	if err := run.execution.FinishWithState(run.id, agentruntime.RunStateCancelled); err != nil {
		// Cancellation remains an explicit terminal transition even if the
		// underlying context has already been cancelled.
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FinishWithState() error = %v", err)
		}
	}
}

func TestEventQuestionRequestRegistersDecision(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID

	run := newTUIRun(sessionID, sessionDir)
	run.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatalf("begin run: %v", err)
	}

	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), sess, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.run = run
	app.eventCh = make(chan agent.Event)

	app.handleAgentEvent(agent.Event{
		Type:            agent.EventQuestionRequest,
		QuestionID:      "question-1",
		QuestionText:    "Pick one?",
		QuestionOptions: []string{"A", "B"},
	})

	if got := len(run.decisions.Pending()); got != 1 {
		t.Fatalf("pending decisions = %d, want 1 (question must be registered)", got)
	}
	found := false
	for _, request := range run.decisions.Pending() {
		if request.ID == "question-1" && request.Kind == agentruntime.DecisionQuestion {
			found = true
		}
	}
	if !found {
		t.Fatalf("registered decisions = %#v, want DecisionQuestion question-1", run.decisions.Pending())
	}
	if !app.waitingForQuestion || app.pendingQuestionID != "question-1" {
		t.Fatalf("question UI state = waiting %v id %q", app.waitingForQuestion, app.pendingQuestionID)
	}

	// A duplicate request must not re-register or corrupt the queue.
	app.handleAgentEvent(agent.Event{
		Type:            agent.EventQuestionRequest,
		QuestionID:      "question-1",
		QuestionText:    "Pick one?",
		QuestionOptions: []string{"A", "B"},
	})
	if got := len(run.decisions.Pending()); got != 1 {
		t.Fatalf("pending decisions after duplicate = %d, want 1", got)
	}
}
func TestEscAbortFinalizesDurableRunBeforeNextInput(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID

	run := newTUIRun(sessionID, sessionDir)
	run.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	startedAt := time.Now()
	intent := agentruntime.ExecutionIntent{ID: "intent-abort", SessionID: sessionID, Source: "tui", CreatedAt: startedAt}
	if _, err := run.execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: run.id, SessionID: sessionID, IntentID: intent.ID, Source: "tui", Status: "running", StartedAt: startedAt,
	}, agentruntime.RunEvent{SessionID: sessionID, RunID: run.id, EventType: "started", Source: "tui", Status: "running", Timestamp: startedAt}); err != nil {
		t.Fatalf("begin durable run: %v", err)
	}

	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), sess, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.run = run
	app.eventCh = make(chan agent.Event)
	app.isThinking = true
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if _, active := run.execution.Active(); active {
		t.Fatal("execution remains active after Esc")
	}
	activeRun, err := session.GetActiveSessionRun(sessionDir, sessionID)
	if err != nil {
		t.Fatalf("load active run after Esc: %v", err)
	}
	if activeRun != nil {
		t.Fatalf("durable run remains active after Esc: %#v", activeRun)
	}

	next := newTUIRun(sessionID, sessionDir)
	next.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	intent.ID = "intent-next"
	intent.CreatedAt = time.Now()
	if _, err := next.execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: next.id, SessionID: sessionID, IntentID: intent.ID, Source: "tui", Status: "running", StartedAt: intent.CreatedAt,
	}, agentruntime.RunEvent{SessionID: sessionID, RunID: next.id, EventType: "started", Source: "tui", Status: "running", Timestamp: intent.CreatedAt}); err != nil {
		t.Fatalf("begin next durable run after Esc: %v", err)
	}
	_ = next.execution.FinishDurable(next.id, agentruntime.RunStateCancelled, "", agentruntime.RunEvent{SessionID: sessionID, RunID: next.id, EventType: "finished", Source: "tui", Status: "cancelled", Timestamp: time.Now()})
}

type escRetryProvider struct {
	mu           sync.Mutex
	calls        int
	firstStarted chan struct{}
}

func (p *escRetryProvider) Chat(ctx context.Context, _ provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()

	ch := make(chan provider.StreamEvent, 2)
	if call == 1 {
		close(p.firstStarted)
		go func() {
			<-ctx.Done()
			close(ch)
		}()
		return ch
	}
	ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "second response"}
	ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "end_turn"}
	close(ch)
	return ch
}

func (p *escRetryProvider) Name() string { return "esc-retry" }
func (p *escRetryProvider) API() string  { return "openai-chat" }
func (p *escRetryProvider) Models() []*provider.Model {
	return []*provider.Model{{ID: "test", Name: "Test"}}
}
func (p *escRetryProvider) GetModel(id string) *provider.Model {
	if id == "test" {
		return p.Models()[0]
	}
	return nil
}

func TestEscAbortThenNextInputCompletes(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	p := &escRetryProvider{firstStarted: make(chan struct{})}
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(p, p.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	firstCmd := app.processInput("first")
	if firstCmd == nil {
		t.Fatal("first processInput returned nil")
	}
	firstStart, ok := firstCmd().(agentStreamStartMsg)
	if !ok || firstStart.err != nil || firstStart.eventCh == nil {
		t.Fatalf("first stream start = %#v", firstStart)
	}
	app.Update(firstStart)
	select {
	case <-p.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first provider call did not start")
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.isThinking {
		t.Fatal("app remains thinking immediately after Esc")
	}

	secondCmd := app.processInput("second")
	if secondCmd == nil {
		t.Fatal("second processInput returned nil")
	}
	secondStart, ok := secondCmd().(agentStreamStartMsg)
	if !ok || secondStart.err != nil || secondStart.eventCh == nil {
		t.Fatalf("second stream start = %#v", secondStart)
	}
	app.Update(secondStart)

	deadline := time.After(3 * time.Second)
	for app.isThinking {
		select {
		case event, open := <-secondStart.eventCh:
			if !open {
				app.Update(agentDoneMsg{eventCh: secondStart.eventCh})
				continue
			}
			app.Update(agentEventMsg{event: event, eventCh: secondStart.eventCh})
		case <-deadline:
			t.Fatal("second input remained stuck in thinking")
		}
	}
}

func TestCancelledRunResetsAgentBeforeNextInput(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}

	responses := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "recovered"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "test", Name: "Test"}}, responses)
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(mock, mock.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	app.ensureAgent()
	if app.agent == nil {
		t.Fatal("main agent was not created")
	}
	// Match the state after a Runtime-originated cancellation: the old Agent's
	// abort channel is already closed before EventRunFinished reaches the TUI.
	app.agent.Abort()
	run := newTUIRun()
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatalf("begin cancelled run: %v", err)
	}
	app.run = run
	app.isThinking = true
	app.handleAgentEvent(agent.Event{Type: agent.EventRunFinished, Status: agent.TaskCanceled})
	if app.agent != nil {
		t.Fatal("cancelled run retained an aborted Agent instance")
	}

	nextCmd := app.processInput("try again")
	if nextCmd == nil {
		t.Fatal("next input did not start")
	}
	nextStart, ok := nextCmd().(agentStreamStartMsg)
	if !ok || nextStart.err != nil || nextStart.eventCh == nil {
		t.Fatalf("next stream start = %#v", nextStart)
	}

	var finished agent.TaskStatus
	for event := range nextStart.eventCh {
		if event.Type == agent.EventRunFinished {
			finished = event.Status
		}
	}
	if finished != agent.TaskSuccess {
		t.Fatalf("next run status = %q, want %q", finished, agent.TaskSuccess)
	}
}

func TestFailedRunDiscardsAbortedAgentBeforeNextInput(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}

	responses := []provider.StreamEvent{
		{Type: provider.StreamTextDelta, TextDelta: "recovered"},
		{Type: provider.StreamDone, StopReason: "end_turn"},
	}
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "test", Name: "Test"}}, responses)
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(mock, mock.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	app.ensureAgent()
	if app.agent == nil {
		t.Fatal("main agent was not created")
	}
	aborted := app.agent
	// A lost execution lease aborts the Agent through the shared runtime, but
	// the run terminalizes as failed rather than canceled. The failed branch
	// does not reset the Agent, so the poisoned instance used to survive and
	// cancel every later submission ("The run was cancelled.: context canceled").
	app.agent.Abort()
	run := newTUIRun()
	if _, err := run.execution.Begin(context.Background(), run.id); err != nil {
		t.Fatalf("begin failed run: %v", err)
	}
	app.run = run
	app.isThinking = true
	app.handleAgentEvent(agent.Event{
		Type:   agent.EventRunFinished,
		Status: agent.TaskFailed,
		Error:  errors.New("session runtime lease was lost"),
	})
	if app.agent == nil {
		t.Fatal("failed run unexpectedly cleared the Agent before the next input")
	}

	nextCmd := app.processInput("try again")
	if nextCmd == nil {
		t.Fatal("next input did not start")
	}
	if app.agent == aborted {
		t.Fatal("aborted Agent instance was reused for the next run")
	}
	nextStart, ok := nextCmd().(agentStreamStartMsg)
	if !ok || nextStart.err != nil || nextStart.eventCh == nil {
		t.Fatalf("next stream start = %#v", nextStart)
	}

	var finished agent.TaskStatus
	for event := range nextStart.eventCh {
		if event.Type == agent.EventRunFinished {
			finished = event.Status
		}
	}
	if finished != agent.TaskSuccess {
		t.Fatalf("next run status = %q, want %q", finished, agent.TaskSuccess)
	}
}

func TestInputDuringRunQueuesWithoutReplacingLeaseOwner(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	p := &escRetryProvider{firstStarted: make(chan struct{})}
	settings := config.DefaultSettings()
	settings.DefaultThinkingLevel = "off"
	app := NewApp(p, p.Models()[0], settings, sess, tools.NewRegistry(workDir, nil), "", "", "", nil, "agent", false, false, nil, nil, nil)

	firstCmd := app.processInput("first")
	if firstCmd == nil {
		t.Fatal("first processInput returned nil")
	}
	firstStart, ok := firstCmd().(agentStreamStartMsg)
	if !ok || firstStart.err != nil || firstStart.eventCh == nil {
		t.Fatalf("first stream start = %#v", firstStart)
	}
	app.Update(firstStart)
	select {
	case <-p.firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first provider call did not start")
	}

	activeRun := app.run
	if activeRun == nil {
		t.Fatal("active run is nil")
	}
	if cmd := app.processInput("second"); cmd != nil {
		t.Fatal("input during a run started a second execution instead of queuing")
	}
	if app.run != activeRun {
		t.Fatal("queued input replaced the active run")
	}
	if got := len(app.queuedPrompts); got != 1 {
		t.Fatalf("queued prompts = %d, want 1", got)
	}

	// The normal cancellation path finalizes the durable run and releases its
	// lease before the queued prompt is permitted to start.
	app.abortPendingRequest("test cancellation")
	if app.run != nil {
		t.Fatal("abort left the active run installed")
	}
	nextCmd := app.startNextQueuedPrompt()
	if nextCmd == nil {
		t.Fatal("queued prompt did not start after terminal cleanup")
	}
	nextStart, ok := nextCmd().(agentStreamStartMsg)
	if !ok || nextStart.err != nil || nextStart.eventCh == nil {
		t.Fatalf("queued stream start = %#v", nextStart)
	}
	if got := len(app.queuedPrompts); got != 0 {
		t.Fatalf("queued prompts after start = %d, want 0", got)
	}
	nextStart.run.finish(agentruntime.RunStateCancelled)
}

var _ *agent.Agent
