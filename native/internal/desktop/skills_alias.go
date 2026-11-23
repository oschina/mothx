package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/skills"
)

type skillView = skills.SkillView
type skillListView = skills.ListView
type skillsPanelState = skills.State

func skillEnabled(skill skills.SkillView) bool       { return skills.Enabled(skill) }
func skillName(skill skills.SkillView) string        { return skills.Name(skill) }
func skillDescription(skill skills.SkillView) string { return skills.Description(skill) }
func skillsRequestCwd(workspace, draftCwd, processCwd string) string {
	return skills.RequestCwd(workspace, draftCwd, processCwd)
}
func buildSkillsListRequest(cwd string) map[string]any { return skills.BuildListRequest(cwd) }
func buildSkillSetRequest(cwd, name string, enabled bool) map[string]any {
	return skills.BuildSetRequest(cwd, name, enabled)
}
func skillToggleStatus(name string, target bool) string { return skills.ToggleStatus(name, target) }

func (a *nativeApp) SelectedWorkspaceCwd() string {
	return skills.RequestCwd(a.workspace, a.draftCwd, a.cwd)
}

func (a *nativeApp) selectedWorkspaceCwd() string {
	return a.SelectedWorkspaceCwd()
}

func (a *nativeApp) skillsCapable() bool {
	return a.skills.Capable(a)
}

func (a *nativeApp) skillsPanelCurrent(seq, reloadSeq int64) bool {
	return a.skills.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateSkillsPanel() {
	a.skills.Invalidate()
}

func (a *nativeApp) startSkillsPanelLoad() {
	a.skills.StartLoad(a)
}

func (a *nativeApp) applySkillsListView(view *skills.ListView) {
	a.skills.ApplyListView(view, a.selectedWorkspaceCwd())
}

func (a *nativeApp) applySkillToggleLocally(name string, enabled bool) {
	a.skills.ApplyToggleLocally(name, enabled)
}

func (a *nativeApp) toggleSkill(index int) {
	a.skills.Toggle(a, index)
}

func (a *nativeApp) settingsSkillsPane(c *ui.Context, p palette) {
	a.skills.View(a, c, p)
}
