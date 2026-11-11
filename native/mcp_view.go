package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsMCPPane renders the native MCP server manager inside the Settings
// View. It is capability-gated by manageMcp and only calls
// mothx/manage/mcp/list and mothx/manage/mcp/set; the panel never reads
// mcp.json directly.
func (a *nativeApp) settingsMCPPane(c *ui.Context, p palette) {
	if !a.mcpCapable() {
		a.settingsCard(c, p, "MCP", "当前运行时未公开 MCP 管理能力。", func() {
			ui.Text(c, "需要 ACP 能力 manageMcp；当前版本不会显示或调用占位表单。").
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.mcp
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, "正在读取 MCP 服务器配置…").FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.mcpErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.mcpStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, "正在刷新 MCP 服务器配置…").FontSize(10.5).TextColor(p.t.TextMuted)
	}

	desc := "全局 mcp.json 服务器列表；保存会通过 mcp/set 完整替换列表。"
	a.settingsCard(c, p, "MCP 服务器", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			if panel.Path != "" {
				ui.Text(c, "配置文件："+panel.Path).FontSize(10).TextColor(p.t.TextMuted).Selectable()
			}
			if len(panel.Servers) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
					ui.Text(c, "尚未配置 MCP 服务器").FontSize(12).FontWeight(600).TextColor(p.t.Text)
					ui.Text(c, "新增 stdio、http 或 sse 服务器后保存即可生效。").FontSize(10.5).TextColor(p.t.TextMuted)
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					for index := range panel.Servers {
						index := index
						a.mcpServerRow(c, p, index)
					}
				})
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				add := ui.PrimaryButton(c.Key("mcp-add"), "新增服务器")
				add.Label("新增 MCP 服务器").Tooltip("打开原生 MCP 服务器编辑对话框")
				add.Disabled(panel.Saving || panel.Deleting)
				if add.Clicked() && !panel.Saving && !panel.Deleting {
					a.openMCPNewDraft()
				}
			})
		})
	})
}

func (a *nativeApp) mcpErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, "MCP 管理不可用").FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, message).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.mcpCapable() {
			if ui.Button(c.Key("mcp-retry"), "重试").Label("重试加载 MCP 服务器").Clicked() {
				a.mcp.Loaded = false
				a.startMCPPanelLoad()
			}
		}
	})
}

func (a *nativeApp) mcpStatusBanner(c *ui.Context, p palette) {
	panel := &a.mcp
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
		} else if panel.Saving || panel.Deleting {
			statusPill(c, p, "running")
		}
		ui.Text(c, panel.Status).FontSize(11).TextColor(color).Grow(1)
	})
}

func (a *nativeApp) mcpServerRow(c *ui.Context, p palette, index int) {
	panel := &a.mcp
	if index < 0 || index >= len(panel.Servers) {
		return
	}
	server := panel.Servers[index]
	enabled := mcpServerEnabled(server)
	ui.Column(c).Padding(9).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := p.t.TextMuted
			if enabled {
				color = p.t.Accent
			}
			ui.Icon(c, iconGlobe).FontSize(15).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, server.Name).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					badge(c, p, mcpServerType(server))
					if enabled {
						badge(c, p, "已启用")
					} else {
						badge(c, p, "已停用")
					}
				})
				ui.Text(c, mcpServerDescription(server)).FontSize(9.5).
					TextColor(p.t.TextMuted).SingleLine()
			})
			edit := ui.Button(c.Key(fmt.Sprintf("mcp-edit:%d", index)), "编辑")
			edit.Label("编辑 MCP 服务器 " + server.Name)
			edit.Disabled(panel.Saving || panel.Deleting)
			if edit.Clicked() && !panel.Saving && !panel.Deleting {
				a.openMCPEditDraft(index)
			}
			remove := ui.Button(c.Key(fmt.Sprintf("mcp-delete:%d", index)), "删除")
			remove.Label("删除 MCP 服务器 " + server.Name)
			remove.Disabled(panel.Saving || panel.Deleting)
			if remove.Clicked() && !panel.Saving && !panel.Deleting {
				a.requestDeleteMCP(index)
			}
		})
	})
}

func mcpServerDescription(server mcpServerView) string {
	transport := "stdio"
	switch mcpServerType(server) {
	case "http":
		transport = "http: " + server.URL
	case "sse":
		transport = "sse: " + server.URL
		if server.MessageURL != "" {
			transport += " → " + server.MessageURL
		}
	default:
		command := strings.TrimSpace(strings.Join(append([]string{server.Command}, server.Args...), " "))
		transport = "stdio: " + command
	}
	parts := []string{transport}
	if len(server.Env) > 0 {
		parts = append(parts, fmt.Sprintf("env: %d", len(server.Env)))
	}
	if len(server.Headers) > 0 {
		parts = append(parts, fmt.Sprintf("headers: %d", len(server.Headers)))
	}
	return strings.Join(parts, " · ")
}

// settingsMCPDialogs renders the Native add/edit and delete confirmation
// dialogs above the settings page. Neither is a browser or OS prompt.
func (a *nativeApp) settingsMCPDialogs(c *ui.Context, p palette) {
	if !a.settingsOpen {
		return
	}
	panel := &a.mcp
	if panel.PendingDelete {
		a.mcpDeleteDialog(c, p)
	}
	if panel.EditorOpen {
		a.mcpEditorDialog(c, p)
	}
}

