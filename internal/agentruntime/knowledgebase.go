package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/session"
)

// KnowledgeBaseIndexPolicy is intentionally deterministic. LLM-based graph
// enrichment is a later Runtime step; this policy first guarantees a local,
// reproducible file/chunk/graph snapshot that no adapter needs to rebuild.
type KnowledgeBaseIndexPolicy struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxChunkBytes int
}

func DefaultKnowledgeBaseIndexPolicy() KnowledgeBaseIndexPolicy {
	return KnowledgeBaseIndexPolicy{MaxFiles: 5000, MaxFileBytes: 2 << 20, MaxChunkBytes: 6000}
}

// KnowledgeBaseService is Runtime-owned indexing and graph-query plumbing for
// Desktop-configured directory knowledge bases. It contains no Desktop UI,
// ACP protocol or provider-message construction.
type KnowledgeBaseService struct {
	sessionDir      string
	policy          KnowledgeBaseIndexPolicy
	settingsMu      sync.RWMutex
	settings        *config.Settings
	providerFactory KnowledgeBaseProviderFactory
	// indexJobs tracks background scans so management RPCs can start a scan
	// without blocking and poll its progress afterwards. indexPending coalesces
	// triggers that arrive while a scan is in flight into a single follow-up pass
	// so a burst of requests never queues one full run per trigger.
	indexJobsMu  sync.Mutex
	indexJobs    map[string]*KnowledgeIndexJob
	indexPending map[string]RuntimeSource
}

// KnowledgeBaseProviderFactory creates the configured provider/model for an
// optional Indexer role. It keeps provider construction in Runtime and makes
// the graph-enrichment path deterministic to test without an adapter-owned
// provider builder.
type KnowledgeBaseProviderFactory func(*config.Settings, string, string) (provider.Provider, *provider.Model, error)

func NewKnowledgeBaseService(sessionDir string, policy KnowledgeBaseIndexPolicy) (*KnowledgeBaseService, error) {
	return NewKnowledgeBaseServiceWithProviderFactory(sessionDir, policy, nil, nil)
}

// NewKnowledgeBaseServiceWithSettings enables the configured Indexer Agent
// role. Directory scanning and graph persistence remain the same Runtime
// service regardless of whether enrichment is enabled.
func NewKnowledgeBaseServiceWithSettings(sessionDir string, policy KnowledgeBaseIndexPolicy, settings *config.Settings) (*KnowledgeBaseService, error) {
	return NewKnowledgeBaseServiceWithProviderFactory(sessionDir, policy, settings, providerfactory.Create)
}

// NewKnowledgeBaseServiceWithProviderFactory is the testable constructor for
// Runtime-owned provider selection. Production callers should use
// NewKnowledgeBaseServiceWithSettings.
func NewKnowledgeBaseServiceWithProviderFactory(sessionDir string, policy KnowledgeBaseIndexPolicy, settings *config.Settings, factory KnowledgeBaseProviderFactory) (*KnowledgeBaseService, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return nil, fmt.Errorf("knowledge base session directory is required")
	}
	if policy.MaxFiles <= 0 || policy.MaxFileBytes <= 0 || policy.MaxChunkBytes <= 0 {
		return nil, fmt.Errorf("knowledge base index limits must be positive")
	}
	var settingsCopy *config.Settings
	if settings != nil {
		value := *settings
		settingsCopy = &value
	}
	return &KnowledgeBaseService{sessionDir: filepath.Clean(sessionDir), policy: policy, settings: settingsCopy, providerFactory: factory}, nil
}

// SetSettings replaces the settings snapshot used to resolve the optional Indexer
// role. A management surface that caches one service for the process calls it
// when settings.json changes, so a later scan uses the new provider/model without
// losing the in-flight background job registry. The stored value is a private
// copy and the previous copy is never mutated, so a concurrent reader may keep
// the pointer it already obtained.
func (s *KnowledgeBaseService) SetSettings(settings *config.Settings) {
	if s == nil {
		return
	}
	var copy *config.Settings
	if settings != nil {
		value := *settings
		copy = &value
	}
	s.settingsMu.Lock()
	s.settings = copy
	s.settingsMu.Unlock()
}

// currentSettings returns the settings snapshot for the current index job. The
// returned pointer is immutable (SetSettings replaces it), so callers may read it
// without holding the lock.
func (s *KnowledgeBaseService) currentSettings() *config.Settings {
	if s == nil {
		return nil
	}
	s.settingsMu.RLock()
	defer s.settingsMu.RUnlock()
	return s.settings
}

func (s *KnowledgeBaseService) Index(ctx context.Context, knowledgeBaseID string) (session.KnowledgeSnapshot, error) {
	return s.IndexDurable(ctx, knowledgeBaseID, SourceACP)
}

