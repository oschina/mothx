package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/dao"
)

const KnowledgeGraphSchemaVersion = 1

var (
	ErrKnowledgeBaseNotFound             = errors.New("knowledge base not found")
	ErrKnowledgeBaseUnindexed            = errors.New("knowledge base has no completed index")
	ErrKnowledgeBaseConfigurationChanged = errors.New("knowledge base configuration changed during indexing")
)

// KnowledgeBaseSpec is the editable Desktop configuration. It deliberately
// stores provider/model/mode as references, not copied provider credentials.
type KnowledgeBaseSpec struct {
	Name              string `json:"name"`
	RootDir           string `json:"rootDir"`
	PreprocessProfile string `json:"preprocessProfile"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	Mode              string `json:"mode"`
	ThinkingLevel     string `json:"thinkingLevel,omitempty"`
	Schedule          string `json:"schedule"`
	Enabled           bool   `json:"enabled"`
}

type KnowledgeBase struct {
	ID string `json:"id"`
	KnowledgeBaseSpec
	ActiveSnapshotID string    `json:"activeSnapshotId,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	ConfigRevision   int64     `json:"-"`
}

type KnowledgeSnapshot struct {
	ID              string    `json:"id"`
	KnowledgeBaseID string    `json:"knowledgeBaseId"`
	RunID           string    `json:"runId,omitempty"`
	Status          string    `json:"status"`
	SchemaVersion   int       `json:"schemaVersion"`
	FileCount       int       `json:"fileCount"`
	ChunkCount      int       `json:"chunkCount"`
	NodeCount       int       `json:"nodeCount"`
	EdgeCount       int       `json:"edgeCount"`
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt,omitempty"`
	ErrorSummary    string    `json:"errorSummary,omitempty"`
}

type KnowledgeFile struct {
	ID            string `json:"id"`
	SnapshotID    string `json:"snapshotId"`
	RelativePath  string `json:"relativePath"`
	ContentSHA256 string `json:"contentSha256"`
	ByteSize      int64  `json:"byteSize"`
	MediaType     string `json:"mediaType"`
	Title         string `json:"title,omitempty"`
	Status        string `json:"status"`
}

