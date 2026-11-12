package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func sidebarTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.dark = true
	a.sessionsLoaded = true
	a.projectsLoaded = true
	a.setFeatures(map[string]bool{
		"projects":       true,
		"sessionMeta":    true,
		"sessionFork":    true,
		"sessionWorkDir": true,
	})
	a.sessions = []listedSession{
		{SessionID: "s1", Cwd: "/work/alpha", Title: "Open Task", UpdatedAt: "2026-11-11T09:00:00Z"},
		{SessionID: "s2", Cwd: "/work/alpha", Title: "Project Task", UpdatedAt: "2026-11-10T09:00:00Z", Meta: sessionMeta{ProjectID: "p1", Pinned: true}},
	}
	a.projects = []projectView{{ID: "p1", Name: "Alpha", SessionCount: historyIntPtr(1)}}
	return a
}

func TestSidebarTaskTreeUsesProjectsRecentAndUngrouped(t *testing.T) {
	a := sidebarTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{"项目", "Alpha", "最近", "未归类", "Open Task", "会话操作"} {
		if !textPresent(tt, want) {
			t.Fatalf("sidebar task tree missing %q: %q", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"改名", "Fork", "目录", "取消置顶"} {
		if tt.HasText(unwanted) {
			t.Fatalf("sidebar row must not render the old text action %q: %q", unwanted, tt.Texts())
		}
	}
	if a.expandedProjects["p1"] {
		t.Fatal("project branch must start collapsed")
	}
	if err := tt.Click("Alpha"); err != nil {
		t.Fatalf("expand project: %v", err)
	}
	if !a.expandedProjects["p1"] {
		t.Fatal("project branch did not expand")
	}
}

func TestSidebarSessionMenuOpensDialogWithoutHistoryView(t *testing.T) {
	a := sidebarTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("会话操作"); err != nil {
		t.Fatalf("open session menu: %v", err)
	}
	if err := tt.ChooseMenuItem("重命名…"); err != nil {
		t.Fatalf("choose rename: %v", err)
	}
	if a.historyOpen {
		t.Fatal("sidebar menu must not open the history view")
	}
	if !a.historyPanel.DialogOpen || a.historyPanel.DialogKind != historyDialogRename {
		t.Fatalf("rename dialog state = %+v", a.historyPanel)
	}
	if !textPresent(tt, "重命名会话") {
		t.Fatalf("rename dialog not rendered outside history: %q", tt.Texts())
	}
}

func TestSessionMenuDisablesBusySessionActions(t *testing.T) {
	a := sidebarTestApp(t)
	a.currentSession = "s1"
	a.running = true
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("会话操作"); err != nil {
		t.Fatalf("open session menu: %v", err)
	}
	menu := tt.Menu()
	disabledSeen := false
	for _, item := range menu {
		if item == "Fork 会话" || item == "设置工作目录…" || item == "删除会话…" {
			disabledSeen = true
			break
		}
	}
	if !disabledSeen {
		t.Fatalf("busy session menu missing guarded actions: %v", menu)
	}
	// The first menu belongs to a sidebar row; choosing a guarded action must
	// not mutate canonical state through the client.
	if err := tt.ChooseMenuItem("Fork 会话"); err == nil {
		t.Fatal("busy session fork must be disabled")
	}
}

func TestSelectSessionKeepsDraftWorkspace(t *testing.T) {
	a := testApp()
	a.workspace = "/work/current"
	a.draftCwd = "/work/draft"
	session := listedSession{SessionID: "s1", Cwd: "/work/session", Title: "Session"}
	a.sessions = []listedSession{session}
	a.selectSession(session)
	if a.currentSession != "s1" || a.loadingSession != "" {
		t.Fatalf("selection state = current %q loading %q", a.currentSession, a.loadingSession)
	}
	if a.workspace != "/work/session" {
		t.Fatalf("active workspace = %q", a.workspace)
	}
	if a.draftCwd != "/work/draft" {
		t.Fatalf("new-session draft directory was overwritten: %q", a.draftCwd)
	}
}

func TestNewChatUsesDraftWorkspace(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.workspace = "/work/session"
	a.draftCwd = "/work/draft"
	a.newChat()
	if a.currentSession != "" {
		t.Fatalf("new chat retained session %q", a.currentSession)
	}
	if a.workspace != "/work/draft" || a.draftCwd != "/work/draft" {
		t.Fatalf("new chat workspace = %q draft = %q", a.workspace, a.draftCwd)
	}
}

func TestBackgroundSessionEventsStayInTheirCache(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.messageIndex = make(map[string]*chatMessage)
	a.appendMessage(&chatMessage{ID: "agent:a", Kind: messageAssistant, Text: "one"})
	a.running = true
	a.sessionStates = make(map[string]sessionSnapshot)

	a.applyUpdate(sessionUpdateNotification{
		SessionID: "s2",
		Update: sessionUpdate{
			SessionUpdate: "agent_message_chunk",
			MessageID:     "b",
			Content:       json.RawMessage(`{"type":"text","text":"two"}`),
		},
	})
	if a.currentSession != "s1" || len(a.messages) != 1 || a.messages[0].Text != "one" {
		t.Fatalf("active session was disturbed: session=%q messages=%+v", a.currentSession, a.messages)
	}
	if state, ok := a.sessionStates["s2"]; !ok || len(state.Messages) != 1 || state.Messages[0].Text != "two" {
		t.Fatalf("background cache = %+v", state)
	}
	a.selectSessionState("s2")
	if a.currentSession != "s2" || len(a.messages) != 1 || a.messages[0].Text != "two" {
		t.Fatalf("restored background session = %q %+v", a.currentSession, a.messages)
	}
	a.selectSessionState("s1")
	if len(a.messages) != 1 || a.messages[0].Text != "one" || !a.running {
		t.Fatalf("restored original session = %+v running=%v", a.messages, a.running)
	}
}

func TestNewChatCanRunWhileAnotherSessionContinues(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.messageIndex = make(map[string]*chatMessage)
	a.sessions = []listedSession{{SessionID: "s1", Cwd: "/work"}}
	a.draftCwd = "/draft"
	a.workspace = "/work"
	a.running = true
	a.sessionStates = make(map[string]sessionSnapshot)
	a.appendMessage(&chatMessage{ID: "agent:a", Kind: messageAssistant, Text: "working"})

	a.newChat()
	if a.currentSession != "" || a.running {
		t.Fatalf("new chat failed to detach: session=%q running=%v status=%q", a.currentSession, a.running, a.status)
	}
	if state, ok := a.sessionStates["s1"]; !ok || !state.Running || len(state.Messages) != 1 {
		t.Fatalf("background session was not preserved: %+v", state)
	}
	if a.workspace != "/draft" {
		t.Fatalf("new chat workspace = %q", a.workspace)
	}
	a.selectSessionState("s1")
	if !a.running || len(a.messages) != 1 || a.messages[0].Text != "working" {
		t.Fatalf("reattached session = %+v running=%v", a.messages, a.running)
	}
}

func TestBackgroundTerminalEventUpdatesSidebarAndCache(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.sessions = []listedSession{{SessionID: "s2", Cwd: "/work", Meta: sessionMeta{LastRun: &sessionLastRun{Active: true, Status: "running"}}}}
	a.sessionStates = make(map[string]sessionSnapshot)
	a.sessionStates["s2"] = sessionSnapshot{ID: "s2", Running: true}
	a.handleSessionEvent(sessionEvent{SessionID: "s2", Event: "terminal", Status: "completed"})
	if a.sessions[0].Meta.LastRun == nil || a.sessions[0].Meta.LastRun.Active || a.sessions[0].Meta.LastRun.Status != "completed" {
		t.Fatalf("sidebar run projection = %+v", a.sessions[0].Meta.LastRun)
	}
	if state := a.sessionStates["s2"]; state.Running {
		t.Fatalf("background cache still running: %+v", state)
	}
	if a.currentSession != "s1" {
		t.Fatalf("active session changed: %q", a.currentSession)
	}
}

func TestDedicatedMainPagesRender(t *testing.T) {
	a := testApp()
	a.dark = true
	a.themeSet = true
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{
		"manageSkills":          true,
		"manageSkillHub":        true,
		"manageSkillHubCatalog": true,
		"manageCron":            true,
		"manageKnowledgeBases":  true,
	})
	a.skills.Loaded = true
	a.skills.Cwd = "/work"
	a.skills.Skills = []skillView{{Name: "review", Description: "审查改动"}}
	a.skillhub.ConfigLoaded = true
	a.skillhub.CatalogLoaded = true
	a.skillhub.SessionID = "s1"
	a.skillhub.SelectedMarket = "skillhub.cn"
	a.skillhub.MarketInfos = []skillHubMarketInfo{{ID: "skillhub.cn", Name: "SkillHub"}}
	a.skillhub.ResultsLoaded = true
	a.skillhub.Results = []skillHubSkill{{Market: "skillhub.cn", ID: "review", Name: "review", Description: "审查改动"}}
	a.cron.Loaded = true
	a.knowledge.Loaded = true

	a.page = "skills"
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"技能", "可直接注入 Composer 的指令卡片、在线技能市场与本地技能入口", "管理本地技能", "市场配置", "技能市场", "review"} {
		if !textPresent(tt, want) {
			t.Fatalf("skills page missing %q: %q", want, tt.Texts())
		}
	}
	a.page = "automation"
	tt = ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"自动化", "定时任务", "新建任务"} {
		if !textPresent(tt, want) {
			t.Fatalf("automation page missing %q: %q", want, tt.Texts())
		}
	}
	a.page = "library"
	tt = ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"资料库", "知识库", "管理知识库"} {
		if !textPresent(tt, want) {
			t.Fatalf("library page missing %q: %q", want, tt.Texts())
		}
	}
}

