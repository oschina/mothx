package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func expertTestList() *expertListView {
	return &expertListView{
		Scope: expertScopeGlobal,
		Experts: []expertSummary{{
			Name:        "global-expert",
			ExpertType:  expertTypeAgent,
			DisplayName: expertLocalizedText{Zh: "全局专家", En: "Global Expert"},
			Source:      expertScopeGlobal,
		}},
		EffectiveExperts: []expertSummary{
			{
				Name:        "software-company",
				ExpertType:  expertTypeTeam,
				DisplayName: expertLocalizedText{Zh: "软件公司", En: "Software Company"},
				Source:      expertSourceBuiltin,
			},
			{
				Name:        "global-expert",
				ExpertType:  expertTypeAgent,
				DisplayName: expertLocalizedText{Zh: "全局专家", En: "Global Expert"},
				Source:      expertScopeGlobal,
			},
			{
				Name:          "project-expert",
				ExpertType:    expertTypeAgent,
				DisplayName:   expertLocalizedText{Zh: "项目专家", En: "Project Expert"},
				Source:        expertScopeProject,
				Invalid:       true,
				InvalidReason: "manifest name 不一致",
			},
		},
	}
}

func expertTestBundle() expertBundle {
	return expertBundle{
		Scope: expertScopeGlobal,
		Manifest: expertManifest{
			SchemaVersion: 1,
			Name:          "global-expert",
			ExpertType:    expertTypeTeam,
			AgentName:     "lead",
			DisplayName:   expertLocalizedText{Zh: "全局专家", En: "Global Expert"},
			CategoryID:    "engineering",
			QuickPrompts: []expertLocalizedText{{
				Zh: "帮我评审代码", En: "Review my code",
			}},
			DefaultInitPrompt: expertLocalizedText{Zh: "初始化", En: "Init"},
			TeamInfo: &expertTeamInfo{
				LeadAgent:    "lead",
				MemberAgents: []string{"reviewer"},
			},
			Members: []expertMemberMeta{
				{ID: "lead", Name: expertLocalizedText{Zh: "主角", En: "Lead"}, Role: expertRoleLead},
				{ID: "reviewer", Name: expertLocalizedText{Zh: "评审", En: "Reviewer"}, Role: expertRoleMember},
			},
		},
		Agents: map[string]string{
			"lead":     "---\nname: lead\n---\n",
			"reviewer": "---\nname: reviewer\n---\n",
		},
	}
}

func expertsTestApp(t *testing.T, features map[string]bool, scope string) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.workspace = "/work/project"
	a.draftCwd = "/work/project"
	a.cwd = "/work/project"
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "experts"
	a.ensureSettingsTabVisible()
	a.experts = newExpertsPanelState()
	a.experts.Scope = scope
	a.experts.View = expertTestList()
	a.experts.Loaded = true
	if scope == expertScopeProject {
		a.experts.Cwd = "/work/project"
	}
	return a
}

func fullExpertFeatures() map[string]bool {
	return map[string]bool{
		"manageApplicationSettings": true,
		"manageExperts":             true,
	}
}

func TestExpertsListViewDecodeAndProjection(t *testing.T) {
	raw := json.RawMessage(`{
		"scope": "project",
		"cwd": "/work/project",
		"experts": [
			{"name":"project-expert","expertType":"agent","displayName":{"zh":"项目专家","en":"Project Expert"},"source":"project"}
		],
		"effectiveExperts": [
			{"name":"software-company","expertType":"team","displayName":{"zh":"软件公司","en":"Software Company"},"source":"builtin"},
			{"name":"global-expert","expertType":"agent","displayName":{"en":"Global Expert"},"source":"global"},
			{"name":"project-expert","expertType":"agent","displayName":{"zh":"项目专家"},"source":"project","invalid":true,"invalidReason":"坏包"}
		]
	}`)
	var view expertListView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Scope != expertScopeProject || view.Cwd != "/work/project" {
		t.Fatalf("list envelope = %+v", view)
	}
	if len(view.Experts) != 1 || view.Experts[0].Name != "project-expert" {
		t.Fatalf("scope experts = %+v", view.Experts)
	}
	items := expertListItems(&view)
	if len(items) != 3 {
		t.Fatalf("effective experts = %+v", items)
	}
	if got := expertDisplayName(items[1]); got != "Global Expert" {
		t.Fatalf("display fallback = %q", got)
	}
	if got := expertSourceLabel(items[0].Source); got != "内置" {
		t.Fatalf("builtin label = %q", got)
	}
	if got := expertScopeLabel(view.Scope); got != "项目" {
		t.Fatalf("project label = %q", got)
	}
	if got := expertSummaryDescription(items[2]); !strings.Contains(got, "project-expert") || !strings.Contains(got, "坏包") {
		t.Fatalf("description = %q", got)
	}
	if got := expertTypeLabel(expertTypeTeam); got != "团队" {
		t.Fatalf("team label = %q", got)
	}
}

