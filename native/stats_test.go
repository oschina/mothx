package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func statsTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{"manageStats": true})
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "stats"
	a.ensureSettingsTabVisible()
	return a
}

func TestStatsWindowAndGroupNormalization(t *testing.T) {
	if got := normalizeStatsDays(0); got != statsMinDays {
		t.Fatalf("zero days = %d", got)
	}
	if got := normalizeStatsDays(1000); got != statsMaxDays {
		t.Fatalf("oversized days = %d", got)
	}
	if got := normalizeStatsDays(30); got != 30 {
		t.Fatalf("valid days = %d", got)
	}
	if got := normalizeStatsGroup("week"); got != "week" {
		t.Fatalf("valid group = %q", got)
	}
	if got := normalizeStatsGroup("year"); got != "day" {
		t.Fatalf("invalid group = %q", got)
	}

	now := time.Date(2026, 11, 11, 15, 4, 5, 0, time.UTC)
	if got := statsWindowFrom(14, now); got != "2026-10-28" {
		t.Fatalf("window from = %q", got)
	}
	summary := buildStatsSummaryRequest(7, now)
	if summary["from"] != "2026-11-04" {
		t.Fatalf("summary request = %#v", summary)
	}
	series := buildStatsTimeseriesRequest(30, "week", now)
	if series["from"] != "2026-10-12" || series["group"] != "week" {
		t.Fatalf("timeseries request = %#v", series)
	}
}

func TestStatsViewsAggregate(t *testing.T) {
	summary := statsSummaryView{
		Sessions: 3,
		Runs:     9,
		Tokens:   statsTokensView{Input: 1200, Output: 800},
		Cost:     0,
		Since:    "2026-10-28T00:00:00Z",
	}
	series := statsTimeseriesView{
		Group: "day",
		Points: []statsPointView{
			{Date: "2026-11-09", Runs: 0, Tokens: 0},
			{Date: "2026-11-10", Runs: 4, Tokens: 500},
			{Date: "2026-11-11", Runs: 2, Tokens: 300},
		},
		From: "2026-10-28T00:00:00Z",
	}
	a := statsTestApp(t)
	a.applyStatsViews(&summary, &series)

	if !a.stats.Loaded || a.stats.Loading || a.stats.Err != "" {
		t.Fatalf("loaded state = %+v", a.stats)
	}
	if a.stats.Summary.Sessions != 3 || a.stats.Summary.Runs != 9 {
		t.Fatalf("summary = %+v", a.stats.Summary)
	}
	if got := statsTokensTotal(a.stats.Summary.Tokens); got != 2000 {
		t.Fatalf("token total = %d", got)
	}
	if got := statsMaxRuns(a.stats.Points); got != 4 {
		t.Fatalf("max runs = %d", got)
	}
	if got := statsBarHeight(0, 4); got != 4 {
		t.Fatalf("zero bucket height = %v", got)
	}
	if got := statsBarHeight(4, 4); got != 70 {
		t.Fatalf("max bucket height = %v", got)
	}
	if got := statsBarHeight(2, 4); got != 35 {
		t.Fatalf("half bucket height = %v", got)
	}
}

func TestStatsPanelRendersProjection(t *testing.T) {
	a := statsTestApp(t)
	summary := statsSummaryView{
		Sessions: 2,
		Runs:     5,
		Tokens:   statsTokensView{Input: 1500, Output: 500, Total: 2000},
	}
	series := statsTimeseriesView{
		Group:  "day",
		Points: []statsPointView{{Date: "2026-11-11", Runs: 5, Tokens: 2000}},
	}
	a.applyStatsViews(&summary, &series)

	tt := ui.NewTester(a.view, 1180, 900)
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"使用统计", "会话", "运行", "Tokens", "成本", "2.0K"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("stats panel missing %q: %s", want, texts)
		}
	}
}

func TestStatsUnsupportedIsCapabilityGated(t *testing.T) {
	a := testApp()
	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "system"
	a.settingsTab = "stats"
	a.setFeatures(map[string]bool{})
	a.ensureSettingsTabVisible()
	// Force the tab after capability fallback to exercise the loader's own
	// fail-closed branch: it must not issue any ACP call.
	a.settingsTab = "stats"
	a.startStatsPanelLoad()
	if !a.stats.Loaded || !strings.Contains(a.stats.Err, "manageStats") {
		t.Fatalf("unsupported stats state = %+v", a.stats)
	}
	if a.activeSettingsSection().ID == "stats" {
		t.Fatalf("stats must not be selectable without manageStats")
	}
}

func TestStatsPanelInvalidatesOnExit(t *testing.T) {
	a := statsTestApp(t)
	a.applyStatsViews(&statsSummaryView{Runs: 1}, &statsTimeseriesView{Group: "day", Points: []statsPointView{{Date: "x", Runs: 1}}})
	a.setSettingsCategory("appearance")
	if a.stats.Loaded || len(a.stats.Points) != 0 || a.stats.Group != "day" || a.stats.Days != statsDefaultDays {
		t.Fatalf("stats panel was not reset: %+v", a.stats)
	}
}
