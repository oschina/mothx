package agentruntime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/session"
)

func TestBuildAndDecodeDecisionEventRoundTrip(t *testing.T) {
	deadline := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	event, err := BuildDecisionEvent(DecisionTransition{
		Request:   DecisionRequest{ID: "approval-1", SessionID: "s-1", RunID: "r-1", Kind: DecisionApproval},
		Status:    DecisionStatusPending,
		Payload:   map[string]any{"tool": "bash"},
		ExpiresAt: deadline,
		Source:    "tui",
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != "decision_pending" || event.Status != DecisionStatusPending || event.Source != "tui" {
		t.Fatalf("event = %#v", event)
	}
	record, ok := DecodeDecisionEvent(session.SessionRunEvent{SessionID: "s-1", RunID: "r-1", EventType: event.EventType, Data: event.Data})
	if !ok {
		t.Fatal("decision event was not decoded")
	}
	if record.ID != "approval-1" || record.Kind != DecisionApproval || record.Status != DecisionStatusPending {
		t.Fatalf("record = %#v", record)
	}
	if record.SessionID != "s-1" || record.RunID != "r-1" || !record.ExpiresAt.Equal(deadline) {
		t.Fatalf("record identity/deadline = %#v", record)
	}
}

func TestDecodeDecisionEventDefaultsIdentityAndRejectsForeign(t *testing.T) {
	data, err := json.Marshal(DecisionEventFields(DecisionRecord{ID: "q-1", Kind: DecisionQuestion, Status: DecisionStatusPending}, nil))
	if err != nil {
		t.Fatal(err)
	}
	// A legacy serve type is still part of the ledger, and identity defaults
	// from the persisted row.
	record, ok := DecodeDecisionEvent(session.SessionRunEvent{SessionID: "s-2", RunID: "r-2", EventType: "question_requested", Data: data})
	if !ok || record.SessionID != "s-2" || record.RunID != "r-2" {
		t.Fatalf("legacy decode = %#v ok=%v", record, ok)
	}
	for name, event := range map[string]session.SessionRunEvent{
		"foreign type":     {EventType: "started", Data: data},
		"empty decision":   {EventType: "decision_pending", Data: json.RawMessage(`{"decision":{}}`)},
		"malformed json":   {EventType: "decision_pending", Data: json.RawMessage(`not json`)},
		"unrelated prefix": {EventType: "decision_deadline", Data: data},
	} {
		if _, ok := DecodeDecisionEvent(event); ok {
			t.Fatalf("%s should not decode", name)
		}
	}
}

func TestNewDecisionRecordShapeByStatus(t *testing.T) {
	request := DecisionRequest{ID: "d-1", SessionID: "s-1", RunID: "r-1", Kind: DecisionApproval}
	deadline := time.Now().Add(time.Minute)
	pending, err := NewDecisionRecord(request, DecisionStatusPending, "", map[string]any{"tool": "bash"}, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != DecisionStatusPending || !pending.ExpiresAt.Equal(deadline) {
		t.Fatalf("pending = %#v", pending)
	}
	resolved, err := NewDecisionRecord(request, DecisionStatusResolved, "allow", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != DecisionStatusResolved || resolved.Value != "allow" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestLoadDecisionRecordsBySessionAndRun(t *testing.T) {
	sessionDir := t.TempDir()
	sink := SessionRunEventSink{SessionDir: sessionDir}
	for _, transition := range []DecisionTransition{
		{Request: DecisionRequest{ID: "a", SessionID: "s-1", RunID: "r-1", Kind: DecisionApproval}, Status: DecisionStatusPending},
		{Request: DecisionRequest{ID: "a", SessionID: "s-1", RunID: "r-1", Kind: DecisionApproval}, Status: DecisionStatusResolved, Value: "allow"},
		{Request: DecisionRequest{ID: "b", SessionID: "s-1", RunID: "r-2", Kind: DecisionQuestion}, Status: DecisionStatusPending},
	} {
		if _, err := RecordDecisionEvent(sink, transition); err != nil {
			t.Fatal(err)
		}
	}
	// A non-decision event must not be surfaced as a record.
	if _, err := sink.Record(RunEvent{SessionID: "s-1", RunID: "r-1", EventType: "started", Source: "tui", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	all, err := LoadDecisionRecords(sessionDir, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all records = %#v", all)
	}
	run1, err := LoadRunDecisionRecords(sessionDir, "s-1", "r-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(run1) != 2 {
		t.Fatalf("run-1 records = %#v", run1)
	}
	if pending := ReplayDecisions(run1); len(pending) != 0 {
		t.Fatalf("resolved run should have no pending decision: %#v", pending)
	}
	if pending := ReplayDecisions(all); len(pending) != 1 || pending["b"].RunID != "r-2" {
		t.Fatalf("session pending = %#v", pending)
	}
}
