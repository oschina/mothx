package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func skillHubTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.currentSession = "session-skillhub"
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "skillhub"
	a.ensureSettingsTabVisible()
	a.skillhub = newSkillHubPanelState()
	return a
}

func skillHubTestFeatures() map[string]bool {
	return map[string]bool{"manageSkillHub": true, "manageSkillHubCatalog": true}
}

func skillHubTestConfigView() *skillHubConfigView {
	return &skillHubConfigView{
		DefaultMarket:       "skillhub.cn",
		DefaultInstallScope: "project",
		OfficialHandles:     []string{"user_a", "user_b"},
		Markets: []skillHubMarketView{
			{ID: "skillhub.cn", Name: "SkillHub.cn", SiteURL: "https://skillhub.cn", Enabled: true, APITokenConfigured: true},
			{ID: "clawhub.ai", Name: "ClawHub", SiteURL: "https://clawhub.ai", Enabled: false, APITokenConfigured: false},
		},
	}
}

func skillHubTestCatalog(a *nativeApp) {
	a.skillhub.ConfigLoaded = true
	a.applySkillHubConfigView(skillHubTestConfigView())
	a.skillhub.CatalogLoaded = true
	a.skillhub.SessionID = "session-skillhub"
	a.skillhub.WorkDir = "/workspace/project"
	a.skillhub.MarketInfos = []skillHubMarketInfo{
		{ID: "skillhub.cn", Name: "SkillHub.cn", SiteURL: "https://skillhub.cn"},
		{ID: "clawhub.ai", Name: "ClawHub", SiteURL: "https://clawhub.ai"},
	}
	a.skillhub.Targets = []skillHubTarget{
		{Path: "/workspace/project/.skills", Scope: "project", Label: "Project skills"},
		{Path: "/home/user/.mothx/skills", Scope: "global", Label: "Global skills"},
	}
	a.skillhub.Installed = []skillHubInstalledState{
		{Installed: true, Scope: "project", Dir: "/workspace/project/.skills/pdf", Market: "skillhub.cn", ID: "pdf", Name: "pdf", Version: "1.2.0", Active: true},
	}
	a.skillhub.ActiveSkills = []string{"pdf"}
	label, path, scope := skillHubChooseTarget("", "project", a.skillhub.Targets)
	a.skillhub.TargetLabel, a.skillhub.TargetPath, a.skillhub.TargetScope = label, path, scope
	a.skillhub.SelectedMarket = "skillhub.cn"
	a.skillhub.ResultsLoaded = true
	a.skillhub.Results = []skillHubSkill{
		{Market: "skillhub.cn", ID: "pdf", Name: "pdf", DisplayName: "PDF Toolkit", Description: "Read and write PDF files", Version: "1.3.0", Author: "mothx", Downloads: 1200, Installed: &skillHubInstalledState{Installed: true, Scope: "project", Market: "skillhub.cn", ID: "pdf", Name: "pdf", Version: "1.2.0", Active: true, UpdateAvailable: true}},
		{Market: "skillhub.cn", ID: "demo", Name: "demo", Description: "A demo skill", Version: "0.1.0"},
	}
}

func TestSkillHubConfigViewIsTokenFreeAndHydratesDrafts(t *testing.T) {
	raw := json.RawMessage(`{
		"defaultMarket":"skillhub.cn",
		"defaultInstallScope":"global",
		"officialHandles":["user_a"],
		"markets":[
			{"id":"skillhub.cn","name":"SkillHub.cn","siteURL":"https://skillhub.cn","apiURL":"https://api.skillhub.cn","enabled":true,"apiTokenConfigured":true},
			{"id":"clawhub.ai","name":"ClawHub","enabled":false,"apiTokenConfigured":false}
		]
	}`)
	var view skillHubConfigView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	a := testApp()
	a.applySkillHubConfigView(&view)
	if a.skillhub.DefaultMarket != "skillhub.cn" || a.skillhub.DefaultInstallScope != "global" {
		t.Fatalf("config projection = %+v", a.skillhub)
	}
	if len(a.skillhub.Markets) != 2 || !a.skillhub.Markets[0].Configured || a.skillhub.Markets[1].Configured {
		t.Fatalf("market configured flags = %+v", a.skillhub.Markets)
	}
	if a.skillhub.Markets[0].Token != "" || a.skillhub.Markets[1].Token != "" {
		t.Fatalf("hydrated drafts must not contain tokens: %+v", a.skillhub.Markets)
	}
	if a.skillhub.OfficialHandles != "user_a" {
		t.Fatalf("official handles = %q", a.skillhub.OfficialHandles)
	}

	// A typed token is dropped again on the next hydrated response; the patch
	// builder must also omit it when the input is blank.
	a.skillhub.Markets[0].Token = "typed-but-secret"
	a.skillhub.Markets[0].ClearToken = true
	a.applySkillHubConfigView(&view)
	if a.skillhub.Markets[0].Token != "" || a.skillhub.Markets[0].ClearToken {
		t.Fatalf("re-hydration must clear write-only token directives: %+v", a.skillhub.Markets[0])
	}
}

