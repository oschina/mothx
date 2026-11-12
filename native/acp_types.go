package main

import "encoding/json"

// ACP v1 payloads used by the native client. The wire types mirror
// internal/acp/acp.go and stay deliberately additive.

type clientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type initializeResult struct {
	ProtocolVersion   int                        `json:"protocolVersion"`
	AgentInfo         clientInfo                 `json:"agentInfo"`
	AgentCapabilities json.RawMessage            `json:"agentCapabilities"`
	Meta              map[string]json.RawMessage `json:"_meta"`
}

type sessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type sessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []sessionMode `json:"availableModes"`
}

type configChoice struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type configOption struct {
	Type         string         `json:"type"`
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Category     string         `json:"category,omitempty"`
	CurrentValue string         `json:"currentValue"`
	Options      []configChoice `json:"options,omitempty"`
}

type sessionResult struct {
	SessionID     string                `json:"sessionId"`
	ParentSession string                `json:"parentSessionId,omitempty"`
	Modes         *sessionModeState     `json:"modes,omitempty"`
	ConfigOptions []configOption        `json:"configOptions,omitempty"`
	Execution     *executionState       `json:"execution,omitempty"`
	History       *transcriptPageResult `json:"history,omitempty"`
}

type transcriptPageResult struct {
	SessionID  string          `json:"sessionId"`
	Updates    []sessionUpdate `json:"updates"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type executionState struct {
	SessionID      string `json:"sessionId"`
	State          string `json:"state"`
	Running        bool   `json:"running"`
	Busy           bool   `json:"busy"`
	CanSubmit      bool   `json:"canSubmit"`
	CanCancelLocal bool   `json:"canCancelLocal"`
	Source         string `json:"source"`
}

type draftConfigResult struct {
	ConfigOptions []configOption `json:"configOptions"`
}

type listedSession struct {
	SessionID             string      `json:"sessionId"`
	Cwd                   string      `json:"cwd"`
	AdditionalDirectories []string    `json:"additionalDirectories,omitempty"`
	Title                 string      `json:"title,omitempty"`
	Provider              string      `json:"provider,omitempty"`
	Model                 string      `json:"model,omitempty"`
	Mode                  string      `json:"mode,omitempty"`
	ThoughtLevel          string      `json:"thoughtLevel,omitempty"`
	ParentSessionID       string      `json:"parentSessionId,omitempty"`
	UpdatedAt             string      `json:"updatedAt,omitempty"`
	Meta                  sessionMeta `json:"_meta,omitempty"`
}

type sessionMeta struct {
	MessageCount int             `json:"messageCount,omitempty"`
	Pinned       bool            `json:"pinned,omitempty"`
	ProjectID    any             `json:"projectId,omitempty"`
	LastRun      *sessionLastRun `json:"lastRun,omitempty"`
}

type sessionLastRun struct {
	Active bool   `json:"active,omitempty"`
	Status string `json:"status,omitempty"`
}

type sessionListResult struct {
	Sessions   []listedSession `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	Name     string `json:"name,omitempty"`
	Title    string `json:"title,omitempty"`
	URI      string `json:"uri,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

type sessionUpdate struct {
	SessionUpdate string                     `json:"sessionUpdate"`
	MessageID     string                     `json:"messageId,omitempty"`
	ConfigID      string                     `json:"configId,omitempty"`
	Value         string                     `json:"value,omitempty"`
	ConfigOptions []configOption             `json:"configOptions,omitempty"`
	CurrentModeID string                     `json:"currentModeId,omitempty"`
	Content       json.RawMessage            `json:"content,omitempty"`
	ToolCallID    string                     `json:"toolCallId,omitempty"`
	Locations     []toolLocation             `json:"locations,omitempty"`
	Title         string                     `json:"title,omitempty"`
	Kind          string                     `json:"kind,omitempty"`
	Status        string                     `json:"status,omitempty"`
	RawInput      map[string]any             `json:"rawInput,omitempty"`
	RawOutput     map[string]any             `json:"rawOutput,omitempty"`
	Used          int                        `json:"used,omitempty"`
	Size          int                        `json:"size,omitempty"`
	Cost          *usageCost                 `json:"cost,omitempty"`
	Entries       []planEntry                `json:"entries,omitempty"`
	Commands      []command                  `json:"availableCommands,omitempty"`
	UpdatedAt     string                     `json:"updatedAt,omitempty"`
	ArtifactID    string                     `json:"artifactId,omitempty"`
	Filename      string                     `json:"filename,omitempty"`
	MediaType     string                     `json:"mediaType,omitempty"`
	RunID         string                     `json:"runId,omitempty"`
	Meta          map[string]json.RawMessage `json:"_meta,omitempty"`
}

type toolLocation struct {
	Path string `json:"path"`
}

type usageCost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type planEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type command struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Meta        map[string]json.RawMessage `json:"_meta,omitempty"`
}

type sessionUpdateNotification struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

type sessionEvent struct {
	SessionID  string         `json:"sessionId"`
	Event      string         `json:"event"`
	Status     string         `json:"status,omitempty"`
	Message    string         `json:"message,omitempty"`
	Error      string         `json:"error,omitempty"`
	RunID      string         `json:"runId,omitempty"`
	JobID      string         `json:"jobId,omitempty"`
	RequestID  string         `json:"requestId,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Deadline   string         `json:"deadline,omitempty"`
	RetryAfter *int           `json:"retryAfterMs,omitempty"`
	Remaining  *int           `json:"remainingMs,omitempty"`
	Attempt    *int           `json:"attempt,omitempty"`
	ErrorInfo  map[string]any `json:"errorInfo,omitempty"`
}

type permissionToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Status     string         `json:"status,omitempty"`
	RawInput   map[string]any `json:"rawInput,omitempty"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  permissionToolCall `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type questionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type questionParams struct {
	SessionID   string          `json:"sessionId,omitempty"`
	Prompt      string          `json:"prompt,omitempty"`
	Question    string          `json:"question,omitempty"`
	Options     json.RawMessage `json:"options,omitempty"`
	Explanation string          `json:"explanation,omitempty"`
	Multi       bool            `json:"multi,omitempty"`
	Title       string          `json:"title,omitempty"`
	Placeholder string          `json:"placeholder,omitempty"`
	TimeoutMs   int             `json:"timeoutMs,omitempty"`
}

type elicitationParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Message   string `json:"message,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Schema    struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type        string   `json:"type"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	} `json:"requestedSchema"`
}

// applicationSettingsDefaults is the safe, non-secret subset of
// mothx/manage/application/get that the native client is allowed to render.
type applicationSettingsDefaults struct {
	DefaultMode       string `json:"defaultMode"`
	EnablePlanTool    bool   `json:"enablePlanTool"`
	EnableArtifact    bool   `json:"enableArtifact"`
	EnableACPArtifact bool   `json:"enableACPArtifact"`
	Authored          bool   `json:"authored"`
	UpdateCheck       bool   `json:"updateCheck"`
}

type applicationContextFilesView struct {
	Enabled    bool     `json:"enabled"`
	ExtraFiles []string `json:"extraFiles"`
}

type applicationCompactionView struct {
	Enabled          bool   `json:"enabled"`
	ReserveTokens    int    `json:"reserveTokens"`
	KeepRecentTokens int    `json:"keepRecentTokens"`
	Tokenizer        string `json:"tokenizer"`
	TokenizerModel   string `json:"tokenizerModel"`
	Template         string `json:"template"`
}

type applicationToolExecutionView struct {
	Mode           string `json:"mode"`
	MaxConcurrency int    `json:"maxConcurrency"`
}

type applicationWebSearchView struct {
	Enabled         bool   `json:"enabled"`
	Backend         string `json:"backend"`
	Provider        string `json:"provider"`
	BaseURL         string `json:"baseUrl"`
	TokenConfigured bool   `json:"tokenConfigured"`
}

type applicationImageGenerationView struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	APIType         string `json:"apiType"`
	BaseURL         string `json:"baseUrl"`
	Model           string `json:"model"`
	TokenConfigured bool   `json:"tokenConfigured"`
}

type applicationRetryView struct {
	Enabled     bool `json:"enabled"`
	MaxRetries  int  `json:"maxRetries"`
	BaseDelayMs int  `json:"baseDelayMs"`
}

type applicationStatusLineView struct {
	Enabled         bool   `json:"enabled"`
	Type            string `json:"type"`
	Command         string `json:"command"`
	Padding         int    `json:"padding"`
	RefreshInterval int    `json:"refreshInterval"`
	TimeoutMs       int    `json:"timeoutMs"`
	Fallback        string `json:"fallback"`
}

type applicationSandboxView struct {
	Enabled      bool     `json:"enabled"`
	Level        string   `json:"level"`
	BwrapPath    string   `json:"bwrapPath"`
	AllowNetwork bool     `json:"allowNetwork"`
	AllowedRead  []string `json:"allowedRead"`
	AllowedWrite []string `json:"allowedWrite"`
	DeniedPaths  []string `json:"deniedPaths"`
	TmpSize      string   `json:"tmpSize"`
	ProtectGit   bool     `json:"protectGit"`
}

type applicationApprovalView struct {
	BashWhitelist      []string `json:"bashWhitelist"`
	BashBlacklist      []string `json:"bashBlacklist"`
	ConfirmBeforeWrite bool     `json:"confirmBeforeWrite"`
}

// applicationSettingsView mirrors the ACP application settings projection.
// Tokens and credentials are never echoed; only a configured/not-configured
// hint is decoded so the UI can show the right placeholder.
type applicationSettingsView struct {
	Defaults        applicationSettingsDefaults    `json:"defaults"`
	ContextFiles    applicationContextFilesView    `json:"contextFiles"`
	Compaction      applicationCompactionView      `json:"compaction"`
	ToolExecution   applicationToolExecutionView   `json:"toolExecution"`
	WebSearch       applicationWebSearchView       `json:"webSearch"`
	ImageGeneration applicationImageGenerationView `json:"imageGeneration"`
	Retry           applicationRetryView           `json:"retry"`
	StatusLine      applicationStatusLineView      `json:"statusLine"`
	Sandbox         applicationSandboxView         `json:"sandbox"`
	Approval        applicationApprovalView        `json:"approval"`
}
