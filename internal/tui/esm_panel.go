package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/oschina/mothx/internal/esm"
	"github.com/oschina/mothx/internal/tui/i18n"
	"github.com/oschina/mothx/internal/tui/renderutil"
)

func (a *App) openESMPanel() {
	a.esmPanelOpen = true
	a.esmPanelScroll = 0
	a.refreshESMPanel()
}

func (a *App) closeESMPanel() {
	a.esmPanelOpen = false
	a.esmPanelScroll = 0
	a.esmPanelObjective = nil
	a.esmPanelErr = nil
}

func (a *App) refreshESMPanel() {
	a.esmMu.Lock()
	tracked := a.esmRunTracked
	a.esmMu.Unlock()
	if !a.esmPanelOpen && !tracked {
		return
	}
	obj, err := a.loadESMObjective(context.Background())
	if errors.Is(err, esm.ErrNotFound) {
		a.setESMFooter(nil)
		if a.esmPanelOpen {
			a.esmPanelObjective = nil
			a.esmPanelErr = nil
		}
		return
	}
	if err == nil {
		a.setESMFooter(obj)
	}
	if a.esmPanelOpen {
		a.esmPanelObjective = obj
		a.esmPanelErr = err
	}
}

func (a *App) scrollESMPanel(delta int) {
	a.esmPanelScroll += delta
	if a.esmPanelScroll < 0 {
		a.esmPanelScroll = 0
	}
	a.scheduleRender()
}

func (a *App) esmPanelPageSize() int {
	footerHeight := a.esmPanelFooterHeight()
	height := a.height - footerHeight - 5
	if a.height <= 0 {
		return 20
	}
	if height < 1 {
		return 1
	}
	return height
}

func (a *App) maxESMPanelOffset() int {
	width := esmPanelContentWidth(esmPanelWidth(a.width))
	maxOffset := len(a.esmPanelLines(width)) - a.esmPanelPageSize()
	if maxOffset < 0 {
		return 0
	}
	return maxOffset
}

func (a *App) renderESMPanel() string {
	width := esmPanelWidth(a.width)
	innerWidth := esmPanelContentWidth(width)
	footerHeight := a.esmPanelFooterHeight()
	height := 20
	if a.height > 0 {
		height = a.height - footerHeight - 5
		if height < 0 {
			height = 0
		}
	}
	lines := a.esmPanelLines(innerWidth)
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if a.esmPanelScroll > maxOffset {
		a.esmPanelScroll = maxOffset
	}
	end := a.esmPanelScroll + height
	if end > len(lines) {
		end = len(lines)
	}
	visible := strings.Join(lines[a.esmPanelScroll:end], "\n")
	if visible == "" {
		visible = " "
	}
	var position string
	if len(lines) == 0 {
		position = a.translator.Text(i18n.MsgESMPanelPositionEmpty)
	} else if height == 0 {
		position = a.translator.Text(i18n.MsgESMPanelPosition, 0, 0, len(lines))
	} else {
		position = a.translator.Text(i18n.MsgESMPanelPosition, a.esmPanelScroll+1, end, len(lines))
	}
	statusText := ""
	if obj := a.esmPanelObjective; obj != nil {
		statusText = fmt.Sprintf("  %s / %s", obj.Status, a.esmPhaseLabel(effectiveESMPhase(obj)))
	}
	titleText := a.translator.Text(i18n.MsgESMProgressTitle) + statusText + "  " + position + "  " + a.translator.Text(i18n.MsgESMPanelShortcutHint)
	suffix := "..."
	if innerWidth < len(suffix) {
		suffix = ""
	}
	title := statusStyle.Render(xansi.Truncate(titleText, innerWidth, suffix))
	divider := strings.Repeat("-", minInt(innerWidth, lipgloss.Width(title)))
	content := title + "\n" + divider + "\n" + visible
	return toolModalStyle.Width(width).Height(height + 3).Render(content)
}

