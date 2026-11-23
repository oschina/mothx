package providers

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the real provider manager inside the Native
// three-pane Settings view. The full catalog/editor requires manageProviders
// plus manageProviderConfig; when the latter is absent the panel degrades to
// the legitimate settings/providers-list operations (defaults, set default,
// test) without exposing unavailable editors or fake settings controls.
func (p *Panel) View(h host.Base, c *ui.Context, pal kit.Palette) {
	caps := p.Capabilities(h)
	if !caps.Catalog && !caps.Settings {
		kit.SettingsCard(c, pal, "供应商", "当前运行时未公开供应商管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageProviders 或 manageSettings。")).FontSize(11).TextColor(pal.T.TextMuted)
		})
		return
	}

	if p.Loading && p.Catalog == nil && p.Settings == nil {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取供应商目录…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}

	if p.Err != "" {
		p.errCard(h, c, pal, p.Err)
	}
	if p.Status != "" {
		p.statusBanner(c, pal)
	}
	if p.Loading && (p.Catalog != nil || p.Settings != nil) {
		ui.Text(c, i18n.T("正在刷新供应商目录…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	if caps.Settings {
		p.defaultsCard(h, c, pal)
	}

	if !caps.Config {
		p.fallbackPane(h, c, pal)
		return
	}

	if p.Catalog == nil {
		if p.Err == "" {
			kit.SettingsCard(c, pal, "供应商目录", "尚未载入供应商目录。", func() {
				ui.Text(c, i18n.T("正在等待 ACP 返回供应商目录。")).FontSize(11).TextColor(pal.T.TextMuted)
			})
		}
		return
	}

	ui.Row(c).AlignItems(ui.Start).Gap(12).Children(func() {
		p.catalogColumn(c, pal)
		p.editorColumn(h, c, pal)
	})
}

func (p *Panel) errCard(h host.Base, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("供应商管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capabilities(h).Catalog {
			if ui.Button(c.Key("provider-retry"), i18n.T("重试")).Label(i18n.T("重试加载供应商")).Clicked() {
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
		} else if p.Saving || p.DefaultsSaving || p.Deleting || p.Testing || p.Discovering {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) defaultsCard(h host.Base, c *ui.Context, pal kit.Palette) {
	kit.SettingsCard(c, pal, "默认供应商与模型", "影响新会话、运行时模型选择以及默认思考级别。", func() {
		if p.SettingsErr != "" {
			ui.Text(c, i18n.T(p.SettingsErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
		}
		if p.Settings == nil {
			ui.Text(c, i18n.T("正在读取默认设置…")).FontSize(11).TextColor(pal.T.TextMuted)
			return
		}
		providers := p.ConfigIDs()
		if len(providers) == 0 {
			ui.Text(c, i18n.T("尚未配置任何供应商，无法保存默认值。")).FontSize(11).TextColor(pal.T.TextMuted)
			return
		}
		previousProvider := p.DefaultsProvider
		ui.Column(c).Gap(10).Children(func() {
			p.labeledSelect(c, pal, "provider-default-provider", "默认供应商", providers, &p.DefaultsProvider)
			if p.DefaultsProvider != previousProvider {
				p.DefaultsModel = DefaultModelFor(p.Catalog, p.DefaultsProvider, "")
			}

			models := ModelIDsFor(p.Catalog, p.DefaultsProvider)
			if len(models) > 0 {
				effective := DefaultModelFor(p.Catalog, p.DefaultsProvider, p.DefaultsModel)
				p.DefaultsModel = effective
				p.labeledSelect(c, pal, "provider-default-model", "默认模型", models, &p.DefaultsModel)
			} else {
				p.DefaultsModel = ""
				ui.Text(c, i18n.T("所选供应商在模型目录中没有可用模型。")).FontSize(10.5).TextColor(pal.T.Warning)
			}
			p.labeledSelect(c, pal, "provider-default-thinking", "思考级别", providerThinkingLevels, &p.DefaultsThinking)

			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				btn := ui.PrimaryButton(c.Key("provider-default-save"), i18n.T("保存默认值"))
				btn.Disabled(p.DefaultsSaving || p.DefaultsModel == "" || len(providers) == 0)
				btn.Label(i18n.T("保存默认供应商、模型与思考级别")).Tooltip(i18n.T("写入全局 settings.json"))
				if btn.Clicked() && !p.DefaultsSaving {
					p.SaveDefaults(h)
				}
			})
		})
	})
}

func (p *Panel) labeledSelect(c *ui.Context, pal kit.Palette, key, label string, options []string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		if len(options) == 0 {
			ui.Text(c, i18n.T("无可选项")).FontSize(11).TextColor(pal.T.TextMuted)
			return
		}
		if value != nil && *value != "" {
			options = SelectOptions(*value, options)
		}
		ui.Select(c.Key(key), value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(label)
	})
}

func (p *Panel) fallbackPane(h host.Base, c *ui.Context, pal kit.Palette) {
	caps := p.Capabilities(h)
	summaries := SummaryList(&p.State, caps.Settings)
	kit.SettingsCard(c, pal, "供应商目录", "当前运行时未公开 manageProviderConfig；这里只显示当前能力可支持的操作。", func() {
		if len(summaries) == 0 {
			ui.Text(c, i18n.T("尚未配置供应商。")).FontSize(11).TextColor(pal.T.TextMuted)
			return
		}
		ui.Column(c).Gap(6).Children(func() {
			for _, provider := range summaries {
				provider := provider
				ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(8, 10).Radius(9).
					Background(pal.T.Background).Border(1, pal.T.Border).Children(func() {
					ui.Icon(c, icons.IconCpu).FontSize(15).TextColor(pal.T.Accent)
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
							ui.Text(c, provider.Name).FontSize(12).FontWeight(600).TextColor(pal.T.Text).SingleLine()
							if provider.IsDefault {
								kit.Badge(c, pal, "默认")
							}
							if provider.APIKeyConfigured {
								kit.Badge(c, pal, "已配置")
							}
						})
						desc := FallbackDescription(provider)
						ui.Text(c, desc).FontSize(10).TextColor(pal.T.TextMuted).SingleLine()
					})
					if caps.Catalog {
						test := ui.Button(c.Key("provider-fallback-test:"+provider.Name), i18n.T("测试连接"))
						test.Label(i18n.T("测试供应商 ") + provider.Name + i18n.T(" 的连接")).Disabled(p.Testing)
						if test.Clicked() {
							p.TestByName(h, provider.Name)
						}
					}
					if caps.Settings {
						setKey := ui.Button(c.Key("provider-fallback-key:"+provider.Name), i18n.T("设置密钥"))
						setKey.Label(i18n.Tf("设置供应商密钥：%s", provider.Name))
						if setKey.Clicked() {
							p.OpenFallbackKey(provider.Name)
						}
					}
					if caps.Settings && !provider.IsDefault {
						setDefault := ui.Button(c.Key("provider-fallback-default:"+provider.Name), i18n.T("设为默认"))
						setDefault.Label(i18n.T("将 ") + provider.Name + i18n.T(" 设为默认供应商"))
						if setDefault.Clicked() {
							p.SetDefault(h, provider.Name)
						}
					}
				})
			}
		})
	})
}

