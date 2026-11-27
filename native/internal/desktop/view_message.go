package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func (a *nativeApp) messageRow(c *ui.Context, p palette, m *chatMessage) {
	switch m.Kind {
	case messageUser:
		ui.Row(c).Key(m.ID).Children(func() {
			ui.Spacer(c)
			ui.Column(c).MaxWidthPercent(78).MaxWidth(560).Padding(9, 13).Radius(15).
				Background(p.Bubble).Children(func() {
				ui.Text(c, m.Text).FontSize(12.8).LineHeight(1.5).Selectable()
				a.renderChatImages(c, p, m.Images, a.userImageMaxWidth(c))
			})
		})
	case messageAssistant:
		ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Gap(4).Children(func() {
			if m.Text != "" {
				if m.Done {
					markdown.View(c, m.Text, &a.md)
				} else {
					markdown.Stream(c, m.Text, &a.md)
				}
			} else if len(m.Images) == 0 && !m.Done {
				ui.Text(c, T("正在生成…")).FontSize(12).TextColor(p.T.TextMuted)
			}
			// Assistant content can carry inline image blocks too; project them
			// below the text instead of leaving the decoded content invisible.
			a.renderChatImages(c, p, m.Images, a.assistantImageMaxWidth(c, 520, 0))
			if m.Done && m.Text != "" {
				ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
					copyButton(c, p, m.Text)
				})
			}
		})
	case messageThought:
		ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Gap(3).Children(func() {
			b := ui.ButtonBase(c.Key("thought:"+m.ID)).Padding(1, 0).Radius(6).AlignItems(ui.Center).Justify(ui.Start).Gap(5)
			b.Children(func() {
				if m.Collapsed {
					ui.Icon(c, iconChevronRight).FontSize(11).TextColor(p.T.TextMuted)
				} else {
					ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.T.TextMuted)
				}
				label := T("已思考")
				if !m.Done {
					label = T("思考中…")
				} else if m.ThoughtDuration > 0 {
					label = fmt.Sprintf("%s · %s", label, formatThoughtDuration(m.ThoughtDuration))
				}
				ui.Text(c, label).FontSize(11.5).TextColor(p.T.TextMuted)
			})
			b.Label(T("已思考"))
			if b.Clicked() {
				m.Collapsed = !m.Collapsed
			}
			// Desktop previews the tail of the thought that is still streaming
			// even while collapsed, so reasoning progress stays visible without
			// expanding the full block. Completed thoughts stay compact.
			if m.Collapsed && !m.Done && m.Text != "" {
				ui.Text(c, thoughtPreview(m.Text)).FontSize(10.5).TextColor(p.T.TextMuted).
					SingleLine().LineHeight(1.3)
			}
			if !m.Collapsed {
				ui.Column(c).Padding(9, 12).Radius(10).Background(p.Thought).
					Border(1, p.T.Border).Children(func() {
					ui.Text(c, m.Text).FontSize(11.5).LineHeight(1.5).
						TextColor(p.T.TextMuted).Selectable()
				})
			}
		})
	case messageTool:
		a.toolCard(c, p, m)
	case messagePlan:
		a.planCard(c, p, m)
	case messageArtifact:
		b := ui.ButtonBase(c.Key("artifact:"+m.ID)).WidthPercent(100).AlignItems(ui.Center).Gap(8).
			Padding(9, 12).Radius(10).Background(p.Thought).Border(1, p.T.Border)
		b.Children(func() {
			icon := iconFile
			if m.ArtifactKind == "image" {
				icon = iconImage
			}
			ui.Icon(c, icon).FontSize(16).TextColor(p.T.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				label := T("制品")
				if m.ArtifactFromToolCall {
					label = T("制品（来自 publish_artifact 工具调用）")
				}
				ui.Text(c, label).FontSize(10).TextColor(p.T.TextMuted)
				ui.Text(c, m.Filename).FontSize(12).FontWeight(550).SingleLine()
				meta := []string{}
				if m.ArtifactKind != "" {
					meta = append(meta, m.ArtifactKind)
				}
				if m.MediaType != "" {
					meta = append(meta, m.MediaType)
				}
				if m.ArtifactSize > 0 {
					meta = append(meta, humanBytes(int64(m.ArtifactSize)))
				}
				if len(meta) > 0 {
					ui.Text(c, strings.Join(meta, " · ")).FontSize(9.5).TextColor(p.T.TextMuted).SingleLine()
				}
			})
			ui.Icon(c, iconChevronRight).FontSize(12).TextColor(p.T.TextMuted)
		})
		b.Label(T("打开制品 " + m.Filename))
		if b.Clicked() {
			a.openArtifactMessage(m)
		}
	case messageDecision:
		a.decisionCard(c, p, m)
	case messageSubagent:
		a.subagentCard(c, p, m)
	case messageStatus:
		ui.Row(c).Key(m.ID).AlignItems(ui.Center).Gap(6).Margin(0, ui.Auto).Children(func() {
			color := p.T.TextMuted
			if !m.Done {
				color = p.T.Accent
			}
			ui.Box(c).Size(5, 5).Radius(3).Background(color)
			ui.Text(c, T(m.Text)).FontSize(10.5).TextColor(color).SingleLine()
		})
	case messageError:
		ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).
			Radius(10).Background(p.ErrorBg).Border(1, ui.RGBA(255, 107, 107, 0.30)).
			Padding(10, 12).Gap(7).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Icon(c, iconAlert).FontSize(14).TextColor(p.T.Danger)
				ui.Text(c, T("运行失败")).FontSize(11.5).FontWeight(650).TextColor(p.T.Danger).Grow(1)
				if m.ErrorCode != "" {
					badge(c, p, m.ErrorCode)
				}
				if m.ErrorRetryable {
					badge(c, p, "可重试")
				}
			})
			ui.Text(c, T(m.Text)).FontSize(10.5).LineHeight(1.4).
				TextColor(p.T.Text).Selectable()
		})
	default:
		color := p.T.TextMuted
		if strings.HasPrefix(m.Text, "发送失败") || strings.HasPrefix(m.Text, "载入会话失败") {
			color = p.T.Danger
		}
		// messageSystem rows have no dedicated branch; their authored text is
		// translated here so locale switches also apply to history already stored
		// in a.messages (for example the disconnected notice and the run-finished
		// stop-reason line).
		ui.Text(c.Key(m.ID), T(m.Text)).FontSize(11).TextColor(color).
			Margin(0, ui.Auto).Selectable()
	}
}

