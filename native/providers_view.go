package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsProvidersPane renders the real provider manager inside the Native
// three-pane Settings view. The full catalog/editor requires manageProviders
// plus manageProviderConfig; when the latter is absent the panel degrades to
// the legitimate settings/providers-list operations (defaults, set default,
// test) without exposing unavailable editors or fake settings controls.
func (a *nativeApp) settingsProvidersPane(c *ui.Context, p palette) {
	caps := a.providerCapabilities()
	if !caps.Catalog && !caps.Settings {
		a.settingsCard(c, p, "供应商", "当前运行时未公开供应商管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageProviders 或 manageSettings。")).FontSize(11).TextColor(p.t.TextMuted)
		})
		return
	}

	panel := &a.providers
	if panel.Loading && panel.Catalog == nil && panel.Settings == nil {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取供应商目录…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.providerErrorCard(c, p, panel.Err)
	}
	if panel.Status != "" {
		a.providerStatusBanner(c, p)
	}
	if panel.Loading && (panel.Catalog != nil || panel.Settings != nil) {
		ui.Text(c, T("正在刷新供应商目录…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	if caps.Settings {
		a.providerDefaultsCard(c, p)
	}

	if !caps.Config {
		a.providerFallbackPane(c, p)
		return
	}

	if panel.Catalog == nil {
		if panel.Err == "" {
			a.settingsCard(c, p, "供应商目录", "尚未载入供应商目录。", func() {
				ui.Text(c, T("正在等待 ACP 返回供应商目录。")).FontSize(11).TextColor(p.t.TextMuted)
			})
		}
		return
	}

	ui.Row(c).AlignItems(ui.Start).Gap(12).Children(func() {
		a.providerCatalogColumn(c, p)
		a.providerEditorColumn(c, p)
	})
}

func (a *nativeApp) providerErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("供应商管理不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.providerCapabilities().Catalog {
			if ui.Button(c.Key("provider-retry"), T("重试")).Label(T("重试加载供应商")).Clicked() {
				a.providers.Loaded = false
				a.startProviderPanelLoad()
			}
		}
	})
}

