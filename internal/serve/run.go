package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/startvibecoding/mothx/internal/agentruntime"
	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/cron"
	"github.com/startvibecoding/mothx/internal/debugpprof"
	"github.com/startvibecoding/mothx/internal/memory"
	"github.com/startvibecoding/mothx/internal/messaging"
	"github.com/startvibecoding/mothx/internal/messaging/feishu"
	"github.com/startvibecoding/mothx/internal/messaging/wechat"
	channels "github.com/startvibecoding/mothx/internal/serve/channels"
	openaiapi "github.com/startvibecoding/mothx/internal/serve/openaiapi"
	"github.com/startvibecoding/mothx/internal/session"
	"github.com/startvibecoding/mothx/internal/stats"
	webui "github.com/startvibecoding/mothx/ui"
)

type RunOptions struct {
	ConfigPath string
	Port       string
	WebUIDir   string
	Provider   string
	Model      string
	WorkDir    string
	Unsafe     bool
	Sandbox    bool
	MultiAgent bool
	Delegate   bool
	Workflows  bool
	WebSearch  bool
	Browser    bool
	Artifact   bool
	A2AMaster  bool
	Lobster    bool
	Verbose    bool
	Debug      bool
	// OnReady observes the fully constructed API server and channel dispatcher.
	OnReady func(*openaiapi.Server, *channels.Dispatcher)
	// Shutdown requests graceful termination of the nested API runtime.
	Shutdown <-chan struct{}
}
type channelRuntime struct {
	mu                    sync.RWMutex
	cronMu                sync.Mutex
	platformMu            sync.Mutex
	cfg                   *Config
	configState           *ServeConfigState
	version               string
	dispatcher            *channels.Dispatcher
	platforms             *PlatformSupervisor
	wechatLogin           *wechatLoginSession
	logHub                *logHub
	cronStore             cron.CronStore
	cronStorePath         string
	cronScheduler         *cron.Scheduler
	sessionDir            string
	identityMux           *session.IdentityLocks
	nativeDirectoryPicker func(context.Context, string) (string, error)
	deliveryCancel        context.CancelFunc
	deliveryDone          chan struct{}
	// deliveryReopened tracks operations already given a second retry window by
	// this process (reconnect recovery), so a permanently undeliverable target
	// cannot loop across every reconnect.
	deliveryReopenedMu sync.Mutex
	deliveryReopened   map[string]struct{}
}

type channelStatus struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
}

type channelToolSelection struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type channelToolsResponse struct {
	SessionID  string                      `json:"sessionId"`
	Platform   string                      `json:"platform"`
	Generation int64                       `json:"generation"`
	AppliesTo  string                      `json:"appliesTo"`
	Tools      []channels.ChannelToolState `json:"tools"`
}

type activeSessionManager interface {
	ListActiveSessions() []openaiapi.ActiveSessionInfo
	DeleteActiveSession(id string) (bool, error)
	GetSessionMessages(id string) ([]openaiapi.SessionMessageEntry, error)
	GetSessionToolResult(id, toolCallID string) (*openaiapi.SessionToolResultDetail, error)
	GetSessionSubAgents(id string) ([]openaiapi.SessionSubAgentInfo, error)
	GetSessionSubAgentMessages(id, agentID string) ([]openaiapi.SessionMessageEntry, error)
	GetSessionRunEvents(id string) ([]openaiapi.SessionRunEventEntry, error)
	GetSessionCapabilityEvents(id string) ([]openaiapi.SessionCapabilityEventEntry, error)
	CapabilityOverview() openaiapi.CapabilityOverview
	GetSessionCapabilities(id string) (*openaiapi.SessionCapabilities, error)
	PatchSessionCapabilities(id string, patch openaiapi.SessionCapabilityPatch) (*openaiapi.SessionCapabilities, error)
	GetSessionRuntime(id string) (*openaiapi.SessionRuntimeSnapshot, error)
	PatchSessionRuntime(id string, patch openaiapi.SessionRuntimePatch) (*openaiapi.SessionRuntimeSnapshot, error)
	ListSessionRuns(id string, limit int) ([]session.SessionRun, error)
}

type featureStatus struct {
	WebUI      bool `json:"webUI"`
	OpenAIAPI  bool `json:"openaiAPI"`
	Wechat     bool `json:"wechat"`
	Feishu     bool `json:"feishu"`
	MultiAgent bool `json:"multiAgent"`
	Delegate   bool `json:"delegate"`
	WebSearch  bool `json:"webSearch"`
	Browser    bool `json:"browser"`
	A2AMaster  bool `json:"a2aMaster"`
	Workflows  bool `json:"workflows"`
	Cron       bool `json:"cron"`
	Memory     bool `json:"memory"`
}

type serveStatus struct {
	Status   string          `json:"status"`
	Listen   string          `json:"listen"`
	Features featureStatus   `json:"features"`
	WebUI    WebUIConfig     `json:"webUI"`
	Channels []channelStatus `json:"channels"`
	Sessions int             `json:"sessions"`
}

func registerServeRoutes(mux *http.ServeMux, rt *channelRuntime, configPath string) {
	if rt == nil {
		return
	}
	rt.routes(configPath)(nil, mux)
}

func Run(opts RunOptions, version string) error {
	config.Verbose = opts.Verbose || opts.Debug
	if opts.Debug {
		_ = os.Setenv("VIBECODING_DEBUG", "1")
		debugpprof.StartForDebug(os.Stderr)
	}

	configState, err := loadServeConfigState(opts)
	if err != nil {
		return err
	}
	cfg := configState.Effective
	path := configState.WritablePath

	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if cfg.API.EnableWebSearch {
		settings.WebSearch.Enabled = config.BoolPtr(true)
	}

	fmt.Fprintf(os.Stderr, "MothX Serve %s starting\n", version)
	displayAddr := displayListenAddr(cfg.API.GetListenAddr())
	if cfg.Features.OpenAIAPI {
		fmt.Fprintf(os.Stderr, "  OpenAI API: http://%s/v1/chat/completions\n", displayAddr)
	} else {
		fmt.Fprintf(os.Stderr, "  OpenAI API: disabled\n")
	}
	if cfg.WebUI.Enabled {
		fmt.Fprintf(os.Stderr, "  Web UI: http://%s/\n", displayAddr)
	} else {
		fmt.Fprintf(os.Stderr, "  Web UI: disabled\n")
	}

	logHub := newLogHub()
	restoreLogs := installLogHub(logHub)
	defer restoreLogs()
	stopUDPLogs := session.SubscribeRuntimeLeaseLogs(func(message string) {
		logHub.publish(serveLogEvent{Type: "log", Message: message, Timestamp: time.Now()})
	})
	defer stopUDPLogs()

	rt, err := startChannels(cfg, settings, version)
	if err != nil {
		return err
	}
	rt.setConfigState(configState)
	rt.logHub = logHub
	defer rt.stop()

	if cfg.LobsterMode {
		fmt.Fprintf(os.Stderr, "  Lobster mode: enabled (yolo, no sandbox, sub-agents on)\n")
	}
	fmt.Fprintf(os.Stderr, "  Config: %s\n", path)
	// A generated template token is publicly known, so an exposed server would be
	// reachable by anyone; warn until the operator replaces it.
	if UsesPlaceholderAuthToken(cfg) {
		fmt.Fprintln(os.Stderr, PlaceholderAuthTokenWarning)
	}

	return openaiapi.Run(openaiapi.RunOptions{
		Config:        &cfg.API,
		DisableAPI:    !cfg.Features.OpenAIAPI,
		Provider:      opts.Provider,
		Model:         opts.Model,
		WorkDir:       opts.WorkDir,
		Unsafe:        opts.Unsafe,
		Sandbox:       opts.Sandbox,
		MultiAgent:    opts.MultiAgent,
		Delegate:      opts.Delegate,
		Workflows:     opts.Workflows,
		WebSearch:     opts.WebSearch,
		Browser:       opts.Browser,
		Artifact:      opts.Artifact,
		A2AMaster:     opts.A2AMaster,
		CronStore:     rt.cronSnapshot(),
		CronScheduler: rt.cronSchedulerSnapshot(),
		Verbose:       opts.Verbose,
		Debug:         opts.Debug,
		Shutdown:      opts.Shutdown,
		ExtraRoutes:   rt.routes(path),
		OnReady: func(api *openaiapi.Server) {
			rt.configureAPI(api)
			// API construction completes shared durable-run recovery before this
			// callback. Start transports only after that recovery so their first
			// inbound delivery cannot collide with a stale local Run.
			rt.startPlatforms()
			if opts.OnReady != nil {
				opts.OnReady(api, rt.dispatcher)
			}
			api.SetRunCompleteObserver(func(sessionID, runID, status, errMsg string) {
				response := ""
				if openaiapi.IsSuccessfulRunStatus(status) {
					if messages, err := api.GetSessionMessages(sessionID); err == nil {
						for i := len(messages) - 1; i >= 0; i-- {
							if messages[i].Role == "assistant" && strings.TrimSpace(messages[i].Content) != "" {
								response = messages[i].Content
								break
							}
						}
					}
				}
				if !openaiapi.IsSuccessfulRunStatus(status) && strings.TrimSpace(errMsg) == "" {
					if events, err := api.GetSessionRunEvents(sessionID); err == nil {
						for i := len(events) - 1; i >= 0; i-- {
							if events[i].RunID != runID || events[i].Data == nil {
								continue
							}
							if message, ok := events[i].Data["error"].(string); ok && strings.TrimSpace(message) != "" {
								errMsg = message
								break
							}
						}
					}
				}
				rt.pushBoundSessionResult(sessionID, response, errorFromRun(status, errMsg))
			})
		},
	}, version)
}

func (rt *channelRuntime) configureAPI(api *openaiapi.Server) {
	if rt == nil || api == nil || rt.dispatcher == nil {
		return
	}
	rt.dispatcher.SetRunObserver(api.PublishExternalSessionUpdate)
	rt.dispatcher.SetSubAgentObserver(api.PublishExternalSubAgentEvent)
	rt.dispatcher.SetBackgroundSubmitter(api.SubmitExternalResponsesBackground)
}

