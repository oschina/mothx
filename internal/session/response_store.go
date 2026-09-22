package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/oschina/mothx/internal/dao"
	"strings"
	"time"
)

const maxResponseArchiveJSONBytes = 128 * 1024

// ResponseTurn is the durable lineage and lifecycle summary for one Responses
// API turn. It intentionally contains summaries, not a second transcript.
type ResponseTurn struct {
	ID                 int64
	SessionID          string
	LocalTurnID        string
	MessageID          *int64
	RequestID          string
	ResponseID         string
	PreviousResponseID string
	ConversationID     string
	Provider           string
	API                string
	Model              string
	StateMode          string
	Status             string
	IncompleteReason   string
	RequestSummary     json.RawMessage
	ResponseSummary    json.RawMessage
	CreatedAt          time.Time
	CompletedAt        *time.Time
}

// ResponseItemArchive stores one sanitized normalized item. Raw provider
// request/response bodies must not be passed here.
type ResponseItemArchive struct {
	ID            int64
	SessionID     string
	LocalTurnID   string
	ResponseID    string
	ItemID        string
	OutputIndex   int
	ItemType      string
	ItemStatus    string
	ItemKey       string
	SanitizedJSON json.RawMessage
	CreatedAt     time.Time
}

// ToolExecutionRecord is the cross-protocol idempotency record for a tool
// invocation. ExecutionKey is local and remains the deduplication authority.
type ToolExecutionRecord struct {
	ID               int64
	SessionID        string
	LocalTurnID      string
	ExecutionKey     string
	Provider         string
	API              string
	ResponseID       string
	ProviderCallID   string
	ToolKind         string
	ToolName         string
	ArgsHash         string
	ExecutionState   string
	ResultSummary    json.RawMessage
	ProviderMetadata json.RawMessage
	SideEffecting    bool
	CreatedAt        time.Time
	CompletedAt      *time.Time
}