// IndexDurable performs one scan/index pass under the canonical durable Run
// lifecycle. The maintenance Run belongs to the same dedicated knowledge-base
// role session used by the Librarian Agent and has no conversation turn. When
// a provider/model is configured, its bounded Indexer Agent runs inside this
// same durable Run after deterministic extraction. SourceCron is used for
// scheduled scans; SourceACP is used by explicit Desktop management scans.
func (s *KnowledgeBaseService) IndexDurable(ctx context.Context, knowledgeBaseID string, source RuntimeSource) (session.KnowledgeSnapshot, error) {
	return s.indexDurable(ctx, knowledgeBaseID, source, nil)
}

// IndexDurableWithProgress behaves like IndexDurable but reports scan/index
// progress into job while the pass runs. It is the entry point used by
// background scans; interactive callers return before the pass completes.
func (s *KnowledgeBaseService) IndexDurableWithProgress(ctx context.Context, knowledgeBaseID string, source RuntimeSource, job *KnowledgeIndexJob) (session.KnowledgeSnapshot, error) {
	return s.indexDurable(ctx, knowledgeBaseID, source, job)
}

func (s *KnowledgeBaseService) indexDurable(ctx context.Context, knowledgeBaseID string, source RuntimeSource, job *KnowledgeIndexJob) (snapshot session.KnowledgeSnapshot, err error) {
	if s == nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("knowledge base service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := session.GetKnowledgeBase(ctx, s.sessionDir, knowledgeBaseID)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	if !base.Enabled {
		return session.KnowledgeSnapshot{}, fmt.Errorf("%w: %s", session.ErrKnowledgeBaseDisabled, base.ID)
	}
	if source == SourceUnknown {
		source = SourceACP
	}
	resolution, resolvedMode, err := ResolvePolicy(SourceResolutionInput{Requested: source}, "", base.Mode, ModeYolo)
	if err != nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("resolve knowledge indexing policy: %w", err)
	}
	source = resolution.Source
	mode := ResolveUnattendedMode(resolvedMode)
	// Do not construct a provider only to discover that a full deterministic
	// scan matches the active snapshot. The configured model name remains
	// useful durable-run provenance even when no Indexer request is needed.
	modelID := strings.TrimSpace(base.Model)
	manager, err := openKnowledgeLibrarianSession(s.sessionDir, base)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	guard, err := AcquireExecutionAdmission(ctx, s.sessionDir, manager.GetHeader().ID, ExecutionAdmissionOptions{Wait: true})
	if err != nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("acquire knowledge indexing admission: %w", err)
	}
	defer guard.Release()

	runID := "knowledge_index_" + session.GenerateID()
	job.update(func(p *KnowledgeIndexProgress) { p.RunID = runID })
	execution := &ExecutionRuntime{}
	execution.SetRunStore(RunStore{SessionDir: s.sessionDir})
	execution.SetEventSink(SessionRunEventSink{SessionDir: s.sessionDir})
	startedAt := time.Now()
	data, _ := json.Marshal(map[string]any{
		"knowledgeBaseId": base.ID,
		"operation":       "index",
		"role":            "indexer",
	})
	_, err = execution.BeginDurable(ctx, DurableRun{
		ID: runID, SessionID: manager.GetHeader().ID, WorkDir: base.RootDir,
		Source: string(source), Model: modelID, Mode: mode, Status: "running", StartedAt: startedAt,
	}, RunEvent{
		SessionID: manager.GetHeader().ID, RunID: runID, EventType: "started", Source: string(source),
		Status: "running", Model: modelID, Mode: mode, Timestamp: startedAt, Data: data,
	})
	if err != nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("begin knowledge indexing run: %w", err)
	}
	state := RunStateCompleted
	message := ""
	defer func() {
		if err != nil {
			state = RunStateFailed
			message = err.Error()
			if errors.Is(err, ErrKnowledgeIndexerModelUnavailable) {
				// Persist a stable, machine-readable code so management surfaces
				// report knowledge_base_model_unavailable instead of the raw provider
				// error. Best-effort: a persistence failure must not mask the original
				// index error.
				_, _ = execution.RecordErrorInfo(ErrorInfo{
					Code:    knowledgeIndexModelUnavailableCode,
					Type:    "configuration_error",
					Message: "knowledge base indexer model is unavailable",
					Detail:  message,
					RunID:   runID,
				})
			}
		}
		finishErr := execution.FinishDurableWithRetry(context.Background(), runID, state, message, RunEvent{
			SessionID: manager.GetHeader().ID, RunID: runID, EventType: "finished", Source: string(source),
			Status: string(state), Model: modelID, Mode: mode, Timestamp: time.Now(), Data: data,
		})
		if finishErr != nil && err == nil {
			err = fmt.Errorf("finish knowledge indexing run: %w", finishErr)
		}
	}()
	files, discovery, scanErr := s.scanFileManifest(ctx, base, job)
	if scanErr != nil {
		return session.KnowledgeSnapshot{}, scanErr
	}
	diff, diffErr := session.DiffKnowledgeManifest(ctx, s.sessionDir, base.ID, files)
	if diffErr != nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("compute knowledge diff summary: %w", diffErr)
	}
	if reused, unchanged, reuseErr := session.ReuseKnowledgeSnapshotIfFilesMatch(ctx, s.sessionDir, base.ID, base.ConfigRevision, files); reuseErr != nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("compare active knowledge snapshot: %w", reuseErr)
	} else if unchanged {
		reuseData, _ := json.Marshal(map[string]any{
			"knowledgeBaseId": base.ID,
			"snapshotId":      reused.ID,
			"operation":       "index_reused",
			"diff":            diff,
			"discovery":       discovery,
		})
		if _, eventErr := execution.RecordEvent(RunEvent{
			SessionID: manager.GetHeader().ID, RunID: runID, EventType: "knowledge_snapshot_reused", Source: string(source),
			Status: "completed", Model: modelID, Mode: mode, Timestamp: time.Now(), Data: reuseData,
		}); eventErr != nil {
			return session.KnowledgeSnapshot{}, fmt.Errorf("record knowledge snapshot reuse: %w", eventErr)
		}
		return reused, nil
	}
	reusePlan, planErr := session.PrepareKnowledgeGraphReusePlan(ctx, s.sessionDir, base.ID, files)
	if planErr != nil {
		return session.KnowledgeSnapshot{}, fmt.Errorf("prepare incremental knowledge graph reuse: %w", planErr)
	}
	job.update(func(p *KnowledgeIndexProgress) {
		p.Phase = KnowledgeIndexPhaseIndexing
		p.FilesTotal = int64(len(files))
		p.FilesDone = 0
	})
	indexedBase, graph, buildErr := s.buildGraph(ctx, knowledgeBaseID, runID, reusePlan, job)
	if buildErr != nil {
		return session.KnowledgeSnapshot{}, buildErr
	}
	job.update(func(p *KnowledgeIndexProgress) {
		p.Phase = KnowledgeIndexPhaseEnriching
		p.Chunks = int64(len(graph.Chunks))
	})
	indexer, err := s.resolveKnowledgeIndexer(indexedBase)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	if err := s.enrichGraphWithIndexer(ctx, execution, manager, indexedBase, &graph, mode, indexer); err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	job.update(func(p *KnowledgeIndexProgress) { p.Phase = KnowledgeIndexPhaseCommitting })
	graph.DiffSummary = &diff
	graph.DiscoverySummary = &discovery
	snapshot, err = session.StoreKnowledgeGraphSnapshot(ctx, s.sessionDir, graph)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	return snapshot, nil
}

