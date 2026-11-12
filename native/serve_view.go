package main

import (
	"github.com/egoist/mygo/ui"
)

// settingsServePane renders the Native equivalent of the Desktop ServePanel
// inside the Settings View. It is capability-gated by manageServeConfig and
// only calls mothx/manage/serve/get and mothx/manage/serve/patch. It never
// reads serve.json directly, starts a server, or changes the running ACP
// Desktop Runtime.
func (a *nativeApp) settingsServePane(c *ui.Context, p palette) {
	if !a.serveCapable() {
		a.settingsCard(c, p, "Serve 配置", "当前运行时未公开 Serve 配置管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageServeConfig；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.serve
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取 Serve 配置…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.serveErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.serveStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, T("正在刷新 Serve 配置…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	ui.Text(c, T("这些设置仅保存到全局 serve.json，供以后启动 mothx serve 使用；不会重启或改变当前 ACP Desktop Runtime。")).
		FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)

	busy := panel.Loading || panel.Saving
	form := &panel.Form

	a.settingsCard(c, p, "运行时 / API", "Serve 监听地址、超时、并发与日志级别。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.serveTextInput(c, p, "serve-listen", "监听地址", "例如 127.0.0.1:7872", &form.Listen, busy)
			a.serveTextInput(c, p, "serve-webui-dir", "Web UI 目录", "例如 ui/dist", &form.WebUIDir, busy)
			a.serveTextInput(c, p, "serve-request-timeout", "请求超时（秒）", "", &form.RequestTimeout, busy)
			a.serveTextInput(c, p, "serve-background-run-max", "后台运行最大时长（秒）", "", &form.BackgroundRunMax, busy)
			a.serveTextInput(c, p, "serve-max-concurrent", "最大并发请求数", "0 表示不限制", &form.MaxConcurrent, busy)
			a.serveSelect(c, p, "serve-log-level", "日志级别", serveLogLevels, &form.LogLevel, busy)
			a.serveSelect(c, p, "serve-default-mode", "默认模式", serveModes, &form.DefaultMode, busy)
			a.serveSelect(c, p, "serve-default-thinking", "默认思考等级", serveThinkingLevels, &form.DefaultThinking, busy)
			a.serveSelect(c, p, "serve-system-prompt-mode", "系统提示词模式", serveSystemPromptModes, &form.SystemPromptMode, busy)
		})
	})

	a.settingsCard(c, p, "功能开关", "启用或禁用 Serve 提供的功能模块。", func() {
		ui.Column(c).Gap(8).Children(func() {
			a.serveCheckbox(c, p, "serve-feature-webui", "Web UI", &form.WebUI, busy)
			a.serveCheckbox(c, p, "serve-feature-openai-api", "OpenAI API", &form.OpenAIAPI, busy)
			a.serveCheckbox(c, p, "serve-feature-multi-agent", "多 Agent", &form.MultiAgent, busy)
			a.serveCheckbox(c, p, "serve-feature-cron", "Cron", &form.CronFeature, busy)
			a.serveCheckbox(c, p, "serve-feature-memory", "Memory", &form.MemoryFeature, busy)
		})
	})

	a.settingsCard(c, p, "能力开关", "控制 Agent 可使用的扩展能力。", func() {
		ui.Column(c).Gap(8).Children(func() {
			a.serveCheckbox(c, p, "serve-enable-delegate", "启用委派", &form.EnableDelegate, busy)
			a.serveCheckbox(c, p, "serve-enable-workflows", "启用工作流", &form.EnableWorkflows, busy)
			a.serveCheckbox(c, p, "serve-enable-web-search", "启用 Web 搜索", &form.EnableWebSearch, busy)
			a.serveCheckbox(c, p, "serve-enable-browser", "启用浏览器", &form.EnableBrowser, busy)
			a.serveCheckbox(c, p, "serve-enable-artifact", "启用 WebUI/API 制品发布", &form.EnableArtifact, busy)
			a.serveCheckbox(c, p, "serve-enable-a2a-master", "启用 A2A Master", &form.EnableA2AMaster, busy)
		})
	})

	a.settingsCard(c, p, "输出与工具可见性", "控制工具调用与系统提示词的暴露方式。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.serveSelect(c, p, "serve-tool-mode", "工具可见性模式", serveToolModes, &form.ToolMode, busy)
			a.serveSelect(c, p, "serve-tool-detail", "工具详情", serveToolDetails, &form.ToolDetail, busy)
		})
	})

	a.settingsCard(c, p, "自动化与记忆", "Cron 与 Memory 的启停及路径。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.serveCheckbox(c, p, "serve-cron-enabled", "启用 Cron", &form.CronFeature, busy)
			a.serveTextInput(c, p, "serve-cron-interval", "Cron 检查间隔（秒）", "", &form.CronInterval, busy)
			a.serveCheckbox(c, p, "serve-memory-enabled", "启用 Memory", &form.MemoryFeature, busy)
			a.serveTextInput(c, p, "serve-memory-path", "Memory 文件路径", "留空使用默认路径", &form.MemoryPath, busy)
		})
	})

	a.settingsCard(c, p, "安全", "智能审批等安全策略。", func() {
		ui.Column(c).Gap(8).Children(func() {
			a.serveCheckbox(c, p, "serve-smart-approvals", "智能审批", &form.SmartApprovals, busy)
		})
	})

	a.settingsCard(c, p, "会话限制", "控制会话空闲超时与最大会话数（0 表示不限）。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.serveTextInput(c, p, "serve-idle-timeout", "空闲超时（秒）", "", &form.IdleTimeout, busy)
			a.serveTextInput(c, p, "serve-max-sessions", "最大会话数", "0 表示不限", &form.MaxSessions, busy)
		})
	})

	a.settingsCard(c, p, "Agent 行为", "运行时长、压力阈值与回合限制。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.serveTextInput(c, p, "serve-max-turns", "最大回合数", "", &form.MaxTurns, busy)
			a.serveCheckbox(c, p, "serve-budget-pressure", "预算压力", &form.BudgetPressure, busy)
			a.serveCheckbox(c, p, "serve-context-pressure", "上下文压力", &form.ContextPressure, busy)
			a.serveTextInput(c, p, "serve-budget-threshold", "预算压力阈值（0-1）", "", &form.BudgetThreshold, busy)
			a.serveTextInput(c, p, "serve-context-threshold", "上下文压力阈值（0-1）", "", &form.ContextThreshold, busy)
			a.serveTextInput(c, p, "serve-run-stale-timeout", "运行无事件超时（秒）", "", &form.RunStaleTimeout, busy)
			a.serveTextInput(c, p, "serve-run-max-duration", "运行最大时长（秒）", "", &form.RunMaxDuration, busy)
			a.serveTextInput(c, p, "serve-agent-background-run-max", "后台运行最大时长（秒）", "", &form.AgentBackgroundRunMax, busy)
		})
	})

	a.settingsCard(c, p, "Lobster 模式", "开启后强制 yolo 模式并禁用沙箱。", func() {
		ui.Column(c).Gap(8).Children(func() {
			a.serveCheckbox(c, p, "serve-lobster-mode", "Lobster 模式", &form.LobsterMode, busy)
		})
	})

	a.serveSaveRow(c, p, busy)
}

