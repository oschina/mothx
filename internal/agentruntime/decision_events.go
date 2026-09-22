package agentruntime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// Decision ledger: adapters persist each approval/question transition as a run
// event whose type is session.DecisionEventType(status) and whose Data carries
// the canonical envelope produced by DecisionEventFields. Both directions live
// here so no adapter hand-assembles or hand-parses the envelope, and moving the
// ledger to a dedicated store is a change to this file alone.

// Decision statuses recorded in the durable ledger.
const (
	DecisionStatusPending   = "pending"
	DecisionStatusRequested = "requested"
	DecisionStatusResolved  = "resolved"
	DecisionStatusCancelled = "cancelled"
	DecisionStatusTimedOut  = "timed_out"
)

// DecisionEventEnvelope is the minimal durable decision envelope: the record
// under the canonical key. Writers that persist only a decision use it directly;
// DecisionEventFields adds an adapter payload.
func DecisionEventEnvelope(record DecisionRecord) map[string]any {
	return map[string]any{"decision": record}
}

// DecisionEventFields is the canonical durable decision envelope. record is the
// Runtime-owned identity and payload is the optional adapter protocol body.
func DecisionEventFields(record DecisionRecord, payload any) map[string]any {
	fields := DecisionEventEnvelope(record)
	fields["payload"] = payload
	return fields
}

// NewDecisionRecord is the single owner of the request/resolution record shape:
// a pending status becomes a request record carrying the optional deadline, any
// other status a resolution record.
func NewDecisionRecord(request DecisionRequest, status, value string, payload any, expiresAt time.Time) (DecisionRecord, error) {
	if status == DecisionStatusPending {
		return NewDecisionRequestRecordWithDeadline(request, payload, expiresAt)
	}
	return NewDecisionResolutionRecord(request, DecisionResolution{ID: request.ID, Kind: request.Kind, Status: status, Value: value}, payload)
}

// DecisionTransition is one durable decision state change.
type DecisionTransition struct {
	Request   DecisionRequest
	Status    string
	Value     string
	Payload   any
	ExpiresAt time.Time
	Source    string
	Model     string
	Mode      string
}

// BuildDecisionEvent builds the canonical durable run event for a transition.
func BuildDecisionEvent(t DecisionTransition) (RunEvent, error) {
	record, err := NewDecisionRecord(t.Request, t.Status, t.Value, t.Payload, t.ExpiresAt)
	if err != nil {
		return RunEvent{}, err
	}
	data, err := json.Marshal(DecisionEventFields(record, t.Payload))
	if err != nil {
		return RunEvent{}, err
	}
	return RunEvent{
		SessionID: t.Request.SessionID, RunID: t.Request.RunID,
		EventType: session.DecisionEventType(t.Status), Source: t.Source, Status: t.Status,
		Model: t.Model, Mode: t.Mode, Timestamp: time.Now(), Data: data,
	}, nil
}

// RecordDecisionEvent persists a transition through sink. A nil sink is a no-op.
func RecordDecisionEvent(sink RunEventSink, t DecisionTransition) (string, error) {
	event, err := BuildDecisionEvent(t)
	if err != nil {
		return "", err
	}
	if sink == nil {
		return "", nil
	}
	return sink.Record(event)
}

// DecodeDecisionEvent decodes a run event that belongs to the decision ledger,
// defaulting session/run identity from the persisted row. ok is false for
// unrelated events or a malformed envelope.
func DecodeDecisionEvent(ev session.SessionRunEvent) (DecisionRecord, bool) {
	if !session.IsDecisionEventType(ev.EventType) {
		return DecisionRecord{}, false
	}
	var envelope struct {
		Decision DecisionRecord `json:"decision"`
	}
	if json.Unmarshal(ev.Data, &envelope) != nil || envelope.Decision.ID == "" {
		return DecisionRecord{}, false
	}
	if envelope.Decision.SessionID == "" {
		envelope.Decision.SessionID = ev.SessionID
	}
	if envelope.Decision.RunID == "" {
		envelope.Decision.RunID = ev.RunID
	}
	return envelope.Decision, true
}

// LoadDecisionRecords returns the decoded decision ledger for a session in
// durable order.
func LoadDecisionRecords(sessionDir, sessionID string) ([]DecisionRecord, error) {
	return LoadDecisionRecordsContext(context.Background(), sessionDir, sessionID)
}

// LoadDecisionRecordsContext is the cancellable form used by recovery paths.
func LoadDecisionRecordsContext(ctx context.Context, sessionDir, sessionID string) ([]DecisionRecord, error) {
	events, err := session.ListSessionRunEventsContext(ctx, sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]DecisionRecord, 0, len(events))
	for _, ev := range events {
		if record, ok := DecodeDecisionEvent(ev); ok {
			records = append(records, record)
		}
	}
	return records, nil
}

// LoadRunDecisionRecords returns the decision ledger for a single run. An empty
// runID returns the whole session ledger.
func LoadRunDecisionRecords(sessionDir, sessionID, runID string) ([]DecisionRecord, error) {
	return LoadRunDecisionRecordsContext(context.Background(), sessionDir, sessionID, runID)
}

// LoadRunDecisionRecordsContext is the cancellable form used by recovery paths.
func LoadRunDecisionRecordsContext(ctx context.Context, sessionDir, sessionID, runID string) ([]DecisionRecord, error) {
	records, err := LoadDecisionRecordsContext(ctx, sessionDir, sessionID)
	if err != nil {
		return nil, err
	}
	if runID == "" {
		return records, nil
	}
	filtered := make([]DecisionRecord, 0, len(records))
	for _, record := range records {
		if record.RunID == runID {
			filtered = append(filtered, record)
		}
	}
	return filtered, nil
}
