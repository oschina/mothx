package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// settingsTestApp prepares a deterministic settings page. It assigns the
// navigation state directly so no ACP request is started while rendering.
func settingsTestApp(t *testing.T, features map[string]bool, category, tab string) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = category
	a.settingsTab = tab
	a.settingsView = fullApplicationSettingsView()
	a.settingsForm = applicationSettingsFormFromView(a.settingsView)
	a.ensureSettingsTabVisible()
	return a
}

func textPresent(tt *ui.Tester, want string) bool {
	if tt.HasText(want) {
		return true
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}

func TestSettingsStandaloneThreePaneView(t *testing.T) {
	a := settingsTestApp(t, map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageKnowledgeBases":      true,
		"manageSkills":              true,
		"manageSkillHub":            true,
		"manageExperts":             true,
		"manageACPProfiles":         true,
		"manageEnv":                 true,
	}, "agent", "application")

	tt := ui.NewTester(a.view, 860, 760)

	// Three panes: category rail, section rail, and scrollable detail content.
	for _, want := range []string{
		"分类", "条目",
		"外观", "Agent", "集成", "自动化", "系统",
		"应用", "供应商", "知识库", "技能", "SkillHub", "主角团", "ACP 工作器", "环境变量",
		"返回对话", "应用设置", "默认行为", "保存", "取消",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("settings page missing %q; texts = %q", want, tt.Texts())
		}
	}

	// The settings page replaces the chat workspace, but the primary sidebar
	// stays available for returning to conversations.
	if tt.HasText(nativeChineseSlogan) {
		t.Fatalf("chat empty state should not render behind settings: %q", tt.Texts())
	}
	if !tt.HasText("新会话") {
		t.Fatalf("primary sidebar missing while settings page is shown: %q", tt.Texts())
	}
	if tt.HasText("Enter 发送 · Shift + Enter 换行") {
		t.Fatalf("chat composer leaked into the settings page: %q", tt.Texts())
	}
}

func TestSettingsCapabilityGatesAndFallbacks(t *testing.T) {
	wantFeatures := map[string]string{
		"application": "manageApplicationSettings",
		"providers":   "manageProviders",
		"knowledge":   "manageKnowledgeBases",
		"skills":      "manageSkills",
		"skillhub":    "manageSkillHub",
		"experts":     "manageExperts",
		"acpWorkers":  "manageACPProfiles",
		"env":         "manageEnv",
		"channels":    "manageChannels",
		"mcp":         "manageMcp",
		"memory":      "manageMemory",
		"cron":        "manageCron",
		"serve":       "manageServeConfig",
		"stats":       "manageStats",
		"appearance":  "",
		"runtime":     "",
		"about":       "",
	}
	a := testApp()
	for id, feature := range wantFeatures {
		sec, ok := a.settingsSectionByID(id)
		if !ok {
			t.Fatalf("missing settings section %q", id)
		}
		if sec.Feature != feature {
			t.Errorf("section %q feature = %q, want %q", id, sec.Feature, feature)
		}
		if feature == "" && !a.sectionEnabled(sec) {
			t.Errorf("local section %q must always be enabled", id)
		}
	}

	// Only the advertised agent sections are visible.
	a.setFeatures(map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
	})
	agent, _ := a.settingsCategoryByID("agent")
	var enabledIDs []string
	for _, sec := range a.enabledSections(agent) {
		enabledIDs = append(enabledIDs, sec.ID)
	}
	if len(enabledIDs) != 2 || enabledIDs[0] != "application" || enabledIDs[1] != "providers" {
		t.Fatalf("enabled agent sections = %v", enabledIDs)
	}

	// A selected tab that loses its capability falls back to the first visible
	// section instead of rendering or calling an unsupported panel.
	a.settingsCategory = "agent"
	a.settingsTab = "knowledge"
	a.ensureSettingsTabVisible()
	if a.settingsCategory != "agent" || a.settingsTab != "application" {
		t.Fatalf("capability fallback = category %q tab %q", a.settingsCategory, a.settingsTab)
	}

	// A category with no visible sections falls back to the always-local
	// appearance page.
	b := testApp()
	b.settingsCategory = "agent"
	b.settingsTab = "application"
	b.ensureSettingsTabVisible()
	if b.settingsCategory != "appearance" || b.settingsTab != "appearance" {
		t.Fatalf("empty-category fallback = category %q tab %q", b.settingsCategory, b.settingsTab)
	}
}

