package agentruntime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// DurableRun is the adapter-neutral lifecycle row for one execution.
type DurableRun struct {
	ID           string
	SessionID    string
	IntentID     string
	RetryOf      string
	Attempt      int
	WorkDir      string
	Source       string
	Model        string
	Mode         string
	Status       string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Error        string
	ErrorInfo    ErrorInfo
	Progress     RetryInfo
	Usage        json.RawMessage
	ContextUsage json.RawMessage
	// InputResourceIDs are Runtime-prepared resources that this admission must
	// bind to the Run in the same transaction as the intent, Run row, and start
	// event. Retries may reference resources already bound to the original Run;
	// the store preserves that canonical ownership.
	InputResourceIDs      []string
	SubmissionKeyHash     string
	SubmissionScope       string
	SubmissionFingerprint string
	UserEntryID           string
	UserMessage           *provider.Message
	AssistantEntryID      string
	AssistantMessage      *provider.Message
	DeliveryPlan          *DeliveryPlan
	ConversationTurnID    string
	ConversationTurn      bool
}

// DurableRunStore is the persistence boundary used by ExecutionRuntime to
// coordinate canonical run rows with in-memory lifecycle transitions.
type DurableRunStore interface {
	Create(DurableRun) error
	Update(string, RunState, string) error
	Finish(string, RunState, string) error
}

// DurableRunMetadataStore is an optional extension implemented by stores that
// persist structured recovery state. Keeping it optional preserves test and
// embedded adapters that only need lifecycle rows.
type DurableRunMetadataStore interface {
	UpdateErrorInfo(string, ErrorInfo) error
	UpdateProgress(string, RetryInfo) error
}

// DurableRunUsageStore is an optional metadata extension for providers that
// expose token/context usage before terminalization. Keeping it separate from
// DurableRunMetadataStore preserves embedded stores that predate usage rows.
type DurableRunUsageStore interface {
	UpdateUsage(string, json.RawMessage, json.RawMessage) error
}

// DurableIntentStore is the Runtime-owned persistence boundary for an
// accepted original request. Adapters keep their request decoding private but
// must not create a second intent store or lifecycle chain.
type DurableIntentStore interface {
	CreateIntentAndRun(ExecutionIntent, DurableRun) error
	GetIntent(string) (*ExecutionIntent, error)
}

// DurableIntentEventStore extends intent admission with an atomic started
// event. Stores that implement it prevent a process loss between the Run row
// and its first replay anchor.
type DurableIntentEventStore interface {
	DurableIntentStore
	CreateIntentAndRunWithEvent(ExecutionIntent, DurableRun, RunEvent) (string, error)
}

// DurableConversationTurnStore extends atomic Run admission for executions
// that append a user/assistant transcript. Non-conversation maintenance Runs
// continue to use the smaller interfaces above.
type DurableConversationTurnStore interface {
	CreateIntentAndRunWithEventAndTurn(ExecutionIntent, DurableRun, RunEvent) (string, error)
	CreateRunWithEventAndTurn(DurableRun, RunEvent) (string, error)
}

type DurableConversationTurnFinisher interface {
	FinishConversationTurn(DurableRun, RunState, string) error
}

type DurableConversationTurnEventFinisher interface {
	FinishRunAndConversationTurn(DurableRun, RunState, string, RunEvent) (string, error)
}

// DurableTerminalPersistenceStore marks the explicit, still-non-terminal
// persistence window before the final Run/turn/event transaction. Stores that
// do not implement it retain the legacy behavior for embedded tests.
type DurableTerminalPersistenceStore interface {
	MarkTerminalizing(string, string) error
}

// DurableRunEventStore atomically admits a linked Run and its initial event.
// It is separate from DurableIntentEventStore because retries reuse an
// immutable intent that already exists.
type DurableRunEventStore interface {
	CreateRunWithEvent(DurableRun, RunEvent) (string, error)
}

// ExecutionIntent is the durable, adapter-neutral accepted request. The
// request and policy snapshots remain opaque at this boundary; their owner
// rehydrates them only through the shared Runtime execution path.
type ExecutionIntent = session.ExecutionIntent

// RunStore persists the canonical run row alongside RunEvent records. It
// reuses the existing session_runs schema so startup recovery can discover runs
// from every adapter.
type RunStore struct {
	SessionDir string
}

// LeaseLost exposes the process-local loss signal for the Session lease. The
// ExecutionRuntime uses it to cancel provider/tool work promptly; persistence
// methods still validate the durable epoch and token independently.
func (s RunStore) LeaseLost(sessionID string) <-chan struct{} {
	return session.RuntimeLeaseLost(s.SessionDir, sessionID)
}

