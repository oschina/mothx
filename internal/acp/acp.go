package acp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/dao"
	"github.com/startvibecoding/mothx/internal/debugpprof"
	"github.com/startvibecoding/mothx/internal/doctor"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/mcp"
	"github.com/startvibecoding/mothx/internal/provider"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	"github.com/startvibecoding/mothx/internal/sandbox"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/systeminit"
	"github.com/startvibecoding/mothx/internal/tools"
	appversion "github.com/startvibecoding/mothx/internal/version"
	"github.com/startvibecoding/mothx/internal/workflow"
)

const protocolVersion = 1
const maxRequestBytes = 10 << 20

var errEmptyMessage = errors.New("empty message")

const mothxExtensionNamespace = "mothx.dev"

// esmSteeringMessages adapts the shared, persisted ESM objective into an ACP
// prompt run. It owns no state and does not schedule work; the source only
// emits a changed objective at an Agent loop steering boundary.
func esmSteeringMessages(settings *config.Settings, sessionID string) func() []provider.Message {
	if settings == nil || settings.GetSessionDir() == "" || sessionID == "" {
		return nil
	}
	return esm.NewSteeringSource(esm.NewStore(settings.GetSessionDir()), sessionID).Next
}

// Decision deadline defaults. RunOptions/CLI flags/env may extend them; the
// zero value always falls back to these documented ACP defaults.
// defaultPermissionTimeout bounds how long an ACP approval decision may stay
// pending. It is aligned with the question deadline: reading a risky command
// routinely takes longer than a few seconds, and a short deadline denies the
// tool call while the user is still deciding.
const (
	defaultPermissionTimeout = 5 * time.Minute
	defaultQuestionTimeout   = 5 * time.Minute
)

type RunOptions struct {
	Version    string
	Provider   string
	Model      string
	Mode       string
	Thinking   string
	Sandbox    bool
	Verbose    bool
	Debug      bool
	MultiAgent bool
	Delegate   bool
	Workflows  bool
	WebSearch  bool
	Browser    bool
	Artifact   bool
	// PermissionTimeout and QuestionTimeout configure the approval and
	// question decision deadlines (Go duration, injected from CLI flags or
	// MOTHX_ACP_PERMISSION_TIMEOUT / MOTHX_ACP_QUESTION_TIMEOUT). Zero values
	// fall back to defaultPermissionTimeout / defaultQuestionTimeout.
	PermissionTimeout time.Duration
	QuestionTimeout   time.Duration
}

type startupError struct {
	Code    string
	Message string
	Fix     string
	Cause   error
}

func (e *startupError) Error() string {
	if e == nil {
		return "ACP startup failed"
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Message
}

func (e *startupError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type server struct {
	mu  sync.Mutex
	wmu sync.Mutex

	settings *config.Settings
	allow    *config.AllowConfig
	cwd      string
	version  string

	p            provider.Provider
	providerName string
	m            *provider.Model
	providers    map[string]provider.Provider

	mode          string
	thinkingLevel provider.ThinkingLevel
	sbMgr         *sandbox.Manager
	skillsMgr     *skills.Manager
	extraContext  string
	ruleContent   string
	contextFiles  string

	multiAgent       bool
	delegate         bool
	workflows        bool
	browser          bool
	artifact         bool
	artifactOverride bool
	runtime          *agentruntime.SessionRuntime
	agentMgr         *agent.AgentManager

	sessions map[string]*sessionRuntime
	pending  map[string]chan json.RawMessage

	toolTitles  map[string]string
	mcpNotify   map[string]bool
	initialized bool
	clientCaps  clientCapabilities

	// subagents tracks which child-agent lifecycle events were already
	// projected per session so exactly one started and one terminal subagent
	// event are emitted per agent ID. Keyed by sessionID\x00agentID.
	subagents map[string]*subagentProjection

	// workspaceCwd and workspaceAdditionalDirectories are negotiated during
	// initialize and define the only roots this ACP process may expose. They
	// remain empty for direct/unit fixtures that do not negotiate workspace
	// metadata.
	workspaceCwd                   string
	workspaceAdditionalDirectories []string

	nextID int64
	r      *bufio.Reader
	w      io.Writer

	permissionTimeout time.Duration
	questionTimeout   time.Duration

	// cronMu guards the Phase 3 management-plane cron runtime: the shared
	// SQLite store, the in-process scheduler started idempotently on the first
	// mothx/manage/cron/* call, and the canonically constructed agent manager
	// that backs transient cron job runs.
	cronMu        sync.Mutex
	cronScheduler *cron.Scheduler
	cronStore     cron.CronStore
	cronAgentMgr  *agent.AgentManager
}

type sessionRuntime struct {
	runtime   *agentruntime.SessionRuntime
	execution *agentruntime.ExecutionRuntime
	decisions *agentruntime.DecisionService
	id        string
	mgr       *session.Manager
	agent     agentpkg.Agent
	registry  *tools.Registry
	cancel    context.CancelFunc
	promptID  string
	// runID is the canonical durable run identity. promptID remains the ACP
	// request key used by $/cancel_request and must not be conflated with it.
	runID            string
	closed           bool
	terminalNotified bool
	cancelMu         sync.Mutex
	// ACP message IDs group streamed chunks into logical messages. A single
	// prompt can contain several model turns when tools are used, so IDs must
	// advance at each Agent turn. Otherwise text emitted after a tool is merged
	// into the pre-tool message by ACP clients and is rendered before the tool
	// execution that produced it.
	messageID        string
	thoughtMessageID string
	userMessageID    string
	streamSegment    int
	activeModel      *provider.Model
	activeMode       string
	activeThinking   provider.ThinkingLevel
	activeSkills     map[string]bool
	mcp              []*mcp.Client
	agentMgr         *agent.AgentManager

	usageMu sync.Mutex
	cost    float64
}

func (s *server) artifactEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.artifact
}

// applyACPArtifactSetting updates the ACP/Desktop policy for subsequent runs
// without moving the setting into Electron-owned presentation storage. A CLI
// override remains authoritative for the lifetime of this ACP process.
func (s *server) applyACPArtifactSetting(enabled bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.settings != nil {
		s.settings.EnableACPArtifact = config.BoolPtr(enabled)
	}
	s.artifact = enabled || s.artifactOverride
	effective := s.artifact
	runtimes := make([]*agentruntime.SessionRuntime, 0, len(s.sessions)+1)
	if s.runtime != nil {
		runtimes = append(runtimes, s.runtime)
	}
	for _, session := range s.sessions {
		if session != nil && session.runtime != nil {
			runtimes = append(runtimes, session.runtime)
		}
	}
	s.mu.Unlock()
	for _, runtime := range runtimes {
		_ = runtime.SetArtifactEnabled(effective)
	}
}

// sessionProviderMismatchError is returned only when a persisted session
// points at a provider that this ACP process cannot construct or select.
type sessionProviderMismatchError struct {
	SessionProvider string
	SessionModel    string
	CurrentProvider string
	Cause           error
}

func (e *sessionProviderMismatchError) Error() string {
	if e == nil {
		return "session provider mismatch"
	}
	if e.Cause != nil {
		return fmt.Sprintf("session provider %q could not be selected: %v", e.SessionProvider, e.Cause)
	}
	return "session provider mismatch"
}

var errACPActiveSessionRun = errors.New("session already has an active run")

// acquirePromptAdmission serializes ACP with the other local entry points and
// checks the durable row before attempting the unique active-run insert. The
// database check is still needed for a run owned by another process; the
// shared runtime lock covers concurrent local adapters and prompt requests.
func (s *server) acquirePromptAdmission(rt *sessionRuntime) (func(), error) {
	if s == nil || s.settings == nil || rt == nil || strings.TrimSpace(rt.id) == "" {
		return nil, fmt.Errorf("ACP session runtime is unavailable")
	}
	guard, err := agentruntime.AcquireExecutionAdmission(context.Background(), s.settings.GetSessionDir(), rt.id, agentruntime.ExecutionAdmissionOptions{})
	if err != nil {
		return nil, errACPActiveSessionRun
	}
	rt.cancelMu.Lock()
	localActive := rt.cancel != nil
	rt.cancelMu.Unlock()
	if localActive {
		guard.Release()
		return nil, errACPActiveSessionRun
	}
	return guard.Release, nil
}

// closeResources releases the shared runtime resources. The legacy MCP slice
// remains as a compatibility alias for ACP session fixtures during migration.
func (r *sessionRuntime) closeResources() {
	if r == nil {
		return
	}
	if r.runtime != nil {
		r.runtime.Close()
		r.mcp = nil
		return
	}
	agentruntime.CloseMCPClients(r.mcp)
	r.mcp = nil
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcp.RPCError   `json:"error,omitempty"`
}

type clientInfo struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type initializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         clientInfo         `json:"clientInfo,omitempty"`
	Meta               requestMeta        `json:"_meta,omitempty"`
}

// workspaceSpec is the protocol-neutral workspace window exchanged in
// _meta.mothx. It is deliberately kept separate from session additional
// directories: cwd is the session's primary root, while the latter are extra
// roots granted to that session.
type workspaceSpec struct {
	Cwd                   string   `json:"cwd,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

type mothxRequestMeta struct {
	Workspace       *workspaceSpec `json:"workspace,omitempty"`
	ParentSessionID string         `json:"parentSessionId,omitempty"`
	Surface         string         `json:"surface,omitempty"`
	EditorContext   *editorContext `json:"editorContext,omitempty"`
}

type editorContext struct {
	URI         string             `json:"uri,omitempty"`
	Path        string             `json:"path,omitempty"`
	Language    string             `json:"language,omitempty"`
	Selection   *editorSelection   `json:"selection,omitempty"`
	Diagnostics []editorDiagnostic `json:"diagnostics,omitempty"`
}

type editorSelection struct {
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Text      string `json:"text,omitempty"`
}

type editorDiagnostic struct {
	Severity  string `json:"severity,omitempty"`
	Line      int    `json:"line,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Message   string `json:"message,omitempty"`
}

// requestMeta accepts both namespaces used by ACP clients. Unknown metadata
// is ignored by design so clients can roll out optional fields independently.
type requestMeta struct {
	Mothx    *mothxRequestMeta `json:"mothx,omitempty"`
	MothxDev *mothxRequestMeta `json:"mothx.dev,omitempty"`
}

func (m requestMeta) workspace() *workspaceSpec {
	if m.Mothx != nil && m.Mothx.Workspace != nil {
		return m.Mothx.Workspace
	}
	if m.MothxDev != nil && m.MothxDev.Workspace != nil {
		return m.MothxDev.Workspace
	}
	return nil
}

func (m requestMeta) parentSessionID() string {
	if m.Mothx != nil && strings.TrimSpace(m.Mothx.ParentSessionID) != "" {
		return m.Mothx.ParentSessionID
	}
	if m.MothxDev != nil {
		return m.MothxDev.ParentSessionID
	}
	return ""
}

func (m requestMeta) editorContext() *editorContext {
	if m.Mothx != nil && m.Mothx.EditorContext != nil {
		return m.Mothx.EditorContext
	}
	if m.MothxDev != nil {
		return m.MothxDev.EditorContext
	}
	return nil
}

func (m requestMeta) surface() string {
	if m.Mothx != nil && strings.TrimSpace(m.Mothx.Surface) != "" {
		return strings.TrimSpace(m.Mothx.Surface)
	}
	if m.MothxDev != nil {
		return strings.TrimSpace(m.MothxDev.Surface)
	}
	return ""
}

// clientCapabilities is deliberately typed even where the current Runtime
// does not invoke client-owned reverse requests. This keeps capability
// negotiation truthful and gives future fs/terminal/elicitation bridges one
// shared state model instead of ACP-local ad hoc maps.
type clientCapabilities struct {
	FS          *clientFSCapabilities          `json:"fs,omitempty"`
	Terminal    bool                           `json:"terminal,omitempty"`
	Auth        *clientAuthCapabilities        `json:"auth,omitempty"`
	Elicitation *clientElicitationCapabilities `json:"elicitation,omitempty"`
	Session     *clientSessionCapabilities     `json:"session,omitempty"`
}

type clientFSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

type clientAuthCapabilities struct {
	Terminal bool `json:"terminal,omitempty"`
}

type clientElicitationCapabilities struct {
	Form *struct{} `json:"form,omitempty"`
	URL  *struct{} `json:"url,omitempty"`
}

type clientSessionCapabilities struct {
	ConfigOptions *clientConfigOptionCapabilities `json:"configOptions,omitempty"`
}

type clientConfigOptionCapabilities struct {
	Boolean *struct{} `json:"boolean,omitempty"`
}