func (a *nativeApp) mcpDeleteDialog(c *ui.Context, p palette) {
	panel := &a.mcp
	dialog := ui.DialogBase(c, &panel.PendingDelete, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconAlert).FontSize(16).TextColor(p.t.Danger)
				ui.Text(c, "删除 MCP 服务器？").FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			name := ""
			if panel.PendingDeleteIndex >= 0 && panel.PendingDeleteIndex < len(panel.Servers) {
				name = panel.Servers[panel.PendingDeleteIndex].Name
			}
			ui.Text(c, fmt.Sprintf("将从全局配置中删除 %q。保存会完整替换服务器列表，其余服务器保持不变。", name)).
				FontSize(11.5).TextColor(p.t.TextMuted).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("mcp-delete-cancel"), "取消")
				cancel.Label("取消删除 MCP 服务器")
				if cancel.Clicked() {
					a.cancelDeleteMCP()
				}
				confirm := ui.Button(c.Key("mcp-delete-confirm"), "确认删除")
				confirm.Label("确认删除 MCP 服务器")
				confirm.Background(p.errorBg).Border(1, p.t.Danger)
				confirm.Disabled(panel.Deleting || name == "")
				if confirm.Clicked() && !panel.Deleting {
					a.confirmDeleteMCP()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.cancelDeleteMCP()
	}
}

func (a *nativeApp) mcpEditorDialog(c *ui.Context, p palette) {
	panel := &a.mcp
	dialog := ui.DialogBase(c, &panel.EditorOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(620).Padding(18).Radius(14).Background(p.t.Background).
			Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconGlobe).FontSize(16).TextColor(p.t.Accent)
				title := "新建 MCP 服务器"
				if panel.EditingIndex >= 0 {
					title = "编辑 MCP 服务器"
				}
				ui.Text(c, title).FontSize(14).FontWeight(650).TextColor(p.t.Text).Grow(1)
			})
			ui.Text(c, "保存会通过 mothx/manage/mcp/set 完整替换全局服务器列表。").
				FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
			ui.Scroll(c).Height(430).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					a.mcpLabeledInput(c, p, "mcp-name", "名称", &panel.Draft.Name)
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, "类型").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
						ui.Select(c.Key("mcp-type"), &panel.Draft.Type, mcpServerTypes).
							FontSize(12).Height(32).Padding(0, 8).
							Background(p.t.Background).Radius(8).Border(1, p.t.Border).
							Label("MCP 传输类型")
					})
					ui.Checkbox(c, &panel.Draft.Enabled, "启用该服务器").FontSize(11.5)
					if panel.Draft.Type == "stdio" {
						a.mcpLabeledInput(c, p, "mcp-command", "命令", &panel.Draft.Command)
						a.mcpLabeledInput(c, p, "mcp-args", "参数（空格分隔）", &panel.Draft.ArgsInput)
					} else {
						a.mcpLabeledInput(c, p, "mcp-url", "URL", &panel.Draft.URL)
						if panel.Draft.Type == "sse" {
							a.mcpLabeledInput(c, p, "mcp-message-url", "消息 URL", &panel.Draft.MessageURL)
						}
					}
					a.mcpPairEditor(c, p, "headers", "请求头", "添加请求头", panel.Draft.Headers)
					a.mcpPairEditor(c, p, "env", "环境变量", "添加环境变量", panel.Draft.Env)
				})
			})
			if panel.DialogErr != "" {
				ui.Text(c, panel.DialogErr).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("mcp-cancel"), "取消")
				cancel.Label("取消编辑 MCP 服务器")
				if cancel.Clicked() {
					a.closeMCPEditor()
				}
				save := ui.PrimaryButton(c.Key("mcp-save"), "保存")
				save.Label("保存 MCP 服务器")
				save.Disabled(panel.Saving)
				if save.Clicked() && !panel.Saving {
					a.saveMCPDraft()
				}
			})
		})
	})
	if dialog.Dismissed() {
		a.closeMCPEditor()
	}
}

func (a *nativeApp) mcpLabeledInput(c *ui.Context, p palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.t.Background).Radius(8).Border(1, p.t.Border).Label(label)
	})
}

func (a *nativeApp) mcpPairEditor(c *ui.Context, p palette, kind, label, addLabel string, pairs []mcpPairDraft) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, label).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted).Grow(1)
			add := ui.Button(c.Key("mcp-add-"+kind), addLabel)
			add.Label(addLabel)
			if add.Clicked() {
				a.addMCPPair(kind)
			}
		})
		if len(pairs) == 0 {
			ui.Text(c, "尚未添加"+label+"。").FontSize(10).TextColor(p.t.TextMuted)
			return
		}
		for index := range pairs {
			entry := &pairs[index]
			index := index
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.TextInput(c.Key(fmt.Sprintf("mcp-%s-name:%d", kind, index)), &entry.Name).
					Placeholder("名称").FontSize(11.5).Height(30).Padding(0, 8).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border).Grow(1).
					Label(label + "名称")
				ui.TextInput(c.Key(fmt.Sprintf("mcp-%s-value:%d", kind, index)), &entry.Value).
					Placeholder("值").FontSize(11.5).Height(30).Padding(0, 8).
					Background(p.t.Background).Radius(8).Border(1, p.t.Border).Grow(1).
					Label(label + "值")
				remove := ui.Button(c.Key(fmt.Sprintf("mcp-%s-remove:%d", kind, index)), "移除")
				remove.Label("移除" + label)
				if remove.Clicked() {
					a.removeMCPPair(kind, index)
				}
			})
		}
	})
}
