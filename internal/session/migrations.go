package session

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const currentSchemaVersion = 43

type schemaMigration struct {
	version int
	name    string
	apply   func(*sql.Tx) error
}

// schemaMigrations lists every schema migration. Migrations are applied in
// ascending version order regardless of their position in this slice (see
// applySchemaMigrations); several later migrations reference tables created by
// earlier ones, so order must never depend on slice layout. New migrations
// must always append a fresh, monotonically increasing version at the end.
var schemaMigrations = []schemaMigration{
	{version: 35, name: "create_input_resource_events", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS input_resource_events (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			resource_id TEXT NOT NULL REFERENCES input_resources(id) ON DELETE CASCADE,
			run_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT '',
			timestamp TEXT NOT NULL,
			data TEXT NOT NULL DEFAULT '{}'
		)`); err != nil {
			return fmt.Errorf("create input resource events: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_input_resource_events_resource
			ON input_resource_events(resource_id, timestamp)`); err != nil {
			return fmt.Errorf("index input resource events by resource: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_input_resource_events_session
			ON input_resource_events(session_id, timestamp)`)
		return err
	}},
	{version: 34, name: "create_delivery_intents_and_operations", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS delivery_intents (
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
		)`); err != nil {
			return fmt.Errorf("create delivery intents: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_delivery_intents_session_status
			ON delivery_intents(session_id, status, updated_at)`); err != nil {
			return fmt.Errorf("index delivery intents: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS delivery_operations (
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
		)`); err != nil {
			return fmt.Errorf("create delivery operations: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_delivery_operations_claim
			ON delivery_operations(status, next_attempt_at, lease_expires_at, sequence)`)
		return err
	}},
	{version: 33, name: "create_runtime_submissions", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS runtime_submissions (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			scope TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL DEFAULT '',
			intent_id TEXT NOT NULL REFERENCES session_execution_intents(id) ON DELETE CASCADE,
			run_id TEXT NOT NULL REFERENCES session_runs(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			UNIQUE(session_id, scope, key_hash)
		)`); err != nil {
			return fmt.Errorf("create runtime submissions: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_runtime_submissions_run
			ON runtime_submissions(run_id)`); err != nil {
			return fmt.Errorf("index runtime submissions by Run: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_runtime_submissions_intent
			ON runtime_submissions(intent_id)`)
		return err
	}},
	{version: 32, name: "create_input_resources", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS input_resources (
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
		)`); err != nil {
			return fmt.Errorf("create input resources: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_input_resources_session
			ON input_resources(session_id, created_at)`); err != nil {
			return fmt.Errorf("index input resources: %w", err)
		}
		_, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_input_resources_item_key
			ON input_resources(session_id, item_key) WHERE item_key <> ''`)
		return err
	}},
	{version: 31, name: "create_session_run_recoveries", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_run_recoveries (
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
		)`); err != nil {
			return fmt.Errorf("create session run recoveries: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_run_recoveries_due
			ON session_run_recoveries(state, next_retry_at)`)
		return err
	}},
	{version: 30, name: "create_session_attachments_and_deliveries", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_attachments (
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
		)`); err != nil {
			return fmt.Errorf("create session attachments: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_attachments_session
			ON session_attachments(session_id, created_at)`); err != nil {
			return fmt.Errorf("index session attachments: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS attachment_deliveries (
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
		)`); err != nil {
			return fmt.Errorf("create attachment deliveries: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_attachment_deliveries_attachment
			ON attachment_deliveries(attachment_id, updated_at)`)
		return err
	}},
	{version: 29, name: "add_session_fork_and_runtime_lease_state", apply: func(tx *sql.Tx) error {
		if exists, err := tableExists(tx, "sessions"); err != nil {
			return err
		} else if exists {
			for _, column := range []struct {
				name       string
				definition string
			}{
				{name: "fork_boundary_seq", definition: "INTEGER NOT NULL DEFAULT 0"},
				{name: "seed_length", definition: "INTEGER NOT NULL DEFAULT 0"},
				{name: "fork_kind", definition: "TEXT NOT NULL DEFAULT ''"},
			} {
				if err := addColumnIfMissing(tx, "sessions", column.name, column.definition); err != nil {
					return err
				}
			}
		}
		if exists, err := tableExists(tx, "sub_session"); err != nil {
			return err
		} else if exists {
			for _, column := range []string{"fork_boundary_seq", "seed_length", "fork_kind"} {
				definition := "TEXT NOT NULL DEFAULT ''"
				if column != "fork_kind" {
					definition = "INTEGER NOT NULL DEFAULT 0"
				}
				if err := addColumnIfMissing(tx, "sub_session", column, definition); err != nil {
					return err
				}
			}
		}
		if exists, err := tableExists(tx, "session_capabilities"); err != nil {
			return err
		} else if exists {
			if err := addColumnIfMissing(tx, "session_capabilities", "display_mode", "TEXT NOT NULL DEFAULT 'work'"); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS conversation_turns (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			intent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'conversation',
			status TEXT NOT NULL,
			start_seq INTEGER NOT NULL,
			end_seq INTEGER,
			started_at TEXT NOT NULL,
			ended_at TEXT
		)`); err != nil {
			return fmt.Errorf("create conversation turns: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_conversation_turns_session ON conversation_turns(session_id, start_seq)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_conversation_turns_open ON conversation_turns(session_id, status)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_fork_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_key_hash TEXT NOT NULL,
			request_fingerprint TEXT NOT NULL,
			source_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			child_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			created_at TEXT NOT NULL,
			UNIQUE(request_key_hash, source_session_id)
		)`); err != nil {
			return fmt.Errorf("create session fork requests: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_runtime_leases (
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
		)`); err != nil {
			return fmt.Errorf("create session runtime leases: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_runtime_leases_expiry ON session_runtime_leases(expires_at)`)
		return err
	}},
	{version: 28, name: "add_run_context_usage", apply: func(tx *sql.Tx) error {
		exists, err := tableExists(tx, "session_runs")
		if err != nil || !exists {
			return err
		}
		return addColumnIfMissing(tx, "session_runs", "context_usage_json", "TEXT NOT NULL DEFAULT '{}'")
	}},
	{version: 27, name: "add_run_retry_metadata", apply: func(tx *sql.Tx) error {
		exists, err := tableExists(tx, "session_runs")
		if err != nil {
			return err
		}
		if !exists {
			// Defensive fallback for legacy installations that predate the
			// run-table migration and may not have session_runs yet. Migrations
			// now apply in ascending version order, so migration 18 normally
			// creates the table first; this keeps migration 27 self-contained
			// either way.
			if _, err := tx.Exec(`CREATE TABLE session_runs (
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
			)`); err != nil {
				return fmt.Errorf("create session runs table: %w", err)
			}
		}
		for _, column := range []struct {
			name       string
			definition string
		}{
			{name: "intent_id", definition: "TEXT NOT NULL DEFAULT ''"},
			{name: "retry_of", definition: "TEXT NOT NULL DEFAULT ''"},
			{name: "attempt", definition: "INTEGER NOT NULL DEFAULT 1"},
			{name: "error_info_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
			{name: "progress_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
			{name: "context_usage_json", definition: "TEXT NOT NULL DEFAULT '{}'"},
		} {
			if err := addColumnIfMissing(tx, "session_runs", column.name, column.definition); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_runs_intent ON session_runs(session_id, intent_id, attempt)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_session_runs_active_session`); err != nil {
			return fmt.Errorf("drop legacy active session run index: %w", err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX idx_session_runs_active_session ON session_runs(session_id) WHERE status IN (%s)`, nonTerminalSessionRunStatusSQL())); err != nil {
			return fmt.Errorf("create active session run index: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_execution_intents (
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
		)`); err != nil {
			return fmt.Errorf("create session execution intents: %w", err)
		}
		_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_execution_intents_session_id ON session_execution_intents(session_id, created_at)`)
		return err
	}},
	{version: 26, name: "add_session_display_mode", apply: func(tx *sql.Tx) error {
		exists, err := tableExists(tx, "session_capabilities")
		if err != nil || !exists {
			return err
		}
		return addColumnIfMissing(tx, "session_capabilities", "display_mode", "TEXT NOT NULL DEFAULT 'work'")
	}},
	{version: 25, name: "create_projects_and_session_metadata", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
			return fmt.Errorf("create projects table: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_metadata (
			session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
			project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
						pinned INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL
		)`); err != nil {
			return fmt.Errorf("create session metadata table: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_metadata_project ON session_metadata(project_id, pinned, updated_at)`)
		return err
	}},
	{version: 24, name: "index_entries_session_type", apply: func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_entries_session_type ON entries(session_id, type)`)
		return err
	}},
	{version: 23, name: "create_esm_guidance", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_esm_guidance (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			objective_version TEXT NOT NULL DEFAULT '',
			guidance TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL,
			consumed_at TEXT
		)`); err != nil {
			return fmt.Errorf("create ESM guidance table: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_esm_guidance_session_status ON session_esm_guidance(session_id, status, created_at)`)
		return err
	}},
	{version: 16, name: "add_channel_binding_columns", apply: func(tx *sql.Tx) error {
		for _, table := range []string{"sessions", "sub_session"} {
			exists, err := tableExists(tx, table)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			if err := addColumnIfMissing(tx, table, "channel_type", "TEXT NOT NULL DEFAULT 'local'"); err != nil {
				return err
			}
			if err := addColumnIfMissing(tx, table, "channel_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_wechat_binding ON sessions(channel_type, channel_id) WHERE channel_type = 'wechat' AND channel_id <> ''`); err != nil {
			return err
		}
		_, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_feishu_binding ON sessions(channel_type, channel_id) WHERE channel_type = 'feishu' AND channel_id <> ''`)
		return err
	}},
	{version: 17, name: "create_session_channel_tools", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_channel_tools (
				session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
				tool_name TEXT NOT NULL,
				enabled INTEGER NOT NULL DEFAULT 1,
				updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (session_id, tool_name)
			)`); err != nil {
			return fmt.Errorf("create session channel tools table: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_channel_tools_session_id ON session_channel_tools(session_id)`)
		return err
	}},
	{version: 18, name: "create_session_runs", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS session_runs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			work_dir TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			finished_at TEXT,
			error TEXT NOT NULL DEFAULT '',
			usage_json TEXT NOT NULL DEFAULT '{}'
		)`); err != nil {
			return fmt.Errorf("create session runs table: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_runs_session_id ON session_runs(session_id)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_session_runs_status ON session_runs(status)`); err != nil {
			return err
		}
		_, err := tx.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_runs_active_session ON session_runs(session_id) WHERE status IN (%s)`, nonTerminalSessionRunStatusSQL()))
		return err
	}},
	{version: 19, name: "create_response_runtime_tables", apply: func(tx *sql.Tx) error {
		if err := createResponseRuntimeTables(tx); err != nil {
			return err
		}
		return nil
	}},
	{version: 20, name: "harden_response_runtime_identity", apply: func(tx *sql.Tx) error {
		if err := addColumnIfMissing(tx, "response_items", "item_key", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		if err := addColumnIfMissing(tx, "response_items", "updated_at", "DATETIME"); err != nil {
			return err
		}
		if err := addColumnIfMissing(tx, "response_runs", "local_turn_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		if err := addColumnIfMissing(tx, "response_runs", "message_id", "INTEGER"); err != nil {
			return err
		}

		// The pre-runtime implementation only appended items. Keep the most
		// recent record for each identity so a done event supersedes its added
		// event before the unique index is introduced.
		if _, err := tx.Exec(`UPDATE response_items
			SET item_key = CASE
				WHEN item_id IS NOT NULL AND item_id <> '' THEN item_id || ':' || output_index
				ELSE 'output:' || output_index
			END,
			updated_at = COALESCE(updated_at, created_at)`); err != nil {
			return fmt.Errorf("backfill response item identity: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM response_items
			WHERE id NOT IN (
				SELECT MAX(id) FROM response_items
				GROUP BY session_id, local_turn_id, item_key
			)`); err != nil {
			return fmt.Errorf("deduplicate response items: %w", err)
		}
		if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_response_items_identity
			ON response_items(session_id, local_turn_id, item_key)`); err != nil {
			return fmt.Errorf("create response item identity index: %w", err)
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS response_session_state (
			session_id TEXT PRIMARY KEY,
			state_mode TEXT NOT NULL DEFAULT 'replay',
			previous_response_id TEXT,
			conversation_id TEXT,
			provider TEXT NOT NULL DEFAULT '',
			api TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL
		)`); err != nil {
			return fmt.Errorf("create response session state: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_response_runs_session_turn
			ON response_runs(session_id, local_turn_id)`)
		return err
	}},
	{version: 21, name: "cleanup_orphan_channel_tools", apply: func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM session_channel_tools
			WHERE session_id NOT IN (SELECT id FROM sessions)`)
		if err != nil {
			return fmt.Errorf("cleanup orphan channel tools: %w", err)
		}
		return nil
	}},
	{version: 22, name: "create_channel_tool_generations", apply: func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE session_channel_tool_generations (
				session_id TEXT PRIMARY KEY,
				generation INTEGER NOT NULL DEFAULT 0,
				updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`); err != nil {
			return fmt.Errorf("create channel tool generations: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM session_channel_tool_generations
			WHERE session_id NOT IN (SELECT id FROM sessions)`); err != nil {
			return fmt.Errorf("cleanup orphan channel tool generations: %w", err)
		}
		_, err := tx.Exec(`CREATE INDEX idx_channel_tool_generations_updated_at
			ON session_channel_tool_generations(updated_at)`)
		return err
	}},
	// Versions 36-40 were used by the historical numbered migration records
	// present in released session databases (for example
	// 001_create_sessions_table). Never reuse that range: a matching version
	// makes applySchemaMigrations correctly treat the migration as already
	// applied, even when its name and schema change are unrelated.
	{version: 41, name: "add_sessions_expert_id", apply: func(tx *sql.Tx) error {
		if exists, err := tableExists(tx, "sessions"); err != nil {
			return err
		} else if exists {
			if err := addColumnIfMissing(tx, "sessions", "expert_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
		if exists, err := tableExists(tx, "sub_session"); err != nil {
			return err
		} else if exists {
			if err := addColumnIfMissing(tx, "sub_session", "expert_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
		return nil
	}},
	// Version 42 is retained for databases that already recorded the former
	// session-database knowledge graph migration. New session databases keep
	// this no-op marker only; each knowledge base now owns a separate SQLite
	// file and its own schema below.
	{version: 42, name: "create_desktop_knowledge_base_graph", apply: func(*sql.Tx) error { return nil }},
	// A delayed or rate-limited platform may exhaust one retry window; an
	// explicit retry (operator action or reconnect recovery) restarts the budget
	// from this timestamp instead of resurrecting the original creation time.
	{version: 43, name: "add_delivery_operations_retry_window_started_at", apply: func(tx *sql.Tx) error {
		exists, err := tableExists(tx, "delivery_operations")
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return addColumnIfMissing(tx, "delivery_operations", "retry_window_started_at", "TEXT")
	}},
}

func createResponseRuntimeTables(tx *sql.Tx) error {
	statements := []struct {
		name string
		sql  string
	}{
		{"response_turns", `CREATE TABLE IF NOT EXISTS response_turns (
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
		)`},
		{"idx_response_turns_session_id", `CREATE INDEX IF NOT EXISTS idx_response_turns_session_id ON response_turns(session_id)`},
		{"idx_response_turns_response_id", `CREATE INDEX IF NOT EXISTS idx_response_turns_response_id ON response_turns(response_id)`},
		{"response_items", `CREATE TABLE IF NOT EXISTS response_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			local_turn_id TEXT NOT NULL,
			response_id TEXT,
			item_id TEXT,
			output_index INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			item_status TEXT,
			sanitized_json BLOB NOT NULL,
			created_at DATETIME NOT NULL
		)`},
		{"idx_response_items_session_turn", `CREATE INDEX IF NOT EXISTS idx_response_items_session_turn ON response_items(session_id, local_turn_id)`},
		{"idx_response_items_response_id", `CREATE INDEX IF NOT EXISTS idx_response_items_response_id ON response_items(response_id)`},
		{"tool_execution_records", `CREATE TABLE IF NOT EXISTS tool_execution_records (
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
		)`},
		{"idx_tool_execution_records_session_turn", `CREATE INDEX IF NOT EXISTS idx_tool_execution_records_session_turn ON tool_execution_records(session_id, local_turn_id)`},
		{"idx_tool_execution_records_provider_call", `CREATE INDEX IF NOT EXISTS idx_tool_execution_records_provider_call ON tool_execution_records(provider, api, provider_call_id)`},
		{"response_runs", `CREATE TABLE IF NOT EXISTS response_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			local_run_id TEXT NOT NULL,
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
		)`},
		{"idx_response_runs_session_id", `CREATE INDEX IF NOT EXISTS idx_response_runs_session_id ON response_runs(session_id)`},
		{"idx_response_runs_state", `CREATE INDEX IF NOT EXISTS idx_response_runs_state ON response_runs(state)`},
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt.sql); err != nil {
			return fmt.Errorf("create %s: %w", stmt.name, err)
		}
	}
	return nil
}

func tableExists(tx *sql.Tx, table string) (bool, error) {
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count); err != nil {
		return false, fmt.Errorf("check table %s: %w", table, err)
	}
	return count != 0, nil
}

func addColumnIfMissing(tx *sql.Tx, table, column, definition string) error {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

type schemaDatabase interface {
	Begin() (*sql.Tx, error)
}

func ensureSchemaMigrationsTable(db schemaDatabase) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		columns, err := tableColumns(tx, "schema_migrations")
		if err != nil {
			return err
		}
		if columns["name"] && columns["applied_at"] && !columns["version"] {
			if _, err := tx.Exec(`ALTER TABLE schema_migrations ADD COLUMN version INTEGER`); err != nil {
				return err
			}
		} else if !columns["version"] || !columns["name"] || !columns["applied_at"] {
			legacy := "schema_migrations_legacy_" + strconv.FormatInt(time.Now().UnixNano(), 10)
			if _, err := tx.Exec("ALTER TABLE schema_migrations RENAME TO " + legacy); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	return tx.Commit()
}

func tableColumns(tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var def any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &def, &pk); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func applySchemaMigrations(db schemaDatabase) error {
	if err := ensureSchemaMigrationsTable(db); err != nil {
		return err
	}
	// Apply migrations in ascending version order. Newer migrations reference
	// tables created by older ones (delivery_operations -> session_attachments,
	// runtime_submissions -> session_execution_intents), so applying the slice
	// in its historical newest-first layout would break once SQLite foreign key
	// enforcement is enabled.
	migrations := make([]schemaMigration, len(schemaMigrations))
	copy(migrations, schemaMigrations)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for _, migration := range migrations {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin schema migration %d: %w", migration.version, err)
		}
		var byVersion, byName int
		if err := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", migration.version).Scan(&byVersion); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE name = ?", migration.name).Scan(&byName); err != nil {
			tx.Rollback()
			return err
		}
		if byVersion != 0 || byName != 0 {
			if byVersion == 0 {
				if _, err := tx.Exec("UPDATE schema_migrations SET version = ? WHERE name = ? AND version IS NULL", migration.version, migration.name); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			continue
		}
		if err := migration.apply(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply schema migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, CURRENT_TIMESTAMP)", migration.version, migration.name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record schema migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// knowledgeStoreSchema is deliberately separate from currentSchema: a
// knowledge base can grow to millions of chunks, so every base gets its own
// SQLite file rather than sharing sessions.db. Keep future changes here as
// explicit per-knowledge-store migrations.
const knowledgeStoreSchema = `
CREATE TABLE knowledge_bases (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	root_dir TEXT NOT NULL,
	preprocess_profile TEXT NOT NULL,
	provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	mode TEXT NOT NULL DEFAULT 'yolo',
	thinking_level TEXT NOT NULL DEFAULT '',
	schedule TEXT NOT NULL DEFAULT 'manual',
	enabled INTEGER NOT NULL DEFAULT 1,
	active_snapshot_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_knowledge_bases_updated ON knowledge_bases(updated_at, name);
CREATE TABLE knowledge_index_snapshots (
	id TEXT PRIMARY KEY,
	knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
	run_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	schema_version INTEGER NOT NULL,
	file_count INTEGER NOT NULL DEFAULT 0,
	chunk_count INTEGER NOT NULL DEFAULT 0,
	node_count INTEGER NOT NULL DEFAULT 0,
	edge_count INTEGER NOT NULL DEFAULT 0,
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL DEFAULT '',
	error_summary TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_knowledge_snapshots_base ON knowledge_index_snapshots(knowledge_base_id, finished_at);
CREATE TABLE knowledge_files (
	id TEXT PRIMARY KEY,
	snapshot_id TEXT NOT NULL REFERENCES knowledge_index_snapshots(id) ON DELETE CASCADE,
	relative_path TEXT NOT NULL,
	content_sha256 TEXT NOT NULL,
	byte_size INTEGER NOT NULL,
	media_type TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'indexed',
	UNIQUE(snapshot_id, relative_path)
);
CREATE TABLE knowledge_chunks (
	id TEXT PRIMARY KEY,
	snapshot_id TEXT NOT NULL REFERENCES knowledge_index_snapshots(id) ON DELETE CASCADE,
	file_id TEXT NOT NULL REFERENCES knowledge_files(id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL,
	text TEXT NOT NULL,
	start_line INTEGER NOT NULL,
	end_line INTEGER NOT NULL,
	content_sha256 TEXT NOT NULL,
	UNIQUE(file_id, ordinal)
);
CREATE INDEX idx_knowledge_chunks_snapshot ON knowledge_chunks(snapshot_id, file_id, ordinal);
CREATE VIRTUAL TABLE knowledge_chunk_fts USING fts5(chunk_id UNINDEXED, snapshot_id UNINDEXED, text);
CREATE TABLE knowledge_nodes (
	id TEXT PRIMARY KEY,
	snapshot_id TEXT NOT NULL REFERENCES knowledge_index_snapshots(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	label TEXT NOT NULL,
	normalized_label TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	attributes TEXT NOT NULL DEFAULT '{}',
	UNIQUE(snapshot_id, kind, normalized_label)
);
CREATE INDEX idx_knowledge_nodes_label ON knowledge_nodes(snapshot_id, normalized_label, kind);
CREATE TABLE knowledge_edges (
	id TEXT PRIMARY KEY,
	snapshot_id TEXT NOT NULL REFERENCES knowledge_index_snapshots(id) ON DELETE CASCADE,
	from_node_id TEXT NOT NULL REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
	to_node_id TEXT NOT NULL REFERENCES knowledge_nodes(id) ON DELETE CASCADE,
	relation_type TEXT NOT NULL,
	confidence REAL NOT NULL DEFAULT 1,
	UNIQUE(snapshot_id, from_node_id, to_node_id, relation_type)
);
CREATE INDEX idx_knowledge_edges_from ON knowledge_edges(snapshot_id, from_node_id, relation_type);
CREATE INDEX idx_knowledge_edges_to ON knowledge_edges(snapshot_id, to_node_id, relation_type);
CREATE TABLE knowledge_evidence (
	id TEXT PRIMARY KEY,
	snapshot_id TEXT NOT NULL REFERENCES knowledge_index_snapshots(id) ON DELETE CASCADE,
	node_id TEXT NOT NULL DEFAULT '',
	edge_id TEXT NOT NULL DEFAULT '',
	chunk_id TEXT NOT NULL REFERENCES knowledge_chunks(id) ON DELETE CASCADE,
	start_line INTEGER NOT NULL,
	end_line INTEGER NOT NULL,
	confidence REAL NOT NULL DEFAULT 1,
	CHECK(node_id <> '' OR edge_id <> '')
);
CREATE INDEX idx_knowledge_evidence_chunk ON knowledge_evidence(snapshot_id, chunk_id, node_id, edge_id);
`

const knowledgeStoreSchemaVersion = 1

// EnsureKnowledgeBaseSchema migrates one dedicated knowledge-base SQLite
// database. It intentionally does not invoke EnsureCurrentSchema, which owns
// the unrelated session/run database schema.
func EnsureKnowledgeBaseSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin knowledge store schema: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS knowledge_store_schema (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create knowledge store schema table: %w", err)
	}
	var version int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM knowledge_store_schema`).Scan(&version); err != nil {
		return fmt.Errorf("read knowledge store schema version: %w", err)
	}
	if version > knowledgeStoreSchemaVersion {
		return fmt.Errorf("knowledge store schema version %d is newer than supported version %d", version, knowledgeStoreSchemaVersion)
	}
	if version < 1 {
		if _, err := tx.Exec(knowledgeStoreSchema); err != nil {
			return fmt.Errorf("create knowledge store schema: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO knowledge_store_schema(version) VALUES (?)`, knowledgeStoreSchemaVersion); err != nil {
			return fmt.Errorf("record knowledge store schema version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge store schema: %w", err)
	}
	return nil
}
