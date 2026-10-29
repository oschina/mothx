package serve

type rawConfig struct {
	Listen             string                   `json:"listen,omitempty"`
	Provider           string                   `json:"provider,omitempty"`
	Model              string                   `json:"model,omitempty"`
	Mode               string                   `json:"mode,omitempty"`
	DefaultWorkDir     string                   `json:"defaultWorkDir,omitempty"`
	WorkDir            string                   `json:"workDir,omitempty"` // legacy alias for defaultWorkDir
	Auth               *rawAuthConfig           `json:"auth,omitempty"`
	API                *rawAPIConfig            `json:"api,omitempty"`
	Features           *rawFeaturesConfig       `json:"features,omitempty"`
	Sandbox            *rawSandboxConfig        `json:"sandbox,omitempty"`
	AllowedWorkDirs    *[]string                `json:"allowedWorkDirs,omitempty"`
	Channels           *rawChannelConfig        `json:"channels,omitempty"`
	Terminal           *rawTerminalConfig       `json:"terminal,omitempty"`
	Session            *rawSessionConfig        `json:"session,omitempty"`
	ToolVisibility     *rawToolVisibilityConfig `json:"toolVisibility,omitempty"`
	Thinking           string                   `json:"thinking,omitempty"`
	SystemPromptMode   string                   `json:"systemPromptMode,omitempty"`
	RequestTimeoutSecs *int                     `json:"requestTimeoutSeconds,omitempty"`
	MaxConcurrentReqs  *int                     `json:"maxConcurrentRequests,omitempty"`
	WebSearch          *bool                    `json:"webSearch,omitempty"`
	Browser            *bool                    `json:"browser,omitempty"`
	Artifact           *bool                    `json:"artifact,omitempty"`
	A2AMaster          *bool                    `json:"a2aMaster,omitempty"`
	Agent              *rawAgentConfig          `json:"agent,omitempty"`
	WebUI              *rawWebUIConfig          `json:"webUI,omitempty"`
	LobsterMode        bool                     `json:"lobsterMode,omitempty"`
	Cron               *rawCronConfig           `json:"cron,omitempty"`
	Memory             *rawMemoryConfig         `json:"memory,omitempty"`
	Security           *rawSecurityConfig       `json:"security,omitempty"`
	Hooks              *rawHooksConfig          `json:"hooks,omitempty"`
}

type rawAPIConfig struct {
	EnableWebSearch *bool `json:"enableWebSearch,omitempty"`
	EnableBrowser   *bool `json:"enableBrowser,omitempty"`
	EnableArtifact  *bool `json:"enableArtifact,omitempty"`
	EnableA2AMaster *bool `json:"enableA2AMaster,omitempty"`
	EnableDelegate  *bool `json:"enableDelegate,omitempty"`
	EnableWorkflows *bool `json:"enableWorkflows,omitempty"`
}

type rawFeaturesConfig struct {
	WebUI      *bool `json:"webUI,omitempty"`
	OpenAIAPI  *bool `json:"openaiAPI,omitempty"`
	Wechat     *bool `json:"wechat,omitempty"`
	Feishu     *bool `json:"feishu,omitempty"`
	MultiAgent *bool `json:"multiAgent,omitempty"`
	Cron       *bool `json:"cron,omitempty"`
	Memory     *bool `json:"memory,omitempty"`
}

type rawChannelConfig struct {
	Artifact *bool            `json:"artifact,omitempty"`
	Wechat   *rawWechatConfig `json:"wechat,omitempty"`
	Feishu   *rawFeishuConfig `json:"feishu,omitempty"`
}

type rawAuthConfig struct {
	Enabled *bool    `json:"enabled,omitempty"`
	Tokens  []string `json:"tokens,omitempty"`
}

type rawSandboxConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Level   string `json:"level,omitempty"`
}

type rawSessionConfig struct {
	IdleTimeoutSeconds *int `json:"idleTimeoutSeconds,omitempty"`
	MaxSessions        *int `json:"maxSessions,omitempty"`
}

// rawTerminalConfig mirrors TerminalConfig on the wire. Enabled stays a
// pointer so "auto" (omitted) is distinct from an explicit false.
type rawTerminalConfig struct {
	Enabled              *bool    `json:"enabled,omitempty"`
	Shell                string   `json:"shell,omitempty"`
	MaxSessions          int      `json:"maxSessions,omitempty"`
	MaxPerSession        int      `json:"maxPerSession,omitempty"`
	ScrollbackBytes      int      `json:"scrollbackBytes,omitempty"`
	MaxOutputBytesPerSec int      `json:"maxOutputBytesPerSec,omitempty"`
	AllowedOrigins       []string `json:"allowedOrigins,omitempty"`
}

type rawToolVisibilityConfig struct {
	Mode   string `json:"mode,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type rawWechatConfig struct {
	Enabled    *bool  `json:"enabled,omitempty"`
	CredPath   string `json:"credPath,omitempty"`
	WorkDir    string `json:"workDir,omitempty"`
	AutoTyping *bool  `json:"autoTyping,omitempty"`
}

type rawFeishuConfig struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	AppID     string `json:"appId,omitempty"`
	AppSecret string `json:"appSecret,omitempty"`
	WorkDir   string `json:"workDir,omitempty"`
}

type rawAgentConfig struct {
	MaxTurns                 *int     `json:"maxTurns,omitempty"`
	BudgetPressure           *bool    `json:"budgetPressure,omitempty"`
	ContextPressure          *bool    `json:"contextPressure,omitempty"`
	BudgetPressureThreshold  *float64 `json:"budgetPressureThreshold,omitempty"`
	ContextPressureThreshold *float64 `json:"contextPressureThreshold,omitempty"`
	RunStaleTimeoutSecs      *int     `json:"runStaleTimeoutSeconds,omitempty"`
	RunMaxDurationSecs       *int     `json:"runMaxDurationSeconds,omitempty"`
	BackgroundRunMaxSecs     *int     `json:"backgroundRunMaxSeconds,omitempty"`
}

type rawWebUIConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Dir     string `json:"dir,omitempty"`
}

type rawCronConfig struct {
	Enabled  *bool `json:"enabled,omitempty"`
	Interval *int  `json:"interval,omitempty"`
}

type rawMemoryConfig struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Path    string `json:"path,omitempty"`
}

type rawSecurityConfig struct {
	SmartApprovals  *bool     `json:"smartApprovals,omitempty"`
	AllowedWorkDirs *[]string `json:"allowedWorkDirs,omitempty"`
}

type rawHooksConfig struct {
	PreToolCall  string `json:"preToolCall,omitempty"`
	PostToolCall string `json:"postToolCall,omitempty"`
}
