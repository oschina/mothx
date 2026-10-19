package session

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

func TestEventEntryRoundTripPreservesSessionEntry(t *testing.T) {
	parent := "parent-entry"
	original := MessageEntry{
		EntryBase: EntryBase{Type: EntryMessage, ID: "message-entry", ParentID: &parent, Timestamp: time.Unix(10, 0).UTC()},
		Message:   provider.NewUserMessage("hello"),
	}
	record, err := EventRecordFromEntry(original, "session-1", "run-1", "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEventRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	message, ok := decoded.(MessageEntry)
	if !ok {
		t.Fatalf("decoded type = %T, want MessageEntry", decoded)
	}
	if message.ID != original.ID || message.ParentID == nil || *message.ParentID != parent || message.Message.Content != "hello" {
		t.Fatalf("decoded message = %#v, want %#v", message, original)
	}
}

func TestEventEntryRoundTripPreservesControlEntries(t *testing.T) {
	entries := []interface{}{
		CompactionEntry{EntryBase: EntryBase{Type: EntryCompaction, ID: "compact-1", Timestamp: time.Now().UTC()}, Summary: "summary", FirstKeptEntry: "message-entry", TokensBefore: 10},
		TurnEndEntry{EntryBase: EntryBase{Type: EntryTurnEnd, ID: "end-1", Timestamp: time.Now().UTC()}, TurnID: "turn-1", Status: "completed"},
		SessionInfoEntry{EntryBase: EntryBase{Type: EntrySessionInfo, ID: "info-1", Timestamp: time.Now().UTC()}, Name: "demo", Source: "manual"},
	}
	for _, original := range entries {
		record, err := EventRecordFromEntry(original, "session-1", "run-1", "turn-1")
		if err != nil {
			t.Fatalf("encode %T: %v", original, err)
		}
		decoded, err := DecodeEventRecord(record)
		if err != nil {
			t.Fatalf("decode %s: %v", record.Kind, err)
		}
		if record.EventID == "" || decoded == nil {
			t.Fatalf("round trip %T = %#v", original, decoded)
		}
	}
}
