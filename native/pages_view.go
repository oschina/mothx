package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// pageGapCard is the Native projection of Desktop's capability gap card. It
// explains which ACP feature is missing instead of rendering a disabled form.
func (a *nativeApp) pageGapCard(c *ui.Context, p palette, icon *ui.SVG, title, desc string) {
	ui.Column(c).Padding(18).Radius(12).Background(p.thought).
		Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icon).FontSize(18).TextColor(p.t.TextMuted)
			ui.Text(c, T(title)).FontSize(13).FontWeight(650).TextColor(p.t.Text).Grow(1)
		})
		ui.Text(c, T(desc)).FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
	})
}

// skillsPageMarket is Desktop's SkillHubCatalog page projection: a hero with
// search, market/category chips, a card grid, and the installed list. Market
// configuration and local skill toggles stay in Settings; this page only
// browses, installs, activates, updates, and uninstalls through ACP.
func (a *nativeApp) skillsPageMarket(c *ui.Context, p palette) {
	panel := &a.skillhub
	if !a.skillHubCatalogCapable() {
		a.pageGapCard(c, p, iconShop, "技能市场不可用",
			"当前运行时未声明 manageSkillHubCatalog；市场目录请求必须绑定活动会话。")
		return
	}
	if strings.TrimSpace(a.currentSession) == "" {
		a.skillsPageMarketSessionRequired(c, p)
		return
	}
	if panel.CatalogLoading && !panel.CatalogLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(4, 2).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取 SkillHub 市场目录…")).FontSize(11.5).TextColor(p.t.TextMuted)
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
		ui.Text(c, T("正在刷新市场目录…")).FontSize(10).TextColor(p.t.TextMuted)
	}

	a.skillHubPageHero(c, p)
	a.skillHubPageResults(c, p)
	a.skillHubPageInstalled(c, p)
}

func (a *nativeApp) skillsPageMarketSessionRequired(c *ui.Context, p palette) {
	ui.Column(c).Padding(18).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Box(c).Size(36, 36).Radius(10).Background(p.active).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconShop).FontSize(18).TextColor(p.t.Accent)
			})
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, T("技能市场需要活动会话")).FontSize(13).FontWeight(650).TextColor(p.t.Text)
				ui.Text(c, T("先在对话中打开或新建会话，再回来浏览与安装在线技能。")).
					FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			})
			if ui.PrimaryButton(c.Key("skills-market-open-session"), T("新建会话")).Clicked() {
				a.newChat()
			}
		})
	})
}

func (a *nativeApp) skillHubPageHero(c *ui.Context, p palette) {
	panel := &a.skillhub
	busy := a.skillHubBrowseBusy()
	ui.Column(c).Padding(14).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Start).Gap(9).Children(func() {
			ui.Box(c).Size(36, 36).Radius(10).Background(p.active).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconShop).FontSize(18).TextColor(p.t.Accent)
			})
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, T("技能市场")).FontSize(15).FontWeight(650).TextColor(p.t.Text)
				ui.Text(c, T("在线目录、详情与安装都通过 ACP 投影；安装目录由 Runtime 工作区决定。")).
					FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.35)
			})
			if a.skillHubConfigCapable() {
				if ui.Button(c.Key("skills-market-config"), T("市场配置")).Clicked() {
					a.openSettingsTab("skillhub")
				}
			}
			refresh := ui.Button(c.Key("skills-market-refresh"), T("刷新目录"))
			refresh.Disabled(busy)
			if refresh.Clicked() && !busy {
				a.refreshSkillHubCatalog()
			}
		})

		ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				search := ui.TextInput(c.Key("skills-market-query"), &panel.Query).
					Placeholder(T("搜索在线技能；留空浏览当前市场热门技能")).FontSize(12).Height(34).
					Padding(0, 10).Background(p.t.Background).Radius(8).Border(1, p.t.Border).
					Label(T("搜索在线技能"))
				search.Disabled(busy)
			})
			search := ui.PrimaryButton(c.Key("skills-market-search"), T("搜索"))
			search.Label(T("搜索 SkillHub 市场"))
			search.Disabled(busy)
			if search.Clicked() && !busy {
				a.searchSkillHub()
			}
		})

		ui.Row(c).AlignItems(ui.Center).Gap(4).Wrap().Children(func() {
			ui.Text(c, T("市场")).FontSize(9.5).FontWeight(600).TextColor(p.t.TextMuted)
			if len(panel.MarketInfos) == 0 {
				ui.Text(c, T("当前运行时没有可用市场。")).FontSize(10).TextColor(p.t.TextMuted)
			}
			for _, market := range panel.MarketInfos {
				market := market
				id := strings.TrimSpace(market.ID)
				if id == "" {
					continue
				}
				label := strings.TrimSpace(market.Name)
				if label == "" {
					label = id
				}
				active := id == panel.SelectedMarket
				b := ui.ButtonBase(c.Key("skills-market:"+id)).Height(26).Padding(0, 10).Radius(999).
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
				b.Disabled(busy)
				if b.Clicked() && !active && !busy {
					panel.SelectedMarket = id
					skillHubOnMarketChanged(panel)
					a.loadSkillHubCategories()
					a.searchSkillHub()
				}
			}
		})
		a.skillHubCategoryRow(c, p)

		targetLabels := skillHubTargetLabels(panel.Targets)
		if len(targetLabels) > 0 {
			ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					old := panel.TargetLabel
					a.settingsSelect(c, p, "skills-market-target", "安装目录", targetLabels, &panel.TargetLabel)
					if old != panel.TargetLabel {
						skillHubOnTargetChanged(panel)
					}
				})
				if panel.TargetPath != "" {
					ui.Text(c, T("安装目标：")+panel.TargetScope+" · "+panel.TargetPath).
						FontSize(9.5).TextColor(p.t.TextMuted).SingleLine().MaxWidth(360)
				}
			})
		}
	})
}

