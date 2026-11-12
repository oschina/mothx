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
	a.services = c.Services()
	if !a.themeSet {
		switch a.themeMode {
		case "dark":
			a.dark = true
		case "light":
			a.dark = false
		default:
			a.dark = c.Theme().Dark
		}
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
		} else if a.page == "skills" {
			a.skillsPage(c, p, bar)
		} else if a.page == "automation" {
			a.automationPage(c, p, bar)
		} else if a.page == "library" {
			a.libraryPage(c, p, bar)
		} else {
			a.mainView(c, p, bar)
		}
	})
	if a.previewOpen {
		a.artifactPreviewDialog(c, p)
	}
	if a.settingsOpen {
		a.settingsProviderDialogs(c, p)
		a.settingsMCPDialogs(c, p)
		a.settingsExpertsDialogs(c, p)
	}
	if a.settingsOpen || a.page == "library" {
		a.settingsKnowledgeDialogs(c, p)
	}
	if a.settingsOpen || a.page == "skills" {
		a.settingsSkillHubDialogs(c, p)
	}
	if a.settingsOpen || a.page == "automation" {
		a.settingsCronDialogs(c, p)
	}
	if a.historyPanel.DialogOpen || a.historyPanel.ProjectDialogOpen {
		a.historyDialogs(c, p)
	}
	if a.worktree.Open {
		a.worktreeDialog(c, p)
	}
	if a.router.Open {
		a.routerDialog(c, p)
	}
	if a.subagent.Open {
		a.subagentDialog(c, p)
	}
	a.modelPickerDialog(c, p)
	a.toastView(c, p)
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
			a.navRow(c, p, "history", iconList, "会话历史", a.history, a.openHistory)
			if count := a.backgroundAttentionCount(); count > 0 {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(0, 10, 4, 36).Children(func() {
					ui.Box(c).Padding(1, 6).Radius(5).Border(1, p.t.Warning).Children(func() {
						ui.Text(c, Tf("%d 个后台会话待处理", count)).FontSize(9).FontWeight(600).
							TextColor(p.t.Warning).SingleLine()
					})
				})
			}
			a.navRow(c, p, "skills", iconSpark, "技能", a.page == "skills" && !a.settingsOpen, func() { a.openPage("skills") })
			a.navRow(c, p, "automation", iconClock, "自动化", a.page == "automation" && !a.settingsOpen, func() { a.openPage("automation") })
			a.navRow(c, p, "library", iconBook, "资料库", a.page == "library" && !a.settingsOpen, func() { a.openPage("library") })
			a.navRow(c, p, "settings", iconSettings, "应用设置", a.settingsOpen, a.openSettings)
			ui.Box(c).Height(1).Background(p.t.Border).Margin(10, 2, 8, 2)
			if a.searching {
				ui.TextInput(c.Key("session-search"), &a.search).Label(T("搜索会话")).
					Placeholder(T("搜索会话或工作区")).
					FontSize(12).Height(30).Padding(0, 8).Margin(2, 2, 6, 2).
					Background(p.chip).Radius(8).Border(1, p.t.Border).AutoFocus()
			}
			a.sessionList(c, p)
			a.sidebarFooter(c, p)
		})
	})
}

func (a *nativeApp) toggleTheme() {
	a.dark = !a.dark
	if a.dark {
		a.themeMode = "dark"
	} else {
		a.themeMode = "light"
	}
	a.themeSet = true
	a.saveNativePreferences()
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
		ui.Text(c, T("新会话")).FontSize(12.5).FontWeight(550).TextColor(p.t.Text)
	})
	b.Label(T("新会话")).Tooltip(T("在当前工作区开始新会话"))
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
		ui.Text(c, T(label)).FontSize(12.5).TextColor(p.t.Text)
		if active {
			ui.Spacer(c)
			ui.Icon(c, iconCheck).FontSize(12).TextColor(p.t.Accent)
		}
	})
	b.Label(T(label))
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) taskSectionHeader(c *ui.Context, p palette, title, addLabel string, add func()) {
	ui.Row(c).AlignItems(ui.Center).Height(26).Padding(8, 4, 2, 4).Gap(2).Children(func() {
		ui.Text(c, T(title)).FontSize(10.5).FontWeight(600).LetterSpacing(0.5).TextColor(p.t.TextMuted)
		ui.Spacer(c)
		if add != nil {
			iconButton(c, p, iconPlus, addLabel, 13, add)
		}
	})
}

func (a *nativeApp) sessionList(c *ui.Context, p palette) {
	ui.Scroll(c).Grow(1).Margin(0, -4, 0, -4).Padding(0, 4, 0, 4).Children(func() {
		tree := a.taskTree()
		a.ensureUngroupedPageLoaded()
		if !a.sessionsLoaded && !a.projectsLoaded {
			ui.Text(c, T("正在读取任务…")).FontSize(11).TextColor(p.t.TextMuted).Padding(8, 8)
			return
		}
		if a.sessionsErr != "" {
			ui.Text(c, T("会话列表不可用")).FontSize(11).TextColor(p.t.Danger).Padding(8, 8)
		}

		if a.historyProjectsCapable() {
			add := func() { a.openProjectDialog(projectDialogCreate, "") }
			a.taskSectionHeader(c, p, "项目", "新建项目", add)
			if a.projectsErr != "" {
				ui.Text(c, T(a.projectsErr)).FontSize(10).TextColor(p.t.Danger).Padding(2, 20).LineHeight(1.35)
			}
			if len(tree.Projects) == 0 {
				text := "尚未创建项目"
				if !a.projectsLoaded {
					text = "正在读取项目…"
				}
				ui.Text(c, T(text)).FontSize(10.5).TextColor(p.t.TextMuted).Padding(6, 20)
			}
			for _, branch := range tree.Projects {
				a.projectBranchRow(c, p, branch)
			}
		}

		if len(tree.Recent) > 0 {
			a.taskSectionHeader(c, p, "最近", "", nil)
			for _, session := range tree.Recent {
				a.sessionRow(c, p, session, "recent")
			}
		}

		a.taskSectionHeader(c, p, "未归类", "", nil)
		if len(tree.Ungrouped) == 0 {
			text := "没有未归类的会话"
			if a.search != "" {
				text = "没有匹配的会话"
			}
			ui.Text(c, T(text)).FontSize(10.5).TextColor(p.t.TextMuted).Padding(6, 20)
		}
		for _, session := range tree.Ungrouped {
			a.sessionRow(c, p, session, "ungrouped")
		}
		a.sessionPageFooter(c, p, tree.UngroupedPage, func() { a.loadMoreUngroupedSessions() })

		if len(tree.Projects) == 0 && len(tree.Recent) == 0 && len(tree.Ungrouped) == 0 && a.sessionsErr == "" {
			ui.Text(c, T("还没有会话")).FontSize(11).TextColor(p.t.TextMuted).Padding(8, 8)
		}
	})
}

