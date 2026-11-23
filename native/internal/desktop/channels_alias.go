package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/channels"
)

type channelsWechatView = channels.WechatView
type channelsFeishuView = channels.FeishuView
type channelsConfigView = channels.ConfigView
type channelsPanelState = channels.State
type deliveryFailureView = channels.DeliveryFailureView
type deliveryListResult = channels.DeliveryListResult
type deliveryRetryResult = channels.DeliveryRetryResult

func channelsWorkDirValid(workDir string) bool {
	return channels.WorkDirValid(workDir)
}

func buildChannelsArtifactRequest(artifact bool) map[string]any {
	return channels.BuildArtifactRequest(artifact)
}

func buildChannelsWechatRequest(panel *channels.State) (map[string]any, error) {
	return channels.BuildWechatRequest(panel)
}

func buildChannelsFeishuRequest(panel *channels.State) (map[string]any, error) {
	return channels.BuildFeishuRequest(panel)
}

func canRetryDelivery(failure channels.DeliveryFailureView) bool {
	return channels.CanRetryDelivery(failure)
}

func isDeliveryFailureTransient(failure channels.DeliveryFailureView) bool {
	return channels.IsDeliveryFailureTransient(failure)
}

func deliveryStatusLabel(status string) string { return channels.DeliveryStatusLabel(status) }
func deliverySummary(count int) string         { return channels.DeliverySummary(count) }
func deliveryTargetLabel(failure channels.DeliveryFailureView) string {
	return channels.DeliveryTargetLabel(failure)
}
func deliveryFailureMeta(failure channels.DeliveryFailureView) string {
	return channels.DeliveryFailureMeta(failure)
}
func channelsConfiguredLabel(configured bool, configuredText, unsetText string) string {
	return channels.ConfiguredLabel(configured, configuredText, unsetText)
}

func (a *nativeApp) channelsCapable() bool {
	return a.channels.Capable(a)
}

func (a *nativeApp) channelsPanelCurrent(seq, reloadSeq int64) bool {
	return a.channels.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateChannelsPanel() {
	a.channels.Invalidate()
}

func (a *nativeApp) startChannelsPanelLoad() {
	a.channels.StartLoad(a)
}

func (a *nativeApp) applyChannelsView(view *channels.ConfigView) {
	a.channels.ApplyView(view)
}

func (a *nativeApp) saveChannelsSection(section string) {
	a.channels.SaveSection(a, section)
}

func (a *nativeApp) deliveriesCapable() bool {
	return a.channels.DeliveriesCapable(a)
}

func (a *nativeApp) channelsDeliveriesCurrent(seq, reloadSeq int64) bool {
	return a.channels.DeliveriesCurrent(a, seq, reloadSeq)
}

func (a *nativeApp) startChannelDeliveriesLoad() {
	a.channels.StartDeliveriesLoad(a)
}

func (a *nativeApp) retryChannelDelivery(operationID string) {
	a.channels.RetryDelivery(a, operationID)
}

func (a *nativeApp) settingsChannelsPane(c *ui.Context, p palette) {
	a.channels.View(a, c, p)
}

func (a *nativeApp) settingsChannelDeliveriesCard(c *ui.Context, p palette) {
	a.channels.DeliveriesCard(a, c, p)
}
