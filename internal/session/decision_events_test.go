package session

import "testing"

func TestIsDecisionEventType(t *testing.T) {
	for _, eventType := range []string{
		"decision_pending", "decision_requested", "decision_resolved",
		"decision_cancelled", "decision_timed_out",
		"approval_requested", "question_requested", "approval_resolved", "question_resolved",
	} {
		if !IsDecisionEventType(eventType) {
			t.Fatalf("%q should be a decision event", eventType)
		}
	}
	for _, eventType := range []string{"", "started", "finished", "decision_deadline", "decision"} {
		if IsDecisionEventType(eventType) {
			t.Fatalf("%q should not be a decision event", eventType)
		}
	}
}
