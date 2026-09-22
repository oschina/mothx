package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/tui/i18n"
)

// renderAgentTabBar renders a horizontal tab bar showing all active agents.
func renderAgentTabBar(tr i18n.Translator, agentMgr *agent.AgentManager, activeID string, width int) string {
	if agentMgr == nil {
		return ""
	}

	ids := agentMgr.List()
	if len(ids) <= 1 {
		return ""
	}

	activeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")).
		Bold(true)

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	stateIcon := func(state string) string {
		switch state {
		case "running":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("●")
		case "ready":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("○")
		case "done":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("✓")
		case "error":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("✗")
		case "canceled":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("⊘")
		default:
			return " "
		}
	}
	localizedState := func(state string) string {
		switch state {
		case "running":
			return tr.Text(i18n.MsgToolModalStateRunning)
		case "ready":
			return tr.Text(i18n.MsgToolModalStateReady)
		case "done":
			return tr.Text(i18n.MsgToolModalStateDone)
		case "error":
			return tr.Text(i18n.MsgToolModalStateError)
		case "canceled":
			return tr.Text(i18n.MsgToolModalStateCanceled)
		default:
			return tr.Text(i18n.MsgToolModalStateUnknown)
		}
	}

	var tabs []string
	for _, id := range ids {
		st, ok := agentMgr.Status(id)
		state := ""
		if ok {
			state = st.State
		}

		name := string(id)
		label := tr.Text(i18n.MsgToolModalAgentTab, stateIcon(state), name)
		if state != "" {
			label += " (" + localizedState(state) + ")"
		}

		if string(id) == activeID {
			tabs = append(tabs, activeStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveStyle.Render(label))
		}
	}

	row := strings.Join(tabs, " ")
	if lipgloss.Width(row) > width {
		row = xansi.Truncate(row, width, "...")
	}

	border := lipgloss.NewStyle().
		BorderBottom(true).
		BorderForeground(lipgloss.Color("240")).
		Width(width)

	return lipgloss.JoinVertical(lipgloss.Left, row, border.Render(""))
}
