package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Dynamic menus are rendered lazily when opened, so the page-level English scan
// cannot see them. These tests open the real menu surfaces and assert that the
// English locale never falls back to the Chinese source labels.
func TestEnglishSessionAndProjectMenusHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := sidebarTestApp(t)
	setLocale("en")
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("Session actions"); err != nil {
		t.Fatalf("open session menu: %v", err)
	}
	assertNoChineseUI(t, "session menu", tt.Texts())

	if err := tt.Click("Project actions"); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	assertNoChineseUI(t, "project menu", tt.Texts())
}

func TestEnglishComposerMenusHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })

	openAndScan := func(name, key string, setup func(a *nativeApp)) {
		t.Helper()
		a := testApp()
		setLocale("en")
		if setup != nil {
			setup(a)
		}
		tt := ui.NewTester(a.view, 1180, 760)
		if err := tt.Click(key); err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		assertNoChineseUI(t, name, tt.Texts())
	}

	openAndScan("attachment menu", "+", nil)
	openAndScan("capabilities menu", "Capabilities", func(a *nativeApp) {
		a.configOptions = []configOption{{
			ID: "sandbox", Name: "Sandbox", CurrentValue: "false",
			Options: []configChoice{{Value: "true", Name: "On"}, {Value: "false", Name: "Off"}},
		}}
	})
	openAndScan("model menu", "M1", func(a *nativeApp) {
		a.setFeatures(map[string]bool{"manageProviders": true})
		a.modelLabel = "M1"
		a.thinkingLabel = ""
		a.configOptions = []configOption{
			{ID: "provider", Name: "Provider", CurrentValue: "moark",
				Options: []configChoice{{Value: "moark", Name: "Moark"}, {Value: "other", Name: "Other"}}},
			{ID: "model", Name: "Model", CurrentValue: "m1",
				Options: []configChoice{{Value: "m1", Name: "M1"}}},
			{ID: "thinking_level", Name: "Thinking", CurrentValue: "off",
				Options: []configChoice{{Value: "off", Name: "Off"}, {Value: "high", Name: "High"}}},
		}
	})
	openAndScan("expert menu", "No expert", func(a *nativeApp) {
		a.configOptions = []configOption{{
			ID: "expert", Name: "Expert", CurrentValue: "",
			Options: []configChoice{{Value: "team-1", Name: "Team One"}},
		}}
	})
}

func TestEnglishDefaultSessionTitleIsLocalized(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.currentSession = "s1"
	a.currentTitle = "新会话"
	tt := ui.NewTester(a.view, 1180, 760)
	if tt.HasText("新会话") {
		t.Fatalf("default session title leaked Chinese in English mode: %q", tt.Texts())
	}
	if !tt.HasText("New task") {
		t.Fatalf("localized default session title missing: %q", tt.Texts())
	}
}

func TestStatusBarRendersOneContextProjectionWithCache(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.currentSession = "s1"
	a.running = false
	a.usageUsed = 5000
	a.usageSize = 100000
	a.usageCost = 0.0123
	a.usageCache = &usageCacheProjection{CacheRead: 100, TotalInputTokens: 1000}
	a.setFeatures(map[string]bool{"usageCacheProjection": true})
	tt := ui.NewTester(a.view, 1180, 760)
	count := 0
	cacheShown := false
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "ctx ") {
			count++
		}
		if strings.Contains(text, "cache 10%") {
			cacheShown = true
		}
	}
	if count != 1 {
		t.Fatalf("status bar rendered %d context projections, want 1: %q", count, tt.Texts())
	}
	if !cacheShown {
		t.Fatalf("status bar did not render the prompt-cache hit rate: %q", tt.Texts())
	}
}

func TestComposerMenusOpenAboveTheBottomBar(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.configOptions = []configOption{{ID: "browser", Name: "Browser", CurrentValue: "false"}}
	tt := ui.NewTester(a.view, 1920, 1097)
	button, ok := tt.Find("能力")
	if !ok {
		t.Fatalf("capabilities button missing: %q", tt.Texts())
	}
	if err := tt.Click("能力"); err != nil {
		t.Fatalf("open capabilities menu: %v", err)
	}
	if menu := tt.Menu(); len(menu) != 1 || menu[0] != "Browser" {
		t.Fatalf("capabilities menu = %q", menu)
	}
	item, ok := tt.Find("Browser")
	if !ok {
		t.Fatalf("capabilities menu item not rendered: %q", tt.Texts())
	}
	// A short menu at the window's bottom edge must open upward; otherwise it
	// is drawn behind the composer hint/status bar and looks like no-op.
	if item.Y+item.H > button.Y {
		t.Fatalf("capabilities menu at y=%.0f..%.0f overlaps the composer button y=%.0f", item.Y, item.Y+item.H, button.Y)
	}
}

