package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func testApp() *nativeApp {
	setLocale("zh")
	a := newNativeApp()
	a.conn = connReady
	a.win = nil
	return a
}

func TestUpdateReducerAggregatesChunks(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{
		SessionID: "s1",
		Update: sessionUpdate{
			SessionUpdate: "user_message_chunk",
			MessageID:     "u1",
			Content:       json.RawMessage(`{"type":"text","text":"hello"}`),
		},
	})
	a.applyUpdate(sessionUpdateNotification{
		SessionID: "s1",
		Update: sessionUpdate{
			SessionUpdate: "agent_thought_chunk",
			MessageID:     "t1",
			Content:       json.RawMessage(`{"type":"text","text":"considering"}`),
		},
	})
	a.applyUpdate(sessionUpdateNotification{
		SessionID: "s1",
		Update: sessionUpdate{
			SessionUpdate: "agent_message_chunk",
			MessageID:     "m1",
			Content:       json.RawMessage(`{"type":"text","text":"Hi "}`),
		},
	})
	a.applyUpdate(sessionUpdateNotification{
		SessionID: "s1",
		Update: sessionUpdate{
			SessionUpdate: "agent_message_chunk",
			MessageID:     "m1",
			Content:       json.RawMessage(`{"type":"text","text":"there"}`),
		},
	})
	if len(a.messages) != 3 {
		t.Fatalf("messages = %d, want 3: %+v", len(a.messages), a.messages)
	}
	if got := a.messages[0].Text; got != "hello" {
		t.Errorf("user text = %q", got)
	}
	if got := a.messages[1].Text; got != "considering" {
		t.Errorf("thought text = %q", got)
	}
	if got := a.messages[2].Text; got != "Hi there" {
		t.Errorf("assistant text = %q", got)
	}
	a.markRunDone("end_turn")
	for _, m := range a.messages {
		if !m.Done {
			t.Errorf("%s not done after run", m.ID)
		}
	}
}

func TestUserEchoReplacesOptimisticMessage(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	local := a.appendMessage(&chatMessage{ID: "user:local:1", Kind: messageUser, Text: "hello", Done: true})
	a.awaitingUserEcho = true
	a.localUserMessageID = local.ID
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "user_message_chunk",
		MessageID:     "u1",
		Content:       json.RawMessage(`{"type":"text","text":"hello"}`),
	}})
	if len(a.messages) != 1 {
		t.Fatalf("messages = %d, want 1: %+v", len(a.messages), a.messages)
	}
	if a.messages[0].Text != "hello" || a.awaitingUserEcho {
		t.Fatalf("echo state = %+v, awaiting %v", a.messages[0], a.awaitingUserEcho)
	}
}

func TestToolCallMergesUpdates(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    "c1",
		Title:         "bash",
		Kind:          "execute",
		Status:        "pending",
		RawInput:      map[string]any{"command": "go test ./..."},
	}})
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "tool_call_update",
		ToolCallID:    "c1",
		Status:        "completed",
		Content:       raw(`[{"type":"content","content":{"type":"text","text":"ok"}}]`),
	}})
	if len(a.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(a.messages))
	}
	m := a.messages[0]
	if m.Kind != messageTool || m.ToolStatus != "completed" || m.ToolText != "ok" {
		t.Fatalf("tool message = %+v", m)
	}
	if !strings.Contains(m.ToolTitle, "go test") {
		t.Errorf("tool title = %q", m.ToolTitle)
	}
	if !m.Done {
		t.Error("tool not marked done")
	}
}

