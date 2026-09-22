package agent

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/tools"
)

func newApprovalTestAgent(t *testing.T, mode string, approval config.ApprovalSettings) *Agent {
	t.Helper()
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", Name: "Model 1"}}, nil)
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	registry.RegisterDefaults()
	return New(Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     mode,
		Settings: &config.Settings{Approval: approval},
	}, registry)
}

func newApprovalTestAgentWithAllow(t *testing.T, mode string, approval config.ApprovalSettings, allow *config.AllowConfig) *Agent {
	t.Helper()
	mockProvider := provider.NewMockProvider("mock", []*provider.Model{{ID: "model1", Name: "Model 1"}}, nil)
	sb := sandbox.NewNoneSandbox()
	registry := tools.NewRegistry(t.TempDir(), sb)
	registry.RegisterDefaults()
	return New(Config{
		Provider: mockProvider,
		Model:    mockProvider.Models()[0],
		Mode:     mode,
		Settings: &config.Settings{Approval: approval},
		Allow:    allow,
	}, registry)
}

func TestNeedsApproval_AllowAutoEditSkipsApproval(t *testing.T) {
	confirm := true
	allow := &config.AllowConfig{AutoEdit: true}
	a := newApprovalTestAgentWithAllow(t, "agent", config.ApprovalSettings{ConfirmBeforeWrite: &confirm}, allow)
	if a.NeedsApproval("write", map[string]any{"path": "any/file.go"}) {
		t.Fatal("write should not require approval when AllowAutoEdit is on")
	}
	if a.NeedsApproval("edit", map[string]any{"path": "any/file.go"}) {
		t.Fatal("edit should not require approval when AllowAutoEdit is on")
	}
}

func TestNeedsApproval_AllowEditPathWhitelist(t *testing.T) {
	confirm := true
	allow := &config.AllowConfig{EditPaths: []string{"internal/**"}}
	a := newApprovalTestAgentWithAllow(t, "agent", config.ApprovalSettings{ConfirmBeforeWrite: &confirm}, allow)
	if a.NeedsApproval("edit", map[string]any{"path": "internal/agent/agent.go"}) {
		t.Fatal("whitelisted path should not require approval")
	}
	if !a.NeedsApproval("edit", map[string]any{"path": "cmd/main.go"}) {
		t.Fatal("non-whitelisted path should still require approval")
	}
}

func TestNeedsApproval_AllowEditPathPlanModeUnaffected(t *testing.T) {
	allow := &config.AllowConfig{AutoEdit: true, EditPaths: []string{"**"}}
	a := newApprovalTestAgentWithAllow(t, "plan", config.ApprovalSettings{}, allow)
	// In plan mode write/edit are not gated by NeedsApproval here (read-only enforced elsewhere),
	// but ensure the allow rules only short-circuit in agent mode by checking bash unaffected.
	if a.NeedsApproval("bash", map[string]any{"command": "ls"}) {
		t.Fatal("plan mode bash should not require approval")
	}
}

func TestNeedsApproval_NonBashNeverNeedsApproval(t *testing.T) {
	a := newApprovalTestAgent(t, "agent", config.ApprovalSettings{})
	if a.NeedsApproval("read", map[string]any{"path": "README.md"}) {
		t.Fatal("non-bash tool should not require approval")
	}
}

func TestNeedsApproval_AgentModeWriteConfirm(t *testing.T) {
	confirm := true
	a := newApprovalTestAgent(t, "agent", config.ApprovalSettings{ConfirmBeforeWrite: &confirm})
	if !a.NeedsApproval("write", map[string]any{"path": "README.md"}) {
		t.Fatal("write should require approval when confirmBeforeWrite is enabled")
	}
	if !a.NeedsApproval("edit", map[string]any{"path": "README.md"}) {
		t.Fatal("edit should require approval when confirmBeforeWrite is enabled")
	}
}

func TestNeedsApproval_YoloModeWriteDoesNotConfirm(t *testing.T) {
	confirm := true
	a := newApprovalTestAgent(t, "yolo", config.ApprovalSettings{ConfirmBeforeWrite: &confirm})
	if a.NeedsApproval("write", map[string]any{"path": "README.md"}) {
		t.Fatal("write should not require approval in yolo mode")
	}
}

func TestNeedsApproval_AgentModeWhitelistSkipsApproval(t *testing.T) {
	a := newApprovalTestAgent(t, "agent", config.ApprovalSettings{
		BashWhitelist: []string{"go ", "make "},
	})
	if a.NeedsApproval("bash", map[string]any{"command": "go test ./..."}) {
		t.Fatal("whitelisted bash command should not require approval in agent mode")
	}
}

