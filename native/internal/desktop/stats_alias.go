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
func (a *nativeApp) invalidateStatsPanel() {
	a.stats.Invalidate()
}

func (a *nativeApp) startStatsPanelLoad() {
	a.stats.StartLoad(a)
}

func (a *nativeApp) applyStatsViews(summary *stats.SummaryView, series *stats.TimeseriesView) {
	a.stats.ApplyViews(summary, series)
}

func (a *nativeApp) settingsStatsPane(c *ui.Context, p palette) {
	a.stats.View(a, c, p)
}
