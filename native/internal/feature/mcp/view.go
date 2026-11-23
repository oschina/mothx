package mcp

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the native MCP server manager inside the Settings
// View. It is capability-gated by manageMcp and only calls
// mothx/manage/mcp/list and mothx/manage/mcp/set; the panel never reads
// mcp.json directly.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "MCP", "当前运行时未公开 MCP 管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageMcp；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取 MCP 服务器配置…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}

	if p.Err != "" {
		p.ErrCard(h, c, pal, p.Err)
		if !p.Loaded {
			return
		}
	}
	if p.Status != "" {
		p.StatusBanner(c, pal)
	}
	if p.Loading && p.Loaded {
		ui.Text(c, i18n.T("正在刷新 MCP 服务器配置…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	kit.SettingsCard(c, pal, "MCP 作用域", "全局配置写入 mcp.json；项目配置属于当前会话的工作目录，由 Runtime 校验目录授权。", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			p.ScopeButton(h, c, pal, ScopeGlobal, "全局")
			p.ScopeButton(h, c, pal, ScopeProject, "项目（当前会话）")
			if p.Scope == ScopeProject && h.CurrentSession() == "" {
				ui.Text(c, i18n.T("请先打开一个会话再管理项目 MCP。")).FontSize(10.5).TextColor(pal.T.Warning)
			}
		})
	})

	desc := "全局 mcp.json 服务器列表；保存会通过 mcp/set 完整替换列表。"
	if p.Scope == ScopeProject {
		desc = "当前会话工作目录下的项目 MCP 服务器列表；保存会通过 mcp/set 完整替换列表。"
	}
	kit.SettingsCard(c, pal, "MCP 服务器", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			if p.Path != "" {
				ui.Text(c, i18n.T("配置文件：")+p.Path).FontSize(10).TextColor(pal.T.TextMuted).Selectable()
			}
			if len(p.Servers) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
					ui.Text(c, i18n.T("尚未配置 MCP 服务器")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
					ui.Text(c, i18n.T("新增 stdio、http 或 sse 服务器后保存即可生效。")).FontSize(10.5).TextColor(pal.T.TextMuted)
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					for index := range p.Servers {
						index := index
						p.ServerRow(c, pal, index)
					}
				})
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				add := ui.PrimaryButton(c.Key("mcp-add"), i18n.T("新增服务器"))
				add.Label(i18n.T("新增 MCP 服务器")).Tooltip(i18n.T("打开原生 MCP 服务器编辑对话框"))
				add.Disabled(p.Saving || p.Deleting)
				if add.Clicked() && !p.Saving && !p.Deleting {
					p.OpenNewDraft()
				}
			})
		})
	})
}

func (p *Panel) ErrCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("MCP 管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("mcp-retry"), i18n.T("重试")).Label(i18n.T("重试加载 MCP 服务器")).Clicked() {
				p.Loaded = false
				p.StartLoad(h)
			}
		}
	})
}

func (p *Panel) StatusBanner(c *ui.Context, pal kit.Palette) {
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
		} else if p.Saving || p.Deleting {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) ServerRow(c *ui.Context, pal kit.Palette, index int) {
	if index < 0 || index >= len(p.Servers) {
		return
	}
	server := p.Servers[index]
	enabled := ServerEnabled(server)
	ui.Column(c).Padding(9).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := pal.T.TextMuted
			if enabled {
				color = pal.T.Accent
			}
			ui.Icon(c, icons.IconGlobe).FontSize(15).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, server.Name).FontSize(12).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					kit.Badge(c, pal, ServerType(server))
					if enabled {
						kit.Badge(c, pal, "已启用")
					} else {
						kit.Badge(c, pal, "已停用")
					}
				})
				ui.Text(c, ServerDescription(server)).FontSize(9.5).
					TextColor(pal.T.TextMuted).SingleLine()
			})
			edit := ui.Button(c.Key(fmt.Sprintf("mcp-edit:%d", index)), i18n.T("编辑"))
			edit.Label(i18n.T("编辑 MCP 服务器 ") + server.Name)
			edit.Disabled(p.Saving || p.Deleting)
			if edit.Clicked() && !p.Saving && !p.Deleting {
				p.OpenEditDraft(index)
			}
			remove := ui.Button(c.Key(fmt.Sprintf("mcp-delete:%d", index)), i18n.T("删除"))
			remove.Label(i18n.T("删除 MCP 服务器 ") + server.Name)
			remove.Disabled(p.Saving || p.Deleting)
			if remove.Clicked() && !p.Saving && !p.Deleting {
				p.RequestDelete(index)
			}
		})
	})
}

