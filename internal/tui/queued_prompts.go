package tui

import (
	"fmt"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/tui/i18n"
)

// maxVisibleQueuedPrompts caps how many queued submissions the live indicator
// lists. The header always carries the true total, so an overflowing queue
// stays fully discoverable without pushing the input field off screen.
const maxVisibleQueuedPrompts = 3

// renderQueuedPrompts projects the submissions the Runtime is genuinely
// holding in its queue into the managed live view. pendingSubmissions also
// carries the head-of-line submission until its admitted hand-off is processed,
// but that one is starting now rather than waiting behind a run: it is
// acknowledged by its transcript echo and the loading indicator, and listing it
// here made a single prompt flash "1 queued" between registration and
// admission. queuedPrompts remains the explicit pre-session/headless
// compatibility bridge. Rendering never starts work.
func (a *App) renderQueuedPrompts(width int) string {
	if a == nil {
		return ""
	}
	waiting := make([]string, 0, len(a.pendingSubmissions)+len(a.queuedPrompts))
	for _, sub := range a.pendingSubmissions {
		if sub.waitingBehindRun() {
			waiting = append(waiting, sub.text)
		}
	}
	for _, prompt := range a.queuedPrompts {
		waiting = append(waiting, prompt.text)
	}
	total := len(waiting)
	if total == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	lines := []string{queuedPromptStyle.Render(a.translator.Text(i18n.MsgQueuedPromptsHeader, total))}
	for visible, text := range waiting {
		if visible >= maxVisibleQueuedPrompts {
			lines = append(lines, statusStyle.Render(fmt.Sprintf("  +%d …", total-visible)))
			break
		}
		lines = append(lines, queuedPromptItemStyle.Render(fmt.Sprintf("  %d. %s", visible+1, queuedPromptPreview(text, width-8))))
	}
	return strings.Join(lines, "\n")
}

// queuedPromptPreview flattens a queued submission into one bounded line so a
// multi-line paste cannot dominate the live view.
func queuedPromptPreview(text string, width int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" {
		flat = "…"
	}
	if width > 4 && xansi.StringWidth(flat) > width {
		flat = xansi.Truncate(flat, width, "…")
	}
	return flat
}
