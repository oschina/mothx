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
