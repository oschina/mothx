package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/startvibecoding/mothx/agent"
	"github.com/startvibecoding/mothx/internal/agent"
	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/session"
)

// Scheduler checks for due cron jobs and executes them via sub-agents.
type Scheduler struct {
	store              CronStore
	manager            *agent.AgentManager
	jobHandler         JobHandler
	interval           time.Duration
	sessionDir         string
	quit               chan struct{}
	running            bool
	claims             map[string]struct{}
	completionObserver func(sessionID, response string, runErr error)
	jobObserver        JobCompletionObserver
	lifecycleMu        sync.Mutex
	mu                 sync.Mutex
	loopWG             sync.WaitGroup
	jobWG              sync.WaitGroup
	stopCtx            context.Context
	stopCancel         context.CancelFunc
}

// JobHandler may claim execution for a persisted job before the Scheduler
// falls back to its ordinary local-agent/A2A behavior. It lets Runtime-owned
// maintenance work reuse cron's claim, recovery, status, and completion
// lifecycle without creating an adapter-specific timer or scheduler.
//
// Returning handled=false delegates to the built-in Cron Agent execution.
// A handled job receives the same final store update and observers as any
// normal cron job.
type JobHandler func(context.Context, CronJob) (handled bool, response string, runErr error)

// A persisted running claim may outlive the process that created it. Keep the
// lease deliberately long so a slow legitimate run is not reclaimed during
// normal operation, while still allowing recovery after a dead process.
const runningLeaseTimeout = 24 * time.Hour

// SetCompletionObserver installs a callback for completed local cron runs.
// The callback is invoked after the job has produced its final response.
func (s *Scheduler) SetCompletionObserver(observer func(sessionID, response string, runErr error)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.completionObserver = observer
	s.mu.Unlock()
}

// JobCompletionObserver receives the completed job itself so management
// surfaces can project job identity (ID/name) alongside the run outcome.
// It complements the session-scoped completion observer and fires for every
// local job run, including jobs without a bound session.
type JobCompletionObserver func(job CronJob, response string, runErr error)

// SetJobCompletionObserver installs a job-scoped completion callback.
func (s *Scheduler) SetJobCompletionObserver(observer JobCompletionObserver) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.jobObserver = observer
	s.mu.Unlock()
}

func (s *Scheduler) notifyCompletion(sessionID, response string, runErr error) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	observer := s.completionObserver
	s.mu.Unlock()
	if observer != nil {
		observer(sessionID, response, runErr)
	}
}

func (s *Scheduler) notifyJobCompletion(job CronJob, response string, runErr error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	observer := s.jobObserver
	s.mu.Unlock()
	if observer != nil {
		observer(job, response, runErr)
	}
}

var a2aHTTPClient = &http.Client{Timeout: 30 * time.Second}

const maxA2AResponseBytes = 1 << 20

type dueJobClaimer interface {
	ClaimDue(id string, now time.Time) (bool, error)
}

// NewScheduler creates a new cron scheduler.
func NewScheduler(store CronStore, manager *agent.AgentManager, interval time.Duration) *Scheduler {
	return NewSchedulerWithSessionDir(store, manager, interval, "")
}

// NewSchedulerWithSessionDir creates a scheduler that can attach scheduled
// local runs to existing sessions by session ID.
func NewSchedulerWithSessionDir(store CronStore, manager *agent.AgentManager, interval time.Duration, sessionDir string) *Scheduler {
	return NewSchedulerWithSessionDirAndHandler(store, manager, interval, sessionDir, nil)
}

// NewSchedulerWithSessionDirAndHandler creates a scheduler with an optional
// Runtime-owned job handler. Existing callers should use
// NewSchedulerWithSessionDir unless they have a durable non-Agent job that
// must share the canonical Cron lifecycle.
func NewSchedulerWithSessionDirAndHandler(store CronStore, manager *agent.AgentManager, interval time.Duration, sessionDir string, handler JobHandler) *Scheduler {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Scheduler{
		store:      store,
		manager:    manager,
		jobHandler: handler,
		interval:   interval,
		sessionDir: sessionDir,
		quit:       make(chan struct{}),
		claims:     make(map[string]struct{}),
	}
}

