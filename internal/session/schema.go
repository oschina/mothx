package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	database "github.com/startvibecoding/mothx/internal/db"
)

const currentSchemaTemplate = `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY,
	cwd TEXT,
	timestamp TEXT,
	parent_session TEXT,
	version INTEGER,
	channel_type TEXT NOT NULL DEFAULT 'local',
	channel_id TEXT NOT NULL DEFAULT '',
	fork_boundary_seq INTEGER NOT NULL DEFAULT 0,
	seed_length INTEGER NOT NULL DEFAULT 0,
	fork_kind TEXT NOT NULL DEFAULT '',
	expert_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE entries (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
	id TEXT UNIQUE,
	type TEXT NOT NULL,
	parent_id TEXT,
	timestamp TEXT NOT NULL,
	data TEXT NOT NULL
);
CREATE INDEX idx_entries_session_id ON entries(session_id);
CREATE INDEX idx_entries_type ON entries(type);
CREATE INDEX idx_sessions_cwd ON sessions(cwd);
CREATE TABLE request_stats (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp TEXT NOT NULL,
	session_id TEXT,
	provider TEXT NOT NULL,
	model TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	protocol TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_request_stats_timestamp ON request_stats(timestamp);
CREATE INDEX idx_request_stats_provider ON request_stats(provider);
CREATE INDEX idx_request_stats_model ON request_stats(model);
CREATE TABLE session_capabilities (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	mode TEXT NOT NULL DEFAULT '',
	display_mode TEXT NOT NULL DEFAULT 'work',
	delegate_mode INTEGER NOT NULL DEFAULT 0,
	multi_agent INTEGER NOT NULL DEFAULT 0,
	workflows INTEGER NOT NULL DEFAULT 0,
	web_search INTEGER NOT NULL DEFAULT 0,
	browser INTEGER NOT NULL DEFAULT 0,
	a2a_master INTEGER NOT NULL DEFAULT 0,
	updated_at TEXT NOT NULL
);
CREATE TABLE session_run_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT UNIQUE NOT NULL,
	session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	mode TEXT NOT NULL DEFAULT '',
	timestamp TEXT NOT NULL,
	data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_session_run_events_session_id ON session_run_events(session_id);
CREATE INDEX idx_session_run_events_run_id ON session_run_events(run_id);
CREATE INDEX idx_session_run_events_type ON session_run_events(event_type);
CREATE TABLE session_runs (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	intent_id TEXT NOT NULL DEFAULT '',
	retry_of TEXT NOT NULL DEFAULT '',
	attempt INTEGER NOT NULL DEFAULT 1,
	work_dir TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	mode TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	started_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	finished_at TEXT,
	error TEXT NOT NULL DEFAULT '',
	error_info_json TEXT NOT NULL DEFAULT '{}',
	progress_json TEXT NOT NULL DEFAULT '{}',
	usage_json TEXT NOT NULL DEFAULT '{}',
	context_usage_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_session_runs_session_id ON session_runs(session_id);
CREATE INDEX idx_session_runs_status ON session_runs(status);
CREATE INDEX idx_session_runs_intent ON session_runs(session_id, intent_id, attempt);
CREATE UNIQUE INDEX idx_session_runs_active_session ON session_runs(session_id) WHERE status IN (%s);
CREATE TABLE session_run_recoveries (
	run_id TEXT PRIMARY KEY REFERENCES session_runs(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	state TEXT NOT NULL,
	trigger_source TEXT NOT NULL DEFAULT '',
	reason_code TEXT NOT NULL DEFAULT '',
	attempt INTEGER NOT NULL DEFAULT 0,
	previous_lease_epoch INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	next_retry_at INTEGER,
	started_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	completed_at INTEGER
);
CREATE INDEX idx_session_run_recoveries_due ON session_run_recoveries(state, next_retry_at);
CREATE TABLE session_execution_intents (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	source TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	mode TEXT NOT NULL DEFAULT '',
	work_dir TEXT NOT NULL DEFAULT '',
	request_fingerprint TEXT NOT NULL DEFAULT '',
	request_json TEXT NOT NULL DEFAULT '{}',
	policy_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL
);
CREATE INDEX idx_session_execution_intents_session_id ON session_execution_intents(session_id, created_at);
CREATE TABLE runtime_submissions (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	scope TEXT NOT NULL,
	key_hash TEXT NOT NULL,
	request_fingerprint TEXT NOT NULL DEFAULT '',
	intent_id TEXT NOT NULL REFERENCES session_execution_intents(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	UNIQUE(session_id, scope, key_hash)
);
CREATE INDEX idx_runtime_submissions_run ON runtime_submissions(run_id);
CREATE INDEX idx_runtime_submissions_intent ON runtime_submissions(intent_id);
CREATE TABLE delivery_intents (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE,
	platform TEXT NOT NULL,
	target_id TEXT NOT NULL DEFAULT '',
	reply_message_id TEXT NOT NULL DEFAULT '',
	transport_context TEXT NOT NULL DEFAULT '{}',
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(run_id, platform, target_id)
);
CREATE INDEX idx_delivery_intents_session_status ON delivery_intents(session_id, status, updated_at);
CREATE TABLE delivery_operations (
	id TEXT PRIMARY KEY,
	intent_id TEXT NOT NULL REFERENCES delivery_intents(id) ON DELETE CASCADE,
	operation_key TEXT NOT NULL,
	artifact_id TEXT REFERENCES session_attachments(id) ON DELETE RESTRICT,
	operation_kind TEXT NOT NULL,
	sequence INTEGER NOT NULL,
	depends_on TEXT REFERENCES delivery_operations(id) ON DELETE RESTRICT,
	idempotency_key TEXT NOT NULL,
	payload_digest TEXT NOT NULL,
	status TEXT NOT NULL,
	provider_asset_id TEXT NOT NULL DEFAULT '',
	provider_message_id TEXT NOT NULL DEFAULT '',
	provider_state TEXT NOT NULL DEFAULT '{}',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	next_attempt_at INTEGER,
	failure_code TEXT NOT NULL DEFAULT '',
	lease_owner TEXT NOT NULL DEFAULT '',
	lease_epoch INTEGER NOT NULL DEFAULT 0,
	lease_expires_at INTEGER,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(intent_id, operation_key),
	UNIQUE(intent_id, sequence)
);
CREATE INDEX idx_delivery_operations_claim ON delivery_operations(status, next_attempt_at, lease_expires_at, sequence);
CREATE TABLE input_resources (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL DEFAULT '',
	origin TEXT NOT NULL DEFAULT '',
	event_id TEXT NOT NULL DEFAULT '',
	item_index INTEGER NOT NULL DEFAULT 0,
	item_key TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	filename TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
	byte_size INTEGER NOT NULL DEFAULT 0,
	sha256 TEXT NOT NULL DEFAULT '',
	relative_path TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	metadata TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_input_resources_session ON input_resources(session_id, created_at);
CREATE UNIQUE INDEX idx_input_resources_item_key ON input_resources(session_id, item_key) WHERE item_key <> '';
CREATE TABLE input_resource_events (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	resource_id TEXT NOT NULL REFERENCES input_resources(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT '',
	timestamp TEXT NOT NULL,
	data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_input_resource_events_resource ON input_resource_events(resource_id, timestamp);
CREATE INDEX idx_input_resource_events_session ON input_resource_events(session_id, timestamp);
CREATE TABLE session_attachments (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL DEFAULT '',
	origin TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	filename TEXT NOT NULL DEFAULT '',
	media_type TEXT NOT NULL DEFAULT '',
	byte_size INTEGER NOT NULL DEFAULT 0,
	sha256 TEXT NOT NULL DEFAULT '',
	storage_key TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	metadata TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_session_attachments_session ON session_attachments(session_id, created_at);
CREATE TABLE attachment_deliveries (
	id TEXT PRIMARY KEY,
	attachment_id TEXT NOT NULL REFERENCES session_attachments(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL DEFAULT '',
	platform TEXT NOT NULL,
	target_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	provider_message_id TEXT NOT NULL DEFAULT '',
	failure_code TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_attachment_deliveries_attachment ON attachment_deliveries(attachment_id, updated_at);
CREATE TABLE session_capability_events (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	id TEXT UNIQUE NOT NULL,
	session_id TEXT REFERENCES sessions(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL DEFAULT '',
	capability TEXT NOT NULL,
	old_value TEXT NOT NULL DEFAULT '',
	new_value TEXT NOT NULL DEFAULT '',
	timestamp TEXT NOT NULL,
	data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_session_capability_events_session_id ON session_capability_events(session_id);
CREATE INDEX idx_session_capability_events_run_id ON session_capability_events(run_id);
CREATE INDEX idx_session_capability_events_capability ON session_capability_events(capability);
CREATE TABLE conversation_turns (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	intent_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'conversation',
	status TEXT NOT NULL,
	start_seq INTEGER NOT NULL,
	end_seq INTEGER,
	started_at TEXT NOT NULL,
	ended_at TEXT
);
CREATE INDEX idx_conversation_turns_session ON conversation_turns(session_id, start_seq);
CREATE INDEX idx_conversation_turns_open ON conversation_turns(session_id, status);
CREATE TABLE session_fork_requests (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	request_key_hash TEXT NOT NULL,
	request_fingerprint TEXT NOT NULL,
	source_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	child_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	UNIQUE(request_key_hash, source_session_id)
);
CREATE TABLE session_runtime_leases (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	owner_instance_id TEXT NOT NULL,
	owner_pid INTEGER NOT NULL,
	owner_kind TEXT NOT NULL,
	lease_token_hash TEXT NOT NULL,
	epoch INTEGER NOT NULL,
	run_id TEXT NOT NULL DEFAULT '',
	purpose TEXT NOT NULL,
	state TEXT NOT NULL,
	acquired_at INTEGER NOT NULL,
	heartbeat_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX idx_session_runtime_leases_expiry ON session_runtime_leases(expires_at);
CREATE TABLE response_turns (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	local_turn_id TEXT NOT NULL,
	message_id INTEGER,
	request_id TEXT,
	response_id TEXT,
	previous_response_id TEXT,
	conversation_id TEXT,
	provider TEXT NOT NULL,
	api TEXT NOT NULL,
	model TEXT NOT NULL,
	state_mode TEXT NOT NULL,
	status TEXT NOT NULL,
	incomplete_reason TEXT,
	request_summary_json BLOB,
	response_summary_json BLOB,
	created_at DATETIME NOT NULL,
	completed_at DATETIME,
	UNIQUE(session_id, local_turn_id)
);
CREATE INDEX idx_response_turns_session_id ON response_turns(session_id);
CREATE INDEX idx_response_turns_response_id ON response_turns(response_id);
CREATE TABLE response_items (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	local_turn_id TEXT NOT NULL,
	response_id TEXT,
	item_id TEXT,
	output_index INTEGER NOT NULL,
	item_type TEXT NOT NULL,
	item_status TEXT,
	item_key TEXT NOT NULL,
	sanitized_json BLOB NOT NULL,
	created_at DATETIME NOT NULL,
	updated_at DATETIME
);
CREATE INDEX idx_response_items_session_turn ON response_items(session_id, local_turn_id);
CREATE INDEX idx_response_items_response_id ON response_items(response_id);
CREATE UNIQUE INDEX idx_response_items_identity ON response_items(session_id, local_turn_id, item_key);
CREATE TABLE tool_execution_records (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	local_turn_id TEXT NOT NULL,
	execution_key TEXT NOT NULL,
	provider TEXT NOT NULL,
	api TEXT NOT NULL,
	response_id TEXT,
	provider_call_id TEXT,
	tool_kind TEXT NOT NULL,
	tool_name TEXT NOT NULL,
	args_hash TEXT NOT NULL,
	execution_state TEXT NOT NULL,
	result_summary_json BLOB,
	provider_metadata_json BLOB,
	side_effecting BOOLEAN NOT NULL,
	created_at DATETIME NOT NULL,
	completed_at DATETIME,
	UNIQUE(execution_key)
);
CREATE INDEX idx_tool_execution_records_session_turn ON tool_execution_records(session_id, local_turn_id);
CREATE INDEX idx_tool_execution_records_provider_call ON tool_execution_records(provider, api, provider_call_id);
CREATE TABLE response_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	local_run_id TEXT NOT NULL,
	local_turn_id TEXT NOT NULL DEFAULT '',
	message_id INTEGER,
	response_id TEXT,
	provider TEXT NOT NULL,
	api TEXT NOT NULL,
	state TEXT NOT NULL,
	polling_url TEXT,
	last_event_sequence INTEGER,
	cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	UNIQUE(session_id, local_run_id)
);
CREATE INDEX idx_response_runs_session_id ON response_runs(session_id);
CREATE INDEX idx_response_runs_state ON response_runs(state);
CREATE INDEX idx_response_runs_session_turn ON response_runs(session_id, local_turn_id);
CREATE TABLE response_session_state (
	session_id TEXT PRIMARY KEY,
	state_mode TEXT NOT NULL DEFAULT 'replay',
	previous_response_id TEXT,
	conversation_id TEXT,
	provider TEXT NOT NULL DEFAULT '',
	api TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	version INTEGER NOT NULL DEFAULT 0,
	updated_at DATETIME NOT NULL
);
CREATE TABLE cron_jobs (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	prompt TEXT NOT NULL DEFAULT '',
	schedule TEXT NOT NULL DEFAULT '',
	oneshot INTEGER NOT NULL DEFAULT 0,
	mode TEXT NOT NULL DEFAULT 'yolo',
	work_dir TEXT NOT NULL DEFAULT '',
	a2a_target TEXT NOT NULL DEFAULT '',
	a2a_token TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	last_run TEXT NOT NULL DEFAULT '',
	next_run TEXT NOT NULL DEFAULT '',
	run_count INTEGER NOT NULL DEFAULT 0,
	last_status TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_cron_jobs_session_id ON cron_jobs(session_id);
CREATE INDEX idx_cron_jobs_enabled ON cron_jobs(enabled);
CREATE INDEX idx_cron_jobs_next_run ON cron_jobs(next_run);
CREATE INDEX idx_cron_jobs_created_at ON cron_jobs(created_at);
CREATE TABLE session_esm_objectives (
	session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
	esm_id TEXT NOT NULL,
	objective TEXT NOT NULL,
	status TEXT NOT NULL,
	token_budget INTEGER,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	time_used_ms INTEGER NOT NULL DEFAULT 0,
	blocked_count INTEGER NOT NULL DEFAULT 0,
	blocked_reason TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	blocked_run_id TEXT NOT NULL DEFAULT '',
	completion_reason TEXT NOT NULL DEFAULT '',
	completion_run_id TEXT NOT NULL DEFAULT '',
	completion_review TEXT NOT NULL DEFAULT '',
	phase TEXT NOT NULL DEFAULT '',
	progress_summary TEXT NOT NULL DEFAULT '',
	remaining_work TEXT NOT NULL DEFAULT '[]',
	completion_rejection_count INTEGER NOT NULL DEFAULT 0,
	completion_rejection_run_id TEXT NOT NULL DEFAULT '',
	recovery_count INTEGER NOT NULL DEFAULT 0,
	recovery_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_session_esm_objectives_status ON session_esm_objectives(status);
CREATE TABLE sub_session (
	id TEXT PRIMARY KEY,
	cwd TEXT,
	timestamp TEXT,
	parent_session TEXT,
	version INTEGER,
	channel_type TEXT NOT NULL DEFAULT 'local',
	channel_id TEXT NOT NULL DEFAULT '',
	fork_boundary_seq INTEGER NOT NULL DEFAULT 0,
	seed_length INTEGER NOT NULL DEFAULT 0,
	fork_kind TEXT NOT NULL DEFAULT '',
	expert_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE sub_entries (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT REFERENCES sub_session(id) ON DELETE CASCADE,
	id TEXT UNIQUE,
	type TEXT NOT NULL,
	parent_id TEXT,
	timestamp TEXT NOT NULL,
	data TEXT NOT NULL
);
CREATE INDEX idx_sub_entries_session_id ON sub_entries(session_id);
CREATE INDEX idx_sub_entries_type ON sub_entries(type);
CREATE INDEX idx_sub_session_cwd ON sub_session(cwd);
`

