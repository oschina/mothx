package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tui/i18n"
)

type sessionsDialogState struct {
	Open    bool
	Cursor  int
	Items   []session.SessionDetail
	Cwd     string
	Error   string
	Message string
}

var sessionsDialogStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("63")).
	Padding(1, 2)

func (a *App) getSessionDir() string {
	if a.session != nil && a.session.GetSessionDir() != "" {
		return a.session.GetSessionDir()
	}
	if a.settings != nil {
		return a.settings.GetSessionDir()
	}
	return platform.SessionDir()
}

// getCurrentSessionID returns the current session's short ID (first 8 chars).
func (a *App) getCurrentSessionID() string {
	if a.session == nil {
		return ""
	}
	if a.session.GetHeader() != nil {
		return a.session.GetHeader().ID
	}
	file := a.session.GetFile()
	if file == "" {
		return ""
	}
	base := filepath.Base(file)
	base = strings.TrimSuffix(base, ".db")
	if idx := strings.Index(base, "_"); idx >= 0 {
		return base[idx+1:]
	}
	return ""
}

// handleSessionsCommand handles the /sessions command and its subcommands.
func (a *App) handleSessionsCommand(parts []string) {
	if len(parts) == 1 {
		a.openSessionsDialog()
		return
	}

	sub := strings.ToLower(parts[1])
	switch sub {
	case "ls", "list":
		a.sessionsList()
	case "set", "switch", "use":
		if len(parts) < 3 {
			a.addCommandError(commandUsage(a.translator, "/sessions set <id>"))
			return
		}
		a.sessionsSet(parts[2])
	case "clear", "new":
		a.sessionsClear()
	case "del", "delete", "rm":
		if len(parts) < 3 {
			a.addCommandError(commandUsage(a.translator, "/sessions del <id>"))
			return
		}
		a.sessionsDel(parts[2])
	case "fork", "branch":
		a.forkCurrentSession()
	default:
		a.addCommandError(a.translator.Text(i18n.MsgSessionsUnknownSubcommand, sub))
	}
}

func (a *App) forkCurrentSession() {
	if err := a.ensureSession(); err != nil {
		a.addCommandError(fmt.Sprintf("Error creating session: %v", err))
		return
	}
	id := a.getCurrentSessionID()
	if id == "" {
		a.addCommandError("No active session to fork")
		return
	}
	result, err := agentruntime.Fork(context.Background(), a.getSessionDir(), agentruntime.ForkOptions{
		SourceSessionID: id,
		RequestID:       "tui-fork-" + session.GenerateID(),
	})
	if err != nil {
		a.addCommandError(fmt.Sprintf("Session fork failed: %v", err))
		return
	}
	child, err := session.OpenByIDExact(a.getSessionDir(), result.SessionID)
	if err != nil {
		a.addCommandError(fmt.Sprintf("Open forked session failed: %v", err))
		return
	}
	header := child.GetHeader()
	detail := session.SessionDetail{SessionInfo: session.SessionInfo{Path: child.GetFile(), ModTime: header.Timestamp, Cwd: header.Cwd, ParentSession: header.ParentSession, ForkBoundarySeq: header.ForkBoundarySeq, SeedLength: header.SeedLength, ForkKind: header.ForkKind}, ID: result.SessionID, MessageCount: len(child.GetMessages())}
	if err := a.switchToSession(detail); err != nil {
		a.addCommandError(fmt.Sprintf("Open forked session failed: %v", err))
		return
	}
	a.addCommandStatus(a.translator.Text(i18n.MsgSessionsSwitched, result.SessionID, detail.MessageCount))
}

func (a *App) sessionsCwd() string {
	if a.session != nil && a.session.GetHeader() != nil && a.session.GetHeader().Cwd != "" {
		return a.session.GetHeader().Cwd
	}
	if a.cwd != "" {
		return a.cwd
	}
	if w, err := os.Getwd(); err == nil {
		return w
	}
	return "."
}

