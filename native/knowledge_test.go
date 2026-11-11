package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func knowledgeTestBase() knowledgeBase {
	return knowledgeBase{
		ID:                "kb-docs",
		Name:              "产品文档",
		RootDir:           "/work/product-docs",
		PreprocessProfile: "documents",
		Provider:          "deepseek",
		Model:             "deepseek-chat",
		Mode:              "yolo",
		ThinkingLevel:     "medium",
		Schedule:          "daily",
		Enabled:           true,
		ActiveSnapshotID:  "snap-1",
	}
}

func knowledgeTestDefaults() *knowledgeBaseDefaults {
	return &knowledgeBaseDefaults{
		Provider:      "deepseek",
		Model:         "deepseek-chat",
		Mode:          "yolo",
		ThinkingLevel: "medium",
		Providers:     []string{"deepseek", "local"},
		Models: []providerCatalogModel{
			{ID: "deepseek-chat", Provider: "deepseek"},
			{ID: "deepseek-reasoner", Provider: "deepseek"},
			{ID: "local-model", Provider: "local"},
		},
	}
}

func knowledgeTestView() knowledgeBaseView {
	base := knowledgeTestBase()
	return knowledgeBaseView{
		KnowledgeBase: base,
		Snapshot: &knowledgeSnapshot{
			ID: "snap-1", Status: "ready",
			FileCount: 7, ChunkCount: 42, NodeCount: 12, EdgeCount: 9,
		},
		Status: "ready",
	}
}

func knowledgeTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "knowledge"
	a.knowledge = newKnowledgePanelState()
	a.knowledge.Loaded = true
	a.knowledge.Defaults = knowledgeTestDefaults()
	view := knowledgeTestView()
	a.knowledge.Views = []knowledgeBaseView{view}
	a.knowledge.Drafts[view.KnowledgeBase.ID] = knowledgeDraftFromBase(view.KnowledgeBase)
	a.knowledge.Schedules[view.KnowledgeBase.ID] = knowledgeScheduleView{
		ID: view.KnowledgeBase.ID, Enabled: true, Schedule: "@daily",
		Configured: true, RootAvailable: true,
		NextRun: "2026-11-12T03:00:00Z", LastStatus: "success",
	}
	a.knowledge.Insights[view.KnowledgeBase.ID] = &knowledgeInsightsState{
		Runs: []knowledgeIndexRunView{{
			RunID: "run-1", Status: "completed", StartedAt: "2026-11-11T02:00:00Z",
			FinishedAt: "2026-11-11T02:04:00Z", FileCount: 7, ChunkCount: 42,
			NodeCount: 12, EdgeCount: 9, Active: true,
		}},
		Sources: []knowledgeSourceView{{
			RelativePath: "guide/intro.md", Title: "入门指南", Status: "indexed",
			ByteSize: 2048, ChunkCount: 3,
		}},
		RunsLoaded: true, SourcesLoaded: true,
	}
	enabled := true
	a.knowledge.MCPServers = []mcpServerView{{
		Name: knowledgeMCPName(view.KnowledgeBase.ID), Type: "stdio", Enabled: &enabled,
	}}
	a.ensureSettingsTabVisible()
	return a
}

func fullKnowledgeFeatures() map[string]bool {
	return map[string]bool{
		"manageApplicationSettings": true,
		"manageSettings":            true,
		"manageProviders":           true,
		"manageKnowledgeBases":      true,
		"manageMcp":                 true,
		"knowledgeRuns":             true,
		"knowledgeSources":          true,
		"knowledgeGraphIndex":       true,
	}
}