func (a *nativeApp) serveTextInput(c *ui.Context, p palette, key, label, placeholder string, value *string, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T(label))
		if placeholder != "" {
			input.Placeholder(T(placeholder))
		}
		input.Disabled(disabled)
	})
}

func (a *nativeApp) serveSelect(c *ui.Context, p palette, key, label string, options []string, value *string, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		selectEl := ui.Select(c.Key(key), value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(T(label))
		selectEl.Disabled(disabled)
	})
}

func (a *nativeApp) serveCheckbox(c *ui.Context, p palette, key, label string, value *bool, disabled bool) {
	box := ui.Checkbox(c.Key(key), value, T(label)).FontSize(11.5)
	box.Disabled(disabled)
}

func (a *nativeApp) serveSaveRow(c *ui.Context, p palette, busy bool) {
	ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if a.serve.Saving {
			statusPill(c, p, "running")
			ui.Text(c, T("保存中…")).FontSize(10.5).TextColor(p.t.TextMuted)
		}
		reload := ui.Button(c.Key("serve-reload"), T("重新加载"))
		reload.Label(T("重新加载 Serve 配置"))
		reload.Disabled(busy)
		if reload.Clicked() && !busy {
			a.serve.Loaded = false
			a.startServePanelLoad()
		}
		save := ui.PrimaryButton(c.Key("serve-save"), T("保存 Serve 配置"))
		save.Label(T("保存 Serve 配置")).Tooltip(T("通过 mothx/manage/serve/patch 写入全局 serve.json"))
		save.Disabled(busy || !a.serve.Loaded)
		if save.Clicked() && !busy && a.serve.Loaded {
			a.saveServeSettings()
		}
	})
}

func (a *nativeApp) serveErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("Serve 配置不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.serveCapable() {
			if ui.Button(c.Key("serve-retry"), T("重试")).Label(T("重试加载 Serve 配置")).Clicked() {
				a.serve.Loaded = false
				a.startServePanelLoad()
			}
		}
	})
}

func (a *nativeApp) serveStatusBanner(c *ui.Context, p palette) {
	panel := &a.serve
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
		} else if panel.Saving {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(panel.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}