func TestExpertsRequestsScopedByProjectWorkspace(t *testing.T) {
	if got := expertsWorkspaceCwd(" /work/project ", "/ignored"); got != "/work/project" {
		t.Fatalf("workspace = %q", got)
	}
	if got := expertsWorkspaceCwd("", "/work/draft"); got != "/work/draft" {
		t.Fatalf("draft workspace = %q", got)
	}
	if got := expertsWorkspaceCwd("", ""); got != "" {
		t.Fatalf("empty workspace = %q", got)
	}

	global := buildExpertsListRequest(expertScopeGlobal, "/work/project")
	if global["scope"] != expertScopeGlobal {
		t.Fatalf("global list = %#v", global)
	}
	if _, ok := global["cwd"]; ok {
		t.Fatalf("global list must not carry cwd: %#v", global)
	}
	project := buildExpertsListRequest(expertScopeProject, " /work/project ")
	if project["scope"] != expertScopeProject || project["cwd"] != "/work/project" {
		t.Fatalf("project list = %#v", project)
	}
	get := buildExpertGetRequest(expertScopeProject, "/work/project", " project-expert ")
	if get["name"] != "project-expert" || get["cwd"] != "/work/project" {
		t.Fatalf("get request = %#v", get)
	}
	save := buildExpertSaveRequest(expertScopeGlobal, "/ignored", map[string]any{"scope": expertScopeGlobal})
	if _, ok := save["cwd"]; ok {
		t.Fatalf("global save must not carry cwd: %#v", save)
	}
	remove := buildExpertDeleteRequest(expertScopeGlobal, "", "global-expert")
	if remove["scope"] != expertScopeGlobal || remove["name"] != "global-expert" {
		t.Fatalf("delete request = %#v", remove)
	}

	app := expertsTestApp(t, fullExpertFeatures(), expertScopeProject)
	app.experts.Cwd = ""
	if got := app.expertsRequestCwd(); got != "/work/project" {
		t.Fatalf("project request cwd fallback = %q", got)
	}
	view := expertTestList()
	view.Scope = expertScopeProject
	view.Cwd = "/work/project"
	app.experts.Loading = true
	app.applyExpertsListView(view)
	if !app.experts.Loaded || app.experts.Loading || app.experts.Scope != expertScopeProject || app.experts.Cwd != "/work/project" {
		t.Fatalf("applied list view = %+v", app.experts)
	}
}

func TestExpertScopeAndBuiltinReadonlyRules(t *testing.T) {
	items := expertListItems(expertTestList())
	if expertCanEdit(items[0], expertScopeGlobal) || expertCanEdit(items[0], expertScopeProject) {
		t.Fatal("builtin experts must never be editable")
	}
	if !expertCanEdit(items[1], expertScopeGlobal) || expertCanEdit(items[1], expertScopeProject) {
		t.Fatalf("global edit rules = %+v", items[1])
	}
	if !expertCanEdit(items[2], expertScopeProject) || expertCanEdit(items[2], expertScopeGlobal) {
		t.Fatalf("project edit rules = %+v", items[2])
	}
	if expertCanEdit(expertSummary{Name: "orphan"}, expertScopeGlobal) {
		t.Fatal("an unknown source must be read-only")
	}
	if got := expertScopeHint(items[0], expertScopeGlobal); got != "内置团队只读" {
		t.Fatalf("builtin hint = %q", got)
	}
	if got := expertScopeHint(items[2], expertScopeGlobal); !strings.Contains(got, "项目") {
		t.Fatalf("scope hint = %q", got)
	}
}