func TestHomePresetTabsShareOneRow(t *testing.T) {
	a := testApp()
	tt := ui.NewTester(a.view, 1180, 760)
	working, okWorking := tt.Find("办公")
	coding, okCoding := tt.Find("代码")
	design, okDesign := tt.Find("创作")
	if !okWorking || !okCoding || !okDesign {
		t.Fatalf("home preset tabs missing: work=%v code=%v design=%v texts=%q",
			okWorking, okCoding, okDesign, tt.Texts())
	}
	if working.Y != coding.Y || coding.Y != design.Y {
		t.Fatalf("home preset tabs are not on one row: work=%+v code=%+v design=%+v", working, coding, design)
	}
	if !(working.X < coding.X && coding.X < design.X) {
		t.Fatalf("home preset tabs are not ordered horizontally: work=%+v code=%+v design=%+v", working, coding, design)
	}
}

func TestEnglishCollaborationDialogsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })

	cases := []struct {
		name  string
		setup func(a *nativeApp)
	}{
		{"worktree", func(a *nativeApp) {
			a.worktree = newWorktreePanelState()
			a.worktree.Open = true
			a.worktree.BaseCwd = "/work/native"
			a.worktree.Loaded = true
		}},
		{"router", func(a *nativeApp) {
			a.router = newRouterPanelState()
			a.router.Open = true
			a.router.SessionID = "s1"
			a.router.Loaded = true
			a.router.PendingQuestions = []workerQuestionView{{
				ID: "q-1", RunID: "run-1", ChildID: "child-1",
				Question: "Which branch should I use?", Options: []string{"main", "dev"},
			}}
			a.router.Messages = []workerMessageView{{ID: "m-1", Direction: "outbound", Kind: "question", ChildID: "child-1", Status: "pending", Body: "Review the diff"}}
		}},
		{"subagent", func(a *nativeApp) {
			a.subagent = subagentPanelState{Open: true, SessionID: "s1", Loaded: true, Agents: []subagentInfoView{{
				AgentID: "a1", MemberDisplayName: "Writer", Status: "completed", MessageCount: 3,
			}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp()
			setLocale("en")
			a.currentSession = "s1"
			a.setFeatures(map[string]bool{
				"worktrees":      true,
				"worktreeReset":  true,
				"manageRouter":   true,
				"workerMessages": true,
				"subagentPanel":  true,
			})
			tc.setup(a)
			tt := ui.NewTester(a.view, 1180, 900)
			assertNoChineseUI(t, tc.name+" dialog", tt.Texts())
			if tc.name == "subagent" && !textPresent(tt, "3 messages") {
				t.Fatalf("subagent message count missing: %q", tt.Texts())
			}
		})
	}
}

func TestEnglishWorktreeConfirmDialogsHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	for _, kind := range []string{worktreeConfirmRemove, worktreeConfirmReset} {
		t.Run(kind, func(t *testing.T) {
			a := testApp()
			setLocale("en")
			a.currentSession = "s1"
			a.setFeatures(map[string]bool{"worktrees": true, "worktreeReset": true})
			a.worktree = newWorktreePanelState()
			a.worktree.Open = true
			a.worktree.BaseCwd = "/work/native"
			a.worktree.Loaded = true
			a.worktree.ConfirmOpen = true
			a.worktree.ConfirmKind = kind
			a.worktree.ConfirmTree = worktreeView{ID: "wt-1", Name: "feature", Directory: "/work/native-feature", Branch: "feature", Status: "ready"}
			tt := ui.NewTester(a.view, 1180, 900)
			assertNoChineseUI(t, "worktree confirm "+kind, tt.Texts())
			want := "Remove worktree?"
			if kind == worktreeConfirmReset {
				want = "Reset worktree?"
			}
			if !textPresent(tt, want) {
				t.Fatalf("localized confirmation %q missing: %q", want, tt.Texts())
			}
		})
	}
}

func TestEnglishRouterWorkerActivityHasNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.currentSession = "s1"
	a.setFeatures(map[string]bool{"manageRouter": true, "workerMessages": true})
	a.router = newRouterPanelState()
	a.router.Open = true
	a.router.SessionID = "s1"
	a.router.Loaded = true
	a.router.PendingQuestions = []workerQuestionView{{
		ID: "q-1", RunID: "run-1", ChildID: "child-1",
		Question: "Which branch should I use?", Options: []string{"main", "dev"},
	}}
	a.router.Messages = []workerMessageView{{
		ID: "m-1", Direction: "outbound", Kind: "question", ChildID: "child-1",
		Status: "pending", Body: "Review the diff",
	}}
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "router worker activity", tt.Texts())
	for _, want := range []string{"Sub-agent child-1", "Run run-1", "Options: main / dev"} {
		if !textPresent(tt, want) {
			t.Fatalf("router worker activity missing %q: %q", want, tt.Texts())
		}
	}
}

func TestEnglishDeliveryFailuresHaveNoUntranslatedChinese(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := deliveryTestApp(t)
	setLocale("en")
	a.currentSession = "s1"
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "delivery failures", tt.Texts())
	for _, want := range []string{"Action op-3", "Session s1", "Retry delivery op-1"} {
		if !textPresent(tt, want) {
			t.Fatalf("delivery failures missing %q: %q", want, tt.Texts())
		}
	}
}