func (a *App) openSessionsDialog() {
	if a.isThinking {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsCannotSwitchRunning))
		return
	}
	cwd := a.sessionsCwd()
	details, err := session.ListForDirDetailed(cwd, a.getSessionDir())
	state := sessionsDialogState{
		Open:  true,
		Items: details,
		Cwd:   cwd,
	}
	if err != nil {
		state.Error = a.translator.Text(i18n.MsgSessionsErrorListing, err)
	}
	currentID := a.getCurrentSessionID()
	for i, d := range details {
		if d.ID == currentID {
			state.Cursor = i
			break
		}
	}
	a.sessionsDialog = state
	a.input = a.input.Blur()
	a.scheduleRender()
}

func (a *App) closeSessionsDialog() {
	a.sessionsDialog = sessionsDialogState{}
	a.input = a.input.Focus()
	a.scheduleRender()
}

func (a *App) moveSessionsCursor(delta int) {
	if len(a.sessionsDialog.Items) == 0 {
		return
	}
	a.sessionsDialog.Cursor += delta
	if a.sessionsDialog.Cursor < 0 {
		a.sessionsDialog.Cursor = len(a.sessionsDialog.Items) - 1
	}
	if a.sessionsDialog.Cursor >= len(a.sessionsDialog.Items) {
		a.sessionsDialog.Cursor = 0
	}
	a.scheduleRender()
}

func (a *App) confirmSessionsDialog() {
	if len(a.sessionsDialog.Items) == 0 {
		return
	}
	item := a.sessionsDialog.Items[a.sessionsDialog.Cursor]
	if item.ID == a.getCurrentSessionID() {
		a.closeSessionsDialog()
		a.addCommandStatus(a.translator.Text(i18n.MsgSessionsAlreadyCurrent))
		return
	}
	if err := a.switchToSession(item); err != nil {
		a.sessionsDialog.Error = err.Error()
		a.scheduleRender()
		return
	}
	a.closeSessionsDialog()
	a.addCommandStatus(a.translator.Text(i18n.MsgSessionsSwitched, item.ID, item.MessageCount))
}

func (a *App) deleteSelectedSessionDialog() {
	if len(a.sessionsDialog.Items) == 0 {
		return
	}
	item := a.sessionsDialog.Items[a.sessionsDialog.Cursor]
	if item.ID == a.getCurrentSessionID() {
		a.sessionsDialog.Error = a.translator.Text(i18n.MsgSessionsCannotDeleteCurrent)
		a.scheduleRender()
		return
	}
	if err := agentruntime.DeleteSession(a.getSessionDir(), item.ID); err != nil {
		a.sessionsDialog.Error = a.translator.Text(i18n.MsgSessionsDeleteFailed, err)
		a.scheduleRender()
		return
	}
	a.sessionsDialog.Items = append(a.sessionsDialog.Items[:a.sessionsDialog.Cursor], a.sessionsDialog.Items[a.sessionsDialog.Cursor+1:]...)
	if a.sessionsDialog.Cursor >= len(a.sessionsDialog.Items) {
		a.sessionsDialog.Cursor = max(0, len(a.sessionsDialog.Items)-1)
	}
	a.sessionsDialog.Message = a.translator.Text(i18n.MsgSessionsDeleted, item.ID)
	a.sessionsDialog.Error = ""
	a.scheduleRender()
}

func (a *App) handleSessionsKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !a.sessionsDialog.Open {
		return false, nil
	}
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		a.closeSessionsDialog()
		return true, nil
	case tea.KeyUp:
		a.moveSessionsCursor(-1)
		return true, nil
	case tea.KeyDown:
		a.moveSessionsCursor(1)
		return true, nil
	case tea.KeyEnter:
		a.confirmSessionsDialog()
		return true, nil
	case tea.KeyRunes:
		switch strings.ToLower(string(msg.Runes)) {
		case "q":
			a.closeSessionsDialog()
			return true, nil
		case "d":
			a.deleteSelectedSessionDialog()
			return true, nil
		case "n":
			a.closeSessionsDialog()
			a.sessionsClear()
			return true, nil
		}
	}
	return true, nil
}

