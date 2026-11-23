package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/knowledge"
	"github.com/oschina/mothx/native/internal/feature/mcp"
	"github.com/oschina/mothx/native/internal/feature/providers"
)

type knowledgeBase = knowledge.Base
type knowledgeSnapshot = knowledge.Snapshot
type knowledgeIndexProgress = knowledge.IndexProgress
type knowledgeBaseView = knowledge.BaseView
type knowledgeListView = knowledge.ListView
type knowledgeScanResult = knowledge.ScanResult
type knowledgeIndexRunView = knowledge.IndexRunView
type knowledgeRunsView = knowledge.RunsView
type knowledgeSourceView = knowledge.SourceView
type knowledgeSourcesView = knowledge.SourcesView
type knowledgeScheduleView = knowledge.ScheduleView
type knowledgeMCPApplyResult = knowledge.MCPApplyResult
type knowledgeChunk = knowledge.Chunk
type knowledgeNode = knowledge.Node
type knowledgeGraphQuery = knowledge.GraphQuery
type knowledgeQueryResultView = knowledge.QueryResultView
type knowledgeSettingsView = knowledge.SettingsView
type knowledgeBaseDefaults = knowledge.BaseDefaults
type knowledgeBaseDraft = knowledge.BaseDraft
type knowledgeInsightsState = knowledge.InsightsState
type knowledgeInsightsData = knowledge.InsightsData
type knowledgePanelState = knowledge.State

const (
	knowledgePollInterval = knowledge.PollInterval
	knowledgeNewDraftKey  = knowledge.NewDraftKey
)

var (
	knowledgeProfiles = knowledge.Profiles
	knowledgeModes    = knowledge.Modes
)

func newKnowledgePanelState() knowledge.Panel { return knowledge.NewPanel() }
func knowledgeDraftFromBase(base knowledge.Base) *knowledge.BaseDraft {
	return knowledge.DraftFromBase(base)
}
func knowledgeModelChoices(models []providers.CatalogModel, provider string) []string {
	return knowledge.ModelChoices(models, provider)
}
func knowledgeUniqueStrings(values []string) []string { return knowledge.UniqueStrings(values) }
func knowledgeDraftKey(id string) string              { return knowledge.DraftKey(id) }
func knowledgeDraftPayload(draft *knowledge.BaseDraft) map[string]any {
	return knowledge.DraftPayload(draft)
}
func validateKnowledgeDraft(draft *knowledge.BaseDraft) error  { return knowledge.ValidateDraft(draft) }
func knowledgeMCPName(id string) string                        { return knowledge.MCPName(id) }
func knowledgeMCPEnabled(view mcp.ServerView) bool             { return knowledge.MCPEnabled(view) }
func knowledgePanelBusy(state *knowledge.State) bool           { return knowledge.PanelBusy(state) }
func knowledgeStatusText(view knowledge.BaseView) string       { return knowledge.StatusText(view) }
func knowledgeStatsText(snapshot *knowledge.Snapshot) string   { return knowledge.StatsText(snapshot) }
func knowledgeRunStatsText(run knowledge.IndexRunView) string  { return knowledge.RunStatsText(run) }
func knowledgeScheduleText(view knowledge.ScheduleView) string { return knowledge.ScheduleText(view) }
func knowledgeFormatDate(value string) string                  { return knowledge.FormatDate(value) }
func knowledgeFormatSize(bytes int64) string                   { return knowledge.FormatSize(bytes) }
func knowledgeSourceMeta(view knowledge.SourceView) string     { return knowledge.SourceMeta(view) }
func knowledgeTruncate(text string, limit int) string          { return knowledge.Truncate(text, limit) }

