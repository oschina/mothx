package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

func TestKnowledgeBaseIndexerStoresQueryableGraphSnapshot(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	content := "# Authentication Guide\n\nUse bearer tokens for API requests.\n\n## Rotation\n\nRotate tokens every ninety days.\n"
	if err := os.WriteFile(filepath.Join(source, "docs", "auth.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Product docs", RootDir: source, PreprocessProfile: "documents", Provider: "test", Model: "test-model", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(context.Background(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "completed" || snapshot.FileCount != 1 || snapshot.ChunkCount == 0 || snapshot.NodeCount < 2 || snapshot.EdgeCount < 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	stored, err := session.GetKnowledgeBase(context.Background(), root, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ActiveSnapshotID != snapshot.ID {
		t.Fatalf("active snapshot = %q, want %q", stored.ActiveSnapshotID, snapshot.ID)
	}
	result, err := service.Query(context.Background(), base.ID, "bearer token rotation", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chunks) == 0 || len(result.Nodes) == 0 || len(result.Edges) == 0 {
		t.Fatalf("query = %#v, want chunks, nodes, and edges", result)
	}
	if result.Chunks[0].RelativePath != "docs/auth.md" {
		t.Fatalf("query chunk path = %q, want docs/auth.md", result.Chunks[0].RelativePath)
	}
	if err := session.DeleteKnowledgeBase(context.Background(), root, base.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Query(context.Background(), base.ID, "bearer", 4); !errors.Is(err, session.ErrKnowledgeBaseNotFound) {
		t.Fatalf("query after delete = %v, want ErrKnowledgeBaseNotFound", err)
	}
	if _, err := os.Stat(filepath.Join(source, "docs", "auth.md")); err != nil {
		t.Fatalf("delete knowledge base removed source material: %v", err)
	}
}

// TestKnowledgeBaseIndexDeduplicatesRepeatedLabels pins the constraint that one
// source file may repeat a heading or a code-comment label without violating
// knowledge_nodes' (snapshot_id, kind, normalized_label) uniqueness. Repeated
// labels collapse into one node, every occurrence keeps its own evidence, the
// file's contains edge is created once, and the graph projection returns each
// node exactly once.
func TestKnowledgeBaseIndexDeduplicatesRepeatedLabels(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	content := "# Guide\n\n## Example\n\nFirst example body.\n\n## Example\n\nSecond example body.\n"
	if err := os.WriteFile(filepath.Join(source, "guide.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(context.Background(), base.ID)
	if err != nil {
		t.Fatalf("index with repeated labels failed: %v", err)
	}
	if snapshot.Status != "completed" || snapshot.FileCount != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	result, err := service.Query(context.Background(), base.ID, "example body", 8)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, node := range result.Nodes {
		seen[node.ID]++
	}
	if len(seen) == 0 {
		t.Fatalf("query returned no nodes: %#v", result)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("node %s projected %d times, want once", id, count)
		}
	}
	// The repeated heading is a single entity, not two nodes.
	examples := 0
	for _, node := range result.Nodes {
		if node.Kind == "section" && node.Label == "Example" {
			examples++
		}
	}
	if examples != 1 {
		t.Fatalf("Example section projected %d times, want 1", examples)
	}
}

// TestKnowledgeBaseIndexDeduplicatesCodeCommentLabels covers the code profile,
// where repeated "# TODO"-style comments would otherwise collide on the node
// uniqueness index and fail the whole scan.
func TestKnowledgeBaseIndexDeduplicatesCodeCommentLabels(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	content := "#!/bin/sh\n\n# TODO\n\necho one\n\n# TODO\n\necho two\n"
	if err := os.WriteFile(filepath.Join(source, "run.sh"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Scripts", RootDir: source, PreprocessProfile: "code", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(context.Background(), base.ID)
	if err != nil {
		t.Fatalf("index with repeated code-comment labels failed: %v", err)
	}
	if snapshot.Status != "completed" || snapshot.FileCount != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPrepareKnowledgeContextBuildsBoundedCitedReference(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "runtime.md"), []byte("# Runtime\n\nThe shared runtime owns durable run lifecycle and graph evidence.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Runtime docs", RootDir: source, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	capsules, err := PrepareKnowledgeContext(t.Context(), sessionDir, "durable graph evidence", []KnowledgeBaseReference{{KnowledgeBaseID: base.ID, Required: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(capsules) != 1 || capsules[0].SnapshotID == "" || len(capsules[0].Citations) == 0 {
		t.Fatalf("capsules = %#v", capsules)
	}
	if got := capsules[0].Citations[0].RelativePath; got != "runtime.md" {
		t.Fatalf("citation path = %q, want runtime.md", got)
	}
	prompt := formatKnowledgeCapsules(capsules)
	for _, want := range []string{"untrusted reference data", "runtime.md", "durable run lifecycle"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("knowledge prompt %q missing %q", prompt, want)
		}
	}
	if _, err := PrepareKnowledgeContext(t.Context(), sessionDir, "durable", []KnowledgeBaseReference{{KnowledgeBaseID: "missing", Required: true}}); !errors.Is(err, session.ErrKnowledgeBaseNotFound) {
		t.Fatalf("required missing reference = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestKnowledgeBaseIndexerIgnoresSymlinkAndBuildOutput(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("# Secret\nnot indexable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "dist", "generated.md"), []byte("# Generated\nignore me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.md"), []byte("# Included\nkeep me"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "linked.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Notes", RootDir: source, PreprocessProfile: "mixed", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(context.Background(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FileCount != 1 {
		t.Fatalf("file count = %d, want only notes.md", snapshot.FileCount)
	}
}

func TestKnowledgeIndexerAddsOnlyEvidenceVerifiedCoMentionEdges(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	content := "# Alpha\n\nAlpha and Beta are both discussed in this architecture note.\n\n## Beta\n\nBeta is documented alongside Alpha.\n"
	if err := os.WriteFile(filepath.Join(source, "architecture.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	model := &provider.Model{ID: "indexer-model", Name: "Indexer model"}
	indexer := &knowledgeIndexerTestProvider{model: model}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Architecture", RootDir: source, PreprocessProfile: "documents",
		Provider: "indexer", Model: model.ID, Mode: ModeYolo, Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{SessionDir: sessionDir}
	service, err := NewKnowledgeBaseServiceWithProviderFactory(sessionDir, DefaultKnowledgeBaseIndexPolicy(), settings,
		func(*config.Settings, string, string) (provider.Provider, *provider.Model, error) {
			return indexer, model, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if indexer.calls != 1 || snapshot.EdgeCount < 3 {
		t.Fatalf("indexer calls=%d snapshot=%#v", indexer.calls, snapshot)
	}
	for _, name := range indexer.toolNames {
		if name != "read" && name != "ls" && name != "grep" && name != "find" {
			t.Fatalf("indexer received non-read-only tool %q", name)
		}
	}
	graph, err := service.Query(t.Context(), base.ID, "Alpha Beta", 8)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, edge := range graph.Edges {
		if edge.RelationType == "co_mentions" {
			found = true
			if edge.Confidence != 1 {
				t.Fatalf("co_mentions confidence = %v", edge.Confidence)
			}
		}
	}
	if !found {
		t.Fatalf("graph edges = %#v, want verified co_mentions edge", graph.Edges)
	}
	run, err := session.GetSessionRun(sessionDir, snapshot.RunID)
	if err != nil || run == nil || run.Model != model.ID || run.Status != string(RunStateCompleted) {
		t.Fatalf("index Run = %#v, err=%v", run, err)
	}
	reused, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != snapshot.ID || indexer.calls != 1 {
		t.Fatalf("unchanged scan snapshot=%#v indexer calls=%d, want reuse without a second model call", reused, indexer.calls)
	}
	events, err := session.ListSessionRunEvents(sessionDir, knowledgeLibrarianSessionID(base))
	if err != nil {
		t.Fatal(err)
	}
	var reusedEvent bool
	for _, event := range events {
		if event.EventType == "knowledge_snapshot_reused" && event.RunID != snapshot.RunID {
			reusedEvent = true
		}
	}
	if !reusedEvent {
		t.Fatalf("reuse event missing from %#v", events)
	}
	changedContent := content + "\n## Gamma\n\nGamma is a new indexed section.\n"
	if err := os.WriteFile(filepath.Join(source, "architecture.md"), []byte(changedContent), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ID == snapshot.ID || indexer.calls != 2 {
		t.Fatalf("changed scan snapshot=%#v indexer calls=%d, want a new snapshot and model pass", changed, indexer.calls)
	}
}

func TestKnowledgeIndexerRejectsUnsupportedModelLinks(t *testing.T) {
	graph := &session.KnowledgeGraphSnapshot{
		Snapshot: session.KnowledgeSnapshot{ID: "snapshot"},
		Chunks:   []session.KnowledgeChunk{{ID: "chunk", SnapshotID: "snapshot", StartLine: 4, EndLine: 5, Text: "Alpha is documented here."}},
		Nodes: []session.KnowledgeNode{
			{ID: "alpha", SnapshotID: "snapshot", Kind: "section", Label: "Alpha"},
			{ID: "beta", SnapshotID: "snapshot", Kind: "section", Label: "Beta"},
		},
	}
	appendVerifiedCoMentionEdges(graph, []indexerLink{
		{FromNodeID: "alpha", ToNodeID: "beta", ChunkID: "chunk", StartLine: 4, EndLine: 5}, // Beta has no source evidence.
		{FromNodeID: "alpha", ToNodeID: "missing", ChunkID: "chunk", StartLine: 4, EndLine: 5},
		{FromNodeID: "alpha", ToNodeID: "beta", ChunkID: "chunk", StartLine: 2, EndLine: 5}, // span escapes the chunk.
	})
	if len(graph.Edges) != 0 || len(graph.Evidence) != 0 {
		t.Fatalf("unsupported model links became graph facts: %#v / %#v", graph.Edges, graph.Evidence)
	}
}

// TestKnowledgeIndexerAddsOnlyGroundedCandidateNodes pins that a model-proposed
// entity is stored as a candidate, never a fact: a label that literally occurs in
// the cited chunk gets node evidence and can surface in a query, while an
// ungrounded label is persisted evidence-free (so default queries never return
// it). Unsupported kinds, fact-label shadows, and duplicates are dropped.
func TestKnowledgeIndexerAddsOnlyGroundedCandidateNodes(t *testing.T) {
	graph := &session.KnowledgeGraphSnapshot{
		Snapshot: session.KnowledgeSnapshot{ID: "snapshot"},
		Chunks: []session.KnowledgeChunk{{ID: "chunk", SnapshotID: "snapshot", StartLine: 4, EndLine: 8,
			Text: "Retrieval augmented generation improves answer grounding."}},
		Nodes: []session.KnowledgeNode{{ID: "alpha", SnapshotID: "snapshot", Kind: "section", Label: "Alpha", NormalizedLabel: "alpha"}},
	}
	appendVerifiedCandidateNodes(graph, []indexerEntity{
		{Label: "Retrieval augmented generation", Kind: "concept", Confidence: 0.7, ChunkID: "chunk", StartLine: 4, EndLine: 4},
		{Label: "Hallucination", Kind: "concept", Confidence: 0.4, ChunkID: "chunk", StartLine: 4, EndLine: 4},
		{Label: "Unsupported", Kind: "unknown-kind", Confidence: 0.9, ChunkID: "chunk", StartLine: 4, EndLine: 4},
		{Label: "Alpha", Kind: "concept", Confidence: 0.9, ChunkID: "chunk", StartLine: 4, EndLine: 4},
		{Label: "Retrieval augmented generation", Kind: "concept", Confidence: 0.7, ChunkID: "chunk", StartLine: 4, EndLine: 4},
		{Label: "X", Kind: "concept", Confidence: 0.5, ChunkID: "chunk", StartLine: 4, EndLine: 4},
	})
	candidates := make(map[string]session.KnowledgeNode)
	for _, node := range graph.Nodes {
		if node.Status == session.KnowledgeNodeStatusCandidate {
			candidates[node.Label] = node
		}
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate nodes = %#v, want exactly the grounded and the ungrounded entity", candidates)
	}
	grounded, ok := candidates["Retrieval augmented generation"]
	if !ok || grounded.Confidence != 0.7 {
		t.Fatalf("grounded candidate = %#v, want confidence 0.7", grounded)
	}
	ungrounded, ok := candidates["Hallucination"]
	if !ok || ungrounded.Confidence != 0.4 {
		t.Fatalf("ungrounded candidate = %#v, want confidence 0.4", ungrounded)
	}
	groundedEvidence, ungroundedEvidence := 0, 0
	for _, item := range graph.Evidence {
		switch item.NodeID {
		case grounded.ID:
			groundedEvidence++
		case ungrounded.ID:
			ungroundedEvidence++
		}
	}
	if groundedEvidence == 0 {
		t.Fatalf("grounded candidate has no evidence: %#v", graph.Evidence)
	}
	if ungroundedEvidence != 0 {
		t.Fatalf("ungrounded candidate gained evidence: %#v", graph.Evidence)
	}
	if _, exists := candidates["Unsupported"]; exists {
		t.Fatal("unsupported entity kind was stored")
	}
	if _, exists := candidates["Alpha"]; exists {
		t.Fatal("candidate shadowed a deterministic node label")
	}
	if graph.Nodes[0].Status != "" {
		t.Fatalf("deterministic node was mutated: %#v", graph.Nodes[0])
	}
}

func TestParseIndexerResponseBoundsEntities(t *testing.T) {
	entities := make([]indexerEntity, maxKnowledgeIndexerEntities+1)
	for i := range entities {
		entities[i] = indexerEntity{Label: "label", Kind: "concept"}
	}
	payload, _ := json.Marshal(indexerResponse{Entities: entities})
	if _, err := parseIndexerResponse(string(payload)); err == nil {
		t.Fatal("parseIndexerResponse accepted too many entities")
	}
	if _, err := parseIndexerResponse(`{"links":[],"entities":[{"label":"Retrieval","kind":"concept","confidence":0.5,"chunkId":"c1","startLine":1,"endLine":1}]}`); err != nil {
		t.Fatalf("parseIndexerResponse rejected a valid entity: %v", err)
	}
}

func TestKnowledgeIndexerStoresGroundedCandidateNodes(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	content := "# Retrieval\n\nRetrieval augmented generation improves answer grounding.\n\n## Pipeline\n\nA pipeline retrieves then generates an answer.\n"
	if err := os.WriteFile(filepath.Join(source, "retrieval.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	model := &provider.Model{ID: "indexer-model", Name: "Indexer model"}
	indexer := &knowledgeIndexerTestProvider{model: model, entityLabel: "Retrieval augmented generation"}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Retrieval", RootDir: source, PreprocessProfile: "documents",
		Provider: "indexer", Model: model.ID, Mode: ModeYolo, Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{SessionDir: sessionDir}
	service, err := NewKnowledgeBaseServiceWithProviderFactory(sessionDir, DefaultKnowledgeBaseIndexPolicy(), settings,
		func(*config.Settings, string, string) (provider.Provider, *provider.Model, error) {
			return indexer, model, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "retrieval augmented generation", 8)
	if err != nil {
		t.Fatal(err)
	}
	var candidate *session.KnowledgeNode
	for i := range graph.Nodes {
		if graph.Nodes[i].Label == "Retrieval augmented generation" {
			candidate = &graph.Nodes[i]
		}
	}
	if candidate == nil || candidate.Status != session.KnowledgeNodeStatusCandidate || candidate.Confidence >= 1 {
		t.Fatalf("grounded candidate not projected: %#v", graph.Nodes)
	}
	var noted bool
	for _, uncertainty := range graph.Uncertainties {
		if uncertainty.Kind == "candidate_node" {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("query did not flag the included candidate: %#v", graph.Uncertainties)
	}
}

func TestKnowledgeIndexerClonesUnchangedFileGraphWhenAnotherFileChanges(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "stable.md"), []byte("# Stable\n\nStable evidence remains available.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "changed.md"), []byte("# Changed\n\nFirst revision.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Incremental docs", RootDir: source, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "changed.md"), []byte("# Changed\n\nSecond revision adds new material.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.FileCount != 2 {
		t.Fatalf("incremental snapshot = %#v, first=%#v", second, first)
	}
	stable, err := service.Query(t.Context(), base.ID, "Stable evidence", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(stable.Chunks) == 0 || stable.Chunks[0].RelativePath != "stable.md" || len(stable.Nodes) == 0 {
		t.Fatalf("unchanged file graph was not present in successor snapshot: %#v", stable)
	}
}

func TestKnowledgeIndexerRevalidatesReusePlanAgainstCurrentContent(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	path := filepath.Join(source, "changing.md")
	if err := os.WriteFile(path, []byte("# First\n\nOld content.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "TOCTOU docs", RootDir: source, PreprocessProfile: "documents", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	current, err := session.GetKnowledgeBase(t.Context(), sessionDir, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := service.scanFileManifest(t.Context(), current, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := session.PrepareKnowledgeGraphReusePlan(t.Context(), sessionDir, base.ID, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("reuse plan files = %d, want 1", len(plan.Files))
	}
	if err := os.WriteFile(path, []byte("# Second\n\nNew content after planning.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, graph, err := service.buildGraph(t.Context(), base.ID, "run-revalidate", plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Chunks) == 0 || !strings.Contains(graph.Chunks[0].Text, "New content after planning") {
		t.Fatalf("stale reuse plan was accepted: %#v", graph.Chunks)
	}

	// Even when the bytes still match, a plan from an older configuration
	// generation must not contribute graph data to a newly configured base.
	if err := os.WriteFile(path, []byte("# First\n\nOld content.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := plan.Files["changing.md"]
	stale.File.Title = "stale-plan-sentinel"
	plan.Files["changing.md"] = stale
	updatedSpec := current.KnowledgeBaseSpec
	updatedSpec.Name = "TOCTOU docs updated"
	if _, err := session.UpdateKnowledgeBase(t.Context(), sessionDir, base.ID, updatedSpec); err != nil {
		t.Fatal(err)
	}
	_, graph, err = service.buildGraph(t.Context(), base.ID, "run-new-config", plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Files) != 1 || graph.Files[0].Title == "stale-plan-sentinel" {
		t.Fatalf("old-configuration reuse plan was accepted: %#v", graph.Files)
	}
}

type knowledgeIndexerTestProvider struct {
	model       *provider.Model
	calls       int
	toolNames   []string
	entityLabel string
	entityKind  string
}

func (p *knowledgeIndexerTestProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.calls++
	p.toolNames = p.toolNames[:0]
	for _, tool := range params.Tools {
		p.toolNames = append(p.toolNames, tool.Name)
	}
	var input struct {
		Nodes  []indexerPromptNode  `json:"nodes"`
		Chunks []indexerPromptChunk `json:"chunks"`
	}
	for _, message := range params.Messages {
		content := message.Content
		for _, block := range message.Contents {
			if block.Type == "text" {
				content += block.Text
			}
		}
		start := strings.Index(content, "<untrusted-index-input>\n")
		end := strings.Index(content, "\n</untrusted-index-input>")
		if start < 0 || end < 0 || end <= start {
			continue
		}
		data := content[start+len("<untrusted-index-input>\n") : end]
		_ = json.Unmarshal([]byte(data), &input)
	}
	response := `{"links":[]}`
	if len(input.Nodes) >= 2 && len(input.Chunks) > 0 {
		chunk := input.Chunks[0]
		parsed := indexerResponse{Links: []indexerLink{{
			FromNodeID: input.Nodes[0].ID, ToNodeID: input.Nodes[1].ID, ChunkID: chunk.ID,
			StartLine: chunk.StartLine, EndLine: chunk.EndLine,
		}}}
		if p.entityLabel != "" {
			kind := p.entityKind
			if kind == "" {
				kind = "concept"
			}
			parsed.Entities = []indexerEntity{{Label: p.entityLabel, Kind: kind, Confidence: 0.6,
				ChunkID: chunk.ID, StartLine: chunk.StartLine, EndLine: chunk.EndLine}}
		}
		responseBytes, _ := json.Marshal(parsed)
		response = string(responseBytes)
	}
	events := make(chan provider.StreamEvent, 3)
	go func() {
		defer close(events)
		select {
		case <-ctx.Done():
			events <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
		case events <- provider.StreamEvent{Type: provider.StreamStart}:
		}
		select {
		case <-ctx.Done():
			events <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
			return
		case events <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: response}:
		}
		events <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "stop"}
	}()
	return events
}

func (p *knowledgeIndexerTestProvider) Name() string              { return "indexer" }
func (p *knowledgeIndexerTestProvider) API() string               { return "openai-chat" }
func (p *knowledgeIndexerTestProvider) Models() []*provider.Model { return []*provider.Model{p.model} }
func (p *knowledgeIndexerTestProvider) GetModel(id string) *provider.Model {
	if p.model != nil && p.model.ID == id {
		return p.model
	}
	return nil
}

func TestKnowledgeLibrarianUsesDedicatedAgentSessionAndDurableRun(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "runtime.md"), []byte("# Runtime\n\nThe runtime owns durable Runs and controls graph evidence.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	model := &provider.Model{ID: "librarian-model", Name: "Librarian model"}
	mock := provider.NewMockProvider("librarian", []*provider.Model{model}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "The shared runtime owns durable Runs. See runtime.md lines 1-3."},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Runtime docs", RootDir: source, PreprocessProfile: "documents",
		Provider: "librarian", Model: model.ID, Mode: ModeYolo, Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(sessionDir, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "who owns durable run", 6)
	if err != nil {
		t.Fatal(err)
	}
	callerWorkDir := t.TempDir()
	callerManager := session.New(callerWorkDir, sessionDir)
	if err := callerManager.Init(); err != nil {
		t.Fatal(err)
	}
	caller, err := AttachSessionResources(AttachedResources{
		ID: callerManager.GetHeader().ID, Source: SourceACP, EntrySource: SourceACP,
		WorkDir: callerWorkDir, Manager: callerManager, Registry: tools.NewRegistry(callerWorkDir, nil),
		Providers: ProviderCatalog{"librarian": mock}, Settings: &config.Settings{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	if err := caller.ConfigureSession(mock, "librarian", model, ModeYolo, ""); err != nil {
		t.Fatal(err)
	}
	capsule, err := service.LibrarianCapsule(t.Context(), caller, base, graph, "who owns durable run", maxKnowledgeCapsuleChars)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capsule.Text, "owns durable Runs") || len(capsule.Citations) == 0 {
		t.Fatalf("librarian capsule = %#v", capsule)
	}
	if calls := mock.GetCallCount(); calls != 1 {
		t.Fatalf("librarian provider calls = %d, want 1", calls)
	}
	librarianSessionID := knowledgeLibrarianSessionID(base)
	runs, err := session.ListSessionRuns(sessionDir, librarianSessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var librarianRunFound bool
	for _, run := range runs {
		if run.ID == "" || run.SessionID == callerManager.GetHeader().ID {
			t.Fatalf("librarian Run incorrectly used caller session or has no identity: %#v", run)
		}
		if run.Model == model.ID {
			librarianRunFound = run.Status == string(RunStateCompleted)
		}
	}
	if !librarianRunFound {
		t.Fatalf("librarian durable Run missing from %#v", runs)
	}
}

// TestKnowledgeBaseQueryMatchesChineseEvidence pins the CJK bigram FTS
// contract end to end: indexing a Chinese document and querying Chinese
// phrases must return the cited chunk, which the default unicode61 tokenizer
// cannot do without the dao-side bigram rewrite.
func TestKnowledgeBaseQueryMatchesChineseEvidence(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	content := "# 架构说明\n\n知识库是一个可重建的图谱索引系统。\n\n## 调度\n\n定时扫描复用 canonical Run 生命周期。\n"
	if err := os.WriteFile(filepath.Join(source, "架构.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "产品文档", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(context.Background(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "completed" || snapshot.FileCount != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, query := range []string{"知识库", "图谱索引", "索引系统", "定时扫描"} {
		result, err := service.Query(context.Background(), base.ID, query, 4)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if len(result.Chunks) == 0 {
			t.Fatalf("query %q returned no chunks; CJK evidence must be findable", query)
		}
		if result.Chunks[0].RelativePath != "架构.md" {
			t.Fatalf("query %q chunk path = %q", query, result.Chunks[0].RelativePath)
		}
		if !strings.Contains(result.Chunks[0].Text, "知识库") {
			t.Fatalf("query %q returned original chunk text = %q", query, result.Chunks[0].Text)
		}
	}
	// The section nodes keep working for Chinese headings too.
	result, err := service.Query(context.Background(), base.ID, "架构说明", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chunks) == 0 {
		t.Fatalf("heading query returned no chunks")
	}
	// An absent phrase stays empty instead of matching everything.
	absent, err := service.Query(context.Background(), base.ID, "向量数据库", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(absent.Chunks) != 0 {
		t.Fatalf("absent phrase matched %d chunks", len(absent.Chunks))
	}
}

// TestKnowledgeBaseServiceSetSettings pins that a cached service follows a
// settings change in place: the next scan resolves the indexer provider through
// the refreshed snapshot, and the same instance (which owns the background
// index-job registry) keeps serving.
func TestKnowledgeBaseServiceSetSettings(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "notes.md"), []byte("# Notes\n\nAlpha is documented here.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	model := &provider.Model{ID: "indexer-model", Name: "Indexer model"}
	indexer := &knowledgeIndexerTestProvider{model: model}
	var seen []string
	service, err := NewKnowledgeBaseServiceWithProviderFactory(sessionDir, DefaultKnowledgeBaseIndexPolicy(),
		&config.Settings{SessionDir: sessionDir, DefaultModel: "first"},
		func(settings *config.Settings, _, _ string) (provider.Provider, *provider.Model, error) {
			seen = append(seen, settings.DefaultModel)
			return indexer, model, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Notes", RootDir: source, PreprocessProfile: "documents",
		Provider: "indexer", Model: model.ID, Mode: ModeYolo, Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.resolveKnowledgeIndexer(base); err != nil {
		t.Fatal(err)
	}

	service.SetSettings(&config.Settings{SessionDir: sessionDir, DefaultModel: "second"})
	if _, err := service.resolveKnowledgeIndexer(base); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 || seen[0] != "first" || seen[1] != "second" {
		t.Fatalf("factory settings = %#v, want the refreshed snapshot on the second resolution", seen)
	}
}

// writeKnowledgeTestFile creates one file (and its parents) under dir.
func writeKnowledgeTestFile(t *testing.T, dir, rel string, content []byte) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestKnowledgeBaseIndexHonorsIgnoreRulesAndReportsDiscovery pins that user
// ignoreGlobs and the root .gitignore suggestions exclude files, that the
// bounded discovery projection explains what was ignored or skipped, and that
// only the remaining files are indexed.
func TestKnowledgeBaseIndexHonorsIgnoreRulesAndReportsDiscovery(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	writeKnowledgeTestFile(t, source, "guide.md", []byte("# Guide\n\nUseful body.\n"))
	writeKnowledgeTestFile(t, source, "draft.md", []byte("# Draft\n\nUnpublished.\n"))
	writeKnowledgeTestFile(t, source, "secret.md", []byte("# Secret\n\nPrivate.\n"))
	writeKnowledgeTestFile(t, source, "ignored/notes.md", []byte("# Notes\n\nHidden dir.\n"))
	writeKnowledgeTestFile(t, source, "blob.md", []byte{0x00, 0x01, 0x02, 0xff})
	writeKnowledgeTestFile(t, source, ".gitignore", []byte("secret.md\n"))

	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
		IgnoreGlobs: []string{"draft.md", "ignored/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(context.Background(), base.ID)
	if err != nil {
		t.Fatalf("index failed: %v", err)
	}
	if snapshot.FileCount != 1 {
		t.Fatalf("file count = %d, want 1 (only guide.md)", snapshot.FileCount)
	}
	if snapshot.DiscoverySummary == nil {
		t.Fatalf("snapshot has no discovery summary: %#v", snapshot)
	}
	discovery := snapshot.DiscoverySummary
	if discovery.Discovered != 1 {
		t.Fatalf("discovered = %d, want 1", discovery.Discovered)
	}
	if discovery.Ignored < 2 {
		t.Fatalf("ignored = %d, want at least 2", discovery.Ignored)
	}
	if discovery.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (binary blob.md)", discovery.Skipped)
	}
	reasons := map[string]string{}
	for _, entry := range discovery.IgnoredEntries {
		reasons[entry.Path] = entry.Reason
	}
	if !strings.Contains(reasons["draft.md"], "ignore_glob") {
		t.Fatalf("draft.md reason = %q, want ignore_glob", reasons["draft.md"])
	}
	if !strings.Contains(reasons["secret.md"], "gitignore") {
		t.Fatalf("secret.md reason = %q, want gitignore", reasons["secret.md"])
	}
	if len(discovery.SkippedEntries) != 1 || discovery.SkippedEntries[0].Path != "blob.md" {
		t.Fatalf("skipped entries = %#v", discovery.SkippedEntries)
	}
}

// TestKnowledgeBaseIndexRecordsIncrementalDiffSummary pins the added/modified/
// removed/unchanged projection persisted with a rebuilt snapshot.
func TestKnowledgeBaseIndexRecordsIncrementalDiffSummary(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	writeKnowledgeTestFile(t, source, "a.md", []byte("# Alpha\n\nFirst body.\n"))
	writeKnowledgeTestFile(t, source, "b.md", []byte("# Beta\n\nSecond body.\n"))

	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Diff", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(context.Background(), base.ID); err != nil {
		t.Fatal(err)
	}
	writeKnowledgeTestFile(t, source, "a.md", []byte("# Alpha\n\nFirst body changed.\n"))
	if _, err := service.Index(context.Background(), base.ID); err != nil {
		t.Fatal(err)
	}
	current, err := session.GetKnowledgeBase(context.Background(), root, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := session.GetKnowledgeSnapshot(context.Background(), root, current.ActiveSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DiffSummary == nil {
		t.Fatalf("snapshot has no diff summary: %#v", snapshot)
	}
	diff := snapshot.DiffSummary
	if diff.Added != 0 || diff.Modified != 1 || diff.Removed != 0 || diff.Unchanged != 1 {
		t.Fatalf("diff summary = %#v, want 0 added / 1 modified / 0 removed / 1 unchanged", diff)
	}
}

// TestKnowledgeBaseQueryTraversesReferencesAndAliases pins the two-hop bounded
// traversal: a query that hits a section reaches the file it links to through a
// deterministic "references" edge, and a basename query seeds the file node via
// its entity alias even when the FTS chunks do not match.
func TestKnowledgeBaseQueryTraversesReferencesAndAliases(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	writeKnowledgeTestFile(t, source, "index.md", []byte("# Index\n\nSee [details](docs/details.md) for the deep content.\n"))
	writeKnowledgeTestFile(t, source, "docs/details.md", []byte("# Details\n\nDeep content lives here.\n"))

	base, err := session.CreateKnowledgeBase(context.Background(), root, session.KnowledgeBaseSpec{
		Name: "Links", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(context.Background(), base.ID); err != nil {
		t.Fatal(err)
	}

	result, err := service.Query(context.Background(), base.ID, "Index", 8)
	if err != nil {
		t.Fatal(err)
	}
	references := 0
	for _, edge := range result.Edges {
		if edge.RelationType == "references" {
			references++
		}
	}
	if references == 0 {
		t.Fatalf("query result has no references edge: %#v", result.Edges)
	}
	foundDetails := false
	for _, node := range result.Nodes {
		if node.Kind == "file" && node.Label == "docs/details.md" {
			foundDetails = true
		}
	}
	if !foundDetails {
		t.Fatalf("two-hop traversal did not reach docs/details.md: %#v", result.Nodes)
	}

	alias, err := service.Query(context.Background(), base.ID, "details.md", 8)
	if err != nil {
		t.Fatal(err)
	}
	foundAlias := false
	for _, node := range alias.Nodes {
		if node.Kind == "file" && node.Label == "docs/details.md" {
			foundAlias = true
		}
	}
	if !foundAlias {
		t.Fatalf("basename alias query did not seed the file node: %#v", alias.Nodes)
	}
}

// TestKnowledgeIndexModelUnavailableSurfacesStableCode pins that an index pass
// which cannot build the configured Indexer provider fails with the sentinel
// error and records the stable knowledge_base_model_unavailable code on the
// canonical Run, instead of leaking the raw provider error to management
// surfaces.
func TestKnowledgeIndexModelUnavailableSurfacesStableCode(t *testing.T) {
	sessionDir := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "a.md"), []byte("# A\n\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), sessionDir, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents",
		Provider: "broken", Model: "broken-model", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	settings := &config.Settings{SessionDir: sessionDir}
	service, err := NewKnowledgeBaseServiceWithProviderFactory(sessionDir, DefaultKnowledgeBaseIndexPolicy(), settings,
		func(*config.Settings, string, string) (provider.Provider, *provider.Model, error) {
			return nil, nil, errors.New("provider unavailable")
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); !errors.Is(err, ErrKnowledgeIndexerModelUnavailable) {
		t.Fatalf("index error = %v, want ErrKnowledgeIndexerModelUnavailable", err)
	}
	runs, err := service.IndexRuns(t.Context(), base.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 || runs[0].Status != string(RunStateFailed) {
		t.Fatalf("index runs = %#v, want one failed run", runs)
	}
	if runs[0].ErrorCode != knowledgeIndexModelUnavailableCode {
		t.Fatalf("run error code = %q, want %q", runs[0].ErrorCode, knowledgeIndexModelUnavailableCode)
	}
}

// TestKnowledgeBaseIndexAddsTestedByRelation pins the deterministic "tested_by"
// relation: a code knowledge base links a source file node to its conventional
// test file node by filename convention, with no model assertion involved.
func TestKnowledgeBaseIndexAddsTestedByRelation(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "calc.go"), []byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "calc_test.go"), []byte("package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Code", RootDir: source, PreprocessProfile: "code", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "add", 8)
	if err != nil {
		t.Fatal(err)
	}
	var testedBy bool
	for _, edge := range graph.Edges {
		if edge.RelationType == "tested_by" {
			testedBy = true
		}
	}
	if !testedBy {
		t.Fatalf("graph edges = %#v, want a tested_by relation", graph.Edges)
	}

	// The relation is rebuilt on an unchanged rescan, not dropped by reuse.
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err = service.Query(t.Context(), base.ID, "add", 8)
	if err != nil {
		t.Fatal(err)
	}
	testedBy = false
	for _, edge := range graph.Edges {
		if edge.RelationType == "tested_by" {
			testedBy = true
		}
	}
	if !testedBy {
		t.Fatalf("reused graph edges = %#v, want the tested_by relation rebuilt", graph.Edges)
	}
}

// TestKnowledgeBaseTestedByRelationIsCodeOnly pins that the code filename
// convention is not applied to a documents knowledge base (which does not index
// code files at all).
func TestKnowledgeBaseTestedByRelationIsCodeOnly(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	for name, body := range map[string]string{
		"calc.go":      "package calc\n\nfunc Add(a, b int) int { return a + b }\n",
		"calc_test.go": "package calc\n\nfunc TestAdd(t *testing.T) {}\n",
		"guide.md":     "# Guide\n\nAddition is documented here.\n",
	} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Index(t.Context(), base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FileCount != 1 {
		t.Fatalf("documents profile must not index code files: %#v", snapshot)
	}
	graph, err := service.Query(t.Context(), base.ID, "addition", 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range graph.Edges {
		if edge.RelationType == "tested_by" {
			t.Fatalf("documents profile produced a tested_by edge: %#v", graph.Edges)
		}
	}
}

func knowledgeTestHitTargets(hits []knowledgeImportHit) string {
	targets := make([]string, 0, len(hits))
	for _, hit := range hits {
		targets = append(targets, hit.target)
	}
	return strings.Join(targets, ",")
}

// TestKnowledgeImportHitsParseGoPythonAndJS pins the deterministic import
// scanners that back the "imports" relation.
func TestKnowledgeImportHitsParseGoPythonAndJS(t *testing.T) {
	goHits := knowledgeImportHits(".go", "package main\n\nimport (\n\t\"fmt\"\n\tf \"os\"\n\t_ \"net/http\"\n)\n\nimport \"strings\"\n")
	if got := knowledgeTestHitTargets(goHits); got != "fmt,os,net/http,strings" {
		t.Fatalf("go imports = %q", got)
	}
	pyHits := knowledgeImportHits(".py", "import os\nimport sys, json\nfrom a.b import c\nimport numpy as np\n")
	if got := knowledgeTestHitTargets(pyHits); got != "os,sys,json,a.b,numpy" {
		t.Fatalf("python imports = %q", got)
	}
	jsHits := knowledgeImportHits(".ts", "import x from \"react\"\nimport \"./side\"\nconst y = require(\"lodash\")\nexport { z } from \"mod\"\n")
	if got := knowledgeTestHitTargets(jsHits); got != "react,./side,lodash,mod" {
		t.Fatalf("js imports = %q", got)
	}
}

// TestKnowledgeBaseIndexAddsImportsRelation pins the deterministic "imports"
// relation end to end, including that it is rebuilt after an unchanged rescan.
func TestKnowledgeBaseIndexAddsImportsRelation(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc main() { fmt.Println(strings.ToUpper(\"x\")) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Code", RootDir: source, PreprocessProfile: "code", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	assertKnowledgeImportsRelation(t, service, base.ID)
	// An unchanged rescan reuses the snapshot but must still rebuild the relation.
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	assertKnowledgeImportsRelation(t, service, base.ID)
}

func assertKnowledgeImportsRelation(t *testing.T, service *KnowledgeBaseService, baseID string) {
	t.Helper()
	graph, err := service.Query(t.Context(), baseID, "Println", 8)
	if err != nil {
		t.Fatal(err)
	}
	var moduleFound, edgeFound bool
	for _, node := range graph.Nodes {
		if node.Kind == knowledgeImportNodeKind && node.Label == "fmt" {
			moduleFound = true
		}
	}
	for _, edge := range graph.Edges {
		if edge.RelationType == "imports" {
			edgeFound = true
		}
	}
	if !moduleFound || !edgeFound {
		t.Fatalf("graph nodes=%#v edges=%#v, want a module node and an imports edge", graph.Nodes, graph.Edges)
	}
}

// TestKnowledgeBaseIndexUsesDeclaresForCodeSymbols pins that a code declaration
// is linked with an explicit "declares" edge (a symbol declaration), not the
// generic "contains" relation.
func TestKnowledgeBaseIndexUsesDeclaresForCodeSymbols(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "util.go"), []byte("package util\n\n// Helper does work.\nfunc Helper() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Code", RootDir: source, PreprocessProfile: "code", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "Helper", 8)
	if err != nil {
		t.Fatal(err)
	}
	var helperID string
	for _, node := range graph.Nodes {
		if node.Kind == "symbol" && node.Label == "Helper" {
			helperID = node.ID
		}
	}
	if helperID == "" {
		t.Fatalf("symbol node Helper not found in %#v", graph.Nodes)
	}
	var declares, contains bool
	for _, edge := range graph.Edges {
		if edge.ToNodeID != helperID {
			continue
		}
		switch edge.RelationType {
		case "declares":
			declares = true
		case "contains":
			contains = true
		}
	}
	if !declares || contains {
		t.Fatalf("edges = %#v, want a declares edge and no contains edge for the symbol", graph.Edges)
	}
}

// TestKnowledgeBaseIndexKeepsContainsForSections pins that a document heading
// remains a "contains" relation.
func TestKnowledgeBaseIndexKeepsContainsForSections(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "guide.md"), []byte("# Guide\n\nBody text.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "Guide", 8)
	if err != nil {
		t.Fatal(err)
	}
	var sectionID string
	for _, node := range graph.Nodes {
		if node.Kind == "section" && node.Label == "Guide" {
			sectionID = node.ID
		}
	}
	if sectionID == "" {
		t.Fatalf("section node Guide not found in %#v", graph.Nodes)
	}
	var contains bool
	for _, edge := range graph.Edges {
		if edge.ToNodeID == sectionID && edge.RelationType == "contains" {
			contains = true
		}
	}
	if !contains {
		t.Fatalf("edges = %#v, want a contains edge for the section", graph.Edges)
	}
}

// TestKnowledgeConfigFilePatternMatchesWholeComponents pins the boundary rule
// that keeps the config-name scan from matching inside a longer filename.
func TestKnowledgeConfigFilePatternMatchesWholeComponents(t *testing.T) {
	cases := map[string]bool{
		"package.json":          true,
		"see package.json here": true,
		"path/to/package.json":  true,
		"mypackage.json":        false,
		"tsconfig.jsonc":        false,
	}
	for line, want := range cases {
		got := len(knowledgeConfigFilePattern.FindAllStringSubmatch(line, -1)) > 0
		if got != want {
			t.Fatalf("match(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestKnowledgeBaseIndexAddsConfiguredByRelation pins the deterministic
// "configured_by" relation: a code file that references a present config file is
// linked to it, and a reference to an absent or similarly named file is not.
func TestKnowledgeBaseIndexAddsConfiguredByRelation(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte("{\"name\":\"x\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mainSrc := "// This file reads package.json and tsconfig.json but not mypackage.json.\npackage main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte(mainSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Code", RootDir: source, PreprocessProfile: "code", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "reads", 8)
	if err != nil {
		t.Fatal(err)
	}
	configID := ""
	for _, node := range graph.Nodes {
		if node.Kind == "file" && node.Label == "package.json" {
			configID = node.ID
		}
	}
	if configID == "" {
		t.Fatalf("config file node package.json not found in %#v", graph.Nodes)
	}
	var configuredBy int
	for _, edge := range graph.Edges {
		if edge.RelationType == "configured_by" {
			configuredBy++
			if edge.ToNodeID != configID {
				t.Fatalf("configured_by target = %q, want %q", edge.ToNodeID, configID)
			}
		}
	}
	if configuredBy != 1 {
		t.Fatalf("configured_by edge count = %d, want exactly 1 (%#v)", configuredBy, graph.Edges)
	}
}

// TestKnowledgeLineCallsSymbolBoundaries pins the call-site detector's boundary
// and spacing rules.
func TestKnowledgeLineCallsSymbolBoundaries(t *testing.T) {
	cases := []struct {
		line  string
		label string
		want  bool
	}{
		{"\treturn Helper()", "Helper", true},
		{"x := Helper (a)", "Helper", true},
		{"Helper", "Helper", false},
		{"myHelper()", "Helper", false},
		{"HelperFunc()", "Helper", false},
		{"y := Helper() + Helper()", "Helper", true},
	}
	for _, testCase := range cases {
		if got := knowledgeLineCallsSymbol(testCase.line, testCase.label); got != testCase.want {
			t.Fatalf("knowledgeLineCallsSymbol(%q, %q) = %v, want %v", testCase.line, testCase.label, got, testCase.want)
		}
	}
}

// TestKnowledgeBaseIndexAddsCallsRelation pins the within-file "calls" relation:
// a symbol that invokes another symbol declared in the same file is linked to
// it, and a comment mention is not mistaken for a call.
func TestKnowledgeBaseIndexAddsCallsRelation(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	content := "package util\n\nfunc Helper() int { return 1 }\n\n// Use calls Helper twice.\nfunc Use() int {\n\treturn Helper() + Helper()\n}\n"
	if err := os.WriteFile(filepath.Join(source, "util.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Code", RootDir: source, PreprocessProfile: "code", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "Helper", 8)
	if err != nil {
		t.Fatal(err)
	}
	labelByID := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		labelByID[node.ID] = node.Label
	}
	var calls int
	for _, edge := range graph.Edges {
		if edge.RelationType != "calls" {
			continue
		}
		calls++
		if labelByID[edge.FromNodeID] != "Use" || labelByID[edge.ToNodeID] != "Helper" {
			t.Fatalf("calls edge %s -> %s, want Use -> Helper", labelByID[edge.FromNodeID], labelByID[edge.ToNodeID])
		}
	}
	if calls != 1 {
		t.Fatalf("calls edge count = %d, want exactly 1 (%#v)", calls, graph.Edges)
	}
}

// TestKnowledgeDocumentRelationPatterns pins the deterministic document scanners
// for definitions and RFC 2119 requirements.
func TestKnowledgeDocumentRelationPatterns(t *testing.T) {
	match := knowledgeDefinitionLine.FindStringSubmatch("**Idempotency** — repeating a request has no additional effect.")
	if len(match) != 3 || match[1] != "Idempotency" {
		t.Fatalf("definition match = %#v", match)
	}
	if knowledgeDefinitionLine.MatchString("just a plain sentence") {
		t.Fatal("a plain sentence must not be a definition")
	}
	if !knowledgeRequirementLine.MatchString("The server MUST reject unauthenticated requests.") {
		t.Fatal("uppercase MUST must be a requirement")
	}
	if knowledgeRequirementLine.MatchString("the server must reject requests") {
		t.Fatal("lowercase prose must not be a requirement")
	}
}

// TestKnowledgeBaseIndexAddsDocumentRelations pins the deterministic "defines"
// and "requires" relations end to end.
func TestKnowledgeBaseIndexAddsDocumentRelations(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	content := "# Terms\n\n**Idempotency** — repeating a request has no additional effect.\n\n# Requirements\n\nThe server MUST reject unauthenticated requests.\n"
	if err := os.WriteFile(filepath.Join(source, "spec.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "Idempotency", 8)
	if err != nil {
		t.Fatal(err)
	}
	labelByID := make(map[string]string, len(graph.Nodes))
	kindByID := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		labelByID[node.ID] = node.Label
		kindByID[node.ID] = node.Kind
	}
	var defines, requires bool
	for _, edge := range graph.Edges {
		switch edge.RelationType {
		case "defines":
			defines = true
			if labelByID[edge.FromNodeID] != "Terms" || kindByID[edge.ToNodeID] != knowledgeDefinedTermKind {
				t.Fatalf("defines edge %s(%s) -> %s(%s)", labelByID[edge.FromNodeID], kindByID[edge.FromNodeID], labelByID[edge.ToNodeID], kindByID[edge.ToNodeID])
			}
		case "requires":
			requires = true
			if kindByID[edge.ToNodeID] != knowledgeRequirementKind {
				t.Fatalf("requires target kind = %q, want %q", kindByID[edge.ToNodeID], knowledgeRequirementKind)
			}
		}
	}
	if !defines || !requires {
		t.Fatalf("edges = %#v, want both defines and requires relations", graph.Edges)
	}
}

// TestKnowledgeSupersedeReferencesExtraction pins reference extraction from a
// supersession line: quoted names and filename-like tokens are captured, and
// plain prose produces nothing.
func TestKnowledgeSupersedeReferencesExtraction(t *testing.T) {
	refs := knowledgeSupersedeReferences("This supersedes `v1.md` and old.md, not the old one.")
	joined := strings.Join(refs, ",")
	if !strings.Contains(joined, "v1.md") || !strings.Contains(joined, "old.md") {
		t.Fatalf("refs = %#v, want v1.md and old.md", refs)
	}
	if got := knowledgeSupersedeReferences("The new API supersedes the old implementation."); len(got) != 0 {
		t.Fatalf("prose refs = %#v, want none", got)
	}
}

// TestKnowledgeBaseIndexAddsSupersedesRelation pins the deterministic
// "supersedes" relation: a document that references another present document as
// superseded is linked to it, and a prose sentence is not.
func TestKnowledgeBaseIndexAddsSupersedesRelation(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "v1.md"), []byte("# Spec v1\n\nOriginal specification.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := "# Spec v2\n\nThis document supersedes `v1.md`. The new API supersedes the old one.\n"
	if err := os.WriteFile(filepath.Join(source, "v2.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := session.CreateKnowledgeBase(t.Context(), root, session.KnowledgeBaseSpec{
		Name: "Docs", RootDir: source, PreprocessProfile: "documents", Mode: "yolo", Schedule: "manual", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewKnowledgeBaseService(root, DefaultKnowledgeBaseIndexPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Index(t.Context(), base.ID); err != nil {
		t.Fatal(err)
	}
	graph, err := service.Query(t.Context(), base.ID, "supersedes", 8)
	if err != nil {
		t.Fatal(err)
	}
	labelByID := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		labelByID[node.ID] = node.Label
	}
	var supersedes int
	for _, edge := range graph.Edges {
		if edge.RelationType != "supersedes" {
			continue
		}
		supersedes++
		if labelByID[edge.FromNodeID] != "v2.md" || labelByID[edge.ToNodeID] != "v1.md" {
			t.Fatalf("supersedes edge %s -> %s, want v2.md -> v1.md", labelByID[edge.FromNodeID], labelByID[edge.ToNodeID])
		}
	}
	if supersedes != 1 {
		t.Fatalf("supersedes edge count = %d, want exactly 1 (%#v)", supersedes, graph.Edges)
	}
}
