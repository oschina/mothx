package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/skillhub"
)

func TestParseSkillHubID(t *testing.T) {
	market, id, err := parseSkillHubID("clawhub.ai/openclaw/git")
	if err != nil || market != skillhub.MarketClawHub || id != "openclaw/git" {
		t.Fatalf("parseSkillHubID() = %q, %q, %v", market, id, err)
	}
	if _, _, err := parseSkillHubID("unknown/example"); err == nil {
		t.Fatal("expected unknown market error")
	}
}

func TestSkillHubCommandSearchOpensOverlay(t *testing.T) {
	a := NewApp(nil, nil, nil, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	if cmd := a.handleSkillHubCommand([]string{"/skillhub", "search", "go", "testing"}); cmd == nil {
		t.Fatal("search command did not return a load command")
	}
	if !a.skillHubOpen || a.skillHubView != skillHubSearch || a.skillHubQuery != "go testing" {
		t.Fatalf("unexpected marketplace state: open=%v view=%d query=%q", a.skillHubOpen, a.skillHubView, a.skillHubQuery)
	}
}

func TestSkillHubKeysChangeScopeAndClose(t *testing.T) {
	a := &App{skillHubOpen: true, skillHubScope: "project", skillHubMarket: skillhub.MarketSkillHub}
	a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if a.skillHubScope != "global" {
		t.Fatalf("scope = %q, want global", a.skillHubScope)
	}
	a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyEsc})
	if a.skillHubOpen {
		t.Fatal("Esc did not close SkillHub")
	}
}

func TestSkillHubConfiguredOfficialViewAndUnicodeSearch(t *testing.T) {
	a := &App{settings: &config.Settings{SkillHub: config.SkillHubSettings{OfficialHandles: []string{"official"}}}}
	if cmd := a.openSkillHub(); cmd == nil {
		t.Fatal("openSkillHub did not return a load command")
	}
	if a.skillHubView != skillHubOfficial {
		t.Fatalf("view = %d, want official", a.skillHubView)
	}
	a.skillHubLoading = false
	a.skillHubSearchFocused = true
	a.skillHubQuery = "测试"
	a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if a.skillHubQuery != "测" {
		t.Fatalf("query after backspace = %q", a.skillHubQuery)
	}
}

func TestSkillHubOfficialCursorStopsAndPagesWithLeftRight(t *testing.T) {
	a := &App{
		skillHub:         skillhub.NewService("", nil, []string{config.DefaultSkillHubOfficialHandle}),
		skillHubOpen:     true,
		skillHubMarket:   skillhub.MarketSkillHub,
		skillHubView:     skillHubOfficial,
		skillHubScope:    "project",
		skillHubResults:  []skillhub.SkillSummary{{ID: "one"}, {ID: "two"}},
		skillHubSelected: 1,
		skillHubPage:     1,
		width:            80,
		height:           24,
	}
	a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyDown})
	if a.skillHubSelected != 1 {
		t.Fatalf("cursor overflowed page: %d", a.skillHubSelected)
	}
	a.skillHubTotal = int64(a.skillHubPageSize() + 1)
	if cmd := a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyRight}); cmd == nil || a.skillHubPage != 2 {
		t.Fatalf("right page: cmd=%v page=%d", cmd != nil, a.skillHubPage)
	}
	a.skillHubLoading = false
	if cmd := a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyLeft}); cmd == nil || a.skillHubPage != 1 {
		t.Fatalf("left page: cmd=%v page=%d", cmd != nil, a.skillHubPage)
	}
	a.skillHubLoading = false
	if cmd := a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyLeft}); cmd != nil || a.skillHubPage != 1 {
		t.Fatalf("left crossed first page: cmd=%v page=%d", cmd != nil, a.skillHubPage)
	}
}

func TestSkillHubDownloadCountAndBadges(t *testing.T) {
	if got := formatSkillHubCount(1234567); got != "1,234,567" {
		t.Fatalf("formatted downloads = %q", got)
	}
	item := skillhub.SkillSummary{Market: skillhub.MarketSkillHub, Source: "enterprise", PublisherVerified: true, Verified: true, Suspicious: true}
	badges := skillHubBadges(item, true)
	want := []string{"official", "certified", "verified", "risk"}
	if len(badges) != len(want) {
		t.Fatalf("badges = %#v", badges)
	}
	for i := range want {
		if badges[i] != want[i] {
			t.Fatalf("badges = %#v", badges)
		}
	}
	item.Installed = &skillhub.InstalledState{Installed: true, UpdateAvailable: true}
	badges = skillHubBadges(item, false)
	if !strings.Contains(strings.Join(badges, ","), "installed,update") {
		t.Fatalf("installed update badges = %#v", badges)
	}
}

func TestSkillHubUpdateRequiresAvailableManagedInstall(t *testing.T) {
	a := &App{
		skillHub:         skillhub.NewService("", nil, nil),
		skillHubResults:  []skillhub.SkillSummary{{Market: skillhub.MarketSkillHub, ID: "go", Version: "2.0.0"}},
		skillHubSelected: 0,
	}
	if cmd := a.updateSelectedSkillHub(); cmd != nil || a.skillHubMessage != "Install the skill before updating it." {
		t.Fatalf("uninstalled update: cmd=%v message=%q", cmd != nil, a.skillHubMessage)
	}
	a.skillHubResults[0].Installed = &skillhub.InstalledState{Installed: true, Dir: "/tmp/go", Version: "2.0.0"}
	if cmd := a.updateSelectedSkillHub(); cmd != nil || a.skillHubMessage != "No update is available." {
		t.Fatalf("current update: cmd=%v message=%q", cmd != nil, a.skillHubMessage)
	}
	a.skillHubResults[0].Installed.UpdateAvailable = true
	if cmd := a.updateSelectedSkillHub(); cmd == nil || !a.skillHubInstalling {
		t.Fatalf("available update: cmd=%v installing=%v", cmd != nil, a.skillHubInstalling)
	}
}

