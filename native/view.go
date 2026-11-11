package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type palette struct {
	t        *ui.Theme
	sidebar  ui.Color
	bubble   ui.Color
	hover    ui.Color
	composer ui.Color
	chip     ui.Color
	thought  ui.Color
	active   ui.Color
	errorBg  ui.Color
	warnBg   ui.Color
	okBg     ui.Color
}

func (a *nativeApp) applyTheme(c *ui.Context) palette {
	if !a.themeSet {
		a.dark = c.Theme().Dark
		a.themeSet = true
	}
	t := ui.DarkTheme()
	if !a.dark {
		t = ui.LightTheme()
	}
	t.FontSize = 13
	t.Radius = 8
	t.Spacing = 4
	t.Accent = ui.Hex("#4d6bfe")
	t.AccentHover = ui.Hex("#6480ff")
	t.AccentPressed = ui.Hex("#3f5ce8")
	t.AccentText = ui.Hex("#ffffff")
	t.InactiveAccent = t.Accent
	t.Focus = ui.RGBA(77, 107, 254, 0.65)
	t.Selection = ui.RGBA(77, 107, 254, 0.30)
	t.Scrollbar = ui.RGBA(127, 133, 148, 0.45)
	if a.dark {
		t.Background = ui.Hex("#191a1f")
		t.Surface = ui.Hex("#24262d")
		t.SurfaceHover = ui.Hex("#2c2e36")
		t.SurfacePressed = ui.Hex("#343740")
		t.Border = ui.Hex("#30323b")
		t.Text = ui.Hex("#eceef4")
		t.TextMuted = ui.Hex("#8d929e")
		t.Danger = ui.Hex("#ff6b6b")
		t.Warning = ui.Hex("#f4b860")
		t.Success = ui.Hex("#5ad19a")
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f4f4f6")
		t.SurfaceHover = ui.Hex("#ececef")
		t.SurfacePressed = ui.Hex("#e2e2e6")
		t.Border = ui.Hex("#e6e6ea")
		t.Text = ui.Hex("#1b1c20")
		t.TextMuted = ui.Hex("#6c707a")
		t.Danger = ui.Hex("#d92d20")
		t.Warning = ui.Hex("#c77700")
		t.Success = ui.Hex("#14875a")
	}
	c.SetTheme(t)
	p := palette{t: t}
	if a.dark {
		p.sidebar = ui.Hex("#121318")
		p.bubble = ui.Hex("#2a2c34")
		p.hover = ui.Hex("#23252c")
		p.composer = ui.Hex("#202127")
		p.chip = ui.Hex("#2a2c34")
		p.thought = ui.Hex("#1d1f25")
		p.active = ui.RGBA(77, 107, 254, 0.18)
		p.errorBg = ui.RGBA(255, 107, 107, 0.10)
		p.warnBg = ui.RGBA(244, 184, 96, 0.10)
		p.okBg = ui.RGBA(90, 209, 154, 0.10)
	} else {
		p.sidebar = ui.Hex("#f7f7f9")
		p.bubble = ui.Hex("#f1f2f4")
		p.hover = ui.Hex("#ececef")
		p.composer = ui.Hex("#ffffff")
		p.chip = ui.Hex("#f0f0f3")
		p.thought = ui.Hex("#f7f7f9")
		p.active = ui.RGBA(77, 107, 254, 0.10)
		p.errorBg = ui.RGBA(217, 45, 32, 0.07)
		p.warnBg = ui.RGBA(199, 119, 0, 0.08)
		p.okBg = ui.RGBA(20, 135, 90, 0.08)
	}
	return p
}

func (a *nativeApp) view(c *ui.Context) {
	p := a.applyTheme(c)
	bar := c.TitleBar()
	ui.Row(c).Fill().Background(p.t.Background).Children(func() {
		if a.sidebar {
			a.sidebarView(c, p, bar)
		}
		if a.settingsOpen {
			a.settingsPage(c, p, bar)
		} else if a.historyOpen {
			a.historyView(c, p, bar)
		} else {
			a.mainView(c, p, bar)
		}
	})
	a.decisionView(c, p)
	if a.settingsOpen {
		a.settingsProviderDialogs(c, p)
		a.settingsKnowledgeDialogs(c, p)
		a.settingsMCPDialogs(c, p)
		a.settingsSkillHubDialogs(c, p)
		a.settingsExpertsDialogs(c, p)
		a.settingsCronDialogs(c, p)
	}
}

