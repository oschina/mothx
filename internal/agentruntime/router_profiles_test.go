package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestListACPWorkerProfilesMarksRouterEligibility(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{
		{ID: "worker-b", Title: "B", Command: []string{"/opt/b", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionAutoReject},
		{ID: "worker-a", Title: "A", Command: []string{"/opt/a", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce},
		{ID: "interactive", Command: []string{"/opt/i", "acp"}, Enabled: true, PermissionPolicy: config.ACPPermissionInteractive},
		{ID: "disabled", Command: []string{"/opt/d", "acp"}, Enabled: false},
		{ID: "broken", Command: []string{" "}, Enabled: true},
		{ID: "remote", Transport: "http", URL: "https://agent.example/acp?token=secret", Enabled: true, PermissionPolicy: config.ACPPermissionAutoAllowOnce},
	}
	profiles := ListACPWorkerProfiles(settings)
	if len(profiles) != 6 {
		t.Fatalf("profiles = %d", len(profiles))
	}
	byID := map[string]ACPWorkerProfileDescriptor{}
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	if !byID["worker-a"].RouterEligible || !byID["worker-b"].RouterEligible {
		t.Fatalf("auto profiles not eligible: %#v", byID)
	}
	if byID["worker-a"].MaxConcurrentChildren != 1 {
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
	if byID["remote"].Transport != config.ACPAgentTransportHTTP || byID["remote"].Endpoint != "https://agent.example/acp" {
		t.Fatalf("remote descriptor = %#v", byID["remote"])
	}
	if byID["worker-a"].Transport != config.ACPAgentTransportStdio || byID["worker-a"].Endpoint != "" {
		t.Fatalf("stdio descriptor = %#v", byID["worker-a"])
	}
}

func TestMothxSelfACPProfileIsRouterEligibleWithoutLegacyRoleFields(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "mothx", Command: []string{"/usr/local/bin/mothx", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	profiles := ListACPWorkerProfiles(settings)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %#v", profiles)
	}
	profile := profiles[0]
	if !profile.RouterEligible || profile.InvalidReason != "" {
		t.Fatalf("self-ACP profile = %#v, want eligible without roles/writeRoots", profile)
	}
}