func TestSettingsCategoryChangeSelectsFirstVisibleSection(t *testing.T) {
	a := testApp()
	a.setFeatures(map[string]bool{"manageMcp": true})

	a.setSettingsCategory("integrations")
	if a.settingsCategory != "integrations" || a.settingsTab != "mcp" {
		t.Fatalf("integrations selection = category %q tab %q", a.settingsCategory, a.settingsTab)
	}

	a.setFeatures(nil)
	a.setSettingsCategory("system")
	if a.settingsCategory != "system" || a.settingsTab != "runtime" {
		t.Fatalf("local system selection = category %q tab %q", a.settingsCategory, a.settingsTab)
	}

	a.setFeatures(map[string]bool{"manageMemory": true})
	a.setSettingsTab("mcp")
	if a.settingsTab == "mcp" {
		t.Fatalf("disabled tab must not become active, got %q", a.settingsTab)
	}
}

func TestSettingsManagedPaneNamesServiceWithoutLoading(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{"manageFuturePanel": true})

	// The managed placeholder is the fallback for a capability-gated section
	// whose panel has not been migrated yet. Every shipped section now has a
	// real pane, so exercise the fallback directly with a synthetic section.
	render := func(c *ui.Context) {
		p := a.applyTheme(c)
		ui.Column(c).Children(func() {
			a.settingsManagedPane(c, p, settingsSection{
				ID: "futurePanel", Label: "未来面板", Description: "尚未迁移的面板",
				Icon: iconDoc, Feature: "manageFuturePanel",
			})
		})
	}
	tt := ui.NewTester(render, 860, 760)
	for _, want := range []string{"该设置项由 ACP-backed 服务管理", "mothx/manage/futurePanel", "不会调用该服务"} {
		if !textPresent(tt, want) {
			t.Fatalf("managed pane missing %q; texts = %q", want, tt.Texts())
		}
	}
	if a.settingsLoading || a.settingsView != nil {
		t.Fatalf("managed tab must not start a load: loading=%v view=%v", a.settingsLoading, a.settingsView)
	}
}

func TestSettingsAppearancePageSelectsTheme(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.settingsOpen = true
	a.settingsCategory = "appearance"
	a.settingsTab = "appearance"

	tt := ui.NewTester(a.view, 860, 760)
	for _, want := range []string{"主题", "跟随系统或固定深色/浅色", "跟随系统", "深色", "浅色"} {
		if !textPresent(tt, want) {
			t.Fatalf("appearance page missing %q; texts = %q", want, tt.Texts())
		}
	}
	if err := tt.Click("浅色"); err != nil {
		t.Fatalf("click light theme: %v", err)
	}
	if a.dark {
		t.Fatal("appearance page did not apply the light theme")
	}
}

func TestSettingsRuntimePageShowsLaunchTimeSelection(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "mothx-native-test")
	t.Setenv("MOTHX_BIN", bin)
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}

	a := testApp()
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "runtime"

	tt := ui.NewTester(a.view, 860, 760)
	for _, want := range []string{"当前解析的运行时", "解析来源", "MOTHX_BIN", abs, "选择 mothx 二进制"} {
		if !textPresent(tt, want) {
			t.Fatalf("runtime page missing %q; texts = %q", want, tt.Texts())
		}
	}
}

func TestSettingsAboutPageShowsVersionConnectionAndFeatureCount(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.conn = connReady
	a.setFeatures(map[string]bool{
		"manageApplicationSettings": true,
		"manageProviders":           true,
		"manageStats":               false,
	})
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "about"

	tt := ui.NewTester(a.view, 860, 760)
	for _, want := range []string{a.version(), "已连接", "可用能力数"} {
		if !textPresent(tt, want) {
			t.Fatalf("about page missing %q; texts = %q", want, tt.Texts())
		}
	}
	if _, ok := tt.Find("2"); !ok {
		t.Fatalf("about page feature count should be 2; texts = %q", tt.Texts())
	}
}

