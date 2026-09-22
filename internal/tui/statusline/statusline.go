package statusline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
)

const (
	DefaultTimeoutMs = 800
	MaxOutputBytes   = 64 * 1024
)

type Payload struct {
	HookEventName string         `json:"hook_event_name,omitempty"`
	SessionID     string         `json:"session_id,omitempty"`
	Cwd           string         `json:"cwd,omitempty"`
	Model         *ModelInfo     `json:"model,omitempty"`
	Workspace     *Workspace     `json:"workspace,omitempty"`
	Version       string         `json:"version,omitempty"`
	OutputStyle   *OutputStyle   `json:"output_style,omitempty"`
	Effort        *Effort        `json:"effort,omitempty"`
	Cost          *CostInfo      `json:"cost,omitempty"`
	ContextWindow *ContextWindow `json:"context_window,omitempty"`
	Vim           any            `json:"vim,omitempty"`
	RateLimits    any            `json:"rate_limits,omitempty"`
}

type ModelInfo struct {
	ID          string `json:"id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type Workspace struct {
	CurrentDir string `json:"current_dir,omitempty"`
	ProjectDir string `json:"project_dir,omitempty"`
}

type OutputStyle struct {
	Name string `json:"name,omitempty"`
}

type Effort struct {
	Level string `json:"level,omitempty"`
}

type CostInfo struct {
	TotalCostUSD       float64 `json:"total_cost_usd,omitempty"`
	TotalDurationMs    int64   `json:"total_duration_ms,omitempty"`
	TotalAPIDurationMs int64   `json:"total_api_duration_ms,omitempty"`
	TotalLinesAdded    int     `json:"total_lines_added,omitempty"`
	TotalLinesRemoved  int     `json:"total_lines_removed,omitempty"`
}

type ContextWindow struct {
	ContextWindowSize   int           `json:"context_window_size,omitempty"`
	TotalInputTokens    int           `json:"total_input_tokens,omitempty"`
	TotalOutputTokens   int           `json:"total_output_tokens,omitempty"`
	CurrentUsage        *CurrentUsage `json:"current_usage,omitempty"`
	UsedPercentage      *float64      `json:"used_percentage,omitempty"`
	RemainingPercentage *float64      `json:"remaining_percentage,omitempty"`
}

type CurrentUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

func Enabled(cfg config.StatusLineSettings) bool {
	return cfg.Enabled && strings.EqualFold(strings.TrimSpace(cfg.Type), "command") && strings.TrimSpace(cfg.Command) != ""
}

func Timeout(cfg config.StatusLineSettings) time.Duration {
	if cfg.TimeoutMs > 0 {
		return time.Duration(cfg.TimeoutMs) * time.Millisecond
	}
	return DefaultTimeoutMs * time.Millisecond
}

func RefreshInterval(cfg config.StatusLineSettings) time.Duration {
	if cfg.RefreshInterval <= 0 {
		return 0
	}
	if cfg.RefreshInterval > 60 {
		return 60 * time.Second
	}
	return time.Duration(cfg.RefreshInterval) * time.Second
}

func Hash(payload []byte, width int, command string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", width, command, payload)))
	return hex.EncodeToString(sum[:])
}

func MarshalPayload(payload Payload) ([]byte, error) {
	return json.Marshal(payload)
}

type Result struct {
	Output string
	Stderr string
	Err    error
}

func Run(ctx context.Context, settings *config.Settings, cfg config.StatusLineSettings, payload []byte, width int) Result {
	if settings == nil {
		settings = config.DefaultSettings()
	}
	shell := settings.GetShell()
	command := strings.TrimSpace(cfg.Command)
	if prefix := strings.TrimSpace(settings.ShellCommandPrefix); prefix != "" {
		command = prefix + " " + command
	}

	cmd := exec.CommandContext(ctx, shell, platform.ShellArgs(shell, command)...)
	cmd.Stdin = strings.NewReader(string(payload))
	cmd.Env = append(os.Environ(), fmt.Sprintf("CCSTATUSLINE_WIDTH=%d", width))

	var stdout, stderr limitedBuffer
	stdout.max = MaxOutputBytes
	stderr.max = MaxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return Result{
		Output: strings.TrimRight(stdout.String(), "\r\n"),
		Stderr: strings.TrimSpace(stderr.String()),
		Err:    err,
	}
}

type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	remaining := b.max - len(b.buf)
	if remaining > 0 {
		if len(p) > remaining {
			b.buf = append(b.buf, p[:remaining]...)
		} else {
			b.buf = append(b.buf, p...)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.buf)
}
