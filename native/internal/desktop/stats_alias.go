package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/stats"
)

type statsTokensView = stats.TokensView
type statsSummaryView = stats.SummaryView
type statsPointView = stats.PointView
type statsTimeseriesView = stats.TimeseriesView

const (
	statsDefaultDays = stats.DefaultDays
	statsMinDays     = stats.MinDays
	statsMaxDays     = stats.MaxDays
)

var statsGroupOptions = stats.GroupOptions

func normalizeStatsDays(days int) int                { return stats.NormalizeDays(days) }
func normalizeStatsGroup(group string) string        { return stats.NormalizeGroup(group) }
func statsWindowFrom(days int, now time.Time) string { return stats.WindowFrom(days, now) }
func buildStatsSummaryRequest(days int, now time.Time) map[string]any {
	return stats.BuildSummaryRequest(days, now)
}
func buildStatsTimeseriesRequest(days int, group string, now time.Time) map[string]any {
	return stats.BuildTimeseriesRequest(days, group, now)
}
func statsTokensTotal(tokens stats.TokensView) int64 { return stats.TokensTotal(tokens) }
func statsMaxRuns(points []stats.PointView) int      { return stats.MaxRuns(points) }
func statsBarHeight(runs, max int) float32           { return stats.BarHeight(runs, max) }
func statsWindowLabel(days int) string               { return stats.WindowLabel(days) }
func statsGroupLabel(group string) string            { return stats.GroupLabel(group) }
func formatTokenCount(value int64) string            { return stats.FormatTokenCount(value) }
func formatStatsCost(cost float64) string            { return stats.FormatCost(cost) }

func (a *nativeApp) statsCapable() bool {
	return a.stats.Capable(a)
}

func (a *nativeApp) statsPanelCurrent(seq, reloadSeq int64) bool {
	return a.stats.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateStatsPanel() {
	a.stats.Invalidate()
}

func (a *nativeApp) startStatsPanelLoad() {
	a.stats.StartLoad(a)
}

func (a *nativeApp) reloadStatsPanel() {
	a.stats.Reload(a)
}

func (a *nativeApp) setStatsWindow(days int) {
	a.stats.SetWindow(a, days)
}

func (a *nativeApp) setStatsGroup(group string) {
	a.stats.SetGroup(a, group)
}

func (a *nativeApp) applyStatsViews(summary *stats.SummaryView, series *stats.TimeseriesView) {
	a.stats.ApplyViews(summary, series)
}

func (a *nativeApp) settingsStatsPane(c *ui.Context, p palette) {
	a.stats.View(a, c, p)
}
