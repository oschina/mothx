package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

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

// openHome mirrors Desktop's Home nav entry: it projects the home surface
// without discarding the active session or its transcript. Sending from the
// home composer continues that session (or creates one when the draft is
// empty), exactly like Desktop's shared composer.
func (a *nativeApp) openHome() {
	if a.settingsOpen {
		a.closeSettings()
	}
	a.historyOpen = false
	a.history = false
	a.page = "home"
}

// homeNavActive mirrors Desktop's view highlight: the home surface is selected
// when explicitly opened or when the shell is showing an empty new-task draft.
func (a *nativeApp) homeNavActive() bool {
	if a.settingsOpen {
		return false
	}
	if a.page == "home" {
		return true
	}
	return a.page == "" && a.currentSession == "" && len(a.messages) == 0
}

func (a *nativeApp) homePage(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		a.emptyState(c, p)
		a.composer(c, p)
		a.statusBar(c, p)
	})
}

// pageHeader mirrors Desktop's PageHead: a wide title block inside the page
// scroll area, not a fixed toolbar above it.
// pageContent is Desktop's PageInner: a centered 880px column with 24px top
// and 48px bottom padding. Pages keep their title and action rows inside it.
func (a *nativeApp) skillsPage(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Background(p.T.Background).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			a.pageContent(c, 1120, func() {
				a.pageHeader(c, p, "技能 · 指令", "来自 ACP available_commands_update：项目技能与内置指令，点击注入到输入框。")
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
		ui.Icon(c, iconSpark).FontSize(14).TextColor(p.T.Accent)
		ui.Text(c, T("本地技能与市场配置在设置中管理；本页只负责发现与注入。")).
			FontSize(10.5).TextColor(p.T.TextMuted).Grow(1)
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
	ui.Column(c).Padding(14).Radius(12).Background(p.T.Surface).Border(1, p.T.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("指令卡片")).FontSize(12).FontWeight(650)
				ui.Text(c, T("来自当前会话 available_commands_update 投影；点击即可注入 Composer。")).
					FontSize(10.5).TextColor(p.T.TextMuted)
			})
			ui.TextInput(c.Key("skills-page-search"), &a.skillsPageQuery).FontSize(11).Height(30).
				Padding(0, 8).Placeholder(T("搜索指令…")).MaxWidth(220).
				Background(p.T.Background).Radius(8).Border(1, p.T.Border)
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
				FontSize(10.5).TextColor(p.T.TextMuted).Padding(8, 4)
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
					Border(1, p.T.Border).Background(p.T.Background).AlignItems(ui.Start).Gap(7)
				if b.Hovered() {
					b.Background(p.Hover).Border(1, p.T.Accent)
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
							ui.Icon(c, icon).FontSize(14).TextColor(p.T.Accent)
							ui.Text(c, name).FontSize(12).FontWeight(650).TextColor(p.T.Text).SingleLine()
							badge(c, p, kindLabel)
						})
						description := command.Description
						if description == "" {
							description = "无描述"
						}
						ui.Text(c, T(description)).FontSize(10).TextColor(p.T.TextMuted).
							LineHeight(1.35).MaxLines(3)
						ui.Text(c, T("使用")).FontSize(9.5).TextColor(p.T.Accent).Margin(2, 0, 0, 0)
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

func (a *nativeApp) automationPage(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Background(p.T.Background).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			a.pageContent(c, 880, func() {
				a.pageHeader(c, p, "自动化 · 定时任务", "让 Agent 按计划自动运行（cron 调度随桌面在线生效）。")
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
	ui.Column(c).Padding(14).Radius(12).Background(p.T.Surface).Border(1, p.T.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconClock).FontSize(15).TextColor(p.T.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("定时任务")).FontSize(12.5).FontWeight(650).TextColor(p.T.Text)
				ui.Text(c, T("调度随 Native/ACP 在线生效；任务持久化在共享 Cron 存储，由 Runtime 执行。")).
					FontSize(10).TextColor(p.T.TextMuted).LineHeight(1.35)
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
				ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.T.TextMuted)
				ui.Text(c, T("正在读取定时任务…")).FontSize(11.5).TextColor(p.T.TextMuted)
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
			ui.Text(c, T("正在刷新定时任务…")).FontSize(10).TextColor(p.T.TextMuted)
		}
		if len(panel.Jobs) == 0 {
			ui.Column(c).Padding(16).Radius(10).Border(1, p.T.Border).Gap(4).Children(func() {
				ui.Text(c, T("还没有定时任务")).FontSize(12).FontWeight(600).TextColor(p.T.Text)
				ui.Text(c, T("新建任务后，Native 会通过 ACP 管理共享 Cron 调度器。")).
					FontSize(10.5).TextColor(p.T.TextMuted)
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

func (a *nativeApp) libraryPage(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).FillHeight().Background(p.T.Background).Children(func() {
		ui.Scroll(c).Grow(1).Children(func() {
			a.pageContent(c, 880, func() {
				a.pageHeader(c, p, "知识库", "管理目录索引、扫描状态与 MCP 使用配置。")
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
					a.pageStat(c, p, "知识库", fmt.Sprintf("%d", len(a.knowledge.Views)), "本机管理的索引")
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
	ui.Column(c).Padding(14).Radius(12).Background(p.T.Surface).Border(1, p.T.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Start).Gap(8).Children(func() {
			ui.Icon(c, iconBook).FontSize(15).TextColor(p.T.Accent)
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("知识库")).FontSize(12.5).FontWeight(650).TextColor(p.T.Text)
				ui.Text(c, T("只读索引状态；创建、扫描、删除与 MCP 接入在设置中管理。")).
					FontSize(10).TextColor(p.T.TextMuted).LineHeight(1.35)
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
				ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.T.TextMuted)
				ui.Text(c, T("正在读取知识库配置与索引状态…")).FontSize(11.5).TextColor(p.T.TextMuted)
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
			ui.Column(c).Padding(16).Radius(10).Border(1, p.T.Border).Gap(4).Children(func() {
				ui.Text(c, T("还没有知识库")).FontSize(12).FontWeight(600).TextColor(p.T.Text)
				ui.Text(c, T("创建后可立即扫描，并在设置中查看索引状态与 MCP 接入。")).
					FontSize(10.5).TextColor(p.T.TextMuted)
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
	ui.Column(c).Padding(10).Radius(9).Background(p.T.Background).
		Border(1, p.T.Border).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconBook).FontSize(15).TextColor(p.T.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, name).FontSize(12).FontWeight(650).TextColor(p.T.Text).SingleLine()
					if base.Enabled {
						badge(c, p, "已启用")
					} else {
						badge(c, p, "已停用")
					}
					if view.Indexing != nil && view.Indexing.Running {
						badge(c, p, "索引中")
					}
				})
				ui.Text(c, T(knowledgeStatusText(view))).FontSize(10).TextColor(p.T.TextMuted).SingleLine()
				if stats := knowledgeStatsText(view.Snapshot); stats != "" {
					ui.Text(c, T(stats)).FontSize(9.5).TextColor(p.T.TextMuted).SingleLine()
				}
				if view.Indexing != nil && view.Indexing.Running && view.Indexing.FilesTotal > 0 {
					ui.Textf(c, "%d/%d", view.Indexing.FilesDone, view.Indexing.FilesTotal).
						FontSize(9.5).TextColor(p.T.Accent).SingleLine()
				}
			})
		})
	})
}
