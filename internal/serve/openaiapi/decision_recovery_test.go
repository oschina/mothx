package openaiapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

func TestResolveOrphanedDecisionsCancelsApprovalAndQuestion(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	sess := &APISession{ID: mgr.GetHeader().ID, WorkDir: srv.cfg.GetWorkDir(), Manager: mgr}
	run := session.SessionRun{ID: "run-orphan-decisions", SessionID: sess.ID, WorkDir: sess.WorkDir, Source: "webui", Model: "m1", Mode: "agent"}
	approval := SessionApprovalRequest{ApprovalID: "approval-1", SessionID: sess.ID, RunID: run.ID, Mode: "agent"}
	question := SessionQuestionRequest{QuestionID: "question-1", SessionID: sess.ID, RunID: run.ID, Question: "continue?"}
	if err := srv.recordSessionApprovalRequest(sess, approval); err != nil {
		t.Fatal(err)
	}
	if err := srv.recordSessionQuestionRequest(sess, question); err != nil {
		t.Fatal(err)
	}
	if err := srv.resolveOrphanedDecisions(run); err != nil {
		t.Fatal(err)
	}
	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, ev := range events {
		var envelope struct {
			Decision agentruntime.DecisionRecord `json:"decision"`
		}
		if json.Unmarshal(ev.Data, &envelope) == nil && envelope.Decision.ID != "" {
			statuses[envelope.Decision.ID] = envelope.Decision.Status
		}
	}
	if statuses["approval-1"] != "cancelled" || statuses["question-1"] != "cancelled" {
		t.Fatalf("statuses = %#v", statuses)
	}
}

func TestWebUIDecisionRequestPersistsDeadline(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()
	mgr := session.New(srv.cfg.GetWorkDir(), srv.settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	sess := &APISession{ID: mgr.GetHeader().ID, WorkDir: srv.cfg.GetWorkDir(), Manager: mgr}
	request := SessionQuestionRequest{QuestionID: "question-deadline", SessionID: sess.ID, RunID: "run-1", Question: "continue?", Timestamp: time.Now().Format(time.RFC3339Nano)}
	if err := srv.recordSessionQuestionRequest(sess, request); err != nil {
		t.Fatal(err)
	}
	events, err := session.ListSessionRunEvents(srv.settings.GetSessionDir(), sess.ID)
	if err != nil || len(events) == 0 {
		t.Fatalf("events = %#v, err=%v", events, err)
	}
	var envelope struct {
		Decision agentruntime.DecisionRecord `json:"decision"`
	}
	if err := json.Unmarshal(events[len(events)-1].Data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Decision.ExpiresAt.IsZero() {
		t.Fatal("decision deadline was not persisted")
	}
}
