package doctor

import (
	"os"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestCheckACPAgentsValidatesShapeAndExecutableWithoutLaunching(t *testing.T) {
	settings := config.DefaultSettings()
	if checks := checkACPAgents(settings); len(checks) != 1 || checks[0].Status != StatusSkip {
		t.Fatalf("no profiles = %#v", checks)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "worker", Command: []string{exe, "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	checks := checkACPAgents(settings)
	if len(checks) != 1 || checks[0].Status != StatusOK {
		t.Fatalf("valid profile = %#v", checks)
	}

	settings.ACPAgents[0].Command[0] = "/definitely/missing/mothx-worker"
	checks = checkACPAgents(settings)
	if len(checks) != 1 || checks[0].Status != StatusError || checks[0].Fix == "" {
		t.Fatalf("missing executable = %#v", checks)
	}

	settings.ACPAgents[0].Command[0] = "relative-worker"
	checks = checkACPAgents(settings)
	if len(checks) != 1 || checks[0].Status != StatusError {
		t.Fatalf("invalid profile shape = %#v", checks)
	}
}

func TestCheckACPAgentsReportsDisabledProfiles(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "worker", Command: []string{"/opt/worker"}, Enabled: false,
	}}
	checks := checkACPAgents(settings)
	if len(checks) != 1 || checks[0].Status != StatusSkip {
		t.Fatalf("disabled profile = %#v", checks)
	}
}
