package agent

import (
	"testing"
)

// TestAuxiliaryRolesDoNotOwnTheSessionMailbox guards the ownership of the
// session member mailbox: only the conversational lead may drain member
// notifications or block on the team's members. Auxiliary roles are created
// with IsSubAgent and no parent (ESM critic/audit/recovery), so they used to
// inherit both hooks and could consume or wait on the lead's members.
func TestAuxiliaryRolesDoNotOwnTheSessionMailbox(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mailbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mailbox, "team")

	lead, err := mgr.Create(AgentOptions{ID: "session-lead"})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	leadAdapter, ok := lead.(*AgentAdapter)
	if !ok || leadAdapter.inner == nil {
		t.Fatalf("lead adapter = %T", lead)
	}
	if leadAdapter.inner.config.GetFollowUpMessages == nil {
		t.Fatal("lead did not receive the member follow-up hook")
	}
	if leadAdapter.inner.config.GetSteeringMessages == nil {
		t.Fatal("lead did not receive the member steering drain")
	}

	role, err := mgr.Create(AgentOptions{ID: "esm-critic", IsSubAgent: true})
	if err != nil {
		t.Fatalf("create auxiliary role: %v", err)
	}
	roleAdapter, ok := role.(*AgentAdapter)
	if !ok || roleAdapter.inner == nil {
		t.Fatalf("role adapter = %T", role)
	}
	if roleAdapter.inner.config.GetFollowUpMessages != nil {
		t.Fatal("auxiliary role inherited the member follow-up wait")
	}
	if roleAdapter.inner.config.GetSteeringMessages != nil {
		t.Fatal("auxiliary role drains member notifications owned by the lead")
	}
}

// TestTeamBoundESMWorkerOwnsTheSessionMailbox guards the explicit opt-in that
// keeps a team-bound ESM worker continuation lead-equivalent: it is created with
// IsSubAgent (it persists outside the session tables) but it is the session's
// lead in ESM mode, so it must still receive member scheduling. The opt-in must
// not leak to any other auxiliary role.
func TestTeamBoundESMWorkerOwnsTheSessionMailbox(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mailbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mailbox, "team")

	worker, err := mgr.Create(AgentOptions{ID: "esm-worker", IsSubAgent: true, OwnsSessionMailbox: true})
	if err != nil {
		t.Fatalf("create team ESM worker: %v", err)
	}
	workerAdapter, ok := worker.(*AgentAdapter)
	if !ok || workerAdapter.inner == nil {
		t.Fatalf("worker adapter = %T", worker)
	}
	if workerAdapter.inner.config.GetFollowUpMessages == nil {
		t.Fatal("team-bound ESM worker lost the member follow-up wait")
	}
	if workerAdapter.inner.config.GetSteeringMessages == nil {
		t.Fatal("team-bound ESM worker lost the member steering drain")
	}

	// The opt-in belongs to the team-bound worker only: the same flags without it
	// (critic/audit/recovery, or a worker outside a team session) stay isolated.
	isolated, err := mgr.Create(AgentOptions{ID: "esm-critic", IsSubAgent: true})
	if err != nil {
		t.Fatalf("create isolated role: %v", err)
	}
	isolatedAdapter, ok := isolated.(*AgentAdapter)
	if !ok || isolatedAdapter.inner == nil {
		t.Fatalf("isolated adapter = %T", isolated)
	}
	if isolatedAdapter.inner.config.GetFollowUpMessages != nil || isolatedAdapter.inner.config.GetSteeringMessages != nil {
		t.Fatal("auxiliary role inherited the mailbox hooks")
	}
}
