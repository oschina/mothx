package dao

import (
	"context"
	"strings"

	"github.com/uptrace/bun"
)

// KnowledgeBaseRecord is the durable Desktop-managed configuration for one
// directory-backed knowledge base. The directory remains the authority for
// source material; this record only owns configuration and the active graph
// snapshot identity.
type KnowledgeBaseRecord struct {
	bun.BaseModel     `bun:"table:knowledge_bases"`
	ID                string `bun:"id,pk"`
	Name              string `bun:"name"`
	RootDir           string `bun:"root_dir"`
	PreprocessProfile string `bun:"preprocess_profile"`
	Provider          string `bun:"provider"`
	Model             string `bun:"model"`
	Mode              string `bun:"mode"`
	ThinkingLevel     string `bun:"thinking_level"`
	Schedule          string `bun:"schedule"`
	Enabled           int    `bun:"enabled"`
	IgnoreGlobs       string `bun:"ignore_globs"`
	ActiveSnapshotID  string `bun:"active_snapshot_id"`
	ConfigRevision    int64  `bun:"config_revision"`
	CreatedAt         string `bun:"created_at"`
	UpdatedAt         string `bun:"updated_at"`
}

// KnowledgeSnapshotRecord describes one immutable, fully materialized graph
// snapshot. Its RunID is a link to the canonical Runtime run once an
// agent-backed index execution is wired; it is deliberately not a second run
// state machine.
type KnowledgeSnapshotRecord struct {
	bun.BaseModel    `bun:"table:knowledge_index_snapshots"`
	ID               string `bun:"id,pk"`
	KnowledgeBaseID  string `bun:"knowledge_base_id"`
	RunID            string `bun:"run_id"`
	Status           string `bun:"status"`
	SchemaVersion    int    `bun:"schema_version"`
	FileCount        int    `bun:"file_count"`
	ChunkCount       int    `bun:"chunk_count"`
	NodeCount        int    `bun:"node_count"`
	EdgeCount        int    `bun:"edge_count"`
	StartedAt        string `bun:"started_at"`
	FinishedAt       string `bun:"finished_at"`
	ErrorSummary     string `bun:"error_summary"`
	DiffSummary      string `bun:"diff_summary"`
	DiscoverySummary string `bun:"discovery_summary"`
}

type KnowledgeFileRecord struct {
	bun.BaseModel `bun:"table:knowledge_files"`
	ID            string `bun:"id,pk"`
	SnapshotID    string `bun:"snapshot_id"`
	RelativePath  string `bun:"relative_path"`
	ContentSHA256 string `bun:"content_sha256"`
	ByteSize      int64  `bun:"byte_size"`
	MediaType     string `bun:"media_type"`
	Title         string `bun:"title"`
	Status        string `bun:"status"`
}

type KnowledgeChunkRecord struct {
	bun.BaseModel `bun:"table:knowledge_chunks"`
	ID            string `bun:"id,pk"`
	SnapshotID    string `bun:"snapshot_id"`
	FileID        string `bun:"file_id"`
	RelativePath  string `bun:"relative_path,scanonly"`
	Ordinal       int    `bun:"ordinal"`
	Text          string `bun:"text"`
	StartLine     int    `bun:"start_line"`
	EndLine       int    `bun:"end_line"`
	ContentSHA256 string `bun:"content_sha256"`
}

type KnowledgeNodeRecord struct {
	bun.BaseModel   `bun:"table:knowledge_nodes"`
	ID              string  `bun:"id,pk"`
	SnapshotID      string  `bun:"snapshot_id"`
	Kind            string  `bun:"kind"`
	Label           string  `bun:"label"`
	NormalizedLabel string  `bun:"normalized_label"`
	Summary         string  `bun:"summary"`
	Attributes      string  `bun:"attributes"`
	Status          string  `bun:"status"`
	Confidence      float64 `bun:"confidence"`
}

// KnowledgeEntityAliasRecord maps one normalized synonym label onto the single
// entity node that owns it inside a snapshot.
type KnowledgeEntityAliasRecord struct {
	bun.BaseModel   `bun:"table:knowledge_entity_aliases"`
	ID              string `bun:"id,pk"`
	SnapshotID      string `bun:"snapshot_id"`
	NormalizedAlias string `bun:"normalized_alias"`
	NodeID          string `bun:"node_id"`
}

