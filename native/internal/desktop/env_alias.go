package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/env"
)

// The env panel is owned by the private internal/feature/env package. These
// aliases preserve the composition-shell vocabulary used by tests and cross
// panel wiring while the implementation stays business-local.
type envVariableView = env.VariableView
type envView = env.View
type envDraft = env.Draft

func (a *nativeApp) envCapable() bool {
	return a.env.Capable(a)
}

func (a *nativeApp) invalidateEnvPanel() {
	a.env.Invalidate()
}

func (a *nativeApp) startEnvPanelLoad() {
	a.env.StartLoad(a)
}

func (a *nativeApp) addEnvVariable() {
	a.env.Add()
}

func (a *nativeApp) removeEnvVariable(name string) {
	a.env.Remove(name)
}

func (a *nativeApp) saveEnvChanges() {
	a.env.SaveChanges(a)
}

func (a *nativeApp) settingsEnvPane(c *ui.Context, p palette) {
	a.env.View(a, c, p)
}

func buildEnvPatchRequest(drafts map[string]*env.Draft) (map[string]any, error) {
	return env.BuildPatchRequest(drafts)
}

func envSummaryLabel(count int) string {
	return env.SummaryLabel(count)
}

func (a *nativeApp) applyEnvView(view *env.View) {
	a.env.ApplyView(view)
}
