package knowledge

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/feature/providers"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the native knowledge-base manager inside the
// Settings View. Every fact comes from the ACP management projection: the
// source directory is read-only and is never scanned by this client, and
// deletion never removes the user's files.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "知识库", "当前运行时未公开知识库管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageKnowledgeBases；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	p.PaneHeader(c, pal)

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取知识库配置与索引状态…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		p.StatusBanner(c, pal)
	}
	if p.Loading && p.Loaded {
		ui.Text(c, i18n.T("正在刷新知识库投影…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	if len(p.Views) == 0 && !p.Creating {
		ui.Column(c).Padding(16).Radius(10).Border(1, pal.T.Border).Gap(4).Children(func() {
			ui.Text(c, i18n.T("还没有知识库")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
			ui.Text(c, i18n.T("创建后可立即扫描，并使用本地索引预览查询。")).FontSize(10.5).TextColor(pal.T.TextMuted)
		})
	}
	for index := range p.Views {
		view := p.Views[index]
		p.BaseCard(h, c, pal, &view, false)
	}
	if p.Creating {
		p.BaseCard(h, c, pal, nil, true)
	}
}

func (p *Panel) PaneHeader(c *ui.Context, pal kit.Palette) {
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Icon(c, icons.IconBook).FontSize(18).TextColor(pal.T.Accent)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, i18n.T("目录知识库")).FontSize(13).FontWeight(650).TextColor(pal.T.Text)
			ui.Text(c, i18n.T("将本地目录建立为可重建的图谱索引。资料只通过 Runtime 与 ACP 管理，不会被 Native 直接读取。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
		})
		add := ui.PrimaryButton(c.Key("knowledge-add"), i18n.T("新建知识库"))
		add.Label(i18n.T("新建知识库")).Tooltip(i18n.T("创建目录知识库"))
		add.Disabled(!p.Loaded || p.Loading || p.Creating || p.Defaults == nil)
		if add.Clicked() && p.Loaded && !p.Loading && !p.Creating && p.Defaults != nil {
			p.OpenNew()
		}
	})
}

func (p *Panel) ErrCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("知识库管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("knowledge-retry"), i18n.T("重试")).Label(i18n.T("重试加载知识库")).Clicked() {
				p.Loaded = false
				p.StartLoad(h)
			}
		}
	})
}

