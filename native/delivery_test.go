package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func deliveryTestApp(t *testing.T) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(map[string]bool{"manageChannels": true, "manageDeliveries": true})
	a.settingsOpen = true
	a.settingsCategory = "integrations"
	a.settingsTab = "channels"
	a.ensureSettingsTabVisible()
	a.channels.Loaded = true
	a.channels.DeliveriesLoaded = true
	a.channels.Deliveries = []deliveryFailureView{
		{
			OperationID: "op-1", SessionID: "s1", RunID: "r1", Platform: "feishu", TargetID: "chat-9",
			OperationKind: "send", Status: "failed", FailureCode: "transport_error",
			AttemptCount: 3, UpdatedAt: "2026-11-11T09:00:00Z", Retryable: true,
		},
		{
			OperationID: "op-2", Platform: "wechat", TargetID: "room-1", OperationKind: "send",
			Status: "failed", FailureCode: "platform_rejected", Retryable: false,
		},
		{
			OperationID: "op-3", Platform: "feishu", Status: "pending", Retryable: false,
		},
	}
	return a
}

func TestDeliveryRetryRules(t *testing.T) {
	retryable := deliveryFailureView{Status: "failed", Retryable: true, FailureCode: "transport_error"}
	if !canRetryDelivery(retryable) || !isDeliveryFailureTransient(retryable) {
		t.Fatalf("retryable failure misclassified")
	}
	if canRetryDelivery(deliveryFailureView{Status: "failed", Retryable: false, FailureCode: "platform_rejected"}) {
		t.Fatalf("permanent failure must not be retryable")
	}
	if canRetryDelivery(deliveryFailureView{Status: "pending", Retryable: true}) {
		t.Fatalf("in-flight operation must not be retryable")
	}
	if isDeliveryFailureTransient(deliveryFailureView{FailureCode: "platform_rejected"}) {
		t.Fatalf("platform rejection must not be transient")
	}
	if got := deliverySummary(0); got != "没有失败的投递操作" {
		t.Fatalf("empty summary = %q", got)
	}
	if got := deliverySummary(3); !strings.Contains(got, "3") {
		t.Fatalf("count summary = %q", got)
	}
	if got := deliveryStatusLabel("failed"); got != "失败" {
		t.Fatalf("status label = %q", got)
	}
}

func TestDeliveryFailureMetadata(t *testing.T) {
	failure := deliveryFailureView{
		OperationID: "op-1", SessionID: "s1", RunID: "r1", Platform: "feishu",
		TargetID: "chat-9", OperationKind: "send", AttemptCount: 2,
	}
	if got := deliveryTargetLabel(failure); got != "feishu · chat-9 · send" {
		t.Fatalf("target label = %q", got)
	}
	meta := deliveryFailureMeta(failure)
	for _, want := range []string{"op-1", "s1", "r1", "2"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("metadata missing %q: %q", want, meta)
		}
	}
}

func TestDeliveryPanelRendersRetryOnlyForRetryable(t *testing.T) {
	a := deliveryTestApp(t)
	tt := ui.NewTester(a.view, 1180, 900)
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"投递失败", "feishu · chat-9 · send", "可重试", "不可重试", "失败", "transport_error", "重试"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("delivery panel missing %q: %s", want, texts)
		}
	}
}

func TestDeliveryCapabilityGating(t *testing.T) {
	a := deliveryTestApp(t)
	a.setFeatures(map[string]bool{"manageChannels": true})
	tt := ui.NewTester(a.view, 1180, 900)
	if textPresent(tt, "投递失败") {
		t.Fatalf("delivery section must be hidden without manageDeliveries: %q", tt.Texts())
	}
	a.startChannelDeliveriesLoad()
	if !a.channels.DeliveriesLoaded || a.channels.Deliveries != nil {
		t.Fatalf("unsupported deliveries state = %+v", a.channels)
	}
}

func TestRetryDeliveryRejectsUnsafeTargets(t *testing.T) {
	a := deliveryTestApp(t)
	// A permanent failure is rejected locally before any ACP call.
	a.retryChannelDelivery("op-2")
	if a.channels.DeliveriesErr == "" || !strings.Contains(a.channels.DeliveriesErr, "不可重试") {
		t.Fatalf("permanent retry error = %q", a.channels.DeliveriesErr)
	}
	// An in-flight operation is also rejected.
	a.retryChannelDelivery("op-3")
	if !strings.Contains(a.channels.DeliveriesErr, "不可重试") {
		t.Fatalf("pending retry error = %q", a.channels.DeliveriesErr)
	}
	// A missing operation is not silently retried.
	a.retryChannelDelivery("op-missing")
	if !strings.Contains(a.channels.DeliveriesErr, "不在当前列表") {
		t.Fatalf("missing retry error = %q", a.channels.DeliveriesErr)
	}
	if a.channels.RetryingDelivery != "" {
		t.Fatalf("rejected retries must not leave a retry marker: %+v", a.channels)
	}
}

func TestDeliveryInvalidationOnTabExit(t *testing.T) {
	a := deliveryTestApp(t)
	a.setSettingsCategory("appearance")
	if a.channels.Deliveries != nil || a.channels.DeliveriesLoaded || a.channels.RetryingDelivery != "" {
		t.Fatalf("leaving channels must clear delivery state: %+v", a.channels)
	}
}