// Start begins the scheduler loop.
func (s *Scheduler) Start() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	quit := make(chan struct{})
	s.quit = quit
	jobCtx, cancel := context.WithCancel(context.Background())
	s.stopCtx = jobCtx
	s.stopCancel = cancel
	s.loopWG.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.loopWG.Done()
		s.loop(quit)
	}()
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	quit := s.quit
	cancel := s.stopCancel
	s.stopCtx = nil
	s.stopCancel = nil
	close(quit)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.loopWG.Wait()
	s.jobWG.Wait()
}

// IsRunning returns whether the scheduler is running.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Scheduler) loop(quit <-chan struct{}) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Check immediately on start
	s.checkAndRun()

	for {
		select {
		case <-quit:
			return
		case <-ticker.C:
			s.checkAndRun()
		}
	}
}

// checkAndRun checks all enabled jobs and runs any that are due.
func (s *Scheduler) checkAndRun() {
	s.mu.Lock()
	jobCtx := context.Background()
	if s.stopCtx != nil {
		jobCtx = s.stopCtx
	}
	s.mu.Unlock()
	jobs, err := s.store.List()
	if err != nil {
		log.Printf("[cron] failed to list jobs: %v", err)
		return
	}

	now := time.Now()
	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		if job.LastStatus == "running" && !s.isStaleRunning(job, now) {
			continue // Don't start a job that's already running
		}
		if s.isStaleRunning(job, now) || s.isDue(job, now) {
			claimed, release, err := s.claimJob(job.ID, now)
			if err != nil {
				log.Printf("[cron] claim job %s: %v", job.ID, err)
				continue
			}
			if claimed {
				s.jobWG.Add(1)
				go func() {
					defer s.jobWG.Done()
					defer release()
					s.executeJobContext(jobCtx, job)
				}()
			}
		}
	}
}

func (s *Scheduler) claimJob(id string, now time.Time) (bool, func(), error) {
	if claimer, ok := s.store.(dueJobClaimer); ok {
		claimed, err := claimer.ClaimDue(id, now)
		return claimed, func() {}, err
	}

	// In-memory stores cannot coordinate across processes, but retain the
	// previous single-scheduler behavior without allowing overlapping ticks.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, claimed := s.claims[id]; claimed {
		return false, func() {}, nil
	}
	s.claims[id] = struct{}{}
	return true, func() {
		s.mu.Lock()
		delete(s.claims, id)
		s.mu.Unlock()
	}, nil
}

// isDue checks if a job should run now.
func (s *Scheduler) isDue(job CronJob, now time.Time) bool {
	// A computed NextRun is authoritative for periodic jobs, including their
	// first run. This prevents a newly-created @daily job from running now.
	if !job.NextRun.IsZero() {
		return !now.Before(job.NextRun)
	}
	// Legacy one-shot jobs have no NextRun and are due until their first claim.
	return job.LastRun.IsZero()
}

func (s *Scheduler) isStaleRunning(job CronJob, now time.Time) bool {
	return job.LastStatus == "running" && !job.LastRun.IsZero() &&
		now.Sub(job.LastRun) >= runningLeaseTimeout
}

// executeJob runs a cron job by spawning a sub-agent or sending to A2A server.
func (s *Scheduler) executeJob(job CronJob) {
	s.executeJobContext(context.Background(), job)
}

