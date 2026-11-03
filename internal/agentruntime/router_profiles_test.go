package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestListACPWorkerProfilesMarksRouterEligibility(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{
		{ID: "worker-b", Title: "B", Command: []string{"/opt/b", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionAutoReject},
		{ID: "worker-a", Title: "A", Command: []string{"/opt/a", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce,
			Roles: []config.ACPAgentRole{config.ACPRoleInvestigate, config.ACPRoleImplement}, WriteRoots: []string{"src"}},
		{ID: "interactive", Command: []string{"/opt/i", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionInteractive},
		{ID: "disabled", Command: []string{"/opt/d", "acp"}, Enabled: false},
		{ID: "broken", Command: []string{" "}, Enabled: true},
	}
	profiles := ListACPWorkerProfiles(settings)
	if len(profiles) != 5 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	byID := map[string]ACPWorkerProfileDescriptor{}
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	if !byID["worker-a"].RouterEligible || !byID["worker-b"].RouterEligible {
		t.Fatalf("auto profiles not eligible: %#v", byID)
	}
	if byID["worker-a"].MaxConcurrentChildren != 1 || len(byID["worker-a"].Roles) != 2 {
		t.Fatalf("worker-a descriptor = %#v", byID["worker-a"])
	}
	if byID["interactive"].RouterEligible {
		t.Fatal("interactive profile was marked Router eligible")
	}
	if byID["disabled"].RouterEligible || byID["broken"].RouterEligible {
		t.Fatal("disabled/invalid profile was marked Router eligible")
	}
	if byID["broken"].InvalidReason == "" {
		t.Fatal("invalid profile did not report a reason")
	}
}
