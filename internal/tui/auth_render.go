package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/oschina/mothx/internal/config"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// previewExpansion tracks which sections of the Review JSON are expanded.
type previewExpansion struct {
	CostExpand   bool
	CompatExpand bool
}

// renderAuthDialog renders the complete auth dialog overlay.
func (a *App) renderAuthDialog() string {
	if !a.auth.Open {
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
	lines = append(lines, a.authTitle(a.auth.View))
	lines = append(lines, "")
	if a.authInputActive() {
		lines = append(lines, a.authInputPrompt(a.auth.View))
		lines = append(lines, a.authInput.View())
		lines = append(lines, "")
		lines = append(lines, statusStyle.Render(a.translator.Text(i18n.MsgAuthEnterSubmit)))
	} else if a.auth.View == authViewReview {
		lines = append(lines, a.renderAuthPreviewLines()...)
		lines = append(lines, "")
		lines = append(lines, a.renderAuthOptions())
		lines = append(lines, statusStyle.Render(a.translator.Text(i18n.MsgAuthEnterSave)))
	} else {
		if searchView, query := a.authSearchState(); searchView {
			if query == "" {
				query = a.translator.Text(i18n.MsgAuthSearch)
			}
			lines = append(lines, statusStyle.Render(a.translator.Text(i18n.MsgAuthSearchLabel, query)), "")
		}
		if a.auth.View == authViewModelsOnline && strings.TrimSpace(a.auth.OnlineSearch) != "" && len(filterOnlineModels(a.auth.OnlineModels, a.auth.OnlineSearch)) == 0 {
			lines = append(lines, statusStyle.Render(a.translator.Text(i18n.MsgAuthNoModelsMatch)))
		}
		lines = append(lines, a.renderAuthOptions())
		lines = append(lines, "")
		hint := i18n.MsgAuthEnterSelect
		if a.auth.View == authViewModelsOnline {
			hint = i18n.MsgAuthOnlineModelsHint
		}
		lines = append(lines, statusStyle.Render(a.translator.Text(hint)))
		if a.auth.OnlineLoading {
			lines = append(lines, "", statusStyle.Render(a.translator.Text(i18n.MsgAuthOnlineModelsLoading)))
		}
	}
	if a.auth.Error != "" {
		lines = append(lines, "", errorStyle.Render(a.auth.Error))
	}
	return authDialogStyle.Width(width).Render(strings.Join(lines, "\n"))
}

// renderAuthPreviewLines renders the review preview with foldable cost/compat.
func (a *App) renderAuthPreviewLines() []string {
	if a.auth.Preview == "" {
		return nil
	}
	// If the preview doesn't contain fold markers, just truncate by lines
	if !strings.Contains(a.auth.Preview, previewFoldMarker) {
		return renderAuthPreview(a.auth.Preview, a.translator)
	}
	rendered := renderFoldedPreview(a.auth.Preview, a.auth.PreviewExpand, a.translator)
	return []string{rendered}
}

const previewFoldMarker = "◸fold:"

// renderFoldedPreview renders a preview string that contains fold markers.
// Sections marked with ◸fold:<name> are collapsed unless expand[name] is true.
func renderFoldedPreview(preview string, exp previewExpansion, translators ...i18n.Translator) string {
	tr := authRenderTranslator(translators...)
	lines := strings.Split(strings.TrimRight(preview, "\n"), "\n")
	var out []string
	foldedSections := map[string]*bool{
		"cost":   &exp.CostExpand,
		"compat": &exp.CompatExpand,
	}
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "\""+previewFoldMarker) {
			// Extract section name: "◸fold:cost": { ... }
			part := strings.TrimPrefix(trimmed, "\""+previewFoldMarker)
			if idx := strings.Index(part, "\""); idx >= 0 {
				part = part[:idx]
			}
			name := part
			expandPtr, known := foldedSections[name]
			if !known {
				// Unknown fold section — just show the line
				out = append(out, line)
				i++
				continue
			}
			if *expandPtr {
				// Expanded: show the full block
				out = append(out, line)
				i++
				// Copy until closing brace at same indent
				for i < len(lines) {
					out = append(out, lines[i])
					i++
					if strings.TrimSpace(lines[i-1]) == "}" || strings.TrimSpace(lines[i-1]) == "}," {
						break
					}
				}
			} else {
				// Collapsed: show one line with ▶ marker
				out = append(out, fmt.Sprintf("  ▶ %s: { … }", name))
				i++
				// Skip until closing brace
				for i < len(lines) {
					if strings.TrimSpace(lines[i]) == "}" || strings.TrimSpace(lines[i]) == "}," {
						i++
						break
					}
					i++
				}
			}
		} else {
			out = append(out, line)
			i++
		}
	}
	// Truncate if still too long
	if len(out) > authMaxPreviewVisibleLines {
		visible := append([]string(nil), out[:authMaxPreviewVisibleLines]...)
		visible = append(visible, statusStyle.Render(tr.Text(i18n.MsgAuthMoreLinesHidden, len(out)-authMaxPreviewVisibleLines)))
		return strings.Join(visible, "\n")
	}
	return strings.Join(out, "\n")
}

