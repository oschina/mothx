package channels

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// reconcileCompletedBackgroundRun delivers a completed channel background
// result after a dispatcher restart. Delivery state lives in run events, while
// the message body remains in the canonical session transcript.
func (d *Dispatcher) reconcileCompletedBackgroundRun(sess *ChannelSession, progress func(string)) {
	if d == nil || sess == nil || progress == nil || sess.Manager == nil {
		return
	}
	header := sess.Manager.GetHeader()
	if header == nil || header.ID == "" {
		return
	}
	events, err := session.ListSessionRunEvents(d.sessionDir, header.ID)
	if err != nil {
		return
	}
	replay := agentruntime.ReplayRunEvents(events, "")
	type pendingDelivery struct {
		runID          string
		assistantEntry string
		progress       []string
	}
	pendingRecords := agentruntime.ReplayDeliveries(events)
	pending := make(map[string]pendingDelivery, len(pendingRecords))
	progressByRun := make(map[string][]string)
	for _, event := range replay.Events {
		if !isChannelRunSource(event.Source) || event.EventType != "tool_progress" {
			continue
		}
		var progress struct {
			Tool    string `json:"tool"`
			Status  string `json:"status"`
			Summary string `json:"summary"`
		}
		if json.Unmarshal(event.Data, &progress) != nil || (strings.TrimSpace(progress.Tool) == "" && strings.TrimSpace(progress.Status) == "") {
			continue
		}
		line := strings.TrimSpace(fmt.Sprintf("Tool %s %s", progress.Tool, progress.Status))
		if strings.TrimSpace(progress.Summary) != "" && progress.Summary != "(empty result)" {
			line += ": " + progress.Summary
		}
		if line != "" {
			progressByRun[event.RunID] = append(progressByRun[event.RunID], line)
		}
	}
	for runID, record := range pendingRecords {
		pending[runID] = pendingDelivery{runID: runID, assistantEntry: record.AssistantEntry, progress: progressByRun[runID]}
	}
	if len(pending) == 0 {
		return
	}
	messages, err := session.ListSessionMessagesAfter(d.sessionDir, header.ID, 0, 500)
	if err != nil {
		return
	}
	for runID, item := range pending {
		var message *provider.Message
		for _, candidate := range messages {
			if item.assistantEntry != "" && candidate.EntryID == item.assistantEntry {
				value := candidate.Message
				message = &value
				break
			}
		}
		if message == nil {
			continue
		}
		for _, line := range item.progress {
			progress(line)
		}
		text := strings.TrimSpace(message.Content)
		if text == "" {
			for _, block := range message.Contents {
				if block.Type == "text" {
					text += block.Text
				}
			}
			text = strings.TrimSpace(text)
		}
		if summary := FormatAttachmentSummary(message.Attachments); summary != "" {
			if text != "" {
				text += "\n\n"
			}
			text += summary
		}
		if text != "" {
			progress(text)
		}
		_, _ = (agentruntime.SessionRunEventSink{SessionDir: d.sessionDir}).Record(agentruntime.NewDeliveryReconciledEvent(
			header.ID, runID, "channel", json.RawMessage(`{"reason":"dispatcher_restart"}`),
		))
	}
}

func isChannelRunSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case string(agentruntime.SourceWeChat), string(agentruntime.SourceFeishu), "channel:wechat", "channel:feishu":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "channel:")
	}
}
