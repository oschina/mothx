package agentruntime

import (
	"os"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestSessionRuntimeRouterBindingPersistsAndRejectsInteractive(t *testing.T) {
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager}
	if err := runtime.SetRouterBinding(RouterBinding{
		ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoAllowOnce, PolicyRevision: "v1",
	}); err != nil {
		t.Fatalf("SetRouterBinding: %v", err)
	}
	binding, ok := runtime.RouterBinding()
	if !ok {
		t.Fatal("RouterBinding was not recovered")
	}
	if binding.ACPProfileID != "codex" || binding.ApprovalStrategy != config.ACPPermissionAutoAllowOnce || binding.PolicyRevision != "v1" {
		t.Fatalf("binding = %#v", binding)
	}
	if err := runtime.SetRouterBinding(RouterBinding{ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionInteractive}); err == nil {
		t.Fatal("interactive Router approval strategy was accepted")
	}
}

func TestACPWorkerEnvironmentUsesOnlyProfileAllowlist(t *testing.T) {
	t.Setenv("ROUTER_ACP_TEST_ONE", "one")
	t.Setenv("ROUTER_ACP_TEST_TWO", "two")
	profile := config.ACPAgentProfile{
		ID: "worker", Command: []string{"/opt/worker"}, Enabled: true,
		Environment: []string{"ROUTER_ACP_TEST_TWO", "ROUTER_ACP_TEST_ONE"},
	}
	env, err := acpWorkerEnvironment(profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 2 || env[0] != "ROUTER_ACP_TEST_ONE=one" || env[1] != "ROUTER_ACP_TEST_TWO=two" {
		t.Fatalf("environment = %#v", env)
	}
	if err := os.Unsetenv("ROUTER_ACP_TEST_ONE"); err != nil {
		t.Fatal(err)
	}
	if _, err := acpWorkerEnvironment(config.ACPAgentProfile{ID: "worker", Command: []string{"/opt/worker"}, Enabled: true, Environment: []string{"ROUTER_ACP_TEST_ONE"}}); err == nil {
		t.Fatal("missing profile environment was accepted")
	}
}

func TestResolveRouterACPProfileRequiresRouterBindingAndEnabledProfile(t *testing.T) {
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, Mode: ModeRouter}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	if _, _, err := runtime.ResolveRouterACPProfile(settings); err == nil {
		t.Fatal("missing Router binding was accepted")
	}
	if err := runtime.SetRouterBinding(RouterBinding{ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoAllowOnce}); err != nil {
		t.Fatal(err)
	}
	profile, binding, err := runtime.ResolveRouterACPProfile(settings)
	if err != nil {
		t.Fatalf("ResolveRouterACPProfile: %v", err)
	}
	if profile.ID != "codex" || binding.ACPProfileID != "codex" {
		t.Fatalf("profile/binding = %#v / %#v", profile, binding)
	}
	settings.ACPAgents[0].Enabled = false
	if _, _, err := runtime.ResolveRouterACPProfile(settings); err == nil {
		t.Fatal("disabled Router profile was accepted")
	}
}
