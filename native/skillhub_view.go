package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsSkillHubPane renders the Native SkillHub manager. Configuration and
// catalogue capabilities are gated independently: manageSkillHub owns the
// global token-free market configuration, while manageSkillHubCatalog owns the
// session-bound marketplace browser. No panel reads settings.json or a skill
// directory directly.
func (a *nativeApp) settingsSkillHubPane(c *ui.Context, p palette) {
	if !a.skillHubConfigCapable() && !a.skillHubCatalogCapable() {
		a.settingsCard(c, p, "SkillHub", "当前运行时不支持 SkillHub 配置或市场目录。", func() {
			ui.Text(c, T("需要 ACP 能力 manageSkillHub 与 manageSkillHubCatalog；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.skillhub
	tab := panel.Tab
	ui.Tabs(c, &tab, T("浏览市场"), T("市场配置"))
	panel.Tab = tab
	if tab == 1 {
		a.skillHubConfigPane(c, p)
		return
	}
	a.skillHubBrowsePane(c, p)
}

func (a *nativeApp) skillHubConfigPane(c *ui.Context, p palette) {
	if !a.skillHubConfigCapable() {
		a.settingsCard(c, p, "SkillHub 配置", "当前运行时未公开 SkillHub 配置能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageSkillHub；市场浏览仍可通过另一项能力独立使用。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}
	panel := &a.skillhub
	if panel.ConfigLoading && !panel.ConfigLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取 SkillHub 配置…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}
	if panel.ConfigErr != "" {
		a.skillHubConfigErrorCard(c, p, panel.ConfigErr)
		if !panel.ConfigLoaded {
			return
		}
	}
	if panel.ConfigStatus != "" {
		a.skillHubStatusBanner(c, p, panel.ConfigStatus, panel.ConfigStatusIsError, panel.ConfigSaving)
	}
	if panel.ConfigLoading && panel.ConfigLoaded {
		ui.Text(c, T("正在刷新 SkillHub 配置…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	a.settingsCard(c, p, "默认行为", "默认市场与安装范围决定市场浏览和新安装的初始选择。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ids := skillHubMarketIDs(panel.Markets)
			if panel.DefaultMarket != "" && !skillHubContains(ids, panel.DefaultMarket) {
				ids = append([]string{panel.DefaultMarket}, ids...)
			}
			if len(ids) > 0 {
				a.settingsSelect(c, p, "skillhub-default-market", "默认市场", ids, &panel.DefaultMarket)
			} else {
				a.skillHubLabeledInput(c, p, "skillhub-default-market-text", "默认市场", &panel.DefaultMarket, panel.ConfigSaving)
			}
			a.settingsSelect(c, p, "skillhub-default-scope", "默认安装范围", []string{"project", "global"}, &panel.DefaultInstallScope)
			a.skillHubLabeledTextArea(c, p, "skillhub-official-handles", "官方推荐 Handles（每行一个）", &panel.OfficialHandles, 3, panel.ConfigSaving)
			ui.Text(c, T("Token 只写入服务端；已配置状态只会显示为布尔标记，从不回显。")).
				FontSize(9.5).TextColor(p.t.TextMuted)
		})
	})

	a.settingsCard(c, p, "市场源", "编辑市场端点与 Token；清空 Token 与提交新 Token 互斥。", func() {
		ui.Column(c).Gap(8).Children(func() {
			if len(panel.Markets) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
					ui.Text(c, T("尚未配置市场源")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
					ui.Text(c, T("添加市场并保存后，市场浏览会按默认市场选择。")).FontSize(10.5).TextColor(p.t.TextMuted)
				})
			} else {
				for index := range panel.Markets {
					index := index
					a.skillHubMarketEditor(c, p, index)
				}
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
				add := ui.Button(c.Key("skillhub-add-market"), T("添加市场"))
				add.Label(T("添加 SkillHub 市场"))
				add.Disabled(panel.ConfigSaving || panel.ConfigLoading)
				if add.Clicked() && !panel.ConfigSaving && !panel.ConfigLoading {
					a.openSkillHubAddMarket()
				}
				save := ui.PrimaryButton(c.Key("skillhub-save-config"), T("保存配置"))
				save.Label(T("保存 SkillHub 配置"))
				save.Disabled(panel.ConfigSaving || panel.ConfigLoading)
				if save.Clicked() && !panel.ConfigSaving && !panel.ConfigLoading {
					a.saveSkillHubConfig()
				}
			})
		})
	})
}

func skillHubContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (a *nativeApp) skillHubMarketEditor(c *ui.Context, p palette, index int) {
	panel := &a.skillhub
	if index < 0 || index >= len(panel.Markets) {
		return
	}
	market := &panel.Markets[index]
	busy := panel.ConfigSaving || panel.ConfigLoading
	ui.Column(c).Padding(10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconShop).FontSize(15).TextColor(p.t.Accent)
			ui.Text(c, market.ID).FontSize(12).FontWeight(650).TextColor(p.t.Text).Grow(1).SingleLine()
			if market.Configured {
				badge(c, p, "Token 已配置")
			} else {
				badge(c, p, "Token 未配置")
			}
			remove := ui.Button(c.Key(fmt.Sprintf("skillhub-remove-market:%d", index)), T("移除"))
			remove.Label(T("移除市场 ") + market.ID)
			remove.Disabled(busy)
			if remove.Clicked() && !busy {
				a.removeSkillHubMarket(index)
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				a.skillHubLabeledInput(c, p, fmt.Sprintf("skillhub-market-name:%d", index), "名称", &market.Name, busy)
			})
			ui.Column(c).Grow(1).Children(func() {
				a.skillHubLabeledInput(c, p, fmt.Sprintf("skillhub-market-site:%d", index), "站点 URL", &market.SiteURL, busy)
			})
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				a.skillHubLabeledInput(c, p, fmt.Sprintf("skillhub-market-api:%d", index), "API URL", &market.APIURL, busy)
			})
			ui.Column(c).Grow(1).Children(func() {
				status := "尚未配置 Token"
				if market.Configured {
					status = "已配置 Token；留空保持不变"
				}
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, "API Token").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
					token := ui.TextInput(c.Key(fmt.Sprintf("skillhub-market-token:%d", index)), &market.Token).
						Password().FontSize(12).Height(32).Padding(0, 8).
						Placeholder(status).Background(p.t.Background).Radius(8).Border(1, p.t.Border).
						Label("API Token（只写）")
					token.Disabled(busy || market.ClearToken)
					token.OnChange(func() {
						if strings.TrimSpace(market.Token) != "" {
							market.ClearToken = false
						}
					})
				})
			})
		})
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			enabled := ui.Checkbox(c, &market.Enabled, T("启用该市场")).FontSize(11.5)
			enabled.Disabled(busy)
			wasClearing := market.ClearToken
			clear := ui.Checkbox(c, &market.ClearToken, T("清除已保存的 Token")).FontSize(11.5)
			clear.Disabled(busy || !market.Configured)
			if market.ClearToken && !wasClearing {
				market.Token = ""
			}
		})
	})
}