// ExecutionBinding returns the exact local lease identity for a newly
// admitted Run. A durable lease row owned elsewhere is an error; absence of a
// lease row remains a compatibility path for embedded/test stores.
func (s RunStore) ExecutionBinding(sessionID, runID string) (session.RuntimeLeaseBinding, bool, error) {
	if strings.TrimSpace(s.SessionDir) == "" {
		return session.RuntimeLeaseBinding{}, false, nil
	}
	binding, ok := session.CurrentRuntimeLeaseBinding(s.SessionDir, sessionID)
	if ok {
		if binding.Purpose != session.RuntimeLeasePurposeExecution || binding.RunID != runID {
			return session.RuntimeLeaseBinding{}, false, session.ErrRuntimeLeaseRunMismatch
		}
		return binding, true, nil
	}
	facts, err := session.ReadSessionExecutionFacts(s.SessionDir, sessionID)
	if err != nil {
		return session.RuntimeLeaseBinding{}, false, err
	}
	if facts.Lease != nil {
		return session.RuntimeLeaseBinding{}, false, session.ErrRuntimeLeaseLost
	}
	return session.RuntimeLeaseBinding{}, false, nil
}

// RetainExecutionLease transfers one reference of the current execution lease
// to the Runtime. The adapter's admission guard can then be released without
// revoking authority needed by a terminal-persistence retry.
func (s RunStore) RetainExecutionLease(sessionID, runID string) (session.RuntimeLeaseBinding, func(), bool, error) {
	return session.RetainRuntimeLease(s.SessionDir, sessionID, runID)
}

// PrepareExistingExecution promotes the current recovery/legacy lease before
// an existing durable Run is reattached to an in-memory ExecutionRuntime.
func (s RunStore) PrepareExistingExecution(sessionID, runID string) error {
	if strings.TrimSpace(s.SessionDir) == "" {
		return nil
	}
	_, err := session.BindRuntimeLeaseToExistingRun(s.SessionDir, sessionID, runID)
	return err
}

func (s RunStore) Create(run DurableRun) error {
	if run.ID == "" || run.SessionID == "" {
		return fmt.Errorf("durable run ID and session ID are required")
	}
	if run.Status == "" {
		run.Status = string(RunStateRunning)
	}
	return session.CreateSessionRun(s.SessionDir, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt, WorkDir: run.WorkDir,
		Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt, UpdatedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Error: run.Error, ErrorInfo: marshalErrorInfo(run.ErrorInfo), Progress: marshalRetryInfo(run.Progress), Usage: run.Usage,
		ContextUsage: run.ContextUsage, InputResourceIDs: append([]string(nil), run.InputResourceIDs...),
		SubmissionKeyHash: run.SubmissionKeyHash, SubmissionScope: run.SubmissionScope, SubmissionFingerprint: run.SubmissionFingerprint,
		UserEntryID: run.UserEntryID, UserMessage: run.UserMessage,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
	})
}

func (s RunStore) Update(runID string, state RunState, message string) error {
	if runID == "" || state == "" {
		return fmt.Errorf("durable run ID and state are required")
	}
	return session.UpdateSessionRunStatus(s.SessionDir, runID, durableRunStatus(state), message, nil)
}

func (s RunStore) Finish(runID string, state RunState, message string) error {
	if !isTerminalRunState(state) {
		return fmt.Errorf("durable run terminal state is invalid: %s", state)
	}
	finishedAt := time.Now()
	return session.UpdateSessionRunStatus(s.SessionDir, runID, durableRunStatus(state), message, &finishedAt)
}

func (s RunStore) MarkTerminalizing(runID, message string) error {
	if runID == "" {
		return fmt.Errorf("durable run ID is required")
	}
	return session.UpdateSessionRunStatus(s.SessionDir, runID, durableRunStatus(RunStateTerminalizing), message, nil)
}

func (s RunStore) UpdateErrorInfo(runID string, info ErrorInfo) error {
	if runID == "" {
		return fmt.Errorf("durable run ID is required")
	}
	return session.UpdateSessionRunErrorInfo(s.SessionDir, runID, marshalErrorInfo(info))
}

func (s RunStore) UpdateProgress(runID string, progress RetryInfo) error {
	if runID == "" {
		return fmt.Errorf("durable run ID is required")
	}
	return session.UpdateSessionRunProgress(s.SessionDir, runID, marshalRetryInfo(progress))
}

func (s RunStore) UpdateUsage(runID string, usage, contextUsage json.RawMessage) error {
	if runID == "" {
		return fmt.Errorf("durable run ID is required")
	}
	return session.UpdateSessionRunUsage(s.SessionDir, runID, usage, contextUsage)
}