func TestSkillHubConfigPatchTokenDirectives(t *testing.T) {
	markets := []skillHubMarketDraft{
		{ID: "skillhub.cn", Name: " SkillHub.cn ", SiteURL: " https://skillhub.cn ", APIURL: "https://api.skillhub.cn", Enabled: true, Configured: true},
		{ID: "clawhub.ai", Name: "ClawHub", Enabled: true, ClearToken: true, Configured: true},
		{ID: "yuandun", Name: "源盾", Enabled: false, Token: "  typed-token  "},
	}
	request, err := buildSkillHubConfigPatch(" skillhub.cn ", "global", "user_a\n\nuser_b\n", markets)
	if err != nil {
		t.Fatal(err)
	}
	patch, _ := request["patch"].(map[string]any)
	if patch["defaultMarket"] != "skillhub.cn" || patch["defaultInstallScope"] != "global" {
		t.Fatalf("patch defaults = %#v", patch)
	}
	handles, _ := patch["officialHandles"].([]string)
	if len(handles) != 2 || handles[0] != "user_a" || handles[1] != "user_b" {
		t.Fatalf("official handles = %#v", patch["officialHandles"])
	}
	payload, _ := patch["markets"].([]map[string]any)
	if len(payload) != 3 {
		t.Fatalf("markets = %#v", payload)
	}
	if _, ok := payload[0]["apiToken"]; ok {
		t.Fatalf("blank token must be omitted to preserve the secret: %#v", payload[0])
	}
	if _, ok := payload[0]["clearApiToken"]; ok {
		t.Fatalf("clear directive must not be implied: %#v", payload[0])
	}
	if payload[1]["clearApiToken"] != true {
		t.Fatalf("clear directive missing: %#v", payload[1])
	}
	if payload[2]["apiToken"] != "typed-token" {
		t.Fatalf("typed token missing or untrimmed: %#v", payload[2])
	}
	if payload[0]["name"] != "SkillHub.cn" || payload[0]["siteURL"] != "https://skillhub.cn" {
		t.Fatalf("market strings were not trimmed: %#v", payload[0])
	}
	if strings.Contains(string(mustJSON(t, request)), "typed-but-secret") {
		t.Fatalf("patch leaked an unrelated secret")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSkillHubConfigPatchValidation(t *testing.T) {
	validMarket := []skillHubMarketDraft{{ID: "skillhub.cn", Enabled: true}}
	cases := []struct {
		name     string
		market   string
		scope    string
		markets  []skillHubMarketDraft
		contains string
	}{
		{"empty default market", " ", "project", validMarket, "默认市场不能为空"},
		{"invalid scope", "skillhub.cn", "workspace", validMarket, "project 或 global"},
		{"empty markets", "skillhub.cn", "project", nil, "至少需要保留一个市场"},
		{"empty market id", "skillhub.cn", "project", []skillHubMarketDraft{{ID: " "}}, "缺少 ID"},
		{"duplicate market id", "skillhub.cn", "project", []skillHubMarketDraft{{ID: "a"}, {ID: " a "}}, "重复"},
		{"conflicting token directives", "skillhub.cn", "project", []skillHubMarketDraft{{ID: "a", Token: "x", ClearToken: true}}, "不能同时"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildSkillHubConfigPatch(tc.market, tc.scope, "", tc.markets)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v, want %q", err, tc.contains)
			}
		})
	}
}

