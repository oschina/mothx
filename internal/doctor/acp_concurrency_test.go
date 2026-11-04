package doctor

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// A profile configured on an earlier build with maxConcurrentChildren=2 must keep
// validating. Rejecting it would strand the user's Router setup behind a settings
// file the new build refuses to load.
func TestACPAgentsAcceptsPreviouslyValidConcurrency(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/bin/sh", "-c", "true"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 2,
	}}
	checks := checkACPAgents(settings)
	for _, check := range checks {
		if check.Status == StatusError {
			t.Fatalf("a previously valid profile is now rejected: %#v", check)
		}
	}
}

// A configured read-only concurrency ceiling is effective, so doctor must not
// report it as unsupported.
func TestACPAgentsHonorsConfiguredConcurrency(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/bin/sh", "-c", "true"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 4,
	}}
	checks := checkACPAgents(settings)
	for i := range checks {
		if checks[i].ID == "acp.profiles.concurrency" {
			t.Fatalf("configured concurrency produced an obsolete warning: %#v", checks[i])
		}
		if checks[i].Status == StatusError {
			t.Fatalf("a valid profile produced an error: %#v", checks[i])
		}
	}
}

// Parallel base-workspace writes are explicit operator trust, so doctor must
// report them. MothX cannot confine an external Agent's own tools to the
// declared write scope, and a silent opt-in would hide that risk.
func TestACPAgentsReportsParallelBaseWorkspaceWrites(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/bin/sh", "-c", "true"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 4,
		AllowParallelBaseWorkspaceWrites: true,
	}}
	checks := checkACPAgents(settings)
	found := false
	for i := range checks {
		if checks[i].ID == "acp.profiles.parallel_base_writes" {
			found = true
			if checks[i].Status != StatusWarn {
				t.Fatalf("parallel base writes check = %#v, want a warning", checks[i])
			}
		}
	}
	if !found {
		t.Fatalf("parallel base-workspace writes were not reported: %#v", checks)
	}
}

// The ordinary configuration stays silent: the parallel opt-in is absent.
func TestACPAgentsSilentForParallelBaseWorkspaceWritesByDefault(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/bin/sh", "-c", "true"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 4,
	}}
	for _, check := range checkACPAgents(settings) {
		if check.ID == "acp.profiles.parallel_base_writes" {
			t.Fatalf("the default configuration produced a parallel-writes notice: %#v", check)
		}
	}
}

// A default profile has nothing to report: the concurrency notice must not appear
// for the ordinary configuration.
func TestACPAgentsSilentForDefaultConcurrency(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/bin/sh", "-c", "true"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	for _, check := range checkACPAgents(settings) {
		if check.ID == "acp.profiles.concurrency" {
			t.Fatalf("default concurrency produced a notice: %#v", check)
		}
	}
}
