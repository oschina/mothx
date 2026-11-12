package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsKnowledgePane renders the native knowledge-base manager inside the
// Settings View. Every fact comes from the ACP management projection: the
// source directory is read-only and is never scanned by this client, and
// deletion never removes the user's files.
func (a *nativeApp) settingsKnowledgePane(c *ui.Context, p palette) {
	if !a.knowledgeCapable() {
		a.settingsCard(c, p, "知识库", "当前运行时未公开知识库管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageKnowledgeBases；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	k := &a.knowledge
	a.knowledgePaneHeader(c, p)

	if k.Loading && !k.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取知识库配置与索引状态…")).FontSize(12).TextColor(p.t.TextMuted)
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
	if k.Loading && k.Loaded {
		ui.Text(c, T("正在刷新知识库投影…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	if len(k.Views) == 0 && !k.Creating {
		ui.Column(c).Padding(16).Radius(10).Border(1, p.t.Border).Gap(4).Children(func() {
			ui.Text(c, T("还没有知识库")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
			ui.Text(c, T("创建后可立即扫描，并使用本地索引预览查询。")).FontSize(10.5).TextColor(p.t.TextMuted)
		})
	}
	for index := range k.Views {
		view := k.Views[index]
		a.knowledgeBaseCard(c, p, &view, false)
	}
	if k.Creating {
		a.knowledgeBaseCard(c, p, nil, true)
	}
}

func (a *nativeApp) knowledgePaneHeader(c *ui.Context, p palette) {
	k := &a.knowledge
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Icon(c, iconBook).FontSize(18).TextColor(p.t.Accent)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, T("目录知识库")).FontSize(13).FontWeight(650).TextColor(p.t.Text)
			ui.Text(c, T("将本地目录建立为可重建的图谱索引。资料只通过 Runtime 与 ACP 管理，不会被 Native 直接读取。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
		})
		add := ui.PrimaryButton(c.Key("knowledge-add"), T("新建知识库"))
		add.Label(T("新建知识库")).Tooltip(T("创建目录知识库"))
		add.Disabled(!k.Loaded || k.Loading || k.Creating || k.Defaults == nil)
		if add.Clicked() && k.Loaded && !k.Loading && !k.Creating && k.Defaults != nil {
			a.openKnowledgeNew()
		}
	})
}

func (a *nativeApp) knowledgeErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("知识库管理不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.knowledgeCapable() {
			if ui.Button(c.Key("knowledge-retry"), T("重试")).Label(T("重试加载知识库")).Clicked() {
				a.knowledge.Loaded = false
				a.startKnowledgePanelLoad()
			}
		}
	})
}

