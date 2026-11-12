package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// worktreeDialog projects the shared worktree manager. It is a chat-header
// popover in Desktop; Native renders the same read/write surface as a modal so
// creation, readiness, reset, and removal stay explicit.
func (a *nativeApp) worktreeDialog(c *ui.Context, p palette) {
	if !a.worktreeCapable() || !a.worktree.Open {
		return
	}
	panel := &a.worktree
	dialog := ui.DialogBase(c, &panel.Open, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(680).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconBranch).FontSize(16).TextColor(p.t.Accent)
				ui.Column(c).Gap(1).Grow(1).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
						ui.Text(c, T("隔离工作树")).FontSize(14).FontWeight(650).TextColor(p.t.Text)
						badge(c, p, worktreeSummary(len(panel.Worktrees)))
						if panel.Creating {
							statusPill(c, p, "running")
						}
					})
					ui.Text(c, panel.BaseCwd).FontSize(10).TextColor(p.t.TextMuted).SingleLine().Selectable()
				})
				refresh := ui.Button(c.Key("worktree-refresh"), T("刷新")).Label(T("刷新工作树"))
				refresh.Disabled(panel.Loading || panel.Creating)
				if refresh.Clicked() && !panel.Loading && !panel.Creating {
					a.loadWorktrees()
				}
				closeBtn := ui.Button(c.Key("worktree-close"), T("关闭")).Label(T("关闭工作树面板"))
				if closeBtn.Clicked() {
					a.closeWorktreePanel()
				}
			})

			ui.Text(c, T("创建、删除与重置由共享 Runtime 执行 git 操作与目录授权；Native 只提交目录和可选名称。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)

			if panel.Status != "" {
				ui.Text(c, T(panel.Status)).FontSize(10.5).TextColor(p.t.TextMuted).SingleLine()
			}
			if panel.Err != "" {
				ui.Column(c).Padding(10).Radius(9).Background(p.errorBg).
					Border(1, ui.RGBA(255, 107, 107, 0.28)).Children(func() {
					ui.Text(c, T(panel.Err)).FontSize(11).TextColor(p.t.Danger).Selectable()
				})
			}

			a.worktreeCreateRow(c, p)

			ui.Box(c).Height(1).Background(p.t.Border)
			ui.Scroll(c).Height(260).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					if panel.Loading && !panel.Loaded {
						ui.Text(c, T("正在读取工作树…")).FontSize(11).TextColor(p.t.TextMuted)
						return
					}
					if len(panel.Worktrees) == 0 {
						ui.Text(c, worktreeSummary(0)).FontSize(11).TextColor(p.t.TextMuted)
						return
					}
					for _, tree := range panel.Worktrees {
						a.worktreeRow(c, p, tree)
					}
				})
			})
		})
	})
	if dialog.Dismissed() {
		a.closeWorktreePanel()
	}
	if panel.ConfirmOpen {
		a.worktreeConfirmDialog(c, p)
	}
}

func (a *nativeApp) worktreeCreateRow(c *ui.Context, p palette) {
	panel := &a.worktree
	busy := panel.Creating || panel.Loading
	ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
		ui.Column(c).Gap(3).Grow(1).Children(func() {
			ui.Text(c, T("新工作树名称（可选）")).FontSize(10).TextColor(p.t.TextMuted)
			input := ui.TextInput(c.Key("worktree-name"), &panel.DraftName).FontSize(11.5).
				Height(32).Padding(0, 8).Placeholder(T("留空由 Runtime 命名")).
				Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("新工作树名称"))
			input.Disabled(busy)
		})
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, T("创建后")).FontSize(10).TextColor(p.t.TextMuted)
			ui.Checkbox(c.Key("worktree-open-new-chat"), &panel.OpenInNewChat, T("在新会话打开")).FontSize(10.5)
		})
		create := ui.PrimaryButton(c.Key("worktree-create"), T("创建工作树"))
		create.Label(T("创建隔离工作树"))
		create.Disabled(busy)
		if create.Clicked() && !busy {
			a.createWorktree()
		}
	})
}

func (a *nativeApp) worktreeRow(c *ui.Context, p palette, tree worktreeView) {
	panel := &a.worktree
	ui.Column(c).Padding(9, 10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconBranch).FontSize(14).TextColor(p.t.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, worktreeDisplayName(tree)).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					badge(c, p, worktreeStatusLabel(tree.Status))
					if tree.External {
						badge(c, p, "外部")
					}
					if branch := strings.TrimSpace(tree.Branch); branch != "" {
						badge(c, p, branch)
					}
				})
				ui.Text(c, tree.Directory).FontSize(10).TextColor(p.t.TextMuted).SingleLine().Selectable()
				if tree.Error != "" {
					ui.Text(c, T(tree.Error)).FontSize(10).TextColor(p.t.Danger).SingleLine()
				}
			})
			if panel.BusyDirectory == tree.Directory {
				statusPill(c, p, "running")
			}
			if !tree.External {
				open := ui.Button(c.Key("worktree-use:"+tree.Directory), T("打开会话")).Label(T("在此工作树新建会话"))
				open.Disabled(a.running || a.loadingSession != "" || !worktreeReady(tree))
				if open.Clicked() && !a.running && a.loadingSession == "" && worktreeReady(tree) {
					a.closeWorktreePanel()
					a.newChatIn(tree.Directory)
				}
				if a.worktreeResetCapable() {
					reset := ui.Button(c.Key("worktree-reset:"+tree.Directory), T("重置")).Label(T("重置工作树"))
					reset.Disabled(panel.ConfirmBusy || panel.Creating)
					if reset.Clicked() && !panel.ConfirmBusy && !panel.Creating {
						a.askResetWorktree(tree)
					}
				}
				remove := ui.Button(c.Key("worktree-remove:"+tree.Directory), T("移除")).Label(T("移除工作树"))
				remove.Background(p.errorBg).Border(1, p.t.Danger)
				remove.Disabled(panel.ConfirmBusy || panel.Creating)
				if remove.Clicked() && !panel.ConfirmBusy && !panel.Creating {
					a.askRemoveWorktree(tree)
				}
			}
		})
	})
}

func (a *nativeApp) worktreeConfirmDialog(c *ui.Context, p palette) {
	panel := &a.worktree
	title := T("移除工作树？")
	desc := T("将删除该隔离工作树的目录登记；正在运行的会话需要 Runtime 允许后才会被强制停止。")
	label := T("确认移除")
	if panel.ConfirmKind == worktreeConfirmReset {
		title = T("重置工作树？")
		desc = T("将把工作树恢复到干净状态；未提交的本地改动可能丢失。")
		label = T("确认重置工作树")
	}
	dialog := ui.DialogBase(c, &panel.ConfirmOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(460).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, title).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, worktreeDisplayName(panel.ConfirmTree)+" · "+panel.ConfirmTree.Directory).
				FontSize(11).TextColor(p.t.TextMuted).Selectable().LineHeight(1.4)
			ui.Text(c, desc).FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			if panel.ConfirmErr != "" {
				ui.Text(c, T(panel.ConfirmErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("worktree-confirm-cancel"), T("取消")).Label(T("取消工作树操作"))
				cancel.Disabled(panel.ConfirmBusy)
				if cancel.Clicked() && !panel.ConfirmBusy {
					a.closeWorktreeConfirm()
				}
				confirm := ui.Button(c.Key("worktree-confirm-ok"), label).Label(label)
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(panel.ConfirmBusy)
				if confirm.Clicked() && !panel.ConfirmBusy {
					a.confirmWorktreeAction()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeWorktreeConfirm()
	}
}
