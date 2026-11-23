package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/kit"
)

// Palette and shared widgets are owned by the private internal/kit package.
// The shell aliases them so panel code reads like UI composition instead of
// package wiring.
type palette = kit.Palette

var (
	iconButton            = kit.IconButton
	badge                 = kit.Badge
	statusPill            = kit.StatusPill
	planStepMarker        = kit.PlanStepMarker
	planProgressBar       = kit.PlanProgressBar
	copyButton            = kit.CopyButton
	formatThoughtDuration = kit.FormatThoughtDuration
	thoughtPreview        = kit.ThoughtPreview
	humanCount            = kit.HumanCount
	mediaLabel            = kit.MediaLabel
	humanBytes            = kit.HumanBytes
	relativeTime          = kit.RelativeTime
	truncateOneLine       = kit.TruncateOneLine
)

func (a *nativeApp) settingsCard(c *ui.Context, p palette, title, desc string, body func()) {
	kit.SettingsCard(c, p, title, desc, body)
}

func (a *nativeApp) settingsTextInput(c *ui.Context, p palette, key, label string, value *string) {
	kit.SettingsTextInput(c, p, key, label, value)
}

func (a *nativeApp) settingsTextArea(c *ui.Context, p palette, key, label string, value *string, lines int) {
	kit.SettingsTextArea(c, p, key, label, value, lines)
}

func (a *nativeApp) settingsSelect(c *ui.Context, p palette, key, label string, options []string, value *string) {
	kit.SettingsSelect(c, p, key, label, options, value)
}

func (a *nativeApp) settingsTokenInput(c *ui.Context, p palette, key, label string, value *string, configured bool, configuredText, unsetText string) {
	kit.SettingsTokenInput(c, p, key, label, value, configured, configuredText, unsetText)
}

func (a *nativeApp) pageHeader(c *ui.Context, p palette, title, subtitle string) {
	kit.PageHeader(c, p, title, subtitle)
}

func (a *nativeApp) pageContent(c *ui.Context, maxWidth float32, children func()) {
	kit.PageContent(c, maxWidth, children)
}

func (a *nativeApp) pageStat(c *ui.Context, p palette, label, value, hint string) {
	kit.PageStat(c, p, label, value, hint)
}