// IndexWithRun builds and commits the deterministic graph baseline. The
// durable IndexDurable path may enrich that graph with separately verified
// Agent-selected links before committing it.
func (s *KnowledgeBaseService) IndexWithRun(ctx context.Context, knowledgeBaseID, runID string) (session.KnowledgeSnapshot, error) {
	base, err := session.GetKnowledgeBase(ctx, s.sessionDir, knowledgeBaseID)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	files, _, err := s.scanFileManifest(ctx, base, nil)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	if reused, unchanged, err := session.ReuseKnowledgeSnapshotIfFilesMatch(ctx, s.sessionDir, base.ID, base.ConfigRevision, files); err != nil {
		return session.KnowledgeSnapshot{}, err
	} else if unchanged {
		return reused, nil
	}
	reusePlan, err := session.PrepareKnowledgeGraphReusePlan(ctx, s.sessionDir, base.ID, files)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	_, graph, err := s.buildGraph(ctx, knowledgeBaseID, runID, reusePlan, nil)
	if err != nil {
		return session.KnowledgeSnapshot{}, err
	}
	return session.StoreKnowledgeGraphSnapshot(ctx, s.sessionDir, graph)
}

// scanFileManifest performs the inexpensive half of indexing: it validates
// every eligible source file and calculates its content hash, but deliberately
// avoids chunk construction, marker extraction, graph allocation, provider
// calls and SQLite writes. Scheduled scans of an unchanged large directory
// therefore return the active immutable snapshot early. It also returns the
// bounded discovery projection (indexed/ignored/skipped) so management
// surfaces can explain what was not indexed.
func (s *KnowledgeBaseService) scanFileManifest(ctx context.Context, base session.KnowledgeBase, job *KnowledgeIndexJob) ([]session.KnowledgeFile, session.KnowledgeDiscoverySummary, error) {
	if s == nil {
		return nil, session.KnowledgeDiscoverySummary{}, fmt.Errorf("knowledge base service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !base.Enabled {
		return nil, session.KnowledgeDiscoverySummary{}, fmt.Errorf("%w: %s", session.ErrKnowledgeBaseDisabled, base.ID)
	}
	root, err := resolveKnowledgeBaseRoot(base)
	if err != nil {
		return nil, session.KnowledgeDiscoverySummary{}, err
	}
	rules := newKnowledgeIgnoreRules(base, root)
	files := make([]session.KnowledgeFile, 0)
	discovery := session.KnowledgeDiscoverySummary{}
	fileCount := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, relErr := knowledgeRelativePath(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if knowledgeBaseIgnoredDirectory(entry.Name()) {
				discovery.RecordIgnored(rel, "ignored_directory")
				return filepath.SkipDir
			}
			if ignored, reason := rules.ignored(rel, true); ignored {
				discovery.RecordIgnored(rel, reason)
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if ignored, reason := rules.ignored(rel, false); ignored {
			discovery.RecordIgnored(rel, reason)
			return nil
		}
		if !knowledgeBaseAllowedFile(base.PreprocessProfile, path) {
			return nil
		}
		fileCount++
		if fileCount > s.policy.MaxFiles {
			return fmt.Errorf("knowledge base exceeds %d indexable files", s.policy.MaxFiles)
		}
		source, skipReason, err := s.readIndexableKnowledgeFile(ctx, root, path, entry)
		if err != nil {
			return err
		}
		if source == nil {
			if skipReason == "" {
				skipReason = "unreadable"
			}
			discovery.RecordSkipped(rel, skipReason)
			return nil
		}
		files = append(files, session.KnowledgeFile{RelativePath: source.relativePath, ContentSHA256: knowledgeSHA256(source.data),
			ByteSize: int64(len(source.data)), MediaType: source.mediaType, Status: "indexed"})
		discovery.Discovered++
		job.update(func(p *KnowledgeIndexProgress) {
			p.Phase = KnowledgeIndexPhaseScanning
			p.FilesDone++
		})
		return nil
	})
	if err != nil {
		return nil, session.KnowledgeDiscoverySummary{}, fmt.Errorf("scan knowledge base manifest: %w", err)
	}
	return files, discovery, nil
}

func (s *KnowledgeBaseService) buildGraph(ctx context.Context, knowledgeBaseID, runID string, reusePlan session.KnowledgeGraphReusePlan, job *KnowledgeIndexJob) (session.KnowledgeBase, session.KnowledgeGraphSnapshot, error) {
	if s == nil {
		return session.KnowledgeBase{}, session.KnowledgeGraphSnapshot{}, fmt.Errorf("knowledge base service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := session.GetKnowledgeBase(ctx, s.sessionDir, knowledgeBaseID)
	if err != nil {
		return session.KnowledgeBase{}, session.KnowledgeGraphSnapshot{}, err
	}
	if !base.Enabled {
		return session.KnowledgeBase{}, session.KnowledgeGraphSnapshot{}, fmt.Errorf("%w: %s", session.ErrKnowledgeBaseDisabled, base.ID)
	}
	root, err := resolveKnowledgeBaseRoot(base)
	if err != nil {
		return session.KnowledgeBase{}, session.KnowledgeGraphSnapshot{}, err
	}

	graph := session.KnowledgeGraphSnapshot{BaseConfigRevision: base.ConfigRevision, Snapshot: session.KnowledgeSnapshot{
		ID: session.GenerateID(), KnowledgeBaseID: base.ID, RunID: strings.TrimSpace(runID), Status: "indexing",
		SchemaVersion: session.KnowledgeGraphSchemaVersion,
	}}
	fileCount := 0
	rules := newKnowledgeIgnoreRules(base, root)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, relErr := knowledgeRelativePath(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if knowledgeBaseIgnoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			if ignored, _ := rules.ignored(rel, true); ignored {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if ignored, _ := rules.ignored(rel, false); ignored {
			return nil
		}
		if !knowledgeBaseAllowedFile(base.PreprocessProfile, path) {
			return nil
		}
		fileCount++
		if fileCount > s.policy.MaxFiles {
			return fmt.Errorf("knowledge base exceeds %d indexable files", s.policy.MaxFiles)
		}
		source, _, err := s.readIndexableKnowledgeFile(ctx, root, path, entry)
		if err != nil {
			return err
		}
		if source == nil {
			return nil
		}
		if reusable, ok := reusePlan.Files[source.relativePath]; ok &&
			reusePlan.BaseConfigRevision == base.ConfigRevision &&
			knowledgeSHA256(source.data) == reusable.File.ContentSHA256 {
			session.AppendKnowledgeFileGraph(&graph, reusable)
			job.update(func(p *KnowledgeIndexProgress) {
				p.Phase = KnowledgeIndexPhaseIndexing
				p.FilesDone++
			})
			return nil
		}
		if err := s.indexSourceFile(source, &graph); err != nil {
			return err
		}
		job.update(func(p *KnowledgeIndexProgress) {
			p.Phase = KnowledgeIndexPhaseIndexing
			p.FilesDone++
		})
		return nil
	})
	if err != nil {
		return session.KnowledgeBase{}, session.KnowledgeGraphSnapshot{}, fmt.Errorf("scan knowledge base: %w", err)
	}
	appendKnowledgeReferenceEdges(&graph)
	appendKnowledgeTestedByEdges(&graph, base.PreprocessProfile)
	appendKnowledgeImportEdges(&graph, base.PreprocessProfile)
	appendKnowledgeConfiguredByEdges(&graph, base.PreprocessProfile)
	appendKnowledgeCallEdges(&graph, base.PreprocessProfile)
	appendKnowledgeDocumentEdges(&graph, base.PreprocessProfile)
	appendKnowledgeSupersedesEdges(&graph, base.PreprocessProfile)
	graph.Aliases = buildKnowledgeEntityAliases(&graph)
	return base, graph, nil
}

// knowledgeMarkdownLink matches a Markdown inline link target.
var knowledgeMarkdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

// appendKnowledgeReferenceEdges adds deterministic, locally verifiable
// "references" edges from a section node to the file node it links to. The link
// target is parsed from the section's own chunk text, so no model assertion is
// involved and the relation survives incremental reuse because it is rebuilt
// over the whole assembled graph on every index.
func appendKnowledgeReferenceEdges(graph *session.KnowledgeGraphSnapshot) {
	if graph == nil {
		return
	}
	nodeByID := make(map[string]session.KnowledgeNode, len(graph.Nodes))
	fileByPath := make(map[string]string)
	fileByBase := make(map[string]string)
	for _, node := range graph.Nodes {
		nodeByID[node.ID] = node
		if node.Kind == "file" {
			fileByPath[node.NormalizedLabel] = node.ID
			base := normalizeKnowledgeLabel(filepath.Base(node.Label))
			if _, exists := fileByBase[base]; !exists {
				fileByBase[base] = node.ID
			}
		}
	}
	if len(fileByPath) == 0 && len(fileByBase) == 0 {
		return
	}
	sectionByChunk := make(map[string]string)
	for _, evidence := range graph.Evidence {
		if evidence.NodeID == "" {
			continue
		}
		if node, ok := nodeByID[evidence.NodeID]; ok && node.Kind == "section" {
			if _, exists := sectionByChunk[evidence.ChunkID]; !exists {
				sectionByChunk[evidence.ChunkID] = node.ID
			}
		}
	}
	if len(sectionByChunk) == 0 {
		return
	}
	existing := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existing[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	for _, chunk := range graph.Chunks {
		fromID, ok := sectionByChunk[chunk.ID]
		if !ok {
			continue
		}
		for _, match := range knowledgeMarkdownLink.FindAllStringSubmatch(chunk.Text, -1) {
			toID := resolveKnowledgeLinkTarget(match[1], fileByPath, fileByBase)
			if toID == "" || toID == fromID {
				continue
			}
			key := knowledgeEdgeKey(fromID, toID, "references")
			if _, duplicate := existing[key]; duplicate {
				continue
			}
			edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: fromID,
				ToNodeID: toID, RelationType: "references", Confidence: 1}
			graph.Edges = append(graph.Edges, edge)
			graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
				EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: chunk.StartLine, EndLine: chunk.EndLine, Confidence: 1})
			existing[key] = struct{}{}
		}
	}
}

func resolveKnowledgeLinkTarget(target string, fileByPath, fileByBase map[string]string) string {
	target = strings.TrimSpace(target)
	if target == "" || strings.HasPrefix(target, "/") || strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
		return ""
	}
	if index := strings.IndexAny(target, "#?"); index >= 0 {
		target = target[:index]
	}
	target = strings.TrimPrefix(strings.TrimSpace(target), "./")
	if target == "" {
		return ""
	}
	if id, ok := fileByPath[normalizeKnowledgeLabel(target)]; ok {
		return id
	}
	return fileByBase[normalizeKnowledgeLabel(filepath.Base(target))]
}

