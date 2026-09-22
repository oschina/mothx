package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/config"
	tuistatusline "github.com/oschina/mothx/internal/tui/statusline"
)

func (a *App) handleStatusLineCommand(parts []string) {
	sub := "status"
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "status":
		a.showStatusLineStatus()
	case "on", "off":
		scope := "project"
		if len(parts) > 2 {
			scope = strings.ToLower(parts[2])
		}
		if scope != "project" && scope != "global" {
			a.addCommandError(commandUsage(a.translator, "/statusline [status|on|off] [project|global]"))
			return
		}
		a.toggleStatusLine(sub == "on", scope)
	case "command":
		a.setStatusLineCommand(parts)
	case "refresh":
		a.setStatusLineRefresh(parts)
	default:
		a.addCommandError(commandUsage(a.translator, "/statusline [status|on|off|command|refresh] ..."))
	}
}

func (a *App) showStatusLineStatus() {
	cfg := a.statusLineConfig()
	if !cfg.Enabled {
		a.addCommandStatus("Status line: OFF", "Footer: builtin")
		return
	}

	var lines []string
	lines = append(lines, "Status line: ON")
	lines = append(lines, fmt.Sprintf("  Type: %s", cfg.Type))
	lines = append(lines, fmt.Sprintf("  Command: %s", strings.TrimSpace(cfg.Command)))
	lines = append(lines, fmt.Sprintf("  Timeout: %dms", tuistatusline.Timeout(cfg).Milliseconds()))
	if cfg.RefreshInterval > 0 {
		lines = append(lines, fmt.Sprintf("  Refresh: %ds", cfg.RefreshInterval))
	} else {
		lines = append(lines, "  Refresh: event-driven")
	}
	if a.statusLineInFlight {
		lines = append(lines, "  Render: running")
	} else if strings.TrimSpace(a.statusLineOutput) != "" {
		lines = append(lines, "  Render: external footer active")
	} else {
		lines = append(lines, "  Render: builtin footer fallback")
	}
	if a.statusLineLastError != "" {
		lines = append(lines, "  Last error: "+a.statusLineLastError)
	}
	a.addCommandStatus(lines...)
}

func (a *App) toggleStatusLine(enabled bool, scope string) {
	s, err := loadStatusLineSettings(scope)
	if err != nil {
		a.addCommandError(fmt.Sprintf("Failed to load %s settings: %v", scope, err))
		return
	}

	if enabled {
		if s.StatusLine.Type == "" {
			s.StatusLine.Type = "command"
		}
		if strings.TrimSpace(s.StatusLine.Command) == "" {
			s.StatusLine.Command = "ccstatusline"
		}
		if s.StatusLine.TimeoutMs == 0 {
			s.StatusLine.TimeoutMs = 800
		}
		if s.StatusLine.Fallback == "" {
			s.StatusLine.Fallback = "builtin"
		}
	}
	s.StatusLine.Enabled = enabled

	if err := saveStatusLineSettings(scope, s); err != nil {
		a.addCommandError(fmt.Sprintf("Failed to save %s settings: %v", scope, err))
		return
	}

	if a.settings == nil {
		a.settings = config.DefaultSettings()
	}
	a.settings.StatusLine = s.StatusLine
	a.invalidateStatusLineRequests()
	if !enabled {
		a.statusLineOutput = ""
		a.statusLineLastError = ""
		a.statusLineLastSuccess = ""
		a.statusLineLastAttempt = ""
		a.statusLinePending = nil
		a.statusLineInFlight = false
	} else if a.ready && a.width > 0 {
		a.requestStatusLineRefresh(true)
	}
	a.scheduleRender()

	state := "OFF"
	if enabled {
		state = "ON"
	}
	a.addCommandStatus(fmt.Sprintf("Status line: %s (%s settings)", state, scope))
}