func (a *nativeApp) projectBranchRow(c *ui.Context, p palette, branch sidebarProjectBranch) {
	project := branch.Project
	expanded := a.expandedProjects[project.ID]
	ui.Row(c).AlignItems(ui.Center).Gap(1).Margin(2, 0, 1, 0).Children(func() {
		b := ui.ButtonBase(c.Key("project:"+project.ID)).Grow(1).Height(28).Padding(4, 6).Radius(7).
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
			ui.Text(c, project.Name).FontSize(11.5).FontWeight(600).TextColor(p.t.Text).SingleLine().Grow(1)
			count := len(branch.Sessions)
			if project.SessionCount != nil {
				count = *project.SessionCount
			}
			ui.Textf(c, "%d", count).FontSize(10.5).TextColor(p.t.TextMuted)
		})
		b.Label(T("项目") + " " + project.Name)
		if b.Clicked() {
			a.expandedProjects[project.ID] = !expanded
		}
		a.projectMenu(c, p, project, "sidebar")
	})
	if !expanded {
		return
	}
	if !branch.Page.Loaded && !branch.Page.Loading {
		a.ensureProjectPageLoaded(project.ID)
	}
	if len(branch.Sessions) == 0 {
		ui.Text(c, T("该项目还没有会话")).FontSize(10.5).TextColor(p.t.TextMuted).Padding(6, 24)
	}
	for _, session := range branch.Sessions {
		a.sessionRow(c, p, session, "project:"+project.ID)
	}
	a.sessionPageFooter(c, p, branch.Page, func() { a.loadMoreProjectSessions(project.ID) })
}

// sessionPageFooter renders one branch's pagination state. Loading/error rows
// stay inside the branch so a failed project page never blanks the task tree.
func (a *nativeApp) sessionPageFooter(c *ui.Context, p palette, page sessionPageStatus, loadMore func()) {
	if page.Err != "" {
		ui.Text(c, T(page.Err)).FontSize(9.5).TextColor(p.t.Danger).Padding(2, 24).LineHeight(1.3)
	}
	if !page.Loaded && !page.Loading && page.NextCursor == "" {
		return
	}
	if page.Loading {
		ui.Row(c).AlignItems(ui.Center).Gap(5).Padding(3, 24).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(10).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在加载更多…")).FontSize(9.5).TextColor(p.t.TextMuted)
		})
		return
	}
	if page.NextCursor == "" {
		return
	}
	b := ui.Button(c.Key("session-more:"+page.NextCursor), T("加载更多")).Label(T("加载更多会话"))
	if b.Clicked() {
		loadMore()
	}
}

func (a *nativeApp) sessionRow(c *ui.Context, p palette, s listedSession, scope string) {
	active := s.SessionID == a.currentSession
	ui.Row(c).AlignItems(ui.Center).Gap(1).Margin(0, 0, 1, 0).Children(func() {
		b := ui.ButtonBase(c.Key("session:"+scope+":"+s.SessionID)).Grow(1).Height(30).Padding(5, 7).Radius(8).
			Gap(6).AlignItems(ui.Center)
		if active {
			b.Background(p.active)
		} else if b.Hovered() {
			b.Background(p.hover)
		}
		b.Children(func() {
			sessionStatusDot(c, p, a.sessionStatusKind(s))
			title := sessionDisplayTitle(s.Title)
			if title == "" {
				title = T("未命名会话")
			}
			textColor := p.t.Text
			if active {
				textColor = p.t.Accent
			}
			ui.Text(c, title).FontSize(12).TextColor(textColor).SingleLine().Grow(1)
			if s.Meta.Pinned {
				ui.Icon(c, iconPin).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			if t := relativeTime(s.UpdatedAt); t != "" {
				ui.Text(c, t).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			}
		})
		b.Label(sessionDisplayTitle(s.Title)).Tooltip(s.Cwd)
		if b.Clicked() {
			a.selectSession(s)
		}
		a.sessionMenu(c, p, s, scope)
	})
}

func (a *nativeApp) openPage(page string) {
	if a.settingsOpen {
		a.closeSettings()
	}
	a.historyOpen = false
	a.history = false
	a.page = page
	switch page {
	case "skills":
		a.startSkillsPanelLoad()
		a.startSkillHubPanelLoad()
	case "automation":
		a.startCronPanelLoad()
	case "library":
		a.startKnowledgePanelLoad()
	}
}

func (a *nativeApp) pageHeader(c *ui.Context, p palette, bar ui.TitleBar, title, subtitle string) {
	right := float32(12 + bar.Right)
	left := float32(18 + bar.Left)
	ui.Row(c).Height(52).Padding(0, right, 0, left).AlignItems(ui.Center).Gap(10).
		BorderWidth(0, 0, 1, 0).BorderColor(p.t.Border).Children(func() {
		ui.Column(c).Gap(1).Grow(1).Children(func() {
			ui.Text(c, T(title)).FontSize(14).FontWeight(650)
			ui.Text(c, T(subtitle)).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
		})
	})
}

func (a *nativeApp) skillsPage(c *ui.Context, p palette, bar ui.TitleBar) {
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		a.pageHeader(c, p, bar, "技能", "可直接注入 Composer 的指令卡片、在线技能市场与本地技能入口")
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(18, 24, 24, 24).Gap(14).Children(func() {
				a.skillsPageActions(c, p)
				a.skillsCommandGrid(c, p)
				a.skillsPageMarket(c, p)
			})
		})
	})
}

