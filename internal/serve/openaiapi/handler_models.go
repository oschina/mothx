package openaiapi

import (
	"net/http"
	"time"

	"github.com/startvibecoding/mothx/internal/config"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}

	models := s.provider.Models()
	items := make([]ModelItem, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		items = append(items, ModelItem{
			ID: m.ID, Name: m.Name, Object: "model", Created: time.Now().Unix(), OwnedBy: "vibecoding",
			Provider: m.Provider, Input: append([]string(nil), m.Input...), Reasoning: m.Reasoning,
			ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens,
		})
	}

	resp := ModelListResponse{
		Object: "list",
		Data:   items,
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleModelCatalog serves the WebUI model picker. Every listed model is
// resolved through providerfactory.ResolvedModels — the same shared logic
// that builds the TUI provider's model list — so both front ends offer one
// canonical catalog instead of the WebUI merging raw settings JSON.
func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error")
		return
	}

	s.mu.RLock()
	settings := s.settings
	currentProvider := s.providerName
	currentModel := ""
	if s.model != nil {
		currentModel = s.model.ID
	}
	s.mu.RUnlock()

	providerIDs := make([]string, 0)
	seen := make(map[string]struct{})
	addProvider := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		providerIDs = append(providerIDs, id)
	}
	if settings != nil {
		for id := range settings.Providers {
			addProvider(id)
		}
	}
	// The active provider is always selectable, even when it comes from a
	// built-in preset or serve flag instead of the settings providers map.
	addProvider(currentProvider)
	providerfactory.SortProviderIDs(providerIDs)

	created := time.Now().Unix()
	items := make([]ModelItem, 0)
	for _, providerID := range providerIDs {
		for _, m := range providerfactory.ResolvedModels(settings, providerID) {
			if m == nil {
				continue
			}
			items = append(items, ModelItem{
				ID: m.ID, Name: m.Name, Object: "model", Created: created, OwnedBy: "vibecoding",
				Provider: providerID, Input: append([]string(nil), m.Input...), Reasoning: m.Reasoning,
				ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens,
			})
		}
	}

	defaults := config.PresetModelConfig("", "", nil)
	resp := ModelCatalogResponse{
		Object:          "list",
		DefaultProvider: currentProvider,
		DefaultModel:    currentModel,
		Providers:       providerIDs,
		Data:            items,
		ModelDefaults: ModelDefaults{Input: config.CloneStringSlice(defaults.Input), Reasoning: defaults.Reasoning,
			ContextWindow: defaults.ContextWindow, MaxTokens: defaults.MaxTokens},
	}
	writeJSON(w, http.StatusOK, resp)
}
