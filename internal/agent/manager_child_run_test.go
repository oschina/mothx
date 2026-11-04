package agent

import (
	"context"
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
)

type childRunStubAgent struct {
	id       agentpkg.AgentID
	workDir  string
	messages []agentpkg.Message
}

func (a *childRunStubAgent) ID() agentpkg.AgentID       { return a.id }
func (a *childRunStubAgent) ParentID() agentpkg.AgentID { return "" }
func (a *childRunStubAgent) WorkDir() string            { return a.workDir }
func (a *childRunStubAgent) Run(context.Context, string) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch
}
func (a *childRunStubAgent) RunWithMessages(context.Context, []agentpkg.Message) <-chan agentpkg.Event {
	ch := make(chan agentpkg.Event)
	close(ch)
	return ch
}
func (a *childRunStubAgent) Abort()                                  {}
func (a *childRunStubAgent) GetMessages() []agentpkg.Message         { return a.messages }
func (a *childRunStubAgent) SetMessages(messages []agentpkg.Message) { a.messages = messages }
func (a *childRunStubAgent) GetContext() *agentpkg.AgentContext      { return nil }
func (a *childRunStubAgent) SetContext(*agentpkg.AgentContext)       {}
func (a *childRunStubAgent) GetContextUsage() *agentpkg.ContextUsage { return nil }
func (a *childRunStubAgent) LoadHistoryMessages([]agentpkg.Message)  {}
func (a *childRunStubAgent) HandleApprovalResponse(string, bool)     {}

type recordingChildRunRecorder struct {
	observations []ChildRunObservation
}

func (r *recordingChildRunRecorder) RecordChildRun(observation ChildRunObservation) error {
	r.observations = append(r.observations, observation)
	return nil
}

func TestAgentManagerRecordsLocalChildRunThroughRuntimeRecorder(t *testing.T) {
	manager := NewAgentManager(nil)
	recorder := &recordingChildRunRecorder{}
	manager.SetChildRunRecorder(recorder, "session-1")
	child := &childRunStubAgent{
		id: "child-1", workDir: "/workspace/project",
		messages: []agentpkg.Message{agentpkg.NewAssistantMessage([]agentpkg.ContentBlock{{Type: "text", Text: "finished"}})},
	}
	manager.Register(child)
	manager.MarkRunning(child.ID())
	manager.MarkDone(child.ID(), "finished")

	if err := manager.RecordLocalChildRun(child, "run-1", string(agentpkg.TaskSuccess)); err != nil {
		t.Fatalf("RecordLocalChildRun: %v", err)
	}
	if len(recorder.observations) != 1 {
		t.Fatalf("recorded observations = %d, want 1", len(recorder.observations))
	}
	got := recorder.observations[0]
	if got.ChildID != "child-1" || got.SessionID != "session-1" ||
		got.ParentRunID != "run-1" || got.Backend != "local" ||
		got.WorkDir != "/workspace/project" || got.Status != "success" ||
		got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("child run observation = %#v", got)
	}
	if !strings.Contains(got.TranscriptJSON, "finished") {
		t.Fatalf("transcript JSON = %s", got.TranscriptJSON)
	}
}

func TestAgentManagerSkipsLocalChildRunWithoutRecorder(t *testing.T) {
	manager := NewAgentManager(nil)
	child := &childRunStubAgent{id: "child-1"}
	if err := manager.RecordLocalChildRun(child, "run-1", "success"); err != nil {
		t.Fatalf("RecordLocalChildRun without recorder: %v", err)
	}
}