func (a *nativeApp) knowledgeStatusBanner(c *ui.Context, p palette) {
	k := &a.knowledge
	color := p.t.TextMuted
	bg := p.thought
	if k.StatusIsError {
		color = p.t.Danger
		bg = p.errorBg
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(bg).
		Border(1, p.t.Border).Children(func() {
		if k.StatusIsError {
			ui.Icon(c, iconAlert).FontSize(13).TextColor(color)
		} else if knowledgePanelBusy(k) {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(k.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func knowledgePanelBusy(k *knowledgePanelState) bool {
	for _, action := range k.Busy {
		if action != "" {
			return true
		}
	}
	return false
}

func (a *nativeApp) knowledgeBaseCard(c *ui.Context, p palette, view *knowledgeBaseView, creating bool) {
	key := knowledgeNewDraftKey
	if view != nil {
		key = knowledgeDraftKey(view.KnowledgeBase.ID)
	}
	draft := a.ensureKnowledgeDraft(key)
	a.normalizeKnowledgeDraft(draft)

	title := strings.TrimSpace(draft.Name)
	if title == "" {
		if creating {
			title = "新知识库"
		} else if view != nil {
			title = view.KnowledgeBase.Name
		}
	}
	if title == "" {
		title = "未命名知识库"
	}
	desc := "选择知识来源目录、预处理类型、模型和扫描计划后保存。"
	if view != nil {
		desc = knowledgeStatusText(*view)
	}
	busy := a.knowledgeMutationBlocked()

	ui.Column(c).Padding(14).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconBook).FontSize(16).TextColor(p.t.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, T(title)).FontSize(13).FontWeight(650).TextColor(p.t.Text).SingleLine()
					if creating {
						badge(c, p, "新建")
					}
					if !draft.Enabled {
						badge(c, p, "已停用")
					}
					if view != nil && view.Indexing != nil && view.Indexing.Running {
						badge(c, p, "索引中")
					}
				})
				ui.Text(c, T(desc)).FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			})
		})

		a.knowledgeTextInput(c, p, knowledgeFieldKey(key, "name"), "名称", "", &draft.Name)
		a.knowledgeRootField(c, p, key, draft)
		a.knowledgeSelect(c, p, knowledgeFieldKey(key, "profile"), "预处理类型", knowledgeProfiles, &draft.PreprocessProfile)
		a.knowledgeProviderField(c, p, key, draft)
		a.knowledgeModelField(c, p, key, draft)
		a.knowledgeSelect(c, p, knowledgeFieldKey(key, "mode"), "模式", knowledgeModes, &draft.Mode)
		a.knowledgeTextInput(c, p, knowledgeFieldKey(key, "thinking"), "思考等级（可选）", "", &draft.ThinkingLevel)
		a.knowledgeTextInput(c, p, knowledgeFieldKey(key, "schedule"), "扫描周期",
			"manual / hourly / daily / @every 6h / 5 段 cron", &draft.Schedule)
		ui.Checkbox(c.Key(knowledgeFieldKey(key, "enabled")), &draft.Enabled, T("启用知识库")).FontSize(11.5)

		if view != nil && view.Snapshot != nil {
			ui.Text(c, knowledgeStatsText(view.Snapshot)).FontSize(11).TextColor(p.t.TextMuted)
		}
		if view != nil {
			a.knowledgeScheduleStatus(c, p, view.KnowledgeBase.ID, key)
		}
		if view != nil && a.knowledgeMCPCapable() {
			a.knowledgeMCPCard(c, p, view.KnowledgeBase.ID, key, busy)
		}
		if view != nil && (a.knowledgeRunsCapable() || a.knowledgeSourcesCapable() || a.knowledgeGraphCapable()) {
			a.knowledgeInsightsPanel(c, p, view.KnowledgeBase.ID, key, busy)
		}

		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(7).Children(func() {
			saveLabel := "保存配置"
			if creating {
				saveLabel = "创建知识库"
			}
			save := ui.PrimaryButton(c.Key(knowledgeFieldKey(key, "save")), T(saveLabel))
			save.Label(T(saveLabel))
			save.Disabled(busy)
			if save.Clicked() && !busy {
				id := ""
				if view != nil {
					id = view.KnowledgeBase.ID
				}
				a.saveKnowledgeBase(id)
			}
			if creating {
				cancel := ui.Button(c.Key(knowledgeFieldKey(key, "cancel")), T("取消"))
				cancel.Label(T("取消新建知识库"))
				if cancel.Clicked() {
					a.cancelKnowledgeNew()
				}
				return
			}
			refresh := ui.Button(c.Key(knowledgeFieldKey(key, "refresh")), T("刷新"))
			refresh.Label(T("刷新知识库状态"))
			refresh.Disabled(busy)
			if refresh.Clicked() && !busy && view != nil {
				a.refreshKnowledgeBase(view.KnowledgeBase.ID)
			}
			scan := ui.Button(c.Key(knowledgeFieldKey(key, "scan")), T("立即扫描"))
			scan.Label(T("立即扫描知识库"))
			scan.Disabled(busy || !draft.Enabled)
			if scan.Clicked() && !busy && draft.Enabled && view != nil {
				a.scanKnowledgeBase(view.KnowledgeBase.ID)
			}
			remove := ui.Button(c.Key(knowledgeFieldKey(key, "delete")), T("删除"))
			remove.Label(T("删除知识库配置和索引"))
			remove.Background(p.errorBg).Border(1, p.t.Danger)
			remove.Disabled(busy)
			if remove.Clicked() && !busy && view != nil {
				a.requestDeleteKnowledgeBase(view.KnowledgeBase.ID)
			}
		})
	})
}