func (p *Panel) StatusBanner(c *ui.Context, pal kit.Palette) {
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
		} else if PanelBusy(&p.State) {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) BaseCard(h Host, c *ui.Context, pal kit.Palette, view *BaseView, creating bool) {
	key := NewDraftKey
	if view != nil {
		key = DraftKey(view.KnowledgeBase.ID)
	}
	draft := p.EnsureDraft(key)
	p.NormalizeDraft(draft)

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
		desc = StatusText(*view)
	}
	busy := p.MutationBlocked()

	ui.Column(c).Padding(14).Radius(12).Background(pal.T.Surface).Border(1, pal.T.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconBook).FontSize(16).TextColor(pal.T.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, i18n.T(title)).FontSize(13).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					if creating {
						kit.Badge(c, pal, "新建")
					}
					if !draft.Enabled {
						kit.Badge(c, pal, "已停用")
					}
					if view != nil && view.Indexing != nil && view.Indexing.Running {
						kit.Badge(c, pal, "索引中")
					}
				})
				ui.Text(c, i18n.T(desc)).FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			})
		})

		p.TextInput(c, pal, knowledgeFieldKey(key, "name"), "名称", "", &draft.Name)
		p.RootField(h, c, pal, key, draft)
		p.SelectField(c, pal, knowledgeFieldKey(key, "profile"), "预处理类型", Profiles, &draft.PreprocessProfile)
		p.ProviderField(c, pal, key, draft)
		p.ModelField(c, pal, key, draft)
		p.SelectField(c, pal, knowledgeFieldKey(key, "mode"), "模式", Modes, &draft.Mode)
		p.TextInput(c, pal, knowledgeFieldKey(key, "thinking"), "思考等级（可选）", "", &draft.ThinkingLevel)
		p.TextInput(c, pal, knowledgeFieldKey(key, "schedule"), "扫描周期",
			"manual / hourly / daily / @every 6h / 5 段 cron", &draft.Schedule)
		ui.Checkbox(c.Key(knowledgeFieldKey(key, "enabled")), &draft.Enabled, i18n.T("启用知识库")).FontSize(11.5)

		if view != nil && view.Snapshot != nil {
			ui.Text(c, StatsText(view.Snapshot)).FontSize(11).TextColor(pal.T.TextMuted)
		}
		if view != nil {
			p.ScheduleStatus(h, c, pal, view.KnowledgeBase.ID, key)
		}
		if view != nil && p.MCPCapable(h) {
			p.MCPCard(h, c, pal, view.KnowledgeBase.ID, key, busy)
		}
		if view != nil && (p.RunsCapable(h) || p.SourcesCapable(h) || p.GraphCapable(h)) {
			p.InsightsPanel(h, c, pal, view.KnowledgeBase.ID, key, busy)
		}

		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(7).Children(func() {
			saveLabel := "保存配置"
			if creating {
				saveLabel = "创建知识库"
			}
			save := ui.PrimaryButton(c.Key(knowledgeFieldKey(key, "save")), i18n.T(saveLabel))
			save.Label(i18n.T(saveLabel))
			save.Disabled(busy)
			if save.Clicked() && !busy {
				id := ""
				if view != nil {
					id = view.KnowledgeBase.ID
				}
				p.Save(h, id)
			}
			if creating {
				cancel := ui.Button(c.Key(knowledgeFieldKey(key, "cancel")), i18n.T("取消"))
				cancel.Label(i18n.T("取消新建知识库"))
				if cancel.Clicked() {
					p.CancelNew()
				}
				return
			}
			refresh := ui.Button(c.Key(knowledgeFieldKey(key, "refresh")), i18n.T("刷新"))
			refresh.Label(i18n.T("刷新知识库状态"))
			refresh.Disabled(busy)
			if refresh.Clicked() && !busy && view != nil {
				p.Refresh(h, view.KnowledgeBase.ID)
			}
			scan := ui.Button(c.Key(knowledgeFieldKey(key, "scan")), i18n.T("立即扫描"))
			scan.Label(i18n.T("立即扫描知识库"))
			scan.Disabled(busy || !draft.Enabled)
			if scan.Clicked() && !busy && draft.Enabled && view != nil {
				p.Scan(h, view.KnowledgeBase.ID)
			}
			remove := ui.Button(c.Key(knowledgeFieldKey(key, "delete")), i18n.T("删除"))
			remove.Label(i18n.T("删除知识库配置和索引"))
			remove.Background(pal.ErrorBg).Border(1, pal.T.Danger)
			remove.Disabled(busy)
			if remove.Clicked() && !busy && view != nil {
				p.RequestDelete(view.KnowledgeBase.ID)
			}
		})
	})
}

func (p *Panel) RootField(h Host, c *ui.Context, pal kit.Palette, key string, draft *BaseDraft) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T("知识源目录")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.TextInput(c.Key(knowledgeFieldKey(key, "root")), &draft.RootDir).FontSize(12).Height(32).Padding(0, 8).
				Placeholder(i18n.T("选择本地目录…")).Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Grow(1)
			choose := ui.Button(c.Key(knowledgeFieldKey(key, "choose-root")), i18n.T("选择目录"))
			choose.Label(i18n.T("选择知识源目录"))
			if choose.Clicked() {
				p.ChooseRoot(h, key)
			}
		})
	})
}

