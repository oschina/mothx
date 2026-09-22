package esm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

// Role is the role in the TUI-defined ESM pipeline.
type Role string

const (
	RoleWorker   Role = "worker"
	RoleCritic   Role = "critic"
	RoleAudit    Role = "audit"
	RoleRecovery Role = "recovery"
)

// LongTaskMaxIterations deliberately leaves an ESM worker or reviewer
// unbounded. ESM owns long-running objectives, so an arbitrary turn count
// must never turn ongoing work into a completed-looking role result.
const LongTaskMaxIterations = -1

// ErrRoleIncomplete identifies a role that stopped before completing its
// assigned work. It is recoverable for an active ESM objective: the next
// continuation resumes from persisted repository state rather than treating
// the partial role as success or pausing the objective.
var ErrRoleIncomplete = errors.New("ESM role incomplete")

// NewRoleIncompleteError preserves an adapter's terminal detail while giving
// Supervisor one shared classification independent of protocol projection.
func NewRoleIncompleteError(role Role, stopReason string, cause error) error {
	detail := strings.TrimSpace(stopReason)
	if detail == "" {
		detail = "stopped before completing its work"
	}
	if role != "" {
		detail = string(role) + " " + detail
	}
	if cause != nil {
		return fmt.Errorf("%w: %s: %v", ErrRoleIncomplete, detail, cause)
	}
	return fmt.Errorf("%w: %s", ErrRoleIncomplete, detail)
}

const (
	// RoleTimeout intentionally has no hard deadline. ESM is the long-task
	// execution mode, so it continues until its work finishes or an explicit
	// cancellation/ownership decision stops it.
	RoleTimeout             time.Duration = 0
	RecoveryObserverTimeout               = 5 * time.Minute
)

// RoleContext applies the ESM-owned deadline policy once for every adapter.
// A zero timeout preserves the parent context rather than creating an already
// expired context with context.WithTimeout.
func RoleContext(parent context.Context, role Role) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := RoleTimeout
	if role == RoleRecovery {
		timeout = RecoveryObserverTimeout
	}
	if timeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, timeout)
}

// RoleRequest is the complete host execution contract. The host runs an
// isolated agent; it must not make ESM state decisions.
type RoleRequest struct {
	SessionID     string
	RunID         string
	Role          Role
	WorkDir       string
	Mode          string
	Tools         []string
	MaxIterations int
	Prompt        string
	Objective     *Objective
}

// RuntimeAdapter is implemented by TUI/WebUI agent hosts. ESM policy remains
// in Supervisor; adapters only execute roles and project host events.
type RuntimeAdapter interface {
	RunRole(context.Context, RoleRequest) (RoleResult, error)
	RunRecoveryObserver(context.Context, RoleRequest, error) (RoleResult, error)
}

// RuntimeEvent is UI-neutral lifecycle output. It is informational only.
type RuntimeEvent struct {
	SessionID string
	RunID     string
	Role      Role
	Type      string
	Status    string
	Message   string
}

type RuntimeEventSink interface {
	PublishESMEvent(context.Context, RuntimeEvent) error
}

// Supervisor is the single ESM runtime extracted from the TUI implementation.
type Supervisor struct {
	Store   *Store
	Adapter RuntimeAdapter
	Events  RuntimeEventSink
}

// Run executes one continuation using the TUI order: worker, then critic and
// audit only for a completion candidate. A normal worker continue returns
// active and is resumed by the next continuation.
//
// runID is the base continuation run ID. All durable state reporting inside
// the continuation uses this base ID so FinishRun and the per-run idempotency
// checks line up; role sub-agents and events keep suffixed IDs derived from
// it. The Supervisor owns the terminal FinishRun call for both adapters.
func (s *Supervisor) Run(ctx context.Context, sessionID, runID, workDir, mode string) (*Objective, error) {
	if s == nil || s.Store == nil {
		return nil, errors.New("esm supervisor store is nil")
	}
	if s.Adapter == nil {
		return nil, errors.New("esm supervisor adapter is nil")
	}
	obj, err := s.Store.Get(ctx, sessionID)
	if err != nil || obj == nil || !obj.CanAutoRun() {
		return obj, err
	}
	if obj.Status == StatusActive {
		obj, err = s.runRole(ctx, obj, RoleWorker, runID, workDir, mode)
		if err != nil || obj == nil || obj.Status != StatusCompleteCandidate {
			return obj, err
		}
	}
	if obj.Status != StatusCompleteCandidate {
		return s.finishContinuation(ctx, sessionID, runID, obj)
	}
	obj, err = s.runRole(ctx, obj, RoleCritic, runID, workDir, mode)
	if err != nil || obj == nil || obj.Status != StatusCompleteCandidate {
		return obj, err
	}
	obj, err = s.runRole(ctx, obj, RoleAudit, runID, workDir, mode)
	if err != nil {
		return obj, err
	}
	return s.finishContinuation(ctx, sessionID, runID, obj)
}

