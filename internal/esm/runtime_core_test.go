package esm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/startvibecoding/mothx/internal/session"
)

func TestRoleContextLeavesLongRunningRolesWithoutDeadline(t *testing.T) {
	ctx, cancel := RoleContext(context.Background(), RoleWorker)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("worker ESM role unexpectedly has a deadline")
	}
}

func TestRoleContextBoundsRecoveryObserver(t *testing.T) {
	before := time.Now()
	ctx, cancel := RoleContext(context.Background(), RoleRecovery)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("recovery observer must retain its bounded deadline")
	}
	if got := deadline.Sub(before); got < RecoveryObserverTimeout-time.Second || got > RecoveryObserverTimeout+time.Second {
		t.Fatalf("recovery deadline = %s from now, want approximately %s", got, RecoveryObserverTimeout)
	}
}

type runtimeTestAdapter struct {
	responses map[Role]string
	roles     []Role
	prompts   map[Role]string
	requests  map[Role]RoleRequest
	roleErr   error
	observers int
}

type runtimeTestEvents struct {
	events []RuntimeEvent
}

func (e *runtimeTestEvents) PublishESMEvent(_ context.Context, event RuntimeEvent) error {
	e.events = append(e.events, event)
	return nil
}

func (a *runtimeTestAdapter) RunRole(_ context.Context, req RoleRequest) (RoleResult, error) {
	a.roles = append(a.roles, req.Role)
	if a.requests != nil {
		a.requests[req.Role] = req
	}
	if a.prompts != nil {
		a.prompts[req.Role] = req.Prompt
	}
	if a.roleErr != nil {
		return RoleResult{}, a.roleErr
	}
	return RoleResult{Response: a.responses[req.Role], ToolCalls: 1, ToolError: map[string]bool{}}, nil
}

func (a *runtimeTestAdapter) RunRecoveryObserver(context.Context, RoleRequest, error) (RoleResult, error) {
	a.observers++
	return RoleResult{}, context.Canceled
}

func newRuntimeTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	sess := session.New(root, root)
	if err := sess.Init(); err != nil {
		t.Fatal(err)
	}
	return NewStore(root), sess.GetHeader().ID
}

func TestSupervisorWorkerContinueStopsAtActive(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{responses: map[Role]string{
		RoleWorker: `{"status":"continue","summary":"made progress","evidence":["read source"],"remaining_work":["finish objective"],"blockers":[]}`,
	}}
	obj, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, "run-1", rootForRuntimeTest(t), "agent")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Status != StatusActive {
		t.Fatalf("status=%s, want active", obj.Status)
	}
	if !reflect.DeepEqual(adapter.roles, []Role{RoleWorker}) {
		t.Fatalf("roles=%v, want worker only", adapter.roles)
	}
}

func TestSupervisorCompletionUsesCriticThenAudit(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{responses: map[Role]string{
		RoleWorker: `{"status":"complete_candidate","summary":"done","evidence":["tests pass"],"remaining_work":[],"blockers":[]}`,
		RoleCritic: `{"verdict":"pass","review":"critic verified","requirements_checked":["objective -> covered"],"missing_work":[],"evidence":["read source"]}`,
		RoleAudit:  `{"verdict":"pass","review":"audit verified","requirements_checked":["objective -> covered"],"missing_work":[],"evidence":["read source"]}`,
	}}
	obj, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, "run-1", rootForRuntimeTest(t), "agent")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Status != StatusComplete {
		t.Fatalf("status=%s, want complete", obj.Status)
	}
	if !reflect.DeepEqual(adapter.roles, []Role{RoleWorker, RoleCritic, RoleAudit}) {
		t.Fatalf("roles=%v", adapter.roles)
	}
}

func TestSupervisorPublishesLifecycleEvents(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{responses: map[Role]string{RoleWorker: `{"status":"continue","summary":"progress","evidence":["inspection"],"remaining_work":["finish"],"blockers":[]}`}}
	events := &runtimeTestEvents{}
	if _, err := (&Supervisor{Store: store, Adapter: adapter, Events: events}).Run(ctx, sessionID, "run-events", rootForRuntimeTest(t), "agent"); err != nil {
		t.Fatal(err)
	}
	if len(events.events) != 2 || events.events[0].Type != "role_started" || events.events[1].Type != "role_finished" {
		t.Fatalf("events=%#v", events.events)
	}
}

func TestSupervisorRepeatedRecoveryStaysActiveAndUsesObserver(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := store.RecordRecovery(ctx, sessionID, "previous interruption", "retry", []string{"finish"}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := &runtimeTestAdapter{roleErr: context.DeadlineExceeded}
	obj, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, "run-limit", rootForRuntimeTest(t), "agent")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Status != StatusActive || obj.RecoveryCount != 6 || adapter.observers != 1 {
		t.Fatalf("objective=%#v observers=%d", obj, adapter.observers)
	}
}

func TestSupervisorIncompleteRoleRecoversAndKeepsObjectiveActive(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{roleErr: NewRoleIncompleteError(RoleWorker, "max_iterations", nil)}
	obj, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, "run-incomplete", rootForRuntimeTest(t), "yolo")
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if obj == nil || obj.Status != StatusActive || obj.RecoveryCount != 1 || !obj.CanAutoRun() {
		t.Fatalf("incomplete role did not recover: %#v", obj)
	}
}