func (a *nativeApp) skillHubBrowsePane(c *ui.Context, p palette) {
	if !a.skillHubCatalogCapable() {
		a.settingsCard(c, p, "SkillHub 市场", "当前运行时未公开 SkillHub 市场目录能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageSkillHubCatalog；市场目录请求必须绑定活动会话。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}
	panel := &a.skillhub
	if strings.TrimSpace(a.currentSession) == "" {
		a.settingsCard(c, p, "技能市场需要活动会话", "先在对话中打开或新建会话，再回来浏览与安装在线技能。", func() {
			if ui.PrimaryButton(c.Key("skillhub-open-session"), T("新建会话")).Clicked() {
				a.newChat()
			}
		})
		return
	}
	if panel.CatalogLoading && !panel.CatalogLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取 SkillHub 市场目录…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}
	if panel.CatalogErr != "" {
		a.skillHubCatalogErrorCard(c, p, panel.CatalogErr)
		if !panel.CatalogLoaded || panel.SessionID == "" {
			return
		}
	}
	if panel.CatalogStatus != "" {
		a.skillHubStatusBanner(c, p, panel.CatalogStatus, panel.CatalogStatusIsError, panel.ActionBusy != "" || panel.SearchBusy)
	}
	if panel.CatalogLoading && panel.CatalogLoaded {
		ui.Text(c, T("正在刷新市场目录…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	busy := a.skillHubBrowseBusy()
	a.settingsCard(c, p, "市场目录", "在线目录、详情与安装都通过 ACP 投影；安装目录由 Runtime 工作区决定。", func() {
		ui.Column(c).Gap(8).Children(func() {
			if panel.WorkDir != "" {
				ui.Text(c, T("工作区：")+panel.WorkDir).FontSize(10).TextColor(p.t.TextMuted).Selectable()
			}
			ui.Row(c).Gap(8).Children(func() {
				ids := make([]string, 0, len(panel.MarketInfos))
				for _, market := range panel.MarketInfos {
					if id := strings.TrimSpace(market.ID); id != "" {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					ui.Text(c, T("当前运行时没有可用市场。")).FontSize(10.5).TextColor(p.t.TextMuted).Grow(1)
				} else {
					old := panel.SelectedMarket
					ui.Column(c).Grow(1).Children(func() {
						a.settingsSelect(c, p, "skillhub-browse-market", "市场", ids, &panel.SelectedMarket)
					})
					if old != panel.SelectedMarket {
						skillHubOnMarketChanged(panel)
						a.loadSkillHubCategories()
					}
				}
				targetLabels := skillHubTargetLabels(panel.Targets)
				if len(targetLabels) == 0 {
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, T("安装目录不可用")).FontSize(10.5).TextColor(p.t.Danger)
					})
				} else {
					oldTarget := panel.TargetLabel
					ui.Column(c).Grow(1).Children(func() {
						a.settingsSelect(c, p, "skillhub-browse-target", "安装目录", targetLabels, &panel.TargetLabel)
					})
					if oldTarget != panel.TargetLabel {
						skillHubOnTargetChanged(panel)
					}
				}
			})
			a.skillHubCategoryRow(c, p)
			ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Text(c, T("搜索在线技能")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
					search := ui.TextInput(c.Key("skillhub-query"), &panel.Query).
						Placeholder(T("输入关键词；留空浏览热门技能")).FontSize(12).Height(32).Padding(0, 8).
						Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("搜索在线技能"))
					search.Disabled(busy)
				})
				btn := ui.PrimaryButton(c.Key("skillhub-search"), T("搜索"))
				btn.Label(T("搜索 SkillHub 市场"))
				btn.Disabled(busy)
				if btn.Clicked() && !busy {
					a.searchSkillHub()
				}
				refresh := ui.Button(c.Key("skillhub-refresh"), T("刷新目录"))
				refresh.Label(T("刷新 SkillHub 市场目录"))
				refresh.Disabled(busy)
				if refresh.Clicked() && !busy {
					a.refreshSkillHubCatalog()
				}
			})
			if panel.TargetPath != "" {
				ui.Text(c, T("安装目标：")+panel.TargetScope+" · "+panel.TargetPath).
					FontSize(9.5).TextColor(p.t.TextMuted).Selectable()
			}
		})
	})

	a.skillHubResultsCard(c, p)
	a.skillHubInstalledCard(c, p)
}

