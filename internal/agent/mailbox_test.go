package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemberMailboxDrainSteeringOrderAndFormat(t *testing.T) {
	m := NewMemberMailbox()
	if m.HasPending() {
		t.Fatal("new mailbox should be empty")
	}
	if msgs := m.DrainSteering(); msgs != nil {
		t.Fatalf("empty drain = %#v, want nil", msgs)
	}

	m.Enqueue(MemberCompletion{Wake: true, MemberID: "pm", DisplayName: "产品经理", Status: MemberStatusDone, Payload: "PRD 已完成"})
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "engineer", DisplayName: "工程师", Status: MemberStatusDone, Payload: "Result: done"})
	if !m.HasPending() {
		t.Fatal("expected pending after enqueue")
	}

	msgs := m.DrainSteering()
	if len(msgs) != 2 {
		t.Fatalf("drained %d messages, want 2", len(msgs))
	}
	for _, msg := range msgs {
		if msg.Role != "user" {
			t.Fatalf("role = %q, want user", msg.Role)
		}
		if !msg.SystemInjected {
			t.Fatal("expected system-injected steering message")
		}
		if !strings.HasPrefix(msg.Content, "[MEMBER_COMPLETION] 系统注入的成员状态上下文（非用户输入）。") {
			t.Fatalf("missing machine-readable marker in %q", msg.Content)
		}
	}
	wantFirst := "[MEMBER_COMPLETION] 系统注入的成员状态上下文（非用户输入）。\n" +
		"member: pm（产品经理）\n" +
		"status: done\n" +
		"payload:\n" +
		"PRD 已完成"
	if msgs[0].Content != wantFirst {
		t.Fatalf("first message =\n%q\nwant\n%q", msgs[0].Content, wantFirst)
	}
	if !strings.Contains(msgs[1].Content, "member: engineer（工程师）") ||
		!strings.Contains(msgs[1].Content, "Result: done") {
		t.Fatalf("second message lost enqueue order or content: %q", msgs[1].Content)
	}

	if m.HasPending() {
		t.Fatal("drain must empty the queue")
	}
	if msgs := m.DrainSteering(); msgs != nil {
		t.Fatalf("second drain = %#v, want nil", msgs)
	}
}

func TestMemberMailboxDrainSteeringTruncatesPayloadRunes(t *testing.T) {
	m := NewMemberMailbox()
	payload := strings.Repeat("测", 4000) // CJK payload: truncation must be rune-based
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "qa", Status: MemberStatusDone, Payload: payload})

	msgs := m.DrainSteering()
	if len(msgs) != 1 {
		t.Fatalf("drained %d messages, want 1", len(msgs))
	}
	content := msgs[0].Content
	if !strings.Contains(content, strings.Repeat("测", 3500)+"…[truncated]") {
		t.Fatal("expected done payload truncated to 3500 runes with truncation suffix")
	}
	if strings.Contains(content, strings.Repeat("测", 3501)) {
		t.Fatal("done payload exceeded the 3500-rune budget")
	}
}

func TestMemberMailboxDrainSteeringErrorTruncationAndNextStep(t *testing.T) {
	m := NewMemberMailbox()
	payload := strings.Repeat("e", 3500)
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "engineer", DisplayName: "工程师", Status: MemberStatusError, Payload: payload})

	msgs := m.DrainSteering()
	if len(msgs) != 1 {
		t.Fatalf("drained %d messages, want 1", len(msgs))
	}
	content := msgs[0].Content
	if !strings.Contains(content, strings.Repeat("e", 3000)+"…[truncated]") {
		t.Fatal("expected error payload truncated to 3000 runes with truncation suffix")
	}
	if strings.Contains(content, strings.Repeat("e", 3001)) {
		t.Fatal("error payload exceeded the 3000-rune budget")
	}
	wantNextStep := "下一步：如仍需该成员，用 subagent_spawn(member:\"engineer\", task:…) 重新派发任务。"
	if !strings.HasSuffix(content, wantNextStep) {
		t.Fatalf("expected error next-step hint as final line, got tail %q", content[max(0, len(content)-160):])
	}
	if !strings.Contains(content, "status: error\n") {
		t.Fatalf("expected status line in %q", content)
	}
}

