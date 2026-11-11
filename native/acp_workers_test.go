package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func acpWorkersTestProfile() acpWorkerProfileView {
	return acpWorkerProfileView{
		ID:                  "codex",
		Title:               "Codex",
		Transport:           "stdio",
		Model:               "fast-model",
		Enabled:             true,
		RouterEligible:      true,
		PermissionPolicy:    "autoAllowOnce",
		Driver:              "codex-acp",
		MCPBridge:           "off",
		CapabilityOverrides: []string{"promptImage", "terminal"},
	}
}

func acpWorkersTestCatalog() acpWorkerCatalogView {
	return acpWorkerCatalogView{
		Profiles:        []acpWorkerProfileView{acpWorkersTestProfile()},
		CapabilityNames: []string{"modelConfigOption", "promptImage", "terminal"},
	}
}

func acpWorkersTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "acpWorkers"
	a.ensureSettingsTabVisible()
	view := acpWorkersTestCatalog()
	a.applyACPWorkerCatalog(&view)
	return a
}

func TestACPWorkersCatalogDecodeHydratesSafeDrafts(t *testing.T) {
	raw := json.RawMessage(`{
		"profiles": [{
			"id": "codex",
			"title": "Codex",
			"transport": "stdio",
			"model": "fast-model",
			"endpoint": "https://worker.example/rpc?token=secret",
			"enabled": true,
			"routerEligible": true,
			"driver": "codex-acp",
			"mcpBridge": "off",
			"capabilityOverrides": ["promptImage"],
			"command": ["/opt/secret/codex"],
			"environment": ["API_TOKEN=should-not-leak"],
			"url": "https://secret.example",
			"configPath": "/secret/settings.json"
		}],
		"capabilityNames": ["promptImage", "terminal"]
	}`)
	var view acpWorkerCatalogView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	a := testApp()
	a.applyACPWorkerCatalog(&view)
	if !a.acpWorkers.Loaded || a.acpWorkers.Loading || a.acpWorkers.Err != "" {
		t.Fatalf("loaded state = %+v", a.acpWorkers)
	}
	if len(a.acpWorkers.Profiles) != 1 || len(a.acpWorkers.CapabilityNames) != 2 {
		t.Fatalf("catalog = %+v", a.acpWorkers)
	}
	draft, ok := a.acpWorkerDraft("codex")
	if !ok || draft.Model != "fast-model" || draft.Driver != "codex-acp" ||
		draft.MCPBridge != "off" || !draft.Overrides["promptImage"] || draft.Overrides["terminal"] {
		t.Fatalf("draft = %+v, ok=%v", draft, ok)
	}
}

func TestACPWorkerDraftDefaultsMatchDesktopPanel(t *testing.T) {
	draft := acpWorkerDraftFromProfile(acpWorkerProfileView{ID: "worker"})
	if draft.Model != "" || draft.Driver != "generic-acp" || draft.MCPBridge != "auto" {
		t.Fatalf("default draft = %+v", draft)
	}
	if len(draft.Overrides) != 0 {
		t.Fatalf("default overrides = %+v", draft.Overrides)
	}
}