func ServerDescription(server ServerView) string {
	transport := "stdio"
	switch ServerType(server) {
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

// Dialogs renders the Native add/edit and delete confirmation
// dialogs above the settings page. Neither is a browser or OS prompt.
func (p *Panel) Dialogs(h Host, c *ui.Context, pal kit.Palette) {
	if !h.SettingsOpen() {
		return
	}
	if p.PendingDelete {
		p.DeleteDialog(h, c, pal)
	}
	if p.EditorOpen {
		p.EditorDialog(h, c, pal)
	}
}

func (p *Panel) DeleteDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.PendingDelete, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(430).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconAlert).FontSize(16).TextColor(pal.T.Danger)
				ui.Text(c, i18n.T("删除 MCP 服务器？")).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			name := ""
			if p.PendingDeleteIndex >= 0 && p.PendingDeleteIndex < len(p.Servers) {
				name = p.Servers[p.PendingDeleteIndex].Name
			}
			ui.Text(c, i18n.Tf("将从全局配置中删除 %q。保存会完整替换服务器列表，其余服务器保持不变。", name)).
				FontSize(11.5).TextColor(pal.T.TextMuted).Selectable()
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("mcp-delete-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消删除 MCP 服务器"))
				if cancel.Clicked() {
					p.CancelDelete()
				}
				confirm := ui.Button(c.Key("mcp-delete-confirm"), i18n.T("确认删除"))
				confirm.Label(i18n.T("确认删除 MCP 服务器"))
				confirm.Background(pal.ErrorBg).Border(1, pal.T.Danger)
				confirm.Disabled(p.Deleting || name == "")
				if confirm.Clicked() && !p.Deleting {
					p.ConfirmDelete(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CancelDelete()
	}
}

func (p *Panel) EditorDialog(h Host, c *ui.Context, pal kit.Palette) {
	dialog := ui.DialogBase(c, &p.EditorOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.55))
		dialog.Width(620).Padding(18).Radius(14).Background(pal.T.Background).
			Border(1, pal.T.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, icons.IconGlobe).FontSize(16).TextColor(pal.T.Accent)
				title := "新建 MCP 服务器"
				if p.EditingIndex >= 0 {
					title = "编辑 MCP 服务器"
				}
				ui.Text(c, i18n.T(title)).FontSize(14).FontWeight(650).TextColor(pal.T.Text).Grow(1)
			})
			ui.Text(c, i18n.T("保存会通过 mothx/manage/mcp/set 完整替换全局服务器列表。")).
				FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
			ui.Scroll(c).Height(430).Children(func() {
				ui.Column(c).Gap(10).Children(func() {
					p.LabeledInput(c, pal, "mcp-name", "名称", &p.Draft.Name)
					ui.Column(c).Gap(3).Children(func() {
						ui.Text(c, i18n.T("类型")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
						ui.Select(c.Key("mcp-type"), &p.Draft.Type, mcpServerTypes).
							FontSize(12).Height(32).Padding(0, 8).
							Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
							Label(i18n.T("MCP 传输类型"))
					})
					ui.Checkbox(c, &p.Draft.Enabled, i18n.T("启用该服务器")).FontSize(11.5)
					if p.Draft.Type == "stdio" {
						p.LabeledInput(c, pal, "mcp-command", "命令", &p.Draft.Command)
						p.LabeledInput(c, pal, "mcp-args", "参数（空格分隔）", &p.Draft.ArgsInput)
					} else {
						p.LabeledInput(c, pal, "mcp-url", "URL", &p.Draft.URL)
						if p.Draft.Type == "sse" {
							p.LabeledInput(c, pal, "mcp-message-url", "消息 URL", &p.Draft.MessageURL)
						}
					}
					p.PairEditor(c, pal, "headers", "请求头", "添加请求头", p.Draft.Headers)
					p.PairEditor(c, pal, "env", "环境变量", "添加环境变量", p.Draft.Env)
				})
			})
			if p.DialogErr != "" {
				ui.Text(c, i18n.T(p.DialogErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Button(c.Key("mcp-cancel"), i18n.T("取消"))
				cancel.Label(i18n.T("取消编辑 MCP 服务器"))
				if cancel.Clicked() {
					p.CloseEditor()
				}
				save := ui.PrimaryButton(c.Key("mcp-save"), i18n.T("保存"))
				save.Label(i18n.T("保存 MCP 服务器"))
				save.Disabled(p.Saving)
				if save.Clicked() && !p.Saving {
					p.Save(h)
				}
			})
		})
	})
	if dialog.Dismissed() {
		p.CloseEditor()
	}
}