// skillsPageActions exposes the two management areas that Desktop keeps in
// Settings. The Skills main page stays a command/market projection rather than
// embedding the settings forms.
func (a *nativeApp) skillsPageActions(c *ui.Context, p palette) {
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, iconSpark).FontSize(14).TextColor(p.t.Accent)
		ui.Text(c, T("本地技能与市场配置在设置中管理；本页只负责发现与注入。")).
			FontSize(10.5).TextColor(p.t.TextMuted).Grow(1)
		if a.skillsCapable() {
			if ui.Button(c.Key("skills-page-manage"), T("管理本地技能")).Clicked() {
				a.openSettingsTab("skills")
			}
		}
		if a.skillHubConfigCapable() || a.skillHubCatalogCapable() {
			if ui.Button(c.Key("skills-page-market-config"), T("市场配置")).Clicked() {
				a.openSettingsTab("skillhub")
			}
		}
	})
}

// skillsCommandGrid is the Desktop SkillsView card projection: one card per
// available command, clicking it injects the command into the Runtime-owned
// composer and returns to chat.
func (a *nativeApp) skillsCommandGrid(c *ui.Context, p palette) {
	ui.Column(c).Padding(14).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("指令卡片")).FontSize(12).FontWeight(650)
				ui.Text(c, T("来自当前会话 available_commands_update 投影；点击即可注入 Composer。")).
					FontSize(10.5).TextColor(p.t.TextMuted)
			})
			ui.TextInput(c.Key("skills-page-search"), &a.skillsPageQuery).FontSize(11).Height(30).
				Padding(0, 8).Placeholder(T("搜索指令…")).MaxWidth(220).
				Background(p.t.Background).Radius(8).Border(1, p.t.Border)
		})
		query := strings.ToLower(strings.TrimSpace(a.skillsPageQuery))
		commands := make([]command, 0, len(a.commands))
		for _, command := range a.commands {
			if query != "" {
				haystack := strings.ToLower(command.Name + " " + command.Description)
				if !strings.Contains(haystack, query) {
					continue
				}
			}
			commands = append(commands, command)
		}
		if len(commands) == 0 {
			ui.Text(c, T("当前会话没有可用指令。打开会话或加载历史后再回来查看。")).
				FontSize(10.5).TextColor(p.t.TextMuted).Padding(8, 4)
			return
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			for _, command := range commands {
				command := command
				name := strings.TrimPrefix(strings.TrimSpace(command.Name), "/")
				if name == "" {
					continue
				}
				b := ui.ButtonBase(c.Key("skills-command:"+name)).Width(250).Padding(10, 11).Radius(10).
					Border(1, p.t.Border).Background(p.t.Background).AlignItems(ui.Start).Gap(7)
				if b.Hovered() {
					b.Background(p.hover).Border(1, p.t.Accent)
				}
				b.Children(func() {
					ui.Column(c).Gap(3).Children(func() {
						kind := commandKind(command)
						icon := iconSlash
						kindLabel := "指令"
						if kind == "skill" {
							icon = iconSpark
							kindLabel = "技能"
						}
						ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
							ui.Icon(c, icon).FontSize(14).TextColor(p.t.Accent)
							ui.Text(c, name).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
							badge(c, p, kindLabel)
						})
						description := command.Description
						if description == "" {
							description = "无描述"
						}
						ui.Text(c, T(description)).FontSize(10).TextColor(p.t.TextMuted).
							LineHeight(1.35).MaxLines(3)
						ui.Text(c, T("使用")).FontSize(9.5).TextColor(p.t.Accent).Margin(2, 0, 0, 0)
					})
				})
				b.Label(name)
				if b.Clicked() {
					a.insertComposerText("/" + name + " ")
					a.page = ""
					a.showToast("已注入指令 /" + name)
				}
			}
		})
	})
}

func (a *nativeApp) pageStat(c *ui.Context, p palette, label, value, hint string) {
	ui.Column(c).Width(190).Padding(11, 12).Radius(10).Background(p.t.Surface).
		Border(1, p.t.Border).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(9.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.Text(c, T(value)).FontSize(20).FontWeight(650).TextColor(p.t.Text)
		if hint != "" {
			ui.Text(c, T(hint)).FontSize(9).TextColor(p.t.TextMuted).SingleLine()
		}
	})
}

func (a *nativeApp) automationPage(c *ui.Context, p palette, bar ui.TitleBar) {
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		a.pageHeader(c, p, bar, "自动化", "Runtime 持有的定时任务与最近运行状态")
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(18, 24, 24, 24).Gap(14).Children(func() {
				enabled := 0
				for _, job := range a.cron.Jobs {
					if job.Enabled {
						enabled++
					}
				}
				state := "未启用"
				if a.cron.Enabled {
					state = "已启用"
				}
				ui.Row(c).Wrap().Gap(10).Children(func() {
					a.pageStat(c, p, "定时任务", fmt.Sprintf("%d", len(a.cron.Jobs)), "Runtime 调度")
					a.pageStat(c, p, "启用中", fmt.Sprintf("%d", enabled), "已开启的任务")
					a.pageStat(c, p, "调度器", state, "由 Runtime 持有")
				})
				a.automationPageList(c, p)
			})
		})
	})
}

