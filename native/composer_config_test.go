package main

import "testing"

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