func (a *nativeApp) providerStatusBanner(c *ui.Context, p palette) {
	panel := &a.providers
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
		} else if panel.Saving || panel.DefaultsSaving || panel.Deleting || panel.Testing || panel.Discovering {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(panel.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (a *nativeApp) providerDefaultsCard(c *ui.Context, p palette) {
	panel := &a.providers
	a.settingsCard(c, p, "默认供应商与模型", "影响新会话、运行时模型选择以及默认思考级别。", func() {
		if panel.SettingsErr != "" {
			ui.Text(c, T(panel.SettingsErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
		}
		if panel.Settings == nil {
			ui.Text(c, T("正在读取默认设置…")).FontSize(11).TextColor(p.t.TextMuted)
			return
		}
		providers := a.providerConfigIDs()
		if len(providers) == 0 {
			ui.Text(c, T("尚未配置任何供应商，无法保存默认值。")).FontSize(11).TextColor(p.t.TextMuted)
			return
		}
		previousProvider := panel.DefaultsProvider
		ui.Column(c).Gap(10).Children(func() {
			a.providerLabeledSelect(c, p, "provider-default-provider", "默认供应商", providers, &panel.DefaultsProvider)
			if panel.DefaultsProvider != previousProvider {
				panel.DefaultsModel = providerDefaultModelFor(panel.Catalog, panel.DefaultsProvider, "")
			}

			models := providerModelIDsFor(panel.Catalog, panel.DefaultsProvider)
			if len(models) > 0 {
				effective := providerDefaultModelFor(panel.Catalog, panel.DefaultsProvider, panel.DefaultsModel)
				panel.DefaultsModel = effective
				a.providerLabeledSelect(c, p, "provider-default-model", "默认模型", models, &panel.DefaultsModel)
			} else {
				panel.DefaultsModel = ""
				ui.Text(c, T("所选供应商在模型目录中没有可用模型。")).FontSize(10.5).TextColor(p.t.Warning)
			}
			a.providerLabeledSelect(c, p, "provider-default-thinking", "思考级别", providerThinkingLevels, &panel.DefaultsThinking)

			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				btn := ui.PrimaryButton(c.Key("provider-default-save"), T("保存默认值"))
				btn.Disabled(panel.DefaultsSaving || panel.DefaultsModel == "" || len(providers) == 0)
				btn.Label(T("保存默认供应商、模型与思考级别")).Tooltip(T("写入全局 settings.json"))
				if btn.Clicked() && !panel.DefaultsSaving {
					a.saveProviderDefaults()
				}
			})
		})
	})
}

func (a *nativeApp) providerLabeledSelect(c *ui.Context, p palette, key, label string, options []string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		if len(options) == 0 {
			ui.Text(c, T("无可选项")).FontSize(11).TextColor(p.t.TextMuted)
			return
		}
		if value != nil && *value != "" {
			options = providerSelectOptions(*value, options)
		}
		ui.Select(c.Key(key), value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(label)
	})
}

func (a *nativeApp) providerFallbackPane(c *ui.Context, p palette) {
	panel := &a.providers
	caps := a.providerCapabilities()
	summaries := providerSummaryList(panel, caps.Settings)
	a.settingsCard(c, p, "供应商目录", "当前运行时未公开 manageProviderConfig；这里只显示当前能力可支持的操作。", func() {
		if len(summaries) == 0 {
			ui.Text(c, T("尚未配置供应商。")).FontSize(11).TextColor(p.t.TextMuted)
			return
		}
		ui.Column(c).Gap(6).Children(func() {
			for _, provider := range summaries {
				provider := provider
				ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(8, 10).Radius(9).
					Background(p.t.Background).Border(1, p.t.Border).Children(func() {
					ui.Icon(c, iconCpu).FontSize(15).TextColor(p.t.Accent)
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
							ui.Text(c, provider.Name).FontSize(12).FontWeight(600).TextColor(p.t.Text).SingleLine()
							if provider.IsDefault {
								badge(c, p, "默认")
							}
							if provider.APIKeyConfigured {
								badge(c, p, "已配置")
							}
						})
						desc := providerFallbackDescription(provider)
						ui.Text(c, desc).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
					})
					if caps.Catalog {
						test := ui.Button(c.Key("provider-fallback-test:"+provider.Name), T("测试连接"))
						test.Label(T("测试供应商 ") + provider.Name + T(" 的连接")).Disabled(panel.Testing)
						if test.Clicked() {
							a.testProviderByName(provider.Name)
						}
					}
					if caps.Settings && !provider.IsDefault {
						setDefault := ui.Button(c.Key("provider-fallback-default:"+provider.Name), T("设为默认"))
						setDefault.Label(T("将 ") + provider.Name + T(" 设为默认供应商"))
						if setDefault.Clicked() {
							a.setDefaultProvider(provider.Name)
						}
					}
				})
			}
		})
	})
}

func providerFallbackDescription(provider providerSummary) string {
	masked := providerMaskedKey(provider.MaskedKey)
	if masked == "" {
		masked = T("未配置密钥")
	}
	parts := []string{masked}
	if provider.BaseURL != "" {
		parts = append(parts, provider.BaseURL)
	}
	if provider.ModelCount > 0 {
		parts = append(parts, Tf("%d 个模型", provider.ModelCount))
	}
	return strings.Join(parts, " · ")
}

func (a *nativeApp) providerCatalogColumn(c *ui.Context, p palette) {
	panel := &a.providers
	ui.Column(c).WidthPercent(42).MaxWidth(260).Padding(10).Radius(12).Background(p.t.Surface).
		Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Text(c, T("供应商目录")).FontSize(12).FontWeight(650).TextColor(p.t.Text)
		ui.TextInput(c.Key("provider-catalog-search"), &panel.Search).
			Placeholder(T("搜索供应商…")).
			FontSize(11.5).Height(30).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).
			Label(T("搜索供应商"))
		ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
			a.providerScopeButton(c, p, "provider-scope-configured", "已配置", providerScopeConfigured)
			a.providerScopeButton(c, p, "provider-scope-all", "全部", providerScopeAll)
		})
		configs := a.providerFilteredConfigs()
		if len(configs) == 0 {
			ui.Column(c).Padding(12).Radius(8).Border(1, p.t.Border).Gap(4).Children(func() {
				ui.Text(c, T("没有匹配的供应商")).FontSize(11).TextColor(p.t.TextMuted)
				ui.Text(c, T("切换范围或新建供应商。")).FontSize(10).TextColor(p.t.TextMuted)
			})
		} else {
			ui.Scroll(c).Height(360).Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					for _, config := range configs {
						config := config
						a.providerCatalogRow(c, p, config)
					}
				})
			})
		}
		add := ui.Button(c.Key("provider-add"), T("新建供应商"))
		add.Label(T("新建供应商")).Tooltip(T("在编辑器中创建新的供应商配置"))
		add.Disabled(panel.Saving || panel.Deleting)
		if add.Clicked() {
			a.startNewProviderDraft()
		}
	})
}