func (a *nativeApp) knowledgeRootField(c *ui.Context, p palette, key string, draft *knowledgeBaseDraft) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T("知识源目录")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.TextInput(c.Key(knowledgeFieldKey(key, "root")), &draft.RootDir).FontSize(12).Height(32).Padding(0, 8).
				Placeholder(T("选择本地目录…")).Background(p.t.Background).Radius(8).Border(1, p.t.Border).Grow(1)
			choose := ui.Button(c.Key(knowledgeFieldKey(key, "choose-root")), T("选择目录"))
			choose.Label(T("选择知识源目录"))
			if choose.Clicked() {
				a.knowledgeChooseRoot(key)
			}
		})
	})
}

func (a *nativeApp) knowledgeProviderField(c *ui.Context, p palette, key string, draft *knowledgeBaseDraft) {
	options := []string{}
	if a.knowledge.Defaults != nil {
		options = a.knowledge.Defaults.Providers
	}
	if len(options) == 0 {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, T("供应商")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
			ui.Text(c, T("没有可用的供应商目录")).FontSize(11.5).TextColor(p.t.TextMuted)
		})
		return
	}
	a.knowledgeSelect(c, p, knowledgeFieldKey(key, "provider"), "供应商", options, &draft.Provider)
}

func (a *nativeApp) knowledgeModelField(c *ui.Context, p palette, key string, draft *knowledgeBaseDraft) {
	var models []providerCatalogModel
	if a.knowledge.Defaults != nil {
		models = a.knowledge.Defaults.Models
	}
	choices := knowledgeModelChoices(models, draft.Provider)
	if len(choices) == 0 {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, T("模型")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
			ui.Text(c, T("没有可用模型")).FontSize(11.5).TextColor(p.t.TextMuted)
		})
		draft.Model = ""
		return
	}
	for _, choice := range choices {
		if choice == draft.Model {
			a.knowledgeSelect(c, p, knowledgeFieldKey(key, "model"), "模型", choices, &draft.Model)
			return
		}
	}
	draft.Model = choices[0]
	a.knowledgeSelect(c, p, knowledgeFieldKey(key, "model"), "模型", choices, &draft.Model)
}

func (a *nativeApp) knowledgeScheduleStatus(c *ui.Context, p palette, id, key string) {
	schedule, ok := a.knowledge.Schedules[id]
	if !ok {
		return
	}
	busy := a.knowledgeMutationBlocked()
	ui.Column(c).Gap(5).Padding(9).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Icon(c, iconClock).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("扫描计划")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		})
		ui.Text(c, knowledgeScheduleText(schedule)).FontSize(11).TextColor(p.t.Text)
		if schedule.LastStatus != "" {
			ui.Text(c, Tf("上次结果：%s", T(schedule.LastStatus))).FontSize(10.5).TextColor(p.t.TextMuted)
		}
		if !schedule.RootAvailable {
			ui.Text(c, T("知识源目录不可用，已停止触发；恢复目录后请手动重扫。")).
				FontSize(10.5).TextColor(p.t.Danger).LineHeight(1.35)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			label := "恢复"
			action := "恢复知识库扫描计划"
			if schedule.Enabled {
				label = "暂停"
				action = "暂停知识库扫描计划"
			}
			btn := ui.Button(c.Key(knowledgeFieldKey(key, "schedule-toggle")), T(label))
			btn.Label(T(action))
			btn.Disabled(busy)
			if btn.Clicked() && !busy {
				a.toggleKnowledgeSchedule(id)
			}
		})
	})
}

func (a *nativeApp) knowledgeMCPCard(c *ui.Context, p palette, id, key string, busy bool) {
	entry, exists := a.knowledgeMCPEntry(id)
	willEnable := !exists || !knowledgeMCPEnabled(entry)
	status := "未配置"
	if exists {
		if knowledgeMCPEnabled(entry) {
			status = "已启用"
		} else {
			status = "已停用"
		}
	}
	action := "快速添加 MCP"
	if willEnable {
		if exists {
			action = "启用 MCP 工具"
		}
	} else {
		action = "停用 MCP 工具"
	}
	ui.Column(c).Padding(10).Radius(10).Background(p.t.Background).
		Border(1, p.t.Border).Gap(6).Children(func() {
		ui.Text(c, T("MCP 快速接入")).FontSize(12).FontWeight(650).TextColor(p.t.Text)
		ui.Text(c, T("一键添加标准 MCP 工具，让新会话可搜索此知识库。无需填写命令或参数。")).
			FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, T("MCP 状态：")+T(status)).FontSize(11).TextColor(p.t.TextMuted).Grow(1)
			btn := ui.Button(c.Key(knowledgeFieldKey(key, "mcp")), T(action))
			btn.Label(T(action))
			btn.Disabled(busy)
			if btn.Clicked() && !busy {
				a.applyKnowledgeMCP(id)
			}
		})
		ui.Text(c, T("此操作仅对新创建或重新加载的 ACP 会话生效；更改后需新建或重载会话。")).
			FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35)
	})
}

