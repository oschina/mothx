package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/serve"
)

type serveToolVisibilityView = serve.ToolVisibilityView
type serveAPISessionView = serve.APISessionView
type serveAPIView = serve.APIView
type serveFeaturesView = serve.FeaturesView
type serveWebUIView = serve.WebUIView
type serveCronView = serve.CronView
type serveMemoryView = serve.MemoryView
type serveSecurityView = serve.SecurityView
type serveAgentView = serve.AgentView
type serveConfigView = serve.ConfigView
type serveForm = serve.Form

func serveFormFromView(view *serve.ConfigView) serve.Form { return serve.FormFromView(view) }
func serveParseInt(label, raw string, min int) (int, error) {
	return serve.ParseInt(label, raw, min)
}
func validateServeForm(form *serve.Form) error                { return serve.ValidateForm(form) }
func buildServePatch(form serve.Form) (map[string]any, error) { return serve.BuildPatch(form) }

func (a *nativeApp) serveCapable() bool {
	return a.serve.Capable(a)
}

func (a *nativeApp) servePanelCurrent(seq, reloadSeq int64) bool {
	return a.serve.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateServePanel() {
	a.serve.Invalidate()
}

func (a *nativeApp) startServePanelLoad() {
	a.serve.StartLoad(a)
}

func (a *nativeApp) applyServeView(view *serve.ConfigView) {
	a.serve.ApplyView(view)
}

func (a *nativeApp) saveServeSettings() {
	a.serve.Save(a)
}

func (a *nativeApp) settingsServePane(c *ui.Context, p palette) {
	a.serve.View(a, c, p)
}
