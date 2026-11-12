package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsStatsPane renders the Native equivalent of Desktop's StatsPanel. It
// is capability-gated by manageStats and only calls mothx/manage/stats/summary
// and mothx/manage/stats/timeseries; sessions.db aggregation stays in the
// Runtime/DAO layer.
func (a *nativeApp) settingsStatsPane(c *ui.Context, p palette) {
	if !a.statsCapable() {
		a.settingsCard(c, p, "使用统计", "当前运行时未公开使用统计能力。", func() {
			ui.Text(c, T("需要 ACP 能力 manageStats；当前版本不会显示或调用占位图表。")).
				FontSize(11).TextColor(p.t.TextMuted).LineHeight(1.45)
		})
		return
	}

	panel := &a.stats
	if panel.Loading && !panel.Loaded {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconRefresh).FontSize(13).TextColor(p.t.TextMuted)
			ui.Text(c, T("正在读取使用统计…")).FontSize(12).TextColor(p.t.TextMuted)
		})
		return
	}

	if panel.Err != "" {
		a.statsErrorCard(c, p, panel.Err)
		if !panel.Loaded {
			return
		}
	}
	if panel.Status != "" || (panel.Loading && panel.Loaded) {
		a.statsStatusBanner(c, p)
	}

	a.settingsCard(c, p, "统计窗口", "汇总与趋势使用同一个时间窗口；趋势按所选粒度分桶。", func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
			for _, days := range []int{7, 14, 30} {
				days := days
				a.statsWindowButton(c, p, days)
			}
			ui.Box(c).Width(12)
			ui.Column(c).Gap(3).Width(130).Children(func() {
				ui.Text(c, T("分桶粒度")).FontSize(10).TextColor(p.t.TextMuted)
				selectEl := ui.Select(c.Key("stats-group"), &panel.Group, statsGroupOptions).
					FontSize(11).Height(30).Padding(0, 8).Background(p.t.Background).
					Radius(8).Border(1, p.t.Border).Label(T("统计分桶粒度"))
				if selectEl.Changed() && !panel.Loading {
					panel.Group = normalizeStatsGroup(panel.Group)
					a.reloadStatsPanel()
				}
			})
			refresh := ui.Button(c.Key("stats-refresh"), T("刷新")).Label(T("刷新使用统计"))
			refresh.Disabled(panel.Loading)
			if refresh.Clicked() && !panel.Loading {
				a.reloadStatsPanel()
			}
		})
	})

	summary := panel.Summary
	tokens := statsTokensTotal(summary.Tokens)
	a.settingsCard(c, p, statsWindowLabel(panel.Days), "数据来自 sessions.db 的 request_stats 投影。", func() {
		ui.Row(c).AlignItems(ui.Stretch).Gap(10).Wrap().Children(func() {
			a.statsMetricCard(c, p, "会话", fmt.Sprintf("%d", summary.Sessions))
			a.statsMetricCard(c, p, "运行", fmt.Sprintf("%d", summary.Runs))
			a.statsMetricCard(c, p, "Tokens", formatTokenCount(tokens))
			a.statsMetricCard(c, p, "成本", formatStatsCost(summary.Cost))
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, Tf("输入 %s · 输出 %s", formatTokenCount(summary.Tokens.Input), formatTokenCount(summary.Tokens.Output))).
				FontSize(10).TextColor(p.t.TextMuted).Grow(1)
			if summary.Until != "" {
				ui.Text(c, T("截至 ")+summary.Until).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
			}
		})
		if summary.Cost == 0 {
			ui.Text(c, T("当前统计 schema 不记录价格，成本恒为 0；这是 ACP 侧已记录的限制，而非本地隐藏。")).
				FontSize(9.5).TextColor(p.t.TextMuted).LineHeight(1.35)
		}
	})

	a.settingsCard(c, p, "运行趋势", Tf("%s · 按 %s 分桶", statsWindowLabel(panel.Days), T(statsGroupLabel(panel.Group))), func() {
		if len(panel.Points) == 0 {
			ui.Text(c, T("该窗口内没有运行记录")).FontSize(11.5).TextColor(p.t.TextMuted)
			return
		}
		max := statsMaxRuns(panel.Points)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Row(c).AlignItems(ui.End).Height(92).Gap(3).Padding(4, 2).Children(func() {
				for _, point := range panel.Points {
					point := point
					height := statsBarHeight(point.Runs, max)
					color := p.chip
					if point.Runs > 0 {
						color = p.t.Accent
					}
					ui.Column(c).Height(82).Width(16).Justify(ui.End).Children(func() {
						bar := ui.Box(c).Width(14).Height(height).Radius(3).Background(color)
						bar.Tooltip(Tf("%s · %d 次运行 · %s tokens", point.Date, point.Runs, formatTokenCount(point.Tokens)))
					})
				}
			})
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			first := panel.Points[0]
			last := panel.Points[len(panel.Points)-1]
			ui.Text(c, Tf("%s → %s · %d 个分桶", first.Date, last.Date, len(panel.Points))).
				FontSize(10).TextColor(p.t.TextMuted).Grow(1)
			ui.Text(c, T("条形：运行次数")).FontSize(10).TextColor(p.t.TextMuted)
		})
	})
}

