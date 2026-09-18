package tui

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	agentpkg "github.com/startvibecoding/mothx/agent"
	internalagent "github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/config"
	ctxpkg "github.com/startvibecoding/mothx/internal/context"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

const (
	esmWorkerCompleteReport = `{"status":"complete_candidate","summary":"done","evidence":["go test ./internal/esm"],"remaining_work":[],"blockers":[]}`
	esmWorkerBlockedReport  = `{"status":"blocked_candidate","summary":"blocked","evidence":["curl failed"],"remaining_work":["need credentials"],"blockers":["missing API token"]}`
	esmAuditPassReport      = `{"verdict":"pass","review":"verified","requirements_checked":["objective -> covered"],"missing_work":[],"evidence":["go test"]}`
	esmAuditFailReport      = `{"verdict":"fail","review":"missing requirement","requirements_checked":["objective -> gap"],"missing_work":["add test"],"evidence":["read file"]}`
)

func TestESMStoreDirUsesSessionRoot(t *testing.T) {
	root := t.TempDir()
	sess := session.New(root, filepath.Join(root, "sessions"))
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	a := &App{session: sess, settings: config.DefaultSettings()}
	a.ensureESMStore()
	if got := a.esmStoreDir; got != filepath.Join(root, "sessions") {
		t.Fatalf("esm store dir = %q, want %q", got, filepath.Join(root, "sessions"))
	}
}

func newTUITestESMStore(t *testing.T) (*esm.Store, string) {
	t.Helper()
	tmp := t.TempDir()
	sessionDir := filepath.Join(tmp, "sessions")
	sess := session.New(tmp, sessionDir)
	if err := sess.Init(); err != nil {
		t.Fatalf("Init session: %v", err)
	}
	return esm.NewStore(sessionDir), sess.GetHeader().ID
}

func createESMCompletionCandidate(t *testing.T, store *esm.Store, sessionID string) *esm.Objective {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Create(ctx, sessionID, "ship the full objective"); err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	obj, err := store.UpdateFromModelForRun(ctx, sessionID, esm.StatusComplete, "worker evidence", "worker-run")
	if err != nil {
		t.Fatalf("Create completion candidate: %v", err)
	}
	if obj.Status != esm.StatusCompleteCandidate {
		t.Fatalf("status = %s, want complete_candidate", obj.Status)
	}
	return obj
}

func esmAppWithRoleResult(result esmRoleResult) *App {
	return &App{
		esmRoleRunner: func(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, id, workDir, mode string, toolFilter []string, maxIterations int, task string) (esmRoleResult, error) {
			return result, nil
		},
	}
}

func esmToolBackedResult(response string) esmRoleResult {
	return esmRoleResult{
		Response:  response,
		ToolCalls: 1,
		ToolNames: map[string]int{"read": 1},
		ToolError: map[string]bool{},
	}
}

func esmAllToolsFailedResult(response string) esmRoleResult {
	return esmRoleResult{
		Response:  response,
		ToolCalls: 2,
		ToolNames: map[string]int{"read": 1, "bash": 1},
		ToolError: map[string]bool{"call-1": true, "call-2": true},
	}
}

func runESMSupervisorReview(t *testing.T, role string, app *App, store *esm.Store, sessionID string, obj *esm.Objective) bool {
	t.Helper()
	eventCh := make(chan internalagent.Event, 20)
	switch role {
	case "critic":
		return app.runESMCritic(context.Background(), eventCh, nil, store, sessionID, "critic-run", "", "agent", obj)
	case "audit":
		return app.runESMAudit(context.Background(), eventCh, nil, store, sessionID, "audit-run", "", "agent", obj)
	default:
		t.Fatalf("unknown supervisor role %q", role)
		return false
	}
}

func requireESMStatus(t *testing.T, store *esm.Store, sessionID string, want esm.Status) *esm.Objective {
	t.Helper()
	obj, err := store.Get(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Get ESM objective: %v", err)
	}
	if obj.Status != want {
		t.Fatalf("status = %s, want %s; obj=%#v", obj.Status, want, obj)
	}
	return obj
}