func (s RunStore) CreateIntentAndRun(intent ExecutionIntent, run DurableRun) error {
	if run.ID == "" || run.SessionID == "" {
		return fmt.Errorf("durable run ID and session ID are required")
	}
	if run.Status == "" {
		run.Status = string(RunStateRunning)
	}
	if run.IntentID == "" {
		run.IntentID = intent.ID
	}
	return session.CreateExecutionIntentAndSessionRun(s.SessionDir, intent, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt,
		WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt, UpdatedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Error: run.Error, ErrorInfo: marshalErrorInfo(run.ErrorInfo), Progress: marshalRetryInfo(run.Progress), Usage: run.Usage, ContextUsage: run.ContextUsage,
		InputResourceIDs:  append([]string(nil), run.InputResourceIDs...),
		SubmissionKeyHash: run.SubmissionKeyHash, SubmissionScope: run.SubmissionScope, SubmissionFingerprint: run.SubmissionFingerprint,
		UserEntryID: run.UserEntryID, UserMessage: run.UserMessage,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
	})
}

func (s RunStore) CreateIntentAndRunWithEvent(intent ExecutionIntent, run DurableRun, event RunEvent) (string, error) {
	if run.ID == "" || run.SessionID == "" {
		return "", fmt.Errorf("durable run ID and session ID are required")
	}
	if run.Status == "" {
		run.Status = string(RunStateRunning)
	}
	if run.IntentID == "" {
		run.IntentID = intent.ID
	}
	return session.CreateExecutionIntentAndSessionRunEvent(s.SessionDir, intent, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt,
		WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt, UpdatedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Error: run.Error, ErrorInfo: marshalErrorInfo(run.ErrorInfo), Progress: marshalRetryInfo(run.Progress), Usage: run.Usage, ContextUsage: run.ContextUsage,
		InputResourceIDs:  append([]string(nil), run.InputResourceIDs...),
		SubmissionKeyHash: run.SubmissionKeyHash, SubmissionScope: run.SubmissionScope, SubmissionFingerprint: run.SubmissionFingerprint,
		UserEntryID: run.UserEntryID, UserMessage: run.UserMessage,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
	}, sessionRunEventFromRuntime(event))
}

func (s RunStore) CreateIntentAndRunWithEventAndTurn(intent ExecutionIntent, run DurableRun, event RunEvent) (string, error) {
	if run.ID == "" || run.SessionID == "" {
		return "", fmt.Errorf("durable run ID and session ID are required")
	}
	if run.Status == "" {
		run.Status = string(RunStateRunning)
	}
	if run.IntentID == "" {
		run.IntentID = intent.ID
	}
	return session.CreateExecutionIntentAndSessionRunEventWithTurn(s.SessionDir, intent, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt,
		WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt, UpdatedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Error: run.Error, ErrorInfo: marshalErrorInfo(run.ErrorInfo), Progress: marshalRetryInfo(run.Progress), Usage: run.Usage, ContextUsage: run.ContextUsage,
		InputResourceIDs:  append([]string(nil), run.InputResourceIDs...),
		SubmissionKeyHash: run.SubmissionKeyHash, SubmissionScope: run.SubmissionScope, SubmissionFingerprint: run.SubmissionFingerprint,
		UserEntryID: run.UserEntryID, UserMessage: run.UserMessage,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
	}, sessionRunEventFromRuntime(event), session.ConversationTurn{
		ID: run.ConversationTurnID, SessionID: run.SessionID, IntentID: run.IntentID, RunID: run.ID,
		Attempt: run.Attempt, StartedAt: run.StartedAt,
	})
}

func (s RunStore) CreateRunWithEvent(run DurableRun, event RunEvent) (string, error) {
	if run.ID == "" || run.SessionID == "" {
		return "", fmt.Errorf("durable run ID and session ID are required")
	}
	if run.Status == "" {
		run.Status = string(RunStateRunning)
	}
	return session.CreateSessionRunAndEvent(s.SessionDir, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt,
		WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt, UpdatedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Error: run.Error, ErrorInfo: marshalErrorInfo(run.ErrorInfo), Progress: marshalRetryInfo(run.Progress), Usage: run.Usage, ContextUsage: run.ContextUsage,
		InputResourceIDs:  append([]string(nil), run.InputResourceIDs...),
		SubmissionKeyHash: run.SubmissionKeyHash, SubmissionScope: run.SubmissionScope, SubmissionFingerprint: run.SubmissionFingerprint,
		UserEntryID: run.UserEntryID, UserMessage: run.UserMessage,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
	}, sessionRunEventFromRuntime(event))
}

