package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/startvibecoding/mothx/internal/dao"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/provider"
)

type ForkKind string

const (
	ForkKindSession ForkKind = "session"
	ForkKindMessage ForkKind = "message"
	ForkKindUnknown ForkKind = ""
)

var (
	ErrForkSessionNotFound     = errors.New("source session not found")
	ErrForkSessionActive       = errors.New("source session is active")
	ErrForkNoCompletedTurn     = errors.New("source session has no completed conversation turn")
	ErrForkUnavailable         = errors.New("fork boundary is unavailable")
	ErrForkInvalidBoundary     = errors.New("fork boundary is invalid")
	ErrForkUnsupportedEntry    = errors.New("fork contains unsupported entry")
	ErrForkIdempotencyRequired = errors.New("fork request ID is required")
	ErrForkIdempotencyTooLong  = errors.New("fork request ID is too long")
	ErrForkIdempotencyConflict = errors.New("fork idempotency request conflicts")
)

type ForkOptions struct {
	SourceSessionID string
	AtSeq           *int64
	RequestID       string
	TitleMode       string
	// ExpertID overrides the forked session's expert binding when non-nil
	// (empty string unbinds); nil preserves the source session's binding.
	ExpertID *string
}

type ForkResult struct {
	SessionID       string   `json:"sessionId"`
	ParentSessionID string   `json:"parentSessionId"`
	ForkKind        ForkKind `json:"forkKind"`
	BoundarySeq     int64    `json:"boundarySeq"`
	SeedLength      int64    `json:"seedLength"`
}

type forkSourceEntry struct {
	Seq       int64
	ID        string
	Type      string
	ParentID  dao.NullString
	Timestamp string
	Data      string
}

type forkSourceFingerprint struct {
	maxSeq     int64
	leaf       string
	openTurns  int64
	activeRuns int64
}

