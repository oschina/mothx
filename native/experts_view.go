package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsExpertsPane renders the Native expert-team manager inside the
// Settings View. Every fact comes from the ACP experts projection: bundles are
// resolved and persisted by the Runtime, builtins are read-only, and the
// project layer is bound to the selected workspace.
func (a *nativeApp) settingsExpertsPane(c *ui.Context, p palette) {
	if !a.expertsCapable() {
		a.settingsCard(c, p, "主角团", "当前运行时未公开团队管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageExperts；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	e := &a.experts
	a.expertsHeader(c, p)
	a.expertsScopeCard(c, p)

	if e.Loading && !e.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取团队目录…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}
	if e.Err != "" {
		a.expertsErrorCard(c, p, e.Err)
		if !e.Loaded {
			return
		}
	}
	if e.Status != "" {
		a.expertsStatusBanner(c, p)
	}
	if e.Loading && e.Loaded {
		ui.Text(c, T("正在刷新团队目录…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	if e.Draft != nil {
		a.expertEditorCard(c, p)
	}
	a.expertsCatalogCard(c, p)
}

func (a *nativeApp) expertsHeader(c *ui.Context, p palette) {
	e := &a.experts
	busy := e.Saving || e.LoadingBundle || e.Deleting
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Icon(c, iconUsers).FontSize(18).TextColor(p.t.Accent)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, T("主角团")).FontSize(13).FontWeight(650).TextColor(p.t.Text)
			ui.Text(c, T("创建、编辑或删除全局/项目级主角团；内置团队只读。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
		})
		add := ui.PrimaryButton(c.Key("experts-add"), T("新建主角团"))
		add.Label(T("新建主角团")).Tooltip(T("在当前作用范围创建团队包"))
		add.Disabled(busy)
		if add.Clicked() && !busy {
			a.startNewExpert()
		}
	})
}

func (a *nativeApp) expertsErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("团队管理不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.expertsCapable() {
			retry := ui.Button(c.Key("experts-retry"), T("重试"))
			retry.Label(T("重试加载团队目录"))
			if retry.Clicked() {
				a.experts.Loaded = false
				a.startExpertsPanelLoad()
			}
		}
	})
}

