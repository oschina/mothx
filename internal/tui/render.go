package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/startvibecoding/mothx/internal/tui/i18n"
	"github.com/startvibecoding/mothx/internal/tui/renderutil"
)

func (a *App) renderMessageAt(idx int) string {
	for i, tr := range a.toolResults {
		if tr.msgIndex == idx {
			return a.renderToolResult(a.toolResults[i])
		}
	}
	if _, ok := a.assistantRaw[idx]; ok {
		return a.renderAssistantMessage(idx)
	}
	if _, ok := a.thinkRaw[idx]; ok {
		return a.renderThinkMessage(idx)
	}
	if idx >= 0 && idx < len(a.messages) {
		if a.compactMode && a.hiddenEventIdx[idx] {
			return ""
		}
		return a.messages[idx]
	}
	return ""
}

func (a *App) isToolMessageIndex(idx int) bool {
	for _, result := range a.toolResults {
		if result.msgIndex == idx {
			return true
		}
	}
	return false
}

func (a *App) renderToolResult(result toolResult) string {
	if result.status == toolResultStatusRunning {
		return toolStyle.Render(formatToolExecutionStartWithTranslator(a.translator, result))
	}
	if result.toolName == "bash" {
		if a.compactMode {
			return toolStyle.Render(formatBashCommandLine(a.translator, result))
		}
		return renderBashToolResult(a.translator, result)
	}
	if result.status == toolResultStatusInterrupted {
		// The tool never produced a result (the run was canceled or the stream
		// ended first), so report the terminal state instead of an empty summary.
		return toolStyle.Render(fmt.Sprintf("%s %s", formatToolHeader(result), a.translator.Text(i18n.MsgToolModalStateCanceled)))
	}
	// Compact mode: single-line summary for all tool types
	if a.compactMode {
		header := formatToolHeader(result)
		summary := result.summary
		if summary == "" {
			summary = "..."
		}
		// Use first line of summary only
		if idx := strings.IndexByte(summary, '\n'); idx >= 0 {
			summary = summary[:idx]
		}
		return toolStyle.Render(fmt.Sprintf("%s %s", header, summary))
	}

	if result.toolName == "edit" {
		if result.summary == "" && result.fullContent == "" && result.diff == nil {
			return toolStyle.Render(fmt.Sprintf("%s ...", formatToolHeader(result)))
		}
		return toolStyle.Render(formatEditedToolResultWithTranslator(a.translator, result))
	}
	summary := result.summary
	if summary == "" {
		summary = "..."
	}
	sep := " "
	if strings.Contains(summary, "\n") {
		sep = "\n"
	}
	return toolStyle.Render(fmt.Sprintf("%s%s%s", formatToolHeader(result), sep, summary))
}

func renderBashToolResult(tr i18n.Translator, result toolResult) string {
	header := toolStyle.Render(formatBashCommandLine(tr, result))
	summary := result.summary
	if summary == "" && result.status == toolResultStatusInterrupted {
		// An interrupted command has no output; the header already carries the
		// terminal state.
		return header
	}
	if summary == "" {
		summary = "..."
	}
	if strings.Contains(summary, "\n") {
		return header + "\n" + summary
	}
	return header + " " + summary
}

func (a *App) renderAssistantMessage(idx int) string {
	raw := a.assistantRaw[idx]
	if raw == "" {
		return ""
	}
	prefixText := a.translator.Text(i18n.MsgAssistantPrefix)
	prefix := assistantStyle.Render(prefixText)
	width := a.assistantMarkdownWidth()
	tableMarkdown := containsMarkdownTable(raw)
	if renderutil.LooksLikeMarkdown(raw) {
		if a.assistantDirty[idx] && a.mdRenderer != nil {
			a.mdRenderer.Update(raw)
			rendered := a.mdRenderer.Output()
			a.assistantRendered[idx] = renderutil.TrimANSIBlankLines(rendered)
			a.assistantDirty[idx] = false
		}
		if rendered, ok := a.assistantRendered[idx]; ok && rendered != "" {
			if tableMarkdown {
				return assistantStyle.Render(strings.TrimRight(prefixText, " ")) + "\n" + renderutil.WrapANSI(rendered, a.assistantFullWidth())
			}
			return prefix + renderutil.WrapANSI(rendered, width)
		}
	}
	return prefix + wrapPlainText(raw, width)
}

func (a *App) renderThinkMessage(idx int) string {
	raw := a.thinkRaw[idx]
	if raw == "" {
		return ""
	}
	prefix := thinkStyle.Render(a.translator.Text(i18n.MsgThinkingPrefix))
	return prefix + renderutil.WrapPlainText(raw, a.thinkMessageWidth())
}

func (a *App) thinkMessageWidth() int {
	width := a.width
	if width <= 0 {
		width = 80
	}
	width -= lipgloss.Width(a.translator.Text(i18n.MsgThinkingPrefix))
	if width < 1 {
		return 1
	}
	return width
}