// finishContinuation records that the continuation ended using the base run
// ID: streaks recorded by this continuation's roles survive, and stale streaks
// left by earlier continuations are cleared.
func (s *Supervisor) finishContinuation(ctx context.Context, sessionID, runID string, obj *Objective) (*Objective, error) {
	if obj == nil {
		return nil, nil
	}
	if finished, err := s.Store.FinishRun(ctx, sessionID, runID); err == nil && finished != nil {
		return finished, nil
	}
	return obj, nil
}

func (s *Supervisor) runRole(ctx context.Context, obj *Objective, role Role, baseRunID, workDir, mode string) (*Objective, error) {
	runID := baseRunID + "-" + string(role)
	req := RoleRequest{SessionID: obj.SessionID, RunID: runID, Role: role, WorkDir: workDir, Mode: mode, Objective: obj, Prompt: rolePrompt(obj, role)}
	// Queued user guidance is injected into every non-recovery role prompt and
	// consumed once the role result is applied, so both adapters share one
	// guidance lifecycle owned by the core.
	var guidanceIDs []string
	if role != RoleRecovery && s.Store != nil {
		if guidance, guidanceErr := s.Store.PendingGuidance(ctx, obj.SessionID); guidanceErr == nil && len(guidance) > 0 {
			req.Prompt += FormatGuidanceSuffix(guidance)
			for _, item := range guidance {
				guidanceIDs = append(guidanceIDs, item.ID)
			}
		}
	}
	phase := PhaseWorker
	switch role {
	case RoleWorker:
		req.MaxIterations = LongTaskMaxIterations
	case RoleCritic, RoleAudit:
		phase = PhaseCritic
		if role == RoleAudit {
			phase = PhaseAudit
		}
		req.MaxIterations = LongTaskMaxIterations
		req.Tools = []string{"read", "grep", "find", "ls"}
	default:
		return obj, fmt.Errorf("unsupported ESM role %q", role)
	}
	if _, err := s.Store.SetPhase(ctx, obj.SessionID, phase); err != nil {
		return obj, err
	}
	_ = s.publish(ctx, RuntimeEvent{SessionID: obj.SessionID, RunID: runID, Role: role, Type: "role_started", Status: "running"})

	started := time.Now()
	result, runErr := s.Adapter.RunRole(ctx, req)
	if result.DurationMS <= 0 {
		result.DurationMS = time.Since(started).Milliseconds()
	}
	obj, err := s.account(ctx, obj, result)
	if err != nil {
		return obj, err
	}
	if runErr != nil {
		return s.handleRoleFailure(ctx, obj, req, baseRunID, result, runErr)
	}

	var outcome Outcome
	var ok bool
	if role == RoleWorker {
		outcome, ok, err = ApplyWorkerResult(ctx, s.Store, obj.SessionID, baseRunID, result)
	} else {
		outcome, ok, err = ApplyReviewResult(ctx, s.Store, obj.SessionID, baseRunID, string(role), result)
	}
	if err != nil {
		return obj, err
	}
	if !ok {
		return obj, fmt.Errorf("ESM %s result was not applied", role)
	}
	if len(guidanceIDs) > 0 {
		_ = s.Store.ConsumeGuidance(ctx, obj.SessionID, guidanceIDs)
	}
	if outcome.Objective != nil {
		obj = outcome.Objective
	}
	message := outcome.Message
	if outcome.Rejected {
		message = outcome.Subject + " rejected: " + outcome.Reason
	}
	_ = s.publish(ctx, RuntimeEvent{SessionID: obj.SessionID, RunID: runID, Role: role, Type: "role_finished", Status: string(obj.Status), Message: message})
	return obj, nil
}

func (s *Supervisor) account(ctx context.Context, obj *Objective, result RoleResult) (*Objective, error) {
	if result.Tokens == 0 && result.DurationMS == 0 {
		return obj, nil
	}
	return s.Store.AccountUsage(ctx, obj.SessionID, result.Tokens, result.DurationMS)
}

