package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/startvibecoding/mothx/internal/config"
	channels "github.com/startvibecoding/mothx/internal/serve/channels"
	openaiapi "github.com/startvibecoding/mothx/internal/serve/openaiapi"
)

type Config struct {
	API         openaiapi.Config        `json:"api"`
	Features    FeatureConfig           `json:"features"`
	Channels    ChannelConfig           `json:"channels"`
	WebUI       WebUIConfig             `json:"webUI"`
	LobsterMode bool                    `json:"lobsterMode,omitempty"`
	Cron        channels.CronConfig     `json:"cron"`
	Memory      channels.MemoryConfig   `json:"memory"`
	Security    channels.SecurityConfig `json:"security"`
	Hooks       channels.HooksConfig    `json:"hooks"`
	Agent       channels.AgentConfig    `json:"agent"`
}

type FeatureConfig struct {
	WebUI      bool `json:"webUI,omitempty"`
	OpenAIAPI  bool `json:"openaiAPI,omitempty"`
	Wechat     bool `json:"wechat,omitempty"`
	Feishu     bool `json:"feishu,omitempty"`
	MultiAgent bool `json:"multiAgent,omitempty"`
	Cron       bool `json:"cron,omitempty"`
	Memory     bool `json:"memory,omitempty"`
}

type ChannelConfig struct {
	Artifact bool                  `json:"artifact,omitempty"`
	Wechat   channels.WechatConfig `json:"wechat"`
	Feishu   channels.FeishuConfig `json:"feishu"`
}

type WebUIConfig struct {
	Enabled bool   `json:"enabled"`
	Dir     string `json:"dir,omitempty"`
}

func DefaultConfig() *Config {
	gw := openaiapi.DefaultConfig()
	gw.Listen = "127.0.0.1:7872"
	h := channels.DefaultConfig()
	return &Config{
		API: *gw,
		Features: FeatureConfig{
			WebUI:      true,
			OpenAIAPI:  true,
			Wechat:     false,
			Feishu:     false,
			MultiAgent: gw.EnableSubAgents,
			Cron:       h.Cron.Enabled,
			Memory:     h.Memory.Enabled,
		},
		WebUI:    WebUIConfig{Enabled: true, Dir: "ui/dist"},
		Cron:     h.Cron,
		Memory:   h.Memory,
		Security: h.Security,
		Hooks:    h.Hooks,
		Agent:    h.Agent,
	}
}

func ConfigPath() string {
	return filepath.Join(config.ConfigDir(), "serve.json")
}

func ProjectConfigPath() string {
	return config.ProjectPath("serve.json")
}

func LoadConfig() (*Config, error) {
	cfg, err := LoadConfigFrom(ConfigPath())
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(ProjectConfigPath()); err == nil {
		if err := DecodeConfigBytesInto(cfg, data); err != nil {
			return nil, fmt.Errorf("parse project serve config %s: %w", ProjectConfigPath(), err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read project serve config %s: %w", ProjectConfigPath(), err)
	}
	normalize(cfg)
	return cfg, nil
}

func LoadConfigFrom(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read serve config %s: %w", path, err)
	}
	if err := DecodeConfigBytesInto(cfg, data); err != nil {
		return nil, fmt.Errorf("parse serve config %s: %w", path, err)
	}
	normalize(cfg)
	return cfg, nil
}

func normalize(cfg *Config) {
	if cfg.API.Listen == "" {
		cfg.API.Listen = "127.0.0.1:7872"
	}
	if cfg.API.DefaultMode == "" {
		cfg.API.DefaultMode = "yolo"
	}
	if cfg.API.ToolVisibility.Mode == "" {
		cfg.API.ToolVisibility.Mode = "content"
	}
	if cfg.API.ToolVisibility.Detail == "" {
		cfg.API.ToolVisibility.Detail = "collapsed"
	}
	if cfg.API.SystemPromptMode == "" {
		cfg.API.SystemPromptMode = "append"
	}
	if cfg.API.DefaultThinkingLevel == "" {
		cfg.API.DefaultThinkingLevel = "medium"
	}
	if cfg.API.RequestTimeoutSecs <= 0 {
		cfg.API.RequestTimeoutSecs = 1800
	}
	if cfg.WebUI.Dir == "" {
		cfg.WebUI.Dir = "ui/dist"
	}
	if cfg.Agent.MaxTurns == 0 {
		cfg.Agent = channels.DefaultConfig().Agent
	}
	if cfg.API.BackgroundRunMaxSecs <= 0 {
		cfg.API.BackgroundRunMaxSecs = cfg.Agent.BackgroundRunMaxSecs
	}
	if cfg.LobsterMode {
		cfg.API.DefaultMode = "yolo"
		cfg.API.Sandbox.Enabled = false
		cfg.API.EnableSubAgents = true
	}
	cfg.Features.WebUI = cfg.WebUI.Enabled
	cfg.Features.Wechat = cfg.Channels.Wechat.Enabled
	cfg.Features.Feishu = cfg.Channels.Feishu.Enabled
	cfg.Features.MultiAgent = cfg.API.EnableSubAgents
	cfg.Features.Cron = cfg.Cron.Enabled
	cfg.Features.Memory = cfg.Memory.Enabled
}

func SaveConfig(path string, cfg *Config) error {
	normalize(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal serve config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

func InitConfig(force bool) (string, error) {
	return InitConfigForProject(false, force)
}

// PlaceholderAuthToken is the API token written by `mothx serve init-config`.
// It is a well-known value, so leaving it in place is equivalent to publishing
// the API key: the server warns on startup until it is replaced.
const PlaceholderAuthToken = "sk-change-me-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

// PlaceholderAuthTokenWarning is printed wherever a template token is created or
// is still in use.
const PlaceholderAuthTokenWarning = "WARNING: serve.json ships the placeholder API token (" + PlaceholderAuthToken + "),\n" +
	"         which is public. Replace api.auth.tokens before enabling auth or exposing this server."

// IsPlaceholderAuthToken reports whether token is the generated template value.
func IsPlaceholderAuthToken(token string) bool {
	return strings.TrimSpace(token) == PlaceholderAuthToken
}

// UsesPlaceholderAuthToken reports whether the resolved config enables auth and
// still ships the generated template token, which means the API is reachable
// with a publicly known key.
func UsesPlaceholderAuthToken(cfg *Config) bool {
	if cfg == nil || !cfg.API.Auth.Enabled {
		return false
	}
	for _, token := range cfg.API.Auth.Tokens {
		if IsPlaceholderAuthToken(token) {
			return true
		}
	}
	return false
}

func InitConfigForProject(project bool, force bool) (string, error) {
	path := ConfigPath()
	if project {
		path = ProjectConfigPath()
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("serve.json already exists: %s", path)
		}
	}
	cfg := DefaultConfig()
	cfg.API.Auth.Tokens = []string{PlaceholderAuthToken}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/home/user"
	}
	cfg.API.DefaultWorkDir = filepath.Join(home, "projects")
	return path, SaveConfig(path, cfg)
}
