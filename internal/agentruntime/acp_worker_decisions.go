package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

// acquireACPWorkerPermit enforces the profile's live-external-worker ceiling for
// this session. The permit is released by the child proxy on every terminal
// path.
//
// limit is the Runtime-resolved ceiling for the profile, so the configured value
// is consumed here rather than being descriptive only. It is one in this release
// (see config.ACPAgentProfile.Validate); the counter exists so raising the
// effective ceiling is a policy change in one place instead of a rewrite of the
// admission rule.
func (r *SessionRuntime) acquireACPWorkerPermit(limit int) (func(), error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	if limit <= 0 {
		limit = 1
	}
	r.acpWorkerMu.Lock()
	defer r.acpWorkerMu.Unlock()
	if r.acpWorkersLive >= limit {
		if limit == 1 {
			return nil, fmt.Errorf("an external ACP worker is already running for this session")
		}
		return nil, fmt.Errorf("this session already has %d live external ACP workers (limit %d)", r.acpWorkersLive, limit)
	}
	r.acpWorkersLive++
	var once sync.Once
	return func() {
		once.Do(func() {
			r.acpWorkerMu.Lock()
			if r.acpWorkersLive > 0 {
				r.acpWorkersLive--
			}
			r.acpWorkerMu.Unlock()
		})
	}, nil
}

// newACPRequestHandler projects an external Agent's reverse requests into the
// canonical decision path. Router only supports non-blocking strategies, so the
// handler answers immediately after recording the decision.
func (r *SessionRuntime) newACPRequestHandler(parentRunID, childID, profileID string, strategy config.ACPPermissionPolicy, emit func(agentpkg.Event)) acpclient.RequestHandler {
	return func(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
		if message.Method != "session/request_permission" {
			return nil, &acpclient.RPCError{Code: -32601, Message: "method not found"}
		}
		request, ok := acpclient.DecodePermissionRequest(message)
		if !ok {
			return nil, &acpclient.RPCError{Code: -32602, Message: "invalid permission request"}
		}
		option, allowed := selectACPPermissionOption(request.Options, strategy)
		decisionID := acpPermissionDecisionID(childID, request, message.ID)
		toolArgs := decodeACPToolInput(request.ToolCall.RawInput)
		steps := DecisionRequest{
			ID: decisionID, RunID: parentRunID, SessionID: r.sessionIDForDecisions(), Kind: DecisionApproval,
		}
		payload := map[string]any{
			"profileId": profileID, "remoteSessionId": request.SessionID,
			"toolCallId": request.ToolCall.ToolCallID, "toolCallTitle": request.ToolCall.Title,
			"toolCallKind": request.ToolCall.Kind, "rawInput": json.RawMessage(request.ToolCall.RawInput),
			"offeredOptions": request.Options, "strategy": string(strategy),
		}
		cancelled := ctx != nil && ctx.Err() != nil
		if !allowed {
			// No offered one-time option matches the configured policy. Never
			// invent an option or upgrade to an always variant.
			cancelled = true
		}
		value := option.OptionID
		status := DecisionStatusResolved
		if cancelled {
			value = ""
			status = DecisionStatusCancelled
			payload["selectedOptionId"] = ""
		} else {
			payload["selectedOptionId"] = value
		}
		if parentRunID != "" {
			r.recordACPDecision(steps, DecisionStatusPending, "", payload)
			decisions := r.decisionService()
			if decisions != nil {
				if err := decisions.Register(steps); err == nil {
					if _, resolveErr := decisions.ResolveWith(DecisionResolution{ID: decisionID, Kind: DecisionApproval, Status: status, Value: value},
						func(resolved DecisionRequest) error {
							r.recordACPDecision(resolved, status, value, payload)
							return nil
						}); resolveErr != nil {
						// The durable row is terminal, so the in-memory pending
						// entry must not survive to be re-projected on reconnect.
						r.recordACPDecision(steps, DecisionStatusCancelled, "cancelled", payload)
						decisions.Discard(decisionID)
					}
				} else {
					r.recordACPDecision(steps, status, value, payload)
				}
			} else {
				r.recordACPDecision(steps, status, value, payload)
			}
		}
		if cancelled {
			return acpclient.PermissionCancelled(), nil
		}
		if emit != nil {
			emit(agentpkg.Event{
				Type:           agentpkg.EventToolApprovalResponse,
				ApprovalID:     decisionID,
				ApprovalTool:   request.ToolCall.Title,
				ApprovalArgs:   toolArgs,
				ApprovalResult: strategy == config.ACPPermissionAutoAllowOnce,
			})
		}
		return acpclient.PermissionSelected(value), nil
	}
}

func (r *SessionRuntime) sessionIDForDecisions() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	id := r.ID
	manager := r.Manager
	r.mu.RUnlock()
	if manager != nil {
		if header := manager.GetHeader(); header != nil && header.ID != "" {
			id = header.ID
		}
	}
	return id
}

func (r *SessionRuntime) decisionService() *DecisionService {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Decisions
}

func (r *SessionRuntime) recordACPDecision(request DecisionRequest, status, value string, payload any) {
	if r == nil || r.Execution == nil || request.RunID == "" {
		return
	}
	sink := r.Execution.eventSink()
	if sink == nil {
		return
	}
	_, _, _, mode, _ := r.ConfigSnapshot()
	_, _ = RecordDecisionEvent(sink, DecisionTransition{
		Request: request, Status: status, Value: value, Payload: payload,
		Source: string(r.Source), Mode: mode,
	})
}

// selectACPPermissionOption chooses only an actually offered one-time option
// that matches the non-blocking strategy. allow_always/reject_always are never
// auto-selected, and a missing option fails closed to "cancelled".
func selectACPPermissionOption(options []acpclient.PermissionOption, strategy config.ACPPermissionPolicy) (acpclient.PermissionOption, bool) {
	wantedKind := ""
	switch strategy {
	case config.ACPPermissionAutoAllowOnce:
		wantedKind = "allow_once"
	case config.ACPPermissionAutoReject:
		wantedKind = "reject_once"
	default:
		return acpclient.PermissionOption{}, false
	}
	for _, option := range options {
		if option.Kind == wantedKind && option.OptionID != "" {
			return option, true
		}
	}
	return acpclient.PermissionOption{}, false
}

func acpPermissionDecisionID(childID string, request acpclient.PermissionRequest, rpcID json.RawMessage) string {
	rpc := strings.Trim(strings.TrimSpace(string(rpcID)), `"`)
	if rpc == "" {
		rpc = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("acp-approval-%s-%s-%s", childID, request.ToolCall.ToolCallID, rpc)
}