// automationPageList is Desktop's AutomationView row list: a page action row,
// canonical cron rows, and empty/loading/error states. Create/edit dialogs are
// still the Runtime-backed cron dialogs used by Settings.
func (a *nativeApp) automationPageList(c *ui.Context, p palette) {
	panel := &a.cron
	ui.Column(c).Padding(14).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconClock).FontSize(15).TextColor(p.t.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("定时任务")).FontSize(12.5).FontWeight(650).TextColor(p.t.Text)
				ui.Text(c, T("调度随 Native/ACP 在线生效；任务持久化在共享 Cron 存储，由 Runtime 执行。")).
					FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35)
			})
			refresh := ui.Button(c.Key("automation-page-refresh"), T("刷新"))
			refresh.Label(T("刷新定时任务列表"))
			refresh.Disabled(panel.Loading || a.cronBusy())
			if refresh.Clicked() && !panel.Loading && !a.cronBusy() {
				a.startCronPanelRefresh()
			}
			add := ui.PrimaryButton(c.Key("automation-page-new"), T("新建任务"))
			add.Label(T("新建定时任务"))
			add.Disabled(!a.cronCapable() || panel.Loading || a.cronBusy())
			if add.Clicked() && a.cronCapable() && !panel.Loading && !a.cronBusy() {
				a.openCronNewDraft()
			}
		})
		if !a.cronCapable() {
			a.pageGapCard(c, p, iconClock, T("定时任务不可用"),
				T("当前运行时未声明 manageCron；升级或切换到支持该能力的 runtime 后可直接使用。"))
			return
		}
		if panel.Loading && !panel.Loaded {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
				ui.Text(c, T("正在读取定时任务…")).FontSize(11.5).TextColor(p.t.TextMuted)
			})
			return
		}
		if panel.Err != "" {
			a.cronErrorCard(c, p, panel.Err)
			if !panel.Loaded {
				return
			}
		}
		if panel.Status != "" {
			a.cronStatusBanner(c, p)
		}
		if panel.Loading && panel.Loaded {
			ui.Text(c, T("正在刷新定时任务…")).FontSize(10).TextColor(p.t.TextMuted)
		}
		if len(panel.Jobs) == 0 {
			ui.Column(c).Padding(16).Radius(10).Border(1, p.t.Border).Gap(4).Children(func() {
				ui.Text(c, T("还没有定时任务")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
				ui.Text(c, T("新建任务后，Native 会通过 ACP 管理共享 Cron 调度器。")).
					FontSize(10.5).TextColor(p.t.TextMuted)
			})
			return
		}
		ui.Column(c).Gap(6).Children(func() {
			for index := range panel.Jobs {
				index := index
				a.cronJobRow(c, p, index)
			}
		})
	})
}

func (a *nativeApp) libraryPage(c *ui.Context, p palette, bar ui.TitleBar) {
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		a.pageHeader(c, p, bar, "资料库", "Runtime 持有的知识库索引与证据状态")
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(18, 24, 24, 24).Gap(14).Children(func() {
				indexed := 0
				files := 0
				for _, view := range a.knowledge.Views {
					if view.Status == "ready" || view.Status == "active" {
						indexed++
					}
					if view.Snapshot != nil {
						files += view.Snapshot.FileCount
					}
				}
				ui.Row(c).Wrap().Gap(10).Children(func() {
					a.pageStat(c, p, "知识库", fmt.Sprintf("%d", len(a.knowledge.Views)), "Desktop 管理的索引")
					a.pageStat(c, p, "已就绪", fmt.Sprintf("%d", indexed), "可查询快照")
					a.pageStat(c, p, "索引文件", fmt.Sprintf("%d", files), "当前活动快照")
				})
				a.libraryPageList(c, p)
			})
		})
	})
}

// libraryPageList is Desktop's read-only LibraryView projection. Editing and
// indexing lifecycle stay in the Settings knowledge panel so the main page
// never duplicates configuration or scan controls.
func (a *nativeApp) libraryPageList(c *ui.Context, p palette) {
	k := &a.knowledge
	ui.Column(c).Padding(14).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Start).Gap(8).Children(func() {
			ui.Icon(c, iconBook).FontSize(15).TextColor(p.t.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("知识库")).FontSize(12.5).FontWeight(650).TextColor(p.t.Text)
				ui.Text(c, T("只读索引状态；创建、扫描、删除与 MCP 接入在设置中管理。")).
					FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35)
			})
			if a.knowledgeCapable() {
				manage := ui.Button(c.Key("library-manage"), T("管理知识库"))
				manage.Label(T("在设置中管理知识库"))
				if manage.Clicked() {
					a.openSettingsTab("knowledge")
				}
			}
		})
		if !a.knowledgeCapable() {
			a.pageGapCard(c, p, iconBook, T("知识库不可用"),
				T("当前运行时未声明 manageKnowledgeBases；升级或切换到支持该能力的 runtime 后可直接使用。"))
			return
		}
		if k.Loading && !k.Loaded {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
				ui.Text(c, T("正在读取知识库配置与索引状态…")).FontSize(11.5).TextColor(p.t.TextMuted)
			})
			return
		}
		if k.Err != "" {
			a.knowledgeErrorCard(c, p, k.Err)
			if !k.Loaded {
				return
			}
		}
		if k.Status != "" {
			a.knowledgeStatusBanner(c, p)
		}
		if len(k.Views) == 0 {
			ui.Column(c).Padding(16).Radius(10).Border(1, p.t.Border).Gap(4).Children(func() {
				ui.Text(c, T("还没有知识库")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
				ui.Text(c, T("创建后可立即扫描，并在设置中查看索引状态与 MCP 接入。")).
					FontSize(10.5).TextColor(p.t.TextMuted)
			})
			return
		}
		ui.Column(c).Gap(7).Children(func() {
			for index := range k.Views {
				index := index
				a.libraryRow(c, p, index)
			}
		})
	})
}