func (a *nativeApp) skillHubResultsCard(c *ui.Context, p palette) {
	panel := &a.skillhub
	desc := "搜索或浏览当前市场。安装前会使用上方选定的目录与范围。"
	if panel.ResultsTotal > 0 {
		desc = Tf("共 %d 个结果；%s", panel.ResultsTotal, T(desc))
	}
	a.settingsCard(c, p, "在线技能", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			if panel.SearchBusy {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
					ui.Text(c, T("正在搜索技能…")).FontSize(11).TextColor(p.t.TextMuted)
				})
				return
			}
			if panel.SearchErr != "" {
				ui.Text(c, T(panel.SearchErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			if !panel.ResultsLoaded {
				ui.Text(c, T("输入关键词搜索，或留空后点击“搜索”浏览当前市场热门技能。")).
					FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
				return
			}
			if len(panel.Results) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
					ui.Text(c, T("没有找到匹配技能")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
					ui.Text(c, T("尝试更换市场、缩短关键词或清空关键词浏览热门技能。")).
						FontSize(10.5).TextColor(p.t.TextMuted)
				})
				return
			}
			for index := range panel.Results {
				index := index
				a.skillHubResultRow(c, p, index)
			}
			if panel.ResultsQuery != "" {
				ui.Text(c, T("结果关键词：")+panel.ResultsQuery).FontSize(9.5).TextColor(p.t.TextMuted)
			}
		})
	})
}