// appendKnowledgeTestedByEdges adds deterministic "tested_by" edges between a
// source file node and its conventional test file node (foo.go -> foo_test.go,
// foo.ts -> foo.test.ts, foo.py -> test_foo.py, ...). The relation is proven by
// the filename convention, so no model assertion is involved; because it is
// rebuilt over the whole assembled graph on every index it also survives
// incremental reuse even though its two endpoints live in different files.
func appendKnowledgeTestedByEdges(graph *session.KnowledgeGraphSnapshot, profile string) {
	if graph == nil {
		return
	}
	switch profile {
	case "code", "mixed":
	default:
		return
	}
	fileByPath := make(map[string]session.KnowledgeNode)
	for _, node := range graph.Nodes {
		if node.Kind == "file" {
			fileByPath[node.Label] = node
		}
	}
	if len(fileByPath) == 0 {
		return
	}
	chunkByID := make(map[string]session.KnowledgeChunk, len(graph.Chunks))
	for _, chunk := range graph.Chunks {
		chunkByID[chunk.ID] = chunk
	}
	// The test file's own file-node evidence anchors the edge; a file node always
	// carries evidence at its first chunk.
	testChunkByNode := make(map[string]session.KnowledgeChunk)
	for _, evidence := range graph.Evidence {
		if evidence.NodeID == "" {
			continue
		}
		if _, exists := testChunkByNode[evidence.NodeID]; exists {
			continue
		}
		if chunk, ok := chunkByID[evidence.ChunkID]; ok {
			testChunkByNode[evidence.NodeID] = chunk
		}
	}
	existing := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existing[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	for relPath, sourceNode := range fileByPath {
		for _, testPath := range knowledgeTestFilePaths(relPath) {
			testNode, ok := fileByPath[testPath]
			if !ok || testNode.ID == sourceNode.ID {
				continue
			}
			key := knowledgeEdgeKey(sourceNode.ID, testNode.ID, "tested_by")
			if _, duplicate := existing[key]; duplicate {
				continue
			}
			chunk, ok := testChunkByNode[testNode.ID]
			if !ok {
				continue
			}
			edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: sourceNode.ID,
				ToNodeID: testNode.ID, RelationType: "tested_by", Confidence: 1}
			graph.Edges = append(graph.Edges, edge)
			graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
				EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: chunk.StartLine, EndLine: chunk.EndLine, Confidence: 1})
			existing[key] = struct{}{}
		}
	}
}

