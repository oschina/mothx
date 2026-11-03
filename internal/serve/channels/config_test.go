package channels

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Wechat.AutoTyping {
		t.Error("expected auto_typing=true")
	}
	if !cfg.Security.SmartApprovals {
		t.Error("expected smart_approvals=true")
	}
	if cfg.Agent.MaxTurns != 180 {
		t.Errorf("expected max_turns=180, got %d", cfg.Agent.MaxTurns)
	}
	if !cfg.Cron.Enabled {
		t.Error("expected cron enabled by default")
	}
	if cfg.MultiAgent {
		t.Error("expected multi_agent disabled by default")
	}
	if cfg.Artifact {
		t.Error("expected artifact publishing disabled by default")
	}
}

func TestGetDefaultProvider(t *testing.T) {
	cfg := &Config{DefaultProvider: "openai"}
	if got := cfg.GetDefaultProvider("deepseek"); got != "openai" {
		t.Errorf("expected openai, got %s", got)
	}

	cfg2 := &Config{}
	if got := cfg2.GetDefaultProvider("deepseek"); got != "deepseek" {
		t.Errorf("expected deepseek fallback, got %s", got)
	}
}

func TestGetDefaultModel(t *testing.T) {
	cfg := &Config{DefaultModel: "gpt-4o"}
	if got := cfg.GetDefaultModel("deepseek-chat"); got != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %s", got)
	}

	cfg2 := &Config{}
	if got := cfg2.GetDefaultModel("deepseek-chat"); got != "deepseek-chat" {
		t.Errorf("expected deepseek-chat fallback, got %s", got)
	}

	cfg3 := &Config{DefaultProvider: "openai"}
	if got := cfg3.GetDefaultModel("deepseek-chat"); got != "" {
		t.Errorf("expected empty string (to fall back to provider's first model) when DefaultProvider is specified, got %s", got)
	}
}

func TestGetWorkDir(t *testing.T) {
	cfg := &Config{WorkDir: "/tmp/test"}
	if got := cfg.GetWorkDir(); got != "/tmp/test" {
		t.Errorf("expected /tmp/test, got %s", got)
	}

	cfg2 := &Config{WorkDir: "."}
	got := cfg2.GetWorkDir()
	if got == "" || got == "." {
		t.Errorf("expected resolved path, got %s", got)
	}
}

func TestGetPlatformWorkDir(t *testing.T) {
	cfg := &Config{
		WorkDir: "/global",
		Wechat:  WechatConfig{WorkDir: "/wechat"},
		Feishu:  FeishuConfig{WorkDir: "/feishu"},
	}

	if got := cfg.GetPlatformWorkDir("wechat"); got != "/wechat" {
		t.Errorf("expected /wechat, got %s", got)
	}
	if got := cfg.GetPlatformWorkDir("feishu"); got != "/feishu" {
		t.Errorf("expected /feishu, got %s", got)
	}
	if got := cfg.GetPlatformWorkDir("ws"); got != "/global" {
		t.Errorf("expected /global, got %s", got)
	}
}

func TestCronConfig(t *testing.T) {
	cfg := &Config{
		Cron: CronConfig{
			Enabled:  true,
			Interval: 60,
		},
	}
	if !cfg.Cron.Enabled {
		t.Error("expected cron enabled")
	}
	if cfg.Cron.Interval != 60 {
		t.Errorf("expected interval 60, got %d", cfg.Cron.Interval)
	}
}