func (a *App) setStatusLineCommand(parts []string) {
	if len(parts) < 3 {
		a.addCommandError(commandUsage(a.translator, "/statusline command <cmd> [project|global]"))
		return
	}
	scope := "project"
	end := len(parts)
	last := strings.ToLower(parts[len(parts)-1])
	if last == "project" || last == "global" {
		scope = last
		end--
	}
	cmd := strings.TrimSpace(strings.Join(parts[2:end], " "))
	if cmd == "" {
		a.addCommandError(commandUsage(a.translator, "/statusline command <cmd> [project|global]"))
		return
	}
	s, err := loadStatusLineSettings(scope)
	if err != nil {
		a.addCommandError(fmt.Sprintf("Failed to load %s settings: %v", scope, err))
		return
	}
	s.StatusLine.Type = "command"
	s.StatusLine.Command = cmd
	if s.StatusLine.TimeoutMs == 0 {
		s.StatusLine.TimeoutMs = 800
	}
	if s.StatusLine.Fallback == "" {
		s.StatusLine.Fallback = "builtin"
	}
	if err := saveStatusLineSettings(scope, s); err != nil {
		a.addCommandError(fmt.Sprintf("Failed to save %s settings: %v", scope, err))
		return
	}
	if a.settings == nil {
		a.settings = config.DefaultSettings()
	}
	a.settings.StatusLine = s.StatusLine
	a.invalidateStatusLineRequests()
	if a.settings.StatusLine.Enabled && a.ready && a.width > 0 {
		a.requestStatusLineRefresh(true)
	}
	a.scheduleRender()
	a.addCommandStatus(fmt.Sprintf("Status line command updated (%s settings): %s", scope, cmd))
}

func (a *App) setStatusLineRefresh(parts []string) {
	if len(parts) < 3 {
		a.addCommandError(commandUsage(a.translator, "/statusline refresh <sec> [project|global]"))
		return
	}
	scope := "project"
	valueIdx := 2
	if len(parts) > 3 {
		last := strings.ToLower(parts[len(parts)-1])
		if last == "project" || last == "global" {
			scope = last
		}
	}
	refreshStr := parts[valueIdx]
	refresh, err := strconv.Atoi(refreshStr)
	if err != nil || refresh < 0 || refresh > 60 {
		a.addCommandError(commandUsage(a.translator, "/statusline refresh <0-60> [project|global]"))
		return
	}
	s, err := loadStatusLineSettings(scope)
	if err != nil {
		a.addCommandError(fmt.Sprintf("Failed to load %s settings: %v", scope, err))
		return
	}
	s.StatusLine.RefreshInterval = refresh
	if err := saveStatusLineSettings(scope, s); err != nil {
		a.addCommandError(fmt.Sprintf("Failed to save %s settings: %v", scope, err))
		return
	}
	if a.settings == nil {
		a.settings = config.DefaultSettings()
	}
	a.settings.StatusLine = s.StatusLine
	a.invalidateStatusLineRequests()
	a.statusLineIntervalInit = false
	if a.statusLineEnabled() && refresh > 0 && a.program != nil {
		a.program.Send(statusLineTickMsg(time.Now()))
	}
	a.scheduleRender()
	if refresh == 0 {
		a.addCommandStatus(fmt.Sprintf("Status line refresh updated (%s settings): event-driven", scope))
		return
	}
	a.addCommandStatus(fmt.Sprintf("Status line refresh updated (%s settings): %ds", scope, refresh))
}

func loadStatusLineSettings(scope string) (*config.Settings, error) {
	switch scope {
	case "global":
		return config.LoadGlobalSettingsSparse()
	default:
		return config.LoadProjectSettingsSparse()
	}
}

func saveStatusLineSettings(scope string, s *config.Settings) error {
	switch scope {
	case "global":
		return config.SaveGlobalSettings(s)
	default:
		return config.SaveProjectSettings(s)
	}
}

// listSkills displays all available skills.
