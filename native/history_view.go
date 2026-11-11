package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// historyView is the all-session projection corresponding to Desktop's
// HistoryView. It deliberately uses the canonical listAll result already
// owned by ACP; selecting a row returns to the normal chat projection.
func (a *nativeApp) historyView(c *ui.Context, p palette, bar ui.TitleBar) {
	ui.Column(c).Grow(1).FillHeight().Background(p.t.Background).Children(func() {
		ui.Row(c).Height(52).Padding(0, float32(12+bar.Right), 0, float32(18+bar.Left)).AlignItems(ui.Center).Gap(10).
			BorderWidth(0, 0, 1, 0).BorderColor(p.t.Border).Children(func() {
			if ui.Button(c.Key("history-back"), "返回对话").Clicked() {
				a.historyOpen = false
			}
			ui.Column(c).Gap(1).Children(func() {
				ui.Text(c, "会话历史").FontSize(14).FontWeight(650)
				ui.Text(c, "全部会话及其最新运行状态").FontSize(10).TextColor(p.t.TextMuted)
			})
		})
		ui.Column(c).Grow(1).Padding(18, 24).Children(func() {
			ui.TextInput(c.Key("history-query"), &a.historyQuery).Placeholder("搜索会话、工作区或模型").
				FontSize(12).Height(34).Padding(0, 9).Background(p.chip).Radius(8).Border(1, p.t.Border)
			ui.Scroll(c).Grow(1).Margin(12, 0, 0, 0).Children(func() {
				shown := 0
				query := strings.ToLower(strings.TrimSpace(a.historyQuery))
				for _, s := range a.sessions {
					if query != "" && !strings.Contains(strings.ToLower(s.Title+" "+s.Cwd+" "+s.Provider+" "+s.Model), query) {
						continue
					}
					shown++
					a.historyRow(c, p, s)
				}
				if shown == 0 {
					ui.Text(c, "没有匹配的会话").FontSize(12).TextColor(p.t.TextMuted).Padding(12, 4)
				}
			})
		})
	})
}

func historySessionStatus(s listedSession) string {
	if s.Meta.LastRun != nil {
		if s.Meta.LastRun.Active {
			return "运行中"
		}
		switch s.Meta.LastRun.Status {
		case "completed":
			return "已完成"
		case "failed", "expired", "timed_out":
			return "失败"
		case "incomplete":
			return "未完成"
		case "cancelled", "canceled":
			return "已取消"
		case "waiting_for_approval", "waiting_for_question", "pending":
			return "等待输入"
		}
	}
	return "空闲"
}

func (a *nativeApp) historyRow(c *ui.Context, p palette, s listedSession) {
	b := ui.ButtonBase(c.Key("history:"+s.SessionID)).WidthPercent(100).Padding(10, 12).Radius(10).Margin(0, 0, 6, 0).AlignItems(ui.Center).Gap(10)
	if b.Hovered() {
		b.Background(p.hover)
	}
	b.Children(func() {
		ui.Icon(c, iconChat).FontSize(15).TextColor(p.t.Accent)
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			title := s.Title
			if title == "" {
				title = "未命名会话"
			}
			ui.Text(c, title).FontSize(12).FontWeight(600).SingleLine()
			meta := strings.Join([]string{s.Cwd, strings.Trim(strings.Join([]string{s.Provider, s.Model}, " / "), " / "), relativeTime(s.UpdatedAt)}, " · ")
			ui.Text(c, meta).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
		})
		statusPill(c, p, historySessionStatus(s))
	})
	b.Label("打开会话 " + s.Title)
	if b.Clicked() {
		a.historyOpen = false
		a.selectSession(s)
	}
}
