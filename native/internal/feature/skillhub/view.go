package skillhub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// settingsSkillHubPane renders the Native SkillHub manager. Configuration and
// catalogue capabilities are gated independently: manageSkillHub owns the
// global token-free market configuration, while manageSkillHubCatalog owns the
// session-bound marketplace browser. No panel reads settings.json or a skill
// directory directly.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.ConfigCapable(h) && !p.CatalogCapable(h) {
		kit.SettingsCard(c, pal, "SkillHub", "当前运行时不支持 SkillHub 配置或市场目录。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageSkillHub 与 manageSkillHubCatalog；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	tab := p.Tab
	ui.Tabs(c, &tab, i18n.T("浏览市场"), i18n.T("市场配置"))
	p.Tab = tab
	if tab == 1 {
		p.ConfigPane(h, c, pal)
		return
	}
	p.BrowsePane(h, c, pal)
}

func (p *Panel) ConfigPane(h Host, c *ui.Context, pal kit.Palette) {
	if !p.ConfigCapable(h) {
		kit.SettingsCard(c, pal, "SkillHub 配置", "当前运行时未公开 SkillHub 配置能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageSkillHub；市场浏览仍可通过另一项能力独立使用。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}
	if p.ConfigLoading && !p.ConfigLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取 SkillHub 配置…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}
	if p.ConfigErr != "" {
		p.ConfigErrorCard(h, c, pal, p.ConfigErr)
		if !p.ConfigLoaded {
			return
		}
	}
	if p.ConfigStatus != "" {
		p.StatusBanner(h, c, pal, p.ConfigStatus, p.ConfigStatusIsError, p.ConfigSaving)
	}
	if p.ConfigLoading && p.ConfigLoaded {
		ui.Text(c, i18n.T("正在刷新 SkillHub 配置…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	kit.SettingsCard(c, pal, "默认行为", "默认市场与安装范围决定市场浏览和新安装的初始选择。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ids := MarketIDs(p.Markets)
			if p.DefaultMarket != "" && !Contains(ids, p.DefaultMarket) {
				ids = append([]string{p.DefaultMarket}, ids...)
			}
			if len(ids) > 0 {
				kit.SettingsSelect(c, pal, "skillhub-default-market", "默认市场", ids, &p.DefaultMarket)
			} else {
				p.LabeledInput(h, c, pal, "skillhub-default-market-text", "默认市场", &p.DefaultMarket, p.ConfigSaving)
			}
			kit.SettingsSelect(c, pal, "skillhub-default-scope", "默认安装范围", []string{"project", "global"}, &p.DefaultInstallScope)
			p.LabeledTextArea(h, c, pal, "skillhub-official-handles", "官方推荐 Handles（每行一个）", &p.OfficialHandles, 3, p.ConfigSaving)
			ui.Text(c, i18n.T("Token 只写入服务端；已配置状态只会显示为布尔标记，从不回显。")).
				FontSize(9.5).TextColor(pal.T.TextMuted)
		})
	})

	kit.SettingsCard(c, pal, "市场源", "编辑市场端点与 Token；清空 Token 与提交新 Token 互斥。", func() {
		ui.Column(c).Gap(8).Children(func() {
			if len(p.Markets) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
					ui.Text(c, i18n.T("尚未配置市场源")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
					ui.Text(c, i18n.T("添加市场并保存后，市场浏览会按默认市场选择。")).FontSize(10.5).TextColor(pal.T.TextMuted)
				})
			} else {
				for index := range p.Markets {
					index := index
					p.MarketEditor(h, c, pal, index)
				}
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
				add := ui.Button(c.Key("skillhub-add-market"), i18n.T("添加市场"))
				add.Label(i18n.T("添加 SkillHub 市场"))
				add.Disabled(p.ConfigSaving || p.ConfigLoading)
				if add.Clicked() && !p.ConfigSaving && !p.ConfigLoading {
					p.OpenAddMarket(h)
				}
				save := ui.PrimaryButton(c.Key("skillhub-save-config"), i18n.T("保存配置"))
				save.Label(i18n.T("保存 SkillHub 配置"))
				save.Disabled(p.ConfigSaving || p.ConfigLoading)
				if save.Clicked() && !p.ConfigSaving && !p.ConfigLoading {
					p.SaveConfig(h)
				}
			})
		})
	})
}

func Contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (p *Panel) MarketEditor(h Host, c *ui.Context, pal kit.Palette, index int) {
	if index < 0 || index >= len(p.Markets) {
		return
	}
	market := &p.Markets[index]
	busy := p.ConfigSaving || p.ConfigLoading
	ui.Column(c).Padding(10).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconShop).FontSize(15).TextColor(pal.T.Accent)
			ui.Text(c, market.ID).FontSize(12).FontWeight(650).TextColor(pal.T.Text).Grow(1).SingleLine()
			if market.Configured {
				kit.Badge(c, pal, "Token 已配置")
			} else {
				kit.Badge(c, pal, "Token 未配置")
			}
			remove := ui.Button(c.Key(fmt.Sprintf("skillhub-remove-market:%d", index)), i18n.T("移除"))
			remove.Label(i18n.T("移除市场 ") + market.ID)
			remove.Disabled(busy)
			if remove.Clicked() && !busy {
				p.RemoveMarket(index)
			}
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				p.LabeledInput(h, c, pal, fmt.Sprintf("skillhub-market-name:%d", index), "名称", &market.Name, busy)
			})
			ui.Column(c).Grow(1).Children(func() {
				p.LabeledInput(h, c, pal, fmt.Sprintf("skillhub-market-site:%d", index), "站点 URL", &market.SiteURL, busy)
			})
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Column(c).Grow(1).Children(func() {
				p.LabeledInput(h, c, pal, fmt.Sprintf("skillhub-market-api:%d", index), "API URL", &market.APIURL, busy)
			})
			ui.Column(c).Grow(1).Children(func() {
				status := "尚未配置 Token"
				if market.Configured {
					status = "已配置 Token；留空保持不变"
				}
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, "API Token").FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
					token := ui.TextInput(c.Key(fmt.Sprintf("skillhub-market-token:%d", index)), &market.Token).
						Password().FontSize(12).Height(32).Padding(0, 8).
						Placeholder(status).Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
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
			enabled := ui.Checkbox(c, &market.Enabled, i18n.T("启用该市场")).FontSize(11.5)
			enabled.Disabled(busy)
			wasClearing := market.ClearToken
			clear := ui.Checkbox(c, &market.ClearToken, i18n.T("清除已保存的 Token")).FontSize(11.5)
			clear.Disabled(busy || !market.Configured)
			if market.ClearToken && !wasClearing {
				market.Token = ""
			}
		})
	})
}