// Page-level dialogs must render outside Settings: Desktop keeps create/edit
// and marketplace detail reachable from the dedicated main views.
func TestDedicatedPageDialogsRenderOutsideSettings(t *testing.T) {
	a := testApp()
	a.dark = true
	a.themeSet = true
	a.setFeatures(map[string]bool{
		"manageCron":            true,
		"manageSkillHubCatalog": true,
	})
	a.cron.Loaded = true
	a.openCronNewDraft()
	a.page = "automation"
	tt := ui.NewTester(a.view, 1180, 900)
	if a.settingsOpen {
		t.Fatal("page dialog test unexpectedly opened settings")
	}
	if !textPresent(tt, "新建定时任务") {
		t.Fatalf("automation page dialog missing: %q", tt.Texts())
	}
	a.cron.EditorOpen = false
	a.page = "skills"
	a.currentSession = "s1"
	a.skillhub.CatalogLoaded = true
	a.skillhub.SessionID = "s1"
	a.skillhub.DetailOpen = true
	a.skillhub.Detail = &skillHubDetail{skillHubSkill: skillHubSkill{Market: "skillhub.cn", ID: "review", Name: "review"}}
	tt = ui.NewTester(a.view, 1180, 900)
	if !textPresent(tt, "review") {
		t.Fatalf("skills page detail dialog missing: %q", tt.Texts())
	}
}

