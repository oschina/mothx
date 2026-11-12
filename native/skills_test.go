package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func skillBoolPtr(value bool) *bool { return &value }

func skillsTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "skills"
	a.ensureSettingsTabVisible()
	a.skills.Cwd = "/tmp/workspace"
	a.skills.Skills = []skillView{
		{Name: "deploy", Description: "deploy the app", Source: "project", Enabled: skillBoolPtr(true)},
		{Name: "global-gen", Description: "global generator", Source: "global", Enabled: skillBoolPtr(false)},
	}
	a.skills.Loaded = true
	return a
}

func TestSkillsListViewDecodeAndRequestCwd(t *testing.T) {
	raw := json.RawMessage(`{
		"cwd": "/home/user/project",
		"skills": [
			{"name":"deploy","description":"deploy the app","source":"project","enabled":true},
			{"name":"legacy","description":"legacy skill","source":"global"}
		]
	}`)
	var view skillListView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Cwd != "/home/user/project" || len(view.Skills) != 2 {
		t.Fatalf("list view = %+v", view)
	}
	if !skillEnabled(view.Skills[0]) {
		t.Fatal("explicit enabled skill decoded as disabled")
	}
	if !skillEnabled(view.Skills[1]) {
		t.Fatal("omitted enabled must keep the legacy enabled meaning")
	}
	if got := skillDescription(view.Skills[0]); got != "deploy the app · project" {
		t.Fatalf("description = %q", got)
	}

	workspace := filepath.Join(t.TempDir(), "workspace")
	if got := skillsRequestCwd(workspace, "/draft", "/process"); got != workspace {
		t.Fatalf("selected workspace cwd = %q, want %q", got, workspace)
	}
	if got := skillsRequestCwd("", "/draft", "/process"); got != "/draft" {
		t.Fatalf("draft fallback cwd = %q", got)
	}
	relative := "relative/workspace"
	abs, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	if got := skillsRequestCwd(relative, "/draft", "/process"); got != abs {
		t.Fatalf("relative cwd = %q, want absolute %q", got, abs)
	}
	if got := skillsRequestCwd("", "", ""); got != "" {
		t.Fatalf("empty cwd = %q", got)
	}

	listRequest := buildSkillsListRequest(workspace)
	if listRequest["cwd"] != workspace {
		t.Fatalf("list request = %#v", listRequest)
	}
	setRequest := buildSkillSetRequest(workspace, " deploy ", false)
	if setRequest["cwd"] != workspace || setRequest["name"] != "deploy" || setRequest["enabled"] != false {
		t.Fatalf("set request = %#v", setRequest)
	}
}

func TestSkillsPanelHeadlessRendersListStatesAndActions(t *testing.T) {
	a := skillsTestApp(t, map[string]bool{"manageSkills": true})
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{
		"技能", "发现目录：/tmp/workspace", "deploy", "deploy the app · project",
		"global-gen", "global generator · global", "已启用", "已停用",
		"启用", "停用", "共 2 个技能", "刷新",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("skills panel missing %q: %q", want, tt.Texts())
		}
	}

	a.skills.Loading = true
	a.skills.Loaded = false
	tt.Frame()
	if !textPresent(tt, "正在读取技能列表…") {
		t.Fatalf("skills loading state missing: %q", tt.Texts())
	}

	a.skills.Loading = false
	a.skills.Err = "读取技能列表失败：boom"
	a.skills.Loaded = false
	tt.Frame()
	for _, want := range []string{"技能管理不可用", "读取技能列表失败：boom", "重试"} {
		if !textPresent(tt, want) {
			t.Fatalf("skills error state missing %q: %q", want, tt.Texts())
		}
	}
}

