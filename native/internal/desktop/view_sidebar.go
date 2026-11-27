package desktop

import (
	"github.com/egoist/mygo/ui"
)

// nativeSidebarWidth is the shell's fixed sidebar column width; the transcript
// width estimate subtracts it when the sidebar is open.
const nativeSidebarWidth float32 = 220

func (a *nativeApp) sidebarView(c *ui.Context, p palette) {
	ui.Column(c).Width(nativeSidebarWidth).FillHeight().Background(p.Sidebar).
		BorderWidth(0, 1, 0, 0).BorderColor(p.T.Border).Children(func() {
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
			// Navigation order mirrors Desktop's canonical sidebar projection:
			// home → skills → history → automation → experts → library. Extra
			// Native-only entries (search, settings) sit outside that sequence.
			a.navRow(c, p, "home", iconSpark, "首页", a.homeNavActive(), a.openHome)
			a.navRow(c, p, "skills", iconZap, "技能", a.page == "skills" && !a.settingsOpen, func() { a.openPage("skills") })
			a.navRow(c, p, "history", iconList, "会话历史", a.history, a.openHistory)
			if count := a.backgroundAttentionCount(); count > 0 {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(0, 10, 4, 36).Children(func() {
					ui.Box(c).Padding(1, 6).Radius(5).Border(1, p.T.Warning).Children(func() {
						ui.Text(c, Tf("%d 个后台会话待处理", count)).FontSize(9).FontWeight(600).
							TextColor(p.T.Warning).SingleLine()
					})
				})
			}
			a.navRow(c, p, "automation", iconClock, "自动化", a.page == "automation" && !a.settingsOpen, func() { a.openPage("automation") })
			if a.featureEnabled("manageExperts") {
				// Desktop never highlights this entry (it opens the Settings tab),
				// so the row stays inactive while still exposing the shortcut.
				a.navRow(c, p, "experts", iconUsers, "主角团", false, func() { a.openSettingsTab("experts") })
			}
			a.navRow(c, p, "library", iconBook, "资料库", a.page == "library" && !a.settingsOpen, func() { a.openPage("library") })
			a.navRow(c, p, "settings", iconSettings, "应用设置", a.settingsOpen, a.openSettings)
			ui.Box(c).Height(1).Background(p.T.Border).Margin(10, 2, 8, 2)
			if a.searching {
				ui.TextInput(c.Key("session-search"), &a.search).Label(T("搜索会话")).
					Placeholder(T("搜索会话或工作区")).
					FontSize(12).Height(30).Padding(0, 8).Margin(2, 2, 6, 2).
					Background(p.Chip).Radius(8).Border(1, p.T.Border).AutoFocus()
			}
			a.sessionList(c, p)
			a.sidebarFooter(c, p)
		})
	})
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
		AlignItems(ui.Center).Justify(ui.Center).Background(p.Chip).Border(1, p.T.Border)
	if b.Hovered() {
		b.Background(p.T.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, iconPlus).FontSize(15).TextColor(p.T.Text)
		ui.Text(c, T("新会话")).FontSize(12.5).FontWeight(550).TextColor(p.T.Text)
	})
	b.Label(T("新会话")).Tooltip(T("开始新任务并选择工作目录"))
	if b.Clicked() {
		a.startNewTask()
	}
}

func (a *nativeApp) navRow(c *ui.Context, p palette, key string, icon *ui.SVG, label string, active bool, action func()) {
	b := ui.ButtonBase(c.Key("nav:"+key)).Height(32).Padding(6, 8).Radius(8).Gap(8).
		AlignItems(ui.Center).Justify(ui.Start).Margin(3, 0, 0, 0)
	if active {
		b.Background(p.Active)
	} else if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		color := p.T.TextMuted
		if active {
			color = p.T.Accent
			ui.Box(c).Absolute().Left(0).Top(8).Width(3).Height(16).Radius(2).Background(p.T.Accent)
		}
		ui.Icon(c, icon).FontSize(15).TextColor(color)
		ui.Text(c, T(label)).FontSize(12.5).TextColor(p.T.Text)
	})
	b.Label(T(label))
	if b.Clicked() {
		action()
	}
}