func (a *nativeApp) sidebarView(c *ui.Context, p palette, bar ui.TitleBar) {
	ui.Column(c).Width(272).FillHeight().Background(p.sidebar).
		BorderWidth(0, 1, 0, 0).BorderColor(p.t.Border).Children(func() {
		top := float32(bar.Height)
		if top < 8 {
			top = 8
		}
		ui.Box(c).Height(top)
		ui.Column(c).Padding(10, 12, 8, 12).Grow(1).Children(func() {
			a.brandRow(c, p)
			a.newChatButton(c, p)
			a.navRow(c, p, "search", iconSearch, "搜索会话", a.searching, func() {
				a.searching = !a.searching
				a.history = false
				if !a.searching {
					a.search = ""
				}
			})
			a.navRow(c, p, "history", iconList, "会话历史", a.history, func() {
				if a.settingsOpen {
					a.closeSettings()
				}
				a.history = true
				a.historyOpen = true
				a.searching = false
				a.search = ""
				go a.refreshSessions(a.acp, a.workspace, a.featureSnapshot())
			})
			a.navRow(c, p, "settings", iconSettings, "应用设置", a.settingsOpen, a.openSettings)
			ui.Box(c).Height(1).Background(p.t.Border).Margin(10, 2, 8, 2)
			a.workspaceHeader(c, p)
			if a.searching {
				ui.TextInput(c.Key("session-search"), &a.search).Label("搜索会话").
					Placeholder("搜索会话或工作区").
					FontSize(12).Height(30).Padding(0, 8).Margin(2, 2, 6, 2).
					Background(p.chip).Radius(8).Border(1, p.t.Border).AutoFocus()
			}
			a.sessionList(c, p)
			a.sidebarFooter(c, p)
		})
	})
}

func (a *nativeApp) appearanceIcon() *ui.SVG {
	if a.dark {
		return iconSun
	}
	return iconMoon
}

func (a *nativeApp) brandRow(c *ui.Context, p palette) {
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(4, 4, 14, 4).Children(func() {
		mothxLogoView(c, 26)
		ui.Text(c, "mothx").FontSize(16.5).FontWeight(650)
		badge(c, p, "NATIVE")
		ui.Spacer(c)
		if iconButton(c, p, iconPanelLeft, "收起侧栏", 15, func() { a.sidebar = false }) {
		}
	})
}

func (a *nativeApp) newChatButton(c *ui.Context, p palette) {
	b := ui.ButtonBase(c.Key("new-chat")).Height(38).Radius(10).Gap(7).
		AlignItems(ui.Center).Justify(ui.Center).Background(p.chip).Border(1, p.t.Border)
	if b.Hovered() {
		b.Background(p.t.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, iconPlus).FontSize(15).TextColor(p.t.Text)
		ui.Text(c, "新会话").FontSize(12.5).FontWeight(550).TextColor(p.t.Text)
	})
	b.Label("新会话").Tooltip("在当前工作区开始新会话")
	if b.Clicked() {
		a.newChat()
	}
}

func (a *nativeApp) navRow(c *ui.Context, p palette, key string, icon *ui.SVG, label string, active bool, action func()) {
	b := ui.ButtonBase(c.Key("nav:"+key)).Height(32).Padding(6, 8).Radius(8).Gap(8).
		AlignItems(ui.Center).Justify(ui.Start).Margin(3, 0, 0, 0)
	if active {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.TextMuted
		if active {
			color = p.t.Accent
		}
		ui.Icon(c, icon).FontSize(15).TextColor(color)
		ui.Text(c, label).FontSize(12.5).TextColor(p.t.Text)
		if active {
			ui.Spacer(c)
			ui.Icon(c, iconCheck).FontSize(12).TextColor(p.t.Accent)
		}
	})
	b.Label(label)
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) workspaceHeader(c *ui.Context, p palette) {
	ui.Row(c).AlignItems(ui.Center).Padding(8, 4, 2, 4).Gap(2).Children(func() {
		ui.Text(c, "工作区").FontSize(10.5).FontWeight(600).LetterSpacing(0.5).TextColor(p.t.TextMuted)
		ui.Spacer(c)
		iconButton(c, p, iconSearch, "搜索会话", 13, func() {
			a.searching = !a.searching
			if !a.searching {
				a.search = ""
			}
		})
		iconButton(c, p, iconPlus, "打开文件夹", 14, a.chooseWorkspace)
	})
}