func TestESMCriticFailReturnsObjectiveActive(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj := createESMCompletionCandidate(t, store, sessionID)
	app := esmAppWithRoleResult(esmToolBackedResult(esmAuditFailReport))

	if ok := runESMSupervisorReview(t, "critic", app, store, sessionID, obj); !ok {
		t.Fatal("runESMCritic returned false")
	}
	got := requireESMStatus(t, store, sessionID, esm.StatusActive)
	if !strings.Contains(got.CompletionReview, "missing requirement") {
		t.Fatalf("completion review = %q, want critic fail review", got.CompletionReview)
	}
}

func TestESMAuditFailReturnsObjectiveActive(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj := createESMCompletionCandidate(t, store, sessionID)
	app := esmAppWithRoleResult(esmToolBackedResult(esmAuditFailReport))

	if ok := runESMSupervisorReview(t, "audit", app, store, sessionID, obj); !ok {
		t.Fatal("runESMAudit returned false")
	}
	got := requireESMStatus(t, store, sessionID, esm.StatusActive)
	if !strings.Contains(got.CompletionReview, "missing requirement") {
		t.Fatalf("completion review = %q, want audit fail review", got.CompletionReview)
	}
}

func TestESMSupervisorPassRequiresRequirementsChecked(t *testing.T) {
	for _, role := range []string{"critic", "audit"} {
		t.Run(role, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj := createESMCompletionCandidate(t, store, sessionID)
			result := esmToolBackedResult(`{"verdict":"pass","review":"verified","requirements_checked":[],"missing_work":[],"evidence":["go test"]}`)
			app := esmAppWithRoleResult(result)

			if ok := runESMSupervisorReview(t, role, app, store, sessionID, obj); !ok {
				t.Fatalf("run %s returned false", role)
			}
			got := requireESMStatus(t, store, sessionID, esm.StatusActive)
			if !strings.Contains(got.CompletionReview, "requirements_checked is empty") {
				t.Fatalf("completion review = %q, want requirements rejection", got.CompletionReview)
			}
		})
	}
}

func TestESMSupervisorPassRequiresEvidence(t *testing.T) {
	for _, role := range []string{"critic", "audit"} {
		t.Run(role, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj := createESMCompletionCandidate(t, store, sessionID)
			result := esmToolBackedResult(`{"verdict":"pass","review":"verified","requirements_checked":["objective -> covered"],"missing_work":[],"evidence":[]}`)
			app := esmAppWithRoleResult(result)

			if ok := runESMSupervisorReview(t, role, app, store, sessionID, obj); !ok {
				t.Fatalf("run %s returned false", role)
			}
			got := requireESMStatus(t, store, sessionID, esm.StatusActive)
			if !strings.Contains(got.CompletionReview, "evidence is empty") {
				t.Fatalf("completion review = %q, want evidence rejection", got.CompletionReview)
			}
		})
	}
}

func TestESMSupervisorPassRequiresSuccessfulToolCall(t *testing.T) {
	for _, role := range []string{"critic", "audit"} {
		t.Run(role, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj := createESMCompletionCandidate(t, store, sessionID)
			app := esmAppWithRoleResult(esmAllToolsFailedResult(esmAuditPassReport))

			if ok := runESMSupervisorReview(t, role, app, store, sessionID, obj); !ok {
				t.Fatalf("run %s returned false", role)
			}
			got := requireESMStatus(t, store, sessionID, esm.StatusActive)
			if !strings.Contains(got.CompletionReview, "all independent inspection tool calls failed") {
				t.Fatalf("completion review = %q, want failed-tool rejection", got.CompletionReview)
			}
		})
	}
}

func TestESMWorkerCompleteCandidateRequiresSuccessfulToolCall(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "ship the full objective")
	if err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	app := esmAppWithRoleResult(esmAllToolsFailedResult(esmWorkerCompleteReport))
	eventCh := make(chan internalagent.Event, 20)

	if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, "worker-run", "", "agent", obj); !ok {
		t.Fatal("runESMWorker returned false")
	}
	got := requireESMStatus(t, store, sessionID, esm.StatusActive)
	if !strings.Contains(got.CompletionReview, "all inspection or validation tool calls failed") {
		t.Fatalf("completion review = %q, want failed-tool rejection", got.CompletionReview)
	}
}