func (a *nativeApp) skillHubResultRow(c *ui.Context, p palette, index int) {
	panel := &a.skillhub
	if index < 0 || index >= len(panel.Results) {
		return
	}
	skill := panel.Results[index]
	state := skill.Installed
	active := state != nil && state.Active
	if !active {
		active = skillHubActive(panel.ActiveSkills, skillHubInstalledName(skill))
	}
	busy := a.skillHubBrowseBusy()
	ui.Column(c).Padding(9).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := p.t.TextMuted
			if state != nil {
				color = p.t.Accent
			}
			ui.Icon(c, iconSpark).FontSize(15).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, skillHubTitle(skill)).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					if market := strings.TrimSpace(skill.Market); market != "" {
						badge(c, p, market)
					}
					if version := strings.TrimSpace(skill.Version); version != "" {
						badge(c, p, "v"+version)
					}
					if state != nil {
						if state.UpdateAvailable {
							badge(c, p, "可更新")
						} else if active {
							badge(c, p, "已激活")
						} else {
							badge(c, p, "已安装")
						}
					}
					if skill.Suspicious {
						badge(c, p, "可疑")
					}
				})
				description := strings.TrimSpace(skill.Description)
				if description == "" {
					description = "无描述"
				}
				ui.Text(c, description).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
				meta := skillHubSkillMeta(skill)
				if meta != "" {
					ui.Text(c, meta).FontSize(9).TextColor(p.t.TextMuted).SingleLine()
				}
			})
			detail := ui.Button(c.Key(fmt.Sprintf("skillhub-detail:%s:%s", skill.Market, skill.ID)), T("详情"))
			detail.Label(T("查看技能详情 ") + skillHubTitle(skill))
			detail.Disabled(busy)
			if detail.Clicked() && !busy {
				a.openSkillHubDetail(skill.Market, skill.ID)
			}
			if state == nil {
				install := ui.PrimaryButton(c.Key(fmt.Sprintf("skillhub-install:%s:%s", skill.Market, skill.ID)), T("安装"))
				install.Label(T("安装技能 ") + skillHubTitle(skill))
				install.Disabled(busy || panel.TargetPath == "")
				if install.Clicked() && !busy && panel.TargetPath != "" {
					a.installSkillHub(skill.Market, skill.ID, skill.Version)
				}
			} else if state.UpdateAvailable {
				update := ui.PrimaryButton(c.Key(fmt.Sprintf("skillhub-update:%s:%s", skill.Market, skill.ID)), T("更新"))
				update.Label(T("更新技能 ") + skillHubTitle(skill))
				update.Disabled(busy || panel.TargetPath == "")
				if update.Clicked() && !busy && panel.TargetPath != "" {
					a.installSkillHubWithOverwrite(skill.Market, skill.ID, skill.Version, true)
				}
			}
			if state != nil {
				activate := ui.Button(c.Key(fmt.Sprintf("skillhub-activate:%s:%s", skill.Market, skill.ID)), T("激活"))
				activate.Label(T("在当前会话激活技能 ") + skillHubTitle(skill))
				activate.Disabled(busy || active)
				if activate.Clicked() && !busy && !active {
					a.activateSkillHub(skill.Market, skill.ID, skillHubInstalledName(skill))
				}
				remove := ui.Button(c.Key(fmt.Sprintf("skillhub-uninstall:%s:%s", skill.Market, skill.ID)), T("卸载"))
				remove.Label(T("卸载技能 ") + skillHubTitle(skill))
				remove.Disabled(busy)
				if remove.Clicked() && !busy {
					scope := state.Scope
					if scope == "" {
						scope = panel.TargetScope
					}
					a.requestUninstallSkillHub(skill.Market, skill.ID, scope, skillHubInstalledName(skill))
				}
			}
		})
	})
}

