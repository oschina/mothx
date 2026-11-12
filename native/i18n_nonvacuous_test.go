package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// These tests deliberately populate the dynamic projections that the page-level
// scans leave empty: fallback provider rows, SkillHub author/download metadata,
// knowledge sources and disabled bases, in-flight panel statuses, and composer
// attachment chips. They assert the real rendered strings, so a missing T/Tf
// call cannot pass vacuously.

func TestEnglishProviderFallbackRowsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "providers"
	// manageProviders without manageProviderConfig forces the fallback pane.
	a.setFeatures(map[string]bool{"manageProviders": true})
	a.providers.Loaded = true
	a.providers.Catalog = &providerCatalogView{Providers: []providerSummary{{
		Name: "Moark", BaseURL: "https://api.example", ModelCount: 3,
	}}}
	a.ensureSettingsTabVisible()
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "provider fallback rows", tt.Texts())
	if !textPresent(tt, "No API key configured · https://api.example · 3 models") {
		t.Fatalf("localized fallback description missing: %q", tt.Texts())
	}
}

func TestEnglishSkillHubMetadataHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.currentSession = "s1"
	a.page = "skills"
	a.setFeatures(map[string]bool{
		"manageSkills":          true,
		"manageSkillHub":        true,
		"manageSkillHubCatalog": true,
	})
	a.skillhub.ConfigLoaded = true
	a.skillhub.CatalogLoaded = true
	a.skillhub.SessionID = "s1"
	a.skillhub.SelectedMarket = "skillhub.cn"
	a.skillhub.MarketInfos = []skillHubMarketInfo{{ID: "skillhub.cn", Name: "SkillHub"}}
	a.skillhub.ResultsLoaded = true
	a.skillhub.ResultsTotal = 1
	a.skillhub.Results = []skillHubSkill{{
		Market: "skillhub.cn", ID: "review", Name: "review",
		Description: "Review changes", Author: "Alice", Category: "Engineering",
		Tags: []string{"review"}, Downloads: 1200, Version: "1.0",
	}}
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "skillhub metadata", tt.Texts())
	if !textPresent(tt, "Author Alice") {
		t.Fatalf("localized author metadata missing: %q", tt.Texts())
	}
	if !textPresent(tt, "1200 downloads") {
		t.Fatalf("localized download metadata missing: %q", tt.Texts())
	}

	// The settings SkillHub tab renders the result-count description, which the
	// page-level catalogue summary does not include.
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "skillhub"
	a.ensureSettingsTabVisible()
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "skillhub settings metadata", tt.Texts())
	if !textPresent(tt, "1 results; ") {
		t.Fatalf("localized result count missing: %q", tt.Texts())
	}

	// The detail dialog reuses skillHubSkillMeta; verify that projection too.
	skill := a.skillhub.Results[0]
	a.skillhub.DetailOpen = true
	a.skillhub.Detail = &skillHubDetail{skillHubSkill: skill}
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "skillhub detail metadata", tt.Texts())
	if !textPresent(tt, "Author Alice") || !textPresent(tt, "1200 downloads") {
		t.Fatalf("localized detail metadata missing: %q", tt.Texts())
	}
}

func TestEnglishKnowledgeSourcesAndEmptyStatesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "knowledge"
	a.setFeatures(map[string]bool{
		"manageKnowledgeBases": true,
		"knowledgeSources":     true,
	})
	a.knowledge.Loaded = true
	a.knowledge.Views = []knowledgeBaseView{
		{KnowledgeBase: knowledgeBase{ID: "kb-off", Name: "Off docs", Enabled: false, RootDir: "/work/off"}},
		{KnowledgeBase: knowledgeBase{ID: "kb-empty", Name: "Empty docs", Enabled: true, RootDir: "/work/empty"}},
		{KnowledgeBase: knowledgeBase{ID: "kb-live", Name: "Live docs", Enabled: true, RootDir: "/work/live"},
			Snapshot: &knowledgeSnapshot{ID: "snap1", Status: "ready", FileCount: 1, ChunkCount: 12}},
	}
	a.knowledge.Insights["kb-live"] = &knowledgeInsightsState{
		SourcesLoaded: true,
		Tab:           0,
		Sources: []knowledgeSourceView{{
			Title: "Guide", RelativePath: "guide.md", Status: "ready",
			ByteSize: 2048, ChunkCount: 12,
		}},
	}
	a.ensureSettingsTabVisible()
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "knowledge sources and empty states", tt.Texts())
	for _, want := range []string{
		"Disabled",
		"No usable index yet",
		"ready · 2.0 KB · 12 chunks",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("knowledge state %q missing: %q", want, tt.Texts())
		}
	}
}

func TestEnglishInFlightStatusesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })

	t.Run("skills", func(t *testing.T) {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.settingsOpen = true
		a.settingsCategory = "agent"
		a.settingsTab = "skills"
		a.setFeatures(map[string]bool{"manageSkills": true})
		a.skills.Loaded = true
		a.skills.Skills = []skillView{{
			Name: "review", Description: "Review changes", Source: "project",
		}}
		a.skills.Toggling = "review"
		a.skills.Status = skillToggleStatus("review", true)
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "skills in-flight", tt.Texts())
		if !textPresent(tt, "Enabling skill review…") {
			t.Fatalf("localized skill status missing: %q", tt.Texts())
		}
		if got := skillToggleStatus("review", false); got != "Disabling skill review…" {
			t.Fatalf("localized disable status = %q", got)
		}
	})

	t.Run("cron", func(t *testing.T) {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.page = "automation"
		a.setFeatures(map[string]bool{"manageCron": true})
		a.cron.Loaded = true
		a.cron.Jobs = []cronJobView{{
			ID: "job-1", Name: "Daily sync", Prompt: "Sync the docs",
			Schedule: "daily", Mode: "yolo", Enabled: true,
		}}
		a.cron.TogglingID = "job-1"
		a.cron.Status = cronToggleStatus("Daily sync", false)
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "cron in-flight", tt.Texts())
		if !textPresent(tt, `Resuming cron job "Daily sync"…`) {
			t.Fatalf("localized cron status missing: %q", tt.Texts())
		}
		if got := cronToggleStatus("Daily sync", true); got != `Pausing cron job "Daily sync"…` {
			t.Fatalf("localized pause status = %q", got)
		}
	})

	t.Run("skillhub", func(t *testing.T) {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.page = "skills"
		a.setFeatures(map[string]bool{
			"manageSkills":          true,
			"manageSkillHub":        true,
			"manageSkillHubCatalog": true,
		})
		a.skillhub.ConfigLoaded = true
		a.skillhub.CatalogLoaded = true
		a.currentSession = "s1"
		a.skillhub.SessionID = "s1"
		a.skillhub.SelectedMarket = "skillhub.cn"
		a.skillhub.MarketInfos = []skillHubMarketInfo{{ID: "skillhub.cn", Name: "SkillHub"}}
		a.skillhub.ResultsLoaded = true
		a.skillhub.CatalogStatus = skillHubInstallStatus("review", false, false, true, 2)
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "skillhub in-flight", tt.Texts())
		if !textPresent(tt, "Skill review installed and activated in this session; 2 warnings") {
			t.Fatalf("localized skillhub status missing: %q", tt.Texts())
		}
	})
}

func TestEnglishComposerEmbeddedAttachmentHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.currentSession = "s1"
	a.attachments = []attachment{{
		Name: "photo.png", MimeType: "image/png", Size: 2048, Embedded: true,
	}}
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "embedded attachment chip", tt.Texts())
	if !textPresent(tt, "photo.png (embedded)") {
		t.Fatalf("localized embedded attachment label missing: %q", tt.Texts())
	}
}

func TestEnglishRuntimeBinarySourceHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	previous := getRuntimeBinaryPreference()
	setRuntimeBinaryPreference("/opt/mothx/bin/mothx", false)
	t.Cleanup(func() { setRuntimeBinaryPreference(previous, false) })

	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "runtime"
	a.ensureSettingsTabVisible()
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "runtime binary source", tt.Texts())
	if !textPresent(tt, "Native settings") {
		t.Fatalf("localized persisted-source label missing: %q", tt.Texts())
	}
	// About also projects the same resolved source.
	a.settingsTab = "about"
	a.ensureSettingsTabVisible()
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "about runtime source", tt.Texts())
}

func TestEnglishFallbackLabelsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	for name, got := range map[string]string{
		"skill title":       skillHubTitle(skillHubSkill{}),
		"skill category":    skillHubCategoryName(skillHubCategory{}),
		"expert team type":  expertTypeLabel(expertTypeTeam),
		"expert agent type": expertTypeLabel(expertTypeAgent),
		"expert builtin":    expertScopeHint(expertSummary{Source: expertSourceBuiltin}, expertScopeGlobal),
		"subagent name":     subagentDisplayName(subagentInfoView{}),
	} {
		assertNoChineseUI(t, name, []string{got})
	}
	if got := skillHubTitle(skillHubSkill{}); got != "Untitled skill" {
		t.Fatalf("skill title fallback = %q", got)
	}
	if got := expertTypeLabel(expertTypeAgent); got != "Single agent" {
		t.Fatalf("expert agent type = %q", got)
	}
	if got := subagentDisplayName(subagentInfoView{}); got != "Sub-agent" {
		t.Fatalf("subagent fallback = %q", got)
	}
}

func TestEnglishTranscriptPagingHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{"sessionHistoryPaging": true})
	a.applyTranscriptPage("s1", &transcriptPageResult{
		SessionID:  "s1",
		Updates:    []sessionUpdate{transcriptTextUpdate("agent_message_chunk", "m1", "hello")},
		NextCursor: "older-1",
	}, false)
	tt := ui.NewTester(a.view, 1180, 760)
	assertNoChineseUI(t, "transcript paging", tt.Texts())
	if !textPresent(tt, "Load earlier messages") {
		t.Fatalf("localized transcript paging label missing: %q", tt.Texts())
	}

	a.transcriptLoading = true
	tt = ui.NewTester(a.view, 1180, 760)
	assertNoChineseUI(t, "transcript paging loading", tt.Texts())
	if !textPresent(tt, "Loading older messages…") {
		t.Fatalf("localized transcript loading label missing: %q", tt.Texts())
	}
}

func TestEnglishDoctorCheckFallbackHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "runtime"
	a.setFeatures(map[string]bool{"doctor": true})
	a.conn = connReady
	a.runtime.Doctor = &doctorResultView{Checks: []doctorCheckView{{
		Status: "ok", Detail: "configured",
	}}}
	if got := T("检查"); got != "Check" {
		t.Fatalf("doctor fallback title translation = %q", got)
	}
	a.ensureSettingsTabVisible()
	tt := ui.NewTester(a.view, 1180, 1000)
	assertNoChineseUI(t, "doctor check fallback", tt.Texts())
	if !textPresent(tt, "Check · OK") {
		t.Fatalf("localized doctor fallback title missing: %q", tt.Texts())
	}
}

func TestEnglishEmptyProjectHistoryHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.historyOpen = true
	a.historyPanel.Loaded = true
	a.historyPanel.Scope = "project"
	a.historyPanel.ProjectID = "missing-project"
	a.sessionsLoaded = true
	a.projectsLoaded = true
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "empty project history", tt.Texts())
	if !textPresent(tt, "No sessions in this project") {
		t.Fatalf("localized empty-project state missing: %q", tt.Texts())
	}
}

func TestEnglishPageCapabilityGapsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	for _, tc := range []struct {
		page string
		want string
	}{
		{"automation", "Cron unavailable"},
		{"library", "Knowledge bases unavailable"},
		{"skills", "Skill marketplace unavailable"},
	} {
		a := testApp()
		setLocale("en")
		a.page = tc.page
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "capability gap "+tc.page, tt.Texts())
		if !textPresent(tt, tc.want) {
			t.Fatalf("%s gap title %q missing: %q", tc.page, tc.want, tt.Texts())
		}
	}
}

func TestEnglishToolFallbackTitleHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	if got := toolTitle("", nil); got != "Tool call" {
		t.Fatalf("tool title fallback = %q", got)
	}
}

func TestEnglishTransientStatusAndDialogTitlesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	for _, text := range []string{
		"已保存到 /tmp/out.txt",
		"选择新会话工作区",
		"选择会话工作目录",
		"附加文件",
		"保存制品",
		"选择首页 Logo",
		"选择首页背景图",
		"选择 mothx 可执行文件",
		"选择知识源目录",
		"MothX 想确认一下",
		"沙箱",
		"浏览器",
		"Web 搜索",
		"不归属项目",
		"外部",
		"技能目录",
	} {
		assertNoChineseUI(t, "transient text", []string{T(text)})
	}
	if got := T("已保存到 /tmp/out.txt"); got != "Saved to /tmp/out.txt" {
		t.Fatalf("saved status = %q", got)
	}
}

func TestEnglishValidationErrorsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	cases := map[string]error{
		"provider draft":  validateProviderDraft(nil, nil, ""),
		"mcp draft":       validateMCPServerDraft(nil, nil, -1),
		"cron draft":      validateCronDraft(&cronJobDraft{}),
		"knowledge draft": validateKnowledgeDraft(&knowledgeBaseDraft{}),
		"serve form":      validateServeForm(&serveForm{}),
		"serve number":    func() error { _, err := serveParseInt("后台运行上限", "x", 1); return err }(),
		"skillhub config": func() error { _, err := buildSkillHubConfigPatch("", "", "", nil); return err }(),
	}
	for name, err := range cases {
		if err == nil {
			t.Fatalf("%s returned no validation error", name)
		}
		assertNoChineseUI(t, name, []string{err.Error()})
	}
	if got := validateProviderDraft(nil, nil, "").Error(); got != "Provider draft does not exist" {
		t.Fatalf("localized provider validation = %q", got)
	}
	if got, err := serveParseInt("后台运行上限", "x", 1); err == nil || got != 0 {
		t.Fatalf("serveParseInt = %d, %v", got, err)
	} else if !textPresentStr(err.Error(), "Background run limit must be an integer") {
		t.Fatalf("localized serve number error = %q", err.Error())
	}
}

func textPresentStr(text, want string) bool {
	return len(want) == 0 || strings.Contains(text, want)
}

func TestEnglishCapabilityFallbackNamesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.currentSession = "s1"
	a.configOptions = []configOption{
		{ID: "sandbox", Name: "", CurrentValue: "false"},
		{ID: "browser", Name: "", CurrentValue: "false"},
		{ID: "web_search", Name: "", CurrentValue: "false"},
	}
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Capabilities"); err != nil {
		t.Fatalf("open capabilities menu: %v", err)
	}
	assertNoChineseUI(t, "capability fallback labels", tt.Texts())
	menu := strings.Join(tt.Menu(), "|")
	for _, want := range []string{"Sandbox", "Browser", "Web search"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("localized capability %q missing from menu %q", want, menu)
		}
	}
}

func TestEnglishSettingsDialogsAndEditorsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })

	openSettings := func(tab string, features map[string]bool) *nativeApp {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.settingsOpen = true
		a.currentSession = "s1"
		a.setFeatures(features)
		a.settingsTab = tab
		return a
	}

	t.Run("cron editor", func(t *testing.T) {
		a := openSettings("cron", map[string]bool{"manageCron": true})
		a.settingsCategory = "automation"
		a.cron.EditorOpen = true
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 950)
		assertNoChineseUI(t, "cron editor", tt.Texts())
	})

	t.Run("mcp editor", func(t *testing.T) {
		a := openSettings("mcp", map[string]bool{"manageMcp": true})
		a.settingsCategory = "integration"
		a.mcp.EditorOpen = true
		a.mcp.Draft = mcpEmptyDraft()
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 950)
		assertNoChineseUI(t, "mcp editor", tt.Texts())
	})

	t.Run("knowledge create", func(t *testing.T) {
		a := openSettings("knowledge", map[string]bool{"manageKnowledgeBases": true})
		a.settingsCategory = "agent"
		a.knowledge.Loaded = true
		a.knowledge.Creating = true
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 1200)
		assertNoChineseUI(t, "knowledge create", tt.Texts())
	})

	t.Run("provider editor", func(t *testing.T) {
		a := openSettings("providers", map[string]bool{"manageProviders": true, "manageProviderConfig": true})
		a.settingsCategory = "agent"
		a.providers.Loaded = true
		a.providers.Draft = &providerDraft{ID: "moark", API: "openai-chat", BaseURL: "https://api.example"}
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 1000)
		assertNoChineseUI(t, "provider editor", tt.Texts())
	})

	t.Run("expert editor", func(t *testing.T) {
		a := openSettings("experts", map[string]bool{"manageExperts": true})
		a.settingsCategory = "agent"
		a.experts.Draft = &expertDraft{IsNew: true, Kind: "agent", Scope: expertScopeGlobal, Name: "team"}
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 1000)
		assertNoChineseUI(t, "expert editor", tt.Texts())
	})

	t.Run("worktree confirm", func(t *testing.T) {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.worktree.Open = true
		a.worktree.ConfirmOpen = true
		a.worktree.ConfirmKind = worktreeConfirmRemove
		a.worktree.ConfirmTree = worktreeView{ID: "wt1", Name: "Alpha", Directory: "/work/alpha"}
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "worktree confirm", tt.Texts())
	})

	t.Run("cron delete confirm", func(t *testing.T) {
		a := openSettings("cron", map[string]bool{"manageCron": true})
		a.settingsCategory = "automation"
		a.cron.PendingDelete = true
		a.cron.PendingDeleteID = "job-1"
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 950)
		assertNoChineseUI(t, "cron delete", tt.Texts())
	})

	t.Run("mcp delete confirm", func(t *testing.T) {
		a := openSettings("mcp", map[string]bool{"manageMcp": true})
		a.settingsCategory = "integration"
		a.mcp.PendingDelete = true
		a.mcp.PendingDeleteIndex = 0
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 950)
		assertNoChineseUI(t, "mcp delete", tt.Texts())
	})

	t.Run("knowledge delete confirm", func(t *testing.T) {
		a := openSettings("knowledge", map[string]bool{"manageKnowledgeBases": true})
		a.settingsCategory = "agent"
		a.knowledge.Loaded = true
		a.knowledge.PendingDeleteOpen = true
		a.knowledge.PendingDeleteID = "kb1"
		a.knowledge.PendingDeleteName = "Docs"
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 950)
		assertNoChineseUI(t, "knowledge delete", tt.Texts())
	})

	t.Run("expert delete confirm", func(t *testing.T) {
		a := openSettings("experts", map[string]bool{"manageExperts": true})
		a.settingsCategory = "agent"
		a.experts.PendingDeleteOpen = true
		a.experts.PendingDeleteName = "team"
		a.ensureSettingsTabVisible()
		tt := ui.NewTester(a.view, 1180, 950)
		assertNoChineseUI(t, "expert delete", tt.Texts())
	})
}

