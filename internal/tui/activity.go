package tui

import (
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/tui/i18n"
)

const maxActivityLines = 200

type activityLine struct {
	Time time.Time
	Text string
}

type agentActivity struct {
	AgentID           agentpkg.AgentID
	MemberID          string
	MemberDisplayName string
	MemberEmoji       string
	Kind              string
	State             string
	LastThink         string
	LastText          string
	LastTool          string
	LastResult        string
	FullThink         string
	FullText          string
	FullResult        string
	LastToolName      string
	LastToolArgs      map[string]any
	UpdatedAt         time.Time
	Events            []activityLine
}

func (a *App) isBackgroundAgentEvent(event agent.Event) bool {
	if event.AgentID == "" {
		return false
	}
	if a.agent != nil && event.AgentID == a.agent.ID() {
		return false
	}
	switch event.Type {
	case agent.EventToolApprovalRequest,
		agent.EventQuestionRequest,
		agent.EventToolApprovalResponse,
		agent.EventQuestionResponse:
		return false
	}
	return true
}

func (a *App) recordAgentActivity(event agent.Event) {
	if event.AgentID == "" {
		return
	}
	a.invalidateToolModalCache()
	if a.agentActivities == nil {
		a.agentActivities = make(map[agentpkg.AgentID]*agentActivity)
	}
	act := a.agentActivities[event.AgentID]
	if act == nil {
		act = &agentActivity{
			AgentID: event.AgentID,
			Kind:    "subagent",
			State:   "running",
		}
		if strings.HasPrefix(string(event.AgentID), "workflow:") {
			act.Kind = "workflow"
		}
		a.agentActivities[event.AgentID] = act
		a.agentActivityOrder = appendUniqueActivityID(a.agentActivityOrder, event.AgentID)
	}
	if event.MemberID != "" {
		act.MemberID = event.MemberID
		act.MemberDisplayName = event.MemberDisplayName
		act.MemberEmoji = event.MemberEmoji
	}

	now := time.Now()
	act.UpdatedAt = now
	switch event.Type {
	case agent.EventStatus:
		if event.RetryStatus {
			break
		}
		if event.StatusMessage != "" {
			act.LastResult = truncatePlain(event.StatusMessage, 160)
			act.Events = appendActivityLine(act.Events, now, event.StatusMessage)
		}
	case agent.EventRetry:
		act.State = "running"
		line := a.retryStatusMessage(event)
		act.LastResult = truncatePlain(line, 160)
		act.Events = appendActivityLine(act.Events, now, line)
	case agent.EventThinkDelta:
		act.State = "running"
		act.LastThink = truncatePlain(act.LastThink+event.ThinkDelta, 240)
		act.FullThink += event.ThinkDelta
	case agent.EventTextDelta:
		act.State = "running"
		act.LastText = truncatePlain(act.LastText+event.TextDelta, 320)
		act.FullText += event.TextDelta
	case agent.EventHostedItem:
		act.State = "running"
		if event.HostedItem != nil {
			line := a.translator.Text(i18n.MsgActivityHostedItem)
			if event.HostedItem.Type != "" {
				line += " [" + event.HostedItem.Type + "]"
			}
			if event.HostedItem.Status != "" {
				line += ": " + event.HostedItem.Status
			}
			act.LastResult = truncatePlain(line, 160)
			act.Events = appendActivityLine(act.Events, now, line)
		}
	case agent.EventToolCall, agent.EventToolExecutionStart:
		act.State = "running"
		name := event.ToolName
		if name == "" && event.ToolCall != nil {
			name = event.ToolCall.Name
		}
		if name != "" {
			act.LastTool = formatActivityTool(name, event.ToolArgs)
			act.LastToolName = name
			act.LastToolArgs = event.ToolArgs
			act.Events = appendActivityLine(act.Events, now, a.translator.Text(i18n.MsgActivityToolStarted, formatDetailedActivityTool(name, event.ToolArgs)))
		}
	case agent.EventToolResult, agent.EventToolExecutionEnd:
		name := event.ToolName
		if name == "" && event.ToolCall != nil {
			name = event.ToolCall.Name
		}
		result := strings.TrimSpace(event.ToolResult)
		if event.ToolError != nil {
			act.State = "error"
			info := agentruntime.ClassifyError(event.ToolError, agentruntime.ErrorClassificationOptions{
				Phase: agentruntime.PhaseTool, SideEffectState: agentruntime.SideEffectUnknown,
			})
			result = agentruntime.DisplayErrorMessage(info)
		}
		if result != "" {
			act.LastResult = truncatePlain(result, 320)
			act.FullResult = result
		}
		if name != "" || result != "" {
			line := a.translator.Text(i18n.MsgActivityToolResult)
			if name != "" {
				line += " [" + name + "]"
			}
			if result != "" {
				line += ":\n" + result
			}
			act.Events = appendActivityLine(act.Events, now, line)
		}
	case agent.EventRunFinished:
		if isTerminalActivityState(act.State) {
			break
		}
		switch event.Status {
		case agent.TaskFailed:
			act.State = "error"
			message := activityFailureMessage(event.Error)
			act.LastResult = truncatePlain(message, 320)
			act.FullResult = message
			act.Events = appendActivityLine(act.Events, now, a.translator.Text(i18n.MsgActivityError, message))
		case agent.TaskCanceled:
			act.State = "canceled"
			act.Events = appendActivityLine(act.Events, now, a.translator.Text(i18n.MsgActivityCanceled))
		default:
			act.State = "done"
			act.Events = appendActivityLine(act.Events, now, a.translator.Text(i18n.MsgActivityDone))
		}
	case agent.EventDone:
		if isTerminalActivityState(act.State) {
			break
		}
		act.State = "done"
		act.Events = appendActivityLine(act.Events, now, a.translator.Text(i18n.MsgActivityDone))
	case agent.EventError:
		if isTerminalActivityState(act.State) {
			break
		}
		act.State = "error"
		message := activityFailureMessage(event.Error)
		act.LastResult = truncatePlain(message, 320)
		act.FullResult = message
		act.Events = appendActivityLine(act.Events, now, a.translator.Text(i18n.MsgActivityError, message))
	}
}

