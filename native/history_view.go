package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// historyView is the task-library projection corresponding to Desktop's
// HistoryView. It renders paginated mothx/session/listAll rows, project/scope
// filters, and the additive session operations. Every row and action is a
// projection of Runtime-owned state; this view never reads a database or the
// workspace settings file.
func (a *nativeApp) historyView(c *ui.Context, p palette, bar ui.TitleBar) {
	panel := &a.historyPanel
	right := float32(12 + bar.Right)
	left := float32(18 + bar.Left)
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		ui.Row(c).Height(52).Padding(0, right, 0, left).AlignItems(ui.Center).Gap(10).
			BorderWidth(0, 0, 1, 0).BorderColor(p.t.Border).Children(func() {
			back := ui.Button(c.Key("history-back"), T("返回对话")).Label(T("返回对话"))
			if back.Clicked() {
				a.closeHistory()
			}
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, T("会话历史")).FontSize(14).FontWeight(650)
				ui.Text(c, T("全部会话、项目归属与最新运行状态")).FontSize(10).TextColor(p.t.TextMuted)
			})
			refresh := ui.Button(c.Key("history-refresh"), T("刷新")).Label(T("刷新会话历史"))
			refresh.Disabled(panel.Loading || panel.LoadingMore)
			if refresh.Clicked() && !panel.Loading && !panel.LoadingMore {
				a.refreshHistory()
			}
		})
		ui.Column(c).Grow(1).Padding(18, 24, 12, 24).Gap(10).Children(func() {
			a.historyFilters(c, p)
			a.historySearchRow(c, p)
			if panel.Status != "" {
				ui.Text(c, T(panel.Status)).FontSize(10.5).TextColor(p.t.TextMuted).SingleLine()
			}
			if panel.Err != "" {
				a.historyErrorCard(c, p, panel.Err)
			}
			if panel.Loading && !panel.Loaded {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
					ui.Text(c, T("正在读取会话历史…")).FontSize(12).TextColor(p.t.TextMuted)
				})
				return
			}
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Gap(0).Children(func() {
					sessions := a.historyVisibleSessions()
					if len(sessions) == 0 {
						ui.Text(c, T(a.historyEmptyReason())).FontSize(12).TextColor(p.t.TextMuted).Padding(12, 4)
						return
					}
					for _, session := range sessions {
						a.historyRow(c, p, session)
					}
					if panel.LoadingMore {
						ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(8, 4).Children(func() {
							ui.Icon(c, iconRefresh).FontSize(12).TextColor(p.t.TextMuted)
							ui.Text(c, T("正在加载更多…")).FontSize(10.5).TextColor(p.t.TextMuted)
						})
					} else if panel.NextCursor != "" {
						more := ui.Button(c.Key("history-more"), T("加载更多")).Label(T("加载更多会话"))
						more.Disabled(panel.Loading)
						if more.Clicked() && !panel.Loading {
							a.loadMoreHistory()
						}
					}
				})
			})
		})
	})
}

func (a *nativeApp) historyFilters(c *ui.Context, p palette) {
	panel := &a.historyPanel
	if !a.historyListCapable() && !a.historyProjectsCapable() {
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
		a.historyScopeChip(c, p, "all", "", "全部")
		a.historyScopeChip(c, p, "ungrouped", "", "未归类")
		for _, project := range a.projects {
			project := project
			label := project.Name
			if project.SessionCount != nil {
				label = fmt.Sprintf("%s (%d)", project.Name, *project.SessionCount)
			}
			a.historyScopeChip(c, p, "project", project.ID, label)
		}
		if a.historyProjectsCapable() {
			ui.Box(c).Width(6)
			create := ui.Button(c.Key("history-project-create"), T("新建项目")).Label(T("新建项目"))
			create.Disabled(panel.ProjectDialogBusy)
			if create.Clicked() && !panel.ProjectDialogBusy {
				a.openProjectDialog(projectDialogCreate, "")
			}
			if panel.Scope == "project" && panel.ProjectID != "" {
				if project, ok := a.projectByID(panel.ProjectID); ok {
					a.projectMenu(c, p, project, "history")
				}
			}
		}
	})
}

func (a *nativeApp) historyScopeChip(c *ui.Context, p palette, scope, projectID, label string) {
	panel := &a.historyPanel
	active := panel.Scope == scope && panel.ProjectID == projectID
	key := "history-scope:" + scope
	if projectID != "" {
		key += ":" + projectID
	}
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 11).Radius(999).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if active {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.Text
		if active {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(11).TextColor(color).SingleLine()
	})
	b.Label(T(label)).Tooltip(Tf("筛选 %s", T(label)))
	if b.Clicked() {
		a.setHistoryScope(scope, projectID)
	}
}

