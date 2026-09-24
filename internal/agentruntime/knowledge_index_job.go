package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/session"
)

var errKnowledgeBaseServiceNil = errors.New("knowledge base service is nil")

func knowledgeBaseDisabledError(id string) error {
	return fmt.Errorf("%w: %s", session.ErrKnowledgeBaseDisabled, id)
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
// goroutine. Concurrent starts for the same base share one job, and any trigger
// that arrives while a scan is running is coalesced into a single follow-up pass
// so the newest changes are still indexed without queueing a run per trigger.
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
			// A scan is in flight: coalesce this trigger into one follow-up run
			// instead of starting a parallel scan or dropping the request.
			s.recordPendingLocked(knowledgeBaseID, source)
			return job, nil
		}
	}
	return s.startIndexJobLocked(knowledgeBaseID, source), nil
}

// startIndexJobLocked registers and launches one background index pass. The
// caller must hold indexJobsMu. On completion it runs afterIndexJob, which
// starts a coalesced follow-up when another trigger arrived mid-scan.
func (s *KnowledgeBaseService) startIndexJobLocked(knowledgeBaseID string, source RuntimeSource) *KnowledgeIndexJob {
	job := newKnowledgeIndexJob()
	if s.indexJobs == nil {
		s.indexJobs = make(map[string]*KnowledgeIndexJob)
	}
	s.indexJobs[knowledgeBaseID] = job
	go func() {
		snapshot, indexErr := s.IndexDurableWithProgress(context.Background(), knowledgeBaseID, source, job)
		job.finish(snapshot, indexErr)
		s.afterIndexJob(knowledgeBaseID)
	}()
	return job
}

// recordPendingLocked coalesces one trigger. The most recent source wins so the
// follow-up run keeps an accurate provenance. The caller must hold indexJobsMu.
func (s *KnowledgeBaseService) recordPendingLocked(knowledgeBaseID string, source RuntimeSource) {
	if s.indexPending == nil {
		s.indexPending = make(map[string]RuntimeSource)
	}
	if source == SourceUnknown {
		source = SourceACP
	}
	s.indexPending[knowledgeBaseID] = source
}

