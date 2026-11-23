package acpworkers

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// settingsACPWorkersPane renders the Native equivalent of the Desktop
// AcpWorkersPanel. It is capability-gated by manageACPProfiles and only calls
// mothx/manage/acp/profiles/list and mothx/manage/acp/profiles/patch.
//
// The editor intentionally exposes only behavior metadata: default model,
// driver, MCP bridge intent, and capability tightening. Argv, URLs,
// environment names, and trust flags remain operator-owned settings.json.
func (p *Panel) View(h host.Base, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "ACP 工作器", "当前运行时未公开 ACP 工作器配置管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageACPProfiles；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取 ACP 工作器配置…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		ui.Text(c, i18n.T("正在刷新 ACP 工作器配置…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	ui.Text(c, i18n.T("只编辑外部 ACP Agent 的默认模型、驱动、MCP 桥接与能力收紧；argv、URL、环境变量与信任标记仍由 settings.json 管理。")).
		FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)

	if len(p.Profiles) == 0 {
		kit.SettingsCard(c, pal, "ACP 工作器", "尚未配置外部 ACP 工作器。", func() {
			ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
				ui.Text(c, i18n.T("没有可编辑的 ACP 工作器")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
				ui.Text(c, i18n.T("请在 settings.json 中新增 ACP Agent profile 后重新打开此面板。")).
					FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			})
		})
		return
	}

	kit.SettingsCard(c, pal, "ACP 工作器", "每个配置文件的编辑仅提交 model、driver、mcpBridge 与 capabilityOverrides。", func() {
		ui.Column(c).Gap(8).Children(func() {
			for i := range p.Profiles {
				profile := p.Profiles[i]
				p.profileRow(h, c, pal, profile)
			}
		})
	})
}

func (p *Panel) profileRow(h host.Base, c *ui.Context, pal kit.Palette, profile ProfileView) {
	draft := p.Drafts[profile.ID]
	if draft == nil {
		fresh := DraftFromProfile(profile)
		draft = &fresh
		p.Drafts[profile.ID] = draft
	}
	busy := p.SavingID != ""
	saving := p.SavingID == profile.ID
	enabled := profile.Enabled
	accent := pal.T.TextMuted
	if enabled {
		accent = pal.T.Accent
	}

	ui.Column(c).Padding(10).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(9).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconCpu).FontSize(15).TextColor(accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					title := strings.TrimSpace(profile.Title)
					if title == "" {
						title = profile.ID
					}
					ui.Text(c, title).FontSize(12).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					kit.Badge(c, pal, profile.ID)
					if transport := strings.TrimSpace(profile.Transport); transport != "" {
						kit.Badge(c, pal, transport)
					}
					if enabled {
						kit.Badge(c, pal, "已启用")
					} else {
						kit.Badge(c, pal, "已停用")
					}
					if profile.RouterEligible {
						kit.Badge(c, pal, "Router 可用")
					}
				})
				if profile.InvalidReason != "" {
					ui.Text(c, i18n.T("配置无效：")+profile.InvalidReason).FontSize(9.5).
						TextColor(pal.T.Danger).SingleLine()
				}
			})
			if saving {
				kit.StatusPill(c, pal, "running")
			}
		})

		ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
			p.field(c, pal, "acp-worker-model:"+profile.ID, "默认模型", "", &draft.Model, 150, busy)
			p.field(c, pal, "acp-worker-driver:"+profile.ID, "驱动", "generic-acp", &draft.Driver, 140, busy)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, i18n.T("MCP 桥接")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				selectEl := ui.Select(c.Key("acp-worker-bridge:"+profile.ID), &draft.MCPBridge, MCPBridges).
					FontSize(11.5).Height(32).Padding(0, 8).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
					Label(i18n.T("MCP 桥接模式"))
				selectEl.Disabled(busy)
			})
			save := ui.PrimaryButton(c.Key("acp-worker-save:"+profile.ID), i18n.T("保存"))
			save.Label(i18n.T("保存 ACP 工作器 ") + profile.ID)
			save.Tooltip(i18n.T("仅提交模型、驱动、MCP 桥接与能力收紧"))
			save.Disabled(busy || p.Loading)
			if save.Clicked() && !busy && !p.Loading {
				p.SaveProfile(h, profile.ID)
			}
		})

		if len(p.CapabilityNames) > 0 {
			ui.Column(c).Gap(5).Children(func() {
				ui.Text(c, i18n.T("收紧能力（勾选后禁用该能力）")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				for start := 0; start < len(p.CapabilityNames); start += 4 {
					end := start + 4
					if end > len(p.CapabilityNames) {
						end = len(p.CapabilityNames)
					}
					names := p.CapabilityNames[start:end]
					ui.Row(c).AlignItems(ui.Center).Gap(14).Children(func() {
						for _, name := range names {
							name := name
							checked := draft.Overrides[name]
							box := ui.Checkbox(c.Key(fmt.Sprintf("acp-worker-cap:%s:%s", profile.ID, name)), &checked, name).
								FontSize(10.5)
							box.Disabled(busy)
							if !busy && box.Changed() {
								p.ToggleCapability(profile.ID, name)
							}
						}
					})
				}
			})
		}
	})
}

func (p *Panel) field(c *ui.Context, pal kit.Palette, key, label, placeholder string, value *string, width float32, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(11.5).Height(32).Padding(0, 8).
			Width(width).Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(label)
		if placeholder != "" {
			input.Placeholder(placeholder)
		}
		input.Disabled(disabled)
	})
}

func (p *Panel) errCard(h host.Base, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("ACP 工作器管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("acp-workers-retry"), i18n.T("重试")).Label(i18n.T("重试加载 ACP 工作器")).Clicked() {
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
		} else if p.SavingID != "" {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}
