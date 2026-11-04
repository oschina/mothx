package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Implementation identifies one ACP implementation during initialization.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// InitializeParams is the ACP v1 client initialization request. Capability
// fields are intentionally caller-provided so the transport never claims a
// filesystem, terminal, or elicitation bridge it does not implement.
type InitializeParams struct {
	ProtocolVersion    int            `json:"protocolVersion"`
	ClientCapabilities map[string]any `json:"clientCapabilities,omitempty"`
	ClientInfo         Implementation `json:"clientInfo,omitempty"`
}

// InitializeResult is the ACP v1 Agent initialization response.
type InitializeResult struct {
	ProtocolVersion   int            `json:"protocolVersion"`
	AgentCapabilities map[string]any `json:"agentCapabilities,omitempty"`
	AgentInfo         Implementation `json:"agentInfo,omitempty"`
	AuthMethods       []any          `json:"authMethods,omitempty"`
}

// ContentBlock is the ACP prompt content representation. Runtime owns which
// normalized inputs may become each variant; this package only encodes the
// wire shape and never reads attachments or local files itself.
type ContentBlock struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	Data     string    `json:"data,omitempty"`
	MIMEType string    `json:"mimeType,omitempty"`
	URI      string    `json:"uri,omitempty"`
	Name     string    `json:"name,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
}

// Resource is an embedded ACP resource. Exactly one of Text or Blob is
// expected; Runtime validates and bounds the bytes before encoding it.
type Resource struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

type NewSessionParams struct {
	Cwd                   string   `json:"cwd"`
	MCPServers            []any    `json:"mcpServers,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

type NewSessionResult struct {
	SessionID string `json:"sessionId"`
}

// LoadSessionParams resumes one existing remote ACP session. The caller must
// have negotiated the Agent's session load capability; this helper performs no
// capability or policy decision.
type LoadSessionParams struct {
	SessionID             string   `json:"sessionId"`
	Cwd                   string   `json:"cwd"`
	MCPServers            []any    `json:"mcpServers,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

// LoadSessionResult is the Agent's response to session/load. ACP Agents may
// replay prior session updates before returning this response.
type LoadSessionResult struct {
	SessionID string `json:"sessionId,omitempty"`
}

// ResumeSessionParams reconnects to an existing ACP session without replaying
// its conversation history. The caller must have negotiated
// sessionCapabilities.resume; this helper performs no policy decision.
type ResumeSessionParams struct {
	SessionID             string   `json:"sessionId"`
	Cwd                   string   `json:"cwd"`
	MCPServers            []any    `json:"mcpServers,omitempty"`
	AdditionalDirectories []string `json:"additionalDirectories,omitempty"`
}

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

type CloseSessionParams struct {
	SessionID string `json:"sessionId"`
}

// ReadTextFileParams is the ACP filesystem read request. The Client validates
// the path against its session roots; this package only carries the wire shape.
type ReadTextFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Line      int    `json:"line,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// ReadTextFileResult is the text content returned by fs/read_text_file.
type ReadTextFileResult struct {
	Content string `json:"content"`
}

// WriteTextFileParams is the ACP filesystem write request. The Client decides
// authorization; this package only carries the wire shape.
type WriteTextFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

// TerminalCreateParams is the ACP terminal/create request. MothX executes the
// argv through the shared Runtime command/sandbox path, never through a
// model-supplied shell string.
type TerminalCreateParams struct {
	SessionID       string           `json:"sessionId"`
	Command         string           `json:"command"`
	Args            []string         `json:"args,omitempty"`
	Env             []TerminalEnvVar `json:"env,omitempty"`
	Cwd             string           `json:"cwd,omitempty"`
	OutputByteLimit int64            `json:"outputByteLimit,omitempty"`
}

// TerminalEnvVar is one ACP terminal environment entry.
type TerminalEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TerminalCreateResult struct {
	TerminalID string `json:"terminalId"`
}

// TerminalRefParams references one already-created terminal.
type TerminalRefParams struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

// TerminalExitStatus is the ACP projection of a finished process.
type TerminalExitStatus struct {
	ExitCode *int    `json:"exitCode"`
	Signal   *string `json:"signal"`
}

type TerminalOutputResult struct {
	Output     string              `json:"output"`
	Truncated  bool                `json:"truncated"`
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
}

// DecodeReadTextFileRequest decodes one fs/read_text_file reverse request.
func DecodeReadTextFileRequest(message Message) (ReadTextFileParams, bool) {
	if message.Method != "fs/read_text_file" {
		return ReadTextFileParams{}, false
	}
	var params ReadTextFileParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return ReadTextFileParams{}, false
	}
	if params.SessionID == "" || params.Path == "" {
		return ReadTextFileParams{}, false
	}
	return params, true
}

