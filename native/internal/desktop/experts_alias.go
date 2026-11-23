package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/experts"
)

type expertLocalizedText = experts.LocalizedText
type expertMemberMeta = experts.MemberMeta
type expertTeamInfo = experts.TeamInfo
type expertManifest = experts.Manifest
type expertBundle = experts.Bundle
type expertSummary = experts.Summary
type expertListView = experts.ListView
type expertMutationView = experts.MutationView
type expertMemberDraft = experts.MemberDraft
type expertAgentDraft = experts.AgentDraft
type expertDraft = experts.Draft
type expertsPanelState = experts.State

const (
	expertScopeGlobal  = experts.ScopeGlobal
	expertScopeProject = experts.ScopeProject

	expertSourceBuiltin = experts.SourceBuiltin

	expertTypeAgent = experts.TypeAgent
	expertTypeTeam  = experts.TypeTeam

	expertRoleLead   = experts.RoleLead
	expertRoleMember = experts.RoleMember
)

var (
	expertTypes = experts.Types
	expertRoles = experts.Roles
)

func newExpertsPanelState() experts.Panel { return experts.NewPanel() }

func expertsWorkspaceCwd(workspace, draftCwd string) string {
	return experts.WorkspaceCwd(workspace, draftCwd)
}
func expertRequestScoped(scope, cwd string, extra map[string]any) map[string]any {
	return experts.RequestScoped(scope, cwd, extra)
}
func buildExpertsListRequest(scope, cwd string) map[string]any {
	return experts.BuildListRequest(scope, cwd)
}
func buildExpertGetRequest(scope, cwd, name string) map[string]any {
	return experts.BuildGetRequest(scope, cwd, name)
}
func buildExpertSaveRequest(scope, cwd string, bundle map[string]any) map[string]any {
	return experts.BuildSaveRequest(scope, cwd, bundle)
}
func buildExpertDeleteRequest(scope, cwd, name string) map[string]any {
	return experts.BuildDeleteRequest(scope, cwd, name)
}
func expertListItems(view *experts.ListView) []experts.Summary {
	return experts.ListItems(view)
}
func expertDisplayName(item experts.Summary) string { return experts.DisplayName(item) }
func expertSourceLabel(source string) string        { return experts.SourceLabel(source) }
func expertScopeLabel(scope string) string          { return experts.ScopeLabel(scope) }
func expertSummaryDescription(item experts.Summary) string {
	return experts.SummaryDescription(item)
}
func expertCanEdit(item experts.Summary, scope string) bool { return experts.CanEdit(item, scope) }
func expertTypeLabel(kind string) string                    { return experts.TypeLabel(kind) }
func expertDraftFromBundle(bundle experts.Bundle) *experts.Draft {
	return experts.DraftFromBundle(bundle)
}
func newExpertDraft(scope string) *experts.Draft { return experts.NewDraft(scope) }
func expertDraftBundle(draft *experts.Draft) (map[string]any, error) {
	return experts.DraftBundle(draft)
}
func expertScopeHint(item experts.Summary, scope string) string {
	return experts.ScopeHint(item, scope)
}
func expertFieldKey(draftKey, field string) string { return experts.FieldKey(draftKey, field) }

func (a *nativeApp) expertsWorkspace() string { return a.ExpertsWorkspace() }

func (a *nativeApp) expertsCapable() bool { return a.experts.Capable(a) }

func (a *nativeApp) expertsRequestCwd() string { return a.experts.RequestCwd(a) }

func (a *nativeApp) expertsPanelCurrent(seq, reloadSeq int64) bool {
	return a.experts.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateExpertsPanel() { a.experts.Invalidate() }

func (a *nativeApp) startExpertsPanelLoad() { a.experts.StartLoad(a) }

func (a *nativeApp) applyExpertsListView(view *experts.ListView) {
	a.experts.ApplyListView(a, view)
}

func (a *nativeApp) setExpertScope(scope string) { a.experts.SetScope(a, scope) }

func (a *nativeApp) openExpertEditor(name string) { a.experts.OpenEditor(a, name) }

func (a *nativeApp) saveExpertDraft() { a.experts.Save(a) }

func (a *nativeApp) requestDeleteExpert(name string) { a.experts.RequestDelete(name) }

func (a *nativeApp) cancelDeleteExpert() { a.experts.CancelDelete() }

func (a *nativeApp) confirmDeleteExpert() { a.experts.ConfirmDelete(a) }

func (a *nativeApp) addExpertMember()         { a.experts.AddMember() }
func (a *nativeApp) removeExpertMember(i int) { a.experts.RemoveMember(i) }
func (a *nativeApp) addExpertAgent()          { a.experts.AddAgent() }
func (a *nativeApp) removeExpertAgent(i int)  { a.experts.RemoveAgent(i) }
func (a *nativeApp) startNewExpert()          { a.experts.StartNew(a) }
func (a *nativeApp) cancelExpertEditor()      { a.experts.CancelEditor() }

func (a *nativeApp) expertSummaryByName(name string) *experts.Summary {
	return a.experts.SummaryByName(name)
}

func (a *nativeApp) settingsExpertsPane(c *ui.Context, p palette) {
	a.experts.View(a, c, p)
}

func (a *nativeApp) settingsExpertsDialogs(c *ui.Context, p palette) {
	a.experts.Dialogs(a, c, p)
}