func FallbackDescription(provider Summary) string {
	masked := MaskedKey(provider.MaskedKey)
	if masked == "" {
		masked = i18n.T("未配置密钥")
	}
	parts := []string{masked}
	if provider.BaseURL != "" {
		parts = append(parts, provider.BaseURL)
	}
	if provider.ModelCount > 0 {
		parts = append(parts, i18n.Tf("%d 个模型", provider.ModelCount))
	}
	return strings.Join(parts, " · ")
}

func (p *Panel) catalogColumn(c *ui.Context, pal kit.Palette) {
	ui.Column(c).WidthPercent(42).MaxWidth(260).Padding(10).Radius(12).Background(pal.T.Surface).
		Border(1, pal.T.Border).Gap(8).Children(func() {
		ui.Text(c, i18n.T("供应商目录")).FontSize(12).FontWeight(650).TextColor(pal.T.Text)
		ui.TextInput(c.Key("provider-catalog-search"), &p.Search).
			Placeholder(i18n.T("搜索供应商…")).
			FontSize(11.5).Height(30).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
			Label(i18n.T("搜索供应商"))
		ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
			p.scopeButton(c, pal, "provider-scope-configured", "已配置", ScopeConfigured)
			p.scopeButton(c, pal, "provider-scope-all", "全部", ScopeAll)
		})
		configs := p.FilteredConfigs()
		if len(configs) == 0 {
			ui.Column(c).Padding(12).Radius(8).Border(1, pal.T.Border).Gap(4).Children(func() {
				ui.Text(c, i18n.T("没有匹配的供应商")).FontSize(11).TextColor(pal.T.TextMuted)
				ui.Text(c, i18n.T("切换范围或新建供应商。")).FontSize(10).TextColor(pal.T.TextMuted)
			})
		} else {
			ui.Scroll(c).Height(360).Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					for _, config := range configs {
						config := config
						p.catalogRow(c, pal, config)
					}
				})
			})
		}
		add := ui.Button(c.Key("provider-add"), i18n.T("新建供应商"))
		add.Label(i18n.T("新建供应商")).Tooltip(i18n.T("在编辑器中创建新的供应商配置"))
		add.Disabled(p.Saving || p.Deleting)
		if add.Clicked() {
			p.StartNewDraft()
		}
	})
}