func (a *nativeApp) libraryRow(c *ui.Context, p palette, index int) {
	k := &a.knowledge
	if index < 0 || index >= len(k.Views) {
		return
	}
	view := k.Views[index]
	base := view.KnowledgeBase
	name := strings.TrimSpace(base.Name)
	if name == "" {
		name = base.ID
	}
	ui.Column(c).Padding(10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconBook).FontSize(15).TextColor(p.t.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, name).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					if base.Enabled {
						badge(c, p, "已启用")
					} else {
						badge(c, p, "已停用")
					}
					if view.Indexing != nil && view.Indexing.Running {
						badge(c, p, "索引中")
					}
				})
				ui.Text(c, T(knowledgeStatusText(view))).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
				if stats := knowledgeStatsText(view.Snapshot); stats != "" {
					ui.Text(c, T(stats)).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
				}
				if view.Indexing != nil && view.Indexing.Running && view.Indexing.FilesTotal > 0 {
					ui.Textf(c, "%d/%d", view.Indexing.FilesDone, view.Indexing.FilesTotal).
						FontSize(9.5).TextColor(p.t.Accent).SingleLine()
				}
			})
		})
	})
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
		iconButton(c, p, a.appearanceIcon(), "切换外观", 14, func() { a.toggleTheme() })
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
			a.transcriptWithPlan(c, p)
		}
		a.composer(c, p)
		a.statusBar(c, p)
	})
}

func (a *nativeApp) header(c *ui.Context, p palette, bar ui.TitleBar) {
	right := float32(12 + bar.Right)
	left := float32(18 + bar.Left)
	ui.Row(c).Height(52).Padding(0, right, 0, left).AlignItems(ui.Center).Gap(6).
		BorderWidth(0, 0, 1, 0).BorderColor(p.t.Border).Background(p.t.Background).Children(func() {
		if label := a.currentSessionStatusLabel(); label != "" {
			statusPill(c, p, label)
		}
		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, a.sessionTitle()).FontSize(12.5).FontWeight(600).SingleLine().MaxWidth(520)
			if a.currentSession != "" {
				ui.Text(c, a.workspaceName()).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			}
		})
		ui.Spacer(c)
		if a.usageLabel() != "" {
			ui.Text(c, a.usageLabel()).FontSize(10).TextColor(p.t.TextMuted).Margin(0, 8, 0, 4)
		}
		if a.worktreeCapable() && a.worktreeBaseDir() != "" {
			iconButton(c, p, iconBranch, "隔离工作树", 15, a.openWorktreePanel)
		}
		if a.routerCapable() && a.currentSession != "" && a.loadingSession == "" {
			iconButton(c, p, iconCpu, "Router 协作", 15, a.openRouterPanel)
		}
		if a.subagentCapable() && a.currentSession != "" && a.loadingSession == "" {
			iconButton(c, p, iconUsers, "子代理", 15, a.openSubagentPanel)
		}
		if a.mcpCapable() && a.currentSession != "" && a.loadingSession == "" {
			iconButton(c, p, iconServer, "项目 MCP", 15, a.openProjectMCPPanel)
		}
		if a.currentSession != "" {
			if session, ok := a.sessionByID(a.currentSession); ok {
				a.sessionMenu(c, p, session, "header")
			}
		}
		iconButton(c, p, a.appearanceIcon(), "切换外观", 15, func() { a.toggleTheme() })
		if a.sidebar {
			iconButton(c, p, iconPanelLeft, "收起侧栏", 15, func() { a.sidebar = false })
		} else {
			iconButton(c, p, iconPanelLeft, "展开侧栏", 15, func() { a.sidebar = true })
		}
	})
}

func (a *nativeApp) emptyState(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		if a.home.backgroundImage != nil {
			opacity := float32(a.home.backgroundOpacity)
			if opacity < 0 {
				opacity = 0
			}
			if opacity > 1 {
				opacity = 1
			}
			x, y := a.homeObjectPosition()
			ui.Image(c, a.home.backgroundImage).Absolute().Left(0).Top(0).Right(0).Bottom(0).
				Fit(a.homeFit()).ObjectPosition(x, y).Opacity(opacity).
				Blur(float32(a.home.backgroundBlur)).
				HitTest(func(x, y float32, r ui.Rect) bool { return false })
		}
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			if a.conn == connFailed {
				a.connectionError(c, p)
				return
			}
			if a.home.logoVisible {
				if a.home.logoImage != nil {
					ui.Image(c, a.home.logoImage).Size(78, 78).Radius(18).Fit(ui.Cover)
				} else {
					mothxLogoView(c, 78)
				}
			}
			ui.Row(c).AlignItems(ui.Center).Gap(9).Margin(10, 0, 0, 0).Children(func() {
				ui.Text(c, "MothX").FontSize(27).FontWeight(650)
				ui.Text(c, T(" : Make OSCHINA Tokens Harness eXecution")).FontSize(14).
					FontWeight(500).TextColor(p.t.TextMuted)
			})
			ui.Text(c, T(nativeChineseSlogan)).FontSize(12).
				LetterSpacing(0.2).TextColor(p.t.TextMuted).Margin(4, 0, 0, 0)
			a.homePresetTabs(c, p)
			preset := homePresetByID(a.home.preset)
			locale := currentLocale()
			ui.Text(c, T(preset.sub(locale))).FontSize(11.5).
				FontWeight(600).TextColor(p.t.Text).Margin(10, 0, 0, 0)
			ui.Text(c, T(preset.desc(locale))).FontSize(10.5).
				TextColor(p.t.TextMuted).MaxWidth(620).LineHeight(1.4).Margin(2, 0, 0, 0)
			a.homePresetPrompts(c, p, preset)
			if a.workspaceName() != "" {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Margin(12, 0, 0, 0).Children(func() {
					ui.Icon(c, iconFolder).FontSize(12).TextColor(p.t.TextMuted)
					ui.Text(c, a.workspaceName()).FontSize(10.5).TextColor(p.t.TextMuted)
				})
			}
		})
	})
}