func TestEnglishPopulatedTranscriptAndStatusLineHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{"sessionHistoryPaging": true, "sessionMeta": true, "projects": true})
	a.messages = []*chatMessage{
		{ID: "u1", Kind: messageUser, Text: "hello", Done: true},
		{ID: "a1", Kind: messageAssistant, Text: "hi", Done: true},
		{ID: "t1", Kind: messageThought, Text: "thinking", Done: true, Collapsed: true},
		{ID: "tool1", Kind: messageTool, Text: "", Done: true, ToolID: "tool1", ToolTitle: "", ToolKind: "edit", ToolStatus: "failed", ToolText: "boom", ToolInput: map[string]any{"path": "/tmp/x"}, ToolDiffs: []toolDiff{{Path: "/tmp/x", OldText: "a", NewText: "b"}}, ToolLocations: []string{"/tmp/x"}},
		{ID: "p1", Kind: messagePlan, Done: true, Plan: []planEntry{{Content: "step", Status: "in_progress"}}},
		{ID: "e1", Kind: messageSystem, Text: "运行结束：max_tokens", Done: true, ErrorCode: "rate_limit", ErrorRetryable: true},
		{ID: "art1", Kind: messageArtifact, Done: true, ArtifactID: "art1", Filename: "out.txt", MediaType: "text/plain", ArtifactKind: "file", ArtifactSize: 12, ArtifactFromToolCall: true},
	}
	a.messageIndex = make(map[string]*chatMessage)
	for _, m := range a.messages {
		a.messageIndex[m.ID] = m
	}
	a.setStatusLine("正在生成…")
	a.transcriptNextCursor = "older"
	a.showToast("已粘贴剪贴板图片")
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "populated transcript", tt.Texts())
}

// TestEnglishTranscriptSystemMessagesHaveNoUntranslatedChinese covers the two
// authored messageSystem paths. Their text stays canonical Chinese in the
// transcript model and the default renderer projects it through T, so these
// assertions must render the real production strings rather than an inert
// English placeholder.
func TestEnglishTranscriptSystemMessagesHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })

	t.Run("disconnected send", func(t *testing.T) {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.currentSession = "s1"
		// send() authors this notice exactly as the disconnected composer does.
		a.conn = connFailed
		a.acp = nil
		a.draft = "hello"
		a.send()
		if len(a.messages) != 1 || a.messages[0].Kind != messageSystem {
			t.Fatalf("disconnected send messages = %+v", a.messages)
		}
		tt := ui.NewTester(a.view, 1180, 760)
		assertNoChineseUI(t, "disconnected send transcript", tt.Texts())
		if !textPresent(tt, "Not connected to MothX ACP.") {
			t.Fatalf("localized disconnected notice missing: %q", tt.Texts())
		}
	})

	t.Run("run finished stop reason", func(t *testing.T) {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.currentSession = "s1"
		// appendRunFinished is the production path used after session/prompt.
		a.appendRunFinished("max_tokens")
		tt := ui.NewTester(a.view, 1180, 760)
		assertNoChineseUI(t, "run finished transcript", tt.Texts())
		if !textPresent(tt, "Run finished: max_tokens") {
			t.Fatalf("localized run-finished line missing: %q", tt.Texts())
		}
	})
}

func TestEnglishPopulatedSidebarAndPanelsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := sidebarTestApp(t)
	setLocale("en")
	a.themeSet = true
	a.currentSession = "s1"
	a.sessions = append(a.sessions,
		listedSession{SessionID: "s3", Cwd: "/work/beta", Title: "", UpdatedAt: "2026-11-09T09:00:00Z"},
	)
	a.transcriptNextCursor = ""
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "sidebar", tt.Texts())

	a.worktree.Open = true
	a.worktree.Loaded = true
	a.worktree.Worktrees = []worktreeView{{ID: "wt1", Name: "Alpha", Directory: "/work/alpha", Status: "ready", Error: ""}}
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "worktree panel", tt.Texts())

	a.router.Open = true
	a.router.Loaded = true
	a.router.Status = "Router 已启用"
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "router panel", tt.Texts())
}

func TestEnglishHomePresetsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	for _, preset := range []string{"working", "coding", "design"} {
		a := testApp()
		setLocale("en")
		a.themeSet = true
		a.home.preset = preset
		tt := ui.NewTester(a.view, 1180, 900)
		assertNoChineseUI(t, "home preset "+preset, tt.Texts())
	}
}