func (a *App) esmPanelFooterHeight() int {
	if a.height > 0 && a.height < 8 {
		return 0
	}
	return lipgloss.Height(a.renderFooter())
}

func esmPanelWidth(terminalWidth int) int {
	if terminalWidth <= 0 {
		terminalWidth = 80
	}
	width := terminalWidth - 4
	if width < 1 {
		return 1
	}
	return width
}

func esmPanelContentWidth(width int) int {
	width -= toolModalStyle.GetHorizontalPadding()
	if width < 1 {
		return 1
	}
	return width
}

func (a *App) esmPanelLines(width int) []string {
	if a.esmPanelErr != nil {
		return []string{a.translator.Text(i18n.MsgESMPanelLoadFailed, a.esmPanelErr.Error())}
	}
	obj := a.esmPanelObjective
	if obj == nil {
		return []string{
			a.translator.Text(i18n.MsgESMPanelNoObjective),
			"",
			a.translator.Text(i18n.MsgESMPanelCreateHint),
		}
	}

	phase := effectiveESMPhase(obj)
	lines := []string{
		a.translator.Text(i18n.MsgESMPanelTitle),
		"",
		a.translator.Text(i18n.MsgESMPanelNow, a.esmPanelNow(obj)),
		a.esmPanelProgress(obj, phase),
		a.translator.Text(i18n.MsgESMPanelNext, a.esmPanelNextStep(obj, phase)),
		"",
		a.translator.Text(i18n.MsgESMPanelStatus, obj.Status),
		a.translator.Text(i18n.MsgESMPanelStage, a.esmPhaseLabel(phase)),
		a.translator.Text(i18n.MsgESMPanelPipeline, a.renderESMPipeline(phase, obj.Status)),
	}
	lines = appendWrappedESMField(lines, a.translator.Text(i18n.MsgESMPanelObjective), obj.Objective, width)

	if obj.ProgressSummary != "" {
		lines = append(lines, "")
		lines = appendWrappedESMField(lines, a.translator.Text(i18n.MsgESMPanelLatestWorkerProgress), obj.ProgressSummary, width)
	}
	if len(obj.RemainingWork) > 0 {
		lines = append(lines, "", a.translator.Text(i18n.MsgESMPanelRemainingWork, len(obj.RemainingWork)))
		lines = appendESMItems(lines, obj.RemainingWork, width)
	}
	if obj.BlockedReason != "" {
		lines = append(lines, "")
		lines = appendWrappedESMField(lines, a.translator.Text(i18n.MsgESMPanelBlocker), obj.BlockedReason, width)
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelRepeatedBlockerAudit, obj.BlockedCount))
	}
	if obj.CompletionReview != "" {
		lines = append(lines, "")
		lines = appendWrappedESMField(lines, a.translator.Text(i18n.MsgESMPanelLatestCompletionReview), obj.CompletionReview, width)
	}
	if obj.RejectionCount > 0 {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelCompletionRejections, obj.RejectionCount))
	}
	if obj.RecoveryCount > 0 {
		lines = append(lines, "", a.translator.Text(i18n.MsgESMPanelAutomaticRecoveries, obj.RecoveryCount))
		if obj.RecoveryReason != "" {
			lines = appendWrappedESMField(lines, a.translator.Text(i18n.MsgESMPanelLatestRecoveryReason), obj.RecoveryReason, width)
		}
	}
	if obj.CompletionReason != "" && obj.Status == esm.StatusCompleteCandidate {
		lines = append(lines, "")
		lines = appendWrappedESMField(lines, a.translator.Text(i18n.MsgESMPanelCompletionCandidate), obj.CompletionReason, width)
	}

	if activity := a.activeESMPanelActivity(width); len(activity) > 0 {
		lines = append(lines, "", a.translator.Text(i18n.MsgESMPanelLiveDetails))
		lines = append(lines, activity...)
	}

	lines = append(lines, "", a.translator.Text(i18n.MsgESMPanelTokens, obj.TokensUsed))
	if obj.TimeUsedMS > 0 {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelTime, formatDurationMSForPanel(obj.TimeUsedMS)))
	}
	if !obj.UpdatedAt.IsZero() {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelLastSaved, formatESMPanelUpdateTime(obj.UpdatedAt)))
	}
	return wrapESMPanelLines(lines, width)
}