// renderAuthPreview truncates a plain (non-folded) preview to max visible lines.
func renderAuthPreview(preview string, translators ...i18n.Translator) []string {
	tr := authRenderTranslator(translators...)
	preview = strings.TrimRight(preview, "\n")
	if preview == "" {
		return nil
	}
	lines := strings.Split(preview, "\n")
	if len(lines) <= authMaxPreviewVisibleLines {
		return lines
	}
	visible := append([]string(nil), lines[:authMaxPreviewVisibleLines]...)
	visible = append(visible, statusStyle.Render(tr.Text(i18n.MsgAuthMoreLinesHidden, len(lines)-authMaxPreviewVisibleLines)))
	return visible
}

// authSearchState reports whether the current view supports inline search and
// returns the active query.
func (a *App) authSearchState() (bool, string) {
	switch a.auth.View {
	case authViewExistingProvider:
		return true, a.auth.Search
	case authViewModelsOnline:
		return true, a.auth.OnlineSearch
	}
	return false, ""
}

func (a *App) renderAuthOptions() string {
	opts := a.authOptions()
	if len(opts) == 0 {
		if a.auth.View == authViewExistingProvider && a.auth.Search != "" {
			return statusStyle.Render(a.translator.Text(i18n.MsgAuthNoProvidersMatch))
		}
		return ""
	}
	start, end := authVisibleRange(a.auth.Cursor, len(opts), authMaxVisibleOptions)
	visible := opts[start:end]
	var lines []string
	for i, opt := range visible {
		actual := start + i
		cursor := "  "
		style := lipgloss.NewStyle()
		if actual == a.auth.Cursor {
			cursor = "› "
			style = style.Foreground(lipgloss.Color("86")).Bold(true)
		}
		scroll := authScrollMarker(actual, len(opts), start, end)
		lines = append(lines, style.Render(cursor+opt.Title)+scroll)
		if opt.Description != "" {
			lines = append(lines, statusStyle.Render("  "+opt.Description))
		}
		if i != len(visible)-1 {
			lines = append(lines, "")
		}
	}
	if len(opts) > authMaxVisibleOptions {
		lines = append(lines, "", statusStyle.Render(a.translator.Text(i18n.MsgAuthShowingRange, start+1, end, len(opts))))
	}
	return strings.Join(lines, "\n")
}

func authVisibleRange(cursor, total, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit <= 0 || total <= limit {
		return 0, total
	}
	start := cursor - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > total {
		start = total - limit
	}
	return start, start + limit
}

func authScrollMarker(actual, total, start, end int) string {
	if total <= authMaxVisibleOptions {
		return ""
	}
	switch {
	case actual == start && start > 0:
		return statusStyle.Render("  ↑")
	case actual == end-1 && end < total:
		return statusStyle.Render("  ↓")
	default:
		return statusStyle.Render("  │")
	}
}