// afterIndexJob starts exactly one coalesced follow-up run when a trigger
// arrived while the finished scan was running. It is a no-op when nothing is
// pending or another scan has already started.
func (s *KnowledgeBaseService) afterIndexJob(knowledgeBaseID string) {
	s.indexJobsMu.Lock()
	defer s.indexJobsMu.Unlock()
	source, pending := s.indexPending[knowledgeBaseID]
	if !pending {
		return
	}
	delete(s.indexPending, knowledgeBaseID)
	if job, ok := s.indexJobs[knowledgeBaseID]; ok {
		select {
		case <-job.done:
		default:
			// Another scan already took over; leave its own completion to
			// coalesce any pending trigger.
			s.indexPending[knowledgeBaseID] = source
			return
		}
	}
	s.startIndexJobLocked(knowledgeBaseID, source)
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

// knowledgeIndexRunPrefix identifies the canonical durable Run that performed
// one knowledge-base index pass inside the dedicated librarian session.
const knowledgeIndexRunPrefix = "knowledge_index_"

// KnowledgeIndexRun is a bounded projection of one canonical index Run plus the
// snapshot statistics it produced. It is derived from canonical Run rows and
// the graph snapshot table, never from a second run store.
type KnowledgeIndexRun struct {
	RunID        string     `json:"runId"`
	SnapshotID   string     `json:"snapshotId,omitempty"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	ErrorCode    string     `json:"errorCode,omitempty"`
	ErrorSummary string     `json:"errorSummary,omitempty"`
	FileCount    int        `json:"fileCount"`
	ChunkCount   int        `json:"chunkCount"`
	NodeCount    int        `json:"nodeCount"`
	EdgeCount    int        `json:"edgeCount"`
	Active       bool       `json:"active"`
}

// IndexRuns projects recent index attempts for one knowledge base from its
// canonical durable Runs, newest first.
func (s *KnowledgeBaseService) IndexRuns(ctx context.Context, knowledgeBaseID string, limit int) ([]KnowledgeIndexRun, error) {
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
	runs, err := session.ListSessionRuns(s.sessionDir, KnowledgeLibrarianSessionID(base.ID, base.RootDir), limit)
	if err != nil {
		return nil, err
	}
	snapshotByRun := s.snapshotByRunID(ctx, base)
	result := make([]KnowledgeIndexRun, 0, len(runs))
	for _, run := range runs {
		if !strings.HasPrefix(run.ID, knowledgeIndexRunPrefix) {
			continue
		}
		result = append(result, knowledgeIndexRunFrom(run, snapshotByRun[run.ID], base.ActiveSnapshotID))
	}
	return result, nil
}

// IndexRun loads one index attempt scoped to its knowledge base. It returns
// ErrKnowledgeBaseUnindexed when the Run is unknown or is not an index Run for
// this base.
func (s *KnowledgeBaseService) IndexRun(ctx context.Context, knowledgeBaseID, runID string) (KnowledgeIndexRun, error) {
	if s == nil {
		return KnowledgeIndexRun{}, errKnowledgeBaseServiceNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runID = strings.TrimSpace(runID)
	if runID == "" || !strings.HasPrefix(runID, knowledgeIndexRunPrefix) {
		return KnowledgeIndexRun{}, session.ErrKnowledgeBaseUnindexed
	}
	base, err := session.GetKnowledgeBase(ctx, s.sessionDir, knowledgeBaseID)
	if err != nil {
		return KnowledgeIndexRun{}, err
	}
	run, err := session.GetSessionRunContext(ctx, s.sessionDir, runID)
	if err != nil {
		return KnowledgeIndexRun{}, err
	}
	if run == nil || run.SessionID != KnowledgeLibrarianSessionID(base.ID, base.RootDir) {
		return KnowledgeIndexRun{}, session.ErrKnowledgeBaseUnindexed
	}
	snapshotByRun := s.snapshotByRunID(ctx, base)
	return knowledgeIndexRunFrom(*run, snapshotByRun[run.ID], base.ActiveSnapshotID), nil
}

// ClearIndex removes the active snapshot and graph rows, keeping configuration
// and the source directory.
func (s *KnowledgeBaseService) ClearIndex(ctx context.Context, knowledgeBaseID string) error {
	if s == nil {
		return errKnowledgeBaseServiceNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return session.ClearKnowledgeBaseIndex(ctx, s.sessionDir, knowledgeBaseID)
}

// Sources projects the active snapshot's file-level provenance.
func (s *KnowledgeBaseService) Sources(ctx context.Context, knowledgeBaseID string) ([]session.KnowledgeSource, error) {
	if s == nil {
		return nil, errKnowledgeBaseServiceNil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return session.ListKnowledgeSources(ctx, s.sessionDir, knowledgeBaseID)
}

// snapshotByRunID maps the base's retained snapshot run ID to its snapshot. At
// most one snapshot is retained per base, so this is a best-effort statistic
// source for the active index run.
func (s *KnowledgeBaseService) snapshotByRunID(ctx context.Context, base session.KnowledgeBase) map[string]session.KnowledgeSnapshot {
	result := make(map[string]session.KnowledgeSnapshot)
	if strings.TrimSpace(base.ActiveSnapshotID) == "" {
		return result
	}
	snapshot, err := session.GetKnowledgeSnapshot(ctx, s.sessionDir, base.ActiveSnapshotID)
	if err != nil || strings.TrimSpace(snapshot.RunID) == "" {
		return result
	}
	result[snapshot.RunID] = snapshot
	return result
}

func knowledgeIndexRunFrom(run session.SessionRun, snapshot session.KnowledgeSnapshot, activeSnapshotID string) KnowledgeIndexRun {
	projection := KnowledgeIndexRun{
		RunID:        run.ID,
		SnapshotID:   snapshot.ID,
		Status:       run.Status,
		StartedAt:    run.StartedAt,
		FinishedAt:   run.FinishedAt,
		ErrorCode:    knowledgeIndexRunErrorCode(run.ErrorInfo),
		ErrorSummary: strings.TrimSpace(run.Error),
		FileCount:    snapshot.FileCount,
		ChunkCount:   snapshot.ChunkCount,
		NodeCount:    snapshot.NodeCount,
		EdgeCount:    snapshot.EdgeCount,
		Active:       snapshot.ID != "" && snapshot.ID == activeSnapshotID,
	}
	return projection
}

// knowledgeIndexRunErrorCode extracts the stable error code the Runtime stored
// on a failed index Run (for example knowledge_base_model_unavailable) from the
// canonical run's structured ErrorInfo, so management surfaces can report a
// machine-readable code without re-parsing free-text error messages.
func knowledgeIndexRunErrorCode(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var info struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return ""
	}
	return strings.TrimSpace(info.Code)
}
