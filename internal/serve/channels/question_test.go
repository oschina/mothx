package channels

import (
	"testing"

	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
)

func TestChannelQuestionObserverAndDecisionLifecycle(t *testing.T) {
	d := &Dispatcher{}
	sess := &ChannelSession{
		ID:        "channels/wechat/user-1",
		runID:     "run-1",
		Decisions: &agentruntime.DecisionService{},
	}
	var observed agent.Event
	d.SetQuestionObserver(func(_ string, ev agent.Event) { observed = ev })

	ev := agent.Event{Type: agent.EventQuestionRequest, QuestionID: "question-1", QuestionText: "continue?"}
	d.notifyQuestionObserver(sess.ID, ev)
	if observed.QuestionID != ev.QuestionID || observed.QuestionText != ev.QuestionText {
		t.Fatalf("observed question = %#v, want %#v", observed, ev)
	}
	d.registerChannelDecision(sess, ev.QuestionID, agentruntime.DecisionQuestion)
	if len(sess.Decisions.Pending()) != 1 {
		t.Fatal("question decision was not registered")
	}
	if _, err := sess.Decisions.Resolve(agentruntime.DecisionResolution{ID: ev.QuestionID, Kind: agentruntime.DecisionQuestion, Status: "cancelled"}); err != nil {
		t.Fatalf("resolve question: %v", err)
	}
	if len(sess.Decisions.Pending()) != 0 {
		t.Fatal("question decision remains pending")
	}
}
