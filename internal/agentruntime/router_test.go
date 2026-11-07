package agentruntime

import (
	"os"
	"strings"
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

func TestSessionRuntimeRouterBindingPersistsMultipleProfiles(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("router-multi"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "router-multi", Source: SourceTUI}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{
		{
			ID: "codex", Command: []string{"/opt/codex", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		},
		{
			ID: "reviewer", Command: []string{"/opt/reviewer", "acp"}, Enabled: true,
			PermissionPolicy: config.ACPPermissionAutoReject,
		},
	}
	binding := RouterBinding{
		ACPProfileID: "codex",
		Profiles: []RouterProfileBinding{
			{ProfileID: "codex"},
			{ProfileID: "reviewer"},
		},
		PolicyRevision: "v2",
	}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, binding); err != nil {
		t.Fatalf("ActivateRouter: %v", err)
	}
	recovered, ok := runtime.RouterBinding()
	if !ok {
		t.Fatal("multi-profile binding was not recovered")
	}
	if got := recovered.ProfileIDs(); len(got) != 2 || got[0] != "codex" || got[1] != "reviewer" {
		t.Fatalf("bound profiles = %v", got)
	}
	if strategy, ok := recovered.strategyFor("reviewer"); !ok || strategy != config.ACPPermissionAutoReject {
		t.Fatalf("reviewer strategy = %q, ok=%v", strategy, ok)
	}
	profile, resolved, err := runtime.ResolveRouterACPProfileByID(settings, "reviewer")
	if err != nil {
		t.Fatalf("resolve reviewer: %v", err)
	}
	if profile.ID != "reviewer" || resolved.ACPProfileID != "codex" {
		t.Fatalf("profile=%#v binding=%#v", profile, resolved)
	}
	if _, _, err := runtime.ResolveRouterACPProfileByID(settings, "unbound"); err == nil {
		t.Fatal("an unbound profile was accepted")
	}
	reopened, err := session.OpenByIDExact(sessionDir, "router-multi")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reopened.GetLatestRouterBinding()
	if !ok || len(entry.Profiles) != 2 || entry.Profiles[1].ProfileID != "reviewer" ||
		entry.Profiles[1].ApprovalStrategy != string(config.ACPPermissionAutoReject) {
		t.Fatalf("replayed binding = %#v, ok=%v", entry, ok)
	}
}

func TestACPWorkerEnvironmentUsesOnlyProfileAllowlist(t *testing.T) {
	t.Setenv("ROUTER_ACP_TEST_ONE", "one")
	t.Setenv("ROUTER_ACP_TEST_TWO", "two")
	t.Setenv("ROUTER_ACP_TEST_SECRET", "must-not-leak")
	profile := config.ACPAgentProfile{
		ID: "worker", Command: []string{"/opt/worker"}, Enabled: true,
		Environment: []string{"ROUTER_ACP_TEST_TWO", "ROUTER_ACP_TEST_ONE"},
	}
	env, err := acpWorkerEnvironment(profile)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ROUTER_ACP_TEST_ONE=one") || !strings.Contains(joined, "ROUTER_ACP_TEST_TWO=two") {
		t.Fatalf("environment = %#v", env)
	}
	if strings.Contains(joined, "ROUTER_ACP_TEST_SECRET") {
		t.Fatalf("non-allowlisted environment leaked: %#v", env)
	}
	if len(env) < 2 || env[0] > env[len(env)-1] {
		t.Fatalf("environment is not sorted: %#v", env)
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

func TestActivateRouterPersistsModeAndBinding(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("activate-router"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "activate-router", Source: SourceTUI}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	// The activator names the worker only; Runtime records the profile policy.
	binding := RouterBinding{ACPProfileID: "codex", PolicyRevision: "v1"}
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, binding); err != nil {
		t.Fatalf("ActivateRouter: %v", err)
	}
	_, _, _, mode, _ := runtime.ConfigSnapshot()
	if mode != ModeRouter {
		t.Fatalf("runtime mode = %q, want router", mode)
	}
	recovered, ok := runtime.RouterBinding()
	if !ok || recovered.ACPProfileID != "codex" || recovered.ApprovalStrategy != config.ACPPermissionAutoAllowOnce {
		t.Fatalf("persisted binding = %#v, ok=%v", recovered, ok)
	}

	// Both facts must replay from a fresh Manager, not just from this handle.
	reopened, err := session.OpenByIDExact(sessionDir, "activate-router")
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reopened.GetLatestRouterBinding()
	if !ok || entry.ACPProfileID != "codex" {
		t.Fatalf("replayed binding = %#v, ok=%v", entry, ok)
	}
	if entry, ok := reopened.GetLatestModeChange(); !ok || entry.Mode != ModeRouter {
		t.Fatalf("replayed mode = %#v, ok=%v", entry, ok)
	}
}

func TestActivateRouterRequiresIdleAndEligibleProfile(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("activate-router-busy"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "activate-router-busy", Source: SourceTUI}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	binding := RouterBinding{ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoAllowOnce}

	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings,
		RouterBinding{ACPProfileID: "missing", ApprovalStrategy: config.ACPPermissionAutoAllowOnce}); err == nil {
		t.Fatal("activation with an unconfigured profile was accepted")
	}
	settings.ACPAgents[0].PermissionPolicy = config.ACPPermissionInteractive
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, binding); err == nil {
		t.Fatal("activation with an interactive profile was accepted")
	}

	settings.ACPAgents[0].PermissionPolicy = config.ACPPermissionAutoAllowOnce
	lease, err := session.AcquireExecutionAdmission(sessionDir, "activate-router-busy")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings, binding); err == nil {
		t.Fatal("activation on a non-idle session was accepted")
	}
	if _, ok := runtime.RouterBinding(); ok {
		t.Fatal("rejected activation persisted a Router binding")
	}
}

