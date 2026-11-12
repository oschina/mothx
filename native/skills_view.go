package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsSkillsPane renders the Native skills manager inside the Settings
// View. It is capability-gated by manageSkills and only calls
// mothx/manage/skills/list and mothx/manage/skills/set for the selected
// workspace; the panel never reads settings.json or skill directories.
func (a *nativeApp) settingsSkillsPane(c *ui.Context, p palette) {
	if !a.skillsCapable() {
		a.settingsCard(c, p, "技能", "当前运行时未公开技能管理能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageSkills；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.skills
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取技能列表…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.skillsErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.skillsStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, T("正在刷新技能列表…")).FontSize(10.5).TextColor(p.t.TextMuted)
	}

	desc := "技能由 Runtime 从内置、全局与当前工作区目录发现；开关写入全局禁用列表并即时生效。"
	if panel.Cwd != "" {
		desc = T("工作区 ") + panel.Cwd + " · " + T(desc)
	}
	a.settingsCard(c, p, "技能", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			if panel.Cwd != "" {
				ui.Text(c, T("发现目录：")+panel.Cwd).FontSize(10).TextColor(p.t.TextMuted).Selectable()
			}
			if len(panel.Skills) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, p.t.Border).Gap(4).Children(func() {
					ui.Text(c, T("当前工作区没有可用技能")).FontSize(12).FontWeight(600).TextColor(p.t.Text)
					ui.Text(c, T("在项目的 .skills 目录或全局技能目录中添加 SKILL.md 后重试。")).
						FontSize(10.5).TextColor(p.t.TextMuted)
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					for index := range panel.Skills {
						index := index
						a.skillsRow(c, p, index)
					}
				})
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, Tf("共 %d 个技能", len(panel.Skills))).
					FontSize(10).TextColor(p.t.TextMuted).Grow(1)
				refresh := ui.Button(c.Key("skills-refresh"), T("刷新"))
				refresh.Label(T("刷新技能列表"))
				refresh.Disabled(panel.Loading || panel.Toggling != "")
				if refresh.Clicked() && !panel.Loading && panel.Toggling == "" {
					panel.Loaded = false
					a.startSkillsPanelLoad()
				}
			})
		})
	})
}

func (a *nativeApp) skillsErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("技能管理不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.skillsCapable() {
			if ui.Button(c.Key("skills-retry"), T("重试")).Label(T("重试加载技能列表")).Clicked() {
				a.skills.Loaded = false
				a.startSkillsPanelLoad()
			}
		}
	})
}

func (a *nativeApp) skillsStatusBanner(c *ui.Context, p palette) {
	panel := &a.skills
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
		} else if panel.Toggling != "" {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(panel.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (a *nativeApp) skillsRow(c *ui.Context, p palette, index int) {
	panel := &a.skills
	if index < 0 || index >= len(panel.Skills) {
		return
	}
	skill := panel.Skills[index]
	enabled := skillEnabled(skill)
	source := strings.TrimSpace(skill.Source)
	toggling := panel.Toggling == skillName(skill)
	busy := panel.Toggling != "" || panel.Loading
	ui.Column(c).Padding(9).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := p.t.TextMuted
			if enabled {
				color = p.t.Accent
			}
			ui.Icon(c, iconSpark).FontSize(15).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, skill.Name).FontSize(12).FontWeight(650).TextColor(p.t.Text).SingleLine()
					if source != "" {
						badge(c, p, source)
					}
					if enabled {
						badge(c, p, "已启用")
					} else {
						badge(c, p, "已停用")
					}
					if toggling {
						statusPill(c, p, "running")
					}
				})
				description := skillDescription(skill)
				if description == "" {
					description = T("无描述")
				}
				ui.Text(c, description).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			})
			// Match the Desktop panel labels: the button shows the current
			// state while clicking it flips that state through skills/set.
			label := "停用"
			hint := "点击启用技能 "
			if enabled {
				label = "启用"
				hint = "点击停用技能 "
			}
			toggle := ui.Button(c.Key(fmt.Sprintf("skill-toggle:%d", index)), T(label))
			toggle.Label(T(hint) + skill.Name)
			toggle.Tooltip(T(hint) + skill.Name)
			toggle.Disabled(busy)
			if toggle.Clicked() && !busy {
				a.toggleSkill(index)
			}
		})
	})
}