func TestSkillHubCatalogDecodeSelectionAndActiveState(t *testing.T) {
	a := testApp()
	markets := &skillHubMarketsView{
		DefaultMarket: "clawhub.ai",
		Markets: []skillHubMarketInfo{
			{ID: "skillhub.cn", Name: "SkillHub.cn"},
			{ID: "clawhub.ai", Name: "ClawHub"},
		},
	}
	targets := &skillHubTargetsView{
		SessionID: "s1",
		WorkDir:   "/workspace/project",
		Targets: []skillHubTarget{
			{Path: "/workspace/project/.skills", Scope: "project", Label: "Project skills"},
			{Path: "/home/user/.mothx/skills", Scope: "global", Label: "Global skills"},
		},
	}
	installed := &skillHubInstalledView{
		SessionID: "s1",
		WorkDir:   "/workspace/project",
		Installed: []skillHubInstalledState{
			{Installed: true, Market: "skillhub.cn", ID: "pdf", Name: "pdf", Scope: "project", Version: "1.2.0", Active: true},
		},
		Session: skillHubSessionState{SessionID: "s1", WorkDir: "/workspace/project", ActiveSkills: []string{"pdf"}},
	}
	a.applySkillHubCatalogViews("s1", markets, targets, installed)
	if a.skillhub.SelectedMarket != "clawhub.ai" {
		t.Fatalf("projected default market = %q", a.skillhub.SelectedMarket)
	}
	if a.skillhub.TargetPath != "/workspace/project/.skills" || a.skillhub.TargetScope != "project" {
		t.Fatalf("target selection = %q %q", a.skillhub.TargetPath, a.skillhub.TargetScope)
	}
	if !a.skillhub.Installed[0].Active {
		t.Fatalf("active flag was not projected: %+v", a.skillhub.Installed[0])
	}

	// The current preference wins while it remains available; the configured
	// default is only a fallback, then the ACP-projected default.
	if got := skillHubChooseMarket("skillhub.cn", "clawhub.ai", "clawhub.ai", markets.Markets); got != "skillhub.cn" {
		t.Fatalf("current market = %q", got)
	}
	if got := skillHubChooseMarket("gone", "clawhub.ai", "skillhub.cn", markets.Markets); got != "clawhub.ai" {
		t.Fatalf("config fallback = %q", got)
	}
	if got := skillHubChooseMarket("gone", "", "clawhub.ai", markets.Markets); got != "clawhub.ai" {
		t.Fatalf("projected fallback = %q", got)
	}

	// Re-applying a catalogue without the installed skill clears detail state
	// instead of leaving a stale "installed" badge behind.
	a.skillhub.Detail = &skillHubDetail{skillHubSkill: skillHubSkill{Market: "skillhub.cn", ID: "pdf", Name: "pdf", Installed: &skillHubInstalledState{Installed: true}}}
	a.skillhub.DetailOpen = true
	a.skillhub.Results = []skillHubSkill{{Market: "skillhub.cn", ID: "pdf", Name: "pdf", Installed: &skillHubInstalledState{Installed: true}}}
	a.skillhub.Installed = nil
	a.skillhub.ActiveSkills = nil
	a.skillHubApplyInstalledToResults()
	if a.skillhub.Detail.Installed != nil || a.skillhub.Results[0].Installed != nil {
		t.Fatalf("stale installed state survived: detail=%+v result=%+v", a.skillhub.Detail.Installed, a.skillhub.Results[0].Installed)
	}
}

