package channels

import (
	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// settingsChannelsPane renders the Native equivalent of the Desktop
// ChannelsPanel inside the Settings View. It is capability-gated by
// manageChannels and only calls mothx/manage/channels/get and
// mothx/manage/channels/patch. It never reads serve.json directly, starts a
// channel, or logs in.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "消息通道", "当前运行时未公开消息通道管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageChannels；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取消息通道…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		ui.Text(c, i18n.T("正在刷新消息通道…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	ui.Text(c, i18n.T("这些设置仅保存到全局 serve.json，不会启动或登录任何渠道。")).
		FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)

	busy := p.Loading || p.Saving != ""

	kit.SettingsCard(c, pal, "Channel 制品", "控制 WeChat 和 Feishu 会话是否可发布并发送生成的制品。", func() {
		ui.Column(c).Gap(8).Children(func() {
			enabled := ui.Checkbox(c, &p.Artifact, i18n.T("启用 Channel 制品发布")).FontSize(11.5)
			enabled.Disabled(busy)
			p.saveRow(h, c, pal, "artifact", "保存制品设置", busy)
		})
	})

	kit.SettingsCard(c, pal, "WeChat", "配置 WeChat iLink 凭据路径与工作目录。", func() {
		ui.Column(c).Gap(8).Children(func() {
			enabled := ui.Checkbox(c, &p.WechatEnabled, i18n.T("启用")).FontSize(11.5)
			enabled.Disabled(busy)
			p.labeledInput(c, pal, "channels-wechat-workdir", "工作目录",
				"例如 /home/user/wechat-workspace", &p.WechatWorkDir, false, busy)
			autoTyping := ui.Checkbox(c, &p.WechatAutoTyping, i18n.T("自动输入（autoTyping）")).FontSize(11.5)
			autoTyping.Disabled(busy)
			p.secretField(c, pal, "channels-wechat-credential", "凭据文件路径",
				p.WechatCredentialConfigured, "凭据已配置（留空保持）", "未配置凭据",
				"输入新的凭据文件路径…", &p.WechatCredential, busy)
			clear := ui.Checkbox(c, &p.WechatClearCredential, i18n.T("清除凭据")).FontSize(11.5)
			clear.Disabled(busy)
			ui.Text(c, i18n.T("凭据只写入服务端，不会回显或保存在 Native 状态输出中。")).
				FontSize(9.5).TextColor(pal.T.TextMuted)
			p.saveRow(h, c, pal, "wechat", "保存 WeChat 配置", busy)
		})
	})

	kit.SettingsCard(c, pal, "Feishu", "配置 Feishu（Lark）应用 ID、密钥与工作目录。", func() {
		ui.Column(c).Gap(8).Children(func() {
			enabled := ui.Checkbox(c, &p.FeishuEnabled, i18n.T("启用")).FontSize(11.5)
			enabled.Disabled(busy)
			p.labeledInput(c, pal, "channels-feishu-workdir", "工作目录",
				"例如 /home/user/feishu-workspace", &p.FeishuWorkDir, false, busy)
			p.secretField(c, pal, "channels-feishu-appid", "App ID",
				p.FeishuAppIDConfigured, "App ID 已配置（留空保持）", "未配置 App ID",
				"输入新的 App ID…", &p.FeishuAppID, busy)
			clearAppID := ui.Checkbox(c, &p.FeishuClearAppID, i18n.T("清除 App ID")).FontSize(11.5)
			clearAppID.Disabled(busy)
			p.secretField(c, pal, "channels-feishu-appsecret", "App Secret",
				p.FeishuAppSecretConfigured, "App Secret 已配置（留空保持）", "未配置 App Secret",
				"输入新的 App Secret…", &p.FeishuAppSecret, busy)
			clearSecret := ui.Checkbox(c, &p.FeishuClearSecret, i18n.T("清除 App Secret")).FontSize(11.5)
			clearSecret.Disabled(busy)
			ui.Text(c, i18n.T("App ID 与 App Secret 只写入服务端，不会回显或保存在 Native 状态输出中。")).
				FontSize(9.5).TextColor(pal.T.TextMuted)
			p.saveRow(h, c, pal, "feishu", "保存 Feishu 配置", busy)
		})
	})

	if p.DeliveriesCapable(h) {
		p.DeliveriesCard(h, c, pal)
	}
}

func (p *Panel) labeledInput(c *ui.Context, pal kit.Palette, key, label, placeholder string, value *string, password, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Placeholder(i18n.T(placeholder)).Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
			Label(i18n.T(label))
		if password {
			input.Password()
		}
		input.Disabled(disabled)
	})
}

// channelsSecretField renders a write-only credential input. The configured
// flag comes from the server, while the input is always empty unless the user
// is currently typing a replacement; a stored secret is never rendered.
func (p *Panel) secretField(c *ui.Context, pal kit.Palette, key, label string, configured bool, configuredText, unsetText, placeholder string, value *string, busy bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
			status := ConfiguredLabel(configured, configuredText, unsetText)
			ui.Text(c, "["+i18n.T(status)+"]").FontSize(9.5).TextColor(pal.T.TextMuted)
		})
		p.labeledInput(c, pal, key, label, placeholder, value, true, busy)
	})
}

func (p *Panel) saveRow(h Host, c *ui.Context, pal kit.Palette, section, label string, disabled bool) {
	ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if p.Saving == section {
			kit.StatusPill(c, pal, "running")
			ui.Text(c, i18n.T("保存中…")).FontSize(10.5).TextColor(pal.T.TextMuted)
		}
		save := ui.PrimaryButton(c.Key("channels-save:"+section), i18n.T(label))
		save.Label(i18n.T(label))
		save.Disabled(disabled)
		if save.Clicked() && !disabled {
			p.SaveSection(h, section)
		}
	})
}

func (p *Panel) errCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("消息通道管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("channels-retry"), i18n.T("重试")).Label(i18n.T("重试加载消息通道")).Clicked() {
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
		} else if p.Saving != "" {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

// channelsConfiguredLabel is a small helper used by tests and the view to keep
// the configured/unset wording consistent without exposing a stored value.
func ConfiguredLabel(configured bool, configuredText, unsetText string) string {
	if configured {
		return configuredText
	}
	return unsetText
}
