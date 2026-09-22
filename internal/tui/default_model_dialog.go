package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oschina/mothx/internal/config"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/tui/components/editor"
	"github.com/oschina/mothx/internal/tui/i18n"
)

type defaultModelView int

const (
	defaultModelViewProvider defaultModelView = iota
	defaultModelViewModel
)

type defaultModelDialogState struct {
	Open bool

	Scope      string
	View       defaultModelView
	ProviderID string
	Search     string
	Cursor     int
	Filtered   []string
	Error      string
}

var defaultModelDialogStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("63")).
	Padding(1, 2)

func (a *App) openDefaultModelDialog(scope string) {
	if scope == "" {
		scope = "global"
	}
	a.defaultModelDialog = defaultModelDialogState{
		Open:     true,
		Scope:    scope,
		View:     defaultModelViewProvider,
		Filtered: defaultModelProviderIDs(a.settings),
	}
	a.modelInput = editor.New(max(20, a.width-8)).SetPlaceholder("filter providers...").SetMaxLines(1)
	a.input = a.input.Blur()
	a.modelInput = a.modelInput.Focus()
	a.scheduleRender()
}

func (a *App) closeDefaultModelDialog() {
	a.defaultModelDialog = defaultModelDialogState{}
	a.input = a.input.Focus()
	a.scheduleRender()
}

func defaultModelProviderIDs(settings *config.Settings) []string {
	return sortedAuthProviderIDs(settings)
}

