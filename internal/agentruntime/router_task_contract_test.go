package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// contractProfile is a fully enabled Router worker. Role and write scope are
// deliberately absent: they are per-task metadata, not profile configuration.
func contractProfile() config.ACPAgentProfile {
	return config.ACPAgentProfile{
		ID:      "codex",
		Title:   "Codex",
		Command: []string{"/opt/codex", "acp"},
		Enabled: true,
	}
}

func validContract() SubagentTaskContract {
	return SubagentTaskContract{
		Objective:            "Add a focused regression test for retry classification.",
		Role:                 SubagentRoleImplement,
		ReadScope:            []string{"src/retry.go", "src/retry_test.go"},
		WriteScope:           []string{"src/retry_test.go"},
		ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableChangedFiles, SubagentDeliverableTestEvidence},
		Verification:         "go test ./...",
		StopConditions:       []string{"the focused test fails or the declared scope is insufficient"},
		TargetProfile:        "codex",
	}
}

func TestSubagentTaskContractAcceptsBoundedImplementTask(t *testing.T) {
	if err := validContract().Validate(contractProfile()); err != nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
}

func TestSubagentTaskContractJSONNeverContainsAttachmentBytes(t *testing.T) {
	contract := validContract()
	contract.Attachments = []SubagentAttachment{{
		ID: "resource-1", Filename: "secret.txt", MediaType: "text/plain",
		Kind: AttachmentFile, RelativePath: ".mothx/tmp/inputs/resource-1/secret.txt",
		Bytes: 12, Data: []byte("do-not-leak"),
	}}
	encoded, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "do-not-leak") {
		t.Fatalf("serialized contract leaked attachment bytes: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"relativePath":".mothx/tmp/inputs/resource-1/secret.txt"`) {
		t.Fatalf("serialized contract lost attachment metadata: %s", encoded)
	}
}

func TestSubagentTaskContractAcceptsDynamicRoles(t *testing.T) {
	contract := validContract()
	contract.Role = SubagentRoleInvestigate
	contract.WriteScope = nil
	contract.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableFindings}
	contract.Verification = ""
	if err := contract.Validate(contractProfile()); err != nil {
		t.Fatalf("investigate contract rejected: %v", err)
	}

	contract.Role = SubagentRoleImplement
	contract.WriteScope = []string{"src/retry_test.go"}
	contract.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableChangedFiles, SubagentDeliverableTestEvidence}
	contract.Verification = "go test ./internal/agentruntime"
	if err := contract.Validate(contractProfile()); err != nil {
		t.Fatalf("implement contract was rejected without a profile role ceiling: %v", err)
	}
}

func TestSubagentTaskContractAllowsWriteScopeWithoutProfileRoots(t *testing.T) {
	contract := validContract()
	contract.WriteScope = []string{"internal/retry_test.go"}
	if err := contract.Validate(contractProfile()); err != nil {
		t.Fatalf("dynamic write scope was constrained by ignored profile fields: %v", err)
	}
}

func TestSubagentTaskContractDefaultsOmittedRoleAndScope(t *testing.T) {
	contract := validContract()
	contract.Role = ""
	contract.WriteScope = nil
	contract.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableFindings}
	contract.Verification = ""
	if err := contract.Validate(contractProfile()); err != nil {
		t.Fatalf("optional role and write scope were treated as required: %v", err)
	}
	if contract.EffectiveRole() != SubagentRoleInvestigate {
		t.Fatalf("effective role = %q, want %q", contract.EffectiveRole(), SubagentRoleInvestigate)
	}
	if !strings.Contains(contract.RenderPrompt("/tmp/work"), "## Role\ninvestigate") {
		t.Fatalf("prompt did not render the default role:\n%s", contract.RenderPrompt("/tmp/work"))
	}
}

func TestSubagentTaskContractAllowsWriteRoleWithoutScope(t *testing.T) {
	contract := validContract()
	contract.WriteScope = nil
	if err := contract.Validate(contractProfile()); err != nil {
		t.Fatalf("write role without optional scope was rejected: %v", err)
	}
	if !strings.Contains(contract.RenderPrompt("/tmp/work"), "not a filesystem restriction") {
		t.Fatalf("unscoped write-role prompt did not disclose the metadata semantics:\n%s", contract.RenderPrompt("/tmp/work"))
	}
}

func TestSubagentTaskContractAllowsScopeHintOnReadOnlyRole(t *testing.T) {
	contract := validContract()
	contract.Role = SubagentRoleInvestigate
	contract.WriteScope = []string{"src"}
	contract.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableFindings}
	contract.Verification = ""
	if err := contract.Validate(contractProfile()); err != nil {
		t.Fatalf("write_scope should be optional task metadata for every role: %v", err)
	}
}

func TestSubagentTaskContractRejectsUnboundedOrMismatchedFields(t *testing.T) {
	cases := map[string]func(*SubagentTaskContract, *config.ACPAgentProfile){
		"empty objective": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.Objective = "  " },
		"oversized objective": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.Objective = strings.Repeat("x", maxSubagentObjectiveBytes+1)
		},
		"unknown role":      func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.Role = "deploy" },
		"mismatched target": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.TargetProfile = "claude" },
		"missing target":    func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.TargetProfile = "" },
		"deliverable not allowed for role": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableReview}
		},
		"unknown deliverable": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.ExpectedDeliverables = []SubagentDeliverable{"patch"}
		},
		"duplicate deliverable": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableChangedFiles, SubagentDeliverableChangedFiles}
		},
		"too many scope entries": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.ReadScope = make([]string, maxSubagentScopeEntries+1)
			for i := range c.ReadScope {
				c.ReadScope[i] = "src/a.go"
			}
		},
		"empty scope entry":   func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.ReadScope = []string{" "} },
		"absolute read scope": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.ReadScope = []string{"/etc/passwd"} },
		"escaping read scope": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.ReadScope = []string{"../outside"} },
		"escaping write scope": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.WriteScope = []string{"src/../../etc/passwd"}
		},
		"oversized verification": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.Verification = strings.Repeat("x", maxSubagentVerificationBytes+1)
		},
		"implement without verification": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.Verification = "" },
		"too many stop conditions": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.StopConditions = make([]string, maxSubagentStopConditions+1)
			for i := range c.StopConditions {
				c.StopConditions[i] = "done"
			}
		},
		"oversized total contract": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) {
			c.Objective = strings.Repeat("o", 4*1024)
			c.WriteScope = []string{"src/a.go"}
			blob := strings.Repeat("r", 400)
			c.ReadScope = make([]string, maxSubagentScopeEntries)
			for i := range c.ReadScope {
				c.ReadScope[i] = "src/" + blob
			}
			c.StopConditions = nil
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			profile := contractProfile()
			contract := validContract()
			mutate(&contract, &profile)
			if err := contract.Validate(profile); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// A task write scope is normalized independently of any profile field or
// prefix-matching heuristic.
func TestSubagentTaskContractWriteScopeNormalization(t *testing.T) {
	inside := validContract()
	inside.WriteScope = []string{"src/nested/file.go"}
	if err := inside.Validate(contractProfile()); err != nil {
		t.Fatalf("nested write scope rejected: %v", err)
	}

	sibling := inside
	sibling.WriteScope = []string{"src-other/file.go"}
	if err := sibling.Validate(contractProfile()); err != nil {
		t.Fatalf("sibling write scope rejected: %v", err)
	}
}
