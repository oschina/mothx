package main

import "github.com/egoist/mygo/ui"

// settingsApplicationPane renders the full Application settings form inside the
// detail pane of the new settings view. It reuses the existing cards, inputs,
// and save footer so load/save/error/reconnect behavior stays intact.
func (a *nativeApp) settingsApplicationPane(c *ui.Context, p palette) {
	a.settingsLoadingOrError(c, p)
	if !a.settingsLoading && a.settingsErr == "" && a.settingsView != nil {
		ui.Column(c).Gap(14).Children(func() {
			a.settingsDefaultsCard(c, p)
			a.settingsContextCard(c, p)
			a.settingsToolsCard(c, p)
			a.settingsImageCard(c, p)
			a.settingsRuntimeCard(c, p)
			a.settingsSafetyCard(c, p)
		})
	}
	a.settingsFooter(c, p)
}

func (a *nativeApp) settingsLoadingOrError(c *ui.Context, p palette) {
	if a.settingsLoading {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, "正在读取应用设置…").FontSize(12).TextColor(p.t.TextMuted)
		})
	}
	if a.settingsErr != "" {
		ui.Column(c).Padding(10).Radius(10).Background(p.errorBg).
			Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, "应用设置不可用").FontSize(12).FontWeight(650).TextColor(p.t.Danger)
			})
			ui.Text(c, a.settingsErr).FontSize(11).TextColor(p.t.TextMuted).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.Start).Gap(8).Children(func() {
				if ui.Button(c.Key("settings-retry"), "重试").Clicked() {
					a.retryLoadSettings()
				}
				if ui.Button(c.Key("settings-close-error"), "关闭").Clicked() {
					a.closeSettings()
				}
			})
		})
	}
}

func (a *nativeApp) settingsFooter(c *ui.Context, p palette) {
	if a.settingsStatus != "" {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			if a.settingsSaving {
				statusPill(c, p, "running")
			}
			ui.Text(c, a.settingsStatus).FontSize(10.5).TextColor(p.t.TextMuted).SingleLine().Grow(1)
		})
	}
	ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if ui.Button(c.Key("settings-cancel"), "取消").Clicked() {
			a.closeSettings()
		}
		btn := ui.PrimaryButton(c.Key("settings-save"), "保存")
		btn.Disabled(a.settingsSaving || a.settingsLoading)
		if btn.Clicked() && !a.settingsSaving && !a.settingsLoading {
			a.saveApplicationSettings()
		}
	})
}

func (a *nativeApp) settingsCard(c *ui.Context, p palette, title, desc string, body func()) {
	ui.Column(c).Padding(14).Radius(12).Background(p.t.Surface).Border(1, p.t.Border).Gap(10).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(12).FontWeight(650).TextColor(p.t.Text)
			if desc != "" {
				ui.Text(c, desc).FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			}
		})
		body()
	})
}

func (a *nativeApp) settingsDefaultsCard(c *ui.Context, p palette) {
	a.settingsCard(c, p, "默认行为", "影响新会话与 Agent 的默认执行策略。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.settingsSelect(c, p, "defaultMode", "默认模式", []string{"agent", "plan", "yolo", "os"}, &a.settingsForm.DefaultMode)
			ui.Checkbox(c, &a.settingsForm.EnablePlanTool, "启用 Plan 工具").FontSize(11.5)
			ui.Checkbox(c, &a.settingsForm.EnableArtifact, "启用 TUI/CLI 制品发布").FontSize(11.5)
			ui.Checkbox(c, &a.settingsForm.EnableACPArtifact, "启用 Desktop (ACP) 制品发布").FontSize(11.5)
			ui.Checkbox(c, &a.settingsForm.Authored, "使用作者模式").FontSize(11.5)
			ui.Checkbox(c, &a.settingsForm.UpdateCheck, "启动时检查更新").FontSize(11.5)
		})
	})
}

