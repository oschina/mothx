package openaiapi

// WebUI ESM execution is a host adapter around the single ESM supervisor.
// Durable state transitions and recovery policy live in internal/esm.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/esm"
	"github.com/startvibecoding/mothx/internal/provider"
	"github.com/startvibecoding/mothx/internal/session"
)

type esmCoordinator struct {
	mu      sync.Mutex
	running map[string]context.CancelFunc
	done    map[string]chan struct{}
	closed  bool
}

func newESMCoordinator() *esmCoordinator {
	return &esmCoordinator{running: make(map[string]context.CancelFunc), done: make(map[string]chan struct{})}
}

func (c *esmCoordinator) start(s *Server, sessionID string) {
	if c == nil || s == nil || sessionID == "" {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if c.running == nil {
		c.running = make(map[string]context.CancelFunc)
	}
	if c.done == nil {
		c.done = make(map[string]chan struct{})
	}
	if _, ok := c.running[sessionID]; ok {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.running[sessionID] = cancel
	c.done[sessionID] = done
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.running, sessionID)
			delete(c.done, sessionID)
			close(done)
			c.mu.Unlock()
		}()
		s.runESMCoordinator(ctx, sessionID)
	}()
}

func (s *Server) ensureESMCoordinator() *esmCoordinator {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.esmCoordinator == nil {
		s.esmCoordinator = newESMCoordinator()
	}
	return s.esmCoordinator
}

func (s *Server) startESM(sessionID string) { s.ensureESMCoordinator().start(s, sessionID) }

// esmSteeringMessages exposes the core's version-aware ESM steering source to
// one normal WebUI run. The source only injects persisted objective updates at
// Agent loop boundaries; it never creates a competing execution.
func (s *Server) esmSteeringMessages(sessionID string) func() []provider.Message {
	return esm.NewSteeringSource(s.esmStore(), sessionID).Next
}

func (s *Server) stopESM(sessionID string) {
	if err := s.stopESMForControl(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: ESM coordinator for session %s did not stop cleanly: %v\n", sessionID, err)
	}
}

// stopESMForControl is the explicit cancellation boundary used by pause and
// clear. It waits for the locally-owned coordinator to release its execution
// lease, so callers can re-check whether another foreground or remote run is
// still active before mutating the persisted objective.
func (s *Server) stopESMForControl(sessionID string) error {
	s.mu.RLock()
	c := s.esmCoordinator
	s.mu.RUnlock()
	if c == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.stop(ctx, sessionID)
}

func (s *Server) esmCoordinatorRunning(sessionID string) bool {
	if s == nil || sessionID == "" {
		return false
	}
	s.mu.RLock()
	c := s.esmCoordinator
	s.mu.RUnlock()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, running := c.running[sessionID]
	return running
}