func (p *Panel) BrowsePane(h Host, c *ui.Context, pal kit.Palette) {
	if !p.CatalogCapable(h) {
		kit.SettingsCard(c, pal, "SkillHub 市场", "当前运行时未公开 SkillHub 市场目录能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageSkillHubCatalog；市场目录请求必须绑定活动会话。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}
	if strings.TrimSpace(h.CurrentSession()) == "" {
		kit.SettingsCard(c, pal, "技能市场需要活动会话", "先在对话中打开或新建会话，再回来浏览与安装在线技能。", func() {
			if ui.PrimaryButton(c.Key("skillhub-open-session"), i18n.T("新建会话")).Clicked() {
				h.StartNewTask()
			}
		})
		return
	}
	if p.CatalogLoading && !p.CatalogLoaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取 SkillHub 市场目录…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}
	if p.CatalogErr != "" {
		p.CatalogErrorCard(h, c, pal, p.CatalogErr)
		if !p.CatalogLoaded || p.SessionID == "" {
			return
		}
	}
	if p.CatalogStatus != "" {
		p.StatusBanner(h, c, pal, p.CatalogStatus, p.CatalogStatusIsError, p.ActionBusy != "" || p.SearchBusy)
	}
	if p.CatalogLoading && p.CatalogLoaded {
		ui.Text(c, i18n.T("正在刷新市场目录…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	busy := p.BrowseBusy()
	kit.SettingsCard(c, pal, "市场目录", "在线目录、详情与安装都通过 ACP 投影；安装目录由 Runtime 工作区决定。", func() {
		ui.Column(c).Gap(8).Children(func() {
			if p.WorkDir != "" {
				ui.Text(c, i18n.T("工作区：")+p.WorkDir).FontSize(10).TextColor(pal.T.TextMuted).Selectable()
			}
			ui.Row(c).Gap(8).Children(func() {
				ids := make([]string, 0, len(p.MarketInfos))
				for _, market := range p.MarketInfos {
					if id := strings.TrimSpace(market.ID); id != "" {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					ui.Text(c, i18n.T("当前运行时没有可用市场。")).FontSize(10.5).TextColor(pal.T.TextMuted).Grow(1)
				} else {
					old := p.SelectedMarket
					ui.Column(c).Grow(1).Children(func() {
						kit.SettingsSelect(c, pal, "skillhub-browse-market", "市场", ids, &p.SelectedMarket)
					})
					if old != p.SelectedMarket {
						OnMarketChanged(&p.State)
						p.LoadCategories(h)
					}
				}
				targetLabels := TargetLabels(p.Targets)
				if len(targetLabels) == 0 {
					ui.Column(c).Grow(1).Children(func() {
						ui.Text(c, i18n.T("安装目录不可用")).FontSize(10.5).TextColor(pal.T.Danger)
					})
				} else {
					oldTarget := p.TargetLabel
					ui.Column(c).Grow(1).Children(func() {
						kit.SettingsSelect(c, pal, "skillhub-browse-target", "安装目录", targetLabels, &p.TargetLabel)
					})
					if oldTarget != p.TargetLabel {
						OnTargetChanged(&p.State)
					}
				}
			})
			p.CategoryRow(h, c, pal)
			p.SortRow(h, c, pal)
			ui.Row(c).AlignItems(ui.End).Gap(8).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Text(c, i18n.T("搜索在线技能")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
					search := ui.TextInput(c.Key("skillhub-query"), &p.Query).
						Placeholder(i18n.T("输入关键词；留空浏览热门技能")).FontSize(12).Height(32).Padding(0, 8).
						Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("搜索在线技能"))
					search.Disabled(busy)
				})
				btn := ui.PrimaryButton(c.Key("skillhub-search"), i18n.T("搜索"))
				btn.Label(i18n.T("搜索 SkillHub 市场"))
				btn.Disabled(busy)
				if btn.Clicked() && !busy {
					p.Search(h)
				}
				refresh := ui.Button(c.Key("skillhub-refresh"), i18n.T("刷新目录"))
				refresh.Label(i18n.T("刷新 SkillHub 市场目录"))
				refresh.Disabled(busy)
				if refresh.Clicked() && !busy {
					p.RefreshCatalog(h)
				}
			})
			if p.TargetPath != "" {
				ui.Text(c, i18n.T("安装目标：")+p.TargetScope+" · "+p.TargetPath).
					FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
			}
		})
	})

	p.ResultsCard(h, c, pal)
	p.InstalledCard(h, c, pal)
}

func (p *Panel) ResultsCard(h Host, c *ui.Context, pal kit.Palette) {
	desc := "搜索或浏览当前市场。安装前会使用上方选定的目录与范围。"
	if p.ResultsTotal > 0 {
		desc = i18n.Tf("共 %d 个结果；%s", p.ResultsTotal, i18n.T(desc))
	}
	kit.SettingsCard(c, pal, "在线技能", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			if p.SearchBusy {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
					ui.Text(c, i18n.T("正在搜索技能…")).FontSize(11).TextColor(pal.T.TextMuted)
				})
				return
			}
			if p.SearchErr != "" {
				ui.Text(c, i18n.T(p.SearchErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			if !p.ResultsLoaded {
				ui.Text(c, i18n.T("输入关键词搜索，或留空后点击“搜索”浏览当前市场热门技能。")).
					FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
				return
			}
			if len(p.Results) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
					ui.Text(c, i18n.T("没有找到匹配技能")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
					ui.Text(c, i18n.T("尝试更换市场、缩短关键词或清空关键词浏览热门技能。")).
						FontSize(10.5).TextColor(pal.T.TextMuted)
				})
				return
			}
			for index := range p.Results {
				index := index
				p.ResultRow(h, c, pal, index)
			}
			if p.ResultsQuery != "" {
				ui.Text(c, i18n.T("结果关键词：")+p.ResultsQuery).FontSize(9.5).TextColor(pal.T.TextMuted)
			}
		})
	})
}

