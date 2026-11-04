package acp

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
)

func TestACPMailboxWakeProjectsCanonicalRunStatusAndClearsState(t *testing.T) {
	var out bytes.Buffer
	rt := &sessionRuntime{
		id:        "session-wake",
		runtime:   &agentruntime.SessionRuntime{ID: "session-wake"},
		execution: &agentruntime.ExecutionRuntime{},
		decisions: &agentruntime.DecisionService{},
	}
	s := &server{
		w:        &out,
		sessions: map[string]*sessionRuntime{"session-wake": rt},
	}
	run := agentruntime.MailboxWakeRun{
		RunID: "wake_run-1", SessionID: "session-wake", Model: "mailbox-model", Mode: "yolo", StartedAt: time.Now(),
	}

	s.projectMailboxWakeStarted(rt, run)
	rt.cancelMu.Lock()
	if rt.runID != run.RunID || rt.promptID != "wake_"+run.RunID {
		t.Fatalf("wake start state = runID %q promptID %q", rt.runID, rt.promptID)
	}
	if rt.messageID == "" || rt.thoughtMessageID == "" || rt.terminalNotified {
		t.Fatalf("wake stream identity not initialized: %#v", rt)
	}
	if rt.mailboxWakeCtx == nil || rt.mailboxWakeCancel == nil {
		t.Fatal("wake cancellation context was not installed")
	}
	rt.cancelMu.Unlock()

	s.projectMailboxWakeFinished(rt, run, agentruntime.RunStateCompleted, nil)
	rt.cancelMu.Lock()
	if rt.runID != "" || rt.promptID != "" || rt.messageID != "" || rt.mailboxWakeCtx != nil {
		t.Fatalf("wake finish left canonical adapter state behind: %#v", rt)
	}
	rt.cancelMu.Unlock()

	wire := out.String()
	if !strings.Contains(wire, `"event":"run_status"`) || !strings.Contains(wire, `"status":"running"`) {
		t.Fatalf("missing running run_status projection: %s", wire)
	}
	if !strings.Contains(wire, `"status":"completed"`) {
		t.Fatalf("missing completed run_status projection: %s", wire)
	}
}