func (rt *channelRuntime) cronSnapshot() cron.CronStore {
	if rt == nil {
		return nil
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	return rt.cronStore
}

func (rt *channelRuntime) cronSchedulerSnapshot() *cron.Scheduler {
	if rt == nil {
		return nil
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	return rt.cronScheduler
}

func displayListenAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

func loadRunConfig(path string) (*Config, string, error) {
	state, err := loadServeConfigState(RunOptions{ConfigPath: path})
	if err != nil {
		return nil, "", err
	}
	return state.Effective, state.WritablePath, nil
}

func applyOverrides(cfg *Config, opts RunOptions) {
	if opts.Port != "" {
		cfg.API.Listen = listenFromPortOverride(opts.Port)
	}
	if opts.WebUIDir != "" {
		webUIDir := opts.WebUIDir
		if useEmbeddedWebUI(webUIDir) {
			if abs, err := filepath.Abs(webUIDir); err == nil {
				webUIDir = abs
			}
		}
		cfg.WebUI.Dir = webUIDir
		cfg.WebUI.Enabled = true
		cfg.Features.WebUI = true
	}
	if opts.WorkDir != "" {
		cfg.API.DefaultWorkDir = opts.WorkDir
		cfg.API.WorkingDir = ""
	}
	if opts.Unsafe {
		cfg.API.ApplyUnsafeAccess()
	}
	if opts.Provider != "" {
		cfg.API.Provider = opts.Provider
	}
	if opts.Model != "" {
		cfg.API.Model = opts.Model
	}
	if opts.Sandbox {
		cfg.API.Sandbox.Enabled = true
	}
	if opts.MultiAgent {
		cfg.API.EnableSubAgents = true
		cfg.Features.MultiAgent = true
	}
	if opts.Delegate {
		cfg.API.EnableDelegate = true
	}
	if opts.Workflows {
		cfg.API.EnableWorkflows = true
	}
	if opts.WebSearch {
		cfg.API.EnableWebSearch = true
	}
	if opts.Artifact {
		cfg.API.EnableArtifact = true
	}
	if opts.Browser {
		cfg.API.EnableBrowser = true
	}
	if opts.A2AMaster {
		cfg.API.EnableA2AMaster = true
	}
	if opts.Lobster {
		cfg.LobsterMode = true
	}
	normalize(cfg)
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

func applyRuntimeFeatures(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.WebUI.Enabled = cfg.Features.WebUI
	cfg.API.EnableSubAgents = cfg.Features.MultiAgent
	cfg.Channels.Wechat.Enabled = cfg.Features.Wechat
	cfg.Channels.Feishu.Enabled = cfg.Features.Feishu
	cfg.Cron.Enabled = cfg.Features.Cron
	cfg.Memory.Enabled = cfg.Features.Memory
}

func startChannels(cfg *Config, settings *config.Settings, version string) (*channelRuntime, error) {
	applyRuntimeFeatures(cfg)

	hCfg := buildConfigFromServeConfig(cfg)
	cronStore := buildCronStore(hCfg, settings)

	dispatcher, err := channels.NewDispatcher(hCfg, settings, version, cronStore, nil)
	if err != nil {
		return nil, fmt.Errorf("create channel dispatcher: %w", err)
	}
	identityMux := session.NewIdentityLocks()
	dispatcher.SetIdentityLocks(identityMux)
	deliveryCtx, deliveryCancel := context.WithCancel(context.Background())
	rt := &channelRuntime{cfg: cfg, version: version, dispatcher: dispatcher, platforms: NewPlatformSupervisor(), cronStore: cronStore, cronStorePath: cronStorePath(settings), sessionDir: settings.GetSessionDir(), identityMux: identityMux, deliveryCancel: deliveryCancel, deliveryDone: make(chan struct{})}
	dispatcher.SetRotateHandler(func(platform, userID string, force bool) error {
		lifecycle := NewSessionLifecycleService(nil, dispatcher, rt.sessionDir, identityMux)
		lifecycle.SetEventPublisher(rt.publishManagementEvent)
		return lifecycle.Rotate(context.Background(), platform, userID, force)
	})
	rt.setupCronScheduler(hCfg)
	go rt.runDeliveryRecovery(deliveryCtx)
	return rt, nil
}

func buildConfigFromServeConfig(cfg *Config) *channels.Config {
	hCfg := channels.DefaultConfig()
	if cfg == nil {
		return hCfg
	}
	applyRuntimeFeatures(cfg)
	hCfg.DefaultProvider = cfg.API.Provider
	hCfg.DefaultModel = cfg.API.Model
	hCfg.MultiAgent = cfg.API.EnableSubAgents
	hCfg.Sandbox = cfg.API.Sandbox.Enabled
	hCfg.WebSearch = cfg.API.EnableWebSearch
	hCfg.Browser = cfg.API.EnableBrowser
	hCfg.Artifact = cfg.Channels.Artifact
	hCfg.A2AMaster = cfg.API.EnableA2AMaster
	hCfg.WorkDir = cfg.API.GetWorkDir()
	hCfg.Wechat = cfg.Channels.Wechat
	hCfg.Feishu = cfg.Channels.Feishu
	hCfg.Cron = cfg.Cron
	hCfg.Memory = cfg.Memory
	hCfg.Security = cfg.Security
	hCfg.Hooks = cfg.Hooks
	hCfg.Agent = cfg.Agent
	return hCfg
}

func (rt *channelRuntime) configSnapshot() *Config {
	if rt == nil {
		return nil
	}
	rt.mu.RLock()
	cfg := rt.cfg
	rt.mu.RUnlock()
	return cloneServeConfig(cfg)
}

func (rt *channelRuntime) setConfig(cfg *Config) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.cfg = cfg
	rt.mu.Unlock()
}

func (rt *channelRuntime) configStateSnapshot(path string, layer ConfigLayer) *ServeConfigState {
	if rt == nil {
		return nil
	}
	rt.mu.RLock()
	state := rt.configState
	rt.mu.RUnlock()
	if state != nil {
		return state
	}
	state = &ServeConfigState{Effective: rt.configSnapshot(), WritablePath: path, WritableLayer: layer}
	rt.mu.Lock()
	if rt.configState == nil {
		rt.configState = state
	} else {
		state = rt.configState
	}
	rt.mu.Unlock()
	return state
}

func (rt *channelRuntime) setConfigState(state *ServeConfigState) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.configState = state
	rt.mu.Unlock()
}

func (rt *channelRuntime) currentWechatLogin() *wechatLoginSession {
	if rt == nil {
		return nil
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.wechatLogin
}

func (rt *channelRuntime) setWechatLogin(sess *wechatLoginSession) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.wechatLogin = sess
	rt.mu.Unlock()
}

func buildCronStore(hCfg *channels.Config, settings *config.Settings) cron.CronStore {
	if hCfg != nil && hCfg.Cron.Enabled {
		sessionDir := ""
		if settings != nil {
			sessionDir = settings.GetSessionDir()
		}
		return cron.NewSQLiteCronStore(sessionDir)
	}
	return nil
}

func errorFromRun(status, errMsg string) error {
	if status == "completed" || status == "incomplete" {
		return nil
	}
	message := strings.TrimSpace(errMsg)
	if message == "" {
		message = status
	}
	if message == "" {
		message = "run failed"
	}
	return errors.New(message)
}

// pushBoundSessionResult mirrors runs initiated outside a messaging channel
// back to the platform currently bound to that session.
func (rt *channelRuntime) pushBoundSessionResult(sessionID, response string, runErr error) {
	if rt == nil || sessionID == "" {
		return
	}
	binding, err := session.FindBindingBySessionID(rt.sessionDir, sessionID)
	if err != nil {
		log.Printf("[serve] find channel binding for session %s: %v", sessionID, err)
		return
	}
	if binding == nil || (binding.ChannelType != "wechat" && binding.ChannelType != "feishu") {
		return
	}
	var platform messaging.Platform
	for _, candidate := range rt.platformSnapshot() {
		if candidate != nil && strings.EqualFold(candidate.Name(), binding.ChannelType) {
			platform = candidate
			break
		}
	}
	if platform == nil {
		log.Printf("[serve] no %s platform for bound session %s", binding.ChannelType, sessionID)
		return
	}
	text := strings.TrimSpace(response)
	if runErr != nil {
		errorText := "Error: " + strings.TrimSpace(runErr.Error())
		if text == "" {
			text = errorText
		} else {
			text += "\n\n" + errorText
		}
	}
	if text == "" {
		return
	}
	if err := platform.SendMessage(context.Background(), binding.ChannelID, text); err != nil {
		log.Printf("[serve] send %s result for session %s: %v", binding.ChannelType, sessionID, err)
	}
}

func cronStorePath(settings *config.Settings) string {
	sessionDir := ""
	if settings != nil {
		sessionDir = settings.GetSessionDir()
	}
	return filepath.Join(sessionDir, "sessions.db")
}

func (rt *channelRuntime) setupCronScheduler(hCfg *channels.Config) {
	if hCfg == nil || !hCfg.Cron.Enabled {
		fmt.Fprintf(os.Stderr, "  Cron: disabled\n")
		return
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	if rt.cronStore == nil || rt.dispatcher == nil || rt.dispatcher.EnsureAgentManager() == nil {
		fmt.Fprintf(os.Stderr, "  Cron: disabled (agent runtime unavailable)\n")
		return
	}
	interval := time.Duration(hCfg.Cron.Interval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	rt.cronScheduler = cron.NewSchedulerWithSessionDir(rt.cronStore, rt.dispatcher.AgentManager(), interval, rt.sessionDir)
	rt.cronScheduler.SetCompletionObserver(rt.pushBoundSessionResult)
	rt.dispatcher.SetCronScheduler(rt.cronScheduler)
	rt.cronScheduler.Start()
	fmt.Fprintf(os.Stderr, "  Cron: enabled\n")
}

func (rt *channelRuntime) applyConfigUpdate(next *Config) error {
	if rt == nil {
		return nil
	}
	applyRuntimeFeatures(next)
	previous := rt.configSnapshot()
	if rt.dispatcher != nil {
		if err := rt.dispatcher.ApplyConfig(buildConfigFromServeConfig(next)); err != nil {
			return fmt.Errorf("apply channel dispatcher config: %w", err)
		}
	}
	rt.setConfig(next)
	rt.syncCronRuntime()
	if err := rt.syncPlatformRuntime(previous, next); err != nil {
		// A platform candidate can fail after the dispatcher accepted the
		// candidate config. Restore the previous runtime snapshot before the
		// config-state transaction restores the writable file.
		if previous != nil {
			if rt.dispatcher != nil {
				if rollbackErr := rt.dispatcher.ApplyConfig(buildConfigFromServeConfig(previous)); rollbackErr != nil {
					log.Printf("[serve] rollback dispatcher config after platform failure: %v", rollbackErr)
				}
			}
			rt.setConfig(previous)
			rt.syncCronRuntime()
		}
		return err
	}
	return nil
}

func (rt *channelRuntime) syncCronRuntime() {
	if rt == nil {
		return
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	if !rt.cronEnabled() {
		rt.stopCronSchedulerLocked()
		rt.cronStore = nil
		rt.cronStorePath = ""
		if rt.dispatcher != nil {
			rt.dispatcher.SetCronStore(nil)
		}
		return
	}

	hCfg := buildConfigFromServeConfig(rt.configSnapshot())
	nextPath := filepath.Join(rt.sessionDir, "sessions.db")
	if rt.cronStore == nil || rt.cronStorePath != nextPath {
		rt.stopCronSchedulerLocked()
		rt.cronStorePath = nextPath
		rt.cronStore = cron.NewSQLiteCronStore(rt.sessionDir)
	}
	if rt.dispatcher != nil {
		rt.dispatcher.SetCronStore(rt.cronStore)
	}

	if rt.cronStore == nil || rt.dispatcher == nil || rt.dispatcher.EnsureAgentManager() == nil {
		rt.stopCronSchedulerLocked()
		return
	}
	if rt.cronScheduler == nil || !rt.cronScheduler.IsRunning() {
		interval := time.Duration(hCfg.Cron.Interval) * time.Second
		if interval <= 0 {
			interval = 30 * time.Second
		}
		rt.cronScheduler = cron.NewSchedulerWithSessionDir(rt.cronStore, rt.dispatcher.AgentManager(), interval, rt.sessionDir)
		rt.cronScheduler.SetCompletionObserver(rt.pushBoundSessionResult)
		rt.dispatcher.SetCronScheduler(rt.cronScheduler)
		rt.cronScheduler.Start()
	}
}

func (rt *channelRuntime) stopCronScheduler() {
	if rt == nil {
		return
	}
	rt.cronMu.Lock()
	defer rt.cronMu.Unlock()
	rt.stopCronSchedulerLocked()
}

func (rt *channelRuntime) stopCronSchedulerLocked() {
	if rt.cronScheduler != nil {
		rt.cronScheduler.Stop()
		rt.cronScheduler = nil
	}
	if rt.dispatcher != nil {
		rt.dispatcher.SetCronScheduler(nil)
	}
}

func (rt *channelRuntime) startPlatforms() {
	cfg := rt.configSnapshot()
	for _, name := range []string{"wechat", "feishu"} {
		if err := rt.restartPlatform(name, cfg); err != nil {
			log.Printf("[serve] %s startup unavailable: %v", name, err)
		}
	}
}

func (rt *channelRuntime) restartPlatform(name string, cfg *Config) error {
	rt.platformMu.Lock()
	rt.publishManagementEvent("channel_status_changed", map[string]any{"platform": name, "state": "starting"})
	var next messaging.Platform
	configured := false
	var candidateErr error
	if cfg != nil {
		switch name {
		case "wechat":
			if cfg.Channels.Wechat.Enabled {
				configured = true
				credPath := cfg.Channels.Wechat.CredPath
				if credPath == "" {
					credPath = filepath.Join(config.ConfigDir(), "wechat-credentials.json")
				}
				if creds, err := wechat.LoadCredentials(credPath); err != nil || creds == nil {
					fmt.Fprintf(os.Stderr, "  WeChat: enabled but not logged in\n")
					if err != nil {
						candidateErr = fmt.Errorf("load wechat credentials: %w", err)
					}
				} else {
					bot := wechat.NewBot(wechat.BotOptions{CredPath: credPath, AutoTyping: cfg.Channels.Wechat.AutoTyping})
					bot.SetStatusCallback(func(bool) { rt.publishChannelStatus() })
					next = bot
				}
			} else {
				fmt.Fprintf(os.Stderr, "  WeChat: disabled\n")
			}
		case "feishu":
			if cfg.Channels.Feishu.Enabled {
				configured = true
				if cfg.Channels.Feishu.AppID == "" || cfg.Channels.Feishu.AppSecret == "" {
					fmt.Fprintf(os.Stderr, "  Feishu: enabled but app_id/app_secret not configured\n")
				} else {
					bot := feishu.NewBot(feishu.BotOptions{AppID: cfg.Channels.Feishu.AppID, AppSecret: cfg.Channels.Feishu.AppSecret})
					bot.SetStatusCallback(func(bool) { rt.publishChannelStatus() })
					next = bot
				}
			} else {
				fmt.Fprintf(os.Stderr, "  Feishu: disabled\n")
			}
		}
	}

	previous := rt.platforms.Get(name)
	// Invalid credentials/configuration are a failed candidate, not a request
	// to tear down a healthy existing instance.
	if configured && next == nil {
		rt.platformMu.Unlock()
		return candidateErr
	}
	if next == nil {
		rt.platforms.Replace(name, nil)
		rt.platformMu.Unlock()
		if previous != nil {
			_ = previous.Stop()
		}
		return nil
	}
	if previous == nil {
		rt.platforms.Replace(name, next)
		rt.platformMu.Unlock()
		go rt.runPlatform(next)
		return nil
	}
	// Keep the healthy owner in the supervisor while the replacement performs
	// its startup handshake. A candidate that fails before readiness therefore
	// cannot interrupt the old receive loop.
	rt.platformMu.Unlock()
	return rt.startPlatformCandidate(name, next, previous)
}

func (rt *channelRuntime) startPlatformCandidate(name string, candidate, previous messaging.Platform) error {
	if rt == nil || candidate == nil {
		return fmt.Errorf("platform candidate is required")
	}
	done := make(chan error, 1)
	go func() { done <- candidate.Start(context.Background(), rt.dispatcher.HandleDelivery) }()
	ready, hasReadiness := candidate.(messaging.Readiness)
	if !hasReadiness {
		// Third-party transports may not expose readiness; preserve the legacy
		// behavior for them while built-in transports use the guarded path.
		rt.platformMu.Lock()
		promoted := rt.platforms.ReplaceIf(name, previous, candidate)
		rt.platformMu.Unlock()
		if !promoted {
			_ = candidate.Stop()
			return nil
		}
		_ = previous.Stop()
		go rt.finishPlatform(candidate, done, previous)
		return nil
	}
	select {
	case startupErr := <-ready.Ready():
		if startupErr != nil {
			<-done
			rt.publishManagementEvent("channel_status_changed", map[string]any{
				"platform": name, "state": "rollback", "error": startupErr.Error(),
			})
			return startupErr
		}
		rt.platformMu.Lock()
		promoted := rt.platforms.ReplaceIf(name, previous, candidate)
		rt.platformMu.Unlock()
		if !promoted {
			_ = candidate.Stop()
			return nil
		}
		_ = previous.Stop()
		go rt.finishPlatform(candidate, done, previous)
		return nil
	case startupErr := <-done:
		if startupErr != nil {
			rt.publishManagementEvent("channel_status_changed", map[string]any{
				"platform": name, "state": "rollback", "error": startupErr.Error(),
			})
			return startupErr
		}
	}
	return nil
}

func (rt *channelRuntime) finishPlatform(platform messaging.Platform, done <-chan error, fallback messaging.Platform) {
	if rt == nil || platform == nil {
		return
	}
	err := <-done
	rt.platformMu.Lock()
	removed := rt.platforms.RemoveIf(platform.Name(), platform)
	if removed && err != nil && fallback != nil {
		rt.platforms.Replace(platform.Name(), fallback)
	}
	rt.platformMu.Unlock()
	if removed {
		_ = platform.Stop()
		if err != nil && fallback != nil {
			go rt.runPlatform(fallback)
		}
	}
	state := "disconnected"
	if err != nil && fallback != nil {
		state = "rollback"
	}
	rt.publishManagementEvent("channel_status_changed", map[string]any{
		"platform": platform.Name(), "state": state, "error": errorString(err),
	})
	rt.publishChannelStatus()
}

func (rt *channelRuntime) runPlatform(p messaging.Platform, fallback ...messaging.Platform) {
	err := p.Start(context.Background(), rt.dispatcher.HandleDelivery)
	if err != nil {
		log.Printf("[serve] %s stopped: %v", p.Name(), err)
	}
	rt.platformMu.Lock()
	removed := rt.platforms.RemoveIf(p.Name(), p)
	if removed && err != nil && len(fallback) > 0 && fallback[0] != nil {
		rt.platforms.Replace(p.Name(), fallback[0])
	}
	rt.platformMu.Unlock()
	if removed {
		_ = p.Stop()
		if err != nil && len(fallback) > 0 && fallback[0] != nil {
			go rt.runPlatform(fallback[0])
		}
	}
	state := "disconnected"
	if err != nil && len(fallback) > 0 && fallback[0] != nil {
		state = "rollback"
	}
	rt.publishManagementEvent("channel_status_changed", map[string]any{
		"platform": p.Name(), "state": state, "error": errorString(err),
	})
	rt.publishChannelStatus()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (rt *channelRuntime) platformSnapshot() []messaging.Platform {
	if rt == nil {
		return nil
	}
	return rt.platforms.Snapshot()
}

func (rt *channelRuntime) publishChannelStatus() {
	if rt.logHub == nil {
		return
	}
	status := serveStatus{
		Status:   "ok",
		Channels: rt.channelStatuses(),
	}
	if cfg := rt.configSnapshot(); cfg != nil {
		status.Features = featureStatusFromConfig(cfg)
	}
	rt.logHub.publish(serveLogEvent{
		Type: "channel_status_changed", Timestamp: time.Now(), Status: &status,
		Data: map[string]any{"state": "updated"},
	})
}

func (rt *channelRuntime) stop() {
	rt.stopCronScheduler()
	if rt.deliveryCancel != nil {
		rt.deliveryCancel()
		if rt.deliveryDone != nil {
			<-rt.deliveryDone
		}
	}
	_ = rt.platforms.StopAll()
	if rt.dispatcher != nil {
		rt.dispatcher.Close()
	}
	rt.publishManagementEvent("channel_status_changed", map[string]any{"state": "stopped"})
}

func (rt *channelRuntime) publishManagementEvent(eventType string, data any) {
	if rt == nil || rt.logHub == nil {
		return
	}
	rt.logHub.publish(serveLogEvent{Type: eventType, Timestamp: time.Now(), Data: data})
}

func (rt *channelRuntime) routes(configPath string) func(*openaiapi.Server, *http.ServeMux) {
	return func(srv *openaiapi.Server, mux *http.ServeMux) {
		sessions := activeSessionManagerFromAPI(srv)
		mux.HandleFunc("/api/status", rt.handleStatus(sessions))
		mux.HandleFunc("/api/serve/config", rt.handleServeConfig(configPath, srv))
		mux.HandleFunc("/api/serve/config/channels/", rt.handleChannelConfigPatch(configPath, srv))
		mux.HandleFunc("/api/capabilities", rt.handleCapabilities(sessions))
		mux.HandleFunc("/api/session-id", rt.handleSessionID(sessions))
		mux.HandleFunc("/api/sessions", rt.handleSessions(sessions))
		mux.HandleFunc("/api/sessions/", rt.handleSessionByID(sessions))
		mux.HandleFunc("/api/experts", rt.handleExperts(sessions))
		mux.HandleFunc("/api/experts/", rt.handleExperts(sessions))
		mux.HandleFunc("/api/knowledge-bases", rt.handleKnowledgeBases)
		mux.HandleFunc("/api/knowledge-bases/", rt.handleKnowledgeBases)
		mux.HandleFunc("/api/projects", rt.handleProjects)
		mux.HandleFunc("/api/projects/", rt.handleProjectByID)
		mux.HandleFunc("/api/stats/", rt.handleStats(srv.SessionDir()))
		mux.HandleFunc("/api/settings", rt.handleSettings(srv))
		mux.HandleFunc("/api/mcp", rt.handleMCPConfig)
		mux.HandleFunc("/api/env", rt.handleEnv)
		mux.HandleFunc("/api/memory", rt.handleMemory)
		mux.HandleFunc("/api/cron", rt.handleCron)
		mux.HandleFunc("/api/cron/", rt.handleCronByID)
		mux.HandleFunc("/api/channels", rt.handleChannels)
		mux.HandleFunc("/api/session-tools/catalog", rt.handleSessionToolCatalog())
		mux.HandleFunc("/api/session-bindings", rt.handleSessionBindings())
		mux.HandleFunc("/api/channels/wechat/login/qr", rt.handleWechatLoginQR)
		mux.HandleFunc("/api/channels/wechat/login", rt.handleWechatLogin(configPath))
		mux.Handle("/ws/runs", srv.RunWebSocketHandler())
		mux.Handle("/ws/logs", rt.handleLogs(sessions))
		mux.HandleFunc("/api/browse", rt.handleBrowse)
		mux.HandleFunc("/api/select-directory", rt.handleSelectDirectory)
		mux.HandleFunc("/api/skillhub/", rt.handleSkillHub(srv))
		mux.HandleFunc("/", rt.handleWebUI)
	}
}

func activeSessionManagerFromAPI(srv *openaiapi.Server) activeSessionManager {
	if srv == nil {
		return nil
	}
	return srv
}

func (rt *channelRuntime) handleStats(sessionDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		endpoint := strings.TrimPrefix(r.URL.Path, "/api/stats/")
		if endpoint == "" {
			endpoint = "summary"
		}
		if sessionDir == "" {
			rt.writeEmptyStatsResponse(w, endpoint)
			return
		}
		dbPath := filepath.Join(sessionDir, "sessions.db")
		if _, err := os.Stat(dbPath); err != nil {
			if os.IsNotExist(err) {
				rt.writeEmptyStatsResponse(w, endpoint)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		db, err := stats.Open(dbPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer db.Close()

		q := stats.ParseQueryParams(r.URL.Query())
		switch endpoint {
		case "summary":
			summary, err := db.Summary(q)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, summary)
		case "timeseries":
			data, err := db.TimeSeries(q)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, data)
		case "by-provider":
			data, err := db.ByProvider(q)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, data)
		case "by-model":
			data, err := db.ByModel(q)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, data)
		case "recent":
			page := parsePositiveInt(r.URL.Query().Get("page"), 1)
			pageSize := parsePositiveInt(r.URL.Query().Get("pageSize"), 20)
			data, err := db.RecentFiltered(q, page, pageSize)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, data)
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown stats endpoint"})
		}
	}
}

func (rt *channelRuntime) writeEmptyStatsResponse(w http.ResponseWriter, endpoint string) {
	switch endpoint {
	case "summary", "":
		writeJSON(w, http.StatusOK, stats.Summary{})
	case "timeseries", "by-provider", "by-model":
		writeJSON(w, http.StatusOK, []stats.Aggregate{})
	case "recent":
		writeJSON(w, http.StatusOK, stats.RecentPage{Items: []stats.StatsEntry{}, Page: 1, PageSize: 20})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown stats endpoint"})
	}
}

func parsePositiveInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func (rt *channelRuntime) handleServeConfig(path string, servers ...*openaiapi.Server) http.HandlerFunc {
	var srv *openaiapi.Server
	if len(servers) > 0 {
		srv = servers[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, rt.configSnapshot())
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			state := rt.configStateSnapshot(path, ConfigLayerExplicit)
			previous := rt.configSnapshot()
			next, err := state.UpdateFull(body, func(candidate *Config) error {
				if err := rt.applyConfigUpdate(candidate); err != nil {
					return err
				}
				if srv != nil {
					if err := srv.ApplyServeConfig(&candidate.API); err != nil {
						_ = rt.applyConfigUpdate(previous)
						return err
					}
				}
				return nil
			})
			if err != nil {
				status := http.StatusInternalServerError
				if strings.HasPrefix(err.Error(), "decode ") {
					status = http.StatusBadRequest
				}
				writeJSON(w, status, map[string]string{"error": err.Error()})
				return
			}
			if rt.logHub != nil {
				status := rt.statusSnapshot(srv)
				rt.logHub.publish(serveLogEvent{
					Type: "config_changed", Timestamp: time.Now(), Status: &status,
					Data: map[string]any{"scope": "serve"},
				})
			}
			writeJSON(w, http.StatusOK, next)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (rt *channelRuntime) handleChannelConfigPatch(fallbackPath string, srv *openaiapi.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		platform := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/serve/config/channels/"), "/")
		if platform != "wechat" && platform != "feishu" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel must be wechat or feishu"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		path := fallbackPath
		layer := ConfigLayerExplicit
		state := rt.configStateSnapshot(path, layer)
		if state != nil {
			path = state.WritablePath
			layer = state.WritableLayer
		}
		old := rt.configSnapshot()
		result, err := state.UpdateChannel(platform, body, rt.applyConfigUpdate)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.HasPrefix(err.Error(), "decode ") || strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "must be") {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		result.Layer = layer
		result.Path = path
		result.Restart = map[string]any{"platform": platform, "required": platformTransportChanged(old, rt.configSnapshot(), platform)}
		if rt.logHub != nil {
			status := rt.statusSnapshot(srv)
			rt.logHub.publish(serveLogEvent{
				Type: "channel_config_changed", Timestamp: time.Now(), Status: &status,
				Data: map[string]any{
					"platform": platform, "layer": result.Layer, "path": result.Path,
					"restart": result.Restart,
				},
			})
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func effectiveChannelConfig(cfg *Config, platform string) any {
	if cfg == nil {
		return nil
	}
	if platform == "wechat" {
		return cfg.Channels.Wechat
	}
	return cfg.Channels.Feishu
}

func platformTransportChanged(old, next *Config, platform string) bool {
	if old == nil || next == nil {
		return true
	}
	if platform == "wechat" {
		return old.Channels.Wechat.Enabled != next.Channels.Wechat.Enabled ||
			old.Channels.Wechat.CredPath != next.Channels.Wechat.CredPath ||
			old.Channels.Wechat.AutoTyping != next.Channels.Wechat.AutoTyping
	}
	return old.Channels.Feishu.Enabled != next.Channels.Feishu.Enabled ||
		old.Channels.Feishu.AppID != next.Channels.Feishu.AppID ||
		old.Channels.Feishu.AppSecret != next.Channels.Feishu.AppSecret
}

func (rt *channelRuntime) handleStatus(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, rt.statusSnapshot(sessions))
	}
}

func (rt *channelRuntime) statusSnapshot(sessions activeSessionManager) serveStatus {
	sessionCount := 0
	if sessions != nil {
		// Use CountAll instead of loading all sessions to avoid expensive ListActiveSessions.
		if srv, ok := sessions.(interface{ SessionDir() string }); ok {
			if dir := srv.SessionDir(); dir != "" {
				if count, err := session.CountAll(dir); err == nil {
					sessionCount = count
				}
			}
		}
		if sessionCount == 0 {
			sessionCount = len(sessions.ListActiveSessions())
		}
	}
	status := serveStatus{
		Status:   "ok",
		Channels: rt.channelStatuses(),
		Sessions: sessionCount,
	}
	if cfg := rt.configSnapshot(); cfg != nil {
		status.Listen = cfg.API.GetListenAddr()
		status.Features = featureStatusFromConfig(cfg)
		status.WebUI = cfg.WebUI
	}
	// Reflect app-level settings.json webSearch availability so the WebUI tool
	// toggle appears even when the serve config flag is not explicitly set.
	if srv, ok := sessions.(interface{ IsWebSearchAvailable() bool }); ok && srv.IsWebSearchAvailable() {
		status.Features.WebSearch = true
	}
	return status
}

func featureStatusFromConfig(cfg *Config) featureStatus {
	if cfg == nil {
		return featureStatus{}
	}
	return featureStatus{
		WebUI:      cfg.Features.WebUI,
		OpenAIAPI:  cfg.Features.OpenAIAPI,
		Wechat:     cfg.Features.Wechat,
		Feishu:     cfg.Features.Feishu,
		MultiAgent: cfg.Features.MultiAgent,
		Delegate:   cfg.API.EnableDelegate,
		WebSearch:  cfg.API.EnableWebSearch,
		Browser:    cfg.API.EnableBrowser,
		A2AMaster:  cfg.API.EnableA2AMaster,
		Workflows:  cfg.API.EnableWorkflows,
		Cron:       cfg.Features.Cron,
		Memory:     cfg.Features.Memory,
	}
}

func (rt *channelRuntime) handleSessionToolCatalog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || rt.dispatcher == nil {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		platform := r.URL.Query().Get("platform")
		if platform != "wechat" && platform != "feishu" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "platform must be wechat or feishu"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"platform": platform, "tools": rt.dispatcher.ToolCatalog(platform)})
	}
}

func (rt *channelRuntime) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		projects, err := session.ListProjects(rt.sessionDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	project, err := session.CreateProject(rt.sessionDir, body.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (rt *channelRuntime) handleProjectByID(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project ID required"})
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		project, err := session.RenameProject(rt.sessionDir, id, body.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, project)
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := session.DeleteProject(rt.sessionDir, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rt *channelRuntime) handleSessionManagementUpdate(sessions activeSessionManager, w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session route"})
		return
	}
	srv, ok := sessions.(*openaiapi.Server)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server not ready"})
		return
	}
	if parts[1] == "title" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		item, err := srv.SetSessionTitle(parts[0], body.Title)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}
	if parts[1] != "metadata" || r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ProjectID *string `json:"projectId"`
		Pinned    *bool   `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	metadata, err := session.GetSessionMetadata(rt.sessionDir, parts[0])
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if body.ProjectID != nil {
		metadata.ProjectID = *body.ProjectID
	}
	if body.Pinned != nil {
		metadata.Pinned = *body.Pinned
	}
	item, err := srv.SetSessionMetadata(parts[0], metadata)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleExperts projects Runtime-owned expert discovery for the WebUI. The
// HTTP adapter never reads expert directories itself; work-directory scoping,
// bundle validation and identity state stay behind openaiapi.Server.
func (rt *channelRuntime) handleExperts(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		srv, ok := sessions.(*openaiapi.Server)
		if !ok || srv == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server not ready"})
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
		if sessionID == "" {
			sessionID = strings.TrimSpace(r.URL.Query().Get("session_id"))
		}
		relative := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/experts"), "/")
		if relative == "" {
			experts, err := srv.ListExperts(sessionID)
			if err != nil {
				writeExpertHTTPError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"experts": experts})
			return
		}
		id, err := url.PathUnescape(relative)
		if err != nil || strings.Contains(id, "/") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid expert ID"})
			return
		}
		detail, err := srv.InspectExpert(sessionID, id)
		if err != nil {
			writeExpertHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}

func writeExpertHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "expert_invalid"
	switch {
	case errors.Is(err, openaiapi.ErrSessionNotFound), errors.Is(err, session.ErrForkSessionNotFound):
		status, code = http.StatusNotFound, "session_not_found"
	case errors.Is(err, agentruntime.ErrExpertSwitchRequiresFork):
		status, code = http.StatusConflict, "expert_switch_requires_fork"
	case openaiapi.IsSessionExpertMutationBusy(err), errors.Is(err, session.ErrForkSessionActive):
		status, code = http.StatusConflict, "session_active"
	}
	writeJSON(w, status, map[string]string{"error": code, "code": code})
}

// handleSessionExpert is the session-scoped identity projection. Mutations
// delegate to Server.SetSessionExpert; clients must use the normal fork route
// with expertId for an identity replacement.
func (rt *channelRuntime) handleSessionExpert(srv *openaiapi.Server, id string, parts []string, w http.ResponseWriter, r *http.Request) {
	if srv == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server not ready"})
		return
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			state, err := srv.GetSessionExpert(id)
			if err != nil {
				writeExpertHTTPError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, state)
			return
		case http.MethodPatch:
			var body struct {
				ExpertID *string `json:"expertId"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.ExpertID == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expertId is required"})
				return
			}
			state, err := srv.SetSessionExpert(r.Context(), id, *body.ExpertID)
			if err != nil {
				writeExpertHTTPError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, state)
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	}
	if len(parts) == 3 && r.Method == http.MethodGet {
		expertID, err := url.PathUnescape(parts[2])
		if err != nil || expertID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid expert ID"})
			return
		}
		detail, err := srv.InspectExpert(id, expertID)
		if err != nil {
			writeExpertHTTPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (rt *channelRuntime) handleSessionBindings() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		bindings, err := session.ListBindings(rt.sessionDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
	}
}

