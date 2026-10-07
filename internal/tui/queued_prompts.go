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

// renderQueuedPrompts projects the pending-submission queue into the managed
// live view while a run owns the session. It is pure adapter rendering of
// TUI-local queue state: the queue itself is filled and drained exclusively by
// processInput/startNextQueuedPrompt, and each prompt still renders its
// canonical "You:" transcript line when it actually starts.
func (a *App) renderQueuedPrompts(width int) string {
	if a == nil || len(a.queuedPrompts) == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	lines := []string{queuedPromptStyle.Render(a.translator.Text(i18n.MsgQueuedPromptsHeader, len(a.queuedPrompts)))}
	for i, prompt := range a.queuedPrompts {
		if i >= maxVisibleQueuedPrompts {
			lines = append(lines, statusStyle.Render(fmt.Sprintf("  +%d …", len(a.queuedPrompts)-maxVisibleQueuedPrompts)))
			break
		}
		lines = append(lines, queuedPromptItemStyle.Render(fmt.Sprintf("  %d. %s", i+1, queuedPromptPreview(prompt.text, width-8))))
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
