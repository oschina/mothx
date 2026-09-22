package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tui/components/editor"
)

type envDialogState struct {
	Open      bool
	Cursor    int
	Editing   string
	InputMode string // key or value
	Vars      map[string]string
	Error     string
}

func (a *App) openEnvDialog() {
	if a.isThinking {
		a.addCommandError("Cannot open /env while the agent is running.")
		return
	}
	a.envDialog = envDialogState{Open: true, Vars: config.LoadEnv().List()}
	a.input = a.input.Blur()
	a.scheduleRender()
}

func (a *App) closeEnvDialog() {
	a.envDialog = envDialogState{}
	a.envInput = editor.Model{}
	a.input = a.input.Focus()
	a.scheduleRender()
}

func (a *App) envKeys() []string {
	keys := make([]string, 0, len(a.envDialog.Vars))
	for key := range a.envDialog.Vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (a *App) startEnvInput(mode, key, value string) {
	a.envDialog.InputMode, a.envDialog.Editing, a.envDialog.Error = mode, key, ""
	placeholder := "environment variable name"
	if mode == "value" {
		placeholder = "value"
	}
	a.envInput = editor.New(max(20, a.width-8)).SetPlaceholder(placeholder).SetMaxLines(1).SetValue(value).Focus()
	a.scheduleRender()
}

func (a *App) handleEnvKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !a.envDialog.Open {
		return false, nil
	}
	if a.envDialog.InputMode != "" {
		switch msg.Type {
		case tea.KeyEsc:
			a.envDialog.InputMode = ""
			a.envDialog.Error = ""
			a.scheduleRender()
			return true, nil
		case tea.KeyEnter:
			value := a.envInput.Value()
			if a.envDialog.InputMode == "key" {
				key := strings.TrimSpace(value)
				if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
					a.envDialog.Error = "Invalid environment variable name"
					return true, nil
				}
				a.envDialog.Vars[key] = ""
				a.startEnvInput("value", key, "")
			} else {
				a.envDialog.Vars[a.envDialog.Editing] = value
				a.envDialog.InputMode, a.envDialog.Editing = "", ""
				a.scheduleRender()
			}
			return true, nil
		default:
			var cmd tea.Cmd
			a.envInput, cmd = a.envInput.Update(msg)
			a.scheduleRender()
			return true, cmd
		}
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		a.closeEnvDialog()
		return true, nil
	case tea.KeyEsc:
		a.closeEnvDialog()
		return true, nil
	case tea.KeyUp:
		a.moveEnvCursor(-1)
	case tea.KeyDown:
		a.moveEnvCursor(1)
	case tea.KeyBackspace, tea.KeyDelete:
		keys := a.envKeys()
		if a.envDialog.Cursor < len(keys) {
			delete(a.envDialog.Vars, keys[a.envDialog.Cursor])
			a.moveEnvCursor(0)
		}
	case tea.KeyEnter:
		keys := a.envKeys()
		switch {
		case a.envDialog.Cursor < len(keys):
			key := keys[a.envDialog.Cursor]
			a.startEnvInput("value", key, a.envDialog.Vars[key])
		case a.envDialog.Cursor == len(keys):
			a.startEnvInput("key", "", "")
		default:
			if err := (&config.EnvConfig{Vars: a.envDialog.Vars}).Save(); err != nil {
				a.envDialog.Error = "Save env: " + err.Error()
				return true, nil
			}
			a.closeEnvDialog()
		}
	}
	return true, nil
}

func (a *App) moveEnvCursor(delta int) {
	max := len(a.envKeys()) + 1
	a.envDialog.Cursor += delta
	if a.envDialog.Cursor < 0 {
		a.envDialog.Cursor = max
	}
	if a.envDialog.Cursor > max {
		a.envDialog.Cursor = 0
	}
	a.scheduleRender()
}

func (a *App) renderEnvDialog() string {
	if !a.envDialog.Open {
		return ""
	}
	keys := a.envKeys()
	lines := []string{"Environment Variables", ""}
	if a.envDialog.InputMode != "" {
		prompt := "Variable name:"
		if a.envDialog.InputMode == "value" {
			prompt = "Value for " + a.envDialog.Editing + ":"
		}
		lines = append(lines, prompt, a.envInput.View(), "", statusStyle.Render("Enter to confirm, Esc to cancel"))
	} else {
		for i, key := range keys {
			prefix := "  "
			if i == a.envDialog.Cursor {
				prefix = "▸ "
			}
			lines = append(lines, prefix+key+" = "+a.envDialog.Vars[key])
		}
		prefix := "  "
		if a.envDialog.Cursor == len(keys) {
			prefix = "▸ "
		}
		lines = append(lines, prefix+"+ Add Variable")
		prefix = "  "
		if a.envDialog.Cursor == len(keys)+1 {
			prefix = "▸ "
		}
		lines = append(lines, prefix+"✓ Done")
		lines = append(lines, "", statusStyle.Render("Enter edit/select · Backspace delete · Esc cancel"))
	}
	if a.envDialog.Error != "" {
		lines = append(lines, "", errorStyle.Render(a.envDialog.Error))
	}
	width := a.width - 4
	if width < 50 {
		width = 50
	}
	if width > 100 {
		width = 100
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(1, 2).Width(width).Render(strings.Join(lines, "\n"))
}
