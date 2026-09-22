package tui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/oschina/mothx/internal/stats"
	"github.com/oschina/mothx/internal/tui/i18n"
)

const defaultStatsAddr = "127.0.0.1:7878"

type statsServerStartedMsg struct {
	server   *stats.Server
	db       *stats.DB
	listener net.Listener
	url      string
}

type statsServerStartFailedMsg struct{ err error }
type statsServerStoppedMsg struct {
	err       error
	requested bool
}
type statsOverlayLoadedMsg struct {
	lines []string
	err   error
}

func (a *App) handleStatsCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		a.addCommandStatus(commandUsage(a.translator, "/stats server|stop-server|tui"))
		return nil
	}
	switch parts[1] {
	case "server":
		return a.startStatsServer()
	case "stop-server":
		return a.stopStatsServer()
	case "tui":
		return a.openStatsOverlay()
	default:
		a.addCommandError(commandUsage(a.translator, "/stats server|stop-server|tui"))
		return nil
	}
}

func (a *App) startStatsServer() tea.Cmd {
	if a.statsServer != nil {
		a.addCommandStatus(a.translator.Text(i18n.MsgStatsServerAlreadyRunning, a.statsServerURL))
		return nil
	}
	a.addCommandStatus(a.translator.Text(i18n.MsgStatsStarting))
	return func() tea.Msg {
		db, err := stats.OpenDefault()
		if err != nil {
			return statsServerStartFailedMsg{err: err}
		}
		listener, err := net.Listen("tcp", defaultStatsAddr)
		if err != nil {
			db.Close()
			return statsServerStartFailedMsg{err: err}
		}
		server := stats.NewServer(db, defaultStatsAddr)
		return statsServerStartedMsg{
			server:   server,
			db:       db,
			listener: listener,
			url:      "http://" + listener.Addr().String(),
		}
	}
}

func (a *App) stopStatsServer() tea.Cmd {
	if a.statsServer == nil {
		a.addCommandStatus(a.translator.Text(i18n.MsgStatsNotRunning))
		return nil
	}
	server := a.statsServer
	a.addCommandStatus(a.translator.Text(i18n.MsgStatsStopping))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return statsServerStoppedMsg{err: server.Shutdown(ctx), requested: true}
	}
}

func (a *App) serveStatsServer(server *stats.Server, listener net.Listener) tea.Cmd {
	return func() tea.Msg {
		return statsServerStoppedMsg{err: server.Serve(listener)}
	}
}

func (a *App) openStatsOverlay() tea.Cmd {
	a.statsOverlayOpen = true
	a.statsOverlayScroll = 0
	a.statsOverlayLines = []string{a.translator.Text(i18n.MsgStatsLoading)}
	return func() tea.Msg {
		db, err := stats.OpenDefault()
		if err != nil {
			return statsOverlayLoadedMsg{err: err}
		}
		defer db.Close()
		lines, err := a.buildStatsOverlayLines(db)
		return statsOverlayLoadedMsg{lines: lines, err: err}
	}
}

func (a *App) handleStatsServerStarted(msg statsServerStartedMsg) tea.Cmd {
	a.statsServer = msg.server
	a.statsServerDB = msg.db
	a.statsServerURL = msg.url
	a.addCommandStatus(a.translator.Text(i18n.MsgStatsRunning, msg.url))
	return a.serveStatsServer(msg.server, msg.listener)
}

func (a *App) handleStatsServerStopped(msg statsServerStoppedMsg) {
	if msg.requested && msg.err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgStatsStopFailed, msg.err))
		return
	}
	if msg.err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgStatsStopFailed, msg.err))
	} else if a.statsServer != nil {
		a.addCommandStatus(a.translator.Text(i18n.MsgStatsStopped))
	}
	if a.statsServerDB != nil {
		a.statsServerDB.Close()
	}
	a.statsServer = nil
	a.statsServerDB = nil
	a.statsServerURL = ""
}

func (a *App) handleStatsOverlayLoaded(msg statsOverlayLoadedMsg) {
	if msg.err != nil {
		a.statsOverlayLines = []string{a.translator.Text(i18n.MsgErrorPrefix) + msg.err.Error()}
		return
	}
	if len(msg.lines) == 0 {
		msg.lines = []string{a.translator.Text(i18n.MsgStatsNoData)}
	}
	a.statsOverlayLines = msg.lines
	a.statsOverlayScroll = 0
}

func (a *App) closeStatsOverlay() {
	a.statsOverlayOpen = false
	a.statsOverlayLines = nil
	a.statsOverlayScroll = 0
}

