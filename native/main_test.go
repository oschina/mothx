package main

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func testApp() *nativeApp {
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

func TestGroupsPutCurrentWorkspaceFirst(t *testing.T) {
	a := testApp()
	a.workspace = "/work/current"
	a.sessions = []listedSession{
		{SessionID: "1", Cwd: "/work/old", Title: "Old", UpdatedAt: "2026-11-09T10:00:00Z"},
		{SessionID: "2", Cwd: "/work/current", Title: "Current", UpdatedAt: "2026-11-01T10:00:00Z"},
	}
	groups := a.groups()
	if len(groups) < 2 || groups[0].Cwd != "/work/current" {
		t.Fatalf("groups = %+v", groups)
	}
	a.search = "old"
	groups = a.groups()
	if len(groups) != 1 || groups[0].Cwd != "/work/old" {
		t.Fatalf("filtered groups = %+v", groups)
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

func TestNewChatInKeepsActiveWorkspaceWhileRunning(t *testing.T) {
	a := testApp()
	a.workspace = "/work/active"
	a.draftCwd = "/work/active"
	a.running = true

	a.newChatIn("/work/other")

	if a.workspace != "/work/active" || a.draftCwd != "/work/active" {
		t.Fatalf("workspace changed while run was active: workspace=%q draft=%q", a.workspace, a.draftCwd)
	}
	if a.status != "请先停止当前运行" {
		t.Fatalf("status = %q", a.status)
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
	if !tt.HasText("关于 MothX Native") || !tt.HasText(nativeVersion) {
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