func (a *nativeApp) expertsStatusBanner(c *ui.Context, p palette) {
	e := &a.experts
	color := p.t.TextMuted
	bg := p.thought
	if e.StatusIsError {
		color = p.t.Danger
		bg = p.errorBg
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(bg).
		Border(1, p.t.Border).Children(func() {
		if e.StatusIsError {
			ui.Icon(c, iconAlert).FontSize(13).TextColor(color)
		} else if e.Saving || e.LoadingBundle || e.Deleting {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(e.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (a *nativeApp) expertsScopeCard(c *ui.Context, p palette) {
	e := &a.experts
	a.settingsCard(c, p, "作用范围", "选择配置保存到全局还是当前工作区项目。", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
			a.expertsScopeButton(c, p, "experts-scope-global", "全局", expertScopeGlobal)
			a.expertsScopeButton(c, p, "experts-scope-project", "项目", expertScopeProject)
		})
		if e.Scope == expertScopeProject {
			cwd := e.Cwd
			if cwd == "" {
				cwd = a.expertsWorkspace()
			}
			if cwd == "" {
				ui.Text(c, T("项目范围需要已选择工作目录。")).
					FontSize(11).TextColor(p.t.Warning).LineHeight(1.4)
			} else {
				ui.Text(c, T("项目工作目录：")+cwd).FontSize(10.5).TextColor(p.t.TextMuted).Selectable()
			}
		}
	})
}

func (a *nativeApp) expertsScopeButton(c *ui.Context, p palette, key, label, scope string) {
	e := &a.experts
	b := ui.ButtonBase(c.Key(key)).Grow(1).Height(28).Radius(7).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if e.Scope == scope {
		b.Background(p.active).Border(1, p.t.Accent)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.TextMuted
		if e.Scope == scope {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(color)
	})
	b.Label(T(label)).Tooltip(T("团队作用范围：") + T(label))
	if b.Clicked() {
		a.setExpertScope(scope)
	}
}

func (a *nativeApp) expertsCatalogCard(c *ui.Context, p palette) {
	e := &a.experts
	items := expertListItems(e.View)
	desc := "当前作用范围可使用的团队，包括内置只读条目；只有当前作用范围内的非内置团队可以编辑或删除。"
	if e.Scope == expertScopeProject && e.Cwd != "" {
		desc = T("项目工作目录：") + e.Cwd + " · " + T(desc)
	}
	a.settingsCard(c, p, "团队目录", desc, func() {
		if e.View == nil {
			ui.Text(c, T("尚未载入团队目录。")).FontSize(11).TextColor(p.t.TextMuted)
			return
		}
		if len(items) == 0 {
			ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
				ui.Text(c, T("还没有团队")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
				ui.Text(c, T("点击“新建主角团”在当前作用范围创建第一个团队包。")).
					FontSize(10.5).TextColor(p.t.TextMuted)
			})
			return
		}
		ui.Column(c).Gap(6).Children(func() {
			for index := range items {
				index := index
				a.expertsCatalogRow(c, p, index, items[index])
			}
		})
	})
}

func (a *nativeApp) expertsCatalogRow(c *ui.Context, p palette, index int, item expertSummary) {
	e := &a.experts
	builtin := strings.TrimSpace(item.Source) == expertSourceBuiltin
	canEdit := expertCanEdit(item, e.Scope)
	busy := e.Saving || e.LoadingBundle || e.Deleting
	icon := iconUsers
	if builtin {
		icon = iconBook
	}
	ui.Column(c).Padding(9).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := p.t.Accent
			if builtin {
				color = p.t.TextMuted
			}
			ui.Icon(c, icon).FontSize(16).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, expertDisplayName(item)).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					badge(c, p, expertSourceLabel(item.Source))
					if builtin {
						badge(c, p, "只读")
					}
					if item.Invalid {
						badge(c, p, "无效")
					}
				})
				description := expertSummaryDescription(item)
				if description == "" {
					description = "无描述"
				}
				ui.Text(c, description).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			})
			if builtin {
				return
			}
			edit := ui.Button(c.Key(fmt.Sprintf("expert-edit:%d", index)), T("编辑"))
			edit.Label(T("编辑团队 ") + item.Name)
			edit.Tooltip(expertScopeHint(item, e.Scope))
			edit.Disabled(!canEdit || busy)
			if edit.Clicked() && canEdit && !busy {
				a.openExpertEditor(item.Name)
			}
			remove := ui.Button(c.Key(fmt.Sprintf("expert-delete:%d", index)), T("删除"))
			remove.Label(T("删除团队 ") + item.Name)
			remove.Background(p.errorBg).Border(1, p.t.Danger)
			remove.Tooltip(expertScopeHint(item, e.Scope))
			remove.Disabled(!canEdit || busy)
			if remove.Clicked() && canEdit && !busy {
				a.requestDeleteExpert(item.Name)
			}
		})
	})
}

func expertScopeHint(item expertSummary, scope string) string {
	source := strings.TrimSpace(item.Source)
	if source == expertSourceBuiltin {
		return T("内置团队只读")
	}
	if source != scope {
		return Tf("只在%s作用范围可编辑", T(expertScopeLabel(source)))
	}
	return ""
}

