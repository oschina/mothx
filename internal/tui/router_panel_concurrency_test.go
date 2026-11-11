package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
)

// A profile configured on an earlier build with a higher ceiling must still be
// editable from the panel: before this, saving an untouched draft failed with no
// field to fix it, which trapped the user.
func TestRouterPanelEditsLegacyConcurrencyProfile(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()
	app.routerPanel.Profiles = []config.ACPAgentProfile{{
		ID: "codex", Title: "Codex", Command: []string{"/opt/codex/bin/codex", "acp"},
		Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		MaxConcurrentChildren: 2,
	}}

	app.beginRouterPanelEdit(0)
	if app.routerPanel.Draft.MaxConcurrentChildren != 2 {
		t.Fatalf("the form dropped the configured ceiling: %#v", app.routerPanel.Draft)
	}
	app.saveRouterPanelDraft()
	if app.routerPanel.Error != "" {
		t.Fatalf("saving a previously valid profile failed: %s", app.routerPanel.Error)
	}
	settings, err := config.LoadGlobalSettingsOrDefault()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.ACPAgents) != 1 || settings.ACPAgents[0].MaxConcurrentChildren != 2 {
		t.Fatalf("the configured ceiling was not preserved: %#v", settings.ACPAgents)
	}
}

// A configured read-only ceiling is effective, so the panel must not label it
// as capped.
func TestRouterPanelDoesNotReportCappedConcurrency(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.openRouterPanel()
	app.routerPanel.Profiles = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"},
		Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		MaxConcurrentChildren: 3,
	}}

	rendered := stripANSI(app.renderRouterPanel())
	if strings.Contains(rendered, "capped") || strings.Contains(rendered, "上限") {
		t.Fatalf("panel reported an obsolete concurrency cap:\n%s", rendered)
	}
}

// The Router panel exposes worker activity as a read-only projection: a pending
// question is visible, but there is no answer control in the panel because only
// the lead may resolve it through delegate_worker_mgr.
func TestRouterPanelShowsWorkerMessagesWithoutAnswerControl(t *testing.T) {
	app := newRouterCommandApp(t)
	t.Setenv("MOTHX_DIR", t.TempDir())
	app.width, app.height = 120, 40
	app.openRouterPanel()
	app.routerPanel.WorkerMessages = []agentruntime.WorkerMessage{{
		ID: "wm_1", ChildID: "child-1", Kind: agentruntime.WorkerMessageFinding,
		Body: "found a race in the scheduler", Status: agentruntime.WorkerMessageStatusCreated,
	}}
	app.routerPanel.PendingQuestions = []agentruntime.WorkerQuestionView{{
		ID: "wq_1", ChildID: "child-1", Question: "which branch should I use?",
	}}

	rendered := stripANSI(app.renderRouterPanel())
	for _, want := range []string{"found", "which branch", "只读"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("router panel missing %q:\n%s", want, rendered)
		}
	}
	for _, forbidden := range []string{"answer", "answer via", "[y]"} {
		if strings.Contains(strings.ToLower(rendered), forbidden) {
			t.Fatalf("router panel exposed an answer affordance %q:\n%s", forbidden, rendered)
		}
	}
}
