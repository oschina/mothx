package tui

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/tools"
	"github.com/startvibecoding/mothx/internal/tui/i18n"
)

func (a *App) handleAgentEvent(event agent.Event) tea.Cmd {
	if a.isBackgroundAgentEvent(event) {
		a.recordAgentActivity(event)
		a.projectMemberLifecycle(event)
		if event.Type == agent.EventStatus {
			a.refreshESMPanel()
		}
		a.scheduleRender()
		return a.listenAgentEvents()
	}

	var observedError *agentruntime.ErrorInfo
	if a.run != nil && a.run.execution != nil {
		observation, err := a.run.execution.ObserveAgentEvent(event)
		if err != nil {
			// Preserve the operational detail in logs, while rendering only the
			// Runtime's safe ErrorInfo below.
			log.Printf("[tui] observe agent event: %v", err)
		} else if observation.Error != nil {
			info := *observation.Error
			observedError = &info
		}
	}

	switch event.Type {
	case agent.EventTextDelta:
		a.invalidateToolModalCache()
		if a.currentAssistantIdx >= 0 && a.currentAssistantIdx < len(a.messages) {
			a.appendAssistantDelta(a.currentAssistantIdx, event.TextDelta)
		} else {
			a.currentAssistantIdx = len(a.messages)
			a.assistantRaw[a.currentAssistantIdx] = ""
			a.appendAssistantDelta(a.currentAssistantIdx, event.TextDelta)
			// placeholder; actual display is built in updateViewportContent
			a.messages = append(a.messages, "")
		}
		a.assistantDirty[a.currentAssistantIdx] = true
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventThinkDelta:
		a.invalidateToolModalCache()
		if a.thinkRaw == nil {
			a.thinkRaw = make(map[int]string)
		}
		if a.currentThinkIdx >= 0 && a.currentThinkIdx < len(a.messages) {
			a.appendThinkDelta(a.currentThinkIdx, event.ThinkDelta)
		} else {
			if a.currentAssistantIdx >= 0 &&
				a.currentAssistantIdx == len(a.messages)-1 &&
				a.assistantRaw[a.currentAssistantIdx] == "" {
				a.currentThinkIdx = a.currentAssistantIdx
				delete(a.assistantRaw, a.currentAssistantIdx)
				delete(a.assistantBuilders, a.currentAssistantIdx)
				delete(a.assistantRendered, a.currentAssistantIdx)
				delete(a.assistantDirty, a.currentAssistantIdx)
				a.currentAssistantIdx = len(a.messages)
				a.assistantRaw[a.currentAssistantIdx] = ""
				a.assistantDirty[a.currentAssistantIdx] = true
				a.messages = append(a.messages, "")
			} else {
				a.currentThinkIdx = len(a.messages)
				a.messages = append(a.messages, "")
			}
			a.thinkRaw[a.currentThinkIdx] = ""
			a.appendThinkDelta(a.currentThinkIdx, event.ThinkDelta)
		}
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventHostedItem:
		if event.HostedItem != nil {
			line := a.translator.Text(i18n.MsgHostedItemTitle)
			if event.HostedItem.Type != "" {
				line += " [" + event.HostedItem.Type + "]"
			}
			if event.HostedItem.Status != "" {
				line += ": " + event.HostedItem.Status
			}
			a.addEventMessage(statusStyle.Render(line), a.shouldShowHostedItem(event.HostedItem))
		}
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventTurnStart:
		a.invalidateToolModalCache()
		// Reserve display slots before streaming deltas arrive so later tool output
		// cannot shift the assistant message index underneath us.
		a.currentAssistantIdx = len(a.messages)
		a.assistantRaw[a.currentAssistantIdx] = ""
		a.messages = append(a.messages, "")
		return a.listenAgentEvents()

	case agent.EventToolCall:
		if event.ToolCall != nil {
			a.appendToolExecutionStart(event.ToolCall.ID, event.ToolCall.Name, event.ToolArgs)
		}
		return a.listenAgentEvents()

	case agent.EventToolExecutionStart:
		a.appendToolExecutionStart(event.ToolCallID, event.ToolName, event.ToolArgs)
		return a.listenAgentEvents()

	case agent.EventToolExecutionEnd:
		a.appendToolResult(event)
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventToolResult:
		a.appendToolResult(event)
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventPlanUpdate:
		a.currentPlan = event.Plan
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventToolApprovalRequest:
		a.commitActiveStream()
		nextApproval := pendingApproval{
			agentID:    event.AgentID,
			approvalID: event.ApprovalID,
			toolName:   event.ApprovalTool,
			args:       event.ApprovalArgs,
		}
		if a.hasPendingApproval(nextApproval) {
			a.scheduleRender()
			return a.listenAgentEvents()
		}
		if a.run != nil {
			if err := a.run.registerDecision(event.ApprovalID, agentruntime.DecisionApproval); err != nil {
				a.addCommandError(fmt.Sprintf("duplicate approval request: %v", err))
				return a.listenAgentEvents()
			} else {
				_ = a.run.decisions.Bind(event.ApprovalID, func(value string) error {
					approved := value != "false"
					if event.AgentID != "" && a.agentMgr != nil {
						if target, ok := a.agentMgr.Get(event.AgentID); ok {
							target.HandleApprovalResponse(event.ApprovalID, approved)
							return nil
						}
					}
					if a.agent != nil {
						a.agent.HandleApprovalResponse(event.ApprovalID, approved)
					}
					return nil
				})
				_ = a.run.waitForApproval()
			}
		}
		a.approvalQueue = append(a.approvalQueue, nextApproval)
		// If not currently waiting, show the next one
		if !a.waitingForApproval {
			a.showNextApproval()
		}
		a.scheduleRender()
		if a.isThinking {
			return a.listenAgentEvents()
		}
		return tea.Batch(a.listenAgentEvents(), a.tickSpinner())

	case agent.EventQuestionRequest:
		a.commitActiveStream()
		if a.run != nil {
			if err := a.run.registerDecision(event.QuestionID, agentruntime.DecisionQuestion); err != nil {
				a.addCommandError(fmt.Sprintf("duplicate question request: %v", err))
				return a.listenAgentEvents()
			}
			_ = a.run.waitForQuestion()
		}
		// Queue the question request
		a.questionQueue = append(a.questionQueue, pendingQuestion{
			questionID: event.QuestionID,
			question:   event.QuestionText,
			options:    event.QuestionOptions,
			context:    event.QuestionContext,
		})
		// If not currently waiting for a question, show the next one
		if !a.waitingForQuestion {
			a.showNextQuestion()
		}
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventTurnEnd:
		a.invalidateToolModalCache()
		if event.ContextUsage != nil {
			a.contextUsage = event.ContextUsage
		}
		if a.currentThinkIdx >= 0 {
			a.finalizeThinkStream(a.currentThinkIdx)
			a.printMessageOnce(a.currentThinkIdx)
		}
		if a.currentAssistantIdx >= 0 {
			a.finalizeAssistantStream(a.currentAssistantIdx)
			a.printMessageOnce(a.currentAssistantIdx)
		}
		a.currentAssistantIdx = -1
		a.currentThinkIdx = -1
		a.updateViewportContent()
		return a.listenAgentEvents()

	case agent.EventRunFinished:
		a.runTerminalHandled = true
		if a.run != nil {
			state := agentruntime.RunStateCompleted
			switch event.Status {
			case agent.TaskFailed:
				state = agentruntime.RunStateFailed
			case agent.TaskCanceled:
				state = agentruntime.RunStateCancelled
			}
			a.run.finish(state)
			a.run = nil
		}
		// No tool can still be executing once the run is terminal.
		a.finalizeInterruptedTools()
		// The durable transition and lease release above complete before a
		// queued prompt may create its successor run.
		a.isThinking = false
		nextPrompt := a.scheduleNextQueuedPrompt()
		switch event.Status {
		case agent.TaskFailed:
			a.commitActiveStream()
			if a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil {
				a.agentMgr.MarkError(a.agent.ID(), errors.New(a.formatAgentError(event, observedError)))
			}
			a.isThinking = false
			a.finishRequestTimer()
			a.addMessage(errorStyle.Render(a.translator.Text(i18n.MsgErrorPrefix)) + a.formatAgentError(event, observedError))
			a.pendingAbortReason = ""
			a.currentAssistantIdx = -1
			a.currentThinkIdx = -1
			a.refreshESMPanel()
			a.updateViewportContent()
			return tea.Batch(a.timer.Stop(), a.listenAgentEvents(), a.finishESMRun(event.Error), nextPrompt)
		case agent.TaskIncomplete:
			a.commitActiveStream()
			if a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil {
				a.agentMgr.MarkIncomplete(a.agent.ID(), event.Error)
			}
			a.isThinking = false
			a.finishRequestTimer()
			a.addMessage(warningStyle.Render(a.translator.Text(i18n.MsgSessionEndedPrefix)) + "incomplete")
			a.pendingAbortReason = ""
			a.currentAssistantIdx = -1
			a.currentThinkIdx = -1
			a.refreshESMPanel()
			a.updateViewportContent()
			return tea.Batch(a.timer.Stop(), a.listenAgentEvents(), a.finishESMRun(event.Error), nextPrompt)

		case agent.TaskCanceled:
			a.commitActiveStream()
			if a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil {
				a.agentMgr.MarkCanceled(a.agent.ID(), event.Error)
			}
			// Agent.Abort is intentionally terminal for an Agent instance. A
			// Runtime cancellation (for example, a lost execution lease) reaches
			// the same abort path as an explicit TUI cancellation. Do not reuse
			// that instance for the next prompt: its closed abort channel would
			// cancel every subsequent run immediately.
			a.resetAgent(errors.New("agent run canceled"))
			a.isThinking = false
			a.finishRequestTimer()
			// Cancellation is a normal terminal outcome, not an error.
			a.addMessage(statusStyle.Render(a.translator.Text(i18n.MsgSessionEndedPrefix)) + a.translator.Text(i18n.MsgToolModalStateCanceled))
			a.pendingAbortReason = ""
			a.currentAssistantIdx = -1
			a.currentThinkIdx = -1
			a.refreshESMPanel()
			a.updateViewportContent()
			return tea.Batch(a.timer.Stop(), a.listenAgentEvents(), a.finishESMRun(nil), nextPrompt)
		case agent.TaskSuccess:
			if isOutputTruncationStopReason(event.StopReason) {
				a.addMessage(warningStyle.Render(a.translator.Text(i18n.MsgOutputTruncated)))
			}
			if attachmentText := a.formatTUIAttachmentSummary(event.Attachments); attachmentText != "" {
				a.addMessage(statusStyle.Render(attachmentText))
			}
			a.invalidateToolModalCache()
			if a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil {
				a.agentMgr.MarkDone(a.agent.ID(), "")
			}
			a.isThinking = false
			a.finishRequestTimer()
			if event.ContextUsage != nil {
				a.contextUsage = event.ContextUsage
			}
			if a.currentThinkIdx >= 0 {
				a.finalizeThinkStream(a.currentThinkIdx)
				a.printMessageOnce(a.currentThinkIdx)
			}
			if a.currentAssistantIdx >= 0 {
				a.finalizeAssistantStream(a.currentAssistantIdx)
				a.printMessageOnce(a.currentAssistantIdx)
			}
			a.currentAssistantIdx = -1
			a.currentThinkIdx = -1
			a.refreshESMPanel()
			a.updateViewportContent()
			return tea.Batch(a.timer.Stop(), a.listenAgentEvents(), a.finishESMRun(nil), nextPrompt)
		}

	case agent.EventDone:
		if a.runTerminalHandled {
			return a.listenAgentEvents()
		}
		a.finalizeInterruptedTools()
		if isOutputTruncationStopReason(event.StopReason) {
			a.addMessage(warningStyle.Render(a.translator.Text(i18n.MsgOutputTruncated)))
		}
		if attachmentText := a.formatTUIAttachmentSummary(event.Attachments); attachmentText != "" {
			a.addMessage(statusStyle.Render(attachmentText))
		}
		a.invalidateToolModalCache()
		if a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil {
			a.agentMgr.MarkDone(a.agent.ID(), "")
		}
		a.isThinking = false
		a.finishRequestTimer()
		if event.ContextUsage != nil {
			a.contextUsage = event.ContextUsage
		}
		if a.currentThinkIdx >= 0 {
			a.finalizeThinkStream(a.currentThinkIdx)
			a.printMessageOnce(a.currentThinkIdx)
		}
		if a.currentAssistantIdx >= 0 {
			a.finalizeAssistantStream(a.currentAssistantIdx)
			a.printMessageOnce(a.currentAssistantIdx)
		}
		a.currentAssistantIdx = -1
		a.currentThinkIdx = -1
		a.refreshESMPanel()
		a.updateViewportContent()
		return tea.Batch(a.timer.Stop(), a.listenAgentEvents(), a.finishESMRun(nil))

	case agent.EventRetry:
		a.commitActiveStream()
		a.addMessage(statusStyle.Render(a.retryStatusMessage(event)))
		a.isThinking = true
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventError:
		if a.runTerminalHandled {
			return a.listenAgentEvents()
		}
		a.finalizeInterruptedTools()
		a.commitActiveStream()
		if a.agentManagementEnabled() && a.agentMgr != nil && a.agent != nil {
			a.agentMgr.MarkError(a.agent.ID(), errors.New(a.formatAgentError(event, observedError)))
		}
		a.isThinking = false
		a.finishRequestTimer()
		a.addMessage(errorStyle.Render(a.translator.Text(i18n.MsgErrorPrefix)) + a.formatAgentError(event, observedError))
		a.pendingAbortReason = ""
		a.currentAssistantIdx = -1
		a.currentThinkIdx = -1
		a.refreshESMPanel()
		a.updateViewportContent()
		return tea.Batch(a.timer.Stop(), a.listenAgentEvents(), a.finishESMRun(event.Error))

	case agent.EventUsage:
		if event.ContextUsage != nil {
			a.contextUsage = event.ContextUsage
		}
		if event.Usage != nil {
			a.latestUsage = cloneUsage(event.Usage)
			a.recordESMUsage(event.Usage)
			// Accumulate cache stats
			a.totalInputTokens += event.Usage.TotalInputTokens()
			a.totalCacheRead += event.Usage.CacheRead
			a.totalCacheWrite += event.Usage.CacheWrite
			a.totalCostUSD += event.Usage.Cost.Total

			// Per-turn cache info
			cacheInfo := ""
			if info := event.Usage.CacheInfo(); info != "" {
				cacheInfo = " | " + info
			}
			costStr := a.translator.Text(i18n.MsgUsageTokens,
				event.Usage.TotalInputTokens(), event.Usage.Output, event.Usage.Cost.Total, cacheInfo)
			a.addEventMessage(statusStyle.Render(costStr), !a.compactMode)
			a.refreshESMPanel()
		}
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventCompactionStart:
		a.addEventMessage(statusStyle.Render(a.translator.Text(i18n.MsgCompacting)), !a.compactMode)
		return a.listenAgentEvents()

	case agent.EventCompactionEnd:
		if event.Error == nil && a.agent != nil {
			a.contextUsage = a.agent.GetContextUsage()
		}
		if event.Error != nil {
			info := agentruntime.ClassifyError(event.Error, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseContext})
			log.Printf("[tui] context compaction failed: %v", event.Error)
			a.addMessage(errorStyle.Render(a.translator.Text(i18n.MsgCompactionFailed)) + agentruntime.DisplayErrorMessage(info))
		} else if event.StopReason == "canceled" {
			a.addMessage(statusStyle.Render(event.StatusMessage))
		} else if event.StatusMessage != "" {
			a.addEventMessage(statusStyle.Render("✅ "+event.StatusMessage), !a.compactMode)
		} else {
			a.addEventMessage(statusStyle.Render(a.translator.Text(i18n.MsgContextCompacted)), !a.compactMode)
		}
		a.scheduleRender()
		return a.listenAgentEvents()

	case agent.EventContextPressure, agent.EventBudgetPressure:
		if event.ContextUsage != nil {
			a.contextUsage = event.ContextUsage
		}
		if event.PressureMessage != "" {
			a.addMessage(warningStyle.Render(event.PressureMessage))
		}
		return a.listenAgentEvents()

	case agent.EventStatus:
		if event.RetryStatus {
			// RetryStatus is a compatibility projection for the following EventRetry.
			// New adapters should consume EventRetry instead.
			return a.listenAgentEvents()
		}
		if event.StatusMessage != "" {
			a.addEventMessage(statusStyle.Render(event.StatusMessage), !a.compactMode || isImportantEventStatus(event.StatusMessage))
		}
		a.refreshESMPanel()
		return a.listenAgentEvents()

	case agent.EventMessageStart:
		if event.Message.Role == "user" && event.Message.Content != "" && !event.Message.SystemInjected {
			a.addMessage(userStyle.Render(a.translator.Text(i18n.MsgYouPrefix)) + event.Message.Content)
		}
		return a.listenAgentEvents()

	default:
		return a.listenAgentEvents()
	}
	return a.listenAgentEvents()
}