func TestSidebarTaskTreeGroupsProjectsRecentAndUngrouped(t *testing.T) {
	a := testApp()
	a.sessions = []listedSession{
		{SessionID: "1", Cwd: "/work/old", Title: "Old", UpdatedAt: "2026-11-09T10:00:00Z"},
		{SessionID: "2", Cwd: "/work/current", Title: "Current", UpdatedAt: "2026-11-01T10:00:00Z"},
		{SessionID: "3", Cwd: "/work/old", Title: "Grouped", UpdatedAt: "2026-11-10T10:00:00Z", Meta: sessionMeta{ProjectID: "p1"}},
	}
	a.projects = []projectView{{ID: "p1", Name: "MothX"}}
	tree := a.taskTree()
	if len(tree.Projects) != 1 || len(tree.Projects[0].Sessions) != 1 || tree.Projects[0].Sessions[0].SessionID != "3" {
		t.Fatalf("project tree = %+v", tree.Projects)
	}
	if len(tree.Recent) != 3 || tree.Recent[0].SessionID != "3" {
		t.Fatalf("recent tree = %+v", tree.Recent)
	}
	if len(tree.Ungrouped) != 2 {
		t.Fatalf("ungrouped tree = %+v", tree.Ungrouped)
	}
	a.search = "grouped"
	tree = a.taskTree()
	if len(tree.Projects) != 1 || len(tree.Projects[0].Sessions) != 1 {
		t.Fatalf("filtered project tree = %+v", tree.Projects)
	}
	if len(tree.Ungrouped) != 0 {
		t.Fatalf("filtered ungrouped tree = %+v", tree.Ungrouped)
	}
}

func TestEmptyViewRendersNativeChatShell(t *testing.T) {
	a := testApp()
	a.dark = true
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText(nativeChineseSlogan) {
		t.Fatalf("empty state tagline missing: %q", tt.Texts())
	}
	if !tt.HasText("新会话") || !tt.HasText("NATIVE") {
		t.Fatalf("sidebar labels missing: %q", tt.Texts())
	}
	if !tt.HasText("Enter 发送 · Shift + Enter 换行") {
		t.Fatalf("composer hint missing: %q", tt.Texts())
	}
}

func TestChatViewRendersMessages(t *testing.T) {
	a := testApp()
	a.dark = true
	a.currentSession = "s1"
	a.currentTitle = "认识 MothX Native"
	a.messages = []*chatMessage{
		{ID: "u", Kind: messageUser, Text: "你好", Done: true},
		{ID: "a", Kind: messageAssistant, Text: "你好，这是 **原生** 回复。", Done: true},
	}
	a.messageIndex = map[string]*chatMessage{"u": a.messages[0], "a": a.messages[1]}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("认识 MothX Native") {
		t.Fatalf("session title missing: %q", tt.Texts())
	}
	if !tt.HasText("你好，这是") {
		t.Fatalf("assistant markdown missing: %q", tt.Texts())
	}
}

func TestNativeWindowOptionsHideSystemTitleBar(t *testing.T) {
	old := nativeIsMac
	t.Cleanup(func() { nativeIsMac = old })

	nativeIsMac = true
	mac := nativeWindowOptions()
	if mac.TitleBarStyle != mygo.TitleBarHidden {
		t.Fatalf("macOS title bar style = %q, want hidden", mac.TitleBarStyle)
	}
	if mac.HideWindowButtons {
		t.Fatal("macOS must keep native traffic lights")
	}
	if mac.TrafficLightPosition == nil || mac.TrafficLightPosition.X != 12 || mac.TrafficLightPosition.Y != 12 {
		t.Fatalf("macOS traffic light position = %+v", mac.TrafficLightPosition)
	}

	nativeIsMac = false
	other := nativeWindowOptions()
	if other.TitleBarStyle != mygo.TitleBarHidden {
		t.Fatalf("non-macOS title bar style = %q, want hidden", other.TitleBarStyle)
	}
	if !other.HideWindowButtons {
		t.Fatal("non-macOS must draw its own window controls")
	}
	if other.TitleBarHeight != nativeTitleBarHeightOther {
		t.Fatalf("non-macOS title bar height = %d, want %d", other.TitleBarHeight, nativeTitleBarHeightOther)
	}
}