func TestESMWorkerTimeoutRunsRecoveryObserverAndKeepsObjectiveActive(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "finish recovery flow")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var roleIDs []string
	app := &App{esmRoleRunner: func(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, id, workDir, mode string, toolFilter []string, maxIterations int, task string) (esmRoleResult, error) {
		roleIDs = append(roleIDs, id)
		if strings.Contains(id, "recovery-observer") {
			return esmToolBackedResult(`{"decision":"resume","summary":"partial implementation is present","evidence":["read changed file"],"remaining_work":["run tests"],"blockers":[]}`), nil
		}
		return esmRoleResult{}, context.DeadlineExceeded
	}}
	eventCh := make(chan internalagent.Event, 20)
	if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, "timeout-run", "", "agent", obj); !ok {
		t.Fatal("runESMWorker returned false")
	}
	if got, want := roleIDs, []string{"timeout-run-worker", "timeout-run-recovery-observer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("role IDs = %#v, want %#v", got, want)
	}
	got := requireESMStatus(t, store, sessionID, esm.StatusActive)
	if got.RecoveryCount != 1 || !strings.Contains(got.RecoveryReason, "worker timed out") || len(got.RemainingWork) != 1 {
		t.Fatalf("recovery state = %#v", got)
	}
	for len(eventCh) > 0 {
		if event := <-eventCh; event.Type == internalagent.EventError {
			t.Fatalf("timeout recovery emitted terminal error: %#v", event)
		}
	}
}

func TestESMWorkerRetryableTransportFailureSkipsObserver(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "finish transport recovery")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.RecordWorkerProgress(context.Background(), sessionID, "partial work", []string{"preserve pending work"}); err != nil {
		t.Fatalf("RecordWorkerProgress: %v", err)
	}
	var calls int
	app := &App{esmRoleRunner: func(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, id, workDir, mode string, toolFilter []string, maxIterations int, task string) (esmRoleResult, error) {
		calls++
		return esmRoleResult{}, fmt.Errorf("send request: %w", &net.DNSError{Err: "connection refused", IsTemporary: true})
	}}
	if ok := app.runESMWorker(context.Background(), make(chan internalagent.Event, 10), nil, store, sessionID, "transport-run", "", "agent", obj); !ok {
		t.Fatal("runESMWorker returned false")
	}
	if calls != 1 {
		t.Fatalf("role calls = %d, want worker only", calls)
	}
	got := requireESMStatus(t, store, sessionID, esm.StatusActive)
	if got.RecoveryCount != 1 || !strings.Contains(got.RecoveryReason, "provider transport failure") || !reflect.DeepEqual(got.RemainingWork, []string{"preserve pending work"}) {
		t.Fatalf("transport recovery state = %#v", got)
	}
}

func TestESMWorkerMissingWorkAliasRejectsCompletionBeforeCritic(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "ship the full objective")
	if err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	response := `{"status":"complete_candidate","summary":"not actually done","evidence":["read files"],"missing_work":["add tests","update docs"],"blockers":["await API access"]}`
	app := esmAppWithRoleResult(esmToolBackedResult(response))
	eventCh := make(chan internalagent.Event, 20)

	if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, "worker-run", "", "agent", obj); !ok {
		t.Fatal("runESMWorker returned false")
	}
	got := requireESMStatus(t, store, sessionID, esm.StatusActive)
	if got.RejectionCount != 1 || len(got.RemainingWork) != 3 {
		t.Fatalf("worker precheck state = %#v", got)
	}
	for _, want := range []string{"remaining work (2)", "add tests", "update docs", "blockers (1)", "await API access"} {
		if !strings.Contains(got.CompletionReview, want) {
			t.Fatalf("completion review %q missing %q", got.CompletionReview, want)
		}
	}
	close(eventCh)
	var statusText string
	for event := range eventCh {
		statusText += event.StatusMessage
	}
	if !strings.Contains(statusText, "worker completion candidate rejected") || !strings.Contains(statusText, "add tests") {
		t.Fatalf("worker rejection status missing details: %q", statusText)
	}
}