// defaultModelModelIDs lists models with the same factory-resolved catalog
// logic the /model dialog uses (built-in presets merged with settings
// overrides), so both dialogs always offer one canonical model list.
func (a *App) defaultModelModelIDs(providerID string) []string {
	models := providerfactory.ResolvedModels(a.settings, providerID)
	ids := make([]string, 0, len(models))
	for _, m := range models {
		if m != nil && m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids
}

func (a *App) filterDefaultModelOptions() {
	q := strings.TrimSpace(strings.ToLower(a.defaultModelDialog.Search))
	var ids []string
	if a.defaultModelDialog.View == defaultModelViewProvider {
		ids = defaultModelProviderIDs(a.settings)
	} else {
		ids = a.defaultModelModelIDs(a.defaultModelDialog.ProviderID)
	}
	if q != "" {
		filtered := make([]string, 0, len(ids))
		for _, id := range ids {
			if strings.Contains(strings.ToLower(id), q) {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	a.defaultModelDialog.Filtered = ids
	if a.defaultModelDialog.Cursor >= len(ids) {
		a.defaultModelDialog.Cursor = max(0, len(ids)-1)
	}
}

func (a *App) moveDefaultModelCursor(delta int) {
	if len(a.defaultModelDialog.Filtered) == 0 {
		return
	}
	a.defaultModelDialog.Cursor += delta
	if a.defaultModelDialog.Cursor < 0 {
		a.defaultModelDialog.Cursor = len(a.defaultModelDialog.Filtered) - 1
	}
	if a.defaultModelDialog.Cursor >= len(a.defaultModelDialog.Filtered) {
		a.defaultModelDialog.Cursor = 0
	}
	a.scheduleRender()
}

func (a *App) confirmDefaultModelDialog() {
	if len(a.defaultModelDialog.Filtered) == 0 {
		return
	}
	id := a.defaultModelDialog.Filtered[a.defaultModelDialog.Cursor]
	switch a.defaultModelDialog.View {
	case defaultModelViewProvider:
		a.defaultModelDialog.ProviderID = id
		a.defaultModelDialog.View = defaultModelViewModel
		a.defaultModelDialog.Cursor = 0
		a.defaultModelDialog.Search = ""
		a.defaultModelDialog.Error = ""
		a.defaultModelDialog.Filtered = a.defaultModelModelIDs(id)
		a.modelInput = editor.New(max(20, a.width-8)).SetPlaceholder("filter models...").SetMaxLines(1).Focus()
		a.scheduleRender()
	case defaultModelViewModel:
		a.saveDefaultModel(a.defaultModelDialog.Scope, a.defaultModelDialog.ProviderID, id)
	}
}

func (a *App) handleDefaultModelKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !a.defaultModelDialog.Open {
		return false, nil
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		a.closeDefaultModelDialog()
		return true, nil
	case tea.KeyEsc:
		if a.defaultModelDialog.View == defaultModelViewModel {
			a.defaultModelDialog.View = defaultModelViewProvider
			a.defaultModelDialog.Cursor = 0
			a.defaultModelDialog.Search = ""
			a.defaultModelDialog.Error = ""
			a.defaultModelDialog.Filtered = defaultModelProviderIDs(a.settings)
			a.modelInput = editor.New(max(20, a.width-8)).SetPlaceholder("filter providers...").SetMaxLines(1).Focus()
			a.scheduleRender()
			return true, nil
		}
		a.closeDefaultModelDialog()
		return true, nil
	case tea.KeyUp:
		a.moveDefaultModelCursor(-1)
		return true, nil
	case tea.KeyDown:
		a.moveDefaultModelCursor(1)
		return true, nil
	case tea.KeyEnter:
		a.confirmDefaultModelDialog()
		return true, nil
	default:
		var cmd tea.Cmd
		a.modelInput, cmd = a.modelInput.Update(msg)
		a.defaultModelDialog.Search = a.modelInput.Value()
		a.filterDefaultModelOptions()
		a.scheduleRender()
		return true, cmd
	}
}

func (a *App) renderDefaultModelDialog() string {
	if !a.defaultModelDialog.Open {
		return ""
	}
	width := a.width - 4
	if width < 50 {
		width = 50
	}
	if width > 100 {
		width = 100
	}

	title := a.translator.Text(i18n.MsgDefaultModelTitle, a.defaultModelDialog.Scope)
	if a.defaultModelDialog.View == defaultModelViewModel {
		title += " · " + a.defaultModelDialog.ProviderID
	}

	var lines []string
	lines = append(lines, title, "", a.modelInput.View(), "")
	if len(a.defaultModelDialog.Filtered) == 0 {
		lines = append(lines, a.translator.Text(i18n.MsgDefaultModelNoMatch))
	} else {
		limit := 10
		start, end := authVisibleRange(a.defaultModelDialog.Cursor, len(a.defaultModelDialog.Filtered), limit)
		for i := start; i < end; i++ {
			id := a.defaultModelDialog.Filtered[i]
			cursor := "  "
			style := lipgloss.NewStyle()
			if i == a.defaultModelDialog.Cursor {
				cursor = "› "
				style = style.Foreground(lipgloss.Color("86")).Bold(true)
			}
			marker := "  "
			if a.defaultModelDialog.View == defaultModelViewProvider && id == a.settings.DefaultProvider {
				marker = "* "
			}
			if a.defaultModelDialog.View == defaultModelViewModel && id == a.settings.DefaultModel {
				marker = "* "
			}
			lines = append(lines, style.Render(cursor+marker+id))
		}
		if len(a.defaultModelDialog.Filtered) > limit {
			lines = append(lines, "", statusStyle.Render(fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(a.defaultModelDialog.Filtered))))
		}
	}
	if a.defaultModelDialog.Error != "" {
		lines = append(lines, "", errorStyle.Render(a.defaultModelDialog.Error))
	}
	lines = append(lines, "", a.translator.Text(i18n.MsgDefaultModelFooter))
	return defaultModelDialogStyle.Width(width).Render(strings.Join(lines, "\n"))
}

func buildDefaultModelSettingsFrom(base *config.Settings, providerID, modelID string) *config.Settings {
	next := *base
	next.DefaultProvider = providerID
	next.DefaultModel = modelID
	return &next
}

func (a *App) saveDefaultModel(scope, providerID, modelID string) {
	sparse, err := loadDefaultModelSettings(scope)
	if err != nil {
		a.defaultModelDialog.Error = a.translator.Text(i18n.MsgDefaultModelLoadFailed, scope, err)
		a.scheduleRender()
		return
	}
	nextScoped := buildDefaultModelSettingsFrom(sparse, providerID, modelID)

	runtimePatched := buildDefaultModelSettingsFrom(a.settings, providerID, modelID)
	p, m, err := providerfactory.Create(runtimePatched, providerID, modelID)
	if err != nil {
		a.defaultModelDialog.Error = a.translator.Text(i18n.MsgDefaultModelValidationFailed, err)
		a.scheduleRender()
		return
	}
	if err := saveDefaultModelSettings(scope, nextScoped); err != nil {
		a.defaultModelDialog.Error = a.translator.Text(i18n.MsgDefaultModelSaveFailed, scope, err)
		a.scheduleRender()
		return
	}

	a.settings = runtimePatched
	a.provider = p
	a.providerName = providerID
	a.model = m
	a.syncAgentManagerRuntime()
	a.resetAgent(fmt.Errorf("default model changed"))
	a.closeDefaultModelDialog()
	a.addCommandStatus(a.translator.Text(i18n.MsgDefaultModelSaved, scope, providerID, modelID))
}

func loadDefaultModelSettings(scope string) (*config.Settings, error) {
	switch scope {
	case "global":
		return config.LoadGlobalSettingsSparse()
	default:
		return config.LoadProjectSettingsSparse()
	}
}

func saveDefaultModelSettings(scope string, s *config.Settings) error {
	switch scope {
	case "global":
		return config.SaveGlobalSettings(s)
	default:
		return config.SaveProjectSettings(s)
	}
}