func (p *Panel) ProviderField(c *ui.Context, pal kit.Palette, key string, draft *BaseDraft) {
	options := []string{}
	if p.Defaults != nil {
		options = p.Defaults.Providers
	}
	if len(options) == 0 {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, i18n.T("供应商")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("没有可用的供应商目录")).FontSize(11.5).TextColor(pal.T.TextMuted)
		})
		return
	}
	p.SelectField(c, pal, knowledgeFieldKey(key, "provider"), "供应商", options, &draft.Provider)
}

func (p *Panel) ModelField(c *ui.Context, pal kit.Palette, key string, draft *BaseDraft) {
	var models []providers.CatalogModel
	if p.Defaults != nil {
		models = p.Defaults.Models
	}
	choices := ModelChoices(models, draft.Provider)
	if len(choices) == 0 {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, i18n.T("模型")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("没有可用模型")).FontSize(11.5).TextColor(pal.T.TextMuted)
		})
		draft.Model = ""
		return
	}
	for _, choice := range choices {
		if choice == draft.Model {
			p.SelectField(c, pal, knowledgeFieldKey(key, "model"), "模型", choices, &draft.Model)
			return
		}
	}
	draft.Model = choices[0]
	p.SelectField(c, pal, knowledgeFieldKey(key, "model"), "模型", choices, &draft.Model)
}

func (p *Panel) ScheduleStatus(h Host, c *ui.Context, pal kit.Palette, id, key string) {
	schedule, ok := p.Schedules[id]
	if !ok {
		return
	}
	busy := p.MutationBlocked()
	ui.Column(c).Gap(5).Padding(9).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Icon(c, icons.IconClock).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("扫描计划")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		})
		ui.Text(c, ScheduleText(schedule)).FontSize(11).TextColor(pal.T.Text)
		if schedule.LastStatus != "" {
			ui.Text(c, i18n.Tf("上次结果：%s", i18n.T(schedule.LastStatus))).FontSize(10.5).TextColor(pal.T.TextMuted)
		}
		if !schedule.RootAvailable {
			ui.Text(c, i18n.T("知识源目录不可用，已停止触发；恢复目录后请手动重扫。")).
				FontSize(10.5).TextColor(pal.T.Danger).LineHeight(1.35)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			label := "恢复"
			action := "恢复知识库扫描计划"
			if schedule.Enabled {
				label = "暂停"
				action = "暂停知识库扫描计划"
			}
			btn := ui.Button(c.Key(knowledgeFieldKey(key, "schedule-toggle")), i18n.T(label))
			btn.Label(i18n.T(action))
			btn.Disabled(busy)
			if btn.Clicked() && !busy {
				p.ToggleSchedule(h, id)
			}
		})
	})
}

func (p *Panel) MCPCard(h Host, c *ui.Context, pal kit.Palette, id, key string, busy bool) {
	entry, exists := p.MCPEntry(id)
	willEnable := !exists || !MCPEnabled(entry)
	status := "未配置"
	if exists {
		if MCPEnabled(entry) {
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
	ui.Column(c).Padding(10).Radius(10).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(6).Children(func() {
		ui.Text(c, i18n.T("MCP 快速接入")).FontSize(12).FontWeight(650).TextColor(pal.T.Text)
		ui.Text(c, i18n.T("一键添加标准 MCP 工具，让新会话可搜索此知识库。无需填写命令或参数。")).
			FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, i18n.T("MCP 状态：")+i18n.T(status)).FontSize(11).TextColor(pal.T.TextMuted).Grow(1)
			btn := ui.Button(c.Key(knowledgeFieldKey(key, "mcp")), i18n.T(action))
			btn.Label(i18n.T(action))
			btn.Disabled(busy)
			if btn.Clicked() && !busy {
				p.ApplyMCP(h, id)
			}
		})
		ui.Text(c, i18n.T("此操作仅对新创建或重新加载的 ACP 会话生效；更改后需新建或重载会话。")).
			FontSize(10).TextColor(pal.T.TextMuted).LineHeight(1.35)
	})
}

