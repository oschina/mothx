package serve

import (
	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the Native equivalent of the Desktop ServePanel
// inside the Settings View. It is capability-gated by manageServeConfig and
// only calls mothx/manage/serve/get and mothx/manage/serve/patch. It never
// reads serve.json directly, starts a server, or changes the running ACP
// Desktop Runtime.
func (p *Panel) View(h host.Base, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "Serve 配置", "当前运行时未公开 Serve 配置管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageServeConfig；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取 Serve 配置…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		ui.Text(c, i18n.T("正在刷新 Serve 配置…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	ui.Text(c, i18n.T("这些设置仅保存到全局 serve.json，供以后启动 mothx serve 使用；不会重启或改变当前 ACP Desktop Runtime。")).
		FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)

	busy := p.Loading || p.Saving
	form := &p.Form

	kit.SettingsCard(c, pal, "运行时 / API", "Serve 监听地址、超时、并发与日志级别。", func() {
		ui.Column(c).Gap(10).Children(func() {
			p.textInput(c, pal, "serve-listen", "监听地址", "例如 127.0.0.1:7872", &form.Listen, busy)
			p.textInput(c, pal, "serve-webui-dir", "Web UI 目录", "例如 ui/dist", &form.WebUIDir, busy)
			p.textInput(c, pal, "serve-request-timeout", "请求超时（秒）", "", &form.RequestTimeout, busy)
			p.textInput(c, pal, "serve-background-run-max", "后台运行最大时长（秒）", "", &form.BackgroundRunMax, busy)
			p.textInput(c, pal, "serve-max-concurrent", "最大并发请求数", "0 表示不限制", &form.MaxConcurrent, busy)
			p.selectField(c, pal, "serve-log-level", "日志级别", serveLogLevels, &form.LogLevel, busy)
			p.selectField(c, pal, "serve-default-mode", "默认模式", serveModes, &form.DefaultMode, busy)
			p.selectField(c, pal, "serve-default-thinking", "默认思考等级", serveThinkingLevels, &form.DefaultThinking, busy)
			p.selectField(c, pal, "serve-system-prompt-mode", "系统提示词模式", serveSystemPromptModes, &form.SystemPromptMode, busy)
		})
	})

	kit.SettingsCard(c, pal, "功能开关", "启用或禁用 Serve 提供的功能模块。", func() {
		ui.Column(c).Gap(8).Children(func() {
			p.checkbox(c, pal, "serve-feature-webui", "Web UI", &form.WebUI, busy)
			p.checkbox(c, pal, "serve-feature-openai-api", "OpenAI API", &form.OpenAIAPI, busy)
			p.checkbox(c, pal, "serve-feature-multi-agent", "多 Agent", &form.MultiAgent, busy)
			p.checkbox(c, pal, "serve-feature-cron", "Cron", &form.CronFeature, busy)
			p.checkbox(c, pal, "serve-feature-memory", "Memory", &form.MemoryFeature, busy)
		})
	})

	kit.SettingsCard(c, pal, "能力开关", "控制 Agent 可使用的扩展能力。", func() {
		ui.Column(c).Gap(8).Children(func() {
			p.checkbox(c, pal, "serve-enable-delegate", "启用委派", &form.EnableDelegate, busy)
			p.checkbox(c, pal, "serve-enable-workflows", "启用工作流", &form.EnableWorkflows, busy)
			p.checkbox(c, pal, "serve-enable-web-search", "启用 Web 搜索", &form.EnableWebSearch, busy)
			p.checkbox(c, pal, "serve-enable-browser", "启用浏览器", &form.EnableBrowser, busy)
			p.checkbox(c, pal, "serve-enable-artifact", "启用 WebUI/API 制品发布", &form.EnableArtifact, busy)
			p.checkbox(c, pal, "serve-enable-a2a-master", "启用 A2A Master", &form.EnableA2AMaster, busy)
		})
	})

	kit.SettingsCard(c, pal, "输出与工具可见性", "控制工具调用与系统提示词的暴露方式。", func() {
		ui.Column(c).Gap(10).Children(func() {
			p.selectField(c, pal, "serve-tool-mode", "工具可见性模式", serveToolModes, &form.ToolMode, busy)
			p.selectField(c, pal, "serve-tool-detail", "工具详情", serveToolDetails, &form.ToolDetail, busy)
		})
	})

	kit.SettingsCard(c, pal, "自动化与记忆", "Cron 与 Memory 的启停及路径。", func() {
		ui.Column(c).Gap(10).Children(func() {
			p.checkbox(c, pal, "serve-cron-enabled", "启用 Cron", &form.CronFeature, busy)
			p.textInput(c, pal, "serve-cron-interval", "Cron 检查间隔（秒）", "", &form.CronInterval, busy)
			p.checkbox(c, pal, "serve-memory-enabled", "启用 Memory", &form.MemoryFeature, busy)
			p.textInput(c, pal, "serve-memory-path", "Memory 文件路径", "留空使用默认路径", &form.MemoryPath, busy)
		})
	})

	kit.SettingsCard(c, pal, "安全", "智能审批等安全策略。", func() {
		ui.Column(c).Gap(8).Children(func() {
			p.checkbox(c, pal, "serve-smart-approvals", "智能审批", &form.SmartApprovals, busy)
		})
	})

	kit.SettingsCard(c, pal, "会话限制", "控制会话空闲超时与最大会话数（0 表示不限）。", func() {
		ui.Column(c).Gap(10).Children(func() {
			p.textInput(c, pal, "serve-idle-timeout", "空闲超时（秒）", "", &form.IdleTimeout, busy)
			p.textInput(c, pal, "serve-max-sessions", "最大会话数", "0 表示不限", &form.MaxSessions, busy)
		})
	})

	kit.SettingsCard(c, pal, "Agent 行为", "运行时长、压力阈值与回合限制。", func() {
		ui.Column(c).Gap(10).Children(func() {
			p.textInput(c, pal, "serve-max-turns", "最大回合数", "", &form.MaxTurns, busy)
			p.checkbox(c, pal, "serve-budget-pressure", "预算压力", &form.BudgetPressure, busy)
			p.checkbox(c, pal, "serve-context-pressure", "上下文压力", &form.ContextPressure, busy)
			p.textInput(c, pal, "serve-budget-threshold", "预算压力阈值（0-1）", "", &form.BudgetThreshold, busy)
			p.textInput(c, pal, "serve-context-threshold", "上下文压力阈值（0-1）", "", &form.ContextThreshold, busy)
			p.textInput(c, pal, "serve-run-stale-timeout", "运行无事件超时（秒）", "", &form.RunStaleTimeout, busy)
			p.textInput(c, pal, "serve-run-max-duration", "运行最大时长（秒）", "", &form.RunMaxDuration, busy)
			p.textInput(c, pal, "serve-agent-background-run-max", "后台运行最大时长（秒）", "", &form.AgentBackgroundRunMax, busy)
		})
	})

	kit.SettingsCard(c, pal, "Lobster 模式", "开启后强制 yolo 模式并禁用沙箱。", func() {
		ui.Column(c).Gap(8).Children(func() {
			p.checkbox(c, pal, "serve-lobster-mode", "Lobster 模式", &form.LobsterMode, busy)
		})
	})

	p.saveRow(h, c, pal, busy)
}