func (p *Panel) scopeButton(c *ui.Context, pal kit.Palette, key, label, scope string) {
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
	b.Label(i18n.T(label)).Tooltip(i18n.Tf("供应商范围：%s", i18n.T(label)))
	if b.Clicked() {
		p.Scope = scope
	}
}

func (p *Panel) catalogRow(c *ui.Context, pal kit.Palette, config ConfigView) {
	active := config.ID == p.ActiveID
	b := ui.ButtonBase(c.Key("provider-catalog:"+config.ID)).Padding(6, 7).Radius(8).
		AlignItems(ui.Center).Gap(7).Border(1, pal.T.Border)
	if active {
		b.Background(pal.Active).Border(1, pal.T.Accent)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	} else {
		b.Background(pal.T.Background)
	}
	b.Children(func() {
		color := pal.T.TextMuted
		if active {
			color = pal.T.Accent
		}
		ui.Icon(c, icons.IconCpu).FontSize(14).TextColor(color)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
				ui.Text(c, config.ID).FontSize(11.5).FontWeight(650).TextColor(pal.T.Text).SingleLine().Grow(1)
				if config.IsDefault {
					kit.Badge(c, pal, "默认")
				}
			})
			desc := ConfigDescription(config)
			ui.Text(c, desc).FontSize(9.5).TextColor(pal.T.TextMuted).SingleLine()
		})
	})
	b.Label(i18n.T("编辑供应商 ") + config.ID).Tooltip(config.Provider.BaseURL)
	if b.Clicked() {
		p.SelectDraft(config.ID, true)
	}
}

func ConfigDescription(config ConfigView) string {
	masked := MaskedKey(config.MaskedKey)
	if masked == "" {
		masked = i18n.T("未配置密钥")
	}
	parts := []string{masked}
	if config.Provider.BaseURL != "" {
		parts = append(parts, config.Provider.BaseURL)
	}
	parts = append(parts, i18n.Tf("%d 个模型", len(config.Provider.Models)))
	return strings.Join(parts, " · ")
}