func (a *nativeApp) statsWindowButton(c *ui.Context, p palette, days int) {
	label := Tf("%d 天", days)
	b := ui.ButtonBase(c.Key(fmt.Sprintf("stats-window:%d", days))).Height(30).Padding(0, 12).
		Radius(8).AlignItems(ui.Center).Justify(ui.Center).Border(1, p.t.Border)
	if a.stats.Days == days {
		b.Background(p.active)
	} else if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Text(c, T(label)).FontSize(11).TextColor(p.t.Text)
	})
	b.Label(label).Tooltip(statsWindowLabel(days))
	if b.Clicked() {
		a.setStatsWindow(days)
	}
}

func (a *nativeApp) statsMetricCard(c *ui.Context, p palette, label, value string) {
	ui.Column(c).Width(150).Padding(10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(3).Children(func() {
		ui.Text(c, T(label)).FontSize(10).FontWeight(600).TextColor(p.t.TextMuted)
		ui.Text(c, value).FontSize(16).FontWeight(700).TextColor(p.t.Text).SingleLine()
	})
}

func statsGroupLabel(group string) string {
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

func formatTokenCount(value int64) string {
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	if value < 1_000_000 {
		return fmt.Sprintf("%.1fK", float64(value)/1000)
	}
	return fmt.Sprintf("%.2fM", float64(value)/1_000_000)
}

func formatStatsCost(cost float64) string {
	return fmt.Sprintf("$%.4f", cost)
}

func (a *nativeApp) statsErrorCard(c *ui.Context, p palette, message string) {
	ui.Column(c).Padding(12).Radius(10).Background(p.errorBg).
		Border(1, ui.RGBA(255, 107, 107, 0.28)).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Icon(c, iconAlert).FontSize(15).TextColor(p.t.Danger)
			ui.Text(c, T("使用统计不可用")).FontSize(12).FontWeight(650).TextColor(p.t.Danger)
		})
		ui.Text(c, T(message)).FontSize(11).TextColor(p.t.TextMuted).Selectable()
		if a.statsCapable() {
			if ui.Button(c.Key("stats-retry"), T("重试")).Label(T("重试加载使用统计")).Clicked() {
				a.stats.Loaded = false
				a.startStatsPanelLoad()
			}
		}
	})
}

func (a *nativeApp) statsStatusBanner(c *ui.Context, p palette) {
	panel := &a.stats
	message := panel.Status
	if message == "" {
		message = "正在刷新使用统计…"
	}
	ui.Row(c).AlignItems(ui.Center).Gap(8).Padding(7, 10).Radius(8).Background(p.thought).
		Border(1, p.t.Border).Children(func() {
		if panel.Loading {
			statusPill(c, p, "running")
		}
		ui.Text(c, T(strings.TrimSpace(message))).FontSize(11).TextColor(p.t.TextMuted).Grow(1)
	})
}