// projectMemberLifecycle renders only canonical member start/terminal events
// as transcript blocks. The Runtime remains the owner of member state; this is
// a Bubble Tea projection of immutable event metadata rather than a second
// member state machine.
func (a *App) projectMemberLifecycle(event agent.Event) {
	if event.MemberID == "" {
		return
	}
	label := event.MemberID
	if event.MemberDisplayName != "" {
		label = event.MemberDisplayName + " (" + event.MemberID + ")"
	}
	if event.MemberEmoji != "" {
		label = event.MemberEmoji + " " + label
	}
	switch event.Type {
	case agent.EventAgentStart:
		a.addEventMessage(statusStyle.Render("Member started: "+label), true)
	case agent.EventRunFinished:
		state := string(event.Status)
		if state == "" {
			state = "completed"
		}
		a.addEventMessage(statusStyle.Render("Member "+state+": "+label), true)
	}
}

// retryStatusMessage renders the friendly i18n retry summary from the
// structured metadata emitted by Agent Core. When the provider supplied a
// sanitized diagnostic (EventRetry.StatusMessage), it is appended as a
// supplementary detail line so the friendly display stays intact.
func (a *App) retryStatusMessage(event agent.Event) string {
	friendly := a.friendlyRetryStatus(event)
	// truncatePlain collapses whitespace and bounds the length, so even a
	// malformed detail cannot flood the status view.
	detail := truncatePlain(event.StatusMessage, 240)
	if detail == "" {
		return friendly
	}
	return friendly + "\n  ↳ " + detail
}