func TestMemberMailboxDrainSteeringErrorShortPayloadKeepsNextStep(t *testing.T) {
	m := NewMemberMailbox()
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "qa", Status: MemberStatusError, Payload: "boom"})

	msgs := m.DrainSteering()
	content := msgs[0].Content
	if !strings.Contains(content, "payload:\nboom\n") {
		t.Fatalf("short error payload mangled: %q", content)
	}
	if !strings.Contains(content, "subagent_spawn(member:\"qa\", task:…)") {
		t.Fatalf("missing next-step hint: %q", content)
	}
}

func TestMemberMailboxExternalCompletionNamesChildHandle(t *testing.T) {
	m := NewMemberMailbox()
	m.Enqueue(MemberCompletion{Wake: true,
		MemberID: "agent-child-1", DisplayName: "ACP worker opencode",
		Status: MemberStatusError, Payload: "worker exited", External: true,
	})
	msgs := m.DrainSteering()
	if len(msgs) != 1 {
		t.Fatalf("drained %d messages, want 1", len(msgs))
	}
	content := msgs[0].Content
	if !strings.Contains(content, "member: agent-child-1（ACP worker opencode）") {
		t.Fatalf("external completion lost the child handle: %q", content)
	}
	if !strings.Contains(content, `subagent_send(handle:"agent-child-1"`) ||
		strings.Contains(content, `subagent_spawn(member:`) {
		t.Fatalf("external completion used the member-only next step: %q", content)
	}
}

func TestMemberMailboxPendingSummaryDoesNotDrain(t *testing.T) {
	m := NewMemberMailbox()
	if summary := m.PendingSummary(); summary != nil {
		t.Fatalf("empty summary = %#v, want nil", summary)
	}
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "pm", Status: MemberStatusDone, Payload: "PRD"})
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "qa", Status: MemberStatusError, Payload: "boom"})

	summary := m.PendingSummary()
	if len(summary) != 2 {
		t.Fatalf("summary len = %d, want 2", len(summary))
	}
	if summary[0].MemberID != "pm" || summary[0].Payload != "PRD" {
		t.Fatalf("summary[0] = %#v", summary[0])
	}
	if !m.HasPending() {
		t.Fatal("PendingSummary must not drain the queue")
	}
	summary[0].Payload = "mutated"
	if got := m.PendingSummary()[0].Payload; got != "PRD" {
		t.Fatalf("summary must be a copy, queue payload = %q", got)
	}
	if msgs := m.DrainSteering(); len(msgs) != 2 {
		t.Fatalf("drained %d messages after summary, want 2", len(msgs))
	}
}

func TestMemberMailboxWaitForActivitySignal(t *testing.T) {
	m := NewMemberMailbox()
	go func() {
		time.Sleep(20 * time.Millisecond)
		m.Enqueue(MemberCompletion{Wake: true, MemberID: "pm", Status: MemberStatusDone})
	}()
	start := time.Now()
	timedOut, err := m.WaitForActivity(context.Background(), 10*time.Second)
	if err != nil {
		t.Fatalf("WaitForActivity error: %v", err)
	}
	if timedOut {
		t.Fatal("expected activity signal, got timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("WaitForActivity took %v, expected prompt return on enqueue", elapsed)
	}
}

func TestMemberMailboxWaitForActivityTimeout(t *testing.T) {
	m := NewMemberMailbox()
	timedOut, err := m.WaitForActivity(context.Background(), 30*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForActivity error: %v", err)
	}
	if !timedOut {
		t.Fatal("expected timeout on empty mailbox")
	}
}

func TestMemberMailboxWaitForActivityContextCanceled(t *testing.T) {
	m := NewMemberMailbox()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	timedOut, err := m.WaitForActivity(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want wrapped context.Canceled", err)
	}
	if timedOut {
		t.Fatal("canceled wait must not report timeout")
	}
}

func TestMemberMailboxDrainClearsActivitySignal(t *testing.T) {
	m := NewMemberMailbox()
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "pm", Status: MemberStatusDone})
	if msgs := m.DrainSteering(); len(msgs) != 1 {
		t.Fatalf("drained %d messages, want 1", len(msgs))
	}
	// The stale activity hint must not wake a later wait: nothing is pending.
	timedOut, err := m.WaitForActivity(context.Background(), 30*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForActivity error: %v", err)
	}
	if !timedOut {
		t.Fatal("expected timeout after drain cleared the activity signal")
	}
}