func skillHubSkillMeta(skill skillHubSkill) string {
	parts := make([]string, 0, 4)
	if author := strings.TrimSpace(skill.Author); author != "" {
		parts = append(parts, T("作者 ")+author)
	}
	if category := strings.TrimSpace(skill.Category); category != "" {
		parts = append(parts, category)
	}
	if len(skill.Tags) > 0 {
		parts = append(parts, strings.Join(skill.Tags, " / "))
	}
	if skill.Downloads > 0 {
		parts = append(parts, Tf("%d 下载", skill.Downloads))
	}
	if status := strings.TrimSpace(skill.SecurityStatusName); status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

func (a *nativeApp) skillHubInstalledCard(c *ui.Context, p palette) {
	panel := &a.skillhub
	a.settingsCard(c, p, "已安装技能", "来自当前工作区与全局技能目录的市场技能；本地技能不可卸载。", func() {
		ui.Column(c).Gap(6).Children(func() {
			if len(panel.Installed) == 0 {
				ui.Text(c, T("尚无已安装的市场技能。")).FontSize(10.5).TextColor(p.t.TextMuted)
				return
			}
			busy := a.skillHubBrowseBusy()
			for index := range panel.Installed {
				index := index
				entry := panel.Installed[index]
				name := strings.TrimSpace(entry.Name)
				if name == "" {
					name = strings.TrimSpace(entry.ID)
				}
				active := entry.Active || skillHubActive(panel.ActiveSkills, name)
				ui.Row(c).AlignItems(ui.Center).Gap(7).Padding(7, 9).Radius(8).
					Background(p.t.Background).Border(1, p.t.Border).Children(func() {
					ui.Icon(c, iconSpark).FontSize(13).TextColor(p.t.TextMuted)
					ui.Column(c).Gap(1).Grow(1).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
							ui.Text(c, name).FontSize(11).FontWeight(600).TextColor(p.t.Text).SingleLine()
							if entry.Market != "" {
								badge(c, p, entry.Market)
							}
							if entry.Scope != "" {
								badge(c, p, entry.Scope)
							}
							if active {
								badge(c, p, "已激活")
							}
							if entry.Local {
								badge(c, p, "本地")
							}
						})
						meta := strings.TrimSpace(strings.Join(nonEmptyStrings(entry.Version, entry.Dir), " · "))
						if meta != "" {
							ui.Text(c, meta).FontSize(9).TextColor(p.t.TextMuted).SingleLine()
						}
					})
					activate := ui.Button(c.Key(fmt.Sprintf("skillhub-installed-activate:%d", index)), T("激活"))
					activate.Label(T("在当前会话激活技能 ") + name)
					activate.Disabled(busy || active || name == "")
					if activate.Clicked() && !busy && !active && name != "" {
						a.activateSkillHub(entry.Market, entry.ID, name)
					}
					remove := ui.Button(c.Key(fmt.Sprintf("skillhub-installed-uninstall:%d", index)), T("卸载"))
					remove.Label(T("卸载技能 ") + name)
					remove.Disabled(busy || entry.Local || entry.Market == "" || entry.ID == "")
					if remove.Clicked() && !busy && !entry.Local {
						a.requestUninstallSkillHub(entry.Market, entry.ID, entry.Scope, name)
					}
				})
			}
		})
	})
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func (a *nativeApp) skillHubStatusBanner(c *ui.Context, p palette, status string, isError, busy bool) {
	color := p.t.TextMuted
	bg := p.thought
	if isError {
		color = p.t.Danger
		bg = p.errorBg
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(bg).
		Border(1, p.t.Border).Children(func() {
		if isError {
			ui.Icon(c, iconAlert).FontSize(13).TextColor(color)
		} else if busy {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (a *nativeApp) skillHubConfigErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("SkillHub 配置不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.skillHubConfigCapable() {
			if ui.Button(c.Key("skillhub-config-retry"), T("重试")).Label(T("重试加载 SkillHub 配置")).Clicked() {
				a.skillhub.ConfigLoaded = false
				a.startSkillHubPanelLoad()
			}
		}
	})
}

func (a *nativeApp) skillHubCatalogErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("SkillHub 市场不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.skillHubCatalogCapable() {
			if ui.Button(c.Key("skillhub-catalog-retry"), T("重试")).Label(T("重试加载 SkillHub 市场")).Clicked() {
				a.skillhub.CatalogLoaded = false
				a.skillhub.CatalogErr = ""
				a.startSkillHubPanelLoad()
			}
		}
	})
}

