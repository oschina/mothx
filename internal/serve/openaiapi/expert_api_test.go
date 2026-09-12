package openaiapi

import (
	"context"
	"errors"
	"testing"

	coreagent "github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

func TestServerExpertCatalogAndSessionBindingUseRuntime(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	catalog, err := srv.ListExperts("")
	if err != nil {
		t.Fatalf("list experts: %v", err)
	}
	seen := make(map[string]ExpertSummary, len(catalog))
	for _, item := range catalog {
		seen[item.ID] = item
	}
	if team, ok := seen["software-company"]; !ok || team.ExpertType != "team" || team.DisplayName.Zh == "" {
		t.Fatalf("software-company summary = %#v", team)
	}
	if _, ok := seen["frontend-developer"]; !ok {
		t.Fatalf("catalog = %#v, missing frontend-developer", catalog)
	}

	sess, err := srv.getOrCreateSession("expert-api-binding", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	state, err := srv.GetSessionExpert(sess.ID)
	if err != nil {
		t.Fatalf("get unbound expert state: %v", err)
	}
	if state.Expert != nil {
		t.Fatalf("unbound state = %#v", state)
	}

	state, err = srv.SetSessionExpert(context.Background(), sess.ID, "software-company")
	if err != nil {
		t.Fatalf("bind team expert: %v", err)
	}
	if state.Expert == nil || state.Expert.ID != "software-company" || state.Expert.ExpertType != "team" || len(state.Expert.Members) != 5 {
		t.Fatalf("bound state = %#v", state)
	}
	if got := sess.Manager.GetExpertID(); got != "software-company" {
		t.Fatalf("persisted expert = %q", got)
	}
	if !sess.Runtime.TeamExpertActive() || sess.AgentMgr == nil || sess.AgentMgr.Members == nil {
		t.Fatalf("team runtime projection = runtime=%#v manager=%#v", sess.Runtime, sess.AgentMgr)
	}
	if _, ok := sess.Registry.Get("subagent_spawn"); !ok {
		t.Fatal("team expert did not install subagent_spawn")
	}
	if _, ok := sess.Registry.Get("subagent_wait"); !ok {
		t.Fatal("team expert did not install subagent_wait")
	}
	lead, err := sess.AgentMgr.Create(coreagent.AgentOptions{ID: "expert-api-lead"})
	if err != nil {
		t.Fatalf("create team lead: %v", err)
	}
	member, err := sess.AgentMgr.Create(coreagent.AgentOptions{
		ID: "expert-api-engineer", ParentID: lead.ID(), MemberID: "software-engineer", ExpertID: "software-company",
		MemberDisplayName: "软件工程师", MemberEmoji: "🛠️", MemberRole: "member",
	})
	if err != nil {
		t.Fatalf("create team member: %v", err)
	}
	sess.AgentMgr.MarkRunning(member.ID())
	subAgents, err := srv.GetSessionSubAgents(sess.ID)
	if err != nil {
		t.Fatalf("get team subagents: %v", err)
	}
	if len(subAgents) != 1 || subAgents[0].MemberID != "software-engineer" || subAgents[0].ExpertID != "software-company" || subAgents[0].MemberDisplayName != "软件工程师" || subAgents[0].MemberEmoji != "🛠️" || subAgents[0].MemberRole != "member" || subAgents[0].Status != "running" {
		t.Fatalf("member card projection = %#v", subAgents)
	}

	if _, err := srv.SetSessionExpert(context.Background(), sess.ID, "frontend-developer"); !errors.Is(err, agentruntime.ErrExpertSwitchRequiresFork) {
		t.Fatalf("in-place expert replacement error = %v, want fork requirement", err)
	}
	state, err = srv.SetSessionExpert(context.Background(), sess.ID, "")
	if err != nil {
		t.Fatalf("unbind expert: %v", err)
	}
	if state.Expert != nil || sess.Runtime.TeamExpertActive() || sess.AgentMgr != nil {
		t.Fatalf("unbound runtime state = state=%#v manager=%#v", state, sess.AgentMgr)
	}
	// Every canonical sub-agent tool must be gone, not just the spawn tool.
	for _, name := range coreagent.SubAgentToolNames() {
		if _, ok := sess.Registry.Get(name); ok {
			t.Fatalf("%s remained after unbinding the only team capability", name)
		}
	}
}

func TestServerForkSessionWithExpertPreservesSource(t *testing.T) {
	srv := newTestServer(t)
	defer srv.pool.Stop()

	source, err := srv.getOrCreateSession("expert-api-fork", srv.cfg.GetWorkDir())
	if err != nil {
		t.Fatalf("create source session: %v", err)
	}
	if err := source.Manager.StartConversationTurn("expert-api-turn", "intent", "run"); err != nil {
		t.Fatalf("start source turn: %v", err)
	}
	if _, err := source.Manager.AppendMessage(provider.NewUserMessage("fork this expert session")); err != nil {
		t.Fatalf("append source message: %v", err)
	}
	if err := source.Manager.EndConversationTurn("expert-api-turn", "completed", "stop"); err != nil {
		t.Fatalf("complete source turn: %v", err)
	}
	if _, err := srv.SetSessionExpert(context.Background(), source.ID, "software-company"); err != nil {
		t.Fatalf("bind source expert: %v", err)
	}

	result, err := srv.ForkSessionWithExpert(context.Background(), source.ID, agentruntime.ForkOptions{
		RequestID: "expert-api-fork-request",
	}, "frontend-developer")
	if err != nil {
		t.Fatalf("fork with expert: %v", err)
	}
	child, err := session.OpenByIDExact(srv.settings.GetSessionDir(), result.SessionID)
	if err != nil {
		t.Fatalf("open child: %v", err)
	}
	if got := child.GetExpertID(); got != "frontend-developer" {
		t.Fatalf("child expert = %q", got)
	}
	if got := source.Manager.GetExpertID(); got != "software-company" {
		t.Fatalf("source expert changed to %q", got)
	}
}