func TestTitleBarDistinguishesMacAndOtherPlatforms(t *testing.T) {
	old := nativeIsMac
	t.Cleanup(func() { nativeIsMac = old })

	nativeIsMac = false
	a := testApp()
	a.dark = true
	tester := ui.NewTester(a.view, 1180, 760)
	if !tester.HasText("MothX") {
		t.Fatalf("title bar brand missing: %q", tester.Texts())
	}
	if _, ok := tester.Find("最小化"); !ok {
		t.Fatal("non-macOS title bar must draw a minimize control")
	}
	if _, ok := tester.Find("最大化"); !ok {
		t.Fatal("non-macOS title bar must draw a maximize control")
	}
	if _, ok := tester.Find("关闭窗口"); !ok {
		t.Fatal("non-macOS title bar must draw a close control")
	}

	nativeIsMac = true
	a = testApp()
	a.dark = true
	tester = ui.NewTester(a.view, 1180, 760)
	tester.SetTitleBar(ui.TitleBar{Height: nativeTitleBarHeightMac, Left: 78})
	if !tester.HasText("Make OSCHINA Tokens Harness eXecution") {
		t.Fatalf("macOS title bar tagline missing: %q", tester.Texts())
	}
	if _, ok := tester.Find("关闭窗口"); ok {
		t.Fatal("macOS must use native traffic lights, not drawn controls")
	}
	brand, ok := tester.Find("MothX")
	if !ok || brand.X < 70 {
		t.Fatalf("macOS title bar did not clear the traffic lights: %+v (%v)", brand, ok)
	}
}

func TestVersionFollowsRuntimeAndStripsTagPrefix(t *testing.T) {
	a := testApp()
	if got := a.version(); got != strings.TrimPrefix(nativeBuildVersion, "v") {
		t.Fatalf("version before connecting = %q, want build version %q", got, nativeBuildVersion)
	}
	a.runtimeVersion = "v1.3.108"
	if got := a.version(); got != "1.3.108" {
		t.Fatalf("version = %q, want 1.3.108", got)
	}
	a.dark = true
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("v1.3.108") {
		t.Fatalf("title bar did not follow the runtime version: %q", tt.Texts())
	}
}

func TestGlobeButtonTogglesLocale(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	a.dark = true
	tester := ui.NewTester(a.view, 1180, 760)
	if _, ok := tester.Find("切换语言"); !ok {
		t.Fatalf("language globe missing: %q", tester.Texts())
	}
	if err := tester.Click("切换语言"); err != nil {
		t.Fatalf("click language globe: %v", err)
	}
	if currentLocale() != "en" {
		t.Fatalf("locale after globe click = %q, want en", currentLocale())
	}
	if _, ok := tester.Find("Switch language"); !ok {
		t.Fatalf("globe tooltip did not follow the locale: %q", tester.Texts())
	}
}

func TestLocalePreferenceRoundTrip(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	a.prefsPath = filepath.Join(t.TempDir(), nativePreferencesFile)
	setLocale("en")
	a.saveNativePreferences()
	prefs, ok := loadNativePreferences(a.prefsPath)
	if !ok || prefs.Locale != "en" {
		t.Fatalf("saved locale preference = %+v", prefs)
	}
	setLocale("zh")
	restored := testApp()
	restored.prefsPath = a.prefsPath
	restored.applyNativePreferences(prefs)
	if currentLocale() != "en" {
		t.Fatalf("restored locale = %q, want en", currentLocale())
	}
}

func TestThoughtHeaderShowsElapsedTime(t *testing.T) {
	a := testApp()
	a.dark = true
	a.currentSession = "s1"
	a.currentTitle = "思考"
	a.messages = []*chatMessage{
		{ID: "t", Kind: messageThought, Text: "reasoning", Done: true, Collapsed: true, ThoughtDuration: 12 * time.Second},
	}
	a.messageIndex = map[string]*chatMessage{"t": a.messages[0]}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("已思考 · 12s") {
		t.Fatalf("thought header missing elapsed time: %q", tt.Texts())
	}
}

func TestFormatThoughtDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{40 * time.Millisecond, "<0.1s"},
		{450 * time.Millisecond, "0.5s"},
		{12 * time.Second, "12s"},
		{time.Minute, "1m"},
		{65 * time.Second, "1m 05s"},
	}
	for _, tc := range cases {
		if got := formatThoughtDuration(tc.in); got != tc.want {
			t.Errorf("formatThoughtDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseQuestionOptionsShapes(t *testing.T) {
	opts := parseQuestionOptions(json.RawMessage(`[{"id":"pg","label":"PostgreSQL"},{"id":"lite","label":"SQLite"}]`))
	if len(opts) != 2 || opts[0].ID != "pg" || opts[1].Label != "SQLite" {
		t.Fatalf("object options = %+v", opts)
	}
	opts = parseQuestionOptions(json.RawMessage(`["A","B"]`))
	if len(opts) != 2 || opts[1].ID != "B" {
		t.Fatalf("string options = %+v", opts)
	}
}

func TestFileURIEscapesReservedPathCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mothx #?% 文件.txt")
	raw := fileURI(path)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse file URI %q: %v", raw, err)
	}
	if parsed.Scheme != "file" || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("file URI = %#v, want a path-only file URI", parsed)
	}
	if !strings.Contains(raw, "%23") || !strings.Contains(raw, "%3F") || !strings.Contains(raw, "%25") {
		t.Fatalf("file URI %q did not escape reserved path characters", raw)
	}
}