func (a *nativeApp) skillHubLabeledInput(c *ui.Context, p palette, key, label string, value *string, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(label)
		input.Disabled(disabled)
	})
}

func (a *nativeApp) skillHubLabeledTextArea(c *ui.Context, p palette, key, label string, value *string, lines int, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		area := ui.TextAreaBase(c.Key(key), value).FontSize(12).Lines(lines, lines*3).
			Padding(6, 8).Background(p.t.Background).Radius(8).Border(1, p.t.Border)
		area.Disabled(disabled)
	})
}

// settingsSkillHubDialogs renders the native add-market, detail, and uninstall
// dialogs. Uninstall is drawn last so its confirmation sits above an open
// detail dialog.
func (a *nativeApp) settingsSkillHubDialogs(c *ui.Context, p palette) {
	if !a.settingsOpen && a.page != "skills" {
		return
	}
	panel := &a.skillhub
	if panel.DetailOpen {
		a.skillHubDetailDialog(c, p)
	}
	if panel.AddMarketOpen {
		a.skillHubAddMarketDialog(c, p)
	}
	if panel.UninstallOpen {
		a.skillHubUninstallDialog(c, p)
	}
}

func (a *nativeApp) skillHubAddMarketDialog(c *ui.Context, p palette) {
	panel := &a.skillhub
	dialog := ui.DialogBase(c, &panel.AddMarketOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconShop).FontSize(16).TextColor(p.t.Accent)
				ui.Text(c, T("添加 SkillHub 市场")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, T("市场 ID 是持久化配置键，例如 yuandun、skillhub.cn 或 clawhub.ai。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			a.skillHubLabeledInput(c, p, "skillhub-add-market-id", "市场 ID", &panel.AddMarketID, false)
			if panel.AddMarketErr != "" {
				ui.Text(c, T(panel.AddMarketErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("skillhub-add-market-cancel"), T("取消"))
				cancel.Label(T("取消添加市场"))
				if cancel.Clicked() {
					a.closeSkillHubAddMarket()
				}
				add := ui.PrimaryButton(c.Key("skillhub-add-market-confirm"), T("添加"))
				add.Label(T("添加市场到草稿列表"))
				if add.Clicked() {
					a.confirmSkillHubAddMarket()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeSkillHubAddMarket()
	}
}

func (a *nativeApp) skillHubDetailDialog(c *ui.Context, p palette) {
	panel := &a.skillhub
	dialog := ui.DialogBase(c, &panel.DetailOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(680).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			detail := panel.Detail
			title := "技能详情"
			if detail != nil {
				title = skillHubTitle(detail.skillHubSkill)
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconSpark).FontSize(17).TextColor(p.t.Accent)
				ui.Text(c, title).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1).SingleLine()
			})
			if panel.DetailBusy && detail == nil {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
					ui.Text(c, T("正在读取技能详情…")).FontSize(11).TextColor(p.t.TextMuted)
				})
			}
			if panel.DetailErr != "" {
				ui.Text(c, T(panel.DetailErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			if detail != nil {
				a.skillHubDetailBody(c, p, detail)
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				close := ui.Button(c.Key("skillhub-detail-close"), T("关闭"))
				close.Label(T("关闭技能详情"))
				if close.Clicked() {
					a.closeSkillHubDetail()
				}
				if detail != nil {
					a.skillHubDetailActions(c, p, detail)
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeSkillHubDetail()
	}
}

func (a *nativeApp) skillHubDetailBody(c *ui.Context, p palette, detail *skillHubDetail) {
	panel := &a.skillhub
	skill := detail.skillHubSkill
	state := detail.Installed
	ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
		if skill.Market != "" {
			badge(c, p, skill.Market)
		}
		if skill.Version != "" {
			badge(c, p, "v"+skill.Version)
		}
		if state != nil {
			if state.UpdateAvailable {
				badge(c, p, "可更新")
			} else if state.Active {
				badge(c, p, "已激活")
			} else {
				badge(c, p, "已安装")
			}
			if state.Scope != "" {
				badge(c, p, state.Scope)
			}
		}
		if skill.Suspicious {
			badge(c, p, "可疑")
		}
	})
	if skill.Description != "" {
		ui.Text(c, skill.Description).FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.4).Selectable()
	}
	if meta := skillHubSkillMeta(skill); meta != "" {
		ui.Text(c, meta).FontSize(10).TextColor(p.t.TextMuted)
	}
	ui.Scroll(c).Height(360).Children(func() {
		ui.Column(c).Gap(10).Children(func() {
			if detail.Readme != "" {
				ui.Text(c, detail.Readme).FontSize(10.5).TextColor(p.t.Text).LineHeight(1.45).Selectable()
			} else {
				ui.Text(c, T("该市场未返回 README。")).FontSize(10.5).TextColor(p.t.TextMuted)
			}
			if len(detail.Files) > 0 {
				ui.Text(c, Tf("文件（%d）", len(detail.Files))).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				for _, file := range detail.Files {
					line := file.Path
					if file.Size > 0 {
						line += fmt.Sprintf(" · %d bytes", file.Size)
					}
					ui.Text(c, line).FontSize(9.5).TextColor(p.t.TextMuted).Selectable()
				}
			}
		})
	})
	scope := panel.TargetScope
	if state != nil && strings.TrimSpace(state.Scope) != "" {
		scope = state.Scope
	}
	ui.Text(c, Tf("安装目标：%s · %s", scope, panel.TargetPath)).
		FontSize(9.5).TextColor(p.t.TextMuted).Selectable()
	overwrite := ui.Checkbox(c, &panel.Overwrite, T("覆盖已存在的技能版本")).FontSize(11)
	overwrite.Disabled(a.skillHubBrowseBusy())
	activate := ui.Checkbox(c, &panel.ActivateOnInstall, T("安装后立即在当前会话激活")).FontSize(11)
	activate.Disabled(a.skillHubBrowseBusy())
}

func (a *nativeApp) skillHubDetailActions(c *ui.Context, p palette, detail *skillHubDetail) {
	panel := &a.skillhub
	skill := detail.skillHubSkill
	state := detail.Installed
	busy := a.skillHubBrowseBusy()
	scope := panel.TargetScope
	name := skillHubInstalledName(skill)
	if state != nil {
		if strings.TrimSpace(state.Scope) != "" {
			scope = state.Scope
		}
		if strings.TrimSpace(state.Name) != "" {
			name = state.Name
		}
	}
	installLabel := "安装"
	switch {
	case state != nil && state.UpdateAvailable:
		installLabel = "更新"
	case state != nil:
		installLabel = "重新安装"
	}
	install := ui.PrimaryButton(c.Key("skillhub-detail-install"), T(installLabel))
	install.Label(T(installLabel + "技能"))
	install.Disabled(busy || panel.TargetPath == "")
	if install.Clicked() && !busy && panel.TargetPath != "" {
		if state != nil && state.UpdateAvailable {
			a.installSkillHubWithOverwrite(skill.Market, skill.ID, skill.Version, true)
		} else {
			a.installSkillHub(skill.Market, skill.ID, skill.Version)
		}
	}
	if state != nil {
		active := state.Active || skillHubActive(panel.ActiveSkills, name)
		activate := ui.Button(c.Key("skillhub-detail-activate"), T("激活"))
		activate.Label(T("在当前会话激活技能 ") + name)
		activate.Disabled(busy || active || name == "")
		if activate.Clicked() && !busy && !active && name != "" {
			a.activateSkillHub(skill.Market, skill.ID, name)
		}
		remove := ui.Button(c.Key("skillhub-detail-uninstall"), T("卸载"))
		remove.Label(T("卸载技能 ") + name)
		remove.Disabled(busy || state.Local)
		if remove.Clicked() && !busy && !state.Local {
			a.requestUninstallSkillHub(skill.Market, skill.ID, scope, name)
		}
	}
}

func (a *nativeApp) skillHubUninstallDialog(c *ui.Context, p palette) {
	panel := &a.skillhub
	dialog := ui.DialogBase(c, &panel.UninstallOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(460).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, T("卸载技能？")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, Tf("将从 %s 范围删除 %s（%s/%s）。该操作只删除市场安装的副本。",
				panel.UninstallScope, panel.UninstallName, panel.UninstallMarket, panel.UninstallID)).
				FontSize(11.5).TextColor(p.t.TextMuted).LineHeight(1.4).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("skillhub-uninstall-cancel"), T("取消"))
				cancel.Label(T("取消卸载技能"))
				if cancel.Clicked() {
					a.cancelUninstallSkillHub()
				}
				confirm := ui.Button(c.Key("skillhub-uninstall-confirm"), T("确认卸载"))
				confirm.Label(T("确认卸载技能"))
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(panel.ActionBusy != "")
				if confirm.Clicked() && panel.ActionBusy == "" {
					a.confirmUninstallSkillHub()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelUninstallSkillHub()
	}
}

