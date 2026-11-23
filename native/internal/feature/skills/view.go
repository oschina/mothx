package skills

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the Native skills manager inside the Settings
// View. It is capability-gated by manageSkills and only calls
// mothx/manage/skills/list and mothx/manage/skills/set for the selected
// workspace; the panel never reads settings.json or skill directories.
func (p *Panel) View(h Host, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "技能", "当前运行时未公开技能管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageSkills；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取技能列表…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		ui.Text(c, i18n.T("正在刷新技能列表…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	desc := "技能由 Runtime 从内置、全局与当前工作区目录发现；开关写入全局禁用列表并即时生效。"
	if p.Cwd != "" {
		desc = i18n.T("工作区 ") + p.Cwd + " · " + i18n.T(desc)
	}
	kit.SettingsCard(c, pal, "技能", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			if p.Cwd != "" {
				ui.Text(c, i18n.T("发现目录：")+p.Cwd).FontSize(10).TextColor(pal.T.TextMuted).Selectable()
			}
			if len(p.Skills) == 0 {
				ui.Column(c).Padding(14).Radius(9).Border(1, pal.T.Border).Gap(4).Children(func() {
					ui.Text(c, i18n.T("当前工作区没有可用技能")).FontSize(12).FontWeight(600).TextColor(pal.T.Text)
					ui.Text(c, i18n.T("在项目的 .skills 目录或全局技能目录中添加 SKILL.md 后重试。")).
						FontSize(10.5).TextColor(pal.T.TextMuted)
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					for index := range p.Skills {
						index := index
						p.row(h, c, pal, index)
					}
				})
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, i18n.Tf("共 %d 个技能", len(p.Skills))).
					FontSize(10).TextColor(pal.T.TextMuted).Grow(1)
				refresh := ui.Button(c.Key("skills-refresh"), i18n.T("刷新"))
				refresh.Label(i18n.T("刷新技能列表"))
				refresh.Disabled(p.Loading || p.Toggling != "")
				if refresh.Clicked() && !p.Loading && p.Toggling == "" {
					p.Loaded = false
					p.StartLoad(h)
				}
			})
		})
	})
}

func (p *Panel) errCard(h Host, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("技能管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("skills-retry"), i18n.T("重试")).Label(i18n.T("重试加载技能列表")).Clicked() {
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
		} else if p.Toggling != "" {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(p.Status)).FontSize(11).TextColor(color).Grow(1)
	})
}

func (p *Panel) row(h Host, c *ui.Context, pal kit.Palette, index int) {
	if index < 0 || index >= len(p.Skills) {
		return
	}
	skill := p.Skills[index]
	enabled := Enabled(skill)
	source := strings.TrimSpace(skill.Source)
	toggling := p.Toggling == Name(skill)
	busy := p.Toggling != "" || p.Loading
	ui.Column(c).Padding(9).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(7).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			color := pal.T.TextMuted
			if enabled {
				color = pal.T.Accent
			}
			ui.Icon(c, icons.IconSpark).FontSize(15).TextColor(color)
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
					ui.Text(c, skill.Name).FontSize(12).FontWeight(650).TextColor(pal.T.Text).SingleLine()
					if source != "" {
						kit.Badge(c, pal, source)
					}
					if enabled {
						kit.Badge(c, pal, "已启用")
					} else {
						kit.Badge(c, pal, "已停用")
					}
					if toggling {
						kit.StatusPill(c, pal, "running")
					}
				})
				description := Description(skill)
				if description == "" {
					description = i18n.T("无描述")
				}
				ui.Text(c, description).FontSize(9.5).TextColor(pal.T.TextMuted).SingleLine()
			})
			// Match the Desktop panel labels: the button shows the current
			// state while clicking it flips that state through skills/set.
			label := "停用"
			hint := "点击启用技能 "
			if enabled {
				label = "启用"
				hint = "点击停用技能 "
			}
			toggle := ui.Button(c.Key(fmt.Sprintf("skill-toggle:%d", index)), i18n.T(label))
			toggle.Label(i18n.T(hint) + skill.Name)
			toggle.Tooltip(i18n.T(hint) + skill.Name)
			toggle.Disabled(busy)
			if toggle.Clicked() && !busy {
				p.Toggle(h, index)
			}
		})
	})
}