func (a *App) authTitle(v authView) string {
	switch v {
	case authViewMain:
		return a.translator.Text(i18n.MsgAuthTitleConnectProvider)
	case authViewExistingProvider:
		if a.auth.Mode == "settings" {
			return a.translator.Text(i18n.MsgAuthTitleSettingsProviders)
		}
		return a.translator.Text(i18n.MsgAuthTitleExistingProvider)
	case authViewSettingsRoot:
		return a.translator.Text(i18n.MsgSettingsTitle)
	case authViewSettingsDefaults:
		return a.translator.Text(i18n.MsgAuthTitleSettingsDefaults)
	case authViewSettingsBehavior:
		return a.translator.Text(i18n.MsgAuthTitleSettingsBehavior)
	case authViewSettingsWebSearch:
		return a.translator.Text(i18n.MsgAuthTitleSettingsWebSearch)
	case authViewSettingsContextFiles:
		return a.translator.Text(i18n.MsgAuthTitleSettingsContextFiles)
	case authViewSettingsStatusLine:
		return a.translator.Text(i18n.MsgAuthTitleSettingsStatusLine)
	case authViewSettingsCompaction:
		return a.translator.Text(i18n.MsgAuthTitleSettingsCompaction)
	case authViewSettingsSandbox:
		return a.translator.Text(i18n.MsgAuthTitleSettingsSandbox)
	case authViewSettingsPaths:
		return a.translator.Text(i18n.MsgAuthTitleSettingsPaths)
	case authViewSettingsRetry:
		return a.translator.Text(i18n.MsgAuthTitleSettingsRetry)
	case authViewSettingsApproval:
		return a.translator.Text(i18n.MsgAuthTitleSettingsApproval)
	case authViewCustomID:
		return a.translator.Text(i18n.MsgAuthTitleCustomProviderID)
	case authViewSettingsDetail:
		return a.translator.Text(i18n.MsgAuthTitleSettingsProvider, a.auth.ProviderID)
	case authViewProviderGroupList:
		return a.translator.Text(i18n.MsgAuthTitleProviderSettings, a.auth.ProviderID)
	case authViewProviderCredentials:
		return a.translator.Text(i18n.MsgAuthTitleProviderCredentials)
	case authViewProviderProtocol:
		return a.translator.Text(i18n.MsgAuthTitleProviderProtocol)
	case authViewProviderNetwork:
		return a.translator.Text(i18n.MsgAuthTitleProviderNetwork)
	case authViewProviderAdvanced:
		return a.translator.Text(i18n.MsgAuthTitleProviderAdvanced)
	case authViewHeadersEdit:
		return a.translator.Text(i18n.MsgAuthTitleProviderHeaders)
	case authViewResponsesEdit:
		return a.translator.Text(i18n.MsgAuthTitleProviderResponses)
	case authViewModelList:
		return a.translator.Text(i18n.MsgAuthTitleProviderModels)
	case authViewModelGroupList:
		return a.translator.Text(i18n.MsgAuthTitleModelParameters, a.auth.CurrentModelID)
	case authViewModelBasics:
		return a.translator.Text(i18n.MsgAuthTitleModelBasics)
	case authViewModelCapabilities:
		return a.translator.Text(i18n.MsgAuthTitleModelCapabilities)
	case authViewModelSampling:
		return a.translator.Text(i18n.MsgAuthTitleModelSampling)
	case authViewModelCost:
		return a.translator.Text(i18n.MsgAuthTitleModelCost)
	case authViewModelCompat:
		return a.translator.Text(i18n.MsgAuthTitleModelCompatibility)
	case authViewAddModelID:
		return a.translator.Text(i18n.MsgAuthTitleAddModelID)
	case authViewAddModelName:
		return a.translator.Text(i18n.MsgAuthTitleAddModelName)
	case authViewModelsOnline:
		return a.translator.Text(i18n.MsgAuthTitleOnlineModels)
	case authViewDefault:
		return a.translator.Text(i18n.MsgAuthTitleSetupDefault)
	case authViewReview:
		return a.translator.Text(i18n.MsgAuthTitleSetupReview)
	case authViewEditMenu:
		return a.translator.Text(i18n.MsgAuthTitleSetupEdit)
	default:
		return a.translator.Text(i18n.MsgAuthTitleSetup)
	}
}

