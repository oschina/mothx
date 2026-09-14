package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/mcp"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	"github.com/startvibecoding/mothx/internal/session"
)

const knowledgeBaseCronJobPrefix = "knowledge-base-index:"

// The knowledge-base management RPCs project Runtime/session owned state. They
// deliberately do not expose graph tables or source files wholesale: query is
// a bounded preview endpoint, while normal Desktop prompts will use the
// Runtime input contract in the next integration step.

type manageKnowledgeBaseMutation struct {
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

func (m manageKnowledgeBaseMutation) spec() session.KnowledgeBaseSpec {
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

type manageKnowledgeBaseIDRequest struct {
	ID string `json:"id"`
}

type manageKnowledgeBaseCreateRequest struct {
	KnowledgeBase manageKnowledgeBaseMutation `json:"knowledgeBase"`
}

type manageKnowledgeBaseUpdateRequest struct {
	ID            string                      `json:"id"`
	KnowledgeBase manageKnowledgeBaseMutation `json:"knowledgeBase"`
}

type manageKnowledgeBaseQueryRequest struct {
	ID    string `json:"id"`
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type manageKnowledgeBaseMCPApplyRequest struct {
	ID      string `json:"id"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type manageKnowledgeBaseView struct {
	KnowledgeBase session.KnowledgeBase      `json:"knowledgeBase"`
	Snapshot      *session.KnowledgeSnapshot `json:"snapshot"`
	Status        string                     `json:"status"`
	Indexing      *manageKnowledgeIndexView  `json:"indexing,omitempty"`
}

// manageKnowledgeIndexView projects the live background scan progress. Hosts
// poll list/status while running is true instead of blocking on the scan RPC.
type manageKnowledgeIndexView struct {
	Running    bool      `json:"running"`
	Phase      string    `json:"phase,omitempty"`
	FilesTotal int64     `json:"filesTotal"`
	FilesDone  int64     `json:"filesDone"`
	Chunks     int64     `json:"chunks"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	RunID      string    `json:"runId,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func manageKnowledgeIndexViewFrom(progress agentruntime.KnowledgeIndexProgress) manageKnowledgeIndexView {
	return manageKnowledgeIndexView{
		Running: progress.Running, Phase: progress.Phase,
		FilesTotal: progress.FilesTotal, FilesDone: progress.FilesDone, Chunks: progress.Chunks,
		StartedAt: progress.StartedAt, RunID: progress.RunID, Error: progress.Error,
	}
}

func (s *server) manageKnowledgeBaseService() (*agentruntime.KnowledgeBaseService, error) {
	if s == nil || s.settings == nil {
		return nil, fmt.Errorf("knowledge base runtime is unavailable")
	}
	return agentruntime.NewKnowledgeBaseServiceWithSettings(s.settings.GetSessionDir(), agentruntime.DefaultKnowledgeBaseIndexPolicy(), s.settings)
}

func knowledgeBaseMCPServerName(id string) string {
	return "knowledge-" + strings.TrimSpace(id)
}

func knowledgeBaseMCPCommand() string {
	command, err := os.Executable()
	if err != nil || strings.TrimSpace(command) == "" {
		return "mothx"
	}
	return command
}

// handleManageKnowledgeBaseMCPApply owns the standard Knowledge MCP server
// projection. Desktop supplies only a knowledge-base ID and desired state;
// the ACP runtime derives the bundled command and canonical arguments.
func (s *server) handleManageKnowledgeBaseMCPApply(req rpcRequest) {
	var in manageKnowledgeBaseMCPApplyRequest
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "knowledge_base_invalid_request", "invalid knowledge MCP request", nil))
		return
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "knowledge_base_invalid_request", "knowledge base id is required", nil))
		return
	}
	if s == nil || s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_unavailable", "knowledge base runtime is unavailable", nil))
		return
	}
	if _, err := session.GetKnowledgeBase(context.Background(), s.settings.GetSessionDir(), in.ID); err != nil {
		code := "knowledge_base_unavailable"
		if errors.Is(err, session.ErrKnowledgeBaseNotFound) {
			code = "knowledge_base_not_found"
		}
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, code, fmt.Sprintf("load knowledge base: %v", err), nil))
		return
	}
	target, err := s.resolveManageMCPTarget("global", "")
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "mcp_unavailable", err.Error(), nil))
		return
	}
	cfg, err := s.manageMCPConfigAtPath(target.Path)
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "mcp_unavailable", fmt.Sprintf("load MCP config: %v", err), nil))
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	entry := config.MCPServer{
		Name:    knowledgeBaseMCPServerName(in.ID),
		Type:    "stdio",
		Command: knowledgeBaseMCPCommand(),
		Args:    []string{"knowledge-mcp", "serve", "--knowledge-base", in.ID},
		Enabled: &enabled,
	}
	updated := false
	for index := range cfg.MCPServers {
		if cfg.MCPServers[index].Name == entry.Name {
			cfg.MCPServers[index] = entry
			updated = true
			break
		}
	}
	if !updated {
		cfg.MCPServers = append(cfg.MCPServers, entry)
	}
	config.NormalizeMCPConfig(cfg)
	if err := config.SaveMCPConfig(target.Path, cfg); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "mcp_unavailable", fmt.Sprintf("save MCP config: %v", err), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"id": in.ID, "name": entry.Name, "enabled": enabled}, nil)
}

func knowledgeBaseCronJobID(id string) string {
	return knowledgeBaseCronJobPrefix + strings.TrimSpace(id)
}

func knowledgeBaseIDFromCronJob(job cron.CronJob) (string, bool) {
	id := strings.TrimSpace(strings.TrimPrefix(job.ID, knowledgeBaseCronJobPrefix))
	return id, strings.HasPrefix(job.ID, knowledgeBaseCronJobPrefix) && id != ""
}

// syncKnowledgeBaseSchedule projects the persisted knowledge-base cadence
// onto the shared Cron store. It owns no scheduler state: Cron remains the
// sole lifecycle owner for claims, recovery, terminal status and next-run
// calculation.
func (s *server) syncKnowledgeBaseSchedule(base session.KnowledgeBase) error {
	_, enabled, err := normalizeKnowledgeBaseSchedule(base.Schedule, base.Enabled)
	if err != nil {
		return err
	}
	// A manual/disabled base has no persisted cron projection to create. Keep
	// offline management fixtures and one-shot scans usable when an ACP host
	// has not initialized its long-running scheduler yet.
	if !enabled {
		s.cronMu.Lock()
		store := s.cronStore
		s.cronMu.Unlock()
		if store == nil {
			return nil
		}
		return s.syncKnowledgeBaseScheduleWithStore(store, base)
	}
	_, store, err := s.ensureManageCron()
	if err != nil {
		return err
	}
	return s.syncKnowledgeBaseScheduleWithStore(store, base)
}

func (s *server) syncKnowledgeBaseScheduleWithStore(store cron.CronStore, base session.KnowledgeBase) error {
	if store == nil {
		return fmt.Errorf("knowledge base cron store is unavailable")
	}
	jobID := knowledgeBaseCronJobID(base.ID)
	schedule, enabled, err := normalizeKnowledgeBaseSchedule(base.Schedule, base.Enabled)
	if err != nil {
		return err
	}
	if !enabled {
		if err := store.Delete(jobID); err != nil && !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("remove knowledge base schedule: %w", err)
		}
		return nil
	}
	job := cron.CronJob{
		ID: jobID, Name: "Knowledge base: " + base.Name,
		Prompt:   "Reindex the Desktop knowledge base " + base.ID + ".",
		Schedule: schedule, Mode: "yolo", WorkDir: base.RootDir, Enabled: true,
	}
	if existing, err := store.Get(jobID); err == nil {
		job.CreatedAt = existing.CreatedAt
		job.LastRun = existing.LastRun
		job.NextRun = existing.NextRun
		job.RunCount = existing.RunCount
		job.LastStatus = existing.LastStatus
		job.LastError = existing.LastError
		if err := cron.NormalizeJobSchedule(&job); err != nil {
			return fmt.Errorf("normalize knowledge base schedule: %w", err)
		}
		if err := store.Update(job); err != nil {
			return fmt.Errorf("update knowledge base schedule: %w", err)
		}
		return nil
	} else if !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("load knowledge base schedule: %w", err)
	}
	if err := cron.NormalizeJobSchedule(&job); err != nil {
		return fmt.Errorf("normalize knowledge base schedule: %w", err)
	}
	if _, err := store.Create(job); err != nil {
		return fmt.Errorf("create knowledge base schedule: %w", err)
	}
	return nil
}

func normalizeKnowledgeBaseSchedule(value string, baseEnabled bool) (schedule string, enabled bool, err error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if !baseEnabled || value == "" || value == "manual" || value == "off" || value == "disabled" {
		return "", false, nil
	}
	switch value {
	case "hourly":
		return "@hourly", true, nil
	case "daily":
		return "@daily", true, nil
	case "weekly":
		return "@weekly", true, nil
	case "monthly":
		return "@monthly", true, nil
	}
	if _, _, err := cron.ParseSchedule(value, time.Now()); err != nil {
		return "", false, fmt.Errorf("invalid knowledge base schedule: %w", err)
	}
	return value, true, nil
}

func (s *server) removeKnowledgeBaseSchedule(id string) error {
	s.cronMu.Lock()
	store := s.cronStore
	s.cronMu.Unlock()
	if store == nil {
		return nil
	}
	if err := store.Delete(knowledgeBaseCronJobID(id)); err != nil && !strings.Contains(err.Error(), "not found") {
		return fmt.Errorf("remove knowledge base schedule: %w", err)
	}
	return nil
}

// runKnowledgeBaseCronJob is called by the shared Scheduler only for its
// namespaced jobs. IndexDurable records the actual maintenance Run; Cron then
// records the scheduling outcome and moves the next-run cursor.
func (s *server) runKnowledgeBaseCronJob(ctx context.Context, job cron.CronJob) (bool, string, error) {
	id, ok := knowledgeBaseIDFromCronJob(job)
	if !ok {
		return false, "", nil
	}
	service, err := s.manageKnowledgeBaseService()
	if err != nil {
		return true, "", err
	}
	// Scheduled scans share the same background job machinery as manual scans;
	// the cron goroutine waits for the terminal result so Cron records the
	// scheduling outcome and moves the next-run cursor.
	indexJob, err := service.StartIndex(ctx, id, agentruntime.SourceCron)
	if err != nil {
		return true, "", err
	}
	snapshot, err := indexJob.Wait(ctx)
	if err != nil {
		return true, "", err
	}
	return true, fmt.Sprintf("indexed knowledge base %s: %d files, %d chunks", id, snapshot.FileCount, snapshot.ChunkCount), nil
}

func (s *server) syncAllKnowledgeBaseSchedulesWithStore(store cron.CronStore) error {
	bases, err := session.ListKnowledgeBases(context.Background(), s.settings.GetSessionDir())
	if err != nil {
		return err
	}
	wanted := make(map[string]struct{}, len(bases))
	for _, base := range bases {
		wanted[knowledgeBaseCronJobID(base.ID)] = struct{}{}
		if err := s.syncKnowledgeBaseScheduleWithStore(store, base); err != nil {
			return err
		}
	}
	jobs, err := store.List()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if _, isKnowledgeJob := knowledgeBaseIDFromCronJob(job); !isKnowledgeJob {
			continue
		}
		if _, exists := wanted[job.ID]; !exists {
			if err := store.Delete(job.ID); err != nil && !strings.Contains(err.Error(), "not found") {
				return err
			}
		}
	}
	return nil
}

func (s *server) manageKnowledgeBaseView(ctx context.Context, base session.KnowledgeBase) (manageKnowledgeBaseView, error) {
	view := manageKnowledgeBaseView{KnowledgeBase: base, Status: "unindexed"}
	if strings.TrimSpace(base.ActiveSnapshotID) == "" {
		return view, nil
	}
	snapshot, err := session.GetKnowledgeSnapshot(ctx, s.settings.GetSessionDir(), base.ActiveSnapshotID)
	if err != nil {
		return manageKnowledgeBaseView{}, err
	}
	view.Snapshot = &snapshot
	view.Status = snapshot.Status
	s.attachKnowledgeIndexProgress(ctx, &view, base.ID)
	return view, nil
}

// attachKnowledgeIndexProgress adds the live scan progress (when a background
// index job is running) so management surfaces can poll periodic progress.
func (s *server) attachKnowledgeIndexProgress(ctx context.Context, view *manageKnowledgeBaseView, baseID string) {
	_ = ctx
	service, err := s.manageKnowledgeBaseService()
	if err != nil {
		return
	}
	progress, running := service.IndexProgress(baseID)
	if !running {
		return
	}
	indexing := manageKnowledgeIndexViewFrom(progress)
	view.Indexing = &indexing
}

func (s *server) validateKnowledgeBaseProvider(spec session.KnowledgeBaseSpec) *mcp.RPCError {
	providerID := strings.TrimSpace(spec.Provider)
	modelID := strings.TrimSpace(spec.Model)
	if providerID == "" && modelID == "" {
		return nil
	}
	if providerID == "" || modelID == "" {
		return acpStructuredRPCError(-32602, "knowledge_base_model_invalid", "provider and model must be configured together", nil)
	}
	settings, err := s.manageSettings()
	if err != nil {
		return acpStructuredRPCError(-32000, "settings_unavailable", err.Error(), nil)
	}
	if settings.GetProviderConfig(providerID) == nil && config.DefaultProviderConfig(providerID) == nil {
		return acpStructuredRPCError(-32602, "knowledge_base_provider_not_found", fmt.Sprintf("provider %q is not configured", providerID), map[string]any{"provider": providerID})
	}
	for _, model := range providerfactory.ResolvedModels(settings, providerID) {
		if model != nil && model.ID == modelID {
			return nil
		}
	}
	return acpStructuredRPCError(-32602, "knowledge_base_model_invalid", fmt.Sprintf("model %q is not configured for provider %q", modelID, providerID), map[string]any{"provider": providerID, "model": modelID})
}

func validateKnowledgeBaseSchedule(spec session.KnowledgeBaseSpec) *mcp.RPCError {
	if _, _, err := normalizeKnowledgeBaseSchedule(spec.Schedule, spec.Enabled); err != nil {
		return acpStructuredRPCError(-32602, "knowledge_base_schedule_invalid", err.Error(), map[string]any{"schedule": spec.Schedule})
	}
	return nil
}

func manageKnowledgeBaseRPCError(err error) *mcp.RPCError {
	switch {
	case errors.Is(err, session.ErrKnowledgeBaseNotFound):
		return acpStructuredRPCError(-32602, "knowledge_base_not_found", "knowledge base was not found", nil)
	case errors.Is(err, session.ErrKnowledgeBaseUnindexed):
		return acpStructuredRPCError(-32602, "knowledge_base_unindexed", "knowledge base has no completed index", nil)
	}
	message := strings.TrimSpace(err.Error())
	if strings.Contains(message, "is disabled") {
		return acpStructuredRPCError(-32602, "knowledge_base_disabled", message, nil)
	}
	if strings.Contains(message, "knowledge base root") || strings.Contains(message, "path escaped root") {
		return acpStructuredRPCError(-32602, "knowledge_base_root_unavailable", message, nil)
	}
	return acpStructuredRPCError(-32000, "knowledge_base_operation_failed", message, nil)
}

func manageKnowledgeBaseID(req rpcRequest) (string, *mcp.RPCError) {
	var input manageKnowledgeBaseIDRequest
	if err := json.Unmarshal(req.Params, &input); err != nil || strings.TrimSpace(input.ID) == "" {
		return "", acpStructuredRPCError(-32602, "invalid_params", "knowledge base id is required", nil)
	}
	return strings.TrimSpace(input.ID), nil
}

func (s *server) handleManageKnowledgeBasesList(req rpcRequest) {
	if s == nil || s.settings == nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_unavailable", "knowledge base runtime is unavailable", nil))
		return
	}
	bases, err := session.ListKnowledgeBases(context.Background(), s.settings.GetSessionDir())
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	views := make([]manageKnowledgeBaseView, 0, len(bases))
	for _, base := range bases {
		view, err := s.manageKnowledgeBaseView(context.Background(), base)
		if err != nil {
			s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
			return
		}
		views = append(views, view)
	}
	s.writeResponse(req.ID, map[string]any{"knowledgeBases": views}, nil)
}

func (s *server) handleManageKnowledgeBasesGet(req rpcRequest) {
	id, rpcErr := manageKnowledgeBaseID(req)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	base, err := session.GetKnowledgeBase(context.Background(), s.settings.GetSessionDir(), id)
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	view, err := s.manageKnowledgeBaseView(context.Background(), base)
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	s.writeResponse(req.ID, view, nil)
}

func (s *server) handleManageKnowledgeBasesCreate(req rpcRequest) {
	var input manageKnowledgeBaseCreateRequest
	if err := json.Unmarshal(req.Params, &input); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "knowledgeBase is required", nil))
		return
	}
	spec := input.KnowledgeBase.spec()
	if rpcErr := s.validateKnowledgeBaseProvider(spec); rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if rpcErr := validateKnowledgeBaseSchedule(spec); rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	base, err := session.CreateKnowledgeBase(context.Background(), s.settings.GetSessionDir(), spec)
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	if err := s.syncKnowledgeBaseSchedule(base); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_schedule_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageKnowledgeBaseView{KnowledgeBase: base, Status: "unindexed"}, nil)
}

func (s *server) handleManageKnowledgeBasesUpdate(req rpcRequest) {
	var input manageKnowledgeBaseUpdateRequest
	if err := json.Unmarshal(req.Params, &input); err != nil || strings.TrimSpace(input.ID) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id and knowledgeBase are required", nil))
		return
	}
	spec := input.KnowledgeBase.spec()
	if rpcErr := s.validateKnowledgeBaseProvider(spec); rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if rpcErr := validateKnowledgeBaseSchedule(spec); rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	base, err := session.UpdateKnowledgeBase(context.Background(), s.settings.GetSessionDir(), strings.TrimSpace(input.ID), spec)
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	if err := s.syncKnowledgeBaseSchedule(base); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_schedule_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, manageKnowledgeBaseView{KnowledgeBase: base, Status: "unindexed"}, nil)
}

func (s *server) handleManageKnowledgeBasesDelete(req rpcRequest) {
	id, rpcErr := manageKnowledgeBaseID(req)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	if err := session.DeleteKnowledgeBase(context.Background(), s.settings.GetSessionDir(), id); err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	if err := s.removeKnowledgeBaseSchedule(id); err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_schedule_unavailable", err.Error(), nil))
		return
	}
	s.writeResponse(req.ID, map[string]any{"deleted": true, "id": id}, nil)
}

func (s *server) handleManageKnowledgeBasesScan(req rpcRequest) {
	id, rpcErr := manageKnowledgeBaseID(req)
	if rpcErr != nil {
		s.writeResponse(req.ID, nil, rpcErr)
		return
	}
	service, err := s.manageKnowledgeBaseService()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_unavailable", err.Error(), nil))
		return
	}
	// Scans always run in the background: the RPC returns as soon as the job is
	// admitted so a long index can never stall the ACP request loop. Hosts poll
	// list/status for progress and terminal snapshot state.
	alreadyRunning := false
	if existing, ok := service.IndexJob(id); ok {
		select {
		case <-existing.Done():
		default:
			alreadyRunning = true
		}
	}
	job, err := service.StartIndex(context.Background(), id, agentruntime.SourceACP)
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	progress := job.Progress()
	s.writeResponse(req.ID, map[string]any{
		"started": true, "alreadyRunning": alreadyRunning, "id": id,
		"status":   "indexing",
		"indexing": manageKnowledgeIndexViewFrom(progress),
	}, nil)
}

func (s *server) handleManageKnowledgeBasesStatus(req rpcRequest) {
	s.handleManageKnowledgeBasesGet(req)
}

func (s *server) handleManageKnowledgeBasesQuery(req rpcRequest) {
	var input manageKnowledgeBaseQueryRequest
	if err := json.Unmarshal(req.Params, &input); err != nil || strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Query) == "" {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32602, "invalid_params", "id and query are required", nil))
		return
	}
	if input.Limit <= 0 {
		input.Limit = 8
	}
	if input.Limit > 20 {
		input.Limit = 20
	}
	service, err := s.manageKnowledgeBaseService()
	if err != nil {
		s.writeResponse(req.ID, nil, acpStructuredRPCError(-32000, "knowledge_base_unavailable", err.Error(), nil))
		return
	}
	result, err := service.Query(context.Background(), strings.TrimSpace(input.ID), strings.TrimSpace(input.Query), input.Limit)
	if err != nil {
		s.writeResponse(req.ID, nil, manageKnowledgeBaseRPCError(err))
		return
	}
	s.writeResponse(req.ID, map[string]any{"query": result}, nil)
}