// DecodeWriteTextFileRequest decodes one fs/write_text_file reverse request.
func DecodeWriteTextFileRequest(message Message) (WriteTextFileParams, bool) {
	if message.Method != "fs/write_text_file" {
		return WriteTextFileParams{}, false
	}
	var params WriteTextFileParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return WriteTextFileParams{}, false
	}
	if params.SessionID == "" || params.Path == "" {
		return WriteTextFileParams{}, false
	}
	return params, true
}

// DecodeTerminalCreateRequest decodes one terminal/create reverse request.
func DecodeTerminalCreateRequest(message Message) (TerminalCreateParams, bool) {
	if message.Method != "terminal/create" {
		return TerminalCreateParams{}, false
	}
	var params TerminalCreateParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return TerminalCreateParams{}, false
	}
	if params.SessionID == "" || params.Command == "" {
		return TerminalCreateParams{}, false
	}
	return params, true
}

// DecodeTerminalRefRequest decodes terminal/output, wait_for_exit, kill, and
// release requests.
func DecodeTerminalRefRequest(message Message) (TerminalRefParams, bool) {
	switch message.Method {
	case "terminal/output", "terminal/wait_for_exit", "terminal/kill", "terminal/release":
	default:
		return TerminalRefParams{}, false
	}
	var params TerminalRefParams
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return TerminalRefParams{}, false
	}
	if params.SessionID == "" || params.TerminalID == "" {
		return TerminalRefParams{}, false
	}
	return params, true
}

// SessionUpdate is the decoded payload of one session/update notification.
// Fields are protocol representation only; Runtime decides which variants map
// to canonical events. All content is untrusted.
type SessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	MessageID     string          `json:"messageId,omitempty"`
	Content       *ContentBlock   `json:"content,omitempty"`
	ToolCallID    string          `json:"toolCallId,omitempty"`
	Title         string          `json:"title,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	Status        string          `json:"status,omitempty"`
	RawInput      json.RawMessage `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
	Entries       []PlanEntry     `json:"entries,omitempty"`
	Used          int             `json:"used,omitempty"`
	Size          int             `json:"size,omitempty"`
}

// SessionNotification is one session/update notification with the remote
// session identity. Multi-session connections must route by SessionID rather
// than relying on a single active session.
type SessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// PlanEntry is one ACP plan step.
type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// PermissionToolCall is the ACP tool-call context carried by a permission
// request. Every field is Agent-authored and therefore untrusted.
type PermissionToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
}

// PermissionOption is one option the remote Agent offered. MothX never invents
// an option ID.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// PermissionRequest is the session/request_permission request payload.
type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// PermissionResult is the response shape for session/request_permission.
type PermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome is either a selected offered option or ACP's cancelled
// outcome.
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// PermissionSelected answers with one offered option.
func PermissionSelected(optionID string) PermissionResult {
	return PermissionResult{Outcome: PermissionOutcome{Outcome: "selected", OptionID: optionID}}
}

// PermissionCancelled answers ACP's cancellation outcome.
func PermissionCancelled() PermissionResult {
	return PermissionResult{Outcome: PermissionOutcome{Outcome: "cancelled"}}
}

// DecodeCancelRequest extracts the request id targeted by $/cancel_request.
// The value is returned in canonical JSON form so a string id and a numeric id
// remain distinct, exactly as JSON-RPC defines them.
func DecodeCancelRequest(message Message) (string, bool) {
	if message.Method != "$/cancel_request" || len(message.ID) != 0 || len(message.Params) == 0 {
		return "", false
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return "", false
	}
	return canonicalRPCID(params.RequestID)
}

// DecodeSessionUpdate decodes a session/update notification. It returns false
// for any other method or malformed params.
func DecodeSessionUpdate(message Message) (SessionUpdate, bool) {
	notification, ok := DecodeSessionNotification(message)
	if !ok {
		return SessionUpdate{}, false
	}
	return notification.Update, true
}

