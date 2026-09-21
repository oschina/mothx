package tui

import (
	"strings"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/tui/i18n"
)

// openSettingsDialog handles the /settings command.
// No args: opens the full settings menu.
// With a providerId arg: opens directly into that provider's settings detail.
func (a *App) openSettingsDialog(args []string) {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgSettingsRunning))
		return
	}
	a.openAuthDialog()
	a.auth.Mode = "settings"
	a.auth.SetDefault = false
	if len(args) > 0 {
		providerID := strings.TrimSpace(args[0])
		if providerID != "" {
			// Go directly to settings detail for this provider
			a.initAuthForProvider(providerID)
			a.auth.View = authViewSettingsDetail
			a.auth.Cursor = 0
			a.scheduleRender()
			return
		}
	}
	a.auth.View = authViewSettingsRoot
	a.auth.Cursor = 0
	a.scheduleRender()
}

// initAuthForProvider initializes the auth dialog state from a known provider's
// built-in defaults merged with any existing runtime config.
func (a *App) initAuthForProvider(providerID string) {
	resolved := config.ResolveProviderConfig(providerID, a.settings)
	a.auth.ProviderID = providerID
	a.auth.Provider = providerEditStateFrom(resolved)

	// Initialize models map from resolved config
	a.auth.Models = map[string]*modelEditState{}
	a.auth.ModelOrder = nil
	for _, m := range resolved.Models {
		me := modelEditStateFromMC(&m)
		if me != nil {
			a.auth.Models[m.ID] = me
			a.auth.ModelOrder = append(a.auth.ModelOrder, m.ID)
		}
	}
}

// initAuthForCustom initializes the auth dialog for a custom provider.
func (a *App) initAuthForCustom(providerID string) {
	a.auth.ProviderID = providerID
	a.auth.Provider = providerEditStateFrom(&config.ProviderConfig{
		API: "openai-chat",
	})
	a.auth.Models = map[string]*modelEditState{}
	a.auth.ModelOrder = nil
}

// initModelFromDefault attempts to find a built-in default for the given model
// under the current provider. Falls back to a generic template if not found.
func (a *App) initModelFromDefault(modelID string) *modelEditState {
	preset := config.PresetModelConfig(a.auth.ProviderID, modelID, a.settings)
	return modelEditStateFromMC(&preset)
}
