package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func channelsTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "integrations"
	a.settingsTab = "channels"
	a.ensureSettingsTabVisible()
	a.channels.Artifact = true
	a.channels.WechatEnabled = true
	a.channels.WechatWorkDir = "/home/user/wechat"
	a.channels.WechatAutoTyping = false
	a.channels.WechatCredentialConfigured = true
	a.channels.FeishuEnabled = true
	a.channels.FeishuWorkDir = "/home/user/feishu"
	a.channels.FeishuAppIDConfigured = true
	a.channels.FeishuAppSecretConfigured = true
	a.channels.Loaded = true
	return a
}

func TestChannelsViewDecodeIsSecretFree(t *testing.T) {
	raw := json.RawMessage(`{
		"artifact": true,
		"wechat": {
			"enabled": true,
			"workDir": "/wechat/wd",
			"autoTyping": false,
			"credentialConfigured": true,
			"credPath": "/secret/cred.json"
		},
		"feishu": {
			"enabled": true,
			"workDir": "/feishu/wd",
			"appIDConfigured": true,
			"appSecretConfigured": true,
			"appId": "app-id-secret",
			"appSecret": "app-secret-secret"
		}
	}`)
	var view channelsConfigView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if !view.Artifact || !view.Wechat.Enabled || view.Wechat.WorkDir != "/wechat/wd" ||
		view.Wechat.AutoTyping == nil || *view.Wechat.AutoTyping || !view.Wechat.CredentialConfigured {
		t.Fatalf("wechat view = %+v", view.Wechat)
	}
	if !view.Feishu.Enabled || view.Feishu.WorkDir != "/feishu/wd" ||
		!view.Feishu.AppIDConfigured || !view.Feishu.AppSecretConfigured {
		t.Fatalf("feishu view = %+v", view.Feishu)
	}

	a := testApp()
	a.applyChannelsView(&view)
	if a.channels.WechatCredential != "" || a.channels.FeishuAppID != "" || a.channels.FeishuAppSecret != "" {
		t.Fatalf("stored secrets must never be reconstructed: %+v", a.channels)
	}
	if !a.channels.Loaded || a.channels.WechatWorkDir != "/wechat/wd" || !a.channels.WechatCredentialConfigured {
		t.Fatalf("hydrated channels state = %+v", a.channels)
	}

	a.themeSet = true
	a.settingsOpen = true
	a.settingsCategory = "integrations"
	a.settingsTab = "channels"
	a.setFeatures(map[string]bool{"manageChannels": true})
	tt := ui.NewTester(a.view, 1180, 900)
	for _, secret := range []string{"/secret/cred.json", "app-id-secret", "app-secret-secret"} {
		for _, text := range tt.Texts() {
			if strings.Contains(text, secret) {
				t.Fatalf("channels panel rendered secret %q: %q", secret, tt.Texts())
			}
		}
	}
}

func TestChannelsMissingAutoTypingDefaultsTrue(t *testing.T) {
	raw := json.RawMessage(`{"artifact": false, "wechat": {"enabled": false, "workDir": "", "credentialConfigured": false}}`)
	var view channelsConfigView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	a := testApp()
	a.applyChannelsView(&view)
	if !a.channels.WechatAutoTyping {
		t.Fatalf("missing autoTyping must keep the Desktop default of true: %+v", a.channels)
	}
}

