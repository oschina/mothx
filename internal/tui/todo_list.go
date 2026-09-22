package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/oschina/mothx/internal/tools"
)

// renderStickyTodoList renders a compact bordered box showing non-done task plan steps.
func renderStickyTodoList(plan *tools.TaskPlan, width int, maxVisible int) string {
	if plan == nil || len(plan.Steps) == 0 {
		return ""
	}

	// Filter out done steps
	var visible []tools.PlanStep
	for _, s := range plan.Steps {
		if s.Status != "done" {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	// Cap width
	if width > 64 {
		width = 64
	}

	iconFor := func(status string) string {
		switch status {
		case "running":
			return "◐"
		case "failed":
			return "✗"
		case "done":
			return "●"
		default:
			return "○"
		}
	}

	var lines []string
	shown := visible
	remaining := 0
	if len(visible) > maxVisible {
		shown = visible[:maxVisible]
		remaining = len(visible) - maxVisible
	}
	for _, s := range shown {
		lines = append(lines, iconFor(s.Status)+" "+s.Title)
	}
	for len(lines) < maxVisible+1 {
		lines = append(lines, "")
	}
	if remaining > 0 {
		lines[maxVisible] = "... and " + itoa(remaining) + " more"
	}

	content := strings.Join(lines, "\n")

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(width)

	return style.Render(content)
}

// itoa converts int to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
