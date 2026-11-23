package desktop

import (
	"fmt"
)

func (a *nativeApp) connectionLabel() string {
	switch a.conn {
	case connStarting:
		return T("连接中")
	case connReady:
		return T("已连接")
	case connFailed:
		return T("离线")
	default:
		return T("未连接")
	}
}

func (a *nativeApp) usageLabel() string {
	if a.usageUsed <= 0 {
		return ""
	}
	label := fmt.Sprintf("%d tokens", a.usageUsed)
	if a.usageSize > 0 {
		percent := int(float64(a.usageUsed) / float64(a.usageSize) * 100)
		label = fmt.Sprintf("%d / %d (%d%%)", a.usageUsed, a.usageSize, percent)
	}
	if a.usageCost > 0 {
		currency := a.usageCurrency
		if currency == "" {
			currency = "$"
		}
		if currency == "USD" || currency == "usd" {
			currency = "$"
		}
		label += fmt.Sprintf(" · %s%.4f", currency, a.usageCost)
	}
	return label
}

func (a *nativeApp) workspaceLabel() string {
	return a.workspaceName()
}