// knowledgeTestFilePaths returns the conventional test file paths for a source
// path. A path that is already a test file has no test counterpart, and only the
// unambiguous per-language conventions are listed.
func knowledgeTestFilePaths(relPath string) []string {
	dir, base := path.Split(relPath)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" || ext == "" {
		return nil
	}
	if strings.HasSuffix(name, "_test") || strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".spec") || strings.HasPrefix(name, "test_") {
		return nil
	}
	switch ext {
	case ".go":
		return []string{dir + name + "_test.go"}
	case ".py":
		return []string{dir + "test_" + name + ".py", dir + name + "_test.py"}
	case ".ts":
		return []string{dir + name + ".test.ts", dir + name + ".spec.ts"}
	case ".tsx":
		return []string{dir + name + ".test.tsx", dir + name + ".spec.tsx"}
	case ".js":
		return []string{dir + name + ".test.js", dir + name + ".spec.js"}
	case ".jsx":
		return []string{dir + name + ".test.jsx", dir + name + ".spec.jsx"}
	default:
		return nil
	}
}

// buildKnowledgeEntityAliases registers the deterministic synonym that a file
// node's basename is the same entity as its relative-path label. Aliases that
// would collide with an existing node label or another alias are dropped so the
// snapshot stays unambiguous.
func buildKnowledgeEntityAliases(graph *session.KnowledgeGraphSnapshot) []session.KnowledgeEntityAlias {
	if graph == nil {
		return nil
	}
	labels := make(map[string]struct{}, len(graph.Nodes))
	for _, node := range graph.Nodes {
		labels[node.NormalizedLabel] = struct{}{}
	}
	used := make(map[string]struct{})
	aliases := make([]session.KnowledgeEntityAlias, 0)
	for _, node := range graph.Nodes {
		if node.Kind != "file" {
			continue
		}
		base := normalizeKnowledgeLabel(filepath.Base(node.Label))
		if base == "" || base == node.NormalizedLabel {
			continue
		}
		if _, isNode := labels[base]; isNode {
			continue
		}
		if _, duplicate := used[base]; duplicate {
			continue
		}
		used[base] = struct{}{}
		aliases = append(aliases, session.KnowledgeEntityAlias{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
			NormalizedAlias: base, NodeID: node.ID})
	}
	return aliases
}