func (a *nativeApp) sessionList(c *ui.Context, p palette) {
	ui.Scroll(c).Grow(1).Margin(0, -4, 0, -4).Padding(0, 4, 0, 4).Children(func() {
		groups := a.groups()
		if len(groups) == 0 {
			if !a.sessionsLoaded {
				ui.Text(c, "正在读取会话…").FontSize(11).TextColor(p.t.TextMuted).Padding(8, 8)
			} else if a.sessionsErr != "" {
				ui.Text(c, "会话列表不可用").FontSize(11).TextColor(p.t.Danger).Padding(8, 8)
			} else {
				ui.Text(c, "还没有会话").FontSize(11).TextColor(p.t.TextMuted).Padding(8, 8)
			}
		}
		current := a.workspace
		if current == "" {
			current = a.draftCwd
		}
		for _, g := range groups {
			cwd := g.Cwd
			expanded := true
			if collapsed, ok := a.collapsed[cwd]; ok {
				expanded = !collapsed
			} else if a.search == "" {
				expanded = cwd == current
			}
			ui.Row(c).AlignItems(ui.Center).Gap(1).Margin(4, 0, 2, 0).Children(func() {
				b := ui.ButtonBase(c.Key("workspace:"+cwd)).Grow(1).Height(28).Padding(4, 6).Radius(7).
					Gap(6).AlignItems(ui.Center)
				if b.Hovered() {
					b.Background(p.hover)
				}
				b.Children(func() {
					if expanded {
						ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.t.TextMuted)
					} else {
						ui.Icon(c, iconChevronRight).FontSize(11).TextColor(p.t.TextMuted)
					}
					ui.Icon(c, iconFolder).FontSize(14).TextColor(p.t.TextMuted)
					ui.Text(c, g.Name).FontSize(11.5).FontWeight(600).TextColor(p.t.Text).SingleLine().Grow(1)
					ui.Textf(c, "%d", len(g.Sessions)).FontSize(10.5).TextColor(p.t.TextMuted)
				})
				b.Label("工作区 " + g.Name)
				if b.Clicked() {
					a.collapsed[cwd] = expanded
				}
				iconButton(c, p, iconPlus, "在此工作区新建会话", 12, func() {
					a.newChatIn(cwd)
				})
			})
			if !expanded {
				continue
			}
			if len(g.Sessions) == 0 {
				ui.Text(c, "尚未开始对话").FontSize(10.5).TextColor(p.t.TextMuted).Padding(6, 22)
			}
			for _, s := range g.Sessions {
				a.sessionRow(c, p, s)
			}
		}
	})
}

func (a *nativeApp) sessionRow(c *ui.Context, p palette, s listedSession) {
	active := s.SessionID == a.currentSession
	b := ui.ButtonBase(c.Key("session:"+s.SessionID)).Height(30).Padding(5, 8).Radius(8).
		Gap(7).AlignItems(ui.Center).Margin(0, 0, 1, 0)
	if active {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.TextMuted
		if active {
			color = p.t.Accent
		}
		ui.Icon(c, iconChat).FontSize(13).TextColor(color)
		title := s.Title
		if title == "" {
			title = "未命名会话"
		}
		textColor := p.t.Text
		if active {
			textColor = p.t.Accent
		}
		ui.Text(c, title).FontSize(12).TextColor(textColor).SingleLine().Grow(1)
		if t := relativeTime(s.UpdatedAt); t != "" {
			ui.Text(c, t).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
		}
	})
	b.Label(s.Title).Tooltip(s.Cwd)
	if b.Clicked() {
		a.selectSession(s)
	}
}

