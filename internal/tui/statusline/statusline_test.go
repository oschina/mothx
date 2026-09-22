package statusline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
)

func shellCommand(cat bool) string {
	if platform.IsWindows() {
		if cat {
			return "[Console]::Out.Write([Console]::In.ReadToEnd())"
		}
		return "Write-Output 'hello-statusline'"
	}
	if cat {
		return "cat"
	}
	return "printf 'hello-statusline'"
}

func sleepCommand() string {
	if platform.IsWindows() {
		return "Start-Sleep -Seconds 2"
	}
	return "sleep 2"
}

func exitCommand() string {
	if platform.IsWindows() {
		return "Write-Error 'boom'; exit 7"
	}
	return "echo boom 1>&2; exit 7"
}

func TestRunPassesPayloadToCommand(t *testing.T) {
	settings := config.DefaultSettings()
	cfg := config.StatusLineSettings{
		Enabled: true,
		Type:    "command",
		Command: shellCommand(true),
	}

	result := Run(context.Background(), settings, cfg, []byte(`{"hook_event_name":"Status"}`), 72)
	if result.Err != nil {
		t.Fatalf("Run() error = %v, stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Output, `{"hook_event_name":"Status"}`; got != want {
		t.Fatalf("Run() output = %q, want %q", got, want)
	}
}

func TestRunTimeout(t *testing.T) {
	settings := config.DefaultSettings()
	cfg := config.StatusLineSettings{
		Enabled:   true,
		Type:      "command",
		Command:   sleepCommand(),
		TimeoutMs: 100,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result := Run(ctx, settings, cfg, []byte(`{}`), 72)
	if result.Err == nil {
		t.Fatalf("Run() error = nil, want timeout")
	}
}

func TestRunNonZeroExitCapturesStderr(t *testing.T) {
	settings := config.DefaultSettings()
	cfg := config.StatusLineSettings{
		Enabled: true,
		Type:    "command",
		Command: exitCommand(),
	}

	result := Run(context.Background(), settings, cfg, []byte(`{}`), 72)
	if result.Err == nil {
		t.Fatalf("Run() error = nil, want non-zero exit")
	}
	if !strings.Contains(result.Stderr, "boom") {
		t.Fatalf("Run() stderr = %q, want boom", result.Stderr)
	}
}