func (a *App) authInputPrompt(v authView) string {
	switch v {
	case authViewCustomID:
		return a.translator.Text(i18n.MsgAuthPromptProviderID)
	case authViewAddModelID:
		return a.translator.Text(i18n.MsgAuthPromptModelID)
	case authViewAddModelName:
		return a.translator.Text(i18n.MsgAuthPromptModelName, a.auth.CurrentModelID)
	case authViewProviderCredentials, authViewProviderProtocol, authViewProviderNetwork,
		authViewProviderAdvanced, authViewResponsesEdit:
		return a.authProviderInputPrompt()
	case authViewHeadersEdit:
		if a.auth.ParamField == "headerKey" {
			return a.translator.Text(i18n.MsgAuthPromptHeaderName)
		}
		return a.translator.Text(i18n.MsgAuthPromptHeaderValue, a.auth.ParamFieldKey)
	case authViewModelBasics, authViewModelCapabilities, authViewModelSampling,
		authViewModelCost, authViewModelCompat:
		return a.authModelInputPrompt()
	case authViewSettingsDefaults, authViewSettingsBehavior, authViewSettingsWebSearch,
		authViewSettingsContextFiles, authViewSettingsStatusLine, authViewSettingsCompaction,
		authViewSettingsSandbox, authViewSettingsPaths, authViewSettingsRetry,
		authViewSettingsApproval:
		return a.authSettingsInputPrompt()
	default:
		return a.translator.Text(i18n.MsgAuthPromptInput)
	}
}

func authRenderTranslator(translators ...i18n.Translator) i18n.Translator {
	if len(translators) > 0 {
		return translators[0]
	}
	return i18n.New(i18n.LanguageEN)
}

// --- Provider selection helpers (moved from auth_dialog.go) ---

func sortedAuthProviderIDs(settings *config.Settings) []string {
	if settings == nil {
		return nil
	}
	ids := make([]string, 0, len(settings.Providers))
	for id := range settings.Providers {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		pi, pj := authProviderSortPriority(ids[i]), authProviderSortPriority(ids[j])
		if pi != pj {
			return pi < pj
		}
		return ids[i] < ids[j]
	})
	return ids
}

func filterAuthProviderIDs(ids []string, query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ids
	}
	type scored struct {
		id    string
		score int
	}
	var matches []scored
	for _, id := range ids {
		lower := strings.ToLower(id)
		score := -1
		switch {
		case lower == query:
			score = 0
		case strings.HasPrefix(lower, query):
			score = 1
		case strings.Contains(lower, query):
			score = 2
		}
		if score >= 0 {
			matches = append(matches, scored{id: id, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		pi, pj := authProviderSortPriority(matches[i].id), authProviderSortPriority(matches[j].id)
		if pi != pj {
			return pi < pj
		}
		return matches[i].id < matches[j].id
	})
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.id
	}
	return out
}

// authProviderSortPriority delegates to the shared factory ordering so the
// TUI dialogs and the WebUI provider/model catalog use one logic.
func authProviderSortPriority(id string) int {
	return providerfactory.ProviderSortPriority(id)
}

// previewBuildFoldedJSON builds a preview JSON string with cost/compat sections
// marked with fold markers so renderFoldedPreview can collapse them.
func previewBuildFoldedJSON(next *config.Settings, providerID string, maskKey bool) string {
	preview := struct {
		DefaultProvider string                            `json:"defaultProvider,omitempty"`
		DefaultModel    string                            `json:"defaultModel,omitempty"`
		Providers       map[string]*config.ProviderConfig `json:"providers"`
	}{DefaultProvider: next.DefaultProvider, DefaultModel: next.DefaultModel, Providers: map[string]*config.ProviderConfig{}}
	pc := *next.Providers[providerID]
	if maskKey {
		pc.APIKey = maskAuthSecret(pc.APIKey)
	}
	preview.Providers[providerID] = &pc
	data, _ := json.MarshalIndent(preview, "", "  ")
	return insertFoldMarkers(string(data))
}

// insertFoldMarkers finds "cost" and "compat" objects in the JSON and marks them
// with a fold key so the renderer can collapse them.
func insertFoldMarkers(jsonStr string) string {
	// Simple approach: replace `"cost": {` with a marked version
	// This works because json.MarshalIndent always puts the key on its own line
	result := jsonStr
	// We add a sibling key right before "cost" that marks the fold
	result = replaceKeyWithFold(result, "cost")
	result = replaceKeyWithFold(result, "compat")
	return result
}

// replaceKeyWithFold replaces `"key": {` with `"◸fold:key": {` + original key
// so the renderer knows which section can be collapsed.
func replaceKeyWithFold(s, keyName string) string {
	// Pattern: newline + spaces + "key": {
	oldLine := fmt.Sprintf("%q:", keyName)
	newLine := fmt.Sprintf("%q: ", previewFoldMarker+keyName)
	return strings.ReplaceAll(s, oldLine, newLine)
}