func TestNewChatInCanDetachWhileAnotherSessionRuns(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.messageIndex = make(map[string]*chatMessage)
	a.sessionStates = make(map[string]sessionSnapshot)
	a.workspace = "/work/active"
	a.draftCwd = "/work/active"
	a.running = true

	a.newChatIn("/work/other")

	if a.currentSession != "" || a.running {
		t.Fatalf("new chat did not detach: session=%q running=%v", a.currentSession, a.running)
	}
	if a.workspace != "/work/other" || a.draftCwd != "/work/other" {
		t.Fatalf("new chat workspace = %q draft = %q", a.workspace, a.draftCwd)
	}
	if state, ok := a.sessionStates["s1"]; !ok || !state.Running {
		t.Fatalf("previous session was not preserved: %+v", state)
	}
}

func TestApplicationSettingsPatchShapeIsMinimal(t *testing.T) {
	view := &applicationSettingsView{
		Defaults: applicationSettingsDefaults{DefaultMode: "yolo", EnablePlanTool: true, EnableArtifact: true, EnableACPArtifact: true, Authored: true, UpdateCheck: true},
	}
	form := applicationSettingsFormFromView(view)
	form.DefaultMode = "plan"
	form.EnableArtifact = false
	form.UpdateCheck = false
	patch := applicationSettingsPatch(view, &form)
	if len(patch) != 1 {
		t.Fatalf("patch top-level keys = %d, want 1", len(patch))
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	defaultsObj, ok := decoded["defaults"].(map[string]any)
	if !ok {
		t.Fatalf("patch.defaults type = %T", decoded["defaults"])
	}
	wantKeys := []string{"defaultMode", "enablePlanTool", "enableArtifact", "enableACPArtifact", "authored", "updateCheck"}
	for _, k := range wantKeys {
		if _, exists := defaultsObj[k]; !exists {
			t.Fatalf("patch.defaults missing %q: %#v", k, defaultsObj)
		}
	}
	if len(defaultsObj) != len(wantKeys) {
		t.Fatalf("patch.defaults keys = %d, want %d: %#v", len(defaultsObj), len(wantKeys), defaultsObj)
	}
}

func TestApplicationSettingsDecodeIgnoresSecretSections(t *testing.T) {
	raw := json.RawMessage(`{
		"defaults": {"defaultMode": "agent", "enablePlanTool": true, "enableArtifact": false, "enableACPArtifact": true, "authored": false, "updateCheck": true},
		"imageGeneration": {"token": "sk-SUPERSECRET", "tokenConfigured": true},
		"webSearch": {"token": "web-SECRET"}
	}`)
	var view applicationSettingsView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Defaults.DefaultMode != "agent" {
		t.Errorf("defaultMode = %q", view.Defaults.DefaultMode)
	}
	if !view.Defaults.EnablePlanTool || view.Defaults.EnableArtifact || !view.Defaults.EnableACPArtifact || view.Defaults.Authored || !view.Defaults.UpdateCheck {
		t.Fatalf("defaults booleans = %+v", view.Defaults)
	}
}

func TestSettingsFeatureGateFallsBackWhenApplicationUnavailable(t *testing.T) {
	a := testApp()
	a.setFeatures(map[string]bool{"sessionListAll": true})
	a.openSettings()
	if !a.settingsOpen {
		t.Fatal("settings page did not open")
	}
	if a.settingsLoading {
		t.Fatal("settings should not load when feature is unavailable")
	}
	if a.settingsErr != "" {
		t.Fatalf("unexpected error when feature is missing: %q", a.settingsErr)
	}
	// The application tab is hidden, so navigation falls back to the
	// always-visible appearance section and never issues an ACP call.
	if a.settingsCategory != "appearance" || a.settingsTab != "appearance" {
		t.Fatalf("expected fallback to appearance, got category=%q tab=%q", a.settingsCategory, a.settingsTab)
	}
	a.retryLoadSettings()
	if a.settingsLoading {
		t.Fatal("retry must not call an unadvertised settings capability")
	}
}

func TestSaveApplicationSettingsWithoutChangesShowsNoDirtyMessage(t *testing.T) {
	a := testApp()
	a.setFeatures(map[string]bool{"manageApplicationSettings": true})
	a.settingsOpen = true
	a.settingsView = &applicationSettingsView{
		Defaults: applicationSettingsDefaults{DefaultMode: "yolo", EnablePlanTool: true, EnableArtifact: true, EnableACPArtifact: true, Authored: true, UpdateCheck: true},
	}
	a.settingsForm = applicationSettingsFormFromView(a.settingsView)
	a.saveApplicationSettings()
	if a.settingsStatus != "没有需要保存的变更" {
		t.Fatalf("status = %q", a.settingsStatus)
	}
}

func TestSettingsPageRendersNativeApplicationForm(t *testing.T) {
	a := testApp()
	a.dark = true
	a.setFeatures(map[string]bool{"manageApplicationSettings": true})
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "application"
	a.settingsView = &applicationSettingsView{
		Defaults: applicationSettingsDefaults{DefaultMode: "yolo", EnablePlanTool: true, EnableArtifact: true, EnableACPArtifact: false, Authored: true, UpdateCheck: true},
	}
	a.settingsForm = applicationSettingsFormFromView(a.settingsView)
	tt := ui.NewTester(a.view, 1180, 760)
	want := []string{
		"应用设置",
		"默认行为", "默认模式", "启用 Plan 工具", "启用 TUI/CLI 制品发布", "启用 Desktop (ACP) 制品发布", "使用作者模式", "启动时检查更新",
		"上下文与压缩", "加载上下文文件", "启用上下文压缩", "预留 Tokens", "保留最近 Tokens", "Tokenizer", "额外上下文文件（每行一个）", "压缩模板",
		"工具与搜索", "启用 Web 搜索", "搜索后端", "搜索 API Token", "工具执行方式", "工具最大并发数",
		"图像生成", "启用图像生成", "图像 API Token",
		"运行时行为", "启用重试", "启用状态行",
		"沙箱与审批", "启用沙箱", "沙箱级别", "写入前要求确认",
		"保存", "取消",
	}
	for _, text := range want {
		if !tt.HasText(text) {
			t.Fatalf("settings page missing %q: %q", text, tt.Texts())
		}
	}
}

func TestSettingsViewIsThreePane(t *testing.T) {
	a := testApp()
	a.dark = true
	a.setFeatures(map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageKnowledgeBases":      true,
		"manageSkills":              true,
		"manageSkillHub":            true,
		"manageExperts":             true,
		"manageACPProfiles":         true,
		"manageEnv":                 true,
		"manageChannels":            true,
		"manageMcp":                 true,
		"manageMemory":              true,
		"manageCron":                true,
		"manageServeConfig":         true,
		"manageStats":               true,
	})
	a.openSettings()
	tt := ui.NewTester(a.view, 1180, 760)
	for _, text := range []string{"分类", "条目", "返回对话", "应用设置"} {
		if !tt.HasText(text) {
			t.Fatalf("settings shell missing %q: %q", text, tt.Texts())
		}
	}
	for _, cat := range []string{"外观", "Agent", "集成", "自动化", "系统"} {
		if !tt.HasText(cat) {
			t.Fatalf("settings category rail missing %q: %q", cat, tt.Texts())
		}
	}
}