// friendlyRetryStatus builds the translated retry summary from structured
// fields only; it never parses presentation text.
func (a *App) friendlyRetryStatus(event agent.Event) string {
	if event.RetryMaxTokens > 0 {
		return a.translator.Text(i18n.MsgOutputRetry, event.RetryMaxTokens)
	}
	if event.RetryAttempt > 0 && event.RetryMaxAttempts > 0 {
		if event.RetryAfterMS > 0 {
			return a.translator.Text(
				i18n.MsgAutomaticRetryWaiting,
				event.RetryAttempt,
				event.RetryMaxAttempts,
				formatRetryDelay(event.RetryAfterMS),
			)
		}
		return a.translator.Text(i18n.MsgAutomaticRetry, event.RetryAttempt, event.RetryMaxAttempts)
	}
	return a.translator.Text(i18n.MsgAutomaticRetryUnknown)
}

func formatRetryDelay(milliseconds int) string {
	return (time.Duration(milliseconds) * time.Millisecond).String()
}

func (a *App) formatTUIAttachmentSummary(items []provider.Attachment) string {
	if len(items) == 0 {
		return ""
	}
	lines := []string{a.translator.Text(i18n.MsgAttachmentsTitle)}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		label := strings.TrimSpace(item.Name)
		if label == "" {
			label = strings.TrimSpace(item.Kind)
		}
		if label == "" {
			label = a.translator.Text(i18n.MsgAttachmentFallback)
		}
		target := strings.TrimSpace(item.URL)
		if target == "" {
			target = strings.TrimSpace(item.ProviderRef)
		}
		if target == "" {
			continue
		}
		key := label + "\x00" + target
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, "- "+label+": "+target)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func isOutputTruncationStopReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max-tokens", "length", "max_output_tokens", "token_limit":
		return true
	default:
		return false
	}
}

