package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// onlineModelsLoadedMsg carries the result of an asynchronous provider model
// discovery triggered from the auth dialog.
type onlineModelsLoadedMsg struct {
	providerID string
	models     []provider.DiscoveredModel
	err        error
}

// startFetchOnlineModels kicks off model discovery against the draft provider
// configuration. Selected models are only added to the draft; nothing is
// persisted until the provider is saved.
func (a *App) startFetchOnlineModels() tea.Cmd {
	if a.auth.OnlineLoading {
		return nil
	}
	pe := &a.auth.Provider
	if strings.TrimSpace(pe.BaseURL) == "" || strings.TrimSpace(pe.API) == "" {
		a.auth.Error = a.translator.Text(i18n.MsgAuthOnlineModelsNeedBaseURL)
		a.scheduleRender()
		return nil
	}
	a.auth.OnlineLoading = true
	a.auth.Error = ""
	a.scheduleRender()
	opts := provider.DiscoverModelsOptions{
		API:         pe.API,
		BaseURL:     pe.BaseURL,
		APIKey:      pe.APIKey,
		HTTPProxy:   pe.HTTPProxy,
		ForceHTTP11: pe.ForceHTTP11,
		Headers:     config.CloneStringMap(pe.Headers),
	}
	providerID := a.auth.ProviderID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		models, err := provider.DiscoverModels(ctx, opts)
		return onlineModelsLoadedMsg{providerID: providerID, models: models, err: err}
	}
}

// handleOnlineModelsLoaded applies a discovery result to the open auth dialog.
// Stale results (dialog closed or provider switched) are dropped.
func (a *App) handleOnlineModelsLoaded(msg onlineModelsLoadedMsg) {
	a.auth.OnlineLoading = false
	if !a.auth.Open || a.auth.ProviderID != msg.providerID {
		return
	}
	if msg.err != nil {
		a.auth.Error = a.translator.Text(i18n.MsgAuthOnlineModelsFetchFailed, msg.err)
		return
	}
	if len(msg.models) == 0 {
		a.auth.Error = a.translator.Text(i18n.MsgAuthOnlineModelsEmpty)
		return
	}
	a.auth.OnlineModels = msg.models
	a.auth.OnlineSearch = ""
	a.pushAuthView(authViewModelsOnline)
}

// authOnlineModelOptions lists discovered models with an add/remove marker,
// filtered by the active search query.
func (a *App) authOnlineModelOptions() []authOption {
	indexes := filterOnlineModels(a.auth.OnlineModels, a.auth.OnlineSearch)
	opts := make([]authOption, 0, len(indexes)+1)
	for _, idx := range indexes {
		m := a.auth.OnlineModels[idx]
		marker := "  "
		if a.isAuthModelAdded(m.ID) {
			marker = "✓ "
		}
		opts = append(opts, authOption{
			Title:       marker + m.ID,
			Description: a.onlineModelSummary(m),
			Value:       "online:" + m.ID,
		})
	}
	opts = append(opts, authOption{Title: a.translator.Text(i18n.MsgAuthDone), Description: a.translator.Text(i18n.MsgAuthDone), Value: "done"})
	return opts
}

// filterOnlineModels returns the indexes of discovered models matching the
// query, ranked exact > prefix > substring across model ID and display name,
// preserving discovery order within the same score. An empty query matches
// every model.
func filterOnlineModels(models []provider.DiscoveredModel, query string) []int {
	indexes := make([]int, 0, len(models))
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		for i := range models {
			indexes = append(indexes, i)
		}
		return indexes
	}
	type scored struct {
		index int
		score int
	}
	matches := make([]scored, 0, len(models))
	for i, m := range models {
		if score := onlineModelMatchScore(m, query); score >= 0 {
			matches = append(matches, scored{index: i, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].score < matches[j].score
	})
	for _, m := range matches {
		indexes = append(indexes, m.index)
	}
	return indexes
}

func onlineModelMatchScore(m provider.DiscoveredModel, query string) int {
	best := -1
	for _, s := range []string{m.ID, m.Name} {
		lower := strings.ToLower(s)
		score := -1
		switch {
		case lower == query:
			score = 0
		case strings.HasPrefix(lower, query):
			score = 1
		case strings.Contains(lower, query):
			score = 2
		}
		if score >= 0 && (best == -1 || score < best) {
			best = score
		}
		if best == 0 {
			break
		}
	}
	return best
}