// DecodeSessionNotification decodes a session/update notification, including
// its remote session identity. It returns false for any other method or
// malformed params.
func DecodeSessionNotification(message Message) (SessionNotification, bool) {
	if message.Method != "session/update" {
		return SessionNotification{}, false
	}
	var params SessionNotification
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return SessionNotification{}, false
	}
	if params.Update.SessionUpdate == "" {
		return SessionNotification{}, false
	}
	return params, true
}

// DecodeSessionID extracts the remote session identity from request or
// notification params. It is intentionally structural: callers still validate
// that the session belongs to the connection before dispatching.
func DecodeSessionID(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var envelope struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return ""
	}
	return envelope.SessionID
}

// DecodePermissionRequest decodes a session/request_permission reverse request.
func DecodePermissionRequest(message Message) (PermissionRequest, bool) {
	if message.Method != "session/request_permission" {
		return PermissionRequest{}, false
	}
	var request PermissionRequest
	if err := json.Unmarshal(message.Params, &request); err != nil {
		return PermissionRequest{}, false
	}
	if request.SessionID == "" {
		return PermissionRequest{}, false
	}
	return request, true
}

// Initialize completes ACP version negotiation for this connection.
func (c *Client) Initialize(ctx context.Context, params InitializeParams) (InitializeResult, error) {
	if params.ProtocolVersion == 0 {
		params.ProtocolVersion = ProtocolVersion
	}
	var result InitializeResult
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return InitializeResult{}, err
	}
	if result.ProtocolVersion != ProtocolVersion {
		return InitializeResult{}, fmt.Errorf("ACP agent selected unsupported protocol version %d", result.ProtocolVersion)
	}
	return result, nil
}

// NewSession creates one remote ACP session. ACP requires cwd to be absolute;
// Runtime validates that ownership before calling this transport helper.
func (c *Client) NewSession(ctx context.Context, params NewSessionParams) (NewSessionResult, error) {
	var result NewSessionResult
	if err := c.Call(ctx, "session/new", params, &result); err != nil {
		return NewSessionResult{}, err
	}
	if result.SessionID == "" {
		return NewSessionResult{}, fmt.Errorf("ACP session/new returned an empty sessionId")
	}
	return result, nil
}

// LoadSession resumes one remote ACP session after explicit capability and
// reconnect policy checks by the caller.
func (c *Client) LoadSession(ctx context.Context, params LoadSessionParams) (LoadSessionResult, error) {
	var result LoadSessionResult
	if err := c.Call(ctx, "session/load", params, &result); err != nil {
		return LoadSessionResult{}, err
	}
	if result.SessionID == "" {
		result.SessionID = params.SessionID
	}
	return result, nil
}

// ResumeSession reconnects to an existing remote session without history
// replay. ACP's success result may be empty, so the request ID remains the
// caller's session identity.
func (c *Client) ResumeSession(ctx context.Context, params ResumeSessionParams) error {
	if params.SessionID == "" {
		return fmt.Errorf("ACP session/resume requires a sessionId")
	}
	return c.Call(ctx, "session/resume", params, nil)
}

// Prompt starts one remote turn and waits for its canonical stop reason.
func (c *Client) Prompt(ctx context.Context, params PromptParams) (PromptResult, error) {
	var result PromptResult
	if err := c.Call(ctx, "session/prompt", params, &result); err != nil {
		return PromptResult{}, err
	}
	if result.StopReason == "" {
		return PromptResult{}, fmt.Errorf("ACP session/prompt returned an empty stopReason")
	}
	return result, nil
}

// Cancel asks the Agent to cancel its active prompt turn. It is a JSON-RPC
// notification and therefore has no response.
func (c *Client) Cancel(sessionID string) error {
	return c.Notify("session/cancel", CancelParams{SessionID: sessionID})
}

// CloseSession frees a remote session when the Agent advertised the standard
// close capability. Callers should cancel an active prompt before closing.
func (c *Client) CloseSession(ctx context.Context, sessionID string) error {
	err := c.Call(ctx, "session/close", CloseSessionParams{SessionID: sessionID}, nil)
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32002 {
		// ACP defines close as idempotent; an unknown/already-closed session is
		// a successful close for the caller.
		return nil
	}
	return err
}
