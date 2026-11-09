package tui

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// Router is still a beta (preview) feature. Every user-visible Router
// surface must say so, so nobody treats an evaluation mode as a production
// contract.
func TestRouterSurfacesAreLabeledAsBetaMode(t *testing.T) {
	app := newRouterCommandApp(t)
	app.mode = agentruntime.ModeRouter
	// The footer truncates the left column to the terminal width, so give the
	// test a width that keeps the whole mode label visible.
	app.width = 200

	footer := app.renderBuiltinFooter()
	if !strings.Contains(footer, "ROUTER (BETA)") {
		t.Fatalf("the status bar does not mark Router as a beta mode: %q", footer)
	}

	permissions := app.translator.Text(i18n.MsgCommandPermissionsRouter)
	if !marksBetaMode(permissions) {
		t.Fatalf("the /mode Router permissions line does not mark the beta mode: %q", permissions)
	}

	title := app.translator.Text(i18n.MsgRouterPanelTitle)
	if !marksBetaMode(title) {
		t.Fatalf("the Router panel title does not mark the beta mode: %q", title)
	}

	description := app.translator.Text(i18n.MsgCommandRouterDescription)
	if !marksBetaMode(description) {
		t.Fatalf("the /router description does not mark the beta mode: %q", description)
	}

	// Both catalogs must carry the marker, so no language can silently drop it.
	for language, messages := range i18n.Catalogs() {
		for _, id := range []i18n.MessageID{
			i18n.MsgCommandPermissionsRouter,
			i18n.MsgRouterPanelTitle,
			i18n.MsgCommandRouterDescription,
		} {
			text := messages[id]
			if !marksBetaMode(text) {
				t.Fatalf("%s message %q does not mark the Router beta mode: %q", language, id, text)
			}
		}
	}
}

// marksBetaMode reports whether a Router label carries the beta / preview
// marker in either supported language.
func marksBetaMode(text string) bool {
	lowered := strings.ToLower(text)
	return strings.Contains(lowered, "beta") ||
		strings.Contains(lowered, "preview mode") ||
		strings.Contains(text, "Beta") ||
		strings.Contains(text, "预览")
}
