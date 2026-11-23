package cron

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// settingsCronPane renders the Native equivalent of the Desktop CronPanel in
// the Settings View. It is capability-gated by manageCron and only calls
// mothx/manage/cron/list, create, update, remove, and run; schedules and run
// state remain Runtime-owned and this panel never starts a local scheduler.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "定时任务", "当前运行时未公开定时任务管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageCron；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取定时任务…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}

	if p.Err != "" {
		p.ErrCard(h, c, pal, p.Err)
		if !p.Loaded {
			return
		}
	}
	if p.Status != "" {
		p.StatusBanner(h, c, pal)
	}
	if p.Loading && p.Loaded {
		ui.Text(c, i18n.T("正在刷新定时任务…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	desc := "调度随 Native/ACP 在线生效；任务持久化在共享 Cron 存储，由 Runtime 执行。"
	kit.SettingsCard(c, pal, "定时任务", desc, func() {
		ui.Column(c).Gap(9).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, icons.IconClock).FontSize(15).TextColor(pal.T.Accent)
				if p.Enabled {
					if p.Running {
						kit.Badge(c, pal, "调度器运行中")
					} else {
						kit.Badge(c, pal, "调度器已停止")
					}
				} else {
					kit.Badge(c, pal, "调度器未启用")
				}
				kit.Badge(c, pal, i18n.Tf("%d 个任务", len(p.Jobs)))
				ui.Text(c, i18n.T("由 ACP 调度器执行")).FontSize(9.5).TextColor(pal.T.TextMuted).Grow(1)
				refresh := ui.Button(c.Key("cron-refresh"), i18n.T("刷新"))
				refresh.Label(i18n.T("刷新定时任务列表"))
				refresh.Disabled(p.Loading || p.Busy())
				if refresh.Clicked() && !p.Loading && !p.Busy() {
					p.StartRefresh(h)
				}
				add := ui.PrimaryButton(c.Key("cron-add"), i18n.T("新建任务"))
				add.Label(i18n.T("新建定时任务")).Tooltip(i18n.T("打开原生定时任务编辑对话框"))
				add.Disabled(p.Loading || p.Busy())
				if add.Clicked() && !p.Loading && !p.Busy() {
					p.OpenNewDraft()
				}
			})
			if len(p.Jobs) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
					ui.Text(c, i18n.T("还没有定时任务")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
					ui.Text(c, i18n.T("新建任务后，Native 会通过 ACP 管理共享 Cron 调度器。")).
						FontSize(10.5).TextColor(pal.T.TextMuted)
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					for index := range p.Jobs {
						index := index
						p.JobRow(h, c, pal, index)
					}
				})
			}
		})
	})
}

func (p *Panel) ErrCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("定时任务管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("cron-retry"), i18n.T("重试")).Label(i18n.T("重试加载定时任务")).Clicked() {
				p.Loaded = false
				p.StartLoad(h)
			}
		}
	})
}

func (p *Panel) StatusBanner(h Host, c *ui.Context, pal kit.Palette) {
	color := pal.T.TextMuted
	bg := pal.Thought
	if p.StatusIsError {
		color = pal.T.Danger
		bg = pal.ErrorBg
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(bg).
		Border(1, pal.T.Border).Children(func() {
		if p.StatusIsError {
			ui.Icon(c, icons.IconAlert).FontSize(13).TextColor(color)
		} else if p.Saving || p.Deleting || p.RunningJobID != "" || p.TogglingID != "" {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) JobRow(h Host, c *ui.Context, pal kit.Palette, index int) {
	if index < 0 || index >= len(p.Jobs) {
		return
	}
	job := p.Jobs[index]
	busy := p.Loading || p.Busy()
	ui.Column(c).Padding(10).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := pal.T.TextMuted
			if job.Enabled {
				color = pal.T.Accent
			}
			ui.Icon(c, icons.IconClock).FontSize(15).TextColor(color)
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, JobDisplayName(job)).FontSize(12).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					if mode := strings.TrimSpace(job.Mode); mode != "" {
						kit.Badge(c, pal, mode)
					}
					if job.OneShot || strings.TrimSpace(job.Schedule) == "" || strings.EqualFold(strings.TrimSpace(job.Schedule), "@once") {
						kit.Badge(c, pal, "单次")
					}
					if job.Enabled {
						kit.Badge(c, pal, "已启用")
					} else {
						kit.Badge(c, pal, "已暂停")
					}
				})
				ui.Text(c, JobMeta(job)).FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
				if prompt := strings.TrimSpace(job.Prompt); prompt != "" {
					ui.Text(c, kit.TruncateOneLine(prompt, 180)).FontSize(10).TextColor(pal.T.TextMuted).SingleLine()
				}
				if lastError := strings.TrimSpace(job.LastError); lastError != "" {
					ui.Text(c, i18n.T("最近错误：")+lastError).FontSize(9.5).TextColor(pal.T.Danger).SingleLine().Selectable()
				}
			})
		})
		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(6).Children(func() {
			run := ui.Button(c.Key(fmt.Sprintf("cron-run:%d", index)), i18n.T("立即运行"))
			run.Label(i18n.T("立即运行定时任务 ") + JobDisplayName(job))
			run.Disabled(busy)
			if run.Clicked() && !busy {
				p.Run(h, index)
			}
			toggleLabel := "暂停"
			if !job.Enabled {
				toggleLabel = "恢复"
			}
			toggle := ui.Button(c.Key(fmt.Sprintf("cron-toggle:%d", index)), i18n.T(toggleLabel))
			if job.Enabled {
				toggle.Label(i18n.T("暂停定时任务 ") + JobDisplayName(job))
			} else {
				toggle.Label(i18n.T("恢复定时任务 ") + JobDisplayName(job))
			}
			toggle.Disabled(busy)
			if toggle.Clicked() && !busy {
				p.Toggle(h, index)
			}
			edit := ui.Button(c.Key(fmt.Sprintf("cron-edit:%d", index)), i18n.T("编辑"))
			edit.Label(i18n.T("编辑定时任务 ") + JobDisplayName(job))
			edit.Disabled(busy)
			if edit.Clicked() && !busy {
				p.OpenEditDraft(index)
			}
			remove := ui.Button(c.Key(fmt.Sprintf("cron-delete:%d", index)), i18n.T("删除"))
			remove.Label(i18n.T("删除定时任务 ") + JobDisplayName(job))
			remove.Background(pal.ErrorBg).Border(1, pal.T.Danger)
			remove.Disabled(busy)
			if remove.Clicked() && !busy {
				p.RequestDelete(index)
			}
		})
	})
}