func (a *App) formatAgentError(event agent.Event, observed *agentruntime.ErrorInfo) string {
	info := agentruntime.ErrorInfo{}
	if observed != nil {
		info = *observed
	} else {
		info = agentruntime.ClassifyError(event.Error, agentruntime.ErrorClassificationOptions{
			Phase: agentruntime.PhaseModel,
		})
	}
	msg := strings.TrimSpace(agentruntime.DisplayErrorMessage(info))
	if msg == "" {
		msg = "The run could not be completed."
	}
	if event.StopReason == "aborted" && a.pendingAbortReason != "" && !strings.Contains(msg, a.pendingAbortReason) {
		msg += a.translator.Text(i18n.MsgReasonSuffix, a.pendingAbortReason)
	}
	return msg
}

func (a *App) appendToolExecutionStart(toolCallID, toolName string, toolArgs map[string]any) {
	if toolName == "" {
		return
	}
	if a.hasToolEntry(toolCallID, toolResultStatusRunning) || a.hasToolEntry(toolCallID, toolResultStatusCompleted) {
		return
	}

	a.invalidateToolModalCache()
	a.commitActiveStream()
	msgIdx := len(a.messages)
	runningEntry := toolResult{
		toolCallID: toolCallID,
		toolName:   toolName,
		toolArgs:   toolArgs,
		status:     toolResultStatusRunning,
		msgIndex:   msgIdx,
	}
	a.toolResults = append(a.toolResults, runningEntry)
	a.messages = append(a.messages, "")
	runningLine := formatToolExecutionStartWithTranslator(a.translator, runningEntry)
	if runningLine != "" {
		a.messages[msgIdx] = toolStyle.Render(runningLine)
	}
	a.updateViewportContent()
}