func (c *esmCoordinator) stop(ctx context.Context, sessionID string) error {
	if c == nil || sessionID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	cancel := c.running[sessionID]
	done := c.done[sessionID]
	c.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopAll cancels every ESM coordinator owned by this Serve process and waits
// for each goroutine to release its session/runtime references. The bounded
// context keeps shutdown responsive if an adapter is already stuck in a
// provider call; SessionRuntime.Shutdown remains the final resource boundary.
func (c *esmCoordinator) stopAll(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	type activeCoordinator struct {
		cancel context.CancelFunc
		done   <-chan struct{}
	}
	c.mu.Lock()
	// Mark the coordinator closed before taking the snapshot so a concurrent
	// Create/Edit/Resume request cannot start a new worker while shutdown waits.
	c.closed = true
	active := make([]activeCoordinator, 0, len(c.running))
	for sessionID, cancel := range c.running {
		active = append(active, activeCoordinator{cancel: cancel, done: c.done[sessionID]})
	}
	c.mu.Unlock()
	for _, coordinator := range active {
		if coordinator.cancel != nil {
			coordinator.cancel()
		}
	}
	for _, coordinator := range active {
		if coordinator.done == nil {
			continue
		}
		select {
		case <-coordinator.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *Server) stopAllESM(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	c := s.esmCoordinator
	s.mu.RUnlock()
	if c == nil {
		return nil
	}
	return c.stopAll(ctx)
}

func (s *Server) shutdownESM() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.stopAllESM(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: ESM coordinators did not stop cleanly: %v\n", err)
	}
}

func (s *Server) runESMCoordinator(ctx context.Context, sessionID string) {
	store := s.esmStore()
	if store == nil {
		return
	}
	workDir, found, err := s.findSessionWorkDir(sessionID)
	if err != nil {
		return
	}
	if !found {
		workDir = s.cfg.GetWorkDir()
	}
	sess, err := s.getOrCreateSession(sessionID, workDir)
	if err != nil || sess == nil || !s.pool.Pin(sess) {
		return
	}
	defer s.pool.Unpin(sess)
	// A user can create or edit an objective while a foreground run owns this
	// session. Wait for that canonical run to finish instead of treating the
	// temporary lease conflict as a reason to drop the requested continuation.
	// Its run-scoped SteeringSource still receives the updated objective at its
	// next loop boundary, so waiting here never creates a concurrent run.
	runtimeGuard, err := agentruntime.AcquireExecutionAdmission(ctx, s.settings.GetSessionDir(), sessionID, agentruntime.ExecutionAdmissionOptions{Wait: true})
	if err != nil {
		return
	}
	release := runtimeGuard.Release
	sess.Lock()
	defer release()
	defer sess.Unlock()
	if err := sess.Manager.Reload(); err != nil {
		return
	}
	effectiveSource, effectiveMode, err := s.resolveESMRuntimePolicy(sess)
	if err != nil {
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		obj, err := store.Get(ctx, sessionID)
		if err != nil || obj == nil || !obj.CanAutoRun() {
			return
		}
		runID := newRunID()
		adapter := &webESMRuntimeAdapter{
			server: s, sess: sess, workDir: workDir, source: effectiveSource, mode: effectiveMode,
		}
		runtime := &esm.Supervisor{Store: store, Adapter: adapter, Events: adapter}
		if _, err := runtime.Run(ctx, sessionID, runID, workDir, effectiveMode); err != nil {
			return
		}
		obj, err = store.Get(ctx, sessionID)
		if err != nil || obj == nil || obj.Status != esm.StatusActive && obj.Status != esm.StatusCompleteCandidate {
			return
		}
	}
}

func (s *Server) resolveESMRuntimePolicy(sess *APISession) (string, string, error) {
	if sess == nil || sess.Runtime == nil {
		return "", "", fmt.Errorf("webui ESM session runtime is unavailable")
	}
	defaultMode := agentruntime.ModeYolo
	if s != nil && s.cfg != nil && s.cfg.DefaultMode != "" {
		defaultMode = s.cfg.DefaultMode
	}
	resolution, mode, err := sess.Runtime.ResolvePolicy(sess.Mode, "", defaultMode)
	if err != nil {
		return "", "", err
	}
	source := string(resolution.Source)
	if source == "" {
		source = string(agentruntime.SourceWebUI)
	}
	// ESM role runs are unattended: never gate them on interactive approval.
	// os is inherited; plan/agent fall back to yolo.
	return source, agentruntime.ResolveUnattendedMode(mode), nil
}

// webESMRuntimeAdapter owns WebUI-specific execution and presentation only.
type webESMRuntimeAdapter struct {
	server  *Server
	sess    *APISession
	workDir string
	source  string
	mode    string
}

func (a *webESMRuntimeAdapter) RunRole(parent context.Context, req esm.RoleRequest) (esm.RoleResult, error) {
	timeout := esm.RoleTimeout
	if req.Role == esm.RoleRecovery {
		timeout = esm.RecoveryObserverTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if a == nil || a.server == nil || a.sess == nil {
		return esm.RoleResult{}, fmt.Errorf("webui ESM adapter is unavailable")
	}
	model := a.currentModel()
	if model == nil {
		return esm.RoleResult{}, fmt.Errorf("webui ESM model is unavailable")
	}

	runID := req.RunID
	effectiveMode := a.mode
	if effectiveMode == "" {
		effectiveMode = req.Mode
	}
	effectiveSource := a.source
	if effectiveSource == "" {
		effectiveSource = string(agentruntime.SourceWebUI)
	}
	started := time.Now()
	finalStatus := "failed"
	finalError := ""
	execution := a.sess.ensureExecution()
	execution.SetRunStore(agentruntime.RunStore{SessionDir: a.server.settings.GetSessionDir()})
	execution.SetEventSink(a.server.runtimeRunEventSink(a.sess))
	if a.sess.Runtime != nil {
		a.sess.Runtime.SetExecution(execution)
	}
	requestSnapshot, snapshotErr := json.Marshal(req)
	if snapshotErr != nil {
		return esm.RoleResult{}, snapshotErr
	}
	policySnapshot, snapshotErr := marshalRunPolicySnapshot(a.server, a.sess, submitRunRequest{Message: req.Prompt, Model: model.ID, Mode: effectiveMode, Tools: req.Tools, WorkDir: a.workDir}, effectiveSource, effectiveMode)
	if snapshotErr != nil {
		return esm.RoleResult{}, snapshotErr
	}
	intent := agentruntime.ExecutionIntent{ID: newExecutionIntentID(), SessionID: req.SessionID, Source: effectiveSource, Model: model.ID, Mode: effectiveMode, WorkDir: a.workDir, RequestFingerprint: requestFingerprint(req), Request: requestSnapshot, Policy: policySnapshot, CreatedAt: started}
	runCtx, err := execution.BeginIntentDurable(ctx, intent, agentruntime.DurableRun{
		ID: runID, SessionID: req.SessionID, IntentID: intent.ID, Attempt: 1, WorkDir: a.workDir,
		Source: effectiveSource, Model: model.ID, Mode: effectiveMode,
		Status: "running", StartedAt: started,
	}, agentruntime.RunEvent{SessionID: req.SessionID, RunID: runID, EventType: "esm.role_started", Source: effectiveSource, Status: "running", Model: model.ID, Mode: effectiveMode, Timestamp: started, Data: rawEventData(map[string]any{"role": req.Role, "intentId": intent.ID, "attempt": 1})})
	if err != nil {
		return esm.RoleResult{}, err
	}
	a.sess.markDurableRun(runID)
	if a.server.runManager != nil {
		_ = a.server.runManager.Register(session.SessionRun{ID: runID, SessionID: req.SessionID, IntentID: intent.ID, Attempt: 1})
	}
	defer func() {
		_ = execution.FinishDurableWithRetry(context.Background(), runID, webUIRunState(finalStatus, finalError), finalError, agentruntime.RunEvent{
			SessionID: req.SessionID, RunID: runID, EventType: "esm.role_finished", Source: effectiveSource, Status: finalStatus, Model: model.ID, Mode: effectiveMode, Timestamp: time.Now(), Data: rawEventData(map[string]any{"role": req.Role, "error": finalError}),
		})
		a.sess.clearDurableRun(runID)
	}()

	mgr := a.server.newAgentManagerForSession(a.sess)
	if mgr == nil {
		// Session shutdown can race with an already-started ESM coordinator.
		// Treat an unavailable runtime as a failed role so the coordinator can
		// terminalize its durable run instead of dereferencing a nil manager.
		return esm.RoleResult{}, errors.New("webui ESM agent manager is unavailable")
	}
	teamWorker := req.Role == esm.RoleWorker && a.sess.Runtime != nil && a.sess.Runtime.TeamExpertActive()
	no := false
	child, err := mgr.Create(agent.AgentOptions{ID: agentpkg.AgentID(runID), IsSubAgent: true, Mode: effectiveMode, WorkDir: a.workDir, Tools: req.Tools, MaxIterations: req.MaxIterations, MultiAgent: &teamWorker, DelegateMode: &no, Workflows: &no, OwnsSessionMailbox: teamWorker})
	if err != nil {
		return esm.RoleResult{}, err
	}
	defer mgr.Destroy(child.ID())
	defer func() {
		if latest, err := a.server.esmStore().Get(context.Background(), req.SessionID); err == nil {
			a.server.publishESM(req.SessionID, esmSnapshot(latest))
		}
	}()
	mgr.MarkRunning(child.ID())
	a.server.PublishExternalSubAgentEvent(req.SessionID, agent.Event{AgentID: child.ID(), Type: agent.EventAgentStart})

	result := esm.RoleResult{ToolError: make(map[string]bool), ToolNames: make(map[string]int)}
	tracker := esm.NewEvidenceTracker()
	completed := false
	var runErr error
	for ev := range child.Run(runCtx, req.Prompt) {
		a.publishRoleEvent(req.SessionID, child.ID(), ev)
		if ev.Usage != nil {
			n := int64(ev.Usage.TotalTokens)
			if n <= 0 {
				n = int64(ev.Usage.InputTokens + ev.Usage.OutputTokens)
			}
			result.Tokens += n
		}
		tracker.Observe(ev)
		switch ev.Type {
		case agentpkg.EventRunFinished:
			completed = true
			if ev.Status == agentpkg.TaskFailed || ev.Status == agentpkg.TaskCanceled {
				runErr = ev.Error
				mgr.MarkError(child.ID(), ev.Error)
			} else {
				mgr.MarkDone(child.ID(), esm.FinalAssistantResponse(child.GetMessages()))
			}
		case agentpkg.EventDone:
			if !completed {
				completed = true
				mgr.MarkDone(child.ID(), esm.FinalAssistantResponse(child.GetMessages()))
			}
		case agentpkg.EventError:
			if !completed {
				completed = true
				runErr = ev.Error
				mgr.MarkError(child.ID(), ev.Error)
			}
		}
	}
	if !completed {
		runErr = ctx.Err()
		mgr.MarkError(child.ID(), runErr)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.Response = esm.FinalAssistantResponse(child.GetMessages())
	result.ToolCalls, result.ToolNames, result.ToolError = tracker.Summary()
	if runErr != nil {
		finalError = runErr.Error()
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			finalStatus = "canceled"
		}
		return result, runErr
	}
	finalStatus = "completed"
	a.server.PublishExternalSubAgentEvent(req.SessionID, agent.Event{AgentID: child.ID(), Type: agent.EventRunFinished, Status: agent.TaskSuccess})
	return result, nil
}

func (a *webESMRuntimeAdapter) PublishESMEvent(ctx context.Context, event esm.RuntimeEvent) error {
	if a != nil && a.server != nil && event.SessionID != "" {
		if obj, err := a.server.esmStore().Get(ctx, event.SessionID); err == nil {
			a.server.publishESM(event.SessionID, esmSnapshot(obj))
		}
	}
	return nil
}

func (a *webESMRuntimeAdapter) RunRecoveryObserver(ctx context.Context, req esm.RoleRequest, interruption error) (esm.RoleResult, error) {
	return a.RunRole(ctx, req)
}

func (a *webESMRuntimeAdapter) currentModel() *provider.Model {
	a.server.mu.RLock()
	defer a.server.mu.RUnlock()
	return cloneModel(a.server.model)
}

func (a *webESMRuntimeAdapter) publishRoleEvent(sessionID string, childID agentpkg.AgentID, ev agentpkg.Event) {
	out := agent.Event{AgentID: childID}
	switch ev.Type {
	case agentpkg.EventTextDelta:
		out.Type, out.TextDelta = agent.EventTextDelta, ev.TextDelta
	case agentpkg.EventToolCall:
		out.Type, out.ToolName, out.ToolCallID, out.ToolArgs = agent.EventToolCall, ev.ToolName, ev.ToolCallID, ev.ToolArgs
	case agentpkg.EventToolExecutionEnd:
		out.Type, out.ToolName, out.ToolCallID, out.ToolArgs, out.ToolResult, out.ToolError = agent.EventToolExecutionEnd, ev.ToolName, ev.ToolCallID, ev.ToolArgs, ev.ToolResult, ev.ToolError
	case agentpkg.EventRunFinished:
		out.Type, out.Status, out.Error = agent.EventRunFinished, agent.TaskStatus(ev.Status), ev.Error
	default:
		return
	}
	a.server.PublishExternalSubAgentEvent(sessionID, out)
}

func (s *Server) applyESMWorker(ctx context.Context, store *esm.Store, obj *esm.Objective, runID string, result esm.RoleResult) bool {
	if obj == nil {
		return false
	}
	_, ok, err := esm.ApplyWorkerResult(ctx, store, obj.SessionID, runID, result)
	return ok && err == nil
}

func (s *Server) applyESMReview(ctx context.Context, store *esm.Store, obj *esm.Objective, role, runID string, result esm.RoleResult) bool {
	if obj == nil {
		return false
	}
	_, ok, err := esm.ApplyReviewResult(ctx, store, obj.SessionID, runID, role, result)
	return ok && err == nil
}