func (a *nativeApp) knowledgeInsightsPanel(c *ui.Context, p palette, id, key string, busy bool) {
	k := &a.knowledge
	ins := k.Insights[id]
	if ins == nil {
		ins = &knowledgeInsightsState{}
		if k.Insights == nil {
			k.Insights = make(map[string]*knowledgeInsightsState)
		}
		k.Insights[id] = ins
	}
	if ins.Tab < 0 {
		ins.Tab = 0
	}

	ui.Column(c).Padding(10).Radius(10).Background(p.t.Background).
		Border(1, p.t.Border).Gap(7).Children(func() {
		tabKinds := make([]string, 0, 2)
		labels := make([]string, 0, 2)
		if a.knowledgeRunsCapable() {
			tabKinds = append(tabKinds, "runs")
			labels = append(labels, T("索引历史"))
		}
		if a.knowledgeSourcesCapable() {
			tabKinds = append(tabKinds, "sources")
			labels = append(labels, T("来源浏览"))
		}

		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			clear := ui.Button(c.Key(knowledgeFieldKey(key, "clear")), T("清除索引"))
			clear.Label(T("清除知识库索引"))
			clear.Disabled(busy)
			if clear.Clicked() && !busy {
				a.requestClearKnowledge(id)
			}
		})

		if ins.Err != "" {
			ui.Text(c, T(ins.Err)).FontSize(10.5).TextColor(p.t.Danger).LineHeight(1.35)
		}
		if len(tabKinds) > 0 {
			if ins.Tab >= len(tabKinds) || ins.Tab < 0 {
				ins.Tab = 0
			}
			selected := ins.Tab
			ui.Tabs(c.Key(knowledgeFieldKey(key, "insights-tabs")), &selected, labels...)
			ins.Tab = selected
			switch tabKinds[ins.Tab] {
			case "runs":
				a.knowledgeRunsTab(c, p, id, key, ins)
			case "sources":
				a.knowledgeSourcesTab(c, p, ins)
			}
		}
		a.knowledgeQueryPreview(c, p, id, key, ins)
	})
}

func (a *nativeApp) knowledgeRunsTab(c *ui.Context, p palette, id, key string, ins *knowledgeInsightsState) {
	if len(ins.Runs) == 0 {
		ui.Text(c, T("暂无索引运行记录")).FontSize(11).TextColor(p.t.TextMuted)
		return
	}
	for index, run := range ins.Runs {
		run := run
		last := index == len(ins.Runs)-1
		ui.Column(c).Gap(2).Padding(2, 0, 7, 0).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, T(run.Status)).FontSize(12).FontWeight(600).TextColor(p.t.Text)
				if run.Active {
					badge(c, p, "当前活动")
				}
				ui.Box(c).Grow(1)
				detail := ui.Button(c.Key("knowledge:"+key+":run-detail:"+run.RunID), T("详情"))
				detail.Label(T("查看索引运行详情"))
				detail.Disabled(ins.RunDetailBusy)
				if detail.Clicked() && !ins.RunDetailBusy {
					a.loadKnowledgeRunDetail(id, run.RunID)
				}
			})
			window := knowledgeFormatDate(run.StartedAt)
			if finished := knowledgeFormatDate(run.FinishedAt); finished != "" {
				window += " → " + finished
			}
			if window != "" {
				ui.Text(c, window).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			ui.Text(c, knowledgeRunStatsText(run)).FontSize(10.5).TextColor(p.t.TextMuted)
			if run.ErrorSummary != "" {
				ui.Text(c, T("失败诊断：")+run.ErrorSummary).FontSize(10.5).TextColor(p.t.Danger).LineHeight(1.35)
			}
			if ins.RunDetailBusy && ins.RunDetailID == run.RunID {
				ui.Text(c, T("正在读取运行详情…")).FontSize(10).TextColor(p.t.TextMuted)
			} else if ins.RunDetailErr != "" && ins.RunDetailID == run.RunID {
				ui.Text(c, T(ins.RunDetailErr)).FontSize(10).TextColor(p.t.Danger).LineHeight(1.35)
			} else if ins.RunDetail != nil && ins.RunDetail.RunID == run.RunID {
				detailText := T("运行 ") + ins.RunDetail.RunID
				if ins.RunDetail.SnapshotID != "" {
					detailText += T(" · 快照 ") + ins.RunDetail.SnapshotID
				}
				ui.Text(c, detailText).FontSize(10).TextColor(p.t.TextMuted).Selectable()
				if ins.RunDetail.ErrorCode != "" {
					ui.Text(c, T("错误码：")+ins.RunDetail.ErrorCode).FontSize(10).TextColor(p.t.Danger)
				}
			}
			if !last {
				ui.Box(c).Height(1).Background(p.t.Border)
			}
		})
	}
}