func (p *Panel) InsightsPanel(h Host, c *ui.Context, pal kit.Palette, id, key string, busy bool) {
	ins := p.Insights[id]
	if ins == nil {
		ins = &InsightsState{}
		if p.Insights == nil {
			p.Insights = make(map[string]*InsightsState)
		}
		p.Insights[id] = ins
	}
	if ins.Tab < 0 {
		ins.Tab = 0
	}

	ui.Column(c).Padding(10).Radius(10).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(7).Children(func() {
		tabKinds := make([]string, 0, 2)
		labels := make([]string, 0, 2)
		if p.RunsCapable(h) {
			tabKinds = append(tabKinds, "runs")
			labels = append(labels, i18n.T("索引历史"))
		}
		if p.SourcesCapable(h) {
			tabKinds = append(tabKinds, "sources")
			labels = append(labels, i18n.T("来源浏览"))
		}

		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			clear := ui.Button(c.Key(knowledgeFieldKey(key, "clear")), i18n.T("清除索引"))
			clear.Label(i18n.T("清除知识库索引"))
			clear.Disabled(busy)
			if clear.Clicked() && !busy {
				p.RequestClear(id)
			}
		})

		if ins.Err != "" {
			ui.Text(c, i18n.T(ins.Err)).FontSize(10.5).TextColor(pal.T.Danger).LineHeight(1.35)
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
				p.RunsTab(h, c, pal, id, key, ins)
			case "sources":
				p.SourcesTab(c, pal, ins)
			}
		}
		p.QueryPreview(h, c, pal, id, key, ins)
	})
}

func (p *Panel) RunsTab(h Host, c *ui.Context, pal kit.Palette, id, key string, ins *InsightsState) {
	if len(ins.Runs) == 0 {
		ui.Text(c, i18n.T("暂无索引运行记录")).FontSize(11).TextColor(pal.T.TextMuted)
		return
	}
	for index, run := range ins.Runs {
		run := run
		last := index == len(ins.Runs)-1
		ui.Column(c).Gap(2).Padding(2, 0, 7, 0).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, i18n.T(run.Status)).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
				if run.Active {
					kit.Badge(c, pal, "当前活动")
				}
				ui.Box(c).Grow(1)
				detail := ui.Button(c.Key("knowledge:"+key+":run-detail:"+run.RunID), i18n.T("详情"))
				detail.Label(i18n.T("查看索引运行详情"))
				detail.Disabled(ins.RunDetailBusy)
				if detail.Clicked() && !ins.RunDetailBusy {
					p.LoadRunDetail(h, id, run.RunID)
				}
			})
			window := FormatDate(run.StartedAt)
			if finished := FormatDate(run.FinishedAt); finished != "" {
				window += " → " + finished
			}
			if window != "" {
				ui.Text(c, window).FontSize(10.5).TextColor(pal.T.TextMuted)
			}
			ui.Text(c, RunStatsText(run)).FontSize(10.5).TextColor(pal.T.TextMuted)
			if run.ErrorSummary != "" {
				ui.Text(c, i18n.T("失败诊断：")+run.ErrorSummary).FontSize(10.5).TextColor(pal.T.Danger).LineHeight(1.35)
			}
			if ins.RunDetailBusy && ins.RunDetailID == run.RunID {
				ui.Text(c, i18n.T("正在读取运行详情…")).FontSize(10).TextColor(pal.T.TextMuted)
			} else if ins.RunDetailErr != "" && ins.RunDetailID == run.RunID {
				ui.Text(c, i18n.T(ins.RunDetailErr)).FontSize(10).TextColor(pal.T.Danger).LineHeight(1.35)
			} else if ins.RunDetail != nil && ins.RunDetail.RunID == run.RunID {
				detailText := i18n.T("运行 ") + ins.RunDetail.RunID
				if ins.RunDetail.SnapshotID != "" {
					detailText += i18n.T(" · 快照 ") + ins.RunDetail.SnapshotID
				}
				ui.Text(c, detailText).FontSize(10).TextColor(pal.T.TextMuted).Selectable()
				if ins.RunDetail.ErrorCode != "" {
					ui.Text(c, i18n.T("错误码：")+ins.RunDetail.ErrorCode).FontSize(10).TextColor(pal.T.Danger)
				}
			}
			if !last {
				ui.Box(c).Height(1).Background(pal.T.Border)
			}
		})
	}
}