func (s *KnowledgeBaseService) indexSourceFile(source *knowledgeSourceFile, graph *session.KnowledgeGraphSnapshot) error {
	if source == nil || graph == nil {
		return nil
	}
	fileID := session.GenerateID()
	file := session.KnowledgeFile{ID: fileID, SnapshotID: graph.Snapshot.ID, RelativePath: source.relativePath,
		ContentSHA256: knowledgeSHA256(source.data), ByteSize: int64(len(source.data)), MediaType: source.mediaType,
		Title: knowledgeFileTitle(source.relativePath, source.text), Status: "indexed"}
	graph.Files = append(graph.Files, file)

	chunks := knowledgeChunks(graph.Snapshot.ID, fileID, source.text, s.policy.MaxChunkBytes)
	if len(chunks) == 0 {
		return nil
	}
	graph.Chunks = append(graph.Chunks, chunks...)
	fileNode := session.KnowledgeNode{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, Kind: "file", Label: source.relativePath,
		NormalizedLabel: normalizeKnowledgeLabel(source.relativePath), Summary: file.Title}
	graph.Nodes = append(graph.Nodes, fileNode)
	graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
		NodeID: fileNode.ID, ChunkID: chunks[0].ID, StartLine: chunks[0].StartLine, EndLine: chunks[0].EndLine, Confidence: 1})

	// A single source file may repeat a heading or a code-comment label (for
	// example two "## Example" sections, or repeated "# TODO" comments in a
	// shell/YAML file). knowledge_nodes is unique on
	// (snapshot_id, kind, normalized_label), so identical labels must collapse
	// into one node; every occurrence still contributes its own evidence and the
	// file's single edge to that node is created once. A code declaration is a
	// "declares" edge (a symbol declaration), while a heading stays "contains".
	nodeIDs := make(map[string]string)
	edgeIDs := make(map[string]string)
	for _, marker := range knowledgeMarkers(source.relativePath, source.text) {
		chunk, ok := knowledgeChunkForLine(chunks, marker.line)
		if !ok {
			continue
		}
		normalized := normalizeKnowledgeLabel(source.relativePath + "\x00" + marker.label)
		nodeID, exists := nodeIDs[marker.kind+"\x00"+normalized]
		if !exists {
			node := session.KnowledgeNode{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, Kind: marker.kind, Label: marker.label,
				NormalizedLabel: normalized, Summary: marker.label}
			graph.Nodes = append(graph.Nodes, node)
			nodeID = node.ID
			nodeIDs[marker.kind+"\x00"+normalized] = nodeID
		}
		graph.Evidence = append(graph.Evidence,
			session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, NodeID: nodeID, ChunkID: chunk.ID, StartLine: marker.line, EndLine: marker.line, Confidence: 1})

		relation := "contains"
		if marker.kind == "symbol" {
			relation = "declares"
		}
		edgeKey := fileNode.ID + "\x00" + nodeID + "\x00" + relation
		edgeID, exists := edgeIDs[edgeKey]
		if !exists {
			edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: fileNode.ID,
				ToNodeID: nodeID, RelationType: relation, Confidence: 1}
			graph.Edges = append(graph.Edges, edge)
			edgeID = edge.ID
			edgeIDs[edgeKey] = edgeID
		}
		graph.Evidence = append(graph.Evidence,
			session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, EdgeID: edgeID, ChunkID: chunk.ID, StartLine: marker.line, EndLine: marker.line, Confidence: 1})
	}
	return nil
}

