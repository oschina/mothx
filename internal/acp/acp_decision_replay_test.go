package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestACPReplayPendingDecisionProjection(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(t.TempDir(), settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	s := &server{settings: settings, w: &output, pending: make(map[string]chan json.RawMessage), sessions: make(map[string]*sessionRuntime)}
	rt := &sessionRuntime{id: mgr.GetHeader().ID, mgr: mgr, decisions: &agentruntime.DecisionService{}, execution: &agentruntime.ExecutionRuntime{}}
	if _, err := rt.execution.Begin(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	s.sessions[rt.id] = rt
	request := agentruntime.DecisionRequest{ID: "question-1", SessionID: rt.id, RunID: "run-1", Kind: agentruntime.DecisionQuestion}
	if err := rt.decisions.Register(request); err != nil {
		t.Fatal(err)
	}
	event, err := agentruntime.BuildDecisionEvent(agentruntime.DecisionTransition{
		Request: request, Status: agentruntime.DecisionStatusPending,
		Payload:   questionRequest{SessionID: rt.id, Question: "continue?", Options: []string{"yes"}},
		ExpiresAt: time.Now().Add(time.Minute), Source: "acp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (agentruntime.SessionRunEventSink{SessionDir: settings.GetSessionDir()}).Record(event); err != nil {
		t.Fatal(err)
	}
	if err := s.replayPendingDecisionRequests(rt.id); err != nil {
		t.Fatal(err)
	}
	var notification map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &notification); err != nil {
		t.Fatal(err)
	}
	if notification["method"] != "_mothx/request_question" {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestACPReplayPendingStandardElicitationProjection(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(t.TempDir(), settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	s := &server{
		settings: settings, w: &output, pending: make(map[string]chan json.RawMessage), sessions: make(map[string]*sessionRuntime),
		clientCaps: clientCapabilities{Elicitation: &clientElicitationCapabilities{Form: &struct{}{}}},
	}
	rt := &sessionRuntime{id: mgr.GetHeader().ID, mgr: mgr, decisions: &agentruntime.DecisionService{}, execution: &agentruntime.ExecutionRuntime{}}
	if _, err := rt.execution.Begin(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	s.sessions[rt.id] = rt
	request := agentruntime.DecisionRequest{ID: "question-standard-1", SessionID: rt.id, RunID: "run-1", Kind: agentruntime.DecisionQuestion}
	if err := rt.decisions.Register(request); err != nil {
		t.Fatal(err)
	}
	event, err := agentruntime.BuildDecisionEvent(agentruntime.DecisionTransition{
		Request: request, Status: agentruntime.DecisionStatusPending,
		Payload: questionRequest{
			SessionID: rt.id, Question: "continue?", Options: []string{"yes"}, Protocol: acpElicitationFormProtocol,
		},
		ExpiresAt: time.Now().Add(time.Minute), Source: "acp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (agentruntime.SessionRunEventSink{SessionDir: settings.GetSessionDir()}).Record(event); err != nil {
		t.Fatal(err)
	}
	if err := s.replayPendingDecisionRequests(rt.id); err != nil {
		t.Fatal(err)
	}
	var notification map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &notification); err != nil {
		t.Fatal(err)
	}
	if notification["method"] != "elicitation/create" {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestACPReplayPendingDecisionProjectionSkipsResolved(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	mgr := session.New(t.TempDir(), settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	s := &server{settings: settings, w: &output, pending: make(map[string]chan json.RawMessage), sessions: make(map[string]*sessionRuntime)}
	rt := &sessionRuntime{id: mgr.GetHeader().ID, mgr: mgr, decisions: &agentruntime.DecisionService{}, execution: &agentruntime.ExecutionRuntime{}}
	if _, err := rt.execution.Begin(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	s.sessions[rt.id] = rt
	request := agentruntime.DecisionRequest{ID: "approval-1", SessionID: rt.id, RunID: "run-1", Kind: agentruntime.DecisionApproval}
	_ = rt.decisions.Register(request)
	for _, transition := range []agentruntime.DecisionTransition{
		{Request: request, Status: agentruntime.DecisionStatusPending, Payload: requestPermissionRequest{SessionID: rt.id}, Source: "acp"},
		{Request: request, Status: agentruntime.DecisionStatusResolved, Source: "acp"},
	} {
		event, err := agentruntime.BuildDecisionEvent(transition)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := (agentruntime.SessionRunEventSink{SessionDir: settings.GetSessionDir()}).Record(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.replayPendingDecisionRequests(rt.id); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("resolved decision replayed: %s", output.String())
	}
}
