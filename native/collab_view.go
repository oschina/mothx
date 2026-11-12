package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// routerDialog projects the session's external ACP worker selection. Native
// mirrors Desktop's composer popover: choose eligible workers, activate, or
// leave Router. Runtime validation and mode persistence stay server-side.
func (a *nativeApp) routerDialog(c *ui.Context, p palette) {
	if !a.routerCapable() || !a.router.Open {
		return
	}
	panel := &a.router
	dialog := ui.DialogBase(c, &panel.Open, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(620).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconCpu).FontSize(16).TextColor(p.t.Accent)
				ui.Column(c).Gap(1).Grow(1).Children(func() {
					ui.Text(c, T("Router 协作")).FontSize(14).FontWeight(650).TextColor(p.t.Text)
					ui.Text(c, T("把当前会话的主线委派给一个或多个外部 ACP 工作器。")).FontSize(10.5).TextColor(p.t.TextMuted)
				})
				refresh := ui.Button(c.Key("router-refresh"), T("刷新")).Label(T("刷新 Router 状态"))
				refresh.Disabled(panel.Loading || panel.Busy)
				if refresh.Clicked() && !panel.Loading && !panel.Busy {
					a.loadRouterState()
				}
				closeBtn := ui.Button(c.Key("router-close"), T("关闭")).Label(T("关闭 Router 面板"))
				if closeBtn.Clicked() {
					a.closeRouterPanel()
				}
			})
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				if panel.Enabled {
					badge(c, p, "已启用")
				} else {
					badge(c, p, "未启用")
				}
				if count := len(a.routerSelectableWorkers()); count > 0 {
					ui.Text(c, Tf("%d 个可用工作器", count)).FontSize(9.5).TextColor(p.t.TextMuted)
				}
				if len(panel.ProfileIDs) > 0 {
					ui.Text(c, strings.Join(panel.ProfileIDs, " · ")).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
				}
			})
			ui.Text(c, T("Router 只提交 worker id；驱动、模型、MCP 桥接与能力收紧由 ACP 工作器面板管理，授权由 Runtime 决定。")).
				FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.4)
			if !a.featureEnabled("manageACPProfiles") {
				ui.Text(c, T("当前运行时未公开 ACP 工作器目录，只能查看或停用已绑定的 Router 选择。")).
					FontSize(10.5).TextColor(p.t.Warning).LineHeight(1.4)
			}
			if panel.Loading && !panel.Loaded {
				ui.Text(c, T("正在读取 Router 状态…")).FontSize(11).TextColor(p.t.TextMuted)
				return
			}
			if panel.Err != "" {
				ui.Column(c).Padding(10).Radius(9).Background(p.errorBg).
					Border(1, ui.RGBA(255, 107, 107, 0.28)).Children(func() {
					ui.Text(c, T(panel.Err)).FontSize(11).TextColor(p.t.Danger).Selectable()
				})
			}
			if panel.Status != "" {
				ui.Text(c, T(panel.Status)).FontSize(10.5).TextColor(p.t.TextMuted).SingleLine()
			}
			if panel.Enabled {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, T("当前状态：已启用")).FontSize(11.5).FontWeight(600).TextColor(p.t.Text)
					if panel.Mode != "" {
						badge(c, p, "模式 "+panel.Mode)
					}
					for _, id := range panel.ProfileIDs {
						badge(c, p, id)
					}
				})
			}

			ui.Scroll(c).Height(240).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					workers := a.routerSelectableWorkers()
					if len(workers) == 0 {
						ui.Text(c, T("没有可用的 Router 工作器。请在 ACP 工作器面板启用一个 Router 可用的配置。")).
							FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.4)
						return
					}
					for _, worker := range workers {
						worker := worker
						checked := panel.Selection[worker.ID]
						ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(8, 10).Radius(8).
							Background(p.t.Background).Border(1, p.t.Border).Children(func() {
							box := ui.Checkbox(c.Key("router-worker:"+worker.ID), &checked, workerLabel(worker)).
								FontSize(11.5).Grow(1)
							box.Disabled(panel.Busy)
							if !panel.Busy && box.Changed() {
								a.toggleRouterWorker(worker.ID)
							}
							if worker.Model != "" {
								badge(c, p, worker.Model)
							}
							if worker.Driver != "" {
								badge(c, p, worker.Driver)
							}
						})
					}
				})
			})

			if a.routerWorkerMessagesCapable() {
				a.routerWorkerActivity(c, p)
			}

			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				if panel.Busy {
					statusPill(c, p, "running")
				}
				if panel.Enabled {
					leave := ui.Button(c.Key("router-deactivate"), T("停用 Router")).Label(T("停用 Router"))
					leave.Disabled(panel.Busy)
					if leave.Clicked() && !panel.Busy {
						a.deactivateRouter()
					}
				}
				activate := ui.PrimaryButton(c.Key("router-activate"), T("启用 Router")).Label(T("启用 Router"))
				activate.Disabled(panel.Busy || panel.Loading || len(a.routerSelectedIDs()) == 0)
				if activate.Clicked() && !panel.Busy && !panel.Loading && len(a.routerSelectedIDs()) > 0 {
					a.activateRouter()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeRouterPanel()
	}
}

