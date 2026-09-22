package session

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"time"
)

// InputResourceEvent is the canonical lifecycle projection for one Runtime
// materialized resource. Transport references are intentionally absent.
type InputResourceEvent struct {
	ID         string
	SessionID  string
	ResourceID string
	RunID      string
	EventType  string
	Status     string
	Timestamp  time.Time
	Data       json.RawMessage
}

// AppendInputResourceEventTx records a resource lifecycle event in the caller's
// transaction. Deterministic event IDs make retries safe after an unknown
// commit result.
func AppendInputResourceEventTx(tx *dao.Tx, event InputResourceEvent) error {
	if tx == nil {
		return fmt.Errorf("input resource event transaction is nil")
	}
	if event.ID == "" || event.SessionID == "" || event.ResourceID == "" || event.EventType == "" {
		return fmt.Errorf("input resource event identity and type are required")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	data := event.Data
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	return dao.NewInputResourceDAO(nil).AppendEvent(context.Background(), tx, &dao.InputResourceEventRecord{
		ID: event.ID, SessionID: event.SessionID, ResourceID: event.ResourceID, RunID: event.RunID,
		EventType: event.EventType, Status: event.Status, Timestamp: event.Timestamp.Format(time.RFC3339Nano), Data: string(data),
	})
}

// SaveInputResourceEvent appends a resource lifecycle event outside a larger
// transaction. Runtime admission paths should use AppendInputResourceEventTx.
func SaveInputResourceEvent(ctx context.Context, sessionDir string, event InputResourceEvent) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
		return AppendInputResourceEventTx(tx, event)
	})
}

// ListInputResourceEvents returns resource lifecycle events in durable order.
func ListInputResourceEvents(ctx context.Context, sessionDir, sessionID string) ([]InputResourceEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sessionID == "" {
		return nil, nil
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewInputResourceDAO(db.Bun()).ListEvents(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var events []InputResourceEvent
	for _, record := range records {
		event := InputResourceEvent{ID: record.ID, SessionID: record.SessionID, ResourceID: record.ResourceID,
			RunID: record.RunID, EventType: record.EventType, Status: record.Status,
			Timestamp: parseSessionTimestamp(record.Timestamp), Data: json.RawMessage(record.Data)}
		events = append(events, event)
	}
	return events, nil
}

// bindInputResourcesToRunTx attaches Runtime-prepared input resources while
// the intent, Run row, and start event are being admitted. A resource already
// attached to another attempt of the same immutable intent is reusable for a
// retry; ownership from a different intent is rejected.
func bindInputResourcesToRunTx(tx *dao.Tx, sessionID, runID, intentID string, resourceIDs []string) error {
	if len(resourceIDs) == 0 {
		return nil
	}
	if sessionID == "" || runID == "" {
		return fmt.Errorf("input resource binding requires session and Run IDs")
	}
	seen := make(map[string]struct{}, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		if resourceID == "" {
			continue
		}
		if _, ok := seen[resourceID]; ok {
			continue
		}
		seen[resourceID] = struct{}{}

		ownerRunID, status, err := dao.NewInputResourceDAO(nil).OwnerRun(context.Background(), tx, resourceID, sessionID)
		if err == dao.ErrNoRows {
			return fmt.Errorf("input resource %s does not belong to session", resourceID)
		}
		if err != nil {
			return err
		}
		if status == "missing" || status == "deleted" {
			return fmt.Errorf("input resource %s is not attachable (status %s)", resourceID, status)
		}
		if ownerRunID == "" || ownerRunID == runID {
			if err := dao.NewInputResourceDAO(nil).UpdateAttachment(context.Background(), tx, sessionID, resourceID, runID); err != nil {
				return err
			}
			if err := AppendInputResourceEventTx(tx, InputResourceEvent{
				ID:        "input-resource-" + resourceID + "-attached-" + runID,
				SessionID: sessionID, ResourceID: resourceID, RunID: runID,
				EventType: "input_resource_attached", Status: "attached",
				Data: json.RawMessage(`{"runId":"` + runID + `"}`),
			}); err != nil {
				return err
			}
			continue
		}

		// Retries reuse the original immutable input resource. There is no need
		// to overwrite its canonical owner just to represent another attempt.
		ownerIntentID, err := dao.NewInputResourceDAO(nil).OwnerIntent(context.Background(), tx, ownerRunID, sessionID)
		if err != nil {
			return fmt.Errorf("resolve input resource %s owner: %w", resourceID, err)
		}
		if intentID == "" || ownerIntentID != intentID {
			return fmt.Errorf("input resource %s is already attached to another Run", resourceID)
		}
	}
	return nil
}