func TestACPWorkerPatchRequestOnlySendsBehaviorMetadata(t *testing.T) {
	profile := acpWorkersTestProfile()
	draft := acpWorkerDraft{
		Model:     "  fast-model  ",
		Driver:    " codex-acp ",
		MCPBridge: "off",
		Overrides: map[string]bool{"promptImage": true, "terminal": false},
	}
	request, err := buildACPWorkerPatchRequest(profile, draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(request) != 5 {
		t.Fatalf("patch keys = %#v", request)
	}
	if request["id"] != "codex" || request["model"] != "fast-model" ||
		request["driver"] != "codex-acp" || request["mcpBridge"] != "off" {
		t.Fatalf("patch = %#v", request)
	}
	overrides, ok := request["capabilityOverrides"].(map[string]bool)
	if !ok || len(overrides) != 1 || overrides["promptImage"] != false {
		t.Fatalf("overrides = %#v", request["capabilityOverrides"])
	}
	for _, forbidden := range []string{"command", "args", "env", "url", "endpoint", "trust"} {
		if _, present := request[forbidden]; present {
			t.Fatalf("patch must not carry %q: %#v", forbidden, request)
		}
	}

	draft.MCPBridge = "  "
	cleared, err := buildACPWorkerPatchRequest(profile, draft)
	if err != nil || cleared["mcpBridge"] != "auto" {
		t.Fatalf("empty bridge must reset to auto: %#v, err=%v", cleared, err)
	}
	draft.MCPBridge = "bridged"
	if _, err := buildACPWorkerPatchRequest(profile, draft); err == nil {
		t.Fatal("unknown MCP bridge mode must be rejected before ACP")
	}
	if _, err := buildACPWorkerPatchRequest(acpWorkerProfileView{}, draft); err == nil {
		t.Fatal("empty profile id must be rejected")
	}
}

func TestACPWorkersCapabilityGatePreventsLoadAndHidesTab(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "acpWorkers"
	a.startACPWorkersPanelLoad()
	if a.acpWorkersCapable() {
		t.Fatal("manageACPProfiles must be required")
	}
	if a.acpWorkers.Loading || !a.acpWorkers.Loaded ||
		!strings.Contains(a.acpWorkers.Err, "manageACPProfiles") {
		t.Fatalf("gated load state = %+v", a.acpWorkers)
	}

	a.settingsCategory = "agent"
	a.settingsTab = "acpWorkers"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "acpWorkers" {
		t.Fatalf("acpWorkers tab must be hidden without manageACPProfiles")
	}
}

func TestACPWorkersPanelHeadlessRendersProfileEditor(t *testing.T) {
	a := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	tt := ui.NewTester(a.view, 1180, 1000)
	for _, want := range []string{
		"ACP 工作器", "Codex", "codex", "stdio", "已启用", "Router 可用",
		"默认模型", "驱动", "MCP 桥接", "保存",
		"收紧能力（勾选后禁用该能力）", "modelConfigOption", "promptImage", "terminal",
		"只编辑外部 ACP Agent 的默认模型、驱动、MCP 桥接与能力收紧",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("acp workers panel missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("需要 ACP 能力 manageACPProfiles") {
		t.Fatalf("supported panel rendered the unsupported gate: %q", tt.Texts())
	}
	for _, secret := range []string{
		"https://worker.example/rpc", "token=secret", "/opt/secret/codex",
		"API_TOKEN=should-not-leak", "/secret/settings.json",
	} {
		for _, text := range tt.Texts() {
			if strings.Contains(text, secret) {
				t.Fatalf("acp workers panel leaked %q: %q", secret, tt.Texts())
			}
		}
	}

	// The checkbox edits the tightening draft, not an argv or trust field.
	if err := tt.Click("promptImage"); err != nil {
		t.Fatalf("click capability override: %v", err)
	}
	draft := a.acpWorkers.Drafts["codex"]
	if draft == nil || draft.Overrides["promptImage"] {
		t.Fatalf("capability override draft was not tightened off: %+v", draft)
	}
	if !draft.Overrides["terminal"] {
		t.Fatalf("unrelated capability override changed: %+v", draft)
	}
}

func TestACPWorkersPanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	seq := atomic.LoadInt64(&a.acpWorkers.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.acpWorkersPanelCurrent(seq, reloadSeq) {
		t.Fatal("current acp workers request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.acpWorkersPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	b.acpWorkers.SavingID = "codex"
	b.acpWorkers.Status = "正在保存 ACP 工作器配置…"
	oldSeq := atomic.LoadInt64(&b.acpWorkers.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.acpWorkers.Seq) <= oldSeq {
		t.Fatal("leaving the acpWorkers tab must invalidate in-flight state")
	}
	if b.acpWorkers.Loaded || b.acpWorkers.SavingID != "" || b.acpWorkers.Status != "" ||
		len(b.acpWorkers.Profiles) != 0 || len(b.acpWorkers.Drafts) != 0 {
		t.Fatalf("acp workers state was not invalidated on tab exit: %+v", b.acpWorkers)
	}

	c := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	c.closeSettings()
	if c.acpWorkers.Loaded || c.acpWorkers.SavingID != "" {
		t.Fatalf("closing settings must invalidate acp workers state: %+v", c.acpWorkers)
	}

	d := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	oldSeq = atomic.LoadInt64(&d.acpWorkers.Seq)
	d.setSettingsCategory("system")
	if atomic.LoadInt64(&d.acpWorkers.Seq) <= oldSeq || d.acpWorkers.Loaded {
		t.Fatalf("changing settings category must invalidate acp workers state: %+v", d.acpWorkers)
	}

	e := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	e.acpWorkers.SavingID = "codex"
	oldSeq = atomic.LoadInt64(&e.acpWorkers.Seq)
	e.setFeatures(nil)
	e.ensureSettingsTabVisible()
	if e.settingsTab == "acpWorkers" {
		t.Fatal("capability fallback did not leave the acpWorkers tab")
	}
	if atomic.LoadInt64(&e.acpWorkers.Seq) <= oldSeq || e.acpWorkers.SavingID != "" {
		t.Fatalf("capability fallback must invalidate acp workers state: %+v", e.acpWorkers)
	}
}

func TestACPWorkersSaveGuardsAndOfflineErrors(t *testing.T) {
	a := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	a.acp = nil

	a.saveACPWorkerProfile("codex")
	if a.acpWorkers.SavingID != "" || !a.acpWorkers.StatusIsError ||
		!strings.Contains(a.acpWorkers.Status, "尚未连接") {
		t.Fatalf("offline save state = %+v", a.acpWorkers)
	}

	a.acpWorkers.Status = ""
	a.acpWorkers.StatusIsError = false
	a.acpWorkers.SavingID = "other"
	a.saveACPWorkerProfile("codex")
	if a.acpWorkers.Status != "" {
		t.Fatalf("save while another profile is saving = %+v", a.acpWorkers)
	}

	a.acpWorkers.SavingID = ""
	a.acpWorkers.Loading = true
	a.saveACPWorkerProfile("codex")
	if a.acpWorkers.Status != "" {
		t.Fatalf("save while loading = %+v", a.acpWorkers)
	}

	a.acpWorkers.Loading = false
	a.saveACPWorkerProfile("missing")
	if !a.acpWorkers.StatusIsError || !strings.Contains(a.acpWorkers.Status, "不存在") {
		t.Fatalf("missing profile save state = %+v", a.acpWorkers)
	}

	a.acpWorkers.Status = ""
	a.setFeatures(nil)
	a.saveACPWorkerProfile("codex")
	if !a.acpWorkers.StatusIsError || !strings.Contains(a.acpWorkers.Status, "manageACPProfiles") {
		t.Fatalf("incapable save state = %+v", a.acpWorkers)
	}
}

func TestACPWorkersApplyPatchedProfilesKeepsCapabilityVocabulary(t *testing.T) {
	a := acpWorkersTestApp(t, map[string]bool{"manageACPProfiles": true})
	profiles := []acpWorkerProfileView{{
		ID: "codex", Title: "Codex", Transport: "stdio", Enabled: true,
		Model: "patched-model", Driver: "generic-acp", MCPBridge: "auto",
	}}
	a.applyACPWorkerProfiles(profiles)
	if len(a.acpWorkers.CapabilityNames) != 3 {
		t.Fatalf("capability names were dropped: %+v", a.acpWorkers)
	}
	draft, ok := a.acpWorkerDraft("codex")
	if !ok || draft.Model != "patched-model" || draft.MCPBridge != "auto" || len(draft.Overrides) != 0 {
		t.Fatalf("patched draft = %+v, ok=%v", draft, ok)
	}
}
