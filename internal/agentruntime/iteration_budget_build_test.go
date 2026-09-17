package agentruntime

import (
	"testing"

	"github.com/startvibecoding/mothx/internal/agent"
)

// TestExtendBudgetToolIsLeadOnly pins the registration boundary: the model-facing
// renewal tool is added to the shared registry only for the session's
// conversational lead. Transient builds (side questions, knowledge indexing) must
// not register it, and the canonical sub-agent toolset never includes it.
func TestExtendBudgetToolIsLeadOnly(t *testing.T) {
	for _, name := range agent.SubAgentToolNames() {
		if name == agent.IterationBudgetToolName {
			t.Fatalf("the sub-agent toolset must not contain %s", agent.IterationBudgetToolName)
		}
	}

	lead, leadMock := mailboxBuildFixture(t)
	if _, ok := lead.Registry.Get(agent.IterationBudgetToolName); ok {
		t.Fatalf("%s must not exist before a lead build", agent.IterationBudgetToolName)
	}
	if _, err := lead.BuildAgent(mailboxBuildOptions("lead", leadMock)); err != nil {
		t.Fatalf("build lead: %v", err)
	}
	if _, ok := lead.Registry.Get(agent.IterationBudgetToolName); !ok {
		t.Fatalf("a lead build must register %s", agent.IterationBudgetToolName)
	}

	transient, transientMock := mailboxBuildFixture(t)
	if _, err := transient.BuildTransientAgent(transient.Registry, mailboxBuildOptions("side-question", transientMock)); err != nil {
		t.Fatalf("build transient: %v", err)
	}
	if _, ok := transient.Registry.Get(agent.IterationBudgetToolName); ok {
		t.Fatalf("a transient build must not register %s", agent.IterationBudgetToolName)
	}
}
