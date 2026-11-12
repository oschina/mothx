package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func historyTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{
		"sessionListAll": true,
		"projects":       true,
		"sessionMeta":    true,
		"sessionFork":    true,
		"sessionWorkDir": true,
	})
	a.history = true
	a.historyOpen = true
	a.historyPanel = newHistoryState()
	a.historyPanel.Loaded = true
	a.historyPanel.Sessions = []listedSession{
		{
			SessionID: "s1", Cwd: "/work/alpha", Title: "重构解析器", Provider: "deepseek", Model: "v4",
			UpdatedAt: "2026-11-11T09:00:00Z",
			Meta:      sessionMeta{Pinned: true, ProjectID: "p1", LastRun: &sessionLastRun{Status: "completed"}},
		},
		{
			SessionID: "s2", Cwd: "/work/beta", Title: "修复构建", Provider: "openai", Model: "gpt",
			UpdatedAt: "2026-11-10T09:00:00Z",
		},
	}
	a.projects = []projectView{
		{ID: "p1", Name: "MothX", SessionCount: historyIntPtr(1)},
		{ID: "p2", Name: "Native"},
	}
	return a
}

func historyIntPtr(value int) *int { return &value }

func TestBuildHistoryListRequest(t *testing.T) {
	if params := buildHistoryListRequest("", "", "", ""); len(params) != 0 {
		t.Fatalf("default request must be empty: %#v", params)
	}
	params := buildHistoryListRequest("project", " p1 ", " 会话 ", " c1 ")
	if params["scope"] != "project" || params["projectId"] != "p1" || params["query"] != "会话" || params["cursor"] != "c1" {
		t.Fatalf("project request = %#v", params)
	}
	params = buildHistoryListRequest("ungrouped", "ignored", "", "")
	if params["scope"] != "ungrouped" {
		t.Fatalf("ungrouped request = %#v", params)
	}
	if _, exists := params["projectId"]; exists {
		t.Fatalf("ungrouped request must not carry projectId: %#v", params)
	}
	if scope, project := normalizeHistoryScope("project", ""); scope != "all" || project != "" {
		t.Fatalf("project scope without id = %q,%q", scope, project)
	}
	if scope, project := normalizeHistoryScope("", "p2"); scope != "project" || project != "p2" {
		t.Fatalf("implicit project scope = %q,%q", scope, project)
	}
	if scope, project := normalizeHistoryScope("bogus", "p2"); scope != "project" || project != "p2" {
		t.Fatalf("bogus scope with id = %q,%q", scope, project)
	}
}

func TestHistoryPageAppendAndStaleState(t *testing.T) {
	a := historyTestApp(t)
	first := sessionListResult{
		Sessions:   []listedSession{{SessionID: "s1", Title: "one"}},
		NextCursor: "c1",
	}
	a.applyHistoryPage(&first, false)
	if len(a.historyPanel.Sessions) != 1 || a.historyPanel.NextCursor != "c1" || !a.historyPanel.Loaded {
		t.Fatalf("first page = %+v", a.historyPanel)
	}
	second := sessionListResult{
		Sessions: []listedSession{{SessionID: "s2", Title: "two"}},
	}
	a.applyHistoryPage(&second, true)
	if len(a.historyPanel.Sessions) != 2 || a.historyPanel.Sessions[1].SessionID != "s2" || a.historyPanel.NextCursor != "" {
		t.Fatalf("appended page = %+v", a.historyPanel)
	}
	a.applyHistoryPage(&first, false)
	if len(a.historyPanel.Sessions) != 1 || a.historyPanel.Sessions[0].SessionID != "s1" {
		t.Fatalf("replace page = %+v", a.historyPanel)
	}

	a.closeHistory()
	if a.historyOpen || a.historyPanel.Loaded || len(a.historyPanel.Sessions) != 0 {
		t.Fatalf("closeHistory must clear the projection: %+v", a.historyPanel)
	}
}

