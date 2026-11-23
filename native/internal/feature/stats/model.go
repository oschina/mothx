// Package stats owns the Native projection of the ACP usage-stats contract
// (mothx/manage/stats/summary and mothx/manage/stats/timeseries).
//
// The server resolves sessions.db read-only and owns the aggregation; this
// panel only selects a window/group, decodes the projection, and renders it.
// Cost stays whatever the shared stats schema reports (currently 0 because
// request_stats does not record pricing).
package stats

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

const (
	DefaultDays = 14
	MinDays     = 7
	MaxDays     = 90
)

var GroupOptions = []string{"day", "1h", "week", "month"}

type TokensView struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Total  int64 `json:"total"`
}

type SummaryView struct {
	Sessions int        `json:"sessions"`
	Runs     int        `json:"runs"`
	Tokens   TokensView `json:"tokens"`
	Cost     float64    `json:"cost"`
	Since    string     `json:"since,omitempty"`
	Until    string     `json:"until,omitempty"`
}

type PointView struct {
	Date   string  `json:"date"`
	Runs   int     `json:"runs"`
	Tokens int64   `json:"tokens"`
	Cost   float64 `json:"cost"`
}

type TimeseriesView struct {
	Group  string      `json:"group"`
	Points []PointView `json:"points"`
	From   string      `json:"from,omitempty"`
	To     string      `json:"to,omitempty"`
}

type State struct {
	Summary SummaryView
	Points  []PointView
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

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{Group: "day", Days: DefaultDays}}
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageStats")
}

func (p *Panel) Current(h host.Base, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "stats" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Summary = SummaryView{}
	p.Points = nil
	p.Group = "day"
	p.Days = DefaultDays
	p.Loaded = false
	p.Loading = false
	p.Err = ""
	p.Status = ""
}

// NormalizeDays keeps a requested window inside the supported range. It is
// used both by the UI buttons and request builders so a stale bound can never
// widen the query unexpectedly.
func NormalizeDays(days int) int {
	if days < MinDays {
		return MinDays
	}
	if days > MaxDays {
		return MaxDays
	}
	return days
}

func NormalizeGroup(group string) string {
	group = strings.TrimSpace(group)
	for _, option := range GroupOptions {
		if group == option {
			return group
		}
	}
	return "day"
}

// WindowFrom formats the inclusive window start the ACP stats endpoints accept
// (YYYY-MM-DD). Day math happens in the caller's local timezone so the chart
// matches what the user calls "today".
func WindowFrom(days int, now time.Time) string {
	return now.AddDate(0, 0, -NormalizeDays(days)).Format("2006-01-02")
}

func BuildSummaryRequest(days int, now time.Time) map[string]any {
	return map[string]any{"from": WindowFrom(days, now)}
}

func BuildTimeseriesRequest(days int, group string, now time.Time) map[string]any {
	return map[string]any{
		"from":  WindowFrom(days, now),
		"group": NormalizeGroup(group),
	}
}

func (p *Panel) StartLoad(h host.Base) {
	if !h.SettingsOpen() || h.SettingsTab() != "stats" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持使用统计（manageStats）。"
		return
	}
	p.Days = NormalizeDays(p.Days)
	p.Group = NormalizeGroup(p.Group)
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = "正在读取使用统计…"
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq, p.Days, p.Group)
}

// Reload re-reads the current window/group after a control change.
func (p *Panel) Reload(h host.Base) {
	if !h.SettingsOpen() || h.SettingsTab() != "stats" || !p.Capable(h) {
		return
	}
	if p.Loading {
		return
	}
	p.StartLoad(h)
}

// SetWindow selects a window and reloads. Invalid values fall back to the
// current window instead of silently widening the query.
func (p *Panel) SetWindow(h host.Base, days int) {
	next := NormalizeDays(days)
	if next == p.Days && p.Loaded {
		return
	}
	p.Days = next
	p.Reload(h)
}

func (p *Panel) SetGroup(h host.Base, group string) {
	next := NormalizeGroup(group)
	if next == p.Group && p.Loaded {
		return
	}
	p.Group = next
	p.Reload(h)
}

func (p *Panel) load(h host.Base, seq, startReloadSeq int64, days int, group string) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Status = ""
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	now := time.Now()
	rawSummary, summaryErr := client.Call("mothx/manage/stats/summary", BuildSummaryRequest(days, now))
	rawSeries, seriesErr := client.Call("mothx/manage/stats/timeseries", BuildTimeseriesRequest(days, group, now))
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		p.Status = ""
		if summaryErr != nil {
			p.Err = "读取使用统计失败：" + summaryErr.Error()
			return
		}
		var summary SummaryView
		if err := json.Unmarshal(rawSummary, &summary); err != nil {
			p.Err = "使用统计汇总响应无效：" + err.Error()
			return
		}
		if seriesErr != nil {
			p.Err = "读取用量趋势失败：" + seriesErr.Error()
			return
		}
		var series TimeseriesView
		if err := json.Unmarshal(rawSeries, &series); err != nil {
			p.Err = "用量趋势响应无效：" + err.Error()
			return
		}
		p.ApplyViews(&summary, &series)
	})
}

func (p *Panel) ApplyViews(summary *SummaryView, series *TimeseriesView) {
	if summary == nil || series == nil {
		return
	}
	p.Summary = *summary
	points := append([]PointView(nil), series.Points...)
	p.Points = points
	if group := strings.TrimSpace(series.Group); group != "" {
		p.Group = NormalizeGroup(group)
	}
	p.Loaded = true
	p.Loading = false
	p.Err = ""
	p.Status = ""
}

// TokensTotal prefers the server total and falls back to input+output so a
// partially populated projection still renders truthfully.
func TokensTotal(tokens TokensView) int64 {
	if tokens.Total > 0 {
		return tokens.Total
	}
	return tokens.Input + tokens.Output
}

// MaxRuns is the bar-chart scale. A window with no runs still keeps one unit so
// every bar renders at the minimum height.
func MaxRuns(points []PointView) int {
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

// BarHeight maps one bucket onto the 78px chart area.
func BarHeight(runs, max int) float32 {
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

func WindowLabel(days int) string {
	return i18n.Tf("近 %d 天", NormalizeDays(days))
}
