package client

import (
	"context"
	"encoding/json"
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

// ContentBlock is the subset of ACP content needed by the MVP text prompt.
// Additional protocol content is added only through the Runtime input contract.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type NewSessionParams struct {
	Cwd string `json:"cwd"`
}

type NewSessionResult struct {
	SessionID string `json:"sessionId"`
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

// DecodeSessionUpdate decodes a session/update notification. It returns false
// for any other method or malformed params.
func DecodeSessionUpdate(message Message) (SessionUpdate, bool) {
	if message.Method != "session/update" {
		return SessionUpdate{}, false
	}
	var params struct {
		Update SessionUpdate `json:"update"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil {
		return SessionUpdate{}, false
	}
	if params.Update.SessionUpdate == "" {
		return SessionUpdate{}, false
	}
	return params.Update, true
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
	return c.Call(ctx, "session/close", CloseSessionParams{SessionID: sessionID}, nil)
}