func TestESMWorkerRepeatedCompletionRejectionsStayActive(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "ship the full objective")
	if err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	response := `{"status":"complete_candidate","summary":"not done","evidence":["read files"],"remaining_work":["finish implementation"],"blockers":[]}`
	app := esmAppWithRoleResult(esmToolBackedResult(response))

	for i := 1; i <= 4; i++ {
		eventCh := make(chan internalagent.Event, 20)
		if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, fmt.Sprintf("worker-run-%d", i), "", "agent", obj); !ok {
			t.Fatalf("runESMWorker %d returned false", i)
		}
		obj, err = store.Get(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("Get ESM objective %d: %v", i, err)
		}
	}
	if obj.Status != esm.StatusActive || obj.RejectionCount != 4 || !obj.CanAutoRun() {
		t.Fatalf("completion rejections should remain active = %#v", obj)
	}
}

func TestESMInvalidWorkerReportsRemainRecoverable(t *testing.T) {
	tests := map[string]string{
		"malformed":       "not json",
		"missing blocker": `{"status":"blocked_candidate","summary":"blocked","evidence":["attempted request"],"remaining_work":["retry"],"blockers":[]}`,
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj, err := store.Create(context.Background(), sessionID, "ship the full objective")
			if err != nil {
				t.Fatalf("Create ESM objective: %v", err)
			}
			app := esmAppWithRoleResult(esmToolBackedResult(response))
			for i := 1; i <= 4; i++ {
				eventCh := make(chan internalagent.Event, 20)
				if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, fmt.Sprintf("worker-run-%d", i), "", "agent", obj); !ok {
					t.Fatalf("runESMWorker %d returned false", i)
				}
				obj, err = store.Get(context.Background(), sessionID)
				if err != nil {
					t.Fatalf("Get ESM objective %d: %v", i, err)
				}
			}
			if obj.Status != esm.StatusActive || obj.RejectionCount != 4 || !obj.CanAutoRun() {
				t.Fatalf("invalid worker report must remain recoverable = %#v", obj)
			}
		})
	}
}

func TestESMSupervisorPassWithMissingWorkLogsDetails(t *testing.T) {
	for _, role := range []string{"critic", "audit"} {
		t.Run(role, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj := createESMCompletionCandidate(t, store, sessionID)
			result := esmToolBackedResult(`{"verdict":"pass","review":"claimed pass","requirements_checked":["objective -> gap"],"missing_work":["add test","finish docs"],"evidence":["read files"]}`)
			app := esmAppWithRoleResult(result)

			if ok := runESMSupervisorReview(t, role, app, store, sessionID, obj); !ok {
				t.Fatalf("run %s returned false", role)
			}
			got := requireESMStatus(t, store, sessionID, esm.StatusActive)
			for _, want := range []string{"missing_work (2)", "add test", "finish docs"} {
				if !strings.Contains(got.CompletionReview, want) {
					t.Fatalf("completion review %q missing %q", got.CompletionReview, want)
				}
			}
			if got.RejectionCount != 1 || len(got.RemainingWork) != 2 {
				t.Fatalf("supervisor rejection state = %#v", got)
			}
		})
	}
}

func TestESMMalformedSupervisorReportRejectsCandidate(t *testing.T) {
	for _, role := range []string{"critic", "audit"} {
		t.Run(role, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj := createESMCompletionCandidate(t, store, sessionID)
			app := esmAppWithRoleResult(esmToolBackedResult("not json"))

			if ok := runESMSupervisorReview(t, role, app, store, sessionID, obj); !ok {
				t.Fatalf("run %s returned false", role)
			}
			got := requireESMStatus(t, store, sessionID, esm.StatusActive)
			if !strings.Contains(got.CompletionReview, "report was not structured") {
				t.Fatalf("completion review = %q, want malformed report rejection", got.CompletionReview)
			}
		})
	}
}

