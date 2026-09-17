package channels

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
)

// Config holds messaging channel runtime configuration.
type Config struct {
	DefaultProvider string         `json:"default_provider,omitempty"`
	DefaultModel    string         `json:"default_model,omitempty"`
	MultiAgent      bool           `json:"multi_agent,omitempty"`
	WebSearch       bool           `json:"web_search,omitempty"`
	Browser         bool           `json:"browser,omitempty"`
	Artifact        bool           `json:"artifact,omitempty"`
	A2AMaster       bool           `json:"a2a_master,omitempty"`
	Sandbox         bool           `json:"sandbox,omitempty"`
	Wechat          WechatConfig   `json:"wechat"`
	Feishu          FeishuConfig   `json:"feishu"`
	Webhooks        WebhookConfig  `json:"webhooks"`
	Cron            CronConfig     `json:"cron"`
	Memory          MemoryConfig   `json:"memory"`
	Security        SecurityConfig `json:"security"`
	Hooks           HooksConfig    `json:"hooks"`
	Agent           AgentConfig    `json:"agent"`
	WorkDir         string         `json:"work_dir"`
}

// WechatConfig defines WeChat iLink platform settings.
type WechatConfig struct {
	Enabled    bool   `json:"enabled"`
	CredPath   string `json:"cred_path"`
	WorkDir    string `json:"work_dir"`
	AutoTyping bool   `json:"auto_typing"`
}

// FeishuConfig defines Feishu (Lark) platform settings.
type FeishuConfig struct {
	Enabled   bool   `json:"enabled"`
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	WorkDir   string `json:"work_dir"`
}

// WebhookConfig defines inbound webhook settings.
type WebhookConfig struct {
	Enabled bool           `json:"enabled"`
	Secret  string         `json:"secret"`
	Routes  []WebhookRoute `json:"routes"`
}

// WebhookRoute maps an inbound webhook path to an agent skill + delivery.
type WebhookRoute struct {
	Path           string   `json:"path"`
	Events         []string `json:"events"`
	Skill          string   `json:"skill"`
	Delivery       string   `json:"delivery"`
	DeliveryTarget string   `json:"delivery_target,omitempty"`
}

// CronConfig defines cron scheduler settings.
type CronConfig struct {
	Enabled  bool `json:"enabled"`
	Interval int  `json:"interval,omitempty"` // seconds between checks (default 30)
}

// MemoryConfig defines persistent memory settings.
type MemoryConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"` // empty = auto-discover .mothx/memory.md → <GLOBAL_DIR>/memory.md
}

// SecurityConfig defines security settings.
type SecurityConfig struct {
	SmartApprovals  bool     `json:"smart_approvals"`
	AllowedWorkDirs []string `json:"allowed_work_dirs"`
}

// HooksConfig defines shell hook scripts.
type HooksConfig struct {
	PreToolCall  string `json:"pre_tool_call"`
	PostToolCall string `json:"post_tool_call"`
}

// AgentConfig defines agent behavior settings.
type AgentConfig struct {
	MaxTurns                 int     `json:"max_turns"`
	BudgetPressure           bool    `json:"budget_pressure"`
	ContextPressure          bool    `json:"context_pressure"`
	BudgetPressureThreshold  float64 `json:"budget_pressure_threshold,omitempty"`  // remaining ratio (0-1), default 0.20
	ContextPressureThreshold float64 `json:"context_pressure_threshold,omitempty"` // usage ratio (0-1), default 0.55
	RunStaleTimeoutSecs      int     `json:"run_stale_timeout_secs,omitempty"`     // watchdog: abort a run with no agent events for this long (default 600)
	RunMaxDurationSecs       int     `json:"run_max_duration_secs,omitempty"`      // watchdog: abort a run exceeding this total duration (default 57600)
	BackgroundRunMaxSecs     int     `json:"background_run_max_secs,omitempty"`    // hard cap for durable background polling (default 57600)
}

const (
	defaultRunStaleTimeoutSecs = 600
	// The run watchdog and the durable background-polling cap share the agent
	// loop's wall-clock budget so a run the policy allows is never cut short by a
	// shorter watchdog or polling limit.
	defaultRunMaxDuration   = agent.DefaultIterationBudgetWallClock
	defaultBackgroundRunMax = agent.DefaultIterationBudgetWallClock
)

