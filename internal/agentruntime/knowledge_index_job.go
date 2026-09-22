package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/session"
)

var errKnowledgeBaseServiceNil = errors.New("knowledge base service is nil")

func knowledgeBaseDisabledError(id string) error {
	return fmt.Errorf("knowledge base %s is disabled", id)
}

// Knowledge indexing phases projected to management surfaces. They describe
// where a background scan currently is; callers poll them periodically and
// never block a transport while a scan runs.
const (
	KnowledgeIndexPhaseScanning   = "scanning"
	KnowledgeIndexPhaseIndexing   = "indexing"
	KnowledgeIndexPhaseEnriching  = "enriching"
	KnowledgeIndexPhaseCommitting = "committing"
)

// KnowledgeIndexProgress is a point-in-time view of one background index job.
type KnowledgeIndexProgress struct {
	Running    bool      `json:"running"`
	Phase      string    `json:"phase,omitempty"`
	FilesTotal int64     `json:"filesTotal"`
	FilesDone  int64     `json:"filesDone"`
	Chunks     int64     `json:"chunks"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	RunID      string    `json:"runId,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// KnowledgeIndexJob tracks one asynchronous scan/index pass. Scans always run
// in their own goroutine so neither the ACP transport nor any management RPC
// handler can be blocked by a long index; observers poll Progress or Wait.
type KnowledgeIndexJob struct {
	mu       sync.Mutex
	progress KnowledgeIndexProgress
	done     chan struct{}
	finished bool
	snapshot session.KnowledgeSnapshot
	err      error
}

func newKnowledgeIndexJob() *KnowledgeIndexJob {
	return &KnowledgeIndexJob{
		done:     make(chan struct{}),
		progress: KnowledgeIndexProgress{Running: true, StartedAt: time.Now()},
	}
}

// Progress returns a consistent copy of the current progress view.
func (j *KnowledgeIndexJob) Progress() KnowledgeIndexProgress {
	if j == nil {
		return KnowledgeIndexProgress{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progress
}

// Done exposes the completion channel of the job.
func (j *KnowledgeIndexJob) Done() <-chan struct{} {
	return j.done
}

// Wait blocks until the job finishes or ctx expires. It is used by cron job
// execution, which needs the terminal result; interactive callers return
// immediately after StartIndex and poll Progress instead.
func (j *KnowledgeIndexJob) Wait(ctx context.Context) (session.KnowledgeSnapshot, error) {
	if j == nil {
		return session.KnowledgeSnapshot{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-j.done:
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.snapshot, j.err
	case <-ctx.Done():
		return session.KnowledgeSnapshot{}, ctx.Err()
	}
}

func (j *KnowledgeIndexJob) update(fn func(*KnowledgeIndexProgress)) {
	if j == nil || fn == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.progress)
}

func (j *KnowledgeIndexJob) finish(snapshot session.KnowledgeSnapshot, err error) {
	if j == nil {
		return
	}
	j.mu.Lock()
	if j.finished {
		j.mu.Unlock()
		return
	}
	j.finished = true
	j.snapshot = snapshot
	j.err = err
	j.progress.Running = false
	if err != nil {
		j.progress.Error = err.Error()
	}
	j.mu.Unlock()
	close(j.done)
}

// StartIndex launches (or reuses) the background index job for one knowledge
// base and returns immediately. Validation of the base happens synchronously
// because it is inexpensive; the scan itself never runs on the caller's
// goroutine. Concurrent starts for the same base share one job.
func (s *KnowledgeBaseService) StartIndex(ctx context.Context, knowledgeBaseID string, source RuntimeSource) (*KnowledgeIndexJob, error) {
	if s == nil {
		return nil, errKnowledgeBaseServiceNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := session.GetKnowledgeBase(ctx, s.sessionDir, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if !base.Enabled {
		return nil, knowledgeBaseDisabledError(base.ID)
	}
	s.indexJobsMu.Lock()
	defer s.indexJobsMu.Unlock()
	if job, ok := s.indexJobs[knowledgeBaseID]; ok {
		select {
		case <-job.done:
			// The previous job finished; a new start replaces it below.
		default:
			return job, nil
		}
	}
	job := newKnowledgeIndexJob()
	if s.indexJobs == nil {
		s.indexJobs = make(map[string]*KnowledgeIndexJob)
	}
	s.indexJobs[knowledgeBaseID] = job
	go func() {
		snapshot, indexErr := s.IndexDurableWithProgress(context.Background(), knowledgeBaseID, source, job)
		job.finish(snapshot, indexErr)
	}()
	return job, nil
}

// IndexJob returns the current (or most recent) index job for a base.
func (s *KnowledgeBaseService) IndexJob(knowledgeBaseID string) (*KnowledgeIndexJob, bool) {
	if s == nil {
		return nil, false
	}
	s.indexJobsMu.Lock()
	defer s.indexJobsMu.Unlock()
	job, ok := s.indexJobs[knowledgeBaseID]
	return job, ok
}

// IndexProgress reports the live progress of a running scan, if any.
func (s *KnowledgeBaseService) IndexProgress(knowledgeBaseID string) (KnowledgeIndexProgress, bool) {
	job, ok := s.IndexJob(knowledgeBaseID)
	if !ok {
		return KnowledgeIndexProgress{}, false
	}
	progress := job.Progress()
	if !progress.Running {
		return KnowledgeIndexProgress{}, false
	}
	return progress, true
}
