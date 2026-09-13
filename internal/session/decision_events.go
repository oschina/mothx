package session

// The durable decision ledger records every approval/question transition as a
// run event. A canonical name is decisionEventPrefix + the decision status
// (pending, resolved, cancelled, timed_out); the legacy approval_*/question_*
// names are still written by the serve compatibility bridge and stay
// recognized so every reader agrees on one vocabulary.
//
// The vocabulary lives in session because session owns the run-event row schema
// and the fork boundary keys on it. The decision envelope itself (and every
// adapter-facing helper) lives in agentruntime, which owns DecisionRecord.
const decisionEventPrefix = "decision_"

var decisionEventTypes = map[string]struct{}{
	decisionEventPrefix + "pending":   {},
	decisionEventPrefix + "requested": {},
	decisionEventPrefix + "resolved":  {},
	decisionEventPrefix + "cancelled": {},
	decisionEventPrefix + "timed_out": {},
	// Legacy names written by the serve compatibility bridge.
	"approval_requested": {},
	"question_requested": {},
	"approval_resolved":  {},
	"question_resolved":  {},
}

// DecisionEventType returns the durable run-event type for a decision status.
func DecisionEventType(status string) string { return decisionEventPrefix + status }

// IsDecisionEventType reports whether eventType belongs to the durable decision
// ledger, including the legacy serve names.
func IsDecisionEventType(eventType string) bool {
	_, ok := decisionEventTypes[eventType]
	return ok
}
