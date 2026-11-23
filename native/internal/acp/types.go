package acp

import "encoding/json"

// ACP v1 payloads used by the native client. The wire types mirror
// internal/acp/acp.go and stay deliberately additive.

type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int                        `json:"protocolVersion"`
	AgentInfo         ClientInfo                 `json:"agentInfo"`
	AgentCapabilities json.RawMessage            `json:"agentCapabilities"`
	Meta              map[string]json.RawMessage `json:"_meta"`
}

type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []SessionMode `json:"availableModes"`
}

type ConfigChoice struct {
	Value         string `json:"value"`
	Name          string `json:"name"`
	NameZh        string `json:"nameZh,omitempty"`
	NameEn        string `json:"nameEn,omitempty"`
	Description   string `json:"description,omitempty"`
	DescriptionZh string `json:"descriptionZh,omitempty"`
	DescriptionEn string `json:"descriptionEn,omitempty"`
}

type ConfigOption struct {
	Type         string         `json:"type"`
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Category     string         `json:"category,omitempty"`
	CurrentValue string         `json:"currentValue"`
	Options      []ConfigChoice `json:"options,omitempty"`
}

type SessionResult struct {
	SessionID     string                `json:"sessionId"`
	ParentSession string                `json:"parentSessionId,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []ConfigOption        `json:"configOptions,omitempty"`
	Execution     *ExecutionState       `json:"execution,omitempty"`
	History       *TranscriptPageResult `json:"history,omitempty"`
}

type TranscriptPageResult struct {
	SessionID  string          `json:"sessionId"`
	Updates    []SessionUpdate `json:"updates"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type ExecutionState struct {
	SessionID       string `json:"sessionId"`
	State           string `json:"state"`
	Running         bool   `json:"running"`
	Busy            bool   `json:"busy"`
	CanSubmit       bool   `json:"canSubmit"`
	CanCancelLocal  bool   `json:"canCancelLocal"`
	CanCancelRemote bool   `json:"canCancelRemote"`
	OwnerScope      string `json:"ownerScope,omitempty"`
	Source          string `json:"source"`
}

type DraftConfigResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

type ListedSession struct {
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
	Meta                  SessionMeta `json:"_meta,omitempty"`
}

type SessionMeta struct {
	MessageCount int             `json:"messageCount,omitempty"`
	Pinned       bool            `json:"pinned,omitempty"`
	ProjectID    any             `json:"projectId,omitempty"`
	LastRun      *SessionLastRun `json:"lastRun,omitempty"`
	Execution    *ExecutionState `json:"execution,omitempty"`
}

type SessionLastRun struct {
	Active bool   `json:"active,omitempty"`
	Status string `json:"status,omitempty"`
}

type SessionListResult struct {
	Sessions   []ListedSession `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	Name     string `json:"name,omitempty"`
	Title    string `json:"title,omitempty"`
	URI      string `json:"uri,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type SessionUpdate struct {
	SessionUpdate string                     `json:"SessionUpdate"`
	MessageID     string                     `json:"messageId,omitempty"`
	ConfigID      string                     `json:"configId,omitempty"`
	Value         string                     `json:"value,omitempty"`
	ConfigOptions []ConfigOption             `json:"configOptions,omitempty"`
	CurrentModeID string                     `json:"currentModeId,omitempty"`
	Content       json.RawMessage            `json:"content,omitempty"`
	ToolCallID    string                     `json:"toolCallId,omitempty"`
	Locations     []ToolLocation             `json:"locations,omitempty"`
	Title         string                     `json:"title,omitempty"`
	Kind          string                     `json:"kind,omitempty"`
	Status        string                     `json:"status,omitempty"`
	RawInput      map[string]any             `json:"rawInput,omitempty"`
	RawOutput     map[string]any             `json:"rawOutput,omitempty"`
	Used          int                        `json:"used,omitempty"`
	Size          int                        `json:"size,omitempty"`
	Cost          *UsageCost                 `json:"cost,omitempty"`
	Entries       []PlanEntry                `json:"entries,omitempty"`
	Commands      []Command                  `json:"availableCommands,omitempty"`
	UpdatedAt     string                     `json:"updatedAt,omitempty"`
	ArtifactID    string                     `json:"artifactId,omitempty"`
	Filename      string                     `json:"filename,omitempty"`
	MediaType     string                     `json:"mediaType,omitempty"`
	RunID         string                     `json:"runId,omitempty"`
	Meta          map[string]json.RawMessage `json:"_meta,omitempty"`
}

type ToolLocation struct {
	Path string `json:"path"`
}

type UsageCost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type Command struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	Meta        map[string]json.RawMessage `json:"_meta,omitempty"`
}

type SessionUpdateNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

type SessionEvent struct {
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

	// Subagent lifecycle projection (_mothx/session_event event=subagent).
	// The Runtime owns child identity and status; these fields only project
	// the canonical event into the transcript card and the session roster.
	AgentID           string `json:"agentId,omitempty"`
	ParentAgentID     string `json:"parentAgentId,omitempty"`
	MemberID          string `json:"memberId,omitempty"`
	ExpertID          string `json:"expertId,omitempty"`
	MemberDisplayName string `json:"memberDisplayName,omitempty"`
	MemberEmoji       string `json:"memberEmoji,omitempty"`
	MemberRole        string `json:"memberRole,omitempty"`
}

type PermissionToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Status     string         `json:"status,omitempty"`
	RawInput   map[string]any `json:"rawInput,omitempty"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type PermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type QuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type QuestionParams struct {
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

type ElicitationParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Message   string `json:"message,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Schema    struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			// Enum entries may be plain strings or {id,label} objects: the
			// Runtime's form projection embeds question options as objects.
			Enum []json.RawMessage `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	} `json:"requestedSchema"`
}

// ApplicationSettingsDefaults is the safe, non-secret subset of
// mothx/manage/application/get that the native client is allowed to render.
type ApplicationSettingsDefaults struct {
	DefaultMode       string `json:"defaultMode"`
	EnablePlanTool    bool   `json:"enablePlanTool"`
	EnableArtifact    bool   `json:"enableArtifact"`
	EnableACPArtifact bool   `json:"enableACPArtifact"`
	Authored          bool   `json:"authored"`
	UpdateCheck       bool   `json:"updateCheck"`
}

type ApplicationContextFilesView struct {
	Enabled    bool     `json:"enabled"`
	ExtraFiles []string `json:"extraFiles"`
}

type ApplicationCompactionView struct {
	Enabled          bool   `json:"enabled"`
	ReserveTokens    int    `json:"reserveTokens"`
	KeepRecentTokens int    `json:"keepRecentTokens"`
	Tokenizer        string `json:"tokenizer"`
	TokenizerModel   string `json:"tokenizerModel"`
	Template         string `json:"template"`
}

type ApplicationToolExecutionView struct {
	Mode           string `json:"mode"`
	MaxConcurrency int    `json:"maxConcurrency"`
}

type ApplicationWebSearchView struct {
	Enabled         bool   `json:"enabled"`
	Backend         string `json:"backend"`
	Provider        string `json:"provider"`
	BaseURL         string `json:"baseUrl"`
	TokenConfigured bool   `json:"tokenConfigured"`
}

type ApplicationImageGenerationView struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	APIType         string `json:"apiType"`
	BaseURL         string `json:"baseUrl"`
	Model           string `json:"model"`
	TokenConfigured bool   `json:"tokenConfigured"`
}

type ApplicationRetryView struct {
	Enabled     bool `json:"enabled"`
	MaxRetries  int  `json:"maxRetries"`
	BaseDelayMs int  `json:"baseDelayMs"`
}

type ApplicationStatusLineView struct {
	Enabled         bool   `json:"enabled"`
	Type            string `json:"type"`
	Command         string `json:"Command"`
	Padding         int    `json:"padding"`
	RefreshInterval int    `json:"refreshInterval"`
	TimeoutMs       int    `json:"timeoutMs"`
	Fallback        string `json:"fallback"`
}

type ApplicationSandboxView struct {
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

type ApplicationApprovalView struct {
	BashWhitelist      []string `json:"bashWhitelist"`
	BashBlacklist      []string `json:"bashBlacklist"`
	ConfirmBeforeWrite bool     `json:"confirmBeforeWrite"`
}

// ApplicationSettingsView mirrors the ACP application settings projection.
// Tokens and credentials are never echoed; only a configured/not-configured
// hint is decoded so the UI can show the right placeholder.
type ApplicationSettingsView struct {
	Defaults        ApplicationSettingsDefaults    `json:"defaults"`
	ContextFiles    ApplicationContextFilesView    `json:"contextFiles"`
	Compaction      ApplicationCompactionView      `json:"compaction"`
	ToolExecution   ApplicationToolExecutionView   `json:"toolExecution"`
	WebSearch       ApplicationWebSearchView       `json:"webSearch"`
	ImageGeneration ApplicationImageGenerationView `json:"imageGeneration"`
	Retry           ApplicationRetryView           `json:"retry"`
	StatusLine      ApplicationStatusLineView      `json:"statusLine"`
	Sandbox         ApplicationSandboxView         `json:"sandbox"`
	Approval        ApplicationApprovalView        `json:"approval"`
}