func (a *nativeApp) homePresetTabs(c *ui.Context, p palette) {
	ui.Row(c).AlignItems(ui.Center).Justify(ui.Center).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		for _, preset := range []struct{ ID, Label string }{
			{"working", "办公"},
			{"coding", "代码"},
			{"design", "创作"},
		} {
			preset := preset
			active := a.home.preset == preset.ID
			b := ui.ButtonBase(c.Key("home-preset:"+preset.ID)).Height(32).Padding(0, 18).Radius(15).
				AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border).Background(p.chip)
			if active {
				b.Background(p.active).Border(1, p.t.Accent)
			} else if b.Hovered() {
				b.Background(p.hover)
			}
			b.Children(func() {
				color := p.t.Text
				if active {
					color = p.t.Accent
				}
				ui.Text(c, T(preset.Label)).FontSize(12).FontWeight(550).TextColor(color)
			})
			b.Label(T(preset.Label))
			if b.Clicked() {
				a.home.preset = preset.ID
				a.saveNativePreferences()
			}
		}
	})
}

func (a *nativeApp) homePresetPrompts(c *ui.Context, p palette, preset homePreset) {
	locale := currentLocale()
	labels := preset.quick(locale)
	tags := preset.tags(locale)
	prompts := preset.prompts(locale)
	protocol := executionProtocol(locale)
	ui.Row(c).Wrap().Gap(8).MaxWidth(660).Justify(ui.Center).Margin(14, 0, 0, 0).Children(func() {
		for i, label := range labels {
			i, label := i, label
			tag := ""
			if i < len(tags) {
				tag = tags[i]
			}
			prompt := label
			if i < len(prompts) {
				prompt = prompts[i] + protocol
			}
			b := ui.ButtonBase(c.Key("home-prompt:"+preset.ID+":"+label)).Padding(8, 11).Radius(10).
				Border(1, p.t.Border).Background(p.chip)
			if b.Hovered() {
				b.Background(p.hover).Border(1, p.t.Accent)
			}
			b.Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, strings.ToUpper(tag)).FontSize(8.5).FontWeight(650).
						LetterSpacing(0.5).TextColor(p.t.TextMuted)
					ui.Text(c, T(label)).FontSize(11).FontWeight(500).TextColor(p.t.Text).SingleLine()
				})
			})
			b.Label(label)
			if b.Clicked() {
				a.insertComposerText(prompt)
			}
		}
	})
}

func (a *nativeApp) connectionError(c *ui.Context, p palette) {
	ui.Column(c).Width(460).Padding(18).Radius(14).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(18).TextColor(p.t.Danger)
			ui.Text(c, T("未连接到 MothX ACP")).FontSize(14).FontWeight(650)
		})
		if a.connErr != "" {
			ui.Text(c, T(a.connErr)).FontSize(11.5).TextColor(p.t.TextMuted).Selectable()
		}
		if len(a.stderr) > 0 {
			ui.Text(c, strings.Join(a.stderr, "\n")).Font("monospace").FontSize(9.5).
				TextColor(p.t.TextMuted).MaxLines(5).Selectable()
		}
		if ui.PrimaryButton(c.Key("retry-connection"), T("重试连接")).Clicked() {
			go a.connect()
		}
	})
}

// transcriptRenderWindow bounds the live DOM/build cost of very long sessions.
// Full canonical history remains in a.messages and is reachable through older
// message paging; only the rendered tail is capped, matching Desktop.
const transcriptRenderWindow = 300

func (a *nativeApp) transcriptWithPlan(c *ui.Context, p palette) {
	entries := a.activePlanEntries()
	if len(entries) == 0 {
		a.transcript(c, p)
		return
	}
	ui.Row(c).Grow(1).Children(func() {
		ui.Column(c).Grow(1).Children(func() { a.transcript(c, p) })
		a.planProgressPanel(c, p, entries)
	})
}

func (a *nativeApp) activePlanEntries() []planEntry {
	if a.planMessageID == "" {
		return nil
	}
	m := a.messageByID(a.planMessageID)
	if m == nil || m.Kind != messagePlan || len(m.Plan) == 0 {
		return nil
	}
	return m.Plan
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
					FontSize(9.5).TextColor(p.t.TextMuted)
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

func (a *nativeApp) planProgressPanel(c *ui.Context, p palette, entries []planEntry) {
	completed := 0
	for _, entry := range entries {
		if entry.Status == "completed" {
			completed++
		}
	}
	ui.Column(c).Width(240).FillHeight().Padding(12, 12).Gap(8).
		BorderWidth(0, 0, 0, 1).BorderColor(p.t.Border).Background(p.t.Background).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconSpark).FontSize(13).TextColor(p.t.Accent)
			ui.Text(c, T("计划进度")).FontSize(11).FontWeight(650).TextColor(p.t.Text)
			ui.Spacer(c)
			ui.Textf(c, "%d/%d", completed, len(entries)).FontSize(9.5).TextColor(p.t.TextMuted)
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(7).Children(func() {
				for _, entry := range entries {
					entry := entry
					ui.Row(c).AlignItems(ui.Start).Gap(6).Children(func() {
						color := p.t.TextMuted
						switch entry.Status {
						case "completed":
							color = p.t.Success
						case "in_progress", "running":
							color = p.t.Accent
						case "failed":
							color = p.t.Danger
						}
						ui.Box(c).Size(6, 6).Radius(3).Background(color).Margin(4, 0, 0, 0)
						t := ui.Text(c, entry.Content).FontSize(10.5).TextColor(p.t.Text).Grow(1).LineHeight(1.35)
						if entry.Status == "completed" {
							t.Strikethrough().TextColor(p.t.TextMuted)
						}
					})
				}
			})
		})
	})
}