func (p *Panel) editorColumn(h host.Base, c *ui.Context, pal kit.Palette) {
	ui.Column(c).Grow(1).Padding(12).Radius(12).Background(pal.T.Surface).
		Border(1, pal.T.Border).Gap(10).Children(func() {
		if p.Draft == nil {
			ui.Column(c).Padding(18).Center().Gap(4).Children(func() {
				ui.Text(c, i18n.T("选择一个供应商开始编辑")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
				ui.Text(c, i18n.T("或点击左侧“新建供应商”。")).FontSize(10.5).TextColor(pal.T.TextMuted)
			})
			return
		}
		p.editorHeader(h, c, pal)
		tab := p.EditorTab
		ui.Tabs(c, &tab, i18n.T("连接"), i18n.T("模型"), i18n.T("高级"))
		p.EditorTab = tab
		switch tab {
		case EditorModels:
			p.modelsTab(h, c, pal)
		case providerEditorAdvanced:
			p.advancedTab(c, pal)
		default:
			p.connectionTab(c, pal)
		}
	})
}

func (p *Panel) editorHeader(h host.Base, c *ui.Context, pal kit.Palette) {
	draft := p.Draft
	isNew := p.OriginalID == "" && ConfigByID(p.Catalog, draft.ID) == nil
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Icon(c, icons.IconCpu).FontSize(17).TextColor(pal.T.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, draft.ID).FontSize(14).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					if draft.IsDefault {
						kit.Badge(c, pal, "默认")
					}
					if isNew {
						kit.Badge(c, pal, "新建")
					}
				})
				sub := strings.TrimSpace(strings.Join([]string{draft.Vendor, draft.BaseURL}, " · "))
				if sub == "" {
					sub = "尚未填写连接信息"
				}
				ui.Text(c, i18n.T(sub)).FontSize(10).TextColor(pal.T.TextMuted).SingleLine()
			})
		})
		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			test := ui.Button(c.Key("provider-test"), i18n.T("测试连接"))
			test.Label(i18n.T("测试供应商 ") + draft.ID + i18n.T(" 的连接")).Tooltip(i18n.T("发送一次最小请求，不会显示密钥"))
			test.Disabled(isNew || p.Testing || p.Saving || p.Deleting)
			if test.Clicked() {
				p.TestDraft(h)
			}
			save := ui.PrimaryButton(c.Key("provider-save"), i18n.T("保存供应商"))
			save.Label(i18n.T("保存供应商 ") + draft.ID)
			save.Disabled(p.Saving || p.Testing || p.Deleting)
			if save.Clicked() {
				p.Save(h)
			}
		})
	})
}

func (p *Panel) connectionTab(c *ui.Context, pal kit.Palette) {
	draft := p.Draft
	isNew := p.OriginalID == "" && ConfigByID(p.Catalog, draft.ID) == nil
	canRename := isNew || draft.GlobalOverride
	ui.Column(c).Gap(10).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, i18n.T("供应商 ID")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
			id := ui.TextInput(c.Key("provider-id"), &draft.ID).FontSize(12).Height(32).Padding(0, 8).
				Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("供应商 ID"))
			id.Disabled(!canRename)
			if !canRename {
				ui.Text(c, i18n.T("内置预置 ID 不可重命名；保存后会创建全局覆盖配置。")).FontSize(9.5).TextColor(pal.T.TextMuted)
			}
		})
		p.labeledInput(c, pal, "provider-vendor", "供应商 Vendor", &draft.Vendor)
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, i18n.T("API 类型")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
			options := SelectOptions(strings.TrimSpace(draft.API), providerAPIChoices)
			if len(options) == 0 {
				options = []string{"openai-chat"}
			}
			ui.Select(c.Key("provider-api"), &draft.API, options).FontSize(12).Height(32).Padding(0, 8).
				Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("API 类型"))
		})
		p.labeledInput(c, pal, "provider-base-url", "Base URL", &draft.BaseURL)
		ui.Column(c).Gap(3).Children(func() {
			ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
				ui.Text(c, i18n.T("API 密钥")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				state := "尚未配置"
				if draft.APIKeyConfigured {
					state = "已配置"
				}
				if draft.MaskedKey != "" {
					state += " · " + draft.MaskedKey
				}
				ui.Text(c, "["+state+"]").FontSize(9.5).TextColor(pal.T.TextMuted)
			})
			placeholder := i18n.T("输入新密钥；留空保持不变")
			if draft.MaskedKey != "" {
				placeholder = i18n.Tf("留空保持不变（当前 %s）", draft.MaskedKey)
			}
			key := ui.TextInput(c.Key("provider-api-key"), &p.Secret.APIKey).Password().
				FontSize(12).Height(32).Padding(0, 8).Placeholder(placeholder).
				Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T("API 密钥（只写）"))
			key.OnChange(func() {
				if strings.TrimSpace(p.Secret.APIKey) != "" {
					p.Secret.ClearKey = false
				}
			})
			ui.Text(c, i18n.T("密钥只写入服务端，不会回显、记录或保存在 Native 状态输出中。")).FontSize(9.5).TextColor(pal.T.TextMuted)
		})
		p.labeledInput(c, pal, "provider-http-proxy", "HTTP 代理", &draft.HTTPProxy)
		ui.Checkbox(c, &draft.ForceHTTP11, i18n.T("强制 HTTP/1.1")).FontSize(11.5)
	})
}

