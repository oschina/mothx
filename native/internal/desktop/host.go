package desktop

import (
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/skillhub"
	"github.com/oschina/mothx/native/internal/kit"
)

// The exported host methods below are the narrow surface private business
// packages implement against. They do not change shell ownership; they only
// make the existing services callable across the package boundary.

func (a *nativeApp) Dispatch(fn func()) {
	a.dispatch(fn)
}

func (a *nativeApp) FeatureEnabled(name string) bool {
	return a.featureEnabled(name)
}

func (a *nativeApp) client() *acpClient {
	return a.acpPtr.Load()
}

func (a *nativeApp) setClient(client *acpClient) {
	a.acpPtr.Store(client)
}

func (a *nativeApp) ACP() *acp.Client {
	return a.client()
}

func (a *nativeApp) SettingsOpen() bool {
	return a.settingsOpen
}

func (a *nativeApp) SettingsTab() string {
	return a.settingsTab
}

func (a *nativeApp) ReloadSequence() int64 {
	return atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) CurrentSession() string {
	return a.currentSession
}

func (a *nativeApp) PanelVisible(page, tab string) bool {
	return a.panelVisible(page, tab)
}

func (a *nativeApp) ExpertsWorkspace() string {
	return expertsWorkspaceCwd(a.workspace, a.draftCwd)
}

func (a *nativeApp) Page() string {
	return a.page
}

func (a *nativeApp) StartNewTask() {
	a.startNewTask()
}

func (a *nativeApp) OpenDirectory(title string, onPick func(string)) {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            win,
			Title:             T(title),
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		onPick(paths[0])
	}()
}

func (a *nativeApp) SkillIconElement(c *ui.Context, pal kit.Palette, skill skillhub.Skill, size float32) {
	a.skillIconElement(c, pal, skill, size)
}
