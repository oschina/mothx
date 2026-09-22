package a2a

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agent"
)

// DefaultExecutor implements AgentExecutor by running tasks through the agent loop.
type DefaultExecutor struct {
	agentFactory AgentFactory
}

// AgentFactory creates agent instances for A2A task execution.
type AgentFactory interface {
	CreateForA2A(workDir string, mode string) (*agent.Agent, error)
}

// NewDefaultExecutor creates a new default executor.
func NewDefaultExecutor(factory AgentFactory) *DefaultExecutor {
	return &DefaultExecutor{agentFactory: factory}
}

// ExecuteTask runs an A2A task through the agent loop.
func (e *DefaultExecutor) ExecuteTask(ctx context.Context, task *Task, msg *Message) (<-chan TaskEvent, error) {
	// Extract text from message parts
	var userInput string
	for _, part := range msg.Parts {
		if part.Type == "text" && part.Text != "" {
			userInput = part.Text
			break
		}
	}
	if userInput == "" {
		return nil, fmt.Errorf("no text content in message")
	}

	// Create agent
	a, err := e.agentFactory.CreateForA2A("", "yolo")
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	// Run agent
	agentCh := a.Run(ctx, userInput)

	// Convert agent events to A2A task events
	taskCh := make(chan TaskEvent, 100)
	go func() {
		defer close(taskCh)

		var response strings.Builder
		terminalSent := false
		for ev := range agentCh {
			// Child-agent failures are isolated and must not fail the parent A2A task.
			if ev.AgentID != "" {
				continue
			}
			now := time.Now()
			switch ev.Type {
			case agent.EventTextDelta:
				response.WriteString(ev.TextDelta)
				taskCh <- TaskEvent{
					TaskID:    task.ID,
					State:     TaskStateWorking,
					Message:   &Message{Role: "agent", Parts: []MessagePart{{Type: "text", Text: ev.TextDelta}}},
					Timestamp: now,
				}

			case agent.EventRunFinished:
				terminalSent = true
				switch ev.Status {
				case agent.TaskFailed:
					errMsg := "unknown error"
					if ev.Error != nil {
						errMsg = ev.Error.Error()
					}
					taskCh <- TaskEvent{
						TaskID:    task.ID,
						State:     TaskStateFailed,
						Error:     &TaskError{Code: -32000, Message: errMsg},
						Timestamp: now,
					}
				case agent.TaskCanceled:
					taskCh <- TaskEvent{
						TaskID:    task.ID,
						State:     TaskStateCanceled,
						Timestamp: now,
					}
				case agent.TaskIncomplete:
					taskCh <- TaskEvent{
						TaskID: task.ID,
						State:  TaskStateIncomplete,
						Artifact: &Artifact{
							Name:  "response",
							Parts: []MessagePart{{Type: "text", Text: response.String()}},
						},
						Timestamp: now,
					}
				case agent.TaskSuccess:
					taskCh <- TaskEvent{
						TaskID: task.ID,
						State:  TaskStateCompleted,
						Artifact: &Artifact{
							Name:  "response",
							Parts: []MessagePart{{Type: "text", Text: response.String()}},
						},
						Timestamp: now,
					}
				}

			case agent.EventDone:
				if terminalSent {
					continue
				}
				terminalSent = true
				taskCh <- TaskEvent{
					TaskID: task.ID,
					State:  TaskStateCompleted,
					Artifact: &Artifact{
						Name:  "response",
						Parts: []MessagePart{{Type: "text", Text: response.String()}},
					},
					Timestamp: now,
				}

			case agent.EventError:
				if terminalSent {
					continue
				}
				terminalSent = true
				errMsg := "unknown error"
				if ev.Error != nil {
					errMsg = ev.Error.Error()
				}
				taskCh <- TaskEvent{
					TaskID:    task.ID,
					State:     TaskStateFailed,
					Error:     &TaskError{Code: -32000, Message: errMsg},
					Timestamp: now,
				}

			case agent.EventToolCall, agent.EventToolExecutionStart, agent.EventToolExecutionEnd:
				toolName := ev.ToolName
				if toolName == "" && ev.ToolCall != nil {
					toolName = ev.ToolCall.Name
				}
				if toolName != "" {
					taskCh <- TaskEvent{
						TaskID: task.ID,
						State:  TaskStateWorking,
						Message: &Message{
							Role: "agent",
							Parts: []MessagePart{{
								Type: "text",
								Text: fmt.Sprintf("[tool: %s]", toolName),
							}},
						},
						Timestamp: now,
					}
				}
			}
		}
		if !terminalSent {
			// Event stream closed without a terminal event — protocol failure,
			// never a successful completion.
			taskCh <- TaskEvent{
				TaskID:    task.ID,
				State:     TaskStateFailed,
				Error:     &TaskError{Code: -32000, Message: "event stream closed without terminal result"},
				Timestamp: time.Now(),
			}
		}
	}()

	return taskCh, nil
}
