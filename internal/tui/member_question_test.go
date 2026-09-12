package tui

import (
	"path/filepath"
	"testing"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

// TestMemberQuestionRequestIsNotAHumanDecision guards the "members ask the
// lead" routing: a question raised by a sub-agent carries the member's AgentID,
// must not become a human decision (that would misroute the answer to the main
// agent and leave the member blocked), and must not park the UI in a question
// wait. The Runtime queues it in the session mailbox for the lead instead.
func TestMemberQuestionRequestIsNotAHumanDecision(t *testing.T) {
	workDir := t.TempDir()
	sessionDir := filepath.Join(workDir, "sessions")
	sess := session.New(workDir, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	sessionID := sess.GetHeader().ID

	run := newTUIRun(sessionID, sessionDir)
	run.execution.SetRunStore(agentruntime.RunStore{SessionDir: sessionDir})
	if _, err := run.execution.Begin(nil, run.id); err != nil {
		t.Fatalf("begin run: %v", err)
	}

	app := NewApp(nil, &provider.Model{ID: "test"}, config.DefaultSettings(), sess, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	app.run = run
	app.eventCh = make(chan agent.Event)

	app.handleAgentEvent(agent.Event{
		Type:              agent.EventQuestionRequest,
		AgentID:           "agent-member-1",
		MemberDisplayName: "Alice",
		QuestionID:        "question-agent-member-1-1",
		QuestionText:      "Which database?",
		QuestionOptions:   []string{"postgres", "sqlite"},
	})

	if got := len(run.decisions.Pending()); got != 0 {
		t.Fatalf("pending decisions = %d, want 0: a member question must not register as a human decision", got)
	}
	if app.waitingForQuestion {
		t.Fatal("member question put the UI into a human question wait")
	}
	if len(app.questionQueue) != 0 {
		t.Fatalf("question queue = %d entries, want 0", len(app.questionQueue))
	}
	if len(app.messages) == 0 {
		t.Fatal("member question was not surfaced in the transcript")
	}
}