type initializeResult struct {
	ProtocolVersion   int            `json:"protocolVersion"`
	AgentCapabilities agentCaps      `json:"agentCapabilities"`
	AgentInfo         clientInfo     `json:"agentInfo"`
	AuthMethods       []authMethod   `json:"authMethods"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

type authMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type agentCaps struct {
	LoadSession         bool           `json:"loadSession"`
	PromptCapabilities  promptCaps     `json:"promptCapabilities"`
	SessionCapabilities sessionCaps    `json:"sessionCapabilities"`
	McPCapabilities     mcpCaps        `json:"mcpCapabilities"`
	Meta                map[string]any `json:"_meta,omitempty"`
}

type mcpCaps struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

type promptCaps struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type sessionCaps struct {
	// session/new, session/prompt, session/cancel, and session/update are
	// required ACP v1 baseline methods, so they are not capability flags.
	Close                 *struct{} `json:"close,omitempty"`
	Delete                *struct{} `json:"delete,omitempty"`
	List                  *struct{} `json:"list,omitempty"`
	Resume                *struct{} `json:"resume,omitempty"`
	Fork                  *struct{} `json:"fork,omitempty"`
	AdditionalDirectories *struct{} `json:"additionalDirectories,omitempty"`
}

type doctorRequest struct {
	Cwd string `json:"cwd,omitempty"`
}

type newSessionRequest struct {
	Cwd                   string             `json:"cwd"`
	AdditionalDirectories []string           `json:"additionalDirectories,omitempty"`
	McpServers            []mcp.ServerConfig `json:"mcpServers,omitempty"`
	Meta                  requestMeta        `json:"_meta,omitempty"`
}

// draftConfigOptionsRequest is an additive, pre-session projection. It has no
// persistence side effect: Desktop uses it to let a user choose the same
// Runtime-owned initial options before sending session/new.
type draftConfigOptionsRequest struct {
	Cwd  string      `json:"cwd"`
	Meta requestMeta `json:"_meta,omitempty"`
}

type newSessionResult struct {
	SessionID       string                             `json:"sessionId"`
	ParentSessionID string                             `json:"parentSessionId,omitempty"`
	Modes           *sessionModeState                  `json:"modes,omitempty"`
	ConfigOptions   []agentruntime.SessionConfigOption `json:"configOptions,omitempty"`
	History         *transcriptPageResult              `json:"history,omitempty"`
}

type transcriptPageRequest struct {
	SessionID string `json:"sessionId"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// transcriptPageResult is an additive ACP projection of canonical session
// messages. Updates use the normal session/update shape so clients do not
// gain a second transcript or content model.
type transcriptPageResult struct {
	SessionID  string          `json:"sessionId"`
	Updates    []sessionUpdate `json:"updates"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type sessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []sessionMode `json:"availableModes"`
}

type sessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func sessionModes(runtime *agentruntime.SessionRuntime) *sessionModeState {
	if runtime == nil {
		return nil
	}
	_, _, _, mode, _ := runtime.ConfigSnapshot()
	return &sessionModeState{
		CurrentModeID: mode,
		AvailableModes: []sessionMode{
			{ID: agentruntime.ModeAgent, Name: "Agent"},
			{ID: agentruntime.ModePlan, Name: "Plan"},
			{ID: agentruntime.ModeYolo, Name: "Yolo"},
			{ID: agentruntime.ModeOS, Name: "OS"},
		},
	}
}

type setConfigOptionRequest struct {
	SessionID string          `json:"sessionId"`
	ConfigID  string          `json:"configId"`
	Type      string          `json:"type,omitempty"`
	Value     json.RawMessage `json:"value"`
	Meta      requestMeta     `json:"_meta,omitempty"`
}

type setModeRequest struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId,omitempty"`
	// Mode is accepted as a compatibility alias for older ACP clients.
	Mode string `json:"mode,omitempty"`
}

type loadSessionRequest struct {
	SessionID             string             `json:"sessionId"`
	Cwd                   string             `json:"cwd"`
	AdditionalDirectories []string           `json:"additionalDirectories,omitempty"`
	McpServers            []mcp.ServerConfig `json:"mcpServers,omitempty"`
	// HistoryLimit limits only the initial ACP transcript projection. The
	// shared SessionRuntime continues to own its complete replay for execution.
	HistoryLimit int         `json:"historyLimit,omitempty"`
	Meta         requestMeta `json:"_meta,omitempty"`
}

type resumeSessionRequest struct {
	SessionID             string             `json:"sessionId"`
	Cwd                   string             `json:"cwd"`
	AdditionalDirectories []string           `json:"additionalDirectories,omitempty"`
	McpServers            []mcp.ServerConfig `json:"mcpServers,omitempty"`
	Meta                  requestMeta        `json:"_meta,omitempty"`
}

type forkSessionRequest struct {
	SessionID             string             `json:"sessionId"`
	Cwd                   string             `json:"cwd"`
	AdditionalDirectories []string           `json:"additionalDirectories,omitempty"`
	McpServers            []mcp.ServerConfig `json:"mcpServers,omitempty"`
	AtSeq                 *int64             `json:"atSeq,omitempty"`
	RequestID             string             `json:"requestId,omitempty"`
	TitleMode             string             `json:"titleMode,omitempty"`
	// ExpertID is a MothX additive fork option. A non-nil empty value creates
	// an unbound child; an omitted value preserves the parent binding.
	ExpertID *string     `json:"expertId,omitempty"`
	Meta     requestMeta `json:"_meta,omitempty"`
}

type promptRequest struct {
	SessionID         string                                `json:"sessionId"`
	Prompt            []contentBlock                        `json:"prompt"`
	KnowledgeBaseRefs []agentruntime.KnowledgeBaseReference `json:"knowledgeBaseRefs,omitempty"`
	Meta              requestMeta                           `json:"_meta,omitempty"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

type cancelRequest struct {
	SessionID string `json:"sessionId"`
}

type closeSessionRequest struct {
	SessionID string      `json:"sessionId"`
	Meta      requestMeta `json:"_meta,omitempty"`
}

type deleteSessionRequest struct {
	SessionID string      `json:"sessionId"`
	Meta      requestMeta `json:"_meta,omitempty"`
}

type setTitleRequest struct {
	SessionID string      `json:"sessionId"`
	Title     string      `json:"title"`
	Meta      requestMeta `json:"_meta,omitempty"`
}

// setWorkDirRequest is deliberately an ACP extension: standard ACP treats
// cwd as part of session setup, while MothX lets a persisted local session be
// moved between projects when it is idle.
type setWorkDirRequest struct {
	SessionID string      `json:"sessionId"`
	Cwd       string      `json:"cwd"`
	Meta      requestMeta `json:"_meta,omitempty"`
}

type setWorkDirResult struct {
	Cwd string `json:"cwd"`
}

type attachmentFetchRequest struct {
	SessionID    string `json:"sessionId"`
	AttachmentID string `json:"attachmentId"`
}

type attachmentFetchResult struct {
	Filename      string `json:"filename"`
	MediaType     string `json:"mediaType"`
	Size          int64  `json:"size"`
	ContentBase64 string `json:"contentBase64"`
}

type cancelRequestNotification struct {
	RequestID json.RawMessage `json:"requestId"`
}

type listSessionsRequest struct {
	Cwd                   string   `json:"cwd,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
	Cursor                string   `json:"cursor,omitempty"`
	// Scope and ProjectID are accepted by mothx/session/listAll only. They
	// keep the global catalog canonical while allowing adapters to paginate a
	// project branch or the ungrouped branch without materializing the entire
	// history locally.
	//
	// Supported scope values are "all" (the default), "project", and
	// "ungrouped". A non-empty projectId also implies the project scope for
	// backwards-friendly clients that do not send scope explicitly.
	Scope     string      `json:"scope,omitempty"`
	ProjectID string      `json:"projectId,omitempty"`
	Query     string      `json:"query,omitempty"`
	Meta      requestMeta `json:"_meta,omitempty"`
}

type listSessionsResult struct {
	Sessions   []listedSession `json:"sessions"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type listedSession struct {
	SessionID             string         `json:"sessionId"`
	Cwd                   string         `json:"cwd"`
	AdditionalDirectories []string       `json:"additionalDirectories,omitempty"`
	Title                 string         `json:"title,omitempty"`
	Provider              string         `json:"provider"`
	Model                 string         `json:"model"`
	Mode                  string         `json:"mode,omitempty"`
	ThoughtLevel          string         `json:"thoughtLevel,omitempty"`
	ParentSessionID       string         `json:"parentSessionId,omitempty"`
	UpdatedAt             string         `json:"updatedAt,omitempty"`
	Meta                  map[string]any `json:"_meta,omitempty"`
}

type requestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  permissionToolCall `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type contentBlock struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Data        string `json:"data,omitempty"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URI         string `json:"uri,omitempty"`
	Size        *int   `json:"size,omitempty"`
}

type sessionUpdate struct {
	SessionUpdate     string                             `json:"sessionUpdate"`
	MessageID         string                             `json:"messageId,omitempty"`
	ConfigID          string                             `json:"configId,omitempty"`
	Value             string                             `json:"value,omitempty"`
	ConfigOptions     []agentruntime.SessionConfigOption `json:"configOptions,omitempty"`
	CurrentModeID     string                             `json:"currentModeId,omitempty"`
	Content           any                                `json:"content,omitempty"`
	ToolCallID        string                             `json:"toolCallId,omitempty"`
	Locations         []toolCallLocation                 `json:"locations,omitempty"`
	Title             string                             `json:"title,omitempty"`
	Kind              string                             `json:"kind,omitempty"`
	Status            string                             `json:"status,omitempty"`
	RawInput          map[string]any                     `json:"rawInput,omitempty"`
	RawOutput         map[string]any                     `json:"rawOutput,omitempty"`
	Used              *int                               `json:"used,omitempty"`
	Size              *int                               `json:"size,omitempty"`
	Cost              *usageCost                         `json:"cost,omitempty"`
	Entries           []planEntry                        `json:"entries,omitempty"`
	AvailableCommands []availableCommand                 `json:"availableCommands,omitempty"`
	UpdatedAt         string                             `json:"updatedAt,omitempty"`
	// Additive projection fields for sessionUpdate="artifact". They carry the
	// canonical Runtime attachment identity so clients can render artifact
	// cards and fetch content through mothx/attachment/fetch.
	ArtifactID string         `json:"artifactId,omitempty"`
	Filename   string         `json:"filename,omitempty"`
	MediaType  string         `json:"mediaType,omitempty"`
	RunID      string         `json:"runId,omitempty"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

type toolCallContent struct {
	Type    string        `json:"type"`
	Content *contentBlock `json:"content,omitempty"`
	Path    string        `json:"path,omitempty"`
	OldText *string       `json:"-"`
	NewText string        `json:"-"`
}

// MarshalJSON keeps the ACP ToolCallContent union strict. In particular, a
// newly-created file must encode oldText as JSON null rather than omitting the
// field, while text content must not acquire diff-only fields.
func (c toolCallContent) MarshalJSON() ([]byte, error) {
	switch c.Type {
	case "diff":
		return json.Marshal(struct {
			Type    string  `json:"type"`
			Path    string  `json:"path"`
			OldText *string `json:"oldText"`
			NewText string  `json:"newText"`
		}{Type: c.Type, Path: c.Path, OldText: c.OldText, NewText: c.NewText})
	default:
		return json.Marshal(struct {
			Type    string        `json:"type"`
			Content *contentBlock `json:"content,omitempty"`
		}{Type: c.Type, Content: c.Content})
	}
}

type availableCommand struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Input       any            `json:"input,omitempty"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

type toolCallLocation struct {
	Path string `json:"path"`
}

type planEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

type usageCost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type permissionToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Status     string         `json:"status,omitempty"`
	RawInput   map[string]any `json:"rawInput,omitempty"`
}

type questionRequest struct {
	SessionID   string   `json:"sessionId"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Explanation string   `json:"explanation,omitempty"`
	TimeoutMs   int64    `json:"timeoutMs"`
	// Protocol marks requests persisted for standard ACP elicitation replay.
	// Legacy extension payloads leave it empty so their wire shape remains
	// backwards compatible.
	Protocol string `json:"protocol,omitempty"`
}

type questionResult struct {
	OK        *bool    `json:"ok,omitempty"`
	Cancelled bool     `json:"cancelled,omitempty"`
	Answer    string   `json:"answer,omitempty"`
	Answers   []string `json:"answers,omitempty"`
}

type requestQuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type requestQuestionPayload struct {
	Prompt      string                  `json:"prompt"`
	Options     []requestQuestionOption `json:"options,omitempty"`
	Multi       bool                    `json:"multi"`
	Title       string                  `json:"title,omitempty"`
	Placeholder string                  `json:"placeholder,omitempty"`
}

func requestQuestionPayloadFor(request questionRequest) requestQuestionPayload {
	options := make([]requestQuestionOption, 0, len(request.Options))
	for _, option := range request.Options {
		option = strings.TrimSpace(option)
		if option != "" {
			options = append(options, requestQuestionOption{ID: option, Label: option})
		}
	}
	return requestQuestionPayload{Prompt: request.Question, Options: options, Multi: false, Title: "MothX", Placeholder: request.Explanation}
}

func (s *server) questionProjection(request questionRequest) (string, any) {
	if !acpInitialized(s) {
		// Keep direct replay/unit fixtures compatible with the pre-v1 extension;
		// initialized wire clients receive the documented camelCase method.
		return "_mothx/request_question", request
	}
	return "mothx/requestQuestion", requestQuestionPayloadFor(request)
}

type elicitationCreateRequest struct {
	SessionID       string         `json:"sessionId"`
	Message         string         `json:"message"`
	Mode            string         `json:"mode"`
	RequestedSchema map[string]any `json:"requestedSchema"`
}

type elicitationResult struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

type permissionResult struct {
	Outcome *permissionOutcome `json:"outcome,omitempty"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// Run starts the ACP stdio server.
func Run(opts RunOptions) (runErr error) {
	var srv *server
	defer func() {
		if runErr != nil && !acpInitialized(srv) {
			if !IsStartupError(runErr) {
				runErr = classifyACPStartupError(runErr)
			}
			writeACPStartupError(runErr)
		}
	}()

	config.Verbose = opts.Verbose || opts.Debug
	if opts.Debug {
		_ = os.Setenv("VIBECODING_DEBUG", "1")
		debugpprof.StartForDebug(os.Stderr)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return &startupError{Code: "cwd_invalid", Message: "working directory is unavailable", Fix: "Start mothx from an existing directory", Cause: err}
	}
	if info, statErr := os.Stat(cwd); statErr != nil || !info.IsDir() {
		cause := statErr
		if cause == nil {
			cause = fmt.Errorf("working directory %q is not a directory", cwd)
		}
		return &startupError{Code: "cwd_invalid", Message: "working directory is unavailable", Fix: "Start mothx from an existing directory", Cause: cause}
	}
	runVersion := strings.TrimSpace(opts.Version)
	if runVersion == "" {
		runVersion = appversion.Current()
	}
	preflightSettings, preflightErr := config.LoadSettingsFor(cwd)
	if preflightErr != nil {
		return &startupError{Code: "config_invalid", Message: "settings could not be loaded", Fix: "Fix settings.json syntax", Cause: preflightErr}
	}
	requestedModel, requestedSet := os.LookupEnv("HARBOR_ACP_REQUESTED_MODEL")
	providerName, modelID, err := resolveACPProviderSelection(preflightSettings, opts, requestedModel, requestedSet)
	if err != nil {
		return classifyACPStartupError(err)
	}
	if selection := doctor.ValidateProvider(preflightSettings, providerName, modelID); len(selection) > 0 {
		if err := startupErrorFromDoctor(doctor.Response{Checks: selection}); err != nil {
			return err
		}
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return &startupError{Code: "config_invalid", Message: "settings could not be loaded", Fix: "Fix settings.json syntax", Cause: err}
	}
	// ACP is a long-running Runtime host. Use the same startup and periodic
	// lease-first recovery coordinator as Serve so another owner's crash
	// converges even when this process remains alive and receives no UDP notice.
	recoveryCtx, stopRecovery := context.WithCancel(context.Background())
	recoveryCoordinator := agentruntime.NewRecoveryCoordinator(settings.GetSessionDir(), agentruntime.RecoveryCoordinatorOptions{})
	if err := recoveryCoordinator.Start(recoveryCtx); err != nil {
		stopRecovery()
		stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
		_ = recoveryCoordinator.Stop(stopCtx)
		cancelStop()
		return fmt.Errorf("recover orphaned runs: %w", err)
	}
	defer func() {
		stopRecovery()
		stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Second)
		defer cancelStop()
		_ = recoveryCoordinator.Stop(stopCtx)
	}()
	if opts.WebSearch {
		settings.WebSearch.Enabled = config.BoolPtr(true)
	}

	srv = &server{
		settings:         settings,
		allow:            config.LoadAllow(),
		cwd:              cwd,
		version:          runVersion,
		multiAgent:       opts.MultiAgent,
		delegate:         opts.Delegate,
		workflows:        opts.Workflows,
		browser:          opts.Browser,
		artifact:         opts.Artifact || settings.IsACPArtifactEnabled(),
		artifactOverride: opts.Artifact,
		sessions:         make(map[string]*sessionRuntime),
		pending:          make(map[string]chan json.RawMessage),
		toolTitles:       make(map[string]string),
		mcpNotify:        make(map[string]bool),
		r:                bufio.NewReader(os.Stdin),
		w:                os.Stdout,

		permissionTimeout: opts.PermissionTimeout,
		questionTimeout:   opts.QuestionTimeout,
	}
	// Other Runtime hosts publish only advisory UDP lease-bus wake-ups. ACP
	// re-reads the durable Run before projecting its standard run_status event;
	// it never trusts notification fields as execution state.
	stopLeaseNotifications := session.SubscribeRuntimeLeaseNotifications(func(notification session.RuntimeLeaseNotification) {
		switch notification.Type {
		case "acquired", "released", "lost", "state_changed":
			go srv.notifyExternalRunStatus(notification.SessionID)
		}
	})
	defer stopLeaseNotifications()
	defer srv.shutdownAllSessionRuntimes()
	// Defers run LIFO: stop the management-plane cron scheduler before the
	// session runtimes so in-flight job runs are cancelled first.
	defer srv.stopManageCron()

	p, model, err := createProvider(settings, providerName, modelID)
	if err != nil {
		return classifyACPStartupError(err)
	}
	srv.p = p
	srv.providerName = providerName
	if srv.providerName == "" {
		srv.providerName = settings.DefaultProvider
	}
	srv.m = model
	srv.providers = map[string]provider.Provider{srv.providerName: p}
	// Build the provider catalog once so ACP can advertise all usable configured
	// providers and switch a session without restarting the process. A provider
	// that cannot be constructed is omitted and will produce a structured
	// mismatch error if an old session still references it.
	for name := range settings.Providers {
		if strings.EqualFold(name, srv.providerName) {
			continue
		}
		candidate, _, createErr := createProvider(settings, name, "")
		if createErr != nil {
			provider.DebugLogf("ACP provider %q unavailable: %v", name, createErr)
			continue
		}
		srv.providers[name] = candidate
	}

	mode := opts.Mode
	if mode == "" {
		mode = settings.DefaultMode
	}
	if mode == "" {
		mode = "yolo"
	}
	srv.mode = mode

	thinkingLevel := opts.Thinking
	if thinkingLevel == "" {
		thinkingLevel = settings.DefaultThinkingLevel
	}
	srv.thinkingLevel = provider.ThinkingLevel(thinkingLevel)

	sbMgr := sandbox.NewManagerWithOptions(cwd, settings.Sandbox.Options())
	sbEnabled := opts.Sandbox || settings.Sandbox.Enabled
	if !sbEnabled {
		_ = sbMgr.SetLevel(sandbox.LevelNone)
	} else {
		level := sandbox.LevelStandard
		if settings.Sandbox.Level == "strict" {
			level = sandbox.LevelStrict
		}
		if err := sbMgr.SetLevel(level); err != nil {
			return &startupError{Code: "config_invalid", Message: "sandbox is unavailable", Fix: "Disable strict sandbox or install bwrap", Cause: err}
		}
		if err := sbMgr.FallbackError(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: sandbox unavailable; using direct execution: %v\n", err)
		}
	}
	srv.sbMgr = sbMgr

	resources, err := agentruntime.LoadContextResources(settings, cwd, opts.Workflows, opts.Browser)
	if err != nil {
		return err
	}
	srv.skillsMgr = resources.SkillsMgr
	srv.extraContext = resources.ExtraContext
	srv.ruleContent = resources.RuleContent

	srv.runtime = &agentruntime.SessionRuntime{
		Source: agentruntime.SourceACP, EntrySource: agentruntime.SourceACP,
		WorkDir: cwd, SandboxMgr: sbMgr, SkillsMgr: resources.SkillsMgr,
		ExtraContext: srv.extraContext, RuleContent: srv.ruleContent,
		Providers: srv.providers, ArtifactEnabled: srv.artifact,
	}
	// Agent manager backs multi-agent and delegate workflows.
	if opts.MultiAgent || opts.Delegate || opts.Workflows {
		mgr, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
			Runtime: srv.runtime, Provider: p, Model: model, Settings: settings,
			ProviderName: srv.providerName, Allow: srv.allow, MultiAgentEnabled: true,
			DelegateEnabled: opts.Delegate, WorkflowsEnabled: opts.Workflows,
		})
		if err != nil {
			return fmt.Errorf("create agent manager: %w", err)
		}
		srv.agentMgr = mgr
	}
	// Knowledge-base schedules are Runtime-owned Cron jobs. Start/reconcile the
	// shared scheduler with ACP so a persisted Desktop schedule resumes after
	// restart even before a user opens the management panel. A transient cron
	// setup failure must not prevent the ACP transport from starting; individual
	// management calls surface the structured scheduler error for correction.
	if _, _, cronErr := srv.ensureManageCron(); cronErr != nil {
		log.Printf("[acp] start management cron runtime: %v", cronErr)
	}

	for {
		req, err := srv.readRequest()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			if errors.Is(err, errEmptyMessage) {
				continue
			}
			if err := srv.writeMessage(map[string]any{
				"jsonrpc": "2.0",
				"id":      nil,
				"error":   &mcp.RPCError{Code: -32700, Message: "parse error"},
			}); err != nil {
				return err
			}
			continue
		}
		if req.JSONRPC != "2.0" || !validRPCID(req.ID) {
			id := req.ID
			if !validRPCID(id) || (len(bytes.TrimSpace(id)) == 0 && req.JSONRPC != "2.0") {
				id = json.RawMessage("null")
			}
			if len(req.ID) > 0 || req.JSONRPC != "2.0" {
				srv.writeResponse(id, nil, &mcp.RPCError{Code: -32600, Message: "invalid request"})
			}
			continue
		}

		if len(req.Method) == 0 && len(req.ID) > 0 {
			srv.deliverResponse(req.ID, req.Result, req.Error)
			continue
		}
		if len(req.Method) == 0 {
			continue
		}
		if req.Method != "initialize" {
			srv.mu.Lock()
			initialized := srv.initialized
			srv.mu.Unlock()
			if !initialized {
				if len(req.ID) > 0 {
					srv.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32600, Message: "initialize must be called first"})
				}
				continue
			}
		}

		switch req.Method {
		case "initialize":
			srv.handleInitialize(req)
		case "mothx/doctor":
			srv.handleDoctor(req)
		case "session/new":
			srv.handleNewSession(req)
		case "session/load":
			srv.handleLoadSession(req)
		case "mothx/session/history":
			srv.handleSessionHistory(req)
		case "mothx/session/draft-config-options":
			srv.handleDraftConfigOptions(req)
		case "session/resume":
			srv.handleResumeSession(req)
		case "session/fork":
			srv.handleForkSession(req)
		case "session/prompt":
			srv.handlePrompt(req)
		case "session/cancel":
			srv.handleCancel(req)
		case "$/cancel_request":
			srv.handleCancelRequest(req)
		case "session/close":
			srv.handleCloseSession(req)
		case "mothx/session/delete":
			srv.handleDeleteSession(req)
		case "session/delete":
			srv.handleDeleteSession(req)
		case "mothx/session/setTitle":
			srv.handleSetSessionTitle(req)
		case "mothx/session/setWorkDir":
			srv.handleSetSessionWorkDir(req)
		case "mothx/session/setMeta":
			srv.handleSetSessionMeta(req)
		case "mothx/projects/list":
			srv.handleProjectsList(req)
		case "mothx/projects/create":
			srv.handleProjectsCreate(req)
		case "mothx/projects/rename":
			srv.handleProjectsRename(req)
		case "mothx/projects/delete":
			srv.handleProjectsDelete(req)
		case "mothx/workspace/extend":
			srv.handleWorkspaceExtend(req)
		case "mothx/attachment/fetch":
			srv.handleAttachmentFetch(req)
		case "mothx/attachment/list":
			srv.handleAttachmentList(req)
		case "session/list":
			srv.handleListSessions(req)
		case "mothx/session/listAll":
			srv.handleListAllSessions(req)
		case "session/set_config_option":
			srv.handleSetConfigOption(req)
		case "session/set_mode":
			srv.handleSetMode(req)
		default:
			if strings.HasPrefix(req.Method, "mothx/manage/") {
				srv.handleManageRequest(req)
			} else if len(req.ID) > 0 {
				srv.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32601, Message: "method not found"})
			}
		}
	}
}

// resolveACPModelSelection applies the Harbor requested-model override while
// keeping explicit CLI selections fail-closed when they disagree. A caller
// may provide either provider or model explicitly; the environment supplies
// the missing half, but never silently changes an explicit value.
func resolveACPModelSelection(opts RunOptions, requested string, requestedSet bool) (providerName, modelID string, err error) {
	providerName = strings.TrimSpace(opts.Provider)
	modelID = strings.TrimSpace(opts.Model)
	if !requestedSet {
		return providerName, modelID, nil
	}
	requestedProvider, requestedModel, err := providerfactory.ParseQualifiedModel(requested)
	if err != nil {
		return "", "", fmt.Errorf("parse HARBOR_ACP_REQUESTED_MODEL: %w", err)
	}
	if providerName != "" && !strings.EqualFold(providerName, requestedProvider) {
		return "", "", fmt.Errorf("HARBOR_ACP_REQUESTED_MODEL provider %q conflicts with configured provider %q", requestedProvider, providerName)
	}
	if modelID != "" && !strings.EqualFold(modelID, requestedModel) {
		return "", "", fmt.Errorf("HARBOR_ACP_REQUESTED_MODEL model %q conflicts with configured model %q", requestedModel, modelID)
	}
	return requestedProvider, requestedModel, nil
}

// resolveACPProviderSelection applies request precedence before the startup
// check. When callers explicitly choose a provider without a model, retain the
// factory's existing behavior of selecting that provider's first model rather
// than incorrectly applying the global default model.
func resolveACPProviderSelection(settings *config.Settings, opts RunOptions, requested string, requestedSet bool) (providerName, modelID string, err error) {
	providerName, modelID, err = resolveACPModelSelection(opts, requested, requestedSet)
	if err != nil {
		return "", "", err
	}
	if providerName != "" || settings == nil {
		return providerName, modelID, nil
	}
	providerName = strings.TrimSpace(settings.DefaultProvider)
	if modelID == "" {
		modelID = strings.TrimSpace(settings.DefaultModel)
	}
	return providerName, modelID, nil
}

func createProvider(settings *config.Settings, providerName, modelID string) (provider.Provider, *provider.Model, error) {
	enabled := true
	return providerfactory.CreateWithOptions(settings, providerName, modelID, providerfactory.Options{
		BuiltinAnthropicCacheControl: &enabled,
		RequireModel:                 true,
	})
}

func (s *server) providerFor(name, modelID string) (provider.Provider, *provider.Model, error) {
	if s == nil {
		return nil, nil, fmt.Errorf("ACP server is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = s.providerName
	}
	for catalogName, candidate := range s.providers {
		if strings.EqualFold(catalogName, name) && candidate != nil {
			if modelID == "" {
				models := candidate.Models()
				if len(models) == 0 {
					return nil, nil, fmt.Errorf("provider %q has no usable model", catalogName)
				}
				return candidate, models[0], nil
			}
			model, err := providerfactory.ResolveModel(candidate, catalogName, modelID)
			return candidate, model, err
		}
	}
	if s.settings == nil {
		return nil, nil, fmt.Errorf("ACP settings are required to construct provider %q", name)
	}
	candidate, model, err := createProvider(s.settings, name, modelID)
	if err != nil {
		return nil, nil, err
	}
	if s.providers == nil {
		s.providers = map[string]provider.Provider{}
	}
	s.providers[name] = candidate
	return candidate, model, nil
}

func (s *server) newToolRegistry(cwd string, mgr *session.Manager) *tools.Registry {
	if cwd == "" {
		cwd = s.cwd
	}
	registry, err := agentruntime.BuildRegistry(cwd, s.sbMgr, s.settings, agentruntime.RegistryPolicy{
		RegisterDefaults: true,
		EnablePlanTool:   agentruntime.DefaultPlanToolPolicy(s.settings),
		SkillsMgr:        s.skillsMgr,
		Browser:          s.browser,
		Mutators: []agentruntime.RegistryMutator{func(registry *tools.Registry) error {
			// The interactive question tool is exposed in plan/agent modes (see
			// Registry.ModeTools). ACP maps it to request_permission.
			registry.Register(tools.NewQuestionTool(registry))
			if s.agentMgr != nil {
				// Team experts receive a session-scoped manager after their Runtime
				// is attached below. Keep this legacy shared manager for explicit
				// ACP multi-agent mode only; using it for a team would lose the
				// session roster and mailbox.
				if s.multiAgent {
					agent.RegisterSubAgentTools(registry, s.agentMgr)
				}
				if s.delegate {
					agent.RegisterDelegateSubAgentTool(registry, s.agentMgr)
				}
				if s.workflows {
					workflow.RegisterTools(registry, s.agentMgr, nil)
				}
			}
			return nil
		}},
	})
	if err != nil {
		return nil
	}
	return registry
}

// registerTeamExpertTools installs the team-only manager after the shared
// SessionRuntime has resolved its persisted expert binding. A process-wide ACP
// manager cannot own this state: member definitions and completion mailboxes
// are session resources and must never be shared between ACP sessions.
func (s *server) registerTeamExpertTools(runtime *agentruntime.SessionRuntime, registry *tools.Registry) (*agent.AgentManager, error) {
	if runtime == nil || registry == nil || !runtime.TeamExpertActive() {
		return nil, nil
	}
	p, providerName, model, _, _ := runtime.ConfigSnapshot()
	if p == nil || model == nil {
		return nil, fmt.Errorf("team expert session provider and model are required")
	}
	manager, err := agentruntime.NewAgentManager(agentruntime.AgentManagerOptions{
		Runtime: runtime, Provider: p, ProviderName: providerName, Model: model,
		Settings: s.settings, Allow: s.allow, MultiAgentEnabled: true,
	})
	if err != nil {
		return nil, err
	}
	agent.RegisterSubAgentTools(registry, manager)
	return manager, nil
}

// refreshSessionExpertTools updates only the adapter projection of the
// Runtime-owned expert binding. SessionRuntime.SetExpert remains responsible
// for validation, persistence, identity/skills rehydration and the canonical
// team capability decision; ACP merely swaps the manager-backed tool handles
// that must point at this session's mailbox and roster.
func (s *server) refreshSessionExpertTools(rt *sessionRuntime) error {
	if rt == nil || rt.runtime == nil || rt.registry == nil {
		return fmt.Errorf("session runtime is unavailable")
	}
	for _, name := range []string{"subagent_spawn", "subagent_status", "subagent_send", "subagent_destroy", "subagent_wait"} {
		rt.registry.Remove(name)
	}
	rt.agentMgr = nil
	if rt.runtime.TeamExpertActive() {
		manager, err := s.registerTeamExpertTools(rt.runtime, rt.registry)
		if err != nil {
			return err
		}
		rt.agentMgr = manager
		return nil
	}
	// Preserve the existing ACP --multi-agent behavior for an unbound or
	// single-expert session after removing a former team manager.
	if s != nil && s.multiAgent && s.agentMgr != nil {
		agent.RegisterSubAgentTools(rt.registry, s.agentMgr)
	}
	return nil
}

// configureSessionBindings restores persisted per-session configuration.
// persistDefaults is only true while creating a session; loading an older
// session must remain read-only when those optional bindings are absent.
func (s *server) configureSessionBindings(runtime *agentruntime.SessionRuntime, mgr *session.Manager, persistDefaults bool) error {
	if runtime == nil || mgr == nil {
		return fmt.Errorf("session runtime and manager are required")
	}
	// Some unit fixtures exercise session replay without constructing an ACP
	// provider catalog. Leave those runtimes unbound; production ACP servers
	// always initialize the catalog before accepting session requests.
	if s == nil || s.p == nil {
		return nil
	}
	providerName := s.providerName
	modelID := ""
	modelEntry, hasModel := mgr.GetLatestModelChange()
	if hasModel {
		if modelEntry.Provider != "" {
			providerName = modelEntry.Provider
		}
		modelID = modelEntry.ModelID
	}
	var p provider.Provider
	var model *provider.Model
	var err error
	if !hasModel && strings.EqualFold(providerName, s.providerName) && s.p != nil && s.m != nil {
		p, model = s.p, s.m
	} else {
		p, model, err = s.providerFor(providerName, modelID)
	}
	if err != nil {
		if hasModel && !strings.EqualFold(providerName, s.providerName) {
			return &sessionProviderMismatchError{SessionProvider: providerName, SessionModel: modelID, CurrentProvider: s.providerName, Cause: err}
		}
		return err
	}
	if model == nil {
		return fmt.Errorf("provider %q has no usable model", providerName)
	}
	mode := s.mode
	modeEntry, hasMode := mgr.GetLatestModeChange()
	if hasMode && strings.TrimSpace(modeEntry.Mode) != "" {
		mode = modeEntry.Mode
	}
	thinking := s.thinkingLevel
	thinkingEntry, hasThinking := mgr.GetLatestThinkingLevelChange()
	if hasThinking && strings.TrimSpace(thinkingEntry.ThinkingLevel) != "" {
		thinking = provider.ThinkingLevel(thinkingEntry.ThinkingLevel)
	}
	_, effectiveMode, err := runtime.ResolvePolicy(mode, mode, agentruntime.ModeYolo)
	if err != nil {
		return err
	}
	if err := runtime.ConfigureSession(p, providerName, model, effectiveMode, thinking); err != nil {
		return err
	}
	if persistDefaults && !hasModel {
		if _, err := mgr.AppendModelChange(providerName, model.ID); err != nil {
			return err
		}
	}
	if persistDefaults && !hasMode {
		if _, err := mgr.AppendModeChange(effectiveMode); err != nil {
			return err
		}
	}
	if persistDefaults && !hasThinking {
		_, _, _, _, effectiveThinking := runtime.ConfigSnapshot()
		if _, err := mgr.AppendThinkingLevelChange(string(effectiveThinking)); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) setSessionAdditionalDirectories(rt *sessionRuntime, directories []string) error {
	if rt == nil || rt.runtime == nil {
		return fmt.Errorf("session runtime is unavailable")
	}
	normalized, err := agentruntime.NormalizeAdditionalDirectories(directories)
	if err != nil {
		return err
	}
	if sameStringSlice(normalized, rt.runtime.AdditionalDirectoriesSnapshot()) {
		return nil
	}
	return s.withSessionMutationLease(rt.id, func() error {
		return rt.runtime.SetAdditionalDirectories(normalized)
	})
}

func (s *server) withSessionMutationLease(sessionID string, mutate func() error) error {
	if mutate == nil {
		return nil
	}
	if s == nil || s.settings == nil || strings.TrimSpace(sessionID) == "" {
		return mutate()
	}
	sessionDir := s.settings.GetSessionDir()
	// A prompt in this process already owns the authoritative lease. Its
	// persistence writes still validate that lease's epoch and token.
	if session.RuntimeLeaseLost(sessionDir, sessionID) != nil {
		return mutate()
	}
	guard, err := session.AcquireMutation(sessionDir, sessionID)
	if err != nil {
		return errACPActiveSessionRun
	}
	defer guard.Release()
	return mutate()
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *server) configureSessionCapabilities(runtime *agentruntime.SessionRuntime) error {
	if runtime == nil {
		return fmt.Errorf("session runtime is required")
	}
	sandboxEnabled := false
	browserEnabled := false
	webSearchEnabled := false
	if s != nil {
		browserEnabled = s.browser
		if s.settings != nil {
			sandboxEnabled = s.settings.Sandbox.Enabled
			webSearchEnabled = s.settings.IsWebSearchEnabled()
		}
	}
	return runtime.ConfigureCapabilities(sandboxEnabled, browserEnabled, webSearchEnabled)
}

func (s *server) sessionConfigOptions(sessionID string) []agentruntime.SessionConfigOption {
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.runtime == nil {
		return nil
	}
	return rt.runtime.ConfigOptions()
}

func (s *server) handleInitialize(req rpcRequest) {
	var in initializeRequest
	if len(bytes.TrimSpace(req.Params)) == 0 {
		// Keep direct unit fixtures and legacy embedders working; wire clients
		// must provide protocolVersion per ACP v1.
		in.ProtocolVersion = protocolVersion
	} else if err := json.Unmarshal(req.Params, &in); err != nil || in.ProtocolVersion != protocolVersion {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: fmt.Sprintf("unsupported protocolVersion %d", in.ProtocolVersion)})
		return
	}
	workspaceCwd, workspaceAdditionalDirectories, err := s.resolveWorkspace(in.Meta, "")
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32600, Message: "initialize may only be called once"})
		return
	}
	s.initialized = true
	s.clientCaps = in.ClientCapabilities
	if in.Meta.workspace() != nil && workspaceCwd != "" {
		s.workspaceCwd = workspaceCwd
		s.workspaceAdditionalDirectories = workspaceAdditionalDirectories
	}
	s.mu.Unlock()
	meta := map[string]any{
		mothxExtensionNamespace: map[string]any{
			"minClientProtocol":  1,
			"doctor":             true,
			"requestQuestion":    true,
			"sessionEvent":       true,
			"artifactProjection": true,
			"attachmentFetch":    true,
			"artifactEnabled":    s.artifactEnabled(),
			"features": []string{
				"sessionConfigProvider",
				"sessionDraftConfigOptions",
				"sessionDelete",
				"sessionSetTitle",
				"sessionListCwd",
				"sessionListAll",
				"sessionWorkDir",
				"sessionHistoryPaging",
				"sessionFork",
				"editorContext",
				"doctor",
				"requestQuestion",
				"artifactProjection",
				"attachmentFetch",
				"runStatus",
				"sessionMeta",
				"projects",
				"workspaceExtend",
				"decisionDeadline",
				"subagentEvents",
				"toolResultImages",
				"attachmentList",
				"manageSettings",
				"manageApplicationSettings",
				"manageServeConfig",
				"manageChannels",
				"manageProviders",
				"manageProviderConfig",
				"manageSkills",
				"manageMcp",
				"manageCron",
				"manageStats",
				"manageMemory",
				"manageSkillHub",
				"manageSkillHubCatalog",
				"manageExperts",
				"manageKnowledgeBases",
				"manageEnv",
				"manageDeliveries",
				"knowledgeGraphIndex",
				"knowledgeBaseContext",
			},
		},
	}
	result := initializeResult{
		ProtocolVersion: protocolVersion,
		AgentCapabilities: agentCaps{
			LoadSession: true,
			// promptToIngresses materializes image/audio content blocks and
			// embedded resource/resource_link content through the Runtime input
			// contract, so the negotiated capabilities declare them truthfully.
			PromptCapabilities: promptCaps{
				Image:           true,
				Audio:           true,
				EmbeddedContext: true,
			},
			SessionCapabilities: sessionCaps{
				Close:                 &struct{}{},
				Delete:                &struct{}{},
				List:                  &struct{}{},
				Resume:                &struct{}{},
				Fork:                  &struct{}{},
				AdditionalDirectories: &struct{}{},
			},
			McPCapabilities: mcpCaps{HTTP: true, SSE: true},
			Meta:            meta,
		},
		AgentInfo: clientInfo{
			Name:    "mothx",
			Title:   "MothX",
			Version: s.productVersion(),
		},
		AuthMethods: []authMethod{},
		Meta:        meta,
	}
	s.writeResponse(req.ID, result, nil)
}

func (s *server) handleDoctor(req rpcRequest) {
	var in doctorRequest
	if len(bytes.TrimSpace(req.Params)) > 0 {
		if err := json.Unmarshal(req.Params, &in); err != nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
			return
		}
	}
	if in.Cwd != "" && !filepath.IsAbs(in.Cwd) {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "cwd must be an absolute path"})
		return
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd = s.cwd
	}
	result := doctor.Run(cwd, s.productVersion())
	s.writeResponse(req.ID, result, nil)
}

// effectivePermissionTimeout and effectiveQuestionTimeout keep the decision
// deadline defaults in one place. RunOptions (CLI flags / env) may extend
// them; a zero or negative configured value falls back to the documented ACP
// defaults instead of disabling the deadline.
func (s *server) effectivePermissionTimeout() time.Duration {
	if s != nil && s.permissionTimeout > 0 {
		return s.permissionTimeout
	}
	return defaultPermissionTimeout
}

func (s *server) effectiveQuestionTimeout() time.Duration {
	if s != nil && s.questionTimeout > 0 {
		return s.questionTimeout
	}
	return defaultQuestionTimeout
}

// artifactSessionUpdate projects one persisted Runtime attachment record as
// the additive sessionUpdate="artifact" notification. It is a thin projection
// of canonical state: identity, kind, size, run ownership, and status come
// from the Runtime-owned attachment record and are never synthesized here.
func artifactSessionUpdate(artifactID, filename, kind, mediaType string, size int64, runID string) sessionUpdate {
	sizeValue := int(size)
	return sessionUpdate{
		SessionUpdate: "artifact",
		ArtifactID:    artifactID,
		Filename:      filename,
		Kind:          kind,
		MediaType:     mediaType,
		Size:          &sizeValue,
		RunID:         runID,
		Status:        "generated",
	}
}

// replayGeneratedArtifacts re-emits artifact updates for artifacts persisted
// by earlier runs when a session is loaded so a client that missed the live
// notifications can still render artifact cards. Listing failures are logged
// and never fail the load itself.
func (s *server) replayGeneratedArtifacts(sessionID string) {
	if s == nil || s.settings == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	artifacts, err := session.ListGeneratedArtifacts(context.Background(), s.settings.GetSessionDir(), sessionID)
	if err != nil {
		log.Printf("[acp] list generated artifacts for %s: %v", sessionID, err)
		return
	}
	for _, artifact := range artifacts {
		_ = s.notify(sessionID, artifactSessionUpdate(artifact.ID, artifact.Filename, artifact.Kind, artifact.MediaType, artifact.Bytes, artifact.RunID))
	}
}

// attachmentService returns the Runtime-owned attachment service bound to the
// session root. ACP provides only the protocol surface; storage layout,
// records, expiry, and integrity verification remain in internal/agentruntime
// backed by internal/session.
func (s *server) attachmentService() (*agentruntime.AttachmentService, error) {
	if s == nil || s.settings == nil {
		return nil, fmt.Errorf("ACP settings are unavailable")
	}
	return agentruntime.NewAttachmentService(s.settings.GetSessionDir(), agentruntime.DefaultAttachmentPolicy())
}

func attachmentFetchRPCError(code, message string, extra map[string]any) *mcp.RPCError {
	return acpStructuredRPCError(-32000, code, message, extra)
}

// handleAttachmentFetch serves the mothx/attachment/fetch extension: a
// read-only retrieval of one session attachment's content from the
// Runtime-owned private store. Content is capped at maxRequestBytes so the
// response stays within the ACP stdio message limit; oversized, missing,
// expired, or session-foreign attachments produce structured RPC errors and
// never fall back to adapter-local file scanning.
func (s *server) handleAttachmentFetch(req rpcRequest) {
	var in attachmentFetchRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.AttachmentID) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId and attachmentId are required"})
		return
	}
	sessionID := strings.TrimSpace(in.SessionID)
	attachmentID := strings.TrimSpace(in.AttachmentID)
	service, err := s.attachmentService()
	if err != nil {
		s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_unavailable", err.Error(), nil))
		return
	}
	record, reader, err := service.Open(context.Background(), sessionID, attachmentID)
	if err != nil {
		switch {
		case dao.IsNoRowsAttachment(err):
			s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_not_found", fmt.Sprintf("attachment %s is not available for session %s", attachmentID, sessionID), nil))
		case strings.Contains(err.Error(), "expired"):
			s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_expired", fmt.Sprintf("attachment %s has expired", attachmentID), nil))
		default:
			s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_unavailable", fmt.Sprintf("open attachment %s: %v", attachmentID, err), nil))
		}
		return
	}
	defer reader.Close()
	if record.Bytes > maxRequestBytes {
		s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_too_large", fmt.Sprintf("attachment %s exceeds the %d byte fetch limit", attachmentID, int64(maxRequestBytes)), map[string]any{"size": record.Bytes, "maxBytes": int64(maxRequestBytes)}))
		return
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if err != nil {
		s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_unavailable", fmt.Sprintf("read attachment %s: %v", attachmentID, err), nil))
		return
	}
	if int64(len(content)) > maxRequestBytes {
		s.writeResponse(req.ID, nil, attachmentFetchRPCError("attachment_too_large", fmt.Sprintf("attachment %s exceeds the %d byte fetch limit", attachmentID, int64(maxRequestBytes)), map[string]any{"size": int64(len(content)), "maxBytes": int64(maxRequestBytes)}))
		return
	}
	s.writeResponse(req.ID, attachmentFetchResult{
		Filename:      record.Filename,
		MediaType:     record.MediaType,
		Size:          record.Bytes,
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	}, nil)
}

func (s *server) productVersion() string {
	if s != nil && strings.TrimSpace(s.version) != "" {
		return s.version
	}
	return appversion.Current()
}

// resolveWorkspace combines request-level workspace metadata with the
// top-level cwd/additionalDirectories fields and applies the workspace window
// negotiated during initialize. A process without negotiated metadata keeps
// the permissive behavior needed by embedded/unit callers.
func (s *server) resolveWorkspace(meta requestMeta, requestedCwd string, requestedAdditional ...[]string) (string, []string, error) {
	var configuredCwd, fallbackCwd string
	var configuredAdditional []string
	if s != nil {
		s.mu.Lock()
		configuredCwd = s.workspaceCwd
		configuredAdditional = append([]string(nil), s.workspaceAdditionalDirectories...)
		fallbackCwd = s.cwd
		s.mu.Unlock()
	}

	spec := meta.workspace()
	requestedCwd = strings.TrimSpace(requestedCwd)
	if spec != nil && strings.TrimSpace(spec.Cwd) != "" {
		metaCwd := filepath.Clean(strings.TrimSpace(spec.Cwd))
		if !filepath.IsAbs(metaCwd) {
			return "", nil, fmt.Errorf("workspace cwd must be an absolute path")
		}
		if requestedCwd != "" && filepath.Clean(requestedCwd) != metaCwd {
			return "", nil, fmt.Errorf("cwd does not match workspace cwd")
		}
		requestedCwd = metaCwd
	}
	if requestedCwd == "" {
		requestedCwd = configuredCwd
	}
	if requestedCwd == "" {
		requestedCwd = fallbackCwd
	}
	if requestedCwd != "" {
		if !filepath.IsAbs(requestedCwd) {
			return "", nil, fmt.Errorf("cwd must be an absolute path")
		}
		requestedCwd = filepath.Clean(requestedCwd)
	}

	var additional []string
	if len(requestedAdditional) > 0 && requestedAdditional[0] != nil {
		additional = requestedAdditional[0]
	} else if spec != nil {
		additional = spec.AdditionalDirectories
	}
	normalizedAdditional, err := agentruntime.NormalizeAdditionalDirectories(additional)
	if err != nil {
		return "", nil, err
	}

	if configuredCwd != "" {
		allowed := map[string]struct{}{filepath.Clean(configuredCwd): {}}
		for _, directory := range configuredAdditional {
			allowed[filepath.Clean(directory)] = struct{}{}
		}
		if requestedCwd == "" {
			return "", nil, fmt.Errorf("workspace cwd is required")
		}
		if _, ok := allowed[requestedCwd]; !ok {
			return "", nil, fmt.Errorf("cwd is outside the negotiated workspace")
		}
		for _, directory := range normalizedAdditional {
			if _, ok := allowed[directory]; !ok {
				return "", nil, fmt.Errorf("additional directory is outside the negotiated workspace: %s", directory)
			}
		}
	}
	return requestedCwd, normalizedAdditional, nil
}

func acpInitialized(s *server) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

func startupErrorFromDoctor(result doctor.Response) error {
	for _, check := range result.Checks {
		if check.Status != doctor.StatusError {
			continue
		}
		code := "config_invalid"
		message := strings.ToLower(strings.TrimSpace(check.Detail))
		if check.ID == "cwd" {
			code = "cwd_invalid"
			message = "working directory is unavailable"
		} else if check.ID == "provider.default" {
			if strings.Contains(strings.ToLower(check.Detail), "unknown provider") {
				code = "provider_unknown"
			} else {
				code = "provider_unusable"
			}
			message = doctorStartupMessage(check.Detail)
		} else if check.ID == "model.default" {
			code = "model_unknown"
			message = doctorStartupMessage(check.Detail)
		}
		if message == "" {
			message = strings.ToLower(check.Title) + " check failed"
		}
		return &startupError{Code: code, Message: message, Fix: check.Fix}
	}
	return nil
}

func doctorStartupMessage(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "configuration is unusable"
	}
	parts := strings.SplitN(detail, ":", 2)
	if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[1]), "missing api key") {
		return "default provider " + strings.TrimSpace(parts[0]) + " has no API key"
	}
	// Doctor details are intentionally secret-free. Normalize the stable
	// provider/model wording for the one-line ACP error without echoing any
	// provider response or credential.
	return strings.ToLower(detail)
}

func classifyACPStartupError(err error) *startupError {
	if err == nil {
		return nil
	}
	var existing *startupError
	if errors.As(err, &existing) && existing != nil {
		return existing
	}
	message := strings.ToLower(err.Error())
	code := "config_invalid"
	publicMessage := "ACP configuration is invalid"
	fix := "Check settings.json and ACP options"
	switch {
	case strings.Contains(message, "unknown provider"):
		code = "provider_unknown"
		publicMessage = "selected provider is not configured"
		fix = "Choose a configured provider"
	case strings.Contains(message, "model") && (strings.Contains(message, "available") || strings.Contains(message, "unknown")):
		code = "model_unknown"
		publicMessage = "selected model is not available for the provider"
		fix = "Choose a model listed for this provider"
	case strings.Contains(message, "api key"), strings.Contains(message, "base url"):
		code = "provider_unusable"
		publicMessage = "selected provider is unusable"
		fix = "Configure the provider API key and base URL"
	}
	return &startupError{Code: code, Message: publicMessage, Fix: fix, Cause: err}
}

func writeACPStartupError(err error) {
	var startup *startupError
	if !errors.As(err, &startup) || startup == nil {
		startup = classifyACPStartupError(err)
	}
	payload := map[string]string{
		"code":    startup.Code,
		"message": startup.Message,
	}
	if startup.Fix != "" {
		payload["fix"] = startup.Fix
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "MOTHX_ACP_ERROR %s\n", data)
}

// IsStartupError reports whether Run failed before ACP initialization. CLI
// adapters use this to avoid appending an unstructured Cobra error after the
// machine-readable stderr line has already been emitted.
func IsStartupError(err error) bool {
	var startup *startupError
	return errors.As(err, &startup)
}

func (s *server) handleNewSession(req rpcRequest) {
	var in newSessionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	cwd, additionalDirectories, err := s.resolveWorkspace(in.Meta, in.Cwd, in.AdditionalDirectories)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	if cwd == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "cwd is required"})
		return
	}
	mgr, err := agentruntime.CreateSession(agentruntime.CreateSessionOptions{WorkDir: cwd, SessionDir: s.settings.GetSessionDir()})
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	id := mgr.GetHeader().ID
	registry := s.newToolRegistry(cwd, mgr)
	if registry == nil {
		_ = session.DeleteSession(mgr.GetFile(), s.settings.GetSessionDir())
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "build ACP registry failed"})
		return
	}
	runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		ID: id, Source: agentruntime.SourceACP, WorkDir: cwd, Manager: mgr, Registry: registry,
		Providers:  s.providers,
		SandboxMgr: s.sbMgr, SkillsMgr: s.skillsMgr, ExtraContext: s.extraContext, RuleContent: s.ruleContent,
		Settings: s.settings, Workflows: s.workflows, Browser: s.browser, ArtifactEnabled: s.artifactEnabled(),
	})
	if err == nil {
		err = s.configureSessionBindings(runtime, mgr, true)
	}
	if err == nil {
		err = s.configureSessionCapabilities(runtime)
	}
	var teamAgentMgr *agent.AgentManager
	if err == nil {
		teamAgentMgr, err = s.registerTeamExpertTools(runtime, registry)
	}
	if err == nil {
		registry.SetAdditionalDirectories(runtime.AdditionalDirectoriesSnapshot())
	}
	if err == nil {
		err = runtime.SetAdditionalDirectories(additionalDirectories)
	}
	if err == nil {
		registry.SetAdditionalDirectories(runtime.AdditionalDirectoriesSnapshot())
	}
	if err == nil {
		err = runtime.ConnectConfiguredMCP(context.Background(), agentruntime.MCPPolicy{Servers: in.McpServers, Callbacks: s.buildMCPCallbacks(id)})
	}
	if err != nil {
		runtime.Close()
		if cleanupErr := session.DeleteSession(mgr.GetFile(), s.settings.GetSessionDir()); cleanupErr != nil {
			log.Printf("[acp] cleanup failed session %s: %v", id, cleanupErr)
		}
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	mcpClients := runtime.MCPClients
	s.mu.Lock()
	if old := s.sessions[id]; old != nil {
		old.closeResources()
	}
	runRuntime := &agentruntime.ExecutionRuntime{}
	runRuntime.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
	runtime.SetExecution(runRuntime)
	runRuntime.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()})
	s.sessions[id] = &sessionRuntime{
		runtime: runtime, execution: runRuntime,
		decisions: &agentruntime.DecisionService{},
		id:        id, mgr: mgr, registry: registry, mcp: mcpClients, agentMgr: teamAgentMgr,
	}
	runtime.SetDecisions(s.sessions[id].decisions)
	s.mu.Unlock()
	s.writeResponse(req.ID, newSessionResult{SessionID: id, Modes: sessionModes(runtime), ConfigOptions: s.sessionConfigOptions(id)}, nil)
	_ = s.notifyAvailableCommands(id)
}

func (s *server) handleDraftConfigOptions(req rpcRequest) {
	var in draftConfigOptionsRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	cwd, _, err := s.resolveWorkspace(in.Meta, in.Cwd)
	if err != nil || strings.TrimSpace(cwd) == "" {
		message := "cwd is required"
		if err != nil {
			message = err.Error()
		}
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: message})
		return
	}
	if s.p == nil {
		s.writeResponse(req.ID, map[string]any{"configOptions": []agentruntime.SessionConfigOption{}}, nil)
		return
	}
	options := agentruntime.SessionConfigOptionsWithProviders(s.providerName, s.providers, s.p.Models(), s.m, s.mode, s.thinkingLevel)
	options = append(options, agentruntime.ExpertConfigOption(cwd, ""))
	s.writeResponse(req.ID, map[string]any{"configOptions": options}, nil)
}

func (s *server) handleLoadSession(req rpcRequest) {
	var in loadSessionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	cwd, additionalDirectories, err := s.resolveWorkspace(in.Meta, in.Cwd, in.AdditionalDirectories)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	if cwd == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "cwd is required"})
		return
	}
	if existing := s.sessionRuntime(in.SessionID); existing != nil {
		if existing.mgr == nil || existing.mgr.GetHeader() == nil || filepath.Clean(existing.mgr.GetHeader().Cwd) != cwd {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "session is not available for cwd"})
			return
		}
		if err := s.setSessionAdditionalDirectories(existing, additionalDirectories); err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
		existing.registry.SetAdditionalDirectories(existing.runtime.AdditionalDirectoriesSnapshot())
		if err := s.replayPendingDecisionRequests(in.SessionID); err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
		history, err := s.projectInitialTranscript(in.SessionID, existing.mgr, in.HistoryLimit)
		if err != nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
			return
		}
		s.replayGeneratedArtifacts(in.SessionID)
		s.writeResponse(req.ID, newSessionResult{SessionID: in.SessionID, Modes: sessionModes(existing.runtime), ConfigOptions: s.sessionConfigOptions(in.SessionID), History: history}, nil)
		_ = s.notifyAvailableCommands(in.SessionID)
		return
	}
	rt, err := s.openSessionRuntime(in.SessionID, cwd, in.McpServers)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	if err := s.setSessionAdditionalDirectories(rt, additionalDirectories); err != nil {
		rt.closeResources()
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	rt.registry.SetAdditionalDirectories(rt.runtime.AdditionalDirectoriesSnapshot())
	s.installSessionRuntime(rt)
	history, err := s.projectInitialTranscript(in.SessionID, rt.mgr, in.HistoryLimit)
	if err != nil {
		rt.closeResources()
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	s.replayGeneratedArtifacts(in.SessionID)
	s.writeResponse(req.ID, newSessionResult{SessionID: in.SessionID, Modes: sessionModes(rt.runtime), ConfigOptions: s.sessionConfigOptions(in.SessionID), History: history}, nil)
	_ = s.notifyAvailableCommands(in.SessionID)
}

// handleSessionHistory returns one earlier transcript page for an already
// loaded session. It is intentionally a read-only ACP projection of the
// Runtime-owned manager, not a Desktop-owned history store.
func (s *server) handleSessionHistory(req rpcRequest) {
	var in transcriptPageRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	rt := s.sessionRuntime(in.SessionID)
	if rt == nil || rt.mgr == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "unknown session"})
		return
	}
	page, err := s.transcriptPage(in.SessionID, rt.mgr, in.Cursor, in.Limit)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	s.writeResponse(req.ID, page, nil)
}

func (s *server) handleResumeSession(req rpcRequest) {
	var in resumeSessionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	cwd, additionalDirectories, err := s.resolveWorkspace(in.Meta, in.Cwd, in.AdditionalDirectories)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	if cwd == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "cwd is required"})
		return
	}
	if existing := s.sessionRuntime(in.SessionID); existing != nil {
		if existing.mgr == nil || existing.mgr.GetHeader() == nil || filepath.Clean(existing.mgr.GetHeader().Cwd) != cwd {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "session is not available for cwd"})
			return
		}
		if err := s.setSessionAdditionalDirectories(existing, additionalDirectories); err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
		existing.registry.SetAdditionalDirectories(existing.runtime.AdditionalDirectoriesSnapshot())
		if err := s.replayPendingDecisionRequests(in.SessionID); err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
		s.replayGeneratedArtifacts(in.SessionID)
		s.writeResponse(req.ID, newSessionResult{SessionID: in.SessionID, Modes: sessionModes(existing.runtime), ConfigOptions: s.sessionConfigOptions(in.SessionID)}, nil)
		_ = s.notifyAvailableCommands(in.SessionID)
		return
	}
	rt, err := s.openSessionRuntime(in.SessionID, cwd, in.McpServers)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	if err := s.setSessionAdditionalDirectories(rt, additionalDirectories); err != nil {
		rt.closeResources()
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	rt.registry.SetAdditionalDirectories(rt.runtime.AdditionalDirectoriesSnapshot())
	s.installSessionRuntime(rt)
	s.replayGeneratedArtifacts(in.SessionID)
	s.writeResponse(req.ID, newSessionResult{SessionID: in.SessionID, Modes: sessionModes(rt.runtime), ConfigOptions: s.sessionConfigOptions(in.SessionID)}, nil)
	_ = s.notifyAvailableCommands(in.SessionID)
}

func (s *server) handleForkSession(req rpcRequest) {
	var in forkSessionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId is required"})
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "ACP settings are unavailable"})
		return
	}
	parent, err := session.OpenByIDExact(s.settings.GetSessionDir(), in.SessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	if parent.GetHeader() == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "parent session header is unavailable"})
		return
	}
	if requestedParent := strings.TrimSpace(in.Meta.parentSessionID()); requestedParent != "" && requestedParent != in.SessionID {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "fork parentSessionId must match sessionId"})
		return
	}
	cwd := in.Cwd
	if strings.TrimSpace(cwd) == "" {
		cwd = parent.GetHeader().Cwd
	}
	resolvedCwd, additionalDirectories, err := s.resolveWorkspace(in.Meta, cwd, in.AdditionalDirectories)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	if resolvedCwd != filepath.Clean(parent.GetHeader().Cwd) {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "fork cwd must match the parent session cwd"})
		return
	}
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(string(req.ID))
	}
	if requestID == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "fork request requires an idempotency requestId"})
		return
	}
	forkOptions := agentruntime.ForkOptions{
		SourceSessionID: in.SessionID,
		AtSeq:           in.AtSeq,
		RequestID:       requestID,
		TitleMode:       in.TitleMode,
	}
	if in.ExpertID != nil {
		expertID := strings.TrimSpace(*in.ExpertID)
		if expertID != "" {
			bundle, inspectErr := agentruntime.InspectExpert(resolvedCwd, expertID)
			if inspectErr != nil {
				s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: inspectErr.Error()})
				return
			}
			if bundle == nil || bundle.Invalid {
				message := fmt.Sprintf("expert bundle %q is invalid", expertID)
				if bundle != nil && strings.TrimSpace(bundle.InvalidReason) != "" {
					message += ": " + bundle.InvalidReason
				}
				s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: message})
				return
			}
		}
		result, err := agentruntime.ForkWithExpert(context.Background(), s.settings.GetSessionDir(), forkOptions, expertID)
		if err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
		s.completeForkSession(req, in, result, resolvedCwd, additionalDirectories)
		return
	}
	result, err := agentruntime.Fork(context.Background(), s.settings.GetSessionDir(), forkOptions)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	s.completeForkSession(req, in, result, resolvedCwd, additionalDirectories)
}

// completeForkSession opens and publishes the child after either a normal
// fork or the Runtime-owned expert-switch fork has persisted it.
func (s *server) completeForkSession(req rpcRequest, in forkSessionRequest, result agentruntime.ForkResult, resolvedCwd string, additionalDirectories []string) {
	rt, err := s.openSessionRuntime(result.SessionID, resolvedCwd, in.McpServers)
	if err != nil {
		_ = agentruntime.DeleteSession(s.settings.GetSessionDir(), result.SessionID)
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	if in.AdditionalDirectories != nil || (in.Meta.workspace() != nil && in.Meta.workspace().AdditionalDirectories != nil) {
		if err := s.setSessionAdditionalDirectories(rt, additionalDirectories); err != nil {
			rt.closeResources()
			_ = agentruntime.DeleteSession(s.settings.GetSessionDir(), result.SessionID)
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
		rt.registry.SetAdditionalDirectories(rt.runtime.AdditionalDirectoriesSnapshot())
	}
	s.installSessionRuntime(rt)
	s.writeResponse(req.ID, newSessionResult{
		SessionID:       result.SessionID,
		ParentSessionID: result.ParentSessionID,
		Modes:           sessionModes(rt.runtime),
		ConfigOptions:   s.sessionConfigOptions(result.SessionID),
	}, nil)
	_ = s.notifyAvailableCommands(result.SessionID)
}

func (s *server) handleSetConfigOption(req rpcRequest) {
	var in setConfigOptionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.ConfigID) == "" || len(bytes.TrimSpace(in.Value)) == 0 {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId, configId, and value are required"})
		return
	}
	configID := strings.TrimSpace(in.ConfigID)
	switch configID {
	case "thought_level", "thinking":
		configID = agentruntime.ConfigOptionThinkingLevel
	case "web-search", "websearch":
		configID = agentruntime.ConfigOptionWebSearch
	}
	value, err := acpConfigValue(in.Value, configID == agentruntime.ConfigOptionExpert)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	rt := s.sessionRuntime(in.SessionID)
	if rt == nil || rt.runtime == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "unknown session"})
		return
	}
	var mutationErr error
	if configID == agentruntime.ConfigOptionSandbox || configID == agentruntime.ConfigOptionBrowser || configID == agentruntime.ConfigOptionWebSearch {
		enabled, parseErr := strconv.ParseBool(strings.TrimSpace(value))
		if parseErr != nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "boolean config value is required"})
			return
		}
		mutationErr = s.withSessionMutationLease(in.SessionID, func() error {
			return rt.runtime.SetCapabilityOption(configID, enabled)
		})
	} else if configID == agentruntime.ConfigOptionExpert {
		mutationErr = s.withSessionMutationLease(in.SessionID, func() error {
			if err := rt.runtime.SetConfigOption(configID, value); err != nil {
				return err
			}
			return s.refreshSessionExpertTools(rt)
		})
	} else {
		mutationErr = s.withSessionMutationLease(in.SessionID, func() error {
			return rt.runtime.SetConfigOption(configID, value)
		})
	}
	if mutationErr != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: mutationErr.Error()})
		return
	}
	options := s.sessionConfigOptions(in.SessionID)
	s.notify(in.SessionID, sessionUpdate{SessionUpdate: "config_option_update", ConfigOptions: options})
	s.notifySessionInfo(in.SessionID)
	s.writeResponse(req.ID, map[string]any{"configOptions": options}, nil)
}

func acpConfigValue(raw json.RawMessage, allowEmpty bool) (string, error) {
	raw = bytes.TrimSpace(raw)
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		if strings.TrimSpace(value) == "" && !allowEmpty {
			return "", fmt.Errorf("config value must not be empty")
		}
		return value, nil
	}
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil && (bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))) {
		return strconv.FormatBool(boolean), nil
	}
	return "", fmt.Errorf("config value must be a string or boolean")
}

func (s *server) handleSetMode(req rpcRequest) {
	var in setModeRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId and mode are required"})
		return
	}
	modeID := strings.TrimSpace(in.ModeID)
	if modeID == "" {
		modeID = strings.TrimSpace(in.Mode)
	}
	if modeID == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId and modeId are required"})
		return
	}
	rt := s.sessionRuntime(in.SessionID)
	if rt == nil || rt.runtime == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "unknown session"})
		return
	}
	// SessionRuntime snapshots the binding for the active prompt; changing the
	// mode here therefore affects the next prompt and remains protocol-safe.
	if err := s.withSessionMutationLease(in.SessionID, func() error {
		return rt.runtime.SetConfigOption(agentruntime.ConfigOptionMode, modeID)
	}); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	options := s.sessionConfigOptions(in.SessionID)
	_, _, _, effectiveMode, _ := rt.runtime.ConfigSnapshot()
	s.notify(in.SessionID, sessionUpdate{SessionUpdate: "current_mode_update", CurrentModeID: effectiveMode})
	s.notify(in.SessionID, sessionUpdate{SessionUpdate: "config_option_update", ConfigOptions: options})
	s.notifySessionInfo(in.SessionID)
	s.writeResponse(req.ID, map[string]any{}, nil)
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func (s *server) sessionRuntime(sessionID string) *sessionRuntime {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	return rt
}

func (s *server) openSessionRuntime(sessionID, cwd string, servers []mcp.ServerConfig) (*sessionRuntime, error) {
	mgr, err := agentruntime.OpenSessionForWorkDir(cwd, s.settings.GetSessionDir(), sessionID)
	if err != nil {
		return nil, err
	}
	registry := s.newToolRegistry(cwd, mgr)
	if registry == nil {
		return nil, fmt.Errorf("build ACP registry failed")
	}
	resolvedSource, err := agentruntime.ResolveSourceFromSession(s.settings.GetSessionDir(), sessionID, agentruntime.SourceResolutionInput{
		SessionHeader: mgr.GetHeader(), Requested: agentruntime.SourceACP,
	})
	if err != nil {
		return nil, err
	}
	runtime, err := agentruntime.AttachSessionResources(agentruntime.AttachedResources{
		ID: sessionID, Source: resolvedSource.Source, EntrySource: agentruntime.SourceACP, WorkDir: cwd, Manager: mgr, Registry: registry,
		Providers:  s.providers,
		SandboxMgr: s.sbMgr, SkillsMgr: s.skillsMgr, ExtraContext: s.extraContext, RuleContent: s.ruleContent,
		Settings: s.settings, Workflows: s.workflows, Browser: s.browser, ArtifactEnabled: s.artifactEnabled(),
	})
	if err == nil {
		err = s.configureSessionBindings(runtime, mgr, false)
	}
	if err == nil {
		err = s.configureSessionCapabilities(runtime)
	}
	var teamAgentMgr *agent.AgentManager
	if err == nil {
		teamAgentMgr, err = s.registerTeamExpertTools(runtime, registry)
	}
	if err == nil {
		registry.SetAdditionalDirectories(runtime.AdditionalDirectoriesSnapshot())
	}
	if err == nil {
		err = runtime.ConnectConfiguredMCP(context.Background(), agentruntime.MCPPolicy{Servers: servers, Callbacks: s.buildMCPCallbacks(sessionID)})
	}
	if err != nil {
		runtime.Close()
		return nil, err
	}
	mcpClients := runtime.MCPClients
	runRuntime := &agentruntime.ExecutionRuntime{}
	runRuntime.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
	runtime.SetExecution(runRuntime)
	runRuntime.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()})
	rt := &sessionRuntime{
		runtime: runtime, execution: runRuntime,
		decisions: &agentruntime.DecisionService{},
		id:        sessionID, mgr: mgr, registry: registry, mcp: mcpClients, agentMgr: teamAgentMgr,
		cost: s.persistedSessionCost(mgr, runtimeModel(runtime)),
	}
	runtime.SetDecisions(rt.decisions)
	if err := s.rehydrateSessionDecisions(rt); err != nil {
		rt.closeResources()
		return nil, err
	}
	return rt, nil
}

func (s *server) installSessionRuntime(rt *sessionRuntime) {
	s.mu.Lock()
	old := s.sessions[rt.id]
	s.sessions[rt.id] = rt
	s.mu.Unlock()
	if old != nil {
		_ = s.shutdownSessionRuntime(old)
	}
}

func (s *server) availableCommands() []availableCommand {
	if s == nil {
		return nil
	}
	return s.availableCommandsFor(s.skillsMgr)
}

func (s *server) availableCommandsFor(manager *skills.Manager) []availableCommand {
	if manager == nil {
		return nil
	}
	commands := []availableCommand{{Name: systeminit.Command, Description: "Initialize project guidance", Meta: map[string]any{mothxExtensionNamespace: map[string]any{"kind": "command"}}}}
	for _, skill := range manager.List() {
		if skill == nil || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		commands = append(commands, availableCommand{
			Name:        "/" + skill.Name,
			Description: skill.Description,
			Meta:        map[string]any{mothxExtensionNamespace: map[string]any{"kind": "skill"}},
		})
	}
	return commands
}

func (s *server) notifyAvailableCommands(sessionID string) error {
	return s.notifyAvailableCommandsFor(sessionID, s.skillsMgr)
}

func (s *server) notifyAvailableCommandsFor(sessionID string, manager *skills.Manager) error {
	commands := s.availableCommandsFor(manager)
	if len(commands) == 0 {
		return nil
	}
	return s.notify(sessionID, sessionUpdate{SessionUpdate: "available_commands_update", AvailableCommands: commands})
}

// activateSkillPrompt recognizes the same explicit skill directives exposed by
// TUI and Serve. Desktop inserts /<skill> from available_commands_update, and
// ACP clients may use /skill <name> or /skill:<name>. The shared Runtime owns
// the reload and prompt context; ACP only recognizes its wire command.
func (s *server) activateSkillPrompt(rt *sessionRuntime, text string) (bool, error) {
	if rt == nil || rt.runtime == nil {
		return false, nil
	}
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 {
		return false, nil
	}
	name := ""
	switch {
	case len(parts) == 1 && strings.HasPrefix(parts[0], "/skill:"):
		name = strings.TrimPrefix(parts[0], "/skill:")
	case len(parts) == 2 && parts[0] == "/skill":
		name = parts[1]
	case len(parts) == 1 && strings.HasPrefix(parts[0], "/"):
		name = strings.TrimPrefix(parts[0], "/")
	default:
		return false, nil
	}
	name = strings.TrimSpace(name)
	if name == "" || rt.runtime.SkillsMgr == nil || rt.runtime.SkillsMgr.Get(name) == nil {
		return false, nil
	}
	if rt.activeSkills == nil {
		rt.activeSkills = make(map[string]bool)
	}
	previous, existed := rt.activeSkills[name]
	rt.activeSkills[name] = true
	_, browserEnabled, _ := rt.runtime.CapabilitySnapshot()
	if err := rt.runtime.RefreshResources(s.settings, agentruntime.RefreshOptions{
		Workflows:    s.workflows,
		Browser:      browserEnabled,
		ActiveSkills: rt.activeSkills,
	}); err != nil {
		if existed {
			rt.activeSkills[name] = previous
		} else {
			delete(rt.activeSkills, name)
		}
		return true, err
	}
	return true, nil
}

func (s *server) handlePrompt(req rpcRequest) {
	var in promptRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	rt := s.sessionForPrompt(in.SessionID)
	if rt == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "unknown session"})
		return
	}
	if rt.runtime == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "session runtime is unavailable"})
		return
	}
	workspaceCwd, workspaceAdditional, err := s.resolveWorkspace(in.Meta, rt.runtime.WorkDir)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: err.Error()})
		return
	}
	if parentID := strings.TrimSpace(in.Meta.parentSessionID()); parentID != "" {
		persistedParent := ""
		if rt.mgr != nil && rt.mgr.GetHeader() != nil {
			persistedParent = rt.mgr.GetHeader().ParentSession
		}
		if persistedParent != parentID {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "prompt parentSessionId does not match the session lineage"})
			return
		}
	}
	promptKey := mcp.RawIDKey(req.ID)
	// knowledgeBaseRefs is an additive ACP input capability.  Its contents are
	// resolved by the shared Runtime, so an ACP client does not need to claim a
	// particular UI surface (and a caller-controlled metadata value must not be
	// treated as an authorization boundary).
	promptText, promptIngresses, err := promptToIngresses(in.Prompt, workspaceCwd, workspaceAdditional, "acp:"+promptKey)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhaseAdmission))
		return
	}
	userText := strings.TrimSpace(promptText)
	if userText == "" && len(promptIngresses) == 0 {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "empty prompt"})
		return
	}
	if len(promptIngresses) == 0 {
		activated, activateErr := s.activateSkillPrompt(rt, userText)
		if activateErr != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(activateErr, nil, agentruntime.PhaseAdmission))
			return
		}
		if activated {
			s.writeResponse(req.ID, promptResult{StopReason: "end_turn"}, nil)
			return
		}
	}
	editorContextText := formatEditorContext(in.Meta.editorContext())
	sessionProvider, sessionProviderName, sessionModel, sessionMode, sessionThinking := rt.runtime.ConfigSnapshot()
	if sessionProvider == nil || sessionModel == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "session model is unavailable"})
		return
	}
	resolution, effectiveMode, err := rt.runtime.ResolvePolicy(sessionMode, "", agentruntime.ModeYolo)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhaseAdmission))
		return
	}
	runSource := string(resolution.Source)
	if runSource == "" {
		runSource = string(agentruntime.SourceACP)
	}
	// Expand the /systeminit slash command into the full instruction prompt.
	// In ACP the question tool is available, so use the interactive variant.
	// /systeminit must also be able to write AGENTS.md, so upgrade plan mode to
	// agent for this prompt only.
	if fields := strings.Fields(strings.TrimSpace(userText)); len(fields) > 0 && fields[0] == systeminit.Command {
		extra := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(userText), systeminit.Command))
		userText = systeminit.Prompt(true, extra)
		if effectiveMode == "plan" {
			effectiveMode = "agent"
		}
	}
	// ACP SDK request IDs restart at 0 for every connection (initialize,
	// new_session, set_config_option, prompt), so a bare request ID would
	// collide with runs persisted by earlier processes. Keep the request ID
	// for readability but append a random suffix for durable uniqueness.
	runID := "acp_" + promptKey + "_" + session.GenerateID()
	runtimeRelease, admissionErr := s.acquirePromptAdmission(rt)
	if admissionErr != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(admissionErr, nil, agentruntime.PhaseAdmission))
		return
	}
	admissionTransferred := false
	defer func() {
		if !admissionTransferred {
			runtimeRelease()
		}
	}()
	if rt.execution == nil {
		rt.execution = &agentruntime.ExecutionRuntime{}
		rt.execution.SetRunStore(agentruntime.RunStore{SessionDir: s.settings.GetSessionDir()})
		rt.execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()})
	}
	startedAt := time.Now()
	sandboxEnabled, browserEnabled, webSearchEnabled := rt.runtime.CapabilitySnapshot()
	workDir := rt.runtime.WorkDir
	if workDir == "" {
		workDir = workspaceCwd
	}
	if workDir == "" {
		workDir = s.cwd
	}
	policySnapshot, snapshotErr := json.Marshal(map[string]any{
		"source": runSource, "mode": effectiveMode, "workDir": workDir,
		"surface":      in.Meta.surface(),
		"capabilities": map[string]any{"multiAgent": s.multiAgent, "delegate": s.delegate, "workflows": s.workflows, "browser": browserEnabled, "webSearch": webSearchEnabled},
		"sandbox":      map[string]any{"enabled": sandboxEnabled}, "approvalPolicy": "runtime", "questionPolicy": "runtime",
	})
	if snapshotErr != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(snapshotErr, nil, agentruntime.PhaseAdmission))
		return
	}
	runInput, inputErr := rt.runtime.AcceptInput(context.Background(), runID, userText, promptIngresses)
	if inputErr != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(inputErr, nil, agentruntime.PhaseAdmission))
		return
	}
	if len(in.KnowledgeBaseRefs) > 0 {
		enrichedInput, knowledgeErr := rt.runtime.WithKnowledgeContext(context.Background(), runInput, in.KnowledgeBaseRefs)
		if knowledgeErr != nil {
			rt.runtime.DiscardInput(context.Background(), runInput)
			s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(knowledgeErr))
			return
		}
		runInput = enrichedInput
	}
	promptMessage, messageErr := rt.runtime.BuildUserMessage(context.Background(), runInput)
	if messageErr != nil {
		rt.runtime.DiscardInput(context.Background(), runInput)
		s.writeResponse(req.ID, nil, acpFailureRPCError(messageErr, nil, agentruntime.PhaseAdmission))
		return
	}
	requestSnapshot, snapshotErr := acpPromptRequestSnapshot(context.Background(), rt.runtime, userText, runInput)
	if snapshotErr != nil {
		rt.runtime.DiscardInput(context.Background(), runInput)
		s.writeResponse(req.ID, nil, acpFailureRPCError(snapshotErr, nil, agentruntime.PhaseAdmission))
		return
	}
	intent := agentruntime.ExecutionIntent{ID: "intent_" + session.GenerateID(), SessionID: rt.id, Source: runSource, Model: sessionModel.ID, Mode: effectiveMode, WorkDir: workDir, RequestFingerprint: fmt.Sprintf("prompt:%x", sha256.Sum256(requestSnapshot)), Request: requestSnapshot, Policy: policySnapshot, CreatedAt: startedAt}
	startData, _ := json.Marshal(map[string]any{"intentId": intent.ID, "attempt": 1})
	ctx, err := rt.execution.BeginIntentDurable(context.Background(), intent, agentruntime.DurableRun{
		ID: runID, SessionID: rt.id, IntentID: intent.ID, Attempt: 1, WorkDir: workDir, Source: runSource, Model: sessionModel.ID, Mode: effectiveMode,
		InputResourceIDs: runInput.ResourceIDs(),
		UserEntryID:      session.RunUserEntryID(runID), UserMessage: &promptMessage,
		Status: "running", StartedAt: startedAt, ConversationTurnID: "turn-" + intent.ID, ConversationTurn: true,
	}, agentruntime.RunEvent{SessionID: rt.id, RunID: runID, EventType: "started", Source: runSource, Status: "running", Model: sessionModel.ID, Mode: effectiveMode, Timestamp: startedAt, Data: startData})
	if err != nil {
		rt.runtime.DiscardInput(context.Background(), runInput)
		if active, activeErr := agentruntime.GetActiveDurableRun(context.Background(), s.settings.GetSessionDir(), rt.id); activeErr == nil && active != nil {
			err = errACPActiveSessionRun
		}
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhaseAdmission))
		return
	}
	// Project the durable begin as the additive run_status event; it
	// complements the terminal event and the prompt response.
	s.notifyRunStatus(rt.id, runID, "running")
	cancel := func() { rt.execution.Cancel() }
	finishEarly := func(state agentruntime.RunState, message string) {
		cancel()
		_ = rt.execution.FinishDurableWithRetry(context.Background(), runID, state, message, agentruntime.RunEvent{
			SessionID: rt.id, RunID: runID, EventType: "finished", Source: runSource,
			Status: string(state), Model: sessionModel.ID, Mode: effectiveMode, Timestamp: time.Now(),
		})
		s.notifyRunStatus(rt.id, runID, acpRunStatus(string(state)))
	}
	// Publication is a Runtime capability, not an ACP-local output convention.
	// It must be installed before BuildAgent freezes the tool registry.
	artifacts, err := rt.runtime.BeginArtifactCollection(runID)
	if err != nil {
		finishEarly(agentruntime.RunStateFailed, err.Error())
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhaseAdmission))
		return
	}
	// Project generated artifacts to the client as canonical session/update
	// notifications. The observer only renders Runtime-owned attachment
	// records after durable persistence; content retrieval stays with the
	// mothx/attachment/fetch extension method.
	artifacts.SetObserver(func(record agentruntime.SessionAttachment) {
		_ = s.notify(rt.id, artifactSessionUpdate(record.ID, record.Filename, string(record.Kind), record.MediaType, record.Bytes, runID))
	})
	rt.cancelMu.Lock()
	rt.cancel = cancel
	rt.promptID = promptKey
	rt.runID = runID
	rt.streamSegment = 0
	rt.messageID = acpStreamMessageID(rt.id, promptKey, "message", rt.streamSegment)
	rt.thoughtMessageID = acpStreamMessageID(rt.id, promptKey, "thought", rt.streamSegment)
	rt.userMessageID = "acp_" + rt.id + "_" + promptKey + "_user"
	rt.activeModel = sessionModel
	rt.activeMode = effectiveMode
	rt.activeThinking = sessionThinking
	rt.terminalNotified = false
	rt.cancelMu.Unlock()
	// Echo the accepted user content using the same message grouping contract
	// used by streamed agent chunks.
	s.notify(rt.id, sessionUpdate{SessionUpdate: "user_message_chunk", MessageID: rt.userMessageID, Content: &contentBlock{Type: "text", Text: userText}})

	extraContext := rt.runtime.ExtraContext
	if editorContextText != "" {
		if strings.TrimSpace(extraContext) != "" {
			extraContext += "\n\n"
		}
		extraContext += editorContextText
	}
	runSettings := s.settings
	if s.settings != nil {
		settingsCopy := *s.settings
		settingsCopy.WebSearch.Enabled = config.BoolPtr(webSearchEnabled)
		runSettings = &settingsCopy
	}
	a, err := rt.runtime.BuildAgent(agentruntime.AgentBuildOptions{
		Provider: sessionProvider, ProviderName: sessionProviderName, Model: sessionModel,
		Settings: runSettings, Allow: s.allow, Mode: effectiveMode, ThinkingLevel: sessionThinking,
		ExtraContext:   extraContext,
		SandboxEnabled: &sandboxEnabled,
		MultiAgent:     s.multiAgent, DelegateMode: s.delegate, Workflows: s.workflows,
		GetSteeringMessages: esmSteeringMessages(runSettings, rt.id),
		ConversationTurnID:  "turn-" + intent.ID, IntentID: intent.ID, RunID: runID,
		ConversationTurn: true, RuntimeOwnsTurnEnd: true,
		ApprovalHandler: func(toolCallID, toolName string, args map[string]any) bool {
			if err := rt.execution.WaitForApproval(runID); err != nil {
				return false
			}
			defer func() { _ = rt.execution.Resume(runID) }()
			return s.requestPermissionContext(ctx, rt.id, toolCallID, toolName, args)
		},
	})
	if err != nil {
		rt.cancelMu.Lock()
		rt.cancel = nil
		rt.promptID = ""
		rt.runID = ""
		rt.activeModel = nil
		rt.activeMode = ""
		rt.activeThinking = ""
		rt.cancelMu.Unlock()
		info, observeErr := rt.execution.RecordFailure(err, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseAdmission})
		if observeErr != nil {
			log.Printf("[acp] record agent build failure for %s: %v", runID, observeErr)
		}
		log.Printf("[acp] build agent for %s failed: %v", runID, err)
		finishEarly(agentruntime.RunStateFailed, agentruntime.DisplayErrorMessage(info))
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, &info, agentruntime.PhaseAdmission))
		return
	}
	rt.execution.SetAgent(a)
	agentMgr := rt.agentMgr
	if agentMgr == nil {
		agentMgr = s.agentMgr
	}
	if agentMgr != nil {
		agentMgr.Register(agent.NewAgentAdapter(a))
	}
	rt.agent = agent.NewAgentAdapter(a)
	// The runtime lock is held for the full lifetime of the admitted Run so
	// another adapter cannot race its terminal persistence with a new Run.
	admissionTransferred = true
	go func() {
		defer runtimeRelease()
		defer artifacts.Close()
		stopReason := "end_turn"
		var runErr error
		var terminalInfo *agentruntime.ErrorInfo
		defer func() {
			if agentMgr != nil && rt.agent != nil {
				agentMgr.Finish(rt.agent.ID(), runErr)
			}
			rt.cancelMu.Lock()
			closed := rt.closed
			if rt.promptID == promptKey {
				rt.cancel = nil
				rt.promptID = ""
				rt.runID = ""
				rt.messageID = ""
				rt.thoughtMessageID = ""
				rt.userMessageID = ""
				rt.activeModel = nil
				rt.activeMode = ""
				rt.activeThinking = ""
			}
			rt.cancelMu.Unlock()
			if closed {
				rt.closeResources()
			}
			cancel()
			state := agentruntime.RunStateCompleted
			switch {
			case errors.Is(runErr, context.DeadlineExceeded):
				state = agentruntime.RunStateTimedOut
			case stopReason == "cancelled" || errors.Is(runErr, context.Canceled):
				state = agentruntime.RunStateCancelled
			case runErr != nil:
				state = agentruntime.RunStateFailed
			}
			message := ""
			var data json.RawMessage
			if runErr != nil {
				info := acpFailureInfo(runErr, terminalInfo, agentruntime.PhaseModel)
				message = agentruntime.DisplayErrorMessage(info)
				data, _ = json.Marshal(map[string]any{"error": message, "errorInfo": info})
			}
			_ = rt.execution.FinishDurableWithRetry(context.Background(), runID, state, message, agentruntime.RunEvent{SessionID: rt.id, RunID: runID, EventType: "finished", Source: runSource, Status: string(state), Model: sessionModel.ID, Mode: effectiveMode, Timestamp: time.Now(), Data: data})
			s.notifyRunStatus(rt.id, runID, acpRunStatus(string(state)))
		}()
		// Consume the canonical internal event stream for Runtime observation,
		// then project each event to ACP's public wire format below.
		events := a.RunWithUserMessage(ctx, promptMessage)
		terminalSeen := false
		legacyTerminalSeen := false
		for coreEvent := range events {
			// Child-agent terminal events are projected to ACP as sub-agent
			// activity and must not mutate the parent Run's terminal facts.
			if coreEvent.AgentID == "" || (coreEvent.Type != agent.EventRunFinished && coreEvent.Type != agent.EventError) {
				observation, observeErr := rt.execution.ObserveAgentEvent(coreEvent)
				if observeErr != nil {
					log.Printf("[acp] observe agent event for %s: %v", runID, observeErr)
				} else if observation.Error != nil {
					info := *observation.Error
					terminalInfo = &info
				}
			}
			ev := agent.EventToPublic(coreEvent)
			s.handleAgentEvent(rt.id, ev)
			// A child-agent timeout is isolated to the child and must not
			// turn the ACP request into a failed parent run.
			if ev.AgentID != "" {
				continue
			}
			switch ev.Type {
			case agentpkg.EventQuestionRequest:
				go s.handleQuestion(ctx, rt, runID, ev)
			case agentpkg.EventRunFinished:
				terminalSeen = true
				switch ev.Status {
				case agentpkg.TaskFailed:
					if ev.Error != nil {
						runErr = ev.Error
					} else {
						runErr = errors.New("run failed")
					}
					stopReason = normalizeStopReason(ev.StopReason)
				case agentpkg.TaskCanceled:
					stopReason = "cancelled"
				case agentpkg.TaskIncomplete:
					stopReason = "max_tokens"
				default:
					stopReason = normalizeStopReason(ev.StopReason)
				}
			case agentpkg.EventDone:
				if !terminalSeen {
					legacyTerminalSeen = true
					stopReason = normalizeStopReason(ev.StopReason)
				}
			case agentpkg.EventError:
				if !terminalSeen {
					legacyTerminalSeen = true
					if ev.Error != nil {
						runErr = ev.Error
					} else {
						runErr = errors.New("agent error event without error detail")
					}
					stopReason = normalizeStopReason(ev.StopReason)
				}
			}
		}
		if !terminalSeen && !legacyTerminalSeen && runErr == nil {
			// Event stream closed without any terminal event — protocol failure,
			// never a successful completion.
			runErr = errors.New("event stream closed without terminal result")
			info, observeErr := rt.execution.RecordFailure(runErr, agentruntime.ErrorClassificationOptions{
				Code: "event_stream_interrupted", Type: "transport_error", Phase: agentruntime.PhaseTransport,
				MessageKey: "run.error.streamInterrupted", Message: "The run stopped before it could finish.",
			})
			if observeErr != nil {
				log.Printf("[acp] record interrupted stream for %s: %v", runID, observeErr)
			}
			terminalInfo = &info
		}
		if runErr != nil && stopReason != "cancelled" {
			log.Printf("[acp] agent prompt %s failed: %v", runID, runErr)
			s.writeResponse(req.ID, nil, acpFailureRPCError(runErr, terminalInfo, agentruntime.PhaseModel))
			return
		}
		s.writeResponse(req.ID, promptResult{StopReason: stopReason}, nil)
	}()
}

func (s *server) handleCancel(req rpcRequest) {
	var in cancelRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		if len(req.ID) > 0 {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId is required"})
		}
		return
	}
	s.mu.Lock()
	rt := s.sessions[in.SessionID]
	s.mu.Unlock()
	if rt == nil {
		if len(req.ID) > 0 {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "unknown session"})
		}
		return
	}
	if rt.execution != nil && rt.execution.Cancel() {
		// ExecutionRuntime also aborts the core agent.
	} else {
		rt.cancelMu.Lock()
		if rt.cancel != nil {
			rt.cancel()
		}
		rt.cancelMu.Unlock()
	}
	if len(req.ID) > 0 {
		s.writeResponse(req.ID, map[string]any{}, nil)
	}
}

func (s *server) handleCancelRequest(req rpcRequest) {
	var in cancelRequestNotification
	if err := json.Unmarshal(req.Params, &in); err != nil || len(in.RequestID) == 0 {
		return
	}
	key := mcp.RawIDKey(in.RequestID)
	s.mu.Lock()
	pending := s.pending[key]
	if pending != nil {
		delete(s.pending, key)
	}
	var execution *agentruntime.ExecutionRuntime
	var cancel context.CancelFunc
	for _, rt := range s.sessions {
		rt.cancelMu.Lock()
		if rt.promptID == key {
			cancel = rt.cancel
			execution = rt.execution
		}
		rt.cancelMu.Unlock()
		if cancel != nil || execution != nil {
			break
		}
	}
	s.mu.Unlock()
	if pending != nil {
		pending <- json.RawMessage(`{"outcome":{"outcome":"cancelled"}}`)
	}
	if execution != nil && execution.Cancel() {
		// already cancelled through shared execution state
	} else if cancel != nil {
		cancel()
	}
}

func (s *server) handleCloseSession(req rpcRequest) {
	var in closeSessionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}

	if s.settings != nil {
		mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), in.SessionID)
		if err == nil && mgr.GetHeader() != nil {
			if _, _, workspaceErr := s.resolveWorkspace(in.Meta, mgr.GetHeader().Cwd); workspaceErr != nil {
				s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: workspaceErr.Error()})
				return
			}
		}
	}
	targets, err := s.sessionCascadeIDs(in.SessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	for _, target := range targets {
		if s.settings != nil {
			mgr, openErr := session.OpenByIDExact(s.settings.GetSessionDir(), target)
			if openErr == nil && mgr.GetHeader() != nil {
				if _, _, workspaceErr := s.resolveWorkspace(in.Meta, mgr.GetHeader().Cwd); workspaceErr != nil {
					s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: workspaceErr.Error()})
					return
				}
			}
		}
		if _, err := s.closeSessionRuntime(target); err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
	}
	s.writeResponse(req.ID, map[string]any{}, nil)
}

func (s *server) closeSessionRuntime(sessionID string) (*sessionRuntime, error) {
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt == nil {
		return nil, nil
	}
	if err := s.shutdownSessionRuntime(rt); err != nil {
		return rt, err
	}
	s.mu.Lock()
	if s.sessions[sessionID] == rt {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()
	return rt, nil
}

func (s *server) shutdownSessionRuntime(rt *sessionRuntime) error {
	if rt == nil {
		return nil
	}
	rt.cancelMu.Lock()
	rt.closed = true
	cancel := rt.cancel
	rt.cancelMu.Unlock()
	s.clearSessionDecisionsForRuntime(rt)
	s.clearSubagentProjections(rt.id)
	if rt.runtime != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := rt.runtime.Shutdown(shutdownCtx)
		shutdownCancel()
		if err == nil {
			rt.closeResources()
		}
		return err
	}
	if rt.execution != nil && rt.execution.Cancel() {
		rt.closeResources()
		return nil
	}
	if cancel != nil {
		cancel()
	}
	rt.closeResources()
	return nil
}

// shutdownAllSessionRuntimes is the process-boundary cleanup path for stdin
// EOF and startup/runtime errors. Explicit session/close uses the same bounded
// shutdown function and removes the runtime only after cleanup succeeds.
func (s *server) shutdownAllSessionRuntimes() {
	if s == nil {
		return
	}
	s.mu.Lock()
	runtimes := make([]*sessionRuntime, 0, len(s.sessions))
	for _, rt := range s.sessions {
		runtimes = append(runtimes, rt)
	}
	s.mu.Unlock()
	for _, rt := range runtimes {
		if err := s.shutdownSessionRuntime(rt); err != nil {
			provider.DebugLogf("ACP session %q shutdown: %v", rt.id, err)
		}
		s.mu.Lock()
		if s.sessions[rt.id] == rt {
			delete(s.sessions, rt.id)
		}
		s.mu.Unlock()
	}
}

// sessionCascadeIDs returns a parent followed by all persisted descendants.
// Fork lineage is stored in the shared session index, so adapters do not need
// to maintain a second in-memory child registry.
func (s *server) sessionCascadeIDs(root string) ([]string, error) {
	if s == nil || s.settings == nil {
		return []string{root}, nil
	}
	details, err := session.ListAllDetailed(s.settings.GetSessionDir())
	if err != nil {
		return nil, err
	}
	children := make(map[string][]string)
	for _, detail := range details {
		if detail.ParentSession != "" {
			children[detail.ParentSession] = append(children[detail.ParentSession], detail.ID)
		}
	}
	result := make([]string, 0, 1)
	queue := []string{root}
	seen := map[string]struct{}{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
		queue = append(queue, children[id]...)
	}
	return result, nil
}

func (s *server) handleDeleteSession(req rpcRequest) {
	var in deleteSessionRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "ACP settings are unavailable"})
		return
	}
	mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), in.SessionID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			s.writeResponse(req.ID, map[string]any{}, nil)
			return
		}
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	if _, _, err := s.resolveWorkspace(in.Meta, mgr.GetHeader().Cwd); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: err.Error()})
		return
	}
	targets, err := s.sessionCascadeIDs(in.SessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	for _, id := range targets {
		s.mu.Lock()
		active := s.sessions[id]
		s.mu.Unlock()
		if active != nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "cannot delete an active session"})
			return
		}
		childMgr, openErr := session.OpenByIDExact(s.settings.GetSessionDir(), id)
		if openErr != nil {
			continue
		}
		if _, _, workspaceErr := s.resolveWorkspace(in.Meta, childMgr.GetHeader().Cwd); workspaceErr != nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: workspaceErr.Error()})
			return
		}
	}
	leaseGroup, err := session.AcquireMutations(s.settings.GetSessionDir(), targets)
	if err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "cannot delete an active session"})
		return
	}
	defer leaseGroup.Release()
	for i := len(targets) - 1; i >= 0; i-- {
		if err := agentruntime.DeleteSession(s.settings.GetSessionDir(), targets[i]); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
	}
	s.writeResponse(req.ID, map[string]any{}, nil)
}

func (s *server) handleSetSessionTitle(req rpcRequest) {
	var in setTitleRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.Title) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId and title are required"})
		return
	}
	var mgr *session.Manager
	if rt := s.sessionRuntime(in.SessionID); rt != nil {
		mgr = rt.mgr
	}
	if mgr == nil {
		if s.settings == nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "ACP settings are unavailable"})
			return
		}
		var err error
		mgr, err = session.OpenByIDExact(s.settings.GetSessionDir(), in.SessionID)
		if err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
	}
	if mgr.GetHeader() == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "session header is unavailable"})
		return
	}
	if _, _, err := s.resolveWorkspace(in.Meta, mgr.GetHeader().Cwd); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: err.Error()})
		return
	}
	if err := s.withSessionMutationLease(in.SessionID, func() error {
		_, err := mgr.AppendSessionTitle(in.Title, "manual")
		return err
	}); err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	_ = s.notifySessionInfo(in.SessionID)
	s.writeResponse(req.ID, map[string]any{}, nil)
}

// handleSetSessionWorkDir moves an idle session to an already-authorized
// directory. Runtime state is never mutated in place: resources such as
// context files, skills, tools, sandbox, and MCP are bound to a workdir by
// SessionRuntime, so an open idle runtime is shut down and rebuilt on the
// next session/load.
func (s *server) handleSetSessionWorkDir(req rpcRequest) {
	var in setWorkDirRequest
	if err := json.Unmarshal(req.Params, &in); err != nil || strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.Cwd) == "" {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "sessionId and cwd are required"})
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "ACP settings are unavailable"})
		return
	}

	targetCwd, _, err := s.resolveWorkspace(in.Meta, in.Cwd)
	if err != nil || targetCwd == "" {
		if err == nil {
			err = fmt.Errorf("cwd is required")
		}
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}
	mgr, err := session.OpenByIDExact(s.settings.GetSessionDir(), in.SessionID)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	header := mgr.GetHeader()
	if header == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "session header is unavailable"})
		return
	}
	// Listing the desktop task library is global, but mutations still require
	// the source and target project roots to be in the negotiated window.
	if _, _, err := s.resolveWorkspace(requestMeta{}, header.Cwd); err != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: err.Error()})
		return
	}
	if filepath.Clean(header.Cwd) == targetCwd {
		s.writeResponse(req.ID, setWorkDirResult{Cwd: targetCwd}, nil)
		return
	}
	if activeRun, activeErr := agentruntime.GetActiveDurableRun(context.Background(), s.settings.GetSessionDir(), in.SessionID); activeErr != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(activeErr, nil, agentruntime.PhasePersistence))
		return
	} else if activeRun != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "cannot change the work directory while the session is running"})
		return
	}

	if rt := s.sessionRuntime(in.SessionID); rt != nil {
		if _, active := rt.execution.Active(); active {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "cannot change the work directory while the session is running"})
			return
		}
		if _, err := s.closeSessionRuntime(in.SessionID); err != nil {
			s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
			return
		}
	}
	if err := s.withSessionMutationLease(in.SessionID, func() error {
		return mgr.SetWorkDir(targetCwd)
	}); err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	_ = s.notifySessionInfo(in.SessionID)
	s.writeResponse(req.ID, setWorkDirResult{Cwd: targetCwd}, nil)
}

const sessionListPageSize = 50

func encodeSessionCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("acp-v1:%d", offset)))
}

func decodeSessionCursor(cursor string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "acp-v1:") {
		return 0, fmt.Errorf("invalid session cursor")
	}
	offset, err := strconv.Atoi(strings.TrimPrefix(string(raw), "acp-v1:"))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid session cursor")
	}
	return offset, nil
}

func (s *server) handleListSessions(req rpcRequest) {
	var in listSessionsRequest
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &in) != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	cwd, additionalDirectories, err := s.resolveWorkspace(in.Meta, in.Cwd, in.AdditionalDirectories)
	if err != nil || cwd == "" {
		if err == nil {
			err = fmt.Errorf("cwd is required")
		}
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: err.Error()})
		return
	}

	details, err := session.ListAllDetailed(s.settings.GetSessionDir())
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	allowedRoots := map[string]struct{}{filepath.Clean(cwd): {}}
	for _, directory := range additionalDirectories {
		allowedRoots[filepath.Clean(directory)] = struct{}{}
	}
	filtered := details[:0]
	for _, detail := range details {
		if _, ok := allowedRoots[filepath.Clean(detail.Cwd)]; ok {
			filtered = append(filtered, detail)
		}
	}
	details = filtered
	s.writeSessionList(req, details, in.Cursor)
}

// handleListAllSessions projects the persisted task library across every
// project. It is additive to ACP's standard session/list endpoint, whose cwd
// filter remains a negotiated-workspace safety boundary for generic ACP
// clients. Consumers still need to authorize a session directory before they
// load, fork, mutate, or prompt that session.
func (s *server) handleListAllSessions(req rpcRequest) {
	var in listSessionsRequest
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &in) != nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid params"})
		return
	}
	if s.settings == nil {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32000, Message: "ACP settings are unavailable"})
		return
	}
	details, err := session.ListAllDetailed(s.settings.GetSessionDir())
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	details, err = s.filterGlobalSessionList(details, in)
	if err != nil {
		s.writeResponse(req.ID, nil, acpFailureRPCError(err, nil, agentruntime.PhasePersistence))
		return
	}
	s.writeSessionList(req, details, in.Cursor)
}

// filterGlobalSessionList narrows mothx/session/listAll before cursor
// pagination. The catalog remains a Runtime/session projection: this helper
// only combines persisted session details, canonical session metadata, and
// canonical project names for a read-only adapter query.
func (s *server) filterGlobalSessionList(details []session.SessionDetail, in listSessionsRequest) ([]session.SessionDetail, error) {
	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	projectID := strings.TrimSpace(in.ProjectID)
	if scope == "" {
		scope = "all"
	}
	if scope == "all" && projectID != "" {
		scope = "project"
	}
	if scope != "all" && scope != "project" && scope != "ungrouped" {
		return nil, fmt.Errorf("invalid session list scope %q", in.Scope)
	}
	if scope == "project" && projectID == "" {
		return nil, fmt.Errorf("projectId is required for project session list scope")
	}
	if scope == "ungrouped" && projectID != "" {
		return nil, fmt.Errorf("projectId is not valid for ungrouped session list scope")
	}

	query := strings.ToLower(strings.TrimSpace(in.Query))
	if scope == "all" && query == "" {
		return details, nil
	}

	ids := make([]string, 0, len(details))
	for _, detail := range details {
		ids = append(ids, detail.ID)
	}
	metadata := s.sessionListMetadata(ids)
	projectNames := map[string]string{}
	if query != "" {
		projects, err := session.ListProjects(s.settings.GetSessionDir())
		if err != nil {
			return nil, fmt.Errorf("list projects for session search: %w", err)
		}
		for _, project := range projects {
			projectNames[project.ID] = project.Name
		}
	}

	filtered := make([]session.SessionDetail, 0, len(details))
	for _, detail := range details {
		meta := metadata[detail.ID]
		switch scope {
		case "project":
			if meta.ProjectID != projectID {
				continue
			}
		case "ungrouped":
			if meta.ProjectID != "" {
				continue
			}
		}
		if query != "" {
			title := detail.Name
			if title == "" {
				title = detail.Preview
			}
			if !strings.Contains(strings.ToLower(detail.ID), query) &&
				!strings.Contains(strings.ToLower(title), query) &&
				!strings.Contains(strings.ToLower(detail.Cwd), query) &&
				!strings.Contains(strings.ToLower(projectNames[meta.ProjectID]), query) {
				continue
			}
		}
		filtered = append(filtered, detail)
	}
	return filtered, nil
}

func (s *server) writeSessionList(req rpcRequest, details []session.SessionDetail, cursor string) {
	offset := 0
	if cursor != "" {
		var err error
		offset, err = decodeSessionCursor(cursor)
		if err != nil {
			s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid cursor"})
			return
		}
	}
	if offset > len(details) {
		s.writeResponse(req.ID, nil, &mcp.RPCError{Code: -32602, Message: "invalid cursor"})
		return
	}

	end := offset + sessionListPageSize
	if end > len(details) {
		end = len(details)
	}
	pageIDs := make([]string, 0, end-offset)
	for _, detail := range details[offset:end] {
		pageIDs = append(pageIDs, detail.ID)
	}
	// Additive _meta projections: latest durable run status and persisted
	// pin/project metadata, both resolved in one pass for the current page
	// through Runtime/session read-only projections.
	lastRuns := s.sessionListLastRun(pageIDs)
	pageMetadata := s.sessionListMetadata(pageIDs)
	result := listSessionsResult{Sessions: make([]listedSession, 0, end-offset)}
	for _, detail := range details[offset:end] {
		title := detail.Name
		if title == "" {
			title = detail.Preview
		}
		modelProvider, modelID, mode, thoughtLevel := "", "", "", ""
		if binding, ok, bindingErr := session.LatestModelChangeByID(s.settings.GetSessionDir(), detail.ID); bindingErr == nil && ok {
			modelProvider, modelID = binding.Provider, binding.ModelID
		}
		if mgr, openErr := session.OpenByIDExact(s.settings.GetSessionDir(), detail.ID); openErr == nil {
			if modeEntry, ok := mgr.GetLatestModeChange(); ok {
				mode = modeEntry.Mode
			}
			if thinkingEntry, ok := mgr.GetLatestThinkingLevelChange(); ok {
				thoughtLevel = thinkingEntry.ThinkingLevel
			}
		}
		if modelProvider == "" {
			modelProvider = s.providerName
		}
		if modelID == "" && s.m != nil {
			modelID = s.m.ID
		}
		meta := map[string]any{"messageCount": detail.MessageCount}
		metadata := pageMetadata[detail.ID]
		meta["pinned"] = metadata.Pinned
		if metadata.ProjectID != "" {
			meta["projectId"] = metadata.ProjectID
		} else {
			meta["projectId"] = nil
		}
		// Sessions without any durable Run carry no lastRun key at all.
		if lastRun, ok := lastRuns[detail.ID]; ok {
			meta["lastRun"] = lastRun
		}
		result.Sessions = append(result.Sessions, listedSession{
			SessionID: detail.ID,
			Cwd:       detail.Cwd,
			AdditionalDirectories: func() []string {
				directories, err := session.LatestAdditionalDirectoriesByID(s.settings.GetSessionDir(), detail.ID)
				if err != nil {
					return []string{}
				}
				return directories
			}(),
			Title:           title,
			Provider:        modelProvider,
			Model:           modelID,
			Mode:            mode,
			ThoughtLevel:    thoughtLevel,
			ParentSessionID: detail.ParentSession,
			UpdatedAt:       detail.ModTime.UTC().Format(time.RFC3339),
			Meta:            meta,
		})
	}
	if end < len(details) {
		result.NextCursor = encodeSessionCursor(end)
	}
	s.writeResponse(req.ID, result, nil)
}

func (s *server) sessionForPrompt(sessionID string) *sessionRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

func (s *server) handleAgentEvent(sessionID string, ev agentpkg.Event) {
	if ev.AgentID != "" {
		// Project child-agent lifecycle as additive subagent session events.
		// Child text/tool events below continue to render on the parent
		// session stream (single stream); child terminal events never mutate
		// parent run facts.
		s.observeSubagentEvent(sessionID, ev)
	}
	switch ev.Type {
	case agentpkg.EventHostedItem:
		if ev.HostedItem != nil {
			status := ev.HostedItem.Status
			if status == "" {
				status = "updated"
			}
			title := ev.HostedItem.Type
			if title == "" {
				title = "hosted item"
			}
			s.notify(sessionID, sessionUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    ev.HostedItem.ID,
				Title:         fmt.Sprintf("%s: %s", title, status),
				Kind:          "other",
				Status:        acpHostedStatus(status),
			})
		}
	case agentpkg.EventTextDelta:
		messageID := s.streamMessageID(sessionID, false, ev.TextDelta)
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "agent_message_chunk",
			MessageID:     messageID,
			Content:       &contentBlock{Type: "text", Text: ev.TextDelta},
		})
	case agentpkg.EventThinkDelta:
		messageID := s.streamMessageID(sessionID, true, ev.ThinkDelta)
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "agent_thought_chunk",
			MessageID:     messageID,
			Content:       &contentBlock{Type: "text", Text: ev.ThinkDelta},
		})
	case agentpkg.EventToolCall:
		if ev.ToolCall != nil {
			title := s.rememberToolTitle(ev.ToolCall.ID, ev.ToolCall.Name, ev.ToolArgs)
			s.notify(sessionID, sessionUpdate{
				SessionUpdate: "tool_call",
				ToolCallID:    ev.ToolCall.ID,
				Title:         title,
				Kind:          acpToolKind(ev.ToolCall.Name),
				Status:        "pending",
				RawInput:      toolRawInput(ev.ToolArgs),
			})
		}
	case agentpkg.EventToolExecutionStart:
		title := s.rememberToolTitle(ev.ToolCallID, ev.ToolName, ev.ToolArgs)
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ev.ToolCallID,
			Title:         title,
			Kind:          acpToolKind(ev.ToolName),
			Status:        "in_progress",
			RawInput:      toolRawInput(ev.ToolArgs),
		})
	case agentpkg.EventToolExecutionEnd:
		status := "completed"
		if ev.ToolError != nil {
			status = "failed"
		}
		toolContent := ev.ToolResult
		rawOutput := map[string]any{"content": toolContent}
		if ev.ToolError != nil {
			info := acpFailureInfo(ev.ToolError, nil, agentruntime.PhaseTool)
			toolContent = agentruntime.DisplayErrorMessage(info)
			rawOutput["content"] = toolContent
			rawOutput["errorInfo"] = info
		}
		if ev.ToolDiff != nil {
			rawOutput["diff"] = ev.ToolDiff
		}
		var toolContents []toolCallContent
		if text := strings.TrimSpace(toolContent); text != "" {
			toolContents = append(toolContents, toolCallContent{Type: "content", Content: &contentBlock{Type: "text", Text: text}})
		}
		if len(ev.ToolImages) > 0 {
			toolContents = append(toolContents, acpToolImageContents(ev.ToolImages)...)
		}
		if ev.ToolDiff != nil {
			toolContents = append(toolContents, toolCallContent{Type: "diff", Path: ev.ToolDiff.Path, OldText: ev.ToolDiff.OldText, NewText: ev.ToolDiff.NewText})
		}
		locations := toolCallLocations(ev.ToolDiff)
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ev.ToolCallID,
			Title:         s.toolTitleFor(ev.ToolCallID, ev.ToolName),
			Kind:          acpToolKind(ev.ToolName),
			Status:        status,
			Content:       toolContents,
			Locations:     locations,
			RawOutput:     rawOutput,
		})
	case agentpkg.EventToolExecutionUpdate:
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    ev.ToolCallID,
			Content:       textToolContent(fmt.Sprint(ev.PartialResult)),
		})
	case agentpkg.EventToolResult:
	case agentpkg.EventPlanUpdate:
		if ev.Plan != nil {
			s.notify(sessionID, sessionUpdate{
				SessionUpdate: "plan",
				Entries:       acpPlanEntries(ev.Plan),
				Meta:          acpPlanMeta(ev.Plan),
			})
		}
	case agentpkg.EventUsage:
		s.emitUsageUpdate(sessionID, ev, true)
	case agentpkg.EventDone:
		s.emitUsageUpdate(sessionID, ev, false)
	case agentpkg.EventError, agentpkg.EventRunFinished:
		// Terminal errors are projected as the same structured Runtime
		// contract used by the prompt response and durable replay. Keep this
		// notification adapter-neutral; ACP clients may ignore the extension
		// while newer clients can render retry and safety actions without
		// parsing provider text. Child-agent terminal events were already
		// projected as subagent lifecycle events above and must not produce a
		// parent terminal projection.
		if ev.AgentID != "" {
			return
		}
		if !s.markTerminalNotified(sessionID) {
			return
		}
		status := "failed"
		var info *agentruntime.ErrorInfo
		if ev.Type == agentpkg.EventRunFinished {
			switch ev.Status {
			case agentpkg.TaskSuccess:
				status = "completed"
			case agentpkg.TaskCanceled:
				status = "cancelled"
			case agentpkg.TaskIncomplete:
				status = "incomplete"
			}
		}
		if status != "completed" {
			classified := acpFailureInfo(ev.Error, nil, agentruntime.PhaseModel)
			if ev.Status == agentpkg.TaskCanceled {
				classified = agentruntime.ClassifyError(context.Canceled, agentruntime.ErrorClassificationOptions{Phase: agentruntime.PhaseModel})
			} else if ev.Status == agentpkg.TaskIncomplete {
				classified.Code = "run_incomplete"
				classified.Type = "incomplete_error"
				classified.FailureClass = agentruntime.FailureIncomplete
				classified.Phase = agentruntime.PhaseModel
				classified.MessageKey = "run.error.incomplete"
				classified.Message = "The run ended before it could complete."
				classified.RetryMode = agentruntime.RetryUser
				classified.Retryable = true
			}
			info = &classified
		}
		params := map[string]any{"sessionId": sessionID, "event": "terminal", "status": status}
		if info != nil {
			params["errorInfo"] = *info
			params["error"] = agentruntime.DisplayErrorMessage(*info)
		}
		s.notifyExtension("_mothx/session_event", params)
	case agentpkg.EventRetry:
		s.notifyExtension("_mothx/session_event", acpRetryEvent(sessionID, ev))
	case agentpkg.EventStatus:
		if ev.RetryStatus {
			return
		}
		s.notifyExtension("_mothx/session_event", map[string]any{
			"sessionId": sessionID,
			"event":     "status",
			"message":   ev.StatusMessage,
		})
	case agentpkg.EventTurnStart:
		// A model turn begins after any preceding tool executions. Assigning a
		// new ID here keeps its streamed text/thought separate from earlier
		// turns and leaves the tool cards at their canonical transcript point.
		s.advanceStreamSegment(sessionID)
		s.notifyExtension("_mothx/session_event", map[string]any{
			"sessionId": sessionID,
			"event":     acpEventName(ev.Type),
			"message":   ev.StatusMessage,
		})
	case agentpkg.EventCompactionStart, agentpkg.EventCompactionEnd, agentpkg.EventTurnEnd:
		s.notifyExtension("_mothx/session_event", map[string]any{
			"sessionId": sessionID,
			"event":     acpEventName(ev.Type),
			"message":   ev.StatusMessage,
		})
	}
}

func toolCallLocations(diff *agentpkg.FileDiff) []toolCallLocation {
	if diff == nil || !filepath.IsAbs(diff.Path) {
		return nil
	}
	return []toolCallLocation{{Path: diff.Path}}
}

func acpStreamMessageID(sessionID, promptID, kind string, segment int) string {
	return fmt.Sprintf("acp_%s_%s_%s_%d", sessionID, promptID, kind, segment)
}

// advanceStreamSegment starts a new model turn. Tool calls already have
// stable toolCallId values, while the following text/thought chunks need a
// new messageId to preserve their transcript position in ACP clients.
func (s *server) advanceStreamSegment(sessionID string) {
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt == nil {
		return
	}
	rt.cancelMu.Lock()
	defer rt.cancelMu.Unlock()
	if rt.promptID == "" || rt.messageID == "" {
		return
	}
	rt.streamSegment++
	rt.messageID = acpStreamMessageID(rt.id, rt.promptID, "message", rt.streamSegment)
	rt.thoughtMessageID = acpStreamMessageID(rt.id, rt.promptID, "thought", rt.streamSegment)
}

// streamMessageID returns the active model turn's stable ACP message ID.
// Fixture sessions that emit events without a prompt still receive a
// deterministic ID so their wire payload remains schema-valid.
func (s *server) streamMessageID(sessionID string, thought bool, _ string) string {
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt != nil {
		rt.cancelMu.Lock()
		defer rt.cancelMu.Unlock()
		if thought && rt.thoughtMessageID != "" {
			return rt.thoughtMessageID
		}
		if !thought && rt.messageID != "" {
			return rt.messageID
		}
	}
	kind := "message"
	if thought {
		kind = "thought"
	}
	digest := sha256.Sum256([]byte(sessionID + "\x00" + kind))
	return fmt.Sprintf("acp_%s_%x", kind, digest[:8])
}

func (s *server) markTerminalNotified(sessionID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rt := s.sessions[sessionID]
	if rt == nil || rt.terminalNotified {
		return false
	}
	rt.terminalNotified = true
	return true
}

func acpRetryEvent(sessionID string, ev agentpkg.Event) map[string]any {
	return map[string]any{
		"sessionId":    sessionID,
		"event":        "retrying",
		"message":      acpRetryMessage(ev),
		"attempt":      ev.RetryAttempt,
		"maxAttempts":  ev.RetryMaxAttempts,
		"retryAfterMs": ev.RetryAfterMS,
	}
}

// acpRetryMessage deliberately uses the structured retry fields instead of
// RetryReason, which may contain provider-specific or otherwise unsafe detail.
func acpRetryMessage(ev agentpkg.Event) string {
	if ev.RetryAttempt > 0 && ev.RetryMaxAttempts > 0 {
		message := fmt.Sprintf("Retrying (attempt %d/%d)", ev.RetryAttempt, ev.RetryMaxAttempts)
		if ev.RetryAfterMS > 0 {
			message += fmt.Sprintf("; waiting %s", time.Duration(ev.RetryAfterMS)*time.Millisecond)
		}
		return message + "..."
	}
	return "Retrying..."
}

func acpHostedStatus(status string) string {
	switch status {
	case "completed", "incomplete", "expired", "failed", "cancelled", "canceled":
		return status
	default:
		return "in_progress"
	}
}

func acpEventName(eventType agentpkg.EventType) string {
	switch eventType {
	case agentpkg.EventCompactionStart:
		return "compaction_started"
	case agentpkg.EventCompactionEnd:
		return "compaction_finished"
	case agentpkg.EventTurnStart:
		return "turn_started"
	case agentpkg.EventTurnEnd:
		return "turn_finished"
	default:
		return "unknown"
	}
}

func acpToolKind(name string) string {
	switch name {
	case "read", "ls":
		return "read"
	case "write", "edit":
		return "edit"
	case "grep", "find":
		return "search"
	case "bash":
		return "execute"
	case "plan":
		return "think"
	default:
		return "other"
	}
}

func textToolContent(text string) []toolCallContent {
	if text == "" {
		return nil
	}
	return []toolCallContent{{Type: "content", Content: &contentBlock{Type: "text", Text: text}}}
}

func acpPlanEntries(plan *agentpkg.TaskPlan) []planEntry {
	entries := make([]planEntry, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		status := "pending"
		switch step.Status {
		case "running":
			status = "in_progress"
		case "done", "failed":
			status = "completed"
		}
		entries = append(entries, planEntry{Content: step.Title, Priority: "medium", Status: status})
	}
	return entries
}

func acpPlanMeta(plan *agentpkg.TaskPlan) map[string]any {
	if plan.Title == "" && plan.Note == "" {
		return nil
	}
	return map[string]any{mothxExtensionNamespace: map[string]string{"title": plan.Title, "note": plan.Note}}
}

func (s *server) emitUsageUpdate(sessionID string, ev agentpkg.Event, addCost bool) {
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt == nil {
		return
	}

	rt.cancelMu.Lock()
	model := rt.activeModel
	rt.cancelMu.Unlock()
	if model == nil {
		model = runtimeModel(rt.runtime)
	}
	if model == nil {
		model = s.m
	}
	used, size := usageContext(ev.ContextUsage, ev.Usage, model)
	rt.usageMu.Lock()
	if addCost && ev.Usage != nil {
		if model != nil {
			ev.Usage.CalculateCost(model.Cost.Input, model.Cost.Output, model.Cost.CacheRead, model.Cost.CacheWrite)
		}
		rt.cost += ev.Usage.Cost.Total
	}
	cost := rt.cost
	rt.usageMu.Unlock()

	update := sessionUpdate{
		SessionUpdate: "usage_update",
		Used:          &used,
		Size:          &size,
	}
	if cost > 0 {
		update.Cost = &usageCost{Amount: cost, Currency: "USD"}
	}
	s.notify(sessionID, update)
}

func runtimeModel(runtime *agentruntime.SessionRuntime) *provider.Model {
	if runtime == nil {
		return nil
	}
	_, _, model, _, _ := runtime.ConfigSnapshot()
	return model
}

func usageContext(contextUsage *agentpkg.ContextUsage, usage *agentpkg.Usage, model *provider.Model) (int, int) {
	used := 0
	size := 0
	if contextUsage != nil {
		used = contextUsage.TotalTokens
		if used == 0 {
			used = contextUsage.Tokens
		}
		size = contextUsage.ContextWindow
	}
	if used == 0 && usage != nil {
		used = usage.TotalTokens
		if used == 0 {
			used = usage.InputTokens + usage.CacheRead + usage.CacheWrite + usage.OutputTokens
		}
	}
	if size == 0 && model != nil {
		size = model.ContextWindow
	}
	return used, size
}

func (s *server) persistedSessionCost(mgr *session.Manager, model *provider.Model) float64 {
	if mgr == nil {
		return 0
	}
	var total float64
	for _, msg := range mgr.GetMessages() {
		if msg.Usage == nil {
			continue
		}
		if msg.Usage.Cost.Total == 0 && model != nil {
			msg.Usage.CalculateCost(model)
		}
		total += msg.Usage.Cost.Total
	}
	return total
}

func formatACPPlan(plan *agentpkg.TaskPlan) string {
	if plan == nil || len(plan.Steps) == 0 {
		return "Plan updated."
	}
	var b strings.Builder
	title := plan.Title
	if title == "" {
		title = "Plan"
	}
	b.WriteString(title)
	for _, step := range plan.Steps {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%s %s", planStatusMarker(step.Status), step.Title))
	}
	if plan.Note != "" {
		b.WriteString("\nnote: " + plan.Note)
	}
	return b.String()
}

func planStatusMarker(status string) string {
	switch status {
	case "running":
		return ">"
	case "done":
		return "x"
	case "failed":
		return "!"
	default:
		return "-"
	}
}

func (s *server) buildMCPCallbacks(sessionID string) mcp.Callbacks {
	return mcp.Callbacks{
		OnNotification: func(serverName, method string, params json.RawMessage) {
			s.handleMCPNotification(sessionID, serverName, method, params)
		},
		OnSamplingCreateMessage: func(ctx context.Context, serverName string, params json.RawMessage) (json.RawMessage, *mcp.RPCError) {
			return s.handleMCPSamplingCreateMessage(ctx, sessionID, serverName, params)
		},
	}
}

func (s *server) handleMCPNotification(sessionID, serverName, method string, params json.RawMessage) {
	callID := "mcp-notify-" + mcp.SanitizeToolName(serverName)
	title := "mcp_notification: " + serverName
	s.mu.Lock()
	if !s.mcpNotify[callID] {
		s.mcpNotify[callID] = true
		s.mu.Unlock()
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "tool_call",
			ToolCallID:    callID,
			Title:         title,
			Kind:          "other",
			Status:        "pending",
		})
	} else {
		s.mu.Unlock()
	}

	rawOut := map[string]any{
		"method": method,
	}
	if parsed := parseJSONRawToMap(params); parsed != nil {
		rawOut["params"] = parsed
	} else if trimmed := strings.TrimSpace(string(params)); trimmed != "" && trimmed != "null" {
		rawOut["paramsText"] = trimmed
	}

	switch method {
	case "notifications/progress", "notifications/message", "logging/message", "notifications/cancelled":
		s.notify(sessionID, sessionUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    callID,
			Title:         title,
			Status:        "in_progress",
			RawOutput:     rawOut,
		})
	}
}

func (s *server) handleMCPSamplingCreateMessage(ctx context.Context, sessionID, serverName string, params json.RawMessage) (json.RawMessage, *mcp.RPCError) {
	rt := s.sessionRuntime(sessionID)
	if rt == nil || rt.runtime == nil {
		return nil, &mcp.RPCError{Code: -32000, Message: "unknown session"}
	}
	p, _, model, _, thinking := rt.runtime.ConfigSnapshot()
	if p == nil || model == nil {
		return nil, &mcp.RPCError{Code: -32000, Message: "session model is unavailable"}
	}
	prompt, systemPrompt, maxTokens := extractSamplingInput(params)
	if strings.TrimSpace(prompt) == "" {
		return nil, &mcp.RPCError{Code: -32602, Message: "sampling/createMessage requires non-empty messages"}
	}
	if maxTokens <= 0 {
		maxTokens = agent.ResolveMaxTokens(model)
	}
	modelID := model.ID
	chatCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	events := p.Chat(chatCtx, provider.ChatParams{
		Messages:      []provider.Message{provider.NewUserMessage(prompt)},
		SystemPrompt:  systemPrompt,
		ThinkingLevel: thinking,
		MaxTokens:     maxTokens,
		Temperature:   config.NormalizeSamplingPtr(model.Temperature),
		TopP:          config.NormalizeSamplingPtr(model.TopP),
		ModelID:       modelID,
	})
	var outText strings.Builder
	for ev := range events {
		switch ev.Type {
		case provider.StreamTextDelta:
			outText.WriteString(ev.TextDelta)
		case provider.StreamDone:
			// noop
		case provider.StreamError:
			if ev.Error != nil {
				log.Printf("[acp] MCP sampling provider error for %s: %v", serverName, ev.Error)
				return nil, acpFailureRPCError(ev.Error, nil, agentruntime.PhaseModel)
			}
		}
	}
	text := strings.TrimSpace(outText.String())
	if text == "" {
		text = "(empty response)"
	}
	result := map[string]any{
		"model": modelID,
		"role":  "assistant",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, acpFailureRPCError(err, nil, agentruntime.PhaseTransport)
	}
	s.notify(sessionID, sessionUpdate{
		SessionUpdate: "agent_message_chunk",
		Content:       &contentBlock{Type: "text", Text: "MCP[" + serverName + "] sampling/createMessage completed"},
	})
	return data, nil
}

// acpFailureInfo is a presentation adapter for the shared Runtime contract.
// It accepts a Runtime observation when one exists, so tool/output safety
// facts are preserved instead of being inferred again by ACP.
func acpFailureInfo(err error, observed *agentruntime.ErrorInfo, phase agentruntime.RunPhase) agentruntime.ErrorInfo {
	if observed != nil && strings.TrimSpace(agentruntime.DisplayErrorMessage(*observed)) != "" {
		return *observed
	}
	return agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{Phase: phase})
}

func acpFailureRPCError(err error, observed *agentruntime.ErrorInfo, phase agentruntime.RunPhase) *mcp.RPCError {
	var mismatch *sessionProviderMismatchError
	if errors.As(err, &mismatch) {
		return &mcp.RPCError{Code: -32002, Message: "session provider mismatch", Data: map[string]any{
			"sessionProvider": mismatch.SessionProvider,
			"sessionModel":    mismatch.SessionModel,
			"currentProvider": mismatch.CurrentProvider,
		}}
	}
	info := acpFailureInfo(err, observed, phase)
	message := strings.TrimSpace(agentruntime.DisplayErrorMessage(info))
	if message == "" {
		message = "The run could not be completed."
	}
	// MCP/JSON-RPC clients receive the complete safe contract in Data. The
	// legacy code/message fields remain for clients that do not understand the
	// extension, while Detail carries the bounded provider diagnostic.
	return &mcp.RPCError{Code: -32000, Message: message, Data: map[string]any{
		"code":            info.Code,
		"type":            info.Type,
		"failureClass":    info.FailureClass,
		"phase":           info.Phase,
		"messageKey":      info.MessageKey,
		"detail":          info.Detail,
		"retryMode":       info.RetryMode,
		"retryable":       info.Retryable,
		"retryAfterMs":    info.RetryAfterMS,
		"attempt":         info.Attempt,
		"maxAttempts":     info.MaxAttempts,
		"sideEffectState": info.SideEffectState,
		"partialOutput":   info.PartialOutput,
		"runId":           info.RunID,
		"intentId":        info.IntentID,
		"requestId":       info.RequestID,
	}}
}

func extractSamplingPrompt(params json.RawMessage) string {
	prompt, _, _ := extractSamplingInput(params)
	return prompt
}

func extractSamplingInput(params json.RawMessage) (prompt string, systemPrompt string, maxTokens int) {
	maxTokens = 0
	if len(params) == 0 {
		return "", "", maxTokens
	}
	var raw map[string]any
	if err := json.Unmarshal(params, &raw); err != nil {
		return strings.TrimSpace(string(params)), "", maxTokens
	}
	if v, ok := raw["maxTokens"].(float64); ok && int(v) > 0 {
		maxTokens = int(v)
	}
	msgs, _ := raw["messages"].([]any)
	var parts []string
	for _, m := range msgs {
		msgMap, ok := m.(map[string]any)
		if !ok {
			continue
		}
		content := msgMap["content"]
		role, _ := msgMap["role"].(string)
		switch v := content.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				if role == "system" {
					if systemPrompt == "" {
						systemPrompt = v
					}
					continue
				}
				parts = append(parts, v)
			}
		case []any:
			var blockTexts []string
			for _, item := range v {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := block["type"].(string); t == "text" {
					if txt, _ := block["text"].(string); strings.TrimSpace(txt) != "" {
						blockTexts = append(blockTexts, txt)
					}
				}
			}
			if len(blockTexts) == 0 {
				continue
			}
			joined := strings.Join(blockTexts, "\n")
			if role == "system" {
				if systemPrompt == "" {
					systemPrompt = joined
				}
				continue
			}
			parts = append(parts, joined)
		}
	}
	return strings.Join(parts, "\n"), systemPrompt, maxTokens
}

func parseJSONRawToMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// handleQuestion routes MothX's interactive question tool through its own ACP
// extension instead of overloading the standard permission request.
func (s *server) handleQuestion(ctx context.Context, rt *sessionRuntime, runID string, ev agentpkg.Event) {
	qh, ok := rt.agent.(agentpkg.QuestionHandler)
	if !ok {
		return
	}
	if rt.execution == nil || rt.execution.WaitForQuestion(runID) != nil {
		qh.HandleQuestionResponse(ev.QuestionID, "")
		return
	}
	defer func() { _ = rt.execution.Resume(runID) }()
	answer := s.requestQuestion(ctx, rt.id, ev.QuestionText, ev.QuestionOptions, ev.QuestionContext)
	qh.HandleQuestionResponse(ev.QuestionID, answer)
}

func (s *server) sessionRunID(sessionID string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt == nil {
		return ""
	}
	rt.cancelMu.Lock()
	defer rt.cancelMu.Unlock()
	if rt.runID != "" {
		return rt.runID
	}
	// Keep fixtures and legacy callers that only set promptID functional while
	// they migrate to the canonical durable run identity.
	return rt.promptID
}

func (s *server) loadPersistedDecisionRecords(sessionID string) ([]agentruntime.DecisionRecord, error) {
	if s == nil || s.settings == nil || sessionID == "" {
		return nil, nil
	}
	events, err := session.ListSessionRunEvents(s.settings.GetSessionDir(), sessionID)
	if err != nil {
		return nil, err
	}
	records := make([]agentruntime.DecisionRecord, 0)
	for _, ev := range events {
		if !strings.HasPrefix(ev.EventType, "decision_") {
			continue
		}
		var envelope struct {
			Decision agentruntime.DecisionRecord `json:"decision"`
		}
		if json.Unmarshal(ev.Data, &envelope) == nil && envelope.Decision.ID != "" {
			if envelope.Decision.SessionID == "" {
				envelope.Decision.SessionID = sessionID
			}
			if envelope.Decision.RunID == "" {
				envelope.Decision.RunID = ev.RunID
			}
			records = append(records, envelope.Decision)
		}
	}
	return records, nil
}

func (s *server) replayPendingDecisionRequests(sessionID string) error {
	records, err := s.loadPersistedDecisionRecords(sessionID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt == nil || rt.decisions == nil {
		return nil
	}
	rehydrated := make(map[string]struct{})
	for _, request := range rt.decisions.Pending() {
		rehydrated[request.ID] = struct{}{}
	}
	pending := agentruntime.ReplayDecisions(records)
	for id, record := range pending {
		if _, ok := rehydrated[id]; !ok {
			continue
		}
		if record.Payload == nil || len(record.Payload) == 0 {
			continue
		}
		s.mu.Lock()
		if _, exists := s.pending[id]; !exists {
			s.pending[id] = make(chan json.RawMessage, 1)
		}
		s.mu.Unlock()
		switch record.Kind {
		case agentruntime.DecisionQuestion:
			var request questionRequest
			if err := json.Unmarshal(record.Payload, &request); err != nil {
				continue
			}
			method, payload := s.questionProjection(request)
			if request.Protocol == acpElicitationFormProtocol && s.supportsElicitationForm() {
				method = "elicitation/create"
				payload = elicitationRequestForQuestion(request)
			}
			if err := s.notifyRequest(id, method, payload); err != nil {
				return err
			}
		case agentruntime.DecisionApproval:
			var request requestPermissionRequest
			if err := json.Unmarshal(record.Payload, &request); err != nil {
				continue
			}
			if err := s.notifyRequest(id, "session/request_permission", request); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *server) rehydrateSessionDecisions(rt *sessionRuntime) error {
	if s == nil || rt == nil || rt.decisions == nil || s.settings == nil {
		return nil
	}
	records, err := s.loadPersistedDecisionRecords(rt.id)
	if err != nil {
		return fmt.Errorf("load persisted decisions for session %s: %w", rt.id, err)
	}
	now := time.Now()
	expired := agentruntime.ExpiredDecisions(records, now)
	pending := agentruntime.ReplayDecisionsAt(records, now)
	activeRunID, active := rt.execution.Active()
	if len(expired) > 0 || (!active && len(pending) > 0) {
		if err := s.withSessionMutationLease(rt.id, func() error {
			for _, record := range expired {
				if err := s.persistDecisionRecord(rt.id, record.RunID, record.ID, record.Kind, "timed_out", "", map[string]any{"reason": "decision expired while session was offline"}); err != nil {
					return fmt.Errorf("terminalize expired decision %s: %w", record.ID, err)
				}
			}
			if !active {
				for _, record := range pending {
					if err := s.persistDecisionRecord(rt.id, record.RunID, record.ID, record.Kind, "cancelled", "", map[string]any{"reason": "decision execution was not recoverable after session restore"}); err != nil {
						return fmt.Errorf("terminalize unrecoverable decision %s: %w", record.ID, err)
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if !active {
		return nil
	}
	activeRecords := make([]agentruntime.DecisionRecord, 0, len(pending))
	for _, record := range pending {
		if record.RunID == activeRunID {
			activeRecords = append(activeRecords, record)
		}
	}
	_, err = rt.decisions.Rehydrate(activeRecords)
	return err
}

func (s *server) registerDecision(sessionID, id string, kind agentruntime.DecisionKind) {
	if s == nil || id == "" {
		return
	}
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt == nil {
		return
	}
	if rt.decisions == nil {
		rt.decisions = &agentruntime.DecisionService{}
	}
	runID := s.sessionRunID(sessionID)
	_ = rt.decisions.Register(agentruntime.DecisionRequest{ID: id, RunID: runID, SessionID: sessionID, Kind: kind})
}

func (s *server) resolveDecision(sessionID, id string, kind agentruntime.DecisionKind, value, status string) error {
	if s == nil || id == "" {
		return nil
	}
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt != nil && rt.decisions != nil {
		runID := s.sessionRunID(sessionID)
		_, err := rt.decisions.ResolveWith(agentruntime.DecisionResolution{ID: id, Kind: kind, Status: status, Value: value}, func(_ agentruntime.DecisionRequest) error {
			return s.persistDecisionRecord(sessionID, runID, id, kind, status, value, map[string]any{"value": value})
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *server) clearSessionDecisions(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	s.clearSessionDecisionsForRuntime(rt)
}

func (s *server) clearSessionDecisionsForRuntime(rt *sessionRuntime) {
	if rt == nil || rt.decisions == nil {
		return
	}
	rt.cancelMu.Lock()
	runID := rt.runID
	if runID == "" {
		runID = rt.promptID
	}
	rt.cancelMu.Unlock()
	for _, request := range rt.decisions.ClearRunWithValue(runID, "") {
		s.persistDecisionRecord(rt.id, runID, request.ID, request.Kind, "cancelled", "", map[string]any{"reason": "ACP session closed before the decision was resolved"})
	}
}

func (s *server) persistDecisionRecord(sessionID, runID, id string, kind agentruntime.DecisionKind, status, value string, payload any) error {
	return s.persistDecisionRecordWithDeadline(sessionID, runID, id, kind, status, value, payload, time.Time{})
}

func (s *server) persistDecisionRecordWithDeadline(sessionID, runID, id string, kind agentruntime.DecisionKind, status, value string, payload any, expiresAt time.Time) error {
	if s == nil || s.settings == nil || sessionID == "" || runID == "" || id == "" {
		return nil
	}
	request := agentruntime.DecisionRequest{ID: id, SessionID: sessionID, RunID: runID, Kind: kind}
	resolution := agentruntime.DecisionResolution{ID: id, Kind: kind, Status: status, Value: value}
	record, err := agentruntime.NewDecisionResolutionRecord(request, resolution, payload)
	if status == "pending" {
		record, err = agentruntime.NewDecisionRequestRecordWithDeadline(request, payload, expiresAt)
	}
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"decision": record, "payload": payload})
	if err != nil {
		return err
	}
	source := string(agentruntime.SourceACP)
	s.mu.Lock()
	rt := s.sessions[sessionID]
	s.mu.Unlock()
	if rt != nil && rt.runtime != nil {
		_, _, _, sessionMode, _ := rt.runtime.ConfigSnapshot()
		if sessionMode == "" {
			sessionMode = s.mode
		}
		if resolution, _, resolveErr := rt.runtime.ResolvePolicy(sessionMode, "", s.mode); resolveErr == nil && resolution.Source != agentruntime.SourceUnknown {
			source = string(resolution.Source)
		}
	}
	_, err = (agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()}).Record(agentruntime.RunEvent{
		SessionID: sessionID, RunID: runID, EventType: "decision_" + status,
		Source: source, Status: status, Timestamp: time.Now(), Data: data,
	})
	return err
}

const acpElicitationFormProtocol = "acp-elicitation-form"

func (s *server) supportsElicitationForm() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientCaps.Elicitation != nil && s.clientCaps.Elicitation.Form != nil
}

func elicitationRequestForQuestion(request questionRequest) elicitationCreateRequest {
	options := make([]any, 0, len(request.Options))
	for _, option := range request.Options {
		options = append(options, option)
	}
	answerSchema := map[string]any{
		"type":        "string",
		"title":       "Answer",
		"description": request.Explanation,
	}
	if len(options) > 0 {
		answerSchema["enum"] = options
	}
	return elicitationCreateRequest{
		SessionID: request.SessionID,
		Message:   request.Question,
		Mode:      "form",
		RequestedSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"answer": answerSchema},
			"required":   []string{"answer"},
		},
	}
}

// requestQuestion sends a question through standard ACP elicitation when the
// client advertises form support, while preserving the legacy extension for
// older clients. Both paths use the same pending request and DecisionService.
func (s *server) requestQuestion(ctx context.Context, sessionID, question string, options []string, explanation string) string {
	id := s.nextRequestID()
	ch := make(chan json.RawMessage, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	s.registerDecision(sessionID, id, agentruntime.DecisionQuestion)
	timeout := s.effectiveQuestionTimeout()
	deadline := time.Now().Add(timeout)
	request := questionRequest{SessionID: sessionID, Question: question, Options: options, Explanation: explanation, TimeoutMs: int64(timeout.Milliseconds())}
	method, payload := s.questionProjection(request)
	if s.supportsElicitationForm() {
		request.Protocol = acpElicitationFormProtocol
		method = "elicitation/create"
		payload = elicitationRequestForQuestion(request)
	}
	if err := s.persistDecisionRecordWithDeadline(sessionID, s.sessionRunID(sessionID), id, agentruntime.DecisionQuestion, "pending", "", request, deadline); err != nil {
		s.deletePending(id)
		return ""
	}

	if err := s.notifyRequest(id, method, payload); err != nil {
		s.deletePending(id)
		s.resolveDecision(sessionID, id, agentruntime.DecisionQuestion, "", "cancelled")
		return ""
	}
	// Remind the client as the decision deadline approaches; the stop call on
	// every resolution path guarantees no reminder fires afterwards and no
	// timer goroutine leaks.
	stopDeadlineReminders := s.scheduleDecisionDeadline(sessionID, id, agentruntime.DecisionQuestion, timeout, deadline)
	defer stopDeadlineReminders()
	select {
	case <-ctx.Done():
		s.deletePending(id)
		s.resolveDecision(sessionID, id, agentruntime.DecisionQuestion, "", "cancelled")
		return ""
	case <-time.After(timeout):
		s.deletePending(id)
		s.resolveDecision(sessionID, id, agentruntime.DecisionQuestion, "", "timed_out")
		return ""
	case resp := <-ch:
		answer, status := questionAnswer(resp, request.Protocol == acpElicitationFormProtocol)
		if err := s.resolveDecision(sessionID, id, agentruntime.DecisionQuestion, answer, status); err != nil {
			return ""
		}
		for _, option := range options {
			if answer == option {
				return option
			}
		}
		return ""
	}
}

func questionAnswer(raw json.RawMessage, standardElicitation bool) (string, string) {
	if standardElicitation {
		var out elicitationResult
		if json.Unmarshal(raw, &out) == nil && out.Action == "accept" {
			answer, _ := out.Content["answer"].(string)
			return answer, "resolved"
		}
		// A restored request can fall back to the legacy extension when a
		// reconnecting client no longer advertises form elicitation. Accept the
		// old result shape without creating a second decision state.
		var legacy questionResult
		if json.Unmarshal(raw, &legacy) == nil && legacy.Answer != "" {
			return legacy.Answer, "resolved"
		}
		return "", "cancelled"
	}
	var out questionResult
	if json.Unmarshal(raw, &out) != nil {
		return "", "cancelled"
	}
	if out.Cancelled || (out.OK != nil && !*out.OK) {
		return "", "cancelled"
	}
	answer := strings.TrimSpace(out.Answer)
	if answer == "" && len(out.Answers) > 0 {
		answer = strings.TrimSpace(out.Answers[0])
	}
	if answer == "" {
		return "", "cancelled"
	}
	return answer, "resolved"
}

func (s *server) requestPermission(sessionID, toolCallID, toolName string, args map[string]any) bool {
	return s.requestPermissionContext(context.Background(), sessionID, toolCallID, toolName, args)
}

func (s *server) requestPermissionContext(ctx context.Context, sessionID, toolCallID, toolName string, args map[string]any) bool {
	id := s.nextRequestID()
	ch := make(chan json.RawMessage, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	s.registerDecision(sessionID, id, agentruntime.DecisionApproval)
	timeout := s.effectivePermissionTimeout()
	deadline := time.Now().Add(timeout)
	if err := s.persistDecisionRecordWithDeadline(sessionID, s.sessionRunID(sessionID), id, agentruntime.DecisionApproval, "pending", "", requestPermissionRequest{SessionID: sessionID, ToolCall: permissionToolCall{ToolCallID: toolCallID, Title: toolName, Status: "pending", RawInput: toolRawInput(args)}}, deadline); err != nil {
		s.deletePending(id)
		return false
	}
	if err := s.notifyRequest(id, "session/request_permission", requestPermissionRequest{
		SessionID: sessionID,
		ToolCall: permissionToolCall{
			ToolCallID: toolCallID,
			Title:      toolName,
			Kind:       acpToolKind(toolName),
			Status:     "pending",
			RawInput:   toolRawInput(args),
		},
		Options: []permissionOption{
			{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
		},
	}); err != nil {
		s.deletePending(id)
		s.resolveDecision(sessionID, id, agentruntime.DecisionApproval, "", "cancelled")
		return false
	}
	stopDeadlineReminders := s.scheduleDecisionDeadline(sessionID, id, agentruntime.DecisionApproval, timeout, deadline)
	defer stopDeadlineReminders()
	select {
	case <-ctx.Done():
		s.deletePending(id)
		s.resolveDecision(sessionID, id, agentruntime.DecisionApproval, "", "cancelled")
		return false
	case <-time.After(timeout):
		s.deletePending(id)
		s.resolveDecision(sessionID, id, agentruntime.DecisionApproval, "", "timed_out")
		return false
	case resp := <-ch:
		var out permissionResult
		_ = json.Unmarshal(resp, &out)
		value := "deny"
		if out.Outcome != nil {
			value = out.Outcome.OptionID
		}
		if err := s.resolveDecision(sessionID, id, agentruntime.DecisionApproval, value, "resolved"); err != nil {
			return false
		}
		return out.Outcome != nil && out.Outcome.Outcome == "selected" && out.Outcome.OptionID == "allow-once"
	}
}

func (s *server) deletePending(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *server) deliverResponse(id json.RawMessage, result json.RawMessage, errMsg json.RawMessage) {
	key := mcp.RawIDKey(id)
	s.mu.Lock()
	ch, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
	}
	s.mu.Unlock()
	if ok {
		if len(errMsg) > 0 {
			ch <- errMsg
			return
		}
		ch <- result
	}
}

func (s *server) emitMessage(sessionID string, msg provider.Message) {
	for _, update := range s.messageUpdates(sessionID, msg, "") {
		_ = s.notify(sessionID, update)
	}
}

const transcriptPageDefaultSize = 40
const transcriptPageMaxSize = 100

func encodeTranscriptCursor(before int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("acp-history-v1:%d", before)))
}

func decodeTranscriptCursor(cursor string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !strings.HasPrefix(string(raw), "acp-history-v1:") {
		return 0, fmt.Errorf("invalid history cursor")
	}
	before, err := strconv.Atoi(strings.TrimPrefix(string(raw), "acp-history-v1:"))
	if err != nil || before < 0 {
		return 0, fmt.Errorf("invalid history cursor")
	}
	return before, nil
}

func transcriptPageSize(limit int) int {
	if limit <= 0 {
		return transcriptPageDefaultSize
	}
	if limit > transcriptPageMaxSize {
		return transcriptPageMaxSize
	}
	return limit
}

func (s *server) transcriptPage(sessionID string, mgr *session.Manager, cursor string, limit int) (transcriptPageResult, error) {
	if mgr == nil {
		return transcriptPageResult{}, fmt.Errorf("session is unavailable")
	}
	state := mgr.GetReplayState()
	before := len(state.Messages)
	if cursor != "" {
		var err error
		before, err = decodeTranscriptCursor(cursor)
		if err != nil || before > len(state.Messages) {
			return transcriptPageResult{}, fmt.Errorf("invalid history cursor")
		}
	}
	start := max(0, before-transcriptPageSize(limit))
	result := transcriptPageResult{SessionID: sessionID}
	for index := start; index < before; index++ {
		entryID := ""
		if index < len(state.EntryIDs) {
			entryID = state.EntryIDs[index]
		}
		result.Updates = append(result.Updates, s.messageUpdates(sessionID, state.Messages[index], entryID)...)
	}
	if start > 0 {
		result.NextCursor = encodeTranscriptCursor(start)
	}
	return result, nil
}

func (s *server) projectInitialTranscript(sessionID string, mgr *session.Manager, limit int) (*transcriptPageResult, error) {
	if limit <= 0 {
		for _, msg := range mgr.GetMessages() {
			s.emitMessage(sessionID, msg)
		}
		return nil, nil
	}
	page, err := s.transcriptPage(sessionID, mgr, "", limit)
	if err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *server) messageUpdates(sessionID string, msg provider.Message, entryID string) []sessionUpdate {
	messageID := func(kind string, index int, text string) string {
		if entryID == "" {
			return replayMessageID(sessionID, kind, text)
		}
		return replayMessageID(sessionID, kind, fmt.Sprintf("%s:%d", entryID, index))
	}
	var updates []sessionUpdate
	if msg.Role == "assistant" {
		for index, c := range msg.Contents {
			if c.Type == "thinking" && c.Thinking != "" {
				updates = append(updates, sessionUpdate{SessionUpdate: "agent_thought_chunk", MessageID: messageID("thought", index, c.Thinking), Content: &contentBlock{Type: "text", Text: c.Thinking}})
			} else if c.Type == "text" && c.Text != "" {
				updates = append(updates, sessionUpdate{SessionUpdate: "agent_message_chunk", MessageID: messageID("message", index, c.Text), Content: &contentBlock{Type: "text", Text: c.Text}})
			} else if c.Type == "toolCall" && c.ToolCall != nil {
				var rawInput map[string]any
				_ = json.Unmarshal(c.ToolCall.Arguments, &rawInput)
				title := s.rememberToolTitle(c.ToolCall.ID, c.ToolCall.Name, rawInput)
				updates = append(updates, sessionUpdate{
					SessionUpdate: "tool_call",
					ToolCallID:    c.ToolCall.ID,
					Title:         title,
					Kind:          acpToolKind(c.ToolCall.Name),
					Status:        "pending",
					RawInput:      toolRawInput(rawInput),
				})
			}
		}
		return updates
	}
	if msg.Role == "user" {
		text := msg.Content
		if text == "" {
			for _, c := range msg.Contents {
				if c.Type == "text" && c.Text != "" {
					text = c.Text
					break
				}
			}
		}
		if text != "" {
			updates = append(updates, sessionUpdate{SessionUpdate: "user_message_chunk", MessageID: messageID("user", 0, text), Content: &contentBlock{Type: "text", Text: text}})
		}
		return updates
	}
	if msg.Role == "toolResult" {
		rawOutput := map[string]any{"content": msg.Content}
		status := "completed"
		if msg.IsError {
			status = "failed"
		}
		title := s.toolTitleFor(msg.ToolCallID, msg.ToolName)
		updates = append(updates, sessionUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    msg.ToolCallID,
			Title:         title,
			Kind:          acpToolKind(msg.ToolName),
			Status:        status,
			Content:       textToolContent(msg.Content),
			RawOutput:     rawOutput,
		})
	}
	return updates
}

func replayMessageID(sessionID, kind, text string) string {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + kind + "\x00" + text))
	return fmt.Sprintf("acp_replay_%s_%x", kind, digest[:8])
}

func promptToText(blocks []contentBlock) (string, error) {
	var parts []string
	for _, b := range blocks {
		if b.Type != "text" {
			return "", fmt.Errorf("prompt content type %q must be normalized through Runtime", b.Type)
		}
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// promptToRunInput is retained for text-only protocol/unit callers. The live
// ACP prompt path uses promptToIngresses so every resource is normalized by
// SessionRuntime rather than becoming prompt text.
func promptToRunInput(blocks []contentBlock) (agentruntime.RunInput, error) {
	text, err := promptToText(blocks)
	if err != nil {
		return agentruntime.RunInput{}, err
	}
	return agentruntime.RunInput{Text: text}, nil
}

// acpPromptRequestSnapshot produces the durable idempotency input without
// retaining raw prompt blocks. In particular, inline base64 media and local
// file URIs stay in the Runtime materializer boundary rather than entering
// session_execution_intents.request_json.
func acpPromptRequestSnapshot(ctx context.Context, runtime *agentruntime.SessionRuntime, text string, input agentruntime.InputSubmission) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	type resourceSnapshot struct {
		ID           string `json:"id"`
		Kind         string `json:"kind"`
		Filename     string `json:"filename"`
		MediaType    string `json:"mediaType"`
		Bytes        int64  `json:"bytes"`
		RelativePath string `json:"relativePath"`
		SHA256       string `json:"sha256"`
	}
	resources := make([]resourceSnapshot, 0, len(input.Resources))
	type knowledgeSnapshot struct {
		KnowledgeBaseID string `json:"knowledgeBaseId"`
		SnapshotID      string `json:"snapshotId"`
		Required        bool   `json:"required"`
		TextSHA256      string `json:"textSha256"`
	}
	knowledge := make([]knowledgeSnapshot, 0, len(input.KnowledgeBaseReferences))
	capsules := make(map[string]agentruntime.KnowledgeCapsule, len(input.KnowledgeCapsules))
	for _, capsule := range input.KnowledgeCapsules {
		capsules[capsule.KnowledgeBaseID] = capsule
	}
	for _, reference := range input.KnowledgeBaseReferences {
		entry := knowledgeSnapshot{KnowledgeBaseID: reference.KnowledgeBaseID, Required: reference.Required}
		if capsule, ok := capsules[reference.KnowledgeBaseID]; ok {
			entry.SnapshotID = capsule.SnapshotID
			entry.TextSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(capsule.Text)))
		}
		knowledge = append(knowledge, entry)
	}
	if runtime != nil && runtime.Inputs != nil {
		for _, prepared := range input.Resources {
			record, err := runtime.Inputs.Get(ctx, runtime.ID, prepared.ResourceID)
			if err != nil {
				return nil, fmt.Errorf("snapshot input resource %s: %w", prepared.ResourceID, err)
			}
			resources = append(resources, resourceSnapshot{
				ID: record.ID, Kind: string(record.Kind), Filename: record.Filename,
				MediaType: record.MediaType, Bytes: record.Bytes,
				RelativePath: record.RelativePath, SHA256: record.SHA256,
			})
		}
	} else if len(input.Resources) > 0 {
		return nil, fmt.Errorf("snapshot input materializer is unavailable")
	}
	return json.Marshal(map[string]any{
		"text":      text,
		"resources": resources,
		"knowledge": knowledge,
	})
}

func promptToIngresses(blocks []contentBlock, workspaceCwd string, additional []string, eventPrefix string) (string, []agentruntime.InputIngress, error) {
	var textParts []string
	ingresses := make([]agentruntime.InputIngress, 0)
	for index, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "resource_link":
			if strings.TrimSpace(block.Name) == "" || strings.TrimSpace(block.URI) == "" {
				return "", nil, fmt.Errorf("resource_link requires name and uri")
			}
			path, err := resolveACPResourcePath(block.URI, workspaceCwd, additional)
			if err != nil {
				return "", nil, err
			}
			ingress, err := localACPIngress(path, block.Name, block.MimeType, block.Size, eventPrefix, index, block.URI)
			if err != nil {
				return "", nil, err
			}
			ingresses = append(ingresses, ingress)
		case "resource":
			if strings.TrimSpace(block.Data) == "" && strings.TrimSpace(block.URI) != "" {
				path, err := resolveACPResourcePath(block.URI, workspaceCwd, additional)
				if err != nil {
					return "", nil, err
				}
				ingress, err := localACPIngress(path, block.Name, block.MimeType, block.Size, eventPrefix, index, block.URI)
				if err != nil {
					return "", nil, err
				}
				ingresses = append(ingresses, ingress)
				continue
			}
			fallthrough
		case "image", "audio":
			ingress, err := encodedACPIngress(block, eventPrefix, index)
			if err != nil {
				return "", nil, err
			}
			ingresses = append(ingresses, ingress)
		default:
			return "", nil, fmt.Errorf("unsupported prompt content type: %s", block.Type)
		}
	}
	return strings.Join(textParts, "\n"), ingresses, nil
}

func resolveACPResourcePath(rawURI, workspaceCwd string, additional []string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil {
		return "", fmt.Errorf("invalid resource URI: %w", err)
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, "file") {
		return "", fmt.Errorf("resource URI scheme %q is not an authenticated local source", u.Scheme)
	}
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		return "", fmt.Errorf("resource URI host is not allowed")
	}
	resourcePath, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", fmt.Errorf("decode resource URI: %w", err)
	}
	if resourcePath == "" {
		return "", fmt.Errorf("resource URI path is empty")
	}
	if !filepath.IsAbs(resourcePath) {
		resourcePath = filepath.Join(workspaceCwd, resourcePath)
	}
	resourcePath, err = filepath.Abs(resourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve resource path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(resourcePath)
	if err != nil {
		return "", fmt.Errorf("resolve resource path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat resource path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("resource path is not a regular file")
	}
	roots := append([]string{workspaceCwd}, additional...)
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		root, rootErr := filepath.Abs(root)
		if rootErr != nil {
			continue
		}
		root, rootErr = filepath.EvalSymlinks(root)
		if rootErr != nil {
			continue
		}
		rel, relErr := filepath.Rel(root, resolved)
		if relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("resource path is outside the negotiated workspace")
}

func localACPIngress(path, filename, mediaType string, size *int, eventPrefix string, index int, reference string) (agentruntime.InputIngress, error) {
	info, err := os.Stat(path)
	if err != nil {
		return agentruntime.InputIngress{}, err
	}
	if size != nil && *size >= 0 && int64(*size) != info.Size() {
		// Runtime reads and validates the local file; a stale protocol hint is
		// deliberately ignored.
		size = nil
	}
	if strings.TrimSpace(filename) == "" {
		filename = filepath.Base(path)
	}
	return agentruntime.InputIngress{
		Origin: "acp", EventID: fmt.Sprintf("%s:%d", eventPrefix, index), ItemIndex: index, Reference: reference,
		Kind: acpAttachmentKind("", mediaType), FilenameHint: filename, MediaTypeHint: mediaType, SizeHint: info.Size(),
		Open: func(context.Context) (agentruntime.InputStream, error) {
			file, err := os.Open(path)
			if err != nil {
				return agentruntime.InputStream{}, err
			}
			return agentruntime.InputStream{Reader: file, Filename: filename, MediaType: mediaType, ContentSize: info.Size()}, nil
		},
	}, nil
}

func encodedACPIngress(block contentBlock, eventPrefix string, index int) (agentruntime.InputIngress, error) {
	data := strings.TrimSpace(block.Data)
	if data == "" {
		return agentruntime.InputIngress{}, fmt.Errorf("prompt content type %q requires base64 data", block.Type)
	}
	if comma := strings.Index(data, ","); strings.HasPrefix(strings.ToLower(data), "data:") && comma >= 0 {
		data = data[comma+1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return agentruntime.InputIngress{}, fmt.Errorf("decode %s content: %w", block.Type, err)
	}
	if len(decoded) == 0 {
		return agentruntime.InputIngress{}, fmt.Errorf("prompt content type %q is empty", block.Type)
	}
	mediaType := strings.TrimSpace(block.MimeType)
	if mediaType == "" && block.Type == "image" {
		mediaType = "image/png"
	}
	filename := strings.TrimSpace(block.Name)
	if filename == "" {
		filename = fmt.Sprintf("acp-%d", index)
	}
	dataCopy := append([]byte(nil), decoded...)
	return agentruntime.InputIngress{
		Origin: "acp", EventID: fmt.Sprintf("%s:%d", eventPrefix, index), ItemIndex: index,
		Kind: acpAttachmentKind(block.Type, mediaType), FilenameHint: filename, MediaTypeHint: mediaType, SizeHint: int64(len(dataCopy)),
		Open: func(context.Context) (agentruntime.InputStream, error) {
			return agentruntime.InputStream{Reader: io.NopCloser(bytes.NewReader(dataCopy)), Filename: filename, MediaType: mediaType, ContentSize: int64(len(dataCopy))}, nil
		},
	}, nil
}

func acpAttachmentKind(contentType, mediaType string) agentruntime.AttachmentKind {
	if strings.EqualFold(strings.TrimSpace(contentType), "image") {
		return agentruntime.AttachmentImage
	}
	if strings.EqualFold(strings.TrimSpace(contentType), "audio") {
		return agentruntime.AttachmentAudio
	}
	baseType := strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	if strings.HasPrefix(baseType, "image/") {
		return agentruntime.AttachmentImage
	}
	if strings.HasPrefix(baseType, "audio/") {
		return agentruntime.AttachmentAudio
	}
	if strings.HasPrefix(baseType, "video/") {
		return agentruntime.AttachmentVideo
	}
	return agentruntime.AttachmentFile
}

// formatEditorContext turns the optional editor snapshot into an explicit,
// bounded context block. The snapshot is user-provided metadata, so it is
// labeled untrusted and is never interpreted as an instruction by ACP itself.
func formatEditorContext(ctx *editorContext) string {
	if ctx == nil {
		return ""
	}
	var lines []string
	if strings.TrimSpace(ctx.Path) != "" {
		lines = append(lines, "Path: "+strings.TrimSpace(ctx.Path))
	} else if strings.TrimSpace(ctx.URI) != "" {
		lines = append(lines, "URI: "+strings.TrimSpace(ctx.URI))
	}
	if strings.TrimSpace(ctx.Language) != "" {
		lines = append(lines, "Language: "+strings.TrimSpace(ctx.Language))
	}
	if ctx.Selection != nil {
		selection := ctx.Selection.Text
		if len(selection) > 80000 {
			selection = selection[:80000] + "\n[selection truncated]"
		}
		if strings.TrimSpace(selection) != "" {
			lines = append(lines, fmt.Sprintf("Selection (lines %d-%d):\n%s", ctx.Selection.StartLine, ctx.Selection.EndLine, selection))
		}
	}
	for _, diagnostic := range ctx.Diagnostics {
		message := strings.TrimSpace(diagnostic.Message)
		if message == "" {
			continue
		}
		startLine, endLine := diagnostic.StartLine, diagnostic.EndLine
		if startLine == 0 {
			startLine = diagnostic.Line
		}
		if endLine == 0 {
			endLine = startLine
		}
		lines = append(lines, fmt.Sprintf("Diagnostic (%s, lines %d-%d): %s", diagnostic.Severity, startLine, endLine, message))
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Editor context (untrusted metadata)\n" + strings.Join(lines, "\n")
}

func toolRawInput(args map[string]any) map[string]any {
	raw := map[string]any{"args": args}
	for key, value := range args {
		raw[key] = value
	}
	return raw
}

func (s *server) rememberToolTitle(toolCallID, name string, args map[string]any) string {
	title := toolTitle(name, args)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.toolTitles[toolCallID]; existing != "" && existing != name {
		return existing
	}
	s.toolTitles[toolCallID] = title
	return title
}

func (s *server) toolTitleFor(toolCallID, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if title := s.toolTitles[toolCallID]; title != "" {
		return title
	}
	return fallback
}

func toolTitle(name string, args map[string]any) string {
	if args == nil {
		return name
	}

	var details []string
	switch name {
	case "bash":
		details = appendStringArg(details, "command", args)
	case "read", "write", "edit", "ls":
		details = appendStringArg(details, "path", args)
	case "grep":
		details = appendStringArg(details, "pattern", args)
		details = appendStringArg(details, "path", args)
	case "find":
		details = appendStringArg(details, "pattern", args)
		details = appendStringArg(details, "path", args)
	default:
		for _, key := range []string{"command", "path", "pattern", "query", "name"} {
			details = appendStringArg(details, key, args)
			if len(details) > 0 {
				break
			}
		}
	}

	if len(details) == 0 {
		return name
	}
	return name + ": " + truncateTitle(strings.Join(details, " "))
}

func appendStringArg(details []string, key string, args map[string]any) []string {
	value, ok := args[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return details
	}
	if key == "command" {
		return append(details, value)
	}
	return append(details, key+"="+value)
}

func truncateTitle(title string) string {
	const maxTitleLength = 160
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if len(title) <= maxTitleLength {
		return title
	}
	return title[:maxTitleLength-3] + "..."
}

func normalizeStopReason(reason string) string {
	switch reason {
	case "", "stop", "end_turn", "tool_use":
		return "end_turn"
	case "max_tokens", "length":
		return "max_tokens"
	case "max_turn_requests":
		return "max_turn_requests"
	case "cancelled", "aborted":
		return "cancelled"
	default:
		return "refusal"
	}
}

func (s *server) nextRequestID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	return fmt.Sprintf("acp-%d", s.nextID)
}

func (s *server) readRequest() (rpcRequest, error) {
	var req rpcRequest
	var buf bytes.Buffer
	for {
		part, err := s.r.ReadSlice('\n')
		if len(part) > 0 {
			if buf.Len()+len(part) > maxRequestBytes {
				return req, fmt.Errorf("message exceeds maximum size of %d bytes", maxRequestBytes)
			}
			buf.Write(part)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return req, err
		}
		break
	}
	payload := strings.TrimRight(buf.String(), "\r\n")
	if strings.TrimSpace(payload) == "" {
		return req, errEmptyMessage
	}
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return req, err
	}
	return req, nil
}

// validRPCID accepts the JSON-RPC scalar ID domain and notifications (empty
// raw ID). Objects and arrays are invalid request IDs and must not be echoed
// back in an error response.
func validRPCID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return true
	case json.Number:
		// JSON-RPC permits integer numeric IDs. Int64 rejects fractional and
		// exponent forms, avoiding ambiguous echoing after float conversion.
		_, err := typed.Int64()
		return err == nil
	default:
		return false
	}
}

func (s *server) writeResponse(id json.RawMessage, result any, errResp *mcp.RPCError) error {
	// A missing ID denotes a JSON-RPC notification. Notifications never receive
	// a response; an explicit JSON null remains a request ID and is preserved.
	if len(bytes.TrimSpace(id)) == 0 {
		return nil
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
	}
	if errResp != nil {
		resp["error"] = errResp
	} else {
		resp["result"] = result
	}
	return s.writeMessage(resp)
}

func (s *server) notify(sessionID string, update sessionUpdate) error {
	return s.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params": map[string]any{
			"sessionId": sessionID,
			"update":    update,
		},
	})
}

// notifySessionInfo projects the standard session metadata update after a
// persisted session mutation. ACP v1 keeps this update intentionally small;
// the authoritative cwd/additionalDirectories remain in SessionInfo and the
// session setup/list responses.
func (s *server) notifySessionInfo(sessionID string) error {
	title := ""
	if rt := s.sessionRuntime(sessionID); rt != nil && rt.mgr != nil {
		if name, _, err := session.LatestSessionTitle(rt.mgr.GetSessionDir(), sessionID); err == nil {
			title = name
		}
	} else if s != nil && s.settings != nil {
		if name, _, err := session.LatestSessionTitle(s.settings.GetSessionDir(), sessionID); err == nil {
			title = name
		}
	}
	return s.notify(sessionID, sessionUpdate{
		SessionUpdate: "session_info_update",
		Title:         title,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *server) notifyExtension(method string, params any) error {
	return s.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (s *server) notifyRequest(id string, method string, params any) error {
	return s.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
}

func (s *server) writeMessage(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.w.Write(data); err != nil {
		return err
	}
	if _, err := s.w.Write([]byte("\n")); err != nil {
		return err
	}
	if f, ok := s.w.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			return err
		}
	}
	return nil
}
