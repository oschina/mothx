package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/tui/components/suggest"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func commandSuggestionItems(translators ...i18n.Translator) []suggest.Item {
	tr := i18n.New(i18n.LanguageEN)
	if len(translators) > 0 {
		tr = translators[0]
	}
	items := make([]suggest.Item, 0, len(commandSpecs))
	for _, spec := range commandSpecs {
		items = append(items, suggest.Item{
			Label:       spec.Name,
			Value:       spec.Value,
			Description: tr.Text(spec.Description),
		})
	}
	return items
}

func (a *App) updateCommandSuggestions() {
	value := a.input.Value()
	items, query, ok := commandSuggestionItemsForInputWithTranslator(value, a.translator)
	if a.auth.Open || a.envDialog.Open || a.defaultModelDialog.Open || a.modelDialog.Open || a.sessionsDialog.Open || a.toolModalOpen || a.statsOverlayOpen || a.skillHubOpen || a.esmPanelOpen || a.waitingForApproval || a.waitingForQuestion || !ok {
		a.suggest = a.suggest.SetItems(commandSuggestionItems(a.translator))
		a.suggest = a.suggest.Update("")
		return
	}
	a.suggest = a.suggest.SetItems(items)
	a.suggest = a.suggest.Update(query)
}

func (a *App) commandSuggestionsVisible() bool {
	return a.suggest.Visible()
}

func (a *App) commandInputActive() bool {
	value := a.input.Value()
	return strings.HasPrefix(value, "/") && !strings.Contains(value, "\n")
}

func (a *App) commandNameInputActive() bool {
	value := a.input.Value()
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, " \t\n")
}

func (a *App) applySelectedCommandSuggestion() bool {
	item, ok := a.suggest.Selected()
	if !ok {
		return false
	}
	if item.Value == a.input.Value() {
		return false
	}
	a.input = a.input.SetValue(item.Value)
	a.input = a.input.CursorEnd()
	a.updateCommandSuggestions()
	a.scheduleRender()
	return true
}

func (a *App) handleCommandSuggestionKey(msg tea.KeyMsg) bool {
	if !a.commandSuggestionsVisible() {
		return false
	}
	switch msg.Type {
	case tea.KeyUp:
		a.suggest = a.suggest.CursorUp()
		a.scheduleRender()
		return true
	case tea.KeyDown:
		a.suggest = a.suggest.CursorDown()
		a.scheduleRender()
		return true
	case tea.KeyTab:
		return a.applySelectedCommandSuggestion()
	}
	return false
}

func commandSuggestionItemsForInput(value string) ([]suggest.Item, string, bool) {
	return commandSuggestionItemsForInputWithTranslator(value, i18n.New(i18n.LanguageEN))
}

func commandSuggestionItemsForInputWithTranslator(value string, tr i18n.Translator) ([]suggest.Item, string, bool) {
	if !strings.HasPrefix(value, "/") || strings.Contains(value, "\n") {
		return nil, "", false
	}
	if !strings.ContainsAny(value, " \t") {
		return commandSuggestionItems(tr), value, true
	}

	items := commandArgumentSuggestionItems(value)
	if len(items) == 0 {
		return nil, "", false
	}
	return items, value, true
}

func commandArgumentSuggestionItems(value string) []suggest.Item {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return nil
	}
	cmd := fields[0]
	argIndex := len(fields) - 1
	if strings.HasSuffix(value, " ") || strings.HasSuffix(value, "\t") {
		argIndex = len(fields)
	}
	if argIndex < 1 {
		argIndex = 1
	}

	switch cmd {
	case "/esm":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"edit", "pause", "resume", "clear", "guide"})
		}
	case "/mode":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"plan", "agent", "yolo", "os"})
		}
	case "/defaultModel":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"project", "global"})
		}
	case "/sessions":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"ls", "set", "clear", "del"})
		}
	case "/expert":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"list", "show", "bind", "unbind", "switch"})
		}
	case "/delegate":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"on", "off", "status"})
		}
	case "/browser":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"on", "off", "status"})
		}
	case "/stats":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"server", "stop-server", "tui"})
		}
	case "/alloweditpath":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"add", "remove", "clear"})
		}
	case "/allowautoedit":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"on", "off"})
		}
		if argIndex == 2 && len(fields) >= 2 && (fields[1] == "on" || fields[1] == "off") {
			return commandArgumentItems(cmd+" "+fields[1], []string{"global"})
		}
	case "/statusline":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"status", "on", "off", "command", "refresh"})
		}
		if argIndex == 2 && len(fields) >= 2 && (fields[1] == "on" || fields[1] == "off") {
			return commandArgumentItems(cmd+" "+fields[1], []string{"project", "global"})
		}
	case "/tuilang":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"global", "project", "auto", "zh", "en"})
		}
		if argIndex == 2 && len(fields) >= 2 && (fields[1] == "global" || fields[1] == "project") {
			return commandArgumentItems(cmd+" "+fields[1], []string{"auto", "zh", "en"})
		}
	case "/agent":
		if argIndex == 1 {
			return commandArgumentItems(cmd, []string{"list", "switch", "destroy"})
		}
	}

	return nil
}

func commandArgumentItems(prefix string, args []string) []suggest.Item {
	items := make([]suggest.Item, 0, len(args))
	for _, arg := range args {
		value := prefix + " " + arg
		items = append(items, suggest.Item{
			Label: value,
			Value: value,
		})
	}
	return items
}