func (a *nativeApp) settingsContextCard(c *ui.Context, p palette) {
	a.settingsCard(c, p, "上下文与压缩", "控制项目上下文文件和长会话的上下文压缩。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Checkbox(c, &a.settingsForm.ContextEnabled, "加载上下文文件").FontSize(11.5)
			a.settingsTextArea(c, p, "extraFiles", "额外上下文文件（每行一个）", &a.settingsForm.ExtraFiles, 3)
			ui.Checkbox(c, &a.settingsForm.CompactionEnabled, "启用上下文压缩").FontSize(11.5)
			a.settingsTextInput(c, p, "reserveTokens", "预留 Tokens", &a.settingsForm.ReserveTokens)
			a.settingsTextInput(c, p, "keepRecentTokens", "保留最近 Tokens", &a.settingsForm.KeepRecentTokens)
			a.settingsTextInput(c, p, "tokenizer", "Tokenizer", &a.settingsForm.Tokenizer)
			a.settingsTextInput(c, p, "tokenizerModel", "Tokenizer 模型", &a.settingsForm.TokenizerModel)
			a.settingsTextArea(c, p, "compactionTemplate", "压缩模板", &a.settingsForm.CompactionTemplate, 3)
		})
	})
}

func (a *nativeApp) settingsToolsCard(c *ui.Context, p palette) {
	a.settingsCard(c, p, "工具与搜索", "控制共享 Runtime 中的搜索、工具并发与图像生成。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Checkbox(c, &a.settingsForm.WebSearchEnabled, "启用 Web 搜索").FontSize(11.5)
			a.settingsBackendSelect(c, p)
			a.settingsTextInput(c, p, "webSearchProvider", "凭证供应商", &a.settingsForm.WebSearchProvider)
			a.settingsTextInput(c, p, "webSearchBaseURL", "搜索 Base URL", &a.settingsForm.WebSearchBaseURL)
			a.settingsTokenInput(c, p, "webSearchToken", "搜索 API Token", &a.settingsForm.WebSearchToken,
				a.settingsForm.WebSearchTokenConfigured, "已配置 Token", "尚未配置 Token")
			ui.Checkbox(c, &a.settingsForm.ClearWebSearchToken, "清除已保存的 Token").FontSize(11.5)
			a.settingsSelect(c, p, "toolMode", "工具执行方式", []string{"parallel", "sequential"}, &a.settingsForm.ToolMode)
			a.settingsTextInput(c, p, "toolConcurrency", "工具最大并发数", &a.settingsForm.ToolConcurrency)
		})
	})
}

func (a *nativeApp) settingsBackendSelect(c *ui.Context, p palette) {
	old := a.settingsForm.WebSearchBackend
	a.settingsSelect(c, p, "webSearchBackend", "搜索后端", []string{"deepseek", "yuanbao"}, &a.settingsForm.WebSearchBackend)
	if a.settingsForm.WebSearchBackend != old {
		a.onWebSearchBackendChanged(a.settingsForm.WebSearchBackend)
	}
}

func (a *nativeApp) settingsImageCard(c *ui.Context, p palette) {
	a.settingsCard(c, p, "图像生成", "独立图像生成工具的连接配置；Token 仅可提交，不会回显。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Checkbox(c, &a.settingsForm.ImageEnabled, "启用图像生成").FontSize(11.5)
			a.settingsTextInput(c, p, "imageProvider", "图像供应商", &a.settingsForm.ImageProvider)
			a.settingsTextInput(c, p, "imageAPI", "图像 API 类型", &a.settingsForm.ImageAPI)
			a.settingsTextInput(c, p, "imageBaseURL", "图像 Base URL", &a.settingsForm.ImageBaseURL)
			a.settingsTextInput(c, p, "imageModel", "图像模型", &a.settingsForm.ImageModel)
			a.settingsTokenInput(c, p, "imageToken", "图像 API Token", &a.settingsForm.ImageToken,
				a.settingsForm.ImageTokenConfigured, "Token 已配置；留空保持不变", "尚未配置 Token")
			ui.Checkbox(c, &a.settingsForm.ClearImageToken, "保存时清除图像 TOKEN").FontSize(11.5)
		})
	})
}