func (a *nativeApp) providerScopeButton(c *ui.Context, p palette, key, label, scope string) {
	panel := &a.providers
	b := ui.ButtonBase(c.Key(key)).Grow(1).Height(28).Radius(7).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if panel.Scope == scope {
		b.Background(p.active).Border(1, p.t.Accent)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		color := p.t.TextMuted
		if panel.Scope == scope {
			color = p.t.Accent
		}
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(color)
	})
	b.Label(T(label)).Tooltip(Tf("供应商范围：%s", T(label)))
	if b.Clicked() {
		panel.Scope = scope
	}
}

func (a *nativeApp) providerCatalogRow(c *ui.Context, p palette, config providerConfigView) {
	panel := &a.providers
	active := config.ID == panel.ActiveID
	b := ui.ButtonBase(c.Key("provider-catalog:"+config.ID)).Padding(6, 7).Radius(8).
		AlignItems(ui.Center).Gap(7).Border(1, p.t.Border)
	if active {
		b.Background(p.active).Border(1, p.t.Accent)
	} else if b.Hovered() {
		b.Background(p.hover)
	} else {
		b.Background(p.t.Background)
	}
	b.Children(func() {
		color := p.t.TextMuted
		if active {
			color = p.t.Accent
		}
		ui.Icon(c, iconCpu).FontSize(14).TextColor(color)
		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
				ui.Text(c, config.ID).FontSize(11.5).FontWeight(650).TextColor(p.t.Text).SingleLine().Grow(1)
				if config.IsDefault {
					badge(c, p, "默认")
				}
			})
			desc := providerConfigDescription(config)
			ui.Text(c, desc).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
		})
	})
	b.Label(T("编辑供应商 ") + config.ID).Tooltip(config.Provider.BaseURL)
	if b.Clicked() {
		a.selectProviderDraft(config.ID, true)
	}
}

func providerConfigDescription(config providerConfigView) string {
	masked := providerMaskedKey(config.MaskedKey)
	if masked == "" {
		masked = T("未配置密钥")
	}
	parts := []string{masked}
	if config.Provider.BaseURL != "" {
		parts = append(parts, config.Provider.BaseURL)
	}
	parts = append(parts, Tf("%d 个模型", len(config.Provider.Models)))
	return strings.Join(parts, " · ")
}

func (a *nativeApp) providerEditorColumn(c *ui.Context, p palette) {
	panel := &a.providers
	ui.Column(c).Grow(1).Padding(12).Radius(12).Background(p.t.Surface).
		Border(1, p.t.Border).Gap(10).Children(func() {
		if panel.Draft == nil {
			ui.Column(c).Padding(18).Center().Gap(4).Children(func() {
				ui.Text(c, T("选择一个供应商开始编辑")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
				ui.Text(c, T("或点击左侧“新建供应商”。")).FontSize(10.5).TextColor(p.t.TextMuted)
			})
			return
		}
		a.providerEditorHeader(c, p)
		tab := panel.EditorTab
		ui.Tabs(c, &tab, T("连接"), T("模型"), T("高级"))
		panel.EditorTab = tab
		switch tab {
		case providerEditorModels:
			a.providerModelsTab(c, p)
		case providerEditorAdvanced:
			a.providerAdvancedTab(c, p)
		default:
			a.providerConnectionTab(c, p)
		}
	})
}

func (a *nativeApp) providerEditorHeader(c *ui.Context, p palette) {
	panel := &a.providers
	draft := panel.Draft
	isNew := panel.OriginalID == "" && providerConfigByID(panel.Catalog, draft.ID) == nil
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Icon(c, iconCpu).FontSize(17).TextColor(p.t.Accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, draft.ID).FontSize(14).FontWeight(650).TextColor(p.t.Text).SingleLine()
					if draft.IsDefault {
						badge(c, p, "默认")
					}
					if isNew {
						badge(c, p, "新建")
					}
				})
				sub := strings.TrimSpace(strings.Join([]string{draft.Vendor, draft.BaseURL}, " · "))
				if sub == "" {
					sub = "尚未填写连接信息"
				}
				ui.Text(c, T(sub)).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
			})
		})
		ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			test := ui.Button(c.Key("provider-test"), T("测试连接"))
			test.Label(T("测试供应商 ") + draft.ID + T(" 的连接")).Tooltip(T("发送一次最小请求，不会显示密钥"))
			test.Disabled(isNew || panel.Testing || panel.Saving || panel.Deleting)
			if test.Clicked() {
				a.testProviderDraft()
			}
			save := ui.PrimaryButton(c.Key("provider-save"), T("保存供应商"))
			save.Label(T("保存供应商 ") + draft.ID)
			save.Disabled(panel.Saving || panel.Testing || panel.Deleting)
			if save.Clicked() {
				a.saveProviderDraft()
			}
		})
	})
}

