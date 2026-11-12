package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDraftConfigSelectionStaysLocal(t *testing.T) {
	a := testApp()
	a.configOptions = []configOption{
		{ID: "model", Name: "Model", CurrentValue: "a", Options: []configChoice{{Value: "a", Name: "A"}, {Value: "b", Name: "B"}}},
		{ID: "mode", Name: "Mode", CurrentValue: "agent", Options: []configChoice{{Value: "agent", Name: "Agent"}, {Value: "plan", Name: "Plan"}}},
		{ID: "thinking_level", Name: "Thinking", CurrentValue: "off", Options: []configChoice{{Value: "off", Name: "Off"}, {Value: "high", Name: "High"}}},
	}
	a.setConfigOption("model", "b")
	a.setConfigOption("mode", "plan")
	a.setConfigOption("thinking_level", "high")
	if a.currentSession != "" {
		t.Fatalf("draft selection must not create a session: %q", a.currentSession)
	}
	if a.draftConfig["model"] != "b" || a.draftConfig["mode"] != "plan" || a.draftConfig["thinking_level"] != "high" {
		t.Fatalf("draft config = %#v", a.draftConfig)
	}
	if got := a.configOptionByID("model"); got == nil || got.CurrentValue != "b" {
		t.Fatalf("model option = %+v", got)
	}
	if a.mode != "plan" || a.currentModeValue() != "plan" || a.modeLabel() != "Plan" {
		t.Fatalf("mode state = %q current %q label %q", a.mode, a.currentModeValue(), a.modeLabel())
	}
	if a.modelLabel != "B" || a.thinkingLabel != "High" {
		t.Fatalf("labels = model %q thinking %q", a.modelLabel, a.thinkingLabel)
	}
}

func TestModeChoicesFallBackToCanonicalModes(t *testing.T) {
	a := testApp()
	a.configOptions = nil
	choices := a.modeChoices()
	if len(choices) != 4 {
		t.Fatalf("fallback mode choices = %+v", choices)
	}
	if a.currentModeValue() != "yolo" || a.modeLabel() != "Yolo" {
		t.Fatalf("default mode = %q label %q", a.currentModeValue(), a.modeLabel())
	}
	a.mode = "plan"
	if a.modeLabel() != "Plan" {
		t.Fatalf("mode label = %q", a.modeLabel())
	}
	a.availableModes = []sessionMode{{ID: "agent", Name: "Agent"}, {ID: "os", Name: "OS"}}
	choices = a.modeChoices()
	if len(choices) != 2 || choices[1].Value != "os" {
		t.Fatalf("available modes projection = %+v", choices)
	}
}

func TestSessionMenuResetsDraftConfigForExistingSession(t *testing.T) {
	a := testApp()
	a.draftConfig = map[string]string{"model": "stale"}
	session := listedSession{SessionID: "s1", Cwd: "/work"}
	a.sessions = []listedSession{session}
	a.selectSession(session)
	if len(a.draftConfig) != 0 {
		t.Fatalf("selecting a session kept draft config: %#v", a.draftConfig)
	}
}

func TestInsertComposerTextPrependsCommandOnce(t *testing.T) {
	a := testApp()
	a.draft = "help"
	a.insertComposerText("/review ")
	if a.draft != "/review help" {
		t.Fatalf("draft = %q", a.draft)
	}
	a.insertComposerText("")
	if a.draft != "/review help" {
		t.Fatalf("empty insertion changed draft: %q", a.draft)
	}
}

func TestExpertChoiceLabelsFollowUILocale(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	choice := configChoice{
		Value: "software-company",
		Name:  "Software Company", NameZh: "软件公司", NameEn: "Software Company",
		Description: "Expert team", DescriptionZh: "主角团", DescriptionEn: "Expert team",
	}
	if got := localizedChoiceName(choice); got != "软件公司" {
		t.Fatalf("zh expert name = %q", got)
	}
	if got := localizedChoiceDescription(choice); got != "主角团" {
		t.Fatalf("zh expert description = %q", got)
	}

	setLocale("en")
	if got := localizedChoiceName(choice); got != "Software Company" {
		t.Fatalf("en expert name = %q", got)
	}
	if got := localizedChoiceDescription(choice); got != "Expert team" {
		t.Fatalf("en expert description = %q", got)
	}

	// Older runtimes only carry the canonical fields; they must still render.
	legacy := configChoice{Value: "legacy", Name: "Legacy"}
	if got := localizedChoiceName(legacy); got != "Legacy" {
		t.Fatalf("legacy expert name = %q", got)
	}
}

func TestExpertDisplayNameFollowsUILocale(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	item := expertSummary{
		Name:        "software-company",
		DisplayName: expertLocalizedText{Zh: "软件公司", En: "Software Company"},
	}
	if got := expertDisplayName(item); got != "软件公司" {
		t.Fatalf("zh expert display name = %q", got)
	}
	setLocale("en")
	if got := expertDisplayName(item); got != "Software Company" {
		t.Fatalf("en expert display name = %q", got)
	}
	fallback := expertSummary{Name: "fallback"}
	if got := expertDisplayName(fallback); got != "fallback" {
		t.Fatalf("fallback expert display name = %q", got)
	}
}

func TestChatExpertMenuLabelFollowsUILocale(t *testing.T) {
	setLocale("zh")
	t.Cleanup(func() { setLocale("zh") })
	a := testApp()
	a.configOptions = []configOption{{
		ID: "expert", Name: "Expert", CurrentValue: "team-1",
		Options: []configChoice{{
			Value: "team-1", Name: "Team One", NameZh: "一号主角团", NameEn: "Team One",
		}},
	}}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("一号主角团") {
		t.Fatalf("zh expert menu label missing: %q", tt.Texts())
	}

	setLocale("en")
	tt = ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Team One") {
		t.Fatalf("en expert menu label missing: %q", tt.Texts())
	}
	if tt.HasText("一号主角团") {
		t.Fatalf("zh expert menu label leaked into en mode: %q", tt.Texts())
	}
}