// sessionsList lists all sessions for the current project directory.
func (a *App) sessionsList() {
	cwd := a.sessionsCwd()
	sessionDir := a.getSessionDir()
	details, err := session.ListForDirDetailed(cwd, sessionDir)
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsErrorListing, err))
		return
	}

	if len(details) == 0 {
		a.addCommandStatus(a.translator.Text(i18n.MsgSessionsNoSessions))
		return
	}

	currentID := a.getCurrentSessionID()

	var sb strings.Builder
	sb.WriteString(a.translator.Text(i18n.MsgSessionsListTitle) + "\n\n")
	for _, d := range details {
		marker := " "
		if d.ID == currentID {
			marker = "*"
		}
		age := formatAgeWithTranslator(a.translator, d.ModTime)
		preview := ""
		if d.Preview != "" {
			preview = " - " + d.Preview
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s  %d msgs  %s%s\n",
			marker, d.ID, d.MessageCount, age, preview))
	}
	sb.WriteString("\n" + a.translator.Text(i18n.MsgSessionsListHint))
	a.addCommandStatus(sb.String())
}

// sessionsSet switches to a different session by ID prefix.
func (a *App) sessionsSet(id string) {
	cwd := a.sessionsCwd()

	// Don't switch to the same session
	if id == a.getCurrentSessionID() {
		a.addCommandStatus(a.translator.Text(i18n.MsgSessionsAlreadyCurrent))
		return
	}

	sessionDir := a.getSessionDir()
	details, err := session.ListForDirDetailed(cwd, sessionDir)
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsErrorListing, err))
		return
	}

	// Find matching session by ID prefix
	var match *session.SessionDetail
	for i, d := range details {
		if strings.HasPrefix(d.ID, id) {
			if match != nil {
				a.addCommandError(a.translator.Text(i18n.MsgSessionsAmbiguousID, id))
				return
			}
			match = &details[i]
		}
	}

	if match == nil {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsNoMatch, id))
		return
	}

	if err := a.switchToSession(*match); err != nil {
		a.addCommandError(err.Error())
		return
	}

	a.addCommandStatus(a.translator.Text(i18n.MsgSessionsSwitched,
		match.ID, match.MessageCount))
}

func (a *App) switchToSession(detail session.SessionDetail) error {
	newSess, err := session.OpenByID(a.sessionsCwd(), a.getSessionDir(), detail.ID)
	if err != nil {
		return fmt.Errorf("Error opening session: %v", err)
	}
	a.discardPendingInput()

	// Side queries and status-line renders belong to the previous session.
	// Cancel/invalidate them before replacing the session state so late events
	// cannot update the new session's UI.
	a.closeBtw()
	a.invalidateStatusLineRequests()
	if err := a.activateSession(newSess); err != nil {
		return fmt.Errorf("activate session: %w", err)
	}
	a.agentActivities = make(map[agentpkg.AgentID]*agentActivity)
	a.agentActivityOrder = nil
	a.historyLoaded = false
	a.agentHistoryLoaded = false

	a.resetAgent(fmt.Errorf("session changed"))
	a.resetTranscriptState()
	a.contextUsage = nil
	a.totalInputTokens = 0
	a.totalCacheRead = 0
	a.totalCacheWrite = 0

	a.LoadHistoryMessages()
	a.contextUsage = a.computeContextUsage()
	a.updateViewportContent()
	for idx := range a.messages {
		a.printMessageOnce(idx)
	}
	return nil
}