// GetRunStaleTimeout returns the watchdog inactivity timeout for channel runs.
func (c AgentConfig) GetRunStaleTimeout() time.Duration {
	if c.RunStaleTimeoutSecs <= 0 {
		return defaultRunStaleTimeoutSecs * time.Second
	}
	return time.Duration(c.RunStaleTimeoutSecs) * time.Second
}

// GetRunMaxDuration returns the watchdog total-duration cap for channel runs.
func (c AgentConfig) GetRunMaxDuration() time.Duration {
	if c.RunMaxDurationSecs <= 0 {
		return defaultRunMaxDuration
	}
	return time.Duration(c.RunMaxDurationSecs) * time.Second
}

// GetBackgroundRunMaxDuration returns the hard cap for durable background polling.
func (c AgentConfig) GetBackgroundRunMaxDuration() time.Duration {
	if c.BackgroundRunMaxSecs <= 0 {
		return defaultBackgroundRunMax
	}
	return time.Duration(c.BackgroundRunMaxSecs) * time.Second
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Wechat: WechatConfig{
			AutoTyping: true,
		},
		Cron: CronConfig{
			Enabled: true,
		},
		Memory: MemoryConfig{
			Enabled: true,
		},
		Security: SecurityConfig{
			SmartApprovals: true,
		},
		Agent: AgentConfig{
			MaxTurns:                 90,
			BudgetPressure:           true,
			ContextPressure:          true,
			BudgetPressureThreshold:  0.20,
			ContextPressureThreshold: 0.55,
			RunStaleTimeoutSecs:      defaultRunStaleTimeoutSecs,
			RunMaxDurationSecs:       int(defaultRunMaxDuration / time.Second),
			BackgroundRunMaxSecs:     int(defaultBackgroundRunMax / time.Second),
		},
		WorkDir: ".",
	}
}

// GetWorkDir returns the resolved work directory.
// Falls back to current directory if not set.
func (c *Config) GetWorkDir() string {
	if c.WorkDir != "" && c.WorkDir != "." {
		return c.WorkDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// GetPlatformWorkDir returns the work directory for a specific platform.
// Priority: platform work_dir → global work_dir → cwd
func (c *Config) GetPlatformWorkDir(platform string) string {
	switch platform {
	case "wechat":
		if c.Wechat.WorkDir != "" {
			return c.Wechat.WorkDir
		}
	case "feishu":
		if c.Feishu.WorkDir != "" {
			return c.Feishu.WorkDir
		}
	}
	return c.GetWorkDir()
}

// GetWechatCredPath returns the wechat credentials path.
func (c *Config) GetWechatCredPath() string {
	if c.Wechat.CredPath != "" {
		return c.Wechat.CredPath
	}
	return filepath.Join(config.ConfigDir(), "wechat-credentials.json")
}

// resolveEnvVars resolves ${VAR} references in string fields.
func (c *Config) resolveEnvVars() {
	c.Feishu.AppID = resolveEnv(c.Feishu.AppID)
	c.Feishu.AppSecret = resolveEnv(c.Feishu.AppSecret)
	c.Webhooks.Secret = resolveEnv(c.Webhooks.Secret)
}

// GetDefaultProvider returns the effective default provider.
// Priority: Config → Settings
func (c *Config) GetDefaultProvider(settingsProvider string) string {
	if c.DefaultProvider != "" {
		return c.DefaultProvider
	}
	return settingsProvider
}

// GetDefaultModel returns the effective default model.
// Priority: Config → Settings
func (c *Config) GetDefaultModel(settingsModel string) string {
	if c.DefaultModel != "" {
		return c.DefaultModel
	}
	if c.DefaultProvider != "" {
		return ""
	}
	return settingsModel
}

// resolveEnv resolves a single ${VAR} reference.
func resolveEnv(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		envName := s[2 : len(s)-1]
		if v := os.Getenv(envName); v != "" {
			return v
		}
	}
	return s
}