func (a *App) onlineModelSummary(m provider.DiscoveredModel) string {
	parts := []string{}
	if m.Name != "" && m.Name != m.ID {
		parts = append(parts, m.Name)
	}
	parts = append(parts, "ctx="+authItoa(m.ContextWindow))
	parts = append(parts, "max="+authItoa(m.MaxTokens))
	if m.Reasoning {
		parts = append(parts, "reasoning")
	}
	if len(m.Input) > 0 {
		parts = append(parts, "in="+strings.Join(m.Input, ","))
	}
	return strings.Join(parts, "  ")
}

// selectOnlineModel toggles a discovered model in/out of the current draft.
func (a *App) selectOnlineModel(value string) {
	if value == "done" {
		a.popAuthView()
		return
	}
	modelID := strings.TrimPrefix(value, "online:")
	if a.isAuthModelAdded(modelID) {
		a.removeAuthModel(modelID)
	} else if m, ok := a.findOnlineModel(modelID); ok {
		a.addAuthModelFromDiscovered(m)
	}
	a.scheduleRender()
}

// deleteSelectedOnlineModel removes the drafted model under the cursor, if any.
func (a *App) deleteSelectedOnlineModel() bool {
	opts := a.authOnlineModelOptions()
	if a.auth.Cursor < 0 || a.auth.Cursor >= len(opts) {
		return false
	}
	value := opts[a.auth.Cursor].Value
	if !strings.HasPrefix(value, "online:") {
		return false
	}
	modelID := strings.TrimPrefix(value, "online:")
	if !a.isAuthModelAdded(modelID) {
		return false
	}
	return a.removeAuthModel(modelID)
}

func (a *App) findOnlineModel(modelID string) (provider.DiscoveredModel, bool) {
	for _, m := range a.auth.OnlineModels {
		if m.ID == modelID {
			return m, true
		}
	}
	return provider.DiscoveredModel{}, false
}

// isAuthModelAdded reports whether a model ID is part of the current draft.
func (a *App) isAuthModelAdded(modelID string) bool {
	if _, ok := a.auth.Models[modelID]; !ok {
		return false
	}
	for _, id := range a.auth.ModelOrder {
		if id == modelID {
			return true
		}
	}
	return false
}

// addAuthModelFromDiscovered adds a discovered model to the draft. Built-in
// defaults for the current provider take precedence over discovered metadata.
func (a *App) addAuthModelFromDiscovered(m provider.DiscoveredModel) {
	if a.isAuthModelAdded(m.ID) {
		return
	}
	me := a.initModelFromDefault(m.ID)
	if resolved := config.ResolveModelConfig(a.auth.ProviderID, m.ID, a.settings); resolved == nil {
		// No built-in defaults: use the discovered metadata.
		if m.Name != "" {
			me.Name = m.Name
		}
		if m.ContextWindow > 0 {
			me.ContextWindow = m.ContextWindow
		}
		if m.MaxTokens > 0 {
			me.MaxTokens = m.MaxTokens
			me.MaxTokensEdited = true
		}
		me.Reasoning = m.Reasoning
		if len(m.Input) > 0 {
			me.Input = config.CloneStringSlice(m.Input)
		}
	}
	if a.auth.Models == nil {
		a.auth.Models = map[string]*modelEditState{}
	}
	a.auth.Models[m.ID] = me
	a.auth.ModelOrder = append(a.auth.ModelOrder, m.ID)
}

// removeAuthModel deletes a model from the draft by ID.
func (a *App) removeAuthModel(modelID string) bool {
	if _, ok := a.auth.Models[modelID]; !ok {
		return false
	}
	delete(a.auth.Models, modelID)
	order := a.auth.ModelOrder[:0]
	for _, id := range a.auth.ModelOrder {
		if id != modelID {
			order = append(order, id)
		}
	}
	a.auth.ModelOrder = order
	if len(a.auth.ModelOrder) == 0 {
		a.auth.ModelOrder = nil
	}
	if a.auth.CurrentModelID == modelID {
		a.auth.CurrentModelID = ""
		if len(a.auth.ModelOrder) > 0 {
			a.auth.CurrentModelID = a.auth.ModelOrder[0]
		}
	}
	return true
}
