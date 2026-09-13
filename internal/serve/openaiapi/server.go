package openaiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	browserfeature "github.com/startvibecoding/mothx/internal/browser"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/contextfiles"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/debugpprof"
	"github.com/startvibecoding/mothx/internal/provider"
	providerfactory "github.com/startvibecoding/mothx/internal/provider/factory"
	openaiprovider "github.com/startvibecoding/mothx/internal/provider/openai"
	"github.com/startvibecoding/mothx/internal/sandbox"
	serviceruntime "github.com/startvibecoding/mothx/internal/serve/runtime"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/skills"
	"github.com/startvibecoding/mothx/internal/workflow"
)

// RunOptions controls the OpenAI-compatible API runtime used by serve.
type RunOptions struct {
	Config        *Config
	DisableAPI    bool
	Port          string
	Provider      string
	Model         string
	WorkDir       string
	Unsafe        bool
	Sandbox       bool
	MultiAgent    bool
	Delegate      bool
	Workflows     bool
	WebSearch     bool
	Browser       bool
	Artifact      bool
	A2AMaster     bool
	CronStore     cron.CronStore
	CronScheduler *cron.Scheduler
	Verbose       bool
	Debug         bool
	ExtraRoutes   func(*Server, *http.ServeMux)
	// OnReady connects external channel runtimes to the canonical WebUI runtime state.
	OnReady func(*Server)
	// OnRunComplete is called once after an API run reaches a terminal state.
	OnRunComplete func(sessionID, runID, status, errMsg string)
	// Shutdown requests graceful termination without relying on process signals.
	Shutdown <-chan struct{}
}

// Server is the OpenAI-compatible API HTTP server.
type Server struct {
	mu sync.RWMutex

	cfg              *Config
	settings         *config.Settings
	allow            *config.AllowConfig
	saveProjectAllow func(*config.AllowConfig) error
	version          string

	provider         provider.Provider
	providerName     string // user-configured vendor name (e.g. "longcat")
	providerOverride string
	modelOverride    string
	model            *provider.Model
	sandboxMgr       *sandbox.Manager
	skillsMgr        *skills.Manager
	pool             *SessionPool
	streamHub        *sessionStreamHub // deprecated; use eventBroker for new code
	eventBroker      *EventBroker
	cronStore        cron.CronStore
	cronScheduler    *cron.Scheduler
	runComplete      func(sessionID, runID, status, errMsg string)

	extraContext        string
	defaultSessionIDs   map[string]string    // key: workDir, used by the standard chat endpoint's internal session reuse
	allocatedSessionIDs map[string]time.Time // server-issued IDs awaiting first WebUI run
	sessionCreateMu     sync.Mutex
	externalSyncMu      sync.Mutex
	externalCursors     map[string]sessionStreamCursor
	externalSubAgentMu  sync.Mutex
	externalSubAgents   map[string]*externalSubAgentHistory
	runSlots            chan struct{}
	runManager          *RunManager
	recoveryCoordinator *agentruntime.RecoveryCoordinator
	responsesRuns       serviceruntime.BackgroundRunDriver
	esmCoordinator      *esmCoordinator
}

// IsWebSearchAvailable reports whether hosted web search is available for sessions.
// It is true when either the serve config enables web search or the app-level
// settings.json has webSearch enabled with a configured provider.
func (s *Server) IsWebSearchAvailable() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg != nil && s.cfg.EnableWebSearch {
		return true
	}
	return s.settings != nil && s.settings.IsWebSearchEnabled()
}

