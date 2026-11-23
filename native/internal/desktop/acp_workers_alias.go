package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/acpworkers"
)

type acpWorkerProfileView = acpworkers.ProfileView
type acpWorkerCatalogView = acpworkers.CatalogView
type acpWorkerDraft = acpworkers.Draft

func acpWorkerDraftFromProfile(profile acpworkers.ProfileView) acpworkers.Draft {
	return acpworkers.DraftFromProfile(profile)
}

func buildACPWorkerPatchRequest(profile acpworkers.ProfileView, draft acpworkers.Draft) (map[string]any, error) {
	return acpworkers.BuildPatchRequest(profile, draft)
}

func (a *nativeApp) acpWorkersCapable() bool {
	return a.acpWorkers.Capable(a)
}

func (a *nativeApp) acpWorkersPanelCurrent(seq, reloadSeq int64) bool {
	return a.acpWorkers.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateACPWorkersPanel() {
	a.acpWorkers.Invalidate()
}

func (a *nativeApp) startACPWorkersPanelLoad() {
	a.acpWorkers.StartLoad(a)
}

func (a *nativeApp) applyACPWorkerCatalog(view *acpworkers.CatalogView) {
	a.acpWorkers.ApplyCatalog(view)
}

func (a *nativeApp) applyACPWorkerProfiles(profiles []acpworkers.ProfileView) {
	a.acpWorkers.ApplyProfiles(profiles)
}

func (a *nativeApp) acpWorkerDraft(id string) (acpworkers.Draft, bool) {
	return a.acpWorkers.Draft(id)
}

func (a *nativeApp) saveACPWorkerProfile(id string) {
	a.acpWorkers.SaveProfile(a, id)
}

func (a *nativeApp) settingsACPWorkersPane(c *ui.Context, p palette) {
	a.acpWorkers.View(a, c, p)
}