func TestSettingsViewFiltersSectionsByCapability(t *testing.T) {
	a := testApp()
	a.dark = true
	a.setFeatures(map[string]bool{
		"manageApplicationSettings": true,
		"manageKnowledgeBases":      true,
	})
	a.openSettings()
	a.setSettingsCategory("agent")
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("应用") {
		t.Fatalf("application section should be visible: %q", tt.Texts())
	}
	if !tt.HasText("知识库") {
		t.Fatalf("knowledge section should be visible: %q", tt.Texts())
	}
	if tt.HasText("ACP 工作器") {
		t.Fatalf("acpWorkers section should be hidden without manageACPProfiles: %q", tt.Texts())
	}
}

func TestSettingsLocalPagesAreReal(t *testing.T) {
	a := testApp()
	a.dark = true
	a.setFeatures(map[string]bool{})
	a.openSettings()
	tt := ui.NewTester(a.view, 1180, 760)

	a.setSettingsTab("appearance")
	tt.Frame()
	if !tt.HasText("主题") || !tt.HasText("深色") || !tt.HasText("浅色") {
		t.Fatalf("appearance page missing theme controls: %q", tt.Texts())
	}

	a.setSettingsTab("runtime")
	tt.Frame()
	if !tt.HasText("当前解析的运行时") || !tt.HasText("解析来源") {
		t.Fatalf("runtime page missing launch-time state: %q", tt.Texts())
	}

	a.setSettingsTab("about")
	tt.Frame()
	if !tt.HasText("关于 MothX Native") || !tt.HasText(a.version()) {
		t.Fatalf("about page missing version or connection info: %q", tt.Texts())
	}
}

