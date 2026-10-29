package serve

import "encoding/json"

func DecodeConfigBytes(data []byte) (*Config, error) {
	cfg := DefaultConfig()
	if err := DecodeConfigBytesInto(cfg, data); err != nil {
		return nil, err
	}
	normalize(cfg)
	return cfg, nil
}

func DecodeConfigBytesInto(cfg *Config, data []byte) error {
	if err := json.Unmarshal(data, cfg); err != nil {
		return err
	}
	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	applyRawConfig(cfg, &raw)
	return nil
}

func applyRawConfig(cfg *Config, raw *rawConfig) {
	if cfg == nil || raw == nil {
		return
	}

	if raw.Listen != "" {
		cfg.API.Listen = raw.Listen
	}
	if raw.Provider != "" {
		cfg.API.Provider = raw.Provider
	}
	if raw.Model != "" {
		cfg.API.Model = raw.Model
	}
	if raw.Mode != "" {
		cfg.API.DefaultMode = raw.Mode
	}
	if raw.DefaultWorkDir != "" {
		cfg.API.DefaultWorkDir = raw.DefaultWorkDir
		cfg.API.WorkingDir = ""
	} else if raw.WorkDir != "" {
		cfg.API.DefaultWorkDir = raw.WorkDir
		cfg.API.WorkingDir = ""
	}
	if raw.Auth != nil {
		if raw.Auth.Enabled != nil {
			cfg.API.Auth.Enabled = *raw.Auth.Enabled
		}
		if raw.Auth.Tokens != nil {
			cfg.API.Auth.Tokens = append([]string(nil), raw.Auth.Tokens...)
		}
	}
	if raw.Sandbox != nil {
		if raw.Sandbox.Enabled != nil {
			cfg.API.Sandbox.Enabled = *raw.Sandbox.Enabled
		}
		if raw.Sandbox.Level != "" {
			cfg.API.Sandbox.Level = raw.Sandbox.Level
		}
	}
	if raw.AllowedWorkDirs != nil {
		allowed := append([]string(nil), (*raw.AllowedWorkDirs)...)
		cfg.API.AllowedWorkDirs = &allowed
	}
	if raw.Session != nil {
		if raw.Session.IdleTimeoutSeconds != nil {
			cfg.API.Session.IdleTimeoutSeconds = *raw.Session.IdleTimeoutSeconds
		}
		if raw.Session.MaxSessions != nil {
			cfg.API.Session.MaxSessions = *raw.Session.MaxSessions
		}
	}
	if raw.ToolVisibility != nil {
		if raw.ToolVisibility.Mode != "" {
			cfg.API.ToolVisibility.Mode = raw.ToolVisibility.Mode
		}
		if raw.ToolVisibility.Detail != "" {
			cfg.API.ToolVisibility.Detail = raw.ToolVisibility.Detail
		}
	}
	if raw.Thinking != "" {
		cfg.API.DefaultThinkingLevel = raw.Thinking
	}
	if raw.SystemPromptMode != "" {
		cfg.API.SystemPromptMode = raw.SystemPromptMode
	}
	if raw.RequestTimeoutSecs != nil {
		cfg.API.RequestTimeoutSecs = *raw.RequestTimeoutSecs
	}
	if raw.MaxConcurrentReqs != nil {
		cfg.API.MaxConcurrentReqs = *raw.MaxConcurrentReqs
	}
	if raw.WebSearch != nil {
		cfg.API.EnableWebSearch = *raw.WebSearch
	} else if raw.API != nil && raw.API.EnableWebSearch != nil {
		cfg.API.EnableWebSearch = *raw.API.EnableWebSearch
	}
	if raw.Browser != nil {
		cfg.API.EnableBrowser = *raw.Browser
	} else if raw.API != nil && raw.API.EnableBrowser != nil {
		cfg.API.EnableBrowser = *raw.API.EnableBrowser
	}
	if raw.Artifact != nil {
		cfg.API.EnableArtifact = *raw.Artifact
	} else if raw.API != nil && raw.API.EnableArtifact != nil {
		cfg.API.EnableArtifact = *raw.API.EnableArtifact
	}
	if raw.A2AMaster != nil {
		cfg.API.EnableA2AMaster = *raw.A2AMaster
	} else if raw.API != nil && raw.API.EnableA2AMaster != nil {
		cfg.API.EnableA2AMaster = *raw.API.EnableA2AMaster
	}
	if raw.API != nil {
		if raw.API.EnableDelegate != nil {
			cfg.API.EnableDelegate = *raw.API.EnableDelegate
		}
		if raw.API.EnableWorkflows != nil {
			cfg.API.EnableWorkflows = *raw.API.EnableWorkflows
		}
	}
	if raw.Agent != nil {
		if raw.Agent.MaxTurns != nil {
			cfg.Agent.MaxTurns = *raw.Agent.MaxTurns
		}
		if raw.Agent.BudgetPressure != nil {
			cfg.Agent.BudgetPressure = *raw.Agent.BudgetPressure
		}
		if raw.Agent.ContextPressure != nil {
			cfg.Agent.ContextPressure = *raw.Agent.ContextPressure
		}
		if raw.Agent.BudgetPressureThreshold != nil {
			cfg.Agent.BudgetPressureThreshold = *raw.Agent.BudgetPressureThreshold
		}
		if raw.Agent.ContextPressureThreshold != nil {
			cfg.Agent.ContextPressureThreshold = *raw.Agent.ContextPressureThreshold
		}
		if raw.Agent.RunStaleTimeoutSecs != nil {
			cfg.Agent.RunStaleTimeoutSecs = *raw.Agent.RunStaleTimeoutSecs
		}
		if raw.Agent.RunMaxDurationSecs != nil {
			cfg.Agent.RunMaxDurationSecs = *raw.Agent.RunMaxDurationSecs
		}
		if raw.Agent.BackgroundRunMaxSecs != nil {
			cfg.Agent.BackgroundRunMaxSecs = *raw.Agent.BackgroundRunMaxSecs
		}
	}
	if raw.WebUI != nil {
		if raw.WebUI.Enabled != nil {
			cfg.WebUI.Enabled = *raw.WebUI.Enabled
		}
		if raw.WebUI.Dir != "" {
			cfg.WebUI.Dir = raw.WebUI.Dir
		}
	}
	if raw.Cron != nil {
		if raw.Cron.Enabled != nil {
			cfg.Cron.Enabled = *raw.Cron.Enabled
		}
		if raw.Cron.Interval != nil {
			cfg.Cron.Interval = *raw.Cron.Interval
		}
	}
	if raw.Memory != nil {
		if raw.Memory.Enabled != nil {
			cfg.Memory.Enabled = *raw.Memory.Enabled
		}
		if raw.Memory.Path != "" {
			cfg.Memory.Path = raw.Memory.Path
		}
	}
	if raw.Security != nil {
		if raw.Security.SmartApprovals != nil {
			cfg.Security.SmartApprovals = *raw.Security.SmartApprovals
		}
		if raw.Security.AllowedWorkDirs != nil {
			cfg.Security.AllowedWorkDirs = append([]string(nil), (*raw.Security.AllowedWorkDirs)...)
		}
	}
	if raw.Hooks != nil {
		if raw.Hooks.PreToolCall != "" {
			cfg.Hooks.PreToolCall = raw.Hooks.PreToolCall
		}
		if raw.Hooks.PostToolCall != "" {
			cfg.Hooks.PostToolCall = raw.Hooks.PostToolCall
		}
	}
	if raw.Channels != nil {
		applyRawChannels(&cfg.Channels, raw.Channels)
	}

	if raw.Features != nil {
		if raw.Features.WebUI != nil {
			cfg.Features.WebUI = *raw.Features.WebUI
			cfg.WebUI.Enabled = *raw.Features.WebUI
		}
		if raw.Features.OpenAIAPI != nil {
			cfg.Features.OpenAIAPI = *raw.Features.OpenAIAPI
		}
		if raw.Features.MultiAgent != nil {
			cfg.Features.MultiAgent = *raw.Features.MultiAgent
			cfg.API.EnableSubAgents = *raw.Features.MultiAgent
		}
		if raw.Features.Wechat != nil {
			cfg.Features.Wechat = *raw.Features.Wechat
			cfg.Channels.Wechat.Enabled = *raw.Features.Wechat
		}
		if raw.Features.Feishu != nil {
			cfg.Features.Feishu = *raw.Features.Feishu
			cfg.Channels.Feishu.Enabled = *raw.Features.Feishu
		}
		if raw.Features.Cron != nil {
			cfg.Features.Cron = *raw.Features.Cron
			cfg.Cron.Enabled = *raw.Features.Cron
		}
		if raw.Features.Memory != nil {
			cfg.Features.Memory = *raw.Features.Memory
			cfg.Memory.Enabled = *raw.Features.Memory
		}
	}

	if raw.LobsterMode {
		cfg.LobsterMode = true
	}
}