func (p *Panel) SourcesTab(c *ui.Context, pal kit.Palette, ins *InsightsState) {
	if len(ins.Sources) == 0 {
		ui.Text(c, i18n.T("当前快照暂无来源文件")).FontSize(11).TextColor(pal.T.TextMuted)
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
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(pal.T.Text).Selectable()
			if source.RelativePath != "" && source.RelativePath != title {
				ui.Text(c, source.RelativePath).FontSize(10.5).TextColor(pal.T.TextMuted).Selectable()
			}
			ui.Text(c, SourceMeta(source)).FontSize(10.5).TextColor(pal.T.TextMuted)
			if !last {
				ui.Box(c).Height(1).Background(pal.T.Border)
			}
		})
	}
}

func (p *Panel) QueryPreview(h Host, c *ui.Context, pal kit.Palette, id, key string, ins *InsightsState) {
	if !p.GraphCapable(h) {
		return
	}
	view := p.ViewByID(id)
	if view == nil || view.Snapshot == nil {
		return
	}
	ui.Column(c).Gap(6).Padding(2, 0, 0, 0).Children(func() {
		ui.Text(c, i18n.T("查询预览（仅调试）")).FontSize(11).FontWeight(650).TextColor(pal.T.Text)
		ui.Text(c, i18n.T("返回有界图谱证据，不会注入主会话上下文。")).FontSize(10).TextColor(pal.T.TextMuted)
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.TextInput(c.Key(knowledgeFieldKey(key, "query")), &ins.Query).FontSize(12).Height(32).
				Padding(0, 8).Placeholder(i18n.T("输入关键词…")).
				Background(pal.T.Surface).Radius(8).Border(1, pal.T.Border).Grow(1)
			btn := ui.Button(c.Key(knowledgeFieldKey(key, "query-run")), i18n.T("查询"))
			btn.Label(i18n.T("预览查询知识库"))
			btn.Disabled(ins.QueryBusy)
			if btn.Clicked() && !ins.QueryBusy {
				p.RunQuery(h, id)
			}
		})
		if ins.QueryBusy {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				kit.StatusPill(c, pal, "running")
				ui.Text(c, i18n.T("正在查询活动快照…")).FontSize(10.5).TextColor(pal.T.TextMuted)
			})
		}
		if ins.QueryErr != "" {
			ui.Text(c, i18n.T(ins.QueryErr)).FontSize(10.5).TextColor(pal.T.Danger).LineHeight(1.35)
		}
		if ins.QueryResult != nil {
			p.QueryResult(c, pal, ins.QueryResult)
		}
	})
}

func (p *Panel) QueryResult(c *ui.Context, pal kit.Palette, result *GraphQuery) {
	ui.Column(c).Gap(3).Children(func() {
		summary := i18n.Tf("返回 %d 个分块 · %d 个节点", len(result.Chunks), len(result.Nodes))
		if result.Truncated {
			summary += i18n.T(" · 结果已截断")
		}
		ui.Text(c, summary).FontSize(10.5).TextColor(pal.T.TextMuted)
		if len(result.Chunks) == 0 {
			ui.Text(c, i18n.T("没有匹配的证据")).FontSize(10.5).TextColor(pal.T.TextMuted)
			return
		}
		limit := len(result.Chunks)
		if limit > 5 {
			limit = 5
		}
		for _, chunk := range result.Chunks[:limit] {
			chunk := chunk
			ui.Column(c).Gap(1).Padding(5, 7).Radius(8).Background(pal.T.Surface).Children(func() {
				path := strings.TrimSpace(chunk.RelativePath)
				if path == "" {
					path = chunk.ID
				}
				ui.Text(c, fmt.Sprintf("%s:%d-%d", path, chunk.StartLine, chunk.EndLine)).
					FontSize(10).TextColor(pal.T.TextMuted).Selectable()
				ui.Text(c, Truncate(chunk.Text, 260)).FontSize(10.5).TextColor(pal.T.Text).LineHeight(1.35)
			})
		}
	})
}