func (p *Panel) ResultRow(h Host, c *ui.Context, pal kit.Palette, index int) {
	if index < 0 || index >= len(p.Results) {
		return
	}
	skill := p.Results[index]
	state := skill.Installed
	active := state != nil && state.Active
	if !active {
		active = Active(p.ActiveSkills, InstalledName(skill))
	}
	busy := p.BrowseBusy()
	ui.Column(c).Padding(9).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			h.SkillIconElement(c, pal, skill, 18)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, Title(skill)).FontSize(12).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					if market := strings.TrimSpace(skill.Market); market != "" {
						kit.Badge(c, pal, market)
					}
					if version := strings.TrimSpace(skill.Version); version != "" {
						kit.Badge(c, pal, "v"+version)
					}
					if state != nil {
						if state.UpdateAvailable {
							kit.Badge(c, pal, "可更新")
						} else if active {
							kit.Badge(c, pal, "已激活")
						} else {
							kit.Badge(c, pal, "已安装")
						}
					}
					if skill.Suspicious {
						kit.Badge(c, pal, "可疑")
					}
				})
				description := strings.TrimSpace(skill.Description)
				if description == "" {
					description = "无描述"
				}
				ui.Text(c, description).FontSize(9.5).TextColor(pal.T.TextMuted).SingleLine()
				meta := SkillMeta(skill)
				if meta != "" {
					ui.Text(c, meta).FontSize(9).TextColor(pal.T.TextMuted).SingleLine()
				}
			})
			detail := ui.Button(c.Key(fmt.Sprintf("skillhub-detail:%s:%s", skill.Market, skill.ID)), i18n.T("详情"))
			detail.Label(i18n.T("查看技能详情 ") + Title(skill))
			detail.Disabled(busy)
			if detail.Clicked() && !busy {
				p.OpenDetail(h, skill.Market, skill.ID)
			}
			if state == nil {
				install := ui.PrimaryButton(c.Key(fmt.Sprintf("skillhub-install:%s:%s", skill.Market, skill.ID)), i18n.T("安装"))
				install.Label(i18n.T("安装技能 ") + Title(skill))
				install.Disabled(busy || p.TargetPath == "")
				if install.Clicked() && !busy && p.TargetPath != "" {
					p.Install(h, skill.Market, skill.ID, skill.Version)
				}
			} else if state.UpdateAvailable {
				update := ui.PrimaryButton(c.Key(fmt.Sprintf("skillhub-update:%s:%s", skill.Market, skill.ID)), i18n.T("更新"))
				update.Label(i18n.T("更新技能 ") + Title(skill))
				update.Disabled(busy || p.TargetPath == "")
				if update.Clicked() && !busy && p.TargetPath != "" {
					p.InstallWithOverwrite(h, skill.Market, skill.ID, skill.Version, true)
				}
			}
			if state != nil {
				activate := ui.Button(c.Key(fmt.Sprintf("skillhub-activate:%s:%s", skill.Market, skill.ID)), i18n.T("激活"))
				activate.Label(i18n.T("在当前会话激活技能 ") + Title(skill))
				activate.Disabled(busy || active)
				if activate.Clicked() && !busy && !active {
					p.Activate(h, skill.Market, skill.ID, InstalledName(skill))
				}
				remove := ui.Button(c.Key(fmt.Sprintf("skillhub-uninstall:%s:%s", skill.Market, skill.ID)), i18n.T("卸载"))
				remove.Label(i18n.T("卸载技能 ") + Title(skill))
				remove.Disabled(busy)
				if remove.Clicked() && !busy {
					scope := state.Scope
					if scope == "" {
						scope = p.TargetScope
					}
					p.RequestUninstall(h, skill.Market, skill.ID, scope, InstalledName(skill))
				}
			}
		})
	})
}

