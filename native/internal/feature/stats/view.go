package stats

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// View renders the Native equivalent of Desktop's StatsPanel. It is
// capability-gated by manageStats and only calls mothx/manage/stats/summary and
// mothx/manage/stats/timeseries; sessions.db aggregation stays in the
// Runtime/DAO layer.
func (p *Panel) View(h host.Base, c *ui.Context, pal kit.Palette) {
	if !p.Capable(h) {
		kit.SettingsCard(c, pal, "使用统计", "当前运行时未公开使用统计能力。", func() {
			ui.Text(c, i18n.T("需要 ACP 能力 manageStats；当前版本不会显示或调用占位图表。")).
				FontSize(11).TextColor(pal.T.TextMuted).LineHeight(1.45)
		})
		return
	}

	if p.Loading && !p.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconRefresh).FontSize(13).TextColor(pal.T.TextMuted)
			ui.Text(c, i18n.T("正在读取使用统计…")).FontSize(12).TextColor(pal.T.TextMuted)
		})
		return
	}

	if p.Err != "" {
		p.errCard(h, c, pal, p.Err)
		if !p.Loaded {
			return
		}
	}
	if p.Status != "" || (p.Loading && p.Loaded) {
		p.statusBanner(c, pal)
	}

	kit.SettingsCard(c, pal, "统计窗口", "汇总与趋势使用同一个时间窗口；趋势按所选粒度分桶。", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			for _, days := range []int{7, 14, 30} {
				days := days
				p.windowButton(h, c, pal, days)
			}
			ui.Box(c).Width(12)
			ui.Column(c).Gap(3).Width(130).Children(func() {
				ui.Text(c, i18n.T("分桶粒度")).FontSize(10).TextColor(pal.T.TextMuted)
				selectEl := ui.Select(c.Key("stats-group"), &p.Group, GroupOptions).
					FontSize(11).Height(30).Padding(0, 8).Background(pal.T.Background).
					Radius(8).Border(1, pal.T.Border).Label(i18n.T("统计分桶粒度"))
				if selectEl.Changed() && !p.Loading {
					p.Group = NormalizeGroup(p.Group)
					p.Reload(h)
				}
			})
			refresh := ui.Button(c.Key("stats-refresh"), i18n.T("刷新")).Label(i18n.T("刷新使用统计"))
			refresh.Disabled(p.Loading)
			if refresh.Clicked() && !p.Loading {
				p.Reload(h)
			}
		})
	})

	summary := p.Summary
	tokens := TokensTotal(summary.Tokens)
	kit.SettingsCard(c, pal, WindowLabel(p.Days), "数据来自 sessions.db 的 request_stats 投影。", func() {
		ui.Row(c).AlignItems(ui.Stretch).Gap(10).Wrap().Children(func() {
			p.metricCard(c, pal, "会话", fmt.Sprintf("%d", summary.Sessions))
			p.metricCard(c, pal, "运行", fmt.Sprintf("%d", summary.Runs))
			p.metricCard(c, pal, "Tokens", FormatTokenCount(tokens))
			p.metricCard(c, pal, "成本", FormatCost(summary.Cost))
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, i18n.Tf("输入 %s · 输出 %s", FormatTokenCount(summary.Tokens.Input), FormatTokenCount(summary.Tokens.Output))).
				FontSize(10).TextColor(pal.T.TextMuted).Grow(1)
			if summary.Until != "" {
				ui.Text(c, i18n.T("截至 ")+summary.Until).FontSize(10).TextColor(pal.T.TextMuted).SingleLine()
			}
		})
		if summary.Cost == 0 {
			ui.Text(c, i18n.T("当前统计 schema 不记录价格，成本恒为 0；这是 ACP 侧已记录的限制，而非本地隐藏。")).
				FontSize(9.5).TextColor(pal.T.TextMuted).LineHeight(1.35)
		}
	})

	kit.SettingsCard(c, pal, "运行趋势", i18n.Tf("%s · 按 %s 分桶", WindowLabel(p.Days), i18n.T(GroupLabel(p.Group))), func() {
		if len(p.Points) == 0 {
			ui.Text(c, i18n.T("该窗口内没有运行记录")).FontSize(11.5).TextColor(pal.T.TextMuted)
			return
		}
		max := MaxRuns(p.Points)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Row(c).AlignItems(ui.End).Height(92).Gap(3).Padding(4, 2).Children(func() {
				for _, point := range p.Points {
					point := point
					height := BarHeight(point.Runs, max)
					color := pal.Chip
					if point.Runs > 0 {
						color = pal.T.Accent
					}
					ui.Column(c).Height(82).Width(16).Justify(ui.End).Children(func() {
						bar := ui.Box(c).Width(14).Height(height).Radius(3).Background(color)
						bar.Tooltip(i18n.Tf("%s · %d 次运行 · %s tokens", point.Date, point.Runs, FormatTokenCount(point.Tokens)))
					})
				}
			})
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			first := p.Points[0]
			last := p.Points[len(p.Points)-1]
			ui.Text(c, i18n.Tf("%s → %s · %d 个分桶", first.Date, last.Date, len(p.Points))).
				FontSize(10).TextColor(pal.T.TextMuted).Grow(1)
			ui.Text(c, i18n.T("条形：运行次数")).FontSize(10).TextColor(pal.T.TextMuted)
		})
	})
}