func (a *nativeApp) sidebarFooter(c *ui.Context, p palette) {
	ui.Box(c).Height(1).Background(p.t.Border).Margin(8, 2, 6, 2)
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(5, 6).Children(func() {
		ui.Box(c).Size(24, 24).Radius(999).Background(p.active).Children(func() {
			mothxLogoView(c, 20)
		})
		ui.Column(c).Gap(1).Grow(1).Children(func() {
			ui.Text(c, "MothX Native").FontSize(11.5).FontWeight(600).SingleLine()
			ui.Text(c, a.connectionLabel()).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
		})
		iconButton(c, p, a.appearanceIcon(), "切换外观", 14, func() { a.dark = !a.dark })
		dot := p.t.Success
		switch a.conn {
		case connFailed:
			dot = p.t.Danger
		case connStarting:
			dot = p.t.Warning
		}
		ui.Box(c).Size(6, 6).Radius(3).Background(dot)
	})
}

func (a *nativeApp) mainView(c *ui.Context, p palette, bar ui.TitleBar) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		a.header(c, p, bar)
		if len(a.messages) == 0 {
			a.emptyState(c, p)
		} else {
			a.transcript(c, p)
		}
		a.composer(c, p)
	})
}

func (a *nativeApp) header(c *ui.Context, p palette, bar ui.TitleBar) {
	right := float32(12 + bar.Right)
	left := float32(18 + bar.Left)
	ui.Row(c).Height(52).Padding(0, right, 0, left).AlignItems(ui.Center).Gap(6).
		BorderWidth(0, 0, 1, 0).BorderColor(p.t.Border).Background(p.t.Background).Children(func() {
		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, a.sessionTitle()).FontSize(12.5).FontWeight(600).SingleLine().MaxWidth(520)
			if a.currentSession != "" {
				ui.Text(c, a.workspaceName()).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			}
		})
		ui.Spacer(c)
		if usage := a.usageLabel(); usage != "" {
			ui.Text(c, usage).FontSize(10).TextColor(p.t.TextMuted).Margin(0, 8, 0, 4)
		}
		iconButton(c, p, a.appearanceIcon(), "切换外观", 15, func() { a.dark = !a.dark })
		if a.sidebar {
			iconButton(c, p, iconPanelLeft, "收起侧栏", 15, func() { a.sidebar = false })
		} else {
			iconButton(c, p, iconPanelLeft, "展开侧栏", 15, func() { a.sidebar = true })
		}
	})
}

func (a *nativeApp) emptyState(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
		if a.conn == connFailed {
			a.connectionError(c, p)
			return
		}
		mothxLogoView(c, 78)
		ui.Row(c).AlignItems(ui.Center).Gap(9).Margin(10, 0, 0, 0).Children(func() {
			ui.Text(c, "MothX").FontSize(27).FontWeight(650)
			badge(c, p, "预览版")
		})
		ui.Text(c, nativeChineseSlogan).FontSize(11.5).
			LetterSpacing(0.2).TextColor(p.t.TextMuted).Margin(4, 0, 0, 0)
		subtitle := "连接 MothX ACP，开始一段纯原生对话"
		if a.conn == connStarting {
			subtitle = "正在启动 MothX ACP…"
		}
		ui.Text(c, subtitle).FontSize(12).TextColor(p.t.TextMuted).Margin(6, 0, 0, 0)
		if a.workspaceName() != "" {
			ui.Row(c).AlignItems(ui.Center).Gap(5).Margin(12, 0, 0, 0).Children(func() {
				ui.Icon(c, iconFolder).FontSize(12).TextColor(p.t.TextMuted)
				ui.Text(c, a.workspaceName()).FontSize(10.5).TextColor(p.t.TextMuted)
			})
		}
	})
}

func (a *nativeApp) connectionError(c *ui.Context, p palette) {
	ui.Column(c).Width(460).Padding(18).Radius(14).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(18).TextColor(p.t.Danger)
			ui.Text(c, "未连接到 MothX ACP").FontSize(14).FontWeight(650)
		})
		if a.connErr != "" {
			ui.Text(c, a.connErr).FontSize(11.5).TextColor(p.t.TextMuted).Selectable()
		}
		if len(a.stderr) > 0 {
			ui.Text(c, strings.Join(a.stderr, "\n")).Font("monospace").FontSize(9.5).
				TextColor(p.t.TextMuted).MaxLines(5).Selectable()
		}
		if ui.PrimaryButton(c.Key("retry-connection"), "重试连接").Clicked() {
			go a.connect()
		}
	})
}