func (p *Panel) TextInput(c *ui.Context, pal kit.Palette, key, label, placeholder string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border)
		if placeholder != "" {
			input.Placeholder(placeholder)
		}
	})
}

func (p *Panel) SelectField(c *ui.Context, pal kit.Palette, key, label string, options []string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		ui.Select(c.Key(key), value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T(label))
	})
}

func knowledgeFieldKey(draftKey, field string) string {
	return "knowledge:" + draftKey + ":" + field
}

// Dialogs renders Native delete/clear confirmations above the
// settings page. Neither action deletes the source directory.
func (p *Panel) Dialogs(h Host, c *ui.Context, pal kit.Palette) {
	if !h.SettingsOpen() && h.Page() != "library" {
		return
	}
	panel := p
	if panel.PendingDeleteOpen {
		p.DeleteDialog(h, c, pal)
	}
	if panel.PendingClearOpen {
		p.ClearDialog(h, c, pal)
	}
}

func (p *Panel) DeleteDialog(h Host, c *ui.Context, pal kit.Palette) {
	panel := p
	dialog := ui.DialogBase(c, &panel.PendingDeleteOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(460).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Danger)
				ui.Text(c, i18n.T("删除知识库？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.Tf("将删除「%s」的配置、索引和扫描计划。", panel.PendingDeleteName)).
				FontSize(11.5).TextColor(pal.T.Text).LineHeight(1.4)
			if panel.PendingDeleteRoot != "" {
				ui.Text(c, i18n.T("知识源目录：")+panel.PendingDeleteRoot).FontSize(10.5).
					TextColor(pal.T.TextMuted).Selectable()
			}
			ui.Text(c, i18n.T("知识源目录及其中的文件不会被删除。")).
				FontSize(11.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("knowledge-delete-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消删除知识库"))
				if cancel.Clicked() {
					p.CancelDelete()
				}
				confirm := ui.Button(c.Key("knowledge-delete-confirm"), i18n.T("确认删除"))
				confirm.Label(i18n.T("确认删除知识库"))
				confirm.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				if confirm.Clicked() {
					p.ConfirmDelete(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CancelDelete()
	}
}

func (p *Panel) ClearDialog(h Host, c *ui.Context, pal kit.Palette) {
	panel := p
	dialog := ui.DialogBase(c, &panel.PendingClearOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(440).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Warning)
				ui.Text(c, i18n.T("清除知识库索引？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.Tf("将清除「%s」的活动索引快照。", panel.PendingClearName)).
				FontSize(11.5).TextColor(pal.T.Text).LineHeight(1.4)
			ui.Text(c, i18n.T("配置与知识源目录会保留；重新扫描前无法查询该知识库。")).
				FontSize(11.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("knowledge-clear-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消清除知识库索引"))
				if cancel.Clicked() {
					p.CancelClear()
				}
				confirm := ui.Button(c.Key("knowledge-clear-confirm"), i18n.T("确认清除"))
				confirm.Label(i18n.T("确认清除知识库索引"))
				confirm.Background(pal.WarnBg).Border(1, pal.T.Warning)
				if confirm.Clicked() {
					p.ConfirmClear(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CancelClear()
	}
}
