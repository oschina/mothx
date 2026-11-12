package main

import (
	"github.com/egoist/mygo/ui"
)

// settingsChannelsPane renders the Native equivalent of the Desktop
// ChannelsPanel inside the Settings View. It is capability-gated by
// manageChannels and only calls mothx/manage/channels/get and
// mothx/manage/channels/patch. It never reads serve.json directly, starts a
// channel, or logs in.
func (a *nativeApp) settingsChannelsPane(c *ui.Context, p palette) {
	if !a.channelsCapable() {
		a.settingsCard(c, p, "消息通道", "当前运行时未公开消息通道管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageChannels；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.channels
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取消息通道…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.channelsErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.channelsStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, T("正在刷新消息通道…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	ui.Text(c, T("这些设置仅保存到全局 serve.json，不会启动或登录任何渠道。")).
		FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)

	busy := panel.Loading || panel.Saving != ""

	a.settingsCard(c, p, "Channel 制品", "控制 WeChat 和 Feishu 会话是否可发布并发送生成的制品。", func() {
		ui.Column(c).Gap(8).Children(func() {
			enabled := ui.Checkbox(c, &panel.Artifact, T("启用 Channel 制品发布")).FontSize(11.5)
			enabled.Disabled(busy)
			a.channelsSaveRow(c, p, "artifact", "保存制品设置", busy)
		})
	})

	a.settingsCard(c, p, "WeChat", "配置 WeChat iLink 凭据路径与工作目录。", func() {
		ui.Column(c).Gap(8).Children(func() {
			enabled := ui.Checkbox(c, &panel.WechatEnabled, T("启用")).FontSize(11.5)
			enabled.Disabled(busy)
			a.channelsLabeledInput(c, p, "channels-wechat-workdir", "工作目录",
				"例如 /home/user/wechat-workspace", &panel.WechatWorkDir, false, busy)
			autoTyping := ui.Checkbox(c, &panel.WechatAutoTyping, T("自动输入（autoTyping）")).FontSize(11.5)
			autoTyping.Disabled(busy)
			a.channelsSecretField(c, p, "channels-wechat-credential", "凭据文件路径",
				panel.WechatCredentialConfigured, "凭据已配置（留空保持）", "未配置凭据",
				"输入新的凭据文件路径…", &panel.WechatCredential, busy)
			clear := ui.Checkbox(c, &panel.WechatClearCredential, T("清除凭据")).FontSize(11.5)
			clear.Disabled(busy)
			ui.Text(c, T("凭据只写入服务端，不会回显或保存在 Native 状态输出中。")).
				FontSize(9.5).TextColor(p.t.TextMuted)
			a.channelsSaveRow(c, p, "wechat", "保存 WeChat 配置", busy)
		})
	})

	a.settingsCard(c, p, "Feishu", "配置 Feishu（Lark）应用 ID、密钥与工作目录。", func() {
		ui.Column(c).Gap(8).Children(func() {
			enabled := ui.Checkbox(c, &panel.FeishuEnabled, T("启用")).FontSize(11.5)
			enabled.Disabled(busy)
			a.channelsLabeledInput(c, p, "channels-feishu-workdir", "工作目录",
				"例如 /home/user/feishu-workspace", &panel.FeishuWorkDir, false, busy)
			a.channelsSecretField(c, p, "channels-feishu-appid", "App ID",
				panel.FeishuAppIDConfigured, "App ID 已配置（留空保持）", "未配置 App ID",
				"输入新的 App ID…", &panel.FeishuAppID, busy)
			clearAppID := ui.Checkbox(c, &panel.FeishuClearAppID, T("清除 App ID")).FontSize(11.5)
			clearAppID.Disabled(busy)
			a.channelsSecretField(c, p, "channels-feishu-appsecret", "App Secret",
				panel.FeishuAppSecretConfigured, "App Secret 已配置（留空保持）", "未配置 App Secret",
				"输入新的 App Secret…", &panel.FeishuAppSecret, busy)
			clearSecret := ui.Checkbox(c, &panel.FeishuClearSecret, T("清除 App Secret")).FontSize(11.5)
			clearSecret.Disabled(busy)
			ui.Text(c, T("App ID 与 App Secret 只写入服务端，不会回显或保存在 Native 状态输出中。")).
				FontSize(9.5).TextColor(p.t.TextMuted)
			a.channelsSaveRow(c, p, "feishu", "保存 Feishu 配置", busy)
		})
	})

	if a.deliveriesCapable() {
		a.settingsChannelDeliveriesCard(c, p)
	}
}

func (a *nativeApp) channelsLabeledInput(c *ui.Context, p palette, key, label, placeholder string, value *string, password, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Placeholder(T(placeholder)).Background(p.t.Background).Radius(8).Border(1, p.t.Border).
			Label(T(label))
		if password {
			input.Password()
		}
		input.Disabled(disabled)
	})
}

// channelsSecretField renders a write-only credential input. The configured
// flag comes from the server, while the input is always empty unless the user
// is currently typing a replacement; a stored secret is never rendered.
func (a *nativeApp) channelsSecretField(c *ui.Context, p palette, key, label string, configured bool, configuredText, unsetText, placeholder string, value *string, busy bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			ui.Text(c, T(label)).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
			status := channelsConfiguredLabel(configured, configuredText, unsetText)
			ui.Text(c, "["+T(status)+"]").FontSize(9.5).TextColor(p.t.TextMuted)
		})
		a.channelsLabeledInput(c, p, key, label, placeholder, value, true, busy)
	})
}

func (a *nativeApp) channelsSaveRow(c *ui.Context, p palette, section, label string, disabled bool) {
	ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if a.channels.Saving == section {
			statusPill(c, p, "running")
			ui.Text(c, T("保存中…")).FontSize(10.5).TextColor(p.t.TextMuted)
		}
		save := ui.PrimaryButton(c.Key("channels-save:"+section), T(label))
		save.Label(T(label))
		save.Disabled(disabled)
		if save.Clicked() && !disabled {
			a.saveChannelsSection(section)
		}
	})
}

func (a *nativeApp) channelsErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("消息通道管理不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.channelsCapable() {
			if ui.Button(c.Key("channels-retry"), T("重试")).Label(T("重试加载消息通道")).Clicked() {
				a.channels.Loaded = false
				a.startChannelsPanelLoad()
			}
		}
	})
}

func (a *nativeApp) channelsStatusBanner(c *ui.Context, p palette) {
	panel := &a.channels
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
		} else if panel.Saving != "" {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(panel.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

// channelsConfiguredLabel is a small helper used by tests and the view to keep
// the configured/unset wording consistent without exposing a stored value.
func channelsConfiguredLabel(configured bool, configuredText, unsetText string) string {
	if configured {
		return configuredText
	}
	return unsetText
}