func (a *nativeApp) messageRow(c *ui.Context, p palette, m *chatMessage) {
	switch m.Kind {
	case messageUser:
		ui.Row(c).Key(m.ID).Children(func() {
			ui.Spacer(c)
			ui.Column(c).MaxWidthPercent(78).MaxWidth(560).Padding(9, 13).Radius(15).
				Background(p.bubble).Children(func() {
				ui.Text(c, m.Text).FontSize(12.8).LineHeight(1.5).Selectable()
				renderChatImages(c, p, m.Images, 360)
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
				ui.Text(c, T("正在生成…")).FontSize(12).TextColor(p.t.TextMuted)
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
				label := T("已思考")
				if !m.Done {
					label = T("思考中…")
				}
				ui.Text(c, T(label)).FontSize(11.5).TextColor(p.t.TextMuted)
			})
			b.Label(T("已思考"))
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
		b := ui.ButtonBase(c.Key("artifact:"+m.ID)).WidthPercent(100).AlignItems(ui.Center).Gap(8).
			Padding(9, 12).Radius(10).Background(p.thought).Border(1, p.t.Border)
		b.Children(func() {
			icon := iconFile
			if m.ArtifactKind == "image" {
				icon = iconImage
			}
			ui.Icon(c, icon).FontSize(16).TextColor(p.t.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				label := T("制品")
				if m.ArtifactFromToolCall {
					label = T("制品（来自 publish_artifact 工具调用）")
				}
				ui.Text(c, label).FontSize(10).TextColor(p.t.TextMuted)
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
					ui.Text(c, strings.Join(meta, " · ")).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
				}
			})
			ui.Icon(c, iconChevronRight).FontSize(12).TextColor(p.t.TextMuted)
		})
		b.Label(T("打开制品 " + m.Filename))
		if b.Clicked() {
			a.openArtifactMessage(m)
		}
	case messageDecision:
		a.decisionCard(c, p, m)
	case messageStatus:
		ui.Row(c).Key(m.ID).AlignItems(ui.Center).Gap(6).Margin(0, ui.Auto).Children(func() {
			color := p.t.TextMuted
			if !m.Done {
				color = p.t.Accent
			}
			ui.Box(c).Size(5, 5).Radius(3).Background(color)
			ui.Text(c, T(m.Text)).FontSize(10.5).TextColor(color).SingleLine()
		})
	case messageError:
		ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).
			Radius(10).Background(p.errorBg).Border(1, ui.RGBA(255, 107, 107, 0.30)).
			Padding(10, 12).Gap(7).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
				ui.Icon(c, iconAlert).FontSize(14).TextColor(p.t.Danger)
				ui.Text(c, T("运行失败")).FontSize(11.5).FontWeight(650).TextColor(p.t.Danger).Grow(1)
				if m.ErrorCode != "" {
					badge(c, p, m.ErrorCode)
				}
				if m.ErrorRetryable {
					badge(c, p, "可重试")
				}
			})
			ui.Text(c, T(m.Text)).FontSize(10.5).LineHeight(1.4).
				TextColor(p.t.Text).Selectable()
		})
	default:
		color := p.t.TextMuted
		if strings.HasPrefix(m.Text, "发送失败") || strings.HasPrefix(m.Text, "载入会话失败") {
			color = p.t.Danger
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
		if m.ToolExpanded {
			if input := prettyToolInput(m.ToolInput); input != "" {
				ui.Box(c).Height(1).Background(p.t.Border)
				ui.Text(c, T("输入")).FontSize(9.5).FontWeight(600).TextColor(p.t.TextMuted).Padding(8, 10, 0, 10)
				ui.Text(c, input).Font("monospace").FontSize(10).LineHeight(1.4).
					TextColor(p.t.TextMuted).Padding(4, 10, 8, 10).Selectable()
			}
			if m.ToolText != "" {
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
			for _, diff := range m.ToolDiffs {
				ui.Box(c).Height(1).Background(p.t.Border)
				ui.Text(c, toolDiffText(diff)).Font("monospace").FontSize(10).LineHeight(1.4).
					TextColor(p.t.TextMuted).Padding(9, 10).Selectable()
			}
			if len(m.ToolLocations) > 0 {
				ui.Box(c).Height(1).Background(p.t.Border)
				ui.Text(c, strings.Join(m.ToolLocations, "\n")).Font("monospace").FontSize(9.5).
					TextColor(p.t.TextMuted).Padding(6, 10).Selectable()
			}
			if len(m.ToolImages) > 0 {
				ui.Box(c).Height(1).Background(p.t.Border)
				ui.Column(c).Padding(8, 10).Children(func() { renderChatImages(c, p, m.ToolImages, 520) })
			}
		}
	})
}

