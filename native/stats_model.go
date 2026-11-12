package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"
)

// This file owns the Native projection of the ACP usage-stats contract
// (mothx/manage/stats/summary and mothx/manage/stats/timeseries).
//
// The server resolves sessions.db read-only and owns the aggregation; this
// panel only selects a window/group, decodes the projection, and renders it.
// Cost stays whatever the shared stats schema reports (currently 0 because
// request_stats does not record pricing).

const (
	statsDefaultDays = 14
	statsMinDays     = 7
	statsMaxDays     = 90
)

var statsGroupOptions = []string{"day", "1h", "week", "month"}

type statsTokensView struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Total  int64 `json:"total"`
}

type statsSummaryView struct {
	Sessions int             `json:"sessions"`
	Runs     int             `json:"runs"`
	Tokens   statsTokensView `json:"tokens"`
	Cost     float64         `json:"cost"`
	Since    string          `json:"since,omitempty"`
	Until    string          `json:"until,omitempty"`
}

type statsPointView struct {
	Date   string  `json:"date"`
	Runs   int     `json:"runs"`
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost"`
}

type statsTimeseriesView struct {
	Group  string           `json:"group"`
	Points []statsPointView `json:"points"`
	From   string           `json:"from,omitempty"`
	To     string           `json:"to,omitempty"`
}

type statsPanelState struct {
	Summary statsSummaryView
	Points  []statsPointView
	Group   string
	Days    int

	Loaded  bool
	Loading bool
	Err     string
	Status  string

	// Seq guards every asynchronous stats call. Bumping it invalidates any
	// in-flight summary/timeseries response, which is how tab exits, settings
	// close, capability fallback, window changes, and reconnects prevent stale
	// state from landing.
	Seq int64
}

func newStatsPanelState() statsPanelState {
	return statsPanelState{Group: "day", Days: statsDefaultDays}
}

func (a *nativeApp) statsCapable() bool {
	return a.featureEnabled("manageStats")
}

func (a *nativeApp) statsPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "stats" &&
		seq == atomic.LoadInt64(&a.stats.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) invalidateStatsPanel() {
	p := &a.stats
	atomic.AddInt64(&p.Seq, 1)
	p.Summary = statsSummaryView{}
	p.Points = nil
	p.Group = "day"
	p.Days = statsDefaultDays
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Status = ""
}

// normalizeStatsDays keeps a requested window inside the supported range. It
// is used both by the UI buttons and request builders so a stale bound can
// never widen the query unexpectedly.
func normalizeStatsDays(days int) int {
	if days < statsMinDays {
		return statsMinDays
	}
	if days > statsMaxDays {
		return statsMaxDays
	}
	return days
}

func normalizeStatsGroup(group string) string {
	group = strings.TrimSpace(group)
	for _, option := range statsGroupOptions {
		if group == option {
			return group
		}
	}
	return "day"
}

// statsWindowFrom formats the inclusive window start the ACP stats endpoints
// accept (YYYY-MM-DD). Day math happens in the caller's local timezone so the
// chart matches what the user calls "today".
func statsWindowFrom(days int, now time.Time) string {
	return now.AddDate(0, 0, -normalizeStatsDays(days)).Format("2006-01-02")
}

func buildStatsSummaryRequest(days int, now time.Time) map[string]any {
	return map[string]any{"from": statsWindowFrom(days, now)}
}

func buildStatsTimeseriesRequest(days int, group string, now time.Time) map[string]any {
	return map[string]any{
		"from":  statsWindowFrom(days, now),
		"group": normalizeStatsGroup(group),
	}
}

func (a *nativeApp) startStatsPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "stats" {
		return
	}
	p := &a.stats
	if p.Loading {
		return
	}
	if !a.statsCapable() {
		p.Loaded = true
		p.Err = "当前运行时不支持使用统计（manageStats）。"
		return
	}
	p.Days = normalizeStatsDays(p.Days)
	p.Group = normalizeStatsGroup(p.Group)
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = "正在读取使用统计…"
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadStatsPanel(seq, startReloadSeq, p.Days, p.Group)
}

