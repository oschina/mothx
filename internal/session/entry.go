package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
)

// EntryType identifies the type of a session entry.
type EntryType string

const (
	EntrySession               EntryType = "session"
	EntryMessage               EntryType = "message"
	EntryModelChange           EntryType = "model_change"
	EntryModeChange            EntryType = "mode_change"
	EntryThinkingChange        EntryType = "thinking_level_change"
	EntryAdditionalDirectories EntryType = "additional_directories"
	EntryCompaction            EntryType = "compaction"
	EntryContentOverride       EntryType = "content_override"
	EntryBranchSummary         EntryType = "branch_summary"
	EntryCustom                EntryType = "custom"
	EntryCustomMessage         EntryType = "custom_message"
	EntryLabel                 EntryType = "label"
	EntrySessionInfo           EntryType = "session_info"
	EntryTurnStart             EntryType = "turn_start"
	EntryTurnEnd               EntryType = "turn_end"
)

// EntryBase contains common fields for all session entries.
type EntryBase struct {
	Type      EntryType `json:"type"`
	ID        string    `json:"id"`
	ParentID  *string   `json:"parentId"`
	Timestamp time.Time `json:"timestamp"`
}

// Header is the first line of a session file.
type Header struct {
	Type            EntryType `json:"type"`
	Version         int       `json:"version"`
	ID              string    `json:"id"`
	Timestamp       time.Time `json:"timestamp"`
	Cwd             string    `json:"cwd"`
	ParentSession   string    `json:"parentSession,omitempty"`
	ChannelType     string    `json:"channelType,omitempty"`
	ChannelID       string    `json:"channelId,omitempty"`
	ForkBoundarySeq int64     `json:"forkBoundarySeq,omitempty"`
	SeedLength      int64     `json:"seedLength,omitempty"`
	ForkKind        string    `json:"forkKind,omitempty"`
	// ExpertID is the bound expert bundle name (empty = no expert identity).
	// Persisted like the channel binding fields so reloads restore identity.
	ExpertID string `json:"expertId,omitempty"`
}

// MessageEntry contains a conversation message.
type MessageEntry struct {
	EntryBase
	Message provider.Message `json:"message"`
}

// ModelChangeEntry records a model switch.
type ModelChangeEntry struct {
	EntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// ModeChangeEntry records a session execution mode change.
type ModeChangeEntry struct {
	EntryBase
	Mode string `json:"mode"`
}

// ThinkingLevelChangeEntry records a thinking level change.
type ThinkingLevelChangeEntry struct {
	EntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

// AdditionalDirectoriesEntry records the complete ordered directory set
// granted to a session. Replacements are replayable session entries.
type AdditionalDirectoriesEntry struct {
	EntryBase
	Directories []string `json:"directories"`
}

// CompactionEntry records a context compaction.
type CompactionEntry struct {
	EntryBase
	Summary              string `json:"summary"`
	FirstKeptEntry       string `json:"firstKeptEntryId"`
	TokensBefore         int    `json:"tokensBefore"`
	SummaryVersion       int    `json:"summaryVersion,omitempty"`
	PreviousCompactionID string `json:"previousCompactionId,omitempty"`
	LastSummarizedEntry  string `json:"lastSummarizedEntryId,omitempty"`
}

// ContentOverrideEntry records an append-only, replayable replacement of a
// previously persisted message entry's content. Replay substitutes Message for
// the target entry; the original entry stays in the log for audit. It is used
// when a provider permanently refuses content (for example an image flagged by
// content inspection) and the conversation must continue without it.
//
// Replacements are replayable session entries, exactly like directory grants:
// the log is never mutated or truncated, only overlaid on replay.
type ContentOverrideEntry struct {
	EntryBase
	TargetEntryID string           `json:"targetEntryId"`
	Message       provider.Message `json:"message"`
	Reason        string           `json:"reason,omitempty"`
	Code          string           `json:"code,omitempty"`
}

// BranchSummaryEntry records a branch switch summary.
type BranchSummaryEntry struct {
	EntryBase
	Summary string `json:"summary"`
	FromID  string `json:"fromId"`
}

// LabelEntry records a user-defined label on an entry.
type LabelEntry struct {
	EntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label,omitempty"`
}

// SessionInfoEntry stores session metadata.
type SessionInfoEntry struct {
	EntryBase
	Name   string `json:"name"`
	Source string `json:"source,omitempty"` // "manual" or "auto"
}

// TurnStartEntry marks the durable beginning of a logical conversation turn.
// It is persisted for boundary recovery but is excluded from provider replay.
type TurnStartEntry struct {
	EntryBase
	TurnID   string `json:"turnId"`
	IntentID string `json:"intentId,omitempty"`
	RunID    string `json:"runId,omitempty"`
	Attempt  int    `json:"attempt,omitempty"`
}

// TurnEndEntry marks the durable terminal boundary of a logical conversation
// turn. It is persisted for fork resolution and recovery, not model replay.
type TurnEndEntry struct {
	EntryBase
	TurnID     string `json:"turnId"`
	IntentID   string `json:"intentId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	Status     string `json:"status"`
	StopReason string `json:"stopReason,omitempty"`
}

// GenerateID generates a random 16-character hex ID. IDs such as entries.id
// are UNIQUE across the whole shared sessions.db, so the previous 32-bit
// space made collisions plausible at tens of millions of entries; 64 bits
// keeps the birthday probability negligible.
func GenerateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID on crypto failure
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