func TestActivateRouterRejectsForcedSourceAndStaleStrategy(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("activate-router-policy"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "activate-router-policy", Source: SourceTUI}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}

	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings,
		RouterBinding{ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoReject}); err == nil {
		t.Fatal("activation with a strategy that contradicts the profile was accepted")
	}

	// WeChat/Feishu force yolo; Router must never be selectable there.
	runtime.Source = SourceWeChat
	if err := runtime.ActivateRouter(t.Context(), sessionDir, settings,
		RouterBinding{ACPProfileID: "codex"}); err == nil {
		t.Fatal("Router activation from a source-forced yolo session was accepted")
	}
	if _, ok := runtime.RouterBinding(); ok {
		t.Fatal("rejected activation persisted a Router binding")
	}
}

func TestActivateRouterRejectsTeamExpert(t *testing.T) {
	sessionDir := t.TempDir()
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID("activate-router-team"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{
		Manager: manager,
		ID:      "activate-router-team",
		Source:  SourceTUI,
		Expert:  &ExpertBinding{Team: true},
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	err := runtime.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "codex"})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with an expert team") {
		t.Fatalf("team Router activation error = %v", err)
	}
	if _, ok := runtime.RouterBinding(); ok {
		t.Fatal("rejected team activation persisted a Router binding")
	}
}

// TestRouterModeRequiresActivation pins the mode/routing invariant: Router is
// not an independent execution mode, so no generic mode switch may produce
// mode=router without a persisted, eligible worker binding.
func TestRouterModeRequiresActivation(t *testing.T) {
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.InitWithID("router-mode-guard"); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, ID: "router-mode-guard", Source: SourceTUI}
	if err := runtime.applyModeOption("", ModeRouter); err == nil {
		t.Fatal("generic mode switch selected Router without a binding")
	}
	_, _, _, mode, _ := runtime.ConfigSnapshot()
	if mode == ModeRouter {
		t.Fatal("failed mode switch left the runtime in Router mode")
	}
	if entry, ok := manager.GetLatestModeChange(); ok && entry.Mode == ModeRouter {
		t.Fatal("failed mode switch persisted a Router mode entry")
	}
}

func TestBindSessionReplaysPersistedRouterMode(t *testing.T) {
	workDir, sessionDir := t.TempDir(), t.TempDir()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID("bind-router-mode"); err != nil {
		t.Fatal(err)
	}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}}
	activator := &SessionRuntime{Manager: manager, ID: "bind-router-mode", Source: SourceTUI}
	if err := activator.ActivateRouter(t.Context(), sessionDir, settings, RouterBinding{ACPProfileID: "codex"}); err != nil {
		t.Fatal(err)
	}

	// A fresh adapter must reopen the session in Router, not in the product
	// default mode, because the persisted entry is the authority.
	bound, err := (Builder{Settings: settings}).Build(t.Context(), BuildOptions{
		ID: "bind-router-mode", Source: SourceTUI, WorkDir: workDir, Manager: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	if _, _, _, mode, _ := bound.ConfigSnapshot(); mode != ModeRouter {
		t.Fatalf("bound runtime mode = %q, want router", mode)
	}
}

func TestResolveRouterACPProfileRejectsInteractivePermissionPolicy(t *testing.T) {
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	runtime := &SessionRuntime{Manager: manager, Mode: ModeRouter}
	settings := config.DefaultSettings()
	settings.ACPAgents = []config.ACPAgentProfile{{
		ID: "codex", Command: []string{"/opt/codex/bin/codex", "acp"}, Enabled: true,
		PermissionPolicy: config.ACPPermissionInteractive,
	}}
	if err := runtime.SetRouterBinding(RouterBinding{ACPProfileID: "codex", ApprovalStrategy: config.ACPPermissionAutoAllowOnce}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.ResolveRouterACPProfile(settings); err == nil {
		t.Fatal("interactive external permission policy was accepted for Router")
	}
}

// A persisted Router mode without a binding is a partially written session.
// Replay must fail closed to the read-only plan surface instead of exposing
// Router with no worker to delegate to.
func TestReplayPersistedRouterModeFailsClosedWithoutBinding(t *testing.T) {
	manager := session.New(t.TempDir(), t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendModeChange(ModeRouter); err != nil {
		t.Fatal(err)
	}
	mode, err := replayPersistedMode(manager, SourceResolution{Source: SourceTUI}, SourceTUI)
	if err != nil {
		t.Fatalf("replay persisted mode: %v", err)
	}
	if mode != ModePlan {
		t.Fatalf("mode without a binding = %q, want %q", mode, ModePlan)
	}

	if _, err := manager.AppendRouterBindingSelection(session.RouterBindingSelection{
		PrimaryProfileID: "codex",
		Profiles: []session.RouterProfileBinding{
			{ProfileID: "codex", ApprovalStrategy: string(config.ACPPermissionAutoAllowOnce)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	mode, err = replayPersistedMode(manager, SourceResolution{Source: SourceTUI}, SourceTUI)
	if err != nil {
		t.Fatalf("replay persisted mode: %v", err)
	}
	if mode != ModeRouter {
		t.Fatalf("mode with a binding = %q, want %q", mode, ModeRouter)
	}
}
