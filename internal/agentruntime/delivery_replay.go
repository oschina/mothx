package agentruntime

import (
	"encoding/json"

	"github.com/oschina/mothx/internal/session"
)

// DeliveryRecord is the protocol-neutral projection of a durable delivery
// handoff. The actual message remains in the session transcript.
type DeliveryRecord struct {
	RunID          string
	SessionID      string
	Pending        bool
	AssistantEntry string
	Status         string
	Source         string
}

// ReplayDeliveries reconstructs pending channel/background deliveries from
// durable run events. Unknown events and protocol payloads remain untouched.
func ReplayDeliveries(events []session.SessionRunEvent) map[string]DeliveryRecord {
	pending := make(map[string]DeliveryRecord)
	for _, event := range events {
		var payload struct {
			Pending        bool   `json:"channelDeliveryPending"`
			AssistantEntry string `json:"assistantEntryId"`
		}
		if len(event.Data) > 0 {
			_ = json.Unmarshal(event.Data, &payload)
		}
		switch event.EventType {
		case "finished":
			if payload.Pending && payload.AssistantEntry != "" {
				pending[event.RunID] = DeliveryRecord{
					RunID: event.RunID, SessionID: event.SessionID, Pending: true,
					AssistantEntry: payload.AssistantEntry, Status: event.Status, Source: event.Source,
				}
			}
		case "channel_delivery_reconciled":
			delete(pending, event.RunID)
		}
	}
	return pending
}

func ReplayDeliveriesFromRunEvents(events []RunEvent) map[string]DeliveryRecord {
	persisted := make([]session.SessionRunEvent, 0, len(events))
	for _, event := range events {
		persisted = append(persisted, session.SessionRunEvent{
			ID: event.ID, SessionID: event.SessionID, RunID: event.RunID,
			EventType: event.EventType, Source: event.Source, Status: event.Status,
			Model: event.Model, Mode: event.Mode, Timestamp: event.Timestamp, Data: event.Data,
		})
	}
	return ReplayDeliveries(persisted)
}

func NewDeliveryReconciledEvent(sessionID, runID, source string, data json.RawMessage) RunEvent {
	return RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "channel_delivery_reconciled",
		Source: source, Status: "delivered", Data: data,
	}
}