func TestOpenPageClosesSettingsAndSelectSessionReturnsToChat(t *testing.T) {
	a := testApp()
	a.settingsOpen = true
	a.openPage("automation")
	if a.settingsOpen || a.page != "automation" {
		t.Fatalf("openPage state = settings %v page %q", a.settingsOpen, a.page)
	}
	a.sessions = []listedSession{{SessionID: "s1", Cwd: "/work"}}
	a.selectSession(a.sessions[0])
	if a.page != "" {
		t.Fatalf("selectSession did not return to chat: page %q", a.page)
	}
}

func TestHomeQuickPromptsInsertDraft(t *testing.T) {
	a := testApp()
	a.dark = true
	a.themeSet = true
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"会议纪要", "邮件起草", "项目计划", "办公事务与知识生产"} {
		if !textPresent(tt, want) {
			t.Fatalf("home quick prompt missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("preset.working.sub") || tt.HasText("preset.working.desc") {
		t.Fatalf("home preset metadata leaked raw keys: %q", tt.Texts())
	}
	if err := tt.Click("邮件起草"); err != nil {
		t.Fatalf("click quick prompt: %v", err)
	}
	if !strings.Contains(a.draft, "请帮我起草一封专业商务邮件") {
		t.Fatalf("quick prompt did not fill composer: %q", a.draft)
	}
}

func TestEnglishLocaleRendersShellLabels(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en") // testApp resets to zh
	a.dark = true
	a.themeSet = true
	a.sessionsLoaded = true
	a.projectsLoaded = true
	a.sessions = []listedSession{{SessionID: "s1", Cwd: "/work", Title: "Task", UpdatedAt: "2026-11-11T09:00:00Z"}}
	a.projects = []projectView{{ID: "p1", Name: "Alpha"}}
	a.setFeatures(map[string]bool{"projects": true, "sessionMeta": true})
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"New task", "Search sessions", "History", "Skills", "Automation", "Library", "Settings", "Projects", "Recent", "Ungrouped"} {
		if !textPresent(tt, want) {
			t.Fatalf("English shell missing %q: %q", want, tt.Texts())
		}
	}
	if textPresent(tt, "新会话") || textPresent(tt, "应用设置") {
		t.Fatalf("Chinese shell labels leaked in English mode: %q", tt.Texts())
	}
}

