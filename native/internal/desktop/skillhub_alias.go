package desktop

import (
	"encoding/json"

	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/skillhub"
)

type skillHubMarketView = skillhub.MarketView
type skillHubConfigView = skillhub.ConfigView
type skillHubMarketDraft = skillhub.MarketDraft
type skillHubMarketInfo = skillhub.MarketInfo
type skillHubCategory = skillhub.Category
type skillHubCategoriesView = skillhub.CategoriesView
type skillHubTarget = skillhub.Target
type skillHubInstalledState = skillhub.InstalledState
type skillHubSessionState = skillhub.SessionState
type skillHubSkill = skillhub.Skill
type skillHubFile = skillhub.File
type skillHubDownloadSource = skillhub.DownloadSource
type skillHubDetail = skillhub.Detail
type skillHubSearchPage = skillhub.SearchPage
type skillHubMarketsView = skillhub.MarketsView
type skillHubTargetsView = skillhub.TargetsView
type skillHubInstalledView = skillhub.InstalledView
type skillHubInstallResult = skillhub.InstallResult
type skillHubInstallResponse = skillhub.InstallResponse
type skillHubActionResponse = skillhub.ActionResponse
type skillHubPanelState = skillhub.State

func newSkillHubPanelState() skillhub.Panel { return skillhub.NewPanel() }

func skillHubActive(active []string, name string) bool        { return skillhub.Active(active, name) }
func skillHubTargetLabels(targets []skillhub.Target) []string { return skillhub.TargetLabels(targets) }
func skillHubOnMarketChanged(panel *skillhub.Panel)           { skillhub.OnMarketChanged(&panel.State) }
func skillHubOnTargetChanged(panel *skillhub.Panel)           { skillhub.OnTargetChanged(&panel.State) }
func nonEmptyStrings(values ...string) []string               { return skillhub.NonEmptyStrings(values...) }

func skillHubCategoryName(category skillhub.Category) string {
	return skillhub.CategoryName(category)
}
func skillHubChooseMarket(current, configDefault, projectedDefault string, markets []skillhub.MarketInfo) string {
	return skillhub.ChooseMarket(current, configDefault, projectedDefault, markets)
}
func skillHubChooseTarget(currentLabel, scope string, targets []skillhub.Target) (string, string, string) {
	return skillhub.ChooseTarget(currentLabel, scope, targets)
}

const skillHubDefaultSort = skillhub.DefaultSort

func skillHubFlattenCategories(categories []skillhub.Category) []skillhub.Category {
	return skillhub.FlattenCategories(categories)
}
func skillHubInstalledName(skill skillhub.Skill) string { return skillhub.InstalledName(skill) }
func skillHubSkillMeta(skill skillhub.Skill) string     { return skillhub.SkillMeta(skill) }
func skillHubInstallStatus(name string, alreadyInstalled, installed, activated bool, warnings int) string {
	return skillhub.InstallStatus(name, alreadyInstalled, installed, activated, warnings)
}
func skillHubTitle(skill skillhub.Skill) string { return skillhub.Title(skill) }
func skillHubVersionDiffers(remote, installed string) bool {
	return skillhub.VersionDiffers(remote, installed)
}
func skillHubContains(values []string, want string) bool {
	return skillhub.Contains(values, want)
}
func skillHubParseHandles(text string) []string { return skillhub.ParseHandles(text) }
func skillHubMarketIDs(markets []skillhub.MarketDraft) []string {
	return skillhub.MarketIDs(markets)
}
func buildSkillHubConfigPatch(defaultMarket, defaultScope, officialHandles string, markets []skillhub.MarketDraft) (map[string]any, error) {
	return skillhub.BuildConfigPatch(defaultMarket, defaultScope, officialHandles, markets)
}
func buildSkillHubMarketsRequest(sessionID string) map[string]any {
	return skillhub.BuildMarketsRequest(sessionID)
}
func buildSkillHubTargetsRequest(sessionID string) map[string]any {
	return skillhub.BuildTargetsRequest(sessionID)
}
func buildSkillHubInstalledRequest(sessionID string) map[string]any {
	return skillhub.BuildInstalledRequest(sessionID)
}
func buildSkillHubSearchRequest(sessionID, market, query, category, sort string) map[string]any {
	return skillhub.BuildSearchRequest(sessionID, market, query, category, sort)
}
func buildSkillHubOfficialRequest(sessionID, market, query string) map[string]any {
	return skillhub.BuildOfficialRequest(sessionID, market, query)
}
func buildSkillHubDetailRequest(sessionID, market, id string) map[string]any {
	return skillhub.BuildDetailRequest(sessionID, market, id)
}
func buildSkillHubInstallRequest(sessionID, market, id, version, scope, targetDir string, overwrite, activate bool) map[string]any {
	return skillhub.BuildInstallRequest(sessionID, market, id, version, scope, targetDir, overwrite, activate)
}
func buildSkillHubActivateRequest(sessionID, id string) map[string]any {
	return skillhub.BuildActivateRequest(sessionID, id)
}
func buildSkillHubUninstallRequest(sessionID, market, id, scope string) map[string]any {
	return skillhub.BuildUninstallRequest(sessionID, market, id, scope)
}
func prettySkillHubJSON(raw json.RawMessage) string { return skillhub.PrettyJSON(raw) }