// SettingsSkillHub returns a copy of marketplace settings for runtime adapters.
func (s *Server) SettingsSkillHub() config.SkillHubSettings {
	if s == nil {
		return config.SkillHubSettings{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.settings == nil {
		return config.SkillHubSettings{}
	}
	value := s.settings.SkillHub
	value.OfficialHandles = append([]string(nil), value.OfficialHandles...)
	value.Markets = append([]config.SkillHubMarketSettings(nil), value.Markets...)
	return value
}

func (s *Server) SessionDir() string {
	if s == nil || s.settings == nil {
		return ""
	}
	return s.settings.GetSessionDir()
}

// SetRunCompleteObserver replaces the callback invoked after a run reaches a
// terminal state. It is used by serve integrations such as channel runtimes.
func (s *Server) SetRunCompleteObserver(observer func(sessionID, runID, status, errMsg string)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.runComplete = observer
	s.mu.Unlock()
}

func (s *Server) ApplyServeConfig(next *Config) error {
	if s == nil || next == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settings == nil {
		s.cfg = cloneConfig(next)
		return nil
	}
	workDir := next.GetWorkDir()
	mgr := sandbox.NewManagerWithOptions(workDir, s.settings.Sandbox.Options())
	level := sandbox.LevelNone
	if next.Sandbox.Enabled {
		level = sandbox.LevelStandard
		if next.Sandbox.Level == "strict" {
			level = sandbox.LevelStrict
		}
		if err := mgr.SetLevel(level); err != nil {
			return fmt.Errorf("apply serve sandbox: %w", err)
		}
	} else if err := mgr.SetLevel(sandbox.LevelNone); err != nil {
		return err
	}
	s.cfg = cloneConfig(next)
	s.sandboxMgr = mgr
	for _, sess := range s.pool.Snapshot() {
		if sess == nil || sess.Registry == nil {
			continue
		}
		sessMgr := sandbox.NewManagerWithOptions(sess.WorkDir, s.settings.Sandbox.Options())
		if err := sessMgr.SetLevel(level); err != nil {
			return fmt.Errorf("apply session sandbox: %w", err)
		}
		sess.SandboxMgr = sessMgr
		sess.Registry.SetSandbox(sessMgr.GetActive())
		if sess.Runtime != nil {
			// A pool snapshot can include a runtime that completed shutdown just
			// before eviction. It cannot start another run, so it needs no update.
			_ = sess.Runtime.SetArtifactEnabled(next.EnableArtifact)
		}
	}
	return nil
}

func (s *Server) authConfig() AuthConfig {
	if s == nil {
		return AuthConfig{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return AuthConfig{}
	}
	return AuthConfig{
		Enabled: s.cfg.Auth.Enabled,
		Tokens:  append([]string(nil), s.cfg.Auth.Tokens...),
	}
}

// ApplySettings updates the runtime provider/model from a saved settings.json.
func (s *Server) ApplySettings(next *config.Settings) error {
	s.mu.RLock()
	if s.cfg == nil {
		s.mu.RUnlock()
		return nil
	}
	cfg := *s.cfg
	providerOverride := s.providerOverride
	modelOverride := s.modelOverride
	s.mu.RUnlock()

	runtime := *next
	if cfg.EnableWebSearch {
		runtime.WebSearch.Enabled = config.BoolPtr(true)
	}
	providerName := cfg.Provider
	if providerOverride != "" {
		providerName = providerOverride
	}
	if providerName == "" {
		providerName = runtime.DefaultProvider
	}
	modelID := cfg.Model
	if modelOverride != "" {
		modelID = modelOverride
	}
	if modelID == "" {
		if providerOverride != "" || cfg.Provider != "" {
			modelID = ""
		} else {
			modelID = runtime.DefaultModel
		}
	}

	p, model, err := providerfactory.Create(&runtime, providerName, modelID)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}
	skillsMgr, extraContext, err := buildWorkDirContext(&runtime, cfg.GetWorkDir(), cfg.EnableWorkflows, cfg.EnableBrowser)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.settings = &runtime
	s.provider = p
	s.providerName = providerName
	s.model = model
	s.skillsMgr = skillsMgr
	s.extraContext = extraContext
	if op, ok := p.(*openaiprovider.Provider); ok && op.API() == "openai-responses" {
		s.responsesRuns = op.NewResponsesRunManager(runtime.GetSessionDir())
	} else {
		s.responsesRuns = nil
	}
	s.mu.Unlock()
	return nil
}

// Run starts the OpenAI-compatible API server.
func Run(opts RunOptions, version string) error {
	config.Verbose = opts.Verbose || opts.Debug
	if opts.Debug {
		_ = os.Setenv("VIBECODING_DEBUG", "1")
		debugpprof.StartForDebug(os.Stderr)
	}

	// Load settings.json
	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	gCfg, err := loadRunConfig(opts)
	if err != nil {
		return fmt.Errorf("load API config: %w", err)
	}
	if err := validateListenSecurity(gCfg, opts.Unsafe); err != nil {
		return err
	}
	if gCfg.EnableWebSearch {
		settings.WebSearch.Enabled = config.BoolPtr(true)
	}

	// Resolve provider/model
	providerName := gCfg.Provider
	if opts.Provider != "" {
		providerName = opts.Provider
	}
	if providerName == "" {
		providerName = settings.DefaultProvider
	}

	modelID := gCfg.Model
	if opts.Model != "" {
		modelID = opts.Model
	}
	if modelID == "" {
		if opts.Provider != "" || gCfg.Provider != "" {
			modelID = ""
		} else {
			modelID = settings.DefaultModel
		}
	}

	p, model, err := providerfactory.Create(settings, providerName, modelID)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	// Setup working directory
	cwd := gCfg.GetWorkDir()

	// Setup sandbox
	sbMgr := sandbox.NewManagerWithOptions(cwd, settings.Sandbox.Options())
	sbEnabled := gCfg.Sandbox.Enabled
	if !sbEnabled {
		_ = sbMgr.SetLevel(sandbox.LevelNone)
	} else {
		level := sandbox.LevelStandard
		if gCfg.Sandbox.Level == "strict" {
			level = sandbox.LevelStrict
		}
		if err := sbMgr.SetLevel(level); err != nil {
			return fmt.Errorf("strict sandbox enabled but unavailable: %w", err)
		}
		if err := sbMgr.FallbackError(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: sandbox unavailable; using direct execution: %v\n", err)
		}
	}

	skillsMgr, extraContext, err := buildWorkDirContext(settings, cwd, gCfg.EnableWorkflows, gCfg.EnableBrowser)
	if err != nil {
		return err
	}

	// Build session pool
	idleTimeout := time.Duration(gCfg.Session.IdleTimeoutSeconds) * time.Second
	pool := NewSessionPool(gCfg.Session.MaxSessions, idleTimeout)
	var runSlots chan struct{}
	if gCfg.MaxConcurrentReqs > 0 {
		runSlots = make(chan struct{}, gCfg.MaxConcurrentReqs)
	}

	srv := &Server{
		cfg:                 gCfg,
		settings:            settings,
		allow:               config.LoadAllow(),
		version:             version,
		provider:            p,
		providerName:        providerName,
		providerOverride:    opts.Provider,
		modelOverride:       opts.Model,
		model:               model,
		sandboxMgr:          sbMgr,
		skillsMgr:           skillsMgr,
		pool:                pool,
		streamHub:           newSessionStreamHub(),
		eventBroker:         NewEventBroker(),
		cronStore:           opts.CronStore,
		cronScheduler:       opts.CronScheduler,
		runComplete:         opts.OnRunComplete,
		extraContext:        extraContext,
		defaultSessionIDs:   make(map[string]string),
		allocatedSessionIDs: make(map[string]time.Time),
		externalCursors:     make(map[string]sessionStreamCursor),
		runSlots:            runSlots,
		runManager:          NewRunManager(settings.GetSessionDir()),
	}
	if op, ok := p.(*openaiprovider.Provider); ok && op.API() == "openai-responses" {
		srv.responsesRuns = op.NewResponsesRunManager(settings.GetSessionDir())
	}

	// Recovery is a Runtime-owned startup and periodic responsibility. Durable
	// response_runs are the evidence for retaining provider-native background
	// work; source labels alone are never treated as proof of a remote owner.
	recoveryCtx, stopRecovery := context.WithCancel(context.Background())
	srv.recoveryCoordinator = agentruntime.NewRecoveryCoordinator(settings.GetSessionDir(), agentruntime.RecoveryCoordinatorOptions{
		OnResult: func(result agentruntime.RunRecoveryResult) {
			if len(result.Kept) == 0 {
				return
			}
			if err := srv.recoverResponsesBackgroundRuns(); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to recover Responses background runs: %v\n", err)
			}
		},
		OnError: func(err error) {
			fmt.Fprintf(os.Stderr, "Warning: failed to recover orphaned runs: %v\n", err)
		},
	})
	if err := srv.recoveryCoordinator.Start(recoveryCtx); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: initial orphan recovery failed: %v\n", err)
	}
	defer func() {
		stopRecovery()
		ctx, cancel := context.WithTimeout(context.Background(), agentruntime.DefaultRecoveryAttemptTimeout)
		defer cancel()
		_ = srv.recoveryCoordinator.Stop(ctx)
	}()
	// ESM objectives are user-controlled background work. Do not infer a new
	// execution request from an old persisted "active" row during serve startup:
	// a role may have failed before the process exited, and replaying it here
	// would silently re-run the user's task. Create/Edit/ResumeESM are the
	// explicit execution entry points.
	// Other local entry points (CLI, TUI, ACP) publish only advisory UDP
	// wake-ups after durable state changes. Re-read SQLite before broadcasting so
	// a lost, duplicated, or forged datagram can never change runtime state.
	stopLeaseNotifications := session.SubscribeRuntimeLeaseNotifications(func(notification session.RuntimeLeaseNotification) {
		switch notification.Type {
		case "acquired", "released", "lost", "state_changed":
			if srv.recoveryCoordinator != nil {
				srv.recoveryCoordinator.Wake()
			}
			go srv.PublishExternalSessionUpdate(notification.SessionID)
		}
	})
	defer stopLeaseNotifications()
	defer srv.shutdownESM()

	if opts.OnReady != nil {
		opts.OnReady(srv)
	}

	// Build routes
	mux := http.NewServeMux()
	registerRoutes(mux, srv, opts)

	// Apply middleware stack (inside-out)
	var handler http.Handler = mux
	handler = ConcurrencyMiddleware(gCfg.MaxConcurrentReqs, handler)
	handler = CORSMiddleware(gCfg.CORS, handler)
	handler = LoggingMiddleware(handler)

	// Auth middleware wraps everything except health and the narrow public Web UI
	// bootstrap/login endpoints. Web UI assets must remain reachable so users can
	// enter an auth token; all management APIs and WebSocket streams stay protected.
	authMux := http.NewServeMux()
	authMux.Handle("/health", LoggingMiddleware(http.HandlerFunc(srv.handleHealth)))
	authMux.Handle("/api/auth/login", LoggingMiddleware(WebUILoginHandlerForConfig(srv.authConfig)))
	authMux.Handle("/api/auth/status", LoggingMiddleware(WebUIAuthStatusHandlerForConfig(srv.authConfig)))
	authMux.Handle("/api/auth/logout", LoggingMiddleware(WebUILogoutHandler()))
	authMux.Handle("/", AuthMiddlewareForConfig(srv.authConfig, handler))

	httpServer := &http.Server{
		Addr:         gCfg.GetListenAddr(),
		Handler:      authMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: time.Duration(gCfg.RequestTimeoutSecs+10) * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "MothX Serve API %s starting on %s\n", version, gCfg.GetListenAddr())
		fmt.Fprintf(os.Stderr, "  Provider: %s | Model: %s | Mode: %s\n", p.Name(), model.ID, gCfg.DefaultMode)
		fmt.Fprintf(os.Stderr, "  WorkDir: %s\n", cwd)
		if gCfg.Auth.Enabled {
			fmt.Fprintf(os.Stderr, "  Auth: enabled (%d tokens)\n", len(gCfg.Auth.Tokens))
		} else {
			fmt.Fprintf(os.Stderr, "  Auth: disabled\n")
		}
		if warning := apiSecurityWarning(gCfg); warning != "" {
			fmt.Fprintf(os.Stderr, "  WARNING: %s\n", warning)
		}
		if gCfg.Sandbox.Enabled {
			fmt.Fprintf(os.Stderr, "  Sandbox: enabled (level: %s)\n", gCfg.Sandbox.Level)
		}
		if gCfg.EnableSubAgents {
			fmt.Fprintf(os.Stderr, "  Sub-Agents: enabled\n")
		}
		if gCfg.EnableDelegate {
			fmt.Fprintf(os.Stderr, "  Delegate: enabled\n")
		}
		if gCfg.EnableWorkflows {
			fmt.Fprintf(os.Stderr, "  Workflows: enabled\n")
		}
		if gCfg.EnableWebSearch {
			fmt.Fprintf(os.Stderr, "  Web search: enabled\n")
		}
		if gCfg.EnableBrowser {
			fmt.Fprintf(os.Stderr, "  Browser: enabled\n")
		}
		if gCfg.EnableArtifact {
			fmt.Fprintf(os.Stderr, "  Artifacts: enabled\n")
		}
		if gCfg.EnableA2AMaster {
			fmt.Fprintf(os.Stderr, "  A2A master: enabled\n")
		}
		fmt.Fprintf(os.Stderr, "  Tool visibility: %s | System prompt: %s\n", gCfg.ToolVisibility.Mode, gCfg.SystemPromptMode)
		fmt.Fprintf(os.Stderr, "\nReady to serve.\n")
		errCh <- httpServer.ListenAndServe()
	}()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	case <-opts.Shutdown:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.shutdownESM()
		if err := pool.Shutdown(ctx); err != nil {
			return fmt.Errorf("session shutdown error: %w", err)
		}
		if err := httpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown error: %w", err)
		}
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "\nReceived %s, shutting down...\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.shutdownESM()
		if err := pool.Shutdown(ctx); err != nil {
			return fmt.Errorf("session shutdown error: %w", err)
		}
		if err := httpServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown error: %w", err)
		}
	}

	return nil
}

