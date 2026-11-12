package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// settingsChannelDeliveriesCard renders the additive delivery-failure
// projection on the Channels panel, mirroring Desktop's ChannelsPanel section.
// Only Runtime-marked retryable failures expose a retry action.
func (a *nativeApp) settingsChannelDeliveriesCard(c *ui.Context, p palette) {
	panel := &a.channels
	desc := T("Runtime 持久化的投递失败记录；仅 failed 且可重试的传输级失败可以重开。")
	if a.currentSession != "" {
		desc = desc + T(" 当前只显示该会话的投递操作。")
	}
	a.settingsCard(c, p, "投递失败", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, T(deliverySummary(len(panel.Deliveries)))).FontSize(10.5).TextColor(p.t.TextMuted).Grow(1)
				refresh := ui.Button(c.Key("deliveries-refresh"), T("刷新")).Label(T("刷新投递失败记录"))
				refresh.Disabled(panel.DeliveriesLoading || panel.RetryingDelivery != "")
				if refresh.Clicked() && !panel.DeliveriesLoading && panel.RetryingDelivery == "" {
					a.startChannelDeliveriesLoad()
				}
			})
			if panel.DeliveriesErr != "" {
				ui.Text(c, T(panel.DeliveriesErr)).FontSize(10.5).TextColor(p.t.Danger).Selectable()
			}
			if panel.DeliveriesLoading && !panel.DeliveriesLoaded {
				ui.Text(c, T("正在读取投递失败记录…")).FontSize(11).TextColor(p.t.TextMuted)
				return
			}
			if len(panel.Deliveries) == 0 {
				ui.Text(c, T("没有需要处理的投递失败。")).FontSize(11).TextColor(p.t.TextMuted).Padding(8, 2)
				return
			}
			for _, failure := range panel.Deliveries {
				a.deliveryFailureRow(c, p, failure)
			}
		})
	})
}

func (a *nativeApp) deliveryFailureRow(c *ui.Context, p palette, failure deliveryFailureView) {
	panel := &a.channels
	busy := panel.RetryingDelivery != ""
	ui.Column(c).Padding(9, 10).Radius(9).Background(p.t.Background).
		Border(1, p.t.Border).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconAlert).FontSize(13).TextColor(p.t.Danger)
			ui.Text(c, deliveryTargetLabel(failure)).FontSize(11.5).FontWeight(650).
				TextColor(p.t.Text).Grow(1).SingleLine()
			statusPill(c, p, failure.Status)
			if failure.Retryable {
				badge(c, p, "可重试")
			} else if isDeliveryFailureTransient(failure) {
				badge(c, p, "传输失败")
			} else {
				badge(c, p, "不可重试")
			}
		})
		ui.Text(c, deliveryFailureMeta(failure)).FontSize(10).TextColor(p.t.TextMuted).SingleLine()
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if failure.FailureCode != "" {
				ui.Text(c, T("原因：")+failure.FailureCode).FontSize(10).TextColor(p.t.TextMuted).Grow(1)
			} else {
				ui.Text(c, "").Grow(1)
			}
			if panel.RetryingDelivery == failure.OperationID {
				statusPill(c, p, "running")
			}
			retry := ui.Button(c.Key("delivery-retry:"+failure.OperationID), T("重试")).
				Label(Tf("重试投递 %s", failure.OperationID))
			retry.Disabled(busy || !canRetryDelivery(failure))
			if retry.Clicked() && !busy && canRetryDelivery(failure) {
				a.retryChannelDelivery(failure.OperationID)
			}
		})
	})
}

func deliveryTargetLabel(failure deliveryFailureView) string {
	parts := make([]string, 0, 3)
	if platform := strings.TrimSpace(failure.Platform); platform != "" {
		parts = append(parts, platform)
	}
	if target := strings.TrimSpace(failure.TargetID); target != "" {
		parts = append(parts, target)
	}
	if kind := strings.TrimSpace(failure.OperationKind); kind != "" {
		parts = append(parts, kind)
	}
	if len(parts) == 0 {
		return Tf("投递操作 %s", failure.OperationID)
	}
	return strings.Join(parts, " · ")
}

func deliveryFailureMeta(failure deliveryFailureView) string {
	parts := []string{Tf("操作 %s", failure.OperationID)}
	if failure.SessionID != "" {
		parts = append(parts, Tf("会话 %s", failure.SessionID))
	}
	if failure.RunID != "" {
		parts = append(parts, Tf("运行 %s", failure.RunID))
	}
	if failure.AttemptCount > 0 {
		parts = append(parts, Tf("尝试 %d 次", failure.AttemptCount))
	}
	if failure.UpdatedAt != "" {
		parts = append(parts, relativeTime(failure.UpdatedAt))
	}
	return strings.Join(parts, " · ")
}