func (a *nativeApp) providerConnectionTab(c *ui.Context, p palette) {
	panel := &a.providers
	draft := panel.Draft
	isNew := panel.OriginalID == "" && providerConfigByID(panel.Catalog, draft.ID) == nil
	canRename := isNew || draft.GlobalOverride
	ui.Column(c).Gap(10).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, T("供应商 ID")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
			id := ui.TextInput(c.Key("provider-id"), &draft.ID).FontSize(12).Height(32).Padding(0, 8).
				Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("供应商 ID"))
			id.Disabled(!canRename)
			if !canRename {
				ui.Text(c, T("内置预置 ID 不可重命名；保存后会创建全局覆盖配置。")).FontSize(9.5).TextColor(p.t.TextMuted)
			}
		})
		a.providerLabeledInput(c, p, "provider-vendor", "供应商 Vendor", &draft.Vendor)
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, T("API 类型")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
			options := providerSelectOptions(strings.TrimSpace(draft.API), providerAPIChoices)
			if len(options) == 0 {
				options = []string{"openai-chat"}
			}
			ui.Select(c.Key("provider-api"), &draft.API, options).FontSize(12).Height(32).Padding(0, 8).
				Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("API 类型"))
		})
		a.providerLabeledInput(c, p, "provider-base-url", "Base URL", &draft.BaseURL)
		ui.Column(c).Gap(3).Children(func() {
			ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
				ui.Text(c, T("API 密钥")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				state := "尚未配置"
				if draft.APIKeyConfigured {
					state = "已配置"
				}
				if draft.MaskedKey != "" {
					state += " · " + draft.MaskedKey
				}
				ui.Text(c, "["+state+"]").FontSize(9.5).TextColor(p.t.TextMuted)
			})
			placeholder := T("输入新密钥；留空保持不变")
			if draft.MaskedKey != "" {
				placeholder = Tf("留空保持不变（当前 %s）", draft.MaskedKey)
			}
			key := ui.TextInput(c.Key("provider-api-key"), &panel.Secret.APIKey).Password().
				FontSize(12).Height(32).Padding(0, 8).Placeholder(placeholder).
				Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T("API 密钥（只写）"))
			key.OnChange(func() {
				if strings.TrimSpace(panel.Secret.APIKey) != "" {
					panel.Secret.ClearKey = false
				}
			})
			ui.Text(c, T("密钥只写入服务端，不会回显、记录或保存在 Native 状态输出中。")).FontSize(9.5).TextColor(p.t.TextMuted)
		})
		a.providerLabeledInput(c, p, "provider-http-proxy", "HTTP 代理", &draft.HTTPProxy)
		ui.Checkbox(c, &draft.ForceHTTP11, T("强制 HTTP/1.1")).FontSize(11.5)
	})
}

func (a *nativeApp) providerLabeledInput(c *ui.Context, p palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(label)
	})
}