func (rt *channelRuntime) handleSessions(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if sessions == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
			return
		}

		limitStr := r.URL.Query().Get("limit")
		offsetStr := r.URL.Query().Get("offset")
		limit, _ := strconv.Atoi(limitStr)
		offset, _ := strconv.Atoi(offsetStr)

		// Paginated path: query the DB directly with LIMIT/OFFSET.
		if limit > 0 {
			srv, ok := sessions.(*openaiapi.Server)
			if !ok {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server not available"})
				return
			}
			dir := srv.SessionDir()
			search := strings.TrimSpace(r.URL.Query().Get("search"))
			details, err := session.ListAllDetailed(dir, session.WithLimit(limit), session.WithOffset(offset), session.WithMessagesOnly(), session.WithSearch(search))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			total, err := session.CountWithMessages(dir, session.WithSearch(search))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			result := make([]openaiapi.ActiveSessionInfo, 0, len(details))
			for _, d := range details {
				item := openaiapi.ActiveSessionInfo{
					ID:              d.ID,
					WorkDir:         d.Cwd,
					LastUsed:        d.ModTime,
					MessageCount:    d.MessageCount,
					Preview:         d.Preview,
					Title:           d.Name,
					ChannelType:     d.ChannelType,
					ChannelID:       d.ChannelID,
					ChannelLabel:    channelLabel(d.ChannelType, d.ChannelID),
					Bound:           d.ChannelType == "wechat" || d.ChannelType == "feishu",
					ParentSessionID: d.ParentSession, ForkBoundarySeq: d.ForkBoundarySeq, SeedLength: d.SeedLength, ForkKind: d.ForkKind,
				}
				if metadata, err := session.GetSessionMetadata(dir, item.ID); err == nil {
					item.ProjectID, item.Pinned = metadata.ProjectID, metadata.Pinned
				}
				execution, inspectErr := agentruntime.InspectSessionExecution(dir, item.ID)
				if inspectErr != nil {
					log.Printf("[serve] inspect execution for session %q: %v", item.ID, inspectErr)
				}
				item.Execution = &execution
				item.Running = execution.Running
				if item.ChannelType == "" {
					item.ChannelType = "local"
					item.ChannelLabel = channelLabel(item.ChannelType, item.ChannelID)
				}
				result = append(result, item)
			}
			writeJSON(w, http.StatusOK, map[string]any{"sessions": result, "total": total})
			return
		}

		scope := r.URL.Query().Get("scope")
		if scope == "" {
			scope = "all"
		}
		list := sessions.ListActiveSessions()
		switch scope {
		case "all":
		case "active":
			list = filterActiveSessions(list)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid scope: expected all or active"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
	}
}