func (p *Panel) textInput(c *ui.Context, pal kit.Palette, key, label, placeholder string, value *string, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T(label))
		if placeholder != "" {
			input.Placeholder(i18n.T(placeholder))
		}
		input.Disabled(disabled)
	})
}

func (p *Panel) selectField(c *ui.Context, pal kit.Palette, key, label string, options []string, value *string, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		selectEl := ui.Select(c.Key(key), value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T(label))
		selectEl.Disabled(disabled)
	})
}

func (p *Panel) checkbox(c *ui.Context, pal kit.Palette, key, label string, value *bool, disabled bool) {
	box := ui.Checkbox(c.Key(key), value, i18n.T(label)).FontSize(11.5)
	box.Disabled(disabled)
}

func (p *Panel) saveRow(h host.Base, c *ui.Context, pal kit.Palette, busy bool) {
	ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if p.Saving {
			kit.StatusPill(c, pal, "running")
			ui.Text(c, i18n.T("保存中…")).FontSize(10.5).TextColor(pal.T.TextMuted)
		}
		reload := ui.Button(c.Key("serve-reload"), i18n.T("重新加载"))
		reload.Label(i18n.T("重新加载 Serve 配置"))
		reload.Disabled(busy)
		if reload.Clicked() && !busy {
			p.Loaded = false
			p.StartLoad(h)
		}
		save := ui.PrimaryButton(c.Key("serve-save"), i18n.T("保存 Serve 配置"))
		save.Label(i18n.T("保存 Serve 配置")).Tooltip(i18n.T("通过 mothx/manage/serve/patch 写入全局 serve.json"))
		save.Disabled(busy || !p.Loaded)
		if save.Clicked() && !busy && p.Loaded {
			p.Save(h)
		}
	})
}

func (p *Panel) errCard(h host.Base, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("Serve 配置不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("serve-retry"), i18n.T("重试")).Label(i18n.T("重试加载 Serve 配置")).Clicked() {
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
		} else if p.Saving {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}
