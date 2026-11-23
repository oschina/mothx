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
// live view while a run owns the session. Runtime-owned submissions are
// projected from pendingSubmissions; queuedPrompts remains the explicit
// pre-session/headless compatibility bridge. Rendering never starts work.
func (a *App) renderQueuedPrompts(width int) string {
	if a == nil {
		return ""
	}
	total := len(a.pendingSubmissions) + len(a.queuedPrompts)
	if total == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	lines := []string{queuedPromptStyle.Render(a.translator.Text(i18n.MsgQueuedPromptsHeader, total))}
	visible := 0
	appendPreview := func(idx int, text string) bool {
		if visible >= maxVisibleQueuedPrompts {
			return false
		}
		lines = append(lines, queuedPromptItemStyle.Render(fmt.Sprintf("  %d. %s", idx+1, queuedPromptPreview(text, width-8))))
		visible++
		return true
	}
	for i, sub := range a.pendingSubmissions {
		if sub == nil {
			continue
		}
		if !appendPreview(i, sub.text) {
			lines = append(lines, statusStyle.Render(fmt.Sprintf("  +%d …", total-visible)))
			return strings.Join(lines, "\n")
		}
	}
	for i, prompt := range a.queuedPrompts {
		if !appendPreview(len(a.pendingSubmissions)+i, prompt.text) {
			lines = append(lines, statusStyle.Render(fmt.Sprintf("  +%d …", total-visible)))
			break
		}
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