func wrapPlainText(s string, width int) string {
	return renderutil.WrapANSI(s, width)
}

func (a *App) assistantFullWidth() int {
	width := a.width
	if width <= 0 {
		width = 80
	}
	if width < 1 {
		return 1
	}
	return width
}

func containsMarkdownTable(s string) bool {
	lines := strings.Split(s, "\n")
	for i := 0; i+1 < len(lines); i++ {
		header := strings.TrimSpace(lines[i])
		if !isMarkdownTableHeader(header) {
			continue
		}
		if isMarkdownTableSeparator(strings.TrimSpace(lines[i+1])) {
			return true
		}
	}
	return false
}

func isMarkdownTableHeader(line string) bool {
	return strings.HasPrefix(line, "|") && strings.Count(line, "|") >= 2
}

func isMarkdownTableSeparator(line string) bool {
	if !strings.HasPrefix(line, "|") || strings.Count(line, "|") < 2 {
		return false
	}
	hasDash := false
	for _, r := range line {
		switch r {
		case '|', ':', ' ', '\t':
		case '-':
			hasDash = true
		default:
			return false
		}
	}
	return hasDash
}

// cacheHitPercent returns the cache read hit ratio against the full input
// footprint, or -1 when no token usage has been recorded yet.
func (a *App) cacheHitPercent() float64 {
	if a.totalInputTokens <= 0 {
		return -1
	}
	pct := float64(a.totalCacheRead) / float64(a.totalInputTokens) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}

// formatCachePercent calculates and returns the cache hit rate string, or empty string if no data.
// The denominator uses the full input footprint so OpenAI and Anthropic can share the same
// cache ratio display after their provider-specific usage fields are normalized.
func (a *App) formatCachePercent() string {
	if pct := a.cacheHitPercent(); pct >= 0 {
		return fmt.Sprintf("Cache: %.0f%%", pct)
	}
	switch {
	case a.totalCacheRead > 0:
		return fmt.Sprintf("CacheRead: %d", a.totalCacheRead)
	case a.totalCacheWrite > 0:
		return fmt.Sprintf("CacheWrite: %d", a.totalCacheWrite)
	default:
		return ""
	}
}

func formatTokens(count int) string {
	if count < 1000 {
		return fmt.Sprintf("%d", count)
	}
	if count < 10000 {
		return fmt.Sprintf("%.1fk", float64(count)/1000)
	}
	if count < 1000000 {
		return fmt.Sprintf("%dk", count/1000)
	}
	if count < 10000000 {
		return fmt.Sprintf("%.1fM", float64(count)/1000000)
	}
	return fmt.Sprintf("%dM", count/1000000)
}

func (a *App) renderFooter() string {
	if out := a.renderExternalFooter(); out != "" {
		return out
	}
	return a.renderBuiltinFooter()
}