func TestSkillsCapabilityGatePreventsLoad(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "skills"
	a.startSkillsPanelLoad()
	if a.skillsCapable() {
		t.Fatal("manageSkills must be required")
	}
	if a.skills.Loading || !a.skills.Loaded || !strings.Contains(a.skills.Err, "manageSkills") {
		t.Fatalf("gated load state = %+v", a.skills)
	}

	a.settingsCategory = "agent"
	a.settingsTab = "skills"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "skills" {
		t.Fatalf("skills tab must be hidden without manageSkills: %+v", a.skills)
	}
}

func TestSkillsToggleValidatesAndReportsOfflineErrors(t *testing.T) {
	a := skillsTestApp(t, map[string]bool{"manageSkills": true})
	a.toggleSkill(-1)
	if !a.skills.StatusIsError || !strings.Contains(a.skills.Status, "不存在") {
		t.Fatalf("invalid toggle state = %+v", a.skills)
	}

	a.skills.Status = ""
	a.skills.StatusIsError = false
	a.toggleSkill(0)
	if a.skills.Toggling != "" {
		t.Fatalf("offline toggle must not remain in flight: %+v", a.skills)
	}
	if !a.skills.StatusIsError || !strings.Contains(a.skills.Status, "尚未连接") {
		t.Fatalf("offline toggle state = %+v", a.skills)
	}

	a.skills.Loaded = true
	a.skills.Skills[0].Enabled = skillBoolPtr(false)
	a.applySkillToggleLocally("deploy", true)
	if !skillEnabled(a.skills.Skills[0]) {
		t.Fatal("local toggle fallback did not update the row")
	}
}

func TestSkillsPanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := skillsTestApp(t, map[string]bool{"manageSkills": true})
	seq := atomic.LoadInt64(&a.skills.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.skillsPanelCurrent(seq, reloadSeq) {
		t.Fatal("current skills request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.skillsPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := skillsTestApp(t, map[string]bool{"manageSkills": true})
	b.skills.Toggling = "deploy"
	b.skills.Status = "正在停用技能 deploy…"
	oldSeq := atomic.LoadInt64(&b.skills.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.skills.Seq) <= oldSeq {
		t.Fatal("leaving the skills tab must invalidate in-flight state")
	}
	if b.skills.Loaded || b.skills.Toggling != "" || b.skills.Skills != nil || b.skills.Cwd != "" {
		t.Fatalf("skills state was not invalidated on tab exit: %+v", b.skills)
	}

	c := skillsTestApp(t, map[string]bool{"manageSkills": true})
	c.closeSettings()
	if c.skills.Loaded || c.skills.Toggling != "" || c.skills.Skills != nil || c.skills.Cwd != "" {
		t.Fatalf("closing settings must invalidate skills state: %+v", c.skills)
	}
}

func TestCommandKindProjectsACPKindMetadata(t *testing.T) {
	var skill command
	if err := json.Unmarshal([]byte(`{"name":"review","_meta":{"mothx.dev":{"kind":"skill"}}}`), &skill); err != nil {
		t.Fatal(err)
	}
	if got := commandKind(skill); got != "skill" {
		t.Fatalf("skill command kind = %q, want skill", got)
	}
	if got := commandKind(command{Name: "help"}); got != "command" {
		t.Fatalf("metadata-free command kind = %q, want command", got)
	}
	var unknown command
	if err := json.Unmarshal([]byte(`{"name":"x","_meta":{"mothx.dev":{"kind":"weird"}}}`), &unknown); err != nil {
		t.Fatal(err)
	}
	if got := commandKind(unknown); got != "weird" {
		t.Fatalf("explicit kind must pass through: %q", got)
	}
}

func TestSkillsPageRendersSkillAndCommandCards(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.page = "skills"
	a.commands = []command{
		{Name: "review", Description: "Review the change", Meta: map[string]json.RawMessage{"mothx.dev": json.RawMessage(`{"kind":"skill"}`)}},
		{Name: "help", Description: "Show help"},
	}
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{"指令卡片", "review", "Review the change", "help", "Show help"} {
		if !textPresent(tt, want) {
			t.Fatalf("skills page missing %q: %q", want, tt.Texts())
		}
	}
}