func (s *Supervisor) handleRoleFailure(ctx context.Context, obj *Objective, req RoleRequest, baseRunID string, result RoleResult, runErr error) (*Objective, error) {
	role := string(req.Role)
	_ = s.publish(ctx, RuntimeEvent{SessionID: obj.SessionID, RunID: req.RunID, Role: req.Role, Type: "role_failed", Status: "failed", Message: compactESMError(runErr)})
	if req.Role != RoleWorker {
		review := fmt.Sprintf("%s sub-agent failed; completion candidate rejected: %s", titleESMRole(role), compactESMError(runErr))
		if next, err := s.Store.RejectCompletionCandidateForRun(ctx, obj.SessionID, baseRunID, review, nil); err == nil {
			obj = next
		}
	}
	if errors.Is(runErr, ErrRoleIncomplete) {
		return s.recordRecovery(ctx, obj, role+" stopped before completion: "+compactESMError(runErr), obj.RemainingWork)
	}
	if errors.Is(runErr, context.DeadlineExceeded) {
		return s.runRecoveryObserver(ctx, obj, req, baseRunID, runErr)
	}
	if isRetryableTransportError(runErr) {
		return s.recordRecovery(ctx, obj, role+" provider transport failure: "+compactESMError(runErr), obj.RemainingWork)
	}
	// A non-retryable role failure must require an explicit Resume before the
	// objective can run again. Leaving the row active lets any future trigger
	// (including guidance or a startup reconciler) silently re-run the failed
	// task. Preserve the original execution error for the caller even if the
	// status write fails.
	if paused, pauseErr := s.Store.Pause(ctx, obj.SessionID); pauseErr == nil && paused != nil {
		obj = paused
	}
	return obj, runErr
}

func (s *Supervisor) runRecoveryObserver(ctx context.Context, obj *Objective, req RoleRequest, baseRunID string, interruption error) (*Objective, error) {
	observer := req
	observer.Role = RoleRecovery
	observer.RunID = baseRunID + "-recovery-observer"
	observer.MaxIterations = 40
	observer.Tools = []string{"read", "grep", "find", "ls"}
	observer.Prompt = RecoveryObserverTaskPrompt(obj, string(req.Role), compactESMError(interruption))
	started := time.Now()
	result, err := s.Adapter.RunRecoveryObserver(ctx, observer, interruption)
	if result.DurationMS <= 0 {
		result.DurationMS = time.Since(started).Milliseconds()
	}
	if _, accountErr := s.account(ctx, obj, result); accountErr != nil {
		return obj, accountErr
	}
	if err != nil {
		return s.recordRecovery(ctx, obj, string(req.Role)+" observer failed: "+compactESMError(err), obj.RemainingWork)
	}
	if result.ToolCalls == 0 || len(result.ToolError) >= result.ToolCalls {
		return s.recordRecovery(ctx, obj, string(req.Role)+" observer inspection was not usable", obj.RemainingWork)
	}
	report, err := ParseRecoveryReport(result.Response)
	if err != nil {
		return s.recordRecovery(ctx, obj, string(req.Role)+" observer report invalid: "+compactESMError(err), obj.RemainingWork)
	}
	next, err := s.Store.RecordRecovery(ctx, obj.SessionID, string(req.Role)+" timed out: "+compactESMError(interruption), "Recovery observer: "+report.Summary, report.RemainingWork)
	if err != nil || next.Status == StatusPaused {
		return next, err
	}
	if report.Decision == RecoveryDecisionBlocked {
		return s.Store.UpdateFromModelForRun(ctx, obj.SessionID, StatusBlocked, FormatItemDetail("recovery observer blockers", report.Blockers), baseRunID)
	}
	return next, nil
}

func (s *Supervisor) recordRecovery(ctx context.Context, obj *Objective, reason string, remaining []string) (*Objective, error) {
	return s.Store.RecordRecovery(ctx, obj.SessionID, reason, "A fresh worker will retry from the persisted repository state.", remaining)
}

func (s *Supervisor) publish(ctx context.Context, event RuntimeEvent) error {
	if s.Events == nil {
		return nil
	}
	return s.Events.PublishESMEvent(ctx, event)
}

func rolePrompt(obj *Objective, role Role) string {
	switch role {
	case RoleWorker:
		return WorkerTaskPrompt(obj)
	case RoleCritic:
		return CriticTaskPrompt(obj)
	case RoleAudit:
		return AuditTaskPrompt(obj)
	default:
		return ""
	}
}

func isRetryableTransportError(err error) bool {
	return err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && strings.Contains(strings.ToLower(err.Error()), "send request:") && provider.IsRetryable(err, 0)
}

func compactESMError(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), "\n", "; ")
}