func (a *nativeApp) skillHubPageResults(c *ui.Context, p palette) {
	panel := &a.skillhub
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, T("在线技能")).FontSize(12).FontWeight(650).TextColor(p.t.Text)
			if panel.ResultsTotal > 0 {
				badge(c, p, fmt.Sprintf("%d", panel.ResultsTotal))
			}
			ui.Spacer(c)
			if panel.ResultsQuery != "" {
				ui.Text(c, T("结果关键词：")+panel.ResultsQuery).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			}
		})
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
			ui.Column(c).Padding(16).Radius(10).Border(1, p.t.Border).Gap(4).Children(func() {
				ui.Text(c, T("没有找到匹配技能")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
				ui.Text(c, T("尝试更换市场、缩短关键词或清空关键词浏览热门技能。")).
					FontSize(10.5).TextColor(p.t.TextMuted)
			})
			return
		}
		ui.Row(c).Wrap().Gap(8).Children(func() {
			for index := range panel.Results {
				index := index
				a.skillHubPageResultCard(c, p, index)
			}
		})
	})
}

func (a *nativeApp) skillHubPageResultCard(c *ui.Context, p palette, index int) {
	panel := &a.skillhub
	if index < 0 || index >= len(panel.Results) {
		return
	}
	skill := panel.Results[index]
	installed := skill.Installed
	active := installed != nil && installed.Active
	if !active {
		active = skillHubActive(panel.ActiveSkills, skillHubInstalledName(skill))
	}
	busy := a.skillHubBrowseBusy()
	ui.Column(c).Width(300).Padding(12).Radius(12).Background(p.t.Surface).
		Border(1, p.t.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Box(c).Size(30, 30).Radius(9).Background(p.active).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconSpark).FontSize(15).TextColor(p.t.Accent)
			})
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Text(c, skillHubTitle(skill)).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
				meta := []string{}
				if market := strings.TrimSpace(skill.Market); market != "" {
					meta = append(meta, market)
				}
				if version := strings.TrimSpace(skill.Version); version != "" {
					meta = append(meta, "v"+version)
				}
				if author := strings.TrimSpace(skill.Author); author != "" {
					meta = append(meta, author)
				}
				if len(meta) > 0 {
					ui.Text(c, strings.Join(meta, " · ")).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
				}
			})
			if skill.Suspicious {
				badge(c, p, "可疑")
			}
			if installed != nil {
				if installed.UpdateAvailable {
					badge(c, p, "可更新")
				} else if active {
					badge(c, p, "已激活")
				} else {
					badge(c, p, "已安装")
				}
			}
		})
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = T("无描述")
		}
		ui.Text(c, description).FontSize(10).TextColor(p.t.TextMuted).LineHeight(1.35).MaxLines(3)
		meta := skillHubSkillMeta(skill)
		if meta != "" {
			ui.Text(c, meta).FontSize(9).TextColor(p.t.TextMuted).SingleLine()
		}
		ui.Row(c).AlignItems(ui.Center).Gap(5).Margin(2, 0, 0, 0).Children(func() {
			detail := ui.Button(c.Key(fmt.Sprintf("skills-market-detail:%s:%s", skill.Market, skill.ID)), T("详情"))
			detail.Label(T("查看技能详情 ") + skillHubTitle(skill))
			detail.Disabled(busy)
			if detail.Clicked() && !busy {
				a.openSkillHubDetail(skill.Market, skill.ID)
			}
			if installed == nil {
				install := ui.PrimaryButton(c.Key(fmt.Sprintf("skills-market-install:%s:%s", skill.Market, skill.ID)), T("安装"))
				install.Label(T("安装技能 ") + skillHubTitle(skill))
				install.Disabled(busy || panel.TargetPath == "")
				if install.Clicked() && !busy && panel.TargetPath != "" {
					a.installSkillHub(skill.Market, skill.ID, skill.Version)
				}
			} else {
				if installed.UpdateAvailable {
					update := ui.PrimaryButton(c.Key(fmt.Sprintf("skills-market-update:%s:%s", skill.Market, skill.ID)), T("更新"))
					update.Label(T("更新技能 ") + skillHubTitle(skill))
					update.Disabled(busy || panel.TargetPath == "")
					if update.Clicked() && !busy && panel.TargetPath != "" {
						a.installSkillHubWithOverwrite(skill.Market, skill.ID, skill.Version, true)
					}
				}
				activate := ui.Button(c.Key(fmt.Sprintf("skills-market-activate:%s:%s", skill.Market, skill.ID)), T("激活"))
				activate.Label(T("在当前会话激活技能 ") + skillHubTitle(skill))
				activate.Disabled(busy || active)
				if activate.Clicked() && !busy && !active {
					a.activateSkillHub(skill.Market, skill.ID, skillHubInstalledName(skill))
				}
				remove := ui.Button(c.Key(fmt.Sprintf("skills-market-uninstall:%s:%s", skill.Market, skill.ID)), T("卸载"))
				remove.Label(T("卸载技能 ") + skillHubTitle(skill))
				remove.Disabled(busy)
				if remove.Clicked() && !busy {
					scope := installed.Scope
					if scope == "" {
						scope = panel.TargetScope
					}
					a.requestUninstallSkillHub(skill.Market, skill.ID, scope, skillHubInstalledName(skill))
				}
			}
		})
	})
}

