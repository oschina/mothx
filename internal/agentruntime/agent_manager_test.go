package agentruntime

import (
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func TestNewAgentManagerRequiresSharedDependencies(t *testing.T) {
	tests := []struct {
		name string
		opts AgentManagerOptions
		want string
	}{
		{name: "runtime", want: "runtime is required"},
		{name: "settings", opts: AgentManagerOptions{Runtime: &SessionRuntime{}}, want: "settings are required"},
		{name: "provider", opts: AgentManagerOptions{Runtime: &SessionRuntime{}, Settings: config.DefaultSettings()}, want: "provider is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAgentManager(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewAgentManager error = %v, want %q", err, tt.want)
			}
		})
	}
}
