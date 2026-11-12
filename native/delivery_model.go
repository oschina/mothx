package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the additive delivery-failure
// management contract (mothx/manage/deliveries/list and .../retry). Delivery
// operations are Runtime-owned durable rows; the panel only projects failures
// and asks the Runtime to reopen a transport-level failure. It never infers a
// delivery from assistant text or stores delivery state locally.

type deliveryFailureView struct {
	OperationID   string `json:"operationId"`
	IntentID      string `json:"intentId,omitempty"`
	SessionID     string `json:"sessionId,omitempty"`
	RunID         string `json:"runId,omitempty"`
	Platform      string `json:"platform,omitempty"`
	TargetID      string `json:"targetId,omitempty"`
	OperationKind string `json:"operationKind,omitempty"`
	Status        string `json:"status,omitempty"`
	FailureCode   string `json:"failureCode,omitempty"`
	AttemptCount  int    `json:"attemptCount,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
	Retryable     bool   `json:"retryable,omitempty"`
}

type deliveryListResult struct {
	Deliveries []deliveryFailureView `json:"deliveries"`
	Count      int                   `json:"count"`
}

type deliveryRetryResult struct {
	Retried bool `json:"retried"`
}

// canRetryDelivery mirrors the ACP retry entry: only a failed, explicitly
// retryable operation may be reopened. In-flight, delivered, and permanent
// failures must stay untouched.
func canRetryDelivery(failure deliveryFailureView) bool {
	return failure.Status == "failed" && failure.Retryable
}

// isDeliveryFailureTransient distinguishes a transport-level failure from a
// permanent platform/media rejection for display only. Authorization remains
// the Runtime's retryable flag.
func isDeliveryFailureTransient(failure deliveryFailureView) bool {
	return failure.FailureCode == "transport_error"
}

func (a *nativeApp) deliveriesCapable() bool {
	return a.featureEnabled("manageDeliveries")
}

func (a *nativeApp) channelsDeliveriesCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "channels" &&
		seq == atomic.LoadInt64(&a.channels.DeliveriesSeq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

func (a *nativeApp) startChannelDeliveriesLoad() {
	if !a.settingsOpen || a.settingsTab != "channels" {
		return
	}
	c := &a.channels
	if c.DeliveriesLoading {
		return
	}
	if !a.deliveriesCapable() {
		atomic.AddInt64(&c.DeliveriesSeq, 1)
		c.Deliveries = nil
		c.DeliveriesLoaded = true
		c.DeliveriesErr = ""
		return
	}
	client := a.acp
	if client == nil {
		c.DeliveriesLoaded = true
		c.DeliveriesErr = "尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&c.DeliveriesSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.DeliveriesLoading = true
	c.DeliveriesErr = ""
	sessionID := a.currentSession
	go func() {
		params := map[string]any{}
		if strings.TrimSpace(sessionID) != "" {
			params["sessionId"] = sessionID
		}
		raw, err := client.call("mothx/manage/deliveries/list", params)
		a.dispatch(func() {
			if !a.channelsDeliveriesCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.channels
			c.DeliveriesLoading = false
			if err != nil {
				c.DeliveriesErr = "读取投递失败记录失败：" + err.Error()
				return
			}
			var result deliveryListResult
			if err := json.Unmarshal(raw, &result); err != nil {
				c.DeliveriesErr = "投递失败记录响应无效：" + err.Error()
				return
			}
			c.Deliveries = result.Deliveries
			c.DeliveriesLoaded = true
			c.DeliveriesErr = ""
		})
	}()
}

func (a *nativeApp) retryChannelDelivery(operationID string) {
	c := &a.channels
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || c.RetryingDelivery != "" {
		return
	}
	if !a.deliveriesCapable() {
		c.DeliveriesErr = "当前运行时不支持投递失败管理（manageDeliveries）。"
		return
	}
	var target *deliveryFailureView
	for i := range c.Deliveries {
		if c.Deliveries[i].OperationID == operationID {
			target = &c.Deliveries[i]
			break
		}
	}
	if target == nil {
		c.DeliveriesErr = "该投递操作已不在当前列表中。"
		return
	}
	if !canRetryDelivery(*target) {
		c.DeliveriesErr = "该投递失败不可重试：只有 failed 且可重试的传输级失败才能重开。"
		return
	}
	client := a.acp
	if client == nil {
		c.DeliveriesErr = "尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&c.DeliveriesSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.RetryingDelivery = operationID
	c.DeliveriesErr = ""
	go func() {
		raw, err := client.call("mothx/manage/deliveries/retry", map[string]any{"operationId": operationID})
		if err == nil {
			var result deliveryRetryResult
			if json.Unmarshal(raw, &result) == nil && !result.Retried {
				err = localizedError("运行时未重开该投递操作")
			}
		}
		a.dispatch(func() {
			if !a.channelsDeliveriesCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.channels
			c.RetryingDelivery = ""
			if err != nil {
				c.DeliveriesErr = "重试投递失败：" + err.Error()
				return
			}
			a.startChannelDeliveriesLoad()
		})
	}()
}

func deliveryStatusLabel(status string) string {
	switch status {
	case "failed":
		return "失败"
	case "pending", "in_flight":
		return "进行中"
	case "delivered":
		return "已投递"
	case "":
		return "未知"
	default:
		return status
	}
}

func deliverySummary(count int) string {
	if count == 0 {
		return T("没有失败的投递操作")
	}
	return Tf("失败投递 · %d", count)
}