// currentSchema is the active new-database schema text. The non-terminal run
// status set is injected from the single source of truth so the partial unique
// index cannot drift from nonTerminalSessionRunStatusList.
var currentSchema = fmt.Sprintf(currentSchemaTemplate, nonTerminalSessionRunStatusSQL())

var requiredSchema = map[string][]string{
	"sessions":                         {"id", "cwd", "timestamp", "parent_session", "version", "channel_type", "channel_id", "fork_boundary_seq", "seed_length", "fork_kind", "expert_id"},
	"entries":                          {"seq", "session_id", "id", "type", "parent_id", "timestamp", "data"},
	"request_stats":                    {"id", "timestamp", "session_id", "provider", "protocol", "model", "input_tokens", "output_tokens", "total_tokens", "duration_ms"},
	"session_capabilities":             {"session_id", "mode", "display_mode", "delegate_mode", "multi_agent", "workflows", "web_search", "browser", "a2a_master", "updated_at"},
	"conversation_turns":               {"id", "session_id", "intent_id", "kind", "status", "start_seq", "end_seq", "started_at", "ended_at"},
	"session_fork_requests":            {"id", "request_key_hash", "request_fingerprint", "source_session_id", "child_session_id", "created_at"},
	"session_runtime_leases":           {"session_id", "owner_instance_id", "owner_pid", "owner_kind", "lease_token_hash", "epoch", "run_id", "purpose", "state", "acquired_at", "heartbeat_at", "expires_at", "updated_at"},
	"session_run_events":               {"seq", "id", "session_id", "run_id", "event_type", "source", "status", "model", "mode", "timestamp", "data"},
	"session_runs":                     {"id", "session_id", "intent_id", "retry_of", "attempt", "work_dir", "source", "model", "mode", "status", "started_at", "updated_at", "finished_at", "error", "error_info_json", "progress_json", "usage_json", "context_usage_json"},
	"session_run_recoveries":           {"run_id", "session_id", "state", "trigger_source", "reason_code", "attempt", "previous_lease_epoch", "last_error", "next_retry_at", "started_at", "updated_at", "completed_at"},
	"session_execution_intents":        {"id", "session_id", "source", "model", "mode", "work_dir", "request_fingerprint", "request_json", "policy_json", "created_at"},
	"runtime_submissions":              {"id", "session_id", "scope", "key_hash", "request_fingerprint", "intent_id", "run_id", "created_at"},
	"input_resource_events":            {"id", "session_id", "resource_id", "run_id", "event_type", "status", "timestamp", "data"},
	"delivery_intents":                 {"id", "session_id", "run_id", "platform", "target_id", "reply_message_id", "transport_context", "status", "created_at", "updated_at"},
	"delivery_operations":              {"id", "intent_id", "operation_key", "artifact_id", "operation_kind", "sequence", "depends_on", "idempotency_key", "payload_digest", "status", "provider_asset_id", "provider_message_id", "provider_state", "attempt_count", "next_attempt_at", "failure_code", "lease_owner", "lease_epoch", "lease_expires_at", "created_at", "updated_at"},
	"input_resources":                  {"id", "session_id", "run_id", "origin", "event_id", "item_index", "item_key", "kind", "filename", "media_type", "byte_size", "sha256", "relative_path", "status", "created_at", "metadata"},
	"session_capability_events":        {"seq", "id", "session_id", "run_id", "event_type", "source", "actor", "capability", "old_value", "new_value", "timestamp", "data"},
	"response_turns":                   {"id", "session_id", "local_turn_id", "message_id", "request_id", "response_id", "previous_response_id", "conversation_id", "provider", "api", "model", "state_mode", "status", "incomplete_reason", "request_summary_json", "response_summary_json", "created_at", "completed_at"},
	"response_items":                   {"id", "session_id", "local_turn_id", "response_id", "item_id", "output_index", "item_type", "item_status", "item_key", "sanitized_json", "created_at", "updated_at"},
	"tool_execution_records":           {"id", "session_id", "local_turn_id", "execution_key", "provider", "api", "response_id", "provider_call_id", "tool_kind", "tool_name", "args_hash", "execution_state", "result_summary_json", "provider_metadata_json", "side_effecting", "created_at", "completed_at"},
	"response_runs":                    {"id", "session_id", "local_run_id", "local_turn_id", "message_id", "response_id", "provider", "api", "state", "polling_url", "last_event_sequence", "cancel_requested", "created_at", "updated_at"},
	"response_session_state":           {"session_id", "state_mode", "previous_response_id", "conversation_id", "provider", "api", "model", "version", "updated_at"},
	"cron_jobs":                        {"id", "session_id", "name", "prompt", "schedule", "oneshot", "mode", "work_dir", "a2a_target", "a2a_token", "enabled", "created_at", "last_run", "next_run", "run_count", "last_status", "last_error"},
	"session_esm_objectives":           {"session_id", "esm_id", "objective", "status", "token_budget", "tokens_used", "time_used_ms", "blocked_count", "blocked_reason", "created_at", "updated_at", "blocked_run_id", "completion_reason", "completion_run_id", "completion_review", "phase", "progress_summary", "remaining_work", "completion_rejection_count", "completion_rejection_run_id", "recovery_count", "recovery_reason"},
	"session_attachments":              {"id", "session_id", "run_id", "origin", "kind", "filename", "media_type", "byte_size", "sha256", "storage_key", "status", "created_at", "expires_at", "metadata"},
	"attachment_deliveries":            {"id", "attachment_id", "run_id", "platform", "target_id", "status", "provider_message_id", "failure_code", "created_at", "updated_at"},
	"session_esm_guidance":             {"id", "session_id", "objective_version", "guidance", "status", "created_at", "consumed_at"},
	"projects":                         {"id", "name", "created_at", "updated_at"},
	"session_metadata":                 {"session_id", "project_id", "pinned", "updated_at"},
	"session_channel_tools":            {"session_id", "tool_name", "enabled", "updated_at"},
	"session_channel_tool_generations": {"session_id", "generation", "updated_at"},
	"sub_session":                      {"id", "cwd", "timestamp", "parent_session", "version", "channel_type", "channel_id", "fork_boundary_seq", "seed_length", "fork_kind", "expert_id"},
	"sub_entries":                      {"seq", "session_id", "id", "type", "parent_id", "timestamp", "data"},
}