func TestSkillHubLoadedMarksActiveManagedSkill(t *testing.T) {
	a := &App{
		skillHubOpen:   true,
		skillHubMarket: skillhub.MarketSkillHub,
		skillHubView:   skillHubBrowse,
		skillHubPage:   1,
		activeSkills:   map[string]string{"go": "context"},
	}
	a.handleSkillHubLoaded(skillHubLoadedMsg{
		market: skillhub.MarketSkillHub,
		view:   skillHubBrowse,
		pageNo: 1,
		page: skillhub.SearchPage{Items: []skillhub.SkillSummary{{
			ID: "go", Installed: &skillhub.InstalledState{Installed: true, Dir: "/tmp/go"},
		}}},
	})
	if !a.skillHubResults[0].Installed.Active {
		t.Fatalf("active state = %#v", a.skillHubResults[0].Installed)
	}
}

func TestClawHubCursorPagesForwardAndBack(t *testing.T) {
	a := &App{
		skillHub:           skillhub.NewService("", nil, nil),
		skillHubOpen:       true,
		skillHubMarket:     skillhub.MarketClawHub,
		skillHubView:       skillHubBrowse,
		skillHubScope:      "project",
		skillHubPage:       1,
		skillHubNextCursor: "cursor-2",
		width:              80,
		height:             24,
	}
	if cmd := a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyRight}); cmd == nil {
		t.Fatal("right did not request the next cursor page")
	}
	if a.skillHubCursor != "cursor-2" || a.skillHubPage != 2 || len(a.skillHubCursorHistory) != 1 {
		t.Fatalf("forward cursor state: cursor=%q page=%d history=%#v", a.skillHubCursor, a.skillHubPage, a.skillHubCursorHistory)
	}
	a.skillHubLoading = false
	if cmd := a.handleSkillHubKey(tea.KeyMsg{Type: tea.KeyLeft}); cmd == nil {
		t.Fatal("left did not request the previous cursor page")
	}
	if a.skillHubCursor != "" || a.skillHubPage != 1 || len(a.skillHubCursorHistory) != 0 {
		t.Fatalf("back cursor state: cursor=%q page=%d history=%#v", a.skillHubCursor, a.skillHubPage, a.skillHubCursorHistory)
	}
}

func TestSkillHubSecurityAndEvaluationSummary(t *testing.T) {
	reports := map[string]any{"keen": map[string]any{"status": "benign", "statusText": "safe"}}
	lines := skillHubSecuritySummary(reports)
	if len(lines) != 1 || lines[0] != "Security keen: benign (safe)" {
		t.Fatalf("security lines = %#v", lines)
	}
	evaluation := map[string]any{"dimensions": map[string]any{"quality": map[string]any{}, "safety": map[string]any{}}}
	if got := skillHubEvaluationDimensions(evaluation); got != 2 {
		t.Fatalf("evaluation dimensions = %d", got)
	}
}

func TestSkillHubCategoryAndSortControls(t *testing.T) {
	a := &App{
		skillHub:           skillhub.NewService("", nil, nil),
		skillHubOpen:       true,
		skillHubMarket:     skillhub.MarketSkillHub,
		skillHubView:       skillHubBrowse,
		skillHubScope:      "project",
		skillHubPage:       3,
		skillHubSort:       "downloads",
		skillHubCategories: []skillhub.Category{{Key: "dev-programming", NameEn: "Development"}},
		width:              80,
		height:             24,
	}
	if cmd := a.cycleSkillHubCategory(); cmd == nil || a.skillHubCategory != "dev-programming" || a.skillHubPage != 1 {
		t.Fatalf("category control: cmd=%v category=%q page=%d", cmd != nil, a.skillHubCategory, a.skillHubPage)
	}
	a.skillHubLoading = false
	if cmd := a.cycleSkillHubSort(); cmd == nil || a.skillHubSort != "stars" {
		t.Fatalf("sort control: cmd=%v sort=%q", cmd != nil, a.skillHubSort)
	}
}

func TestSkillHubExpandedDetailIncludesReports(t *testing.T) {
	a := &App{skillHubDetail: &skillhub.SkillDetail{
		SkillSummary:    skillhub.SkillSummary{Market: skillhub.MarketSkillHub, ID: "test", Name: "Test", Downloads: 10},
		Files:           []skillhub.SkillFile{{Path: "SKILL.md", Size: 12, SHA256: "abc"}},
		DownloadSources: []skillhub.DownloadSource{{Kind: "api", URL: "https://api.test/download"}, {Kind: "cdn", URL: "https://cdn.test/download", Fallback: true}},
		SecurityReports: map[string]any{"keen": map[string]any{"status": "benign"}},
		Evaluation:      map[string]any{"dimensions": map[string]any{"quality": map[string]any{}}},
	}}
	content := strings.Join(a.skillHubExpandedDetailLines(100), "\n")
	for _, want := range []string{"Files (1):", "SKILL.md", "Download sources:", "api.test/download", "cdn fallback", "Security reports:", "benign", "Evaluation:", "quality"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expanded detail missing %q:\n%s", want, content)
		}
	}
}
