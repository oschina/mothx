package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/mcp"
)

type mcpPair = mcp.Pair
type mcpServerView = mcp.ServerView
type mcpListView = mcp.ListView
type mcpPairDraft = mcp.PairDraft
type mcpServerDraft = mcp.ServerDraft

const (
	mcpScopeGlobal  = mcp.ScopeGlobal
	mcpScopeProject = mcp.ScopeProject
	mcpNewServerIdx = mcp.NewServerIdx
)

func mcpEmptyDraft() mcp.ServerDraft                         { return mcp.EmptyDraft() }
func mcpServerType(view mcp.ServerView) string               { return mcp.ServerType(view) }
func mcpServerEnabled(view mcp.ServerView) bool              { return mcp.ServerEnabled(view) }
func mcpDraftFromServer(view mcp.ServerView) mcp.ServerDraft { return mcp.DraftFromServer(view) }
func validateMCPServerDraft(draft *mcp.ServerDraft, servers []mcp.ServerView, editingIndex int) error {
	return mcp.ValidateServerDraft(draft, servers, editingIndex)
}
func mcpDraftPayload(draft *mcp.ServerDraft) map[string]any { return mcp.DraftPayload(draft) }
func buildMCPSetRequest(servers []mcp.ServerView, draft *mcp.ServerDraft, editingIndex int) (map[string]any, error) {
	return mcp.BuildSetRequest(servers, draft, editingIndex)
}
func buildMCPDeleteRequest(servers []mcp.ServerView, index int) (map[string]any, error) {
	return mcp.BuildDeleteRequest(servers, index)
}

func (a *nativeApp) mcpCapable() bool { return a.mcp.Capable(a) }
func (a *nativeApp) mcpPanelCurrent(seq, reloadSeq int64) bool {
	return a.mcp.Current(a, seq, reloadSeq)
}
func (a *nativeApp) invalidateMCPPanel()                         { a.mcp.Invalidate() }
func (a *nativeApp) startMCPPanelLoad()                          { a.mcp.StartLoad(a) }
func (a *nativeApp) mcpScopeParams() (map[string]any, error)     { return a.mcp.ScopeParams(a) }
func (a *nativeApp) openMCPNewDraft()                            { a.mcp.OpenNewDraft() }
func (a *nativeApp) openMCPEditDraft(index int)                  { a.mcp.OpenEditDraft(index) }
func (a *nativeApp) closeMCPEditor()                             { a.mcp.CloseEditor() }
func (a *nativeApp) saveMCPDraft()                               { a.mcp.Save(a) }
func (a *nativeApp) requestDeleteMCP(index int)                  { a.mcp.RequestDelete(index) }
func (a *nativeApp) confirmDeleteMCP()                           { a.mcp.ConfirmDelete(a) }
func (a *nativeApp) settingsMCPPane(c *ui.Context, p palette)    { a.mcp.View(a, c, p) }
func (a *nativeApp) settingsMCPDialogs(c *ui.Context, p palette) { a.mcp.Dialogs(a, c, p) }