func applyRawChannels(cfg *ChannelConfig, raw *rawChannelConfig) {
	if cfg == nil || raw == nil {
		return
	}
	if raw.Artifact != nil {
		cfg.Artifact = *raw.Artifact
	}
	if raw.Wechat != nil {
		if raw.Wechat.Enabled != nil {
			cfg.Wechat.Enabled = *raw.Wechat.Enabled
		}
		if raw.Wechat.AutoTyping != nil {
			cfg.Wechat.AutoTyping = *raw.Wechat.AutoTyping
		}
		if raw.Wechat.CredPath != "" {
			cfg.Wechat.CredPath = raw.Wechat.CredPath
		}
		if raw.Wechat.WorkDir != "" {
			cfg.Wechat.WorkDir = raw.Wechat.WorkDir
		}
	}
	if raw.Feishu != nil {
		if raw.Feishu.Enabled != nil {
			cfg.Feishu.Enabled = *raw.Feishu.Enabled
		}
		if raw.Feishu.AppID != "" {
			cfg.Feishu.AppID = raw.Feishu.AppID
		}
		if raw.Feishu.AppSecret != "" {
			cfg.Feishu.AppSecret = raw.Feishu.AppSecret
		}
		if raw.Feishu.WorkDir != "" {
			cfg.Feishu.WorkDir = raw.Feishu.WorkDir
		}
	}
}

