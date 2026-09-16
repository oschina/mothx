package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestKnowledgeStoreMigratesLegacyFTSToBigramIndex proves the v1 -> v2
// knowledge-store migration: an existing store whose knowledge_chunk_fts
// mirror holds raw (unicode61-uncut) CJK text must be reindexed in place so
// Chinese phrase queries match afterwards, without touching graph rows or the
// active snapshot.
func TestKnowledgeStoreMigratesLegacyFTSToBigramIndex(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	baseID := "kb-legacy-fts"
	path, err := KnowledgeBaseDatabasePath(sessionDir, baseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	seed := func(db *sql.DB) {
		t.Helper()
		if _, err := db.Exec(`CREATE TABLE knowledge_store_schema (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(knowledgeStoreSchema); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO knowledge_store_schema(version) VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO knowledge_bases
			(id, name, root_dir, preprocess_profile, provider, model, mode, thinking_level, schedule, enabled, active_snapshot_id, created_at, updated_at)
			VALUES (?, 'Legacy', ?, 'documents', '', '', 'yolo', '', 'manual', 1, 'snap1', '2026-09-07T00:00:00Z', '2026-09-07T00:00:00Z')`, baseID, rootDir); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO knowledge_index_snapshots
			(id, knowledge_base_id, run_id, status, schema_version, file_count, chunk_count, node_count, edge_count, started_at, finished_at, error_summary)
			VALUES ('snap1', ?, '', 'completed', ?, 1, 1, 0, 0, '2026-09-07T00:00:00Z', '2026-09-07T00:00:01Z', '')`, baseID, KnowledgeGraphSchemaVersion); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO knowledge_files
			(id, snapshot_id, relative_path, content_sha256, byte_size, media_type, title, status)
			VALUES ('file1', 'snap1', '架构.md', 'hash', 12, 'text/markdown', '', 'indexed')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO knowledge_chunks
			(id, snapshot_id, file_id, ordinal, text, start_line, end_line, content_sha256)
			VALUES ('chunk1', 'snap1', 'file1', 0, '知识库是一个可重建的图谱索引系统', 1, 1, 'hash')`); err != nil {
			t.Fatal(err)
		}
		// v1 wrote the raw chunk text into the FTS mirror.
		if _, err := db.Exec(`INSERT INTO knowledge_chunk_fts(chunk_id, snapshot_id, text) VALUES ('chunk1', 'snap1', '知识库是一个可重建的图谱索引系统')`); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	seed(raw)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	// The first managed open runs EnsureKnowledgeBaseSchema and must migrate.
	query, err := QueryKnowledgeGraph(context.Background(), sessionDir, baseID, "图谱索引", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Chunks) != 1 || query.Chunks[0].ID != "chunk1" {
		t.Fatalf("query after migration = %#v, want the migrated chunk", query.Chunks)
	}
	if query.Chunks[0].Text != "知识库是一个可重建的图谱索引系统" {
		t.Fatalf("migration must not alter canonical chunk text: %q", query.Chunks[0].Text)
	}
	if query.Snapshot.ID != "snap1" || query.KnowledgeBase.ActiveSnapshotID != "snap1" {
		t.Fatalf("migration disturbed the active snapshot: %#v", query.Snapshot)
	}

	check, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var version int
	if err := check.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM knowledge_store_schema`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != knowledgeStoreSchemaVersion || version < 2 {
		t.Fatalf("store schema version = %d, want %d", version, knowledgeStoreSchemaVersion)
	}
	var ftsText string
	if err := check.QueryRow(`SELECT text FROM knowledge_chunk_fts WHERE chunk_id = 'chunk1'`).Scan(&ftsText); err != nil {
		t.Fatal(err)
	}
	if ftsText == "知识库是一个可重建的图谱索引系统" {
		t.Fatalf("FTS mirror still holds raw v1 text: %q", ftsText)
	}
}
