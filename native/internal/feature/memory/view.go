package memory

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the Native equivalent of the Desktop MemoryPanel inside the
// Settings View. It is capability-gated by manageMemory and only calls
// mothx/manage/memory/get and mothx/manage/memory/put; the panel never reads
// memory.md or settings.json directly.
func (p *Panel) View(h host.Base, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "全局记忆", "当前运行时未公开记忆管理能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageMemory；当前版本不会显示或调用占位表单。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取全局记忆…")).FontSize(12).TextColor(pal.T.TextMuted)
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
		ui.Text(c, i18n.T("正在刷新全局记忆…")).FontSize(10.5).TextColor(pal.T.TextMuted)
	}

	desc := "全局 memory.md 文本；保存会通过 memory/put 整体替换该文件。"
	if path := strings.TrimSpace(p.Path); path != "" {
		desc = i18n.T("文件 ") + path + " · " + i18n.T(desc)
	}
	kit.SettingsCard(c, pal, "全局记忆", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, i18n.T("记忆内容")).FontSize(10.5).FontWeight(600).TextColor(pal.T.TextMuted).Grow(1)
				if source := strings.TrimSpace(p.Source); source != "" {
					kit.Badge(c, pal, source)
				}
				kit.Badge(c, pal, "memory.md")
			})
			area := ui.TextAreaBase(c.Key("memory-content"), &p.Content).
				FontSize(12).LineHeight(1.45).Lines(10, 20).Padding(8, 10).
				Placeholder(i18n.T("在这里记录希望 Agent 长期遵守的全局记忆…")).
				Background(pal.T.Background).Radius(8).Border(1, pal.T.Border).
				Label("memory.md")
			area.Disabled(p.Saving)

			current := ContentBytes(p.Content)
			over := !ContentValid(p.Content)
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				color := pal.T.TextMuted
				if over {
					color = pal.T.Danger
				}
				ui.Text(c, i18n.Tf("大小：%d 字节 · 当前 %d 字节 · 上限 1 MB（%d 字节）", p.Size, current, MaxBytes)).
					FontSize(10).TextColor(color).Grow(1)
				ui.Text(c, i18n.T("更新时间：")+i18n.T(UpdatedAtLabel(p.UpdatedAt))).
					FontSize(10).TextColor(pal.T.TextMuted).SingleLine()
			})
			if over {
				ui.Text(c, i18n.Tf("内容超过 1 MB 上限 %d 字节，必须缩减后才能保存。", current-MaxBytes)).
					FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}

			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				if p.Saving {
					kit.StatusPill(c, pal, "running")
				}
				save := ui.PrimaryButton(c.Key("memory-save"), i18n.T("保存记忆"))
				save.Label(i18n.T("保存全局记忆")).Tooltip(i18n.T("通过 mothx/manage/memory/put 整体替换 memory.md"))
				save.Disabled(p.Saving || p.Loading || over)
				if save.Clicked() && !p.Saving && !p.Loading && !over {
					p.SaveContent(h)
				}
			})
		})
	})
}

func UpdatedAtLabel(updatedAt string) string {
	updatedAt = strings.TrimSpace(updatedAt)
	if updatedAt == "" {
		return "尚未写入"
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, updatedAt)
	}
	if err != nil {
		return updatedAt
	}
	relative := kit.RelativeTime(updatedAt)
	if relative == "" {
		return parsed.Local().Format("2006-01-02 15:04:05")
	}
	return relative + " · " + parsed.Local().Format("2006-01-02 15:04:05")
}

func (p *Panel) errCard(h host.Base, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("记忆管理不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("memory-retry"), i18n.T("重试")).Label(i18n.T("重试加载全局记忆")).Clicked() {
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
