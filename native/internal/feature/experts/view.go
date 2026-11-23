package experts

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// settingsExpertsPane renders the Native expert-team manager inside the
// Settings View. Every fact comes from the ACP experts projection: bundles are
// resolved and persisted by the Runtime, builtins are read-only, and the
// project layer is bound to the selected workspace.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "主角团", "当前运行时未公开团队管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageExperts；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	p.header(h, c, pal)
	p.scopeCard(h, c, pal)

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取团队目录…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}
	if p.Err != "" {
		p.errCard(h, c, pal, p.Err)
		if !p.Loaded {
			return
		}
	}
	if p.Status != "" {
		p.statusBanner(c, pal)
	}
	if p.Loading && p.Loaded {
		ui.Text(c, i18n.T("正在刷新团队目录…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	if p.Draft != nil {
		p.editorCard(h, c, pal)
	}
	p.catalogCard(h, c, pal)
}

func (p *Panel) header(h Host, c *ui.Context, pal kit.Palette) {
	busy := p.Saving || p.LoadingBundle || p.Deleting
	ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Icon(c, icons.IconUsers).FontSize(18).TextColor(pal.T.Accent)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, i18n.T("主角团")).FontSize(13).FontWeight(650).TextColor(pal.T.Text)
			ui.Text(c, i18n.T("创建、编辑或删除全局/项目级主角团；内置团队只读。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
		})
		add := ui.PrimaryButton(c.Key("experts-add"), i18n.T("新建主角团"))
		add.Label(i18n.T("新建主角团")).Tooltip(i18n.T("在当前作用范围创建团队包"))
		add.Disabled(busy)
		if add.Clicked() && !busy {
			p.StartNew(h)
		}
	})
}

func (p *Panel) errCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("团队管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			retry := ui.Button(c.Key("experts-retry"), i18n.T("重试"))
			retry.Label(i18n.T("重试加载团队目录"))
			if retry.Clicked() {
				p.Loaded = false
				p.StartLoad(h)
			}
		}
	})
}

func (p *Panel) statusBanner(c *ui.Context, pal kit.Palette) {
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
		} else if p.Saving || p.LoadingBundle || p.Deleting {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) scopeCard(h Host, c *ui.Context, pal kit.Palette) {
	kit.SettingsCard(c, pal, "作用范围", "选择配置保存到全局还是当前工作区项目。", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
			p.scopeButton(h, c, pal, "experts-scope-global", "全局", ScopeGlobal)
			p.scopeButton(h, c, pal, "experts-scope-project", "项目", ScopeProject)
		})
		if p.Scope == ScopeProject {
			cwd := p.Cwd
			if cwd == "" {
				cwd = h.ExpertsWorkspace()
			}
			if cwd == "" {
				ui.Text(c, i18n.T("项目范围需要已选择工作目录。")).
					FontSize(11).TextColor(pal.T.Warning).LineHeight(1.4)
			} else {
				ui.Text(c, i18n.T("项目工作目录：")+cwd).FontSize(10.5).TextColor(pal.T.TextMuted).Selectable()
			}
		}
	})
}

func (p *Panel) scopeButton(h Host, c *ui.Context, pal kit.Palette, key, label, scope string) {
	b := ui.ButtonBase(c.Key(key)).Grow(1).Height(28).Radius(7).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, pal.T.Border)
	if p.Scope == scope {
		b.Background(pal.Active).Border(1, pal.T.Accent)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() {
		color := pal.T.TextMuted
		if p.Scope == scope {
			color = pal.T.Accent
		}
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(color)
	})
	b.Label(i18n.T(label)).Tooltip(i18n.T("团队作用范围：") + i18n.T(label))
	if b.Clicked() {
		p.SetScope(h, scope)
	}
}