func TestExpertDraftRoundTripPreservesBundleFields(t *testing.T) {
	bundle := expertTestBundle()
	draft := expertDraftFromBundle(bundle)
	if draft.Scope != expertScopeGlobal || draft.OriginalName != "global-expert" || draft.IsNew {
		t.Fatalf("draft identity = %+v", draft)
	}
	if draft.Kind != expertTypeTeam || draft.AgentName != "lead" || draft.Category != "engineering" {
		t.Fatalf("draft manifest = %+v", draft)
	}
	if len(draft.Members) != 2 || draft.Members[0].Role != expertRoleLead || draft.Members[1].ID != "reviewer" {
		t.Fatalf("draft members = %+v", draft.Members)
	}
	if len(draft.Agents) != 2 || draft.Agents[0].ID != "lead" || draft.Agents[1].ID != "reviewer" {
		t.Fatalf("draft agents = %+v", draft.Agents)
	}
	if draft.DefaultInitPrompt.Zh != "初始化" || len(draft.QuickPrompts) != 1 {
		t.Fatalf("preserved prompts = %+v %+v", draft.QuickPrompts, draft.DefaultInitPrompt)
	}

	payload, err := expertDraftBundle(draft)
	if err != nil {
		t.Fatal(err)
	}
	if payload["scope"] != expertScopeGlobal {
		t.Fatalf("payload scope = %#v", payload)
	}
	manifest, ok := payload["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("payload manifest = %#v", payload["manifest"])
	}
	if manifest["schemaVersion"] != 1 || manifest["name"] != "global-expert" || manifest["expertType"] != expertTypeTeam {
		t.Fatalf("payload manifest = %#v", manifest)
	}
	if _, ok := manifest["quickPrompts"]; !ok {
		t.Fatalf("quickPrompts were dropped: %#v", manifest)
	}
	if _, ok := manifest["defaultInitPrompt"]; !ok {
		t.Fatalf("defaultInitPrompt was dropped: %#v", manifest)
	}
	team, ok := manifest["teamInfo"].(map[string]any)
	if !ok || team["leadAgent"] != "lead" {
		t.Fatalf("teamInfo = %#v", manifest["teamInfo"])
	}
	memberAgents, ok := team["memberAgents"].([]string)
	if !ok || len(memberAgents) != 1 || memberAgents[0] != "reviewer" {
		t.Fatalf("memberAgents = %#v", team["memberAgents"])
	}
	agents, ok := payload["agents"].(map[string]any)
	if !ok || len(agents) != 2 {
		t.Fatalf("payload agents = %#v", payload["agents"])
	}
}

func TestExpertDraftValidationMatchesDesktopRules(t *testing.T) {
	if _, err := expertDraftBundle(nil); err == nil {
		t.Fatal("nil draft must fail")
	}
	draft := newExpertDraft(expertScopeGlobal)
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "标识") {
		t.Fatalf("empty name error = %v", err)
	}
	draft.Name = "team-expert"
	draft.Kind = expertTypeTeam
	draft.Members[0].Role = expertRoleMember
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "lead") {
		t.Fatalf("missing team lead error = %v", err)
	}
	draft.Members = nil
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "成员") {
		t.Fatalf("missing members error = %v", err)
	}
	draft = newExpertDraft(expertScopeGlobal)
	draft.Name = "agent-expert"
	draft.Agents = nil
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "Agent") {
		t.Fatalf("missing agents error = %v", err)
	}
	draft = newExpertDraft(expertScopeGlobal)
	draft.Name = "agent-expert"
	draft.AgentName = "missing"
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing agent source error = %v", err)
	}
	draft = newExpertDraft(expertScopeGlobal)
	draft.Name = "../escape"
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "路径") {
		t.Fatalf("path traversal error = %v", err)
	}
	draft = newExpertDraft(expertScopeGlobal)
	draft.Name = "agent-expert"
	draft.Agents = append(draft.Agents, expertAgentDraft{ID: "lead", Source: "dup"})
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("duplicate agent error = %v", err)
	}
	draft = newExpertDraft(expertScopeGlobal)
	draft.Name = "agent-expert"
	draft.Members = append(draft.Members, expertMemberDraft{ID: "lead", Role: expertRoleMember})
	if _, err := expertDraftBundle(draft); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("duplicate member error = %v", err)
	}
}