func (a *nativeApp) transcript(c *ui.Context, p palette) {
	a.list.Key = func(i int) any { return a.messages[i].ID }
	a.list.FollowEnd = true
	a.list.FollowAnimation = &ui.Smooth
	ui.List(c, &a.list, len(a.messages), func(i int) {
		a.messageRow(c, p, a.messages[i])
	}).Grow(1).Padding(22, 24, 10, 24).Gap(18)
}

func (a *nativeApp) messageRow(c *ui.Context, p palette, m *chatMessage) {
	switch m.Kind {
	case messageUser:
		ui.Row(c).Key(m.ID).Children(func() {
			ui.Spacer(c)
			ui.Column(c).MaxWidthPercent(78).MaxWidth(560).Padding(9, 13).Radius(15).
				Background(p.bubble).Children(func() {
				ui.Text(c, m.Text).FontSize(12.8).LineHeight(1.5).Selectable()
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
			} else if !m.Done {
				ui.Text(c, "正在生成…").FontSize(12).TextColor(p.t.TextMuted)
			}
			if m.Done && m.Text != "" {
				ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
					copyButton(c, p, m.Text)
				})
			}
		})
	case messageThought:
		ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Gap(3).Children(func() {
			b := ui.ButtonBase(c.Key("thought:"+m.ID)).Padding(1, 0).Radius(6).AlignItems(ui.Center).Gap(5)
			b.Children(func() {
				if m.Collapsed {
					ui.Icon(c, iconChevronRight).FontSize(11).TextColor(p.t.TextMuted)
				} else {
					ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.t.TextMuted)
				}
				label := "已思考"
				if !m.Done {
					label = "思考中…"
				}
				ui.Text(c, label).FontSize(11.5).TextColor(p.t.TextMuted)
			})
			b.Label("已思考")
			if b.Clicked() {
				m.Collapsed = !m.Collapsed
			}
			if !m.Collapsed {
				ui.Column(c).Padding(9, 12).Radius(10).Background(p.thought).
					Border(1, p.t.Border).Children(func() {
					ui.Text(c, m.Text).FontSize(11.5).LineHeight(1.5).
						TextColor(p.t.TextMuted).Selectable()
				})
			}
		})
	case messageTool:
		a.toolCard(c, p, m)
	case messagePlan:
		a.planCard(c, p, m)
	case messageArtifact:
		ui.Row(c).Key(m.ID).AlignItems(ui.Center).Gap(8).Padding(9, 12).Radius(10).
			Background(p.thought).Border(1, p.t.Border).Children(func() {
			ui.Icon(c, iconFile).FontSize(16).TextColor(p.t.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, m.Filename).FontSize(12).FontWeight(550).SingleLine()
				ui.Text(c, "生成制品"+(mediaLabel(m.MediaType))).FontSize(9.5).TextColor(p.t.TextMuted)
			})
		})
	default:
		color := p.t.TextMuted
		if strings.HasPrefix(m.Text, "发送失败") || strings.HasPrefix(m.Text, "载入会话失败") {
			color = p.t.Danger
		}
		ui.Text(c.Key(m.ID), m.Text).FontSize(11).TextColor(color).
			Margin(0, ui.Auto).Selectable()
	}
}