func (a *App) appendToolResult(event agent.Event) {
	if a.hasToolEntry(event.ToolCallID, toolResultStatusCompleted) {
		return
	}

	a.invalidateToolModalCache()
	matchedArgs := event.ToolArgs
	matchedName := event.ToolName
	for j := len(a.toolResults) - 1; j >= 0; j-- {
		if a.toolResults[j].toolCallID == event.ToolCallID {
			if matchedArgs == nil {
				matchedArgs = a.toolResults[j].toolArgs
			}
			if matchedName == "" {
				matchedName = a.toolResults[j].toolName
			}
			break
		}
	}

	for j := len(a.toolResults) - 1; j >= 0; j-- {
		if a.toolResults[j].toolCallID != event.ToolCallID || a.toolResults[j].status != toolResultStatusRunning {
			continue
		}
		resultEntry := &a.toolResults[j]
		resultEntry.toolName = matchedName
		resultEntry.toolArgs = matchedArgs
		resultEntry.status = toolResultStatusCompleted
		resultEntry.fullContent = event.ToolResult
		resultEntry.diff = event.ToolDiff
		resultEntry.summary = a.summarizeToolResult(matchedName, event.ToolResult, event.ToolDiff)
		resultEntry.toolError = toolEventErrorMessage(event.ToolError)
		resultEntry.executionState = event.ToolExecutionState
		resultEntry.expanded = ""
		// The running row was never printed. Print the coalesced terminal row
		// exactly once, regardless of compact/full display mode.
		a.printMessageOnce(resultEntry.msgIndex)
		a.updateViewportContent()
		return
	}

	if a.hasToolEntry(event.ToolCallID, toolResultStatusInterrupted) {
		// No running row is waiting for this result and the call was already
		// committed as canceled when the run terminalized. A late straggler from
		// the aborted run must not open a second transcript row for the same call.
		return
	}

	msgIdx := len(a.messages)
	resultEntry := toolResult{
		toolCallID:     event.ToolCallID,
		toolName:       matchedName,
		toolArgs:       matchedArgs,
		status:         toolResultStatusCompleted,
		msgIndex:       msgIdx,
		fullContent:    event.ToolResult,
		diff:           event.ToolDiff,
		summary:        a.summarizeToolResult(matchedName, event.ToolResult, event.ToolDiff),
		toolError:      toolEventErrorMessage(event.ToolError),
		executionState: event.ToolExecutionState,
	}

	a.toolResults = append(a.toolResults, resultEntry)
	a.messages = append(a.messages, "")
	a.printMessageOnce(msgIdx)
}