type KnowledgeEdgeRecord struct {
	bun.BaseModel `bun:"table:knowledge_edges"`
	ID            string  `bun:"id,pk"`
	SnapshotID    string  `bun:"snapshot_id"`
	FromNodeID    string  `bun:"from_node_id"`
	ToNodeID      string  `bun:"to_node_id"`
	RelationType  string  `bun:"relation_type"`
	Confidence    float64 `bun:"confidence"`
}

type KnowledgeEvidenceRecord struct {
	bun.BaseModel `bun:"table:knowledge_evidence"`
	ID            string  `bun:"id,pk"`
	SnapshotID    string  `bun:"snapshot_id"`
	NodeID        string  `bun:"node_id"`
	EdgeID        string  `bun:"edge_id"`
	ChunkID       string  `bun:"chunk_id"`
	StartLine     int     `bun:"start_line"`
	EndLine       int     `bun:"end_line"`
	Confidence    float64 `bun:"confidence"`
}

// KnowledgeGraphProjection is the bounded graph data needed to answer one
// local knowledge query. All of its rows are read from the same SQLite
// transaction so a caller never combines an old active snapshot ID with rows
// that a successor publication has already pruned.
type KnowledgeGraphProjection struct {
	Base      KnowledgeBaseRecord
	Snapshot  KnowledgeSnapshotRecord
	Chunks    []KnowledgeChunkRecord
	Nodes     []KnowledgeNodeRecord
	Edges     []KnowledgeEdgeRecord
	Evidence  []KnowledgeEvidenceRecord
	Truncated bool
}

// Bounded two-hop traversal limits. They cap a query result so a broad match
// cannot pull the whole graph into memory or a model's context.
const (
	KnowledgeGraphMaxHops  = 2
	KnowledgeGraphMaxNodes = 64
	KnowledgeGraphMaxEdges = 96
)

// KnowledgeBaseDAO is the only owner of SQL/Bun for the Desktop knowledge
// base configuration, graph snapshots and their read projections.
type KnowledgeBaseDAO struct{ db *bun.DB }

func NewKnowledgeBaseDAO(db *bun.DB) *KnowledgeBaseDAO { return &KnowledgeBaseDAO{db: db} }

func (d *KnowledgeBaseDAO) ListBases(ctx context.Context) ([]KnowledgeBaseRecord, error) {
	var records []KnowledgeBaseRecord
	err := d.db.NewSelect().Model(&records).OrderExpr("updated_at DESC, name COLLATE NOCASE").Scan(ctx)
	return records, err
}

// ListLegacyBases reads the former shared-store shape, which predates the
// per-database configuration revision column.
func (d *KnowledgeBaseDAO) ListLegacyBases(ctx context.Context) ([]KnowledgeBaseRecord, error) {
	var records []KnowledgeBaseRecord
	err := d.db.NewSelect().Model(&records).
		Column("id", "name", "root_dir", "preprocess_profile", "provider", "model", "mode", "thinking_level", "schedule", "enabled", "active_snapshot_id", "created_at", "updated_at").
		OrderExpr("updated_at DESC, name COLLATE NOCASE").Scan(ctx)
	for i := range records {
		records[i].ConfigRevision = 1
	}
	return records, err
}

func (d *KnowledgeBaseDAO) FindBase(ctx context.Context, id string) (*KnowledgeBaseRecord, error) {
	return d.FindBaseWith(ctx, d.db, id)
}

func (d *KnowledgeBaseDAO) FindBaseWith(ctx context.Context, executor bun.IDB, id string) (*KnowledgeBaseRecord, error) {
	record := new(KnowledgeBaseRecord)
	err := executor.NewSelect().Model(record).Where("id = ?", id).Limit(1).Scan(ctx)
	return record, err
}

