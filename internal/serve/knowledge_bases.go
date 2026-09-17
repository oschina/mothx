package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/session"
)

// knowledgeBaseMutation is the WebUI projection of the Runtime-owned
// configuration. It never carries source files or graph rows.
type knowledgeBaseMutation struct {
	Name              string `json:"name"`
	RootDir           string `json:"rootDir"`
	PreprocessProfile string `json:"preprocessProfile"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	Mode              string `json:"mode"`
	ThinkingLevel     string `json:"thinkingLevel,omitempty"`
	Schedule          string `json:"schedule"`
	Enabled           *bool  `json:"enabled,omitempty"`
}

func (m knowledgeBaseMutation) spec() session.KnowledgeBaseSpec {
	enabled := true
	if m.Enabled != nil {
		enabled = *m.Enabled
	}
	return session.KnowledgeBaseSpec{
		Name: m.Name, RootDir: m.RootDir, PreprocessProfile: m.PreprocessProfile,
		Provider: m.Provider, Model: m.Model, Mode: m.Mode, ThinkingLevel: m.ThinkingLevel,
		Schedule: m.Schedule, Enabled: enabled,
	}
}

// validateWebKnowledgeBaseSpec keeps this thin HTTP projection honest about
// the capabilities it actually exposes. Provider-backed enrichment remains
// optional, but its two identifiers are one Runtime configuration unit. WebUI
// does not own a scheduler yet, so accepting a non-manual cadence here would
// create persisted configuration with no corresponding Serve lifecycle.
func validateWebKnowledgeBaseSpec(spec session.KnowledgeBaseSpec) error {
	providerID := strings.TrimSpace(spec.Provider)
	modelID := strings.TrimSpace(spec.Model)
	if (providerID == "") != (modelID == "") {
		return errors.New("provider and model must be configured together")
	}
	schedule := strings.TrimSpace(strings.ToLower(spec.Schedule))
	if schedule != "" && schedule != "manual" {
		return errors.New("scheduled knowledge base indexing is not available in WebUI")
	}
	return nil
}

// knowledgeBaseView is intentionally the same bounded management projection
// used by ACP: configuration plus aggregate snapshot metadata, never source
// contents. Query has its own bounded endpoint below.
type knowledgeBaseView struct {
	KnowledgeBase session.KnowledgeBase      `json:"knowledgeBase"`
	Snapshot      *session.KnowledgeSnapshot `json:"snapshot"`
	Status        string                     `json:"status"`
	Indexing      *knowledgeIndexView        `json:"indexing,omitempty"`
}

// knowledgeIndexView projects the live background scan progress. WebUI polls
// list/status while running instead of blocking on the scan request, so a page
// reload can still observe a scan that is in flight.
type knowledgeIndexView struct {
	Running    bool      `json:"running"`
	Phase      string    `json:"phase,omitempty"`
	FilesTotal int64     `json:"filesTotal"`
	FilesDone  int64     `json:"filesDone"`
	Chunks     int64     `json:"chunks"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	RunID      string    `json:"runId,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func knowledgeIndexViewFrom(progress agentruntime.KnowledgeIndexProgress) knowledgeIndexView {
	return knowledgeIndexView{
		Running: progress.Running, Phase: progress.Phase,
		FilesTotal: progress.FilesTotal, FilesDone: progress.FilesDone, Chunks: progress.Chunks,
		StartedAt: progress.StartedAt, RunID: progress.RunID, Error: progress.Error,
	}
}

// knowledgeBaseService returns the process-wide cached Runtime service. Caching
// is required because the service owns the background index-job registry: a
// fresh instance per request would hide a running scan from progress polling on
// reload and let repeated scan requests start parallel jobs.
func (rt *channelRuntime) knowledgeBaseService() (*agentruntime.KnowledgeBaseService, error) {
	rt.knowledgeMu.Lock()
	defer rt.knowledgeMu.Unlock()
	if rt.knowledgeService != nil {
		return rt.knowledgeService, nil
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return nil, err
	}
	service, err := agentruntime.NewKnowledgeBaseServiceWithSettings(rt.sessionDir, agentruntime.DefaultKnowledgeBaseIndexPolicy(), settings)
	if err != nil {
		return nil, err
	}
	rt.knowledgeService = service
	return service, nil
}

// runKnowledgeBaseCronJob routes namespaced knowledge-base reindex jobs
// through the shared Runtime handler. The cron store is shared by sessionDir,
// so schedules persisted by Desktop/ACP are also claimed by this scheduler;
// without this handler they would execute as ordinary agent prompts inside
// the knowledge source directory.
func (rt *channelRuntime) runKnowledgeBaseCronJob(ctx context.Context, job cron.CronJob) (bool, string, error) {
	if _, ok := agentruntime.KnowledgeBaseIDFromCronJobID(job.ID); !ok {
		return false, "", nil
	}
	if rt == nil || strings.TrimSpace(rt.sessionDir) == "" {
		return true, "", fmt.Errorf("knowledge base runtime is unavailable")
	}
	service, err := rt.knowledgeBaseService()
	if err != nil {
		return true, "", err
	}
	return agentruntime.RunKnowledgeBaseCronJob(ctx, service, job.ID)
}

func (rt *channelRuntime) knowledgeBaseView(ctx context.Context, base session.KnowledgeBase) (knowledgeBaseView, error) {
	view := knowledgeBaseView{KnowledgeBase: base, Status: "unindexed"}
	if strings.TrimSpace(base.ActiveSnapshotID) != "" {
		snapshot, err := session.GetKnowledgeSnapshot(ctx, rt.sessionDir, base.ActiveSnapshotID)
		if err != nil {
			return knowledgeBaseView{}, err
		}
		view.Snapshot = &snapshot
		view.Status = snapshot.Status
	}
	// Project live progress even without an active snapshot: the first scan of a
	// new base is exactly when the WebUI needs the running-job view to poll.
	rt.attachKnowledgeIndexProgress(&view, base.ID)
	return view, nil
}

// attachKnowledgeIndexProgress adds the live scan progress (when a background
// index job is running) so the WebUI can render and poll an in-flight scan.
func (rt *channelRuntime) attachKnowledgeIndexProgress(view *knowledgeBaseView, baseID string) {
	service, err := rt.knowledgeBaseService()
	if err != nil {
		return
	}
	progress, running := service.IndexProgress(baseID)
	if !running {
		return
	}
	indexing := knowledgeIndexViewFrom(progress)
	view.Indexing = &indexing
}

// refreshKnowledgeServiceSettings updates the cached Runtime service's settings
// snapshot so a later scan resolves the new Indexer provider/model. It never
// creates the service (a process that has not touched knowledge bases pays
// nothing) and never rebuilds it, because the cached instance owns the in-flight
// background index-job registry that progress polling depends on.
func (rt *channelRuntime) refreshKnowledgeServiceSettings(settings *config.Settings) {
	if rt == nil {
		return
	}
	rt.knowledgeMu.Lock()
	service := rt.knowledgeService
	rt.knowledgeMu.Unlock()
	if service != nil {
		service.SetSettings(settings)
	}
}

func (rt *channelRuntime) handleKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	if rt == nil || strings.TrimSpace(rt.sessionDir) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "knowledge base runtime is unavailable"})
		return
	}
	relative := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/knowledge-bases"), "/")
	if relative == "" {
		switch r.Method {
		case http.MethodGet:
			rt.listKnowledgeBases(w, r)
		case http.MethodPost:
			rt.createKnowledgeBase(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	parts := strings.Split(relative, "/")
	if len(parts) > 2 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge base path"})
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge base ID"})
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "scan":
			if r.Method == http.MethodPost {
				rt.scanKnowledgeBase(w, r, id)
				return
			}
		case "query":
			if r.Method == http.MethodPost {
				rt.queryKnowledgeBase(w, r, id)
				return
			}
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rt.getKnowledgeBase(w, r, id)
	case http.MethodPatch:
		rt.updateKnowledgeBase(w, r, id)
	case http.MethodDelete:
		rt.deleteKnowledgeBase(w, r, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (rt *channelRuntime) listKnowledgeBases(w http.ResponseWriter, r *http.Request) {
	bases, err := session.ListKnowledgeBases(r.Context(), rt.sessionDir)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	views := make([]knowledgeBaseView, 0, len(bases))
	for _, base := range bases {
		view, err := rt.knowledgeBaseView(r.Context(), base)
		if err != nil {
			writeKnowledgeBaseError(w, err)
			return
		}
		views = append(views, view)
	}
	writeJSON(w, http.StatusOK, map[string]any{"knowledgeBases": views})
}

func (rt *channelRuntime) getKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	base, err := session.GetKnowledgeBase(r.Context(), rt.sessionDir, id)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	view, err := rt.knowledgeBaseView(r.Context(), base)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (rt *channelRuntime) createKnowledgeBase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KnowledgeBase knowledgeBaseMutation `json:"knowledgeBase"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge base JSON"})
		return
	}
	spec := body.KnowledgeBase.spec()
	if err := validateWebKnowledgeBaseSpec(spec); err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	base, err := session.CreateKnowledgeBase(r.Context(), rt.sessionDir, spec)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	view, err := rt.knowledgeBaseView(r.Context(), base)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (rt *channelRuntime) updateKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		KnowledgeBase knowledgeBaseMutation `json:"knowledgeBase"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid knowledge base JSON"})
		return
	}
	spec := body.KnowledgeBase.spec()
	if err := validateWebKnowledgeBaseSpec(spec); err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	base, err := session.UpdateKnowledgeBase(r.Context(), rt.sessionDir, id, spec)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	view, err := rt.knowledgeBaseView(r.Context(), base)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (rt *channelRuntime) deleteKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	if err := session.DeleteKnowledgeBase(r.Context(), rt.sessionDir, id); err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

func (rt *channelRuntime) scanKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	service, err := rt.knowledgeBaseService()
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	// Scans always run in the background so the HTTP handler never blocks on a
	// long index and a page reload can still observe the running job. The WebUI
	// polls list/status while indexing.running is true and refreshes to the
	// terminal snapshot state afterwards. Concurrent scans of the same base share
	// one job.
	job, err := service.StartIndex(r.Context(), id, agentruntime.SourceWebUI)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	base, err := session.GetKnowledgeBase(r.Context(), rt.sessionDir, id)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	view, err := rt.knowledgeBaseView(r.Context(), base)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	// Prefer the admitted job's progress over a possible race where the job
	// finished (or was replaced) between admission and this read.
	if view.Indexing == nil && job.Progress().Running {
		indexing := knowledgeIndexViewFrom(job.Progress())
		view.Indexing = &indexing
	}
	writeJSON(w, http.StatusOK, view)
}

func (rt *channelRuntime) queryKnowledgeBase(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || strings.TrimSpace(body.Query) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
		return
	}
	if body.Limit <= 0 {
		body.Limit = 8
	} else if body.Limit > 20 {
		body.Limit = 20
	}
	service, err := rt.knowledgeBaseService()
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	result, err := service.Query(r.Context(), id, strings.TrimSpace(body.Query), body.Limit)
	if err != nil {
		writeKnowledgeBaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": result})
}

func writeKnowledgeBaseError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, session.ErrKnowledgeBaseNotFound):
		status = http.StatusNotFound
	case errors.Is(err, session.ErrKnowledgeBaseUnindexed), strings.Contains(err.Error(), "is disabled"):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