func (a *nativeApp) expertEditorCard(c *ui.Context, p palette) {
	e := &a.experts
	draft := e.Draft
	if draft == nil {
		return
	}
	title := strings.TrimSpace(draft.OriginalName)
	if draft.IsNew || title == "" {
		title = "新团队"
	}
	desc := "修改显示名、类型与主角源文件；保存后立即生效。"
	if draft.IsNew {
		desc = "填写名称、显示名与主角配置后保存。"
	}
	busy := e.Saving || e.LoadingBundle || e.Deleting
	a.settingsCard(c, p, title, desc, func() {
		if e.LoadingBundle {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				statusPill(c, p, "running")
				ui.Text(c, T("正在读取团队包…")).FontSize(11).TextColor(p.t.TextMuted)
			})
			return
		}
		key := "expert-draft"
		if draft.OriginalName != "" {
			key = "expert:" + draft.OriginalName
		}
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, T("团队类型")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted).Width(90)
				ui.Select(c.Key(expertFieldKey(key, "type")), &draft.Kind, expertTypes).
					FontSize(12).Height(32).Padding(0, 8).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("团队类型"))
				ui.Text(c, expertTypeLabel(draft.Kind)).FontSize(10).TextColor(p.t.TextMuted)
			})
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, T("名称")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				input := ui.TextInput(c.Key(expertFieldKey(key, "name")), &draft.Name).FontSize(12).Height(32).
					Padding(0, 8).Placeholder(T("例如 my-expert")).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("团队标识（Name）"))
				input.Disabled(!draft.IsNew)
				if !draft.IsNew {
					ui.Text(c, T("已创建的团队标识不可修改。")).
						FontSize(9.5).TextColor(p.t.TextMuted)
				}
			})
			a.expertLabeledInput(c, p, expertFieldKey(key, "display-zh"), "显示名称（中文）", "可选", &draft.DisplayZh)
			a.expertLabeledInput(c, p, expertFieldKey(key, "display-en"), "显示名称（English）", "可选", &draft.DisplayEn)
			a.expertLabeledInput(c, p, expertFieldKey(key, "agent-name"), "主 Agent 标识",
				"agent 类型必填；留空时使用 lead 成员", &draft.AgentName)
			a.expertLabeledInput(c, p, expertFieldKey(key, "category"), "分类 ID", "可选", &draft.Category)
		})
		a.expertMembersEditor(c, p, key, draft, busy)
		a.expertAgentsEditor(c, p, key, draft, busy)
		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(7).Children(func() {
			label := T("保存团队")
			hint := T("保存团队 ") + draft.Name
			if draft.IsNew {
				label = T("创建团队")
				hint = T("创建团队 ") + draft.Name
			}
			save := ui.PrimaryButton(c.Key(expertFieldKey(key, "save")), label)
			save.Label(hint).Tooltip(T("通过 mothx/manage/experts 写入"))
			save.Disabled(busy)
			if save.Clicked() && !busy {
				a.saveExpertDraft()
			}
			cancel := ui.Button(c.Key(expertFieldKey(key, "cancel")), T("取消"))
			cancel.Label(T("取消编辑团队"))
			cancel.Disabled(busy)
			if cancel.Clicked() && !busy {
				a.cancelExpertEditor()
			}
			if !draft.IsNew {
				remove := ui.Button(c.Key(expertFieldKey(key, "delete")), T("删除"))
				remove.Label(T("删除团队 ") + draft.Name)
				remove.Background(p.errorBg).Border(1, p.t.Danger)
				remove.Disabled(busy)
				if remove.Clicked() && !busy {
					a.requestDeleteExpert(draft.OriginalName)
				}
			}
		})
	})
}

func (a *nativeApp) expertLabeledInput(c *ui.Context, p palette, key, label, placeholder string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T(label))
		if placeholder != "" {
			input.Placeholder(T(placeholder))
		}
	})
}

func (a *nativeApp) expertMembersEditor(c *ui.Context, p palette, key string, draft *expertDraft, busy bool) {
	ui.Column(c).Padding(10).Radius(10).Background(p.t.Background).
		Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Text(c, T("成员")).FontSize(12).FontWeight(650).TextColor(p.t.Text)
		ui.Text(c, T("编辑团队成员、角色与显示名；team 类型会据此生成 teamInfo。")).
			FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35)
		for index := range draft.Members {
			index := index
			member := &draft.Members[index]
			ui.Column(c).Padding(9).Radius(9).Background(p.t.Surface).
				Border(1, p.t.Border).Gap(7).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, Tf("成员 %d", index+1)).FontSize(10.5).
						FontWeight(650).TextColor(p.t.TextMuted).Grow(1)
					remove := ui.Button(c.Key(fmt.Sprintf("expert-member-remove:%s:%d", key, index)), T("移除成员"))
					remove.Label(T("移除成员 ") + member.ID)
					remove.Disabled(busy)
					if remove.Clicked() && !busy {
						a.removeExpertMember(index)
					}
				})
				ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
					ui.Column(c).Gap(3).Grow(1).Children(func() {
						ui.Text(c, T("成员 ID")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
						ui.TextInput(c.Key(fmt.Sprintf("expert-member-id:%s:%d", key, index)), &member.ID).
							FontSize(12).Height(32).Padding(0, 8).
							Background(p.t.Background).Radius(8).Border(1, p.t.Border).Disabled(true)
					})
					ui.Column(c).Gap(3).Width(130).Children(func() {
						ui.Text(c, T("角色")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
						ui.Select(c.Key(fmt.Sprintf("expert-member-role:%s:%d", key, index)), &member.Role, expertRoles).
							FontSize(12).Height(32).Padding(0, 8).
							Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("成员角色"))
					})
				})
				a.expertLabeledInput(c, p, fmt.Sprintf("expert-member-name-zh:%s:%d", key, index), "成员名（中文）", "", &member.NameZh)
				a.expertLabeledInput(c, p, fmt.Sprintf("expert-member-name-en:%s:%d", key, index), "成员名（English）", "", &member.NameEn)
			})
		}
		ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, T("成员 ID")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				ui.TextInput(c.Key(expertFieldKey(key, "new-member")), &draft.NewMemberID).
					FontSize(12).Height(32).Padding(0, 8).Placeholder(T("例如 reviewer")).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border)
			})
			add := ui.Button(c.Key(expertFieldKey(key, "add-member")), T("添加成员"))
			add.Label(T("添加成员到团队"))
			add.Disabled(busy)
			if add.Clicked() && !busy {
				a.addExpertMember()
			}
		})
	})
}

