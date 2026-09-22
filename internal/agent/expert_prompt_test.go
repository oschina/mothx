package agent

import (
	"strings"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/config"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestExpertPromptSectionsOrderAndDefault(t *testing.T) {
	base := BuildSystemPromptWithOptions("yolo", nil, "/tmp", "RULE-BODY", "EXTRA-BODY", nil, nil, false, false, false, SystemPromptOptions{})
	if strings.Contains(base, "Expert Identity") || strings.Contains(base, "Team Roster") {
		t.Fatal("default prompt (no expert) must not contain expert sections")
	}

	full := BuildSystemPromptWithOptions("yolo", nil, "/tmp", "RULE-BODY", "EXTRA-BODY", nil, nil, false, false, false, SystemPromptOptions{
		ExpertIdentity: "## Expert Identity\nIDENTITY-BODY\n",
		ExpertRoster:   "## Team Roster & Dispatch\nROSTER-BODY\n",
	})
	ruleIdx := strings.Index(full, "RULE-BODY")
	identityIdx := strings.Index(full, "IDENTITY-BODY")
	rosterIdx := strings.Index(full, "ROSTER-BODY")
	contextIdx := strings.Index(full, "EXTRA-BODY")
	if ruleIdx < 0 || identityIdx < 0 || rosterIdx < 0 || contextIdx < 0 {
		t.Fatalf("expert prompt missing sections: %q", full)
	}
	if !(ruleIdx < identityIdx && identityIdx < rosterIdx && rosterIdx < contextIdx) {
		t.Fatalf("section order must be rules -> identity -> roster -> context (got %d/%d/%d/%d)", ruleIdx, identityIdx, rosterIdx, contextIdx)
	}

	// Roster without identity (defensive: only one section present).
	rosterOnly := BuildSystemPromptWithOptions("yolo", nil, "/tmp", "", "EXTRA-BODY", nil, nil, false, false, false, SystemPromptOptions{
		ExpertRoster: "ROSTER-ONLY\n",
	})
	if strings.Contains(rosterOnly, "Expert Identity") || !strings.Contains(rosterOnly, "ROSTER-ONLY") {
		t.Fatal("roster-only injection must not fabricate an identity section")
	}
}

func TestFactoryInjectsExpertOnlyIntoMainAgent(t *testing.T) {
	provider := provider.NewMockProvider("mock", []*provider.Model{{ID: "m1", Name: "M1"}}, nil)
	factory := NewAgentFactoryWithOptions(
		provider, provider.Models()[0], config.DefaultSettings(), nil, "", "", nil,
		ctxpkg.CompactionSettings{}, nil,
		AgentFactoryOptions{ExpertIdentity: "LEAD-IDENTITY", ExpertRoster: "TEAM-ROSTER"},
	)
	mgr := session.New(t.TempDir(), t.TempDir())
	if err := mgr.InitWithID("expert-factory"); err != nil {
		t.Fatalf("init session: %v", err)
	}

	main := factory.Create(AgentOptions{Session: mgr, ID: "main", Mode: "yolo"}).(*AgentAdapter)
	if main.inner.config.ExpertIdentity != "LEAD-IDENTITY" || main.inner.config.ExpertRoster != "TEAM-ROSTER" {
		t.Fatalf("main agent expert fields = %q/%q", main.inner.config.ExpertIdentity, main.inner.config.ExpertRoster)
	}

	child := factory.Create(AgentOptions{Session: mgr, ID: "child", ParentID: agentpkg.AgentID("main"), Mode: "yolo"}).(*AgentAdapter)
	if child.inner.config.ExpertIdentity != "" || child.inner.config.ExpertRoster != "" {
		t.Fatalf("sub-agent must never inherit the lead expert overlay, got %q/%q", child.inner.config.ExpertIdentity, child.inner.config.ExpertRoster)
	}
}
