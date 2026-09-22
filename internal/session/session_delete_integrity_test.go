package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
)

// TestDeleteSessionRemovesEveryChildRow is the integrity guard required after
// a session deletion: every table that carries session data must be empty for
// the deleted session. It enumerates tables dynamically so a future table with
// a session_id column fails the test until it is added to the deletion list.
func TestDeleteSessionRemovesEveryChildRow(t *testing.T) {
	sessionDir := t.TempDir()
	m := New("/tmp/delete-integrity", sessionDir)
	if err := m.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := m.GetHeader().ID
	if _, err := m.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatalf("append message: %v", err)
	}
	// Usage statistics are intentionally kept after deletion; seed one row to
	// prove the exception. Record it before the runtime lease row below is
	// seeded: RecordUsage is lease-fenced and would reject the write once a
	// (released) lease exists for this session.
	if err := m.RecordUsage("test", "chat", "model", 10, 20, 30, 100); err != nil {
		t.Fatalf("seed request stats: %v", err)
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	runID, intentID := "run-delete-test", "intent-delete-test"
	attachmentID, resourceID, deliveryIntentID := "att-delete-test", "res-delete-test", "di-delete-test"

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	ctx := context.Background()
	sessionDAO := dao.NewSessionDAO(nil)
	if err := sessionDAO.UpsertCapability(ctx, tx, &dao.SessionCapabilityRecord{SessionID: sessionID, Mode: "yolo", DisplayMode: "work", UpdatedAt: now}); err != nil {
		t.Fatalf("seed capabilities: %v", err)
	}
	if err := sessionDAO.InsertCapabilityEvent(ctx, tx, &dao.SessionCapabilityEventRecord{ID: "capev-1", SessionID: sessionID, EventType: "change", Capability: "mode", Timestamp: now, Data: "{}"}); err != nil {
		t.Fatalf("seed capability event: %v", err)
	}
	runDAO := dao.NewRunDAO(nil)
	if err := runDAO.InsertIntent(ctx, tx, &dao.ExecutionIntentRecord{ID: intentID, SessionID: sessionID, RequestJSON: "{}", PolicyJSON: "{}", CreatedAt: now}); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	if err := runDAO.InsertRun(ctx, tx, &dao.SessionRunRecord{
		ID: runID, SessionID: sessionID, IntentID: intentID, Attempt: 1, Status: "completed",
		StartedAt: now, UpdatedAt: now, ErrorInfoJSON: "{}", ProgressJSON: "{}", UsageJSON: "{}", ContextUsageJSON: "{}",
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if err := sessionDAO.InsertRunEvent(ctx, tx, &dao.SessionRunEventRecord{ID: "runev-1", SessionID: sessionID, RunID: runID, EventType: "started", Timestamp: now, Data: "{}"}); err != nil {
		t.Fatalf("seed run event: %v", err)
	}
	if err := dao.NewRecoveryDAO(nil).Upsert(ctx, tx, &dao.RecoveryRecord{RunID: runID, SessionID: sessionID, State: "completed", StartedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed run recovery: %v", err)
	}
	if err := dao.NewRuntimeSubmissionDAO(nil).Insert(ctx, tx, &dao.RuntimeSubmissionRecord{ID: "sub-1", SessionID: sessionID, Scope: "run", KeyHash: "kh-1", IntentID: intentID, RunID: runID, CreatedAt: now}); err != nil {
		t.Fatalf("seed submission: %v", err)
	}
	inputDAO := dao.NewInputResourceDAO(nil)
	if err := inputDAO.Insert(ctx, tx, &dao.InputResourceRecord{ID: resourceID, SessionID: sessionID, Kind: "file", RelativePath: "a.txt", Status: "prepared", CreatedAt: now, Metadata: "{}"}); err != nil {
		t.Fatalf("seed input resource: %v", err)
	}
	if err := inputDAO.AppendEvent(ctx, tx, &dao.InputResourceEventRecord{ID: "resev-1", SessionID: sessionID, ResourceID: resourceID, EventType: "prepared", Timestamp: now, Data: "{}"}); err != nil {
		t.Fatalf("seed input resource event: %v", err)
	}
	if err := dao.NewAttachmentDAO(nil).Insert(ctx, tx, &dao.AttachmentRecord{ID: attachmentID, SessionID: sessionID, Kind: "image", StorageKey: "key-1", Status: "active", CreatedAt: now, ExpiresAt: now, Metadata: "{}"}); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}
	deliveryDAO := dao.NewDeliveryDAO(nil)
	if _, err := deliveryDAO.InsertIntent(ctx, tx, &dao.DeliveryIntentRecord{ID: deliveryIntentID, SessionID: sessionID, RunID: runID, Platform: "wechat", Status: "pending", TransportContext: "{}", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed delivery intent: %v", err)
	}
	if _, err := deliveryDAO.InsertOperation(ctx, tx, &dao.DeliveryOperationRecord{
		ID: "dop-1", IntentID: deliveryIntentID, OperationKey: "op-1", ArtifactID: &attachmentID,
		OperationKind: "send_artifact", Sequence: 1, IdempotencyKey: "idem-1", PayloadDigest: "digest",
		Status: "pending", ProviderState: "{}", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed delivery operation: %v", err)
	}
	if err := dao.NewForkDAO(nil).InsertForkRequest(ctx, tx, &dao.ForkRequestRecord{RequestKeyHash: "fork-key-1", RequestFingerprint: "fp-1", SourceSessionID: sessionID, ChildSessionID: "child-does-not-matter", CreatedAt: now}); err != nil {
		t.Fatalf("seed fork request: %v", err)
	}
	if err := dao.NewRuntimeLeaseDAO(nil).Insert(ctx, tx, &dao.RuntimeLeaseRecord{
		SessionID: sessionID, OwnerID: "owner-1", OwnerPID: 1, OwnerKind: "test", TokenHash: "token", Epoch: 1,
		Purpose: "mutation", State: "released", AcquiredAt: 1, HeartbeatAt: 1, ExpiresAt: 2, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed runtime lease: %v", err)
	}
	if err := dao.NewESMDAO(nil).Insert(ctx, tx, &dao.ESMObjectiveRecord{SessionID: sessionID, ESMID: "esm-1", Objective: "objective", Status: "active", RemainingWork: "[]", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed esm objective: %v", err)
	}
	if err := dao.NewESMGuidanceDAO(nil).Insert(ctx, tx, &dao.ESMGuidanceRecord{ID: "guidance-1", SessionID: sessionID, Guidance: "guidance", Status: "pending", CreatedAt: now}); err != nil {
		t.Fatalf("seed esm guidance: %v", err)
	}
	if err := dao.NewConversationTurnDAO(nil).Insert(ctx, tx, &dao.ConversationTurnRecord{ID: "turn-1", SessionID: sessionID, Kind: "conversation", Status: "completed", StartSeq: 1, StartedAt: now}); err != nil {
		t.Fatalf("seed conversation turn: %v", err)
	}
	responseDAO := dao.NewResponseDAO(nil)
	if err := responseDAO.InsertTurn(ctx, tx, &dao.ResponseTurnRecord{SessionID: sessionID, LocalTurnID: "lt-1", Provider: "p", API: "a", Model: "m", StateMode: "replay", Status: "completed", CreatedAt: now}); err != nil {
		t.Fatalf("seed response turn: %v", err)
	}
	if err := responseDAO.UpsertItem(ctx, tx, &dao.ResponseItemRecord{SessionID: sessionID, LocalTurnID: "lt-1", OutputIndex: 0, ItemType: "message", ItemKey: "item-key", SanitizedJSON: []byte("{}"), CreatedAt: now}); err != nil {
		t.Fatalf("seed response item: %v", err)
	}
	if err := responseDAO.InsertRun(ctx, tx, &dao.ResponseRunRecord{SessionID: sessionID, LocalRunID: "lr-1", Provider: "p", API: "a", State: "completed", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed response run: %v", err)
	}
	if _, err := responseDAO.InsertSessionState(ctx, tx, &dao.ResponseSessionStateRecord{SessionID: sessionID, StateMode: "replay", UpdatedAt: now}); err != nil {
		t.Fatalf("seed response session state: %v", err)
	}
	// tool_execution_records and attachment_deliveries have no dedicated
	// insert DAO; seed them with raw statements through the transaction.
	if _, err := tx.NewRaw(`INSERT INTO tool_execution_records
		(session_id, local_turn_id, execution_key, provider, api, tool_kind, tool_name, args_hash, execution_state, side_effecting, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, "lt-1", "exec-key-1", "p", "a", "builtin", "bash", "hash", "completed", 0, now).Exec(ctx); err != nil {
		t.Fatalf("seed tool execution record: %v", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO attachment_deliveries
		(id, attachment_id, run_id, platform, target_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"adel-1", attachmentID, runID, "wechat", "target-1", "pending", now, now).Exec(ctx); err != nil {
		t.Fatalf("seed attachment delivery: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed transaction: %v", err)
	}

	// DAOs that own their transactions seed the remaining tables.
	if err := dao.NewProjectDAO(db.Bun()).UpsertMetadata(ctx, &dao.SessionMetadataRecord{SessionID: sessionID, Pinned: 1, UpdatedAt: now}); err != nil {
		t.Fatalf("seed session metadata: %v", err)
	}
	if err := dao.NewCronDAO(db.Bun()).Create(ctx, &dao.CronJobRecord{ID: "cron-1", SessionID: sessionID, Prompt: "p", Schedule: "* * * * *", Mode: "yolo", Enabled: true, CreatedAt: now}); err != nil {
		t.Fatalf("seed cron job: %v", err)
	}
	if err := dao.NewBindingDAO(db.Bun()).SetChannelTools(ctx, sessionID, []dao.ChannelToolRecord{{ToolName: "tool_a", Enabled: true}}); err != nil {
		t.Fatalf("seed channel tools: %v", err)
	}

	if err := DeleteSession(m.GetFile(), sessionDir); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(m.GetFile()); !os.IsNotExist(err) {
		t.Fatalf("session file still exists after deletion (err=%v)", err)
	}

	sqlDB := db.Bun().DB
	if sqlDB == nil {
		t.Fatal("resolve sql handle")
	}
	tables, err := sqlDB.Query(`SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tableNames []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			tables.Close()
			t.Fatalf("scan table name: %v", err)
		}
		tableNames = append(tableNames, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	tables.Close()

	countWhere := func(table, where string, args ...any) int {
		var count int
		query := "SELECT COUNT(*) FROM " + table
		if where != "" {
			query += " WHERE " + where
		}
		if err := sqlDB.QueryRow(query, args...).Scan(&count); err != nil {
			t.Fatalf("count rows in %s: %v", table, err)
		}
		return count
	}
	hasSessionIDColumn := func(table string) bool {
		columns, err := sqlDB.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		defer columns.Close()
		for columns.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := columns.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				t.Fatalf("scan %s columns: %v", table, err)
			}
			if name == "session_id" {
				return true
			}
		}
		return false
	}

	for _, table := range tableNames {
		switch table {
		case "sessions", "projects", "sub_session", "request_stats":
			// Checked below or intentionally not session-scoped.
			continue
		case "delivery_operations", "attachment_deliveries", "session_fork_requests":
			continue // no session_id column; checked with their own keys below
		}
		if !hasSessionIDColumn(table) {
			continue
		}
		if count := countWhere(table, "session_id = ?", sessionID); count != 0 {
			t.Errorf("table %s still has %d row(s) for deleted session %s", table, count, sessionID)
		}
	}

	if count := countWhere("sessions", "id = ?", sessionID); count != 0 {
		t.Errorf("sessions table still has %d row(s) for deleted session", count)
	}
	if count := countWhere("session_fork_requests", "source_session_id = ? OR child_session_id = ?", sessionID, sessionID); count != 0 {
		t.Errorf("session_fork_requests still has %d row(s) for deleted session", count)
	}
	// Only the deleted session's rows existed in these tables, so any leftover
	// row is an orphan.
	if count := countWhere("delivery_operations", ""); count != 0 {
		t.Errorf("delivery_operations still has %d orphaned row(s)", count)
	}
	if count := countWhere("attachment_deliveries", ""); count != 0 {
		t.Errorf("attachment_deliveries still has %d orphaned row(s)", count)
	}
	// Usage statistics survive session deletion by design.
	if count := countWhere("request_stats", "session_id = ?", sessionID); count == 0 {
		t.Error("request_stats rows were deleted; usage statistics must be retained")
	}
}