func (c *Config) MarshalJSON() ([]byte, error) {
	normalize(c)

	features := rawFeaturesConfig{}
	wechatEnabled := c.Channels.Wechat.Enabled
	wechatAutoTyping := c.Channels.Wechat.AutoTyping
	feishuEnabled := c.Channels.Feishu.Enabled
	authEnabled := c.API.Auth.Enabled
	sandboxEnabled := c.API.Sandbox.Enabled
	webUIEnabled := c.WebUI.Enabled
	openAIAPIEnabled := c.Features.OpenAIAPI
	multiAgentEnabled := c.API.EnableSubAgents
	cronEnabled := c.Cron.Enabled
	memoryEnabled := c.Memory.Enabled
	idleTimeoutSeconds := c.API.Session.IdleTimeoutSeconds
	maxSessions := c.API.Session.MaxSessions
	requestTimeoutSeconds := c.API.RequestTimeoutSecs
	maxConcurrentRequests := c.API.MaxConcurrentReqs
	webSearchEnabled := c.API.EnableWebSearch
	browserEnabled := c.API.EnableBrowser
	artifactEnabled := c.API.EnableArtifact
	channelArtifactEnabled := c.Channels.Artifact
	a2aMasterEnabled := c.API.EnableA2AMaster
	defaultWorkDir := c.API.DefaultWorkDir
	if defaultWorkDir == "" {
		defaultWorkDir = c.API.WorkingDir
	}
	agentMaxTurns := c.Agent.MaxTurns
	agentBudgetPressure := c.Agent.BudgetPressure
	agentContextPressure := c.Agent.ContextPressure
	agentBudgetPressureThreshold := c.Agent.BudgetPressureThreshold
	agentContextPressureThreshold := c.Agent.ContextPressureThreshold
	agentRunStaleTimeoutSecs := c.Agent.RunStaleTimeoutSecs
	agentRunMaxDurationSecs := c.Agent.RunMaxDurationSecs
	agentBackgroundRunMaxSecs := c.Agent.BackgroundRunMaxSecs
	cronInterval := c.Cron.Interval
	memoryStoreEnabled := c.Memory.Enabled
	securitySmartApprovals := c.Security.SmartApprovals
	terminal := rawTerminalConfig{
		Enabled:              c.Terminal.Enabled,
		Shell:                c.Terminal.Shell,
		MaxSessions:          c.Terminal.MaxSessions,
		MaxPerSession:        c.Terminal.MaxPerSession,
		ScrollbackBytes:      c.Terminal.ScrollbackBytes,
		MaxOutputBytesPerSec: c.Terminal.MaxOutputBytesPerSec,
		AllowedOrigins:       append([]string(nil), c.Terminal.AllowedOrigins...),
	}

	features.WebUI = &webUIEnabled
	features.OpenAIAPI = &openAIAPIEnabled
	features.Wechat = &wechatEnabled
	features.Feishu = &feishuEnabled
	features.MultiAgent = &multiAgentEnabled
	features.Cron = &cronEnabled
	features.Memory = &memoryEnabled

	raw := rawConfig{
		Listen:             c.API.Listen,
		Provider:           c.API.Provider,
		Model:              c.API.Model,
		Mode:               c.API.DefaultMode,
		DefaultWorkDir:     defaultWorkDir,
		Auth:               &rawAuthConfig{Enabled: &authEnabled, Tokens: append([]string(nil), c.API.Auth.Tokens...)},
		Features:           &features,
		Sandbox:            &rawSandboxConfig{Enabled: &sandboxEnabled, Level: c.API.Sandbox.Level},
		AllowedWorkDirs:    c.API.AllowedWorkDirs,
		Session:            &rawSessionConfig{IdleTimeoutSeconds: &idleTimeoutSeconds, MaxSessions: &maxSessions},
		Terminal:           &terminal,
		ToolVisibility:     &rawToolVisibilityConfig{Mode: c.API.ToolVisibility.Mode, Detail: c.API.ToolVisibility.Detail},
		Thinking:           c.API.DefaultThinkingLevel,
		SystemPromptMode:   c.API.SystemPromptMode,
		RequestTimeoutSecs: &requestTimeoutSeconds,
		MaxConcurrentReqs:  &maxConcurrentRequests,
		WebSearch:          &webSearchEnabled,
		Browser:            &browserEnabled,
		Artifact:           &artifactEnabled,
		A2AMaster:          &a2aMasterEnabled,
		Agent: &rawAgentConfig{
			MaxTurns:                 &agentMaxTurns,
			BudgetPressure:           &agentBudgetPressure,
			ContextPressure:          &agentContextPressure,
			BudgetPressureThreshold:  &agentBudgetPressureThreshold,
			ContextPressureThreshold: &agentContextPressureThreshold,
			RunStaleTimeoutSecs:      &agentRunStaleTimeoutSecs,
			RunMaxDurationSecs:       &agentRunMaxDurationSecs,
			BackgroundRunMaxSecs:     &agentBackgroundRunMaxSecs,
		},
		WebUI:       &rawWebUIConfig{Enabled: &webUIEnabled, Dir: c.WebUI.Dir},
		LobsterMode: c.LobsterMode,
		Cron:        &rawCronConfig{Enabled: &cronEnabled, Interval: &cronInterval},
		Memory:      &rawMemoryConfig{Enabled: &memoryStoreEnabled, Path: c.Memory.Path},
		Security:    &rawSecurityConfig{SmartApprovals: &securitySmartApprovals, AllowedWorkDirs: &c.Security.AllowedWorkDirs},
		Hooks:       &rawHooksConfig{PreToolCall: c.Hooks.PreToolCall, PostToolCall: c.Hooks.PostToolCall},
		Channels: &rawChannelConfig{
			Artifact: &channelArtifactEnabled,
			Wechat: &rawWechatConfig{
				Enabled:    &wechatEnabled,
				CredPath:   c.Channels.Wechat.CredPath,
				WorkDir:    c.Channels.Wechat.WorkDir,
				AutoTyping: &wechatAutoTyping,
			},
			Feishu: &rawFeishuConfig{
				Enabled:   &feishuEnabled,
				AppID:     c.Channels.Feishu.AppID,
				AppSecret: c.Channels.Feishu.AppSecret,
				WorkDir:   c.Channels.Feishu.WorkDir,
			},
		},
	}

	return json.Marshal(raw)
}