func (a *nativeApp) knowledgeSourcesTab(c *ui.Context, p palette, ins *knowledgeInsightsState) {
	if len(ins.Sources) == 0 {
		ui.Text(c, T("当前快照暂无来源文件")).FontSize(11).TextColor(p.t.TextMuted)
		return
	}
	for index, source := range ins.Sources {
		source := source
		last := index == len(ins.Sources)-1
		ui.Column(c).Gap(2).Padding(2, 0, 7, 0).Children(func() {
			title := strings.TrimSpace(source.Title)
			if title == "" {
				title = source.RelativePath
			}
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(p.t.Text).Selectable()
			if source.RelativePath != "" && source.RelativePath != title {
				ui.Text(c, source.RelativePath).FontSize(10.5).TextColor(p.t.TextMuted).Selectable()
			}
			ui.Text(c, knowledgeSourceMeta(source)).FontSize(10.5).TextColor(p.t.TextMuted)
			if !last {
				ui.Box(c).Height(1).Background(p.t.Border)
			}
		})
	}
}

func (a *nativeApp) knowledgeQueryPreview(c *ui.Context, p palette, id, key string, ins *knowledgeInsightsState) {
	if !a.knowledgeGraphCapable() {
		return
	}
	view := a.knowledgeViewByID(id)
	if view == nil || view.Snapshot == nil {
		return
	}
	ui.Column(c).Gap(6).Padding(2, 0, 0, 0).Children(func() {
		ui.Text(c, T("查询预览（仅调试）")).FontSize(11).FontWeight(650).TextColor(p.t.Text)
		ui.Text(c, T("返回有界图谱证据，不会注入主会话上下文。")).FontSize(10).TextColor(p.t.TextMuted)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.TextInput(c.Key(knowledgeFieldKey(key, "query")), &ins.Query).FontSize(12).Height(32).
				Padding(0, 8).Placeholder(T("输入关键词…")).
				Background(p.t.Surface).Radius(8).Border(1, p.t.Border).Grow(1)
			btn := ui.Button(c.Key(knowledgeFieldKey(key, "query-run")), T("查询"))
			btn.Label(T("预览查询知识库"))
			btn.Disabled(ins.QueryBusy)
			if btn.Clicked() && !ins.QueryBusy {
				a.runKnowledgeQuery(id)
			}
		})
		if ins.QueryBusy {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				statusPill(c, p, "running")
				ui.Text(c, T("正在查询活动快照…")).FontSize(10.5).TextColor(p.t.TextMuted)
			})
		}
		if ins.QueryErr != "" {
			ui.Text(c, T(ins.QueryErr)).FontSize(10.5).TextColor(p.t.Danger).LineHeight(1.35)
		}
		if ins.QueryResult != nil {
			a.knowledgeQueryResult(c, p, ins.QueryResult)
		}
	})
}

