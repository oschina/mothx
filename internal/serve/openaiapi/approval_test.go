package openaiapi

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

func TestResolveOrphanedQuestions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("question-orphan", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	run := session.SessionRun{ID: "run-orphan", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "webui", Model: "test", Mode: "agent"}
	request := SessionQuestionRequest{QuestionID: "question-orphan", SessionID: sess.ID, RunID: run.ID, Question: "continue?", Options: []string{"yes"}}
	if err := srv.recordSessionQuestionRequest(sess, request); err != nil {
		t.Fatalf("record request: %v", err)
	}
	if err := srv.resolveOrphanedQuestions(run); err != nil {
		t.Fatalf("resolve orphaned questions: %v", err)
	}
	if pending := srv.recoveredPendingQuestions(sess.ID, run.ID); len(pending) != 0 {
		t.Fatalf("orphaned question remains pending: %#v", pending)
	}
}
func TestRecoveredPendingQuestions(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("question-recovery", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	request := SessionQuestionRequest{QuestionID: "question-recover", SessionID: sess.ID, RunID: "run-recover", Question: "continue?", Options: []string{"yes", "no"}}
	if err := srv.recordSessionQuestionRequest(sess, request); err != nil {
		t.Fatalf("record request: %v", err)
	}
	pending := srv.recoveredPendingQuestions(sess.ID, request.RunID)
	if len(pending) != 1 || pending[0].QuestionID != request.QuestionID {
		t.Fatalf("recovered pending questions = %#v", pending)
	}
	resolution := &SessionQuestionResolution{QuestionID: request.QuestionID, SessionID: sess.ID, RunID: request.RunID, Answer: "yes", Status: "resolved"}
	if err := srv.recordSessionQuestionResolution(sess, request, resolution); err != nil {
		t.Fatalf("record resolution: %v", err)
	}
	if pending := srv.recoveredPendingQuestions(sess.ID, request.RunID); len(pending) != 0 {
		t.Fatalf("resolved question still recovered: %#v", pending)
	}
}

func TestClearSessionQuestionsOnRunEnd(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("question-clear", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	sess.beginRun("run-question-clear")
	sess.approvalMu.Lock()
	sess.pendingQuestions = map[string]pendingSessionQuestion{
		"question-1": {Request: SessionQuestionRequest{QuestionID: "question-1", SessionID: sess.ID, RunID: "run-question-clear", Question: "continue?"}},
	}
	sess.approvalMu.Unlock()
	srv.clearSessionApprovalsForRun(sess, "run-question-clear", "cancelled", "run ended")
	sess.approvalMu.Lock()
	remaining := len(sess.pendingQuestions)
	sess.approvalMu.Unlock()
	if remaining != 0 {
		t.Fatalf("pending questions remain: %d", remaining)
	}
}

func TestSessionQuestionRuntimeLifecycle(t *testing.T) {
	runtime := &agentruntime.ExecutionRuntime{}
	if _, err := runtime.Begin(context.Background(), "run-question"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := runtime.WaitForQuestion("run-question"); err != nil {
		t.Fatalf("WaitForQuestion: %v", err)
	}
	if got := runtime.State(); got != agentruntime.RunStateWaitingQuestion {
		t.Fatalf("state = %q, want %q", got, agentruntime.RunStateWaitingQuestion)
	}
	if err := runtime.Resume("run-question"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := runtime.State(); got != agentruntime.RunStateRunning {
		t.Fatalf("resumed state = %q, want %q", got, agentruntime.RunStateRunning)
	}
	runtime.Finish("run-question")
}

func beginApprovalTestRun(sess *APISession, runID string, a *agent.Agent) context.CancelFunc {
	sess.beginRun(runID)
	_, cancel := context.WithCancel(context.Background())
	if a != nil && !sess.attachRunAgent(runID, a, cancel) {
		panic("failed to attach test run agent")
	}
	return cancel
}

func persistApprovalTestRun(t *testing.T, srv *Server, sess *APISession, runID string) {
	t.Helper()
	now := time.Now()
	if err := session.SaveSessionRun(srv.settings.GetSessionDir(), session.SessionRun{
		ID: runID, SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "webui",
		Model: "test", Mode: "agent", Status: "running", StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist test run: %v", err)
	}
}

func beginDurableApprovalTestRun(t *testing.T, srv *Server, sess *APISession, runID string, a *agent.Agent) {
	t.Helper()
	guard, err := session.AcquireExecutionAdmission(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatalf("acquire test admission: %v", err)
	}
	execution := sess.ensureExecution()
	execution.SetRunStore(agentruntime.RunStore{SessionDir: srv.settings.GetSessionDir()})
	startedAt := time.Now()
	if _, err := execution.BeginDurable(t.Context(), agentruntime.DurableRun{
		ID: runID, SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "webui",
		Model: "test", Mode: "agent", Status: string(agentruntime.RunStateRunning), StartedAt: startedAt,
	}, agentruntime.RunEvent{EventType: "started", Timestamp: startedAt}); err != nil {
		guard.Release()
		t.Fatalf("begin test run: %v", err)
	}
	execution.SetAgent(a)
	beginApprovalTestRun(sess, runID, a)
	t.Cleanup(func() {
		if activeID, active := execution.Active(); active && activeID == runID {
			_ = execution.FinishDurable(runID, agentruntime.RunStateCancelled, "test cleanup", agentruntime.RunEvent{EventType: "finished"})
		}
		guard.Release()
	})
}

func TestRuntimeSnapshotIncludesPendingApproval(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-runtime", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	persistApprovalTestRun(t, srv, sess, "run_1")
	beginApprovalTestRun(sess, "run_1", nil)
	sess.Decisions = &agentruntime.DecisionService{}
	if err := sess.Decisions.Register(agentruntime.DecisionRequest{ID: "approval_1", RunID: "run_1", SessionID: sess.ID, Kind: agentruntime.DecisionApproval}); err != nil {
		t.Fatal(err)
	}
	sess.approvalMu.Lock()
	sess.pendingApprovals = map[string]pendingSessionApproval{
		"approval_1": {Request: SessionApprovalRequest{ApprovalID: "approval_1", SessionID: sess.ID, RunID: "run_1", Summary: "Run bash"}},
	}
	sess.approvalMu.Unlock()

	snapshot, err := srv.GetSessionRuntime(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveRun == nil || snapshot.ActiveRun.RunID != "run_1" {
		t.Fatalf("active run = %#v, want run_1", snapshot.ActiveRun)
	}
	if len(snapshot.PendingApprovals) != 1 || snapshot.PendingApprovals[0].ApprovalID != "approval_1" {
		t.Fatalf("pending approvals = %#v", snapshot.PendingApprovals)
	}
}

func TestWebSocketRuntimeSnapshotIncludesPendingApproval(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-runtime-ws", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	persistApprovalTestRun(t, srv, sess, "run_ws")
	beginApprovalTestRun(sess, "run_ws", nil)
	sess.Decisions = &agentruntime.DecisionService{}
	if err := sess.Decisions.Register(agentruntime.DecisionRequest{ID: "approval_ws", RunID: "run_ws", SessionID: sess.ID, Kind: agentruntime.DecisionApproval}); err != nil {
		t.Fatal(err)
	}
	sess.approvalMu.Lock()
	sess.pendingApprovals = map[string]pendingSessionApproval{
		"approval_ws": {Request: SessionApprovalRequest{ApprovalID: "approval_ws", SessionID: sess.ID, RunID: "run_ws"}},
	}
	sess.approvalMu.Unlock()

	var event runWebSocketEvent
	srv.writeRunWebSocketRuntimeSnapshot(func(value any) error {
		event = value.(runWebSocketEvent)
		return nil
	}, sess.ID)
	if event.Stream != "runtime" || event.Event != "runtime_event" || event.RunID != "run_ws" {
		t.Fatalf("runtime websocket event = %#v", event)
	}
	snapshot, ok := event.Data.(*SessionRuntimeSnapshot)
	if !ok || len(snapshot.PendingApprovals) != 1 || snapshot.PendingApprovals[0].ApprovalID != "approval_ws" {
		t.Fatalf("runtime websocket snapshot = %#v", event.Data)
	}
}

func TestCancelSessionRunAbortsPendingApproval(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-cancel", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(srv.cfg.GetWorkDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	a := agent.New(agent.Config{Mode: "agent"}, registry)
	events := make(chan agent.Event, 1)
	result := make(chan bool, 1)
	go func() { result <- a.RequestApproval(events, "bash", map[string]any{"command": "go test ./..."}) }()

	var event agent.Event
	select {
	case event = <-events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked approval request")
	}
	beginDurableApprovalTestRun(t, srv, sess, "run_cancel", a)
	srv.registerSessionApproval(sess, a, event)

	if err := srv.CancelSessionRun(sess.ID); err != nil {
		t.Fatalf("CancelSessionRun: %v", err)
	}
	select {
	case approved := <-result:
		if approved {
			t.Fatal("cancelled approval was approved")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked approval did not exit after cancellation")
	}
	sess.approvalMu.Lock()
	if len(sess.pendingApprovals) != 0 {
		sess.approvalMu.Unlock()
		t.Fatalf("pending approvals remain: %#v", sess.pendingApprovals)
	}
	sess.approvalMu.Unlock()

	stored, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatalf("ListSessionRunEvents: %v", err)
	}
	var requested, cancelled bool
	for _, item := range stored {
		if item.RunID != "run_cancel" {
			continue
		}
		requested = requested || item.EventType == "approval_requested" && item.Status == "pending"
		cancelled = cancelled || item.EventType == "approval_resolved" && item.Status == "cancelled"
	}
	if !requested || !cancelled {
		t.Fatalf("approval audit events requested=%v cancelled=%v: %#v", requested, cancelled, stored)
	}
}

func TestCancelSessionRunBeforeApprovalRegistrationAbortsAgent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-cancel-before-register", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New(agent.Config{Mode: "agent"}, tools.NewRegistry(srv.cfg.GetWorkDir(), sandbox.NewNoneSandbox()))
	events := make(chan agent.Event, 1)
	result := make(chan bool, 1)
	go func() { result <- a.RequestApproval(events, "bash", map[string]any{"command": "go test ./..."}) }()
	var event agent.Event
	select {
	case event = <-events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval event")
	}
	beginDurableApprovalTestRun(t, srv, sess, "run_before_register", a)

	if err := srv.CancelSessionRun(sess.ID); err != nil {
		t.Fatalf("CancelSessionRun: %v", err)
	}
	select {
	case approved := <-result:
		if approved {
			t.Fatal("cancelled approval was approved")
		}
	case <-time.After(time.Second):
		t.Fatal("agent remained blocked before approval registration")
	}
	if request := srv.registerSessionApproval(sess, a, event); request != nil {
		t.Fatalf("late approval became pending: %#v", request)
	}
	snapshot, err := srv.GetSessionRuntime(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PendingApprovals) != 0 || snapshot.ActiveRun == nil || snapshot.ActiveRun.Status != "cancelling" {
		t.Fatalf("runtime after stop = %#v", snapshot)
	}
	stored, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored[0].EventType != "started" || stored[1].EventType != "approval_requested" || stored[1].Status != "pending" || stored[2].EventType != "approval_resolved" || stored[2].Status != "cancelled" || stored[2].RunID != "run_before_register" {
		t.Fatalf("late approval audit = %#v", stored)
	}
}

func TestCancelSessionRunDoesNotAffectOtherSessionApproval(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	makePending := func(id, runID string) (*APISession, <-chan bool) {
		sess, err := srv.getOrCreateSession(id, srv.cfg.GetWorkDir())
		if err != nil {
			t.Fatal(err)
		}
		a := agent.New(agent.Config{Mode: "agent"}, tools.NewRegistry(srv.cfg.GetWorkDir(), sandbox.NewNoneSandbox()))
		events := make(chan agent.Event, 1)
		result := make(chan bool, 1)
		go func() { result <- a.RequestApproval(events, "bash", map[string]any{"command": id}) }()
		event := <-events
		beginDurableApprovalTestRun(t, srv, sess, runID, a)
		if srv.registerSessionApproval(sess, a, event) == nil {
			t.Fatalf("failed to register approval for %s", id)
		}
		return sess, result
	}
	sessA, resultA := makePending("approval-isolation-a", "run_a")
	sessB, resultB := makePending("approval-isolation-b", "run_b")

	if err := srv.CancelSessionRun(sessA.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case approved := <-resultA:
		if approved {
			t.Fatal("session A approval was approved")
		}
	case <-time.After(time.Second):
		t.Fatal("session A approval remained blocked")
	}
	select {
	case <-resultB:
		t.Fatal("session B approval was affected by session A stop")
	default:
	}
	snapshotB, err := srv.GetSessionRuntime(sessB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotB.ActiveRun == nil || snapshotB.ActiveRun.RunID != "run_b" || len(snapshotB.PendingApprovals) != 1 {
		t.Fatalf("session B runtime changed: %#v", snapshotB)
	}
}

func TestResolveSessionApprovalFirstResponseWins(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-race", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	beginApprovalTestRun(sess, "run_race", nil)
	sess.approvalMu.Lock()
	sess.pendingApprovals = map[string]pendingSessionApproval{
		"approval_1": {Request: SessionApprovalRequest{ApprovalID: "approval_1", SessionID: sess.ID, RunID: "run_race", Mode: "agent"}},
	}
	sess.approvalMu.Unlock()

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, action := range []string{"approve_once", "deny_once"} {
		wg.Add(1)
		go func(action string) {
			defer wg.Done()
			_, err := srv.ResolveSessionApproval(sess.ID, "approval_1", SessionApprovalResponse{Action: action})
			results <- err
		}(action)
	}
	wg.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful responses = %d, want exactly 1", successes)
	}
}

func TestResolveSessionApprovalResumesBlockedAgent(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-resume", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(srv.cfg.GetWorkDir(), sandbox.NewNoneSandbox())
	registry.RegisterDefaults()
	a := agent.New(agent.Config{Mode: "agent"}, registry)
	result := make(chan bool, 1)
	events := make(chan agent.Event, 1)
	go func() { result <- a.RequestApproval(events, "bash", map[string]any{"command": "go test ./..."}) }()

	var event agent.Event
	select {
	case event = <-events:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked approval request")
	}
	beginApprovalTestRun(sess, "run_resume", a)
	srv.registerSessionApproval(sess, a, event)
	if _, err := srv.ResolveSessionApproval(sess.ID, event.ApprovalID, SessionApprovalResponse{Action: "approve_once"}); err != nil {
		t.Fatal(err)
	}
	select {
	case approved := <-result:
		if !approved {
			t.Fatal("agent received denied approval, want approved")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked agent did not resume after approval")
	}
}

func TestResolveSessionApprovalRollsBackRuleWhenSaveFails(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	srv.allow = &config.AllowConfig{}
	srv.saveProjectAllow = func(*config.AllowConfig) error { return errors.New("disk full") }
	sess, err := srv.getOrCreateSession("approval-rollback", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	request := SessionApprovalRequest{ApprovalID: "approval_1", SessionID: sess.ID, RunID: "run_rollback", Tool: map[string]any{"args": map[string]any{"command": "go test ./..."}}}
	beginApprovalTestRun(sess, request.RunID, nil)
	sess.approvalMu.Lock()
	sess.pendingApprovals = map[string]pendingSessionApproval{"approval_1": {Request: request}}
	sess.approvalMu.Unlock()

	if _, err := srv.ResolveSessionApproval(sess.ID, request.ApprovalID, SessionApprovalResponse{Action: "remember_command"}); err == nil {
		t.Fatal("expected rule persistence failure")
	}
	if srv.allow.MatchBashCommand("go test ./...") {
		t.Fatal("failed rule persistence must rollback in-memory allow rule")
	}
	sess.approvalMu.Lock()
	defer sess.approvalMu.Unlock()
	if _, ok := sess.pendingApprovals[request.ApprovalID]; !ok {
		t.Fatal("approval must remain pending after rule persistence failure")
	}
}

func TestClearSessionApprovalsResolvesAndRemovesPending(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-cleanup", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	beginApprovalTestRun(sess, "run_cleanup", nil)
	sess.Decisions = &agentruntime.DecisionService{}
	if err := sess.Decisions.Register(agentruntime.DecisionRequest{ID: "approval_1", RunID: "run_cleanup", SessionID: sess.ID, Kind: agentruntime.DecisionApproval}); err != nil {
		t.Fatal(err)
	}
	sess.approvalMu.Lock()
	sess.pendingApprovals = map[string]pendingSessionApproval{
		"approval_1": {Request: SessionApprovalRequest{ApprovalID: "approval_1", SessionID: sess.ID, RunID: "run_cleanup"}},
	}
	sess.approvalMu.Unlock()

	srv.clearSessionApprovals(sess, "cancelled", "run cancelled")
	sess.approvalMu.Lock()
	if len(sess.pendingApprovals) != 0 {
		sess.approvalMu.Unlock()
		t.Fatalf("pending approvals remain: %#v", sess.pendingApprovals)
	}
	sess.approvalMu.Unlock()
	if pending := sess.Decisions.Pending(); len(pending) != 0 {
		t.Fatalf("runtime decisions remain: %#v", pending)
	}
}

func TestRecoveredApprovalDecisionUsesMatchingDurableResolution(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	sess, err := srv.getOrCreateSession("approval-recovery", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatal(err)
	}
	request := SessionApprovalRequest{
		ApprovalID: "approval-old", ToolCallID: "call-1", SessionID: sess.ID, RunID: "run-recovery",
		Tool: map[string]any{"name": "bash", "args": map[string]any{"command": "echo recovered"}},
	}
	resolution := &SessionApprovalResolution{ApprovalID: request.ApprovalID, SessionID: sess.ID, Action: "approve_once", Status: "resolved"}
	if err := srv.recordSessionApprovalResolution(sess, request, resolution); err != nil {
		t.Fatalf("persist resolution: %v", err)
	}
	approved, found := srv.recoveredApprovalDecision(sess.ID, request.RunID, "call-1", "bash", map[string]any{"command": "echo recovered"})
	if !found || !approved {
		t.Fatalf("approved=%v found=%v, want persisted approval", approved, found)
	}
	if _, found := srv.recoveredApprovalDecision(sess.ID, request.RunID, "call-2", "bash", map[string]any{"command": "echo recovered"}); found {
		t.Fatal("different tool call must not reuse a decision")
	}
	if _, found := srv.recoveredApprovalDecision(sess.ID, request.RunID, "call-1", "bash", map[string]any{"command": "echo changed"}); found {
		t.Fatal("different arguments must not reuse a decision")
	}
}