func (a *App) handleInitMCPCommand(parts []string) {
	target := "project"
	template := "full"
	force := false

	for _, p := range parts[1:] {
		switch strings.ToLower(p) {
		case "project", "global":
			target = strings.ToLower(p)
		case "basic", "full":
			template = strings.ToLower(p)
		case "--force":
			force = true
		default:
			a.addCommandError(commandUsage(a.translator, "/init_mcp [project|global] [basic|full] [--force]"))
			return
		}
	}

	path := config.ProjectMCPPath()
	if target == "global" {
		path = config.GlobalMCPPath()
	}

	if !force {
		if _, err := os.Stat(path); err == nil {
			a.addCommandStatus(fmt.Sprintf("MCP config already exists: %s (use --force to overwrite)", path))
			return
		}
	}

	var cfg *config.MCPConfig
	if template == "basic" {
		cfg = config.DefaultMCPConfig()
	} else {
		cfg = config.FullMCPConfigTemplate()
	}

	if err := config.SaveMCPConfig(path, cfg); err != nil {
		a.addCommandError(fmt.Sprintf("Init MCP config failed: %v", err))
		return
	}
	a.addCommandStatus(fmt.Sprintf("✅ Created MCP config: %s", path))
	a.addCommandStatus(fmt.Sprintf("Template: %s | Target: %s", template, target))
}

func (a *App) handleMCPsCommand() {
	type sourceInfo struct {
		label string
		path  string
	}
	sources := []sourceInfo{
		{label: "Global", path: config.GlobalMCPPath()},
		{label: "Project", path: config.ProjectMCPPath()},
	}

	var sb strings.Builder
	sb.WriteString("MCP servers:\n")
	foundAny := false

	for _, src := range sources {
		sb.WriteString(fmt.Sprintf("\n%s (%s):\n", src.label, src.path))
		cfg, err := config.LoadMCPConfig(src.path)
		if err != nil {
			if os.IsNotExist(err) {
				sb.WriteString("  (not configured)\n")
				continue
			}
			sb.WriteString(fmt.Sprintf("  (invalid: %v)\n", err))
			continue
		}
		config.NormalizeMCPConfig(cfg)
		if len(cfg.MCPServers) == 0 {
			sb.WriteString("  (empty)\n")
			continue
		}
		for _, srv := range cfg.MCPServers {
			foundAny = true
			target := srv.Command
			if target == "" {
				target = srv.URL
			}
			if target == "" {
				target = "-"
			}
			sb.WriteString(fmt.Sprintf("  - %s [%s] %s\n", srv.Name, srv.Type, target))
		}
	}

	if !foundAny {
		sb.WriteString("\nUse /init_mcp to create project mcp.json.")
	}
	a.addCommandStatus(sb.String())
}

// sessionsClear creates a new session, starting fresh.
func (a *App) sessionsClear() {
	a.closeBtw()
	a.invalidateStatusLineRequests()
	a.session = nil
	if a.runtime != nil {
		if err := a.runtime.UnbindSession(); err != nil {
			a.addCommandError(fmt.Sprintf("unbind session runtime: %v", err))
		}
	}
	a.historyLoaded = false
	a.agentHistoryLoaded = false

	// Reset agent and UI state
	a.resetAgent(fmt.Errorf("session changed"))
	a.resetTranscriptState()
	a.contextUsage = nil
	a.totalInputTokens = 0
	a.totalCacheRead = 0
	a.totalCacheWrite = 0
	a.updateViewportContent()

	a.addCommandStatus(a.translator.Text(i18n.MsgSessionsNewOnNextMessage))
}

// sessionsDel deletes a session by ID prefix.
func (a *App) sessionsDel(id string) {
	cwd := a.sessionsCwd()

	// Don't delete the current session
	if id == a.getCurrentSessionID() {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsCannotDeleteCurrent))
		return
	}

	sessionDir := a.getSessionDir()
	details, err := session.ListForDirDetailed(cwd, sessionDir)
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsErrorListing, err))
		return
	}

	// Find matching session by ID prefix
	var match *session.SessionDetail
	for i, d := range details {
		if strings.HasPrefix(d.ID, id) {
			if match != nil {
				a.addCommandError(a.translator.Text(i18n.MsgSessionsAmbiguousID, id))
				return
			}
			match = &details[i]
		}
	}

	if match == nil {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsNoMatch, id))
		return
	}

	if err := agentruntime.DeleteSession(a.getSessionDir(), match.ID); err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgSessionsDeleteFailed, err))
		return
	}

	a.addCommandStatus(a.translator.Text(i18n.MsgSessionsDeleted, match.ID))
}

