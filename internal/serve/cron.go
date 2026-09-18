package serve

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/util"
)

// cronMaintenancePolicy resolves the Runtime maintenance policy from the current
// global settings so serve projects the operator's cadence and on/off choice.
// An unreadable settings file keeps the Runtime default, which is safe: the
// reconciliation is bounded by the attachment retention window and fails closed
// when the durable reference set cannot be read.
func cronMaintenancePolicy() agentruntime.MaintenancePolicy {
	settings, err := config.LoadSettings()
	if err != nil {
		log.Printf("[serve] load settings for the cron maintenance policy: %v", err)
		return agentruntime.DefaultMaintenancePolicy()
	}
	return agentruntime.MaintenancePolicyFromSettings(settings)
}

type cronAPIResponse struct {
	Enabled bool           `json:"enabled"`
	Running bool           `json:"running"`
	Path    string         `json:"path,omitempty"`
	Jobs    []cron.CronJob `json:"jobs"`
}

type cronJobRequest struct {
	SessionID *string `json:"sessionId,omitempty"`
	Name      *string `json:"name,omitempty"`
	Prompt    *string `json:"prompt,omitempty"`
	Schedule  *string `json:"schedule,omitempty"`
	OneShot   *bool   `json:"oneshot,omitempty"`
	Mode      *string `json:"mode,omitempty"`
	WorkDir   *string `json:"workDir,omitempty"`
	A2ATarget *string `json:"a2aTarget,omitempty"`
	A2AToken  *string `json:"a2aToken,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
}

func (rt *channelRuntime) handleCron(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rt.writeCronStatus(w, r)
	case http.MethodPost:
		rt.handleCronCreate(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (rt *channelRuntime) handleCronByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/cron/"), "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cron job ID required"})
		return
	}

	switch r.Method {
	case http.MethodPatch, http.MethodPut:
		rt.handleCronUpdate(w, r, id)
	case http.MethodDelete:
		rt.handleCronDelete(w, r, id)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (rt *channelRuntime) writeCronStatus(w http.ResponseWriter, r *http.Request) {
	jobs, err := rt.listCronJobs(cronSessionIDFromRequest(r, cronJobRequest{}))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	publicJobs := make([]cron.CronJob, len(jobs))
	for i, job := range jobs {
		publicJobs[i] = publicCronJob(job)
	}
	writeJSON(w, http.StatusOK, cronAPIResponse{
		Enabled: rt.cronEnabled(),
		Running: rt.cronRunning(),
		Path:    rt.cronPath(),
		Jobs:    publicJobs,
	})
}

func (rt *channelRuntime) handleCronCreate(w http.ResponseWriter, r *http.Request) {
	store := rt.ensureCronStore()
	if store == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cron is disabled"})
		return
	}

	var req cronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if req.Prompt == nil || strings.TrimSpace(*req.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
		return
	}
	sessionID := cronSessionIDFromRequest(r, req)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionId is required"})
		return
	}

	job := cron.CronJob{
		SessionID: sessionID,
		Name:      strings.TrimSpace(*req.Name),
		Prompt:    *req.Prompt,
		Enabled:   true,
		Mode:      "yolo",
		WorkDir:   rt.cronWorkDirForSession(sessionID),
		Schedule:  "",
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	if req.Mode != nil && strings.TrimSpace(*req.Mode) != "" {
		job.Mode = strings.TrimSpace(*req.Mode)
	}
	if req.WorkDir != nil && strings.TrimSpace(*req.WorkDir) != "" {
		job.WorkDir = strings.TrimSpace(*req.WorkDir)
	}
	if err := rt.validateCronWorkDir(job.WorkDir); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if req.Schedule != nil {
		job.Schedule = strings.TrimSpace(*req.Schedule)
	}
	if req.OneShot != nil {
		job.OneShot = *req.OneShot
	}
	if req.A2ATarget != nil {
		job.A2ATarget = strings.TrimSpace(*req.A2ATarget)
	}
	if req.A2AToken != nil {
		job.A2AToken = strings.TrimSpace(*req.A2AToken)
	}
	if err := normalizeCronJobSchedule(&job); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	created, err := store.Create(job)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job": publicCronJob(*created)})
}

func (rt *channelRuntime) handleCronUpdate(w http.ResponseWriter, r *http.Request, id string) {
	store := rt.ensureCronStore()
	if store == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cron is disabled"})
		return
	}
	var req cronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	sessionID := cronSessionIDFromRequest(r, req)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionId is required"})
		return
	}
	store = cron.NewSessionScopedStore(store, sessionID)
	job, err := store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	if req.Name != nil {
		job.Name = strings.TrimSpace(*req.Name)
	}
	if req.Prompt != nil {
		job.Prompt = *req.Prompt
	}
	if req.Schedule != nil {
		job.Schedule = strings.TrimSpace(*req.Schedule)
	}
	if req.OneShot != nil {
		job.OneShot = *req.OneShot
	}
	if req.Mode != nil {
		job.Mode = strings.TrimSpace(*req.Mode)
	}
	if req.WorkDir != nil {
		job.WorkDir = strings.TrimSpace(*req.WorkDir)
	}
	if err := rt.validateCronWorkDir(job.WorkDir); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if req.A2ATarget != nil {
		job.A2ATarget = strings.TrimSpace(*req.A2ATarget)
	}
	if req.A2AToken != nil {
		job.A2AToken = strings.TrimSpace(*req.A2AToken)
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	if req.SessionID != nil && strings.TrimSpace(*req.SessionID) != "" && strings.TrimSpace(*req.SessionID) != sessionID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "cron job not found in this session"})
		return
	}
	if strings.TrimSpace(job.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if strings.TrimSpace(job.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
		return
	}
	if err := normalizeCronJobSchedule(job); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := store.Update(*job); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": publicCronJob(*job)})
}

func (rt *channelRuntime) handleCronDelete(w http.ResponseWriter, r *http.Request, id string) {
	store := rt.ensureCronStore()
	if store == nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cron is disabled"})
		return
	}
	sessionID := cronSessionIDFromRequest(r, cronJobRequest{})
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionId is required"})
		return
	}
	store = cron.NewSessionScopedStore(store, sessionID)
	if err := store.Delete(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (rt *channelRuntime) listCronJobs(sessionID string) ([]cron.CronJob, error) {
	store := rt.ensureCronStore()
	if store == nil {
		return []cron.CronJob{}, nil
	}
	if sessionID != "" {
		store = cron.NewSessionScopedStore(store, sessionID)
	}
	jobs, err := store.List()
	if err != nil {
		return nil, err
	}
	// Runtime-owned maintenance jobs are scheduled housekeeping, not user
	// automations, so the Web UI cron view never renders them.
	jobs = cron.UserVisibleJobs(jobs)
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func cronSessionIDFromRequest(r *http.Request, req cronJobRequest) string {
	if req.SessionID != nil {
		return strings.TrimSpace(*req.SessionID)
	}
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("sessionId"))
}

func (rt *channelRuntime) ensureCronStore() cron.CronStore {
	if rt == nil || !rt.cronEnabled() {
		return nil
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	nextPath := filepath.Join(rt.sessionDir, "sessions.db")
	if rt.cronStore == nil || rt.cronStorePath != nextPath {
		rt.stopCronSchedulerLocked()
		rt.cronStorePath = nextPath
		rt.cronStore = cron.NewSQLiteCronStore(rt.sessionDir)
	}
	return rt.cronStore
}

func (rt *channelRuntime) cronEnabled() bool {
	cfg := rt.configSnapshot()
	return cfg != nil && cfg.Features.Cron
}

func (rt *channelRuntime) cronPath() string {
	if rt == nil {
		return ""
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	if rt.cronStorePath != "" {
		return rt.cronStorePath
	}
	if rt.configSnapshot() == nil {
		return ""
	}
	return filepath.Join(rt.sessionDir, "sessions.db")
}

func (rt *channelRuntime) cronRunning() bool {
	if rt == nil {
		return false
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	return rt.cronScheduler != nil && rt.cronScheduler.IsRunning()
}

func (rt *channelRuntime) cronWorkDirForSession(sessionID string) string {
	if rt != nil && sessionID != "" && rt.sessionDir != "" {
		if mgr, err := session.OpenByIDExact(rt.sessionDir, sessionID); err == nil {
			if header := mgr.GetHeader(); header != nil && header.Cwd != "" {
				return header.Cwd
			}
		}
	}
	if cfg := rt.configSnapshot(); cfg != nil {
		return cfg.API.GetWorkDir()
	}
	return ""
}

func normalizeCronJobSchedule(job *cron.CronJob) error {
	if job == nil {
		return fmt.Errorf("cron job required")
	}
	if job.Mode == "" {
		job.Mode = "yolo"
	}
	if job.Mode != "agent" && job.Mode != "yolo" {
		return fmt.Errorf("mode must be agent or yolo")
	}

	next, isOneShot, err := cron.ParseSchedule(job.Schedule, time.Now())
	if err != nil {
		return err
	}
	if job.OneShot || isOneShot {
		job.OneShot = true
		job.NextRun = time.Time{}
		return nil
	}
	job.NextRun = next
	return nil
}

func publicCronJob(job cron.CronJob) cron.CronJob {
	job.A2AToken = ""
	return job
}

func (rt *channelRuntime) validateCronWorkDir(workDir string) error {
	if rt == nil || strings.TrimSpace(workDir) == "" {
		return nil
	}
	cfg := rt.configSnapshot()
	if cfg == nil {
		return nil
	}
	if cfg.API.AllowedWorkDirs != nil {
		return cfg.API.ValidateWorkDir(workDir)
	}
	if len(cfg.Security.AllowedWorkDirs) == 0 {
		return nil
	}
	for _, allowed := range cfg.Security.AllowedWorkDirs {
		within, err := util.IsWithinPath(allowed, workDir)
		if err == nil && within {
			return nil
		}
	}
	return fmt.Errorf("working directory %s not in allowed_work_dirs", workDir)
}