func TestKnowledgeListDecodeProjectsSnapshotAndIndexing(t *testing.T) {
	raw := json.RawMessage(`{
		"knowledgeBases": [{
			"knowledgeBase": {
				"id": "kb-1", "name": "Docs", "rootDir": "/work/docs",
				"preprocessProfile": "code", "provider": "deepseek", "model": "deepseek-chat",
				"mode": "yolo", "thinkingLevel": "high", "schedule": "hourly",
				"enabled": true, "ignoreGlobs": ["vendor/**"], "activeSnapshotId": "snap-1",
				"createdAt": "2026-11-10T10:00:00Z", "updatedAt": "2026-11-11T10:00:00Z"
			},
			"snapshot": {
				"id": "snap-1", "status": "ready", "fileCount": 3,
				"chunkCount": 9, "nodeCount": 4, "edgeCount": 2,
				"startedAt": "2026-11-11T10:00:00Z", "finishedAt": "2026-11-11T10:01:00Z"
			},
			"status": "ready",
			"indexing": {"running": true, "phase": "extracting", "filesTotal": 10, "filesDone": 4}
		}]
	}`)
	var list knowledgeListView
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.KnowledgeBases) != 1 {
		t.Fatalf("views = %d", len(list.KnowledgeBases))
	}
	view := list.KnowledgeBases[0]
	if view.KnowledgeBase.ID != "kb-1" || view.KnowledgeBase.RootDir != "/work/docs" {
		t.Fatalf("knowledge base = %+v", view.KnowledgeBase)
	}
	if view.KnowledgeBase.PreprocessProfile != "code" || view.KnowledgeBase.ThinkingLevel != "high" ||
		len(view.KnowledgeBase.IgnoreGlobs) != 1 || view.KnowledgeBase.IgnoreGlobs[0] != "vendor/**" {
		t.Fatalf("knowledge config = %+v", view.KnowledgeBase)
	}
	if view.Snapshot == nil || view.Snapshot.ChunkCount != 9 || view.Snapshot.EdgeCount != 2 {
		t.Fatalf("snapshot = %+v", view.Snapshot)
	}
	if view.Indexing == nil || !view.Indexing.Running || view.Indexing.Phase != "extracting" ||
		view.Indexing.FilesDone != 4 || view.Indexing.FilesTotal != 10 {
		t.Fatalf("indexing = %+v", view.Indexing)
	}
	if got := knowledgeStatusText(view); !strings.Contains(got, "索引中") || !strings.Contains(got, "4/10") {
		t.Fatalf("status text = %q", got)
	}
	if got := knowledgeStatsText(view.Snapshot); got != "文件 3 · 分块 9 · 节点 4 · 关系 2" {
		t.Fatalf("stats text = %q", got)
	}
}

func TestKnowledgeDraftNormalizesModelAndPayload(t *testing.T) {
	a := testApp()
	a.knowledge.Defaults = knowledgeTestDefaults()
	draft := &knowledgeBaseDraft{
		Name: "  Docs  ", RootDir: " /work/docs ", PreprocessProfile: "", Provider: "deepseek",
		Model: "missing-model", Mode: "", Schedule: "", Enabled: true,
		IgnoreGlobs: []string{"vendor/**"},
	}
	a.normalizeKnowledgeDraft(draft)
	if draft.PreprocessProfile != "documents" || draft.Mode != "yolo" || draft.Schedule != "manual" {
		t.Fatalf("normalized defaults = %+v", draft)
	}
	if draft.Model != "deepseek-chat" {
		t.Fatalf("model = %q, want first provider model", draft.Model)
	}
	payload := knowledgeDraftPayload(draft)
	if payload["name"] != "Docs" || payload["rootDir"] != "/work/docs" ||
		payload["provider"] != "deepseek" || payload["model"] != "deepseek-chat" {
		t.Fatalf("payload = %#v", payload)
	}
	globs, _ := payload["ignoreGlobs"].([]string)
	if len(globs) != 1 || globs[0] != "vendor/**" {
		t.Fatalf("ignoreGlobs = %#v", payload["ignoreGlobs"])
	}
	if err := validateKnowledgeDraft(draft); err != nil {
		t.Fatalf("valid draft rejected: %v", err)
	}

	noGlobs := knowledgeDraftPayload(&knowledgeBaseDraft{Name: "x", RootDir: "/x"})
	if _, exists := noGlobs["ignoreGlobs"]; exists {
		t.Fatalf("new draft must omit ignoreGlobs: %#v", noGlobs)
	}
	if a := knowledgeModelChoices(knowledgeTestDefaults().Models, "local"); len(a) != 1 || a[0] != "local-model" {
		t.Fatalf("local models = %v", a)
	}
}

func TestKnowledgeDraftValidationRejectsIncompleteConfig(t *testing.T) {
	cases := []knowledgeBaseDraft{
		{},
		{Name: "docs"},
		{Name: "docs", RootDir: "/docs", Provider: "deepseek"},
		{Name: "docs", RootDir: "/docs", Model: "deepseek-chat"},
	}
	for index, draft := range cases {
		if err := validateKnowledgeDraft(&draft); err == nil {
			t.Fatalf("case %d should fail: %+v", index, draft)
		}
	}
}