func (a *App) esmPanelNow(obj *esm.Objective) string {
	phase := effectiveESMPhase(obj)
	base := a.esmPhaseActivityLabel(phase, obj.Status)

	a.esmMu.Lock()
	id := a.esmActiveAgentID
	a.esmMu.Unlock()
	if id == "" {
		return base
	}
	act := a.agentActivities[id]
	if act == nil {
		return base + "; " + a.translator.Text(i18n.MsgESMPanelSubagentStarting)
	}
	if act.LastTool != "" {
		return base + "; " + a.translator.Text(i18n.MsgESMPanelLatestTool, act.LastTool)
	}
	if act.LastResult != "" {
		return base + "; " + a.translator.Text(i18n.MsgESMPanelLatestResult, act.LastResult)
	}
	if act.LastText != "" {
		return base + "; " + a.translator.Text(i18n.MsgESMPanelLatestResponse, act.LastText)
	}
	if act.LastThink != "" {
		return base + "; reasoning in progress"
	}
	return base + "; sub-agent is running"
}

func esmCompletedStages(phase esm.Phase) int {
	switch phase {
	case esm.PhaseCritic:
		return 1
	case esm.PhaseAudit:
		return 2
	case esm.PhaseComplete:
		return 3
	default:
		return 0
	}
}

func (a *App) esmPanelProgress(obj *esm.Objective, phase esm.Phase) string {
	progress := a.translator.Text(i18n.MsgESMPanelProgress, esmCompletedStages(phase))
	if remaining := len(obj.RemainingWork); remaining > 0 {
		progress += a.translator.Text(i18n.MsgESMPanelWorkRemaining, remaining)
	}
	return progress
}

func (a *App) esmPhaseActivityLabel(phase esm.Phase, status esm.Status) string {
	switch status {
	case esm.StatusPaused:
		return a.translator.Text(i18n.MsgESMPanelESMPaused)
	case esm.StatusBlocked:
		return a.translator.Text(i18n.MsgESMPanelESMBlocked)
	case esm.StatusUsageLimited:
		return a.translator.Text(i18n.MsgESMPanelUsageLimited)
	case esm.StatusComplete:
		return a.translator.Text(i18n.MsgESMPanelAuditPassed)
	}
	switch phase {
	case esm.PhaseCritic:
		return a.translator.Text(i18n.MsgESMPanelCriticReviewing)
	case esm.PhaseAudit:
		return a.translator.Text(i18n.MsgESMPanelAuditing)
	default:
		return a.translator.Text(i18n.MsgESMPanelWorkerInvestigating)
	}
}

func (a *App) esmPanelNextStep(obj *esm.Objective, phase esm.Phase) string {
	switch obj.Status {
	case esm.StatusPaused:
		return a.translator.Text(i18n.MsgESMPanelNextPaused)
	case esm.StatusBlocked:
		return a.translator.Text(i18n.MsgESMPanelNextBlocked)
	case esm.StatusUsageLimited:
		return a.translator.Text(i18n.MsgESMPanelNextUsageLimited)
	case esm.StatusComplete:
		return a.translator.Text(i18n.MsgESMPanelNextComplete)
	}
	switch phase {
	case esm.PhaseCritic:
		return a.translator.Text(i18n.MsgESMPanelNextCritic)
	case esm.PhaseAudit:
		return a.translator.Text(i18n.MsgESMPanelNextAudit)
	default:
		return a.translator.Text(i18n.MsgESMPanelNextWorker)
	}
}

