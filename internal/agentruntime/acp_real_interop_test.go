package agentruntime

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
)

// TestRealOpenCodeACPInterop pins the first real ACP interoperability target:
// OpenCode's native `opencode acp` stdio server. It is opt-in so the normal
// suite stays hermetic; generic fake-Agent tests must not encode one vendor's
// extensions as ACP v1.
//
// Run with:
//
//	MOTHX_ACP_INTEROP=1 go test ./internal/agentruntime -run TestRealOpenCodeACPInterop -v
//
// Set MOTHX_ACP_INTEROP_OPENCODE_BIN to a specific executable, and
// MOTHX_ACP_INTEROP_ENV to a comma-separated allowlist of credential variable
// names (for example OPENAI_API_KEY) when the installed agent needs them.
func TestRealOpenCodeACPInterop(t *testing.T) {
	if os.Getenv("MOTHX_ACP_INTEROP") != "1" {
		t.Skip("set MOTHX_ACP_INTEROP=1 to run the real OpenCode ACP fixture")
	}
	binary := strings.TrimSpace(os.Getenv("MOTHX_ACP_INTEROP_OPENCODE_BIN"))
	if binary == "" {
		binary = "opencode"
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("OpenCode ACP executable %q is not available: %v", binary, err)
	}
	envNames := make([]string, 0, 4)
	for _, name := range strings.Split(os.Getenv("MOTHX_ACP_INTEROP_ENV"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			envNames = append(envNames, name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := (ACPWorkerBridge{}).Run(ctx, ACPWorkerInvocation{
		Profile: config.ACPAgentProfile{
			ID:               "opencode",
			Title:            "OpenCode",
			Command:          []string{binary, "acp"},
			Enabled:          true,
			Environment:      envNames,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
			Roles:            []config.ACPAgentRole{config.ACPRoleInvestigate},
		},
		Contract: SubagentTaskContract{
			Objective:            "Reply with one short sentence confirming the ACP interop check. Do not modify any files.",
			Role:                 SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "opencode",
			StopConditions:       []string{"a short reply is returned"},
		},
		WorkDir:  t.TempDir(),
		Launcher: acpProcessLauncher{},
	})
	if err != nil {
		t.Fatalf("OpenCode ACP interop: %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("OpenCode ACP returned no session id")
	}
	if strings.TrimSpace(result.Output) == "" {
		t.Fatal("OpenCode ACP returned no assistant text")
	}
	if result.StopReason == "" {
		t.Fatal("OpenCode ACP returned no prompt stop reason")
	}
}
