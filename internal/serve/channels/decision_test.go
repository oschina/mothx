package channels

import (
	"testing"

	"github.com/oschina/mothx/internal/agentruntime"
)

func TestChannelDecisionServiceLifecycle(t *testing.T) {
	sess := &ChannelSession{
		ID:        "channels/wechat/user-1",
		runID:     "run-1",
		Decisions: &agentruntime.DecisionService{},
	}
	d := &Dispatcher{}
	d.registerChannelDecision(sess, "tool-call-1", agentruntime.DecisionApproval)
	pending := sess.Decisions.Pending()
	if len(pending) != 1 || pending[0].Kind != agentruntime.DecisionApproval {
		t.Fatalf("pending decisions = %#v", pending)
	}
	if _, err := sess.Decisions.Resolve(agentruntime.DecisionResolution{ID: "tool-call-1", Kind: agentruntime.DecisionApproval, Status: "resolved"}); err != nil {
		t.Fatalf("resolve decision: %v", err)
	}

	d.registerChannelDecision(sess, "question-1", agentruntime.DecisionQuestion)
	d.clearChannelDecisions(sess)
	if pending := sess.Decisions.Pending(); len(pending) != 0 {
		t.Fatalf("pending decisions after clear = %#v", pending)
	}
}
