package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsCronPane renders the Native equivalent of the Desktop CronPanel in
// the Settings View. It is capability-gated by manageCron and only calls
// mothx/manage/cron/list, create, update, remove, and run; schedules and run
// state remain Runtime-owned and this panel never starts a local scheduler.
func (a *nativeApp) settingsCronPane(c *ui.Context, p palette) {
	if !a.cronCapable() {
		a.settingsCard(c, p, "定时任务", "当前运行时未公开定时任务管理能力。", func() {
			ui.Text(c, "需要 ACP 能力 manageCron；当前版本不会显示或调用占位表单。").
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.cron
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, "正在读取定时任务…").FontSize(12).TextColor(p.t.TextMuted)
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
		ui.Text(c, "正在刷新定时任务…").FontSize(10.5).TextColor(p.t.TextMuted)
	}

	desc := "调度随 Native/ACP 在线生效；任务持久化在共享 Cron 存储，由 Runtime 执行。"
	a.settingsCard(c, p, "定时任务", desc, func() {
		ui.Column(c).Gap(9).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, iconClock).FontSize(15).TextColor(p.t.Accent)
				if panel.Enabled {
					if panel.Running {
						badge(c, p, "调度器运行中")
					} else {
						badge(c, p, "调度器已停止")
					}
				} else {
					badge(c, p, "调度器未启用")
				}
				badge(c, p, fmt.Sprintf("%d 个任务", len(panel.Jobs)))
				ui.Text(c, "由 ACP 调度器执行").FontSize(9.5).TextColor(p.t.TextMuted).Grow(1)
				refresh := ui.Button(c.Key("cron-refresh"), "刷新")
				refresh.Label("刷新定时任务列表")
				refresh.Disabled(panel.Loading || a.cronBusy())
				if refresh.Clicked() && !panel.Loading && !a.cronBusy() {
					a.startCronPanelRefresh()
				}
				add := ui.PrimaryButton(c.Key("cron-add"), "新建任务")
				add.Label("新建定时任务").Tooltip("打开原生定时任务编辑对话框")
				add.Disabled(panel.Loading || a.cronBusy())
				if add.Clicked() && !panel.Loading && !a.cronBusy() {
					a.openCronNewDraft()
				}
			})
			if len(panel.Jobs) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
					ui.Text(c, "还没有定时任务").FontSize(12).FontWeight(600).TextColor(p.t.Text)
					ui.Text(c, "新建任务后，Native 会通过 ACP 管理共享 Cron 调度器。").
						FontSize(10.5).TextColor(p.t.TextMuted)
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					for index := range panel.Jobs {
						index := index
						a.cronJobRow(c, p, index)
					}
				})
			}
		})
	})
}

func (a *nativeApp) cronErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, "定时任务管理不可用").FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, message).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.cronCapable() {
			if ui.Button(c.Key("cron-retry"), "重试").Label("重试加载定时任务").Clicked() {
				a.cron.Loaded = false
				a.startCronPanelLoad()
			}
		}
	})
}

func (a *nativeApp) cronStatusBanner(c *ui.Context, p palette) {
	panel := &a.cron
	color := p.t.TextMuted
	bg := p.thought
	if panel.StatusIsError {
		color = p.t.Danger
		bg = p.errorBg
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(bg).
		Border(1, p.t.Border).Children(func() {
		if panel.StatusIsError {
			ui.Icon(c, iconAlert).FontSize(13).TextColor(color)
		} else if panel.Saving || panel.Deleting || panel.RunningJobID != "" || panel.TogglingID != "" {
			statusPill(c, p, "running")
		}
		ui.Text(c, panel.Status).FontSize(11).TextColor(color).Grow(1)
	})
}

func (a *nativeApp) cronJobRow(c *ui.Context, p palette, index int) {
	panel := &a.cron
	if index < 0 || index >= len(panel.Jobs) {
		return
	}
	job := panel.Jobs[index]
	busy := panel.Loading || a.cronBusy()
	ui.Column(c).Padding(10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := p.t.TextMuted
			if job.Enabled {
				color = p.t.Accent
			}
			ui.Icon(c, iconClock).FontSize(15).TextColor(color)
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, cronJobDisplayName(job)).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					if mode := strings.TrimSpace(job.Mode); mode != "" {
						badge(c, p, mode)
					}
					if job.OneShot || strings.TrimSpace(job.Schedule) == "" || strings.EqualFold(strings.TrimSpace(job.Schedule), "@once") {
						badge(c, p, "单次")
					}
					if job.Enabled {
						badge(c, p, "已启用")
					} else {
						badge(c, p, "已暂停")
					}
				})
				ui.Text(c, cronJobMeta(job)).FontSize(9.5).TextColor(p.t.TextMuted).Selectable()
				if prompt := strings.TrimSpace(job.Prompt); prompt != "" {
					ui.Text(c, truncateOneLine(prompt, 180)).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
				}
				if lastError := strings.TrimSpace(job.LastError); lastError != "" {
					ui.Text(c, "最近错误："+lastError).FontSize(9.5).TextColor(p.t.Danger).SingleLine().Selectable()
				}
			})
		})
		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(6).Children(func() {
			run := ui.Button(c.Key(fmt.Sprintf("cron-run:%d", index)), "立即运行")
			run.Label("立即运行定时任务 " + cronJobDisplayName(job))
			run.Disabled(busy)
			if run.Clicked() && !busy {
				a.runCronJob(index)
			}
			toggleLabel := "暂停"
			if !job.Enabled {
				toggleLabel = "恢复"
			}
			toggle := ui.Button(c.Key(fmt.Sprintf("cron-toggle:%d", index)), toggleLabel)
			toggle.Label(toggleLabel + "定时任务 " + cronJobDisplayName(job))
			toggle.Disabled(busy)
			if toggle.Clicked() && !busy {
				a.toggleCronJob(index)
			}
			edit := ui.Button(c.Key(fmt.Sprintf("cron-edit:%d", index)), "编辑")
			edit.Label("编辑定时任务 " + cronJobDisplayName(job))
			edit.Disabled(busy)
			if edit.Clicked() && !busy {
				a.openCronEditDraft(index)
			}
			remove := ui.Button(c.Key(fmt.Sprintf("cron-delete:%d", index)), "删除")
			remove.Label("删除定时任务 " + cronJobDisplayName(job))
			remove.Background(p.errorBg).Border(1, p.t.Danger)
			remove.Disabled(busy)
			if remove.Clicked() && !busy {
				a.requestDeleteCron(index)
			}
		})
	})
}