func TestExpertDraftAddRemoveMembersAndAgents(t *testing.T) {
	a := expertsTestApp(t, fullExpertFeatures(), expertScopeGlobal)
	a.startNewExpert()
	draft := a.experts.Draft
	if draft == nil || !draft.IsNew || draft.Scope != expertScopeGlobal {
		t.Fatalf("new draft = %+v", draft)
	}
	if len(draft.Members) != 1 || draft.Members[0].ID != "lead" ||
		len(draft.Agents) != 1 || draft.Agents[0].ID != "lead" {
		t.Fatalf("default draft = %+v", draft)
	}

	draft.NewMemberID = " reviewer "
	a.addExpertMember()
	if len(draft.Members) != 2 || draft.Members[1].ID != "reviewer" || draft.Members[1].Role != expertRoleMember {
		t.Fatalf("added member = %+v", draft.Members)
	}
	draft.NewMemberID = "reviewer"
	a.addExpertMember()
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "已存在") {
		t.Fatalf("duplicate member status = %+v", a.experts)
	}

	draft.NewAgentID = " reviewer "
	a.addExpertAgent()
	if len(draft.Agents) != 2 || draft.Agents[1].ID != "reviewer" {
		t.Fatalf("added agent = %+v", draft.Agents)
	}
	if !strings.Contains(draft.Agents[1].Source, "name: reviewer") {
		t.Fatalf("agent stub = %q", draft.Agents[1].Source)
	}
	draft.NewAgentID = "reviewer"
	a.addExpertAgent()
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "已存在") {
		t.Fatalf("duplicate agent status = %+v", a.experts)
	}

	a.removeExpertMember(1)
	a.removeExpertAgent(1)
	if len(draft.Members) != 1 || len(draft.Agents) != 1 {
		t.Fatalf("remove member/agent = %+v %+v", draft.Members, draft.Agents)
	}
	a.removeExpertMember(9)
	a.removeExpertAgent(9)
}

func TestExpertsCapabilityGateAndInvalidation(t *testing.T) {
	a := testApp()
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "experts"
	a.startExpertsPanelLoad()
	if a.expertsCapable() {
		t.Fatal("manageExperts must be required")
	}
	if a.experts.Loading || !a.experts.Loaded || !strings.Contains(a.experts.Err, "manageExperts") {
		t.Fatalf("gated load state = %+v", a.experts)
	}

	gated := testApp()
	gated.settingsCategory = "agent"
	gated.settingsTab = "experts"
	gated.ensureSettingsTabVisible()
	if gated.settingsTab == "experts" {
		t.Fatalf("experts tab must be hidden without manageExperts: %q", gated.settingsTab)
	}

	b := expertsTestApp(t, fullExpertFeatures(), expertScopeProject)
	b.experts.Draft = newExpertDraft(expertScopeProject)
	b.experts.Status = "保存中"
	b.experts.StatusIsError = true
	b.experts.PendingDeleteOpen = true
	b.experts.PendingDeleteName = "project-expert"
	oldSeq := atomic.LoadInt64(&b.experts.Seq)
	b.invalidateExpertsPanel()
	if atomic.LoadInt64(&b.experts.Seq) != oldSeq+1 {
		t.Fatal("invalidation must advance the sequence guard")
	}
	if b.experts.Scope != expertScopeGlobal || b.experts.View != nil || b.experts.Draft != nil ||
		b.experts.Loaded || b.experts.PendingDeleteOpen || b.experts.Status != "" {
		t.Fatalf("invalidated state = %+v", b.experts)
	}
}