func (a *App) renderSessionsDialog() string {
	if !a.sessionsDialog.Open {
		return ""
	}
	width := a.width - 4
	if width < 60 {
		width = 60
	}
	if width > 110 {
		width = 110
	}

	var lines []string
	lines = append(lines, a.translator.Text(i18n.MsgSessionsTitle))
	path := a.sessionsDialog.Cwd
	if xansi.StringWidth(path) > width-4 {
		path = xansi.Truncate(path, width-4, "…")
	}
	lines = append(lines, statusStyle.Render(path), "")

	if len(a.sessionsDialog.Items) == 0 {
		lines = append(lines, a.translator.Text(i18n.MsgSessionsNoSessions))
	} else {
		limit := 10
		if a.height > 0 && a.height-10 < limit {
			limit = a.height - 10
			if limit < 4 {
				limit = 4
			}
		}
		start, end := visibleRange(a.sessionsDialog.Cursor, len(a.sessionsDialog.Items), limit)
		currentID := a.getCurrentSessionID()
		for i := start; i < end; i++ {
			d := a.sessionsDialog.Items[i]
			cursor := "  "
			style := lipgloss.NewStyle()
			if i == a.sessionsDialog.Cursor {
				cursor = "› "
				style = style.Foreground(lipgloss.Color("86")).Bold(true)
			}
			marker := "  "
			if d.ID == currentID {
				marker = "* "
			}
			preview := d.Preview
			if preview != "" {
				preview = " - " + strings.ReplaceAll(preview, "\n", " ")
			}
			line := fmt.Sprintf("%s%s%s  %d msgs  %s%s", cursor, marker, d.ID, d.MessageCount, formatAgeWithTranslator(a.translator, d.ModTime), preview)
			if xansi.StringWidth(line) > width-4 {
				line = xansi.Truncate(line, width-4, "…")
			}
			lines = append(lines, style.Render(line))
		}
		if len(a.sessionsDialog.Items) > limit {
			lines = append(lines, "", statusStyle.Render(a.translator.Text(i18n.MsgSessionsShowingRange, start+1, end, len(a.sessionsDialog.Items))))
		}
	}

	if a.sessionsDialog.Message != "" {
		lines = append(lines, "", statusStyle.Render(a.sessionsDialog.Message))
	}
	if a.sessionsDialog.Error != "" {
		lines = append(lines, "", errorStyle.Render(a.sessionsDialog.Error))
	}
	lines = append(lines, "", a.translator.Text(i18n.MsgSessionsDialogHint))

	return sessionsDialogStyle.Width(width).Render(strings.Join(lines, "\n"))
}

func visibleRange(cursor, total, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit <= 0 || limit >= total {
		return 0, total
	}
	start := cursor - limit/2
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > total {
		end = total
		start = end - limit
	}
	return start, end
}

// formatAge returns a human-readable age string for a time (English default).
func formatAge(t time.Time) string {
	return formatAgeWithTranslator(i18n.New(i18n.LanguageEN), t)
}

func formatAgeWithTranslator(tr i18n.Translator, t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return tr.Text(i18n.MsgSessionsAgeJustNow)
	case d < time.Hour:
		return tr.Text(i18n.MsgSessionsAgeMinutes, int(d.Minutes()))
	case d < 24*time.Hour:
		return tr.Text(i18n.MsgSessionsAgeHours, int(d.Hours()))
	case d < 30*24*time.Hour:
		return tr.Text(i18n.MsgSessionsAgeDays, int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