func (a *nativeApp) planCard(c *ui.Context, p palette, m *chatMessage) {
	ui.Column(c).Key(m.ID).WidthPercent(100).MaxWidth(780).Margin(0, ui.Auto).Radius(10).Background(p.thought).
		Border(1, p.t.Border).Padding(10, 12).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconSpark).FontSize(13).TextColor(p.t.Accent)
			ui.Text(c, T("计划")).FontSize(11).FontWeight(650).TextColor(p.t.TextMuted)
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
						ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(4, 8).Radius(7).
							Background(p.chip).Border(1, p.t.Border).Children(func() {
							if f.Preview != nil {
								ui.Image(c, f.Preview).Size(30, 30).Fit(ui.Cover).Radius(5)
							} else {
								ui.Icon(c, iconPaperclip).FontSize(11).TextColor(p.t.TextMuted)
							}
							ui.Column(c).Gap(1).Children(func() {
								label := f.Name
								if f.Embedded {
									label += T("（内嵌）")
								}
								ui.Text(c, label).FontSize(10.5).TextColor(p.t.Text).SingleLine().MaxWidth(180)
								details := attachmentDetailLabel(f)
								if details != "" {
									ui.Text(c, details).FontSize(8.5).TextColor(p.t.TextMuted).SingleLine()
								}
							})
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
					b := ui.ButtonBase(c.Key("workspace-picker")).Padding(4, 7).Radius(7).
						Gap(5).AlignItems(ui.Center)
					if b.Hovered() {
						b.Background(p.hover)
					}
					b.Children(func() {
						ui.Icon(c, iconFolder).FontSize(12).TextColor(p.t.TextMuted)
						ui.Text(c, a.workspaceLabel()).FontSize(10.5).TextColor(p.t.TextMuted).SingleLine().MaxWidth(150)
						ui.Icon(c, iconChevronDown).FontSize(10).TextColor(p.t.TextMuted)
					})
					b.Label(T("切换工作区")).Tooltip(T("已打开会话时修改会话工作目录；否则选择下一个新会话目录"))
					if b.Clicked() {
						a.chooseWorkspace()
					}
					ui.Spacer(c)
					a.expertMenu(c, p)
					a.modelMenu(c, p)
					a.sendButton(c, p)
				})
			})
			ui.Row(c).AlignItems(ui.Center).Padding(0, 3).Children(func() {
				if a.status != "" {
					ui.Text(c, T(a.status)).FontSize(9.8).TextColor(p.t.TextMuted).SingleLine().Grow(1)
				} else {
					ui.Text(c, T("Enter 发送 · Shift + Enter 换行")).FontSize(9.5).
						TextColor(p.t.TextMuted).Grow(1)
				}
				if a.usageUsed > 0 && a.usageSize > 0 {
					ui.Textf(c, "%s tokens", humanCount(a.usageUsed)).FontSize(9.5).TextColor(p.t.TextMuted)
				}
			})
		})
	})
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
		b.Label(T("停止")).Tooltip(T("停止生成"))
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
	b.Label(T("发送")).Tooltip(T("发送消息"))
	if b.Clicked() && !empty {
		a.send()
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
	b.Label(T(label)).Tooltip(T(label))
	if b.Clicked() {
		action()
		return true
	}
	return false
}

func badge(c *ui.Context, p palette, text string) {
	ui.Box(c).Padding(2, 6).Radius(4).Border(1, p.t.Border).Children(func() {
		ui.Text(c, T(text)).FontSize(8).FontWeight(700).LetterSpacing(0.7).TextColor(p.t.TextMuted)
	})
}

// renderChatImages projects bounded inline images carried by canonical content.
// No path scanning or artifact inference happens here.
func renderChatImages(c *ui.Context, p palette, images []chatImage, maxWidth float32) {
	if len(images) == 0 {
		return
	}
	ui.Row(c).Wrap().Gap(6).Margin(6, 0, 0, 0).Children(func() {
		for _, image := range images {
			if image.Image == nil {
				continue
			}
			w, h := ui.ImageSize(image.Image)
			displayW := maxWidth
			displayH := float32(220)
			if w > 0 && h > 0 {
				scale := displayW / float32(w)
				displayH = float32(h) * scale
				if displayH > 260 {
					displayH = 260
					displayW = float32(w) * (260 / float32(h))
				}
			}
			ui.Image(c, image.Image).Size(displayW, displayH).Fit(ui.Cover).Radius(8).
				Border(1, p.t.Border)
		}
	})
}

func statusPill(c *ui.Context, p palette, status string) {
	label := status
	color := p.t.TextMuted
	bg := p.chip
	switch status {
	case "pending":
		label = T("等待")
	case "in_progress", "running":
		label = T("运行中")
		color = p.t.Accent
		bg = p.active
	case "completed":
		label = T("完成")
		color = p.t.Success
		bg = p.okBg
	case "failed":
		label = T("失败")
		color = p.t.Danger
		bg = p.errorBg
	case "cancelled":
		label = T("已取消")
	case "incomplete":
		label = T("未完成")
		color = p.t.Warning
		bg = p.warnBg
	default:
		label = T(label)
	}
	ui.Box(c).Padding(1, 6).Radius(999).Background(bg).Children(func() {
		ui.Text(c, T(label)).FontSize(9).TextColor(color)
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
			b.WriteString("## Plan\n\n")
			for _, entry := range m.Plan {
				b.WriteString("- [")
				b.WriteString(entry.Status)
				b.WriteString("] ")
				b.WriteString(entry.Content)
				b.WriteString("\n")
			}
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

func copyButton(c *ui.Context, p palette, text string) {
	b := ui.ButtonBase(c).Padding(3, 6).Radius(6).Gap(4).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconFile).FontSize(11).TextColor(p.t.TextMuted)
		ui.Text(c, T("复制")).FontSize(9.8).TextColor(p.t.TextMuted)
	})
	b.Label(T("复制")).Tooltip(T("复制回复"))
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

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

// Keep markdown imported here: the view owns the Markdown options shared by
// every message row.
var _ markdown.Options