func (a *nativeApp) providerModelsTab(c *ui.Context, p palette) {
	panel := &a.providers
	draft := panel.Draft
	ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, T("模型目录")).FontSize(12).FontWeight(650).TextColor(p.t.Text).Grow(1)
		add := ui.Button(c.Key("provider-add-model"), T("添加模型"))
		add.Label(T("添加模型"))
		if add.Clicked() {
			draft.Models = append(draft.Models, providerModelDefaultsFor(panel.Catalog))
		}
		discover := ui.Button(c.Key("provider-discover"), T("发现模型"))
		discover.Label(T("从供应商发现模型")).Tooltip(T("调用 providers/discover，结果需确认后才会加入"))
		discover.Disabled(panel.Discovering || panel.Saving)
		if discover.Clicked() {
			a.discoverProviderModels()
		}
	})
	if len(draft.Models) == 0 {
		ui.Text(c, T("该供应商还没有模型。添加模型或使用“发现模型”。")).FontSize(11).TextColor(p.t.TextMuted)
		return
	}
	ui.Column(c).Gap(8).Children(func() {
		for i := range draft.Models {
			a.providerModelRow(c, p, i)
		}
	})
}

func (a *nativeApp) providerModelRow(c *ui.Context, p palette, index int) {
	panel := &a.providers
	draft := panel.Draft
	if draft == nil || index < 0 || index >= len(draft.Models) {
		return
	}
	model := &draft.Models[index]
	ui.Column(c).Padding(9).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, Tf("模型 %d", index+1)).FontSize(10.5).FontWeight(650).TextColor(p.t.TextMuted).Grow(1)
			remove := ui.Button(c.Key(fmt.Sprintf("provider-remove-model:%d", index)), T("移除模型"))
			remove.Label(T("移除模型 ") + model.ID)
			if remove.Clicked() {
				draft.Models = append(draft.Models[:index], draft.Models[index+1:]...)
			}
		})
		a.providerLabeledInput(c, p, fmt.Sprintf("provider-model-id:%d", index), "模型 ID", &model.ID)
		a.providerLabeledInput(c, p, fmt.Sprintf("provider-model-name:%d", index), "模型名称", &model.Name)
		a.providerLabeledInput(c, p, fmt.Sprintf("provider-model-context:%d", index), "上下文窗口", &model.ContextWindow)
		a.providerLabeledInput(c, p, fmt.Sprintf("provider-model-max:%d", index), "最大 Tokens", &model.MaxTokens)
		a.providerLabeledInput(c, p, fmt.Sprintf("provider-model-input:%d", index), "输入类型（逗号分隔）", &model.Input)
		ui.Checkbox(c, &model.Reasoning, T("支持推理")).FontSize(11.5)
	})
}

func (a *nativeApp) providerAdvancedTab(c *ui.Context, p palette) {
	panel := &a.providers
	draft := panel.Draft
	ui.Column(c).Gap(10).Children(func() {
		if draft.GlobalOverride {
			ui.Column(c).Padding(9).Radius(9).Background(p.errorBg).
				Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(7).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Icon(c, iconAlert).FontSize(14).TextColor(p.t.Danger)
					ui.Text(c, T("危险区域")).FontSize(11.5).FontWeight(650).TextColor(p.t.Danger).Grow(1)
					remove := ui.Button(c.Key("provider-remove-override"), T("重置 / 移除"))
					remove.Label(T("重置或移除供应商 ") + draft.ID)
					remove.Disabled(draft.IsDefault || panel.Deleting)
					if remove.Clicked() {
						a.requestDeleteProvider()
					}
				})
				if draft.IsDefault {
					ui.Text(c, T("当前默认供应商不能移除；请先在“默认供应商与模型”中切换默认供应商。")).FontSize(10).TextColor(p.t.TextMuted)
				} else {
					ui.Text(c, T("预置供应商会恢复内置配置，自定义供应商会被删除。")).FontSize(10).TextColor(p.t.TextMuted)
				}
			})
		} else {
			ui.Text(c, T("该供应商使用内置预置配置；保存后会创建全局覆盖。")).FontSize(10.5).TextColor(p.t.TextMuted)
		}
		a.providerLabeledInput(c, p, "provider-thinking-format", "思考格式", &draft.ThinkingFormat)
		a.providerLabeledInput(c, p, "provider-max-images", "每次请求最大图片数（-1 表示不限）", &draft.MaxImagesPerRequest)
		ui.Checkbox(c, &draft.CacheControl, T("启用提示缓存（cacheControl）")).FontSize(11.5)
		ui.Column(c).Gap(2).Children(func() {
			clear := ui.Checkbox(c, &panel.Secret.ClearKey, T("保存时清除已配置的 API 密钥")).FontSize(11.5)
			clear.OnChange(func() {
				if panel.Secret.ClearKey {
					panel.Secret.APIKey = ""
				}
			})
			ui.Text(c, T("仅在保存时提交空密钥以清除；普通保存不会发送密钥。")).FontSize(9.5).TextColor(p.t.TextMuted)
		})
	})
}