func TestNeedsApproval_AgentModeProjectBashRulesSkipApproval(t *testing.T) {
	allow := &config.AllowConfig{
		BashCommands: []string{"make test"},
		BashPrefixes: []string{"go test "},
	}
	a := newApprovalTestAgentWithAllow(t, "agent", config.ApprovalSettings{}, allow)
	if a.NeedsApproval("bash", map[string]any{"command": "make test"}) {
		t.Fatal("project exact bash command should not require approval in agent mode")
	}
	if a.NeedsApproval("bash", map[string]any{"command": "go test ./internal/tui"}) {
		t.Fatal("project bash command prefix should not require approval in agent mode")
	}
	if !a.NeedsApproval("bash", map[string]any{"command": "go env"}) {
		t.Fatal("non-matching bash command should still require approval in agent mode")
	}
}

func TestNeedsApproval_BashRulesAcceptCmdAlias(t *testing.T) {
	allow := &config.AllowConfig{BashCommands: []string{"make test"}}
	a := newApprovalTestAgentWithAllow(t, "agent", config.ApprovalSettings{}, allow)
	if a.NeedsApproval("bash", map[string]any{"cmd": "make test"}) {
		t.Fatal("cmd alias should use project bash allow rule")
	}
}

func TestNeedsApproval_AgentModeBlacklistForcesApproval(t *testing.T) {
	a := newApprovalTestAgent(t, "agent", config.ApprovalSettings{
		BashWhitelist: []string{"go ", "rm "},
		BashBlacklist: []string{"rm -rf"},
	})
	if !a.NeedsApproval("bash", map[string]any{"command": "rm -rf /tmp/demo"}) {
		t.Fatal("blacklisted bash command should require approval in agent mode")
	}
}

func TestNeedsApproval_BlacklistOverridesProjectBashAllow(t *testing.T) {
	allow := &config.AllowConfig{BashCommands: []string{"rm -rf build"}}
	a := newApprovalTestAgentWithAllow(t, "agent", config.ApprovalSettings{
		BashBlacklist: []string{"rm -rf"},
	}, allow)
	if !a.NeedsApproval("bash", map[string]any{"command": "rm -rf build"}) {
		t.Fatal("blacklist should override project bash allow rule")
	}
}

func TestNeedsApproval_AgentModeNonWhitelistedNeedsApproval(t *testing.T) {
	a := newApprovalTestAgent(t, "agent", config.ApprovalSettings{
		BashWhitelist: []string{"go "},
	})
	if !a.NeedsApproval("bash", map[string]any{"command": "python script.py"}) {
		t.Fatal("non-whitelisted bash command should require approval in agent mode")
	}
}

func TestNeedsApproval_YoloModeAllowsUnlessBlacklisted(t *testing.T) {
	a := newApprovalTestAgent(t, "yolo", config.ApprovalSettings{
		BashBlacklist: []string{"rm -rf"},
	})
	if a.NeedsApproval("bash", map[string]any{"command": "go test ./..."}) {
		t.Fatal("non-blacklisted bash command should not require approval in yolo mode")
	}
	if !a.NeedsApproval("bash", map[string]any{"command": "rm -rf /"}) {
		t.Fatal("blacklisted bash command should still require approval in yolo mode")
	}
}

func TestNeedsApproval_OSModeAllowsUnlessBlacklisted(t *testing.T) {
	a := newApprovalTestAgent(t, "os", config.ApprovalSettings{
		BashBlacklist: []string{"rm -rf"},
	})
	if a.NeedsApproval("bash", map[string]any{"command": "go test ./..."}) {
		t.Fatal("non-blacklisted bash command should not require approval in OS mode")
	}
	if !a.NeedsApproval("bash", map[string]any{"command": "rm -rf /"}) {
		t.Fatal("blacklisted bash command should still require approval in OS mode")
	}
}

func TestNeedsApproval_BlacklistOverridesWhitelist(t *testing.T) {
	a := newApprovalTestAgent(t, "agent", config.ApprovalSettings{
		BashWhitelist: []string{"rm ", "rm -rf"},
		BashBlacklist: []string{"rm -rf"},
	})
	if !a.NeedsApproval("bash", map[string]any{"command": "rm -rf build"}) {
		t.Fatal("blacklist should override whitelist")
	}
}
