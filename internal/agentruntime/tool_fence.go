package agentruntime

import (
	"context"
	"fmt"
	"strings"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
)

// beforeToolExecuteForRuntime is installed by SessionRuntime for every Agent
// it builds, including managed children. It is intentionally late in the Core
// tool path: approval/waits and the durable operation claim have already run.
func beforeToolExecuteForRuntime(runtime *SessionRuntime) func(agent.BeforeToolExecuteContext) *agent.ToolCallBlockResult {
	return func(toolCtx agent.BeforeToolExecuteContext) *agent.ToolCallBlockResult {
		if runtime == nil || !toolCtx.SideEffecting {
			return nil
		}
		runtime.mu.RLock()
		manager := runtime.Manager
		execution := runtime.Execution
		runtimeID := runtime.ID
		runtime.mu.RUnlock()
		if execution == nil || manager == nil || strings.TrimSpace(toolCtx.RunID) == "" {
			return nil
		}
		if toolCtx.ExecutionContext != nil {
			if err := toolCtx.ExecutionContext.Err(); err != nil {
				return blockToolExecutionFence(err.Error())
			}
		}
		activeRunID, active := execution.Active()
		if !active || activeRunID != toolCtx.RunID {
			return blockToolExecutionFence("the local execution is no longer active")
		}
		if runtimeID == "" {
			if header := manager.GetHeader(); header != nil {
				runtimeID = header.ID
			}
		}
		if runtimeID == "" {
			return blockToolExecutionFence("the session identity is unavailable")
		}
		checkCtx := toolCtx.ExecutionContext
		if checkCtx == nil {
			checkCtx = context.Background()
		}
		if err := session.ValidateRuntimeLeaseContext(checkCtx, manager.GetSessionDir(), runtimeID, toolCtx.RunID, session.RuntimeLeasePurposeExecution); err != nil {
			return blockToolExecutionFence(err.Error())
		}
		return nil
	}
}

func blockToolExecutionFence(reason string) *agent.ToolCallBlockResult {
	if strings.TrimSpace(reason) == "" {
		reason = "the execution lease could not be revalidated"
	}
	return &agent.ToolCallBlockResult{Block: true, Reason: fmt.Sprintf("tool execution blocked by Runtime ownership fence: %s", reason)}
}