func (a *nativeApp) toolCard(c *ui.Context, p palette, m *chatMessage) {
	status := m.ToolStatus
	if status == "" {
		status = "pending"
	}
	ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Radius(10).Background(p.thought).
		Border(1, p.t.Border).Children(func() {
		b := ui.ButtonBase(c.Key("tool:"+m.ToolID)).Padding(8, 10).Gap(8).AlignItems(ui.Center)
		b.Children(func() {
			icon := iconWrench
			if m.ToolKind == "execute" {
				icon = iconTerminal
			}
			ui.Icon(c, icon).FontSize(14).TextColor(p.t.TextMuted)
			ui.Text(c, m.ToolTitle).FontSize(11.5).TextColor(p.t.Text).SingleLine().Grow(1)
			statusPill(c, p, status)
			if m.ToolText != "" {
				if m.ToolExpanded {
					ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.t.TextMuted)
				} else {
					ui.Icon(c, iconChevronRight).FontSize(11).TextColor(p.t.TextMuted)
				}
			}
		})
		b.Label(m.ToolTitle)
		if b.Clicked() && m.ToolText != "" {
			m.ToolExpanded = !m.ToolExpanded
		}
		if m.ToolExpanded && m.ToolText != "" {
			ui.Box(c).Height(1).Background(p.t.Border)
			if len(m.ToolText) > 1600 {
				ui.Scroll(c).Height(240).Padding(9, 10).Children(func() {
					ui.Text(c, truncate(m.ToolText, 20000)).Font("monospace").FontSize(10).LineHeight(1.4).
						TextColor(p.t.TextMuted).Selectable()
				})
			} else {
				ui.Text(c, truncate(m.ToolText, 12000)).Font("monospace").FontSize(10).LineHeight(1.4).
					TextColor(p.t.TextMuted).Padding(9, 10).Selectable()
			}
		}
	})
}

func (a *nativeApp) planCard(c *ui.Context, p palette, m *chatMessage) {
	ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Radius(10).Background(p.thought).
		Border(1, p.t.Border).Padding(10, 12).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconSpark).FontSize(13).TextColor(p.t.Accent)
			ui.Text(c, "计划").FontSize(11).FontWeight(650).TextColor(p.t.TextMuted)
		})
		for _, entry := range m.Plan {
			entry := entry
			ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
				color := p.t.TextMuted
				switch entry.Status {
				case "completed":
					color = p.t.Success
				case "in_progress", "running":
					color = p.t.Accent
				case "failed":
					color = p.t.Danger
				}
				ui.Box(c).Size(5, 5).Radius(3).Background(color)
				t := ui.Text(c, entry.Content).FontSize(11.5).TextColor(p.t.Text).Grow(1)
				if entry.Status == "completed" {
					t.Strikethrough().TextColor(p.t.TextMuted)
				}
			})
		}
	})
}

func (a *nativeApp) composer(c *ui.Context, p palette) {
	ui.Column(c).Padding(0, 24, 16, 24).Children(func() {
		ui.Column(c).MaxWidth(800).WidthPercent(100).Margin(0, ui.Auto).Gap(7).Children(func() {
			if len(a.attachments) > 0 {
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for i, f := range a.attachments {
						i, f := i, f
						ui.Row(c).AlignItems(ui.Center).Gap(4).Padding(4, 8).Radius(7).
							Background(p.chip).Border(1, p.t.Border).Children(func() {
							ui.Icon(c, iconPaperclip).FontSize(11).TextColor(p.t.TextMuted)
							ui.Text(c, f.Name).FontSize(10.5).TextColor(p.t.Text).SingleLine().MaxWidth(180)
							iconButton(c, p, iconX, "移除附件", 10, func() { a.removeAttachment(i) })
						})
					}
				})
			}
			box := ui.Column(c).Padding(10, 10, 8, 10).Radius(15).Background(p.composer).
				Border(1, p.t.Border).Gap(2)
			if !a.dark {
				box.Shadow(0, 6, 22, 0, ui.RGBA(20, 24, 40, 0.06))
			}
			box.Children(func() {
				in := ui.TextAreaBase(c.Key("composer"), &a.draft).Placeholder("描述你想要构建的内容，/ 调用指令，@ 文件或对话").
					FontSize(12.8).LineHeight(1.45).Lines(1, 8).Padding(2, 4)
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
					iconButton(c, p, iconPlus, "附加文件", 15, a.attachFiles)
					b := ui.ButtonBase(c.Key("workspace-picker")).Padding(4, 7).Radius(7).
						Gap(5).AlignItems(ui.Center)
					if b.Hovered() {
						b.Background(p.hover)
					}
					b.Children(func() {
						ui.Icon(c, iconFolder).FontSize(12).TextColor(p.t.TextMuted)
						ui.Text(c, a.workspaceLabel()).FontSize(10.5).TextColor(p.t.TextMuted).SingleLine().MaxWidth(200)
						ui.Icon(c, iconChevronDown).FontSize(10).TextColor(p.t.TextMuted)
					})
					b.Label("切换工作区").Tooltip("选择工作区文件夹")
					if b.Clicked() {
						a.chooseWorkspace()
					}
					ui.Spacer(c)
					a.modelMenu(c, p)
					a.sendButton(c, p)
				})
			})
			ui.Row(c).AlignItems(ui.Center).Padding(0, 3).Children(func() {
				if a.status != "" {
					ui.Text(c, a.status).FontSize(9.8).TextColor(p.t.TextMuted).SingleLine().Grow(1)
				} else {
					ui.Text(c, "Enter 发送 · Shift + Enter 换行").FontSize(9.5).
						TextColor(p.t.TextMuted).Grow(1)
				}
				if a.usageUsed > 0 && a.usageSize > 0 {
					ui.Textf(c, "%s tokens", humanCount(a.usageUsed)).FontSize(9.5).TextColor(p.t.TextMuted)
				}
			})
		})
	})
}

