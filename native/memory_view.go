package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// settingsMemoryPane renders the Native equivalent of the Desktop MemoryPanel
// inside the Settings View. It is capability-gated by manageMemory and only
// calls mothx/manage/memory/get and mothx/manage/memory/put; the panel never
// reads memory.md or settings.json directly.
func (a *nativeApp) settingsMemoryPane(c *ui.Context, p palette) {
	if !a.memoryCapable() {
		a.settingsCard(c, p, "全局记忆", "当前运行时未公开记忆管理能力。", func() {
			ui.Text(c, "需要 ACP 能力 manageMemory；当前版本不会显示或调用占位表单。").
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.memory
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, "正在读取全局记忆…").FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.memoryErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" {
		a.memoryStatusBanner(c, p)
	}
	if panel.Loading && panel.Loaded {
		ui.Text(c, "正在刷新全局记忆…").FontSize(10.5).TextColor(p.t.TextMuted)
	}

	desc := "全局 memory.md 文本；保存会通过 memory/put 整体替换该文件。"
	if path := strings.TrimSpace(panel.Path); path != "" {
		desc = "文件 " + path + "；" + desc
	}
	a.settingsCard(c, p, "全局记忆", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, "记忆内容").FontSize(10.5).FontWeight(600).TextColor(p.t.TextMuted).Grow(1)
				if source := strings.TrimSpace(panel.Source); source != "" {
					badge(c, p, source)
				}
				badge(c, p, "memory.md")
			})
			area := ui.TextAreaBase(c.Key("memory-content"), &panel.Content).
				FontSize(12).LineHeight(1.45).Lines(10, 20).Padding(8, 10).
				Placeholder("在这里记录希望 Agent 长期遵守的全局记忆…").
				Background(p.t.Background).Radius(8).Border(1, p.t.Border).
				Label("memory.md")
			area.Disabled(panel.Saving)

			current := memoryContentBytes(panel.Content)
			over := !memoryContentValid(panel.Content)
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				color := p.t.TextMuted
				if over {
					color = p.t.Danger
				}
				ui.Textf(c, "大小：%d 字节 · 当前 %d 字节 · 上限 1 MB（%d 字节）", panel.Size, current, memoryMaxBytes).
					FontSize(10).TextColor(color).Grow(1)
				ui.Text(c, "更新时间："+memoryUpdatedAtLabel(panel.UpdatedAt)).
					FontSize(10).TextColor(p.t.TextMuted).SingleLine()
			})
			if over {
				ui.Text(c, fmt.Sprintf("内容超过 1 MB 上限 %d 字节，必须缩减后才能保存。", current-memoryMaxBytes)).
					FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}

			ui.Row(c).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				if panel.Saving {
					statusPill(c, p, "running")
				}
				save := ui.PrimaryButton(c.Key("memory-save"), "保存记忆")
				save.Label("保存全局记忆").Tooltip("通过 mothx/manage/memory/put 整体替换 memory.md")
				save.Disabled(panel.Saving || panel.Loading || over)
				if save.Clicked() && !panel.Saving && !panel.Loading && !over {
					a.saveMemoryContent()
				}
			})
		})
	})
}

func memoryUpdatedAtLabel(updatedAt string) string {
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
	relative := relativeTime(updatedAt)
	if relative == "" {
		return parsed.Local().Format("2006-01-02 15:04:05")
	}
	return relative + " · " + parsed.Local().Format("2006-01-02 15:04:05")
}

func (a *nativeApp) memoryErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, "记忆管理不可用").FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, message).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.memoryCapable() {
			if ui.Button(c.Key("memory-retry"), "重试").Label("重试加载全局记忆").Clicked() {
				a.memory.Loaded = false
				a.startMemoryPanelLoad()
			}
		}
	})
}

func (a *nativeApp) memoryStatusBanner(c *ui.Context, p palette) {
	panel := &a.memory
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
		ui.Text(c, panel.Status).FontSize(11).TextColor(color).Grow(1)
	})
}