// settingsProviderDialogs renders the Native confirmation and discovery
// dialogs above the settings page. Neither is a browser/native prompt.
func (a *nativeApp) settingsProviderDialogs(c *ui.Context, p palette) {
	if !a.settingsOpen {
		return
	}
	panel := &a.providers
	if panel.PendingDelete {
		a.providerDeleteDialog(c, p)
	}
	if panel.DiscoverOpen {
		a.providerDiscoverDialog(c, p)
	}
}

func (a *nativeApp) providerDeleteDialog(c *ui.Context, p palette) {
	panel := &a.providers
	draft := panel.Draft
	dialog := ui.DialogBase(c, &panel.PendingDelete, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, T("重置或移除供应商配置？")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			id := ""
			if draft != nil {
				id = draft.ID
			}
			ui.Text(c, Tf("将移除 %q 的全局覆盖。预置供应商会恢复内置配置，自定义供应商会从目录中删除。", id)).
				FontSize(11.5).TextColor(p.t.TextMuted).Selectable()
			ui.Text(c, T("此操作不会发送或显示任何 API 密钥。")).FontSize(10).TextColor(p.t.TextMuted)
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("provider-delete-cancel"), T("取消"))
				cancel.Label(T("取消重置或移除供应商"))
				if cancel.Clicked() {
					a.cancelDeleteProvider()
				}
				confirm := ui.Button(c.Key("provider-delete-confirm"), T("确认移除"))
				confirm.Label(T("确认重置或移除供应商"))
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(draft == nil || draft.IsDefault)
				if confirm.Clicked() {
					a.confirmDeleteProvider()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelDeleteProvider()
	}
}

func (a *nativeApp) providerDiscoverDialog(c *ui.Context, p palette) {
	panel := &a.providers
	dialog := ui.DialogBase(c, &panel.DiscoverOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(560).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconSpark).FontSize(16).TextColor(p.t.Accent)
				ui.Text(c, T("发现模型")).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, T("从当前供应商获取可用模型。选择后会加入编辑器草稿，仍需保存供应商才会生效。")).
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			ui.TextInput(c.Key("provider-discover-search"), &panel.DiscoverSearch).
				Placeholder(T("搜索发现的模型…")).FontSize(11.5).Height(30).Padding(0, 8).
				Background(p.t.Surface).Radius(8).Border(1, p.t.Border).Label(T("搜索发现的模型"))
			query := strings.ToLower(strings.TrimSpace(panel.DiscoverSearch))
			shown := make([]providerModel, 0, len(panel.DiscoverCandidates))
			for _, candidate := range panel.DiscoverCandidates {
				if query != "" && !strings.Contains(strings.ToLower(candidate.ID+" "+candidate.Name), query) {
					continue
				}
				shown = append(shown, candidate)
			}
			if len(shown) == 0 {
				ui.Text(c, T("没有匹配的模型。")).FontSize(11).TextColor(p.t.TextMuted)
			} else {
				ui.Scroll(c).Height(300).Children(func() {
					ui.Column(c).Gap(4).Children(func() {
						for _, candidate := range shown {
							candidate := candidate
							selected := panel.DiscoverSelected[candidate.ID]
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
			for _, selected := range panel.DiscoverSelected {
				if selected != nil && *selected {
					selectedCount++
				}
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				ui.Text(c, Tf("已选择 %d 个", selectedCount)).FontSize(10.5).TextColor(p.t.TextMuted).Grow(1)
				cancel := ui.Button(c.Key("provider-discover-cancel"), T("取消"))
				cancel.Label(T("取消添加发现的模型"))
				if cancel.Clicked() {
					a.closeDiscoverDialog()
				}
				add := ui.PrimaryButton(c.Key("provider-discover-add"), T("添加选中模型"))
				add.Label(T("添加选中的发现模型"))
				add.Disabled(selectedCount == 0)
				if add.Clicked() && selectedCount > 0 {
					a.addDiscoveredModels()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeDiscoverDialog()
	}
}