func (s RunStore) CreateRunWithEventAndTurn(run DurableRun, event RunEvent) (string, error) {
	if run.ID == "" || run.SessionID == "" {
		return "", fmt.Errorf("durable run ID and session ID are required")
	}
	if run.Status == "" {
		run.Status = string(RunStateRunning)
	}
	return session.CreateSessionRunAndEventWithTurn(s.SessionDir, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, IntentID: run.IntentID, RetryOf: run.RetryOf, Attempt: run.Attempt,
		WorkDir: run.WorkDir, Source: run.Source, Model: run.Model, Mode: run.Mode, Status: run.Status,
		StartedAt: run.StartedAt, UpdatedAt: run.StartedAt, FinishedAt: run.FinishedAt,
		Error: run.Error, ErrorInfo: marshalErrorInfo(run.ErrorInfo), Progress: marshalRetryInfo(run.Progress), Usage: run.Usage, ContextUsage: run.ContextUsage,
		InputResourceIDs:  append([]string(nil), run.InputResourceIDs...),
		SubmissionKeyHash: run.SubmissionKeyHash, SubmissionScope: run.SubmissionScope, SubmissionFingerprint: run.SubmissionFingerprint,
		UserEntryID: run.UserEntryID, UserMessage: run.UserMessage,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
	}, sessionRunEventFromRuntime(event), session.ConversationTurn{
		ID: run.ConversationTurnID, SessionID: run.SessionID, IntentID: run.IntentID, RunID: run.ID,
		Attempt: run.Attempt, StartedAt: run.StartedAt,
	})
}

func (s RunStore) FinishConversationTurn(run DurableRun, state RunState, message string) error {
	if !run.ConversationTurn || run.ConversationTurnID == "" || run.SessionID == "" {
		return nil
	}
	return session.EndConversationTurn(s.SessionDir, run.SessionID, run.ConversationTurnID, durableRunStatus(state), message, time.Now())
}

func (s RunStore) FinishRunAndConversationTurn(run DurableRun, state RunState, message string, event RunEvent) (string, error) {
	if !run.ConversationTurn || run.ConversationTurnID == "" {
		return "", fmt.Errorf("conversation turn is not configured")
	}
	status := durableRunStatus(state)
	return session.FinishSessionRunAndConversationTurn(s.SessionDir, session.SessionRun{
		ID: run.ID, SessionID: run.SessionID, Status: status, FinishedAt: timePtr(time.Now()), Error: message,
		AssistantEntryID: run.AssistantEntryID, AssistantMessage: run.AssistantMessage,
		DeliveryPlan: sessionDeliveryPlan(run.DeliveryPlan),
	}, sessionRunEventFromRuntime(event), run.ConversationTurnID, status, message)
}

func sessionDeliveryPlan(plan *DeliveryPlan) *session.DeliveryPlan {
	if plan == nil {
		return nil
	}
	result := &session.DeliveryPlan{Intent: session.DeliveryIntent{
		ID: plan.Intent.ID, SessionID: plan.Intent.SessionID, RunID: plan.Intent.RunID,
		Platform: plan.Intent.Platform, TargetID: plan.Intent.TargetID, ReplyMessageID: plan.Intent.ReplyMessageID,
		TransportContext: append(json.RawMessage(nil), plan.Intent.TransportContext...), Status: plan.Intent.Status,
		CreatedAt: plan.Intent.CreatedAt, UpdatedAt: plan.Intent.CreatedAt,
	}}
	for _, operation := range plan.Operations {
		result.Operations = append(result.Operations, session.DeliveryOperation{
			ID: operation.ID, IntentID: plan.Intent.ID, OperationKey: operation.OperationKey,
			ArtifactID: operation.ArtifactID, OperationKind: operation.OperationKind, Sequence: operation.Sequence,
			DependsOn: operation.DependsOn, IdempotencyKey: operation.IdempotencyKey,
			PayloadDigest: operation.PayloadDigest, Status: operation.Status,
			CreatedAt: operation.CreatedAt, UpdatedAt: operation.CreatedAt,
		})
	}
	return result
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func sessionRunEventFromRuntime(event RunEvent) session.SessionRunEvent {
	return session.SessionRunEvent{
		ID: event.ID, SessionID: event.SessionID, RunID: event.RunID, EventType: event.EventType,
		Source: event.Source, Status: event.Status, Model: event.Model, Mode: event.Mode,
		Timestamp: event.Timestamp, Data: event.Data,
	}
}

func (s RunStore) GetIntent(intentID string) (*ExecutionIntent, error) {
	return session.GetExecutionIntent(s.SessionDir, intentID)
}

func durableRunStatus(state RunState) string {
	if state == RunStateCancelled {
		return "cancelled"
	}
	return string(state)
}

func marshalErrorInfo(info ErrorInfo) json.RawMessage {
	if info == (ErrorInfo{}) {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func marshalRetryInfo(info RetryInfo) json.RawMessage {
	if info == (RetryInfo{}) {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}