func TestExpertsPanelSequenceGuards(t *testing.T) {
	a := expertsTestApp(t, fullExpertFeatures(), expertScopeGlobal)
	seq := atomic.LoadInt64(&a.experts.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.expertsPanelCurrent(seq, reloadSeq) {
		t.Fatal("current experts request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.expertsPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}
	atomic.AddInt64(&a.experts.Seq, 1)
	if a.expertsPanelCurrent(seq, atomic.LoadInt64(&a.reloadSeq)) {
		t.Fatal("a stale experts response must be rejected")
	}
	a.settingsOpen = false
	if a.expertsPanelCurrent(atomic.LoadInt64(&a.experts.Seq), atomic.LoadInt64(&a.reloadSeq)) {
		t.Fatal("a response after leaving settings must be rejected")
	}
}

func TestExpertsProjectScopeRequiresWorkspace(t *testing.T) {
	a := expertsTestApp(t, fullExpertFeatures(), expertScopeGlobal)
	a.workspace = ""
	a.draftCwd = ""
	a.cwd = ""
	a.experts.Scope = expertScopeGlobal
	a.setExpertScope(expertScopeProject)
	if a.experts.Scope != expertScopeGlobal {
		t.Fatalf("scope switched without workspace: %+v", a.experts)
	}
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "工作目录") {
		t.Fatalf("missing-workspace status = %+v", a.experts)
	}

	a.experts.Scope = expertScopeProject
	a.experts.Loaded = false
	a.startExpertsPanelLoad()
	if !a.experts.Loaded || !strings.Contains(a.experts.Err, "工作目录") {
		t.Fatalf("project load gate = %+v", a.experts)
	}
}

func TestExpertsOfflineAndReadonlyActions(t *testing.T) {
	a := expertsTestApp(t, fullExpertFeatures(), expertScopeGlobal)
	a.startNewExpert()
	a.experts.Draft.Name = "new-expert"
	a.saveExpertDraft()
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "尚未连接") {
		t.Fatalf("offline save status = %+v", a.experts)
	}

	a.experts.Status = ""
	a.experts.StatusIsError = false
	a.openExpertEditor("software-company")
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "内置") {
		t.Fatalf("builtin edit status = %+v", a.experts)
	}
	a.experts.Status = ""
	a.experts.StatusIsError = false
	a.openExpertEditor("global-expert")
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "尚未连接") {
		t.Fatalf("offline get status = %+v", a.experts)
	}

	a.experts.Status = ""
	a.experts.StatusIsError = false
	a.requestDeleteExpert("software-company")
	if !a.experts.StatusIsError || !strings.Contains(a.experts.Status, "只读") {
		t.Fatalf("builtin delete status = %+v", a.experts)
	}

	b := expertsTestApp(t, fullExpertFeatures(), expertScopeProject)
	b.requestDeleteExpert("global-expert")
	if !b.experts.StatusIsError || !strings.Contains(b.experts.Status, "作用范围") {
		t.Fatalf("cross-scope delete status = %+v", b.experts)
	}
	b.experts.Status = ""
	b.experts.StatusIsError = false
	b.requestDeleteExpert("project-expert")
	if !b.experts.PendingDeleteOpen || b.experts.PendingDeleteName != "project-expert" {
		t.Fatalf("delete confirmation = %+v", b.experts)
	}
	b.cancelDeleteExpert()
	if b.experts.PendingDeleteOpen || b.experts.PendingDeleteName != "" {
		t.Fatalf("cancelled delete = %+v", b.experts)
	}
}

func TestExpertsPanelHeadlessRendersScopesCatalogAndEditor(t *testing.T) {
	a := expertsTestApp(t, fullExpertFeatures(), expertScopeGlobal)
	tt := ui.NewTester(a.view, 1180, 980)
	for _, want := range []string{
		"主角团", "作用范围", "全局", "项目", "新建主角团", "团队目录",
		"软件公司", "内置", "只读", "全局专家", "项目专家", "无效", "manifest name 不一致",
		"编辑", "删除",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("experts panel missing %q: %q", want, tt.Texts())
		}
	}

	a.startNewExpert()
	tt.Frame()
	for _, want := range []string{
		"新团队", "团队类型", "团队标识（Name）", "显示名称（中文）", "成员", "Agent 源文件",
		"添加成员", "添加 Agent", "创建团队", "取消",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("new expert editor missing %q: %q", want, tt.Texts())
		}
	}

	b := expertsTestApp(t, fullExpertFeatures(), expertScopeGlobal)
	b.experts.Draft = expertDraftFromBundle(expertTestBundle())
	tt = ui.NewTester(b.view, 1180, 980)
	for _, want := range []string{"global-expert", "保存团队", "删除", "lead", "reviewer", "Agent 源文件"} {
		if !textPresent(tt, want) {
			t.Fatalf("loaded expert editor missing %q: %q", want, tt.Texts())
		}
	}
}

func TestExpertsPanelHeadlessLoadingErrorAndProjectScope(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.setFeatures(fullExpertFeatures())
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "experts"
	a.experts = newExpertsPanelState()
	a.experts.Loading = true
	tt := ui.NewTester(a.view, 1180, 980)
	if !textPresent(tt, "正在读取团队目录…") {
		t.Fatalf("experts loading state missing: %q", tt.Texts())
	}

	a.experts.Loading = false
	a.experts.Err = "读取团队目录失败：boom"
	a.experts.Loaded = false
	tt.Frame()
	for _, want := range []string{"团队管理不可用", "读取团队目录失败：boom", "重试"} {
		if !textPresent(tt, want) {
			t.Fatalf("experts error state missing %q: %q", want, tt.Texts())
		}
	}

	b := expertsTestApp(t, fullExpertFeatures(), expertScopeProject)
	tt = ui.NewTester(b.view, 1180, 980)
	for _, want := range []string{"项目工作目录：/work/project", "项目专家", "软件公司"} {
		if !textPresent(tt, want) {
			t.Fatalf("project experts panel missing %q: %q", want, tt.Texts())
		}
	}
}