type knowledgeSourceFile struct {
	relativePath string
	data         []byte
	text         string
	mediaType    string
}

func resolveKnowledgeBaseRoot(base session.KnowledgeBase) (string, error) {
	root, err := filepath.EvalSymlinks(base.RootDir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", session.ErrKnowledgeBaseRootUnavailable, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", session.ErrKnowledgeBaseRootUnavailable, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: not a directory", session.ErrKnowledgeBaseRootUnavailable)
	}
	return root, nil
}

func (s *KnowledgeBaseService) readIndexableKnowledgeFile(ctx context.Context, root, path string, entry fs.DirEntry) (*knowledgeSourceFile, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	info, err := entry.Info()
	if err != nil {
		return nil, "", err
	}
	if info.Size() > s.policy.MaxFileBytes {
		return nil, "too_large", nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "unreadable", nil
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, "path_escaped_root", fmt.Errorf("%w: path escaped root", session.ErrKnowledgeBaseRootUnavailable)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "unreadable", nil
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return nil, "binary_or_non_utf8", nil
	}
	text := strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff")
	if strings.TrimSpace(text) == "" {
		return nil, "empty", nil
	}
	return &knowledgeSourceFile{relativePath: filepath.ToSlash(rel), data: data, text: text, mediaType: knowledgeMediaType(path)}, "", nil
}

func (s *KnowledgeBaseService) Query(ctx context.Context, knowledgeBaseID, query string, limit int) (session.KnowledgeGraphQuery, error) {
	if s == nil {
		return session.KnowledgeGraphQuery{}, fmt.Errorf("knowledge base service is nil")
	}
	return session.QueryKnowledgeGraph(ctx, s.sessionDir, knowledgeBaseID, query, limit)
}

func knowledgeBaseIgnoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", ".mothx", "node_modules", "vendor", "dist", "build", "coverage", ".next", ".vite":
		return true
	default:
		return false
	}
}

func knowledgeRelativePath(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// knowledgeIgnoreRules merges the knowledge-base's explicit ignoreGlobs with
// the suggested patterns read from the root .gitignore/.mothxignore files. The
// suggestion files are read-only inputs: they are never rewritten.
type knowledgeIgnoreRules struct {
	globs        []string
	filePatterns []knowledgeIgnorePattern
}

type knowledgeIgnorePattern struct {
	pattern string
	negated bool
	source  string
}

func newKnowledgeIgnoreRules(base session.KnowledgeBase, root string) knowledgeIgnoreRules {
	rules := knowledgeIgnoreRules{globs: append([]string(nil), base.IgnoreGlobs...)}
	rules.filePatterns = append(rules.filePatterns, readKnowledgeIgnoreFile(root, ".gitignore", "gitignore")...)
	rules.filePatterns = append(rules.filePatterns, readKnowledgeIgnoreFile(root, ".mothxignore", "mothxignore")...)
	return rules
}

func readKnowledgeIgnoreFile(root, name, source string) []knowledgeIgnorePattern {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return nil
	}
	patterns := make([]knowledgeIgnorePattern, 0)
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		negated := false
		if strings.HasPrefix(line, "!") {
			negated = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		}
		if line == "" {
			continue
		}
		patterns = append(patterns, knowledgeIgnorePattern{pattern: line, negated: negated, source: source})
	}
	return patterns
}