func (p *Panel) labeledInput(c *ui.Context, pal kit.Palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(label)
	})
}

func (p *Panel) modelsTab(h host.Base, c *ui.Context, pal kit.Palette) {
	draft := p.Draft
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, i18n.T("模型目录")).FontSize(12).FontWeight(650).TextColor(pal.T.Text).Grow(1)
		add := ui.Button(c.Key("provider-add-model"), i18n.T("添加模型"))
		add.Label(i18n.T("添加模型"))
		if add.Clicked() {
			draft.Models = append(draft.Models, ModelDefaultsFor(p.Catalog))
		}
		discover := ui.Button(c.Key("provider-discover"), i18n.T("发现模型"))
		discover.Label(i18n.T("从供应商发现模型")).Tooltip(i18n.T("调用 providers/discover，结果需确认后才会加入"))
		discover.Disabled(p.Discovering || p.Saving)
		if discover.Clicked() {
			p.DiscoverModels(h)
		}
	})
	if len(draft.Models) == 0 {
		ui.Text(c, i18n.T("该供应商还没有模型。添加模型或使用“发现模型”。")).FontSize(11).TextColor(pal.T.TextMuted)
		return
	}
	ui.Column(c).Gap(8).Children(func() {
		for i := range draft.Models {
			p.modelRow(c, pal, i)
		}
	})
}

func (p *Panel) modelRow(c *ui.Context, pal kit.Palette, index int) {
	draft := p.Draft
	if draft == nil || index < 0 || index >= len(draft.Models) {
		return
	}
	model := &draft.Models[index]
	ui.Column(c).Padding(9).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, i18n.Tf("模型 %d", index+1)).FontSize(10.5).FontWeight(650).TextColor(pal.T.TextMuted).Grow(1)
			remove := ui.Button(c.Key(fmt.Sprintf("provider-remove-model:%d", index)), i18n.T("移除模型"))
			remove.Label(i18n.T("移除模型 ") + model.ID)
			if remove.Clicked() {
				draft.Models = append(draft.Models[:index], draft.Models[index+1:]...)
			}
		})
		p.labeledInput(c, pal, fmt.Sprintf("provider-model-id:%d", index), "模型 ID", &model.ID)
		p.labeledInput(c, pal, fmt.Sprintf("provider-model-name:%d", index), "模型名称", &model.Name)
		p.labeledInput(c, pal, fmt.Sprintf("provider-model-context:%d", index), "上下文窗口", &model.ContextWindow)
		p.labeledInput(c, pal, fmt.Sprintf("provider-model-max:%d", index), "最大 Tokens", &model.MaxTokens)
		p.labeledInput(c, pal, fmt.Sprintf("provider-model-input:%d", index), "输入类型（逗号分隔）", &model.Input)
		ui.Checkbox(c, &model.Reasoning, i18n.T("支持推理")).FontSize(11.5)
	})
}

