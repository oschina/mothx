package esm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/tools"
)

type sessionIDFunc func() string
type runIDFunc func() string

// NewGetTool returns the model-facing ESM state query tool.
func NewGetTool(store *Store, sessionID sessionIDFunc) tools.Tool {
	return &getTool{store: store, sessionID: sessionID}
}

// NewUpdateTool returns the model-facing ESM status update tool.
func NewUpdateTool(store *Store, sessionID sessionIDFunc, runID ...runIDFunc) tools.Tool {
	var runIDFn runIDFunc
	if len(runID) > 0 {
		runIDFn = runID[0]
	}
	return &updateTool{store: store, sessionID: sessionID, runID: runIDFn}
}

type getTool struct {
	store     *Store
	sessionID sessionIDFunc
}

func (t *getTool) Name() string { return "get_esm" }

func (t *getTool) Description() string {
	return "Inspect the current Enable Supervisor Mode objective, status, and progress."
}

func (t *getTool) PromptSnippet() string {
	return "Inspect the current Enable Supervisor Mode objective, status, and progress."
}

func (t *getTool) PromptGuidelines() []string {
	return []string{
		"When an ESM objective is active, use get_esm if you need current status and update_esm only to propose complete with evidence or report a real blocker.",
	}
}

func (t *getTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (t *getTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.store == nil || t.sessionID == nil || t.sessionID() == "" {
		return tools.NewTextToolResult("No ESM objective is available for this session."), nil
	}
	obj, err := t.store.Get(ctx, t.sessionID())
	if errors.Is(err, ErrNotFound) {
		return tools.NewTextToolResult("No ESM objective is available for this session."), nil
	}
	if err != nil {
		return tools.ToolResult{}, err
	}
	return tools.NewTextToolResult(FormatObjective(obj)), nil
}

type updateTool struct {
	store     *Store
	sessionID sessionIDFunc
	runID     runIDFunc
}

func (t *updateTool) Name() string { return "update_esm" }

func (t *updateTool) Description() string {
	return "Propose the current Enable Supervisor Mode objective as complete with requirement-by-requirement evidence, or report a concrete repeated blocker."
}

func (t *updateTool) PromptSnippet() string {
	return "Propose the current ESM objective complete with verification evidence, or report a blocker for the supervisor audit."
}

func (t *updateTool) PromptGuidelines() []string {
	return []string{
		"Use update_esm status=complete only to submit a complete_candidate when current evidence appears to prove every objective requirement is satisfied and no required work remains; include that evidence in reason.",
		"status=complete is not terminal: ESM will run an independent audit before the objective can actually stop.",
		"Do not mark complete for a demo, partial implementation, narrow passing check, plausible final answer, or because this run is ending.",
		"Use update_esm status=blocked only after the same concrete blocker repeats across at least three consecutive ESM agent runs; include the blocker in reason.",
	}
}

func (t *updateTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"status":{"type":"string","enum":["complete","blocked"]},
			"reason":{"type":"string","description":"Required. For complete, provide concise verification evidence covering the full objective. For blocked, provide the repeated concrete blocker."}
		},
		"required":["status","reason"],
		"additionalProperties":false
	}`)
}

func (t *updateTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	if t.store == nil || t.sessionID == nil || t.sessionID() == "" {
		return tools.ToolResult{}, fmt.Errorf("no ESM session is available")
	}
	status := Status(strings.TrimSpace(stringParam(params, "status")))
	reason := strings.TrimSpace(stringParam(params, "reason"))
	runID := ""
	if t.runID != nil {
		runID = t.runID()
	}
	obj, err := t.store.UpdateFromModelForRun(ctx, t.sessionID(), status, reason, runID)
	if err != nil {
		return tools.ToolResult{}, err
	}
	switch obj.Status {
	case StatusCompleteCandidate:
		return tools.NewTextToolResult("ESM completion candidate recorded. An independent audit must pass before the objective is marked complete."), nil
	case StatusComplete:
		return tools.NewTextToolResult("ESM objective marked complete. Report the verification evidence and final state to the user."), nil
	case StatusBlocked:
		return tools.NewTextToolResult(fmt.Sprintf("ESM objective marked blocked after %d matching blocker reports.", BlockedAuditLimit)), nil
	default:
		if status == StatusBlocked {
			return tools.NewTextToolResult(fmt.Sprintf("Blocked audit recorded (%d/%d). ESM remains active until the same blocker repeats in %d consecutive agent runs.", obj.BlockedCount, BlockedAuditLimit, BlockedAuditLimit)), nil
		}
		return tools.NewTextToolResult(FormatObjective(obj)), nil
	}
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	switch v := v.(type) {
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// FormatObjective returns a compact plain-text representation safe for TUI and tools.
func FormatObjective(obj *Objective) string {
	if obj == nil {
		return "No ESM objective is available for this session."
	}
	var b strings.Builder
	b.WriteString("Enable Supervisor Mode\n")
	b.WriteString(fmt.Sprintf("Status: %s\n", obj.Status))
	if obj.Phase != "" {
		b.WriteString(fmt.Sprintf("Phase: %s\n", obj.Phase))
	}
	b.WriteString(fmt.Sprintf("Objective: %s\n", obj.Objective))
	b.WriteString(fmt.Sprintf("Tokens: %d\n", obj.TokensUsed))
	if obj.TimeUsedMS > 0 {
		b.WriteString(fmt.Sprintf("Time: %s\n", formatDurationMS(obj.TimeUsedMS)))
	}
	if obj.BlockedCount > 0 && obj.BlockedReason != "" {
		b.WriteString(fmt.Sprintf("Blocked audit: %d/%d (%s)\n", obj.BlockedCount, BlockedAuditLimit, obj.BlockedReason))
	}
	if obj.ProgressSummary != "" {
		b.WriteString(fmt.Sprintf("Latest progress: %s\n", obj.ProgressSummary))
	}
	if len(obj.RemainingWork) > 0 {
		b.WriteString(fmt.Sprintf("Remaining work (%d): %s\n", len(obj.RemainingWork), strings.Join(obj.RemainingWork, "; ")))
	}
	if obj.RejectionCount > 0 {
		b.WriteString(fmt.Sprintf("Completion rejections: %d\n", obj.RejectionCount))
	}
	if obj.CompletionReason != "" {
		b.WriteString(fmt.Sprintf("Completion candidate: %s\n", obj.CompletionReason))
	}
	if obj.CompletionReview != "" {
		b.WriteString(fmt.Sprintf("Completion audit: %s\n", obj.CompletionReview))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatDurationMS(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	return fmt.Sprintf("%dh%dm", hours, minutes%60)
}