func TestChannelsRequestBuilders(t *testing.T) {
	artifact := buildChannelsArtifactRequest(true)
	artifactPatch, ok := artifact["patch"].(map[string]any)
	if !ok || artifactPatch["artifact"] != true {
		t.Fatalf("artifact request = %#v", artifact)
	}

	panel := &channelsPanelState{
		WechatEnabled:    true,
		WechatWorkDir:    " /wechat/wd ",
		WechatAutoTyping: false,
		WechatCredential: " /secret/new-cred.json ",
		FeishuEnabled:    true,
		FeishuWorkDir:    " /feishu/wd ",
		FeishuAppID:      " new-id ",
		FeishuAppSecret:  " new-secret ",
	}
	request, err := buildChannelsWechatRequest(panel)
	if err != nil {
		t.Fatal(err)
	}
	patch := request["patch"].(map[string]any)
	wechat := patch["wechat"].(map[string]any)
	if wechat["enabled"] != true || wechat["workDir"] != "/wechat/wd" ||
		wechat["autoTyping"] != false || wechat["credPath"] != "/secret/new-cred.json" {
		t.Fatalf("wechat patch = %#v", wechat)
	}
	if _, hasClear := wechat["clearCredPath"]; hasClear {
		t.Fatalf("wechat patch must not set and clear together: %#v", wechat)
	}

	panel.WechatClearCredential = true
	request, err = buildChannelsWechatRequest(panel)
	if err != nil {
		t.Fatal(err)
	}
	wechat = request["patch"].(map[string]any)["wechat"].(map[string]any)
	if wechat["clearCredPath"] != true {
		t.Fatalf("wechat clear patch = %#v", wechat)
	}
	if _, hasSet := wechat["credPath"]; hasSet {
		t.Fatalf("clear must win over a typed credential: %#v", wechat)
	}

	request, err = buildChannelsFeishuRequest(panel)
	if err != nil {
		t.Fatal(err)
	}
	feishu := request["patch"].(map[string]any)["feishu"].(map[string]any)
	if feishu["enabled"] != true || feishu["workDir"] != "/feishu/wd" ||
		feishu["appId"] != "new-id" || feishu["appSecret"] != "new-secret" {
		t.Fatalf("feishu patch = %#v", feishu)
	}
	if _, hasClear := feishu["clearAppId"]; hasClear {
		t.Fatalf("feishu patch must not set and clear app id together: %#v", feishu)
	}
	if _, hasClear := feishu["clearAppSecret"]; hasClear {
		t.Fatalf("feishu patch must not set and clear app secret together: %#v", feishu)
	}

	panel.FeishuClearAppID = true
	panel.FeishuClearSecret = true
	request, err = buildChannelsFeishuRequest(panel)
	if err != nil {
		t.Fatal(err)
	}
	feishu = request["patch"].(map[string]any)["feishu"].(map[string]any)
	if feishu["clearAppId"] != true || feishu["clearAppSecret"] != true {
		t.Fatalf("feishu clear patch = %#v", feishu)
	}
	if _, hasSet := feishu["appId"]; hasSet {
		t.Fatalf("clear must win over a typed app id: %#v", feishu)
	}
	if _, hasSet := feishu["appSecret"]; hasSet {
		t.Fatalf("clear must win over a typed app secret: %#v", feishu)
	}

	empty := &channelsPanelState{}
	if _, err := buildChannelsWechatRequest(empty); err == nil || !strings.Contains(err.Error(), "工作目录") {
		t.Fatalf("empty wechat workDir error = %v", err)
	}
	if _, err := buildChannelsFeishuRequest(empty); err == nil || !strings.Contains(err.Error(), "工作目录") {
		t.Fatalf("empty feishu workDir error = %v", err)
	}
	if _, err := buildChannelsWechatRequest(nil); err == nil {
		t.Fatal("nil wechat panel must be rejected")
	}
}

func TestChannelsPanelHeadlessRendersCardsAndConfiguredFlags(t *testing.T) {
	a := channelsTestApp(t, map[string]bool{"manageChannels": true})
	tt := ui.NewTester(a.view, 1180, 1000)
	for _, want := range []string{
		"消息通道", "Channel 制品", "启用 Channel 制品发布", "保存制品设置",
		"WeChat", "启用", "工作目录", "自动输入（autoTyping）",
		"凭据文件路径", "凭据已配置（留空保持）", "清除凭据", "保存 WeChat 配置",
		"Feishu", "App ID", "App ID 已配置（留空保持）", "清除 App ID",
		"App Secret", "App Secret 已配置（留空保持）", "清除 App Secret", "保存 Feishu 配置",
		"这些设置仅保存到全局 serve.json",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("channels panel missing %q: %q", want, tt.Texts())
		}
	}
	if a.channels.WechatWorkDir != "/home/user/wechat" || a.channels.FeishuWorkDir != "/home/user/feishu" {
		t.Fatalf("work directories must remain in panel state: %+v", a.channels)
	}
	if tt.HasText("需要 ACP 能力 manageChannels") {
		t.Fatalf("supported channels panel rendered the unsupported gate: %q", tt.Texts())
	}

	// A typed write-only value must never be exposed as rendered text.
	a.channels.WechatCredential = "typed-credential-value"
	a.channels.FeishuAppSecret = "typed-app-secret-value"
	tt.Frame()
	for _, secret := range []string{"typed-credential-value", "typed-app-secret-value"} {
		if textPresent(tt, secret) {
			t.Fatalf("channels panel rendered write-only value %q: %q", secret, tt.Texts())
		}
	}
}

func TestChannelsCapabilityGatePreventsLoadAndHidesTab(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "channels"
	a.startChannelsPanelLoad()
	if a.channelsCapable() {
		t.Fatal("manageChannels must be required")
	}
	if a.channels.Loading || !a.channels.Loaded || !strings.Contains(a.channels.Err, "manageChannels") {
		t.Fatalf("gated load state = %+v", a.channels)
	}

	a.settingsCategory = "integrations"
	a.settingsTab = "channels"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "channels" {
		t.Fatalf("channels tab must be hidden without manageChannels: %+v", a.channels)
	}
}

