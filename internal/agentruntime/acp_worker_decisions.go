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
// this session. ACPChildBackend acquires the permit while constructing the
// child, and the proxy releases it on the terminal path or when a child is
// destroyed before it runs.
//
// limit is the Runtime-resolved read-only ceiling for the profile, so the
// configured value is consumed here rather than being descriptive only. Write
// roles are additionally serialized by the role-aware admission check.
func (r *SessionRuntime) acquireACPWorkerPermit(limit int) (func(), error) {
	return r.acquireACPWorkerPermitForRole(limit, SubagentRoleInvestigate)
}

// acquireACPWorkerPermitForRole treats a writer as a base-workspace writer. Use
// acquireACPWorkerPermitForWorkspace for an implementation worker in a managed
// worktree so it can run alongside readers while integration stays exclusive.
func (r *SessionRuntime) acquireACPWorkerPermitForRole(limit int, role SubagentRole) (func(), error) {
	return r.acquireACPWorkerPermitForWorkspace(limit, role, false, false, nil)
}

// acquireACPWorkerPermitForWorkspace applies the profile fan-out ceiling and the
// base-workspace write lane separately. Read-only workers and isolated
// implementation workers may share the ceiling. A base-workspace writer is
// exclusive in both directions unless the profile explicitly opts into parallel
// base-workspace writes, in which case it shares the ordinary ceiling.
//
// allowParallelBaseWrites is the profile's explicit trust decision. An external
// Agent runs its own tools, so a disjoint write_scope does not prove the workers
// will not touch the same file; the caller that enables this accepts that risk.
//
// writeScope is the contract's declared scope. When parallel base writes are
// allowed, a writer whose scope overlaps a live base writer is still refused:
// that overlap is statically visible, and refusing it is the one part of the
// risk that can be checked without confining the worker's own tools. A writer
// with no declared scope is treated as overlapping every other writer.
func (r *SessionRuntime) acquireACPWorkerPermitForWorkspace(limit int, role SubagentRole, isolated, allowParallelBaseWrites bool, writeScope []string) (func(), error) {
	if r == nil {
		return nil, fmt.Errorf("agent runtime is nil")
	}
	if strings.TrimSpace(string(role)) == "" {
		role = SubagentRoleInvestigate
	}
	if limit <= 0 {
		limit = 1
	}
	writes := subagentRoleWrites(role)
	baseWriter := writes && !isolated
	// A parallel base writer still consumes the ordinary ceiling; only the
	// bidirectional exclusivity is relaxed. A profile that opts in with the
	// default ceiling of one still serializes.
	parallelBaseWriter := baseWriter && allowParallelBaseWrites && limit > 1
	r.acpWorkerMu.Lock()
	defer r.acpWorkerMu.Unlock()
	if baseWriter && !parallelBaseWriter && r.acpWorkersLive > 0 {
		return nil, fmt.Errorf("an external base-workspace writer requires exclusive access; wait for the current external worker to finish")
	}
	if r.acpBaseWritersLive > 0 && !parallelBaseWriter {
		return nil, fmt.Errorf("an external base-workspace writer is active; wait for it to finish before starting another worker")
	}
	if parallelBaseWriter {
		for _, live := range r.acpBaseWriterScopes {
			if subagentWriteScopesOverlap(live.Scope, writeScope) {
				return nil, fmt.Errorf(
					"an external base-workspace writer is already modifying %q; this task's write scope overlaps it",
					strings.Join(live.Scope, ", "),
				)
			}
		}
	}
	if r.acpWorkersLive >= limit {
		if limit == 1 {
			return nil, fmt.Errorf("an external ACP worker is already running for this session")
		}
		return nil, fmt.Errorf("this session already has %d live external ACP workers (limit %d)", r.acpWorkersLive, limit)
	}
	r.acpWorkersLive++
	if baseWriter && !parallelBaseWriter {
		r.acpBaseWritersLive++
	}
	scopeEntry := -1
	if parallelBaseWriter {
		scopeEntry = len(r.acpBaseWriterScopes)
		r.acpBaseWriterScopes = append(r.acpBaseWriterScopes, acpBaseWriterScope{Scope: append([]string(nil), writeScope...)})
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.acpWorkerMu.Lock()
			if r.acpWorkersLive > 0 {
				r.acpWorkersLive--
			}
			if baseWriter && !parallelBaseWriter && r.acpBaseWritersLive > 0 {
				r.acpBaseWritersLive--
			}
			if scopeEntry >= 0 && scopeEntry < len(r.acpBaseWriterScopes) {
				r.acpBaseWriterScopes = append(r.acpBaseWriterScopes[:scopeEntry], r.acpBaseWriterScopes[scopeEntry+1:]...)
			}
			r.acpWorkerMu.Unlock()
		})
	}, nil
}

// acpBaseWriterScope is the declared write scope of one live parallel
// base-workspace writer.
type acpBaseWriterScope struct {
	Scope []string
}

// acpWorkerRequestScope is the already-resolved authorization context for one
// remote session's reverse requests. It is deliberately per-child: a pooled
// process may host read-only and write sessions at the same time, and the
// connection routes each request to the matching session handler.
type acpWorkerRequestScope struct {
	ParentRunID string
	ChildID     string
	Profile     config.ACPAgentProfile
	Contract    SubagentTaskContract
	WorkDir     string
	Strategy    config.ACPPermissionPolicy
	Workspace   *acpWorkerWorkspace
}

// newACPRequestHandler projects an external Agent's reverse requests into the
// canonical decision path. Router only supports non-blocking strategies, so the
// handler answers immediately after recording the decision.
func (r *SessionRuntime) newACPRequestHandler(scope acpWorkerRequestScope, emit func(agentpkg.Event)) acpclient.RequestHandler {
	return func(ctx context.Context, message acpclient.Message) (any, *acpclient.RPCError) {
		if message.Method == "fs/read_text_file" {
			return r.handleACPReadTextFile(ctx, message, scope.Workspace)
		}
		if message.Method == "fs/write_text_file" {
			return r.handleACPWriteTextFile(ctx, message, scope)
		}
		if message.Method != "session/request_permission" {
			return nil, &acpclient.RPCError{Code: -32601, Message: "method not found"}
		}
		request, ok := acpclient.DecodePermissionRequest(message)
		if !ok {
			return nil, &acpclient.RPCError{Code: -32602, Message: "invalid permission request"}
		}
		option, allowed := selectACPPermissionOption(request.Options, scope.Strategy)
		decisionID := acpPermissionDecisionID(scope.ChildID, request, message.ID)
		toolArgs := decodeACPToolInput(request.ToolCall.RawInput)
		steps := DecisionRequest{
			ID: decisionID, RunID: scope.ParentRunID, SessionID: r.sessionIDForDecisions(), Kind: DecisionApproval,
		}
		payload := map[string]any{
			"profileId": scope.Profile.ID, "remoteSessionId": request.SessionID,
			"toolCallId": request.ToolCall.ToolCallID, "toolCallTitle": request.ToolCall.Title,
			"toolCallKind": request.ToolCall.Kind, "rawInput": json.RawMessage(request.ToolCall.RawInput),
			"offeredOptions": request.Options, "strategy": string(scope.Strategy),
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
		if scope.ParentRunID != "" {
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
				ApprovalResult: scope.Strategy == config.ACPPermissionAutoAllowOnce,
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