func TestHistoryRequestBuildersCarryWorkspaceMeta(t *testing.T) {
	session := listedSession{SessionID: "s1", Cwd: "/work/alpha"}
	title := buildSessionTitleRequest(session, " 新标题 ")
	if title["sessionId"] != "s1" || title["title"] != "新标题" {
		t.Fatalf("title request = %#v", title)
	}
	meta, _ := title["_meta"].(map[string]any)
	workspace, _ := meta["mothx"].(map[string]any)
	wc, _ := workspace["workspace"].(map[string]any)
	if wc["cwd"] != "/work/alpha" {
		t.Fatalf("title workspace meta = %#v", title)
	}

	pinned := true
	projectID := "p1"
	metaRequest := buildSessionMetaRequest(session, &pinned, &projectID)
	if metaRequest["pinned"] != true || metaRequest["projectId"] != "p1" {
		t.Fatalf("meta request = %#v", metaRequest)
	}
	empty := ""
	clear := buildSessionMetaRequest(session, nil, &empty)
	if value, exists := clear["projectId"]; !exists || value != nil {
		t.Fatalf("clear project must send null: %#v", clear)
	}
	if _, exists := clear["pinned"]; exists {
		t.Fatalf("absent pinned must stay absent: %#v", clear)
	}

	fork := buildSessionForkRequest(session)
	if fork["cwd"] != "/work/alpha" {
		t.Fatalf("fork request = %#v", fork)
	}
	forkMeta := fork["_meta"].(map[string]any)["mothx"].(map[string]any)
	if forkMeta["parentSessionId"] != "s1" {
		t.Fatalf("fork parent = %#v", forkMeta)
	}

	workdir := buildSessionWorkDirRequest(session, " /work/beta ")
	if workdir["cwd"] != "/work/beta" || workdir["sessionId"] != "s1" {
		t.Fatalf("workdir request = %#v", workdir)
	}

	create := buildProjectRequest("", " 新项目 ")
	if _, exists := create["id"]; exists || create["name"] != "新项目" {
		t.Fatalf("create project request = %#v", create)
	}
	rename := buildProjectRequest("p1", "改名")
	if rename["id"] != "p1" || rename["name"] != "改名" {
		t.Fatalf("rename project request = %#v", rename)
	}
}

func TestSessionProjectIDDecoding(t *testing.T) {
	if got := sessionProjectID(listedSession{Meta: sessionMeta{ProjectID: "p1"}}); got != "p1" {
		t.Fatalf("string project = %q", got)
	}
	if got := sessionProjectID(listedSession{Meta: sessionMeta{ProjectID: nil}}); got != "" {
		t.Fatalf("null project = %q", got)
	}
	if got := sessionProjectID(listedSession{}); got != "" {
		t.Fatalf("absent project = %q", got)
	}
}

func TestHistoryLocalUpdates(t *testing.T) {
	a := historyTestApp(t)
	a.currentSession = "s1"
	a.currentTitle = "重构解析器"
	a.updateSessionTitleLocal("s1", "新标题")
	if a.currentTitle != "新标题" || a.historyPanel.Sessions[0].Title != "新标题" {
		t.Fatalf("local title update failed: title=%q sessions=%+v", a.currentTitle, a.historyPanel.Sessions[0])
	}
	a.updateSessionProject("s1", "p2")
	if sessionProjectID(a.historyPanel.Sessions[0]) != "p2" {
		t.Fatalf("local project update failed: %+v", a.historyPanel.Sessions[0].Meta)
	}
	a.updateSessionProject("s1", "")
	if sessionProjectID(a.historyPanel.Sessions[0]) != "" {
		t.Fatalf("local project clear failed: %+v", a.historyPanel.Sessions[0].Meta)
	}
	a.updateSessionCwdLocal("s1", "/work/gamma")
	if a.workspace != "/work/gamma" || a.historyPanel.Sessions[0].Cwd != "/work/gamma" {
		t.Fatalf("local cwd update failed: %+v cwd=%q", a.historyPanel.Sessions[0], a.workspace)
	}
	a.setSessionPinnedLocal("s1", false)
	if a.historyPanel.Sessions[0].Meta.Pinned {
		t.Fatalf("local pin update failed")
	}
}

