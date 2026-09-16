package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/tools"
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

type knowledgeIndexerTestProvider struct {
	model     *provider.Model
	calls     int
	toolNames []string
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
		responseBytes, _ := json.Marshal(indexerLinkResponse{Links: []indexerLink{{
			FromNodeID: input.Nodes[0].ID, ToNodeID: input.Nodes[1].ID, ChunkID: chunk.ID,
			StartLine: chunk.StartLine, EndLine: chunk.EndLine,
		}}})
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