func workerLabel(worker acpWorkerProfileView) string {
	if title := strings.TrimSpace(worker.Title); title != "" {
		return title + " · " + worker.ID
	}
	return worker.ID
}

// routerWorkerActivity renders the read-only worker evidence projection. It is
// informational: pending questions are explicitly not an answer surface.
func (a *nativeApp) routerWorkerActivity(c *ui.Context, p palette) {
	panel := &a.router
	ui.Box(c).Height(1).Background(p.t.Border)
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, T("Worker 活动")).FontSize(10).FontWeight(700).TextColor(p.t.TextMuted).Grow(1)
		if panel.WorkerLoading {
			statusPill(c, p, "running")
		}
		reload := ui.Button(c.Key("router-worker-refresh"), T("刷新")).Label(T("刷新 Worker 消息"))
		reload.Disabled(panel.WorkerLoading)
		if reload.Clicked() && !panel.WorkerLoading {
			a.startWorkerMessagesLoad()
		}
	})
	if panel.WorkerErr != "" {
		ui.Text(c, T(panel.WorkerErr)).FontSize(10).TextColor(p.t.Danger).SingleLine().Selectable()
	}
	if len(panel.PendingQuestions) > 0 {
		ui.Text(c, Tf("%d 个待回答的 Worker 问题（由 Runtime 决策链路处理，不在此回答）", len(panel.PendingQuestions))).
			FontSize(10).TextColor(p.t.Warning).LineHeight(1.35)
		pending := panel.PendingQuestions
		if len(pending) > 5 {
			pending = pending[len(pending)-5:]
		}
		ui.Column(c).Gap(4).Children(func() {
			for _, question := range pending {
				question := question
				ui.Column(c).Gap(2).Padding(6, 8).Radius(7).Background(p.t.Background).
					Border(1, p.t.Border).Children(func() {
					if identity := workerQuestionIdentity(question); identity != "" {
						ui.Text(c, identity).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
					}
					ui.Text(c, workerQuestionBody(question)).FontSize(10).TextColor(p.t.Text).LineHeight(1.35).Selectable()
					if options := workerQuestionOptionsLine(question); options != "" {
						ui.Text(c, options).FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35).Selectable()
					}
				})
			}
		})
	}
	if len(panel.Messages) == 0 {
		if !panel.WorkerLoading {
			ui.Text(c, T("没有 Worker 消息")).FontSize(10.5).TextColor(p.t.TextMuted)
		}
		return
	}
	ui.Scroll(c).Height(110).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			for _, message := range panel.Messages {
				line := workerMessageLine(message)
				if line == "" {
					continue
				}
				ui.Text(c, line).FontSize(10).TextColor(p.t.Text).LineHeight(1.35).Selectable()
			}
		})
	})
}

func workerMessageLine(message workerMessageView) string {
	parts := make([]string, 0, 4)
	if from := strings.TrimSpace(message.Direction); from != "" {
		parts = append(parts, from)
	}
	if kind := strings.TrimSpace(message.Kind); kind != "" {
		parts = append(parts, kind)
	}
	if child := strings.TrimSpace(message.ChildID); child != "" {
		parts = append(parts, child)
	}
	if status := strings.TrimSpace(message.Status); status != "" {
		parts = append(parts, status)
	}
	head := strings.Join(parts, " · ")
	body := strings.TrimSpace(message.Body)
	if body != "" {
		body = truncateOneLine(body, 160)
	}
	switch {
	case head != "" && body != "":
		return head + " — " + body
	case body != "":
		return body
	default:
		return head
	}
}

// workerQuestionIdentity is the read-only provenance line for a pending
// question. Child and run identity both come from the canonical Runtime view;
// the question id is only a fallback when neither is projected.
func workerQuestionIdentity(question workerQuestionView) string {
	parts := make([]string, 0, 3)
	if child := strings.TrimSpace(question.ChildID); child != "" {
		parts = append(parts, Tf("子代理 %s", child))
	}
	if run := strings.TrimSpace(question.RunID); run != "" {
		parts = append(parts, Tf("运行 %s", run))
	}
	if len(parts) == 0 {
		if id := strings.TrimSpace(question.ID); id != "" {
			parts = append(parts, Tf("问题 %s", id))
		}
	}
	return strings.Join(parts, " · ")
}

func workerQuestionBody(question workerQuestionView) string {
	body := strings.TrimSpace(question.Question)
	if body == "" {
		return T("（问题内容未提供）")
	}
	return truncateOneLine(body, 160)
}

func workerQuestionOptionsLine(question workerQuestionView) string {
	options := make([]string, 0, len(question.Options))
	for _, option := range question.Options {
		if trimmed := strings.TrimSpace(option); trimmed != "" {
			options = append(options, trimmed)
		}
	}
	if len(options) == 0 {
		return ""
	}
	return Tf("选项：%s", strings.Join(options, " / "))
}