func (p *Panel) advancedTab(c *ui.Context, pal kit.Palette) {
	draft := p.Draft
	ui.Column(c).Gap(10).Children(func() {
		if draft.GlobalOverride {
			ui.Column(c).Padding(9).Radius(9).Background(pal.ErrorBg).
				Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(7).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, icons.IconAlert).FontSize(14).TextColor(pal.T.Danger)
					ui.Text(c, i18n.T("危险区域")).FontSize(11.5).FontWeight(650).TextColor(pal.T.Danger).Grow(1)
					remove := ui.Button(c.Key("provider-remove-override"), i18n.T("重置 / 移除"))
					remove.Label(i18n.T("重置或移除供应商 ") + draft.ID)
					remove.Disabled(draft.IsDefault || p.Deleting)
					if remove.Clicked() {
						p.RequestDelete()
					}
				})
				if draft.IsDefault {
					ui.Text(c, i18n.T("当前默认供应商不能移除；请先在“默认供应商与模型”中切换默认供应商。")).FontSize(10).TextColor(pal.T.TextMuted)
				} else {
					ui.Text(c, i18n.T("预置供应商会恢复内置配置，自定义供应商会被删除。")).FontSize(10).TextColor(pal.T.TextMuted)
				}
			})
		} else {
			ui.Text(c, i18n.T("该供应商使用内置预置配置；保存后会创建全局覆盖。")).FontSize(10.5).TextColor(pal.T.TextMuted)
		}
		p.labeledInput(c, pal, "provider-thinking-format", "思考格式", &draft.ThinkingFormat)
		p.labeledInput(c, pal, "provider-max-images", "每次请求最大图片数（-1 表示不限）", &draft.MaxImagesPerRequest)
		ui.Checkbox(c, &draft.CacheControl, i18n.T("启用提示缓存（cacheControl）")).FontSize(11.5)
		ui.Column(c).Gap(2).Children(func() {
			clear := ui.Checkbox(c, &p.Secret.ClearKey, i18n.T("保存时清除已配置的 API 密钥")).FontSize(11.5)
			clear.OnChange(func() {
				if p.Secret.ClearKey {
					p.Secret.APIKey = ""
				}
			})
			ui.Text(c, i18n.T("仅在保存时提交空密钥以清除；普通保存不会发送密钥。")).FontSize(9.5).TextColor(pal.T.TextMuted)
		})
	})
}

// dialogs renders the Native confirmation and discovery
// dialogs above the settings page. Neither is a browser/native prompt.
func (p *Panel) Dialogs(h host.Base, c *ui.Context, pal kit.Palette) {
	if !h.SettingsOpen() {
		return
	}
	if p.PendingDelete {
		p.deleteDialog(h, c, pal)
	}
	if p.DiscoverOpen {
		p.discoverDialog(c, pal)
	}
	if p.FallbackKeyOpen {
		p.fallbackKeyDialog(h, c, pal)
	}
}

