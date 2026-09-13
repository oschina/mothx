package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Runner evaluates JavaScript workflow DSL and delegates agent tasks to a Host.
type Runner struct {
	Host        Host
	Store       Store
	Active      *ActiveRegistry
	Concurrency int
	Now         func() time.Time
	Progress    func(ProgressEvent)
	// EvalTimeout caps source evaluation inside the JavaScript VM. Zero uses
	// jsEvalTimeout; the lint tool sets the tighter lintEvalTimeout.
	EvalTimeout time.Duration
}

// Run evaluates a workflow source string.
func (r *Runner) Run(ctx context.Context, source string) (*RunState, error) {
	if r.Host == nil {
		return nil, fmt.Errorf("workflow host is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	now := r.now()
	rt := &runtime{
		runner:      r,
		state:       &RunState{Status: StatusRunning, StartedAt: now, UpdatedAt: now, Results: make(map[string]AgentResult)},
		phaseIndex:  -1,
		concurrency: r.Concurrency,
		cancel:      cancel,
	}
	defer rt.unregisterActive()
	if rt.concurrency <= 0 {
		rt.concurrency = 5
	}
	wf, err := evalJSWorkflowWithin(runCtx, source, r.evalTimeout())
	if err == nil {
		rt.mu.Lock()
		rt.state.Name = wf.name
		rt.state.ID = makeRunID(wf.name, now)
		if wf.concurrency > 0 {
			rt.concurrency = wf.concurrency
		}
		rt.mu.Unlock()
		err = rt.save(runCtx)
	}
	if err == nil {
		err = rt.registerActive()
	}
	if err == nil {
		err = rt.executeJSNodes(runCtx, wf.children, wf, "", -1)
	}
	if err != nil {
		rt.markError(err)
		_ = rt.save(context.WithoutCancel(ctx))
		return rt.snapshot(), err
	}
	rt.finish(StatusDone, "")
	_ = rt.save(context.WithoutCancel(ctx))
	return rt.snapshot(), nil
}

func (r *Runner) evalTimeout() time.Duration {
	if r != nil && r.EvalTimeout > 0 {
		return r.EvalTimeout
	}
	return jsEvalTimeout
}

func (r *Runner) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

type runtime struct {
	mu          sync.Mutex
	runner      *Runner
	state       *RunState
	activeID    string
	cancel      context.CancelFunc
	phase       string
	phaseIndex  int
	concurrency int
	sem         chan struct{}
}

func (rt *runtime) runAgent(ctx context.Context, task AgentTask, phaseIndex int) (AgentResult, error) {
	if err := validateInstanceKey(task.InstanceKey); err != nil {
		return AgentResult{}, fmt.Errorf("agent %q :key: %w", task.Name, err)
	}
	sem := rt.semaphore()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return AgentResult{}, ctx.Err()
	}
	defer func() { <-sem }()

	key := taskStorageKey(task.Phase, task.Name, task.InstanceKey)
	started := rt.runner.now()
	rt.recordTaskStart(key, phaseIndex)
	rt.emitProgress(ProgressEvent{
		Phase:   task.Phase,
		Task:    task.Name,
		Status:  StatusRunning,
		Message: fmt.Sprintf("task %s started", key),
	})
	result, err := rt.runner.Host.RunAgent(ctx, task)
	finished := rt.runner.now()
	if result.Key == "" {
		result.Key = key
	}
	result.Name = task.Name
	result.Phase = task.Phase
	result.InstanceKey = task.InstanceKey
	if result.StartedAt.IsZero() {
		result.StartedAt = started
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = finished
	}
	result.Duration = result.FinishedAt.Sub(result.StartedAt).Round(time.Millisecond).String()
	if err != nil {
		result.Status = statusForError(err)
		result.Error = err.Error()
	} else if result.Status == "" {
		result.Status = StatusDone
	}
	rt.recordResult(result)
	rt.emitProgress(ProgressEvent{
		Phase:   task.Phase,
		Task:    task.Name,
		Status:  result.Status,
		Message: fmt.Sprintf("task %s %s", key, result.Status),
	})
	if err := rt.save(ctx); err != nil {
		return result, err
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (rt *runtime) semaphore() chan struct{} {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.sem == nil {
		rt.sem = make(chan struct{}, rt.concurrency)
	}
	return rt.sem
}

func (rt *runtime) startPhase(name string) int {
	now := rt.runner.now()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.Phases = append(rt.state.Phases, PhaseState{Name: name, Status: StatusRunning, StartedAt: now})
	rt.state.UpdatedAt = now
	idx := len(rt.state.Phases) - 1
	rt.emitProgressLocked(ProgressEvent{
		Phase:   name,
		Status:  StatusRunning,
		Message: fmt.Sprintf("phase %q started", name),
	})
	return idx
}

func (rt *runtime) finishPhase(idx int, status string, msg string) {
	now := rt.runner.now()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if idx >= 0 && idx < len(rt.state.Phases) {
		rt.state.Phases[idx].Status = status
		rt.state.Phases[idx].FinishedAt = now
		rt.state.Phases[idx].Error = msg
		rt.emitProgressLocked(ProgressEvent{
			Phase:   rt.state.Phases[idx].Name,
			Status:  status,
			Message: fmt.Sprintf("phase %q %s", rt.state.Phases[idx].Name, status),
		})
	}
	rt.state.UpdatedAt = now
}

func (rt *runtime) recordTaskStart(key string, phaseIndex int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if phaseIndex >= 0 && phaseIndex < len(rt.state.Phases) {
		rt.state.Phases[phaseIndex].Tasks = append(rt.state.Phases[phaseIndex].Tasks, key)
	}
	rt.state.UpdatedAt = rt.runner.now()
}

func (rt *runtime) recordResult(result AgentResult) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.Results[result.Key] = result
	rt.state.UpdatedAt = rt.runner.now()
}

func (rt *runtime) markError(err error) {
	rt.finish(statusForError(err), err.Error())
}

func (rt *runtime) finish(status string, msg string) {
	now := rt.runner.now()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.Status = status
	rt.state.Error = msg
	rt.state.UpdatedAt = now
	rt.state.FinishedAt = now
	message := fmt.Sprintf("workflow %s", status)
	if msg != "" {
		message += ": " + msg
	}
	rt.emitProgressLocked(ProgressEvent{Status: status, Message: message})
}

func (rt *runtime) emitProgress(ev ProgressEvent) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.emitProgressLocked(ev)
}

func (rt *runtime) emitProgressLocked(ev ProgressEvent) {
	if rt.runner == nil || rt.runner.Progress == nil {
		return
	}
	if ev.RunID == "" {
		ev.RunID = rt.state.ID
	}
	if ev.Name == "" {
		ev.Name = rt.state.Name
	}
	if ev.Phase == "" {
		ev.Phase = rt.phase
	}
	if ev.Time.IsZero() {
		ev.Time = rt.runner.now()
	}
	rt.runner.Progress(ev)
}

func (rt *runtime) snapshot() *RunState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	cp := *rt.state
	cp.Phases = append([]PhaseState(nil), rt.state.Phases...)
	cp.Logs = append([]WorkflowLog(nil), rt.state.Logs...)
	cp.Results = make(map[string]AgentResult, len(rt.state.Results))
	for k, v := range rt.state.Results {
		cp.Results[k] = v
	}
	return &cp
}

