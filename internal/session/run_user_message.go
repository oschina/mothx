package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/provider"
)

// RunUserEntryID is the deterministic transcript identity for a Run's
// admitted user message. Retries do not create another user entry.
func RunUserEntryID(runID string) string {
	if runID == "" {
		return ""
	}
	return "run-user-" + runID
}

// RunAssistantEntryID is the deterministic transcript identity for the final
// assistant entry committed by a Runtime-owned conversation Run. Keeping the
// identity tied to the Run makes terminal retries and recovery idempotent.
func RunAssistantEntryID(runID string) string {
	if runID == "" {
		return ""
	}
	return "run-assistant-" + runID
}

// RunTerminalEventID is the deterministic lifecycle event identity for a
// terminal Run. Event type is included so a cancelled recovery cannot collide
// with a previously selected terminal outcome.
func RunTerminalEventID(runID, eventType string) string {
	if runID == "" || eventType == "" {
		return ""
	}
	return "run-terminal-" + runID + "-" + eventType
}

// RunAssistantMessageFingerprint returns a stable digest for an assistant
// message. It is useful to validate a recovery payload without persisting the
// message in a separate adapter-owned table.
func RunAssistantMessageFingerprint(runID string, message provider.Message) string {
	message, _ = provider.NormalizeMessage(message)
	encoded, err := json.Marshal(struct {
		RunID   string           `json:"runId"`
		Message provider.Message `json:"message"`
	}{RunID: runID, Message: message})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func appendRunUserMessageTx(tx *dao.Tx, run SessionRun) error {
	if run.UserMessage == nil {
		return nil
	}
	if run.SessionID == "" || run.ID == "" {
		return fmt.Errorf("session run identity is required for user entry")
	}
	message := *run.UserMessage
	message, _ = provider.NormalizeMessage(message)
	if message.SystemInjected {
		return fmt.Errorf("runtime-admitted user entry cannot be system injected")
	}
	if message.Role == "" {
		message.Role = "user"
	}
	if message.Role != "user" {
		return fmt.Errorf("runtime-admitted entry must have user role")
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = run.StartedAt
		if message.Timestamp.IsZero() {
			message.Timestamp = time.Now()
		}
	}
	entryID := run.UserEntryID
	if entryID == "" {
		entryID = RunUserEntryID(run.ID)
	}
	parentID, err := currentLeafTx(tx, run.SessionID)
	if err != nil {
		return err
	}
	entry := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: entryID, ParentID: stringPtr(parentID), Timestamp: message.Timestamp},
		Message:   message,
	}
	if _, err := appendTurnEntryTx(tx, run.SessionID, entry, parentID); err != nil {
		return fmt.Errorf("append runtime user entry: %w", err)
	}
	return nil
}

// appendRunAssistantMessageTx appends the final assistant message during the
// same transaction that closes the Run/turn and creates delivery operations.
// Existing deterministic IDs are treated as an idempotent retry.
func appendRunAssistantMessageTx(tx *dao.Tx, run SessionRun) error {
	if run.AssistantMessage == nil {
		return nil
	}
	if run.SessionID == "" || run.ID == "" {
		return fmt.Errorf("session run identity is required for assistant entry")
	}
	message := *run.AssistantMessage
	message, _ = provider.NormalizeMessage(message)
	if message.SystemInjected {
		return fmt.Errorf("runtime assistant entry cannot be system injected")
	}
	if message.Role == "" {
		message.Role = "assistant"
	}
	if message.Role != "assistant" {
		return fmt.Errorf("runtime assistant entry must have assistant role")
	}
	if message.Timestamp.IsZero() {
		switch {
		case run.FinishedAt != nil && !run.FinishedAt.IsZero():
			message.Timestamp = *run.FinishedAt
		case !run.StartedAt.IsZero():
			message.Timestamp = run.StartedAt
		default:
			// A recovery payload without lifecycle timestamps still needs a
			// deterministic value so an idempotent retry cannot change its
			// message fingerprint.
			message.Timestamp = time.Unix(0, 0).UTC()
		}
	}
	entryID := run.AssistantEntryID
	if entryID == "" {
		entryID = RunAssistantEntryID(run.ID)
	}
	existingRecord, err := dao.NewConversationTurnDAO(nil).Entry(context.Background(), tx, entryID)
	if err == nil {
		if existingRecord.SessionID != run.SessionID || existingRecord.Type != string(EntryMessage) {
			return fmt.Errorf("assistant entry %s belongs to another session or entry type", entryID)
		}
		var existing MessageEntry
		if err := json.Unmarshal([]byte(existingRecord.Data), &existing); err != nil {
			return fmt.Errorf("assistant entry %s has invalid persisted content", entryID)
		}
		if RunAssistantMessageFingerprint(run.ID, existing.Message) != RunAssistantMessageFingerprint(run.ID, message) {
			return fmt.Errorf("assistant entry %s conflicts with the terminal message", entryID)
		}
		return nil
	}
	if err != dao.ErrNoRows {
		return err
	}
	parentID, err := currentLeafTx(tx, run.SessionID)
	if err != nil {
		return err
	}
	entry := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: entryID, ParentID: stringPtr(parentID), Timestamp: message.Timestamp},
		Message:   message,
	}
	if _, err := appendTurnEntryTx(tx, run.SessionID, entry, parentID); err != nil {
		return fmt.Errorf("append runtime assistant entry: %w", err)
	}
	return nil
}
