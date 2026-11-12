package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// statusBar is the Desktop StatusBar projection in Native: connection, version,
// context usage, prompt-cache hit rate, mode, model, and workspace. Every value
// is already Runtime-owned state; this view only formats it.
func (a *nativeApp) statusBar(c *ui.Context, p palette) {
	dot := p.t.Success
	switch a.conn {
	case connFailed:
		dot = p.t.Danger
	case connStarting:
		dot = p.t.Warning
	}
	ui.Row(c).Height(22).Padding(0, 12).AlignItems(ui.Center).Gap(12).
		BorderWidth(1, 0, 0, 0).BorderColor(p.t.Border).Background(p.t.Background).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(dot)
			ui.Text(c, a.connectionLabel()).FontSize(9.5).TextColor(p.t.TextMuted)
		})
		ui.Textf(c, "MothX Native v%s · ACP", a.version()).FontSize(9.5).TextColor(p.t.TextMuted)
		ui.Spacer(c)
		if a.usageUsed > 0 {
			label := fmt.Sprintf("ctx %s", humanCount(a.usageUsed))
			if a.usageSize > 0 {
				percent := int(float64(a.usageUsed) / float64(a.usageSize) * 100)
				label = fmt.Sprintf("ctx %s/%s (%d%%)", humanCount(a.usageUsed), humanCount(a.usageSize), percent)
			}
			if a.usageCost > 0 {
				currency := a.usageCurrency
				if currency == "" {
					currency = "$"
				}
				label += fmt.Sprintf(" · %s%.4f", currency, a.usageCost)
			}
			var cache *usageCacheProjection
			cachePercent := 0
			if candidate := a.visibleUsageCache(); candidate != nil && candidate.TotalInputTokens > 0 {
				cache = candidate
				cachePercent = int(float64(cache.CacheRead) / float64(cache.TotalInputTokens) * 100)
				if cachePercent > 100 {
					cachePercent = 100
				}
			}
			// Desktop only appends the cache hit rate once it has a non-zero
			// result; a zero-value projection is not rendered as “cache 0%”.
			if cache != nil && cachePercent > 0 {
				label += fmt.Sprintf(" · %s %d%%", T("缓存"), cachePercent)
			}
			element := ui.Text(c, label).FontSize(9.5).TextColor(p.t.TextMuted)
			if cache != nil {
				element.Tooltip(fmt.Sprintf("%s%d · %s%d · %s%d",
					T("命中"), cache.CacheRead, T("写入"), cache.CacheWrite, T("输入总量"), cache.TotalInputTokens))
			}
		}
		ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Icon(c, iconSettings).FontSize(11).TextColor(p.t.TextMuted)
			ui.Text(c, strings.TrimSpace(a.modeLabel())).FontSize(9.5).TextColor(p.t.TextMuted)
		})
		ui.Row(c).AlignItems(ui.Center).Gap(4).MaxWidth(260).Children(func() {
			ui.Icon(c, iconCpu).FontSize(11).TextColor(p.t.TextMuted)
			ui.Text(c, a.modelDisplay()).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
		})
		workspace := a.workspace
		if workspace == "" {
			workspace = a.draftCwd
		}
		if workspace != "" {
			ui.Row(c).AlignItems(ui.Center).Gap(4).MaxWidth(220).Children(func() {
				ui.Icon(c, iconFolder).FontSize(11).TextColor(p.t.TextMuted)
				ui.Text(c, a.workspaceName()).FontSize(9.5).TextColor(p.t.TextMuted).SingleLine()
			})
		}
	})
}

func (a *nativeApp) visibleUsageCache() *usageCacheProjection {
	if a.usageCache == nil {
		return nil
	}
	if !a.featureEnabled("usageCacheProjection") {
		return nil
	}
	return a.usageCache
}
