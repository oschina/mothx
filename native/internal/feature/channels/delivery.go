package channels

import (
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// Host adds the active session projection to the shared base host surface.
type Host interface {
	host.Base
	CurrentSession() string
}

// This file owns the Native projection of the additive delivery-failure
// management contract (mothx/manage/deliveries/list and .../retry). Delivery
// operations are Runtime-owned durable rows; the panel only projects failures
// and asks the Runtime to reopen a transport-level failure. It never infers a
// delivery from assistant text or stores delivery state locally.

type DeliveryFailureView struct {
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

type DeliveryListResult struct {
	Deliveries []DeliveryFailureView `json:"deliveries"`
	Count      int                   `json:"count"`
}

type DeliveryRetryResult struct {
	Retried bool `json:"retried"`
}

// CanRetryDelivery mirrors the ACP retry entry: only a failed, explicitly
// retryable operation may be reopened. In-flight, delivered, and permanent
// failures must stay untouched.
func CanRetryDelivery(failure DeliveryFailureView) bool {
	return failure.Status == "failed" && failure.Retryable
}

// IsDeliveryFailureTransient distinguishes a transport-level failure from a
// permanent platform/media rejection for display only. Authorization remains
// the Runtime's retryable flag.
func IsDeliveryFailureTransient(failure DeliveryFailureView) bool {
	return failure.FailureCode == "transport_error"
}

func (p *Panel) DeliveriesCapable(h host.Base) bool {
	return h.FeatureEnabled("manageDeliveries")
}

func (p *Panel) DeliveriesCurrent(h Host, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "channels" &&
		seq == atomic.LoadInt64(&p.DeliveriesSeq) &&
		reloadSeq == h.ReloadSequence()
}

func (p *Panel) StartDeliveriesLoad(h Host) {
	if !h.SettingsOpen() || h.SettingsTab() != "channels" {
		return
	}
	if p.DeliveriesLoading {
		return
	}
	if !p.DeliveriesCapable(h) {
		atomic.AddInt64(&p.DeliveriesSeq, 1)
		p.Deliveries = nil
		p.DeliveriesLoaded = true
		p.DeliveriesErr = ""
		return
	}
	client := h.ACP()
	if client == nil {
		p.DeliveriesLoaded = true
		p.DeliveriesErr = "尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&p.DeliveriesSeq, 1)
	startReloadSeq := h.ReloadSequence()
	p.DeliveriesLoading = true
	p.DeliveriesErr = ""
	sessionID := h.CurrentSession()
	go func() {
		params := map[string]any{}
		if strings.TrimSpace(sessionID) != "" {
			params["sessionId"] = sessionID
		}
		raw, err := client.Call("mothx/manage/deliveries/list", params)
		h.Dispatch(func() {
			if !p.DeliveriesCurrent(h, seq, startReloadSeq) {
				return
			}
			p.DeliveriesLoading = false
			if err != nil {
				p.DeliveriesErr = "读取投递失败记录失败：" + err.Error()
				return
			}
			var result DeliveryListResult
			if err := json.Unmarshal(raw, &result); err != nil {
				p.DeliveriesErr = "投递失败记录响应无效：" + err.Error()
				return
			}
			p.Deliveries = result.Deliveries
			p.DeliveriesLoaded = true
			p.DeliveriesErr = ""
		})
	}()
}

func (p *Panel) RetryDelivery(h Host, operationID string) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || p.RetryingDelivery != "" {
		return
	}
	if !p.DeliveriesCapable(h) {
		p.DeliveriesErr = "当前运行时不支持投递失败管理（manageDeliveries）。"
		return
	}
	var target *DeliveryFailureView
	for i := range p.Deliveries {
		if p.Deliveries[i].OperationID == operationID {
			target = &p.Deliveries[i]
			break
		}
	}
	if target == nil {
		p.DeliveriesErr = "该投递操作已不在当前列表中。"
		return
	}
	if !CanRetryDelivery(*target) {
		p.DeliveriesErr = "该投递失败不可重试：只有 failed 且可重试的传输级失败才能重开。"
		return
	}
	client := h.ACP()
	if client == nil {
		p.DeliveriesErr = "尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&p.DeliveriesSeq, 1)
	startReloadSeq := h.ReloadSequence()
	p.RetryingDelivery = operationID
	p.DeliveriesErr = ""
	go func() {
		raw, err := client.Call("mothx/manage/deliveries/retry", map[string]any{"operationId": operationID})
		if err == nil {
			var result DeliveryRetryResult
			if json.Unmarshal(raw, &result) == nil && !result.Retried {
				err = i18n.LocalizedError("运行时未重开该投递操作")
			}
		}
		h.Dispatch(func() {
			if !p.DeliveriesCurrent(h, seq, startReloadSeq) {
				return
			}
			p.RetryingDelivery = ""
			if err != nil {
				p.DeliveriesErr = "重试投递失败：" + err.Error()
				return
			}
			p.StartDeliveriesLoad(h)
		})
	}()
}

func DeliveryStatusLabel(status string) string {
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

func DeliverySummary(count int) string {
	if count == 0 {
		return i18n.T("没有失败的投递操作")
	}
	return i18n.Tf("失败投递 · %d", count)
}