func (s *Scheduler) executeJobContext(ctx context.Context, job CronJob) {
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	var response strings.Builder
	defer func() {
		s.notifyCompletion(job.SessionID, response.String(), lastErr)
		s.notifyJobCompletion(job, response.String(), lastErr)
	}()

	if s.jobHandler != nil {
		if handled, handlerResponse, handlerErr := s.jobHandler(ctx, job); handled {
			response.WriteString(handlerResponse)
			lastErr = handlerErr
			s.updateJob(job.ID, func(current *CronJob) {
				s.completeJob(current, lastErr)
			})
			return
		}
	}

	// A2A target mode: send task to remote A2A server
	if job.A2ATarget != "" {
		lastErr = s.executeA2AJob(ctx, job)
	} else {
		// Local agent mode
		multiAgentPrompt := false
		var sess *session.Manager
		workDir := job.WorkDir
		// Channel/API runs serialize writes to a bound session through the
		// shared runtime lock. Cron runs must use the same lock before opening
		// and appending to that session, otherwise both agents can write from
		// the same stale leaf and trigger ErrSessionModified.
		var releaseRuntime func()
		if job.SessionID != "" && s.sessionDir != "" {
			guard, err := agentruntime.AcquireExecutionAdmission(ctx, s.sessionDir, job.SessionID, agentruntime.ExecutionAdmissionOptions{Wait: true})
			if err != nil {
				lastErr = fmt.Errorf("acquire cron execution admission: %w", err)
				return
			}
			releaseRuntime = guard.Release
			defer releaseRuntime()
			if opened, err := session.OpenByIDExact(s.sessionDir, job.SessionID); err == nil {
				sess = opened
				if workDir == "" {
					if header := opened.GetHeader(); header != nil && header.Cwd != "" {
						workDir = header.Cwd
					}
				}
			}
		}
		resolution, effectiveMode, policyErr := agentruntime.ResolvePolicy(agentruntime.SourceResolutionInput{
			Requested: agentruntime.SourceCron,
		}, "", job.Mode, agentruntime.ModeYolo)
		if sess != nil && job.SessionID != "" && s.sessionDir != "" {
			resolution, effectiveMode, policyErr = agentruntime.ResolvePolicyFromSession(
				s.sessionDir,
				job.SessionID,
				agentruntime.SourceResolutionInput{SessionHeader: sess.GetHeader(), Requested: agentruntime.SourceCron},
				"",
				job.Mode,
				agentruntime.ModeYolo,
			)
		}
		runSource := string(resolution.Source)
		if runSource == "" {
			runSource = string(agentruntime.SourceCron)
		}
		if policyErr != nil {
			lastErr = fmt.Errorf("resolve cron execution policy: %w", policyErr)
		}
		var runID string
		var runCtx context.Context
		var execution *agentruntime.ExecutionRuntime
		if lastErr == nil && sess != nil && job.SessionID != "" && s.sessionDir != "" {
			runID = "cron_" + session.GenerateID()
			startedAt := time.Now()
			data, _ := json.Marshal(map[string]string{
				"cronJobId":   job.ID,
				"cronJobName": job.Name,
			})
			execution = &agentruntime.ExecutionRuntime{}
			execution.SetRunStore(agentruntime.RunStore{SessionDir: s.sessionDir})
			execution.SetEventSink(agentruntime.SessionRunEventSink{SessionDir: s.sessionDir})
			var beginErr error
			// A cron run must survive the scheduler being stopped: saving settings or
			// restarting the cron runtime used to cancel every in-flight job through
			// the scheduler context. Detach the durable run from that lifecycle and
			// bound it with the same generous lease the job record uses.
			runCtxParent, cancelRunCtx := context.WithTimeout(context.WithoutCancel(ctx), runningLeaseTimeout)
			defer cancelRunCtx()
			runCtx, beginErr = execution.BeginDurable(runCtxParent, agentruntime.DurableRun{
				ID: runID, SessionID: job.SessionID, WorkDir: workDir,
				Source: runSource, Mode: effectiveMode, Status: "running", StartedAt: startedAt,
			}, agentruntime.RunEvent{
				SessionID: job.SessionID, RunID: runID, EventType: "started",
				Source: runSource, Status: "running", Mode: effectiveMode, Data: data,
			})
			if beginErr != nil {
				lastErr = fmt.Errorf("begin cron run: %w", beginErr)
				execution = nil
			} else {
				defer func() {
					status := agentruntime.RunStateCompleted
					message := ""
					if lastErr != nil {
						status = agentruntime.RunStateFailed
						message = lastErr.Error()
					}
					if err := execution.FinishDurable(runID, status, message, agentruntime.RunEvent{
						SessionID: job.SessionID, RunID: runID, EventType: func() string {
							if status == agentruntime.RunStateFailed {
								return "failed"
							}
							return "finished"
						}(), Source: runSource, Status: string(status), Mode: effectiveMode,
						Data: func() json.RawMessage {
							if message == "" {
								return data
							}
							encoded, _ := json.Marshal(map[string]string{"cronJobId": job.ID, "cronJobName": job.Name, "error": message})
							return encoded
						}(), Timestamp: time.Now(),
					}); err != nil {
						log.Printf("[cron] finish run %s: %v", runID, err)
					}
				}()
			}
		}
		if lastErr == nil && s.manager == nil {
			lastErr = fmt.Errorf("create agent: agent manager unavailable")
		}
		if lastErr == nil {
			a, err := s.manager.Create(agent.AgentOptions{
				IsSubAgent: sess == nil,
				Mode:       effectiveMode,
				WorkDir:    workDir,
				Session:    sess,
				MultiAgent: &multiAgentPrompt,
			})
			if err != nil {
				lastErr = fmt.Errorf("create agent: %w", err)
			} else {
				if execution != nil {
					execution.SetAgent(a)
				} else {
					runCtx = context.Background()
				}
				ch := a.Run(runCtx, job.Prompt)
				for event := range ch {
					if event.Type == agentpkg.EventTextDelta {
						response.WriteString(event.TextDelta)
					}
					if event.Error != nil {
						lastErr = event.Error
					}
				}
				s.manager.Destroy(a.ID())
			}
		}
	}

	s.updateJob(job.ID, func(current *CronJob) { s.completeJob(current, lastErr) })
}