func TestESMWorkerBlockedCandidatePath(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "ship the full objective")
	if err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	app := esmAppWithRoleResult(esmToolBackedResult(esmWorkerBlockedReport))

	for i, runID := range []string{"worker-run-1", "worker-run-2", "worker-run-3"} {
		eventCh := make(chan internalagent.Event, 20)
		if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, runID, "", "agent", obj); !ok {
			t.Fatalf("runESMWorker %d returned false", i+1)
		}
		want := esm.StatusActive
		if i == 2 {
			want = esm.StatusBlocked
		}
		obj = requireESMStatus(t, store, sessionID, want)
		if obj.BlockedReason == "" || !strings.Contains(obj.BlockedReason, "missing API token") {
			t.Fatalf("blocked reason = %q, want worker blocker", obj.BlockedReason)
		}
	}
}

func TestESMWorkerBlockedCandidateUsesStableBlockerReason(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	obj, err := store.Create(context.Background(), sessionID, "ship the full objective")
	if err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	reports := []string{
		`{"status":"blocked_candidate","summary":"first attempt","evidence":["curl failed"],"remaining_work":["retry"],"blockers":["missing API token"]}`,
		`{"status":"blocked_candidate","summary":"second attempt","evidence":["request returned 401"],"remaining_work":["retry"],"blockers":["missing API token"]}`,
		`{"status":"blocked_candidate","summary":"third attempt","evidence":["authentication still unavailable"],"remaining_work":["need credentials"],"blockers":["missing API token"]}`,
	}

	for i, response := range reports {
		app := esmAppWithRoleResult(esmToolBackedResult(response))
		eventCh := make(chan internalagent.Event, 20)
		if ok := app.runESMWorker(context.Background(), eventCh, nil, store, sessionID, fmt.Sprintf("worker-run-%d", i+1), "", "agent", obj); !ok {
			t.Fatalf("runESMWorker %d returned false", i+1)
		}
		want := esm.StatusActive
		if i == len(reports)-1 {
			want = esm.StatusBlocked
		}
		obj = requireESMStatus(t, store, sessionID, want)
	}
	if obj.BlockedReason != "missing API token" {
		t.Fatalf("blocked reason = %q, want stable blocker", obj.BlockedReason)
	}
}

func TestESMRoleTerminalEventsAreNotForwarded(t *testing.T) {
	for _, eventType := range []agentpkg.EventType{agentpkg.EventAgentStart, agentpkg.EventAgentEnd, agentpkg.EventDone, agentpkg.EventError, agentpkg.EventRunFinished} {
		if shouldForwardESMRoleEvent(eventType) {
			t.Fatalf("event %v should not be forwarded", eventType)
		}
	}
	if !shouldForwardESMRoleEvent(agentpkg.EventStatus) {
		t.Fatal("status event should be forwarded")
	}
}

func TestESMSupervisorRolesOnlyReceiveReadOnlyTools(t *testing.T) {
	for _, role := range []string{"critic", "audit"} {
		t.Run(role, func(t *testing.T) {
			store, sessionID := newTUITestESMStore(t)
			obj := createESMCompletionCandidate(t, store, sessionID)
			var gotTools []string
			app := &App{esmRoleRunner: func(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, id, workDir, mode string, toolFilter []string, maxIterations int, task string) (esmRoleResult, error) {
				gotTools = append([]string(nil), toolFilter...)
				return esmToolBackedResult(esmAuditFailReport), nil
			}}
			if ok := runESMSupervisorReview(t, role, app, store, sessionID, obj); !ok {
				t.Fatalf("run %s returned false", role)
			}
			want := []string{"read", "grep", "find", "ls"}
			if !reflect.DeepEqual(gotTools, want) {
				t.Fatalf("tools = %v, want %v", gotTools, want)
			}
		})
	}
}

