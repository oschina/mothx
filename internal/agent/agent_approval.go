package agent

import (
	"context"
	"fmt"
	"strings"
)

// NeedsApproval checks if a tool call needs user approval based on the current mode.
func (a *Agent) NeedsApproval(toolName string, args map[string]any) bool {
	if (toolName == "write" || toolName == "edit") && a.config.Mode == "agent" {
		// Auto-approve edits globally when AllowAutoEdit is on.
		if a.config.Allow != nil && a.config.Allow.GetAutoEdit() {
			return false
		}
		// Auto-approve edits whose path matches the allow.json whitelist.
		if a.config.Allow != nil {
			if p, ok := args["path"].(string); ok && a.config.Allow.MatchEditPath(p) {
				return false
			}
		}
		return a.config.Settings != nil &&
			a.config.Settings.Approval.ConfirmBeforeWrite != nil &&
			*a.config.Settings.Approval.ConfirmBeforeWrite
	}
	if toolName != "bash" {
		return false
	}
	if a.isBashBlacklisted(args) {
		return true
	}
	switch a.config.Mode {
	case "plan":
		// Plan mode: no tools should be executed (read-only tools don't need approval)
		return false
	case "agent":
		// Agent mode: project allow rules and settings whitelists can skip approval.
		if a.isBashProjectAllowed(args) {
			return false
		}
		return !a.isBashWhitelisted(args)
	case "yolo", "os":
		// YOLO and OS modes: allow bash unless explicitly blacklisted above.
		return false
	default:
		return false
	}
}

func (a *Agent) isBashProjectAllowed(args map[string]any) bool {
	if a.config.Allow == nil {
		return false
	}
	command, ok := bashCommandArg(args)
	if !ok {
		return false
	}
	return a.config.Allow.MatchBashCommand(command)
}

