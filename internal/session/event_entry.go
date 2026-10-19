package session

import (
	"encoding/json"
	"fmt"
)

// EventRecordFromEntry preserves the existing Session entry schema inside the
// EventStore payload. Keeping the entry envelope intact makes the first
// storage migration lossless: replay semantics stay in one place while the
// physical payload leaves the shared SQLite B-tree.
func EventRecordFromEntry(entry interface{}, sessionID, runID, turnID string) (EventRecord, error) {
	entry = normalizeSessionEntry(entry)
	id, kind, _, timestamp := getEntryMetadata(entry)
	if id == "" || kind == "" {
		return EventRecord{}, errorsForEventEntry("entry has no ID or type")
	}
	payload, err := marshalSessionEntry(entry)
	if err != nil {
		return EventRecord{}, err
	}
	return EventRecord{
		EventID: id, SessionID: sessionID, RunID: runID, TurnID: turnID,
		Kind: kind, Payload: payload, CreatedAt: timestamp,
	}, nil
}

// DecodeEventRecord reverses EventRecordFromEntry. Only committed records
// should reach this function; physical batches are not canonical by themselves.
//
// The switch is the closed set of entry types the EventStore can replay. A type
// that exists in the legacy schema but has no declared structure, reference
// rewrite policy, or provider projection — today `custom` and `custom_message`
// — must fail loudly here rather than decode into an opaque blob: migration
// routes every legacy row through this decoder, so an unsupported type refuses
// the export instead of silently dropping it from the child transcript. Fork
// already refuses the same pair for the same reason. Nothing in production
// writes those types today; see the named debt in the proposal.
func DecodeEventRecord(record EventRecord) (interface{}, error) {
	var entry interface{}
	switch EntryType(record.Kind) {
	case EntrySession:
		entry = &Header{}
	case EntryMessage:
		entry = &MessageEntry{}
	case EntryModelChange:
		entry = &ModelChangeEntry{}
	case EntryModeChange:
		entry = &ModeChangeEntry{}
	case EntryThinkingChange:
		entry = &ThinkingLevelChangeEntry{}
	case EntryAdditionalDirectories:
		entry = &AdditionalDirectoriesEntry{}
	case EntryCompaction:
		entry = &CompactionEntry{}
	case EntryContentOverride:
		entry = &ContentOverrideEntry{}
	case EntryBranchSummary:
		entry = &BranchSummaryEntry{}
	case EntryLabel:
		entry = &LabelEntry{}
	case EntrySessionInfo:
		entry = &SessionInfoEntry{}
	case EntryTurnStart:
		entry = &TurnStartEntry{}
	case EntryTurnEnd:
		entry = &TurnEndEntry{}
	default:
		return nil, fmt.Errorf("unsupported event entry kind %q", record.Kind)
	}
	if err := json.Unmarshal(record.Payload, entry); err != nil {
		return nil, fmt.Errorf("decode event %s: %w", record.EventID, err)
	}
	return entryValue(entry), nil
}

func entryValue(entry interface{}) interface{} {
	switch value := entry.(type) {
	case *Header:
		return *value
	case *MessageEntry:
		return *value
	case *ModelChangeEntry:
		return *value
	case *ModeChangeEntry:
		return *value
	case *ThinkingLevelChangeEntry:
		return *value
	case *AdditionalDirectoriesEntry:
		return *value
	case *CompactionEntry:
		return *value
	case *ContentOverrideEntry:
		return *value
	case *BranchSummaryEntry:
		return *value
	case *LabelEntry:
		return *value
	case *SessionInfoEntry:
		return *value
	case *TurnStartEntry:
		return *value
	case *TurnEndEntry:
		return *value
	default:
		return entry
	}
}

func errorsForEventEntry(message string) error {
	return fmt.Errorf("invalid session event entry: %s", message)
}