func SkillMeta(skill Skill) string {
	parts := make([]string, 0, 4)
	if author := strings.TrimSpace(skill.Author); author != "" {
		parts = append(parts, i18n.T("作者 ")+author)
	}
	if category := strings.TrimSpace(skill.Category); category != "" {
		parts = append(parts, category)
	}
	if len(skill.Tags) > 0 {
		parts = append(parts, strings.Join(skill.Tags, " / "))
	}
	if skill.Downloads > 0 {
		parts = append(parts, i18n.Tf("%d 下载", skill.Downloads))
	}
	if skill.Stars > 0 {
		parts = append(parts, i18n.Tf("%d %s", skill.Stars, i18n.T("Star")))
	}
	if status := strings.TrimSpace(skill.SecurityStatusName); status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

func (p *Panel) InstalledCard(h Host, c *ui.Context, pal kit.Palette) {
	kit.SettingsCard(c, pal, "已安装技能", "来自当前工作区与全局技能目录的市场技能；本地技能不可卸载。", func() {
		ui.Column(c).Gap(6).Children(func() {
			if len(p.Installed) == 0 {
				ui.Text(c, i18n.T("尚无已安装的市场技能。")).FontSize(10.5).TextColor(pal.T.TextMuted)
				return
			}
			busy := p.BrowseBusy()
			for index := range p.Installed {
				index := index
				entry := p.Installed[index]
				name := strings.TrimSpace(entry.Name)
				if name == "" {
					name = strings.TrimSpace(entry.ID)
				}
				active := entry.Active || Active(p.ActiveSkills, name)
				ui.Row(c).AlignItems(ui.Center).Gap(7).Padding(7, 9).Radius(8).
					Background(pal.T.Background).Border(1, pal.T.Border).Children(func() {
					ui.Icon(c, icons.IconSpark).FontSize(13).TextColor(pal.T.TextMuted)
					ui.Column(c).Gap(1).Grow(1).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
							ui.Text(c, name).FontSize(11).FontWeight(600).TextColor(pal.T.Text).SingleLine()
							if entry.Market != "" {
								kit.Badge(c, pal, entry.Market)
							}
							if entry.Scope != "" {
								kit.Badge(c, pal, entry.Scope)
							}
							if active {
								kit.Badge(c, pal, "已激活")
							}
							if entry.Local {
								kit.Badge(c, pal, "本地")
							}
						})
						meta := strings.TrimSpace(strings.Join(NonEmptyStrings(entry.Version, entry.Dir), " · "))
						if meta != "" {
							ui.Text(c, meta).FontSize(9).TextColor(pal.T.TextMuted).SingleLine()
						}
					})
					activate := ui.Button(c.Key(fmt.Sprintf("skillhub-installed-activate:%d", index)), i18n.T("激活"))
					activate.Label(i18n.T("在当前会话激活技能 ") + name)
					activate.Disabled(busy || active || name == "")
					if activate.Clicked() && !busy && !active && name != "" {
						p.Activate(h, entry.Market, entry.ID, name)
					}
					remove := ui.Button(c.Key(fmt.Sprintf("skillhub-installed-uninstall:%d", index)), i18n.T("卸载"))
					remove.Label(i18n.T("卸载技能 ") + name)
					remove.Disabled(busy || entry.Local || entry.Market == "" || entry.ID == "")
					if remove.Clicked() && !busy && !entry.Local {
						p.RequestUninstall(h, entry.Market, entry.ID, entry.Scope, name)
					}
				})
			}
		})
	})
}

func NonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

// PrettyJSON formats an optional ACP JSON payload for the detail
// dialog. Empty or malformed payloads are omitted rather than echoed raw.
func PrettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}

func (p *Panel) StatusBanner(h Host, c *ui.Context, pal kit.Palette, status string, isError, busy bool) {
	color := pal.T.TextMuted
	bg := pal.Thought
	if isError {
		color = pal.T.Danger
		bg = pal.ErrorBg
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(bg).
		Border(1, pal.T.Border).Children(func() {
		if isError {
			ui.Icon(c, icons.IconAlert).FontSize(13).TextColor(color)
		} else if busy {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) ConfigErrorCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("SkillHub 配置不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.ConfigCapable(h) {
			if ui.Button(c.Key("skillhub-config-retry"), i18n.T("重试")).Label(i18n.T("重试加载 SkillHub 配置")).Clicked() {
				p.ConfigLoaded = false
				p.StartPanelLoad(h)
			}
		}
	})
}

func (p *Panel) CatalogErrorCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("SkillHub 市场不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.CatalogCapable(h) {
			if ui.Button(c.Key("skillhub-catalog-retry"), i18n.T("重试")).Label(i18n.T("重试加载 SkillHub 市场")).Clicked() {
				p.CatalogLoaded = false
				p.CatalogErr = ""
				p.StartPanelLoad(h)
			}
		}
	})
}

func (p *Panel) LabeledInput(h Host, c *ui.Context, pal kit.Palette, key, label string, value *string, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(label)
		input.Disabled(disabled)
	})
}