// ignored reports whether rel is ignored and, if so, the diagnostic reason.
func (r knowledgeIgnoreRules) ignored(rel string, _ bool) (bool, string) {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "./")
	if rel == "" {
		return false, ""
	}
	for _, glob := range r.globs {
		if knowledgeMatchGlob(glob, rel) {
			return true, "ignore_glob:" + glob
		}
	}
	matched, reason := false, ""
	for _, pattern := range r.filePatterns {
		if !knowledgeMatchGlob(pattern.pattern, rel) {
			continue
		}
		if pattern.negated {
			matched, reason = false, ""
			continue
		}
		matched, reason = true, pattern.source+":"+pattern.pattern
	}
	return matched, reason
}

// knowledgeMatchGlob matches a gitignore-style pattern against a slash-separated
// relative path. It supports "*", "?", and "**" (any number of segments). A
// pattern without a slash matches at any path depth.
func knowledgeMatchGlob(pattern, target string) bool {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	pattern = strings.TrimPrefix(pattern, "./")
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" || target == "" {
		return false
	}
	target = strings.TrimPrefix(target, "./")
	segments := strings.Split(target, "/")
	if !strings.Contains(pattern, "/") {
		for _, segment := range segments {
			if ok, _ := path.Match(pattern, segment); ok {
				return true
			}
		}
		return false
	}
	return knowledgeMatchGlobSegments(strings.Split(pattern, "/"), segments)
}

func knowledgeMatchGlobSegments(pattern, segments []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(segments); i++ {
				if knowledgeMatchGlobSegments(pattern[1:], segments[i:]) {
					return true
				}
			}
			return false
		}
		if len(segments) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], segments[0]); !ok {
			return false
		}
		pattern, segments = pattern[1:], segments[1:]
	}
	return len(segments) == 0
}

func knowledgeBaseAllowedFile(profile, path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	text := map[string]bool{".md": true, ".mdx": true, ".txt": true, ".rst": true, ".adoc": true, ".html": true, ".htm": true}
	code := map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".py": true, ".java": true, ".rs": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true, ".sql": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".graphql": true, ".gql": true, ".sh": true}
	switch profile {
	case "documents":
		return text[extension]
	case "code":
		return code[extension] || extension == ".md"
	case "notes":
		return extension == ".md" || extension == ".txt"
	case "mixed":
		return text[extension] || code[extension]
	default:
		return false
	}
}

func knowledgeMediaType(path string) string {
	if mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); mediaType != "" {
		return strings.Split(mediaType, ";")[0]
	}
	return "text/plain"
}

func knowledgeFileTitle(relativePath, text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			if title := strings.TrimSpace(strings.TrimLeft(line, "#")); title != "" {
				return title
			}
		}
	}
	return filepath.Base(relativePath)
}

func knowledgeSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func knowledgeChunks(snapshotID, fileID, text string, maxBytes int) []session.KnowledgeChunk {
	lines := strings.Split(text, "\n")
	chunks := make([]session.KnowledgeChunk, 0, len(lines)/16+1)
	start, size, ordinal := 1, 0, 0
	var builder strings.Builder
	flush := func(end int) {
		value := strings.TrimSpace(builder.String())
		if value == "" {
			return
		}
		chunks = append(chunks, session.KnowledgeChunk{ID: session.GenerateID(), SnapshotID: snapshotID, FileID: fileID,
			Ordinal: ordinal, Text: value, StartLine: start, EndLine: end, ContentSHA256: knowledgeSHA256([]byte(value))})
		ordinal++
		builder.Reset()
		size = 0
	}
	for index, line := range lines {
		lineBytes := len(line) + 1
		if size > 0 && size+lineBytes > maxBytes {
			flush(index)
			start = index + 1
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
		size += lineBytes
	}
	flush(len(lines))
	return chunks
}

type knowledgeMarker struct {
	kind  string
	label string
	line  int
}

var knowledgeCodeDeclaration = regexp.MustCompile(`^(?:func|type|var|const|class|interface|struct|enum|function)\s+([A-Za-z_][A-Za-z0-9_]*)`)

func knowledgeMarkers(relativePath, text string) []knowledgeMarker {
	markers := make([]knowledgeMarker, 0)
	for index, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if label := strings.TrimSpace(strings.TrimLeft(trimmed, "#")); label != "" {
				markers = append(markers, knowledgeMarker{kind: "section", label: label, line: index + 1})
			}
			continue
		}
		if match := knowledgeCodeDeclaration.FindStringSubmatch(trimmed); len(match) == 2 {
			markers = append(markers, knowledgeMarker{kind: "symbol", label: match[1], line: index + 1})
		}
	}
	sort.SliceStable(markers, func(i, j int) bool { return markers[i].line < markers[j].line })
	return markers
}

func knowledgeChunkForLine(chunks []session.KnowledgeChunk, line int) (session.KnowledgeChunk, bool) {
	for _, chunk := range chunks {
		if line >= chunk.StartLine && line <= chunk.EndLine {
			return chunk, true
		}
	}
	return session.KnowledgeChunk{}, false
}

func normalizeKnowledgeLabel(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