// HasStorage reports whether this database is a legacy session database that
// still contains the former shared knowledge-base tables. It is used only by
// the one-time Runtime-owned storage migration.
func (d *KnowledgeBaseDAO) HasStorage(ctx context.Context) (bool, error) {
	var count int
	err := d.db.NewRaw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'knowledge_bases'`).Scan(ctx, &count)
	return count != 0, err
}

func (d *KnowledgeBaseDAO) InsertBase(ctx context.Context, executor bun.IDB, record *KnowledgeBaseRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) InsertLegacyBase(ctx context.Context, executor bun.IDB, record *KnowledgeBaseRecord) error {
	_, err := executor.NewInsert().Model(record).
		Column("id", "name", "root_dir", "preprocess_profile", "provider", "model", "mode", "thinking_level", "schedule", "enabled", "active_snapshot_id", "created_at", "updated_at").
		Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) UpdateBase(ctx context.Context, executor bun.IDB, record *KnowledgeBaseRecord, expectedRevision int64) (int64, error) {
	result, err := executor.NewUpdate().Model((*KnowledgeBaseRecord)(nil)).
		Set("name = ?", record.Name).
		Set("root_dir = ?", record.RootDir).
		Set("preprocess_profile = ?", record.PreprocessProfile).
		Set("provider = ?", record.Provider).
		Set("model = ?", record.Model).
		Set("mode = ?", record.Mode).
		Set("thinking_level = ?", record.ThinkingLevel).
		Set("schedule = ?", record.Schedule).
		Set("enabled = ?", record.Enabled).
		Set("ignore_globs = ?", record.IgnoreGlobs).
		Set("active_snapshot_id = ?", record.ActiveSnapshotID).
		Set("updated_at = ?", record.UpdatedAt).
		Set("config_revision = ?", record.ConfigRevision).
		Where("id = ? AND config_revision = ?", record.ID, expectedRevision).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *KnowledgeBaseDAO) DeleteBase(ctx context.Context, executor bun.IDB, id string) (int64, error) {
	// FTS rows do not participate in SQLite foreign-key cascades. Remove them
	// while the base/snapshot IDs are still visible, then let relational
	// cascades delete the graph records.
	if _, err := executor.NewRaw(`DELETE FROM knowledge_chunk_fts
		WHERE snapshot_id IN (SELECT id FROM knowledge_index_snapshots WHERE knowledge_base_id = ?)`, id).Exec(ctx); err != nil {
		return 0, err
	}
	result, err := executor.NewDelete().Model((*KnowledgeBaseRecord)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *KnowledgeBaseDAO) InsertSnapshot(ctx context.Context, executor bun.IDB, record *KnowledgeSnapshotRecord) error {
	_, err := executor.NewInsert().Model(record).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) InsertFiles(ctx context.Context, executor bun.IDB, records []KnowledgeFileRecord) error {
	if len(records) == 0 {
		return nil
	}
	_, err := executor.NewInsert().Model(&records).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) InsertChunks(ctx context.Context, executor bun.IDB, records []KnowledgeChunkRecord) error {
	if len(records) == 0 {
		return nil
	}
	if _, err := executor.NewInsert().Model(&records).Exec(ctx); err != nil {
		return err
	}
	for _, record := range records {
		if _, err := executor.NewRaw(`INSERT INTO knowledge_chunk_fts(chunk_id, snapshot_id, text) VALUES (?, ?, ?)`, record.ID, record.SnapshotID, KnowledgeFTSIndexText(record.Text)).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (d *KnowledgeBaseDAO) InsertNodes(ctx context.Context, executor bun.IDB, records []KnowledgeNodeRecord) error {
	if len(records) == 0 {
		return nil
	}
	_, err := executor.NewInsert().Model(&records).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) InsertEdges(ctx context.Context, executor bun.IDB, records []KnowledgeEdgeRecord) error {
	if len(records) == 0 {
		return nil
	}
	_, err := executor.NewInsert().Model(&records).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) InsertEvidence(ctx context.Context, executor bun.IDB, records []KnowledgeEvidenceRecord) error {
	if len(records) == 0 {
		return nil
	}
	_, err := executor.NewInsert().Model(&records).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) InsertAliases(ctx context.Context, executor bun.IDB, records []KnowledgeEntityAliasRecord) error {
	if len(records) == 0 {
		return nil
	}
	_, err := executor.NewInsert().Model(&records).Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) ActivateSnapshot(ctx context.Context, executor bun.IDB, baseID, snapshotID, updatedAt string, expectedRevision int64) (int64, error) {
	result, err := executor.NewUpdate().Model((*KnowledgeBaseRecord)(nil)).
		Set("active_snapshot_id = ?", snapshotID).Set("updated_at = ?", updatedAt).
		Where("id = ? AND config_revision = ?", baseID, expectedRevision).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PruneSnapshotsExcept removes every non-retained snapshot for one knowledge
// base. FTS rows require an explicit delete because the virtual table does not
// participate in SQLite foreign-key cascades; deleting the snapshot records
// then cascades to files, chunks, nodes, edges, and evidence.
//
// An empty retainedSnapshotID clears every snapshot. Callers must invoke this
// in the same transaction that publishes or invalidates active_snapshot_id so
// readers observe either the prior complete graph or the new retained state.
func (d *KnowledgeBaseDAO) PruneSnapshotsExcept(ctx context.Context, executor bun.IDB, baseID, retainedSnapshotID string) error {
	ftsQuery := `DELETE FROM knowledge_chunk_fts
		WHERE snapshot_id IN (SELECT id FROM knowledge_index_snapshots WHERE knowledge_base_id = ?)`
	if retainedSnapshotID != "" {
		ftsQuery += ` AND snapshot_id <> ?`
	}
	args := []any{baseID}
	if retainedSnapshotID != "" {
		args = append(args, retainedSnapshotID)
	}
	if _, err := executor.NewRaw(ftsQuery, args...).Exec(ctx); err != nil {
		return err
	}

	snapshotQuery := executor.NewDelete().Model((*KnowledgeSnapshotRecord)(nil)).Where("knowledge_base_id = ?", baseID)
	if retainedSnapshotID != "" {
		snapshotQuery = snapshotQuery.Where("id <> ?", retainedSnapshotID)
	}
	_, err := snapshotQuery.Exec(ctx)
	return err
}

func (d *KnowledgeBaseDAO) FindSnapshot(ctx context.Context, id string) (*KnowledgeSnapshotRecord, error) {
	record := new(KnowledgeSnapshotRecord)
	err := d.db.NewSelect().Model(record).Where("id = ?", id).Limit(1).Scan(ctx)
	return record, err
}

// The following list methods are only used when importing pre-separation
// knowledge data from sessions.db into its per-knowledge-base SQLite file.
// Keeping the row reads in DAO preserves the database access boundary.
func (d *KnowledgeBaseDAO) ListSnapshotsForBase(ctx context.Context, baseID string) ([]KnowledgeSnapshotRecord, error) {
	var records []KnowledgeSnapshotRecord
	err := d.db.NewSelect().Model(&records).Where("knowledge_base_id = ?", baseID).OrderExpr("started_at, id").Scan(ctx)
	return records, err
}

func (d *KnowledgeBaseDAO) ListFilesForSnapshot(ctx context.Context, snapshotID string) ([]KnowledgeFileRecord, error) {
	var records []KnowledgeFileRecord
	err := d.db.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID).OrderExpr("relative_path, id").Scan(ctx)
	return records, err
}

func (d *KnowledgeBaseDAO) ListChunksForSnapshot(ctx context.Context, snapshotID string) ([]KnowledgeChunkRecord, error) {
	var records []KnowledgeChunkRecord
	err := d.db.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID).OrderExpr("file_id, ordinal").Scan(ctx)
	return records, err
}

// CountChunksPerFile returns how many chunks each file contributed to one
// snapshot. It backs the bounded source-provenance projection without loading
// chunk text, so a large directory cannot bloat a sources listing.
func (d *KnowledgeBaseDAO) CountChunksPerFile(ctx context.Context, snapshotID string) (map[string]int, error) {
	var rows []struct {
		FileID string `bun:"file_id"`
		Total  int    `bun:"total"`
	}
	err := d.db.NewSelect().Table("knowledge_chunks").
		Column("file_id").ColumnExpr("COUNT(*) AS total").
		Where("snapshot_id = ?", snapshotID).
		Group("file_id").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.FileID] = row.Total
	}
	return counts, nil
}

func (d *KnowledgeBaseDAO) ListNodesForSnapshot(ctx context.Context, snapshotID string) ([]KnowledgeNodeRecord, error) {
	var records []KnowledgeNodeRecord
	err := d.db.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID).OrderExpr("kind, normalized_label, id").Scan(ctx)
	return records, err
}

func (d *KnowledgeBaseDAO) ListEdgesForSnapshot(ctx context.Context, snapshotID string) ([]KnowledgeEdgeRecord, error) {
	var records []KnowledgeEdgeRecord
	err := d.db.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID).OrderExpr("relation_type, id").Scan(ctx)
	return records, err
}

func (d *KnowledgeBaseDAO) ListEvidenceForSnapshot(ctx context.Context, snapshotID string) ([]KnowledgeEvidenceRecord, error) {
	var records []KnowledgeEvidenceRecord
	err := d.db.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID).OrderExpr("id").Scan(ctx)
	return records, err
}

func (d *KnowledgeBaseDAO) ListAliasesForSnapshot(ctx context.Context, snapshotID string) ([]KnowledgeEntityAliasRecord, error) {
	var records []KnowledgeEntityAliasRecord
	err := d.db.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID).OrderExpr("normalized_alias, id").Scan(ctx)
	return records, err
}

// ListRunsForBase projects recent index attempts from the canonical snapshot
// rows, newest first. It backs the management run-history surface without a
// second run store.
func (d *KnowledgeBaseDAO) ListRunsForBase(ctx context.Context, baseID string, limit int) ([]KnowledgeSnapshotRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	var records []KnowledgeSnapshotRecord
	err := d.db.NewSelect().Model(&records).Where("knowledge_base_id = ?", baseID).
		OrderExpr("started_at DESC, id DESC").Limit(limit).Scan(ctx)
	return records, err
}

// FindSnapshotForBase reads one snapshot only when it belongs to the base, so
// a management lookup cannot cross knowledge-base boundaries.
func (d *KnowledgeBaseDAO) FindSnapshotForBase(ctx context.Context, baseID, snapshotID string) (*KnowledgeSnapshotRecord, error) {
	record := new(KnowledgeSnapshotRecord)
	err := d.db.NewSelect().Model(record).
		Where("id = ? AND knowledge_base_id = ?", snapshotID, baseID).Limit(1).Scan(ctx)
	return record, err
}

// ClearActiveSnapshot deactivates the base's active snapshot under the same
// configuration-revision fence used by publication. It clears only the active
// pointer; the caller prunes graph rows in the same transaction so a reader
// never observes an active snapshot whose rows were already removed.
func (d *KnowledgeBaseDAO) ClearActiveSnapshot(ctx context.Context, executor bun.IDB, baseID, updatedAt string, expectedRevision int64) (int64, error) {
	result, err := executor.NewUpdate().Model((*KnowledgeBaseRecord)(nil)).
		Set("active_snapshot_id = ?", "").Set("updated_at = ?", updatedAt).
		Where("id = ? AND config_revision = ?", baseID, expectedRevision).Exec(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (d *KnowledgeBaseDAO) SearchChunks(ctx context.Context, snapshotID, query string, limit int) ([]KnowledgeChunkRecord, error) {
	if limit <= 0 {
		limit = 8
	}
	terms := knowledgeFTSQuery(query)
	if terms == "" {
		return []KnowledgeChunkRecord{}, nil
	}
	var records []KnowledgeChunkRecord
	err := d.db.NewRaw(`SELECT c.id, c.snapshot_id, c.file_id, kf.relative_path, c.ordinal, c.text, c.start_line, c.end_line, c.content_sha256
		FROM knowledge_chunk_fts AS f
		JOIN knowledge_chunks AS c ON c.id = f.chunk_id
		JOIN knowledge_files AS kf ON kf.id = c.file_id
		WHERE f.snapshot_id = ? AND knowledge_chunk_fts MATCH ?
		ORDER BY bm25(knowledge_chunk_fts), c.ordinal ASC
		LIMIT ?`, snapshotID, terms, limit).Scan(ctx, &records)
	return records, err
}

func (d *KnowledgeBaseDAO) NodesForChunks(ctx context.Context, executor bun.IDB, snapshotID string, chunkIDs []string) ([]KnowledgeNodeRecord, error) {
	if len(chunkIDs) == 0 {
		return []KnowledgeNodeRecord{}, nil
	}
	var records []KnowledgeNodeRecord
	err := executor.NewSelect().Model(&records).
		Where("knowledge_node_record.snapshot_id = ?", snapshotID).
		Where("knowledge_node_record.id IN (SELECT e.node_id FROM knowledge_evidence AS e WHERE e.chunk_id IN (?))", bun.In(chunkIDs)).
		OrderExpr("knowledge_node_record.kind, knowledge_node_record.label COLLATE NOCASE").
		Scan(ctx)
	return records, err
}

func (d *KnowledgeBaseDAO) EdgesForNodes(ctx context.Context, executor bun.IDB, snapshotID string, nodeIDs []string) ([]KnowledgeEdgeRecord, error) {
	if len(nodeIDs) == 0 {
		return []KnowledgeEdgeRecord{}, nil
	}
	var records []KnowledgeEdgeRecord
	err := executor.NewSelect().Model(&records).
		Where("snapshot_id = ?", snapshotID).
		Where("from_node_id IN (?) OR to_node_id IN (?)", bun.In(nodeIDs), bun.In(nodeIDs)).
		OrderExpr("relation_type, id").Scan(ctx)
	return records, err
}

// ActiveGraphProjection reads the active completed graph for a base through
// one caller-owned transaction. indexed is false when the base exists but has
// no completed active snapshot; dao.ErrNoRows means the base does not exist.
func (d *KnowledgeBaseDAO) ActiveGraphProjection(ctx context.Context, executor bun.IDB, baseID, query string, limit int) (projection KnowledgeGraphProjection, indexed bool, err error) {
	base := new(KnowledgeBaseRecord)
	if err := executor.NewSelect().Model(base).Where("id = ?", baseID).Limit(1).Scan(ctx); err != nil {
		return KnowledgeGraphProjection{}, false, err
	}
	if strings.TrimSpace(base.ActiveSnapshotID) == "" {
		return KnowledgeGraphProjection{Base: *base}, false, nil
	}

	snapshot := new(KnowledgeSnapshotRecord)
	if err := executor.NewSelect().Model(snapshot).Where("id = ?", base.ActiveSnapshotID).Limit(1).Scan(ctx); err != nil {
		if err == ErrNoRows {
			return KnowledgeGraphProjection{Base: *base}, false, nil
		}
		return KnowledgeGraphProjection{}, false, err
	}
	if snapshot.Status != "completed" {
		return KnowledgeGraphProjection{Base: *base}, false, nil
	}
	projection.Base, projection.Snapshot = *base, *snapshot

	if limit <= 0 {
		limit = 8
	}
	terms := knowledgeFTSQuery(query)
	if terms == "" {
		return projection, true, nil
	}
	if err := executor.NewRaw(`SELECT c.id, c.snapshot_id, c.file_id, kf.relative_path, c.ordinal, c.text, c.start_line, c.end_line, c.content_sha256
		FROM knowledge_chunk_fts AS f
		JOIN knowledge_chunks AS c ON c.id = f.chunk_id
		JOIN knowledge_files AS kf ON kf.id = c.file_id
		WHERE f.snapshot_id = ? AND knowledge_chunk_fts MATCH ?
		ORDER BY bm25(knowledge_chunk_fts), c.ordinal ASC
		LIMIT ?`, snapshot.ID, terms, limit).Scan(ctx, &projection.Chunks); err != nil {
		return KnowledgeGraphProjection{}, false, err
	}
	chunkIDs := make([]string, 0, len(projection.Chunks))
	for _, chunk := range projection.Chunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}
	var seedNodes []KnowledgeNodeRecord
	if len(chunkIDs) > 0 {
		seedNodes, err = d.NodesForChunks(ctx, executor, snapshot.ID, chunkIDs)
		if err != nil {
			return KnowledgeGraphProjection{}, false, err
		}
	}
	// Entity aliases widen recall: a query that names a file by its basename
	// should seed that file node even when the FTS chunks did not surface it.
	aliasIDs, err := d.AliasNodeIDs(ctx, executor, snapshot.ID, knowledgeAliasTerms(query))
	if err != nil {
		return KnowledgeGraphProjection{}, false, err
	}
	if len(aliasIDs) > 0 {
		seenSeed := make(map[string]struct{}, len(seedNodes))
		for _, node := range seedNodes {
			seenSeed[node.ID] = struct{}{}
		}
		missing := make([]string, 0, len(aliasIDs))
		for _, id := range aliasIDs {
			if _, ok := seenSeed[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			aliasNodes, err := d.NodesByIDs(ctx, executor, snapshot.ID, missing)
			if err != nil {
				return KnowledgeGraphProjection{}, false, err
			}
			seedNodes = append(seedNodes, aliasNodes...)
		}
	}
	if len(seedNodes) == 0 {
		return projection, true, nil
	}
	projection.Nodes = append(projection.Nodes, seedNodes...)
	known := make(map[string]struct{}, len(seedNodes))
	frontier := make([]string, 0, len(seedNodes))
	for _, node := range seedNodes {
		known[node.ID] = struct{}{}
		frontier = append(frontier, node.ID)
	}
	// Bounded breadth-first traversal: from the FTS seed nodes follow at most
	// KnowledgeGraphMaxHops hops of adjacency, stopping once the node or edge cap
	// is reached. The frontier shrinks to the newly discovered nodes each hop.
	knownEdges := make(map[string]struct{})
	for hop := 0; hop < KnowledgeGraphMaxHops && len(frontier) > 0; hop++ {
		edges, err := d.EdgesForNodes(ctx, executor, snapshot.ID, frontier)
		if err != nil {
			return KnowledgeGraphProjection{}, false, err
		}
		nextFrontier := make([]string, 0)
		for _, edge := range edges {
			if _, ok := knownEdges[edge.ID]; ok {
				continue
			}
			if len(projection.Edges) >= KnowledgeGraphMaxEdges {
				projection.Truncated = true
				break
			}
			projection.Edges = append(projection.Edges, edge)
			knownEdges[edge.ID] = struct{}{}
			for _, endpoint := range []string{edge.FromNodeID, edge.ToNodeID} {
				if _, ok := known[endpoint]; ok {
					continue
				}
				known[endpoint] = struct{}{}
				nextFrontier = append(nextFrontier, endpoint)
			}
		}
		if len(nextFrontier) == 0 {
			break
		}
		if len(projection.Nodes) >= KnowledgeGraphMaxNodes {
			projection.Truncated = true
			break
		}
		remaining := KnowledgeGraphMaxNodes - len(projection.Nodes)
		if len(nextFrontier) > remaining {
			nextFrontier = nextFrontier[:remaining]
			projection.Truncated = true
		}
		neighbors, err := d.NodesByIDs(ctx, executor, snapshot.ID, nextFrontier)
		if err != nil {
			return KnowledgeGraphProjection{}, false, err
		}
		projection.Nodes = append(projection.Nodes, neighbors...)
		frontier = nextFrontier
	}
	nodeIDs := make([]string, 0, len(projection.Nodes))
	for _, node := range projection.Nodes {
		nodeIDs = append(nodeIDs, node.ID)
	}
	edgeIDs := make([]string, 0, len(projection.Edges))
	for _, edge := range projection.Edges {
		edgeIDs = append(edgeIDs, edge.ID)
	}
	evidence, err := d.EvidenceForNodesAndEdges(ctx, executor, snapshot.ID, nodeIDs, edgeIDs)
	if err != nil {
		return KnowledgeGraphProjection{}, false, err
	}
	projection.Evidence = evidence
	return projection, true, nil
}

// AliasNodeIDs resolves query terms that exactly match an entity alias to the
// node that owns the alias inside one snapshot.
func (d *KnowledgeBaseDAO) AliasNodeIDs(ctx context.Context, executor bun.IDB, snapshotID string, terms []string) ([]string, error) {
	if len(terms) == 0 {
		return nil, nil
	}
	var ids []string
	err := executor.NewSelect().Model((*KnowledgeEntityAliasRecord)(nil)).
		Column("node_id").
		Where("snapshot_id = ?", snapshotID).
		Where("normalized_alias IN (?)", bun.In(terms)).
		Scan(ctx, &ids)
	return ids, err
}

func knowledgeAliasTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || knowledgeIsFTSCJK(r))
	})
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			terms = append(terms, field)
		}
	}
	return terms
}

// NodesByIDs loads the nodes named by ids inside one snapshot. The caller
// bounds len(ids).
func (d *KnowledgeBaseDAO) NodesByIDs(ctx context.Context, executor bun.IDB, snapshotID string, ids []string) ([]KnowledgeNodeRecord, error) {
	if len(ids) == 0 {
		return []KnowledgeNodeRecord{}, nil
	}
	var records []KnowledgeNodeRecord
	err := executor.NewSelect().Model(&records).
		Where("snapshot_id = ?", snapshotID).
		Where("id IN (?)", bun.In(ids)).
		OrderExpr("kind, label COLLATE NOCASE").Scan(ctx)
	return records, err
}

// EvidenceForNodesAndEdges returns the minimal evidence rows that cite any of
// the given nodes or edges. It never reads chunk text.
func (d *KnowledgeBaseDAO) EvidenceForNodesAndEdges(ctx context.Context, executor bun.IDB, snapshotID string, nodeIDs, edgeIDs []string) ([]KnowledgeEvidenceRecord, error) {
	if len(nodeIDs) == 0 && len(edgeIDs) == 0 {
		return []KnowledgeEvidenceRecord{}, nil
	}
	var records []KnowledgeEvidenceRecord
	query := executor.NewSelect().Model(&records).Where("snapshot_id = ?", snapshotID)
	switch {
	case len(nodeIDs) > 0 && len(edgeIDs) > 0:
		query = query.Where("(node_id IN (?) OR edge_id IN (?))", bun.In(nodeIDs), bun.In(edgeIDs))
	case len(nodeIDs) > 0:
		query = query.Where("node_id IN (?)", bun.In(nodeIDs))
	default:
		query = query.Where("edge_id IN (?)", bun.In(edgeIDs))
	}
	err := query.OrderExpr("chunk_id, id").Scan(ctx)
	return records, err
}

func knowledgeFTSQuery(query string) string {
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || knowledgeIsFTSCJK(r))
	})
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(strings.ReplaceAll(term, `"`, ``))
		// Punctuation-only terms would produce an empty FTS5 phrase and a
		// MATCH syntax error, so they are dropped instead of quoted.
		if term == "" || !knowledgeFTSHasTokenRune(term) {
			continue
		}
		// The index-side bigram rewrite turns CJK terms into adjacent token
		// phrases that the unicode61 tokenizer can actually match.
		quoted = append(quoted, `"`+strings.TrimSpace(KnowledgeFTSIndexText(term))+`"`)
	}
	return strings.Join(quoted, " OR ")
}