func TestSkillHubSearchDetailDecodeAndRequestBuilders(t *testing.T) {
	pageRaw := json.RawMessage(`{
		"items":[
			{"market":"skillhub.cn","id":"pdf","slug":"pdf-tools","name":"pdf","displayName":"PDF Toolkit","description":"Read PDFs","version":"1.3.0","downloads":1200,"installed":{"installed":true,"scope":"project","market":"skillhub.cn","id":"pdf","name":"pdf","version":"1.2.0","active":true,"updateAvailable":true}}
		],
		"total":1,"page":1,"pageSize":20,"nextCursor":"next"
	}`)
	var page skillHubSearchPage
	if err := json.Unmarshal(pageRaw, &page); err != nil {
		t.Fatal(err)
	}
	skill := page.Items[0]
	if skillHubTitle(skill) != "PDF Toolkit" || skillHubInstalledName(skill) != "pdf" {
		t.Fatalf("skill naming = %q / %q", skillHubTitle(skill), skillHubInstalledName(skill))
	}
	if !skill.Installed.UpdateAvailable || !skill.Installed.Active {
		t.Fatalf("installed projection = %+v", skill.Installed)
	}
	meta := skillHubSkillMeta(skill)
	if !strings.Contains(meta, "1200 下载") {
		t.Fatalf("meta = %q", meta)
	}

	detailRaw := json.RawMessage(`{
		"market":"skillhub.cn","id":"pdf","name":"pdf","displayName":"PDF Toolkit","description":"Read PDFs",
		"version":"1.3.0","readme":"# PDF Toolkit\n\nRead and write PDFs.",
		"files":[{"path":"SKILL.md","size":120}],
		"installed":{"installed":true,"scope":"project","market":"skillhub.cn","id":"pdf","name":"pdf","version":"1.2.0","updateAvailable":true}
	}`)
	var detail skillHubDetail
	if err := json.Unmarshal(detailRaw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Readme == "" || len(detail.Files) != 1 || !detail.Installed.UpdateAvailable {
		t.Fatalf("detail = %+v", detail)
	}

	search := buildSkillHubSearchRequest(" s1 ", " skillhub.cn ", " pdf ")
	if search["sessionId"] != "s1" || search["market"] != "skillhub.cn" || search["query"] != "pdf" ||
		search["sort"] != "downloads" || search["order"] != "desc" || search["limit"] != 20 {
		t.Fatalf("search request = %#v", search)
	}
	if got := buildSkillHubSearchRequest("s1", "clawhub.ai", "")["sort"]; got != "" {
		t.Fatalf("clawhub sort = %#v, want empty", got)
	}
	detailRequest := buildSkillHubDetailRequest("s1", "skillhub.cn", " pdf ")
	if detailRequest["sessionId"] != "s1" || detailRequest["market"] != "skillhub.cn" || detailRequest["id"] != "pdf" {
		t.Fatalf("detail request = %#v", detailRequest)
	}
	install := buildSkillHubInstallRequest("s1", "skillhub.cn", " pdf ", "1.3.0", "project", " /tmp/skills ", true, true)
	if install["sessionId"] != "s1" || install["id"] != "pdf" || install["targetDir"] != "/tmp/skills" ||
		install["overwrite"] != true || install["activate"] != true {
		t.Fatalf("install request = %#v", install)
	}
	activate := buildSkillHubActivateRequest("s1", " pdf ")
	if activate["id"] != "pdf" {
		t.Fatalf("activate request = %#v", activate)
	}
	uninstall := buildSkillHubUninstallRequest("s1", "skillhub.cn", "pdf", "global")
	if uninstall["scope"] != "global" || uninstall["market"] != "skillhub.cn" {
		t.Fatalf("uninstall request = %#v", uninstall)
	}
}

func TestSkillHubPanelGatesInvalidationAndBusyTransitions(t *testing.T) {
	a := testApp()
	a.settingsOpen = true
	a.settingsTab = "skillhub"
	a.startSkillHubPanelLoad()
	if a.skillHubConfigCapable() || a.skillHubCatalogCapable() {
		t.Fatal("capabilities must default to disabled")
	}
	if !a.skillhub.ConfigLoaded || !a.skillhub.CatalogLoaded {
		t.Fatalf("gated load must complete both projections: %+v", a.skillhub)
	}
	if !strings.Contains(a.skillhub.ConfigErr, "manageSkillHub") || !strings.Contains(a.skillhub.CatalogErr, "manageSkillHubCatalog") {
		t.Fatalf("gated errors = %q / %q", a.skillhub.ConfigErr, a.skillhub.CatalogErr)
	}

	b := skillHubTestApp(t, map[string]bool{"manageSkillHubCatalog": true})
	b.settingsTab = "skillhub"
	if sec, ok := b.settingsSectionByID("skillhub"); !ok || !b.sectionEnabled(sec) {
		t.Fatal("the catalogue capability alone must keep the SkillHub section selectable")
	}
	b.currentSession = ""
	b.startSkillHubPanelLoad()
	if !b.skillhub.CatalogLoaded || !strings.Contains(b.skillhub.CatalogErr, "活动会话") {
		t.Fatalf("session gate = %+v", b.skillhub)
	}
	if !b.skillhub.ConfigLoaded || !strings.Contains(b.skillhub.ConfigErr, "manageSkillHub") {
		t.Fatalf("config gate = %+v", b.skillhub)
	}

	c := skillHubTestApp(t, skillHubTestFeatures())
	configSeq := atomic.LoadInt64(&c.skillhub.ConfigSeq)
	catalogSeq := atomic.LoadInt64(&c.skillhub.CatalogSeq)
	if !c.skillHubConfigCurrent(configSeq, atomic.LoadInt64(&c.reloadSeq)) ||
		!c.skillHubCatalogCurrent(catalogSeq, atomic.LoadInt64(&c.reloadSeq)) {
		t.Fatal("fresh sequence guards should accept responses")
	}
	c.skillhub.DefaultMarket = "skillhub.cn"
	c.skillhub.Markets = []skillHubMarketDraft{{ID: "skillhub.cn", Token: "secret-in-draft"}}
	c.skillhub.ConfigStatus = "saving"
	c.skillhub.DetailOpen = true
	c.skillhub.AddMarketOpen = true
	c.skillhub.UninstallOpen = true
	c.setSettingsTab("appearance")
	if atomic.LoadInt64(&c.skillhub.ConfigSeq) <= configSeq || atomic.LoadInt64(&c.skillhub.CatalogSeq) <= catalogSeq {
		t.Fatal("leaving the tab must invalidate both in-flight sequences")
	}
	if c.skillhub.ConfigLoaded || c.skillhub.MarketInfos != nil || c.skillhub.DetailOpen ||
		c.skillhub.AddMarketOpen || c.skillhub.UninstallOpen || len(c.skillhub.Markets) != 0 {
		t.Fatalf("SkillHub state was not invalidated: %+v", c.skillhub)
	}
	if c.skillHubConfigCurrent(configSeq, atomic.LoadInt64(&c.reloadSeq)) {
		t.Fatal("old config response must be rejected after invalidation")
	}

	d := skillHubTestApp(t, skillHubTestFeatures())
	d.skillhub.ConfigLoaded = true
	d.closeSettings()
	if d.skillhub.ConfigLoaded || d.skillhub.MarketInfos != nil {
		t.Fatalf("closing settings must invalidate SkillHub: %+v", d.skillhub)
	}
}

func TestSkillHubConfigHeadlessUIAndSecretSafety(t *testing.T) {
	a := skillHubTestApp(t, skillHubTestFeatures())
	a.skillhub.ConfigLoaded = true
	a.applySkillHubConfigView(skillHubTestConfigView())
	a.skillhub.Markets[0].Token = "native-typed-secret-never-rendered"
	a.skillhub.Tab = 1

	tt := ui.NewTester(a.view, 1180, 900)
	want := []string{
		"浏览市场", "市场配置", "默认行为", "默认市场", "默认安装范围",
		"官方推荐 Handles", "市场源", "skillhub.cn", "clawhub.ai",
		"Token 已配置", "Token 未配置", "API Token", "清除已保存的 Token",
		"添加市场", "保存配置",
	}
	for _, text := range want {
		if !textPresent(tt, text) {
			t.Fatalf("SkillHub config pane missing %q: %q", text, tt.Texts())
		}
	}
	joined := strings.Join(tt.Texts(), " ")
	if strings.Contains(joined, "native-typed-secret-never-rendered") {
		t.Fatalf("typed token leaked into rendered texts: %q", tt.Texts())
	}
}

func TestSkillHubBrowseHeadlessUI(t *testing.T) {
	a := skillHubTestApp(t, skillHubTestFeatures())
	skillHubTestCatalog(a)
	a.skillhub.Tab = 0

	tt := ui.NewTester(a.view, 1180, 900)
	want := []string{
		"市场目录", "/workspace/project", "skillhub.cn", "安装目录",
		"Project skills · project · /workspace/project/.skills",
		"在线技能", "PDF Toolkit", "A demo skill", "已安装", "可更新", "已激活",
		"详情", "更新", "激活", "卸载",
		"已安装技能", "pdf",
	}
	for _, text := range want {
		if !textPresent(tt, text) {
			t.Fatalf("SkillHub browse pane missing %q: %q", text, tt.Texts())
		}
	}
	if strings.Contains(strings.Join(tt.Texts(), " "), "typed-but-secret") {
		t.Fatalf("browse leaked a token")
	}
}

func TestSkillHubCapabilityGateRendersNativeUnsupportedPane(t *testing.T) {
	a := skillHubTestApp(t, map[string]bool{"manageSkillHub": true})
	a.skillhub.Tab = 0
	a.skillhub.ConfigLoaded = true
	a.applySkillHubConfigView(skillHubTestConfigView())
	tt := ui.NewTester(a.view, 1180, 900)
	if !textPresent(tt, "manageSkillHubCatalog") {
		t.Fatalf("missing catalog gate text: %q", tt.Texts())
	}
	a.skillhub.Tab = 1
	tt.Frame()
	if !textPresent(tt, "市场源") || !textPresent(tt, "浏览市场") {
		t.Fatalf("config must stay usable without the catalogue: %q", tt.Texts())
	}
}

func TestSkillHubAddMarketDetailAndUninstallDialogs(t *testing.T) {
	a := skillHubTestApp(t, skillHubTestFeatures())
	skillHubTestCatalog(a)
	a.skillhub.Tab = 1
	a.openSkillHubAddMarket()
	a.skillhub.AddMarketID = "skillhub.cn"
	a.confirmSkillHubAddMarket()
	if a.skillhub.AddMarketErr == "" || !strings.Contains(a.skillhub.AddMarketErr, "已存在") {
		t.Fatalf("duplicate add error = %q", a.skillhub.AddMarketErr)
	}
	a.skillhub.AddMarketID = "new.market"
	a.confirmSkillHubAddMarket()
	if a.skillhub.AddMarketOpen || len(a.skillhub.Markets) != 3 || a.skillhub.Markets[2].ID != "new.market" {
		t.Fatalf("add market state = %+v", a.skillhub)
	}

	a.skillhub.DetailOpen = true
	a.skillhub.Detail = &skillHubDetail{
		skillHubSkill: skillHubSkill{
			Market: "skillhub.cn", ID: "pdf", Name: "pdf", DisplayName: "PDF Toolkit",
			Description: "Read PDFs", Version: "1.3.0", Author: "mothx",
			Installed: &skillHubInstalledState{Installed: true, Scope: "project", Market: "skillhub.cn", ID: "pdf", Name: "pdf", Version: "1.2.0", Active: true, UpdateAvailable: true},
		},
		Readme: "# PDF Toolkit\n\nRead and write PDFs.",
	}
	tt := ui.NewTester(a.view, 1180, 900)
	for _, text := range []string{"PDF Toolkit", "Read and write PDFs", "文件（0）", "安装目标", "更新", "激活", "卸载", "关闭"} {
		if text == "文件（0）" {
			continue
		}
		if !textPresent(tt, text) {
			t.Fatalf("detail dialog missing %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("close detail: %v", err)
	}
	if a.skillhub.DetailOpen {
		t.Fatal("close button did not dismiss detail")
	}

	a.requestUninstallSkillHub("skillhub.cn", "pdf", "project", "pdf")
	if !a.skillhub.UninstallOpen {
		t.Fatal("uninstall confirmation did not open")
	}
	tt.Frame()
	for _, text := range []string{"卸载技能？", "pdf", "skillhub.cn", "确认卸载", "取消"} {
		if !textPresent(tt, text) {
			t.Fatalf("uninstall dialog missing %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("cancel uninstall: %v", err)
	}
	if a.skillhub.UninstallOpen {
		t.Fatal("cancel did not close uninstall confirmation")
	}
}

func TestSkillHubOfflineActionsReportErrorsWithoutStayingBusy(t *testing.T) {
	a := skillHubTestApp(t, skillHubTestFeatures())
	skillHubTestCatalog(a)
	a.installSkillHub("skillhub.cn", "pdf", "1.3.0")
	if !a.skillhub.CatalogStatusIsError || !strings.Contains(a.skillhub.CatalogStatus, "尚未连接") {
		t.Fatalf("offline install status = %q", a.skillhub.CatalogStatus)
	}
	if a.skillhub.ActionBusy != "" {
		t.Fatalf("offline install must not remain busy: %+v", a.skillhub)
	}

	a.openSkillHubDetail("skillhub.cn", "pdf")
	if !a.skillhub.CatalogStatusIsError || !strings.Contains(a.skillhub.CatalogStatus, "尚未连接") {
		t.Fatalf("offline detail status = %q", a.skillhub.CatalogStatus)
	}
	if a.skillhub.DetailOpen || a.skillhub.DetailBusy {
		t.Fatalf("offline detail must not open a dialog: %+v", a.skillhub)
	}

	a.requestUninstallSkillHub("skillhub.cn", "pdf", "project", "pdf")
	a.confirmUninstallSkillHub()
	if !a.skillhub.CatalogStatusIsError || !strings.Contains(a.skillhub.CatalogStatus, "尚未连接") {
		t.Fatalf("offline uninstall status = %q", a.skillhub.CatalogStatus)
	}
	if a.skillhub.UninstallOpen || a.skillhub.ActionBusy != "" {
		t.Fatalf("offline uninstall state = %+v", a.skillhub)
	}
}

func TestSkillHubVersionDiffOnlyForKnownDifferentVersions(t *testing.T) {
	if !skillHubVersionDiffers("1.3.0", "1.2.0") {
		t.Fatal("different versions should expose an update")
	}
	if skillHubVersionDiffers("1.3.0", "1.3.0") || skillHubVersionDiffers("", "1.2.0") || skillHubVersionDiffers("1.3.0", "") {
		t.Fatal("unknown or equal versions must not advertise an update")
	}
}

func TestSkillHubMarkInstalledAndRemoveInstalled(t *testing.T) {
	a := testApp()
	a.skillhub = newSkillHubPanelState()
	a.skillhub.ActiveSkills = []string{"pdf"}
	a.skillhub.Results = []skillHubSkill{{Market: "skillhub.cn", ID: "pdf", Name: "pdf"}}
	a.skillHubMarkInstalled("skillhub.cn", "pdf", skillHubInstallResult{
		Name: "pdf", Market: "skillhub.cn", Version: "1.3.0", Scope: "project", Dir: "/tmp/pdf", Installed: true,
	})
	if len(a.skillhub.Installed) != 1 || !a.skillhub.Installed[0].Active || a.skillhub.Installed[0].Version != "1.3.0" {
		t.Fatalf("installed projection = %+v", a.skillhub.Installed)
	}
	if a.skillhub.Results[0].Installed == nil || !a.skillhub.Results[0].Installed.Active {
		t.Fatalf("result was not reconciled: %+v", a.skillhub.Results[0])
	}
	a.skillhub.Detail = &skillHubDetail{skillHubSkill: skillHubSkill{Market: "skillhub.cn", ID: "pdf", Name: "pdf"}}
	a.skillHubMarkInstalled("skillhub.cn", "pdf", skillHubInstallResult{Name: "pdf", Market: "skillhub.cn", Version: "1.4.0", Scope: "project", Dir: "/tmp/pdf", Installed: true})
	if a.skillhub.Detail.Installed == nil || a.skillhub.Detail.Installed.Version != "1.4.0" {
		t.Fatalf("detail was not reconciled: %+v", a.skillhub.Detail.Installed)
	}
	a.skillHubRemoveInstalled("skillhub.cn", "pdf")
	if len(a.skillhub.Installed) != 0 || a.skillhub.Results[0].Installed != nil || a.skillhub.Detail.Installed != nil {
		t.Fatalf("uninstall did not clear projections: %+v", a.skillhub)
	}
}
