package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tui/i18n"
)

const backgroundRunPollInterval = time.Second

type backgroundRunPollMsg time.Time

// trackExistingBackgroundRuns discovers runs submitted by this TUI or by a
// shared serve runtime. The current transcript length is the replay boundary,
// so history loaded at startup is not printed a second time.
func (a *App) trackExistingBackgroundRuns() {
	if a == nil || a.session == nil || a.settings == nil {
		return
	}
	header := a.session.GetHeader()
	if header == nil || header.ID == "" {
		return
	}
	runs, err := session.ListSessionRuns(a.settings.GetSessionDir(), header.ID, 200)
	if err != nil {
		return
	}
	if a.backgroundRuns == nil {
		a.backgroundRuns = make(map[string]int)
	}
	messageCount := len(a.session.GetMessages())
	for _, run := range runs {
		if !isTUIBackgroundRun(run) || isTerminalBackgroundRun(run.Status) {
			continue
		}
		if _, tracked := a.backgroundRuns[run.ID]; !tracked {
			a.backgroundRuns[run.ID] = messageCount
		}
	}
}

func isTUIBackgroundRun(run session.SessionRun) bool {
	source := strings.ToLower(strings.TrimSpace(run.Source))
	return strings.Contains(source, "tui") || source == "responses_background"
}

func isTerminalBackgroundRun(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "incomplete", "expired", "failed", "cancelled", "canceled", "abandoned":
		return true
	default:
		return false
	}
}

func (a *App) backgroundRunPoll() tea.Cmd {
	return tea.Tick(backgroundRunPollInterval, func(t time.Time) tea.Msg {
		return backgroundRunPollMsg(t)
	})
}

func (a *App) pollBackgroundRuns() {
	if a == nil || a.session == nil || a.settings == nil || len(a.backgroundRuns) == 0 {
		return
	}
	header := a.session.GetHeader()
	if header == nil || header.ID == "" {
		return
	}
	runs, err := session.ListSessionRuns(a.settings.GetSessionDir(), header.ID, 200)
	if err != nil {
		return
	}
	byID := make(map[string]session.SessionRun, len(runs))
	for _, run := range runs {
		byID[run.ID] = run
	}
	messages := a.session.GetMessages()
	for runID, boundary := range a.backgroundRuns {
		run, ok := byID[runID]
		if !ok || !isTerminalBackgroundRun(run.Status) {
			continue
		}
		if run.Status == "completed" || run.Status == "incomplete" {
			for i := boundary; i < len(messages); i++ {
				if messages[i].Role != "assistant" {
					continue
				}
				text := messages[i].Content
				if text == "" {
					for _, block := range messages[i].Contents {
						if block.Type == "text" {
							text += block.Text
						}
					}
				}
				if strings.TrimSpace(text) != "" {
					a.addMessage(assistantStyle.Render(text))
				}
				break
			}
		} else {
			runErr := strings.TrimSpace(run.Error)
			if runErr != "" {
				runErr = ": " + runErr
			}
			message := a.translator.Text(i18n.MsgBackgroundRunStatus, runID, strings.ToLower(run.Status), runErr)
			a.addMessage(statusStyle.Render(message))
		}
		delete(a.backgroundRuns, runID)
	}
}