// reloadStatsPanel re-reads the current window/group after a control change.
func (a *nativeApp) reloadStatsPanel() {
	p := &a.stats
	if !a.settingsOpen || a.settingsTab != "stats" || !a.statsCapable() {
		return
	}
	if p.Loading {
		return
	}
	a.startStatsPanelLoad()
}

// setStatsWindow selects a window and reloads. Invalid values fall back to the
// current window instead of silently widening the query.
func (a *nativeApp) setStatsWindow(days int) {
	p := &a.stats
	next := normalizeStatsDays(days)
	if next == p.Days && p.Loaded {
		return
	}
	p.Days = next
	a.reloadStatsPanel()
}

func (a *nativeApp) setStatsGroup(group string) {
	p := &a.stats
	next := normalizeStatsGroup(group)
	if next == p.Group && p.Loaded {
		return
	}
	p.Group = next
	a.reloadStatsPanel()
}

func (a *nativeApp) loadStatsPanel(seq, startReloadSeq int64, days int, group string) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.statsPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.stats.Loading = false
			a.stats.Status = ""
			a.stats.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	now := time.Now()
	rawSummary, summaryErr := client.call("mothx/manage/stats/summary", buildStatsSummaryRequest(days, now))
	rawSeries, seriesErr := client.call("mothx/manage/stats/timeseries", buildStatsTimeseriesRequest(days, group, now))
	a.dispatch(func() {
		if !a.statsPanelCurrent(seq, startReloadSeq) {
			return
		}
		p := &a.stats
		p.Loading = false
		p.Status = ""
		if summaryErr != nil {
			p.Err = "读取使用统计失败：" + summaryErr.Error()
			return
		}
		var summary statsSummaryView
		if err := json.Unmarshal(rawSummary, &summary); err != nil {
			p.Err = "使用统计汇总响应无效：" + err.Error()
			return
		}
		if seriesErr != nil {
			p.Err = "读取用量趋势失败：" + seriesErr.Error()
			return
		}
		var series statsTimeseriesView
		if err := json.Unmarshal(rawSeries, &series); err != nil {
			p.Err = "用量趋势响应无效：" + err.Error()
			return
		}
		a.applyStatsViews(&summary, &series)
	})
}

func (a *nativeApp) applyStatsViews(summary *statsSummaryView, series *statsTimeseriesView) {
	if summary == nil || series == nil {
		return
	}
	p := &a.stats
	p.Summary = *summary
	points := append([]statsPointView(nil), series.Points...)
	p.Points = points
	if group := strings.TrimSpace(series.Group); group != "" {
		p.Group = normalizeStatsGroup(group)
	}
	p.Loaded = true
	p.Loading = false
	p.Err = ""
	p.Status = ""
}

// statsTokensTotal prefers the server total and falls back to input+output so a
// partially populated projection still renders truthfully.
func statsTokensTotal(tokens statsTokensView) int64 {
	if tokens.Total > 0 {
		return tokens.Total
	}
	return tokens.Input + tokens.Output
}

// statsMaxRuns is the bar-chart scale. A window with no runs still keeps one
// unit so every bar renders at the minimum height.
func statsMaxRuns(points []statsPointView) int {
	max := 0
	for _, point := range points {
		if point.Runs > max {
			max = point.Runs
		}
	}
	if max < 1 {
		return 1
	}
	return max
}

// statsBarHeight maps one bucket onto the 78px chart area.
func statsBarHeight(runs, max int) float32 {
	if max < 1 {
		max = 1
	}
	height := float32(runs) / float32(max) * 70
	if height < 4 {
		height = 4
	}
	if height > 74 {
		height = 74
	}
	return height
}

func statsWindowLabel(days int) string {
	return Tf("近 %d 天", normalizeStatsDays(days))
}