func (a *nativeApp) toolCard(c *ui.Context, p palette, m *chatMessage) {
	status := m.ToolStatus
	if status == "" {
		status = "pending"
	}
	ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Radius(10).Background(p.Thought).
		Border(1, p.T.Border).Children(func() {
		b := ui.ButtonBase(c.Key("tool:"+m.ToolID)).Padding(8, 10).Gap(8).AlignItems(ui.Center)
		b.Children(func() {
			icon := iconWrench
			if m.ToolKind == "execute" {
				icon = iconTerminal
			}
			ui.Icon(c, icon).FontSize(14).TextColor(p.T.TextMuted)
			ui.Text(c, m.ToolTitle).FontSize(11.5).TextColor(p.T.Text).SingleLine().Grow(1)
			statusPill(c, p, status)
			if m.ToolText != "" {
				if m.ToolExpanded {
					ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.T.TextMuted)
				} else {
					ui.Icon(c, iconChevronRight).FontSize(11).TextColor(p.T.TextMuted)
				}
			}
		})
		b.Label(m.ToolTitle)
		if b.Clicked() && m.ToolText != "" {
			m.ToolExpanded = !m.ToolExpanded
		}
		if m.ToolExpanded {
			if input := prettyToolInput(m.ToolInput); input != "" {
				ui.Box(c).Height(1).Background(p.T.Border)
				ui.Text(c, T("输入")).FontSize(9.5).FontWeight(600).TextColor(p.T.TextMuted).Padding(8, 10, 0, 10)
				ui.Text(c, input).Font("monospace").FontSize(10).LineHeight(1.4).
					TextColor(p.T.TextMuted).Padding(4, 10, 8, 10).Selectable()
			}
			if m.ToolText != "" {
				ui.Box(c).Height(1).Background(p.T.Border)
				if len(m.ToolText) > 1600 {
					ui.Scroll(c).Height(240).Padding(9, 10).Children(func() {
						ui.Text(c, truncate(m.ToolText, 20000)).Font("monospace").FontSize(10).LineHeight(1.4).
							TextColor(p.T.TextMuted).Selectable()
					})
				} else {
					ui.Text(c, truncate(m.ToolText, 12000)).Font("monospace").FontSize(10).LineHeight(1.4).
						TextColor(p.T.TextMuted).Padding(9, 10).Selectable()
				}
			}
			for _, diff := range m.ToolDiffs {
				ui.Box(c).Height(1).Background(p.T.Border)
				ui.Text(c, toolDiffText(diff)).Font("monospace").FontSize(10).LineHeight(1.4).
					TextColor(p.T.TextMuted).Padding(9, 10).Selectable()
			}
			if len(m.ToolLocations) > 0 {
				ui.Box(c).Height(1).Background(p.T.Border)
				ui.Text(c, strings.Join(m.ToolLocations, "\n")).Font("monospace").FontSize(9.5).
					TextColor(p.T.TextMuted).Padding(6, 10).Selectable()
			}
			if len(m.ToolImages) > 0 {
				ui.Box(c).Height(1).Background(p.T.Border)
				ui.Column(c).Padding(8, 10).Children(func() {
					a.renderChatImages(c, p, m.ToolImages, a.assistantImageMaxWidth(c, 520, 20))
				})
			}
		}
	})
}

