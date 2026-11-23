package env

import (
	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the Native equivalent of Desktop's EnvPanel. It is
// capability-gated by manageEnv and only calls mothx/manage/env/get and
// mothx/manage/env/patch. Stored values are never returned by ACP, so each row
// shows a masked, empty input whose placeholder says a value is configured.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "环境变量", "当前运行时未公开环境变量管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageEnv；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取环境变量…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		ui.Text(c, i18n.T("正在刷新环境变量…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	ui.Text(c, i18n.T("环境变量持久化在全局 env.json；ACP 只返回变量名，已保存的值不会回显。输入新值即可替换，留空不会改动已保存的值。")).
		FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)

	busy := p.Saving || p.Loading
	names := p.VisibleNames()

	kit.SettingsCard(c, pal, "已配置变量", "值仅可写入，不回显；删除会在保存时提交 unset。", func() {
		ui.Column(c).Gap(8).Children(func() {
			if len(names) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
					ui.Text(c, i18n.T("尚未配置环境变量")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
					ui.Text(c, i18n.T("在下方新增变量后保存即可写入 env.json。")).
						FontSize(10.5).TextColor(pal.T.TextMuted).LineHeight(1.4)
				})
				return
			}
			for _, name := range names {
				p.variableRow(c, pal, name, p.Drafts[name], busy)
			}
		})
	})

	kit.SettingsCard(c, pal, "新增变量", "变量名与值会在本地暂存，点击保存后一次性提交。", func() {
		ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
			ui.Column(c).Gap(3).Width(200).Children(func() {
				ui.Text(c, i18n.T("变量名")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				input := ui.TextInput(c.Key("env-new-name"), &p.NewName).FontSize(11.5).Height(32).
					Padding(0, 8).Placeholder("MY_VARIABLE").Background(pal.T.Background).
					Radius(8).Border(1, pal.T.Border).Label(i18n.T("新变量名"))
				input.Disabled(busy)
			})
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, i18n.T("变量值")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted)
				input := ui.TextInput(c.Key("env-new-value"), &p.NewValue).FontSize(11.5).Height(32).
					Padding(0, 8).Placeholder(i18n.T("变量值，仅提交不回显")).Background(pal.T.Background).
					Radius(8).Border(1, pal.T.Border).Label(i18n.T("新变量值"))
				input.Password().Disabled(busy)
			})
			add := ui.Button(c.Key("env-add"), i18n.T("新增")).Label(i18n.T("暂存新环境变量"))
			add.Disabled(busy)
			if add.Clicked() && !busy {
				p.Add()
			}
			save := ui.PrimaryButton(c.Key("env-save"), i18n.T("保存"))
			save.Label(i18n.T("保存环境变量")).Tooltip(i18n.T("通过 mothx/manage/env/patch 提交 set/unset"))
			save.Disabled(busy)
			if save.Clicked() && !busy {
				p.SaveChanges(h)
			}
		})
	})
}

func (p *Panel) variableRow(c *ui.Context, pal kit.Palette, name string, draft *Draft, busy bool) {
	if draft == nil {
		return
	}
	ui.Row(c).AlignItems(ui.End).Gap(10).Padding(8, 10).Radius(9).
		Background(pal.T.Background).Border(1, pal.T.Border).Children(func() {
		ui.Column(c).Gap(2).Width(190).Children(func() {
			ui.Text(c, name).FontSize(11.5).FontWeight(650).TextColor(pal.T.Text).SingleLine()
			if draft.IsNew {
				kit.Badge(c, pal, "待新增")
			} else {
				kit.Badge(c, pal, "已配置")
			}
		})
		ui.Column(c).Gap(3).Grow(1).Children(func() {
			ui.Text(c, i18n.T("替换值（留空保持原值）")).FontSize(10).TextColor(pal.T.TextMuted)
			input := ui.TextInput(c.Key("env-value:"+name), &draft.Value).FontSize(11.5).Height(32).
				Padding(0, 8).Placeholder(i18n.T("已配置，不显示原值")).Background(pal.T.Background).
				Radius(8).Border(1, pal.T.Border).Label(i18n.T("环境变量 ") + name + i18n.T(" 的值"))
			input.Password()
			input.Disabled(busy)
			if input.Changed() {
				draft.Touched = true
			}
		})
		remove := ui.Button(c.Key("env-remove:"+name), i18n.T("删除")).Label(i18n.T("删除环境变量 ") + name)
		remove.Disabled(busy)
		if remove.Clicked() && !busy {
			p.Remove(name)
		}
	})
}

func (p *Panel) errCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("环境变量管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("env-retry"), i18n.T("重试")).Label(i18n.T("重试加载环境变量")).Clicked() {
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