// activityFailureMessage is a presentation-only projection of the shared
// Runtime failure contract. It must not expose the raw provider error carried
// by legacy Agent events.
func activityFailureMessage(err error) string {
	info := agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel})
	if message := strings.TrimSpace(agentruntime.DisplayErrorMessage(info)); message != "" {
		return message
	}
	return "The run could not be completed."
}

func isTerminalActivityState(state string) bool {
	return state == "done" || state == "error" || state == "canceled"
}

func appendUniqueActivityID(ids []agentpkg.AgentID, id agentpkg.AgentID) []agentpkg.AgentID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func appendActivityLine(lines []activityLine, t time.Time, text string) []activityLine {
	text = strings.TrimSpace(text)
	if text == "" {
		return lines
	}
	lines = append(lines, activityLine{Time: t, Text: text})
	if len(lines) > maxActivityLines {
		lines = lines[len(lines)-maxActivityLines:]
	}
	return lines
}

func formatDetailedActivityTool(name string, args map[string]any) string {
	if details := strings.TrimSpace(formatToolArgs(name, args)); details != "" {
		return name + "\n" + details
	}
	return name
}

func formatActivityTool(name string, args map[string]any) string {
	if len(args) == 0 {
		return name
	}
	var parts []string
	for _, key := range []string{"path", "cmd", "query", "pattern", "handle", "message", "source", "task"} {
		if v, ok := args[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%q", key, truncatePlain(fmt.Sprint(v), 80)))
		}
	}
	if len(parts) == 0 {
		return name
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

func truncatePlain(s string, max int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func (a *App) renderActivitySummary(width int) string {
	if len(a.agentActivityOrder) == 0 {
		return ""
	}
	limit := 4
	var lines []string
	for i := len(a.agentActivityOrder) - 1; i >= 0 && len(lines) < limit; i-- {
		id := a.agentActivityOrder[i]
		act := a.agentActivities[id]
		if act == nil {
			continue
		}
		detail := act.LastTool
		if detail == "" {
			detail = act.LastResult
		}
		if detail == "" {
			detail = act.LastText
		}
		if detail == "" {
			detail = act.LastThink
		}
		state := act.State
		if state == "" {
			state = "running"
		}
		line := fmt.Sprintf("%s [%s]", id, state)
		if detail != "" {
			line += " " + detail
		}
		if width > 0 {
			line = truncatePlain(line, width-2)
		}
		lines = append(lines, statusStyle.Render(line))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