func TestSettingsProvidersPanelIsReal(t *testing.T) {
	a := settingsTestApp(t, map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageProviderConfig":      true,
		"manageSettings":            true,
	}, "agent", "providers")
	installProviderFixture(a)
	// A directly selected provider tab must not start a load during rendering.
	a.providers.Loading = false

	tt := ui.NewTester(a.view, 1180, 760)
	for _, want := range []string{
		"供应商目录", "默认供应商与模型", "保存默认值",
		"新建供应商", "保存供应商", "测试连接", "连接", "模型", "高级", "API 密钥",
	} {
		if !tt.HasText(want) {
			t.Fatalf("providers panel missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("默认模式") {
		t.Fatalf("providers pane must not reuse application form fields: %q", tt.Texts())
	}
	if tt.HasText("该设置项由 ACP-backed 服务管理") {
		t.Fatalf("providers pane must not render the management placeholder: %q", tt.Texts())
	}
}

func TestSettingsBackToChatRestoresChatShell(t *testing.T) {
	a := testApp()
	a.dark = true
	a.setFeatures(map[string]bool{"manageApplicationSettings": true})
	a.openSettings()
	a.settingsView = &applicationSettingsView{
		Defaults: applicationSettingsDefaults{DefaultMode: "yolo", EnablePlanTool: true, EnableArtifact: true, EnableACPArtifact: true, Authored: true, UpdateCheck: true},
	}
	a.settingsForm = applicationSettingsFormFromView(a.settingsView)
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("应用设置") {
		t.Fatalf("settings did not open: %q", tt.Texts())
	}

	if err := tt.Click("返回对话"); err != nil {
		t.Fatalf("click 返回对话: %v", tt.Texts())
	}

	if a.settingsOpen {
		t.Fatal("settings should be closed after returning to chat")
	}
	if !tt.HasText(nativeChineseSlogan) {
		t.Fatalf("chat shell not restored: %q", tt.Texts())
	}
	if tt.HasText("分类") {
		t.Fatalf("settings rail leaked into chat shell: %q", tt.Texts())
	}
}

func TestSettingsAndHistoryViewsAreMutuallyExclusive(t *testing.T) {
	a := testApp()
	a.history = true
	a.historyOpen = true
	a.openSettings()
	if !a.settingsOpen || a.historyOpen || a.history {
		t.Fatalf("opening settings must close history: settings=%v historyOpen=%v history=%v", a.settingsOpen, a.historyOpen, a.history)
	}
	a.closeSettings()
	a.historyOpen = true
	if a.settingsOpen && a.historyOpen {
		t.Fatal("settings and history must not be active together")
	}
}

func TestToolUpdateKeepsInputDiffAndLocations(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{
		SessionID: "s1",
		Update: sessionUpdate{
			SessionUpdate: "tool_call",
			ToolCallID:    "t1",
			Title:         "edit",
			Kind:          "edit",
			Status:        "completed",
			RawInput:      map[string]any{"path": "main.go", "command": "go test"},
			Locations:     []toolLocation{{Path: "main.go"}, {Path: "view.go"}},
			Content:       json.RawMessage(`[{"type":"diff","path":"main.go","oldText":"old line","newText":"new line"}]`),
		},
	})
	if len(a.messages) != 1 {
		t.Fatalf("messages = %d", len(a.messages))
	}
	m := a.messages[0]
	if m.ToolInput["command"] != "go test" {
		t.Fatalf("tool input = %#v", m.ToolInput)
	}
	if len(m.ToolDiffs) != 1 || m.ToolDiffs[0].OldText != "old line" || m.ToolDiffs[0].NewText != "new line" {
		t.Fatalf("tool diffs = %#v", m.ToolDiffs)
	}
	if len(m.ToolLocations) != 2 || m.ToolLocations[1] != "view.go" {
		t.Fatalf("tool locations = %#v", m.ToolLocations)
	}
	if text := toolDiffText(m.ToolDiffs[0]); !strings.Contains(text, "- old line") || !strings.Contains(text, "+ new line") {
		t.Fatalf("diff text = %q", text)
	}
}