func (a *nativeApp) taskSectionHeader(c *ui.Context, p palette, title, addLabel string, add func()) {
	ui.Row(c).AlignItems(ui.Center).Height(26).Padding(8, 4, 2, 4).Gap(2).Children(func() {
		ui.Text(c, T(title)).FontSize(10.5).FontWeight(600).LetterSpacing(0.5).TextColor(p.T.TextMuted)
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
			ui.Text(c, T("正在读取任务…")).FontSize(11).TextColor(p.T.TextMuted).Padding(8, 8)
			return
		}
		if a.sessionsErr != "" {
			ui.Text(c, T("会话列表不可用")).FontSize(11).TextColor(p.T.Danger).Padding(8, 8)
		}

		if a.historyProjectsCapable() {
			add := func() { a.openProjectDialog(projectDialogCreate, "") }
			a.taskSectionHeader(c, p, "项目", "新建项目", add)
			if a.projectsErr != "" {
				ui.Text(c, T(a.projectsErr)).FontSize(10).TextColor(p.T.Danger).Padding(2, 20).LineHeight(1.35)
			}
			if len(tree.Projects) == 0 {
				text := "尚未创建项目"
				if !a.projectsLoaded {
					text = "正在读取项目…"
				}
				ui.Text(c, T(text)).FontSize(10.5).TextColor(p.T.TextMuted).Padding(6, 20)
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
			ui.Text(c, T(text)).FontSize(10.5).TextColor(p.T.TextMuted).Padding(6, 20)
		}
		for _, session := range tree.Ungrouped {
			a.sessionRow(c, p, session, "ungrouped")
		}
		a.sessionPageFooter(c, p, tree.UngroupedPage, func() { a.loadMoreUngroupedSessions() })

		if len(tree.Projects) == 0 && len(tree.Recent) == 0 && len(tree.Ungrouped) == 0 && a.sessionsErr == "" {
			ui.Text(c, T("还没有会话")).FontSize(11).TextColor(p.T.TextMuted).Padding(8, 8)
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
			b.Background(p.Hover)
		}
		b.Children(func() {
			if expanded {
				ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.T.TextMuted)
			} else {
				ui.Icon(c, iconChevronRight).FontSize(11).TextColor(p.T.TextMuted)
			}
			ui.Icon(c, iconFolder).FontSize(14).TextColor(p.T.TextMuted)
			ui.Text(c, project.Name).FontSize(11.5).FontWeight(600).TextColor(p.T.Text).SingleLine().Grow(1)
			count := len(branch.Sessions)
			if project.SessionCount != nil {
				count = *project.SessionCount
			}
			ui.Textf(c, "%d", count).FontSize(10.5).TextColor(p.T.TextMuted)
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
		ui.Text(c, T("该项目还没有会话")).FontSize(10.5).TextColor(p.T.TextMuted).Padding(6, 24)
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
		ui.Text(c, T(page.Err)).FontSize(9.5).TextColor(p.T.Danger).Padding(2, 24).LineHeight(1.3)
	}
	if !page.Loaded && !page.Loading && page.NextCursor == "" {
		return
	}
	if page.Loading {
		ui.Row(c).AlignItems(ui.Center).Gap(5).Padding(3, 24).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(10).TextColor(p.T.TextMuted)
			ui.Text(c, T("正在加载更多…")).FontSize(9.5).TextColor(p.T.TextMuted)
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
			b.Background(p.Active)
		} else if b.Hovered() {
			b.Background(p.Hover)
		}
		b.Children(func() {
			sessionStatusDot(c, p, a.sessionStatusKind(s))
			title := sessionDisplayTitle(s.Title)
			if title == "" {
				title = T("未命名会话")
			}
			textColor := p.T.Text
			if active {
				textColor = p.T.Accent
			}
			ui.Text(c, title).FontSize(12).TextColor(textColor).SingleLine().Grow(1)
			if s.Meta.Pinned {
				ui.Icon(c, iconPin).FontSize(10.5).TextColor(p.T.TextMuted)
			}
			if t := relativeTime(s.UpdatedAt); t != "" {
				ui.Text(c, t).FontSize(9.5).TextColor(p.T.TextMuted).SingleLine()
			}
		})
		b.Label(sessionDisplayTitle(s.Title)).Tooltip(s.Cwd)
		if b.Clicked() {
			a.selectSession(s)
		}
		a.sessionMenu(c, p, s, scope)
	})
}

func (a *nativeApp) sidebarFooter(c *ui.Context, p palette) {
	ui.Box(c).Height(1).Background(p.T.Border).Margin(8, 2, 6, 2)
	ui.Row(c).AlignItems(ui.Center).Gap(4).Padding(5, 6).Children(func() {
		ui.Text(c, T("本地模式 · ACP")).FontSize(11).TextColor(p.T.TextMuted).SingleLine().Grow(1)
		iconButton(c, p, iconGlobe, "切换语言", 14, func() { a.toggleLocale() })
		iconButton(c, p, a.appearanceIcon(), "切换外观", 14, func() { a.toggleTheme() })
		dot := p.T.Success
		switch a.conn {
		case connFailed:
			dot = p.T.Danger
		case connStarting:
			dot = p.T.Warning
		}
		ui.Box(c).Size(6, 6).Radius(3).Background(dot)
	})
}