func TestKnowledgeScheduleAndFormattingProjections(t *testing.T) {
	if got := knowledgeScheduleText(knowledgeScheduleView{Schedule: "", Enabled: true}); got != "仅手动扫描" {
		t.Fatalf("manual schedule = %q", got)
	}
	if got := knowledgeScheduleText(knowledgeScheduleView{Schedule: "@daily", Configured: true, NextRun: "2026-11-12T03:00:00Z"}); !strings.HasPrefix(got, "下次运行：") {
		t.Fatalf("scheduled text = %q", got)
	}
	if got := knowledgeFormatSize(512); got != "512 B" {
		t.Fatalf("size B = %q", got)
	}
	if got := knowledgeFormatSize(2048); got != "2.0 KB" {
		t.Fatalf("size KB = %q", got)
	}
	if got := knowledgeFormatSize(3 * 1024 * 1024); got != "3.0 MB" {
		t.Fatalf("size MB = %q", got)
	}
	if got := knowledgeTruncate("知识库索引预览", 3); !strings.HasSuffix(got, "…") || len([]rune(got)) != 4 {
		t.Fatalf("truncate = %q", got)
	}
	if got := knowledgeFormatDate("0001-01-01T00:00:00Z"); got != "" {
		t.Fatalf("zero date = %q", got)
	}
	if got := knowledgeFormatDate("not-a-date"); got != "not-a-date" {
		t.Fatalf("opaque date = %q", got)
	}
}

func TestKnowledgePanelRendersACPProjection(t *testing.T) {
	a := knowledgeTestApp(t, fullKnowledgeFeatures())
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{
		"目录知识库", "新建知识库",
		"名称", "知识源目录", "选择目录", "预处理类型", "供应商", "模型", "模式",
		"思考等级（可选）", "扫描周期", "启用知识库",
		"索引状态：ready", "文件 7 · 分块 42 · 节点 12 · 关系 9",
		"扫描计划", "下次运行：", "暂停",
		"MCP 快速接入", "MCP 状态：已启用", "停用 MCP 工具",
		"索引历史", "来源浏览", "当前活动", "详情", "清除索引",
		"查询预览（仅调试）", "返回有界图谱证据",
		"保存配置", "刷新", "立即扫描", "删除",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("knowledge panel missing %q; texts = %q", want, tt.Texts())
		}
	}
	if tt.HasText("该设置项由 ACP-backed 服务管理。") {
		t.Fatalf("knowledge pane must not render the management placeholder: %q", tt.Texts())
	}
}

func TestKnowledgeSourcesTabRendersWithoutReadingFiles(t *testing.T) {
	a := knowledgeTestApp(t, fullKnowledgeFeatures())
	a.knowledge.Insights[knowledgeTestBase().ID].Tab = 1
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"来源浏览", "入门指南", "guide/intro.md", "indexed · 2.0 KB · 3 个分块"} {
		if !textPresent(tt, want) {
			t.Fatalf("sources tab missing %q; texts = %q", want, tt.Texts())
		}
	}
}

func TestKnowledgeFeatureGatesHideOptionalProjections(t *testing.T) {
	a := knowledgeTestApp(t, map[string]bool{
		"manageApplicationSettings": true,
		"manageKnowledgeBases":      true,
	})
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("保存配置") || !tt.HasText("知识源目录") {
		t.Fatalf("base knowledge card missing: %q", tt.Texts())
	}
	for _, hidden := range []string{"MCP 快速接入", "索引历史", "来源浏览", "查询预览（仅调试）"} {
		if tt.HasText(hidden) {
			t.Fatalf("optional projection %q leaked without its feature: %q", hidden, tt.Texts())
		}
	}

	gated := knowledgeTestApp(t, map[string]bool{
		"manageApplicationSettings": true,
		"manageKnowledgeBases":      true,
		"knowledgeRuns":             true,
		"knowledgeSources":          true,
	})
	tt = ui.NewTester(gated.view, 1180, 760)
	if !tt.HasText("索引历史") || !tt.HasText("来源浏览") {
		t.Fatalf("insights tabs missing with their features: %q", tt.Texts())
	}
	if tt.HasText("查询预览（仅调试）") {
		t.Fatalf("query preview must require knowledgeGraphIndex: %q", tt.Texts())
	}

	unsupported := testApp()
	unsupported.themeSet = true
	unsupported.setFeatures(map[string]bool{})
	tt = ui.NewTester(func(c *ui.Context) {
		p := unsupported.applyTheme(c)
		ui.Column(c).Padding(16).Children(func() {
			unsupported.settingsKnowledgePane(c, p)
		})
	}, 860, 760)
	if !tt.HasText("当前运行时未公开知识库管理能力。") {
		t.Fatalf("unsupported knowledge panel missing: %q", tt.Texts())
	}
}

