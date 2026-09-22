package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestResolveSourceBindingWinsOverRequestAndRuntime(t *testing.T) {
	resolved := ResolveSource(SourceResolutionInput{
		Binding: &session.Binding{ChannelType: "wechat"},
		Current: SourceWebUI, Requested: SourceACP,
	})
	if resolved.Source != SourceWeChat {
		t.Fatalf("source = %q, want %q", resolved.Source, SourceWeChat)
	}
}

func TestResolvePolicyBoundChannelCannotDowngradeMode(t *testing.T) {
	resolved, mode, err := ResolvePolicy(SourceResolutionInput{
		Binding:   &session.Binding{ChannelType: "feishu"},
		Requested: SourceWebUI,
	}, ModeAgent, ModePlan, ModeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Source != SourceFeishu || mode != ModeYolo {
		t.Fatalf("resolution = %#v mode = %q, want feishu/yolo", resolved, mode)
	}
}

func TestResolvePolicyRejectsPersistedRuntimeSourceConflict(t *testing.T) {
	_, _, err := ResolvePolicy(SourceResolutionInput{
		Binding: &session.Binding{ChannelType: "feishu"},
		Current: SourceWebUI, Requested: SourceACP,
	}, ModeAgent, ModePlan, ModeAgent)
	if err == nil {
		t.Fatal("ResolvePolicy accepted conflicting persisted/runtime source")
	}
	if _, ok := err.(*SourceConflictError); !ok {
		t.Fatalf("ResolvePolicy error = %T %v, want SourceConflictError", err, err)
	}
}

func TestResolvePolicyUnboundWebUIUsesRequestedMode(t *testing.T) {
	_, mode, err := ResolvePolicy(SourceResolutionInput{
		Current: SourceWebUI, Requested: SourceWebUI,
	}, ModeAgent, ModeYolo, ModeAgent)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeYolo {
		t.Fatalf("mode = %q, want yolo", mode)
	}
}

func TestResolvePolicyRejectsUnknownSourceCandidates(t *testing.T) {
	for _, input := range []SourceResolutionInput{
		{Requested: RuntimeSource("adapter-without-policy")},
		{Current: RuntimeSource("stale-runtime")},
	} {
		if _, _, err := ResolvePolicy(input, "", "", ModeAgent); err == nil {
			t.Fatalf("ResolvePolicy accepted unknown source candidate %#v", input)
		}
	}
}