func (a *nativeApp) settingsRuntimeCard(c *ui.Context, p palette) {
	a.settingsCard(c, p, "运行时行为", "控制重试和状态行显示行为。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Checkbox(c, &a.settingsForm.RetryEnabled, "启用重试").FontSize(11.5)
			a.settingsTextInput(c, p, "maxRetries", "最大重试次数", &a.settingsForm.MaxRetries)
			a.settingsTextInput(c, p, "baseDelay", "重试基础延迟（ms）", &a.settingsForm.BaseDelay)
			ui.Checkbox(c, &a.settingsForm.StatusLineEnabled, "启用状态行").FontSize(11.5)
			a.settingsTextInput(c, p, "statusType", "状态行类型", &a.settingsForm.StatusType)
			a.settingsTextInput(c, p, "statusCommand", "状态行命令", &a.settingsForm.StatusCommand)
			a.settingsTextInput(c, p, "statusPadding", "状态行间距", &a.settingsForm.StatusPadding)
			a.settingsTextInput(c, p, "statusRefresh", "刷新间隔", &a.settingsForm.StatusRefresh)
			a.settingsTextInput(c, p, "statusTimeout", "命令超时（ms）", &a.settingsForm.StatusTimeout)
			a.settingsTextInput(c, p, "statusFallback", "状态行回退方式", &a.settingsForm.StatusFallback)
		})
	})
}

func (a *nativeApp) settingsSafetyCard(c *ui.Context, p palette) {
	a.settingsCard(c, p, "沙箱与审批", "影响 Agent 的工具隔离与交互审批。", func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Checkbox(c, &a.settingsForm.SandboxEnabled, "启用沙箱").FontSize(11.5)
			a.settingsSelect(c, p, "sandboxLevel", "沙箱级别", []string{"none", "standard", "strict"}, &a.settingsForm.SandboxLevel)
			a.settingsTextInput(c, p, "bwrapPath", "bwrap 路径", &a.settingsForm.BwrapPath)
			ui.Checkbox(c, &a.settingsForm.AllowNetwork, "允许网络访问").FontSize(11.5)
			a.settingsTextInput(c, p, "tmpSize", "临时目录大小", &a.settingsForm.TmpSize)
			ui.Checkbox(c, &a.settingsForm.ProtectGit, "保护 Git 工作区").FontSize(11.5)
			a.settingsTextArea(c, p, "allowedRead", "允许读取路径（每行一个）", &a.settingsForm.AllowedRead, 3)
			a.settingsTextArea(c, p, "allowedWrite", "允许写入路径（每行一个）", &a.settingsForm.AllowedWrite, 3)
			a.settingsTextArea(c, p, "deniedPaths", "禁止访问路径（每行一个）", &a.settingsForm.DeniedPaths, 3)
			ui.Checkbox(c, &a.settingsForm.ConfirmWrite, "写入前要求确认").FontSize(11.5)
			a.settingsTextArea(c, p, "bashWhitelist", "Bash 自动批准前缀（每行一个）", &a.settingsForm.BashWhitelist, 3)
			a.settingsTextArea(c, p, "bashBlacklist", "Bash 强制确认前缀（每行一个）", &a.settingsForm.BashBlacklist, 3)
		})
	})
}

func (a *nativeApp) settingsTextInput(c *ui.Context, p palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border)
	})
}

func (a *nativeApp) settingsTextArea(c *ui.Context, p palette, key, label string, value *string, lines int) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.TextAreaBase(c.Key(key), value).FontSize(12).Lines(lines, lines*3).
			Padding(6, 8).Background(p.t.Background).Radius(8).Border(1, p.t.Border)
	})
}

func (a *nativeApp) settingsSelect(c *ui.Context, p palette, key, label string, options []string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.Select(c, value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border)
	})
}

func (a *nativeApp) settingsTokenInput(c *ui.Context, p palette, key, label string, value *string, configured bool, configuredText, unsetText string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
			status := unsetText
			if configured {
				status = configuredText
			}
			ui.Text(c, "["+status+"]").FontSize(9.5).TextColor(p.t.TextMuted)
		})
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Placeholder("输入新 Token…").Background(p.t.Background).Radius(8).Border(1, p.t.Border)
	})
}
