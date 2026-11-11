package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsACPWorkersPane renders the Native equivalent of the Desktop
// AcpWorkersPanel. It is capability-gated by manageACPProfiles and only calls
// mothx/manage/acp/profiles/list and mothx/manage/acp/profiles/patch.
//
// The editor intentionally exposes only behavior metadata: default model,
// driver, MCP bridge intent, and capability tightening. Argv, URLs,
// environment names, and trust flags remain operator-owned settings.json.
func (a *nativeApp) settingsACPWorkersPane(c *ui.Context, p palette) {
	if !a.acpWorkersCapable() {
		a.settingsCard(c, p, "ACP 工作器", "当前运行时未公开 ACP 工作器配置管理能力。", func() {
			ui.Text(c, "需要 ACP 能力 manageACPProfiles；当前版本不会显示或调用占位表单。").
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.acpWorkers
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, "正在读取 ACP 工作器配置…").FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.acpWorkersErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.acpWorkersStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, "正在刷新 ACP 工作器配置…").FontSize(10.5).TextColor(p.t.TextMuted)
	}

	ui.Text(c, "只编辑外部 ACP Agent 的默认模型、驱动、MCP 桥接与能力收紧；argv、URL、环境变量与信任标记仍由 settings.json 管理。").
		FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)

	if len(panel.Profiles) == 0 {
		a.settingsCard(c, p, "ACP 工作器", "尚未配置外部 ACP 工作器。", func() {
			ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
				ui.Text(c, "没有可编辑的 ACP 工作器").FontSize(12).FontWeight(600).TextColor(p.t.Text)
				ui.Text(c, "请在 settings.json 中新增 ACP Agent profile 后重新打开此面板。").
					FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			})
		})
		return
	}

	a.settingsCard(c, p, "ACP 工作器", "每个配置文件的编辑仅提交 model、driver、mcpBridge 与 capabilityOverrides。", func() {
		ui.Column(c).Gap(8).Children(func() {
			for i := range panel.Profiles {
				profile := panel.Profiles[i]
				a.acpWorkerProfileRow(c, p, profile)
			}
		})
	})
}

func (a *nativeApp) acpWorkerProfileRow(c *ui.Context, p palette, profile acpWorkerProfileView) {
	panel := &a.acpWorkers
	draft := panel.Drafts[profile.ID]
	if draft == nil {
		fresh := acpWorkerDraftFromProfile(profile)
		draft = &fresh
		panel.Drafts[profile.ID] = draft
	}
	busy := panel.SavingID != ""
	saving := panel.SavingID == profile.ID
	enabled := profile.Enabled
	accent := p.t.TextMuted
	if enabled {
		accent = p.t.Accent
	}

	ui.Column(c).Padding(10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(9).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconCpu).FontSize(15).TextColor(accent)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					title := strings.TrimSpace(profile.Title)
					if title == "" {
						title = profile.ID
					}
					ui.Text(c, title).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					badge(c, p, profile.ID)
					if transport := strings.TrimSpace(profile.Transport); transport != "" {
						badge(c, p, transport)
					}
					if enabled {
						badge(c, p, "已启用")
					} else {
						badge(c, p, "已停用")
					}
					if profile.RouterEligible {
						badge(c, p, "Router 可用")
					}
				})
				if profile.InvalidReason != "" {
					ui.Text(c, "配置无效："+profile.InvalidReason).FontSize(9.5).
						TextColor(p.t.Danger).SingleLine()
				}
			})
			if saving {
				statusPill(c, p, "running")
			}
		})

		ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
			a.acpWorkerField(c, p, "acp-worker-model:"+profile.ID, "默认模型", "", &draft.Model, 150, busy)
			a.acpWorkerField(c, p, "acp-worker-driver:"+profile.ID, "驱动", "generic-acp", &draft.Driver, 140, busy)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, "MCP 桥接").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				selectEl := ui.Select(c.Key("acp-worker-bridge:"+profile.ID), &draft.MCPBridge, acpWorkerMCPBridges).
					FontSize(11.5).Height(32).Padding(0, 8).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border).
					Label("MCP 桥接模式")
				selectEl.Disabled(busy)
			})
			save := ui.PrimaryButton(c.Key("acp-worker-save:"+profile.ID), "保存")
			save.Label("保存 ACP 工作器 " + profile.ID)
			save.Tooltip("仅提交模型、驱动、MCP 桥接与能力收紧")
			save.Disabled(busy || panel.Loading)
			if save.Clicked() && !busy && !panel.Loading {
				a.saveACPWorkerProfile(profile.ID)
			}
		})

		if len(panel.CapabilityNames) > 0 {
			ui.Column(c).Gap(5).Children(func() {
				ui.Text(c, "收紧能力（勾选后禁用该能力）").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				for start := 0; start < len(panel.CapabilityNames); start += 4 {
					end := start + 4
					if end > len(panel.CapabilityNames) {
						end = len(panel.CapabilityNames)
					}
					names := panel.CapabilityNames[start:end]
					ui.Row(c).AlignItems(ui.Center).Gap(14).Children(func() {
						for _, name := range names {
							name := name
							checked := draft.Overrides[name]
							box := ui.Checkbox(c.Key(fmt.Sprintf("acp-worker-cap:%s:%s", profile.ID, name)), &checked, name).
								FontSize(10.5)
							box.Disabled(busy)
							if !busy && box.Changed() {
								a.toggleACPWorkerCapability(profile.ID, name)
							}
						}
					})
				}
			})
		}
	})
}

func (a *nativeApp) acpWorkerField(c *ui.Context, p palette, key, label, placeholder string, value *string, width float32, disabled bool) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		input := ui.TextInput(c.Key(key), value).FontSize(11.5).Height(32).Padding(0, 8).
			Width(width).Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(label)
		if placeholder != "" {
			input.Placeholder(placeholder)
		}
		input.Disabled(disabled)
	})
}

func (a *nativeApp) acpWorkersErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, "ACP 工作器管理不可用").FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, message).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.acpWorkersCapable() {
			if ui.Button(c.Key("acp-workers-retry"), "重试").Label("重试加载 ACP 工作器").Clicked() {
				a.acpWorkers.Loaded = false
				a.startACPWorkersPanelLoad()
			}
		}
	})
}

func (a *nativeApp) acpWorkersStatusBanner(c *ui.Context, p palette) {
	panel := &a.acpWorkers
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
		} else if panel.SavingID != "" {
			statusPill(c, p, "running")
		}
		ui.Text(c, panel.Status).FontSize(11).TextColor(color).Grow(1)
	})
}