func loadRunConfig(opts RunOptions) (*Config, error) {
	var cfg *Config
	if opts.Config != nil {
		cfg = cloneConfig(opts.Config)
		normalizeConfig(cfg)
	} else {
		cfg = DefaultConfig()
	}
	applyRunOverrides(cfg, opts)
	return cfg, nil
}

func applyRunOverrides(cfg *Config, opts RunOptions) {
	if cfg == nil {
		return
	}
	if opts.Port != "" {
		cfg.Listen = listenFromPortOverride(opts.Port)
	}
	if opts.Unsafe {
		cfg.ApplyUnsafeAccess()
	}
	if opts.MultiAgent {
		cfg.EnableSubAgents = true
	}
	if opts.Delegate {
		cfg.EnableDelegate = true
	}
	if opts.Workflows {
		cfg.EnableWorkflows = true
	}
	if opts.WebSearch {
		cfg.EnableWebSearch = true
	}
	if opts.Browser {
		cfg.EnableBrowser = true
	}
	if opts.Artifact {
		cfg.EnableArtifact = true
	}
	if opts.A2AMaster {
		cfg.EnableA2AMaster = true
	}
	if opts.Sandbox {
		cfg.Sandbox.Enabled = true
	}
	if opts.WorkDir != "" {
		cfg.DefaultWorkDir = opts.WorkDir
		cfg.WorkingDir = ""
	}
}

