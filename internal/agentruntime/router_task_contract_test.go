package agentruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// contractProfile is a fully enabled Router worker that may implement inside
// src/ and run tests.
func contractProfile() config.ACPAgentProfile {
	return config.ACPAgentProfile{
		ID:         "codex",
		Title:      "Codex",
		Command:    []string{"/opt/codex", "acp"},
		Enabled:    true,
		Roles:      []config.ACPAgentRole{config.ACPRoleInvestigate, config.ACPRoleImplement, config.ACPRoleTest, config.ACPRoleReview},
		WriteRoots: []string{"src"},
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

func TestSubagentTaskContractRejectsRoleOutsideProfileCeiling(t *testing.T) {
	profile := contractProfile()
	profile.Roles = []config.ACPAgentRole{config.ACPRoleInvestigate}
	profile.WriteRoots = nil

	contract := validContract()
	contract.Role = SubagentRoleInvestigate
	contract.WriteScope = nil
	contract.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableFindings}
	contract.Verification = ""
	if err := contract.Validate(profile); err != nil {
		t.Fatalf("investigate contract rejected: %v", err)
	}

	contract.Role = SubagentRoleImplement
	contract.WriteScope = []string{"src/retry_test.go"}
	contract.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableChangedFiles}
	if err := contract.Validate(profile); err == nil {
		t.Fatal("implement contract was accepted by an investigate-only profile")
	}
}

func TestSubagentTaskContractRejectsWriteOutsideProfileRoots(t *testing.T) {
	contract := validContract()
	contract.WriteScope = []string{"internal/retry_test.go"}
	err := contract.Validate(contractProfile())
	if err == nil {
		t.Fatal("write outside the profile write roots was accepted")
	}
	if !strings.Contains(err.Error(), "write scope") {
		t.Fatalf("error = %v, want a write-scope rejection", err)
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
		"write on read-only role": func(c *SubagentTaskContract, p *config.ACPAgentProfile) {
			c.Role = SubagentRoleReview
			c.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableReview}
			c.Verification = ""
		},
		"write role without write scope": func(c *SubagentTaskContract, _ *config.ACPAgentProfile) { c.WriteScope = nil },
		"read role with write scope": func(c *SubagentTaskContract, p *config.ACPAgentProfile) {
			c.Role = SubagentRoleInvestigate
			c.ExpectedDeliverables = []SubagentDeliverable{SubagentDeliverableFindings}
			c.Verification = ""
		},
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

// TestSubagentTaskContractRejectsWriteRootEscapeThroughProfile pins that a
// misconfigured profile is rejected at configuration time, not silently
// normalized into a broader write ceiling.
func TestSubagentTaskContractWriteRootContainment(t *testing.T) {
	profile := contractProfile()
	profile.WriteRoots = []string{"src"}

	inside := validContract()
	inside.WriteScope = []string{"src/nested/file.go"}
	if err := inside.Validate(profile); err != nil {
		t.Fatalf("nested write inside the declared root rejected: %v", err)
	}

	sibling := validContract()
	sibling.WriteScope = []string{"src-other/file.go"}
	if err := sibling.Validate(profile); err == nil {
		t.Fatal("sibling directory with the same prefix was treated as a write root")
	}
}
