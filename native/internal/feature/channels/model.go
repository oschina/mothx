// Package channels owns the Native projections of ACP channel configuration
// (mothx/manage/channels/*) and transport-level delivery failures
// (mothx/manage/deliveries/*).
package channels

import (
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
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

// WechatView mirrors the WeChat section of a channels get/patch
// response. AutoTyping is a pointer so a missing field keeps the Desktop
// default of true instead of silently disabling auto typing.
type WechatView struct {
	Enabled              bool   `json:"enabled"`
	WorkDir              string `json:"workDir"`
	AutoTyping           *bool  `json:"autoTyping"`
	CredentialConfigured bool   `json:"credentialConfigured"`
}

// FeishuView mirrors the Feishu section of a channels get/patch
// response. Stored app ids and secrets are never projected; only the boolean
// configured flags are available to the panel.
type FeishuView struct {
	Enabled             bool   `json:"enabled"`
	WorkDir             string `json:"workDir"`
	AppIDConfigured     bool   `json:"appIDConfigured"`
	AppSecretConfigured bool   `json:"appSecretConfigured"`
}

// ConfigView mirrors the full mothx/manage/channels get/patch response.
type ConfigView struct {
	Artifact bool       `json:"artifact"`
	Wechat   WechatView `json:"wechat"`
	Feishu   FeishuView `json:"feishu"`
}

type State struct {
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
	Deliveries        []DeliveryFailureView
	DeliveriesLoaded  bool
	DeliveriesLoading bool
	DeliveriesErr     string
	RetryingDelivery  string
	DeliveriesSeq     int64
}

type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{State: State{WechatAutoTyping: true}}
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageChannels")
}

func (p *Panel) Current(h host.Base, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "channels" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// invalidateChannelsPanel drops transient state and makes every in-flight
// channels response stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Artifact = false
	p.WechatEnabled = false
	p.WechatWorkDir = ""
	p.WechatAutoTyping = true
	p.WechatCredentialConfigured = false
	p.WechatCredential = ""
	p.WechatClearCredential = false
	p.FeishuEnabled = false
	p.FeishuWorkDir = ""
	p.FeishuAppIDConfigured = false
	p.FeishuAppSecretConfigured = false
	p.FeishuAppID = ""
	p.FeishuAppSecret = ""
	p.FeishuClearAppID = false
	p.FeishuClearSecret = false
	p.Loaded = false
	p.Loading = false
	p.Saving = ""
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	atomic.AddInt64(&p.DeliveriesSeq, 1)
	p.Deliveries = nil
	p.DeliveriesLoaded = false
	p.DeliveriesLoading = false
	p.DeliveriesErr = ""
	p.RetryingDelivery = ""
}

// startChannelsPanelLoad starts one capability-gated channels read. It is
// called from the UI thread on tab entry.
func (p *Panel) StartLoad(h Host) {
	if !h.SettingsOpen() || h.SettingsTab() != "channels" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持消息通道管理（manageChannels）。"
		return
	}
	seq := atomic.AddInt64(&p.Seq, 1)
	p.Loading = true
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
	startReloadSeq := h.ReloadSequence()
	go p.load(h, seq, startReloadSeq)
}

func (p *Panel) load(h Host, seq, startReloadSeq int64) {
	client := h.ACP()
	if client == nil {
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Loading = false
			p.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.Call("mothx/manage/channels/get", map[string]any{})
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取消息通道失败：" + err.Error()
			return
		}
		var view ConfigView
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "消息通道响应无效：" + err.Error()
			return
		}
		p.ApplyView(&view)
		p.StartDeliveriesLoad(h)
	})
}

// applyChannelsView replaces the local projection with a server response and
// clears every write-only input. Stored credentials and app secrets are not
// part of the response, and Native never reconstructs them from a configured
// flag.
func (p *Panel) ApplyView(view *ConfigView) {
	if view == nil {
		return
	}
	p.Artifact = view.Artifact
	p.WechatEnabled = view.Wechat.Enabled
	p.WechatWorkDir = view.Wechat.WorkDir
	p.WechatAutoTyping = true
	if view.Wechat.AutoTyping != nil {
		p.WechatAutoTyping = *view.Wechat.AutoTyping
	}
	p.WechatCredentialConfigured = view.Wechat.CredentialConfigured
	p.WechatCredential = ""
	p.WechatClearCredential = false
	p.FeishuEnabled = view.Feishu.Enabled
	p.FeishuWorkDir = view.Feishu.WorkDir
	p.FeishuAppIDConfigured = view.Feishu.AppIDConfigured
	p.FeishuAppSecretConfigured = view.Feishu.AppSecretConfigured
	p.FeishuAppID = ""
	p.FeishuAppSecret = ""
	p.FeishuClearAppID = false
	p.FeishuClearSecret = false
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

// WorkDirValid mirrors the required-working-directory rule enforced by
// the Desktop panel before it issues a channel patch.
func WorkDirValid(workDir string) bool {
	return strings.TrimSpace(workDir) != ""
}

func BuildArtifactRequest(artifact bool) map[string]any {
	return map[string]any{
		"patch": map[string]any{"artifact": artifact},
	}
}

// BuildWechatRequest projects the WeChat card into a patch envelope.
// A typed credential is submitted only when it is non-empty and the clear
// toggle is off, so credPath and clearCredPath can never be sent together.
func BuildWechatRequest(panel *State) (map[string]any, error) {
	if panel == nil {
		return nil, i18n.LocalizedError("通道配置不存在")
	}
	workDir := strings.TrimSpace(panel.WechatWorkDir)
	if !WorkDirValid(workDir) {
		return nil, i18n.LocalizedError("工作目录不能为空")
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

// BuildFeishuRequest projects the Feishu card into a patch envelope.
// Set and clear fields are mutually exclusive for both app id and app secret.
func BuildFeishuRequest(panel *State) (map[string]any, error) {
	if panel == nil {
		return nil, i18n.LocalizedError("通道配置不存在")
	}
	workDir := strings.TrimSpace(panel.FeishuWorkDir)
	if !WorkDirValid(workDir) {
		return nil, i18n.LocalizedError("工作目录不能为空")
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
func (p *Panel) SaveSection(h Host, section string) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持消息通道管理（manageChannels）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.Saving != "" {
		return
	}

	var (
		request map[string]any
		err     error
		label   string
	)
	switch section {
	case "artifact":
		request = BuildArtifactRequest(p.Artifact)
		label = "Channel 制品设置"
	case "wechat":
		request, err = BuildWechatRequest(&p.State)
		label = "WeChat 配置"
	case "feishu":
		request, err = BuildFeishuRequest(&p.State)
		label = "Feishu 配置"
	default:
		return
	}
	if err != nil {
		p.Status = "保存失败：" + err.Error()
		p.StatusIsError = true
		return
	}

	client := h.ACP()
	if client == nil {
		p.Status = "保存失败：尚未连接到 MothX ACP。"
		p.StatusIsError = true
		return
	}

	seq := atomic.AddInt64(&p.Seq, 1)
	startReloadSeq := h.ReloadSequence()
	p.Saving = section
	p.Status = i18n.Tf("正在保存 %s…", i18n.T(label))
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/channels/patch", request)
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = ""
			if err != nil {
				p.Status = i18n.Tf("保存 %s失败：%s", i18n.T(label), err.Error())
				p.StatusIsError = true
				return
			}
			var view ConfigView
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后消息通道响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.ApplyView(&view)
			p.Status = "渠道配置已保存"
			p.StatusIsError = false
		})
	}()
}