func listenFromPortOverride(port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	if strings.HasPrefix(port, ":") || strings.Contains(port, ":") {
		return port
	}
	return ":" + port
}

func registerRoutes(mux *http.ServeMux, srv *Server, opts RunOptions) {
	if !opts.DisableAPI {
		mux.HandleFunc("/v1/chat/completions", srv.handleChatCompletions)
		mux.HandleFunc("/api/runs/", srv.HandleRunAPI)
		mux.HandleFunc("/api/responses/runs/", srv.HandleResponsesRunAPI)
		mux.HandleFunc("/api/attachments/", srv.HandleAttachmentAPI)
		mux.HandleFunc("/api/deliveries/failures", srv.HandleDeliveryFailuresAPI)
		mux.HandleFunc("/api/deliveries/retry", srv.HandleDeliveryRetryAPI)
		mux.HandleFunc("/v1/models", srv.handleModels)
		mux.HandleFunc("/api/models/catalog", srv.handleModelCatalog)
	}
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/api/provider/models", srv.handleProviderModels)
	mux.HandleFunc("/api/provider/test", srv.handleProviderModelTest)
	if opts.ExtraRoutes != nil {
		opts.ExtraRoutes(srv, mux)
	}
}

func buildWorkDirContext(settings *config.Settings, workDir string, workflows bool, browser bool) (*skills.Manager, string, error) {
	if workflows {
		if _, _, err := workflow.EnsureProjectSkill(workDir); err != nil {
			return nil, "", fmt.Errorf("create workflow skill: %w", err)
		}
	}
	skillsMgr := skills.NewManagerWithProjectDirs(settings.GetGlobalSkillsDir(), skills.ProjectSkillDirs(workDir))
	_ = skillsMgr.Load()

	var extraContext string
	if settings.ContextFiles.Enabled {
		cfResult := contextfiles.LoadContextFiles(workDir, config.ConfigDir(), settings.ContextFiles.ExtraFiles)
		if ctx := contextfiles.BuildContextString(cfResult); ctx != "" {
			extraContext = ctx
		}
	}
	extraContext += skillsMgr.BuildAllSkillsContext()
	if workflows {
		extraContext += skillsMgr.BuildSkillContext(workflow.SkillName)
	}
	if browser {
		extraContext += skillsMgr.BuildSkillContext(browserfeature.SkillName)
	}
	return skillsMgr, extraContext, nil
}