// Dialogs renders the Native create/edit and delete confirmation
// dialogs above the settings page. Neither is a browser or OS prompt.
func (p *Panel) Dialogs(h Host, c *ui.Context, pal kit.Palette) {
	if (!h.SettingsOpen() && h.Page() != "automation") || !p.Capable(h) {
		return
	}
	if p.PendingDelete {
		p.deleteDialog(h, c, pal)
	}
	if p.EditorOpen {
		p.editorDialog(h, c, pal)
	}
}

func (p *Panel) deleteDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.PendingDelete, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Danger)
				ui.Text(c, i18n.T("删除定时任务？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			name := strings.TrimSpace(p.PendingDeleteID)
			if job, ok := p.JobByID(name); ok {
				name = JobDisplayName(job)
			}
			if name == "" {
				name = "该任务"
			}
			ui.Text(c, i18n.Tf("将从共享 Cron 存储中删除 %q。该操作不会删除历史会话或运行记录。", name)).
				FontSize(11.5).TextColor(pal.T.TextMuted).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("cron-delete-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消删除定时任务"))
				if cancel.Clicked() {
					p.CancelDelete()
				}
				confirm := ui.Button(c.Key("cron-delete-confirm"), i18n.T("确认删除"))
				confirm.Label(i18n.T("确认删除定时任务"))
				confirm.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				confirm.Disabled(p.Deleting || strings.TrimSpace(p.PendingDeleteID) == "")
				if confirm.Clicked() && !p.Deleting {
					p.ConfirmDelete(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CancelDelete()
	}
}

func (p *Panel) editorDialog(h Host, c *ui.Context, pal kit.Palette) {
	title := "新建定时任务"
	if strings.TrimSpace(p.EditingID) != "" {
		title = "编辑定时任务"
	}
	dialog := ui.DialogBase(c, &p.EditorOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(580).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconClock).FontSize(16).TextColor(pal.T.Accent)
				ui.Text(c, i18n.T(title)).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.T("通过 mothx/manage/cron/create 或 update 保存；调度表达式由共享 Cron 校验。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			ui.Scroll(c).Height(380).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					p.labeledInput(h, c, pal, "cron-name", "名称", &p.Draft.Name)
					p.labeledInput(h, c, pal, "cron-schedule", "Cron 表达式", &p.Draft.Schedule)
					ui.Text(c, i18n.T("示例：@once、@every 30m、@hourly、@daily、@weekly、@monthly 或 5 段 cron。")).
						FontSize(9.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, i18n.T("模式")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
						ui.Select(c.Key("cron-mode"), &p.Draft.Mode, Modes).
							FontSize(12).Height(32).Padding(0, 8).
							Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
							Label(i18n.T("定时任务模式"))
					})
					ui.Checkbox(c, &p.Draft.Enabled, i18n.T("启用任务")).FontSize(11.5)
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, i18n.T("任务提示词")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
						ui.TextAreaBase(c.Key("cron-prompt"), &p.Draft.Prompt).
							FontSize(12).LineHeight(1.45).Lines(4, 8).Padding(6, 8).
							Placeholder(i18n.T("描述每次调度时 Agent 需要完成的任务…")).
							Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
							Label(i18n.T("任务提示词"))
					})
				})
			})
			if p.DialogErr != "" {
				ui.Text(c, i18n.T(p.DialogErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("cron-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消编辑定时任务"))
				if cancel.Clicked() {
					p.CloseEditor()
				}
				save := ui.PrimaryButton(c.Key("cron-save"), i18n.T("保存"))
				save.Label(i18n.T("保存定时任务"))
				save.Disabled(p.Saving)
				if save.Clicked() && !p.Saving {
					p.Save(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CloseEditor()
	}
}

func (p *Panel) labeledInput(h Host, c *ui.Context, pal kit.Palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T(label))
	})
}