func TestHistoryViewRendersCompactSessionMenu(t *testing.T) {
	a := historyTestApp(t)
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"会话历史", "全部", "未归类", "MothX (1)", "Native", "新建项目", "重构解析器", "修复构建", "已完成", "会话操作"} {
		if !textPresent(tt, want) {
			t.Fatalf("history view missing %q; texts = %q", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"改名", "Fork", "目录", "取消置顶"} {
		if tt.HasText(unwanted) {
			t.Fatalf("history row must not render the old text action %q: %q", unwanted, tt.Texts())
		}
	}

	if err := tt.Click("会话操作"); err != nil {
		t.Fatalf("open session menu: %v", err)
	}
	menu := tt.Menu()
	for _, want := range []string{"取消置顶", "重命名…", "移动到项目", "设置工作目录…", "Fork 会话", "删除会话…"} {
		found := false
		for _, item := range menu {
			if item == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("session menu missing %q: %v", want, menu)
		}
	}
	if err := tt.ChooseMenuItem("重命名…"); err != nil {
		t.Fatalf("choose rename: %v", err)
	}
	if !a.historyPanel.DialogOpen || a.historyPanel.DialogKind != historyDialogRename {
		t.Fatalf("rename dialog state = %+v", a.historyPanel)
	}
	if !textPresent(tt, "重命名会话") {
		t.Fatalf("rename dialog not rendered: %q", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click cancel: %v", err)
	}
	if a.historyPanel.DialogOpen {
		t.Fatalf("cancel must close the dialog")
	}
}

func TestHistoryDeleteDialogRequiresConfirmation(t *testing.T) {
	a := historyTestApp(t)
	a.openHistoryDialog(historyDialogDelete, "s2")
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"删除会话？", "修复构建", "确认删除"} {
		if !textPresent(tt, want) {
			t.Fatalf("delete dialog missing %q: %q", want, tt.Texts())
		}
	}
	a.closeHistoryDialog()
	if a.historyPanel.DialogOpen || a.historyPanel.DialogKind != "" {
		t.Fatalf("dialog was not closed: %+v", a.historyPanel)
	}
}

func TestHistoryProjectFilterIsExplicit(t *testing.T) {
	a := historyTestApp(t)
	a.historyPanel.Scope = "ungrouped"
	a.historyPanel.ProjectID = ""
	params := buildHistoryListRequest(a.historyPanel.Scope, a.historyPanel.ProjectID, a.historyPanel.AppliedQuery, "")
	if params["scope"] != "ungrouped" {
		t.Fatalf("ungrouped filter request = %#v", params)
	}
	if _, ok := params["projectId"]; ok {
		t.Fatalf("ungrouped filter leaked projectId: %#v", params)
	}
	if reason := a.historyEmptyReason(); !strings.Contains(reason, "未归类") {
		t.Fatalf("empty reason = %q", reason)
	}
	a.historyPanel.Scope = "project"
	a.historyPanel.ProjectID = "p2"
	if reason := a.historyEmptyReason(); !strings.Contains(reason, "Native") {
		t.Fatalf("project empty reason = %q", reason)
	}
}

func TestHistoryFallbackFiltersSidebarCatalog(t *testing.T) {
	a := historyTestApp(t)
	a.setFeatures(map[string]bool{})
	a.sessions = []listedSession{
		{SessionID: "s1", Title: "Alpha", Cwd: "/a"},
		{SessionID: "s2", Title: "Beta", Cwd: "/b"},
	}
	a.historyQuery = "alp"
	a.historyPanel.AppliedQuery = "alp"
	visible := a.historyVisibleSessions()
	if len(visible) != 1 || visible[0].SessionID != "s1" {
		t.Fatalf("fallback filter = %+v", visible)
	}
}

func TestHistoryProjectDialogStates(t *testing.T) {
	a := historyTestApp(t)
	a.openProjectDialog(projectDialogRename, "p1")
	if !a.historyPanel.ProjectDialogOpen || a.historyPanel.ProjectDialogName != "MothX" {
		t.Fatalf("project rename dialog = %+v", a.historyPanel)
	}
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"重命名项目", "MothX"} {
		if !textPresent(tt, want) {
			t.Fatalf("project dialog missing %q: %q", want, tt.Texts())
		}
	}
	a.closeProjectDialog()

	a.openProjectDialog(projectDialogDelete, "p2")
	if a.historyPanel.ProjectDialogName != "Native" {
		t.Fatalf("project delete dialog = %+v", a.historyPanel)
	}
	tt = ui.NewTester(a.view, 1180, 900)
	if !textPresent(tt, "删除项目「Native」") {
		t.Fatalf("project delete copy missing: %q", tt.Texts())
	}
}