func TestSettingsBackRestoresChatShellAndState(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "appearance"
	a.settingsTab = "appearance"
	a.currentSession = "s1"
	a.currentTitle = "保留的会话"
	a.messages = []*chatMessage{{ID: "m1", Kind: messageAssistant, Text: "会话内容仍然保留", Done: true}}
	a.messageIndex = map[string]*chatMessage{"m1": a.messages[0]}

	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("返回对话") {
		t.Fatalf("settings page missing back control: %q", tt.Texts())
	}
	if err := tt.Click("返回对话"); err != nil {
		t.Fatalf("click back control: %v", err)
	}
	if a.settingsOpen {
		t.Fatal("back control did not leave the settings page")
	}
	if a.currentSession != "s1" || a.currentTitle != "保留的会话" || len(a.messages) != 1 {
		t.Fatalf("chat state lost: session=%q title=%q messages=%d", a.currentSession, a.currentTitle, len(a.messages))
	}

	tt.Frame()
	if tt.HasText("返回对话") {
		t.Fatalf("settings header still rendered after returning: %q", tt.Texts())
	}
	for _, want := range []string{"新会话", "Enter 发送 · Shift + Enter 换行", "会话内容仍然保留"} {
		if !tt.HasText(want) {
			t.Fatalf("chat shell missing %q; texts = %q", want, tt.Texts())
		}
	}
}

func TestSidebarNewChatLeavesSettingsPage(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "appearance"
	a.settingsTab = "appearance"

	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("新会话"); err != nil {
		t.Fatalf("click sidebar new chat: %v", err)
	}
	if a.settingsOpen {
		t.Fatal("sidebar new chat did not return to the chat workspace")
	}
}

func TestEnglishSettingsPagesRenderTranslatedState(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	setLocale("en")
	a.themeSet = true
	a.settingsOpen = true
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
	a.settingsCategory = "appearance"
	a.settingsTab = "appearance"
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"Appearance", "Theme", "System", "Dark", "Light", "Home Logo"} {
		if !textPresent(tt, want) {
			t.Fatalf("english appearance missing %q: %q", want, tt.Texts())
		}
	}
	a.settingsCategory = "system"
	a.settingsTab = "runtime"
	tt.Frame()
	for _, want := range []string{"Resolved runtime", "Resolution source", "Main-process diagnostics", "Run diagnostics"} {
		if !textPresent(tt, want) {
			t.Fatalf("english runtime missing %q: %q", want, tt.Texts())
		}
	}
	a.settingsTab = "about"
	tt.Frame()
	for _, want := range []string{"About MothX Native", "Runtime binary", "Platform"} {
		if !textPresent(tt, want) {
			t.Fatalf("english about missing %q: %q", want, tt.Texts())
		}
	}
	// Section and category labels are part of the persistent settings rail, so
	// they must stay translated on every page, not only in the initial shell.
	a.settingsCategory = "agent"
	a.settingsTab = "application"
	tt.Frame()
	for _, want := range []string{"Agent", "Application", "Provider", "Knowledge bases", "Skills", "SkillHub", "Expert teams", "ACP workers"} {
		if !textPresent(tt, want) {
			t.Fatalf("english agent settings missing %q: %q", want, tt.Texts())
		}
	}
	a.settingsCategory = "integrations"
	a.settingsTab = "channels"
	tt.Frame()
	for _, want := range []string{"Integrations", "Channels", "MCP", "Memory", "Channel artifacts", "Save artifact settings"} {
		if !textPresent(tt, want) {
			t.Fatalf("english integration settings missing %q: %q", want, tt.Texts())
		}
	}
	a.settingsCategory = "automation"
	a.settingsTab = "cron"
	tt.Frame()
	for _, want := range []string{"Automation", "Scheduled tasks"} {
		if !textPresent(tt, want) {
			t.Fatalf("english automation settings missing %q: %q", want, tt.Texts())
		}
	}
	a.settingsCategory = "system"
	a.settingsTab = "stats"
	tt.Frame()
	for _, want := range []string{"System", "Services", "Runtime", "Statistics", "About", "Usage statistics"} {
		if !textPresent(tt, want) {
			t.Fatalf("english system settings missing %q: %q", want, tt.Texts())
		}
	}
	a.settingsTab = "runtime"
	tt.Frame()
	for _, want := range []string{"Restart connection", "Status"} {
		if !textPresent(tt, want) {
			t.Fatalf("english runtime actions missing %q: %q", want, tt.Texts())
		}
	}
}
