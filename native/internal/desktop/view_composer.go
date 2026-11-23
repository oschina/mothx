package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *nativeApp) composer(c *ui.Context, p palette) {
	ui.Column(c).Padding(0, 24, 16, 24).Children(func() {
		ui.Column(c).MaxWidth(820).WidthPercent(100).Margin(0, ui.Auto).Gap(7).Children(func() {
			if len(a.attachments) > 0 {
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for i, f := range a.attachments {
						i, f := i, f
						ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(4, 8).Radius(7).
							Background(p.Chip).Border(1, p.T.Border).Children(func() {
							if f.Preview != nil {
								ui.Image(c, f.Preview).Size(30, 30).Fit(ui.Cover).Radius(5)
							} else {
								ui.Icon(c, iconPaperclip).FontSize(11).TextColor(p.T.TextMuted)
							}
							ui.Column(c).Gap(1).Children(func() {
								label := f.Name
								if f.Embedded {
									label += T("（内嵌）")
								}
								ui.Text(c, label).FontSize(10.5).TextColor(p.T.Text).SingleLine().MaxWidth(180)
								details := attachmentDetailLabel(f)
								if details != "" {
									ui.Text(c, details).FontSize(8.5).TextColor(p.T.TextMuted).SingleLine()
								}
							})
							iconButton(c, p, iconX, "移除附件", 10, func() { a.removeAttachment(i) })
						})
					}
				})
			}
			box := ui.Column(c).Padding(10, 10, 8, 10).Radius(15).Background(p.Composer).
				Border(1, p.T.Border).Gap(2)
			if !a.dark {
				box.Shadow(0, 6, 22, 0, ui.RGBA(20, 24, 40, 0.06))
			}
			box.Children(func() {
				in := ui.TextAreaBase(c.Key("composer"), &a.draft).Placeholder(T("描述你想要构建的内容，/ 调用指令，@ 文件或对话")).
					FontSize(12.8).LineHeight(1.45).Lines(1, 8).Padding(2, 4)
				in.OnPaste(func(text string) bool {
					if strings.TrimSpace(text) != "" {
						return false
					}
					return a.attachClipboardImage()
				})
				in.OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && k.Mods == 0 {
						text := strings.TrimSpace(k.Text())
						if text == "" || a.running {
							return true
						}
						k.SetText("")
						a.draft = text
						a.send()
						return true
					}
					return false
				})
				ui.Row(c).AlignItems(ui.Center).Gap(4).Padding(2, 0, 0, 0).Children(func() {
					a.attachmentMenu(c, p)
					a.capabilitiesMenu(c, p)
					a.modeMenu(c, p)
					// The workspace picker projects the shell's recent-directory
					// history (Desktop's desktop-store convenience) plus the native
					// picker. Selection semantics stay in applyWorkspaceChoice.
					workspaceBtn := ui.PopoverMenuButton(c.Key("workspace-picker"), a.workspaceLabel(), func(m *ui.Menu) {
						if m.Item(T("选择目录") + "…").ID("workspace:choose").Chosen() {
							a.chooseWorkspace()
						}
						if len(a.recentWorkspaces) > 0 {
							m.Separator()
							for _, entry := range a.recentWorkspaces {
								entry := entry
								if m.Item(entry).ID("workspace:recent:" + entry).Chosen() {
									a.applyWorkspaceChoice(entry)
								}
							}
						}
					}, composerMenuOptions...).FontSize(10.5).TextColor(p.T.TextMuted).Padding(4, 6).Radius(7)
					workspaceBtn.Tooltip(T("已打开会话时修改会话工作目录；否则选择下一个新会话目录"))
					ui.Spacer(c)
					a.expertMenu(c, p)
					a.modelMenu(c, p)
					a.sendButton(c, p)
				})
			})
			ui.Row(c).AlignItems(ui.Center).Padding(0, 3).Children(func() {
				if a.status != "" {
					ui.Text(c, T(a.status)).FontSize(9.8).TextColor(p.T.TextMuted).SingleLine().Grow(1)
				} else {
					ui.Text(c, T("Enter 发送 · Shift + Enter 换行")).FontSize(9.5).
						TextColor(p.T.TextMuted).Grow(1)
				}
				if a.usageUsed > 0 && a.usageSize > 0 {
					ui.Textf(c, "%s tokens", humanCount(a.usageUsed)).FontSize(9.5).TextColor(p.T.TextMuted)
				}
			})
		})
	})
}

func (a *nativeApp) sendButton(c *ui.Context, p palette) {
	if a.running {
		b := ui.ButtonBase(c.Key("stop")).Size(30, 30).Radius(999).Justify(ui.Center).AlignItems(ui.Center).
			Background(p.T.Text)
		if b.Hovered() {
			b.Background(p.T.TextMuted)
		}
		b.Children(func() {
			ui.Icon(c, iconStop).FontSize(13).TextColor(p.T.Background)
		})
		b.Label(T("停止")).Tooltip(T("停止生成"))
		if b.Clicked() {
			a.stop()
		}
		return
	}
	empty := strings.TrimSpace(a.draft) == "" || a.conn != connReady
	b := ui.ButtonBase(c.Key("send")).Size(30, 30).Radius(999).Justify(ui.Center).AlignItems(ui.Center).
		Background(p.T.Accent).Disabled(empty)
	if b.Hovered() && !empty {
		b.Background(p.T.AccentHover)
	}
	b.Children(func() {
		ui.Icon(c, iconArrowUp).FontSize(15).TextColor(p.T.AccentText)
	})
	b.Label(T("发送")).Tooltip(T("发送消息"))
	if b.Clicked() && !empty {
		a.send()
	}
}
