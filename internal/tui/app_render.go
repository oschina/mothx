package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/startvibecoding/GoStreamingMarkdown/gsm"
	"github.com/oschina/mothx/internal/tui/i18n"
	"github.com/oschina/mothx/internal/tui/renderutil"
)

func (a *App) updateViewportContent() {
	if a.program != nil {
		a.liveContent = ""
		return
	}
	content := a.renderTranscriptContent()
	a.liveContent = content
}

func (a *App) renderTranscriptContent() string {
	count := len(a.messages)
	if a.currentThinkIdx >= count {
		count = a.currentThinkIdx + 1
	}
	if a.currentAssistantIdx >= count {
		count = a.currentAssistantIdx + 1
	}
	blocks := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		rendered := strings.TrimRight(a.renderMessageAt(idx), "\n")
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		blocks = append(blocks, rendered)
	}
	return strings.Join(blocks, "\n\n")
}

func (a *App) renderLiveTranscriptContent() string {
	if a.program == nil {
		return a.liveContent
	}

	count := len(a.messages)
	if a.currentThinkIdx >= count {
		count = a.currentThinkIdx + 1
	}
	if a.currentAssistantIdx >= count {
		count = a.currentAssistantIdx + 1
	}
	blocks := make([]string, 0, 2)
	for idx := 0; idx < count; idx++ {
		if a.printedMessageIdx[idx] {
			continue
		}
		isCurrentApproval := a.waitingForApproval && a.currentApprovalIdx >= 0 && idx == a.currentApprovalIdx
		// Keep an active tool in the managed viewport until its terminal result
		// arrives. This is shared by compact and full event display so the
		// transient "running" row is never committed to terminal scrollback.
		isRunningTool := a.isToolMessageIndex(idx) && a.toolResultRunningAt(idx)
		if idx != a.currentThinkIdx && idx != a.currentAssistantIdx && !isCurrentApproval && !isRunningTool {
			continue
		}
		rendered := strings.TrimRight(a.renderMessageAt(idx), "\n")
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		blocks = append(blocks, rendered)
	}
	return strings.Join(blocks, "\n\n")
}

func (a *App) toolResultRunningAt(idx int) bool {
	for _, result := range a.toolResults {
		if result.msgIndex == idx {
			return result.status == toolResultStatusRunning
		}
	}
	return false
}

func (a *App) configureMarkdownRenderer() {
	width := renderutil.MarkdownStyleWrapWidth(a.assistantMarkdownWidth())
	a.mdRenderer = gsm.NewStream(width, nil)
}

func (a *App) assistantMarkdownWidth() int {
	width := a.width
	if width <= 0 {
		width = 80
	}
	width -= lipgloss.Width(a.translator.Text(i18n.MsgAssistantPrefix))
	if width < 1 {
		return 1
	}
	return width
}

func (a *App) renderFixedHeight(view string) string {
	if a.height <= 0 {
		return view
	}
	view = strings.TrimRight(view, "\n")
	lines := strings.Split(view, "\n")
	if len(lines) > a.height {
		lines = lines[len(lines)-a.height:]
	}
	for len(lines) < a.height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