type KnowledgeChunk struct {
	ID            string `json:"id"`
	SnapshotID    string `json:"snapshotId"`
	FileID        string `json:"fileId"`
	RelativePath  string `json:"relativePath,omitempty"`
	Ordinal       int    `json:"ordinal"`
	Text          string `json:"text"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	ContentSHA256 string `json:"contentSha256"`
}

type KnowledgeNode struct {
	ID              string `json:"id"`
	SnapshotID      string `json:"snapshotId"`
	Kind            string `json:"kind"`
	Label           string `json:"label"`
	NormalizedLabel string `json:"normalizedLabel"`
	Summary         string `json:"summary,omitempty"`
}

type KnowledgeEdge struct {
	ID           string  `json:"id"`
	SnapshotID   string  `json:"snapshotId"`
	FromNodeID   string  `json:"fromNodeId"`
	ToNodeID     string  `json:"toNodeId"`
	RelationType string  `json:"relationType"`
	Confidence   float64 `json:"confidence"`
}

type KnowledgeEvidence struct {
	ID         string  `json:"id"`
	SnapshotID string  `json:"snapshotId"`
	NodeID     string  `json:"nodeId,omitempty"`
	EdgeID     string  `json:"edgeId,omitempty"`
	ChunkID    string  `json:"chunkId"`
	StartLine  int     `json:"startLine"`
	EndLine    int     `json:"endLine"`
	Confidence float64 `json:"confidence"`
}

// KnowledgeGraphSnapshot is the immutable payload committed by a successful
// indexer. The session package atomically stores it and switches the active
// snapshot only after all graph rows are durable.
type KnowledgeGraphSnapshot struct {
	BaseConfigRevision int64
	Snapshot           KnowledgeSnapshot
	Files              []KnowledgeFile
	Chunks             []KnowledgeChunk
	Nodes              []KnowledgeNode
	Edges              []KnowledgeEdge
	Evidence           []KnowledgeEvidence
}

type KnowledgeGraphQuery struct {
	KnowledgeBase KnowledgeBase     `json:"knowledgeBase"`
	Snapshot      KnowledgeSnapshot `json:"snapshot"`
	Chunks        []KnowledgeChunk  `json:"chunks"`
	Nodes         []KnowledgeNode   `json:"nodes"`
	Edges         []KnowledgeEdge   `json:"edges"`
}

func CreateKnowledgeBase(ctx context.Context, sessionDir string, spec KnowledgeBaseSpec) (KnowledgeBase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeBase{}, err
	}
	if err := validateKnowledgeBaseSpec(&spec); err != nil {
		return KnowledgeBase{}, err
	}
	now := time.Now().UTC()
	base := KnowledgeBase{ID: GenerateID(), KnowledgeBaseSpec: spec, CreatedAt: now, UpdatedAt: now, ConfigRevision: 1}
	err := writeKnowledgeBaseDatabase(ctx, sessionDir, base.ID, true, func(tx *dao.Tx) error {
		return dao.NewKnowledgeBaseDAO(nil).InsertBase(ctx, tx, knowledgeBaseRecord(base))
	})
	if err != nil {
		return KnowledgeBase{}, err
	}
	return base, nil
}

func ListKnowledgeBases(ctx context.Context, sessionDir string) ([]KnowledgeBase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return nil, err
	}
	ids, err := listKnowledgeBaseDatabaseIDs(sessionDir)
	if err != nil {
		return nil, err
	}
	bases := make([]KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		var record *dao.KnowledgeBaseRecord
		err := queryKnowledgeBaseDatabase(sessionDir, id, func(db *dao.Database) error {
			var queryErr error
			record, queryErr = dao.NewKnowledgeBaseDAO(db.Bun()).FindBase(ctx, id)
			return queryErr
		})
		if errors.Is(err, dao.ErrNoRows) || errors.Is(err, ErrKnowledgeBaseNotFound) {
			continue // incomplete/stale file left by an interrupted delete
		}
		if err != nil {
			return nil, err
		}
		bases = append(bases, knowledgeBaseFromRecord(*record))
	}
	sort.Slice(bases, func(i, j int) bool {
		if bases[i].UpdatedAt.Equal(bases[j].UpdatedAt) {
			return strings.ToLower(bases[i].Name) < strings.ToLower(bases[j].Name)
		}
		return bases[i].UpdatedAt.After(bases[j].UpdatedAt)
	})
	return bases, nil
}

func GetKnowledgeBase(ctx context.Context, sessionDir, id string) (KnowledgeBase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return KnowledgeBase{}, ErrKnowledgeBaseNotFound
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeBase{}, err
	}
	var record *dao.KnowledgeBaseRecord
	err := queryKnowledgeBaseDatabase(sessionDir, id, func(db *dao.Database) error {
		var err error
		record, err = dao.NewKnowledgeBaseDAO(db.Bun()).FindBase(ctx, id)
		return err
	})
	if errors.Is(err, dao.ErrNoRows) {
		return KnowledgeBase{}, ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return KnowledgeBase{}, err
	}
	return knowledgeBaseFromRecord(*record), nil
}

func UpdateKnowledgeBase(ctx context.Context, sessionDir, id string, spec KnowledgeBaseSpec) (KnowledgeBase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return KnowledgeBase{}, ErrKnowledgeBaseNotFound
	}
	if err := validateKnowledgeBaseSpec(&spec); err != nil {
		return KnowledgeBase{}, err
	}
	base, err := GetKnowledgeBase(ctx, sessionDir, id)
	if err != nil {
		return KnowledgeBase{}, err
	}
	base.KnowledgeBaseSpec = spec
	// A snapshot is only authoritative for the exact directory/profile that
	// produced it. Configuration edits therefore require an explicit fresh
	// scan instead of allowing a stale graph to be queried by callers.
	base.ActiveSnapshotID = ""
	base.UpdatedAt = time.Now().UTC()
	expectedRevision := base.ConfigRevision
	base.ConfigRevision++
	err = writeKnowledgeBaseDatabase(ctx, sessionDir, id, false, func(tx *dao.Tx) error {
		store := dao.NewKnowledgeBaseDAO(nil)
		changed, err := store.UpdateBase(ctx, tx, knowledgeBaseRecord(base), expectedRevision)
		if err != nil {
			return err
		}
		if changed != 1 {
			if _, findErr := store.FindBaseWith(ctx, tx, id); errors.Is(findErr, dao.ErrNoRows) {
				return ErrKnowledgeBaseNotFound
			} else if findErr != nil {
				return findErr
			}
			return ErrKnowledgeBaseConfigurationChanged
		}
		// Configuration determines the source and meaning of the graph. Once it
		// changes, preserve neither a stale active snapshot nor historical graph
		// payloads that can no longer be queried.
		return store.PruneSnapshotsExcept(ctx, tx, id, "")
	})
	if err != nil {
		return KnowledgeBase{}, err
	}
	return base, nil
}

func DeleteKnowledgeBase(ctx context.Context, sessionDir, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrKnowledgeBaseNotFound
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return err
	}
	if _, err := GetKnowledgeBase(ctx, sessionDir, id); err != nil {
		return err
	}
	if err := writeKnowledgeBaseDatabase(ctx, sessionDir, id, false, func(tx *dao.Tx) error {
		changed, err := dao.NewKnowledgeBaseDAO(nil).DeleteBase(ctx, tx, id)
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrKnowledgeBaseNotFound
		}
		return nil
	}); err != nil {
		return err
	}
	return deleteKnowledgeBaseDatabase(sessionDir, id)
}

func GetKnowledgeSnapshot(ctx context.Context, sessionDir, id string) (KnowledgeSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeSnapshot{}, err
	}
	id = strings.TrimSpace(id)
	bases, err := listKnowledgeBaseDatabaseIDs(sessionDir)
	if err != nil {
		return KnowledgeSnapshot{}, err
	}
	for _, base := range bases {
		var record *dao.KnowledgeSnapshotRecord
		err := queryKnowledgeBaseDatabase(sessionDir, base, func(db *dao.Database) error {
			var queryErr error
			record, queryErr = dao.NewKnowledgeBaseDAO(db.Bun()).FindSnapshot(ctx, id)
			return queryErr
		})
		if errors.Is(err, dao.ErrNoRows) {
			continue
		}
		if err != nil {
			return KnowledgeSnapshot{}, err
		}
		return knowledgeSnapshotFromRecord(*record), nil
	}
	return KnowledgeSnapshot{}, ErrKnowledgeBaseUnindexed
}

func StoreKnowledgeGraphSnapshot(ctx context.Context, sessionDir string, graph KnowledgeGraphSnapshot) (KnowledgeSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateKnowledgeGraphSnapshot(&graph); err != nil {
		return KnowledgeSnapshot{}, err
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeSnapshot{}, err
	}
	now := time.Now().UTC()
	graph.Snapshot.Status = "completed"
	graph.Snapshot.SchemaVersion = KnowledgeGraphSchemaVersion
	graph.Snapshot.FileCount = len(graph.Files)
	graph.Snapshot.ChunkCount = len(graph.Chunks)
	graph.Snapshot.NodeCount = len(graph.Nodes)
	graph.Snapshot.EdgeCount = len(graph.Edges)
	if graph.Snapshot.StartedAt.IsZero() {
		graph.Snapshot.StartedAt = now
	}
	graph.Snapshot.FinishedAt = now
	err := writeKnowledgeBaseDatabase(ctx, sessionDir, graph.Snapshot.KnowledgeBaseID, false, func(tx *dao.Tx) error {
		store := dao.NewKnowledgeBaseDAO(nil)
		if err := store.InsertSnapshot(ctx, tx, knowledgeSnapshotRecord(graph.Snapshot)); err != nil {
			return err
		}
		if err := store.InsertFiles(ctx, tx, knowledgeFileRecords(graph.Files)); err != nil {
			return err
		}
		if err := store.InsertChunks(ctx, tx, knowledgeChunkRecords(graph.Chunks)); err != nil {
			return err
		}
		if err := store.InsertNodes(ctx, tx, knowledgeNodeRecords(graph.Nodes)); err != nil {
			return err
		}
		if err := store.InsertEdges(ctx, tx, knowledgeEdgeRecords(graph.Edges)); err != nil {
			return err
		}
		if err := store.InsertEvidence(ctx, tx, knowledgeEvidenceRecords(graph.Evidence)); err != nil {
			return err
		}
		changed, err := store.ActivateSnapshot(ctx, tx, graph.Snapshot.KnowledgeBaseID, graph.Snapshot.ID, now.Format(time.RFC3339Nano), graph.BaseConfigRevision)
		if err != nil {
			return err
		}
		if changed != 1 {
			if _, findErr := store.FindBaseWith(ctx, tx, graph.Snapshot.KnowledgeBaseID); errors.Is(findErr, dao.ErrNoRows) {
				return ErrKnowledgeBaseNotFound
			} else if findErr != nil {
				return findErr
			}
			return ErrKnowledgeBaseConfigurationChanged
		}
		// Keep exactly the snapshot that was just atomically made active. The
		// graph query projects all needed rows into memory before returning, and
		// this shared SQLite transaction prevents an observer from seeing a
		// partially pruned graph.
		return store.PruneSnapshotsExcept(ctx, tx, graph.Snapshot.KnowledgeBaseID, graph.Snapshot.ID)
	})
	if err != nil {
		return KnowledgeSnapshot{}, err
	}
	return graph.Snapshot, nil
}

// ReuseKnowledgeSnapshotIfFilesMatch returns the active immutable snapshot
// when the deterministic scan produced the exact same indexable file set.
// It deliberately compares content hashes rather than timestamps so a caller
// cannot serve stale knowledge merely because a tool preserved mtimes.
func ReuseKnowledgeSnapshotIfFilesMatch(ctx context.Context, sessionDir, baseID string, expectedRevision int64, files []KnowledgeFile) (KnowledgeSnapshot, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeSnapshot{}, false, err
	}
	baseID = strings.TrimSpace(baseID)
	var matched KnowledgeSnapshot
	var reusable bool
	err := queryKnowledgeBaseDatabase(sessionDir, baseID, func(db *dao.Database) error {
		store := dao.NewKnowledgeBaseDAO(db.Bun())
		baseRecord, err := store.FindBase(ctx, baseID)
		if err != nil {
			return err
		}
		if baseRecord.ConfigRevision != expectedRevision {
			return nil
		}
		if strings.TrimSpace(baseRecord.ActiveSnapshotID) == "" {
			return nil
		}
		snapshotRecord, err := store.FindSnapshot(ctx, baseRecord.ActiveSnapshotID)
		if errors.Is(err, dao.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		snapshot := knowledgeSnapshotFromRecord(*snapshotRecord)
		if snapshot.Status != "completed" || snapshot.SchemaVersion != KnowledgeGraphSchemaVersion {
			return nil
		}
		storedFiles, err := store.ListFilesForSnapshot(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		if !knowledgeFilesMatch(files, storedFiles) {
			return nil
		}
		matched, reusable = snapshot, true
		return nil
	})
	if errors.Is(err, dao.ErrNoRows) || errors.Is(err, ErrKnowledgeBaseNotFound) {
		return KnowledgeSnapshot{}, false, nil
	}
	if err != nil {
		return KnowledgeSnapshot{}, false, err
	}
	return matched, reusable, nil
}

func knowledgeFilesMatch(files []KnowledgeFile, records []dao.KnowledgeFileRecord) bool {
	if len(files) != len(records) {
		return false
	}
	byPath := make(map[string]KnowledgeFile, len(files))
	for _, file := range files {
		if file.RelativePath == "" {
			return false
		}
		if _, exists := byPath[file.RelativePath]; exists {
			return false
		}
		byPath[file.RelativePath] = file
	}
	for _, record := range records {
		file, ok := byPath[record.RelativePath]
		if !ok || file.ContentSHA256 != record.ContentSHA256 || file.ByteSize != record.ByteSize || file.MediaType != record.MediaType || file.Status != record.Status {
			return false
		}
	}
	return true
}

// KnowledgeFileGraph is the self-contained, evidence-backed subgraph owned by
// one source file. It is used to carry unchanged file work from an immutable
// snapshot into its successor without re-running extractors or an Indexer.
type KnowledgeFileGraph struct {
	File     KnowledgeFile
	Chunks   []KnowledgeChunk
	Nodes    []KnowledgeNode
	Edges    []KnowledgeEdge
	Evidence []KnowledgeEvidence
}

// KnowledgeGraphReusePlan contains source-file subgraphs that still match the
// current directory manifest. The caller must clone them into a new snapshot;
// no row, node, chunk or edge identity is shared between snapshots.
type KnowledgeGraphReusePlan struct {
	BaseConfigRevision int64
	SourceSnapshotID   string
	Files              map[string]KnowledgeFileGraph // key: normalized relative path
}

// PrepareKnowledgeGraphReusePlan loads unchanged per-file graph work from the
// active snapshot. It is deliberately all-or-nothing per file: an edge whose
// endpoint or evidence escapes the file is omitted rather than claiming an
// unverified cross-file relationship in the successor snapshot.
func PrepareKnowledgeGraphReusePlan(ctx context.Context, sessionDir, baseID string, manifest []KnowledgeFile) (KnowledgeGraphReusePlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeGraphReusePlan{}, err
	}
	baseID = strings.TrimSpace(baseID)
	plan := KnowledgeGraphReusePlan{Files: map[string]KnowledgeFileGraph{}}
	err := queryKnowledgeBaseDatabase(sessionDir, baseID, func(db *dao.Database) error {
		store := dao.NewKnowledgeBaseDAO(db.Bun())
		base, err := store.FindBase(ctx, baseID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(base.ActiveSnapshotID) == "" {
			return nil
		}
		plan.BaseConfigRevision = base.ConfigRevision
		snapshot, err := store.FindSnapshot(ctx, base.ActiveSnapshotID)
		if errors.Is(err, dao.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if snapshot.Status != "completed" || snapshot.SchemaVersion != KnowledgeGraphSchemaVersion {
			return nil
		}
		storedFiles, err := store.ListFilesForSnapshot(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		matching := matchingKnowledgeFileRecords(manifest, storedFiles)
		if len(matching) == 0 {
			return nil
		}
		chunks, err := store.ListChunksForSnapshot(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		nodes, err := store.ListNodesForSnapshot(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		edges, err := store.ListEdgesForSnapshot(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		evidence, err := store.ListEvidenceForSnapshot(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		plan.SourceSnapshotID = snapshot.ID
		plan.Files = partitionKnowledgeFileGraphs(matching, chunks, nodes, edges, evidence)
		return nil
	})
	if errors.Is(err, dao.ErrNoRows) || errors.Is(err, ErrKnowledgeBaseNotFound) {
		return KnowledgeGraphReusePlan{Files: map[string]KnowledgeFileGraph{}}, nil
	}
	if err != nil {
		return KnowledgeGraphReusePlan{}, err
	}
	return plan, nil
}

func matchingKnowledgeFileRecords(manifest []KnowledgeFile, records []dao.KnowledgeFileRecord) map[string]dao.KnowledgeFileRecord {
	byPath := make(map[string]KnowledgeFile, len(manifest))
	for _, file := range manifest {
		if file.RelativePath != "" {
			byPath[file.RelativePath] = file
		}
	}
	matching := make(map[string]dao.KnowledgeFileRecord)
	for _, record := range records {
		file, ok := byPath[record.RelativePath]
		if ok && file.ContentSHA256 == record.ContentSHA256 && file.ByteSize == record.ByteSize && file.MediaType == record.MediaType && file.Status == record.Status {
			matching[record.ID] = record
		}
	}
	return matching
}

func partitionKnowledgeFileGraphs(files map[string]dao.KnowledgeFileRecord, chunks []dao.KnowledgeChunkRecord, nodes []dao.KnowledgeNodeRecord, edges []dao.KnowledgeEdgeRecord, evidence []dao.KnowledgeEvidenceRecord) map[string]KnowledgeFileGraph {
	result := make(map[string]KnowledgeFileGraph, len(files))
	byChunk := make(map[string]string)
	for _, record := range files {
		result[record.RelativePath] = KnowledgeFileGraph{File: KnowledgeFile{ID: record.ID, SnapshotID: record.SnapshotID, RelativePath: record.RelativePath,
			ContentSHA256: record.ContentSHA256, ByteSize: record.ByteSize, MediaType: record.MediaType, Title: record.Title, Status: record.Status}}
	}
	for _, chunk := range chunks {
		file, ok := files[chunk.FileID]
		if !ok {
			continue
		}
		graph := result[file.RelativePath]
		graph.Chunks = append(graph.Chunks, KnowledgeChunk{ID: chunk.ID, SnapshotID: chunk.SnapshotID, FileID: chunk.FileID, Ordinal: chunk.Ordinal,
			RelativePath: chunk.RelativePath, Text: chunk.Text, StartLine: chunk.StartLine, EndLine: chunk.EndLine, ContentSHA256: chunk.ContentSHA256})
		result[file.RelativePath] = graph
		byChunk[chunk.ID] = file.RelativePath
	}

	nodePath := map[string]string{}
	edgePath := map[string]string{}
	for _, item := range evidence {
		path, ok := byChunk[item.ChunkID]
		if !ok {
			continue
		}
		if item.NodeID != "" {
			if previous, exists := nodePath[item.NodeID]; !exists || path < previous {
				nodePath[item.NodeID] = path
			}
		}
		if item.EdgeID != "" {
			if previous, exists := edgePath[item.EdgeID]; !exists || path < previous {
				edgePath[item.EdgeID] = path
			}
		}
	}
	for _, node := range nodes {
		path, ok := nodePath[node.ID]
		if !ok {
			continue
		}
		graph := result[path]
		graph.Nodes = append(graph.Nodes, KnowledgeNode{ID: node.ID, SnapshotID: node.SnapshotID, Kind: node.Kind, Label: node.Label,
			NormalizedLabel: node.NormalizedLabel, Summary: node.Summary})
		result[path] = graph
	}
	for _, edge := range edges {
		path, evidenced := edgePath[edge.ID]
		fromPath, fromOK := nodePath[edge.FromNodeID]
		toPath, toOK := nodePath[edge.ToNodeID]
		if !evidenced || !fromOK || !toOK || fromPath != path || toPath != path {
			continue
		}
		graph := result[path]
		graph.Edges = append(graph.Edges, KnowledgeEdge{ID: edge.ID, SnapshotID: edge.SnapshotID, FromNodeID: edge.FromNodeID,
			ToNodeID: edge.ToNodeID, RelationType: edge.RelationType, Confidence: edge.Confidence})
		result[path] = graph
	}
	retainedEdges := map[string]struct{}{}
	for _, graph := range result {
		for _, edge := range graph.Edges {
			retainedEdges[edge.ID] = struct{}{}
		}
	}
	for _, item := range evidence {
		path, ok := byChunk[item.ChunkID]
		if !ok {
			continue
		}
		if item.NodeID != "" {
			if nodePath[item.NodeID] != path {
				continue
			}
		} else if _, ok := retainedEdges[item.EdgeID]; !ok || edgePath[item.EdgeID] != path {
			continue
		}
		graph := result[path]
		graph.Evidence = append(graph.Evidence, KnowledgeEvidence{ID: item.ID, SnapshotID: item.SnapshotID, NodeID: item.NodeID, EdgeID: item.EdgeID,
			ChunkID: item.ChunkID, StartLine: item.StartLine, EndLine: item.EndLine, Confidence: item.Confidence})
		result[path] = graph
	}
	return result
}

// AppendKnowledgeFileGraph clones a file subgraph into graph. New IDs prevent
// a successor snapshot from sharing mutable identity with its source.
func AppendKnowledgeFileGraph(graph *KnowledgeGraphSnapshot, source KnowledgeFileGraph) {
	if graph == nil || graph.Snapshot.ID == "" || source.File.ID == "" {
		return
	}
	fileIDs := map[string]string{source.File.ID: GenerateID()}
	file := source.File
	file.ID, file.SnapshotID = fileIDs[source.File.ID], graph.Snapshot.ID
	graph.Files = append(graph.Files, file)

	chunkIDs := make(map[string]string, len(source.Chunks))
	for _, sourceChunk := range source.Chunks {
		chunk := sourceChunk
		chunk.ID, chunk.SnapshotID, chunk.FileID = GenerateID(), graph.Snapshot.ID, file.ID
		chunkIDs[sourceChunk.ID] = chunk.ID
		graph.Chunks = append(graph.Chunks, chunk)
	}
	nodeIDs := make(map[string]string, len(source.Nodes))
	for _, sourceNode := range source.Nodes {
		node := sourceNode
		node.ID, node.SnapshotID = GenerateID(), graph.Snapshot.ID
		nodeIDs[sourceNode.ID] = node.ID
		graph.Nodes = append(graph.Nodes, node)
	}
	edgeIDs := make(map[string]string, len(source.Edges))
	for _, sourceEdge := range source.Edges {
		from, fromOK := nodeIDs[sourceEdge.FromNodeID]
		to, toOK := nodeIDs[sourceEdge.ToNodeID]
		if !fromOK || !toOK {
			continue
		}
		edge := sourceEdge
		edge.ID, edge.SnapshotID, edge.FromNodeID, edge.ToNodeID = GenerateID(), graph.Snapshot.ID, from, to
		edgeIDs[sourceEdge.ID] = edge.ID
		graph.Edges = append(graph.Edges, edge)
	}
	for _, sourceEvidence := range source.Evidence {
		chunkID, chunkOK := chunkIDs[sourceEvidence.ChunkID]
		if !chunkOK {
			continue
		}
		evidence := sourceEvidence
		evidence.ID, evidence.SnapshotID, evidence.ChunkID = GenerateID(), graph.Snapshot.ID, chunkID
		if sourceEvidence.NodeID != "" {
			mapped, ok := nodeIDs[sourceEvidence.NodeID]
			if !ok {
				continue
			}
			evidence.NodeID, evidence.EdgeID = mapped, ""
		} else {
			mapped, ok := edgeIDs[sourceEvidence.EdgeID]
			if !ok {
				continue
			}
			evidence.NodeID, evidence.EdgeID = "", mapped
		}
		graph.Evidence = append(graph.Evidence, evidence)
	}
}

// QueryKnowledgeGraph executes the local FTS seed lookup followed by a
// bounded graph projection. It never reads the source directory itself; the
// active completed snapshot is the sole query authority.
func QueryKnowledgeGraph(ctx context.Context, sessionDir, baseID, query string, limit int) (KnowledgeGraphQuery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := migrateLegacyKnowledgeBaseStorage(ctx, sessionDir); err != nil {
		return KnowledgeGraphQuery{}, err
	}
	baseID = strings.TrimSpace(baseID)
	result := KnowledgeGraphQuery{}
	err := readKnowledgeBaseDatabase(ctx, sessionDir, baseID, func(tx *dao.Tx) error {
		projection, indexed, err := dao.NewKnowledgeBaseDAO(nil).ActiveGraphProjection(ctx, tx, baseID, query, limit)
		if err != nil {
			return err
		}
		if !indexed {
			return ErrKnowledgeBaseUnindexed
		}
		result.KnowledgeBase = knowledgeBaseFromRecord(projection.Base)
		result.Snapshot = knowledgeSnapshotFromRecord(projection.Snapshot)
		result.Chunks = knowledgeChunksFromRecords(projection.Chunks)
		result.Nodes = knowledgeNodesFromRecords(projection.Nodes)
		result.Edges = knowledgeEdgesFromRecords(projection.Edges)
		return nil
	})
	if errors.Is(err, dao.ErrNoRows) || errors.Is(err, ErrKnowledgeBaseNotFound) {
		return KnowledgeGraphQuery{}, ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return KnowledgeGraphQuery{}, err
	}
	return result, nil
}

// migrateLegacyKnowledgeBaseStorage moves the short-lived shared-store layout
// into one private database per knowledge base. The destination commit happens
// before the source rows are removed, so an interrupted migration is safe to
// retry; source material and canonical session/Run records are never touched.
func migrateLegacyKnowledgeBaseStorage(ctx context.Context, sessionDir string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var bases []dao.KnowledgeBaseRecord
	err := QueryRootDatabase(sessionDir, func(db *dao.Database) error {
		store := dao.NewKnowledgeBaseDAO(db.Bun())
		exists, err := store.HasStorage(ctx)
		if err != nil || !exists {
			return err
		}
		bases, err = store.ListLegacyBases(ctx)
		return err
	})
	if err != nil || len(bases) == 0 {
		return err
	}
	for _, base := range bases {
		legacy, err := readLegacyKnowledgeBase(ctx, sessionDir, base)
		if err != nil {
			return err
		}
		if err := writeLegacyKnowledgeBaseToDedicatedStore(ctx, sessionDir, legacy); err != nil {
			return err
		}
		if err := WriteRootDatabase(ctx, sessionDir, func(tx *dao.Tx) error {
			changed, err := dao.NewKnowledgeBaseDAO(nil).DeleteBase(ctx, tx, base.ID)
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrKnowledgeBaseNotFound
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

type legacyKnowledgeBaseData struct {
	base      dao.KnowledgeBaseRecord
	snapshots []legacyKnowledgeSnapshotData
}

type legacyKnowledgeSnapshotData struct {
	snapshot dao.KnowledgeSnapshotRecord
	files    []dao.KnowledgeFileRecord
	chunks   []dao.KnowledgeChunkRecord
	nodes    []dao.KnowledgeNodeRecord
	edges    []dao.KnowledgeEdgeRecord
	evidence []dao.KnowledgeEvidenceRecord
}

func readLegacyKnowledgeBase(ctx context.Context, sessionDir string, base dao.KnowledgeBaseRecord) (legacyKnowledgeBaseData, error) {
	data := legacyKnowledgeBaseData{base: base}
	err := QueryRootDatabase(sessionDir, func(db *dao.Database) error {
		store := dao.NewKnowledgeBaseDAO(db.Bun())
		snapshots, err := store.ListSnapshotsForBase(ctx, base.ID)
		if err != nil {
			return err
		}
		data.snapshots = make([]legacyKnowledgeSnapshotData, 0, len(snapshots))
		for _, snapshot := range snapshots {
			item := legacyKnowledgeSnapshotData{snapshot: snapshot}
			if item.files, err = store.ListFilesForSnapshot(ctx, snapshot.ID); err != nil {
				return err
			}
			if item.chunks, err = store.ListChunksForSnapshot(ctx, snapshot.ID); err != nil {
				return err
			}
			if item.nodes, err = store.ListNodesForSnapshot(ctx, snapshot.ID); err != nil {
				return err
			}
			if item.edges, err = store.ListEdgesForSnapshot(ctx, snapshot.ID); err != nil {
				return err
			}
			if item.evidence, err = store.ListEvidenceForSnapshot(ctx, snapshot.ID); err != nil {
				return err
			}
			data.snapshots = append(data.snapshots, item)
		}
		return nil
	})
	return data, err
}

func writeLegacyKnowledgeBaseToDedicatedStore(ctx context.Context, sessionDir string, legacy legacyKnowledgeBaseData) error {
	return writeKnowledgeBaseDatabase(ctx, sessionDir, legacy.base.ID, true, func(tx *dao.Tx) error {
		store := dao.NewKnowledgeBaseDAO(nil)
		// If a prior migration attempt committed the destination before a crash,
		// replace that complete private copy with the still-authoritative source.
		if _, err := store.DeleteBase(ctx, tx, legacy.base.ID); err != nil {
			return err
		}
		base := legacy.base
		if err := store.InsertBase(ctx, tx, &base); err != nil {
			return err
		}
		for _, item := range legacy.snapshots {
			snapshot := item.snapshot
			if err := store.InsertSnapshot(ctx, tx, &snapshot); err != nil {
				return err
			}
			if err := store.InsertFiles(ctx, tx, item.files); err != nil {
				return err
			}
			if err := store.InsertChunks(ctx, tx, item.chunks); err != nil {
				return err
			}
			if err := store.InsertNodes(ctx, tx, item.nodes); err != nil {
				return err
			}
			if err := store.InsertEdges(ctx, tx, item.edges); err != nil {
				return err
			}
			if err := store.InsertEvidence(ctx, tx, item.evidence); err != nil {
				return err
			}
		}
		// The dedicated-store layout has the same active-only retention policy
		// as newly indexed data, so historical shared-store snapshots do not
		// create a large permanent duplicate during migration.
		return store.PruneSnapshotsExcept(ctx, tx, legacy.base.ID, legacy.base.ActiveSnapshotID)
	})
}

func validateKnowledgeBaseSpec(spec *KnowledgeBaseSpec) error {
	if spec == nil {
		return fmt.Errorf("knowledge base configuration is required")
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.RootDir = filepath.Clean(strings.TrimSpace(spec.RootDir))
	spec.PreprocessProfile = strings.ToLower(strings.TrimSpace(spec.PreprocessProfile))
	spec.Provider = strings.TrimSpace(spec.Provider)
	spec.Model = strings.TrimSpace(spec.Model)
	spec.Mode = strings.TrimSpace(spec.Mode)
	spec.ThinkingLevel = strings.TrimSpace(spec.ThinkingLevel)
	spec.Schedule = strings.TrimSpace(spec.Schedule)
	if spec.Name == "" {
		return fmt.Errorf("knowledge base name is required")
	}
	if !filepath.IsAbs(spec.RootDir) {
		return fmt.Errorf("knowledge base root directory must be absolute")
	}
	info, err := os.Stat(spec.RootDir)
	if err != nil {
		return fmt.Errorf("knowledge base root directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("knowledge base root directory is not a directory")
	}
	switch spec.PreprocessProfile {
	case "documents", "code", "notes", "mixed":
	default:
		return fmt.Errorf("unsupported knowledge base preprocess profile %q", spec.PreprocessProfile)
	}
	if spec.Mode == "" {
		spec.Mode = "yolo"
	}
	if spec.Schedule == "" {
		spec.Schedule = "manual"
	}
	if len(spec.Schedule) > 128 || strings.ContainsAny(spec.Schedule, "\r\n") {
		return fmt.Errorf("invalid knowledge base schedule")
	}
	return nil
}

func validateKnowledgeGraphSnapshot(graph *KnowledgeGraphSnapshot) error {
	if graph == nil {
		return fmt.Errorf("knowledge graph snapshot is required")
	}
	graph.Snapshot.ID = strings.TrimSpace(graph.Snapshot.ID)
	graph.Snapshot.KnowledgeBaseID = strings.TrimSpace(graph.Snapshot.KnowledgeBaseID)
	if graph.Snapshot.ID == "" || graph.Snapshot.KnowledgeBaseID == "" {
		return fmt.Errorf("knowledge graph snapshot and base IDs are required")
	}
	if graph.BaseConfigRevision < 1 {
		return fmt.Errorf("knowledge graph base configuration revision is required")
	}
	for _, file := range graph.Files {
		if file.ID == "" || file.SnapshotID != graph.Snapshot.ID || file.RelativePath == "" {
			return fmt.Errorf("invalid knowledge graph file")
		}
	}
	for _, chunk := range graph.Chunks {
		if chunk.ID == "" || chunk.SnapshotID != graph.Snapshot.ID || chunk.FileID == "" || chunk.StartLine <= 0 || chunk.EndLine < chunk.StartLine {
			return fmt.Errorf("invalid knowledge graph chunk")
		}
	}
	for _, node := range graph.Nodes {
		if node.ID == "" || node.SnapshotID != graph.Snapshot.ID || node.Kind == "" || node.NormalizedLabel == "" {
			return fmt.Errorf("invalid knowledge graph node")
		}
	}
	for _, edge := range graph.Edges {
		if edge.ID == "" || edge.SnapshotID != graph.Snapshot.ID || edge.FromNodeID == "" || edge.ToNodeID == "" || edge.RelationType == "" {
			return fmt.Errorf("invalid knowledge graph edge")
		}
	}
	for _, evidence := range graph.Evidence {
		if evidence.ID == "" || evidence.SnapshotID != graph.Snapshot.ID || evidence.ChunkID == "" || (evidence.NodeID == "" && evidence.EdgeID == "") {
			return fmt.Errorf("invalid knowledge graph evidence")
		}
	}
	return nil
}

func knowledgeBaseRecord(base KnowledgeBase) *dao.KnowledgeBaseRecord {
	return &dao.KnowledgeBaseRecord{ID: base.ID, Name: base.Name, RootDir: base.RootDir, PreprocessProfile: base.PreprocessProfile,
		Provider: base.Provider, Model: base.Model, Mode: base.Mode, ThinkingLevel: base.ThinkingLevel, Schedule: base.Schedule,
		Enabled: boolToInt(base.Enabled), ActiveSnapshotID: base.ActiveSnapshotID, ConfigRevision: max(base.ConfigRevision, 1),
		CreatedAt: base.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: base.UpdatedAt.Format(time.RFC3339Nano)}
}

func knowledgeBaseFromRecord(record dao.KnowledgeBaseRecord) KnowledgeBase {
	return KnowledgeBase{ID: record.ID, KnowledgeBaseSpec: KnowledgeBaseSpec{Name: record.Name, RootDir: record.RootDir,
		PreprocessProfile: record.PreprocessProfile, Provider: record.Provider, Model: record.Model, Mode: record.Mode,
		ThinkingLevel: record.ThinkingLevel, Schedule: record.Schedule, Enabled: record.Enabled != 0},
		ActiveSnapshotID: record.ActiveSnapshotID, CreatedAt: parseProjectTime(record.CreatedAt), UpdatedAt: parseProjectTime(record.UpdatedAt), ConfigRevision: record.ConfigRevision}
}

func knowledgeSnapshotRecord(snapshot KnowledgeSnapshot) *dao.KnowledgeSnapshotRecord {
	return &dao.KnowledgeSnapshotRecord{ID: snapshot.ID, KnowledgeBaseID: snapshot.KnowledgeBaseID, RunID: snapshot.RunID,
		Status: snapshot.Status, SchemaVersion: snapshot.SchemaVersion, FileCount: snapshot.FileCount, ChunkCount: snapshot.ChunkCount,
		NodeCount: snapshot.NodeCount, EdgeCount: snapshot.EdgeCount, StartedAt: snapshot.StartedAt.Format(time.RFC3339Nano),
		FinishedAt: timestampString(snapshot.FinishedAt), ErrorSummary: snapshot.ErrorSummary}
}

func knowledgeSnapshotFromRecord(record dao.KnowledgeSnapshotRecord) KnowledgeSnapshot {
	return KnowledgeSnapshot{ID: record.ID, KnowledgeBaseID: record.KnowledgeBaseID, RunID: record.RunID, Status: record.Status,
		SchemaVersion: record.SchemaVersion, FileCount: record.FileCount, ChunkCount: record.ChunkCount, NodeCount: record.NodeCount,
		EdgeCount: record.EdgeCount, StartedAt: parseProjectTime(record.StartedAt), FinishedAt: parseProjectTime(record.FinishedAt), ErrorSummary: record.ErrorSummary}
}

func timestampString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func knowledgeFileRecords(files []KnowledgeFile) []dao.KnowledgeFileRecord {
	records := make([]dao.KnowledgeFileRecord, 0, len(files))
	for _, file := range files {
		records = append(records, dao.KnowledgeFileRecord{ID: file.ID, SnapshotID: file.SnapshotID, RelativePath: file.RelativePath,
			ContentSHA256: file.ContentSHA256, ByteSize: file.ByteSize, MediaType: file.MediaType, Title: file.Title, Status: file.Status})
	}
	return records
}

func knowledgeChunkRecords(chunks []KnowledgeChunk) []dao.KnowledgeChunkRecord {
	records := make([]dao.KnowledgeChunkRecord, 0, len(chunks))
	for _, chunk := range chunks {
		records = append(records, dao.KnowledgeChunkRecord{ID: chunk.ID, SnapshotID: chunk.SnapshotID, FileID: chunk.FileID,
			Ordinal: chunk.Ordinal, Text: chunk.Text, StartLine: chunk.StartLine, EndLine: chunk.EndLine, ContentSHA256: chunk.ContentSHA256})
	}
	return records
}

func knowledgeNodeRecords(nodes []KnowledgeNode) []dao.KnowledgeNodeRecord {
	records := make([]dao.KnowledgeNodeRecord, 0, len(nodes))
	for _, node := range nodes {
		records = append(records, dao.KnowledgeNodeRecord{ID: node.ID, SnapshotID: node.SnapshotID, Kind: node.Kind, Label: node.Label,
			NormalizedLabel: node.NormalizedLabel, Summary: node.Summary, Attributes: "{}"})
	}
	return records
}

func knowledgeEdgeRecords(edges []KnowledgeEdge) []dao.KnowledgeEdgeRecord {
	records := make([]dao.KnowledgeEdgeRecord, 0, len(edges))
	for _, edge := range edges {
		records = append(records, dao.KnowledgeEdgeRecord{ID: edge.ID, SnapshotID: edge.SnapshotID, FromNodeID: edge.FromNodeID,
			ToNodeID: edge.ToNodeID, RelationType: edge.RelationType, Confidence: edge.Confidence})
	}
	return records
}

func knowledgeEvidenceRecords(evidence []KnowledgeEvidence) []dao.KnowledgeEvidenceRecord {
	records := make([]dao.KnowledgeEvidenceRecord, 0, len(evidence))
	for _, item := range evidence {
		records = append(records, dao.KnowledgeEvidenceRecord{ID: item.ID, SnapshotID: item.SnapshotID, NodeID: item.NodeID,
			EdgeID: item.EdgeID, ChunkID: item.ChunkID, StartLine: item.StartLine, EndLine: item.EndLine, Confidence: item.Confidence})
	}
	return records
}

func knowledgeChunksFromRecords(records []dao.KnowledgeChunkRecord) []KnowledgeChunk {
	chunks := make([]KnowledgeChunk, 0, len(records))
	for _, record := range records {
		chunks = append(chunks, KnowledgeChunk{ID: record.ID, SnapshotID: record.SnapshotID, FileID: record.FileID, Ordinal: record.Ordinal,
			RelativePath: record.RelativePath, Text: record.Text, StartLine: record.StartLine, EndLine: record.EndLine, ContentSHA256: record.ContentSHA256})
	}
	return chunks
}

func knowledgeNodesFromRecords(records []dao.KnowledgeNodeRecord) []KnowledgeNode {
	nodes := make([]KnowledgeNode, 0, len(records))
	for _, record := range records {
		nodes = append(nodes, KnowledgeNode{ID: record.ID, SnapshotID: record.SnapshotID, Kind: record.Kind, Label: record.Label,
			NormalizedLabel: record.NormalizedLabel, Summary: record.Summary})
	}
	return nodes
}

func knowledgeEdgesFromRecords(records []dao.KnowledgeEdgeRecord) []KnowledgeEdge {
	edges := make([]KnowledgeEdge, 0, len(records))
	for _, record := range records {
		edges = append(edges, KnowledgeEdge{ID: record.ID, SnapshotID: record.SnapshotID, FromNodeID: record.FromNodeID,
			ToNodeID: record.ToNodeID, RelationType: record.RelationType, Confidence: record.Confidence})
	}
	return edges
}