func TestSupervisorRolesUseUnboundedLongTaskIterations(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{requests: make(map[Role]RoleRequest), responses: map[Role]string{
		RoleWorker: `{"status":"complete_candidate","summary":"done","evidence":["tests pass"],"remaining_work":[],"blockers":[]}`,
		RoleCritic: `{"verdict":"pass","review":"critic verified","requirements_checked":["objective -> covered"],"missing_work":[],"evidence":["read source"]}`,
		RoleAudit:  `{"verdict":"pass","review":"audit verified","requirements_checked":["objective -> covered"],"missing_work":[],"evidence":["read source"]}`,
	}}
	if _, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, "run-unbounded", rootForRuntimeTest(t), "yolo"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []Role{RoleWorker, RoleCritic, RoleAudit} {
		if got := adapter.requests[role].MaxIterations; got != LongTaskMaxIterations {
			t.Fatalf("%s MaxIterations = %d, want %d", role, got, LongTaskMaxIterations)
		}
	}
}

func TestSupervisorNonRetryableFailurePausesUntilExplicitResume(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("provider rejected the request")
	adapter := &runtimeTestAdapter{roleErr: wantErr}
	obj, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, "run-failed", rootForRuntimeTest(t), "yolo")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
	if obj == nil || obj.Status != StatusPaused || obj.CanAutoRun() {
		t.Fatalf("failed objective = %#v, want paused and not runnable", obj)
	}
	stored, err := store.Get(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusPaused {
		t.Fatalf("persisted status = %s, want paused", stored.Status)
	}
}

func TestSupervisorSharedStorePersistsAcrossRuntimeInstances(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	response := `{"status":"continue","summary":"progress","evidence":["inspection"],"remaining_work":["finish"],"blockers":[]}`
	first := &runtimeTestAdapter{responses: map[Role]string{RoleWorker: response}}
	second := &runtimeTestAdapter{responses: map[Role]string{RoleWorker: response}}
	if _, err := (&Supervisor{Store: store, Adapter: first}).Run(ctx, sessionID, "tui-run", rootForRuntimeTest(t), "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Supervisor{Store: store, Adapter: second}).Run(ctx, sessionID, "webui-run", rootForRuntimeTest(t), "agent"); err != nil {
		t.Fatal(err)
	}
	obj, err := store.Get(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if obj.ProgressSummary != "progress" || len(first.roles) != 1 || len(second.roles) != 1 {
		t.Fatalf("persisted objective=%#v roles=%v/%v", obj, first.roles, second.roles)
	}
}

func rootForRuntimeTest(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// TestSupervisorRejectedCompletionsContinueAcrossContinuations verifies that a
// rejected completion remains work to do rather than an unattended circuit
// breaker that pauses the long-running objective.
func TestSupervisorRejectedCompletionsContinueAcrossContinuations(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	adapter := &runtimeTestAdapter{responses: map[Role]string{
		RoleWorker: `{"status":"complete_candidate","summary":"done","evidence":["tests pass"],"remaining_work":[],"blockers":[]}`,
		RoleCritic: `{"verdict":"fail","review":"missing regression tests","requirements_checked":["objective -> gap"],"missing_work":["add regression tests"],"evidence":["read source"]}`,
	}}
	for i := 1; i <= 4; i++ {
		obj, err := (&Supervisor{Store: store, Adapter: adapter}).Run(ctx, sessionID, fmt.Sprintf("run-%d", i), rootForRuntimeTest(t), "yolo")
		if err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
		if obj.Status != StatusActive || obj.RejectionCount != i || !obj.CanAutoRun() {
			t.Fatalf("after continuation %d: %#v", i, obj)
		}
	}
	obj, err := store.Get(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !obj.CanAutoRun() {
		t.Fatalf("rejected completion stopped continuation: %#v", obj)
	}
}

// TestSupervisorBlockedAuditAccumulatesAcrossContinuations verifies the
// three-run blocked audit across continuations and that an intervening
// successful continue clears the stale streak.
func TestSupervisorBlockedAuditAccumulatesAcrossContinuations(t *testing.T) {
	store, sessionID := newRuntimeTestStore(t)
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "finish the objective"); err != nil {
		t.Fatal(err)
	}
	blocked := &runtimeTestAdapter{responses: map[Role]string{
		RoleWorker: `{"status":"blocked_candidate","summary":"cannot proceed","evidence":["attempted provisioning"],"remaining_work":[],"blockers":["missing API token"]}`,
	}}
	continueAdapter := &runtimeTestAdapter{responses: map[Role]string{
		RoleWorker: `{"status":"continue","summary":"progress","evidence":["inspection"],"remaining_work":["finish"],"blockers":[]}`,
	}}

	obj, err := (&Supervisor{Store: store, Adapter: blocked}).Run(ctx, sessionID, "run-1", rootForRuntimeTest(t), "yolo")
	if err != nil {
		t.Fatal(err)
	}
	if obj.Status != StatusActive || obj.BlockedCount != 1 {
		t.Fatalf("after run-1: status=%s blockedCount=%d", obj.Status, obj.BlockedCount)
	}

	// A continuation that finishes without the blocker clears the streak.
	obj, err = (&Supervisor{Store: store, Adapter: continueAdapter}).Run(ctx, sessionID, "run-2", rootForRuntimeTest(t), "yolo")
	if err != nil {
		t.Fatal(err)
	}
	if obj.BlockedCount != 0 {
		t.Fatalf("stale blocked streak not cleared: %#v", obj)
	}

	for i := 3; i <= 5; i++ {
		obj, err = (&Supervisor{Store: store, Adapter: blocked}).Run(ctx, sessionID, fmt.Sprintf("run-%d", i), rootForRuntimeTest(t), "yolo")
		if err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if obj.Status != StatusBlocked {
		t.Fatalf("after repeated blocker: status=%s blockedCount=%d, want blocked", obj.Status, obj.BlockedCount)
	}
}