func (p *Panel) windowButton(h host.Base, c *ui.Context, pal kit.Palette, days int) {
	label := i18n.Tf("%d 天", days)
	b := ui.ButtonBase(c.Key(fmt.Sprintf("stats-window:%d", days))).Height(30).Padding(0, 12).
		Radius(8).AlignItems(ui.Center).Justify(ui.Center).Border(1, pal.T.Border)
	if p.Days == days {
		b.Background(pal.Active)
	} else if b.Hovered() {
		b.Background(pal.Hover)
	}
	b.Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(11).TextColor(pal.T.Text)
	})
	b.Label(label).Tooltip(WindowLabel(days))
	if b.Clicked() {
		p.SetWindow(h, days)
	}
}

func (p *Panel) metricCard(c *ui.Context, pal kit.Palette, label, value string) {
	ui.Column(c).Width(150).Padding(10).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10).FontWeight(600).TextColor(pal.T.TextMuted)
		ui.Text(c, value).FontSize(16).FontWeight(700).TextColor(pal.T.Text).SingleLine()
	})
}

func GroupLabel(group string) string {
	switch group {
	case "1h":
		return "小时"
	case "week":
		return "周"
	case "month":
		return "月"
	default:
		return "日"
	}
}

func FormatTokenCount(value int64) string {
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	if value < 1_000_000 {
		return fmt.Sprintf("%.1fK", float64(value)/1000)
	}
	return fmt.Sprintf("%.2fM", float64(value)/1_000_000)
}

func FormatCost(cost float64) string {
	return fmt.Sprintf("$%.4f", cost)
}

func (p *Panel) errCard(h host.Base, c *ui.Context, pal kit.Palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(pal.ErrorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(15).TextColor(pal.T.Danger)
			ui.Text(c, i18n.T("使用统计不可用")).FontSize(12).FontWeight(650).TextColor(pal.T.Danger)
		})
		ui.Text(c, i18n.T(message)).FontSize(11).TextColor(pal.T.TextMuted).Selectable()
		if p.Capable(h) {
			if ui.Button(c.Key("stats-retry"), i18n.T("重试")).Label(i18n.T("重试加载使用统计")).Clicked() {
				p.Loaded = false
				p.StartLoad(h)
			}
		}
	})
}

func (p *Panel) statusBanner(c *ui.Context, pal kit.Palette) {
	message := p.Status
	if message == "" {
		message = "正在刷新使用统计…"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(pal.Thought).
		Border(1, pal.T.Border).Children(func() {
		if p.Loading {
			kit.StatusPill(c, pal, "running")
		}
		ui.Text(c, i18n.T(strings.TrimSpace(message))).FontSize(11).TextColor(pal.T.TextMuted).Grow(1)
	})
}