// planStepMarker mirrors Desktop's plan step indicator: a filled check for a
// completed step, an accent ring with a dot while running, a danger ring for a
// failed step, and a neutral empty ring otherwise.
func (a *nativeApp) planCard(c *ui.Context, p palette, m *chatMessage) {
	ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Radius(10).Background(p.Thought).
		Border(1, p.T.Border).Padding(10, 12).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconSpark).FontSize(13).TextColor(p.T.Accent)
			label := m.PlanTitle
			if label == "" {
				label = Tf("执行计划 · %d 步", len(m.Plan))
			}
			ui.Text(c, label).FontSize(11).FontWeight(650).TextColor(p.T.TextMuted)
		})
		if m.PlanNote != "" {
			ui.Text(c, m.PlanNote).FontSize(10.5).TextColor(p.T.TextMuted).LineHeight(1.35)
		}
		for _, entry := range m.Plan {
			entry := entry
			ui.Row(c).AlignItems(ui.Start).Gap(7).Children(func() {
				planStepMarker(c, p, entry.Status)
				t := ui.Text(c, entry.Content).FontSize(11.5).TextColor(p.T.Text).Grow(1)
				if entry.Status == "completed" {
					t.Strikethrough().TextColor(p.T.TextMuted)
				}
			})
		}
	})
}

// chatImageMaxHeight bounds an inline image's thumbnail so a tall portrait
// cannot dominate the transcript.
const chatImageMaxHeight = 260

// boundedImageSize scales w×h into maxW×maxH while preserving its aspect
// ratio. It never upscales: a 16×16 icon stays 16×16 instead of becoming a
// blurry 260×260 block.
func boundedImageSize(w, h, maxW, maxH float32) (displayW, displayH float32) {
	if w <= 0 || h <= 0 {
		return maxW, maxH
	}
	scale := float32(1)
	if maxW > 0 && w > maxW {
		scale = maxW / w
	}
	if maxH > 0 && h*scale > maxH {
		scale = maxH / h
	}
	return w * scale, h * scale
}

// renderChatImages projects bounded inline images carried by canonical content.
// No path scanning or artifact inference happens here.
func (a *nativeApp) renderChatImages(c *ui.Context, p palette, images []chatImage, maxWidth float32) {
	if len(images) == 0 {
		return
	}
	ui.Row(c).Wrap().Gap(6).Margin(6, 0, 0, 0).Children(func() {
		for _, image := range images {
			if image.Image == nil {
				continue
			}
			w, h := ui.ImageSize(image.Image)
			if w <= 0 || h <= 0 {
				ui.Image(c, image.Image).Size(maxWidth, 220).Fit(ui.Cover).Radius(8).
					Border(1, p.T.Border)
				continue
			}
			displayW, displayH := boundedImageSize(w, h, maxWidth, chatImageMaxHeight)
			ui.Image(c, image.Image).Size(displayW, displayH).Fit(ui.Contain).Radius(8).
				Border(1, p.T.Border).Label(T("图片"))
		}
	})
}

// transcriptContentWidth estimates the usable transcript row width from the
// window and the shell's fixed chrome. Inline images get an explicit size from
// it instead of a percentage width: a virtualized List measures a row before
// the sibling plan panel settles, and a percentage width plus AspectRatio can
// cache a squashed image height from that probe pass.
func (a *nativeApp) transcriptContentWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	if a.sidebar {
		width -= nativeSidebarWidth
	}
	if plan := a.activePlanMessage(); plan != nil {
		width -= a.planPanelWidth(plan)
	}
	// Transcript list horizontal padding and scrollbar/rounding headroom.
	width -= 48 + 12
	if width < 160 {
		width = 160
	}
	return width
}

func (a *nativeApp) userImageMaxWidth(c *ui.Context) float32 {
	// The user bubble is at most 78% of the row (and 560 DIP), with 13 DIP
	// padding on either side.
	width := min(a.transcriptContentWidth(c)*0.78, 560) - 26
	return clampWidth(width, 80, 360)
}

func (a *nativeApp) assistantImageMaxWidth(c *ui.Context, requested, inset float32) float32 {
	avail := min(a.transcriptContentWidth(c), 780) - inset
	return clampWidth(min(avail, requested), 80, 0)
}

// clampWidth clamps value into [lower, upper], where a non-positive upper means
// no upper bound.
func clampWidth(value, lower, upper float32) float32 {
	if value < lower {
		value = lower
	}
	if upper > 0 && value > upper {
		value = upper
	}
	return value
}

// formatThoughtDuration renders reasoning time compactly: 0.4s, 12s, or
// 1m 05s. It stays language neutral so the header needs no extra translation.
// thoughtPreview projects the compact tail of a still-streaming thought for
// the collapsed live row, mirroring Desktop's 600-character preview window.
func attachmentDetailLabel(f attachment) string {
	parts := make([]string, 0, 2)
	if f.MimeType != "" {
		parts = append(parts, f.MimeType)
	}
	if f.Size > 0 {
		parts = append(parts, humanBytes(f.Size))
	}
	return strings.Join(parts, " · ")
}

// Keep markdown imported here: the view owns the Markdown options shared by
// every message row.
var _ markdown.Options