func TestHistoryRefreshCancelsInFlightPage(t *testing.T) {
	a := historyTestApp(t)
	a.historyPanel.Loading = true
	a.historyPanel.NextCursor = "stale-cursor"
	seq := a.historyPanel.Seq
	a.refreshHistory()
	if a.historyPanel.Seq <= seq {
		t.Fatalf("refresh must invalidate the in-flight page: seq %d -> %d", seq, a.historyPanel.Seq)
	}
	if a.historyPanel.NextCursor != "" {
		t.Fatalf("refresh must clear the cursor, got %q", a.historyPanel.NextCursor)
	}
}

func TestHistoryProjectIDJSONCompatibility(t *testing.T) {
	// The server serializes projectId as a string or null in _meta; both must
	// decode without inventing a project.
	raw := json.RawMessage(`{"sessionId":"s1","cwd":"/w","_meta":{"projectId":null}}`)
	var s listedSession
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if sessionProjectID(s) != "" {
		t.Fatalf("null projectId decoded as %q", sessionProjectID(s))
	}
	raw = json.RawMessage(`{"sessionId":"s1","cwd":"/w","_meta":{"projectId":"p9"}}`)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if sessionProjectID(s) != "p9" {
		t.Fatalf("string projectId decoded as %q", sessionProjectID(s))
	}
}

func TestHistorySessionStatusUsesExecutionSnapshot(t *testing.T) {
	cases := []struct{ state, want string }{
		{"reserved", "Session 正被占用"},
		{"local", "正在此进程执行"},
		{"external", "正在另一进程执行"},
		{"detached_remote", "远端任务仍在执行"},
		{"orphaned", "正在恢复遗留执行"},
		{"recovery_failed", "遗留执行恢复失败"},
		{"inconsistent", "正在确认执行状态"},
		{"unknown", "无法确认执行状态"},
	}
	for _, tc := range cases {
		s := listedSession{Meta: sessionMeta{Execution: &executionState{State: tc.state}}}
		if got := historySessionStatus(s); got != tc.want {
			t.Errorf("execution state %q = %q, want %q", tc.state, got, tc.want)
		}
	}
	// The ownership snapshot wins over the durable lastRun status.
	s := listedSession{Meta: sessionMeta{
		Execution: &executionState{State: "external"},
		LastRun:   &sessionLastRun{Active: true, Status: "running"},
	}}
	if got := historySessionStatus(s); got != "正在另一进程执行" {
		t.Fatalf("status = %q, want execution label", got)
	}
	// An idle snapshot falls back to the durable lastRun projection.
	s = listedSession{Meta: sessionMeta{
		Execution: &executionState{State: "idle"},
		LastRun:   &sessionLastRun{Status: "completed"},
	}}
	if got := historySessionStatus(s); got != "已完成" {
		t.Fatalf("idle execution status = %q, want lastRun label", got)
	}
}

func TestSessionListDecodesExecutionSnapshot(t *testing.T) {
	const raw = `{"sessions":[{"sessionId":"s1","_meta":{"lastRun":{"active":true,"status":"running"},"execution":{"sessionId":"s1","state":"external","running":true,"busy":true,"canSubmit":false,"canCancelLocal":false,"canCancelRemote":true,"ownerScope":"external"}}}]}`
	var result sessionListResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	exec := result.Sessions[0].Meta.Execution
	if exec == nil || exec.State != "external" || !exec.Running || !exec.Busy || !exec.CanCancelRemote {
		t.Fatalf("execution snapshot = %+v", exec)
	}
	if got := historySessionStatus(result.Sessions[0]); got != "正在另一进程执行" {
		t.Fatalf("history status = %q", got)
	}
}

func TestHistoryRowShowsAnotherProcessExecution(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	a := historyTestApp(t)
	a.historyPanel.Sessions[1].Meta.Execution = &executionState{State: "external", Running: true, Busy: true}
	tt := ui.NewTester(a.view, 1180, 900)
	if !textPresent(tt, "正在另一进程执行") {
		t.Fatalf("history row missing cross-process status: %q", tt.Texts())
	}
	setLocale("en")
	tt.Frame()
	if !textPresent(tt, "Running in another process") {
		t.Fatalf("English history row missing cross-process status: %q", tt.Texts())
	}

	a.setSessionRunLocal("s2", "completed", false)
	if a.historyPanel.Sessions[1].Meta.Execution != nil {
		t.Fatal("terminal runs must clear the stale execution snapshot")
	}
}