// settingsCronDialogs renders the Native create/edit and delete confirmation
// dialogs above the settings page. Neither is a browser or OS prompt.
func (a *nativeApp) settingsCronDialogs(c *ui.Context, p palette) {
	if !a.settingsOpen || !a.cronCapable() {
		return
	}
	panel := &a.cron
	if panel.PendingDelete {
		a.cronDeleteDialog(c, p)
	}
	if panel.EditorOpen {
		a.cronEditorDialog(c, p)
	}
}

func (a *nativeApp) cronDeleteDialog(c *ui.Context, p palette) {
	panel := &a.cron
	dialog := ui.DialogBase(c, &panel.PendingDelete, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, "删除定时任务？").FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			name := strings.TrimSpace(panel.PendingDeleteID)
			if job, ok := a.cronJobByID(name); ok {
				name = cronJobDisplayName(job)
			}
			if name == "" {
				name = "该任务"
			}
			ui.Text(c, fmt.Sprintf("将从共享 Cron 存储中删除 %q。该操作不会删除历史会话或运行记录。", name)).
				FontSize(11.5).TextColor(p.t.TextMuted).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("cron-delete-cancel"), "取消")
				cancel.Label("取消删除定时任务")
				if cancel.Clicked() {
					a.cancelDeleteCron()
				}
				confirm := ui.Button(c.Key("cron-delete-confirm"), "确认删除")
				confirm.Label("确认删除定时任务")
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(panel.Deleting || strings.TrimSpace(panel.PendingDeleteID) == "")
				if confirm.Clicked() && !panel.Deleting {
					a.confirmDeleteCron()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelDeleteCron()
	}
}

func (a *nativeApp) cronEditorDialog(c *ui.Context, p palette) {
	panel := &a.cron
	title := "新建定时任务"
	if strings.TrimSpace(panel.EditingID) != "" {
		title = "编辑定时任务"
	}
	dialog := ui.DialogBase(c, &panel.EditorOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(580).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconClock).FontSize(16).TextColor(p.t.Accent)
				ui.Text(c, title).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, "通过 mothx/manage/cron/create 或 update 保存；调度表达式由共享 Cron 校验。").
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			ui.Scroll(c).Height(380).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					a.cronLabeledInput(c, p, "cron-name", "名称", &panel.Draft.Name)
					a.cronLabeledInput(c, p, "cron-schedule", "Cron 表达式", &panel.Draft.Schedule)
					ui.Text(c, "示例：@once、@every 30m、@hourly、@daily、@weekly、@monthly 或 5 段 cron。").
						FontSize(9.5).TextColor(p.t.TextMuted).LineHeight(1.4)
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, "模式").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
						ui.Select(c.Key("cron-mode"), &panel.Draft.Mode, cronModes).
							FontSize(12).Height(32).Padding(0, 8).
							Background(p.t.Background).Radius(8).Border(1, p.t.Border).
							Label("定时任务模式")
					})
					ui.Checkbox(c, &panel.Draft.Enabled, "启用任务").FontSize(11.5)
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, "任务提示词").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
						ui.TextAreaBase(c.Key("cron-prompt"), &panel.Draft.Prompt).
							FontSize(12).LineHeight(1.45).Lines(4, 8).Padding(6, 8).
							Placeholder("描述每次调度时 Agent 需要完成的任务…").
							Background(p.t.Background).Radius(8).Border(1, p.t.Border).
							Label("任务提示词")
					})
				})
			})
			if panel.DialogErr != "" {
				ui.Text(c, panel.DialogErr).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("cron-cancel"), "取消")
				cancel.Label("取消编辑定时任务")
				if cancel.Clicked() {
					a.closeCronEditor()
				}
				save := ui.PrimaryButton(c.Key("cron-save"), "保存")
				save.Label("保存定时任务")
				save.Disabled(panel.Saving)
				if save.Clicked() && !panel.Saving {
					a.saveCronDraft()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeCronEditor()
	}
}

func (a *nativeApp) cronLabeledInput(c *ui.Context, p palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(label)
	})
}