func (a *nativeApp) skillHubCategoryRow(c *ui.Context, p palette) {
	panel := &a.skillhub
	categories := skillHubFlattenCategories(panel.Categories)
	officialMarket := strings.EqualFold(strings.TrimSpace(panel.SelectedMarket), "skillhub.cn")
	if len(categories) == 0 && panel.CategoryErr == "" && !panel.CategoryLoading && !officialMarket {
		return
	}
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
			ui.Text(c, T("分类")).FontSize(10).FontWeight(600).TextColor(p.t.TextMuted)
			a.skillHubCategoryChip(c, p, "", "全部")
			for _, category := range categories {
				a.skillHubCategoryChip(c, p, category.Key, skillHubCategoryName(category))
			}
			if strings.EqualFold(strings.TrimSpace(panel.SelectedMarket), "skillhub.cn") {
				ui.Box(c).Width(8)
				label := "官方推荐"
				if panel.Official {
					label = "官方推荐：开"
				}
				b := ui.ButtonBase(c.Key("skillhub-official")).Height(26).Padding(0, 10).Radius(999).
					AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
				if panel.Official {
					b.Background(p.active)
				} else if b.Hovered() {
					b.Background(p.hover)
				}
				b.Children(func() {
					color := p.t.Text
					if panel.Official {
						color = p.t.Accent
					}
					ui.Text(c, T(label)).FontSize(10.5).TextColor(color)
				})
				b.Label(T("官方推荐技能"))
				if b.Clicked() && !a.skillHubBrowseBusy() {
					panel.Official = !panel.Official
					a.searchSkillHub()
				}
			}
		})
		if panel.CategoryLoading {
			ui.Text(c, T("正在读取技能分类…")).FontSize(10).TextColor(p.t.TextMuted)
		}
		if panel.CategoryErr != "" {
			ui.Text(c, T(panel.CategoryErr)).FontSize(10).TextColor(p.t.Danger).Selectable()
		}
	})
}

func (a *nativeApp) skillHubCategoryChip(c *ui.Context, p palette, key, label string) {
	panel := &a.skillhub
	active := panel.Category == key
	b := ui.ButtonBase(c.Key("skillhub-category:"+key)).Height(26).Padding(0, 10).Radius(999).
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
		ui.Text(c, T(label)).FontSize(10.5).TextColor(color).SingleLine()
	})
	b.Label(T(label))
	if b.Clicked() && !active && !a.skillHubBrowseBusy() {
		panel.Category = key
		panel.Official = false
		a.searchSkillHub()
	}
}