func (p *Panel) LabeledInput(c *ui.Context, pal kit.Palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Label(i18n.T(label))
	})
}

func (p *Panel) PairEditor(c *ui.Context, pal kit.Palette, kind, label, addLabel string, pairs []PairDraft) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted).Grow(1)
			add := ui.Button(c.Key("mcp-add-"+kind), i18n.T(addLabel))
			add.Label(i18n.T(addLabel))
			if add.Clicked() {
				p.AddPair(kind)
			}
		})
		if len(pairs) == 0 {
			ui.Text(c, i18n.Tf("尚未添加%s。", i18n.T(label))).FontSize(10).TextColor(pal.T.TextMuted)
			return
		}
		for index := range pairs {
			entry := &pairs[index]
			index := index
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.TextInput(c.Key(fmt.Sprintf("mcp-%s-name:%d", kind, index)), &entry.Name).
					Placeholder(i18n.T("名称")).FontSize(11.5).Height(30).Padding(0, 8).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Grow(1).
					Label(i18n.T(label) + " " + i18n.T("名称"))
				ui.TextInput(c.Key(fmt.Sprintf("mcp-%s-value:%d", kind, index)), &entry.Value).
					Placeholder(i18n.T("值")).FontSize(11.5).Height(30).Padding(0, 8).
					Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).Grow(1).
					Label(i18n.T(label) + " " + i18n.T("值"))
				remove := ui.Button(c.Key(fmt.Sprintf("mcp-%s-remove:%d", kind, index)), i18n.T("移除"))
				remove.Label(i18n.T("移除") + " " + i18n.T(label))
				if remove.Clicked() {
					p.RemovePair(kind, index)
				}
			})
		}
	})
}

func (p *Panel) ScopeButton(h Host, c *ui.Context, pal kit.Palette, scope, label string) {
	m := p
	active := m.Scope == scope
	b := ui.ButtonBase(c.Key("mcp-scope:"+scope)).Height(30).Padding(0, 12).Radius(8).
		AlignItems(ui.Center).Justify(ui.Center).Border(1, pal.T.Border)
	if active {
		b.Background(pal.Active)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() {
		color := pal.T.Text
		if active {
			color = pal.T.Accent
		}
		ui.Text(c, i18n.T(label)).FontSize(11).TextColor(color)
	})
	b.Label(i18n.T(label))
	if b.Clicked() && !active {
		atomic.AddInt64(&m.Seq, 1)
		m.Scope = scope
		m.Path = ""
		m.Servers = nil
		m.Loaded = false
		m.Loading = false
		m.Err = ""
		m.Status = ""
		p.StartLoad(h)
	}
}
