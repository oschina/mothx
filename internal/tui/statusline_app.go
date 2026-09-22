package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	tuistatusline "github.com/oschina/mothx/internal/tui/statusline"
	"github.com/oschina/mothx/internal/ua"
)

type statusLineRenderedMsg struct {
	requestHash string
	output      string
	errText     string
	generation  uint64
	sessionID   string
	workingDir  string
}

type statusLineTickMsg time.Time

func (a *App) statusLineConfig() config.StatusLineSettings {
	if a.settings == nil {
		return config.DefaultSettings().StatusLine
	}
	return a.settings.StatusLine
}

func (a *App) statusLineEnabled() bool {
	return tuistatusline.Enabled(a.statusLineConfig())
}

func (a *App) tickStatusLine() tea.Cmd {
	interval := tuistatusline.RefreshInterval(a.statusLineConfig())
	if interval <= 0 {
		return nil
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg { return statusLineTickMsg(t) })
}

func (a *App) requestStatusLineRefresh(force bool) {
	if !a.statusLineEnabled() || a.width <= 0 || !a.ready {
		return
	}
	payload, err := tuistatusline.MarshalPayload(a.buildStatusLinePayload())
	if err != nil {
		a.statusLineOutput = ""
		a.statusLineLastError = err.Error()
		return
	}

	cfg := a.statusLineConfig()
	sessionID, workingDir := a.statusLineScope()
	req := &statusLineRequest{
		hash:       tuistatusline.Hash(payload, a.width, cfg.Command),
		force:      force,
		payload:    payload,
		width:      a.width,
		generation: a.statusLineGeneration,
		sessionID:  sessionID,
		workingDir: workingDir,
	}

	if a.statusLineInFlight {
		a.statusLinePending = req
		return
	}
	if !force && req.hash == a.statusLineLastAttempt {
		return
	}
	if !force && a.statusLineOutput != "" && req.hash == a.statusLineLastSuccess {
		return
	}
	a.startStatusLineRequest(req)
}