func (a *nativeApp) historySearchRow(c *ui.Context, p palette) {
	panel := &a.historyPanel
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		input := ui.TextInput(c.Key("history-query"), &a.historyQuery).Placeholder(T("搜索会话标题、工作区、供应商或模型")).
			FontSize(12).Height(34).Padding(0, 9).Background(p.chip).Radius(8).Border(1, p.t.Border).
			Label(T("搜索会话"))
		input.Disabled(panel.Loading)
		search := ui.Button(c.Key("history-search"), T("搜索")).Label(T("搜索会话"))
		search.Disabled(panel.Loading)
		if search.Clicked() && !panel.Loading {
			a.applyHistoryQuery()
		}
		clear := ui.Button(c.Key("history-search-clear"), T("清空")).Label(T("清空会话搜索"))
		clear.Disabled(panel.Loading || (a.historyQuery == "" && panel.AppliedQuery == ""))
		if clear.Clicked() && !panel.Loading {
			a.clearHistoryQuery()
		}
		if panel.AppliedQuery != "" {
			badge(c, p, "筛选："+panel.AppliedQuery)
		}
	})
}

func (a *nativeApp) historyRow(c *ui.Context, p palette, s listedSession) {
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(8, 10).Radius(10).Margin(0, 0, 6, 0).
		Background(p.t.Background).Border(1, p.t.Border).Children(func() {
		main := ui.ButtonBase(c.Key("history:"+s.SessionID)).Grow(1).Padding(2, 4).AlignItems(ui.Center).Gap(10)
		if main.Hovered() {
			main.Background(p.hover)
		}
		main.Children(func() {
			ui.Icon(c, iconChat).FontSize(15).TextColor(p.t.Accent)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				title := sessionDisplayTitle(s.Title)
				if title == "" {
					title = T("未命名会话")
				}
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, title).FontSize(12).FontWeight(600).SingleLine()
					if s.Meta.Pinned {
						ui.Icon(c, iconPin).FontSize(11).TextColor(p.t.Accent)
					}
					if projectID := sessionProjectID(s); projectID != "" {
						badge(c, p, a.projectName(projectID))
					}
					if status := a.worktreeStatusLabel(s.Cwd); status != "" {
						badge(c, p, "工作树："+status)
					}
				})
				meta := strings.Join([]string{s.Cwd, strings.Trim(strings.Join([]string{s.Provider, s.Model}, " / "), " / "), relativeTime(s.UpdatedAt)}, " · ")
				ui.Text(c, meta).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
			})
			statusPill(c, p, historySessionStatus(s))
		})
		main.Label(Tf("打开会话 %s", sessionDisplayTitle(s.Title)))
		if main.Clicked() {
			a.closeHistory()
			a.selectSession(s)
		}
		a.sessionMenu(c, p, s, "history")
	})
}

func (a *nativeApp) historyErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(10).Radius(9).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(14).TextColor(p.t.Danger)
			ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Grow(1).Selectable()
			retry := ui.Button(c.Key("history-retry"), T("重试")).Label(T("重试加载会话历史"))
			if retry.Clicked() {
				a.refreshHistory()
			}
		})
	})
}

// --- dialogs ----------------------------------------------------------------

func (a *nativeApp) historyDialogs(c *ui.Context, p palette) {
	panel := &a.historyPanel
	if panel.DialogOpen {
		switch panel.DialogKind {
		case historyDialogRename, historyDialogProject:
			a.historyEditDialog(c, p)
		case historyDialogDelete:
			a.historyDeleteDialog(c, p)
		}
	}
	if panel.ProjectDialogOpen {
		a.historyProjectDialog(c, p)
	}
}

func (a *nativeApp) historyEditDialog(c *ui.Context, p palette) {
	panel := &a.historyPanel
	title := "重命名会话"
	desc := "新标题会写入 Runtime 拥有的会话标题记录。"
	if panel.DialogKind == historyDialogProject {
		title = "项目归属"
		desc = "会话归属由 mothx/session/setMeta 持久化；选择“不归属项目”会清除归属。"
	}
	dialog := ui.DialogBase(c, &panel.DialogOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(460).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconList).FontSize(16).TextColor(p.t.Accent)
				ui.Text(c, T(title)).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, T(desc)).FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			if panel.DialogKind == historyDialogRename {
				ui.TextInput(c.Key("history-dialog-title"), &panel.DialogValue).FontSize(12).Height(34).
					Padding(0, 9).Background(p.chip).Radius(8).Border(1, p.t.Border).
					Placeholder(T("会话标题")).Label(T("会话标题"))
			} else {
				a.historyProjectChoices(c, p)
			}
			if panel.DialogErr != "" {
				ui.Text(c, T(panel.DialogErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("history-dialog-cancel"), T("取消")).Label(T("取消"))
				cancel.Disabled(panel.DialogBusy)
				if cancel.Clicked() && !panel.DialogBusy {
					a.closeHistoryDialog()
				}
				confirm := ui.PrimaryButton(c.Key("history-dialog-confirm"), T("保存")).Label(T("保存"))
				confirm.Disabled(panel.DialogBusy)
				if confirm.Clicked() && !panel.DialogBusy {
					a.confirmHistoryDialog()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeHistoryDialog()
	}
}

func (a *nativeApp) historyProjectChoices(c *ui.Context, p palette) {
	ui.Column(c).Gap(4).Children(func() {
		a.historyProjectChoice(c, p, "", "不归属项目")
		for _, project := range a.projects {
			a.historyProjectChoice(c, p, project.ID, project.Name)
		}
		if len(a.projects) == 0 {
			ui.Text(c, T("尚未创建项目；可先在筛选栏新建项目。")).
				FontSize(10.5).TextColor(p.t.TextMuted)
		}
	})
}

func (a *nativeApp) historyProjectChoice(c *ui.Context, p palette, id, label string) {
	panel := &a.historyPanel
	active := panel.DialogValue == id
	b := ui.ButtonBase(c.Key("history-project-choice:"+id)).Height(30).Padding(6, 10).Radius(8).
		AlignItems(ui.Center).Gap(8).Border(1, p.t.Border)
	if active {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.Text
		if active {
			color = p.t.Accent
		}
		ui.Icon(c, iconList).FontSize(13).TextColor(color)
		ui.Text(c, T(label)).FontSize(11.5).TextColor(color).Grow(1)
	})
	b.Label(label)
	if b.Clicked() {
		panel.DialogValue = id
	}
}