func TestMemberMailboxNilReceiverIsSafe(t *testing.T) {
	var m *MemberMailbox
	m.Enqueue(MemberCompletion{Wake: true, MemberID: "x"})
	if m.HasPending() {
		t.Fatal("nil mailbox must report no pending")
	}
	if msgs := m.DrainSteering(); msgs != nil {
		t.Fatalf("nil mailbox drain = %#v, want nil", msgs)
	}
	if summary := m.PendingSummary(); summary != nil {
		t.Fatalf("nil mailbox summary = %#v, want nil", summary)
	}
	if _, err := m.WaitForActivity(context.Background(), time.Millisecond); err == nil {
		t.Fatal("nil mailbox wait must return an error")
	}
}

func TestMemberMailboxNonWakingItemDoesNotSignal(t *testing.T) {
	mailbox := NewMemberMailbox()
	mailbox.Enqueue(MemberCompletion{
		MemberID: "worker-1", Status: MemberStatusFinding, Payload: "found it", Kind: MemberItemFinding,
	})
	if !mailbox.HasPending() {
		t.Fatal("non-waking item was not queued")
	}
	if mailbox.HasWakingPending() {
		t.Fatal("non-waking item reported a waking pending state")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if timedOut, err := mailbox.WaitForActivity(ctx, 20*time.Millisecond); err != nil || !timedOut {
		t.Fatalf("non-waking item signaled activity: timedOut=%v err=%v", timedOut, err)
	}
	if drained := mailbox.DrainSteering(); len(drained) != 1 {
		t.Fatalf("drained messages = %d, want the queued non-waking item", len(drained))
	}
}

func TestMemberMailboxFindingEntersSteeringAsWorkerEvidence(t *testing.T) {
	mailbox := NewMemberMailbox()
	mailbox.Enqueue(MemberCompletion{
		MemberID: "worker-9", Status: MemberStatusFinding, Payload: "a race is possible",
		Kind: MemberItemFinding, Wake: true,
	})
	if !mailbox.HasWakingPending() {
		t.Fatal("waking finding was not reported as waking")
	}
	steering := mailbox.DrainSteering()
	if len(steering) != 1 || !strings.Contains(steering[0].Content, "[WORKER_FINDING]") ||
		!strings.Contains(steering[0].Content, "worker-9") || !strings.Contains(steering[0].Content, "worker-reported") {
		t.Fatalf("finding steering = %#v", steering)
	}
}

func TestMemberMailboxCoalescesFindingsPerWorker(t *testing.T) {
	mailbox := NewMemberMailbox()
	mailbox.EnqueueCoalesced(MemberCompletion{Kind: MemberItemFinding, MemberID: "worker-1", Status: MemberStatusFinding, Payload: "first", Wake: true})
	mailbox.EnqueueCoalesced(MemberCompletion{Kind: MemberItemFinding, MemberID: "worker-1", Status: MemberStatusFinding, Payload: "latest", Wake: true})
	mailbox.EnqueueCoalesced(MemberCompletion{Kind: MemberItemFinding, MemberID: "worker-2", Status: MemberStatusFinding, Payload: "other", Wake: true})
	pending := mailbox.PendingSummary()
	if len(pending) != 2 {
		t.Fatalf("pending findings = %#v, want one per worker", pending)
	}
	if pending[0].Payload != "latest" {
		t.Fatalf("first finding was not coalesced: %#v", pending[0])
	}
}
