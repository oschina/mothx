package openaiapi

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/agent"
)

// externalSubAgentHistory retains channel-owned sub-agent activity. Channel
// dispatchers own their AgentManager, so their child transcripts cannot be read
// from an APISession's AgentMgr. Keeping this small WebUI projection in the
// serve runtime makes those live transcripts available through the same
// sub-agent endpoints as API-owned sessions.
type externalSubAgentHistory struct {
	mu       sync.RWMutex
	agents   map[string]SessionSubAgentInfo
	messages map[string][]SessionMessageEntry
}

type externalSubAgentUpdate struct {
	changed       bool
	recoveredText string
}

func (s *Server) externalSubAgentHistoryFor(sessionID string) *externalSubAgentHistory {
	if s == nil || sessionID == "" {
		return nil
	}
	s.externalSubAgentMu.Lock()
	defer s.externalSubAgentMu.Unlock()
	if s.externalSubAgents == nil {
		s.externalSubAgents = make(map[string]*externalSubAgentHistory)
	}
	if s.externalSubAgents[sessionID] == nil {
		s.externalSubAgents[sessionID] = &externalSubAgentHistory{
			agents: make(map[string]SessionSubAgentInfo), messages: make(map[string][]SessionMessageEntry),
		}
	}
	return s.externalSubAgents[sessionID]
}

func (h *externalSubAgentHistory) update(sessionID string, ev agent.Event) externalSubAgentUpdate {
	if h == nil || sessionID == "" || ev.AgentID == "" {
		return externalSubAgentUpdate{}
	}
	id := string(ev.AgentID)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	h.mu.Lock()
	defer h.mu.Unlock()
	info := h.agents[id]
	if info.ID == "" {
		info = SessionSubAgentInfo{ID: id, Status: "running", Active: true, StartedAt: now}
	} else if !info.Active && (info.Status == "done" || info.Status == "incomplete" || info.Status == "error" || info.Status == "canceled") {
		// Terminal state is sticky: the manager status listener and the parent
		// event stream can both deliver the same terminal event.
		return externalSubAgentUpdate{}
	}
	info.UpdatedAt = now
	mergeSubAgentMemberMetadata(&info, ev)
	entries := h.messages[id]
	update := externalSubAgentUpdate{changed: true}

	switch ev.Type {
	case agent.EventTextDelta:
		if ev.TextDelta != "" {
			if n := len(entries); n > 0 && entries[n-1].Role == "assistant" {
				entries[n-1].Content += ev.TextDelta
			} else {
				entries = append(entries, SessionMessageEntry{ID: fmt.Sprintf("%s:assistant:%d", id, len(entries)), Role: "assistant", AgentID: id, Content: ev.TextDelta})
			}
		}
	case agent.EventToolCall:
		name, callID := resolveToolEvent(ev)
		entries = append(entries, transcriptToolCallEntry(name, callID, ev))
	case agent.EventToolExecutionEnd:
		status := "completed"
		if ev.ToolError != nil {
			status = "failed"
		}
		entries = append(entries, transcriptToolResultEntry(ev.ToolName, ev, status))
	case agent.EventRunFinished:
		entries, update.recoveredText = reconcileExternalAssistantResult(entries, id, ev.StatusMessage)
		switch ev.Status {
		case agent.TaskFailed:
			info.Status = "error"
			info.Error = safeAgentErrorMessage(ev.Error)
		case agent.TaskIncomplete:
			info.Status = "incomplete"
			info.Error = safeAgentErrorMessage(ev.Error)
		case agent.TaskCanceled:
			info.Status = "canceled"
			info.Error = safeAgentErrorMessage(ev.Error)
		default:
			info.Status = "done"
		}
		info.Active = false
		info.LastResponse = lastExternalAssistantResponse(entries)
		entries = append(entries, externalSubAgentStatusEntry(id, info.Status, info.Error))
	case agent.EventDone:
		info.Status = "done"
		info.Active = false
		info.LastResponse = lastExternalAssistantResponse(entries)
		entries = append(entries, externalSubAgentStatusEntry(id, "done", ""))
	case agent.EventError:
		info.Status = "error"
		info.Active = false
		info.Error = safeAgentErrorMessage(ev.Error)
		entries = append(entries, externalSubAgentStatusEntry(id, "error", info.Error))
	}

	for i := range entries {
		entries[i].AgentID = id
	}
	info.MessageCount = len(entries)
	h.agents[id] = info
	h.messages[id] = entries
	return update
}

// mergeSubAgentMemberMetadata copies the immutable child-event identity only
// when it is present. Generic sub-agents and ESM workers intentionally keep
// these fields empty; the serve projection never attempts to infer a persona
// from a mutable expert bundle.
func mergeSubAgentMemberMetadata(info *SessionSubAgentInfo, ev agent.Event) {
	if info == nil || ev.MemberID == "" {
		return
	}
	info.MemberID = ev.MemberID
	info.ExpertID = ev.ExpertID
	info.MemberDisplayName = ev.MemberDisplayName
	info.MemberEmoji = ev.MemberEmoji
	info.MemberRole = ev.MemberRole
}