func (a *nativeApp) modelMenu(c *ui.Context, p palette) {
	label := a.modelDisplay()
	btn := ui.PopoverMenuButton(c.Key("model-menu"), label, func(m *ui.Menu) {
		model := a.currentModelOption()
		thinking := a.currentThinkingOption()
		added := false
		if model != nil && len(model.Options) > 0 {
			for _, opt := range model.Options {
				opt := opt
				if m.Item(opt.Name).Checked(model.CurrentValue == opt.Value).Chosen() {
					a.setConfigOption("model", opt.Value)
				}
			}
			added = true
		}
		if thinking != nil && len(thinking.Options) > 0 {
			if added {
				m.Separator()
			}
			for _, opt := range thinking.Options {
				opt := opt
				if m.Item("思考：" + opt.Name).Checked(thinking.CurrentValue == opt.Value).Chosen() {
					a.setConfigOption(thinking.ID, opt.Value)
				}
			}
			added = true
		}
		if !added {
			m.Item("使用默认模型").Disabled(true)
		}
	}).FontSize(10.5).TextColor(p.t.TextMuted).Padding(4, 6).Radius(7).Disabled(a.running || a.creatingSession)
	btn.Tooltip("切换模型与思考级别")
}

func (a *nativeApp) sendButton(c *ui.Context, p palette) {
	if a.running {
		b := ui.ButtonBase(c.Key("stop")).Size(30, 30).Radius(999).Justify(ui.Center).AlignItems(ui.Center).
			Background(p.t.Text)
		if b.Hovered() {
			b.Background(p.t.TextMuted)
		}
		b.Children(func() {
			ui.Icon(c, iconStop).FontSize(13).TextColor(p.t.Background)
		})
		b.Label("停止").Tooltip("停止生成")
		if b.Clicked() {
			a.stop()
		}
		return
	}
	empty := strings.TrimSpace(a.draft) == "" || a.conn != connReady
	b := ui.ButtonBase(c.Key("send")).Size(30, 30).Radius(999).Justify(ui.Center).AlignItems(ui.Center).
		Background(p.t.Accent).Disabled(empty)
	if b.Hovered() && !empty {
		b.Background(p.t.AccentHover)
	}
	b.Children(func() {
		ui.Icon(c, iconArrowUp).FontSize(15).TextColor(p.t.AccentText)
	})
	b.Label("发送").Tooltip("发送消息")
	if b.Clicked() && !empty {
		a.send()
	}
}

