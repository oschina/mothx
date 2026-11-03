package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// Profiles written by earlier builds must survive validation across a range of
// shapes: the compatibility surface is the whole existing settings file, not
// just the concurrency field.
func TestLegacyProfileShapesStillValidate(t *testing.T) {
	cases := map[string]config.ACPAgentProfile{
		"minimal": {
			ID: "minimal", Command: []string{"/opt/a", "acp"}, Enabled: true,
		},
		"explicit concurrency within old bound": {
			ID: "c8", Command: []string{"/opt/b", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 8,
		},
		"write role with relative roots": {
			ID: "writer", Command: []string{"/opt/c", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
			Roles:            []config.ACPAgentRole{config.ACPRoleImplement}, WriteRoots: []string{"src"},
		},
		"interactive policy": {
			ID: "interactive", Command: []string{"/opt/d", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionInteractive,
		},
		"disabled with high concurrency": {
			ID: "disabled", Command: []string{"/opt/e", "acp"}, Enabled: false,
			MaxConcurrentChildren: 8,
		},
	}
	for name, profile := range cases {
		t.Run(name, func(t *testing.T) {
			if err := profile.Validate(); err != nil {
				t.Fatalf("a previously legal profile shape is now rejected: %v", err)
			}
		})
	}
}

// The enforced ceiling must be one no matter what was configured, because the
// safety argument for serial workers does not depend on user intent.
func TestEnforcedChildrenIsAlwaysOne(t *testing.T) {
	for _, configured := range []int{-1, 0, 1, 2, 8} {
		profile := config.ACPAgentProfile{
			ID: "p", Command: []string{"/opt/p", "acp"}, Enabled: true,
			MaxConcurrentChildren: configured,
		}
		if got := profile.EffectiveEnforcedChildren(); got != 1 {
			t.Fatalf("configured %d enforced %d workers, want 1", configured, got)
		}
	}
}

// A profile above the enforced ceiling must still be eligible as a Router worker:
// the ceiling limits parallelism, not eligibility.
func TestCappedProfileRemainsRouterEligible(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce, MaxConcurrentChildren: 4,
	}}
	profiles := ListACPWorkerProfiles(settings)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %#v", profiles)
	}
	got := profiles[0]
	if !got.RouterEligible {
		t.Fatal("a capped profile was reported ineligible; the ceiling regulates parallelism, not eligibility")
	}
	if got.MaxConcurrentChildren != 4 {
		t.Fatalf("configured ceiling reported as %d, want 4", got.MaxConcurrentChildren)
	}
	if got.MaxConcurrentChildrenEnforced != 1 {
		t.Fatalf("enforced ceiling reported as %d, want 1", got.MaxConcurrentChildrenEnforced)
	}
}
