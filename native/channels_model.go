package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP channels management contract
// (mothx/manage/channels/get and mothx/manage/channels/patch). Channel settings
// are a thin, secret-safe projection of the global serve.json; this panel only
// reads and writes them through ACP and never touches serve.json or starts a
// channel itself.
//
// Secrets are write-only. The server projects only "configured" booleans for
// the WeChat credential and the Feishu app id/app secret, so the view types
// below intentionally have no field for a stored secret. Native keeps the
// user's typed value only in the panel state long enough to submit it, then
// clears it on every hydrated response.

// channelsWechatView mirrors the WeChat section of a channels get/patch
// response. AutoTyping is a pointer so a missing field keeps the Desktop
// default of true instead of silently disabling auto typing.
type channelsWechatView struct {
	Enabled              bool   `json:"enabled"`
	WorkDir              string `json:"workDir"`
	AutoTyping           *bool  `json:"autoTyping"`
	CredentialConfigured bool   `json:"credentialConfigured"`
}

// channelsFeishuView mirrors the Feishu section of a channels get/patch
// response. Stored app ids and secrets are never projected; only the boolean
// configured flags are available to the panel.
type channelsFeishuView struct {
	Enabled             bool   `json:"enabled"`
	WorkDir             string `json:"workDir"`
	AppIDConfigured     bool   `json:"appIDConfigured"`
	AppSecretConfigured bool   `json:"appSecretConfigured"`
}

// channelsConfigView mirrors the full mothx/manage/channels get/patch response.
type channelsConfigView struct {
	Artifact bool               `json:"artifact"`
	Wechat   channelsWechatView `json:"wechat"`
	Feishu   channelsFeishuView `json:"feishu"`
}

type channelsPanelState struct {
	Artifact bool

	WechatEnabled              bool
	WechatWorkDir              string
	WechatAutoTyping           bool
	WechatCredentialConfigured bool
	// WechatCredential is the write-only input; it is never filled from a
	// server response and is cleared after a successful save.
	WechatCredential      string
	WechatClearCredential bool

	FeishuEnabled             bool
	FeishuWorkDir             string
	FeishuAppIDConfigured     bool
	FeishuAppSecretConfigured bool
	// FeishuAppID and FeishuAppSecret are write-only inputs, for the same
	// reason as WechatCredential.
	FeishuAppID       string
	FeishuAppSecret   string
	FeishuClearAppID  bool
	FeishuClearSecret bool

	Loaded  bool
	Loading bool
	// Saving names the section currently being saved ("artifact", "wechat",
	// or "feishu"); an empty value means no save is in flight.
	Saving        string
	Err           string
	Status        string
	StatusIsError bool

	// Seq guards every asynchronous channels call. Bumping it invalidates any
	// in-flight get/patch response, which is how tab exits, settings close,
	// and reconnects prevent stale state from landing.
	Seq int64

	// Delivery failures are a separate additive projection
	// (mothx/manage/deliveries/*) shown on the same panel. They are never part
	// of channels config and use their own in-flight guard.
	Deliveries        []deliveryFailureView
	DeliveriesLoaded  bool
	DeliveriesLoading bool
	DeliveriesErr     string
	RetryingDelivery  string
	DeliveriesSeq     int64
}

func newChannelsPanelState() channelsPanelState {
	return channelsPanelState{WechatAutoTyping: true}
}

func (a *nativeApp) channelsCapable() bool {
	return a.featureEnabled("manageChannels")
}