func TestDecisionDeadlineUpdatesInlineCard(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	d := &decision{ID: json.RawMessage(`"req-1"`), Kind: "permission", Title: "bash", RawInput: map[string]any{"command": "go test"}}
	a.appendMessage(&chatMessage{ID: "decision:req-1", Kind: messageDecision, Decision: d})
	remaining := 1500
	a.applyDecisionDeadline(sessionEvent{
		Event:     "decision_deadline",
		RequestID: "req-1",
		Kind:      "approval",
		Remaining: &remaining,
	})
	if d.Deadline.IsZero() {
		t.Fatal("deadline was not projected")
	}
	if label := decisionDeadlineLabel(d.Deadline); label == "" {
		t.Fatal("deadline label is empty")
	}
	// Numeric JSON-RPC ids must not be mistaken for strings.
	if got := decisionIDString(json.RawMessage(`42`)); got != "42" {
		t.Fatalf("numeric id = %q", got)
	}
}

func TestDecisionCardRendersInlineAndResolves(t *testing.T) {
	a := testApp()
	a.dark = true
	a.themeSet = true
	a.currentSession = "s1"
	d := &decision{
		ID:      json.RawMessage(`"req-2"`),
		Kind:    "question",
		Session: "s1",
		Title:   "继续吗",
		Prompt:  "请确认是否继续",
		Options: []decisionOption{{ID: "yes", Label: "继续"}, {ID: "no", Label: "停止"}},
	}
	a.appendMessage(&chatMessage{ID: "decision:req-2", Kind: messageDecision, Decision: d})
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"继续吗", "请确认是否继续", "继续", "停止"} {
		if !textPresent(tt, want) {
			t.Fatalf("decision card missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("继续"); err != nil {
		t.Fatalf("click question option: %v", err)
	}
	if !d.Resolved || d.ResolvedValue == "" {
		t.Fatalf("decision not resolved: %+v", d)
	}
	if !textPresent(tt, d.ResolvedValue) {
		t.Fatalf("resolved label missing: %q", tt.Texts())
	}
}

func TestUsageLabelProjectsContextAndCost(t *testing.T) {
	a := testApp()
	if got := a.usageLabel(); got != "" {
		t.Fatalf("empty usage label = %q", got)
	}
	a.usageUsed = 2500
	a.usageSize = 10000
	a.usageCost = 0.0123
	a.usageCurrency = "USD"
	want := "2500 / 10000 (25%) · $0.0123"
	if got := a.usageLabel(); got != want {
		t.Fatalf("usage label = %q, want %q", got, want)
	}
}

func TestPathWithinDirectoryKeepsAttachmentBoundary(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "notes.txt")
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if !pathWithinDirectory(root, inside) {
		t.Fatal("file inside the workspace must stay a resource_link")
	}
	if pathWithinDirectory(root, outside) {
		t.Fatal("file outside the workspace must be embedded")
	}
	if pathWithinDirectory("", outside) {
		t.Fatal("empty workspace must embed")
	}
}