func (a *nativeApp) historyDeleteDialog(c *ui.Context, p palette) {
	panel := &a.historyPanel
	dialog := ui.DialogBase(c, &panel.DialogOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(440).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, T("删除会话？")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			name := strings.TrimSpace(panel.DialogTitle)
			if name == "" {
				name = panel.DialogSession
			}
			ui.Text(c, Tf("将从 Runtime 的会话库删除 %q 及其运行记录。", name)).
				FontSize(11.5).TextColor(p.t.TextMuted).Selectable()
			ui.Text(c, T("该操作不可恢复；正在运行的会话必须先停止。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			if panel.DialogErr != "" {
				ui.Text(c, T(panel.DialogErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("history-delete-cancel"), T("取消")).Label(T("取消删除会话"))
				cancel.Disabled(panel.DialogBusy)
				if cancel.Clicked() && !panel.DialogBusy {
					a.closeHistoryDialog()
				}
				confirm := ui.Button(c.Key("history-delete-confirm"), T("确认删除")).Label(T("确认删除会话"))
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(panel.DialogBusy)
				if confirm.Clicked() && !panel.DialogBusy {
					a.confirmHistoryDialog()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeHistoryDialog()
	}
}

func (a *nativeApp) historyProjectDialog(c *ui.Context, p palette) {
	panel := &a.historyPanel
	title := "新建项目"
	deleteMode := panel.ProjectDialogKind == projectDialogDelete
	if panel.ProjectDialogKind == projectDialogRename {
		title = "重命名项目"
	} else if deleteMode {
		title = "删除项目"
	}
	dialog := ui.DialogBase(c, &panel.ProjectDialogOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(440).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconList).FontSize(16).TextColor(p.t.Accent)
				ui.Text(c, T(title)).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			if deleteMode {
				ui.Text(c, Tf("删除项目「%s」？会话本身会保留，但会清除其项目归属。", panel.ProjectDialogName)).
					FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.45)
			} else {
				ui.Text(c, T("项目只是会话分组元数据，由 Runtime 持久化；它不改变工作区授权。")).
					FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
				ui.TextInput(c.Key("history-project-name"), &panel.ProjectDialogName).FontSize(12).
					Height(34).Padding(0, 9).Background(p.chip).Radius(8).Border(1, p.t.Border).
					Placeholder(T("项目名称")).Label(T("项目名称"))
			}
			if panel.ProjectDialogErr != "" {
				ui.Text(c, T(panel.ProjectDialogErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("history-project-cancel"), T("取消")).Label(T("取消项目操作"))
				cancel.Disabled(panel.ProjectDialogBusy)
				if cancel.Clicked() && !panel.ProjectDialogBusy {
					a.closeProjectDialog()
				}
				label := "保存"
				if deleteMode {
					label = "确认删除"
				}
				confirm := ui.Button(c.Key("history-project-confirm"), T(label)).Label(T(label))
				if deleteMode {
					confirm.Background(p.errorBg).Border(1, p.t.Danger)
				}
				confirm.Disabled(panel.ProjectDialogBusy)
				if confirm.Clicked() && !panel.ProjectDialogBusy {
					a.confirmProjectDialog()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeProjectDialog()
	}
}

func historySessionStatus(s listedSession) string {
	if s.Meta.LastRun != nil {
		if s.Meta.LastRun.Active {
			return "运行中"
		}
		switch s.Meta.LastRun.Status {
		case "completed":
			return "已完成"
		case "failed", "expired", "timed_out":
			return "失败"
		case "incomplete":
			return "未完成"
		case "cancelled", "canceled":
			return "已取消"
		case "waiting_for_approval", "waiting_for_question", "pending":
			return "等待输入"
		}
	}
	return "空闲"
}