// ResponseRun is the durable state for a Responses background run.
type ResponseRun struct {
	ID                int64     `json:"id"`
	SessionID         string    `json:"sessionId"`
	LocalRunID        string    `json:"localRunId"`
	LocalTurnID       string    `json:"localTurnId,omitempty"`
	MessageID         *int64    `json:"messageId,omitempty"`
	ResponseID        string    `json:"responseId,omitempty"`
	Provider          string    `json:"provider"`
	API               string    `json:"api"`
	State             string    `json:"state"`
	PollingURL        string    `json:"pollingUrl,omitempty"`
	LastEventSequence *int64    `json:"lastEventSequence,omitempty"`
	CancelRequested   bool      `json:"cancelRequested,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// ResponseSessionState is the compare-and-swap protected remote lineage for a
// single local session. Provider config supplies defaults; this record keeps
// concurrent sessions and concurrent turns from sharing mutable remote state.
type ResponseSessionState struct {
	SessionID          string
	StateMode          string
	PreviousResponseID string
	ConversationID     string
	Provider           string
	API                string
	Model              string
	Version            int64
	UpdatedAt          time.Time
}

// ResponseReplayTurn groups the native output items belonging to one local
// Responses turn. It lets callers place those items at the corresponding
// assistant position while rebuilding a complete local conversation.
type ResponseReplayTurn struct {
	LocalTurnID string
	Items       []json.RawMessage
}

func SaveResponseTurn(sessionDir string, turn ResponseTurn) error {
	if err := validateResponseTurn(turn); err != nil {
		return err
	}
	requestSummary, err := archiveJSON(turn.RequestSummary)
	if err != nil {
		return fmt.Errorf("request summary: %w", err)
	}
	responseSummary, err := archiveJSON(turn.ResponseSummary)
	if err != nil {
		return fmt.Errorf("response summary: %w", err)
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	var completed any
	if turn.CompletedAt != nil {
		completed = turn.CompletedAt.Format(time.RFC3339Nano)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, turn.SessionID); err != nil {
		return err
	}
	responseDAO := dao.NewResponseDAO(db.Bun())
	err = responseDAO.InsertTurn(context.Background(), tx, &dao.ResponseTurnRecord{
		SessionID: turn.SessionID, LocalTurnID: turn.LocalTurnID, MessageID: turn.MessageID,
		RequestID: nullableStringPtr(turn.RequestID), ResponseID: nullableStringPtr(turn.ResponseID),
		PreviousResponseID: nullableStringPtr(turn.PreviousResponseID), ConversationID: nullableStringPtr(turn.ConversationID),
		Provider: turn.Provider, API: turn.API, Model: turn.Model, StateMode: turn.StateMode, Status: turn.Status,
		IncompleteReason: nullableStringPtr(turn.IncompleteReason), RequestSummaryJSON: requestSummary,
		ResponseSummaryJSON: responseSummary, CreatedAt: turn.CreatedAt.Format(time.RFC3339Nano), CompletedAt: stringPtrAny(completed),
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func validateResponseTurn(turn ResponseTurn) error {
	switch {
	case turn.SessionID == "":
		return fmt.Errorf("response turn session ID is required")
	case turn.LocalTurnID == "":
		return fmt.Errorf("response turn local turn ID is required")
	case turn.Provider == "":
		return fmt.Errorf("response turn provider is required")
	case turn.API == "":
		return fmt.Errorf("response turn API is required")
	case turn.Model == "":
		return fmt.Errorf("response turn model is required")
	case turn.StateMode == "":
		return fmt.Errorf("response turn state mode is required")
	case turn.Status == "":
		return fmt.Errorf("response turn status is required")
	default:
		return nil
	}
}

func GetResponseTurn(sessionDir, sessionID, localTurnID string) (*ResponseTurn, error) {
	if sessionID == "" || localTurnID == "" {
		return nil, fmt.Errorf("session ID and local turn ID are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewResponseDAO(db.Bun()).FindTurn(context.Background(), sessionID, localTurnID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	turn := responseTurnFromRecord(record)
	return &turn, nil
}

func SaveResponseItem(sessionDir string, item ResponseItemArchive) error {
	if item.SessionID == "" || item.LocalTurnID == "" || item.ItemType == "" {
		return fmt.Errorf("session ID, local turn ID and item type are required")
	}
	sanitized, err := archiveJSON(item.SanitizedJSON)
	if err != nil {
		return fmt.Errorf("sanitized item: %w", err)
	}
	if len(sanitized) == 0 {
		return fmt.Errorf("sanitized item is required")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	if item.ItemKey == "" {
		if item.ItemID != "" {
			item.ItemKey = fmt.Sprintf("%s:%d", item.ItemID, item.OutputIndex)
		} else {
			item.ItemKey = fmt.Sprintf("output:%d", item.OutputIndex)
		}
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, item.SessionID); err != nil {
		return err
	}
	err = dao.NewResponseDAO(db.Bun()).UpsertItem(context.Background(), tx, &dao.ResponseItemRecord{
		SessionID: item.SessionID, LocalTurnID: item.LocalTurnID, ResponseID: nullableStringPtr(item.ResponseID),
		ItemID: nullableStringPtr(item.ItemID), OutputIndex: item.OutputIndex, ItemType: item.ItemType,
		ItemStatus: nullableStringPtr(item.ItemStatus), ItemKey: item.ItemKey, SanitizedJSON: sanitized,
		CreatedAt: item.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: stringPtr(item.CreatedAt.Format(time.RFC3339Nano)),
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func ListResponseItems(sessionDir, sessionID, localTurnID string) ([]ResponseItemArchive, error) {
	if sessionID == "" || localTurnID == "" {
		return nil, fmt.Errorf("session ID and local turn ID are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewResponseDAO(db.Bun()).ListItems(context.Background(), sessionID, localTurnID)
	if err != nil {
		return nil, err
	}
	var result []ResponseItemArchive
	for _, record := range records {
		result = append(result, responseItemFromRecord(&record))
	}
	return result, nil
}

// ListResponseReplayItems returns the ordered, sanitized native items from
// completed Responses turns. Callers can pass this sequence to a provider's
// native replay path instead of reconstructing prior assistant output from
// plain transcript text.
func ListResponseReplayItems(sessionDir, sessionID string, limit int) ([]json.RawMessage, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewResponseDAO(db.Bun()).ListReplayItems(context.Background(), sessionID, limit)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	for _, record := range records {
		raw := record.SanitizedJSON
		if !json.Valid(raw) {
			return nil, fmt.Errorf("stored response replay item is invalid JSON")
		}
		items = append(items, cloneArchiveJSON(raw))
	}
	return items, nil
}

// ListResponseReplayTurns returns completed native output grouped by local
// turn, ordered by their original completion order.
func ListResponseReplayTurns(sessionDir, sessionID string, limit int) ([]ResponseReplayTurn, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewResponseDAO(db.Bun()).ListReplayTurns(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	byTurn := make(map[string]int)
	seenCalls := make(map[string]map[string]struct{})
	var turns []ResponseReplayTurn
	for _, record := range records {
		turnID, raw := record.LocalTurnID, record.SanitizedJSON
		if !json.Valid(raw) {
			return nil, fmt.Errorf("stored response replay item is invalid JSON")
		}
		// Older archives may contain both the streamed output_item and a
		// response.completed snapshot for one function call. Gate replay by
		// provider call identity so those historical duplicates are not sent
		// back to the provider as two calls.
		var identity struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			CallID string `json:"call_id"`
		}
		if json.Unmarshal(raw, &identity) == nil &&
			(identity.Type == "function_call" || identity.Type == "custom_tool_call") {
			callID := identity.CallID
			if callID == "" {
				callID = identity.ID
			}
			if callID != "" {
				if seenCalls[turnID] == nil {
					seenCalls[turnID] = make(map[string]struct{})
				}
				if _, duplicate := seenCalls[turnID][identity.Type+"\x00"+callID]; duplicate {
					continue
				}
				seenCalls[turnID][identity.Type+"\x00"+callID] = struct{}{}
			}
		}
		index, ok := byTurn[turnID]
		if !ok {
			if len(turns) >= limit {
				break
			}
			index = len(turns)
			byTurn[turnID] = index
			turns = append(turns, ResponseReplayTurn{LocalTurnID: turnID})
		}
		turns[index].Items = append(turns[index].Items, cloneArchiveJSON(raw))
	}
	return turns, nil
}

// ClaimToolExecutionRecord atomically claims an execution key. A false
// created result means another request already owns the key and its record
// must be consulted before executing a side effect.
func ClaimToolExecutionRecord(sessionDir string, record ToolExecutionRecord) (*ToolExecutionRecord, bool, error) {
	if record.SessionID == "" || record.ExecutionKey == "" || record.ToolName == "" || record.ArgsHash == "" {
		return nil, false, fmt.Errorf("session ID, execution key, tool name and args hash are required")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	resultSummary, err := archiveJSON(record.ResultSummary)
	if err != nil {
		return nil, false, fmt.Errorf("result summary: %w", err)
	}
	providerMetadata, err := archiveJSON(record.ProviderMetadata)
	if err != nil {
		return nil, false, fmt.Errorf("provider metadata: %w", err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err := validateRuntimeLeaseTx(tx, sessionDir, record.SessionID); err != nil {
		return nil, false, err
	}
	stored, created, err := dao.NewResponseDAO(db.Bun()).ClaimTool(context.Background(), tx, &dao.ToolExecutionRecord{
		SessionID: record.SessionID, LocalTurnID: record.LocalTurnID, ExecutionKey: record.ExecutionKey,
		Provider: record.Provider, API: record.API, ResponseID: nullableStringPtr(record.ResponseID), ProviderCallID: nullableStringPtr(record.ProviderCallID),
		ToolKind: record.ToolKind, ToolName: record.ToolName, ArgsHash: record.ArgsHash, ExecutionState: record.ExecutionState,
		ResultSummaryJSON: resultSummary, ProviderMetadataJSON: providerMetadata, SideEffecting: record.SideEffecting,
		CreatedAt: record.CreatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, false, err
	}
	claimed := toolExecutionFromRecord(stored)
	if claimed.SessionID != record.SessionID || claimed.LocalTurnID != record.LocalTurnID ||
		claimed.Provider != record.Provider || claimed.API != record.API ||
		claimed.ToolName != record.ToolName || claimed.ArgsHash != record.ArgsHash ||
		(record.ProviderCallID != "" && claimed.ProviderCallID != record.ProviderCallID) {
		return nil, false, fmt.Errorf("execution key collision for %q", record.ExecutionKey)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return &claimed, created > 0, nil
}

func UpdateToolExecutionRecord(sessionDir string, record ToolExecutionRecord) error {
	if record.ExecutionKey == "" || record.ExecutionState == "" {
		return fmt.Errorf("execution key and execution state are required")
	}
	resultSummary, err := archiveJSON(record.ResultSummary)
	if err != nil {
		return fmt.Errorf("result summary: %w", err)
	}
	providerMetadata, err := archiveJSON(record.ProviderMetadata)
	if err != nil {
		return fmt.Errorf("provider metadata: %w", err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	var completed any
	if record.CompletedAt != nil {
		completed = record.CompletedAt.Format(time.RFC3339Nano)
	}
	// Only an actively owned execution may publish a result. This prevents a
	// stale process from overwriting an explicit abandon or a newer recovery.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, record.SessionID); err != nil {
		return err
	}
	count, err := dao.NewResponseDAO(db.Bun()).UpdateTool(context.Background(), tx, &dao.ToolExecutionRecord{ExecutionKey: record.ExecutionKey, ExecutionState: record.ExecutionState, ResultSummaryJSON: resultSummary, ProviderMetadataJSON: providerMetadata, CompletedAt: stringPtrAny(completed)})
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("tool execution %q is no longer writable", record.ExecutionKey)
	}
	return tx.Commit()
}

// ReclaimInterruptedToolExecution atomically reopens a tool record after a
// process interruption. Read-only running/interrupted records are eligible
// automatically; side-effecting records require the explicit retry_requested
// state set by the confirmation API.
func ReclaimInterruptedToolExecution(sessionDir, executionKey string) (bool, error) {
	if sessionDir == "" || executionKey == "" {
		return false, fmt.Errorf("session directory and execution key are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	record, err := dao.NewResponseDAO(db.Bun()).FindTool(context.Background(), tx, executionKey)
	if err != nil {
		if errors.Is(err, dao.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	sessionID := record.SessionID
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return false, err
	}
	state, sideEffecting, metadata := record.ExecutionState, record.SideEffecting, record.ProviderMetadataJSON
	if !((!sideEffecting && (state == "running" || state == "interrupted")) || state == "retry_requested") {
		return false, nil
	}
	reason := "automatic_read_only"
	if state == "retry_requested" {
		reason = "user_confirmed"
	}
	meta := map[string]any{}
	if len(metadata) > 0 && json.Unmarshal(metadata, &meta) != nil {
		meta = map[string]any{}
	}
	meta["recoveryReason"] = reason
	meta["recoveryAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	metadataJSON, err := archiveJSON(mustJSON(meta))
	if err != nil {
		return false, fmt.Errorf("recovery metadata: %w", err)
	}
	n, err := dao.NewResponseDAO(db.Bun()).ReclaimTool(context.Background(), tx, executionKey, metadataJSON, state)
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// RequestToolExecutionRecovery marks selected interrupted tool calls for an
// explicit user-confirmed retry. It never changes completed records and does
// not itself execute any tool.
func RequestToolExecutionRecovery(sessionDir, sessionID, localTurnID string, providerCallIDs []string) (int64, error) {
	_, count, err := RequestToolExecutionRecoveryRecords(sessionDir, sessionID, localTurnID, providerCallIDs)
	return count, err
}

// RequestToolExecutionRecoveryRecords records explicit user confirmation and
// returns only matching interrupted calls. The records are retained as audit
// evidence while recovery starts as a fresh execution; terminal Runs are never
// reactivated to consume these records.
func RequestToolExecutionRecoveryRecords(sessionDir, sessionID, localTurnID string, providerCallIDs []string) ([]ToolExecutionRecord, int64, error) {
	if sessionDir == "" || sessionID == "" || localTurnID == "" || len(providerCallIDs) == 0 {
		return nil, 0, fmt.Errorf("session, local turn and provider call IDs are required")
	}
	for _, id := range providerCallIDs {
		if strings.TrimSpace(id) == "" {
			return nil, 0, fmt.Errorf("provider call IDs must not be empty")
		}
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return nil, 0, err
	}
	responseDAO := dao.NewResponseDAO(db.Bun())
	count, err := responseDAO.RequestToolRecovery(context.Background(), tx, sessionID, localTurnID, providerCallIDs)
	if err != nil {
		return nil, 0, err
	}
	records, err := responseDAO.ListRequestedToolRecoveries(context.Background(), tx, sessionID, localTurnID, providerCallIDs)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	result := make([]ToolExecutionRecord, 0, len(records))
	for i := range records {
		result = append(result, toolExecutionFromRecord(&records[i]))
	}
	return result, count, nil
}

// AbandonInterruptedToolExecutionRecords marks uncertain executions as
// explicitly abandoned. It never retries a tool or invents a tool output;
// callers use it only after they have established that no runtime owns the
// session lock. This makes a subsequent user-submitted run a new operation
// instead of silently replaying a potentially side-effecting call.
func AbandonInterruptedToolExecutionRecords(sessionDir, sessionID, localTurnID string) (int64, error) {
	if sessionID == "" || localTurnID == "" {
		return 0, fmt.Errorf("session ID and local turn ID are required")
	}
	details, err := archiveJSON(json.RawMessage(`{"content":"Tool execution explicitly abandoned after interruption; it was not retried.","isError":true,"reason":"manual_abandon"}`))
	if err != nil {
		return 0, fmt.Errorf("build abandonment summary: %w", err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return 0, err
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, sessionID); err != nil {
		return 0, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	count, err := dao.NewResponseDAO(db.Bun()).AbandonTools(context.Background(), tx, sessionID, localTurnID, details, now)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func SaveResponseRun(sessionDir string, run ResponseRun) error {
	if run.SessionID == "" || run.LocalRunID == "" || run.Provider == "" || run.API == "" || run.State == "" {
		return fmt.Errorf("session ID, local run ID, provider, API and state are required")
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now()
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = run.CreatedAt
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, run.SessionID); err != nil {
		return err
	}
	err = dao.NewResponseDAO(db.Bun()).UpsertRun(context.Background(), tx, &dao.ResponseRunRecord{
		SessionID: run.SessionID, LocalRunID: run.LocalRunID, LocalTurnID: run.LocalTurnID, MessageID: run.MessageID,
		ResponseID: nullableStringPtr(run.ResponseID), Provider: run.Provider, API: run.API, State: run.State,
		PollingURL: nullableStringPtr(run.PollingURL), LastEventSequence: run.LastEventSequence, CancelRequested: run.CancelRequested,
		CreatedAt: run.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: run.UpdatedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	return tx.Commit()
}

func GetResponseRun(sessionDir, sessionID, localRunID string) (*ResponseRun, error) {
	if sessionID == "" || localRunID == "" {
		return nil, fmt.Errorf("session ID and local run ID are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewResponseDAO(db.Bun()).GetRun(context.Background(), sessionID, localRunID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run := responseRunFromRecord(record)
	return &run, nil
}

func ListResponseRuns(sessionDir, sessionID string, limit int) ([]ResponseRun, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	records, err := dao.NewResponseDAO(db.Bun()).ListRunsForSession(context.Background(), sessionID, limit)
	if err != nil {
		return nil, err
	}
	var result []ResponseRun
	for _, record := range records {
		result = append(result, responseRunFromRecord(&record))
	}
	return result, nil
}

// GetResponseSessionState returns the durable remote lineage for a local
// session. A missing record means the caller must use its configured default,
// normally replay mode.
func GetResponseSessionState(sessionDir, sessionID string) (*ResponseSessionState, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return nil, err
	}
	record, err := dao.NewResponseDAO(db.Bun()).GetSessionState(context.Background(), sessionID)
	if err == dao.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	state := responseSessionStateFromRecord(record)
	return &state, nil
}

// CompareAndSwapResponseSessionState advances a session lineage only when the
// caller observed expectedVersion. It prevents two concurrent turns from
// silently branching a previous_response_id chain.
func CompareAndSwapResponseSessionState(sessionDir string, state ResponseSessionState, expectedVersion int64) (bool, error) {
	if state.SessionID == "" || state.StateMode == "" {
		return false, fmt.Errorf("session ID and state mode are required")
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRuntimeLeaseTx(tx, sessionDir, state.SessionID); err != nil {
		return false, err
	}
	if expectedVersion == 0 {
		changed, err := dao.NewResponseDAO(db.Bun()).InsertSessionState(context.Background(), tx, &dao.ResponseSessionStateRecord{
			SessionID: state.SessionID, StateMode: state.StateMode, PreviousResponseID: nullableStringPtr(state.PreviousResponseID),
			ConversationID: nullableStringPtr(state.ConversationID), Provider: state.Provider, API: state.API, Model: state.Model,
			Version: 1, UpdatedAt: state.UpdatedAt.Format(time.RFC3339Nano),
		})
		if err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return changed == 1, nil
	}
	changed, err := dao.NewResponseDAO(db.Bun()).UpdateSessionStateCAS(context.Background(), tx, &dao.ResponseSessionStateRecord{
		SessionID: state.SessionID, StateMode: state.StateMode, PreviousResponseID: nullableStringPtr(state.PreviousResponseID),
		ConversationID: nullableStringPtr(state.ConversationID), Provider: state.Provider, API: state.API, Model: state.Model,
		UpdatedAt: state.UpdatedAt.Format(time.RFC3339Nano),
	}, expectedVersion)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed == 1, nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableStringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func toolExecutionFromRecord(record *dao.ToolExecutionRecord) ToolExecutionRecord {
	result := ToolExecutionRecord{ID: record.ID, SessionID: record.SessionID, LocalTurnID: record.LocalTurnID,
		ExecutionKey: record.ExecutionKey, Provider: record.Provider, API: record.API, ToolKind: record.ToolKind,
		ToolName: record.ToolName, ArgsHash: record.ArgsHash, ExecutionState: record.ExecutionState,
		ResultSummary: cloneArchiveJSON(record.ResultSummaryJSON), ProviderMetadata: cloneArchiveJSON(record.ProviderMetadataJSON),
		SideEffecting: record.SideEffecting, CreatedAt: parseSessionTimestamp(record.CreatedAt)}
	if record.ResponseID != nil {
		result.ResponseID = *record.ResponseID
	}
	if record.ProviderCallID != nil {
		result.ProviderCallID = *record.ProviderCallID
	}
	if record.CompletedAt != nil && *record.CompletedAt != "" {
		value := parseSessionTimestamp(*record.CompletedAt)
		result.CompletedAt = &value
	}
	return result
}

func responseRunFromRecord(record *dao.ResponseRunRecord) ResponseRun {
	result := ResponseRun{ID: record.ID, SessionID: record.SessionID, LocalRunID: record.LocalRunID, LocalTurnID: record.LocalTurnID,
		MessageID: record.MessageID, Provider: record.Provider, API: record.API, State: record.State, LastEventSequence: record.LastEventSequence,
		CancelRequested: record.CancelRequested, CreatedAt: parseSessionTimestamp(record.CreatedAt), UpdatedAt: parseSessionTimestamp(record.UpdatedAt)}
	if record.ResponseID != nil {
		result.ResponseID = *record.ResponseID
	}
	if record.PollingURL != nil {
		result.PollingURL = *record.PollingURL
	}
	return result
}

func responseSessionStateFromRecord(record *dao.ResponseSessionStateRecord) ResponseSessionState {
	result := ResponseSessionState{SessionID: record.SessionID, StateMode: record.StateMode, Provider: record.Provider, API: record.API,
		Model: record.Model, Version: record.Version, UpdatedAt: parseSessionTimestamp(record.UpdatedAt)}
	if record.PreviousResponseID != nil {
		result.PreviousResponseID = *record.PreviousResponseID
	}
	if record.ConversationID != nil {
		result.ConversationID = *record.ConversationID
	}
	return result
}

func stringPtrAny(value any) *string {
	if value == nil {
		return nil
	}
	s, ok := value.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

func responseTurnFromRecord(record *dao.ResponseTurnRecord) ResponseTurn {
	turn := ResponseTurn{ID: record.ID, SessionID: record.SessionID, LocalTurnID: record.LocalTurnID,
		MessageID: record.MessageID, Provider: record.Provider, API: record.API, Model: record.Model,
		StateMode: record.StateMode, Status: record.Status, RequestSummary: cloneArchiveJSON(record.RequestSummaryJSON),
		ResponseSummary: cloneArchiveJSON(record.ResponseSummaryJSON), CreatedAt: parseSessionTimestamp(record.CreatedAt)}
	if record.RequestID != nil {
		turn.RequestID = *record.RequestID
	}
	if record.ResponseID != nil {
		turn.ResponseID = *record.ResponseID
	}
	if record.PreviousResponseID != nil {
		turn.PreviousResponseID = *record.PreviousResponseID
	}
	if record.ConversationID != nil {
		turn.ConversationID = *record.ConversationID
	}
	if record.IncompleteReason != nil {
		turn.IncompleteReason = *record.IncompleteReason
	}
	if record.CompletedAt != nil && *record.CompletedAt != "" {
		value := parseSessionTimestamp(*record.CompletedAt)
		turn.CompletedAt = &value
	}
	return turn
}

func responseItemFromRecord(record *dao.ResponseItemRecord) ResponseItemArchive {
	item := ResponseItemArchive{ID: record.ID, SessionID: record.SessionID, LocalTurnID: record.LocalTurnID,
		OutputIndex: record.OutputIndex, ItemType: record.ItemType, ItemKey: record.ItemKey,
		SanitizedJSON: cloneArchiveJSON(record.SanitizedJSON), CreatedAt: parseSessionTimestamp(record.CreatedAt)}
	if record.ResponseID != nil {
		item.ResponseID = *record.ResponseID
	}
	if record.ItemID != nil {
		item.ItemID = *record.ItemID
	}
	if record.ItemStatus != nil {
		item.ItemStatus = *record.ItemStatus
	}
	return item
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64Value(value dao.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func archiveJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	sanitized := redactArchiveValue(value)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxResponseArchiveJSONBytes {
		return nil, fmt.Errorf("JSON exceeds %d bytes", maxResponseArchiveJSONBytes)
	}
	return encoded, nil
}

func redactArchiveValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			lower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			if strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "password") || strings.Contains(lower, "cookie") {
				if isArchiveUsageCounter(lower, nested) {
					result[key] = nested
					continue
				}
				result[key] = "[REDACTED]"
				continue
			}
			result[key] = redactArchiveValue(nested)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, nested := range typed {
			result[i] = redactArchiveValue(nested)
		}
		return result
	default:
		return value
	}
}

// isArchiveUsageCounter preserves well-known numeric usage counters without
// weakening redaction for credentials such as access_token or id_token.
func isArchiveUsageCounter(key string, value any) bool {
	if _, ok := value.(json.Number); !ok {
		return false
	}
	key = strings.ReplaceAll(key, "_", "")
	switch key {
	case "inputtokens", "outputtokens", "totaltokens", "cachedtokens", "reasoningtokens",
		"prompttokens", "completiontokens", "cachereadtokens", "cachewritetokens":
		return true
	default:
		return false
	}
}

func cloneArchiveJSON(raw []byte) json.RawMessage {
	if raw == nil {
		return nil
	}
	result := make([]byte, len(raw))
	copy(result, raw)
	return json.RawMessage(result)
}
