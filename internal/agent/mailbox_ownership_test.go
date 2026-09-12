package agent

import (
	"testing"
)

// bindTeamMailbox installs the session mailbox exactly as a bound expert team
// does: the lead drains member notifications and may hold its run open for
// still-running members.
func bindTeamMailbox(t *testing.T, mgr *AgentManager) *MemberMailbox {
	t.Helper()
	mailbox := NewMemberMailbox()
	mgr.SetMemberContext(NewMemberDefRegistry([]*MemberDef{{ID: "engineer"}}), mailbox, "team")
	mgr.SetMemberWaitEnabled(true)
	return mailbox
}

// TestSessionLeadGetsMailboxSteeringWithoutTeamWait guards N8 at the factory
// boundary: every session installs the mailbox, so a lead always drains member
// questions and completions, while only a bound expert team may hold its run
// open for members. A missing steering hook would strand a member's question
// until its own 30-minute timeout.
func TestSessionLeadGetsMailboxSteeringWithoutTeamWait(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	mailbox := NewMemberMailbox()
	mgr.SetMemberContext(nil, mailbox, "") // no expert team bound
	mgr.SetMemberWaitEnabled(false)

	lead, err := mgr.Create(AgentOptions{ID: "session-lead"})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	leadAdapter, ok := lead.(*AgentAdapter)
	if !ok || leadAdapter.inner == nil {
		t.Fatalf("lead adapter = %T", lead)
	}
	if leadAdapter.inner.config.GetSteeringMessages == nil {
		t.Fatal("lead did not receive the session mailbox steering drain")
	}
	if leadAdapter.inner.config.GetFollowUpMessages != nil {
		t.Fatal("a session without a bound team must not wait for members at the wrap-up turn")
	}
	mailbox.Enqueue(MemberCompletion{MemberID: "member-1", Status: MemberStatusDone, Payload: "done"})
	if messages := leadAdapter.inner.config.GetSteeringMessages(); len(messages) != 1 {
		t.Fatalf("mailbox steering = %#v, want the member notification", messages)
	}
}

// TestTeamBoundLeadKeepsTheMemberWait guards the other side of the same gate: a
// bound expert team still waits for members before its run may end.
func TestTeamBoundLeadKeepsTheMemberWait(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	bindTeamMailbox(t, mgr)

	lead, err := mgr.Create(AgentOptions{ID: "session-lead"})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	leadAdapter, ok := lead.(*AgentAdapter)
	if !ok || leadAdapter.inner == nil {
		t.Fatalf("lead adapter = %T", lead)
	}
	if leadAdapter.inner.config.GetSteeringMessages == nil {
		t.Fatal("team lead did not receive the member steering drain")
	}
	if leadAdapter.inner.config.GetFollowUpMessages == nil {
		t.Fatal("team lead did not receive the member follow-up wait")
	}
}

// TestAuxiliaryRolesDoNotOwnTheSessionMailbox guards the ownership of the
// session member mailbox: only the conversational lead may drain member
// notifications or block on the team's members. Auxiliary roles are created
// with IsSubAgent and no parent (ESM critic/audit/recovery), so they used to
// inherit both hooks and could consume or wait on the lead's members.
func TestAuxiliaryRolesDoNotOwnTheSessionMailbox(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	bindTeamMailbox(t, mgr)

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

// TestSessionBoundCronJobDoesNotOwnTheSessionMailbox guards the explicit
// opt-out: a session-bound cron job is parentless and (with a session) not
// IsSubAgent, so it matches the lead's shape while never being the session's
// conversational lead.
func TestSessionBoundCronJobDoesNotOwnTheSessionMailbox(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	bindTeamMailbox(t, mgr)

	job, err := mgr.Create(AgentOptions{ID: "cron-job", AuxiliaryRole: true})
	if err != nil {
		t.Fatalf("create cron job: %v", err)
	}
	jobAdapter, ok := job.(*AgentAdapter)
	if !ok || jobAdapter.inner == nil {
		t.Fatalf("cron adapter = %T", job)
	}
	if jobAdapter.inner.config.GetFollowUpMessages != nil || jobAdapter.inner.config.GetSteeringMessages != nil {
		t.Fatal("a scheduled job must not drain or wait on the session's members")
	}
}

// TestTeamBoundESMWorkerOwnsTheSessionMailbox guards the explicit opt-in that
// keeps a team-bound ESM worker continuation lead-equivalent: it is created with
// IsSubAgent (it persists outside the session tables) but it is the session's
// lead in ESM mode, so it must still receive member scheduling. The opt-in must
// not leak to any other auxiliary role.
func TestTeamBoundESMWorkerOwnsTheSessionMailbox(t *testing.T) {
	_, mgr := newTestFactoryAndManager(t)
	bindTeamMailbox(t, mgr)

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
