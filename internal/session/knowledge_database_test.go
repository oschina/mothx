package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

func TestKnowledgeBaseUsesDedicatedSQLiteDatabase(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	base, err := CreateKnowledgeBase(t.Context(), sessionDir, KnowledgeBaseSpec{
		Name: "Dedicated", RootDir: rootDir, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := KnowledgeBaseDatabasePath(sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("dedicated knowledge database = %q, info=%#v, err=%v", path, info, err)
	}
	assertNoKnowledgeTablesInSessionDatabase(t, sessionDir)

	now := time.Now().UTC()
	graph := KnowledgeGraphSnapshot{
		BaseConfigRevision: base.ConfigRevision,
		Snapshot:           KnowledgeSnapshot{ID: "snapshot", KnowledgeBaseID: base.ID, Status: "indexing", StartedAt: now},
		Files:              []KnowledgeFile{{ID: "file", SnapshotID: "snapshot", RelativePath: "architecture.md", ContentSHA256: "hash", ByteSize: 12, Status: "indexed"}},
		Chunks:             []KnowledgeChunk{{ID: "chunk", SnapshotID: "snapshot", FileID: "file", Ordinal: 0, Text: "Alpha owns the runtime.", StartLine: 1, EndLine: 1, ContentSHA256: "hash"}},
		Nodes:              []KnowledgeNode{{ID: "node", SnapshotID: "snapshot", Kind: "section", Label: "Alpha", NormalizedLabel: "alpha"}},
		Evidence:           []KnowledgeEvidence{{ID: "evidence", SnapshotID: "snapshot", NodeID: "node", ChunkID: "chunk", StartLine: 1, EndLine: 1, Confidence: 1}},
	}
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, graph); err != nil {
		t.Fatal(err)
	}
	query, err := QueryKnowledgeGraph(t.Context(), sessionDir, base.ID, "Alpha", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Chunks) != 1 || len(query.Nodes) != 1 {
		t.Fatalf("dedicated graph query = %#v", query)
	}
	assertNoKnowledgeTablesInSessionDatabase(t, sessionDir)

	if err := DeleteKnowledgeBase(t.Context(), sessionDir, base.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("knowledge database remains after delete: %v", err)
	}
}

func TestKnowledgeSnapshotRetentionKeepsOnlyActiveGraph(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	base, err := CreateKnowledgeBase(t.Context(), sessionDir, KnowledgeBaseSpec{
		Name: "Retention", RootDir: rootDir, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, knowledgeGraphForRetention(base.ID, base.ConfigRevision, "first", "First indexed fact.")); err != nil {
		t.Fatal(err)
	}
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, knowledgeGraphForRetention(base.ID, base.ConfigRevision, "second", "Second indexed fact.")); err != nil {
		t.Fatal(err)
	}

	if _, err := GetKnowledgeSnapshot(t.Context(), sessionDir, "first"); !errors.Is(err, ErrKnowledgeBaseUnindexed) {
		t.Fatalf("retired snapshot error = %v, want %v", err, ErrKnowledgeBaseUnindexed)
	}
	query, err := QueryKnowledgeGraph(t.Context(), sessionDir, base.ID, "Second", 4)
	if err != nil {
		t.Fatal(err)
	}
	if query.Snapshot.ID != "second" || len(query.Chunks) != 1 || query.Chunks[0].SnapshotID != "second" {
		t.Fatalf("retained graph query = %#v", query)
	}
	assertKnowledgeGraphRowCounts(t, sessionDir, base.ID, 1, 1, 1, 1)
}

func TestKnowledgeBaseUpdateInvalidatesAndPrunesGraph(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	base, err := CreateKnowledgeBase(t.Context(), sessionDir, KnowledgeBaseSpec{
		Name: "Reconfigure", RootDir: rootDir, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, knowledgeGraphForRetention(base.ID, base.ConfigRevision, "before-update", "Configuration-sensitive fact.")); err != nil {
		t.Fatal(err)
	}
	inFlight := knowledgeGraphForRetention(base.ID, base.ConfigRevision, "in-flight", "Stale in-flight fact.")
	updatedSpec := base.KnowledgeBaseSpec
	updatedSpec.Name = "Reconfigured"
	updated, err := UpdateKnowledgeBase(t.Context(), sessionDir, base.ID, updatedSpec)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveSnapshotID != "" {
		t.Fatalf("updated base retained active snapshot %q", updated.ActiveSnapshotID)
	}
	if _, err := QueryKnowledgeGraph(t.Context(), sessionDir, base.ID, "Configuration", 4); !errors.Is(err, ErrKnowledgeBaseUnindexed) {
		t.Fatalf("query after configuration update error = %v, want %v", err, ErrKnowledgeBaseUnindexed)
	}
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, inFlight); !errors.Is(err, ErrKnowledgeBaseConfigurationChanged) {
		t.Fatalf("stale in-flight snapshot error = %v, want %v", err, ErrKnowledgeBaseConfigurationChanged)
	}
	if current, err := GetKnowledgeBase(t.Context(), sessionDir, base.ID); err != nil {
		t.Fatal(err)
	} else if current.ActiveSnapshotID != "" || current.ConfigRevision != updated.ConfigRevision {
		t.Fatalf("stale snapshot changed base = %#v", current)
	}
	assertKnowledgeGraphRowCounts(t, sessionDir, base.ID, 0, 0, 0, 0)
}

func TestKnowledgeBaseMigratesLegacySessionStoreIntoDedicatedDatabase(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	base := KnowledgeBase{
		ID: "legacybase", KnowledgeBaseSpec: KnowledgeBaseSpec{
			Name: "Legacy", RootDir: rootDir, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
		},
		ActiveSnapshotID: "legacy-snapshot", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	root, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Bun().Exec(knowledgeStoreSchema); err != nil {
		t.Fatalf("create legacy shared knowledge tables: %v", err)
	}
	// The shared-store layout that predates the per-base database may already
	// carry the current additive columns; simulate that so the migration test
	// exercises the row move rather than an outdated column set.
	for _, stmt := range []string{
		`ALTER TABLE knowledge_bases ADD COLUMN ignore_globs TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE knowledge_index_snapshots ADD COLUMN diff_summary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE knowledge_index_snapshots ADD COLUMN discovery_summary TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE knowledge_nodes ADD COLUMN status TEXT NOT NULL DEFAULT 'fact'`,
		`ALTER TABLE knowledge_nodes ADD COLUMN confidence REAL NOT NULL DEFAULT 1`,
	} {
		if _, err := root.Bun().Exec(stmt); err != nil {
			t.Fatalf("extend legacy shared knowledge tables: %v", err)
		}
	}
	if err := WriteRootDatabase(t.Context(), sessionDir, func(tx *dao.Tx) error {
		store := dao.NewKnowledgeBaseDAO(nil)
		if err := store.InsertLegacyBase(t.Context(), tx, knowledgeBaseRecord(base)); err != nil {
			return err
		}
		retired := KnowledgeSnapshot{ID: "legacy-retired", KnowledgeBaseID: base.ID, Status: "completed", SchemaVersion: KnowledgeGraphSchemaVersion,
			StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
		if err := store.InsertSnapshot(t.Context(), tx, knowledgeSnapshotRecord(retired)); err != nil {
			return err
		}
		snapshot := KnowledgeSnapshot{ID: "legacy-snapshot", KnowledgeBaseID: base.ID, Status: "completed", SchemaVersion: KnowledgeGraphSchemaVersion,
			FileCount: 1, ChunkCount: 1, NodeCount: 1, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
		if err := store.InsertSnapshot(t.Context(), tx, knowledgeSnapshotRecord(snapshot)); err != nil {
			return err
		}
		if err := store.InsertFiles(t.Context(), tx, knowledgeFileRecords([]KnowledgeFile{{ID: "legacy-file", SnapshotID: snapshot.ID, RelativePath: "legacy.md", ContentSHA256: "hash", ByteSize: 10, Status: "indexed"}})); err != nil {
			return err
		}
		if err := store.InsertChunks(t.Context(), tx, knowledgeChunkRecords([]KnowledgeChunk{{ID: "legacy-chunk", SnapshotID: snapshot.ID, FileID: "legacy-file", Ordinal: 0, Text: "Legacy Alpha evidence.", StartLine: 1, EndLine: 1, ContentSHA256: "hash"}})); err != nil {
			return err
		}
		if err := store.InsertNodes(t.Context(), tx, knowledgeNodeRecords([]KnowledgeNode{{ID: "legacy-node", SnapshotID: snapshot.ID, Kind: "section", Label: "Alpha", NormalizedLabel: "alpha"}})); err != nil {
			return err
		}
		return store.InsertEvidence(t.Context(), tx, knowledgeEvidenceRecords([]KnowledgeEvidence{{ID: "legacy-evidence", SnapshotID: snapshot.ID, NodeID: "legacy-node", ChunkID: "legacy-chunk", StartLine: 1, EndLine: 1, Confidence: 1}}))
	}); err != nil {
		t.Fatal(err)
	}

	bases, err := ListKnowledgeBases(t.Context(), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(bases) != 1 || bases[0].ID != base.ID {
		t.Fatalf("migrated bases = %#v", bases)
	}
	query, err := QueryKnowledgeGraph(t.Context(), sessionDir, base.ID, "Alpha", 4)
	if err != nil || len(query.Chunks) != 1 || query.Snapshot.ID != "legacy-snapshot" {
		t.Fatalf("migrated query = %#v, err=%v", query, err)
	}
	if _, err := GetKnowledgeSnapshot(t.Context(), sessionDir, "legacy-retired"); !errors.Is(err, ErrKnowledgeBaseUnindexed) {
		t.Fatalf("retired migrated snapshot error = %v, want %v", err, ErrKnowledgeBaseUnindexed)
	}
	assertKnowledgeGraphRowCounts(t, sessionDir, base.ID, 1, 1, 1, 1)
	path, err := KnowledgeBaseDatabasePath(sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("migrated dedicated database %q: %v", path, err)
	}
	var remaining int
	if err := root.Bun().QueryRow(`SELECT COUNT(*) FROM knowledge_bases`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("legacy session rows remain = %d, err=%v", remaining, err)
	}
}

func TestKnowledgeGraphReusePlanClonesOnlyUnchangedFileSubgraph(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	base, err := CreateKnowledgeBase(t.Context(), sessionDir, KnowledgeBaseSpec{
		Name: "Incremental", RootDir: rootDir, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stable := KnowledgeFile{ID: "stable-file", SnapshotID: "source", RelativePath: "stable.md", ContentSHA256: "stable-hash", ByteSize: 10, MediaType: "text/markdown", Status: "indexed"}
	changed := KnowledgeFile{ID: "changed-file", SnapshotID: "source", RelativePath: "changed.md", ContentSHA256: "old-hash", ByteSize: 10, MediaType: "text/markdown", Status: "indexed"}
	graph := KnowledgeGraphSnapshot{
		BaseConfigRevision: base.ConfigRevision,
		Snapshot:           KnowledgeSnapshot{ID: "source", KnowledgeBaseID: base.ID, Status: "indexing", StartedAt: now},
		Files:              []KnowledgeFile{stable, changed},
		Chunks: []KnowledgeChunk{
			{ID: "stable-chunk", SnapshotID: "source", FileID: stable.ID, Ordinal: 0, Text: "stable evidence", StartLine: 1, EndLine: 1, ContentSHA256: "stable-chunk-hash"},
			{ID: "changed-chunk", SnapshotID: "source", FileID: changed.ID, Ordinal: 0, Text: "old evidence", StartLine: 1, EndLine: 1, ContentSHA256: "changed-chunk-hash"},
		},
		Nodes: []KnowledgeNode{
			{ID: "stable-file-node", SnapshotID: "source", Kind: "file", Label: "stable.md", NormalizedLabel: "stable.md"},
			{ID: "stable-section", SnapshotID: "source", Kind: "section", Label: "Stable", NormalizedLabel: "stable\x00stable"},
			{ID: "changed-node", SnapshotID: "source", Kind: "file", Label: "changed.md", NormalizedLabel: "changed.md"},
		},
		Edges: []KnowledgeEdge{{ID: "stable-edge", SnapshotID: "source", FromNodeID: "stable-file-node", ToNodeID: "stable-section", RelationType: "contains", Confidence: 1}},
		Evidence: []KnowledgeEvidence{
			{ID: "stable-file-evidence", SnapshotID: "source", NodeID: "stable-file-node", ChunkID: "stable-chunk", StartLine: 1, EndLine: 1, Confidence: 1},
			{ID: "stable-section-evidence", SnapshotID: "source", NodeID: "stable-section", ChunkID: "stable-chunk", StartLine: 1, EndLine: 1, Confidence: 1},
			{ID: "stable-edge-evidence", SnapshotID: "source", EdgeID: "stable-edge", ChunkID: "stable-chunk", StartLine: 1, EndLine: 1, Confidence: 1},
			{ID: "changed-evidence", SnapshotID: "source", NodeID: "changed-node", ChunkID: "changed-chunk", StartLine: 1, EndLine: 1, Confidence: 1},
		},
	}
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, graph); err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareKnowledgeGraphReusePlan(t.Context(), sessionDir, base.ID, []KnowledgeFile{
		{RelativePath: stable.RelativePath, ContentSHA256: stable.ContentSHA256, ByteSize: stable.ByteSize, MediaType: stable.MediaType, Status: stable.Status},
		{RelativePath: changed.RelativePath, ContentSHA256: "new-hash", ByteSize: changed.ByteSize, MediaType: changed.MediaType, Status: changed.Status},
	})
	if err != nil {
		t.Fatal(err)
	}
	reused, ok := plan.Files[stable.RelativePath]
	if !ok || len(plan.Files) != 1 || len(reused.Chunks) != 1 || len(reused.Nodes) != 2 || len(reused.Edges) != 1 || len(reused.Evidence) != 3 {
		t.Fatalf("reuse plan = %#v", plan)
	}
	target := KnowledgeGraphSnapshot{Snapshot: KnowledgeSnapshot{ID: "target", KnowledgeBaseID: base.ID}}
	AppendKnowledgeFileGraph(&target, reused)
	if len(target.Files) != 1 || len(target.Chunks) != 1 || len(target.Nodes) != 2 || len(target.Edges) != 1 || len(target.Evidence) != 3 {
		t.Fatalf("cloned graph = %#v", target)
	}
	if target.Files[0].ID == stable.ID || target.Chunks[0].ID == "stable-chunk" || target.Nodes[0].SnapshotID != "target" || target.Edges[0].SnapshotID != "target" || target.Evidence[0].SnapshotID != "target" {
		t.Fatalf("cloned graph reused source identities: %#v", target)
	}
}

func assertNoKnowledgeTablesInSessionDatabase(t *testing.T, sessionDir string) {
	t.Helper()
	root, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := root.Bun().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'knowledge_%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("sessions.db unexpectedly contains %d knowledge tables (%s)", count, filepath.Join(sessionDir, "sessions.db"))
	}
}

func knowledgeGraphForRetention(baseID string, revision int64, snapshotID, text string) KnowledgeGraphSnapshot {
	now := time.Now().UTC()
	fileID := "file-" + snapshotID
	chunkID := "chunk-" + snapshotID
	nodeID := "node-" + snapshotID
	return KnowledgeGraphSnapshot{
		BaseConfigRevision: revision,
		Snapshot:           KnowledgeSnapshot{ID: snapshotID, KnowledgeBaseID: baseID, Status: "indexing", StartedAt: now},
		Files:              []KnowledgeFile{{ID: fileID, SnapshotID: snapshotID, RelativePath: snapshotID + ".md", ContentSHA256: "hash-" + snapshotID, ByteSize: int64(len(text)), Status: "indexed"}},
		Chunks:             []KnowledgeChunk{{ID: chunkID, SnapshotID: snapshotID, FileID: fileID, Ordinal: 0, Text: text, StartLine: 1, EndLine: 1, ContentSHA256: "chunk-hash-" + snapshotID}},
		Nodes:              []KnowledgeNode{{ID: nodeID, SnapshotID: snapshotID, Kind: "section", Label: snapshotID, NormalizedLabel: snapshotID}},
		Evidence:           []KnowledgeEvidence{{ID: "evidence-" + snapshotID, SnapshotID: snapshotID, NodeID: nodeID, ChunkID: chunkID, StartLine: 1, EndLine: 1, Confidence: 1}},
	}
}

func assertKnowledgeGraphRowCounts(t *testing.T, sessionDir, baseID string, snapshots, chunks, fts, evidence int) {
	t.Helper()
	var gotSnapshots, gotChunks, gotFTS, gotEvidence int
	err := queryKnowledgeBaseDatabase(sessionDir, baseID, func(db *dao.Database) error {
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM knowledge_index_snapshots`).Scan(&gotSnapshots); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM knowledge_chunks`).Scan(&gotChunks); err != nil {
			return err
		}
		if err := db.Bun().QueryRow(`SELECT COUNT(*) FROM knowledge_chunk_fts`).Scan(&gotFTS); err != nil {
			return err
		}
		return db.Bun().QueryRow(`SELECT COUNT(*) FROM knowledge_evidence`).Scan(&gotEvidence)
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSnapshots != snapshots || gotChunks != chunks || gotFTS != fts || gotEvidence != evidence {
		t.Fatalf("knowledge graph row counts = snapshots:%d chunks:%d fts:%d evidence:%d, want snapshots:%d chunks:%d fts:%d evidence:%d", gotSnapshots, gotChunks, gotFTS, gotEvidence, snapshots, chunks, fts, evidence)
	}
}

// TestClearKnowledgeBaseIndexPreservesConfigurationAndSource pins that clearing
// removes the active snapshot and every graph row while keeping the base
// configuration and the source directory untouched.
func TestClearKnowledgeBaseIndexPreservesConfigurationAndSource(t *testing.T) {
	sessionDir := t.TempDir()
	rootDir := t.TempDir()
	base, err := CreateKnowledgeBase(t.Context(), sessionDir, KnowledgeBaseSpec{
		Name: "Clearable", RootDir: rootDir, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
		IgnoreGlobs: []string{"*.tmp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph := knowledgeGraphForRetention(base.ID, base.ConfigRevision, "clear-snap", "Alpha evidence body.")
	if _, err := StoreKnowledgeGraphSnapshot(t.Context(), sessionDir, graph); err != nil {
		t.Fatal(err)
	}
	sources, err := ListKnowledgeSources(t.Context(), sessionDir, base.ID)
	if err != nil || len(sources) != 1 || sources[0].RelativePath != "clear-snap.md" {
		t.Fatalf("sources before clear = %#v, err=%v", sources, err)
	}
	if err := ClearKnowledgeBaseIndex(t.Context(), sessionDir, base.ID); err != nil {
		t.Fatal(err)
	}
	after, err := ListKnowledgeSources(t.Context(), sessionDir, base.ID)
	if err != nil || len(after) != 0 {
		t.Fatalf("sources after clear = %#v, err=%v", after, err)
	}
	current, err := GetKnowledgeBase(t.Context(), sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ActiveSnapshotID != "" || len(current.IgnoreGlobs) != 1 || current.IgnoreGlobs[0] != "*.tmp" {
		t.Fatalf("clear changed configuration: %#v", current)
	}
	if _, err := QueryKnowledgeGraph(t.Context(), sessionDir, base.ID, "Alpha", 4); !errors.Is(err, ErrKnowledgeBaseUnindexed) {
		t.Fatalf("query after clear error = %v, want ErrKnowledgeBaseUnindexed", err)
	}
	assertKnowledgeGraphRowCounts(t, sessionDir, base.ID, 0, 0, 0, 0)
	if _, err := os.Stat(rootDir); err != nil {
		t.Fatalf("clear must preserve the source directory: %v", err)
	}
}