// knowledgeIsFTSCJK reports whether r is in the CJK range preserved by the
// knowledge FTS query tokenizer. Only this range receives bigram splitting on
// both the index and query side so the two stay symmetric.
func knowledgeIsFTSCJK(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff
}

// knowledgeFTSHasTokenRune reports whether a query term contains at least one
// rune the unicode61 tokenizer can index.
func knowledgeFTSHasTokenRune(term string) bool {
	for _, r := range term {
		switch {
		case r == '_', r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', knowledgeIsFTSCJK(r):
			return true
		}
	}
	return false
}

// KnowledgeFTSIndexText rewrites chunk text for the knowledge_chunk_fts mirror
// column. The default unicode61 tokenizer treats a whole CJK run as one token,
// so Chinese phrase queries would never match substrings of that token. Every
// CJK run is therefore split into overlapping bigrams (an isolated single
// character is kept as-is) and padded with spaces so it never fuses with an
// adjacent non-CJK token. The canonical chunk text in knowledge_chunks stays
// untouched; only the FTS mirror carries this form, and knowledgeFTSQuery
// applies the same rewrite to each query term.
func KnowledgeFTSIndexText(text string) string {
	if !strings.ContainsFunc(text, knowledgeIsFTSCJK) {
		return text
	}
	runes := []rune(text)
	var builder strings.Builder
	builder.Grow(len(text) * 2)
	for i := 0; i < len(runes); {
		if !knowledgeIsFTSCJK(runes[i]) {
			builder.WriteRune(runes[i])
			i++
			continue
		}
		start := i
		for i < len(runes) && knowledgeIsFTSCJK(runes[i]) {
			i++
		}
		run := runes[start:i]
		builder.WriteByte(' ')
		if len(run) == 1 {
			builder.WriteRune(run[0])
		} else {
			for j := 0; j+1 < len(run); j++ {
				if j > 0 {
					builder.WriteByte(' ')
				}
				builder.WriteRune(run[j])
				builder.WriteRune(run[j+1])
			}
		}
		builder.WriteByte(' ')
	}
	return builder.String()
}
