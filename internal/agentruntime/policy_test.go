package agentruntime

import (
	"testing"

	"github.com/oschina/mothx/internal/session"
)

func TestPolicyResolveMode(t *testing.T) {
	tests := []struct {
		name      string
		policy    Policy
		session   string
		requested string
		want      string
	}{
		{
			name:      "wechat cannot be downgraded",
			policy:    Policy{Source: SourceWeChat, DefaultMode: ModeAgent},
			session:   ModeAgent,
			requested: ModePlan,
			want:      ModeYolo,
		},
		{
			name:      "wechat ignores malformed adapter hint",
			policy:    Policy{Source: SourceWeChat, DefaultMode: ModeAgent},
			session:   "not-a-mode",
			requested: "invalid",
			want:      ModeYolo,
		},
		{
			name:   "feishu empty session uses yolo",
			policy: Policy{Source: SourceFeishu, DefaultMode: ModeAgent},
			want:   ModeYolo,
		},
		{
			name:      "regular request overrides session",
			policy:    Policy{Source: SourceWebUI, DefaultMode: ModeAgent},
			session:   ModePlan,
			requested: ModeYolo,
			want:      ModeYolo,
		},
		{
			name:   "regular empty session uses default",
			policy: Policy{Source: SourceWebUI, DefaultMode: ModeAgent},
			want:   ModeAgent,
		},
		{
			name:   "empty policy default falls back to yolo",
			policy: Policy{Source: SourceWebUI},
			want:   ModeYolo,
		},
		{
			name:      "regular request uses os",
			policy:    Policy{Source: SourceWebUI, DefaultMode: ModeAgent},
			requested: ModeOS,
			want:      ModeOS,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.policy.ResolveMode(tt.session, tt.requested)
			if err != nil {
				t.Fatalf("ResolveMode: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestModeResolver(t *testing.T) {
	got, err := (ModeResolver{Policy: ExecutionPolicy{Source: SourceFeishu, DefaultMode: ModeAgent}}).Resolve(ModePlan, ModeAgent)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != ModeYolo {
		t.Fatalf("Resolve() = %q, want yolo", got)
	}
}

func TestSourceFromSessionHeader(t *testing.T) {
	if got := SourceFromSessionHeader(&session.Header{ChannelType: "feishu"}); got != SourceFeishu {
		t.Fatalf("source = %q, want %q", got, SourceFeishu)
	}
	if got := SourceFromSessionHeader(&session.Header{ChannelType: "local"}); got != SourceUnknown {
		t.Fatalf("local source = %q, want unknown", got)
	}
}

func TestResolveUnattendedMode(t *testing.T) {
	tests := []struct {
		session string
		want    string
	}{
		{session: "", want: ModeYolo},
		{session: ModePlan, want: ModeYolo},
		{session: ModeAgent, want: ModeYolo},
		{session: ModeYolo, want: ModeYolo},
		{session: ModeOS, want: ModeOS},
		{session: " os ", want: ModeOS},
		{session: "not-a-mode", want: ModeYolo},
	}
	for _, tt := range tests {
		if got := ResolveUnattendedMode(tt.session); got != tt.want {
			t.Fatalf("ResolveUnattendedMode(%q) = %q, want %q", tt.session, got, tt.want)
		}
	}
}