func (a *nativeApp) channelsPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "channels" &&
		seq == atomic.LoadInt64(&a.channels.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateChannelsPanel drops transient state and makes every in-flight
// channels response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateChannelsPanel() {
	c := &a.channels
	atomic.AddInt64(&c.Seq, 1)
	c.Artifact = false
	c.WechatEnabled = false
	c.WechatWorkDir = ""
	c.WechatAutoTyping = true
	c.WechatCredentialConfigured = false
	c.WechatCredential = ""
	c.WechatClearCredential = false
	c.FeishuEnabled = false
	c.FeishuWorkDir = ""
	c.FeishuAppIDConfigured = false
	c.FeishuAppSecretConfigured = false
	c.FeishuAppID = ""
	c.FeishuAppSecret = ""
	c.FeishuClearAppID = false
	c.FeishuClearSecret = false
	c.Loaded = false
	c.Loading = false
	c.Saving = ""
	c.Err = ""
	c.Status = ""
	c.StatusIsError = false
	atomic.AddInt64(&c.DeliveriesSeq, 1)
	c.Deliveries = nil
	c.DeliveriesLoaded = false
	c.DeliveriesLoading = false
	c.DeliveriesErr = ""
	c.RetryingDelivery = ""
}

// startChannelsPanelLoad starts one capability-gated channels read. It is
// called from the UI thread on tab entry.
func (a *nativeApp) startChannelsPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "channels" {
		return
	}
	c := &a.channels
	if c.Loading {
		return
	}
	if !a.channelsCapable() {
		c.Loaded = true
		c.Err = "当前运行时不支持消息通道管理（manageChannels）。"
		return
	}
	seq := atomic.AddInt64(&c.Seq, 1)
	c.Loading = true
	c.Err = ""
	c.Status = ""
	c.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadChannelsPanel(seq, startReloadSeq)
}

func (a *nativeApp) loadChannelsPanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.channelsPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.channels.Loading = false
			a.channels.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/channels/get", map[string]any{})
	a.dispatch(func() {
		if !a.channelsPanelCurrent(seq, startReloadSeq) {
			return
		}
		c := &a.channels
		c.Loading = false
		if err != nil {
			c.Err = "读取消息通道失败：" + err.Error()
			return
		}
		var view channelsConfigView
		if err := json.Unmarshal(raw, &view); err != nil {
			c.Err = "消息通道响应无效：" + err.Error()
			return
		}
		a.applyChannelsView(&view)
		a.startChannelDeliveriesLoad()
	})
}

// applyChannelsView replaces the local projection with a server response and
// clears every write-only input. Stored credentials and app secrets are not
// part of the response, and Native never reconstructs them from a configured
// flag.
func (a *nativeApp) applyChannelsView(view *channelsConfigView) {
	if view == nil {
		return
	}
	c := &a.channels
	c.Artifact = view.Artifact
	c.WechatEnabled = view.Wechat.Enabled
	c.WechatWorkDir = view.Wechat.WorkDir
	c.WechatAutoTyping = true
	if view.Wechat.AutoTyping != nil {
		c.WechatAutoTyping = *view.Wechat.AutoTyping
	}
	c.WechatCredentialConfigured = view.Wechat.CredentialConfigured
	c.WechatCredential = ""
	c.WechatClearCredential = false
	c.FeishuEnabled = view.Feishu.Enabled
	c.FeishuWorkDir = view.Feishu.WorkDir
	c.FeishuAppIDConfigured = view.Feishu.AppIDConfigured
	c.FeishuAppSecretConfigured = view.Feishu.AppSecretConfigured
	c.FeishuAppID = ""
	c.FeishuAppSecret = ""
	c.FeishuClearAppID = false
	c.FeishuClearSecret = false
	c.Loaded = true
	c.Loading = false
	c.Err = ""
}

// channelsWorkDirValid mirrors the required-working-directory rule enforced by
// the Desktop panel before it issues a channel patch.
func channelsWorkDirValid(workDir string) bool {
	return strings.TrimSpace(workDir) != ""
}

func buildChannelsArtifactRequest(artifact bool) map[string]any {
	return map[string]any{
		"patch": map[string]any{"artifact": artifact},
	}
}