func (a *nativeApp) expertAgentsEditor(c *ui.Context, p palette, key string, draft *expertDraft, busy bool) {
	ui.Column(c).Padding(10).Radius(10).Background(p.t.Background).
		Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Text(c, T("Agent 源文件")).FontSize(12).FontWeight(650).TextColor(p.t.Text)
		ui.Text(c, T("每个 Agent 对应 agents/<id>.md 完整源文件（含 frontmatter）；保存时由 Runtime 校验。")).
			FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35)
		for index := range draft.Agents {
			index := index
			agent := &draft.Agents[index]
			ui.Column(c).Padding(9).Radius(9).Background(p.t.Surface).
				Border(1, p.t.Border).Gap(7).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, "Agent "+agent.ID).FontSize(10.5).
						FontWeight(650).TextColor(p.t.TextMuted).Grow(1)
					remove := ui.Button(c.Key(fmt.Sprintf("expert-agent-remove:%s:%d", key, index)), T("移除 Agent"))
					remove.Label(T("移除 Agent ") + agent.ID)
					remove.Disabled(busy)
					if remove.Clicked() && !busy {
						a.removeExpertAgent(index)
					}
				})
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, T("Agent 源文件")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
					ui.TextAreaBase(c.Key(fmt.Sprintf("expert-agent-source:%s:%d", key, index)), &agent.Source).
						FontSize(11.5).Lines(6, 18).Padding(6, 8).
						Background(p.t.Background).Radius(8).Border(1, p.t.Border)
				})
			})
		}
		ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, "Agent ID").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				ui.TextInput(c.Key(expertFieldKey(key, "new-agent")), &draft.NewAgentID).
					FontSize(12).Height(32).Padding(0, 8).Placeholder(T("例如 reviewer")).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border)
			})
			add := ui.Button(c.Key(expertFieldKey(key, "add-agent")), T("添加 Agent"))
			add.Label(T("添加 Agent 源文件"))
			add.Disabled(busy)
			if add.Clicked() && !busy {
				a.addExpertAgent()
			}
		})
	})
}

func expertFieldKey(draftKey, field string) string {
	return "expert:" + draftKey + ":" + field
}

// settingsExpertsDialogs renders the Native delete confirmation above the
// settings page.
func (a *nativeApp) settingsExpertsDialogs(c *ui.Context, p palette) {
	if !a.settingsOpen {
		return
	}
	if a.experts.PendingDeleteOpen {
		a.expertDeleteDialog(c, p)
	}
}

func (a *nativeApp) expertDeleteDialog(c *ui.Context, p palette) {
	e := &a.experts
	dialog := ui.DialogBase(c, &e.PendingDeleteOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(440).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, T("删除团队？")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, Tf("删除团队「%s」？此操作不可恢复。", e.PendingDeleteName)).
				FontSize(11.5).TextColor(p.t.Text).LineHeight(1.4)
			ui.Text(c, T("内置团队只读；此操作不会删除其它作用范围中的同名团队。")).
				FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("experts-delete-cancel"), T("取消"))
				cancel.Label(T("取消删除团队"))
				if cancel.Clicked() {
					a.cancelDeleteExpert()
				}
				confirm := ui.Button(c.Key("experts-delete-confirm"), T("确认删除"))
				confirm.Label(T("确认删除团队"))
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(e.Deleting)
				if confirm.Clicked() && !e.Deleting {
					a.confirmDeleteExpert()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelDeleteExpert()
	}
}