func (a *nativeApp) knowledgeCapable() bool        { return a.knowledge.Capable(a) }
func (a *nativeApp) knowledgeRunsCapable() bool    { return a.knowledge.RunsCapable(a) }
func (a *nativeApp) knowledgeSourcesCapable() bool { return a.knowledge.SourcesCapable(a) }
func (a *nativeApp) knowledgeGraphCapable() bool   { return a.knowledge.GraphCapable(a) }
func (a *nativeApp) knowledgeMCPCapable() bool     { return a.knowledge.MCPCapable(a) }
func (a *nativeApp) knowledgePanelCurrent(seq, reloadSeq int64) bool {
	return a.knowledge.Current(a, seq, reloadSeq)
}
func (a *nativeApp) invalidateKnowledgePanel() { a.knowledge.Invalidate() }
func (a *nativeApp) startKnowledgePanelLoad()  { a.knowledge.StartLoad(a) }
func (a *nativeApp) applyKnowledgePanel(views []knowledge.BaseView, defaults *knowledge.BaseDefaults, servers []mcp.ServerView, schedules map[string]knowledge.ScheduleView, insights map[string]knowledge.InsightsData) {
	a.knowledge.ApplyPanel(views, defaults, servers, schedules, insights)
}
func (a *nativeApp) newKnowledgeDraft() *knowledge.BaseDraft { return a.knowledge.NewDraft() }
func (a *nativeApp) normalizeKnowledgeDraft(draft *knowledge.BaseDraft) {
	a.knowledge.NormalizeDraft(draft)
}
func (a *nativeApp) ensureKnowledgeDraft(key string) *knowledge.BaseDraft {
	return a.knowledge.EnsureDraft(key)
}
func (a *nativeApp) openKnowledgeNew()   { a.knowledge.OpenNew(a) }
func (a *nativeApp) cancelKnowledgeNew() { a.knowledge.CancelNew() }
func (a *nativeApp) knowledgeMCPEntry(id string) (mcp.ServerView, bool) {
	return a.knowledge.MCPEntry(id)
}
func (a *nativeApp) knowledgeMutationBlocked() bool                  { return a.knowledge.MutationBlocked() }
func (a *nativeApp) setKnowledgeBusy(key, action string)             { a.knowledge.SetBusy(key, action) }
func (a *nativeApp) clearKnowledgeBusy(key string)                   { a.knowledge.ClearBusy(key) }
func (a *nativeApp) knowledgeViewByID(id string) *knowledge.BaseView { return a.knowledge.ViewByID(id) }
func (a *nativeApp) applyKnowledgeView(view knowledge.BaseView)      { a.knowledge.ApplyView(view) }
func (a *nativeApp) removeKnowledgeView(id string)                   { a.knowledge.RemoveView(id) }
func (a *nativeApp) saveKnowledgeBase(id string)                     { a.knowledge.Save(a, id) }
func (a *nativeApp) scanKnowledgeBase(id string)                     { a.knowledge.Scan(a, id) }
func (a *nativeApp) refreshKnowledgeBase(id string)                  { a.knowledge.Refresh(a, id) }
func (a *nativeApp) requestDeleteKnowledgeBase(id string)            { a.knowledge.RequestDelete(id) }
func (a *nativeApp) cancelDeleteKnowledgeBase()                      { a.knowledge.CancelDelete() }
func (a *nativeApp) confirmDeleteKnowledgeBase()                     { a.knowledge.ConfirmDelete(a) }
func (a *nativeApp) requestClearKnowledge(id string)                 { a.knowledge.RequestClear(id) }
func (a *nativeApp) cancelClearKnowledge()                           { a.knowledge.CancelClear() }
func (a *nativeApp) confirmClearKnowledge()                          { a.knowledge.ConfirmClear(a) }
func (a *nativeApp) toggleKnowledgeSchedule(id string)               { a.knowledge.ToggleSchedule(a, id) }
func (a *nativeApp) applyKnowledgeMCP(id string)                     { a.knowledge.ApplyMCP(a, id) }
func (a *nativeApp) runKnowledgeQuery(id string)                     { a.knowledge.RunQuery(a, id) }
func (a *nativeApp) loadKnowledgeRunDetail(id, runID string)         { a.knowledge.LoadRunDetail(a, id, runID) }
func (a *nativeApp) startKnowledgePolling()                          { a.knowledge.StartPolling(a) }
func (a *nativeApp) knowledgeChooseRoot(key string)                  { a.knowledge.ChooseRoot(a, key) }
func (a *nativeApp) settingsKnowledgePane(c *ui.Context, p palette)  { a.knowledge.View(a, c, p) }

func (a *nativeApp) knowledgeErrorCard(c *ui.Context, p palette, message string) {
	a.knowledge.ErrCard(a, c, p, message)
}
func (a *nativeApp) knowledgeStatusBanner(c *ui.Context, p palette) {
	a.knowledge.StatusBanner(a, c, p)
}
func (a *nativeApp) settingsKnowledgeDialogs(c *ui.Context, p palette) {
	a.knowledge.Dialogs(a, c, p)
}
