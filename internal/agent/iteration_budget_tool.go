package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/oschina/mothx/internal/tools"
)

// ExtendBudgetTool lets the model request more iterations when a run is close to
// its iteration limit but the task is genuinely unfinished. It is stateless: the
// per-run budget handle is carried on the run context, and the Runtime clamps
// every request (hard ceiling, renewal count, minimum interval). It is registered
// only for the session's conversational lead.
type ExtendBudgetTool struct{}

// NewExtendBudgetTool creates the extend_budget tool.
func NewExtendBudgetTool() *ExtendBudgetTool { return &ExtendBudgetTool{} }

func (t *ExtendBudgetTool) Name() string { return IterationBudgetToolName }

func (t *ExtendBudgetTool) Description() string {
	return "Request more agent iterations when the run is close to its iteration limit but the task is genuinely unfinished. The Runtime decides how many turns (if any) to grant."
}

func (t *ExtendBudgetTool) PromptSnippet() string {
	return "Request more iterations when near the iteration limit and the task is unfinished"
}

func (t *ExtendBudgetTool) PromptGuidelines() []string {
	return []string{
		"Call extend_budget only when a [Budget Pressure] notice reports few turns remaining and the task is genuinely unfinished — not to avoid wrapping up",
		"Always give a concrete reason describing the remaining work; the Runtime may refuse the request",
		"Prefer finishing or summarizing the task over extending the budget",
	}
}

func (t *ExtendBudgetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"reason": {"type": "string", "description": "Concrete description of the work that still remains and why more turns are needed. Required."},
			"additional_turns": {"type": "integer", "minimum": 1, "description": "Optional number of additional turns to request. Omit to let the Runtime pick a default grant."}
		},
		"required": ["reason"]
	}`)
}

func (t *ExtendBudgetTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	budget, ok := iterationBudgetFromContext(ctx)
	if !ok {
		return tools.ToolResult{}, fmt.Errorf("iteration budget renewal is not available for this run")
	}
	reason, _ := params["reason"].(string)
	if len(reason) == 0 {
		return tools.ToolResult{}, fmt.Errorf("reason is required to request more turns")
	}
	additional := 0
	if v, ok := params["additional_turns"].(float64); ok && v > 0 {
		additional = int(v)
	}
	granted, remaining, renewals, err := budget.Request(additional, reason)
	if err != nil {
		return tools.ToolResult{}, err
	}
	if granted <= 0 {
		return tools.NewTextToolResult(fmt.Sprintf(
			"Iteration budget is already at its hard ceiling (%d). %d turns remain; finish the task or summarize progress.",
			budget.Hard(), remaining)), nil
	}
	return tools.NewTextToolResult(fmt.Sprintf(
		"Granted %d additional turns. Effective limit is now %d (%d hard), %d turns remaining, %d renewal(s) used.",
		granted, budget.Limit(), budget.Hard(), remaining, renewals)), nil
}