func (a *nativeApp) knowledgeQueryResult(c *ui.Context, p palette, result *knowledgeGraphQuery) {
	ui.Column(c).Gap(3).Children(func() {
		summary := Tf("返回 %d 个分块 · %d 个节点", len(result.Chunks), len(result.Nodes))
		if result.Truncated {
			summary += T(" · 结果已截断")
		}
		ui.Text(c, summary).FontSize(10.5).TextColor(p.t.TextMuted)
		if len(result.Chunks) == 0 {
			ui.Text(c, T("没有匹配的证据")).FontSize(10.5).TextColor(p.t.TextMuted)
			return
		}
		limit := len(result.Chunks)
		if limit > 5 {
			limit = 5
		}
		for _, chunk := range result.Chunks[:limit] {
			chunk := chunk
			ui.Column(c).Gap(1).Padding(5, 7).Radius(8).Background(p.t.Surface).Children(func() {
				path := strings.TrimSpace(chunk.RelativePath)
				if path == "" {
					path = chunk.ID
				}
				ui.Text(c, fmt.Sprintf("%s:%d-%d", path, chunk.StartLine, chunk.EndLine)).
					FontSize(10).TextColor(p.t.TextMuted).Selectable()
				ui.Text(c, knowledgeTruncate(chunk.Text, 260)).FontSize(10.5).TextColor(p.t.Text).LineHeight(1.35)
			})
		}
	})
}

func (a *nativeApp) knowledgeTextInput(c *ui.Context, p palette, key, label, placeholder string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border)
		if placeholder != "" {
			input.Placeholder(placeholder)
		}
	})
}

func (a *nativeApp) knowledgeSelect(c *ui.Context, p palette, key, label string, options []string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.Select(c.Key(key), value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T(label))
	})
}

func knowledgeFieldKey(draftKey, field string) string {
	return "knowledge:" + draftKey + ":" + field
}

// settingsKnowledgeDialogs renders Native delete/clear confirmations above the
// settings page. Neither action deletes the source directory.
func (a *nativeApp) settingsKnowledgeDialogs(c *ui.Context, p palette) {
	if !a.settingsOpen && a.page != "library" {
		return
	}
	panel := &a.knowledge
	if panel.PendingDeleteOpen {
		a.knowledgeDeleteDialog(c, p)
	}
	if panel.PendingClearOpen {
		a.knowledgeClearDialog(c, p)
	}
}

func (a *nativeApp) knowledgeDeleteDialog(c *ui.Context, p palette) {
	panel := &a.knowledge
	dialog := ui.DialogBase(c, &panel.PendingDeleteOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(460).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, T("删除知识库？")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, Tf("将删除「%s」的配置、索引和扫描计划。", panel.PendingDeleteName)).
				FontSize(11.5).TextColor(p.t.Text).LineHeight(1.4)
			if panel.PendingDeleteRoot != "" {
				ui.Text(c, T("知识源目录：")+panel.PendingDeleteRoot).FontSize(10.5).
					TextColor(p.t.TextMuted).Selectable()
			}
			ui.Text(c, T("知识源目录及其中的文件不会被删除。")).
				FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("knowledge-delete-cancel"), T("取消"))
				cancel.Label(T("取消删除知识库"))
				if cancel.Clicked() {
					a.cancelDeleteKnowledgeBase()
				}
				confirm := ui.Button(c.Key("knowledge-delete-confirm"), T("确认删除"))
				confirm.Label(T("确认删除知识库"))
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				if confirm.Clicked() {
					a.confirmDeleteKnowledgeBase()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelDeleteKnowledgeBase()
	}
}

func (a *nativeApp) knowledgeClearDialog(c *ui.Context, p palette) {
	panel := &a.knowledge
	dialog := ui.DialogBase(c, &panel.PendingClearOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(440).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Warning)
				ui.Text(c, T("清除知识库索引？")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, Tf("将清除「%s」的活动索引快照。", panel.PendingClearName)).
				FontSize(11.5).TextColor(p.t.Text).LineHeight(1.4)
			ui.Text(c, T("配置与知识源目录会保留；重新扫描前无法查询该知识库。")).
				FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("knowledge-clear-cancel"), T("取消"))
				cancel.Label(T("取消清除知识库索引"))
				if cancel.Clicked() {
					a.cancelClearKnowledge()
				}
				confirm := ui.Button(c.Key("knowledge-clear-confirm"), T("确认清除"))
				confirm.Label(T("确认清除知识库索引"))
				confirm.Background(p.warnBg).Border(1, p.t.Warning)
				if confirm.Clicked() {
					a.confirmClearKnowledge()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelClearKnowledge()
	}
}
