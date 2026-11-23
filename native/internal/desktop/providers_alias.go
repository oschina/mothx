package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/providers"
)

type providerModel = providers.Model
type providerCatalogModel = providers.CatalogModel
type providerConfigShape = providers.ConfigShape
type providerConfigView = providers.ConfigView
type providerSummary = providers.Summary
type providerModelDefaults = providers.ModelDefaults
type providerCatalogView = providers.CatalogView
type providerSettingsView = providers.SettingsView
type providerModelDraft = providers.ModelDraft
type providerDraft = providers.Draft
type providerSecretDraft = providers.SecretDraft

const (
	providerScopeConfigured = providers.ScopeConfigured
	providerScopeAll        = providers.ScopeAll
	providerEditorModels    = providers.EditorModels
)

func newProviderPanelState() providers.Panel { return providers.NewPanel() }
func providerModelDefaultsFor(catalog *providers.CatalogView) providers.ModelDraft {
	return providers.ModelDefaultsFor(catalog)
}
func providerDraftFromConfig(view providers.ConfigView) providers.Draft {
	return providers.DraftFromConfig(view)
}
func providerModelsFor(catalog *providers.CatalogView, provider string) []providers.CatalogModel {
	return providers.ModelsFor(catalog, provider)
}
func providerModelIDsFor(catalog *providers.CatalogView, provider string) []string {
	return providers.ModelIDsFor(catalog, provider)
}
func providerDefaultModelFor(catalog *providers.CatalogView, provider, preferred string) string {
	return providers.DefaultModelFor(catalog, provider, preferred)
}
func validateProviderDraft(draft *providers.Draft, catalog *providers.CatalogView, originalID string) error {
	return providers.ValidateDraft(draft, catalog, originalID)
}
func buildProviderSaveRequest(draft *providers.Draft, secret providers.SecretDraft, previousID string, catalog *providers.CatalogView) (map[string]any, error) {
	return providers.BuildSaveRequest(draft, secret, previousID, catalog)
}
func buildProviderDefaultsPatch(provider, model, thinking string) (map[string]any, error) {
	return providers.BuildDefaultsPatch(provider, model, thinking)
}
func providerScrubSecret(message, secret string) string {
	return providers.ScrubSecret(message, secret)
}
func (a *nativeApp) providerCapabilities() providers.CapabilitySet {
	return a.providers.Capabilities(a)
}

func (a *nativeApp) providerPanelCurrent(seq, reloadSeq int64) bool {
	return a.providers.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateProviderPanel() {
	a.providers.Invalidate()
}

func (a *nativeApp) startProviderPanelLoad() {
	a.providers.StartLoad(a)
}

func (a *nativeApp) normalizeProviderDefaults() {
	a.providers.NormalizeDefaults()
}

func (a *nativeApp) selectProviderDraft(id string, invalidate bool) {
	a.providers.SelectDraft(id, invalidate)
}

func (a *nativeApp) requestDeleteProvider() {
	a.providers.RequestDelete()
}

func (a *nativeApp) providerFilteredConfigs() []providers.ConfigView {
	return a.providers.FilteredConfigs()
}

func (a *nativeApp) saveProviderFallbackKey() {
	a.providers.SaveFallbackKey(a)
}

func (a *nativeApp) settingsProvidersPane(c *ui.Context, p palette) {
	a.providers.View(a, c, p)
}

func (a *nativeApp) settingsProviderDialogs(c *ui.Context, p palette) {
	a.providers.Dialogs(a, c, p)
}