// handleSessionID allocates an ID for a WebUI session whose durable creation
// is intentionally deferred until the first prompt/run is submitted.
func (rt *channelRuntime) handleSessionID(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		allocator, ok := sessions.(interface{ AllocateSessionID() (string, error) })
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session allocator is not ready"})
			return
		}
		id, err := allocator.AllocateSessionID()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"sessionId": id})
	}
}

func (rt *channelRuntime) handleCapabilities(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if sessions == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
			return
		}
		writeJSON(w, http.StatusOK, sessions.CapabilityOverview())
	}
}

func (rt *channelRuntime) handleSessionByID(sessions activeSessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/metadata") || strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/title") {
			rt.handleSessionManagementUpdate(sessions, w, r)
			return
		}
		if strings.Contains(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/esm") {
			if esmHandler, ok := sessions.(interface {
				HandleESMAPI(http.ResponseWriter, *http.Request)
			}); ok {
				esmHandler.HandleESMAPI(w, r)
				return
			}
		}
		lifecycle := NewSessionLifecycleService(sessions, rt.dispatcher, rt.sessionDir, rt.identityMux)
		lifecycle.SetEventPublisher(rt.publishManagementEvent)
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/"), "/")
		if len(parts) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session ID required"})
			return
		}
		id, err := url.PathUnescape(parts[0])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session ID"})
			return
		}
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session ID required"})
			return
		}
		if len(parts) >= 2 && parts[1] == "expert" {
			srv, ok := sessions.(*openaiapi.Server)
			if !ok {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server not ready"})
				return
			}
			rt.handleSessionExpert(srv, id, parts, w, r)
			return
		}
		if len(parts) == 2 && parts[1] == "fork" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if requestID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idempotency_key_required", "code": "idempotency_key_required"})
				return
			}
			if len(requestID) > 256 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idempotency_key_too_long", "code": "idempotency_key_too_long"})
				return
			}
			var body struct {
				AtSeq     *int64  `json:"atSeq"`
				TitleMode string  `json:"titleMode"`
				ExpertID  *string `json:"expertId"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
			options := agentruntime.ForkOptions{SourceSessionID: id, AtSeq: body.AtSeq, RequestID: requestID, TitleMode: body.TitleMode}
			var result agentruntime.ForkResult
			if body.ExpertID != nil {
				srv, ok := sessions.(*openaiapi.Server)
				if !ok {
					writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server not ready"})
					return
				}
				result, err = srv.ForkSessionWithExpert(r.Context(), id, options, *body.ExpertID)
			} else {
				result, err = agentruntime.Fork(r.Context(), rt.sessionDir, options)
			}
			if err != nil {
				if body.ExpertID != nil && (errors.Is(err, openaiapi.ErrSessionNotFound) || errors.Is(err, agentruntime.ErrExpertSwitchRequiresFork) || openaiapi.IsSessionExpertMutationBusy(err)) {
					writeExpertHTTPError(w, err)
					return
				}
				status := http.StatusConflict
				code := "fork_failed"
				switch {
				case errors.Is(err, session.ErrForkSessionNotFound):
					status, code = http.StatusNotFound, "session_not_found"
				case errors.Is(err, session.ErrForkInvalidBoundary):
					status, code = http.StatusBadRequest, "invalid_boundary"
				case errors.Is(err, session.ErrForkIdempotencyRequired):
					status, code = http.StatusBadRequest, "idempotency_key_required"
				case errors.Is(err, session.ErrForkIdempotencyTooLong):
					status, code = http.StatusBadRequest, "idempotency_key_too_long"
				case errors.Is(err, session.ErrForkIdempotencyConflict):
					status, code = http.StatusConflict, "idempotency_key_conflict"
				case errors.Is(err, session.ErrForkNoCompletedTurn):
					status, code = http.StatusConflict, "no_completed_turn"
				case errors.Is(err, session.ErrForkUnavailable):
					status, code = http.StatusConflict, "fork_unavailable"
				case errors.Is(err, session.ErrForkSessionActive):
					status, code = http.StatusConflict, "session_active"
				case errors.Is(err, session.ErrForkUnsupportedEntry):
					status, code = http.StatusConflict, "fork_unsupported_entry"
				case errors.Is(err, session.ErrSessionModified):
					status, code = http.StatusConflict, "session_modified"
				case errors.Is(err, session.ErrRuntimeLeaseLost):
					status, code = http.StatusConflict, "session_lease_lost"
				}
				writeJSON(w, status, map[string]string{"error": code, "code": code})
				return
			}
			writeJSON(w, http.StatusOK, result)
			return
		}
		if len(parts) == 1 && id == "active" && r.Method == http.MethodGet {
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"sessions": filterActiveSessions(sessions.ListActiveSessions())})
			return
		}
		if len(parts) == 3 && parts[1] == "approvals" && r.Method == http.MethodPost {
			resolver, ok := sessions.(interface {
				ResolveSessionApproval(string, string, openaiapi.SessionApprovalResponse) (*openaiapi.SessionApprovalResolution, error)
			})
			if !ok {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "approval responses are not supported"})
				return
			}
			var response openaiapi.SessionApprovalResponse
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
			resolved, err := resolver.ResolveSessionApproval(id, parts[2], response)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, resolved)
			return
		}
		if len(parts) == 3 && parts[1] == "questions" && r.Method == http.MethodPost {
			resolver, ok := sessions.(interface {
				ResolveSessionQuestion(string, string, openaiapi.SessionQuestionResponse) (*openaiapi.SessionQuestionResolution, error)
			})
			if !ok {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "question responses are not supported"})
				return
			}
			var response openaiapi.SessionQuestionResponse
			if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
				return
			}
			resolved, err := resolver.ResolveSessionQuestion(id, parts[2], response)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, resolved)
			return
		}
		if len(parts) >= 2 && parts[1] == "esm" {
			if handler, ok := sessions.(interface {
				HandleESMAPI(http.ResponseWriter, *http.Request)
			}); ok {
				handler.HandleESMAPI(w, r)
				return
			}
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "ESM controls are not supported"})
			return
		}
		if len(parts) == 2 && parts[1] == "channel-tools" {
			rt.handleChannelToolsBySession(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "trajectory" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			provider, ok := sessions.(interface {
				GetSessionTrajectory(string, string, int) (*openaiapi.SessionTrajectoryResponse, error)
			})
			if !ok {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "trajectory is not supported"})
				return
			}
			limit := parsePositiveInt(r.URL.Query().Get("limit"), 200)
			response, err := provider.GetSessionTrajectory(id, r.URL.Query().Get("before"), limit)
			if errors.Is(err, openaiapi.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if err != nil {
				if errors.Is(err, openaiapi.ErrInvalidTrajectoryCursor) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": openaiapi.ErrInvalidTrajectoryCursor.Error()})
				} else {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "trajectory unavailable"})
				}
				return
			}
			writeJSON(w, http.StatusOK, response)
			return
		}
		if len(parts) == 2 && parts[1] == "export" {
			exporter, ok := sessions.(interface {
				HandleSessionExport(http.ResponseWriter, *http.Request, string)
			})
			if !ok {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "session export is not supported"})
				return
			}
			exporter.HandleSessionExport(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "mcp" {
			rt.handleSessionMCPConfig(w, r, sessions, id)
			return
		}
		if len(parts) == 2 && parts[1] == "bindings" {
			switch r.Method {
			case http.MethodPost:
				var req struct {
					ChannelType string `json:"channelType"`
					ChannelID   string `json:"channelId"`
				}
				if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
					return
				}
				if err := lifecycle.Bind(r.Context(), id, req.ChannelType, req.ChannelID); err != nil {
					writeLifecycleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"sessionId": id, "channelType": req.ChannelType, "channelId": req.ChannelID})
				return
			case http.MethodPut:
				var req struct {
					ChannelType   string `json:"channelType"`
					ChannelID     string `json:"channelId"`
					FromSessionID string `json:"fromSessionId"`
					ToSessionID   string `json:"toSessionId"`
				}
				if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
					return
				}
				if err := lifecycle.Transfer(r.Context(), req.ChannelType, req.ChannelID, req.FromSessionID, req.ToSessionID); err != nil {
					writeLifecycleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"channelType": req.ChannelType, "channelId": req.ChannelID, "sessionId": req.ToSessionID})
				return
			case http.MethodDelete:
				_, err := lifecycle.Unbind(r.Context(), id)
				if err != nil {
					writeLifecycleError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"sessionId": id, "channelType": "local", "channelId": ""})
				return
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		}
		if len(parts) == 2 && parts[1] == "runs" {
			if r.Method == http.MethodGet {
				lister, ok := sessions.(interface {
					ListSessionRuns(string, int) ([]session.SessionRun, error)
				})
				if !ok {
					writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "run listing is not supported"})
					return
				}
				limit := parsePositiveInt(r.URL.Query().Get("limit"), 100)
				runs, err := lister.ListSessionRuns(id, limit)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"sessionId": id, "runs": runs})
				return
			}
			if r.Method == http.MethodPost {
				submitter, ok := sessions.(interface {
					HandleSubmitRun(http.ResponseWriter, *http.Request)
				})
				if !ok {
					writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "run submission is not supported"})
					return
				}
				submitter.HandleSubmitRun(w, r)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if len(parts) == 2 && parts[1] == "stop" && r.Method == http.MethodPost {
			stopper, ok := sessions.(interface {
				RequestSessionStop(context.Context, string) (agentruntime.SessionStopResult, error)
			})
			if !ok {
				writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "run cancellation is not supported"})
				return
			}
			result, err := stopper.RequestSessionStop(r.Context(), id)
			status := http.StatusConflict
			switch result.Code {
			case agentruntime.SessionStopAccepted, agentruntime.SessionStopRemoteAccepted, agentruntime.SessionStopRecoveryStarted:
				status = http.StatusAccepted
			case agentruntime.SessionStopRecoveryFailed, agentruntime.SessionStopStateUnavailable:
				status = http.StatusServiceUnavailable
			case agentruntime.SessionStopRemoteFailed:
				status = http.StatusBadGateway
			}
			response := map[string]any{
				"status":    result.Code,
				"code":      result.Code,
				"sessionId": id,
				"execution": result.Execution,
			}
			if err != nil {
				response["error"] = err.Error()
			}
			writeJSON(w, status, response)
			return
		}
		if len(parts) == 2 && parts[1] == "runtime" {
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			switch r.Method {
			case http.MethodGet:
				runtime, err := sessions.GetSessionRuntime(id)
				if errors.Is(err, openaiapi.ErrSessionNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
					return
				}
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, runtime)
				return
			case http.MethodPatch:
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
					return
				}
				var patch openaiapi.SessionRuntimePatch
				if len(strings.TrimSpace(string(body))) > 0 {
					if err := json.Unmarshal(body, &patch); err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
						return
					}
				}
				runtime, err := sessions.PatchSessionRuntime(id, patch)
				if errors.Is(err, openaiapi.ErrSessionNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
					return
				}
				if errors.Is(err, openaiapi.ErrInvalidCapability) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, runtime)
				return
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		}
		if len(parts) == 2 && parts[1] == "capabilities" {
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			switch r.Method {
			case http.MethodGet:
				caps, err := sessions.GetSessionCapabilities(id)
				if errors.Is(err, openaiapi.ErrSessionNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
					return
				}
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, caps)
				return
			case http.MethodPatch:
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read request body"})
					return
				}
				var patch openaiapi.SessionCapabilityPatch
				if len(strings.TrimSpace(string(body))) > 0 {
					if err := json.Unmarshal(body, &patch); err != nil {
						writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
						return
					}
				}
				caps, err := sessions.PatchSessionCapabilities(id, patch)
				if errors.Is(err, openaiapi.ErrSessionNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
					return
				}
				if errors.Is(err, openaiapi.ErrInvalidCapability) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
					return
				}
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, caps)
				return
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		}
		if len(parts) == 2 && parts[1] == "stream" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			streamer, ok := sessions.(interface {
				StreamSession(http.ResponseWriter, *http.Request, string)
			})
			if !ok {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session stream is unavailable"})
				return
			}
			streamer.StreamSession(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "messages" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}

			// Support paginated message loading: ?before=<seq>&limit=N or ?limit=N (latest)
			beforeStr := r.URL.Query().Get("before")
			limitStr := r.URL.Query().Get("limit")
			if beforeStr != "" || limitStr != "" {
				limit, _ := strconv.Atoi(limitStr)
				if limit <= 0 || limit > 200 {
					limit = 50
				}
				if srv, ok := sessions.(*openaiapi.Server); ok {
					if beforeStr != "" {
						beforeSeq, err := strconv.ParseInt(beforeStr, 10, 64)
						if err != nil {
							writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid before seq"})
							return
						}
						msgs, hasMore, err := srv.GetSessionMessagesBefore(id, beforeSeq, limit)
						if err != nil {
							writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
							return
						}
						writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "hasMore": hasMore})
						return
					}
					// No before, only limit: load latest N messages.
					msgs, hasMore, err := srv.GetSessionMessagesLatest(id, limit)
					if err != nil {
						writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
						return
					}
					writeJSON(w, http.StatusOK, map[string]any{"messages": msgs, "hasMore": hasMore})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server not available"})
				return
			}

			msgs, err := sessions.GetSessionMessages(id)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
			return
		}
		if len(parts) == 2 && parts[1] == "subagents" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			agents, err := sessions.GetSessionSubAgents(id)
			if errors.Is(err, openaiapi.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"subagents": agents})
			return
		}
		if len(parts) == 4 && parts[1] == "subagents" && parts[3] == "messages" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			agentID, err := url.PathUnescape(parts[2])
			if err != nil || agentID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sub-agent ID"})
				return
			}
			msgs, err := sessions.GetSessionSubAgentMessages(id, agentID)
			if errors.Is(err, openaiapi.ErrSessionNotFound) || errors.Is(err, openaiapi.ErrSubAgentNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
			return
		}
		if len(parts) == 2 && parts[1] == "run-events" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			events, err := sessions.GetSessionRunEvents(id)
			if errors.Is(err, openaiapi.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"events": events})
			return
		}
		if len(parts) == 2 && parts[1] == "capability-events" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			events, err := sessions.GetSessionCapabilityEvents(id)
			if errors.Is(err, openaiapi.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"events": events})
			return
		}
		if len(parts) == 3 && parts[1] == "tool-results" {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if sessions == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
				return
			}
			toolCallID, err := url.PathUnescape(parts[2])
			if err != nil || toolCallID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tool call ID"})
				return
			}
			detail, err := sessions.GetSessionToolResult(id, toolCallID)
			if errors.Is(err, openaiapi.ErrSessionToolResultNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if detail == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": openaiapi.ErrSessionToolResultNotFound.Error()})
				return
			}
			writeJSON(w, http.StatusOK, detail)
			return
		}
		if len(parts) != 1 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if sessions == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "API server not ready"})
			return
		}
		if lifecycle == nil || sessions == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session lifecycle service unavailable"})
			return
		}
		deleted, err := lifecycle.Delete(r.Context(), id)
		if conflict, ok := err.(*lifecycleConflict); ok {
			writeConflict(w, conflict.Code, conflict.Message)
			return
		}
		if errors.Is(err, openaiapi.ErrActiveSessionIDAmbiguous) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !deleted {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
	}
}

func (rt *channelRuntime) channelToolsAppliesTo(sessionID string) string {
	if rt == nil || sessionID == "" {
		return "current"
	}
	snapshot, err := agentruntime.InspectSessionExecution(rt.sessionDir, sessionID)
	if err != nil || !snapshot.CanSubmit {
		return "next_run"
	}
	return "current"
}

func (rt *channelRuntime) handleChannelToolsBySession(w http.ResponseWriter, r *http.Request, sessionID string) {
	if rt.dispatcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "channel dispatcher unavailable"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		response, status, err := rt.channelToolsState(sessionID)
		if err != nil {
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, response)
	case http.MethodPut:
		var req struct {
			Tools []channelToolSelection `json:"tools"`
		}
		decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		response, status, err := rt.replaceChannelTools(sessionID, req.Tools)
		if err != nil {
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, response)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (rt *channelRuntime) channelToolsState(sessionID string) (*channelToolsResponse, int, error) {
	binding, err := session.FindBindingBySessionID(rt.sessionDir, sessionID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if binding == nil {
		return nil, http.StatusConflict, fmt.Errorf("session is not bound to wechat or feishu")
	}
	states, generation, err := rt.dispatcher.SessionToolStates(sessionID, binding.ChannelType)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	appliesTo := rt.channelToolsAppliesTo(sessionID)
	return &channelToolsResponse{
		SessionID: sessionID, Platform: binding.ChannelType, Generation: generation,
		AppliesTo: appliesTo, Tools: states,
	}, http.StatusOK, nil
}

func (rt *channelRuntime) replaceChannelTools(sessionID string, selections []channelToolSelection) (*channelToolsResponse, int, error) {
	releaseData := session.LockSessionData(rt.sessionDir, sessionID)
	defer releaseData()
	binding, err := session.FindBindingBySessionID(rt.sessionDir, sessionID)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	if binding == nil {
		return nil, http.StatusConflict, fmt.Errorf("session is not bound to wechat or feishu")
	}
	catalog := rt.dispatcher.ToolCatalog(binding.ChannelType)
	if len(selections) != len(catalog) {
		return nil, http.StatusBadRequest, fmt.Errorf("tools must contain the complete catalog (%d entries)", len(catalog))
	}
	definitions := make(map[string]channels.ToolCatalogItem, len(catalog))
	for _, item := range catalog {
		definitions[item.Name] = item
	}
	seen := make(map[string]bool, len(selections))
	configured := make([]session.ChannelToolConfig, 0, len(selections))
	for _, item := range selections {
		name := strings.TrimSpace(item.Name)
		definition, ok := definitions[name]
		if !ok {
			return nil, http.StatusBadRequest, fmt.Errorf("unknown channel tool %q", name)
		}
		if seen[name] {
			return nil, http.StatusBadRequest, fmt.Errorf("duplicate channel tool %q", name)
		}
		seen[name] = true
		if item.Enabled && !definition.Available {
			reason := definition.UnavailableReason
			if reason == "" {
				reason = "tool is unavailable"
			}
			return nil, http.StatusConflict, fmt.Errorf("tool %q is unavailable: %s", name, reason)
		}
		configured = append(configured, session.ChannelToolConfig{ToolName: name, Enabled: item.Enabled})
	}
	if err := session.SetChannelTools(rt.sessionDir, sessionID, configured); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, http.StatusNotFound, err
		}
		return nil, http.StatusInternalServerError, err
	}
	rt.dispatcher.RefreshSessionTools(sessionID)
	response, status, err := rt.channelToolsState(sessionID)
	if err != nil {
		return nil, status, err
	}
	rt.publishManagementEvent("channel_tools_changed", map[string]any{
		"sessionId": sessionID, "platform": binding.ChannelType,
		"generation": response.Generation, "appliesTo": response.AppliesTo,
	})
	return response, status, nil
}

func writeConflict(w http.ResponseWriter, code, message string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeLifecycleError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if conflict, ok := err.(*lifecycleConflict); ok {
		writeConflict(w, conflict.Code, conflict.Message)
		return
	}
	status := http.StatusInternalServerError
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusRequestTimeout
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func filterActiveSessions(list []openaiapi.ActiveSessionInfo) []openaiapi.ActiveSessionInfo {
	active := make([]openaiapi.ActiveSessionInfo, 0, len(list))
	for _, sess := range list {
		if sess.Active {
			active = append(active, sess)
		}
	}
	return active
}

func channelLabel(channelType, channelID string) string {
	switch channelType {
	case "wechat":
		return "WeChat"
	case "feishu":
		return "Feishu"
	default:
		return "Local"
	}
}

func (rt *channelRuntime) handleChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, rt.channelStatuses())
}

func (rt *channelRuntime) channelStatuses() []channelStatus {
	cfg := rt.configSnapshot()
	if cfg == nil {
		return []channelStatus{
			{Name: "wechat", Enabled: false},
			{Name: "feishu", Enabled: false},
		}
	}
	statuses := []channelStatus{
		{Name: "wechat", Enabled: cfg.Channels.Wechat.Enabled, Connected: false},
		{Name: "feishu", Enabled: cfg.Channels.Feishu.Enabled, Connected: false},
	}
	byName := map[string]int{
		"wechat": 0,
		"feishu": 1,
	}
	for _, p := range rt.platformSnapshot() {
		if idx, ok := byName[p.Name()]; ok {
			statuses[idx].Connected = p.IsConnected()
			continue
		}
		statuses = append(statuses, channelStatus{Name: p.Name(), Enabled: true, Connected: p.IsConnected()})
	}
	return statuses
}

type envVariableView struct {
	Name            string `json:"name"`
	ValueConfigured bool   `json:"valueConfigured"`
}

type envView struct {
	Variables []envVariableView `json:"variables"`
}

type envVariableEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type envPatchRequest struct {
	Set   []envVariableEntry `json:"set"`
	Unset []string           `json:"unset"`
}

func envViewFromConfig(cfg *config.EnvConfig) envView {
	vars := cfg.List()
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	view := envView{Variables: make([]envVariableView, 0, len(names))}
	for _, name := range names {
		view.Variables = append(view.Variables, envVariableView{Name: name, ValueConfigured: true})
	}
	return view
}

// handleEnv exposes the global env.json through a secret-safe contract. GET
// only returns sorted variable names and a configured flag; PATCH applies an
// atomic set/unset mutation. The older PUT replacement method remains
// accepted for compatibility, but its response is also secret-safe. Values
// are never echoed in responses or errors.
func (rt *channelRuntime) handleEnv(w http.ResponseWriter, r *http.Request) {
	cfg := config.LoadEnv()
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, envViewFromConfig(cfg))
	case http.MethodPut:
		var body config.EnvConfig
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if body.Vars == nil {
			body.Vars = map[string]string{}
		}
		for name := range body.Vars {
			if err := config.ValidateEnvName(name); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid environment variable name"})
				return
			}
		}
		cfg.Vars = body.Vars
		if err := cfg.Save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, envViewFromConfig(cfg))
	case http.MethodPatch:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		var req envPatchRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		set := map[string]string{}
		for _, entry := range req.Set {
			name := strings.TrimSpace(entry.Name)
			if err := config.ValidateEnvName(name); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid name %q", name)})
				return
			}
			set[name] = entry.Value
		}
		unset := make([]string, 0, len(req.Unset))
		for _, name := range req.Unset {
			name = strings.TrimSpace(name)
			if err := config.ValidateEnvName(name); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid name %q", name)})
				return
			}
			unset = append(unset, name)
		}
		if err := cfg.ApplyPatch(set, unset); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, envViewFromConfig(cfg))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (rt *channelRuntime) handleSettings(srv *openaiapi.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			settings, err := config.LoadSettings()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, settings)
		case http.MethodPut:
			var settings config.Settings
			if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := config.SaveGlobalSettings(&settings); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if srv != nil {
				if err := srv.ApplySettings(&settings); err != nil {
					log.Printf("serve: apply settings: %v", err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
			}
			if rt.dispatcher != nil {
				if err := rt.dispatcher.ApplySettings(&settings); err != nil {
					log.Printf("serve: apply channel settings: %v", err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
			}
			writeJSON(w, http.StatusOK, settings)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (rt *channelRuntime) handleMemory(w http.ResponseWriter, r *http.Request) {
	cfg := rt.configSnapshot()
	if cfg == nil || !cfg.Features.Memory {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "content": ""})
		case http.MethodPut:
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "memory is disabled"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	store := memory.NewStore(cfg.Memory.Path, cfg.API.GetWorkDir())
	switch r.Method {
	case http.MethodGet:
		content, path, source, err := store.Read()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": true,
			"path":    path,
			"source":  source,
			"content": content,
		})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if err := store.WriteAll(body.Content); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		content, path, source, err := store.Read()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": true,
			"path":    path,
			"source":  source,
			"content": content,
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (rt *channelRuntime) handleWebUI(w http.ResponseWriter, r *http.Request) {
	cfg := rt.configSnapshot()
	if cfg == nil || !cfg.WebUI.Enabled {
		http.NotFound(w, r)
		return
	}
	uiHandler(cfg.WebUI.Dir).ServeHTTP(w, r)
}

func (rt *channelRuntime) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	if runtime.GOOS == "windows" && path == windowsDriveListRoot {
		writeJSON(w, http.StatusOK, windowsDriveListResponse())
		return
	}
	if path == "" {
		path = rt.browseDefaultDir()
		if fallback := nearestExistingBrowseDir(path); fallback != "" {
			path = fallback
		}
	}
	abs, parent, err := rt.resolveBrowseDir(path)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	type dirEntry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"isDir"`
	}
	var dirs []dirEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, dirEntry{Name: name, Path: filepath.Join(abs, name), IsDir: true})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       abs,
		"parent":     parent,
		"entries":    dirs,
		"selectable": true,
	})
}

