package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/knowledge"
	"github.com/oschina/mothx/native/internal/feature/providers"
)

type knowledgeBase = knowledge.Base
type knowledgeSnapshot = knowledge.Snapshot
type knowledgeBaseView = knowledge.BaseView
type knowledgeListView = knowledge.ListView
type knowledgeIndexRunView = knowledge.IndexRunView
type knowledgeSourceView = knowledge.SourceView
type knowledgeScheduleView = knowledge.ScheduleView
type knowledgeChunk = knowledge.Chunk
type knowledgeNode = knowledge.Node
type knowledgeGraphQuery = knowledge.GraphQuery
type knowledgeQueryResultView = knowledge.QueryResultView
type knowledgeBaseDefaults = knowledge.BaseDefaults
type knowledgeBaseDraft = knowledge.BaseDraft
type knowledgeInsightsState = knowledge.InsightsState

const (
	knowledgePollInterval = knowledge.PollInterval
	knowledgeNewDraftKey  = knowledge.NewDraftKey
)

var ()

func newKnowledgePanelState() knowledge.Panel { return knowledge.NewPanel() }
func knowledgeDraftFromBase(base knowledge.Base) *knowledge.BaseDraft {
	return knowledge.DraftFromBase(base)
}
func knowledgeModelChoices(models []providers.CatalogModel, provider string) []string {
	return knowledge.ModelChoices(models, provider)
}
func knowledgeDraftPayload(draft *knowledge.BaseDraft) map[string]any {
	return knowledge.DraftPayload(draft)
}
func validateKnowledgeDraft(draft *knowledge.BaseDraft) error  { return knowledge.ValidateDraft(draft) }
func knowledgeMCPName(id string) string                        { return knowledge.MCPName(id) }
func knowledgeStatusText(view knowledge.BaseView) string       { return knowledge.StatusText(view) }
func knowledgeStatsText(snapshot *knowledge.Snapshot) string   { return knowledge.StatsText(snapshot) }
func knowledgeScheduleText(view knowledge.ScheduleView) string { return knowledge.ScheduleText(view) }
func knowledgeFormatDate(value string) string                  { return knowledge.FormatDate(value) }
func knowledgeFormatSize(bytes int64) string                   { return knowledge.FormatSize(bytes) }
func knowledgeTruncate(text string, limit int) string          { return knowledge.Truncate(text, limit) }

func (a *nativeApp) knowledgeCapable() bool    { return a.knowledge.Capable(a) }
func (a *nativeApp) invalidateKnowledgePanel() { a.knowledge.Invalidate() }
func (a *nativeApp) startKnowledgePanelLoad()  { a.knowledge.StartLoad(a) }
func (a *nativeApp) normalizeKnowledgeDraft(draft *knowledge.BaseDraft) {
	a.knowledge.NormalizeDraft(draft)
}
func (a *nativeApp) cancelKnowledgeNew()                            { a.knowledge.CancelNew() }
func (a *nativeApp) settingsKnowledgePane(c *ui.Context, p palette) { a.knowledge.View(a, c, p) }

func (a *nativeApp) knowledgeErrorCard(c *ui.Context, p palette, message string) {
	a.knowledge.ErrCard(a, c, p, message)
}
func (a *nativeApp) knowledgeStatusBanner(c *ui.Context, p palette) {
	a.knowledge.StatusBanner(c, p)
}
func (a *nativeApp) settingsKnowledgeDialogs(c *ui.Context, p palette) {
	a.knowledge.Dialogs(a, c, p)
}