func TestChannelsSaveValidatesWorkDirAndReportsOfflineErrors(t *testing.T) {
	a := channelsTestApp(t, map[string]bool{"manageChannels": true})
	a.acp = nil

	a.channels.WechatWorkDir = "   "
	a.saveChannelsSection("wechat")
	if a.channels.Saving != "" || !a.channels.StatusIsError || !strings.Contains(a.channels.Status, "工作目录") {
		t.Fatalf("empty wechat workDir save state = %+v", a.channels)
	}

	a.channels.Status = ""
	a.channels.StatusIsError = false
	a.channels.WechatWorkDir = "/home/user/wechat"
	a.saveChannelsSection("wechat")
	if a.channels.Saving != "" || !a.channels.StatusIsError || !strings.Contains(a.channels.Status, "尚未连接") {
		t.Fatalf("offline wechat save state = %+v", a.channels)
	}

	a.channels.Status = ""
	a.channels.FeishuWorkDir = ""
	a.saveChannelsSection("feishu")
	if a.channels.Saving != "" || !a.channels.StatusIsError || !strings.Contains(a.channels.Status, "工作目录") {
		t.Fatalf("empty feishu workDir save state = %+v", a.channels)
	}

	a.channels.Status = ""
	a.channels.FeishuWorkDir = "/home/user/feishu"
	a.saveChannelsSection("feishu")
	if a.channels.Saving != "" || !a.channels.StatusIsError || !strings.Contains(a.channels.Status, "尚未连接") {
		t.Fatalf("offline feishu save state = %+v", a.channels)
	}

	a.channels.Status = ""
	a.saveChannelsSection("artifact")
	if a.channels.Saving != "" || !a.channels.StatusIsError || !strings.Contains(a.channels.Status, "尚未连接") {
		t.Fatalf("offline artifact save state = %+v", a.channels)
	}
}

func TestChannelsHydrationClearsWriteOnlyInputs(t *testing.T) {
	a := channelsTestApp(t, map[string]bool{"manageChannels": true})
	a.channels.WechatCredential = "typed-credential"
	a.channels.WechatClearCredential = true
	a.channels.FeishuAppID = "typed-app-id"
	a.channels.FeishuAppSecret = "typed-app-secret"
	a.channels.FeishuClearAppID = true
	a.channels.FeishuClearSecret = true

	a.applyChannelsView(&channelsConfigView{
		Artifact: true,
		Wechat:   channelsWechatView{Enabled: true, WorkDir: "/wechat", CredentialConfigured: false},
		Feishu:   channelsFeishuView{Enabled: true, WorkDir: "/feishu", AppIDConfigured: true, AppSecretConfigured: false},
	})
	if a.channels.WechatCredential != "" || a.channels.WechatClearCredential ||
		a.channels.FeishuAppID != "" || a.channels.FeishuAppSecret != "" ||
		a.channels.FeishuClearAppID || a.channels.FeishuClearSecret {
		t.Fatalf("write-only inputs must be cleared after hydration: %+v", a.channels)
	}
	if a.channels.WechatCredentialConfigured || !a.channels.FeishuAppIDConfigured || a.channels.FeishuAppSecretConfigured {
		t.Fatalf("configured flags = %+v", a.channels)
	}
}

func TestChannelsPanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := channelsTestApp(t, map[string]bool{"manageChannels": true})
	seq := atomic.LoadInt64(&a.channels.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.channelsPanelCurrent(seq, reloadSeq) {
		t.Fatal("current channels request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.channelsPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := channelsTestApp(t, map[string]bool{"manageChannels": true})
	b.channels.Saving = "wechat"
	b.channels.Status = "正在保存 WeChat 配置…"
	b.channels.WechatCredential = "typed-secret"
	b.channels.FeishuAppSecret = "typed-secret"
	b.channels.WechatClearCredential = true
	b.channels.FeishuClearSecret = true
	oldSeq := atomic.LoadInt64(&b.channels.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.channels.Seq) <= oldSeq {
		t.Fatal("leaving the channels tab must invalidate in-flight state")
	}
	if b.channels.Loaded || b.channels.Saving != "" || b.channels.Status != "" ||
		b.channels.WechatCredential != "" || b.channels.FeishuAppSecret != "" ||
		b.channels.WechatClearCredential || b.channels.FeishuClearSecret {
		t.Fatalf("channels state was not invalidated on tab exit: %+v", b.channels)
	}

	c := channelsTestApp(t, map[string]bool{"manageChannels": true})
	c.closeSettings()
	if c.channels.Loaded || c.channels.Saving != "" {
		t.Fatalf("closing settings must invalidate channels state: %+v", c.channels)
	}

	d := channelsTestApp(t, map[string]bool{"manageChannels": true})
	oldSeq = atomic.LoadInt64(&d.channels.Seq)
	d.setSettingsCategory("agent")
	if atomic.LoadInt64(&d.channels.Seq) <= oldSeq || d.channels.Loaded {
		t.Fatalf("changing settings category must invalidate channels state: %+v", d.channels)
	}

	e := channelsTestApp(t, map[string]bool{"manageChannels": true})
	e.channels.Saving = "feishu"
	oldSeq = atomic.LoadInt64(&e.channels.Seq)
	e.setFeatures(nil)
	e.ensureSettingsTabVisible()
	if e.settingsTab == "channels" {
		t.Fatal("capability fallback did not leave the channels tab")
	}
	if atomic.LoadInt64(&e.channels.Seq) <= oldSeq || e.channels.Saving != "" {
		t.Fatalf("capability fallback must invalidate channels state: %+v", e.channels)
	}
}