// finalizeInterruptedTools terminalizes tool rows that were still running when
// the run reached a terminal outcome (cancellation, failure, or a dropped event
// stream). renderLiveTranscriptContent keeps a running tool in the managed
// viewport until its terminal result arrives, so a row left in the running state
// would stay pinned above the input forever instead of moving to terminal
// scrollback. Rows that already carry a result are untouched.
func (a *App) finalizeInterruptedTools() {
	changed := false
	for i := range a.toolResults {
		result := &a.toolResults[i]
		if result.status != toolResultStatusRunning {
			continue
		}
		result.status = toolResultStatusInterrupted
		result.executionState = "interrupted"
		// The running row was never printed; commit the terminal row exactly once.
		a.printMessageOnce(result.msgIndex)
		changed = true
	}
	if changed {
		a.invalidateToolModalCache()
		a.updateViewportContent()
	}
}

func toolEventErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *App) shouldShowHostedItem(item *provider.HostedItem) bool {
	if item == nil || !a.compactMode {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status == "" {
		return true
	}
	switch status {
	case "completed", "complete", "done", "failed", "error", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func isImportantEventStatus(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, marker := range []string{"warning", "error", "failed", "denied", "canceled", "cancelled", "permission"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (a *App) hasToolEntry(toolCallID string, status toolResultStatus) bool {
	if toolCallID == "" {
		return false
	}
	for _, result := range a.toolResults {
		if result.toolCallID == toolCallID && result.status == status {
			return true
		}
	}
	return false
}

func (a *App) summarizeToolResult(toolName, result string, diff *tools.FileDiff) string {
	switch toolName {
	case "bash":
		return compactBashOutput(result)
	case "read":
		lines := strings.Split(result, "\n")
		return a.translator.Text(i18n.MsgToolResultLines, len(lines))
	case "ls":
		return compactBashOutput(result)
	case "write":
		if summary := summarizeFileDiff(diff); summary != "" {
			return summary
		}
		return summarizeWriteToolResult(result)
	case "edit":
		if summary := summarizeFileDiff(diff); summary != "" {
			return summary
		}
		return a.translator.Text(i18n.MsgToolResultApplied)
	default:
		return truncate(result, 50)
	}
}
