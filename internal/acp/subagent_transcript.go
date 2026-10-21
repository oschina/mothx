package acp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
)

// subagentListParams and subagentMessagesParams are the read-only projections
// of one ACP session's managed child agents. They expose no mutation surface:
// spawning, sending, asking, and destroying stay Runtime/tool concerns.
type subagentListParams struct {
	SessionID string `json:"sessionId"`
}

type subagentMessagesParams struct {
	SessionID string `json:"sessionId"`
	AgentID   string `json:"agentId"`
}

type subagentInfo struct {
	AgentID           string `json:"agentId"`
	ParentAgentID     string `json:"parentAgentId,omitempty"`
	MemberID          string `json:"memberId,omitempty"`
	ExpertID          string `json:"expertId,omitempty"`
	MemberDisplayName string `json:"memberDisplayName,omitempty"`
	MemberEmoji       string `json:"memberEmoji,omitempty"`
	MemberRole        string `json:"memberRole,omitempty"`
	Status            string `json:"status"`
	Active            bool   `json:"active"`
	MessageCount      int    `json:"messageCount"`
	LastResponse      string `json:"lastResponse,omitempty"`
	Error             string `json:"error,omitempty"`
	StartedAt         string `json:"startedAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
}

type subagentListResult struct {
	Agents []subagentInfo `json:"agents"`
}

type subagentMessagesResult struct {
	AgentID string          `json:"agentId"`
	Updates []sessionUpdate `json:"updates"`
}

// acpSubagentStatus maps AgentManager lifecycle states onto the same
// running/completed/failed vocabulary the subagent session event already uses.
func acpSubagentStatus(state string) string {
	switch strings.TrimSpace(state) {
	case "", "ready", "running":
		return "running"
	case "done", "completed":
		return "completed"
	case "incomplete":
		return "incomplete"
	case "error", "failed":
		return "failed"
	case "canceled", "cancelled":
		return "cancelled"
	default:
		return state
	}
}

// subagentManager returns the manager that owns a session's children. Team
// experts use the session-scoped manager; explicit multi-agent/delegate modes
// keep the legacy process-wide manager.
func (s *server) subagentManager(sessionID string) (*agent.AgentManager, bool) {
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt != nil && rt.agentMgr != nil {
		return rt.agentMgr, true
	}
	return s.agentMgr, false
}

// sessionSubagentProjections copies the lifecycle projections belonging to one
// session. The copy keeps AgentManager calls outside the server lock.
func (s *server) sessionSubagentProjections(sessionID string) map[string]subagentProjection {
	prefix := sessionID + "\x00"
	out := make(map[string]subagentProjection)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, projection := range s.subagents {
		if !strings.HasPrefix(key, prefix) || projection == nil {
			continue
		}
		out[strings.TrimPrefix(key, prefix)] = *projection
	}
	return out
}

// handleSubagentList projects the current session's managed child agents. The
// list is a union of the lifecycle projection (which is what associates an
// agent with a session) and live AgentManager status. It never creates a
// subagent record of its own.
func (s *server) handleSubagentList(req rpcRequest) {
	var in subagentListParams
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "subagent_invalid_params", "sessionId is required", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	projections := s.sessionSubagentProjections(sessionID)
	manager, scoped := s.subagentManager(sessionID)

	statuses := map[agentpkg.AgentID]agent.ManagedAgentStatus{}
	if manager != nil {
		for _, status := range manager.Statuses() {
			statuses[status.ID] = status
		}
	}

	type entry struct {
		id         string
		projection subagentProjection
		status     *agent.ManagedAgentStatus
	}
	entries := make(map[string]*entry, len(projections))
	for id, projection := range projections {
		entries[id] = &entry{id: id, projection: projection}
	}
	if scoped {
		// A team manager belongs to exactly one session, so every parented
		// status is that session's child even if its lifecycle event has not
		// been observed yet.
		for id, status := range statuses {
			if status.ParentID == "" {
				continue
			}
			agentID := string(id)
			if _, ok := entries[agentID]; !ok {
				entries[agentID] = &entry{id: agentID}
			}
		}
	}

	result := subagentListResult{Agents: make([]subagentInfo, 0, len(entries))}
	for _, item := range entries {
		info := subagentInfo{AgentID: item.id, Status: "running"}
		if item.projection.started {
			info.Status = acpSubagentStatus(item.projection.status)
			if info.Status == "" {
				info.Status = "running"
			}
			info.MemberID = item.projection.memberID
			info.ExpertID = item.projection.expertID
			info.MemberDisplayName = item.projection.memberDisplayName
			info.MemberEmoji = item.projection.memberEmoji
			info.MemberRole = item.projection.memberRole
			if !item.projection.startedAt.IsZero() {
				info.StartedAt = item.projection.startedAt.UTC().Format(time.RFC3339)
			}
		}
		status, ok := statuses[agentpkg.AgentID(item.id)]
		if ok {
			info.ParentAgentID = string(status.ParentID)
			if status.MemberID != "" {
				info.MemberID = status.MemberID
			}
			if status.ExpertID != "" {
				info.ExpertID = status.ExpertID
			}
			if status.MemberDisplayName != "" {
				info.MemberDisplayName = status.MemberDisplayName
			}
			if status.MemberEmoji != "" {
				info.MemberEmoji = status.MemberEmoji
			}
			if status.MemberRole != "" {
				info.MemberRole = status.MemberRole
			}
			info.Status = acpSubagentStatus(status.State)
			info.LastResponse = truncateSubagentText(status.Result)
			info.Error = truncateSubagentText(status.Error)
			if !status.StartedAt.IsZero() {
				info.StartedAt = status.StartedAt.UTC().Format(time.RFC3339)
			}
			if !status.UpdatedAt.IsZero() {
				info.UpdatedAt = status.UpdatedAt.UTC().Format(time.RFC3339)
			}
		}
		if manager != nil {
			if child, found := manager.Get(agentpkg.AgentID(item.id)); found {
				info.Active = true
				info.MessageCount = len(child.GetMessages())
			}
		}
		if info.Status == "" {
			info.Status = "running"
		}
		result.Agents = append(result.Agents, info)
	}
	sort.Slice(result.Agents, func(i, j int) bool {
		left, right := result.Agents[i], result.Agents[j]
		if left.StartedAt != right.StartedAt {
			return left.StartedAt < right.StartedAt
		}
		return left.AgentID < right.AgentID
	})
	s.writeResponse(req.ID, result, nil)
}

// handleSubagentMessages projects one live child agent's in-memory transcript
// as the same ACP session updates the parent stream uses. The Desktop can then
// render it with the ordinary transcript components without a second message
// format.
func (s *server) handleSubagentMessages(req rpcRequest) {
	var in subagentMessagesParams
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.AgentID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "subagent_invalid_params", "sessionId and agentId are required", nil))
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	agentID := strings.TrimSpace(in.AgentID)
	manager, scoped := s.subagentManager(sessionID)
	projections := s.sessionSubagentProjections(sessionID)
	_, known := projections[agentID]
	if !known && !scoped {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32004, "subagent_not_found", fmt.Sprintf("sub-agent %s does not belong to session %s", agentID, sessionID), nil))
		return
	}
	if manager == nil {
		s.writeResponse(req.ID, subagentMessagesResult{AgentID: agentID, Updates: []sessionUpdate{}}, nil)
		return
	}
	child, ok := manager.Get(agentpkg.AgentID(agentID))
	if !ok {
		// Terminal cleanup may drop the live agent while its status remains
		// inspectable. An empty transcript is the documented projection.
		s.writeResponse(req.ID, subagentMessagesResult{AgentID: agentID, Updates: []sessionUpdate{}}, nil)
		return
	}
	messages := agent.MessagesFromPublic(child.GetMessages())
	result := subagentMessagesResult{AgentID: agentID}
	for index, message := range messages {
		result.Updates = append(result.Updates, s.messageUpdates(sessionID, message, fmt.Sprintf("%s:%d", agentID, index))...)
	}
	if result.Updates == nil {
		result.Updates = []sessionUpdate{}
	}
	s.writeResponse(req.ID, result, nil)
}

func truncateSubagentText(text string) string {
	const limit = 2000
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
