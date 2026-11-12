package main

import (
	"github.com/egoist/mygo/ui"
)

// settingsEnvPane renders the Native equivalent of Desktop's EnvPanel. It is
// capability-gated by manageEnv and only calls mothx/manage/env/get and
// mothx/manage/env/patch. Stored values are never returned by ACP, so each row
// shows a masked, empty input whose placeholder says a value is configured.
func (a *nativeApp) settingsEnvPane(c *ui.Context, p palette) {
	if !a.envCapable() {
		a.settingsCard(c, p, "环境变量", "当前运行时未公开环境变量管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageEnv；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.env
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取环境变量…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.envErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.envStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, T("正在刷新环境变量…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	ui.Text(c, T("环境变量持久化在全局 env.json；ACP 只返回变量名，已保存的值不会回显。输入新值即可替换，留空不会改动已保存的值。")).
		FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)

	busy := panel.Saving || panel.Loading
	names := panel.visibleEnvNames()

	a.settingsCard(c, p, "已配置变量", "值仅可写入，不回显；删除会在保存时提交 unset。", func() {
		ui.Column(c).Gap(8).Children(func() {
			if len(names) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
					ui.Text(c, T("尚未配置环境变量")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
					ui.Text(c, T("在下方新增变量后保存即可写入 env.json。")).
						FontSize(10.5).TextColor(p.t.TextMuted).LineHeight(1.4)
				})
				return
			}
			for _, name := range names {
				a.envVariableRow(c, p, name, panel.Drafts[name], busy)
			}
		})
	})

	a.settingsCard(c, p, "新增变量", "变量名与值会在本地暂存，点击保存后一次性提交。", func() {
		ui.Row(c).AlignItems(ui.End).Gap(10).Children(func() {
			ui.Column(c).Gap(3).Width(200).Children(func() {
				ui.Text(c, T("变量名")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				input := ui.TextInput(c.Key("env-new-name"), &panel.NewName).FontSize(11.5).Height(32).
					Padding(0, 8).Placeholder("MY_VARIABLE").Background(p.t.Background).
					Radius(8).Border(1, p.t.Border).Label(T("新变量名"))
				input.Disabled(busy)
			})
			ui.Column(c).Gap(3).Grow(1).Children(func() {
				ui.Text(c, T("变量值")).FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted)
				input := ui.TextInput(c.Key("env-new-value"), &panel.NewValue).FontSize(11.5).Height(32).
					Padding(0, 8).Placeholder(T("变量值，仅提交不回显")).Background(p.t.Background).
					Radius(8).Border(1, p.t.Border).Label(T("新变量值"))
				input.Password().Disabled(busy)
			})
			add := ui.Button(c.Key("env-add"), T("新增")).Label(T("暂存新环境变量"))
			add.Disabled(busy)
			if add.Clicked() && !busy {
				a.addEnvVariable()
			}
			save := ui.PrimaryButton(c.Key("env-save"), T("保存"))
			save.Label(T("保存环境变量")).Tooltip(T("通过 mothx/manage/env/patch 提交 set/unset"))
			save.Disabled(busy)
			if save.Clicked() && !busy {
				a.saveEnvChanges()
			}
		})
	})
}

func (a *nativeApp) envVariableRow(c *ui.Context, p palette, name string, draft *envDraft, busy bool) {
	if draft == nil {
		return
	}
	ui.Row(c).AlignItems(ui.End).Gap(10).Padding(8, 10).Radius(9).
		Background(p.t.Background).Border(1, p.t.Border).Children(func() {
		ui.Column(c).Gap(2).Width(190).Children(func() {
			ui.Text(c, name).FontSize(11.5).FontWeight(650).TextColor(p.t.Text).SingleLine()
			if draft.IsNew {
				badge(c, p, "待新增")
			} else {
				badge(c, p, "已配置")
			}
		})
		ui.Column(c).Gap(3).Grow(1).Children(func() {
			ui.Text(c, T("替换值（留空保持原值）")).FontSize(10).TextColor(p.t.TextMuted)
			input := ui.TextInput(c.Key("env-value:"+name), &draft.Value).FontSize(11.5).Height(32).
				Padding(0, 8).Placeholder(T("已配置，不显示原值")).Background(p.t.Background).
				Radius(8).Border(1, p.t.Border).Label(T("环境变量 ") + name + T(" 的值"))
			input.Password()
			input.Disabled(busy)
			if input.Changed() {
				draft.Touched = true
			}
		})
		remove := ui.Button(c.Key("env-remove:"+name), T("删除")).Label(T("删除环境变量 ") + name)
		remove.Disabled(busy)
		if remove.Clicked() && !busy {
			a.removeEnvVariable(name)
		}
	})
}

func (a *nativeApp) envErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("环境变量管理不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.envCapable() {
			if ui.Button(c.Key("env-retry"), T("重试")).Label(T("重试加载环境变量")).Clicked() {
				a.env.Loaded = false
				a.startEnvPanelLoad()
			}
		}
	})
}

func (a *nativeApp) envStatusBanner(c *ui.Context, p palette) {
	panel := &a.env
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

// envSummaryLabel is used by the settings entry description and tests to keep
// the secret-free wording in one place.
func envSummaryLabel(count int) string {
	if count == 0 {
		return "尚未配置环境变量"
	}
	return Tf("%d 个环境变量已配置（值不回显）", count)
}
