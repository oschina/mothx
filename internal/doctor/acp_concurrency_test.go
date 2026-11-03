package doctor

import (
	"strings"
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

// The effective ceiling is lower than the configured one, so the user must be
// told. Reporting it as a warning (not an error) keeps a working setup working.
func TestACPAgentsReportsConfiguredConcurrencyNotYetEffective(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/bin/sh", "-c", "true"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 4,
	}}
	checks := checkACPAgents(settings)
	var concurrency *Check
	for i := range checks {
		if checks[i].ID == "acp.profiles.concurrency" {
			concurrency = &checks[i]
		}
		if checks[i].Status == StatusError {
			t.Fatalf("a valid profile produced an error: %#v", checks[i])
		}
	}
	if concurrency == nil {
		t.Fatal("a configured ceiling above the enforced one was not reported")
	}
	if concurrency.Status != StatusWarn {
		t.Fatalf("concurrency report status = %q, want warn", concurrency.Status)
	}
	if !strings.Contains(concurrency.Detail, "codex=4") || !strings.Contains(concurrency.Detail, "one external worker") {
		t.Fatalf("concurrency detail = %q", concurrency.Detail)
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