func (a *Agent) isBashWhitelisted(args map[string]any) bool {
	if a.config.Settings == nil {
		return false
	}
	command, ok := bashCommandArg(args)
	if !ok {
		return false
	}
	for _, prefix := range a.config.Settings.Approval.BashWhitelist {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func (a *Agent) isBashBlacklisted(args map[string]any) bool {
	if a.config.Settings == nil {
		return false
	}
	command, ok := bashCommandArg(args)
	if !ok {
		return false
	}
	for _, prefix := range a.config.Settings.Approval.BashBlacklist {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func bashCommandArg(args map[string]any) (string, bool) {
	for _, key := range []string{"command", "cmd"} {
		command, ok := args[key].(string)
		if !ok {
			continue
		}
		command = strings.TrimSpace(command)
		if command != "" {
			return command, true
		}
	}
	return "", false
}

// RequestApproval sends an approval request and waits for the user's response.
// It has no context-bounded cancellation and is kept for callers that only have
// an event channel; new callers inside the loop use RequestToolApproval with
// the run context so a cancelled run can unblock the wait.
func (a *Agent) RequestApproval(ch chan<- Event, toolName string, args map[string]any) bool {
	return a.RequestToolApproval(context.Background(), ch, "", toolName, args)
}

// RequestToolApproval sends an approval request that retains the provider
// call identity, allowing a durable server runtime to match a decision after
// recovery without confusing concurrent function calls.
//
// The approval ID embeds the Agent ID so decision registries keyed by ID
// (TUI/WebUI/ACP durable decision records) stay unique when several agents of
// one run raise their own first approval. Per-instance counters alone would
// collide ("approval-1" from a lead and from each sub-agent).
func (a *Agent) RequestToolApproval(ctx context.Context, ch chan<- Event, toolCallID, toolName string, args map[string]any) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	a.approvalMu.Lock()
	a.approvalCounter++
	approvalID := fmt.Sprintf("approval-%s-%d", a.id, a.approvalCounter)
	responseCh := make(chan bool, 1)
	a.pendingApprovals[approvalID] = responseCh
	a.approvalMu.Unlock()

	// Send approval request event. It goes through the context-aware send so a
	// cancelled run stops here instead of parking on a full channel whose
	// consumer already stopped reading (the cancellation select below would never
	// be reached).
	a.sendEvent(ch, Event{
		Type:         EventToolApprovalRequest,
		ToolCallID:   toolCallID,
		ApprovalID:   approvalID,
		ApprovalTool: toolName,
		ApprovalArgs: args,
	})

	// Wait for response, abort, or run cancellation. Watching ctx.Done() is what
	// lets a cancelled run finish: without it a child agent parked in an approval
	// prompt never returns, the parent's tool batch never completes, and the run
	// can never write a terminal state.
	select {
	case approved := <-responseCh:
		return approved
	case <-a.abort:
		a.dropPendingApproval(approvalID)
		return false
	case <-ctx.Done():
		a.dropPendingApproval(approvalID)
		return false
	}
}

func (a *Agent) dropPendingApproval(approvalID string) {
	a.approvalMu.Lock()
	delete(a.pendingApprovals, approvalID)
	a.approvalMu.Unlock()
}

// HandleApprovalResponse processes the user's approval response.
func (a *Agent) HandleApprovalResponse(approvalID string, approved bool) {
	a.approvalMu.Lock()
	defer a.approvalMu.Unlock()

	if ch, ok := a.pendingApprovals[approvalID]; ok {
		ch <- approved
		delete(a.pendingApprovals, approvalID)
	}
}

// RequestQuestion sends a question request and waits for the user's answer.
// It returns an empty string when the agent is aborted or the context is
// canceled, so unattended runtimes (e.g. channel sessions) never block a run
// forever on an answer that cannot arrive.
func (a *Agent) RequestQuestion(ctx context.Context, ch chan<- Event, question string, options []string, context string) string {
	a.questionMu.Lock()
	a.questionCounter++
	// The question ID embeds the Agent ID for the same reason as approval IDs:
	// decision registries keyed by ID must stay unique across the agents of one
	// run.
	questionID := fmt.Sprintf("question-%s-%d", a.id, a.questionCounter)
	responseCh := make(chan string, 1)
	a.pendingQuestions[questionID] = responseCh
	a.questionMu.Unlock()

	// The question request follows the same context-aware send contract as the
	// approval request: a cancelled run must not park on an undeliverable event
	// before it can honor ctx.Done below.
	a.sendEvent(ch, Event{
		Type:            EventQuestionRequest,
		QuestionID:      questionID,
		QuestionText:    question,
		QuestionOptions: options,
		QuestionContext: context,
	})

	select {
	case answer := <-responseCh:
		return answer
	case <-a.abort:
		a.questionMu.Lock()
		delete(a.pendingQuestions, questionID)
		a.questionMu.Unlock()
		return ""
	case <-ctx.Done():
		a.questionMu.Lock()
		delete(a.pendingQuestions, questionID)
		a.questionMu.Unlock()
		return ""
	}
}

// HandleQuestionResponse processes the user's answer to a question. It keeps the
// silent contract for protocol adapters (an unknown or already resolved ID is
// ignored); callers that answer on another agent's behalf must use
// DeliverQuestionAnswer so they never report a false success.
func (a *Agent) HandleQuestionResponse(questionID string, answer string) {
	a.DeliverQuestionAnswer(questionID, answer)
}

// DeliverQuestionAnswer resolves a pending question and reports whether the
// answer was actually delivered. Callers that answer on someone else's behalf
// (subagent_answer routing a member's question to its lead) use the result to
// distinguish a delivered answer from a question that was already resolved,
// expired, or never existed — reporting success there would tell the lead a
// blocked member had been unblocked when it was not.
func (a *Agent) DeliverQuestionAnswer(questionID string, answer string) bool {
	if a == nil || questionID == "" {
		return false
	}
	a.questionMu.Lock()
	defer a.questionMu.Unlock()
	ch, ok := a.pendingQuestions[questionID]
	if !ok {
		return false
	}
	ch <- answer
	delete(a.pendingQuestions, questionID)
	return true
}

// AskQuestion implements the tools.QuestionAsker interface.
// It gets the event channel from the context and delegates to RequestQuestion.
func (a *Agent) AskQuestion(ctx context.Context, question string, options []string, explanation string) string {
	eventCh, ok := EventChanFromContext(ctx)
	if !ok {
		return ""
	}
	return a.RequestQuestion(ctx, eventCh, question, options, explanation)
}
