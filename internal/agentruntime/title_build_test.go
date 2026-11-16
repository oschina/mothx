package agentruntime

import (
	"context"
	"testing"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tools"
)

type nativeTitleMockProvider struct {
	*provider.MockProvider
}

func (p nativeTitleMockProvider) SupportsNativeTitle() bool { return true }

func nativeTitleRuntime(t *testing.T) (*SessionRuntime, *session.Manager) {
	t.Helper()
	workDir := t.TempDir()
	manager := session.New(workDir, t.TempDir())
	if err := manager.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	registry := tools.NewRegistry(workDir, sandbox.NewNoneSandbox())
	runtime, err := AttachSessionResources(AttachedResources{
		Source: SourceCLI, WorkDir: workDir, Manager: manager, Registry: registry,
	})
	if err != nil {
		t.Fatalf("attach session resources: %v", err)
	}
	t.Cleanup(runtime.Close)
	return runtime, manager
}

func runTitledAgent(t *testing.T, runtime *SessionRuntime, mock *provider.MockProvider, prompt string, onTitle func(string)) agent.TaskStatus {
	t.Helper()
	build := AgentBuildOptions{
		ID: agentpkg.AgentID("title-test"), Provider: nativeTitleMockProvider{mock},
		ProviderName: "mock", Model: mock.Models()[0], Mode: ModeYolo,
		OnTitle: onTitle,
	}
	built, err := runtime.BuildAgent(build)
	if err != nil {
		t.Fatalf("build agent: %v", err)
	}
	status := agent.TaskStatus("")
	for event := range built.Run(context.Background(), prompt) {
		if event.Type == agent.EventRunFinished {
			status = event.Status
		}
	}
	return status
}

func TestBuildAgentPersistsDeepSeekNativeTitle(t *testing.T) {
	runtime, manager := nativeTitleRuntime(t)
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "deepseek-flash"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "answer"},
		{Type: provider.StreamDone, StopReason: "stop", Title: "北京明天天气"},
	})
	var projected []string
	if status := runTitledAgent(t, runtime, mock, "北京明天会下雨吗", func(title string) { projected = append(projected, title) }); status != agent.TaskSuccess {
		t.Fatalf("run status = %q, want %q", status, agent.TaskSuccess)
	}
	info, ok := manager.GetLatestSessionInfo()
	if !ok || info.Name != "北京明天天气" || info.Source != "auto" {
		t.Fatalf("persisted title = %#v ok=%v, want auto 北京明天天气", info, ok)
	}
	if len(projected) != 1 || projected[0] != "北京明天天气" {
		t.Fatalf("projected titles = %#v, want one 北京明天天气", projected)
	}
}

func TestBuildAgentFallsBackWhenNativeTitleMissing(t *testing.T) {
	runtime, manager := nativeTitleRuntime(t)
	mock := provider.NewMockProvider("mock", []*provider.Model{{ID: "deepseek-flash"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "answer"},
		{Type: provider.StreamDone, StopReason: "stop"},
	})
	if status := runTitledAgent(t, runtime, mock, "北京明天会下雨吗", nil); status != agent.TaskSuccess {
		t.Fatalf("run status = %q, want %q", status, agent.TaskSuccess)
	}
	info, ok := manager.GetLatestSessionInfo()
	if !ok || info.Name != "北京明天会下雨吗" || info.Source != "auto" {
		t.Fatalf("fallback title = %#v ok=%v, want auto 北京明天会下雨吗", info, ok)
	}

	// A second build sees the persisted title and must not request or overwrite it.
	second := provider.NewMockProvider("mock", []*provider.Model{{ID: "deepseek-flash"}}, []provider.StreamEvent{
		{Type: provider.StreamStart},
		{Type: provider.StreamTextDelta, TextDelta: "answer"},
		{Type: provider.StreamDone, StopReason: "stop", Title: "不应覆盖"},
	})
	if status := runTitledAgent(t, runtime, second, "另一个问题", nil); status != agent.TaskSuccess {
		t.Fatalf("second run status = %q, want %q", status, agent.TaskSuccess)
	}
	info, ok = manager.GetLatestSessionInfo()
	if !ok || info.Name != "北京明天会下雨吗" {
		t.Fatalf("title after second run = %#v ok=%v, want unchanged", info, ok)
	}
}