// busyRetryDatabase applies the shared SQLITE_BUSY begin retry (see
// internal/db.BeginSQLTx) to the schema migration transaction boundary: every
// process opening one session directory migrates the same database, and the
// single writer lock can outlast a busy_timeout while another process commits.
type busyRetryDatabase struct{ db *sql.DB }

func (b busyRetryDatabase) Begin() (*sql.Tx, error) {
	return database.BeginSQLTx(context.Background(), b.db, nil)
}

// EnsureCurrentSchema creates the current schema only for an empty database.
// Existing databases are validated but never migrated or otherwise modified.
func EnsureCurrentSchema(db *sql.DB) error {
	var tableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`).Scan(&tableCount); err != nil {
		return fmt.Errorf("inspect database schema: %w", err)
	}
	if tableCount == 0 {
		// Initialization races other processes opening the same session
		// directory; a busy begin must not turn schema setup into a hard failure.
		tx, err := database.BeginSQLTx(context.Background(), db, nil)
		if err != nil {
			return fmt.Errorf("begin schema initialization: %w", err)
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`).Scan(&tableCount); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recheck database schema: %w", err)
		}
		if tableCount == 0 {
			if _, err := tx.Exec(currentSchema); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("initialize database schema: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit database schema: %w", err)
		}
	}

	if err := applySchemaMigrations(busyRetryDatabase{db: db}); err != nil {
		return fmt.Errorf("apply schema migrations: %w", err)
	}

	for table, requiredColumns := range requiredSchema {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return fmt.Errorf("inspect table %s: %w", table, err)
		}
		columns := make(map[string]bool)
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return fmt.Errorf("inspect table %s: %w", table, err)
			}
			columns[name] = true
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("inspect table %s: %w", table, err)
		}
		var missing []string
		for _, column := range requiredColumns {
			if !columns[column] {
				missing = append(missing, column)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("database schema is incompatible: table %s is missing columns %s", table, strings.Join(missing, ", "))
		}
	}
	return nil
}