func (p *Panel) LabeledTextArea(h Host, c *ui.Context, pal kit.Palette, key, label string, value *string, lines int, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		area := ui.TextAreaBase(c.Key(key), value).FontSize(12).Lines(lines, lines*3).
			Padding(6, 8).Background(pal.T.Background).Radius(8).Border(1, pal.T.Border)
		area.Disabled(disabled)
	})
}

// Dialogs renders the native add-market, detail, and uninstall
// dialogs. Uninstall is drawn last so its confirmation sits above an open
// detail dialog.
func (p *Panel) Dialogs(h Host, c *ui.Context, pal kit.Palette) {
	if !h.SettingsOpen() && h.Page() != "skills" {
		return
	}
	if p.DetailOpen {
		p.DetailDialog(h, c, pal)
	}
	if p.AddMarketOpen {
		p.AddMarketDialog(h, c, pal)
	}
	if p.UninstallOpen {
		p.UninstallDialog(h, c, pal)
	}
}

func (p *Panel) AddMarketDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.AddMarketOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconShop).FontSize(16).TextColor(pal.T.Accent)
				ui.Text(c, i18n.T("添加 SkillHub 市场")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.T("市场 ID 是持久化配置键，例如 yuandun、skillhub.cn 或 clawhub.ai。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			p.LabeledInput(h, c, pal, "skillhub-add-market-id", "市场 ID", &p.AddMarketID, false)
			if p.AddMarketErr != "" {
				ui.Text(c, i18n.T(p.AddMarketErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("skillhub-add-market-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消添加市场"))
				if cancel.Clicked() {
					p.CloseAddMarket()
				}
				add := ui.PrimaryButton(c.Key("skillhub-add-market-confirm"), i18n.T("添加"))
				add.Label(i18n.T("添加市场到草稿列表"))
				if add.Clicked() {
					p.ConfirmAddMarket(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CloseAddMarket()
	}
}

func (p *Panel) DetailDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.DetailOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(680).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			detail := p.Detail
			title := "技能详情"
			if detail != nil {
				title = Title(detail.Skill)
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if detail != nil {
					h.SkillIconElement(c, pal, detail.Skill, 22)
				} else {
					ui.Icon(c, icons.IconSpark).FontSize(17).TextColor(pal.T.Accent)
				}
				ui.Text(c, title).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1).SingleLine()
			})
			if p.DetailBusy && detail == nil {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
					ui.Text(c, i18n.T("正在读取技能详情…")).FontSize(11).TextColor(pal.T.TextMuted)
				})
			}
			if p.DetailErr != "" {
				ui.Text(c, i18n.T(p.DetailErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			if detail != nil {
				p.DetailBody(h, c, pal, detail)
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				close := ui.Button(c.Key("skillhub-detail-close"), i18n.T("关闭"))
				close.Label(i18n.T("关闭技能详情"))
				if close.Clicked() {
					p.CloseDetail()
				}
				if detail != nil {
					p.DetailActions(h, c, pal, detail)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CloseDetail()
	}
}

func (p *Panel) DetailBody(h Host, c *ui.Context, pal kit.Palette, detail *Detail) {
	skill := detail.Skill
	state := detail.Installed
	ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
		if skill.Market != "" {
			kit.Badge(c, pal, skill.Market)
		}
		if skill.Version != "" {
			kit.Badge(c, pal, "v"+skill.Version)
		}
		if state != nil {
			if state.UpdateAvailable {
				kit.Badge(c, pal, "可更新")
			} else if state.Active {
				kit.Badge(c, pal, "已激活")
			} else {
				kit.Badge(c, pal, "已安装")
			}
			if state.Scope != "" {
				kit.Badge(c, pal, state.Scope)
			}
		}
		if skill.Suspicious {
			kit.Badge(c, pal, "可疑")
		}
	})
	if skill.Description != "" {
		ui.Text(c, skill.Description).FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.4).Selectable()
	}
	if meta := SkillMeta(skill); meta != "" {
		ui.Text(c, meta).FontSize(10).TextColor(pal.T.TextMuted)
	}
	ui.Scroll(c).Height(360).Children(func() {
		ui.Column(c).Gap(10).Children(func() {
			if detail.Readme != "" {
				ui.Text(c, detail.Readme).FontSize(10.5).TextColor(pal.T.Text).LineHeight(1.45).Selectable()
			} else {
				ui.Text(c, i18n.T("该市场未返回 README。")).FontSize(10.5).TextColor(pal.T.TextMuted)
			}
			if len(detail.Files) > 0 {
				ui.Text(c, i18n.Tf("文件（%d）", len(detail.Files))).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				for _, file := range detail.Files {
					line := file.Path
					if file.Size > 0 {
						line += fmt.Sprintf(" · %d bytes", file.Size)
					}
					ui.Text(c, line).FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
				}
			}
			if text := PrettyJSON(detail.SecurityReports); text != "" {
				ui.Text(c, i18n.T("安全报告")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				ui.Text(c, text).Font("monospace").FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
			}
			if text := PrettyJSON(detail.Evaluation); text != "" {
				ui.Text(c, i18n.T("评估")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				ui.Text(c, text).Font("monospace").FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
			}
			if len(detail.DownloadSources) > 0 {
				ui.Text(c, i18n.T("下载来源")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				for _, source := range detail.DownloadSources {
					line := strings.TrimSpace(source.Kind)
					if line != "" {
						line += " · "
					}
					line += source.URL
					ui.Text(c, line).FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
				}
			}
		})
	})
	scope := p.TargetScope
	if state != nil && strings.TrimSpace(state.Scope) != "" {
		scope = state.Scope
	}
	ui.Text(c, i18n.Tf("安装目标：%s · %s", scope, p.TargetPath)).
		FontSize(9.5).TextColor(pal.T.TextMuted).Selectable()
	overwrite := ui.Checkbox(c, &p.Overwrite, i18n.T("覆盖已存在的技能版本")).FontSize(11)
	overwrite.Disabled(p.BrowseBusy())
	activate := ui.Checkbox(c, &p.ActivateOnInstall, i18n.T("安装后立即在当前会话激活")).FontSize(11)
	activate.Disabled(p.BrowseBusy())
}

func (p *Panel) DetailActions(h Host, c *ui.Context, pal kit.Palette, detail *Detail) {
	skill := detail.Skill
	state := detail.Installed
	busy := p.BrowseBusy()
	scope := p.TargetScope
	name := InstalledName(skill)
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
	install := ui.PrimaryButton(c.Key("skillhub-detail-install"), i18n.T(installLabel))
	install.Label(i18n.T(installLabel + "技能"))
	install.Disabled(busy || p.TargetPath == "")
	if install.Clicked() && !busy && p.TargetPath != "" {
		if state != nil && state.UpdateAvailable {
			p.InstallWithOverwrite(h, skill.Market, skill.ID, skill.Version, true)
		} else {
			p.Install(h, skill.Market, skill.ID, skill.Version)
		}
	}
	if state != nil {
		active := state.Active || Active(p.ActiveSkills, name)
		activate := ui.Button(c.Key("skillhub-detail-activate"), i18n.T("激活"))
		activate.Label(i18n.T("在当前会话激活技能 ") + name)
		activate.Disabled(busy || active || name == "")
		if activate.Clicked() && !busy && !active && name != "" {
			p.Activate(h, skill.Market, skill.ID, name)
		}
		remove := ui.Button(c.Key("skillhub-detail-uninstall"), i18n.T("卸载"))
		remove.Label(i18n.T("卸载技能 ") + name)
		remove.Disabled(busy || state.Local)
		if remove.Clicked() && !busy && !state.Local {
			p.RequestUninstall(h, skill.Market, skill.ID, scope, name)
		}
	}
}

func (p *Panel) UninstallDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.UninstallOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(460).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Danger)
				ui.Text(c, i18n.T("卸载技能？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.Tf("将从 %s 范围删除 %s（%s/%s）。该操作只删除市场安装的副本。",
				p.UninstallScope, p.UninstallName, p.UninstallMarket, p.UninstallID)).
				FontSize(11.5).TextColor(pal.T.TextMuted).LineHeight(1.4).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("skillhub-uninstall-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消卸载技能"))
				if cancel.Clicked() {
					p.CancelUninstall()
				}
				confirm := ui.Button(c.Key("skillhub-uninstall-confirm"), i18n.T("确认卸载"))
				confirm.Label(i18n.T("确认卸载技能"))
				confirm.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				confirm.Disabled(p.ActionBusy != "")
				if confirm.Clicked() && p.ActionBusy == "" {
					p.ConfirmUninstall(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CancelUninstall()
	}
}

func (p *Panel) CategoryRow(h Host, c *ui.Context, pal kit.Palette) {
	categories := FlattenCategories(p.Categories)
	officialMarket := strings.EqualFold(strings.TrimSpace(p.SelectedMarket), "skillhub.cn")
	if len(categories) == 0 && p.CategoryErr == "" && !p.CategoryLoading && !officialMarket {
		return
	}
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
			ui.Text(c, i18n.T("分类")).FontSize(10).FontWeight(600).TextColor(pal.T.TextMuted)
			p.CategoryChip(h, c, pal, "", "全部")
			for _, category := range categories {
				p.CategoryChip(h, c, pal, category.Key, CategoryName(category))
			}
			if strings.EqualFold(strings.TrimSpace(p.SelectedMarket), "skillhub.cn") {
				ui.Box(c).Width(8)
				label := "官方推荐"
				if p.Official {
					label = "官方推荐：开"
				}
				b := ui.ButtonBase(c.Key("skillhub-official")).Height(26).Padding(0, 10).Radius(999).
					AlignItems(ui.Center).Justify(ui.Center).Border(1, pal.T.Border)
				if p.Official {
					b.Background(pal.Active)
				} else if b.Hovered() {
					b.Background(pal.Hover)
				}
				b.Children(func() {
					color := pal.T.Text
					if p.Official {
						color = pal.T.Accent
					}
					ui.Text(c, i18n.T(label)).FontSize(10.5).TextColor(color)
				})
				b.Label(i18n.T("官方推荐技能"))
				if b.Clicked() && !p.BrowseBusy() {
					p.Official = !p.Official
					p.Search(h)
				}
			}
		})
		if p.CategoryLoading {
			ui.Text(c, i18n.T("正在读取技能分类…")).FontSize(10).TextColor(pal.T.TextMuted)
		}
		if p.CategoryErr != "" {
			ui.Text(c, i18n.T(p.CategoryErr)).FontSize(10).TextColor(pal.T.Danger).Selectable()
		}
	})
}

