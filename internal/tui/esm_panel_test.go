package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tui/i18n"
)

func newESMPanelTestApp() *App {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.ready = true
	a.width = 80
	a.height = 24
	a.translator = i18n.New(i18n.LanguageEN)
	return a
}

func TestCtrlEOpensAndClosesESMProgressPanel(t *testing.T) {
	a := newESMPanelTestApp()

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !a.esmPanelOpen {
		t.Fatal("Ctrl+E did not open ESM panel")
	}
	view := stripANSI(a.View())
	if !strings.Contains(view, "ESM Progress") || !strings.Contains(view, "No Enable Supervisor Mode objective") {
		t.Fatalf("empty ESM panel missing expected content:\n%s", view)
	}

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if a.esmPanelOpen {
		t.Fatal("second Ctrl+E did not close ESM panel")
	}

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.esmPanelOpen {
		t.Fatal("Esc did not close ESM panel")
	}

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl+C in ESM panel did not return quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("Ctrl+C in ESM panel did not produce tea.QuitMsg")
	}
}

func TestESMProgressPanelShowsPhaseMissingWorkAndRecoveryCounts(t *testing.T) {
	a := newESMPanelTestApp()
	a.esmPanelOpen = true
	a.esmPanelObjective = &esm.Objective{
		Objective:        "finish the complete ESM workflow",
		Status:           esm.StatusActive,
		Phase:            esm.PhaseCritic,
		ProgressSummary:  "implemented the parser gate",
		RemainingWork:    []string{"add regression tests", "verify narrow terminal layout"},
		CompletionReview: "review: missing coverage\nmissing_work (2): add regression tests; verify narrow terminal layout",
		RejectionCount:   3,
		RecoveryCount:    2,
		RecoveryReason:   "worker timed out after 30m",
		TokensUsed:       1250,
		TimeUsedMS:       65000,
		UpdatedAt:        time.Now(),
	}

	content := strings.Join(a.esmPanelLines(74), "\n")
	for _, want := range []string{
		"Now: Critic is independently reviewing the worker evidence",
		"Progress: 1/3 pipeline stages completed; 2 work item(s) remaining",
		"Next: A passing critic review advances the candidate to final audit",
		"Status: active",
		"Stage: Critic review",
		"[x] Worker execution -> [>] Critic review -> [ ] Final audit",
		"Latest worker progress: implemented the parser gate",
		"Remaining work (2):",
		"add regression tests",
		"verify narrow terminal layout",
		"Consecutive completion rejections: 3",
		"Consecutive automatic recoveries: 2",
		"Latest recovery reason: worker timed out after 30m",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("ESM panel missing %q:\n%s", want, content)
		}
	}
	view := stripANSI(a.View())
	if !strings.Contains(view, "ESM Progress") {
		t.Fatalf("rendered ESM panel missing title:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != a.height {
		t.Fatalf("View height = %d, want %d", got, a.height)
	}
}

func TestESMProgressPanelScrollKeysStayInPanel(t *testing.T) {
	a := newESMPanelTestApp()
	remaining := make([]string, 30)
	for i := range remaining {
		remaining[i] = "remaining item"
	}
	a.esmPanelOpen = true
	a.esmPanelObjective = &esm.Objective{Objective: "long objective", Status: esm.StatusActive, Phase: esm.PhaseWorker, RemainingWork: remaining}

	a.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if a.esmPanelScroll == 0 {
		t.Fatal("PgDown did not scroll ESM panel")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if a.esmPanelScroll != a.maxESMPanelOffset() {
		t.Fatalf("End scroll = %d, want %d", a.esmPanelScroll, a.maxESMPanelOffset())
	}
	a.Update(tea.KeyMsg{Type: tea.KeyHome})
	if a.esmPanelScroll != 0 {
		t.Fatalf("Home scroll = %d, want 0", a.esmPanelScroll)
	}
}

func TestESMProgressPanelFitsNarrowShortTerminal(t *testing.T) {
	a := newESMPanelTestApp()
	a.width = 30
	a.height = 7
	a.esmPanelOpen = true
	a.esmPanelObjective = &esm.Objective{Objective: "long objective that needs wrapping", Status: esm.StatusActive, Phase: esm.PhaseWorker}

	view := stripANSI(a.View())
	lines := strings.Split(view, "\n")
	if len(lines) != a.height {
		t.Fatalf("View height = %d, want %d", len(lines), a.height)
	}
	if !strings.HasPrefix(lines[0], "╭") || !strings.Contains(view, "ESM Progress") {
		t.Fatalf("panel top was cropped:\n%s", view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > a.width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, width, a.width, line)
		}
	}
}
