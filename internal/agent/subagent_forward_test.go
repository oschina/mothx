package agent

import (
	"context"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
)

func TestForwardChildAgentEventPreservesPlanAndUsage(t *testing.T) {
	ch := make(chan Event, 2)
	ok := ForwardChildAgentEvent(context.Background(), ch, "child", agentpkg.Event{
		Type:         agentpkg.EventPlanUpdate,
		Plan:         &agentpkg.TaskPlan{Title: "work", Steps: []agentpkg.PlanStep{{Title: "one", Status: "in_progress"}}},
		Usage:        &agentpkg.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		ContextUsage: &agentpkg.ContextUsage{TotalTokens: 100, ContextWindow: 1000},
	})
	if !ok {
		t.Fatal("plan event was not forwarded")
	}
	event := <-ch
	if event.Plan == nil || event.Plan.Steps[0].Title != "one" || event.Plan.Title != "work" {
		t.Fatalf("forwarded plan = %#v", event.Plan)
	}
	if event.Usage == nil || event.Usage.Input != 10 || event.Usage.Output != 5 {
		t.Fatalf("forwarded usage = %#v", event.Usage)
	}
	if event.ContextUsage == nil || event.ContextUsage.ContextWindow != 1000 {
		t.Fatalf("forwarded context usage = %#v", event.ContextUsage)
	}
}