func (a *App) scrollStatsOverlay(delta int) {
	a.statsOverlayScroll += delta
	if a.statsOverlayScroll < 0 {
		a.statsOverlayScroll = 0
	}
	a.scheduleRender()
}

func (a *App) renderStatsOverlay() string {
	width := a.width - 4
	if width < 20 {
		width = 20
	}
	innerWidth := width - 2
	if innerWidth < 10 {
		innerWidth = 10
	}
	footerHeight := 0
	if a.height > 0 {
		footerHeight = lipgloss.Height(a.renderFooter())
	}
	height := a.height - footerHeight - 5
	if height < 3 {
		height = 3
	}
	lines := a.statsOverlayLines
	if len(lines) == 0 {
		lines = []string{a.translator.Text(i18n.MsgStatsLoading)}
	}
	maxOffset := len(lines) - height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if a.statsOverlayScroll > maxOffset {
		a.statsOverlayScroll = maxOffset
	}
	end := a.statsOverlayScroll + height
	if end > len(lines) {
		end = len(lines)
	}
	visible := strings.Join(lines[a.statsOverlayScroll:end], "\n")
	if visible == "" {
		visible = " "
	}
	title := statusStyle.Render(a.translator.Text(i18n.MsgStatsFooter))
	divider := strings.Repeat("─", minInt(innerWidth, lipgloss.Width(title)))
	content := title + "\n" + divider + "\n" + visible
	return toolModalStyle.Width(width).Height(height + 3).Render(content)
}

func (a *App) buildStatsOverlayLines(db *stats.DB) ([]string, error) {
	query := stats.Query{}
	summary, err := db.Summary(query)
	if err != nil {
		return nil, fmt.Errorf("query summary: %w", err)
	}
	byProvider, err := db.ByProvider(query)
	if err != nil {
		return nil, fmt.Errorf("query providers: %w", err)
	}
	byModel, err := db.ByModel(query)
	if err != nil {
		return nil, fmt.Errorf("query models: %w", err)
	}
	recent, err := db.Recent(1, 10)
	if err != nil {
		return nil, fmt.Errorf("query recent requests: %w", err)
	}

	var lines []string
	lines = append(lines,
		a.translator.Text(i18n.MsgStatsTitle),
		"",
		a.translator.Text(i18n.MsgStatsRequests, summary.TotalRequests),
		a.translator.Text(i18n.MsgStatsInputTokens, summary.InputTokens),
		a.translator.Text(i18n.MsgStatsOutputTokens, summary.OutputTokens),
		a.translator.Text(i18n.MsgStatsTotalTokens, summary.TotalTokens),
		"",
	)
	lines = appendStatsAggregateLines(lines, a.translator.Text(i18n.MsgStatsByProvider), byProvider, 5, func(a stats.Aggregate) string {
		if a.Protocol == "" {
			return emptyStatsLabel(a.Vendor)
		}
		return emptyStatsLabel(fmt.Sprintf("%s (%s)", a.Vendor, a.Protocol))
	})
	lines = appendStatsAggregateLines(lines, a.translator.Text(i18n.MsgStatsByModel), byModel, 5, func(a stats.Aggregate) string {
		if a.Model != "" {
			return a.Model
		}
		return emptyStatsLabel(a.Label)
	})
	lines = append(lines, "", a.translator.Text(i18n.MsgStatsRecentRequests))
	if recent == nil || len(recent.Items) == 0 {
		lines = append(lines, a.translator.Text(i18n.MsgStatsNoRows))
		return lines, nil
	}
	for _, item := range recent.Items {
		lines = append(lines, fmt.Sprintf("  %s  %s  %s  in:%d out:%d  %s",
			formatStatsOverlayTime(item.Timestamp),
			emptyStatsLabel(item.Vendor),
			emptyStatsLabel(item.Model),
			item.InputTokens,
			item.OutputTokens,
			formatStatsOverlayDuration(item.DurationMs),
		))
	}
	return lines, nil
}

func appendStatsAggregateLines(lines []string, title string, rows []stats.Aggregate, limit int, labelFn func(stats.Aggregate) string) []string {
	lines = append(lines, title)
	if len(rows) == 0 {
		return append(lines, "  No data", "")
	}
	for i, row := range rows {
		if i >= limit {
			break
		}
		lines = append(lines, fmt.Sprintf("  %s  req:%d  in:%d  out:%d  total:%d",
			labelFn(row), row.Requests, row.InputTokens, row.OutputTokens, row.TotalTokens))
	}
	return append(lines, "")
}

func emptyStatsLabel(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func formatStatsOverlayTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatStatsOverlayDuration(ms int) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