func (p *Panel) CategoryChip(h Host, c *ui.Context, pal kit.Palette, key, label string) {
	active := p.Category == key
	b := ui.ButtonBase(c.Key("skillhub-category:"+key)).Height(26).Padding(0, 10).Radius(999).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, pal.T.Border)
	if active {
		b.Background(pal.Active)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() {
		color := pal.T.Text
		if active {
			color = pal.T.Accent
		}
		ui.Text(c, i18n.T(label)).FontSize(10.5).TextColor(color).SingleLine()
	})
	b.Label(i18n.T(label))
	if b.Clicked() && !active && !p.BrowseBusy() {
		p.Category = key
		p.Official = false
		p.Search(h)
	}
}

// SortRow mirrors Desktop's market sort switch. clawhub.ai does not
// support server-side sorting, so the row stays hidden there.
func (p *Panel) SortRow(h Host, c *ui.Context, pal kit.Palette) {
	if strings.EqualFold(strings.TrimSpace(p.SelectedMarket), "clawhub.ai") {
		return
	}
	ui.Row(c).AlignItems(ui.Center).Gap(6).Wrap().Children(func() {
		ui.Text(c, i18n.T("排序")).FontSize(10).FontWeight(600).TextColor(pal.T.TextMuted)
		for _, option := range []struct{ ID, Label string }{
			{"downloads", "下载"},
			{"stars", "Star"},
			{"updated_at", "最新"},
		} {
			option := option
			p.SortChip(h, c, pal, option.ID, option.Label)
		}
	})
}

func (p *Panel) SortChip(h Host, c *ui.Context, pal kit.Palette, sort, label string) {
	current := p.Sort
	if current == "" {
		current = DefaultSort
	}
	active := current == sort
	b := ui.ButtonBase(c.Key("skillhub-sort:"+sort)).Height(26).Padding(0, 10).Radius(999).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, pal.T.Border)
	if active {
		b.Background(pal.Active)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() {
		color := pal.T.Text
		if active {
			color = pal.T.Accent
		}
		ui.Text(c, i18n.T(label)).FontSize(10.5).TextColor(color).SingleLine()
	})
	b.Label(i18n.T(label))
	if b.Clicked() && !active && !p.BrowseBusy() {
		p.Sort = sort
		p.Official = false
		p.Search(h)
	}
}
