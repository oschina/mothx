package main

import (
	"github.com/egoist/mygo/ui"
)

// This file owns the compact session/project action menus shared by the
// sidebar task tree, the history view, and the chat header. Every action is a
// projection of an ACP capability and reuses the canonical mutation path; the
// menu never keeps its own session or project state.

func (a *nativeApp) sessionMenu(c *ui.Context, p palette, session listedSession, scope string) {
	if session.SessionID == "" {
		return
	}
	key := "session-menu:" + scope + ":" + session.SessionID
	b := ui.ButtonBase(c.Key(key)).Size(22, 22).Radius(6).
		Justify(ui.Center).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconMore).FontSize(14).TextColor(p.t.TextMuted)
	})
	label := "会话操作"
	if scope == "header" {
		label = "当前会话操作"
	}
	b.Label(T(label)).Tooltip(T(label))
	b.PopoverMenuButton(func(m *ui.Menu) {
		a.buildSessionMenu(m, session)
	})
}

func (a *nativeApp) buildSessionMenu(m *ui.Menu, session listedSession) {
	prefix := "session:" + session.SessionID + ":"
	m.MaxWidth(280)
	metaCapable := a.historySessionMetaCapable()
	if metaCapable {
		pinned := session.Meta.Pinned
		label := "置顶"
		if pinned {
			label = "取消置顶"
		}
		if m.Item(T(label)).ID(prefix + "pin").Chosen() {
			a.toggleHistoryPin(session)
		}
	}
	if m.Item(T("重命名…")).ID(prefix + "rename").Chosen() {
		a.openHistoryDialog(historyDialogRename, session.SessionID)
	}
	if a.sessionRunActive(session) {
		if m.Item(T("停止运行")).ID(prefix + "cancel").Chosen() {
			a.cancelSessionRun(session.SessionID)
		}
	}

	if session.SessionID == a.currentSession && len(a.messages) > 0 {
		m.Separator()
		if m.Item(T("复制完整转录")).ID(prefix + "copy-transcript").Chosen() {
			a.copyTranscript()
		}
		if m.Item(T("导出转录…")).ID(prefix + "export-transcript").Chosen() {
			a.exportTranscript()
		}
	}

	if a.historyProjectsCapable() && metaCapable {
		m.Separator()
		projectID := sessionProjectID(session)
		m.Submenu(T("移动到项目"), func(sub *ui.Menu) {
			if len(a.projects) == 0 {
				sub.Item(T("尚未创建项目")).ID(prefix + "project:none").Disabled(true)
			}
			for _, project := range a.projects {
				project := project
				item := sub.Item(project.Name).ID(prefix + "project:" + project.ID).Radio(project.ID == projectID)
				if item.Chosen() {
					a.runHistoryProjectAssignment(session, project.ID)
				}
			}
			if projectID != "" {
				sub.Separator()
				if sub.Item(T("移出项目")).ID(prefix + "project:remove").Chosen() {
					a.runHistoryProjectAssignment(session, "")
				}
			}
		})
		if m.Item(T("新建项目并移动…")).ID(prefix + "project:create").Chosen() {
			a.openProjectDialogForSession(projectDialogCreate, "", session.SessionID)
		}
	}

	m.Separator()
	busy := a.sessionRunActive(session)
	if a.historyWorkDirCapable() {
		item := m.Item(T("设置工作目录…")).ID(prefix + "workdir").Disabled(busy)
		if item.Chosen() && !busy {
			a.chooseHistoryWorkDir(session)
		}
	}
	if a.historyForkCapable() {
		item := m.Item(T("Fork 会话")).ID(prefix + "fork").Disabled(busy)
		if item.Chosen() && !busy {
			a.forkHistorySession(session)
		}
	}

	m.Separator()
	item := m.Item(T("删除会话…")).ID(prefix + "delete").Danger()
	if busy {
		item.Disabled(true)
	} else if item.Chosen() {
		a.openHistoryDialog(historyDialogDelete, session.SessionID)
	}
}

// projectMenu is the compact project-row action menu. Creating a project is
// also available from the section header.
func (a *nativeApp) projectMenu(c *ui.Context, p palette, project projectView, scope string) {
	b := ui.ButtonBase(c.Key("project-menu:"+scope+":"+project.ID)).Size(22, 22).Radius(6).
		Justify(ui.Center).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconMore).FontSize(14).TextColor(p.t.TextMuted)
	})
	b.Label(T("项目操作")).Tooltip(T("项目操作"))
	b.PopoverMenuButton(func(m *ui.Menu) {
		m.MaxWidth(220)
		if m.Item(T("新建项目…")).ID("project:" + scope + ":" + project.ID + ":create").Chosen() {
			a.openProjectDialog(projectDialogCreate, "")
		}
		if m.Item(T("重命名项目…")).ID("project:" + scope + ":" + project.ID + ":rename").Chosen() {
			a.openProjectDialog(projectDialogRename, project.ID)
		}
		m.Separator()
		if m.Item(T("删除项目…")).ID("project:" + scope + ":" + project.ID + ":delete").Danger().Chosen() {
			a.openProjectDialog(projectDialogDelete, project.ID)
		}
	})
}

// sessionStatusDot draws the same per-session run vocabulary Desktop uses.
// The canonical lastRun projection decides the color; the client never turns a
// missing projection into a running state.
func sessionStatusDot(c *ui.Context, p palette, kind string) {
	color := p.t.TextMuted
	switch kind {
	case "working", "planning":
		color = p.t.Accent
	case "pending":
		color = p.t.Warning
	case "completed":
		color = p.t.Success
	case "failed", "incomplete":
		color = p.t.Danger
	case "cancelled":
		color = p.t.TextMuted
	}
	ui.Box(c).Size(6, 6).Radius(3).Background(color)
}