func (rt *runtime) save(ctx context.Context) error {
	if rt.runner.Store == nil {
		return nil
	}
	return rt.runner.Store.Save(ctx, rt.snapshot())
}

func (rt *runtime) activeRegistry() *ActiveRegistry {
	if rt.runner.Active != nil {
		return rt.runner.Active
	}
	return DefaultActiveRegistry()
}

func (rt *runtime) registerActive() error {
	rt.mu.Lock()
	id := rt.state.ID
	if rt.activeID == id {
		rt.mu.Unlock()
		return nil
	}
	rt.activeID = id
	rt.mu.Unlock()
	return rt.activeRegistry().Register(id, rt.cancel)
}

func (rt *runtime) unregisterActive() {
	rt.mu.Lock()
	id := rt.activeID
	rt.activeID = ""
	rt.mu.Unlock()
	rt.activeRegistry().Unregister(id)
}

func taskStorageKey(phase string, name string, instanceKey string) string {
	base := name
	if phase != "" {
		base = phase + "." + name
	}
	return resultStorageKey(base, instanceKey)
}

func resultStorageKey(baseKey string, instanceKey string) string {
	if instanceKey == "" {
		return baseKey
	}
	return baseKey + "[" + instanceKey + "]"
}

func resultBaseKey(result AgentResult) string {
	if result.Phase == "" {
		return result.Name
	}
	return result.Phase + "." + result.Name
}

func resultMatchesBase(result AgentResult, baseKey string) bool {
	return resultBaseKey(result) == baseKey || result.Key == baseKey
}

func validateInstanceKey(key string) error {
	if key == "" {
		return nil
	}
	if strings.TrimSpace(key) != key {
		return fmt.Errorf("must not have leading or trailing whitespace")
	}
	if strings.ContainsAny(key, "[]\n\r\t") {
		return fmt.Errorf("must not contain brackets or control whitespace")
	}
	return nil
}

func statusForError(err error) string {
	if errors.Is(err, context.Canceled) {
		return StatusCanceled
	}
	return StatusError
}

func makeRunID(name string, t time.Time) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case b.Len() > 0 && b.String()[b.Len()-1] != '-':
			b.WriteByte('-')
		}
	}
	slug = strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "workflow"
	}
	return fmt.Sprintf("%s-%s", slug, t.UTC().Format("20060102T150405.000000000"))
}