func (s *Scheduler) completeJob(current *CronJob, runErr error) {
	if current == nil {
		return
	}
	current.RunCount++
	if runErr != nil {
		current.LastStatus = "failed"
		current.LastError = runErr.Error()
	} else {
		current.LastStatus = "success"
		current.LastError = ""
	}

	// Compute next run from the latest stored schedule.
	next, isOneShot, err := ParseSchedule(current.Schedule, time.Now())
	if err != nil {
		isOneShot = true
	}
	if isOneShot || current.OneShot {
		current.Enabled = false
		current.NextRun = time.Time{}
	} else {
		current.NextRun = next
	}
}

func (s *Scheduler) updateJob(id string, update func(*CronJob)) {
	current, err := s.store.Get(id)
	if err != nil {
		return
	}
	update(current)
	_ = s.store.Update(*current)
}

// ErrJobAlreadyRunning is returned by RunNow when the stored job still holds
// a fresh running claim.
var ErrJobAlreadyRunning = fmt.Errorf("cron job is already running")

// RunNow triggers one immediate manual execution of a stored job without
// waiting for the next scheduler tick. It reuses the claim path so last_run /
// running stamping and cross-process coordination stay identical to scheduled
// runs, and executes asynchronously; completion surfaces through the
// installed completion observers. Stopping the scheduler cancels a manual run
// that has not finished yet.
func (s *Scheduler) RunNow(id string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("cron store unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("cron job id is required")
	}
	job, err := s.store.Get(id)
	if err != nil {
		return err
	}
	now := time.Now()
	if job.LastStatus == "running" && !s.isStaleRunning(*job, now) {
		return fmt.Errorf("%w: %s", ErrJobAlreadyRunning, id)
	}
	// Manual run is an explicit override (same semantics as the cron tool run
	// action): re-enable the job and clear its schedule state so the claim
	// path stamps last_run/running atomically.
	job.Enabled = true
	job.LastRun = time.Time{}
	job.NextRun = time.Time{}
	job.LastStatus = ""
	job.LastError = ""
	if err := s.store.Update(*job); err != nil {
		return err
	}
	claimed, release, err := s.claimJob(job.ID, now)
	if err != nil {
		return err
	}
	if !claimed {
		release()
		return fmt.Errorf("cron job %s could not be claimed for a manual run", id)
	}
	s.mu.Lock()
	runCtx := s.stopCtx
	s.mu.Unlock()
	if runCtx == nil {
		runCtx = context.Background()
	}
	s.jobWG.Add(1)
	go func() {
		defer s.jobWG.Done()
		defer release()
		s.executeJobContext(runCtx, *job)
	}()
	return nil
}

// executeA2AJob sends a task to a remote A2A server.
func (s *Scheduler) executeA2AJob(ctx context.Context, job CronJob) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"role":  "user",
				"parts": []map[string]string{{"type": "text", "text": job.Prompt}},
			},
		},
		"id": 1,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", job.A2ATarget+"/a2a", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if job.A2AToken != "" {
		req.Header.Set("Authorization", "Bearer "+job.A2AToken)
	}

	resp, err := a2aHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("a2a request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("a2a request: status %d", resp.StatusCode)
	}

	var result struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxA2AResponseBytes)).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if result.Error != nil {
		return fmt.Errorf("a2a error: %s", result.Error.Message)
	}
	return nil
}
