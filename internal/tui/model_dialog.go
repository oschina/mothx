package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/oschina/mothx/internal/tui/components/editor"
)

type modelDialogState struct {
	Open     bool
	Search   string
	Cursor   int
	Filtered []string
	Current  string
}

var modelDialogStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("63")).
	Padding(1, 2)

func (a *App) openModelDialog() {
	ids := a.modelIDs()
	cur := ""
	if a.model != nil {
		cur = a.model.ID
	}
	a.modelDialog = modelDialogState{
		Open:     true,
		Filtered: ids,
		Current:  cur,
	}
	a.modelInput = editor.New(max(20, a.width-8)).SetPlaceholder("filter models...").SetMaxLines(1)
	a.input = a.input.Blur()
	a.modelInput = a.modelInput.Focus()
	a.scheduleRender()
}

func (a *App) closeModelDialog() {
	a.modelDialog = modelDialogState{}
	a.input = a.input.Focus()
	a.scheduleRender()
}

func (a *App) modelIDs() []string {
	var ids []string
	for _, m := range a.provider.Models() {
		ids = append(ids, m.ID)
	}
	return ids
}

func (a *App) filterModelOptions() {
	q := strings.TrimSpace(strings.ToLower(a.modelDialog.Search))
	ids := a.modelIDs()
	if q == "" {
		a.modelDialog.Filtered = ids
	} else {
		var filtered []string
		for _, id := range ids {
			if strings.Contains(strings.ToLower(id), q) {
				filtered = append(filtered, id)
			}
		}
		a.modelDialog.Filtered = filtered
	}
	if a.modelDialog.Cursor >= len(a.modelDialog.Filtered) {
		a.modelDialog.Cursor = max(0, len(a.modelDialog.Filtered)-1)
	}
}

func (a *App) moveModelCursor(delta int) {
	if len(a.modelDialog.Filtered) == 0 {
		return
	}
	a.modelDialog.Cursor += delta
	if a.modelDialog.Cursor < 0 {
		a.modelDialog.Cursor = len(a.modelDialog.Filtered) - 1
	}
	if a.modelDialog.Cursor >= len(a.modelDialog.Filtered) {
		a.modelDialog.Cursor = 0
	}
	a.scheduleRender()
}

func (a *App) confirmModelDialog() {
	if len(a.modelDialog.Filtered) == 0 {
		return
	}
	id := a.modelDialog.Filtered[a.modelDialog.Cursor]
	newModel := a.provider.GetModel(id)
	if newModel == nil {
		return
	}
	a.model = newModel
	a.syncAgentManagerRuntime()
	a.resetAgent(fmt.Errorf("model changed"))
	a.addCommandStatus(fmt.Sprintf("✅ Model switched to: %s (%s)", newModel.Name, newModel.ID))
	a.closeModelDialog()
}

func (a *App) handleModelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !a.modelDialog.Open {
		return false, nil
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		a.closeModelDialog()
		return true, nil
	case tea.KeyEsc:
		a.closeModelDialog()
		return true, nil
	case tea.KeyUp:
		a.moveModelCursor(-1)
		return true, nil
	case tea.KeyDown:
		a.moveModelCursor(1)
		return true, nil
	case tea.KeyEnter:
		a.confirmModelDialog()
		return true, nil
	default:
		var cmd tea.Cmd
		a.modelInput, cmd = a.modelInput.Update(msg)
		a.modelDialog.Search = a.modelInput.Value()
		a.filterModelOptions()
		return true, cmd
	}
}

func (a *App) renderModelDialog() string {
	if !a.modelDialog.Open {
		return ""
	}
	width := a.width - 4
	if width < 50 {
		width = 50
	}
	if width > 100 {
		width = 100
	}

	var lines []string
	lines = append(lines, "Switch Model")
	lines = append(lines, "")
	lines = append(lines, a.modelInput.View())
	lines = append(lines, "")

	if len(a.modelDialog.Filtered) == 0 {
		lines = append(lines, "No models match.")
	} else {
		limit := 10
		start, end := authVisibleRange(a.modelDialog.Cursor, len(a.modelDialog.Filtered), limit)
		for i := start; i < end; i++ {
			id := a.modelDialog.Filtered[i]
			cursor := "  "
			style := lipgloss.NewStyle()
			if i == a.modelDialog.Cursor {
				cursor = "› "
				style = style.Foreground(lipgloss.Color("86")).Bold(true)
			}
			marker := "  "
			if id == a.modelDialog.Current {
				marker = "* "
			}
			lines = append(lines, style.Render(cursor+marker+id))
		}
		if len(a.modelDialog.Filtered) > limit {
			lines = append(lines, "", statusStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(a.modelDialog.Filtered))))
		}
	}

	lines = append(lines, "")
	lines = append(lines, "Enter to switch, ↑↓ to navigate, Esc to close")

	return modelDialogStyle.Width(width).Render(strings.Join(lines, "\n"))
}