func (a *App) renderExternalFooter() string {
	if !a.statusLineEnabled() {
		return ""
	}
	if strings.TrimSpace(a.statusLineOutput) == "" {
		return ""
	}
	lines := strings.Split(a.statusLineOutput, "\n")
	padding := 0
	if a.settings != nil && a.settings.StatusLine.Padding > 0 {
		padding = a.settings.StatusLine.Padding
	}
	for i := 0; i < padding; i++ {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (a *App) renderBuiltinFooter() string {
	modelName := "unknown"
	if a.model != nil {
		modelName = a.model.Name
	}

	var modeStr string
	switch a.mode {
	case "plan":
		modeStr = "🗒 PLAN"
	case "agent":
		modeStr = "🔧 AGENT"
	case "yolo":
		modeStr = "🚀 YOLO"
	case "os":
		modeStr = "🖥 OS"
	default:
		modeStr = strings.ToUpper(a.mode)
	}

	cwd := a.currentCwd()
	if len(cwd) > 30 {
		cwd = "..." + cwd[len(cwd)-27:]
	}

	// Build right column: sandbox + context + cache (fixed width, never compressed)
	var rightParts []string
	if a.sandboxInfo != "" {
		rightParts = append(rightParts, a.sandboxInfo)
	}
	if a.contextUsage != nil && a.contextUsage.ContextWindow > 0 {
		if a.contextUsage.Percent != nil {
			percent := *a.contextUsage.Percent
			contextDisplay := fmt.Sprintf("%.1f%%/%s",
				percent,
				formatTokens(a.contextUsage.ContextWindow))
			if percent > 90 {
				rightParts = append(rightParts, errorStyle.Render(contextDisplay))
			} else if percent > 70 {
				rightParts = append(rightParts, userStyle.Render(contextDisplay))
			} else {
				rightParts = append(rightParts, contextDisplay)
			}
		} else {
			rightParts = append(rightParts, fmt.Sprintf("?/%s", formatTokens(a.contextUsage.ContextWindow)))
		}
	}
	if cachePercentStr := a.formatCachePercent(); cachePercentStr != "" {
		if a.cacheHitPercent() >= 50 {
			rightParts = append(rightParts, statusStyle.Render(cachePercentStr))
		} else {
			rightParts = append(rightParts, cachePercentStr)
		}
	}
	if a.esmFooter != "" {
		rightParts = append(rightParts, a.esmFooter)
	}
	if expertStatus := a.expertFooter(); expertStatus != "" {
		rightParts = append(rightParts, expertStatus)
	}
	rightStr := strings.Join(rightParts, " | ")
	rightWidth := lipgloss.Width(rightStr)

	// Build left column: mode | model | path (single line)
	// Color each text segment explicitly so the styled separator's ANSI reset
	// does not strip the footer color from following segments.
	sep := footerSepStyle.Render("|")
	text := footerTextStyle.Render
	leftLine1 := fmt.Sprintf(" %s %s %s %s %s", text(modeStr), sep, text(modelName), sep, text(cwd))

	// Second line: dynamic hints
	var leftLine2 string
	if a.waitingForApproval {
		leftLine2 = " " + a.renderApprovalFooterAlert()
	} else if a.isAgentActive() {
		leftLine2 = " " + spinnerChars[a.spinnerIndex] + " " + formatDuration(a.timer.Elapsed()) + " · " + a.translator.Text(i18n.MsgCancelHint)
	} else {
		if a.lastDuration > 0 {
			leftLine2 = " " + a.translator.Text(i18n.MsgFooterLastDuration, formatDuration(a.lastDuration))
		}
		if a.toolModalOpen {
			leftLine2 += " | " + a.translator.Text(i18n.MsgFooterToolModalHints)
		} else {
			leftLine2 += " | " + a.translator.Text(i18n.MsgFooterMainHints)
		}
	}

	leftContent := leftLine1 + "\n" + leftLine2

	// Calculate left width (total width minus right column minus separator).
	// On narrow terminals the right column is truncated so the footer keeps
	// its two lines instead of wrapping underneath the left column.
	sepWidth := 2 // " |" separator
	const minLeftWidth = 10
	leftWidth := a.width - rightWidth - sepWidth
	if leftWidth < minLeftWidth {
		if a.width > 0 {
			// A zero width means the terminal size is not known yet (no
			// WindowSizeMsg); keep the untruncated layout in that case.
			maxRightWidth := a.width - minLeftWidth - sepWidth
			if maxRightWidth < 0 {
				maxRightWidth = 0
			}
			if rightWidth > maxRightWidth {
				rightStr = xansi.Truncate(rightStr, maxRightWidth, "…")
				rightWidth = lipgloss.Width(rightStr)
			}
		}
		leftWidth = a.width - rightWidth - sepWidth
		if leftWidth < minLeftWidth {
			leftWidth = minLeftWidth
		}
	}

	// Truncate left lines to fit
	leftLines := strings.Split(leftContent, "\n")
	for i, line := range leftLines {
		if xansi.StringWidth(line) > leftWidth {
			leftLines[i] = xansi.Truncate(line, leftWidth, "…")
		}
	}

	// Build footer: left lines + right-aligned last line
	rightPadding := leftWidth - xansi.StringWidth(leftLines[len(leftLines)-1])
	if rightPadding < 2 {
		rightPadding = 2
	}
	// Append right column to the last left line
	leftLines[len(leftLines)-1] += strings.Repeat(" ", rightPadding) + rightStr

	return footerStyle.Width(a.width).Render(strings.Join(leftLines, "\n"))
}

// expertFooter is a compact projection of Runtime-owned expert identity and
// the member lifecycle evidence already observed by this TUI. It does not
// resolve packages or maintain member state independently.
func (a *App) expertFooter() string {
	if a == nil || a.runtime == nil {
		return ""
	}
	binding, _ := a.runtime.ExpertState()
	if binding == nil {
		return ""
	}
	if !binding.Team {
		return "Expert:" + binding.ID
	}
	running := 0
	for _, activity := range a.agentActivities {
		if activity != nil && activity.MemberID != "" && activity.State == "running" {
			running++
		}
	}
	if running > 0 {
		return fmt.Sprintf("Team:%s %d/%d active", binding.ID, running, len(binding.MemberDefs))
	}
	return fmt.Sprintf("Team:%s %d members", binding.ID, len(binding.MemberDefs))
}

func (a *App) renderApprovalFooterAlert() string {
	alert := a.translator.Text(i18n.MsgFooterApprovalAlert)
	if a.spinnerIndex%2 == 0 {
		return warningStyle.Render(alert)
	}
	return strings.Repeat(" ", lipgloss.Width(alert))
}