// LoggingMiddleware logs each request.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, lw.statusCode, time.Since(start).Round(time.Millisecond))
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.statusCode = code
	lw.ResponseWriter.WriteHeader(code)
}

func (lw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(lw.ResponseWriter).Hijack()
}

// Ensure loggingResponseWriter also satisfies http.Flusher for SSE.
func (lw *loggingResponseWriter) Flush() {
	_ = http.NewResponseController(lw.ResponseWriter).Flush()
}

func apiSecurityWarning(cfg *Config) string {
	if cfg.DefaultMode != "yolo" || (cfg.Auth.Enabled && len(cfg.Auth.Tokens) > 0) {
		return ""
	}
	listen := cfg.Listen
	if strings.HasPrefix(listen, ":") ||
		strings.HasPrefix(listen, "0.0.0.0:") ||
		strings.HasPrefix(listen, "[::]:") {
		return "API is listening beyond loopback in yolo mode without authentication"
	}
	return ""
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message, errType string) {
	safe := strings.TrimSpace(message)
	if status >= http.StatusInternalServerError || strings.EqualFold(strings.TrimSpace(errType), "server_error") {
		safe = ""
	}
	phase := agentruntime.PhaseAdmission
	if status >= http.StatusInternalServerError {
		phase = agentruntime.PhasePersistence
	}
	info := agentruntime.ClassifyError(errors.New(message), agentruntime.ErrorClassificationOptions{
		Type: errType, Message: safe, Phase: phase, HTTPStatus: status,
	})
	writeErrorInfo(w, status, info)
}

func writeErrorInfo(w http.ResponseWriter, status int, info agentruntime.ErrorInfo) {
	if info.Message == "" {
		info.Message = "The request could not be completed."
	}
	message := agentruntime.DisplayErrorMessage(info)
	if info.Type == "" {
		info.Type = "server_error"
	}
	if info.RetryAfterMS > 0 {
		seconds := (info.RetryAfterMS + 999) / 1000
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeJSON(w, status, ErrorResponse{Error: ErrorDetail{
		Message:         message,
		Type:            info.Type,
		Code:            info.Code,
		FailureClass:    string(info.FailureClass),
		Phase:           string(info.Phase),
		MessageKey:      info.MessageKey,
		Detail:          info.Detail,
		RetryMode:       string(info.RetryMode),
		Retryable:       info.Retryable,
		RetryAfterMS:    info.RetryAfterMS,
		Attempt:         info.Attempt,
		MaxAttempts:     info.MaxAttempts,
		SideEffectState: string(info.SideEffectState),
		PartialOutput:   info.PartialOutput,
		RunID:           info.RunID,
		IntentID:        info.IntentID,
		RequestID:       info.RequestID,
	}})
}