func TestESMCompletionReviewIsIncludedInNextWorkerPrompt(t *testing.T) {
	store, sessionID := newTUITestESMStore(t)
	if _, err := store.Create(context.Background(), sessionID, "ship the full objective"); err != nil {
		t.Fatalf("Create ESM objective: %v", err)
	}
	const review = "previous audit found missing edge-case coverage"
	if _, err := store.RecordCompletionReview(context.Background(), sessionID, review); err != nil {
		t.Fatalf("RecordCompletionReview: %v", err)
	}

	var workerPrompt string
	app := &App{
		esmRoleRunner: func(ctx context.Context, eventCh chan<- internalagent.Event, manager *internalagent.AgentManager, id, workDir, mode string, toolFilter []string, maxIterations int, task string) (esmRoleResult, error) {
			workerPrompt = task
			return esmToolBackedResult(`{"status":"continue","summary":"continuing","evidence":["read review"],"remaining_work":["finish work"],"blockers":[]}`), nil
		},
	}
	eventCh := make(chan internalagent.Event, 20)
	manager := internalagent.NewAgentManager(&internalagent.AgentFactory{})

	app.runESMSubAgentSupervisor(context.Background(), eventCh, manager, store, sessionID, "worker-run", "", "agent")
	for range eventCh {
	}

	if !strings.Contains(workerPrompt, "Previous failed completion audit") {
		t.Fatalf("worker prompt missing completion audit section:\n%s", workerPrompt)
	}
	if !strings.Contains(workerPrompt, review) {
		t.Fatalf("worker prompt missing previous review %q:\n%s", review, workerPrompt)
	}
}

func TestAppSyncAgentManagerRuntimeUpdatesFutureESMAgentProviderAndModel(t *testing.T) {
	tmp := t.TempDir()
	oldModel := &provider.Model{ID: "old-model", Name: "Old", Provider: "old-provider"}
	newModel := &provider.Model{ID: "new-model", Name: "New", Provider: "new-provider"}
	oldProvider := &recordingRuntimeProvider{name: "old-provider", models: []*provider.Model{oldModel}}
	newProvider := &recordingRuntimeProvider{name: "new-provider", models: []*provider.Model{newModel}}
	settings := config.DefaultSettings()
	settings.SessionDir = filepath.Join(tmp, "sessions")
	settings.DefaultProvider = "new-provider"
	settings.DefaultModel = "new-model"

	factory := internalagent.NewAgentFactoryWithOptions(
		oldProvider,
		oldModel,
		settings,
		nil,
		"",
		"",
		nil,
		ctxpkg.CompactionSettings{ReserveTokens: 1024, KeepRecentTokens: 1024},
		nil,
		internalagent.AgentFactoryOptions{MultiAgentEnabled: true, ProviderName: "old-provider"},
	)
	manager := internalagent.NewAgentManager(factory)
	app := &App{
		provider:     newProvider,
		providerName: "new-provider",
		model:        newModel,
		settings:     settings,
		agentMgr:     manager,
	}

	app.syncAgentManagerRuntime()

	child, err := manager.Create(internalagent.AgentOptions{ID: "esm-future-worker", WorkDir: tmp, Mode: "agent"})
	if err != nil {
		t.Fatalf("Create future agent: %v", err)
	}
	for range child.Run(context.Background(), "use configured runtime") {
	}

	if got := oldProvider.callCount(); got != 0 {
		t.Fatalf("old provider calls = %d, want 0", got)
	}
	calls := newProvider.callsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("new provider calls = %d, want 1", len(calls))
	}
	if calls[0].ModelID != "new-model" {
		t.Fatalf("ModelID = %q, want new-model", calls[0].ModelID)
	}
}

type recordingRuntimeProvider struct {
	mu     sync.Mutex
	name   string
	models []*provider.Model
	calls  []provider.ChatParams
}

func (p *recordingRuntimeProvider) Chat(ctx context.Context, params provider.ChatParams) <-chan provider.StreamEvent {
	p.mu.Lock()
	p.calls = append(p.calls, params)
	p.mu.Unlock()
	ch := make(chan provider.StreamEvent, 2)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Error: ctx.Err()}
		default:
			ch <- provider.StreamEvent{Type: provider.StreamTextDelta, TextDelta: "ok"}
			ch <- provider.StreamEvent{Type: provider.StreamDone, StopReason: "end_turn"}
		}
	}()
	return ch
}

func (p *recordingRuntimeProvider) Name() string { return p.name }

func (p *recordingRuntimeProvider) API() string { return "openai-chat" }

func (p *recordingRuntimeProvider) Models() []*provider.Model { return p.models }

func (p *recordingRuntimeProvider) GetModel(id string) *provider.Model {
	for _, m := range p.models {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (p *recordingRuntimeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func (p *recordingRuntimeProvider) callsSnapshot() []provider.ChatParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.ChatParams(nil), p.calls...)
}