func (a *nativeApp) skillHubPageInstalled(c *ui.Context, p palette) {
	panel := &a.skillhub
	ui.Column(c).Gap(7).Children(func() {
		ui.Text(c, T("已安装技能")).FontSize(12).FontWeight(650).TextColor(p.t.Text)
		if len(panel.Installed) == 0 {
			ui.Text(c, T("尚无已安装的市场技能。")).FontSize(10.5).TextColor(p.t.TextMuted)
			return
		}
		busy := a.skillHubBrowseBusy()
		ui.Row(c).Wrap().Gap(7).Children(func() {
			for index := range panel.Installed {
				index := index
				entry := panel.Installed[index]
				name := strings.TrimSpace(entry.Name)
				if name == "" {
					name = strings.TrimSpace(entry.ID)
				}
				active := entry.Active || skillHubActive(panel.ActiveSkills, name)
				ui.Column(c).Width(280).Padding(9, 10).Radius(9).Background(p.t.Background).
					Border(1, p.t.Border).Gap(5).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
						ui.Icon(c, iconSpark).FontSize(13).TextColor(p.t.TextMuted)
						ui.Text(c, name).FontSize(11).FontWeight(600).TextColor(p.t.Text).SingleLine().Grow(1)
						if entry.Market != "" {
							badge(c, p, entry.Market)
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
					ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
						activate := ui.Button(c.Key(fmt.Sprintf("skills-market-installed-activate:%d", index)), T("激活"))
						activate.Label(T("在当前会话激活技能 ") + name)
						activate.Disabled(busy || active || name == "")
						if activate.Clicked() && !busy && !active && name != "" {
							a.activateSkillHub(entry.Market, entry.ID, name)
						}
						remove := ui.Button(c.Key(fmt.Sprintf("skills-market-installed-uninstall:%d", index)), T("卸载"))
						remove.Label(T("卸载技能 ") + name)
						remove.Disabled(busy || entry.Local || entry.Market == "" || entry.ID == "")
						if remove.Clicked() && !busy && !entry.Local {
							a.requestUninstallSkillHub(entry.Market, entry.ID, entry.Scope, name)
						}
					})
				})
			}
		})
	})
}