func (p *Panel) catalogCard(h Host, c *ui.Context, pal kit.Palette) {
	items := ListItems(p.List)
	desc := "当前作用范围可使用的团队，包括内置只读条目；只有当前作用范围内的非内置团队可以编辑或删除。"
	if p.Scope == ScopeProject && p.Cwd != "" {
		desc = i18n.T("项目工作目录：") + p.Cwd + " · " + i18n.T(desc)
	}
	kit.SettingsCard(c, pal, "团队目录", desc, func() {
		if p.List == nil {
			ui.Text(c, i18n.T("尚未载入团队目录。")).FontSize(11).TextColor(pal.T.TextMuted)
			return
		}
		if len(items) == 0 {
			ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
				ui.Text(c, i18n.T("还没有团队")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
				ui.Text(c, i18n.T("点击“新建主角团”在当前作用范围创建第一个团队包。")).
					FontSize(10.5).TextColor(pal.T.TextMuted)
			})
			return
		}
		ui.Column(c).Gap(6).Children(func() {
			for index := range items {
				index := index
				p.catalogRow(h, c, pal, index, items[index])
			}
		})
	})
}

func (p *Panel) catalogRow(h Host, c *ui.Context, pal kit.Palette, index int, item Summary) {
	builtin := strings.TrimSpace(item.Source) == SourceBuiltin
	canEdit := CanEdit(item, p.Scope)
	busy := p.Saving || p.LoadingBundle || p.Deleting
	icon := icons.IconUsers
	if builtin {
		icon = icons.IconBook
	}
	ui.Column(c).Padding(9).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := pal.T.Accent
			if builtin {
				color = pal.T.TextMuted
			}
			ui.Icon(c, icon).FontSize(16).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, DisplayName(item)).FontSize(12).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					kit.Badge(c, pal, SourceLabel(item.Source))
					if builtin {
						kit.Badge(c, pal, "只读")
					}
					if item.Invalid {
						kit.Badge(c, pal, "无效")
					}
				})
				description := SummaryDescription(item)
				if description == "" {
					description = "无描述"
				}
				ui.Text(c, description).FontSize(9.5).TextColor(pal.T.TextMuted).SingleLine()
			})
			if builtin {
				return
			}
			edit := ui.Button(c.Key(fmt.Sprintf("expert-edit:%d", index)), i18n.T("编辑"))
			edit.Label(i18n.T("编辑团队 ") + item.Name)
			edit.Tooltip(ScopeHint(item, p.Scope))
			edit.Disabled(!canEdit || busy)
			if edit.Clicked() && canEdit && !busy {
				p.OpenEditor(h, item.Name)
			}
			remove := ui.Button(c.Key(fmt.Sprintf("expert-delete:%d", index)), i18n.T("删除"))
			remove.Label(i18n.T("删除团队 ") + item.Name)
			remove.Background(pal.ErrorBg).Border(1, pal.T.Danger)
			remove.Tooltip(ScopeHint(item, p.Scope))
			remove.Disabled(!canEdit || busy)
			if remove.Clicked() && canEdit && !busy {
				p.RequestDelete(item.Name)
			}
		})
	})
}

func ScopeHint(item Summary, scope string) string {
	source := strings.TrimSpace(item.Source)
	if source == SourceBuiltin {
		return i18n.T("内置团队只读")
	}
	if source != scope {
		return i18n.Tf("只在%s作用范围可编辑", i18n.T(ScopeLabel(source)))
	}
	return ""
}