func (a *App) startStatusLineRequest(req *statusLineRequest) {
	if req == nil || !a.statusLineEnabled() {
		return
	}
	a.statusLineInFlight = true
	a.statusLineLastAttempt = req.hash
	cfg := a.statusLineConfig()
	settings := a.settings
	program := a.program
	timeout := tuistatusline.Timeout(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	a.statusLineCancel = cancel
	go func() {
		defer cancel()
		result := tuistatusline.Run(ctx, settings, cfg, req.payload, req.width)
		errText := ""
		switch {
		case result.Err != nil && result.Stderr != "":
			errText = fmt.Sprintf("%v: %s", result.Err, result.Stderr)
		case result.Err != nil:
			errText = result.Err.Error()
		case result.Output == "":
			errText = "empty status line output"
		}
		if program != nil {
			program.Send(statusLineRenderedMsg{
				requestHash: req.hash,
				output:      result.Output,
				errText:     errText,
				generation:  req.generation,
				sessionID:   req.sessionID,
				workingDir:  req.workingDir,
			})
		}
	}()
}

func (a *App) handleStatusLineRendered(msg statusLineRenderedMsg) tea.Cmd {
	if msg.generation != a.statusLineGeneration || msg.sessionID != a.currentStatusLineSessionID() || msg.workingDir != a.currentStatusLineWorkingDir() {
		return nil
	}
	a.statusLineInFlight = false
	a.statusLineCancel = nil
	if msg.errText != "" || msg.output == "" {
		a.statusLineOutput = ""
		a.statusLineLastError = msg.errText
	} else {
		a.statusLineOutput = msg.output
		a.statusLineLastError = ""
		a.statusLineLastSuccess = msg.requestHash
	}
	if pending := a.statusLinePending; pending != nil {
		a.statusLinePending = nil
		if pending.force || a.statusLineOutput == "" || pending.hash != a.statusLineLastSuccess {
			a.startStatusLineRequest(pending)
		}
	}
	a.scheduleRender()
	return nil
}

func (a *App) invalidateStatusLineRequests() {
	a.statusLineGeneration++
	if a.statusLineCancel != nil {
		a.statusLineCancel()
		a.statusLineCancel = nil
	}
	a.statusLineInFlight = false
	a.statusLinePending = nil
	a.statusLineOutput = ""
	a.statusLineLastError = ""
	a.statusLineLastSuccess = ""
	a.statusLineLastAttempt = ""
}

func (a *App) statusLineScope() (string, string) {
	sessionID := ""
	workingDir := a.cwd
	if a.session != nil && a.session.GetHeader() != nil {
		header := a.session.GetHeader()
		sessionID = header.ID
		if header.Cwd != "" {
			workingDir = header.Cwd
		}
	}
	if sessionID == "" {
		sessionID = a.getCurrentSessionID()
	}
	if workingDir == "" {
		workingDir = "."
		if wd, err := os.Getwd(); err == nil && wd != "" {
			workingDir = wd
		}
	}
	return sessionID, workingDir
}

func (a *App) currentStatusLineSessionID() string {
	id, _ := a.statusLineScope()
	return id
}

func (a *App) currentStatusLineWorkingDir() string {
	_, dir := a.statusLineScope()
	return dir
}

func (a *App) buildStatusLinePayload() tuistatusline.Payload {
	settings := a.settings
	if settings == nil {
		settings = config.DefaultSettings()
	}
	cwd := "."
	sessionID := ""
	projectDir := cwd
	var sessionStart time.Time
	if a.session != nil && a.session.GetHeader() != nil {
		header := a.session.GetHeader()
		if header.Cwd != "" {
			cwd = header.Cwd
			projectDir = header.Cwd
		}
		sessionID = header.ID
		sessionStart = header.Timestamp
	}
	if sessionID == "" {
		sessionID = a.getCurrentSessionID()
	}
	if cwd == "." {
		if wd, err := os.Getwd(); err == nil && wd != "" {
			cwd = wd
			projectDir = wd
		}
	}

	var effort *tuistatusline.Effort
	if level := strings.TrimSpace(settings.DefaultThinkingLevel); level != "" {
		effort = &tuistatusline.Effort{Level: level}
	}

	var modelInfo *tuistatusline.ModelInfo
	contextWindowSize := 0
	if a.model != nil {
		modelInfo = &tuistatusline.ModelInfo{
			ID:          a.model.ID,
			DisplayName: a.model.Name,
		}
		contextWindowSize = a.model.ContextWindow
	}

	var usedPct *float64
	var remainingPct *float64
	if a.contextUsage != nil {
		if a.contextUsage.ContextWindow > 0 {
			contextWindowSize = a.contextUsage.ContextWindow
		}
		if a.contextUsage.Percent != nil {
			v := *a.contextUsage.Percent
			usedPct = &v
			r := 100 - v
			remainingPct = &r
		}
	}

	var currentUsage *tuistatusline.CurrentUsage
	if a.latestUsage != nil {
		currentUsage = &tuistatusline.CurrentUsage{
			InputTokens:              a.latestUsage.Input,
			OutputTokens:             a.latestUsage.Output,
			CacheCreationInputTokens: a.latestUsage.CacheWrite,
			CacheReadInputTokens:     a.latestUsage.CacheRead,
		}
	}

	durationMs := int64(0)
	if !sessionStart.IsZero() {
		durationMs = time.Since(sessionStart).Milliseconds()
	} else if a.lastDuration > 0 {
		durationMs = a.lastDuration.Milliseconds()
	}

	outputStyle := "default"
	if strings.TrimSpace(settings.Theme) != "" {
		outputStyle = settings.Theme
	}

	payload := tuistatusline.Payload{
		HookEventName: "Status",
		SessionID:     sessionID,
		Cwd:           cwd,
		Model:         modelInfo,
		Workspace: &tuistatusline.Workspace{
			CurrentDir: cwd,
			ProjectDir: projectDir,
		},
		Version: ua.Version,
		OutputStyle: &tuistatusline.OutputStyle{
			Name: outputStyle,
		},
		Effort: effort,
		Cost: &tuistatusline.CostInfo{
			TotalCostUSD:       a.totalCostUSD,
			TotalDurationMs:    durationMs,
			TotalAPIDurationMs: a.lastDuration.Milliseconds(),
		},
		ContextWindow: &tuistatusline.ContextWindow{
			ContextWindowSize:   contextWindowSize,
			TotalInputTokens:    a.totalInputTokens,
			TotalOutputTokens:   totalOutputTokensFromUsage(a.latestUsage),
			CurrentUsage:        currentUsage,
			UsedPercentage:      usedPct,
			RemainingPercentage: remainingPct,
		},
		Vim:        nil,
		RateLimits: nil,
	}
	return payload
}

func totalOutputTokensFromUsage(usage *provider.Usage) int {
	if usage == nil {
		return 0
	}
	return usage.Output
}

func cloneUsage(u *provider.Usage) *provider.Usage {
	if u == nil {
		return nil
	}
	cloned := *u
	return &cloned
}
