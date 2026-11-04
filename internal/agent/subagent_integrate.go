package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/tools"
)

const (
	maxChangeSetSelections = 8
	subAgentIntegrateTool  = "subagent_integrate"
)

// SubAgentIntegrateTool lets the read-only Router lead select immutable child
// snapshots for one Runtime-owned integration turn. It never applies patches or
// reads child worktrees itself.
type SubAgentIntegrateTool struct {
	manager *AgentManager
}

func NewSubAgentIntegrateTool(manager *AgentManager) tools.Tool {
	return &SubAgentIntegrateTool{manager: manager}
}

func (t *SubAgentIntegrateTool) Name() string { return subAgentIntegrateTool }
func (t *SubAgentIntegrateTool) Description() string {
	return "Apply selected immutable change-set snapshots from finished external Router workers through the Runtime-owned exclusive integration lane. Pass each child handle and the exact changeSetDigest returned by subagent_status, subagent_wait, or the blocking delegate worker envelope. A multi-selection is transactional: if a later snapshot conflicts, earlier snapshots from the same call are rolled back."
}
func (t *SubAgentIntegrateTool) PromptSnippet() string {
	return "Integrate selected worker change sets through the Runtime-owned lane"
}
func (t *SubAgentIntegrateTool) PromptGuidelines() []string {
	return []string{
		"Use this only after the child is terminal and its changeSetDigest is visible in subagent_status, subagent_wait, or the blocking delegate worker envelope",
		"Pass the exact digest; a missing, truncated, or mismatched snapshot is rejected instead of being guessed",
		"Treat a conflict or failed integration as review evidence; a failed multi-selection rolls back the snapshots it applied in that call",
	}
}

func (t *SubAgentIntegrateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"changes": {
				"type": "array",
				"minItems": 1,
				"maxItems": 8,
				"items": {
					"type": "object",
					"properties": {
						"handle": {"type": "string", "description": "Canonical child handle returned by subagent_spawn"},
						"digest": {"type": "string", "description": "Exact changeSetDigest from subagent_status or subagent_wait"}
					},
					"required": ["handle", "digest"]
				}
			}
		},
		"required": ["changes"]
	}`)
}

func (t *SubAgentIntegrateTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.manager == nil {
		return tools.ToolResult{}, fmt.Errorf("agent manager is not initialized")
	}
	selections, err := decodeChangeSetSelections(params["changes"])
	if err != nil {
		return tools.ToolResult{}, err
	}
	result, err := t.manager.IntegrateChangeSets(ctx, selections)
	if err != nil {
		return tools.ToolResult{}, err
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return tools.ToolResult{}, fmt.Errorf("encode integration result: %w", marshalErr)
	}
	return tools.NewTextToolResult(string(encoded)), nil
}

func decodeChangeSetSelections(raw any) ([]ChangeSetSelection, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("changes must contain at least one {handle, digest} selection")
	}
	if len(items) > maxChangeSetSelections {
		return nil, fmt.Errorf("changes exceeds %d selections", maxChangeSetSelections)
	}
	selections := make([]ChangeSetSelection, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("changes[%d] must be an object", index)
		}
		handle, _ := fields["handle"].(string)
		digest, _ := fields["digest"].(string)
		handle, digest = strings.TrimSpace(handle), strings.TrimSpace(digest)
		if handle == "" || digest == "" {
			return nil, fmt.Errorf("changes[%d] requires non-empty handle and digest", index)
		}
		key := handle + "\x00" + digest
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("change-set selection %q appears twice", handle)
		}
		seen[key] = struct{}{}
		selections = append(selections, ChangeSetSelection{Handle: handle, Digest: digest})
	}
	return selections, nil
}