func formatESMPanelUpdateTime(updatedAt time.Time) string {
	ago := time.Since(updatedAt)
	if ago < 0 {
		ago = 0
	}
	return updatedAt.Local().Format("2006-01-02 15:04:05") + " (" + formatDuration(ago) + " ago)"
}

func (a *App) activeESMPanelActivity(width int) []string {
	a.esmMu.Lock()
	id := a.esmActiveAgentID
	a.esmMu.Unlock()
	if id == "" {
		return nil
	}
	act := a.agentActivities[id]
	if act == nil {
		return []string{a.translator.Text(i18n.MsgESMPanelActivityStarting, string(id))}
	}
	lines := []string{a.translator.Text(i18n.MsgESMPanelActivityState, id, act.State)}
	if act.LastTool != "" {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelTool, act.LastTool))
	}
	if act.LastResult != "" {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelLatest, act.LastResult))
	} else if act.LastText != "" {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelLatest, act.LastText))
	} else if act.LastThink != "" {
		lines = append(lines, a.translator.Text(i18n.MsgESMPanelThinking, act.LastThink))
	}
	return wrapESMPanelLines(lines, width)
}

func effectiveESMPhase(obj *esm.Objective) esm.Phase {
	if obj.Phase != "" {
		return obj.Phase
	}
	switch obj.Status {
	case esm.StatusComplete:
		return esm.PhaseComplete
	case esm.StatusCompleteCandidate:
		return esm.PhaseCritic
	default:
		return esm.PhaseWorker
	}
}

func (a *App) esmPhaseLabel(phase esm.Phase) string {
	switch phase {
	case esm.PhaseCritic:
		return a.translator.Text(i18n.MsgESMPanelCriticReview)
	case esm.PhaseAudit:
		return a.translator.Text(i18n.MsgESMPanelFinalAudit)
	case esm.PhaseComplete:
		return a.translator.Text(i18n.MsgESMPanelComplete)
	default:
		return a.translator.Text(i18n.MsgESMPanelWorkerExecution)
	}
}

func (a *App) renderESMPipeline(phase esm.Phase, status esm.Status) string {
	stages := []struct {
		phase esm.Phase
		label string
	}{
		{esm.PhaseWorker, a.translator.Text(i18n.MsgESMPanelWorkerExecution)},
		{esm.PhaseCritic, a.translator.Text(i18n.MsgESMPanelCriticReview)},
		{esm.PhaseAudit, a.translator.Text(i18n.MsgESMPanelFinalAudit)},
	}
	current := esmPhaseIndex(phase)
	parts := make([]string, 0, len(stages))
	for i, stage := range stages {
		marker := " "
		switch {
		case phase == esm.PhaseComplete || i < current:
			marker = "x"
		case i == current && status == esm.StatusPaused:
			marker = "!"
		case i == current:
			marker = ">"
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", marker, stage.label))
	}
	return strings.Join(parts, " -> ")
}

func esmPhaseIndex(phase esm.Phase) int {
	switch phase {
	case esm.PhaseCritic:
		return 1
	case esm.PhaseAudit:
		return 2
	case esm.PhaseComplete:
		return 3
	default:
		return 0
	}
}

func appendWrappedESMField(lines []string, label, value string, width int) []string {
	return append(lines, strings.Split(renderutil.WrapPlainText(label+": "+strings.TrimSpace(value), width), "\n")...)
}

func appendESMItems(lines []string, items []string, width int) []string {
	for i, item := range items {
		wrapped := renderutil.WrapPlainText(fmt.Sprintf("  %d. %s", i+1, item), width)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines
}

func wrapESMPanelLines(lines []string, width int) []string {
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(renderutil.WrapPlainText(line, width), "\n")...)
	}
	return wrapped
}

func formatDurationMSForPanel(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return formatDuration(time.Duration(ms) * time.Millisecond)
}
