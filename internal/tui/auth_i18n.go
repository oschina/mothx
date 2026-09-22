package tui

import "github.com/oschina/mothx/internal/tui/i18n"

func modelGroupMessageID(id string) i18n.MessageID {
	switch id {
	case modelGroupBasics:
		return i18n.MsgAuthTitleModelBasics
	case modelGroupCapabilities:
		return i18n.MsgAuthTitleModelCapabilities
	case modelGroupSampling:
		return i18n.MsgAuthTitleModelSampling
	case modelGroupCost:
		return i18n.MsgAuthTitleModelCost
	case modelGroupCompat:
		return i18n.MsgAuthTitleModelCompatibility
	default:
		return i18n.MsgAuthLabelConfirm
	}
}

func providerGroupMessageID(id string) i18n.MessageID {
	switch id {
	case providerGroupCredentials:
		return i18n.MsgAuthTitleProviderCredentials
	case providerGroupProtocol:
		return i18n.MsgAuthTitleProviderProtocol
	case providerGroupNetwork:
		return i18n.MsgAuthTitleProviderNetwork
	case providerGroupAdvanced:
		return i18n.MsgAuthTitleProviderAdvanced
	default:
		return i18n.MsgAuthLabelConfirm
	}
}