func TestKnowledgeNewDraftRendersInline(t *testing.T) {
	a := knowledgeTestApp(t, fullKnowledgeFeatures())
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("新建知识库"); err != nil {
		t.Fatalf("click new knowledge base: %v", err)
	}
	tt.Frame()
	if !a.knowledge.Creating {
		t.Fatal("new knowledge draft was not opened")
	}
	for _, want := range []string{"新知识库", "创建知识库", "取消"} {
		if !textPresent(tt, want) {
			t.Fatalf("new knowledge card missing %q; texts = %q", want, tt.Texts())
		}
	}
	if _, exists := a.knowledge.Drafts[knowledgeNewDraftKey]; !exists {
		t.Fatal("new draft state missing")
	}
	a.cancelKnowledgeNew()
	if a.knowledge.Creating {
		t.Fatal("cancel did not close the new knowledge card")
	}
	if _, exists := a.knowledge.Drafts[knowledgeNewDraftKey]; exists {
		t.Fatal("cancel did not remove the new draft")
	}
}

func TestKnowledgeInvalidationDropsTransientState(t *testing.T) {
	a := knowledgeTestApp(t, fullKnowledgeFeatures())
	a.knowledge.Polling = true
	a.knowledge.Status = "正在保存"
	a.knowledge.Busy[knowledgeTestBase().ID] = "save"
	a.knowledge.PendingDeleteID = "kb-docs"
	a.knowledge.PendingDeleteOpen = true
	before := a.knowledge.Seq

	a.invalidateKnowledgePanel()

	if a.knowledge.Loaded || a.knowledge.Loading {
		t.Fatalf("invalidation left load state: %+v", a.knowledge)
	}
	if a.knowledge.Seq == before {
		t.Fatal("invalidation must bump the async sequence")
	}
	if a.knowledge.Polling || a.knowledge.PendingDeleteOpen || len(a.knowledge.Drafts) != 0 ||
		len(a.knowledge.Insights) != 0 || len(a.knowledge.Schedules) != 0 || len(a.knowledge.Busy) != 0 {
		t.Fatalf("invalidation left transient knowledge state: %+v", a.knowledge)
	}

	// Leaving the tab goes through the same invalidation path.
	a.knowledge.Loaded = true
	a.settingsTab = "knowledge"
	a.setSettingsTab("mcp")
	if a.knowledge.Loaded {
		t.Fatal("leaving knowledge must invalidate its loaded projection")
	}

	a.knowledge.Loaded = true
	a.closeSettings()
	if a.knowledge.Loaded {
		t.Fatal("closing settings must invalidate the knowledge projection")
	}
}

func TestKnowledgePanelLoadWithoutCapabilityDoesNotCallACP(t *testing.T) {
	a := testApp()
	a.setFeatures(map[string]bool{})
	a.settingsOpen = true
	a.settingsTab = "knowledge"
	a.startKnowledgePanelLoad()
	if !a.knowledge.Loaded || !strings.Contains(a.knowledge.Err, "manageKnowledgeBases") {
		t.Fatalf("capability gate state = %+v", a.knowledge)
	}
	if a.knowledge.Loading {
		t.Fatal("missing capability must not start a load")
	}
}

func TestKnowledgeQueryProjectionDecodesBoundedEvidence(t *testing.T) {
	raw := json.RawMessage(`{
		"query": {
			"snapshot": {"id": "snap-1", "status": "ready"},
			"chunks": [
				{"id": "c1", "relativePath": "guide/intro.md", "ordinal": 0, "text": "durable graph", "startLine": 4, "endLine": 9},
				{"id": "c2", "relativePath": "design/notes.md", "ordinal": 1, "text": "snapshot isolation", "startLine": 1, "endLine": 3}
			],
			"nodes": [{"id": "n1", "kind": "concept", "label": "graph"}],
			"edges": [{"id": "e1"}],
			"evidence": [{"id": "ev1"}],
			"truncated": true
		}
	}`)
	var result knowledgeQueryResultView
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	query := result.Query
	if len(query.Chunks) != 2 || query.Chunks[0].RelativePath != "guide/intro.md" ||
		query.Chunks[0].StartLine != 4 || query.Chunks[0].EndLine != 9 {
		t.Fatalf("chunks = %+v", query.Chunks)
	}
	if len(query.Nodes) != 1 || query.Nodes[0].Label != "graph" || !query.Truncated {
		t.Fatalf("query = %+v", query)
	}
}
