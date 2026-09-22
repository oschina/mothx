package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	internalagent "github.com/oschina/mothx/internal/agent"
)

// AgentHost runs workflow tasks through the existing AgentManager.
type AgentHost struct {
	Manager       *internalagent.AgentManager
	ParentID      agentpkg.AgentID
	ParentMode    string
	ParentEventCh chan<- internalagent.Event
	ParentRunCtx  context.Context
}

func (h *AgentHost) RunAgent(ctx context.Context, task AgentTask) (AgentResult, error) {
	if h.Manager == nil {
		return AgentResult{}, fmt.Errorf("agent manager is not initialized")
	}
	mode := task.Mode
	if mode == "" {
		mode = h.ParentMode
	}
	if mode == "" {
		mode = "yolo"
	}
	maxIter := task.MaxIterations
	if maxIter <= 0 {
		maxIter = 50
	}

	multiAgent := false
	delegateMode := false
	workflows := false
	runCtx := ctx
	if h.ParentRunCtx != nil {
		var cancel context.CancelFunc
		runCtx, cancel = contextWithEitherCancel(ctx, h.ParentRunCtx)
		defer cancel()
	}

	a, err := h.Manager.Create(internalagent.AgentOptions{
		ID:                workflowAgentID(task.Name, task.InstanceKey),
		ParentID:          h.ParentID,
		Mode:              mode,
		WorkDir:           task.WorkDir,
		Tools:             task.Tools,
		SystemPromptExtra: task.SystemPromptExtra,
		MaxIterations:     maxIter,
		MultiAgent:        &multiAgent,
		DelegateMode:      &delegateMode,
		Workflows:         &workflows,
	})
	if err != nil {
		return AgentResult{}, fmt.Errorf("create workflow worker: %w", err)
	}
	defer func() { _ = h.Manager.Destroy(a.ID()) }()

	started := time.Now()
	h.Manager.MarkRunning(a.ID())
	var runErr error
	resultStatus := StatusDone
	completed := false
	for ev := range a.Run(runCtx, buildTaskPrompt(task)) {
		if ev.Type == agentpkg.EventToolApprovalRequest && h.ParentEventCh != nil {
			_ = sendParentEvent(runCtx, h.ParentEventCh, internalagent.Event{
				Type:         internalagent.EventToolApprovalRequest,
				AgentID:      a.ID(),
				ApprovalID:   ev.ApprovalID,
				ApprovalTool: ev.ApprovalTool,
				ApprovalArgs: ev.ApprovalArgs,
			})
		}
		internalagent.ForwardChildAgentEvent(runCtx, h.ParentEventCh, a.ID(), ev)
		switch ev.Type {
		case agentpkg.EventRunFinished:
			completed = true
			switch ev.Status {
			case agentpkg.TaskFailed:
				runErr = ev.Error
				if runErr == nil {
					runErr = fmt.Errorf("workflow worker failed")
				}
				h.Manager.MarkError(a.ID(), runErr)
			case agentpkg.TaskIncomplete:
				runErr = ev.Error
				h.Manager.MarkIncomplete(a.ID(), runErr)
				resultStatus = StatusIncomplete
			case agentpkg.TaskCanceled:
				resultStatus = StatusCanceled
				runErr = ev.Error
				if runErr == nil {
					runErr = fmt.Errorf("workflow worker canceled")
				}
				h.Manager.MarkCanceled(a.ID(), runErr)
			default:
				h.Manager.MarkDone(a.ID(), lastAssistantResponse(a))
			}
		case agentpkg.EventDone:
			if !completed {
				completed = true
				h.Manager.MarkDone(a.ID(), lastAssistantResponse(a))
			}
		case agentpkg.EventError:
			if !completed {
				completed = true
				runErr = ev.Error
				h.Manager.MarkError(a.ID(), ev.Error)
			}
		}
	}
	if !completed && runCtx.Err() != nil {
		runErr = runCtx.Err()
		h.Manager.MarkError(a.ID(), runErr)
	}

	result := AgentResult{
		Name:        task.Name,
		Phase:       task.Phase,
		InstanceKey: task.InstanceKey,
		Status:      StatusDone,
		Result:      lastAssistantResponse(a),
		StartedAt:   started,
		FinishedAt:  time.Now(),
	}
	if resultStatus == StatusIncomplete {
		result.Status = StatusIncomplete
		if runErr != nil {
			result.Error = runErr.Error()
		}
		return result, nil
	}
	if runErr != nil {
		result.Status = resultStatus
		if resultStatus == StatusDone {
			result.Status = StatusError
		}
		result.Error = runErr.Error()
		if resultStatus == StatusIncomplete || resultStatus == StatusCanceled {
			return result, nil
		}
		return result, runErr
	}
	return result, nil
}

func workflowAgentID(name string, instanceKey string) agentpkg.AgentID {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if instanceKey != "" {
		name = resultStorageKey(name, strings.TrimSpace(instanceKey))
	}
	return agentpkg.AgentID("agent-" + name)
}

func buildTaskPrompt(task AgentTask) string {
	if task.InstanceKey != "" {
		return fmt.Sprintf(`Workflow task: %s
Instance key: %s
Phase: %s

%s

Return a concise final result with evidence and risks.`, task.Name, task.InstanceKey, task.Phase, strings.TrimSpace(task.Prompt))
	}
	return fmt.Sprintf(`Workflow task: %s
Phase: %s

%s

Return a concise final result with evidence and risks.`, task.Name, task.Phase, strings.TrimSpace(task.Prompt))
}

func lastAssistantResponse(a agentpkg.Agent) string {
	messages := a.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == agentpkg.RoleAssistant {
			if messages[i].Content != "" {
				return messages[i].Content
			}
			var sb strings.Builder
			for _, block := range messages[i].Contents {
				if block.Type == "text" && block.Text != "" {
					sb.WriteString(block.Text)
				}
			}
			return sb.String()
		}
	}
	return ""
}

func sendParentEvent(ctx context.Context, ch chan<- internalagent.Event, ev internalagent.Event) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func contextWithEitherCancel(a context.Context, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	go func() {
		select {
		case <-b.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