func (a *nativeApp) skillHubConfigCapable() bool  { return a.skillhub.ConfigCapable(a) }
func (a *nativeApp) skillHubCatalogCapable() bool { return a.skillhub.CatalogCapable(a) }
func (a *nativeApp) skillHubCapable() bool        { return a.skillhub.Capable(a) }
func (a *nativeApp) skillHubConfigCurrent(seq, reloadSeq int64) bool {
	return a.skillhub.ConfigCurrent(a, seq, reloadSeq)
}
func (a *nativeApp) skillHubCatalogCurrent(seq, reloadSeq int64) bool {
	return a.skillhub.CatalogCurrent(a, seq, reloadSeq)
}
func (a *nativeApp) invalidateSkillHubPanel()                       { a.skillhub.Invalidate() }
func (a *nativeApp) startSkillHubPanelLoad()                        { a.skillhub.StartPanelLoad(a) }
func (a *nativeApp) startSkillHubConfigLoad()                       { a.skillhub.StartConfigLoad(a) }
func (a *nativeApp) applySkillHubConfigView(v *skillhub.ConfigView) { a.skillhub.ApplyConfigView(v) }
func (a *nativeApp) saveSkillHubConfig()                            { a.skillhub.SaveConfig(a) }
func (a *nativeApp) openSkillHubAddMarket()                         { a.skillhub.OpenAddMarket(a) }
func (a *nativeApp) closeSkillHubAddMarket()                        { a.skillhub.CloseAddMarket() }
func (a *nativeApp) confirmSkillHubAddMarket()                      { a.skillhub.ConfirmAddMarket() }
func (a *nativeApp) removeSkillHubMarket(i int)                     { a.skillhub.RemoveMarket(i) }
func (a *nativeApp) startSkillHubCatalogLoad()                      { a.skillhub.StartCatalogLoad(a) }
func (a *nativeApp) applySkillHubCatalogViews(sessionID string, markets *skillhub.MarketsView, targets *skillhub.TargetsView, installed *skillhub.InstalledView) {
	a.skillhub.ApplyCatalogViews(a, sessionID, markets, targets, installed)
}
func (a *nativeApp) skillHubRefreshActiveFlags()      { a.skillhub.RefreshActiveFlags() }
func (a *nativeApp) skillHubApplyInstalledToResults() { a.skillhub.ApplyInstalledToResults() }
func (a *nativeApp) skillHubApplySession(session skillhub.SessionState) {
	a.skillhub.ApplySession(session)
}
func (a *nativeApp) skillHubMarkInstalled(market, id string, result skillhub.InstallResult) {
	a.skillhub.MarkInstalled(market, id, result)
}
func (a *nativeApp) skillHubRemoveInstalled(market, id string) {
	a.skillhub.RemoveInstalled(market, id)
}
func (a *nativeApp) skillHubBrowseBusy() bool             { return a.skillhub.BrowseBusy() }
func (a *nativeApp) loadSkillHubCategories()              { a.skillhub.LoadCategories(a) }
func (a *nativeApp) searchSkillHub()                      { a.skillhub.Search(a) }
func (a *nativeApp) refreshSkillHubCatalog()              { a.skillhub.RefreshCatalog(a) }
func (a *nativeApp) openSkillHubDetail(market, id string) { a.skillhub.OpenDetail(a, market, id) }
func (a *nativeApp) closeSkillHubDetail()                 { a.skillhub.CloseDetail() }
func (a *nativeApp) installSkillHub(market, id, version string) {
	a.skillhub.Install(a, market, id, version)
}
func (a *nativeApp) installSkillHubWithOverwrite(market, id, version string, overwrite bool) {
	a.skillhub.InstallWithOverwrite(a, market, id, version, overwrite)
}
func (a *nativeApp) activateSkillHub(market, id, name string) {
	a.skillhub.Activate(a, market, id, name)
}
func (a *nativeApp) requestUninstallSkillHub(market, id, scope, name string) {
	a.skillhub.RequestUninstall(a, market, id, scope, name)
}
func (a *nativeApp) cancelUninstallSkillHub()  { a.skillhub.CancelUninstall() }
func (a *nativeApp) confirmUninstallSkillHub() { a.skillhub.ConfirmUninstall(a) }
func (a *nativeApp) skillHubCatalogErrorCard(c *ui.Context, p palette, message string) {
	a.skillhub.CatalogErrorCard(a, c, p, message)
}
func (a *nativeApp) skillHubStatusBanner(c *ui.Context, p palette, status string, isError, busy bool) {
	a.skillhub.StatusBanner(c, p, status, isError, busy)
}
func (a *nativeApp) skillHubCategoryRow(c *ui.Context, p palette) { a.skillhub.CategoryRow(a, c, p) }
func (a *nativeApp) skillHubSortRow(c *ui.Context, p palette)     { a.skillhub.SortRow(a, c, p) }

func (a *nativeApp) settingsSkillHubPane(c *ui.Context, p palette)    { a.skillhub.View(a, c, p) }
func (a *nativeApp) settingsSkillHubDialogs(c *ui.Context, p palette) { a.skillhub.Dialogs(a, c, p) }