func TestEnglishKnowledgeScheduleAndRunDetailsHaveNoUntranslatedChinese(t *testing.T) {
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
		"knowledgeRuns":        true,
		"knowledgeSources":     true,
		"knowledgeGraph":       true,
	})
	a.knowledge.Loaded = true
	a.knowledge.Views = []knowledgeBaseView{{
		KnowledgeBase: knowledgeBase{ID: "kb1", Name: "Docs", Enabled: true, RootDir: "/work/docs"},
		Snapshot:      &knowledgeSnapshot{ID: "snap1", Status: "ready", FileCount: 2},
	}}
	a.knowledge.Schedules["kb1"] = knowledgeScheduleView{
		ID: "kb1", Enabled: true, Schedule: "@daily", Configured: true,
		NextRun: "2026-11-12T03:00:00Z", LastStatus: "成功", RootAvailable: false,
	}
	a.knowledge.Insights["kb1"] = &knowledgeInsightsState{
		RunsLoaded: true,
		Tab:        0,
		Runs: []knowledgeIndexRunView{{
			RunID: "run-1", SnapshotID: "snap1", Status: "failed",
			StartedAt: "2026-11-11T09:00:00Z", FinishedAt: "2026-11-11T09:10:00Z",
		}},
		RunDetail: &knowledgeIndexRunView{RunID: "run-1", SnapshotID: "snap1", ErrorCode: "E_BOOM"},
		QueryResult: &knowledgeGraphQuery{
			Chunks:    []knowledgeChunk{{ID: "chunk-1", RelativePath: "docs/a.md", StartLine: 1, EndLine: 3, Text: "Evidence text"}},
			Nodes:     []knowledgeNode{{ID: "n1", Kind: "concept", Label: "Node"}},
			Truncated: true,
		},
	}
	a.ensureSettingsTabVisible()
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "knowledge schedule and run detail", tt.Texts())
	for _, want := range []string{"Next run: ", "Last result: Success", "Run run-1 · Snapshot snap1"} {
		if !textPresent(tt, want) {
			t.Fatalf("knowledge detail missing %q: %q", want, tt.Texts())
		}
	}

	// The paused schedule exposes the resume action instead of pause.
	schedule := a.knowledge.Schedules["kb1"]
	schedule.Enabled = false
	a.knowledge.Schedules["kb1"] = schedule
	tt = ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "knowledge resume schedule", tt.Texts())
}

func TestPermissionOptionLabelsKeepRuntimeNames(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	if got := permissionOptionLabel("Allow once", "allow_once"); got != "Allow once" {
		t.Fatalf("runtime name was replaced: %q", got)
	}
	if got := permissionOptionLabel("", "reject_once"); got != "Reject" {
		t.Fatalf("fallback label = %q, want Reject", got)
	}
	if got := permissionOptionLabel("", "allow_once"); got != "Allow once" {
		t.Fatalf("fallback allow label = %q", got)
	}
}

func TestSessionDisplayTitleLocalizesRuntimeDefaults(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	if got := sessionDisplayTitle("未命名会话"); got != "Untitled session" {
		t.Fatalf("untitled default = %q", got)
	}
	if got := sessionDisplayTitle("新会话"); got != "New task" {
		t.Fatalf("new-session default = %q", got)
	}
	if got := sessionDisplayTitle("Fix the parser"); got != "Fix the parser" {
		t.Fatalf("user title must stay verbatim: %q", got)
	}
}

func TestEnglishHistoryListLocalizesUntitledSessions(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.historyOpen = true
	a.historyPanel.Loaded = true
	a.sessionsLoaded = true
	a.projectsLoaded = true
	a.sessions = []listedSession{{SessionID: "s1", Title: "未命名会话", Cwd: "/work"}}
	a.setFeatures(map[string]bool{"projects": true})
	tt := ui.NewTester(a.view, 1180, 900)
	assertNoChineseUI(t, "history list", tt.Texts())
	if !tt.HasText("Untitled session") {
		t.Fatalf("localized untitled title missing: %q", tt.Texts())
	}
}

func TestHistoryWaitsForACPFeaturesInsteadOfCachingFallback(t *testing.T) {
	a := testApp()
	a.conn = connStarting
	a.historyOpen = true
	a.startHistoryLoad(false)
	if a.historyPanel.Loaded {
		t.Fatalf("history marked loaded before initialize: %+v", a.historyPanel)
	}
	if !a.historyPanel.Loading {
		t.Fatalf("history must show loading while initialize is pending: %+v", a.historyPanel)
	}
	a.setFeatures(map[string]bool{"sessionListAll": true})
	if !a.featuresInitialized() {
		t.Fatal("features not marked ready")
	}
}

func TestDecisionDeadlineLabelIsLocalized(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	label := decisionDeadlineLabel(time.Now().Add(90 * time.Second))
	if !strings.Contains(label, "Times out") {
		t.Fatalf("deadline label = %q", label)
	}
	assertNoChineseUI(t, "decision deadline", []string{label})
	if got := decisionDeadlineLabel(time.Now().Add(-time.Second)); got != "Approval deadline reached" {
		t.Fatalf("expired deadline label = %q", got)
	}
}
