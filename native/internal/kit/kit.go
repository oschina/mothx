package kit

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
)

// Palette is the shared rendering palette. It is owned by the private kit
// package so business panels can render without importing the composition
// shell.
type Palette struct {
	T        *ui.Theme
	Sidebar  ui.Color
	Bubble   ui.Color
	Hover    ui.Color
	Composer ui.Color
	Chip     ui.Color
	Thought  ui.Color
	Active   ui.Color
	ErrorBg  ui.Color
	WarnBg   ui.Color
	OkBg     ui.Color
}

// Veiled projects the translucent chrome surfaces Desktop uses over an app-wide
// background so text stays readable on any image. Dialogs and menus keep their
// opaque theme surfaces.
func (p Palette) Veiled() Palette {
	p.T.Background = p.T.Background.Alpha(0.72)
	p.Sidebar = p.Sidebar.Alpha(0.72)
	p.Bubble = p.Bubble.Alpha(0.82)
	p.Hover = p.Hover.Alpha(0.72)
	p.Composer = p.Composer.Alpha(0.82)
	p.Chip = p.Chip.Alpha(0.82)
	p.Thought = p.Thought.Alpha(0.82)
	return p
}

// IconButton renders one compact icon action.
func IconButton(c *ui.Context, p Palette, icon *ui.SVG, label string, size float32, action func()) bool {
	b := ui.ButtonBase(c).Size(28, 28).Radius(7).Justify(ui.Center).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(size).TextColor(p.T.TextMuted)
	})
	b.Label(i18n.T(label)).Tooltip(i18n.T(label))
	if b.Clicked() {
		action()
		return true
	}
	return false
}

// Badge renders one compact metadata pill.
func Badge(c *ui.Context, p Palette, text string) {
	ui.Box(c).Padding(2, 6).Radius(4).Border(1, p.T.Border).Children(func() {
		ui.Text(c, i18n.T(text)).FontSize(8).FontWeight(700).LetterSpacing(0.7).TextColor(p.T.TextMuted)
	})
}

// StatusPill renders the canonical status vocabulary shared by every panel.
func StatusPill(c *ui.Context, p Palette, status string) {
	label := status
	color := p.T.TextMuted
	bg := p.Chip
	switch status {
	case "pending":
		label = i18n.T("等待")
	case "in_progress", "running":
		label = i18n.T("运行中")
		color = p.T.Accent
		bg = p.Active
	case "completed":
		label = i18n.T("完成")
		color = p.T.Success
		bg = p.OkBg
	case "failed":
		label = i18n.T("失败")
		color = p.T.Danger
		bg = p.ErrorBg
	case "cancelled":
		label = i18n.T("已取消")
	case "incomplete":
		label = i18n.T("未完成")
		color = p.T.Warning
		bg = p.WarnBg
	case "正在另一进程执行", "正在此进程执行", "远端任务仍在执行":
		color = p.T.Accent
		bg = p.Active
	case "Session 正被占用", "正在恢复遗留执行", "正在确认执行状态":
		color = p.T.Warning
		bg = p.WarnBg
	case "遗留执行恢复失败":
		color = p.T.Danger
		bg = p.ErrorBg
	case "无法确认执行状态":
		color = p.T.TextMuted
		bg = p.Chip
	default:
		label = i18n.T(label)
	}
	ui.Box(c).Padding(1, 6).Radius(999).Background(bg).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(9).TextColor(color)
	})
}

// PageHeader renders the shared page title block.
func PageHeader(c *ui.Context, p Palette, title, subtitle string) {
	ui.Column(c).Margin(0, 0, 20, 0).Gap(4).Children(func() {
		ui.Text(c, i18n.T(title)).FontSize(19).FontWeight(700).TextColor(p.T.Text)
		ui.Text(c, i18n.T(subtitle)).FontSize(12.5).TextColor(p.T.TextMuted).LineHeight(1.4)
	})
}

// PageContent is Desktop's PageInner: a centered column with fixed page
// padding. Pages keep their title and action rows inside it.
func PageContent(c *ui.Context, maxWidth float32, children func()) {
	ui.Column(c).MaxWidth(maxWidth).WidthPercent(94).Margin(0, ui.Auto).
		Padding(24, 0, 48, 0).Gap(14).Children(children)
}

// SettingsCard renders the shared settings card shell.
func SettingsCard(c *ui.Context, p Palette, title, desc string, body func()) {
	ui.Column(c).Padding(14).Radius(12).Background(p.T.Surface).Border(1, p.T.Border).Gap(10).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, i18n.T(title)).FontSize(12).FontWeight(650).TextColor(p.T.Text)
			if desc != "" {
				ui.Text(c, i18n.T(desc)).FontSize(10.5).TextColor(p.T.TextMuted).LineHeight(1.4)
			}
		})
		body()
	})
}

func SettingsTextInput(c *ui.Context, p Palette, key, label string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(p.T.TextMuted)
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Background(p.T.Background).Radius(8).Border(1, p.T.Border)
	})
}

func SettingsTextArea(c *ui.Context, p Palette, key, label string, value *string, lines int) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(p.T.TextMuted)
		ui.TextAreaBase(c.Key(key), value).FontSize(12).Lines(lines, lines*3).
			Padding(6, 8).Background(p.T.Background).Radius(8).Border(1, p.T.Border)
	})
}