func reconcileExternalAssistantResult(entries []SessionMessageEntry, agentID, result string) ([]SessionMessageEntry, string) {
	if result == "" {
		return entries, ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role != "assistant" {
			continue
		}
		existing := entries[i].Content
		if existing == result || strings.HasPrefix(existing, result) {
			return entries, ""
		}
		entries[i].Content = result
		if strings.HasPrefix(result, existing) {
			return entries, result[len(existing):]
		}
		return entries, result
	}
	entries = append(entries, SessionMessageEntry{
		ID: fmt.Sprintf("%s:assistant:%d", agentID, len(entries)), Role: "assistant", AgentID: agentID, Content: result,
	})
	return entries, result
}

func externalSubAgentStatusEntry(agentID, status, summary string) SessionMessageEntry {
	return SessionMessageEntry{
		ID:      fmt.Sprintf("%s:status:%s:%s", agentID, status, summary),
		Role:    "status",
		AgentID: agentID,
		Content: status,
		Summary: summary,
		IsError: status == "error",
	}
}

func lastExternalAssistantResponse(entries []SessionMessageEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "assistant" && entries[i].Content != "" {
			return entries[i].Content
		}
	}
	return ""
}

func (h *externalSubAgentHistory) list() []SessionSubAgentInfo {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]SessionSubAgentInfo, 0, len(h.agents))
	for _, info := range h.agents {
		out = append(out, info)
	}
	return out
}

func (h *externalSubAgentHistory) transcript(agentID string) ([]SessionMessageEntry, bool) {
	if h == nil || agentID == "" {
		return nil, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	entries, ok := h.messages[agentID]
	if !ok {
		return nil, false
	}
	return append([]SessionMessageEntry(nil), entries...), true
}

// NewExternalSubAgentServer creates the minimal serve-owned event/history sink
// used by external runtimes such as messaging channel dispatchers.
func NewExternalSubAgentServer() *Server {
	return &Server{eventBroker: NewEventBroker(), pool: NewSessionPool(0, 0)}
}

// SubscribeSessionEvents subscribes to live broker events for a session.
func (s *Server) SubscribeSessionEvents(sessionID string) (<-chan BrokerEvent, func()) {
	if s == nil {
		ch := make(chan BrokerEvent)
		close(ch)
		return ch, func() {}
	}
	return s.getEventBroker().Subscribe(sessionID)
}

func (s *Server) PublishExternalSubAgentEvent(sessionID string, ev agent.Event) {
	if s == nil || sessionID == "" || ev.AgentID == "" {
		return
	}
	history := s.externalSubAgentHistoryFor(sessionID)
	update := history.update(sessionID, ev)
	if !update.changed {
		return
	}

	runID := s.activeRunIDForSession(sessionID)
	if update.recoveredText != "" {
		s.getEventBroker().PublishTranscriptEvent(sessionID, runID, assistantDeltaTranscriptEvent(update.recoveredText, ev.AgentID, ev))
	}
	switch ev.Type {
	case agent.EventTextDelta:
		s.getEventBroker().PublishTranscriptEvent(sessionID, runID, assistantDeltaTranscriptEvent(ev.TextDelta, ev.AgentID, ev))
	case agent.EventToolCall:
		name, callID := resolveToolEvent(ev)
		s.publishToolEvent(sessionID, ToolStatusEvent{Tool: name, ToolCallID: callID, AgentID: string(ev.AgentID), Status: "running", Args: ev.ToolArgs})
	case agent.EventToolExecutionEnd:
		status := "completed"
		if ev.ToolError != nil {
			status = "failed"
		}
		s.publishToolEvent(sessionID, ToolStatusEvent{Tool: ev.ToolName, ToolCallID: ev.ToolCallID, AgentID: string(ev.AgentID), Status: status, Args: ev.ToolArgs, Summary: toolStatusSummary(ev.ToolResult, ev.ToolError), IsError: ev.ToolError != nil, HasDetail: ev.ToolCallID != ""})
	case agent.EventRunFinished:
		summary := ""
		if ev.Status != agent.TaskSuccess {
			summary = safeAgentErrorMessage(ev.Error)
		}
		s.getEventBroker().PublishTranscriptEvent(sessionID, runID, subAgentStatusTranscriptEvent(ev.AgentID, subAgentStatusForTaskStatus(ev.Status), summary, ev))
	case agent.EventDone:
		s.getEventBroker().PublishTranscriptEvent(sessionID, runID, subAgentStatusTranscriptEvent(ev.AgentID, "done", "", ev))
	case agent.EventError:
		summary := safeAgentErrorMessage(ev.Error)
		s.getEventBroker().PublishTranscriptEvent(sessionID, runID, subAgentStatusTranscriptEvent(ev.AgentID, "error", summary, ev))
	}
}
