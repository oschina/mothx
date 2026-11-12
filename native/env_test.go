package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func envTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{"manageEnv": true})
	a.settingsOpen = true
	a.settingsCategory = "agent"
	a.settingsTab = "env"
	a.ensureSettingsTabVisible()
	return a
}

func TestEnvViewIsValueFree(t *testing.T) {
	raw := json.RawMessage(`{"variables":[{"name":"ZED_TOKEN","valueConfigured":true},{"name":"ALPHA_KEY","valueConfigured":true}]}`)
	var view envView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	a := envTestApp(t)
	a.applyEnvView(&view)

	names := a.env.visibleEnvNames()
	if len(names) != 2 || names[0] != "ALPHA_KEY" || names[1] != "ZED_TOKEN" {
		t.Fatalf("sorted names = %#v", names)
	}
	for _, name := range names {
		draft := a.env.Drafts[name]
		if draft == nil {
			t.Fatalf("missing draft for %s", name)
		}
		if draft.Value != "" || draft.Touched || draft.IsNew || draft.Deleted {
			t.Fatalf("loaded draft must start empty and untouched: %#v", *draft)
		}
	}
	if !a.env.Loaded || a.env.Loading {
		t.Fatalf("loaded state = %+v", a.env)
	}

	// Rendering the loaded panel must never contain a server-side value: the
	// response carries none, and the draft stays empty.
	tt := ui.NewTester(a.view, 1180, 900)
	for _, text := range tt.Texts() {
		if strings.Contains(text, "ZED_TOKEN_VALUE") || strings.Contains(text, "ALPHA_KEY_VALUE") {
			t.Fatalf("env panel rendered a configured value: %q", text)
		}
	}
}

func TestEnvAddRemoveAndPatch(t *testing.T) {
	a := envTestApp(t)
	a.applyEnvView(&envView{Variables: []envVariableView{
		{Name: "API_TOKEN", ValueConfigured: true},
		{Name: "EXISTING", ValueConfigured: true},
	}})

	a.env.NewName = "  NEW_SECRET "
	a.env.NewValue = "s3cret"
	a.addEnvVariable()
	if a.env.NewName != "" || a.env.NewValue != "" {
		t.Fatalf("add must clear the pending fields: %q %q", a.env.NewName, a.env.NewValue)
	}
	draft := a.env.Drafts["NEW_SECRET"]
	if draft == nil || !draft.IsNew || !draft.Touched || draft.Value != "s3cret" {
		t.Fatalf("staged new draft = %#v", draft)
	}

	// Editing a configured value stages a replacement; untouched values are
	// never sent so a stored secret cannot be overwritten by an empty string.
	a.env.Drafts["EXISTING"].Value = "replacement"
	a.env.Drafts["EXISTING"].Touched = true

	// A saved name is marked for unset; a staged name is dropped locally.
	a.removeEnvVariable("API_TOKEN")
	if !a.env.Drafts["API_TOKEN"].Deleted {
		t.Fatalf("saved name must be staged for unset: %#v", a.env.Drafts["API_TOKEN"])
	}
	a.removeEnvVariable("NEW_SECRET")
	if _, ok := a.env.Drafts["NEW_SECRET"]; ok {
		t.Fatalf("staged new name must be dropped before save")
	}

	request, err := buildEnvPatchRequest(a.env.Drafts)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := request["set"].([]map[string]string)
	unset, _ := request["unset"].([]string)
	if len(set) != 1 || set[0]["name"] != "EXISTING" || set[0]["value"] != "replacement" {
		t.Fatalf("set = %#v", set)
	}
	if len(unset) != 1 || unset[0] != "API_TOKEN" {
		t.Fatalf("unset = %#v", unset)
	}
}

func TestEnvPatchNoopAndDuplicateGuard(t *testing.T) {
	a := envTestApp(t)
	a.applyEnvView(&envView{Variables: []envVariableView{{Name: "KEEP", ValueConfigured: true}}})
	request, err := buildEnvPatchRequest(a.env.Drafts)
	if err != nil {
		t.Fatal(err)
	}
	if request != nil {
		t.Fatalf("unchanged drafts must produce no patch: %#v", request)
	}

	a.env.NewName = "KEEP"
	a.env.NewValue = "x"
	a.addEnvVariable()
	if a.env.Drafts["KEEP"].IsNew || a.env.Drafts["KEEP"].Value != "" {
		t.Fatalf("re-adding a saved name must not stage a duplicate: %#v", a.env.Drafts["KEEP"])
	}
	if !a.env.StatusIsError || !strings.Contains(a.env.Status, "已存在") {
		t.Fatalf("duplicate status = %q (error=%v)", a.env.Status, a.env.StatusIsError)
	}
	if _, exists := a.env.Drafts["KEEP_2"]; exists {
		t.Fatalf("duplicate add invented a variable")
	}
}

func TestEnvDraftsAreInvalidatedOnTabExit(t *testing.T) {
	a := envTestApp(t)
	a.applyEnvView(&envView{Variables: []envVariableView{{Name: "KEEP", ValueConfigured: true}}})
	a.env.Drafts["KEEP"] = &envDraft{Value: "typed-secret", Touched: true}
	a.setSettingsCategory("appearance")
	if a.settingsTab == "env" {
		t.Fatalf("category did not change")
	}
	if len(a.env.Drafts) != 0 || a.env.Loaded {
		t.Fatalf("leaving the env tab must drop draft secrets: %#v", a.env.Drafts)
	}
	if a.settingsCategory != "appearance" {
		t.Fatalf("settings category = %q", a.settingsCategory)
	}
}
