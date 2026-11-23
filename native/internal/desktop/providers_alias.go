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
type providerTestResultView = providers.TestResultView
type providerDiscoverResultView = providers.DiscoverResultView
type providerModelDraft = providers.ModelDraft
type providerDraft = providers.Draft
type providerSecretDraft = providers.SecretDraft
type providerPanelState = providers.State
type providerCapabilitySet = providers.CapabilitySet

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
func providerConfigByID(catalog *providers.CatalogView, id string) *providers.ConfigView {
	return providers.ConfigByID(catalog, id)
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
func providerUniqueID(catalog *providers.CatalogView) string { return providers.UniqueID(catalog) }
func providerSelectOptions(current string, options []string) []string {
	return providers.SelectOptions(current, options)
}
func providerStringList(input string) []string { return providers.StringList(input) }
func optionalIntString(value int) string       { return providers.OptionalIntString(value) }
func parseProviderOptionalInt(value string) (int, bool, error) {
	return providers.ParseOptionalInt(value)
}
func validateProviderDraft(draft *providers.Draft, catalog *providers.CatalogView, originalID string) error {
	return providers.ValidateDraft(draft, catalog, originalID)
}
func providerModelsPayload(draft *providers.Draft) ([]map[string]any, error) {
	return providers.ModelsPayload(draft)
}
func buildProviderSaveRequest(draft *providers.Draft, secret providers.SecretDraft, previousID string, catalog *providers.CatalogView) (map[string]any, error) {
	return providers.BuildSaveRequest(draft, secret, previousID, catalog)
}
func buildProviderDefaultsPatch(provider, model, thinking string) (map[string]any, error) {
	return providers.BuildDefaultsPatch(provider, model, thinking)
}
func providerMaskedKey(masked *string) string { return providers.MaskedKey(masked) }
func providerScrubSecret(message, secret string) string {
	return providers.ScrubSecret(message, secret)
}
func providerSummaryList(p *providers.State, settingsAvailable bool) []providers.Summary {
	return providers.SummaryList(p, settingsAvailable)
}
func providerConfigSummary(config providers.ConfigView) providers.Summary {
	return providers.ConfigSummary(config)
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

func (a *nativeApp) providerConfigIDs() []string {
	return a.providers.ConfigIDs()
}

func (a *nativeApp) selectInitialProvider(invalidate bool) {
	a.providers.SelectInitial(invalidate)
}

func (a *nativeApp) selectProviderDraft(id string, invalidate bool) {
	a.providers.SelectDraft(id, invalidate)
}

func (a *nativeApp) startNewProviderDraft() {
	a.providers.StartNewDraft()
}

func (a *nativeApp) saveProviderDraft() {
	a.providers.Save(a)
}

func (a *nativeApp) applyCatalogDefaults() {
	a.providers.ApplyCatalogDefaults()
}

func (a *nativeApp) requestDeleteProvider() {
	a.providers.RequestDelete()
}

func (a *nativeApp) cancelDeleteProvider() {
	a.providers.CancelDelete()
}

func (a *nativeApp) confirmDeleteProvider() {
	a.providers.ConfirmDelete(a)
}

func (a *nativeApp) testProviderDraft() {
	a.providers.TestDraft(a)
}

func (a *nativeApp) saveProviderDefaults() {
	a.providers.SaveDefaults(a)
}

func (a *nativeApp) discoverProviderModels() {
	a.providers.DiscoverModels(a)
}

func (a *nativeApp) closeDiscoverDialog() {
	a.providers.CloseDiscover()
}

func (a *nativeApp) addDiscoveredModels() {
	a.providers.AddDiscoveredModels()
}

func (a *nativeApp) providerFilteredConfigs() []providers.ConfigView {
	return a.providers.FilteredConfigs()
}

func (a *nativeApp) testProviderByName(name string) {
	a.providers.TestByName(a, name)
}

func (a *nativeApp) setDefaultProvider(name string) {
	a.providers.SetDefault(a, name)
}

func (a *nativeApp) openProviderFallbackKey(name string) {
	a.providers.OpenFallbackKey(name)
}

func (a *nativeApp) closeProviderFallbackKey() {
	a.providers.CloseFallbackKey()
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