func (p *Panel) editorCard(h Host, c *ui.Context, pal kit.Palette) {
	draft := p.Draft
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
	busy := p.Saving || p.LoadingBundle || p.Deleting
	kit.SettingsCard(c, pal, title, desc, func() {
		if p.LoadingBundle {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				kit.StatusPill(c, pal, "running")
				ui.Text(c, i18n.T("正在读取团队包…")).FontSize(11).TextColor(pal.T.TextMuted)
			})
			return
		}
		key := "expert-draft"
		if draft.OriginalName != "" {
			key = "expert:" + draft.OriginalName
		}
		ui.Column(c).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, i18n.T("团队类型")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted).Width(90)
				ui.Select(c.Key(FieldKey(key, "type")), &draft.Kind, Types).
					FontSize(12).Height(32).Padding(0, 8).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("团队类型"))
				ui.Text(c, TypeLabel(draft.Kind)).FontSize(10).TextColor(pal.T.TextMuted)
			})
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, i18n.T("名称")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				input := ui.TextInput(c.Key(FieldKey(key, "name")), &draft.Name).FontSize(12).Height(32).
					Padding(0, 8).Placeholder(i18n.T("例如 my-expert")).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("团队标识（Name）"))
				input.Disabled(!draft.IsNew)
				if !draft.IsNew {
					ui.Text(c, i18n.T("已创建的团队标识不可修改。")).
						FontSize(9.5).TextColor(pal.T.TextMuted)
				}
			})
			p.labeledInput(c, pal, FieldKey(key, "display-zh"), "显示名称（中文）", "可选", &draft.DisplayZh)
			p.labeledInput(c, pal, FieldKey(key, "display-en"), "显示名称（English）", "可选", &draft.DisplayEn)
			p.labeledInput(c, pal, FieldKey(key, "agent-name"), "主 Agent 标识",
				"agent 类型必填；留空时使用 lead 成员", &draft.AgentName)
			p.labeledInput(c, pal, FieldKey(key, "category"), "分类 ID", "可选", &draft.Category)
		})
		p.membersEditor(c, pal, key, draft, busy)
		p.agentsEditor(c, pal, key, draft, busy)
		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(7).Children(func() {
			label := i18n.T("保存团队")
			hint := i18n.T("保存团队 ") + draft.Name
			if draft.IsNew {
				label = i18n.T("创建团队")
				hint = i18n.T("创建团队 ") + draft.Name
			}
			save := ui.PrimaryButton(c.Key(FieldKey(key, "save")), label)
			save.Label(hint).Tooltip(i18n.T("通过 mothx/manage/experts 写入"))
			save.Disabled(busy)
			if save.Clicked() && !busy {
				p.Save(h)
			}
			cancel := ui.Button(c.Key(FieldKey(key, "cancel")), i18n.T("取消"))
			cancel.Label(i18n.T("取消编辑团队"))
			cancel.Disabled(busy)
			if cancel.Clicked() && !busy {
				p.CancelEditor()
			}
			if !draft.IsNew {
				remove := ui.Button(c.Key(FieldKey(key, "delete")), i18n.T("删除"))
				remove.Label(i18n.T("删除团队 ") + draft.Name)
				remove.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				remove.Disabled(busy)
				if remove.Clicked() && !busy {
					p.RequestDelete(draft.OriginalName)
				}
			}
		})
	})
}

func (p *Panel) labeledInput(c *ui.Context, pal kit.Palette, key, label, placeholder string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T(label))
		if placeholder != "" {
			input.Placeholder(i18n.T(placeholder))
		}
	})
}

func (p *Panel) membersEditor(c *ui.Context, pal kit.Palette, key string, draft *Draft, busy bool) {
	ui.Column(c).Padding(10).Radius(10).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(8).Children(func() {
		ui.Text(c, i18n.T("成员")).FontSize(12).FontWeight(650).TextColor(pal.T.Text)
		ui.Text(c, i18n.T("编辑团队成员、角色与显示名；team 类型会据此生成 teamInfo。")).
			FontSize(10).TextColor(pal.T.TextMuted).LineHeight(1.35)
		for index := range draft.Members {
			index := index
			member := &draft.Members[index]
			ui.Column(c).Padding(9).Radius(9).Background(pal.T.Surface).
				Border(1, pal.T.Border).Gap(7).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, i18n.Tf("成员 %d", index+1)).FontSize(10.5).
						FontWeight(650).TextColor(pal.T.TextMuted).Grow(1)
					remove := ui.Button(c.Key(fmt.Sprintf("expert-member-remove:%s:%d", key, index)), i18n.T("移除成员"))
					remove.Label(i18n.T("移除成员 ") + member.ID)
					remove.Disabled(busy)
					if remove.Clicked() && !busy {
						p.RemoveMember(index)
					}
				})
				ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
					ui.Column(c).Gap(3).Grow(1).Children(func() {
						ui.Text(c, i18n.T("成员 ID")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
						ui.TextInput(c.Key(fmt.Sprintf("expert-member-id:%s:%d", key, index)), &member.ID).
							FontSize(12).Height(32).Padding(0, 8).
							Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Disabled(true)
					})
					ui.Column(c).Gap(3).Width(130).Children(func() {
						ui.Text(c, i18n.T("角色")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
						ui.Select(c.Key(fmt.Sprintf("expert-member-role:%s:%d", key, index)), &member.Role, Roles).
							FontSize(12).Height(32).Padding(0, 8).
							Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("成员角色"))
					})
				})
				p.labeledInput(c, pal, fmt.Sprintf("expert-member-name-zh:%s:%d", key, index), "成员名（中文）", "", &member.NameZh)
				p.labeledInput(c, pal, fmt.Sprintf("expert-member-name-en:%s:%d", key, index), "成员名（English）", "", &member.NameEn)
			})
		}
		ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, i18n.T("成员 ID")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				ui.TextInput(c.Key(FieldKey(key, "new-member")), &draft.NewMemberID).
					FontSize(12).Height(32).Padding(0, 8).Placeholder(i18n.T("例如 reviewer")).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border)
			})
			add := ui.Button(c.Key(FieldKey(key, "add-member")), i18n.T("添加成员"))
			add.Label(i18n.T("添加成员到团队"))
			add.Disabled(busy)
			if add.Clicked() && !busy {
				p.AddMember()
			}
		})
	})
}

