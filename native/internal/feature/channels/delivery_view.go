package channels

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/i18n"
	"github.com/oschina/mothx/native/internal/icons"
	"github.com/oschina/mothx/native/internal/kit"
)

// DeliveriesCard renders the additive delivery-failure
// projection on the Channels panel, mirroring Desktop's ChannelsPanel section.
// Only Runtime-marked retryable failures expose a retry action.
func (p *Panel) DeliveriesCard(h Host, c *ui.Context, pal kit.Palette) {
	desc := i18n.T("Runtime 持久化的投递失败记录；仅 failed 且可重试的传输级失败可以重开。")
	if h.CurrentSession() != "" {
		desc = desc + i18n.T(" 当前只显示该会话的投递操作。")
	}
	kit.SettingsCard(c, pal, "投递失败", desc, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, i18n.T(DeliverySummary(len(p.Deliveries)))).FontSize(10.5).TextColor(pal.T.TextMuted).Grow(1)
				refresh := ui.Button(c.Key("deliveries-refresh"), i18n.T("刷新")).Label(i18n.T("刷新投递失败记录"))
				refresh.Disabled(p.DeliveriesLoading || p.RetryingDelivery != "")
				if refresh.Clicked() && !p.DeliveriesLoading && p.RetryingDelivery == "" {
					p.StartDeliveriesLoad(h)
				}
			})
			if p.DeliveriesErr != "" {
				ui.Text(c, i18n.T(p.DeliveriesErr)).FontSize(10.5).TextColor(pal.T.Danger).Selectable()
			}
			if p.DeliveriesLoading && !p.DeliveriesLoaded {
				ui.Text(c, i18n.T("正在读取投递失败记录…")).FontSize(11).TextColor(pal.T.TextMuted)
				return
			}
			if len(p.Deliveries) == 0 {
				ui.Text(c, i18n.T("没有需要处理的投递失败。")).FontSize(11).TextColor(pal.T.TextMuted).Padding(8, 2)
				return
			}
			for _, failure := range p.Deliveries {
				p.deliveryRow(h, c, pal, failure)
			}
		})
	})
}

func (p *Panel) deliveryRow(h Host, c *ui.Context, pal kit.Palette, failure DeliveryFailureView) {
	busy := p.RetryingDelivery != ""
	ui.Column(c).Padding(9, 10).Radius(9).Background(pal.T.Background).
		Border(1, pal.T.Border).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icons.IconAlert).FontSize(13).TextColor(pal.T.Danger)
			ui.Text(c, DeliveryTargetLabel(failure)).FontSize(11.5).FontWeight(650).
				TextColor(pal.T.Text).Grow(1).SingleLine()
			kit.StatusPill(c, pal, failure.Status)
			if failure.Retryable {
				kit.Badge(c, pal, "可重试")
			} else if IsDeliveryFailureTransient(failure) {
				kit.Badge(c, pal, "传输失败")
			} else {
				kit.Badge(c, pal, "不可重试")
			}
		})
		ui.Text(c, DeliveryFailureMeta(failure)).FontSize(10).TextColor(pal.T.TextMuted).SingleLine()
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if failure.FailureCode != "" {
				ui.Text(c, i18n.T("原因：")+failure.FailureCode).FontSize(10).TextColor(pal.T.TextMuted).Grow(1)
			} else {
				ui.Text(c, "").Grow(1)
			}
			if p.RetryingDelivery == failure.OperationID {
				kit.StatusPill(c, pal, "running")
			}
			retry := ui.Button(c.Key("delivery-retry:"+failure.OperationID), i18n.T("重试")).
				Label(i18n.Tf("重试投递 %s", failure.OperationID))
			retry.Disabled(busy || !CanRetryDelivery(failure))
			if retry.Clicked() && !busy && CanRetryDelivery(failure) {
				p.RetryDelivery(h, failure.OperationID)
			}
		})
	})
}

func DeliveryTargetLabel(failure DeliveryFailureView) string {
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
		return i18n.Tf("投递操作 %s", failure.OperationID)
	}
	return strings.Join(parts, " · ")
}

func DeliveryFailureMeta(failure DeliveryFailureView) string {
	parts := []string{i18n.Tf("操作 %s", failure.OperationID)}
	if failure.SessionID != "" {
		parts = append(parts, i18n.Tf("会话 %s", failure.SessionID))
	}
	if failure.RunID != "" {
		parts = append(parts, i18n.Tf("运行 %s", failure.RunID))
	}
	if failure.AttemptCount > 0 {
		parts = append(parts, i18n.Tf("尝试 %d 次", failure.AttemptCount))
	}
	if failure.UpdatedAt != "" {
		parts = append(parts, kit.RelativeTime(failure.UpdatedAt))
	}
	return strings.Join(parts, " · ")
}
