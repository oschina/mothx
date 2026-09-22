package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
)

func TestResolveSourcePrefersPersistedBindingAndReportsConflicts(t *testing.T) {
	resolved := ResolveSource(SourceResolutionInput{
		Binding:       &session.Binding{ChannelType: "wechat"},
		SessionHeader: &session.Header{ChannelType: "feishu"},
		Current:       SourceWebUI,
		Requested:     SourceACP,
	})
	if resolved.Source != SourceWeChat {
		t.Fatalf("source = %q, want %q", resolved.Source, SourceWeChat)
	}
	if !resolved.Conflicted || len(resolved.Diagnostics) != 3 {
		t.Fatalf("conflict = %v diagnostics = %#v, want three diagnostics", resolved.Conflicted, resolved.Diagnostics)
	}
}

func TestResolveSourceFallsBackForNewSession(t *testing.T) {
	resolved := ResolveSource(SourceResolutionInput{Requested: SourceACP})
	if resolved.Source != SourceACP || resolved.Conflicted {
		t.Fatalf("resolution = %#v, want ACP without conflict", resolved)
	}
}

func TestResolvePolicyUsesResolvedChannelSource(t *testing.T) {
	resolved, mode, err := ResolvePolicy(SourceResolutionInput{
		SessionHeader: &session.Header{ChannelType: "feishu"},
		Requested:     SourceWebUI,
	}, ModeAgent, ModePlan, ModeAgent)
	if err != nil {
		t.Fatalf("ResolvePolicy: %v", err)
	}
	if resolved.Source != SourceFeishu || mode != ModeYolo {
		t.Fatalf("resolution = %#v mode = %q, want feishu/yolo", resolved, mode)
	}
}

func TestSessionRuntimeRejectsMutationAfterClose(t *testing.T) {
	settings := config.DefaultSettings()
	settings.ContextFiles.Enabled = false
	runtime, err := (Builder{Settings: settings, SandboxLevel: sandbox.LevelNone}).Build(context.Background(), BuildOptions{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	runtime.Close()
	runtime.Close()
	if err := runtime.ApplyRegistryHooks(nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("ApplyRegistryHooks error = %v, want closed", err)
	}
	if err := runtime.RefreshResources(settings, RefreshOptions{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("RefreshResources error = %v, want closed", err)
	}
	if _, err := runtime.BuildAgent(AgentBuildOptions{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("BuildAgent error = %v, want closed", err)
	}
}