func (p *Panel) fallbackKeyDialog(h host.Base, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.FallbackKeyOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconCpu).FontSize(16).TextColor(pal.T.Accent)
				ui.Text(c, i18n.Tf("设置供应商密钥：%s", p.FallbackKeyName)).
					FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1).SingleLine()
			})
			ui.Text(c, i18n.T("密钥只写入服务端，不会回显。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			key := ui.TextInput(c.Key("provider-fallback-key-input"), &p.FallbackKeyValue).
				Password().FontSize(12).Height(32).Padding(0, 8).
				Placeholder(i18n.T("输入新密钥；留空保持不变")).
				Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
				Label(i18n.T("API 密钥（只写）"))
			key.Disabled(p.FallbackKeySaving)
			if p.FallbackKeyErr != "" {
				ui.Text(c, i18n.T(p.FallbackKeyErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("provider-fallback-key-cancel"), i18n.T("取消"))
				cancel.Disabled(p.FallbackKeySaving)
				if cancel.Clicked() && !p.FallbackKeySaving {
					p.CloseFallbackKey()
				}
				ui.Spacer(c)
				save := ui.PrimaryButton(c.Key("provider-fallback-key-save"), i18n.T("保存"))
				save.Disabled(p.FallbackKeySaving)
				if save.Clicked() && !p.FallbackKeySaving {
					p.SaveFallbackKey(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CloseFallbackKey()
	}
}

func (p *Panel) deleteDialog(h host.Base, c *ui.Context, pal kit.Palette) {
	draft := p.Draft
	dialog := ui.DialogBase(c, &p.PendingDelete, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Danger)
				ui.Text(c, i18n.T("重置或移除供应商配置？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			id := ""
			if draft != nil {
				id = draft.ID
			}
			ui.Text(c, i18n.Tf("将移除 %q 的全局覆盖。预置供应商会恢复内置配置，自定义供应商会从目录中删除。", id)).
				FontSize(11.5).TextColor(pal.T.TextMuted).Selectable()
			ui.Text(c, i18n.T("此操作不会发送或显示任何 API 密钥。")).FontSize(10).TextColor(pal.T.TextMuted)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("provider-delete-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消重置或移除供应商"))
				if cancel.Clicked() {
					p.CancelDelete()
				}
				confirm := ui.Button(c.Key("provider-delete-confirm"), i18n.T("确认移除"))
				confirm.Label(i18n.T("确认重置或移除供应商"))
				confirm.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				confirm.Disabled(draft == nil || draft.IsDefault)
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

func (p *Panel) discoverDialog(c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.DiscoverOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(560).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconSpark).FontSize(16).TextColor(pal.T.Accent)
				ui.Text(c, i18n.T("发现模型")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.T("从当前供应商获取可用模型。选择后会加入编辑器草稿，仍需保存供应商才会生效。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			ui.TextInput(c.Key("provider-discover-search"), &p.DiscoverSearch).
				Placeholder(i18n.T("搜索发现的模型…")).FontSize(11.5).Height(30).Padding(0, 8).
				Background(pal.T.Surface).Radius(8).Border(1, pal.T.Border).Label(i18n.T("搜索发现的模型"))
			query := strings.ToLower(strings.TrimSpace(p.DiscoverSearch))
			shown := make([]Model, 0, len(p.DiscoverCandidates))
			for _, candidate := range p.DiscoverCandidates {
				if query != "" && !strings.Contains(strings.ToLower(candidate.ID+" "+candidate.Name), query) {
					continue
				}
				shown = append(shown, candidate)
			}
			if len(shown) == 0 {
				ui.Text(c, i18n.T("没有匹配的模型。")).FontSize(11).TextColor(pal.T.TextMuted)
			} else {
				ui.Scroll(c).Height(300).Children(func() {
					ui.Column(c).Gap(4).Children(func() {
						for _, candidate := range shown {
							candidate := candidate
							selected := p.DiscoverSelected[candidate.ID]
							if selected == nil {
								value := false
								selected = &value
							}
							label := candidate.ID
							if candidate.Name != "" && candidate.Name != candidate.ID {
								label += " · " + candidate.Name
							}
							ui.Checkbox(c, selected, label).FontSize(11.5)
						}
					})
				})
			}
			selectedCount := 0
			for _, selected := range p.DiscoverSelected {
				if selected != nil && *selected {
					selectedCount++
				}
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				ui.Text(c, i18n.Tf("已选择 %d 个", selectedCount)).FontSize(10.5).TextColor(pal.T.TextMuted).Grow(1)
				cancel := ui.Button(c.Key("provider-discover-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消添加发现的模型"))
				if cancel.Clicked() {
					p.CloseDiscover()
				}
				add := ui.PrimaryButton(c.Key("provider-discover-add"), i18n.T("添加选中模型"))
				add.Label(i18n.T("添加选中的发现模型"))
				add.Disabled(selectedCount == 0)
				if add.Clicked() && selectedCount > 0 {
					p.AddDiscoveredModels()
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CloseDiscover()
	}
}