func TestLanguageButtonSwitchesLocale(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	a.dark = true
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "appearance"
	a.settingsTab = "appearance"
	tt := ui.NewTester(a.view, 1180, 900)
	if err := tt.Click("English"); err != nil {
		t.Fatalf("click English: %v", err)
	}
	if currentLocale() != "en" {
		t.Fatalf("locale = %q", currentLocale())
	}
	setLocale("zh")
}

func TestEnglishLocaleRendersDedicatedPages(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.dark = true
	a.themeSet = true
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{
		"manageSkills":          true,
		"manageSkillHub":        true,
		"manageSkillHubCatalog": true,
		"manageCron":            true,
		"manageKnowledgeBases":  true,
	})
	a.skills.Loaded = true
	a.skillhub.ConfigLoaded = true
	a.skillhub.CatalogLoaded = true
	a.skillhub.SessionID = "s1"
	a.skillhub.SelectedMarket = "skillhub.cn"
	a.skillhub.MarketInfos = []skillHubMarketInfo{{ID: "skillhub.cn", Name: "SkillHub"}}
	a.skillhub.ResultsLoaded = true
	a.skillhub.Results = []skillHubSkill{{Market: "skillhub.cn", ID: "review", Name: "review", Description: "Review changes"}}
	a.cron.Loaded = true
	a.knowledge.Loaded = true
	a.knowledge.Views = []knowledgeBaseView{{
		KnowledgeBase: knowledgeBase{ID: "kb1", Name: "Docs", Enabled: true},
		Snapshot:      &knowledgeSnapshot{ID: "snap1", Status: "ready", FileCount: 2, ChunkCount: 7},
	}}

	a.page = "skills"
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"Skills", "Skill marketplace", "Manage local skills", "Marketplace configuration", "Online skills"} {
		if !textPresent(tt, want) {
			t.Fatalf("English skills page missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("preset.working.sub") {
		t.Fatalf("home preset metadata leaks into skills page: %q", tt.Texts())
	}
	a.page = "automation"
	tt = ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"Automation", "New task", "Scheduled tasks", "Enabled", "Runtime-owned scheduled tasks and recent run state"} {
		if !textPresent(tt, want) {
			t.Fatalf("English automation page missing %q: %q", want, tt.Texts())
		}
	}
	a.page = "library"
	tt = ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"Library", "Knowledge bases", "Manage knowledge bases", "Docs", "Runtime-owned knowledge index and evidence state"} {
		if !textPresent(tt, want) {
			t.Fatalf("English library page missing %q: %q", want, tt.Texts())
		}
	}
	for _, leaked := range []string{"新建任务", "管理本地技能", "管理知识库"} {
		if tt.HasText(leaked) {
			t.Fatalf("Chinese page label %q leaked in English mode: %q", leaked, tt.Texts())
		}
	}
}

func TestSkillsMarketShowsFriendlySessionState(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.dark = true
	a.themeSet = true
	a.setFeatures(map[string]bool{"manageSkillHubCatalog": true})
	a.page = "skills"
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"The skill marketplace needs an active session", "New task", "Open or create a conversation first"} {
		if !textPresent(tt, want) {
			t.Fatalf("session-required marketplace state missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("SkillHub 市场需要活动会话，请先在对话中打开或新建会话。") {
		t.Fatalf("raw session-required error leaked: %q", tt.Texts())
	}
}

func TestConnectionFailureStopsSidebarLoadingPlaceholder(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.dark = true
	a.themeSet = true
	a.setConn(connFailed, "ACP unavailable")
	tt := ui.NewTester(a.view, 1180, 760)
	if tt.HasText("Loading tasks…") {
		t.Fatalf("sidebar stayed in loading state after ACP failure: %q", tt.Texts())
	}
	if !textPresent(tt, "Not connected to MothX ACP") {
		t.Fatalf("connection failure banner missing: %q", tt.Texts())
	}
}

func TestSidebarShowsBackgroundAttentionCount(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := sidebarTestApp(t)
	setLocale("en")
	a.currentSession = "s1"
	a.sessions[1].Meta.LastRun = &sessionLastRun{Status: "waiting_for_approval"}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("1 background sessions need attention") {
		t.Fatalf("sidebar attention count missing: %q", tt.Texts())
	}
	a.currentSession = "s2"
	tt = ui.NewTester(a.view, 1180, 760)
	if tt.HasText("background sessions need attention") {
		t.Fatalf("attention count must exclude the current session: %q", tt.Texts())
	}
}
