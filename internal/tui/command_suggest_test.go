package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/tui/i18n"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCommandSuggestionsForSlash(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/au")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected command suggestions to be visible")
	}
	if !a.applySelectedCommandSuggestion() {
		t.Fatal("expected selected command suggestion to apply")
	}
	if got := a.input.Value(); got != "/auth" {
		t.Fatalf("input = %q, want /auth", got)
	}
}

func TestCommandSuggestionsForBareSlash(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected command suggestions to be visible for bare slash")
	}
	item, ok := a.suggest.Selected()
	if !ok {
		t.Fatal("expected selected command suggestion")
	}
	if item.Label == "" || item.Label[0] != '/' {
		t.Fatalf("selected item = %#v, want slash command", item)
	}
}

func TestCommandSuggestionsHiddenAfterSpace(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/mode ")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected argument suggestions after space")
	}
}

func TestCommandSuggestionContinuesToArgumentSuggestions(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/mo")
	a.updateCommandSuggestions()
	if !a.applySelectedCommandSuggestion() {
		t.Fatal("expected selected command suggestion to apply")
	}
	if got := a.input.Value(); got != "/mode " {
		t.Fatalf("input = %q, want /mode ", got)
	}
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected argument suggestions to remain visible")
	}
	item, ok := a.suggest.Selected()
	if !ok {
		t.Fatal("expected selected argument suggestion")
	}
	if item.Value != "/mode plan" {
		t.Fatalf("selected value = %q, want /mode plan", item.Value)
	}
}

func TestEnterSubmitsExactCommandSuggestion(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/clear")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected command suggestions to be visible")
	}

	a.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := a.input.Value(); got != "" {
		t.Fatalf("input = %q, want empty after submit", got)
	}
	if a.commandSuggestionsVisible() {
		t.Fatal("expected suggestions hidden after submit")
	}
	if len(a.messages) == 0 || !strings.Contains(stripANSI(a.messages[len(a.messages)-1]), a.translator.Text(i18n.MsgConversationCleared)) {
		t.Fatalf("expected /clear to execute, messages = %#v", a.messages)
	}
}

func TestCommandSuggestionEnterFlushesQueuedInputBeforeApplying(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/mo")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected command suggestions to be visible before queued input")
	}

	a.queueInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("de ")})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := a.input.Value(); got != "" {
		t.Fatalf("input = %q, want command to execute after queued input flush", got)
	}
	if a.commandSuggestionsVisible() {
		t.Fatal("expected suggestions hidden after queued input adds a space")
	}
}

func TestCommandArgumentSuggestionForMode(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/mode a")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected mode argument suggestions to be visible")
	}
	if !a.applySelectedCommandSuggestion() {
		t.Fatal("expected selected mode argument suggestion to apply")
	}
	if got := a.input.Value(); got != "/mode agent" {
		t.Fatalf("input = %q, want /mode agent", got)
	}
}

func TestCommandArgumentSuggestionForStats(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/stats s")
	a.updateCommandSuggestions()
	if !a.commandSuggestionsVisible() {
		t.Fatal("expected stats argument suggestions to be visible")
	}
	if !a.applySelectedCommandSuggestion() {
		t.Fatal("expected selected stats argument suggestion to apply")
	}
	if got := a.input.Value(); got != "/stats server" {
		t.Fatalf("input = %q, want /stats server", got)
	}
}

func TestTabCompletesCommandArgumentInsteadOfCyclingMode(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/mode ")
	a.updateCommandSuggestions()

	a.Update(tea.KeyMsg{Type: tea.KeyTab})

	if got := a.mode; got != "agent" {
		t.Fatalf("mode = %q, want agent", got)
	}
	if got := a.input.Value(); got != "/mode plan" {
		t.Fatalf("input = %q, want /mode plan", got)
	}
}

func TestTabInSlashCommandWithoutArgumentSuggestionDoesNotCycleMode(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.input = a.input.SetValue("/skill ")
	a.updateCommandSuggestions()

	a.Update(tea.KeyMsg{Type: tea.KeyTab})

	if got := a.mode; got != "agent" {
		t.Fatalf("mode = %q, want agent", got)
	}
	if got := a.input.Value(); got != "/skill " {
		t.Fatalf("input = %q, want /skill ", got)
	}
}

func TestAgentCommandSuggestionIsMultiAgentNotModeShortcut(t *testing.T) {
	items, query, ok := commandSuggestionItemsForInput("/agent")
	if !ok {
		t.Fatal("expected command suggestions for /agent")
	}
	if query != "/agent" {
		t.Fatalf("query = %q, want /agent", query)
	}
	for _, item := range items {
		if item.Label == "/agent" {
			if item.Value != "/agent " {
				t.Fatalf("/agent suggestion value = %q, want /agent ", item.Value)
			}
			if item.Description == "" || item.Description == "Switch or show execution mode (plan/agent/yolo/os)" {
				t.Fatalf("/agent description = %q, want multi-agent description", item.Description)
			}
			return
		}
	}
	t.Fatal("missing /agent suggestion")
}