func ForkSession(ctx context.Context, sessionDir string, options ForkOptions) (ForkResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(options.SourceSessionID) == "" {
		return ForkResult{}, ErrForkSessionNotFound
	}
	if strings.TrimSpace(options.RequestID) == "" {
		return ForkResult{}, ErrForkIdempotencyRequired
	}
	if len(options.RequestID) > 256 {
		return ForkResult{}, ErrForkIdempotencyTooLong
	}
	if options.TitleMode == "" {
		options.TitleMode = "increment"
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return ForkResult{}, err
	}
	requestHash := hashForkRequest(options.RequestID)
	fingerprint := forkFingerprint(options)
	// Idempotent retries must return the original child even if the source has
	// since started another run. The durable request record is authoritative.
	forkDAO := dao.NewForkDAO(db.Bun())
	existing, err := forkDAO.FindRequest(ctx, db.Bun(), requestHash, options.SourceSessionID)
	var existingChild, existingFingerprint string
	if err == nil {
		existingChild, existingFingerprint = existing.ChildSessionID, existing.RequestFingerprint
	}
	if err == nil {
		if existingFingerprint != fingerprint {
			return ForkResult{}, ErrForkIdempotencyConflict
		}
		return forkResultByDB(db, existingChild)
	}
	if err != dao.ErrNoRows {
		return ForkResult{}, err
	}
	lease, err := AcquireFork(sessionDir, options.SourceSessionID)
	if err != nil {
		switch {
		case errors.Is(err, ErrRuntimeSessionNotFound):
			return ForkResult{}, ErrForkSessionNotFound
		case errors.Is(err, ErrRuntimeLeaseBusy), errors.Is(err, ErrSessionRunActive):
			return ForkResult{}, ErrForkSessionActive
		default:
			return ForkResult{}, err
		}
	}
	defer lease.Release()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ForkResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := validateRuntimeLeaseTx(tx, sessionDir, options.SourceSessionID); err != nil {
		return ForkResult{}, err
	}
	existingChild, existingFingerprint = "", ""
	existing, err = forkDAO.FindRequest(ctx, tx, requestHash, options.SourceSessionID)
	if err == nil {
		existingChild, existingFingerprint = existing.ChildSessionID, existing.RequestFingerprint
	}
	if err == nil {
		if existingFingerprint != fingerprint {
			return ForkResult{}, ErrForkIdempotencyConflict
		}
		return forkResultByIDTx(tx, existingChild)
	}
	if err != dao.ErrNoRows {
		return ForkResult{}, err
	}

	if _, err := forkDAO.FindSession(ctx, tx, options.SourceSessionID); err != nil {
		if err == dao.ErrNoRows {
			return ForkResult{}, ErrForkSessionNotFound
		}
		return ForkResult{}, err
	}
	active, err := forkDAO.ActiveRunCount(ctx, tx, options.SourceSessionID, NonTerminalSessionRunStatuses())
	if err != nil {
		return ForkResult{}, err
	}
	if active != 0 {
		return ForkResult{}, ErrForkSessionActive
	}
	openTurns, err := forkDAO.OpenTurnCount(ctx, tx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	if openTurns != 0 {
		return ForkResult{}, ErrForkSessionActive
	}
	pendingDecisions, err := pendingDecisionsTx(tx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	if pendingDecisions {
		return ForkResult{}, ErrForkSessionActive
	}

	entries, err := loadForkEntriesTx(tx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	turns, err := loadForkTurnsTx(tx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	boundary, kind, err := resolveForkBoundaryTx(tx, options.SourceSessionID, entries, turns, options.AtSeq)
	if err != nil {
		return ForkResult{}, err
	}
	if boundary <= 0 {
		return ForkResult{}, ErrForkNoCompletedTurn
	}
	copyEntries := make([]forkSourceEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Seq <= boundary {
			copyEntries = append(copyEntries, entry)
		}
	}
	if len(copyEntries) == 0 {
		return ForkResult{}, ErrForkNoCompletedTurn
	}
	snapshot, err := forkSourceFingerprintTx(tx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	// Finish the read snapshot before opening the child-write transaction. This
	// lets the final fingerprint check observe a newer committed source edit;
	// normal writers cannot pass the source lease fence while this operation is
	// active, but the check also protects against direct legacy DB mutations.
	if err := tx.Commit(); err != nil {
		return ForkResult{}, err
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		return ForkResult{}, err
	}
	if err := validateRuntimeLeaseTx(tx, sessionDir, options.SourceSessionID); err != nil {
		return ForkResult{}, err
	}
	current, err := forkSourceFingerprintTx(tx, options.SourceSessionID)
	if err != nil {
		return ForkResult{}, err
	}
	if current != snapshot {
		if current.openTurns != snapshot.openTurns || current.activeRuns != snapshot.activeRuns {
			return ForkResult{}, ErrForkSessionActive
		}
		return ForkResult{}, fmt.Errorf("%w: source changed during fork snapshot", ErrSessionModified)
	}

	childID := GenerateID()
	if childID == options.SourceSessionID {
		childID = GenerateID()
	}
	entryIDMap := make(map[string]string, len(copyEntries))
	for _, entry := range copyEntries {
		entryIDMap[entry.ID] = GenerateID()
	}
	turnIDMap := make(map[string]string)
	for _, turn := range turns {
		if turn.EndSeq != nil && *turn.EndSeq <= boundary {
			turnIDMap[turn.ID] = GenerateID()
		}
	}

	if err := forkDAO.InsertSessionFrom(ctx, tx, childID, options.SourceSessionID, boundary, int64(len(copyEntries)), string(kind)); err != nil {
		return ForkResult{}, err
	}
	if options.ExpertID != nil {
		if err := dao.NewSessionDAO(nil).UpdateSessionExpertID(ctx, tx, "sessions", childID, *options.ExpertID); err != nil {
			return ForkResult{}, err
		}
	}
	seqMap := make(map[int64]int64, len(copyEntries))
	for _, source := range copyEntries {
		newID := entryIDMap[source.ID]
		parentID := ""
		if source.ParentID.Valid {
			parentID = entryIDMap[source.ParentID.String]
		}
		data, err := remapForkData(source.Type, source.Data, entryIDMap, turnIDMap)
		if err != nil {
			return ForkResult{}, fmt.Errorf("%w: entry %s: %v", ErrForkUnsupportedEntry, source.ID, err)
		}
		if source.Type == string(EntrySession) {
			var header Header
			if err := json.Unmarshal([]byte(data), &header); err != nil {
				return ForkResult{}, fmt.Errorf("%w: session header: %v", ErrForkUnsupportedEntry, err)
			}
			header.ID = childID
			header.ParentSession = options.SourceSessionID
			header.ChannelType = "local"
			header.ChannelID = ""
			header.ForkBoundarySeq = boundary
			header.SeedLength = int64(len(copyEntries))
			header.ForkKind = string(kind)
			if options.ExpertID != nil {
				header.ExpertID = *options.ExpertID
			}
			encoded, marshalErr := json.Marshal(header)
			if marshalErr != nil {
				return ForkResult{}, marshalErr
			}
			data = string(encoded)
			parentID = ""
		} else {
			data, err = rewriteForkEntryIdentity(data, newID, parentID)
			if err != nil {
				return ForkResult{}, fmt.Errorf("%w: entry identity %s: %v", ErrForkUnsupportedEntry, source.ID, err)
			}
		}
		entryRecord := &dao.ForkEntryRecord{SessionID: childID, ID: newID, Type: source.Type, ParentID: dao.NullString{String: parentID, Valid: parentID != ""}, Timestamp: source.Timestamp, Data: data}
		seq, err := forkDAO.InsertEntry(ctx, tx, entryRecord)
		if err != nil {
			return ForkResult{}, err
		}
		seqMap[source.Seq] = seq
	}
	for _, turn := range turns {
		if turn.EndSeq == nil || *turn.EndSeq > boundary {
			continue
		}
		startSeq, okStart := seqMap[turn.StartSeq]
		endSeq, okEnd := seqMap[*turn.EndSeq]
		if !okStart || !okEnd {
			return ForkResult{}, fmt.Errorf("%w: turn %s boundary mapping", ErrForkUnsupportedEntry, turn.ID)
		}
		if err := forkDAO.InsertTurn(ctx, tx, turnIDMap[turn.ID], childID, turn.IntentID, turn.Kind, turn.Status, startSeq, endSeq, turn.StartedAt.Format(time.RFC3339Nano), formatOptionalTime(turn.EndedAt)); err != nil {
			return ForkResult{}, err
		}
	}
	if err := copyForkCapabilitiesTx(tx, options.SourceSessionID, childID); err != nil {
		return ForkResult{}, err
	}
	if err := copyForkProjectTx(tx, options.SourceSessionID, childID); err != nil {
		return ForkResult{}, err
	}
	childLeaf, err := currentEntryIDTx(tx, childID)
	if err != nil {
		return ForkResult{}, err
	}
	title, err := nextForkTitleTx(tx, options.SourceSessionID, childID, titleFromEntries(copyEntries))
	if err != nil {
		return ForkResult{}, err
	}
	if title != "" {
		titleEntry := SessionInfoEntry{EntryBase: EntryBase{Type: EntrySessionInfo, ID: GenerateID(), ParentID: stringPtr(childLeaf), Timestamp: time.Now()}, Name: title, Source: "auto"}
		data, _ := json.Marshal(titleEntry)
		if err := forkDAO.InsertRawEntry(ctx, tx, childID, titleEntry.ID, string(titleEntry.Type), childLeaf, titleEntry.Timestamp.Format(time.RFC3339Nano), string(data)); err != nil {
			return ForkResult{}, err
		}
	}
	if err := forkDAO.InsertForkRequest(ctx, tx, &dao.ForkRequestRecord{RequestKeyHash: requestHash, RequestFingerprint: fingerprint, SourceSessionID: options.SourceSessionID, ChildSessionID: childID, CreatedAt: time.Now().Format(time.RFC3339Nano)}); err != nil {
		return ForkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ForkResult{}, err
	}
	return ForkResult{SessionID: childID, ParentSessionID: options.SourceSessionID, ForkKind: kind, BoundarySeq: boundary, SeedLength: int64(len(copyEntries))}, nil
}

func rewriteForkEntryIdentity(raw, id, parentID string) (string, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", err
	}
	value["id"] = id
	if parentID == "" {
		value["parentId"] = nil
	} else {
		value["parentId"] = parentID
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func hashForkRequest(requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return hex.EncodeToString(sum[:])
}

func forkFingerprint(options ForkOptions) string {
	seq := ""
	if options.AtSeq != nil {
		seq = fmt.Sprintf("%d", *options.AtSeq)
	}
	// SQLite text values must not contain embedded NUL bytes: the modernc
	// driver truncates those parameters. Use a stable printable delimiter for
	// the idempotency snapshot instead.
	return fmt.Sprintf("%s|%s|%s", options.SourceSessionID, seq, options.TitleMode)
}

func forkNullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Format(time.RFC3339Nano)
}

func loadForkEntriesTx(tx *dao.Tx, sessionID string) ([]forkSourceEntry, error) {
	records, err := dao.NewForkDAO(nil).ListEntries(context.Background(), tx, sessionID)
	if err != nil {
		return nil, err
	}
	var result []forkSourceEntry
	for _, record := range records {
		result = append(result, forkSourceEntry{Seq: record.Seq, ID: record.ID, Type: record.Type, ParentID: record.ParentID, Timestamp: record.Timestamp, Data: record.Data})
	}
	return result, nil
}

func forkSourceFingerprintTx(tx *dao.Tx, sessionID string) (forkSourceFingerprint, error) {
	record, err := dao.NewForkDAO(nil).Fingerprint(context.Background(), tx, sessionID, NonTerminalSessionRunStatuses())
	if err != nil {
		return forkSourceFingerprint{}, err
	}
	return forkSourceFingerprint{maxSeq: record.MaxSeq, leaf: record.Leaf, openTurns: record.OpenTurns, activeRuns: record.ActiveRuns}, nil
}

func pendingDecisionsTx(tx *dao.Tx, sessionID string) (bool, error) {
	records, err := dao.NewSessionDAO(nil).ListRunEventsFrom(context.Background(), tx, sessionID)
	if err != nil {
		return false, err
	}
	type decisionRecord struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	pending := make(map[string]struct{})
	for _, record := range records {
		eventType, data := record.EventType, record.Data
		if !IsDecisionEventType(eventType) {
			continue
		}
		var envelope struct {
			Decision decisionRecord `json:"decision"`
		}
		if err := json.Unmarshal([]byte(data), &envelope); err != nil || envelope.Decision.ID == "" {
			continue
		}
		if envelope.Decision.Status == "pending" {
			pending[envelope.Decision.ID] = struct{}{}
		} else {
			delete(pending, envelope.Decision.ID)
		}
	}
	return len(pending) != 0, nil
}

func loadForkTurnsTx(tx *dao.Tx, sessionID string) ([]ConversationTurn, error) {
	records, err := dao.NewConversationTurnDAO(nil).ListFrom(context.Background(), tx, sessionID)
	if err != nil {
		return nil, err
	}
	var turns []ConversationTurn
	for _, record := range records {
		turns = append(turns, scanConversationTurnRecord(record))
	}
	return turns, nil
}

func resolveForkBoundaryTx(tx *dao.Tx, sessionID string, entries []forkSourceEntry, turns []ConversationTurn, atSeq *int64) (int64, ForkKind, error) {
	if len(turns) == 0 {
		return resolveLegacyForkBoundaryTx(tx, sessionID, entries, atSeq)
	}
	if atSeq == nil {
		for i := len(turns) - 1; i >= 0; i-- {
			if turns[i].EndSeq != nil && turns[i].Status != "open" {
				return absorbForkMetadata(entries, *turns[i].EndSeq), ForkKindSession, nil
			}
		}
		return 0, ForkKindUnknown, ErrForkNoCompletedTurn
	}
	if *atSeq <= 0 {
		return 0, ForkKindUnknown, ErrForkInvalidBoundary
	}
	record, err := dao.NewForkDAO(nil).EntryAtSeq(context.Background(), tx, sessionID, *atSeq)
	if err != nil {
		if err == dao.ErrNoRows {
			return 0, ForkKindUnknown, ErrForkInvalidBoundary
		}
		return 0, ForkKindUnknown, err
	}
	entryType, data := record.Type, record.Data
	if entryType != string(EntryMessage) {
		return 0, ForkKindUnknown, ErrForkUnavailable
	}
	var message MessageEntry
	if err := json.Unmarshal([]byte(data), &message); err != nil || message.Message.Role != "assistant" {
		return 0, ForkKindUnknown, ErrForkUnavailable
	}
	for _, turn := range turns {
		if turn.EndSeq == nil || *atSeq < turn.StartSeq || *atSeq > *turn.EndSeq {
			continue
		}
		var lastMessageSeq int64
		var lastMessage MessageEntry
		for _, candidate := range entries {
			if candidate.Seq < turn.StartSeq || candidate.Seq > *turn.EndSeq || candidate.Type != string(EntryMessage) {
				continue
			}
			var candidateMessage MessageEntry
			if json.Unmarshal([]byte(candidate.Data), &candidateMessage) != nil {
				continue
			}
			lastMessageSeq = candidate.Seq
			lastMessage = candidateMessage
		}
		if lastMessageSeq != *atSeq || lastMessage.Message.Role != "assistant" || !hasAssistantText(lastMessage.Message) || len(lastMessage.Message.Contents) > 0 && hasToolCall(lastMessage.Message.Contents) {
			return 0, ForkKindUnknown, ErrForkUnavailable
		}
		return absorbForkMetadata(entries, *turn.EndSeq), ForkKindMessage, nil
	}
	return 0, ForkKindUnknown, ErrForkUnavailable
}

type legacyForkBoundary struct {
	startSeq int64
	endSeq   int64
}

// resolveLegacyForkBoundaryTx gives pre-turn-index sessions a conservative
// compatibility path. A completed durable Run is usable only when its time
// interval maps to exactly one non-overlapping transcript message interval.
// Ambiguous histories remain unavailable instead of being guessed into a fork.
func resolveLegacyForkBoundaryTx(tx *dao.Tx, sessionID string, entries []forkSourceEntry, atSeq *int64) (int64, ForkKind, error) {
	records, err := dao.NewForkDAO(nil).RunWindows(context.Background(), tx, sessionID, TerminalSessionRunStatuses())
	if err != nil {
		return 0, ForkKindUnknown, err
	}
	type runWindow struct {
		start time.Time
		end   time.Time
	}
	var windows []runWindow
	for _, record := range records {
		started, finished := record.StartedAt, record.FinishedAt
		start, end := parseSessionTimestamp(started), parseSessionTimestamp(finished)
		if start.IsZero() || end.IsZero() || !end.After(start) {
			continue
		}
		if len(windows) > 0 && !start.After(windows[len(windows)-1].end) {
			return 0, ForkKindUnknown, ErrForkUnavailable
		}
		windows = append(windows, runWindow{start: start, end: end})
	}
	if len(windows) == 0 {
		return 0, ForkKindUnknown, ErrForkNoCompletedTurn
	}
	boundaries := make([]legacyForkBoundary, 0, len(windows))
	for _, window := range windows {
		var first, last int64
		for _, entry := range entries {
			if entry.Type != string(EntryMessage) {
				continue
			}
			ts := parseSessionTimestamp(entry.Timestamp)
			if ts.IsZero() || ts.Before(window.start) || ts.After(window.end) {
				continue
			}
			if first == 0 {
				first = entry.Seq
			}
			last = entry.Seq
		}
		if first == 0 || last == 0 {
			continue
		}
		boundaries = append(boundaries, legacyForkBoundary{startSeq: first, endSeq: last})
	}
	if len(boundaries) == 0 {
		return 0, ForkKindUnknown, ErrForkNoCompletedTurn
	}
	if atSeq == nil {
		return absorbForkMetadata(entries, boundaries[len(boundaries)-1].endSeq), ForkKindSession, nil
	}
	for _, boundary := range boundaries {
		if *atSeq < boundary.startSeq || *atSeq > boundary.endSeq {
			continue
		}
		var selected MessageEntry
		var selectedSeq int64
		var lastSeq int64
		for _, entry := range entries {
			if entry.Seq < boundary.startSeq || entry.Seq > boundary.endSeq || entry.Type != string(EntryMessage) {
				continue
			}
			var message MessageEntry
			if json.Unmarshal([]byte(entry.Data), &message) != nil {
				return 0, ForkKindUnknown, ErrForkUnavailable
			}
			lastSeq = entry.Seq
			if entry.Seq == *atSeq {
				selected, selectedSeq = message, entry.Seq
			}
		}
		if selectedSeq == 0 || selectedSeq != lastSeq || selected.Message.Role != "assistant" || !hasAssistantText(selected.Message) || hasToolCall(selected.Message.Contents) {
			return 0, ForkKindUnknown, ErrForkUnavailable
		}
		return absorbForkMetadata(entries, boundary.endSeq), ForkKindMessage, nil
	}
	return 0, ForkKindUnknown, ErrForkUnavailable
}

func hasToolCall(contents []provider.ContentBlock) bool {
	for _, block := range contents {
		if block.Type == "toolCall" || block.ToolCall != nil {
			return true
		}
	}
	return false
}

func hasAssistantText(message provider.Message) bool {
	if strings.TrimSpace(message.Content) != "" {
		return true
	}
	for _, block := range message.Contents {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return true
		}
	}
	return false
}

func absorbForkMetadata(entries []forkSourceEntry, endSeq int64) int64 {
	boundary := endSeq
	for _, entry := range entries {
		if entry.Seq <= endSeq {
			continue
		}
		if entry.Type == string(EntryTurnStart) {
			break
		}
		boundary = entry.Seq
	}
	return boundary
}

func remapForkData(sourceType, raw string, entryIDs, turnIDs map[string]string) (string, error) {
	remapEntryID := func(value string) string {
		if replacement, ok := entryIDs[value]; ok {
			return replacement
		}
		return value
	}
	remapTurnID := func(value string) string {
		if replacement, ok := turnIDs[value]; ok {
			return replacement
		}
		return value
	}
	switch EntryType(sourceType) {
	case EntrySession, EntryMessage, EntryModelChange, EntryModeChange, EntryThinkingChange, EntryAdditionalDirectories, EntrySessionInfo:
		return raw, nil
	case EntryCompaction:
		var entry CompactionEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", err
		}
		entry.FirstKeptEntry = remapEntryID(entry.FirstKeptEntry)
		entry.PreviousCompactionID = remapEntryID(entry.PreviousCompactionID)
		entry.LastSummarizedEntry = remapEntryID(entry.LastSummarizedEntry)
		encoded, err := json.Marshal(entry)
		return string(encoded), err
	case EntryContentOverride:
		var entry ContentOverrideEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", err
		}
		entry.TargetEntryID = remapEntryID(entry.TargetEntryID)
		encoded, err := json.Marshal(entry)
		return string(encoded), err
	case EntryBranchSummary:
		var entry BranchSummaryEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", err
		}
		entry.FromID = remapEntryID(entry.FromID)
		encoded, err := json.Marshal(entry)
		return string(encoded), err
	case EntryLabel:
		var entry LabelEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", err
		}
		entry.TargetID = remapEntryID(entry.TargetID)
		encoded, err := json.Marshal(entry)
		return string(encoded), err
	case EntryTurnStart:
		var entry TurnStartEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", err
		}
		entry.TurnID = remapTurnID(entry.TurnID)
		encoded, err := json.Marshal(entry)
		return string(encoded), err
	case EntryTurnEnd:
		var entry TurnEndEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", err
		}
		entry.TurnID = remapTurnID(entry.TurnID)
		encoded, err := json.Marshal(entry)
		return string(encoded), err
	case EntryCustom, EntryCustomMessage:
		return "", fmt.Errorf("custom entry type %s has no declared reference rewrite policy", sourceType)
	default:
		return "", fmt.Errorf("unknown entry type %s", sourceType)
	}
}

