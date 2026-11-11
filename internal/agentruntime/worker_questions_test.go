package agentruntime

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
)

func newWorkerQuestionRuntime(t *testing.T) (*SessionRuntime, string) {
	t.Helper()
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("session-worker-question"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{
		ID: "session-worker-question", Manager: manager,
		Source: SourceTUI, Mode: ModeRouter,
		Decisions: &DecisionService{},
		Mailbox:   agent.NewMemberMailbox(),
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime, sessionDir
}

func TestWorkerQuestionRoundTripResolvesThroughDecisionService(t *testing.T) {
	runtime, sessionDir := newWorkerQuestionRuntime(t)
	type result struct {
		answer string
		status string
	}
	done := make(chan result, 1)
	go func() {
		answer, status := runtime.askWorkerQuestion(context.Background(), "child-1", "run-1", "which branch?", []string{"A", "B"}, 2*time.Second)
		done <- result{answer: answer, status: status}
	}()

	questionID := ""
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, item := range runtime.Mailbox.PendingSummary() {
			if item.Kind == agent.MemberItemQuestion {
				questionID = item.QuestionID
				if !strings.Contains(item.Payload, "which branch?") {
					t.Fatalf("question payload = %q", item.Payload)
				}
				if !item.External {
					t.Fatal("worker question was not marked external")
				}
			}
		}
		if questionID != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if questionID == "" {
		t.Fatal("worker question never reached the lead mailbox")
	}
	if err := runtime.ResolveWorkerQuestion(context.Background(), questionID, "A"); err != nil {
		t.Fatalf("resolve worker question: %v", err)
	}
	select {
	case got := <-done:
		if got.answer != "A" || got.status != "answered" {
			t.Fatalf("ask result = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not unblock after the answer")
	}

	records, err := LoadDecisionRecords(sessionDir, "session-worker-question")
	if err != nil {
		t.Fatal(err)
	}
	seenPending, seenResolved := false, false
	for _, record := range records {
		if record.ID != questionID {
			continue
		}
		switch record.Status {
		case DecisionStatusPending:
			seenPending = true
		case DecisionStatusResolved:
			seenResolved = true
			if record.Value != "A" {
				t.Fatalf("durable resolution value = %q", record.Value)
			}
		}
	}
	if !seenPending || !seenResolved {
		t.Fatalf("durable decision ledger = %#v", records)
	}

	// The same answer is idempotent; a different answer cannot overwrite it.
	if err := runtime.ResolveWorkerQuestion(context.Background(), questionID, "A"); err != nil {
		t.Fatalf("repeated identical answer = %v", err)
	}
	if err := runtime.ResolveWorkerQuestion(context.Background(), questionID, "B"); err == nil {
		t.Fatal("a different answer overwrote a resolved worker question")
	}
}

func TestWorkerQuestionTimesOutAndTerminalizesDecision(t *testing.T) {
	runtime, sessionDir := newWorkerQuestionRuntime(t)
	answer, status := runtime.askWorkerQuestion(context.Background(), "child-2", "run-2", "still there?", nil, 40*time.Millisecond)
	if status != "timeout" || answer != "" {
		t.Fatalf("timeout result = %q/%q", answer, status)
	}
	records, err := LoadDecisionRecords(sessionDir, "session-worker-question")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.Status == DecisionStatusTimedOut {
			found = true
		}
	}
	if !found {
		t.Fatalf("timeout was not durable: %#v", records)
	}
	if runtime.Decisions.Pending() != nil && len(runtime.Decisions.Pending()) != 0 {
		t.Fatalf("timed-out question stayed pending: %#v", runtime.Decisions.Pending())
	}
	messages, err := runtime.WorkerMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Kind != WorkerMessageQuestion || messages[0].Status != WorkerMessageStatusExpired {
		t.Fatalf("timed-out worker message projection = %#v", messages)
	}
}

func TestPendingWorkerQuestionsProjectsOnlyUnresolvedWorkerAsks(t *testing.T) {
	runtime, _ := newWorkerQuestionRuntime(t)
	done := make(chan string, 1)
	go func() {
		_, status := runtime.askWorkerQuestion(context.Background(), "child-pending", "run-pending", "which file?", []string{"a.go", "b.go"}, 2*time.Second)
		done <- status
	}()

	var pending []WorkerQuestionView
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		projection, err := runtime.PendingWorkerQuestions()
		if err != nil {
			t.Fatal(err)
		}
		if len(projection) == 1 {
			pending = projection
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatal("pending worker question was not projected")
	}
	if pending[0].ChildID != "child-pending" || pending[0].Question != "which file?" || len(pending[0].Options) != 2 {
		t.Fatalf("pending projection = %#v", pending[0])
	}
	if !IsWorkerQuestionID(pending[0].ID) {
		t.Fatalf("worker question id = %q", pending[0].ID)
	}
	if err := runtime.ResolveWorkerQuestion(context.Background(), pending[0].ID, "a.go"); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status != "answered" {
			t.Fatalf("ask status = %q", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not finish")
	}
	if after, err := runtime.PendingWorkerQuestions(); err != nil || len(after) != 0 {
		t.Fatalf("resolved question stayed pending: %#v err=%v", after, err)
	}
	snapshot, err := runtime.WorkerSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Status != WorkerMessageStatusDelivered {
		t.Fatalf("resolved worker message = %#v", snapshot.Messages)
	}
}

func TestWorkerMessageCompactionPreservesFoldedStatus(t *testing.T) {
	original := workerMessageCompactBytes
	workerMessageCompactBytes = 1
	t.Cleanup(func() { workerMessageCompactBytes = original })

	log, err := OpenWorkerMessageLog(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	message, _, err := log.Create(WorkerMessage{SessionID: "s", ChildID: "c", Kind: WorkerMessageQuestion, Body: "why?"})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Mark(message.ID, workerMessageOpExpired, "", "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := log.Compact(); err != nil {
		t.Fatal(err)
	}
	messages, err := log.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Status != WorkerMessageStatusExpired || messages[0].Reason != "timeout" {
		t.Fatalf("compacted messages = %#v", messages)
	}
}

func TestWorkerQuestionCancellationFollowsRequestContext(t *testing.T) {
	runtime, _ := newWorkerQuestionRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		_, status := runtime.askWorkerQuestion(ctx, "child-3", "run-3", "waiting", nil, time.Minute)
		done <- status
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case status := <-done:
		if status != "cancelled" {
			t.Fatalf("cancelled ask status = %q", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled ask did not return")
	}
}

func TestResolveWorkerQuestionRejectsForeignSession(t *testing.T) {
	runtime, _ := newWorkerQuestionRuntime(t)
	decisions := runtime.Decisions
	request := DecisionRequest{ID: "wq_foreign", RunID: "run-foreign", SessionID: "other-session", Kind: DecisionQuestion}
	if err := decisions.Register(request); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ResolveWorkerQuestion(context.Background(), "wq_foreign", "yes"); err == nil ||
		!strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign question resolution = %v", err)
	}
}

// The append-only log compacts automatically once it crosses the threshold,
// retaining every folded message and its terminal status.
func TestWorkerMessageLogCompactsAutomaticallyAtThreshold(t *testing.T) {
	original := workerMessageCompactBytes
	workerMessageCompactBytes = 1 << 10
	t.Cleanup(func() { workerMessageCompactBytes = original })

	dir := t.TempDir()
	log, err := OpenWorkerMessageLog(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for i := 0; i < 20; i++ {
		message, _, err := log.Create(WorkerMessage{
			SessionID: "s", ChildID: "c", Kind: WorkerMessageProgress,
			Body: strings.Repeat("x", 100),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := log.Mark(message.ID, workerMessageOpObserved, "run", ""); err != nil {
			t.Fatal(err)
		}
		if err := log.Mark(message.ID, workerMessageOpDelivered, "run", ""); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(mustWorkerMessageLogPath(t, dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	// Three rows per message (created/observed/delivered) fold to two
	// (created/delivered). Without compaction the log is roughly 11 KiB; the
	// threshold-triggered rewrite keeps it below 9 KiB.
	if info.Size() >= 9<<10 {
		t.Fatalf("log was not compacted: %d bytes (threshold=%d floor=%d)", info.Size(), workerMessageCompactBytes, log.compactFloor)
	}
	messages, err := log.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 20 {
		t.Fatalf("compaction lost messages: %d", len(messages))
	}
	for _, message := range messages {
		if message.Status != WorkerMessageStatusDelivered {
			t.Fatalf("compaction lost status: %#v", message)
		}
	}
}

// A read-only projection must not create the JSONL file for a session that
// never used the bridge.
func TestWorkerMessagesReadDoesNotCreateLogFile(t *testing.T) {
	runtime, sessionDir := newWorkerQuestionRuntime(t)
	messages, err := runtime.WorkerMessages()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %#v", messages)
	}
	if _, err := os.Stat(mustWorkerMessageLogPath(t, sessionDir, "session-worker-question")); !os.IsNotExist(err) {
		t.Fatalf("read-only projection created %s: %v", workerMessageFile, err)
	}
}