// buildChannelsWechatRequest projects the WeChat card into a patch envelope.
// A typed credential is submitted only when it is non-empty and the clear
// toggle is off, so credPath and clearCredPath can never be sent together.
func buildChannelsWechatRequest(panel *channelsPanelState) (map[string]any, error) {
	if panel == nil {
		return nil, localizedError("通道配置不存在")
	}
	workDir := strings.TrimSpace(panel.WechatWorkDir)
	if !channelsWorkDirValid(workDir) {
		return nil, localizedError("工作目录不能为空")
	}
	wechat := map[string]any{
		"enabled":    panel.WechatEnabled,
		"workDir":    workDir,
		"autoTyping": panel.WechatAutoTyping,
	}
	if panel.WechatClearCredential {
		wechat["clearCredPath"] = true
	} else if credential := strings.TrimSpace(panel.WechatCredential); credential != "" {
		wechat["credPath"] = credential
	}
	return map[string]any{"patch": map[string]any{"wechat": wechat}}, nil
}

// buildChannelsFeishuRequest projects the Feishu card into a patch envelope.
// Set and clear fields are mutually exclusive for both app id and app secret.
func buildChannelsFeishuRequest(panel *channelsPanelState) (map[string]any, error) {
	if panel == nil {
		return nil, localizedError("通道配置不存在")
	}
	workDir := strings.TrimSpace(panel.FeishuWorkDir)
	if !channelsWorkDirValid(workDir) {
		return nil, localizedError("工作目录不能为空")
	}
	feishu := map[string]any{
		"enabled": panel.FeishuEnabled,
		"workDir": workDir,
	}
	if panel.FeishuClearAppID {
		feishu["clearAppId"] = true
	} else if appID := strings.TrimSpace(panel.FeishuAppID); appID != "" {
		feishu["appId"] = appID
	}
	if panel.FeishuClearSecret {
		feishu["clearAppSecret"] = true
	} else if appSecret := strings.TrimSpace(panel.FeishuAppSecret); appSecret != "" {
		feishu["appSecret"] = appSecret
	}
	return map[string]any{"patch": map[string]any{"feishu": feishu}}, nil
}

// saveChannelsSection submits one card's patch. Only one save is allowed at a
// time so a second section cannot overwrite the first response's hydration.
func (a *nativeApp) saveChannelsSection(section string) {
	c := &a.channels
	if !a.channelsCapable() {
		c.Status = "当前运行时不支持消息通道管理（manageChannels）。"
		c.StatusIsError = true
		return
	}
	if c.Loading || c.Saving != "" {
		return
	}

	var (
		request map[string]any
		err     error
		label   string
	)
	switch section {
	case "artifact":
		request = buildChannelsArtifactRequest(c.Artifact)
		label = "Channel 制品设置"
	case "wechat":
		request, err = buildChannelsWechatRequest(c)
		label = "WeChat 配置"
	case "feishu":
		request, err = buildChannelsFeishuRequest(c)
		label = "Feishu 配置"
	default:
		return
	}
	if err != nil {
		c.Status = "保存失败：" + err.Error()
		c.StatusIsError = true
		return
	}

	client := a.acp
	if client == nil {
		c.Status = "保存失败：尚未连接到 MothX ACP。"
		c.StatusIsError = true
		return
	}

	seq := atomic.AddInt64(&c.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	c.Saving = section
	c.Status = Tf("正在保存 %s…", T(label))
	c.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/channels/patch", request)
		a.dispatch(func() {
			if !a.channelsPanelCurrent(seq, startReloadSeq) {
				return
			}
			c := &a.channels
			c.Saving = ""
			if err != nil {
				c.Status = Tf("保存 %s失败：%s", T(label), err.Error())
				c.StatusIsError = true
				return
			}
			var view channelsConfigView
			if err := json.Unmarshal(raw, &view); err != nil {
				c.Status = "保存后消息通道响应无效：" + err.Error()
				c.StatusIsError = true
				return
			}
			a.applyChannelsView(&view)
			c.Status = "渠道配置已保存"
			c.StatusIsError = false
		})
	}()
}