func copyForkCapabilitiesTx(tx *dao.Tx, sourceID, childID string) error {
	return dao.NewForkDAO(nil).CopyCapabilities(context.Background(), tx, sourceID, childID)
}

func copyForkProjectTx(tx *dao.Tx, sourceID, childID string) error {
	return dao.NewForkDAO(nil).CopyProject(context.Background(), tx, sourceID, childID)
}

func currentEntryIDTx(tx *dao.Tx, sessionID string) (string, error) {
	id, err := dao.NewForkDAO(nil).CurrentEntryID(context.Background(), tx, sessionID)
	if err == dao.ErrNoRows {
		return "", nil
	}
	return id, err
}

func titleFromEntries(entries []forkSourceEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type != string(EntrySessionInfo) {
			continue
		}
		var entry SessionInfoEntry
		if json.Unmarshal([]byte(entries[i].Data), &entry) == nil {
			return entry.Name
		}
	}
	return "Session"
}

func nextForkTitleTx(tx *dao.Tx, parentID, childID, base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", nil
	}
	for index := 1; index < 10000; index++ {
		candidate := fmt.Sprintf("%s (%d)", base, index)
		exists, err := dao.NewForkDAO(nil).TitleExists(context.Background(), tx, parentID, string(EntrySessionInfo), candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to allocate fork title")
}

func forkResultByIDTx(tx *dao.Tx, childID string) (ForkResult, error) {
	record, err := dao.NewForkDAO(nil).Result(context.Background(), tx, childID)
	if err != nil {
		return ForkResult{}, err
	}
	return forkResultFromRecord(record), nil
}

func forkResultByDB(db *dao.Database, childID string) (ForkResult, error) {
	record, err := dao.NewForkDAO(db.Bun()).Result(context.Background(), db.Bun(), childID)
	if err != nil {
		return ForkResult{}, err
	}
	return forkResultFromRecord(record), nil
}

func forkResultFromRecord(record dao.ForkSessionRecord) ForkResult {
	return ForkResult{SessionID: record.ID, ParentSessionID: record.ParentSession.String, ForkKind: ForkKind(record.ForkKind.String), BoundarySeq: record.ForkBoundary, SeedLength: record.SeedLength}
}