func (p *Panel) agentsEditor(c *ui.Context, pal kit.Palette, key string, draft *Draft, busy bool) {
	ui.Column(c).Padding(10).Radius(10).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(8).Children(func() {
		ui.Text(c, i18n.T("Agent 源文件")).FontSize(12).FontWeight(650).TextColor(pal.T.Text)
		ui.Text(c, i18n.T("每个 Agent 对应 agents/<id>.md 完整源文件（含 frontmatter）；保存时由 Runtime 校验。")).
			FontSize(10).TextColor(pal.T.TextMuted).LineHeight(1.35)
		for index := range draft.Agents {
			index := index
			agent := &draft.Agents[index]
			ui.Column(c).Padding(9).Radius(9).Background(pal.T.Surface).
				Border(1, pal.T.Border).Gap(7).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, "Agent "+agent.ID).FontSize(10.5).
						FontWeight(650).TextColor(pal.T.TextMuted).Grow(1)
					remove := ui.Button(c.Key(fmt.Sprintf("expert-agent-remove:%s:%d", key, index)), i18n.T("移除 Agent"))
					remove.Label(i18n.T("移除 Agent ") + agent.ID)
					remove.Disabled(busy)
					if remove.Clicked() && !busy {
						p.RemoveAgent(index)
					}
				})
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, i18n.T("Agent 源文件")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
					ui.TextAreaBase(c.Key(fmt.Sprintf("expert-agent-source:%s:%d", key, index)), &agent.Source).
						FontSize(11.5).Lines(6, 18).Padding(6, 8).
						Background(pal.T.Background).Radius(8).Border(1, pal.T.Border)
				})
			})
		}
		ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, "Agent ID").FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				ui.TextInput(c.Key(FieldKey(key, "new-agent")), &draft.NewAgentID).
					FontSize(12).Height(32).Padding(0, 8).Placeholder(i18n.T("例如 reviewer")).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border)
			})
			add := ui.Button(c.Key(FieldKey(key, "add-agent")), i18n.T("添加 Agent"))
			add.Label(i18n.T("添加 Agent 源文件"))
			add.Disabled(busy)
			if add.Clicked() && !busy {
				p.AddAgent()
			}
		})
	})
}

func FieldKey(draftKey, field string) string {
	return "expert:" + draftKey + ":" + field
}

// dialogs renders the Native delete confirmation above the
// settings page.
func (p *Panel) Dialogs(h Host, c *ui.Context, pal kit.Palette) {
	if !h.SettingsOpen() {
		return
	}
	if p.PendingDeleteOpen {
		p.deleteDialog(h, c, pal)
	}
}

func (p *Panel) deleteDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.PendingDeleteOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(440).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Danger)
				ui.Text(c, i18n.T("删除团队？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.Tf("删除团队「%s」？此操作不可恢复。", p.PendingDeleteName)).
				FontSize(11.5).TextColor(pal.T.Text).LineHeight(1.4)
			ui.Text(c, i18n.T("内置团队只读；此操作不会删除其它作用范围中的同名团队。")).
				FontSize(11.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("experts-delete-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消删除团队"))
				if cancel.Clicked() {
					p.CancelDelete()
				}
				confirm := ui.Button(c.Key("experts-delete-confirm"), i18n.T("确认删除"))
				confirm.Label(i18n.T("确认删除团队"))
				confirm.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				confirm.Disabled(p.Deleting)
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
