package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *nativeApp) transcriptWithPlan(c *ui.Context, p palette) {
	plan := a.activePlanMessage()
	if plan == nil {
		a.transcript(c, p)
		return
	}
	ui.Row(c).Grow(1).Children(func() {
		ui.Column(c).Grow(1).Children(func() { a.transcript(c, p) })
		a.planProgressPanel(c, p, plan)
	})
}

func (a *nativeApp) activePlanMessage() *chatMessage {
	if a.planMessageID == "" {
		return nil
	}
	m := a.messageByID(a.planMessageID)
	if m == nil || m.Kind != messagePlan || len(m.Plan) == 0 {
		return nil
	}
	return m
}

func (a *nativeApp) transcriptWindow() ([]*chatMessage, int) {
	if len(a.messages) <= transcriptRenderWindow {
		return a.messages, 0
	}
	return a.messages[len(a.messages)-transcriptRenderWindow:], len(a.messages) - transcriptRenderWindow
}

func (a *nativeApp) transcript(c *ui.Context, p palette) {
	window, hidden := a.transcriptWindow()
	a.list.Key = func(i int) any { return window[i].ID }
	a.list.FollowEnd = true
	a.list.FollowAnimation = &ui.Smooth
	ui.Column(c).Grow(1).Children(func() {
		if hidden > 0 {
			ui.Row(c).Justify(ui.Center).Padding(2, 24).Children(func() {
				ui.Text(c, Tf("仅显示最近 %d 条消息", transcriptRenderWindow)).
					FontSize(9.5).TextColor(p.T.TextMuted)
			})
		}
		if a.transcriptPagingEnabled() && (a.transcriptNextCursor != "" || a.transcriptLoading) {
			ui.Row(c).Justify(ui.Center).Padding(0, 24, 4, 24).Children(func() {
				label := T("加载更早消息")
				if a.transcriptLoading {
					label = T("正在加载更早消息…")
				}
				b := ui.Button(c.Key("transcript-older"), label).Label(T("加载更早的消息"))
				b.Disabled(a.transcriptLoading || a.transcriptNextCursor == "")
				if b.Clicked() && !a.transcriptLoading {
					a.loadOlderTranscript()
				}
			})
		}
		ui.List(c, &a.list, len(window), func(i int) {
			a.messageRow(c, p, window[i])
		}).Grow(1).Padding(22, 24, 10, 24).Gap(18)
	})
}

func (a *nativeApp) planProgressPanel(c *ui.Context, p palette, plan *chatMessage) {
	entries := plan.Plan
	completed := 0
	for _, entry := range entries {
		if entry.Status == "completed" {
			completed++
		}
	}
	title := plan.PlanTitle
	if title == "" {
		title = T("计划进度")
	}
	collapsed := a.planCollapsed && a.planCollapsedID == plan.ID
	width := float32(288)
	if collapsed {
		width = 200
	}
	ui.Column(c).Width(width).FillHeight().Padding(12, 12).Gap(8).
		BorderWidth(0, 0, 0, 1).BorderColor(p.T.Border).Background(p.T.Background).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconSpark).FontSize(13).TextColor(p.T.Accent)
			ui.Text(c, title).FontSize(11).FontWeight(650).TextColor(p.T.Text).Grow(1).SingleLine()
			ui.Text(c, Tf("%d/%d 已完成", completed, len(entries))).FontSize(9.5).TextColor(p.T.TextMuted)
			icon := iconChevronDown
			label := T("收起计划进度")
			if collapsed {
				icon = iconChevronRight
				label = T("展开计划进度")
			}
			toggle := ui.ButtonBase(c.Key("plan-toggle")).Size(20, 20).Radius(5).
				Justify(ui.Center).AlignItems(ui.Center)
			if toggle.Hovered() {
				toggle.Background(p.Hover)
			}
			toggle.Children(func() {
				ui.Icon(c, icon).FontSize(11).TextColor(p.T.TextMuted)
			})
			toggle.Label(label).Tooltip(label)
			if toggle.Clicked() {
				a.planCollapsed = !collapsed
				a.planCollapsedID = plan.ID
			}
		})
		if collapsed {
			return
		}
		if plan.PlanNote != "" {
			ui.Text(c, plan.PlanNote).FontSize(10).TextColor(p.T.TextMuted).LineHeight(1.35)
		}
		planProgressBar(c, p, completed, len(entries))
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(7).Children(func() {
				for _, entry := range entries {
					entry := entry
					ui.Row(c).AlignItems(ui.Start).Gap(6).Children(func() {
						planStepMarker(c, p, entry.Status)
						t := ui.Text(c, entry.Content).FontSize(10.5).TextColor(p.T.Text).Grow(1).LineHeight(1.35)
						if entry.Status == "completed" {
							t.Strikethrough().TextColor(p.T.TextMuted)
						}
					})
				}
			})
		})
	})
}

// transcriptPlainText is the full canonical transcript projection for copying.
// It includes every message in a.messages, not just the render window.
func (a *nativeApp) transcriptPlainText() string {
	var b strings.Builder
	for _, m := range a.messages {
		switch m.Kind {
		case messageUser:
			b.WriteString("## User\n\n")
			b.WriteString(m.Text)
		case messageAssistant:
			b.WriteString("## Assistant\n\n")
			b.WriteString(m.Text)
		case messageThought:
			b.WriteString("## Reasoning\n\n")
			b.WriteString(m.Text)
		case messageTool:
			b.WriteString("## Tool: ")
			b.WriteString(m.ToolTitle)
			b.WriteString("\n\n")
			b.WriteString(m.ToolText)
		case messagePlan:
			b.WriteString("## Plan")
			if m.PlanTitle != "" {
				b.WriteString(": ")
				b.WriteString(m.PlanTitle)
			}
			b.WriteString("\n\n")
			if m.PlanNote != "" {
				b.WriteString(m.PlanNote)
				b.WriteString("\n\n")
			}
			for _, entry := range m.Plan {
				b.WriteString("- [")
				b.WriteString(entry.Status)
				b.WriteString("] ")
				b.WriteString(entry.Content)
				b.WriteString("\n")
			}
		case messageSubagent:
			b.WriteString("## Sub-agent: ")
			b.WriteString(m.SubagentTitle)
			b.WriteString(" — ")
			b.WriteString(m.SubagentStatus)
		case messageStatus, messageSystem, messageError:
			b.WriteString(m.Text)
		case messageArtifact:
			b.WriteString("## Artifact: ")
			b.WriteString(m.Filename)
		}
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}