func (rt *channelRuntime) handleSelectDirectory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		DefaultPath string `json:"defaultPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	defaultPath := body.DefaultPath
	if defaultPath == "" {
		defaultPath = rt.browseDefaultDir()
	}
	defaultPath = nearestExistingBrowseDir(defaultPath)
	picker := rt.nativeDirectoryPicker
	if picker == nil {
		picker = openNativeDirectoryPicker
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	selected, err := picker(ctx, defaultPath)
	if err != nil {
		if errors.Is(err, errNativeDirectoryPickerUnavailable) {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": err.Error()})
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "native directory picker timed out or was canceled"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(selected) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"canceled": true, "path": ""})
		return
	}
	abs, _, err := rt.resolveBrowseDir(selected)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canceled": false, "path": abs})
}

func (rt *channelRuntime) browseDefaultDir() string {
	if cfg := rt.configSnapshot(); cfg != nil {
		return cfg.API.GetWorkDir()
	}
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		return cwd
	}
	return "."
}

func nearestExistingBrowseDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	abs = filepath.Clean(abs)
	for {
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			if st, statErr := os.Stat(real); statErr == nil && st.IsDir() {
				return filepath.Clean(real)
			}
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

func (rt *channelRuntime) resolveBrowseDir(path string) (string, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("invalid path: %w", err)
	}
	abs = filepath.Clean(abs)
	realAbs, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("invalid path: %w", err)
	}
	realAbs = filepath.Clean(realAbs)
	roots, err := rt.browseAllowedRoots(realAbs)
	if err != nil {
		return "", "", err
	}
	if !pathWithinAnyRoot(realAbs, roots) {
		return "", "", fmt.Errorf("directory %q is not in allowed browse roots", path)
	}
	// Windows volume roots (C:\) share no common parent; the virtual drive
	// list acts as their parent so browsing can switch between volumes.
	if runtime.GOOS == "windows" && isWindowsDriveRoot(realAbs) {
		return realAbs, windowsDriveListRoot, nil
	}
	parent := filepath.Dir(realAbs)
	if parent == realAbs || !pathWithinAnyRoot(parent, roots) {
		parent = realAbs
	}
	return realAbs, parent, nil
}

func (rt *channelRuntime) browseAllowedRoots(path string) ([]string, error) {
	cfg := rt.configSnapshot()
	if cfg == nil {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		return []string{filepath.Clean(cwd)}, nil
	}
	var configured []string
	if cfg.API.AllowedWorkDirs != nil {
		configured = append(configured, (*cfg.API.AllowedWorkDirs)...)
	} else if len(cfg.Security.AllowedWorkDirs) > 0 {
		configured = append(configured, cfg.Security.AllowedWorkDirs...)
	} else {
		configured = browseFilesystemRoots(path)
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("directory browsing is disabled")
	}
	roots := make([]string, 0, len(configured))
	for _, root := range configured {
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("invalid browse root %q: %w", root, err)
		}
		abs = filepath.Clean(abs)
		if realRoot, err := filepath.EvalSymlinks(abs); err == nil {
			abs = filepath.Clean(realRoot)
		}
		roots = append(roots, abs)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("directory browsing is disabled")
	}
	return roots, nil
}

// windowsDriveListRoot is a virtual browse path that lists every available
// Windows drive root. Drive roots have no common parent directory, so
// regular parent navigation cannot switch between drives.
const windowsDriveListRoot = `\`

func isWindowsDriveRoot(path string) bool {
	volume := filepath.VolumeName(path)
	return len(volume) == 2 && volume[1] == ':' && path == filepath.Clean(volume+string(filepath.Separator))
}

func windowsDriveRoots() []string {
	var roots []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(rune(letter)) + ":" + string(filepath.Separator)
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			roots = append(roots, root)
		}
	}
	return roots
}

func windowsDriveListResponse() map[string]any {
	entries := make([]map[string]any, 0)
	for _, root := range windowsDriveRoots() {
		entries = append(entries, map[string]any{"name": root, "path": root, "isDir": true})
	}
	return map[string]any{
		"path":       windowsDriveListRoot,
		"parent":     windowsDriveListRoot,
		"entries":    entries,
		"selectable": false,
	}
}

func browseFilesystemRoots(path string) []string {
	abs, err := filepath.Abs(path)
	if err != nil || abs == "" {
		abs = string(filepath.Separator)
	}
	abs = filepath.Clean(abs)
	volume := filepath.VolumeName(abs)
	if runtime.GOOS == "windows" {
		roots := windowsDriveRoots()
		// A UNC share is its own volume. It cannot be enumerated alongside
		// drive letters, so retain the current share as an additional root.
		if len(volume) > 2 {
			uncRoot := filepath.Clean(volume + string(filepath.Separator))
			if !pathWithinAnyRoot(uncRoot, roots) {
				roots = append(roots, uncRoot)
			}
		}
		if len(roots) > 0 {
			return roots
		}
	}
	if volume != "" {
		return []string{filepath.Clean(volume + string(filepath.Separator))}
	}
	return []string{string(filepath.Separator)}
}

func pathWithinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

const defaultWebUIDir = "ui/dist"

func uiHandler(dir string) http.Handler {
	if useEmbeddedWebUI(dir) {
		return uiFSHandler(webui.DistFS(), "embedded Web UI assets not found")
	}
	return uiFSHandler(os.DirFS(resolveWebUIDir(dir)), "Web UI assets not found. Build ui/dist or set webUI.dir to a built frontend directory.")
}

func uiFSHandler(fsys fs.FS, missingMessage string) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(pathpkg.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
			if name == "index.html" {
				serveUIIndex(w, fsys)
				return
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if st, err := fs.Stat(fsys, "index.html"); err == nil && !st.IsDir() {
			serveUIIndex(w, fsys)
			return
		}
		http.Error(w, missingMessage, http.StatusServiceUnavailable)
	})
}

func serveUIIndex(w http.ResponseWriter, fsys fs.FS) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "Web UI index not found", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func resolveWebUIDir(dir string) string {
	if dir == "" {
		dir = defaultWebUIDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}

	var fallback string
	if cwd, err := os.Getwd(); err == nil {
		fallback = filepath.Join(cwd, dir)
		if hasUIIndex(fallback) {
			return fallback
		}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		for _, candidate := range []string{
			filepath.Join(exeDir, dir),
			filepath.Join(exeDir, "..", "share", "mothx", dir),
		} {
			if hasUIIndex(candidate) {
				return candidate
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	return dir
}

func useEmbeddedWebUI(dir string) bool {
	return dir == "" || filepath.ToSlash(filepath.Clean(dir)) == defaultWebUIDir
}

func hasUIIndex(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !st.IsDir()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