func (a *nativeApp) decisionView(c *ui.Context, p palette) {
	if !a.decisionOpen {
		return
	}
	d := a.currentDecision()
	if d == nil {
		a.decisionOpen = false
		return
	}
	panel := ui.DialogBase(c, &a.decisionOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		panel.Width(460).Padding(18).Radius(16).Background(p.t.Background).
			Border(1, p.t.Border).Shadow(0, 18, 50, 0, ui.RGBA(0, 0, 0, 0.35)).Gap(10)
		panel.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Warning)
				ui.Text(c, d.Title).FontSize(14).FontWeight(650).Grow(1)
			})
			if d.Prompt != "" && d.Kind != "permission" {
				ui.Text(c, d.Prompt).FontSize(11.8).TextColor(p.t.TextMuted).Selectable()
			}
			if d.Kind == "permission" && d.Prompt != "" {
				ui.Text(c, d.Prompt).Font("monospace").FontSize(10.5).Padding(8, 10).Radius(8).
					Background(p.thought).Border(1, p.t.Border).Selectable()
			}
			if d.Multi {
				for _, opt := range d.Options {
					if ptr := a.questionChoices[opt.ID]; ptr != nil {
						ui.Checkbox(c, ptr, opt.Label).FontSize(11.5)
					}
				}
			} else if d.Kind != "permission" {
				for _, opt := range d.Options {
					opt := opt
					if ui.Button(c.Key("decision-option:"+opt.ID), opt.Label).Clicked() {
						a.answerDecision(opt.ID)
					}
				}
			}
			if d.Kind != "permission" {
				ui.TextInput(c.Key("decision-answer"), &a.questionText).Placeholder("输入回答…").
					FontSize(12).Height(32).Padding(0, 9).Background(p.chip).Radius(8).
					Border(1, p.t.Border)
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				if ui.Button(c.Key("decision-cancel"), "取消").Clicked() {
					a.cancelDecision()
				}
				if d.Kind == "permission" {
					for _, opt := range d.Options {
						opt := opt
						label := opt.Label
						if opt.Kind == "allow_once" || opt.Kind == "allow" {
							if ui.PrimaryButton(c.Key("decision-allow:"+opt.ID), label).Clicked() {
								a.answerDecision(opt.ID)
							}
						} else if ui.Button(c.Key("decision-option:"+opt.ID), label).Clicked() {
							a.answerDecision(opt.ID)
						}
					}
				} else if d.Multi || d.Kind == "elicitation" || len(d.Options) > 0 {
					if ui.PrimaryButton(c.Key("decision-submit"), "确认").Clicked() {
						a.answerDecision("")
					}
				}
			})
		})
	})
	if panel.Dismissed() {
		a.cancelDecision()
	}
}

func iconButton(c *ui.Context, p palette, icon *ui.SVG, label string, size float32, action func()) bool {
	b := ui.ButtonBase(c).Size(28, 28).Radius(7).Justify(ui.Center).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(size).TextColor(p.t.TextMuted)
	})
	b.Label(label).Tooltip(label)
	if b.Clicked() {
		action()
		return true
	}
	return false
}

func badge(c *ui.Context, p palette, text string) {
	ui.Box(c).Padding(2, 6).Radius(4).Border(1, p.t.Border).Children(func() {
		ui.Text(c, text).FontSize(8).FontWeight(700).LetterSpacing(0.7).TextColor(p.t.TextMuted)
	})
}

func statusPill(c *ui.Context, p palette, status string) {
	label := status
	color := p.t.TextMuted
	bg := p.chip
	switch status {
	case "pending":
		label = "等待"
	case "in_progress", "running":
		label = "运行中"
		color = p.t.Accent
		bg = p.active
	case "completed":
		label = "完成"
		color = p.t.Success
		bg = p.okBg
	case "failed":
		label = "失败"
		color = p.t.Danger
		bg = p.errorBg
	case "cancelled":
		label = "已取消"
	case "incomplete":
		label = "未完成"
		color = p.t.Warning
		bg = p.warnBg
	}
	ui.Box(c).Padding(1, 6).Radius(999).Background(bg).Children(func() {
		ui.Text(c, label).FontSize(9).TextColor(color)
	})
}

func copyButton(c *ui.Context, p palette, text string) {
	b := ui.ButtonBase(c).Padding(3, 6).Radius(6).Gap(4).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconFile).FontSize(11).TextColor(p.t.TextMuted)
		ui.Text(c, "复制").FontSize(9.8).TextColor(p.t.TextMuted)
	})
	b.Label("复制").Tooltip("复制回复")
	if b.Clicked() {
		c.Services().WriteClipboard(text)
	}
}

func humanCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
}

func mediaLabel(media string) string {
	if media == "" {
		return ""
	}
	return " · " + media
}

// Keep markdown imported here: the view owns the Markdown options shared by
// every message row.
var _ markdown.Options