func SettingsSelect(c *ui.Context, p Palette, key, label string, options []string, value *string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(p.T.TextMuted)
		ui.Select(c, value, options).FontSize(12).Height(32).Padding(0, 8).
			Background(p.T.Background).Radius(8).Border(1, p.T.Border)
	})
}

func SettingsTokenInput(c *ui.Context, p Palette, key, label string, value *string, configured bool, configuredText, unsetText string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Row(c).AlignItems(ui.Baseline).Gap(6).Children(func() {
			ui.Text(c, i18n.T(label)).FontSize(10.5).FontWeight(600).TextColor(p.T.TextMuted)
			status := unsetText
			if configured {
				status = configuredText
			}
			ui.Text(c, "["+i18n.T(status)+"]").FontSize(9.5).TextColor(p.T.TextMuted)
		})
		ui.TextInput(c.Key(key), value).FontSize(12).Height(32).Padding(0, 8).
			Placeholder(i18n.T("输入新 Token…")).Background(p.T.Background).Radius(8).Border(1, p.T.Border)
	})
}

func PlanStepMarker(c *ui.Context, p Palette, status string) {
	color := p.T.Border
	switch status {
	case "completed":
		color = p.T.Accent
	case "in_progress", "running":
		color = p.T.Accent
	case "failed":
		color = p.T.Danger
	}
	box := ui.Box(c).Size(15, 15).Radius(8).Border(1.5, color).
		Justify(ui.Center).AlignItems(ui.Center).Margin(1, 0, 0, 0)
	switch status {
	case "completed":
		box.Background(color).Children(func() {
			ui.Icon(c, icons.IconCheck).FontSize(9).TextColor(p.T.AccentText)
		})
	case "in_progress", "running":
		box.Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(p.T.Accent)
		})
	case "failed":
		box.Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(p.T.Danger)
		})
	}
}

func PlanProgressBar(c *ui.Context, p Palette, completed, total int) {
	percent := 0
	if total > 0 {
		percent = completed * 100 / total
	}
	const barWidth = 264
	ui.Row(c).Height(8).WidthPercent(100).Radius(4).Background(p.Chip).Clip().Children(func() {
		if percent > 0 {
			ui.Box(c).Height(8).Width(float32(barWidth * percent / 100)).Radius(4).Background(p.T.Accent)
		}
	})
}

func CopyButton(c *ui.Context, p Palette, text string) {
	b := ui.ButtonBase(c).Padding(3, 6).Radius(6).Gap(4).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(p.Hover)
	}
	b.Children(func() {
		ui.Icon(c, icons.IconFile).FontSize(11).TextColor(p.T.TextMuted)
		ui.Text(c, i18n.T("复制")).FontSize(9.8).TextColor(p.T.TextMuted)
	})
	b.Label(i18n.T("复制")).Tooltip(i18n.T("复制回复"))
	if b.Clicked() {
		c.Services().WriteClipboard(text)
	}
}

// FormatThoughtDuration renders reasoning time compactly: 0.4s, 12s, or 1m 05s.
func FormatThoughtDuration(d time.Duration) string {
	if d < 100*time.Millisecond {
		return "<0.1s"
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	total := int(d.Round(time.Second) / time.Second)
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	minutes, seconds := total/60, total%60
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm %02ds", minutes, seconds)
}

// ThoughtPreview projects the compact tail of a still-streaming thought for
// the collapsed live row, mirroring Desktop's 600-character preview window.
func ThoughtPreview(text string) string {
	runes := []rune(text)
	if len(runes) > 600 {
		runes = runes[len(runes)-600:]
	}
	return strings.Join(strings.Fields(string(runes)), " ")
}

func HumanCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
}

func MediaLabel(media string) string {
	if media == "" {
		return ""
	}
	return " · " + media
}

func HumanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}

// PageStat renders one compact page metric card.
func PageStat(c *ui.Context, p Palette, label, value, hint string) {
	ui.Column(c).Width(190).Padding(11, 12).Radius(10).Background(p.T.Surface).
		Border(1, p.T.Border).Gap(3).Children(func() {
		ui.Text(c, i18n.T(label)).FontSize(9.5).FontWeight(600).TextColor(p.T.TextMuted)
		ui.Text(c, i18n.T(value)).FontSize(20).FontWeight(650).TextColor(p.T.Text)
		if hint != "" {
			ui.Text(c, i18n.T(hint)).FontSize(9).TextColor(p.T.TextMuted).SingleLine()
		}
	})
}

// RelativeTime renders a canonical RFC3339 timestamp for compact metadata.
func RelativeTime(iso string) string {
	if iso == "" {
		return ""
	}
	tm, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		if tm, err = time.Parse(time.RFC3339, iso); err != nil {
			return ""
		}
	}
	d := time.Since(tm)
	if i18n.CurrentLocale() == "en" {
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		case d < 24*time.Hour:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		case d < 7*24*time.Hour:
			return fmt.Sprintf("%dd ago", int(d.Hours()/24))
		case d < 365*24*time.Hour:
			return tm.Format("Jan 02")
		default:
			return tm.Format("2006-01-02")
		}
	}
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return tm.Format("01-02")
	default:
		return tm.Format("2006-01-02")
	}
}

// TruncateOneLine keeps a single-line metadata preview bounded by runes.
func TruncateOneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len([]rune(s)) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}
