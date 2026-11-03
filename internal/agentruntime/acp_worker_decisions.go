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

// acquireACPWorkerPermit enforces the MVP's one-live-external-worker rule per
// session. The permit is released by the child proxy on every terminal path.
func (r *SessionRuntime) acquireACPWorkerPermit() (func(), error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	r.acpWorkerMu.Lock()
	defer r.acpWorkerMu.Unlock()
	if r.acpWorkerBusy {
		return nil, fmt.Errorf("an external ACP worker is already running for this session")
	}
	r.acpWorkerBusy = true
	var once sync.Once
	return func() {
		once.Do(func() {
			r.acpWorkerMu.Lock()
			r.acpWorkerBusy = false
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
		if cancelled {
			value = ""
			payload["selectedOptionId"] = ""
			if parentRunID != "" {
				r.recordACPDecision(steps, DecisionStatusPending, "", payload)
				r.recordACPDecision(steps, DecisionStatusCancelled, "cancelled", payload)
			}
			return acpclient.PermissionCancelled(), nil
		}
		payload["selectedOptionId"] = value
		if parentRunID != "" {
			r.recordACPDecision(steps, DecisionStatusPending, "", payload)
			decisions := r.decisionService()
			if decisions != nil {
				if err := decisions.Register(steps); err == nil {
					if _, resolveErr := decisions.ResolveWith(DecisionResolution{ID: decisionID, Kind: DecisionApproval, Status: DecisionStatusResolved, Value: value},
						func(resolved DecisionRequest) error {
							r.recordACPDecision(resolved, DecisionStatusResolved, value, payload)
							return nil
						}); resolveErr != nil {
						// A failed resolve must not leave an unrecoverable pending row.
						r.recordACPDecision(steps, DecisionStatusCancelled, "cancelled", payload)
					}
				} else {
					r.recordACPDecision(steps, DecisionStatusResolved, value, payload)
				}
			} else {
				r.recordACPDecision(steps, DecisionStatusResolved, value, payload)
			}
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
