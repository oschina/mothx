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
type servePanelState = serve.State
type serveNumbers = serve.Numbers

func newServeForm() serve.Form                       { return serve.NewForm() }
func serveIntString(value *int, fallback int) string { return serve.IntString(value, fallback) }
func serveFloatString(value *float64, fallback float64) string {
	return serve.FloatString(value, fallback)
}
func serveFormFromView(view *serve.ConfigView) serve.Form { return serve.FormFromView(view) }
func serveOptionAllowed(options []string, value string) bool {
	return serve.OptionAllowed(options, value)
}
func serveParseInt(label, raw string, min int) (int, error) {
	return serve.ParseInt(label, raw, min)
}
func serveParseFloat(label, raw string, min, max float64) (float64, error) {
	return serve.ParseFloat(label, raw, min, max)
}
func parseServeForm(form *serve.Form) (serve.Numbers, error)  { return serve.ParseForm(form) }
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