// subagentDialog projects the read-only child-agent roster and transcript.
func (a *nativeApp) subagentDialog(c *ui.Context, p palette) {
	if !a.subagentCapable() || !a.subagent.Open {
		return
	}
	panel := &a.subagent
	dialog := ui.DialogBase(c, &panel.Open, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(760).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconUsers).FontSize(16).TextColor(p.t.Accent)
				ui.Column(c).Gap(1).Grow(1).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						ui.Text(c, T("子代理")).FontSize(14).FontWeight(650).TextColor(p.t.Text)
						badge(c, p, "只读")
						if running, total := subagentRunSummary(panel.Agents); total > 0 {
							ui.Text(c, Tf("%d 个成员 · %d 运行中", total, running)).
								FontSize(10).TextColor(p.t.TextMuted)
						}
					})
					ui.Text(c, T("只读投影：子代理列表与已发生的子会话事件；不会创建或驱动子代理。")).FontSize(10).TextColor(p.t.TextMuted)
				})
				refresh := ui.Button(c.Key("subagent-refresh"), T("刷新")).Label(T("刷新子代理"))
				refresh.Disabled(panel.Loading || panel.LoadingMsg)
				if refresh.Clicked() && !panel.Loading && !panel.LoadingMsg {
					a.loadSubagents()
				}
				closeBtn := ui.Button(c.Key("subagent-close"), T("关闭")).Label(T("关闭子代理面板"))
				if closeBtn.Clicked() {
					a.closeSubagentPanel()
				}
			})
			if panel.Loading && !panel.Loaded {
				ui.Text(c, T("正在读取子代理…")).FontSize(11).TextColor(p.t.TextMuted)
				return
			}
			if panel.Err != "" {
				ui.Column(c).Padding(10).Radius(9).Background(p.errorBg).
					Border(1, ui.RGBA(255, 107, 107, 0.28)).Children(func() {
					ui.Text(c, T(panel.Err)).FontSize(11).TextColor(p.t.Danger).Selectable()
				})
			}
			if len(panel.Agents) == 0 {
				ui.Text(c, T("当前会话没有子代理或团队成员记录。")).FontSize(11.5).TextColor(p.t.TextMuted).Padding(12, 4)
				return
			}
			ui.Row(c).Grow(1).AlignItems(ui.Start).Gap(12).Children(func() {
				ui.Column(c).Width(220).Gap(5).Children(func() {
					ui.Text(c, T("成员")).FontSize(10).FontWeight(700).TextColor(p.t.TextMuted)
					ui.Scroll(c).Height(300).Children(func() {
						ui.Column(c).Gap(4).Children(func() {
							for _, agent := range panel.Agents {
								a.subagentRow(c, p, agent)
							}
						})
					})
				})
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
						if info, ok := a.subagentSelectedInfo(); ok {
							ui.Text(c, subagentDisplayName(info)).FontSize(12).FontWeight(650).TextColor(p.t.Text).Grow(1).SingleLine()
							statusPill(c, p, info.Status)
						} else {
							ui.Text(c, T("选择一个子代理")).FontSize(11).TextColor(p.t.TextMuted).Grow(1)
						}
						if panel.LoadingMsg {
							statusPill(c, p, "running")
						}
					})
					if panel.Status != "" {
						ui.Text(c, T(panel.Status)).FontSize(10).TextColor(p.t.Danger).SingleLine()
					}
					ui.Scroll(c).Height(300).Children(func() {
						ui.Column(c).Gap(10).Children(func() {
							messages := panel.Messages
							if len(messages) == 0 && len(panel.Updates) > 0 {
								messages = reduceSubagentUpdates(panel.SessionID, panel.Updates)
							}
							if len(messages) == 0 && !panel.LoadingMsg {
								ui.Text(c, T("没有可显示的子代理事件")).FontSize(11).TextColor(p.t.TextMuted)
								return
							}
							for _, message := range messages {
								a.messageRow(c, p, message)
							}
						})
					})
				})
			})
		})
	})
	if dialog.Dismissed() {
		a.closeSubagentPanel()
	}
}

func (a *nativeApp) subagentRow(c *ui.Context, p palette, agent subagentInfoView) {
	panel := &a.subagent
	active := panel.Selected == agent.AgentID
	b := ui.ButtonBase(c.Key("subagent:"+agent.AgentID)).WidthPercent(100).Padding(7, 9).Radius(8).
		AlignItems(ui.Center).Gap(7).Border(1, p.t.Border)
	if active {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconUsers).FontSize(13).TextColor(p.t.Accent)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, subagentDisplayName(agent)).FontSize(11).FontWeight(600).TextColor(p.t.Text).SingleLine()
			meta := strings.TrimSpace(agent.MemberRole)
			if meta == "" {
				meta = Tf("%d 条消息", agent.MessageCount)
			}
			ui.Text(c, meta).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
		})
		statusPill(c, p, agent.Status)
	})
	b.Label(Tf("查看子代理 %s", subagentDisplayName(agent)))
	if b.Clicked() {
		a.selectSubagent(agent.AgentID)
	}
}
