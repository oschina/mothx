package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/startvibecoding/mothx/internal/tools"
)

// Bounded wait window for subagent_wait. These are tool ergonomics, not user
// configuration: they intentionally stay package constants and must not enter
// the settings schema.
const (
	subAgentWaitMinTimeoutMS     = 2500
	subAgentWaitMaxTimeoutMS     = 120000
	subAgentWaitDefaultTimeoutMS = 30000
)

// SubAgentWaitTool blocks the calling (lead) agent until the session member
// mailbox reports activity or a bounded timeout elapses. It only merges the
// caller into already-scheduled member completions; like the mailbox itself it
// never wakes or starts a run. The result lists pending completions without
// their payloads — content is delivered by the mailbox drain at the next
// iteration boundary.
type SubAgentWaitTool struct {
	manager *AgentManager
}

// NewSubAgentWaitTool creates the subagent_wait tool.
func NewSubAgentWaitTool(m *AgentManager) tools.Tool {
	return &SubAgentWaitTool{manager: m}
}

func (t *SubAgentWaitTool) Name() string { return "subagent_wait" }
func (t *SubAgentWaitTool) Description() string {
	return "Wait for expert-team member activity: blocks until a spawned member or sub-agent completion lands in the session mailbox, or the bounded timeout elapses. Returns only a pending summary (member id, status); completion content is delivered automatically at the next iteration boundary."
}
func (t *SubAgentWaitTool) PromptSnippet() string {
	return "Wait for a member completion when the critical path is blocked"
}
func (t *SubAgentWaitTool) PromptGuidelines() []string {
	return []string{
		"Use subagent_wait only when the critical path is blocked on a member's result; do non-overlapping local work while members run",
		"Completion content is delivered automatically after the wait returns; never call subagent_wait again immediately after a wait (no reflexive waiting)",
	}
}

func (t *SubAgentWaitTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"timeout_ms": {"type": "integer", "description": "Maximum wait time in milliseconds (clamped to 2500-120000, default 30000)"}
		}
	}`)
}

func (t *SubAgentWaitTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	timeoutMS := resolveSubAgentWaitTimeoutMS(params)

	if t.manager == nil || t.manager.Mailbox == nil {
		return newSubAgentWaitResult("no member mailbox is bound to this session", false, nil), nil
	}
	mailbox := t.manager.Mailbox

	timedOut := false
	if !mailbox.HasPending() {
		var err error
		timedOut, err = mailbox.WaitForActivity(ctx, time.Duration(timeoutMS)*time.Millisecond)
		if err != nil {
			return tools.ToolResult{}, fmt.Errorf("subagent_wait: %w", err)
		}
	}
	message := "Wait completed."
	if timedOut {
		message = "Wait timed out."
	}
	return newSubAgentWaitResult(message, timedOut, mailbox.PendingSummary()), nil
}

// resolveSubAgentWaitTimeoutMS applies the bounded wait window: default 30000,
// clamped to [2500, 120000] when timeout_ms is provided.
func resolveSubAgentWaitTimeoutMS(params map[string]any) int {
	timeoutMS := subAgentWaitDefaultTimeoutMS
	switch v := params["timeout_ms"].(type) {
	case float64:
		timeoutMS = int(v)
	case int:
		timeoutMS = v
	}
	if timeoutMS < subAgentWaitMinTimeoutMS {
		timeoutMS = subAgentWaitMinTimeoutMS
	}
	if timeoutMS > subAgentWaitMaxTimeoutMS {
		timeoutMS = subAgentWaitMaxTimeoutMS
	}
	return timeoutMS
}

// subAgentWaitPending is one summary entry of the wait result. It never
// carries the completion payload; content delivery belongs to the mailbox
// drain at the iteration boundary. A pending member question carries its
// question ID so the lead can answer it with subagent_answer even when it only
// inspects this projection.
type subAgentWaitPending struct {
	Member      string `json:"member"`
	Status      string `json:"status"`
	DisplayName string `json:"display_name,omitempty"`
	QuestionID  string `json:"question_id,omitempty"`
}

func newSubAgentWaitResult(message string, timedOut bool, pending []MemberCompletion) tools.ToolResult {
	payload := struct {
		Message  string                `json:"message"`
		TimedOut bool                  `json:"timed_out"`
		Pending  []subAgentWaitPending `json:"pending,omitempty"`
	}{Message: message, TimedOut: timedOut}
	for _, c := range pending {
		payload.Pending = append(payload.Pending, subAgentWaitPending{
			Member:      c.MemberID,
			Status:      c.Status,
			DisplayName: c.DisplayName,
			QuestionID:  c.QuestionID,
		})
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return tools.NewTextToolResult(fmt.Sprintf(`{"message":%q,"timed_out":%t}`, message, timedOut))
	}
	return tools.NewTextToolResult(string(data))
}
