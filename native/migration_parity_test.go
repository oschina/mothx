package main

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageCacheProjectionReadsOnlyMothxDevMeta(t *testing.T) {
	meta := map[string]json.RawMessage{
		"mothx.dev": json.RawMessage(`{"cacheRead":40,"cacheWrite":8,"totalInputTokens":58}`),
	}
	cache := usageCacheFromMeta(meta)
	if cache == nil || cache.CacheRead != 40 || cache.CacheWrite != 8 || cache.TotalInputTokens != 58 {
		t.Fatalf("cache = %+v", cache)
	}
	if usageCacheFromMeta(map[string]json.RawMessage{"mothx.dev": json.RawMessage(`{"cacheRead":10}`)}) != nil {
		t.Fatal("missing totalInputTokens must not invent a projection")
	}
	if usageCacheFromMeta(nil) != nil {
		t.Fatal("nil meta must not project cache")
	}
}

func TestUsageUpdateStoresCacheProjection(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "usage_update",
		Used:          100,
		Size:          1000,
		Meta: map[string]json.RawMessage{
			"mothx.dev": json.RawMessage(`{"cacheRead":60,"cacheWrite":10,"totalInputTokens":90}`),
		},
	}})
	if a.usageCache == nil || a.usageCache.CacheRead != 60 || a.usageCache.TotalInputTokens != 90 {
		t.Fatalf("usage cache = %+v", a.usageCache)
	}
}

func TestTTranslatesDynamicErrorPrefix(t *testing.T) {
	setLocale("en")
	t.Cleanup(func() { setLocale("zh") })
	got := T("读取供应商模型失败：connection refused")
	if got != "Failed to read provider models: connection refused" {
		t.Fatalf("dynamic translation = %q", got)
	}
}

func TestTranscriptWindowKeepsNewestMessages(t *testing.T) {
	a := testApp()
	for i := 0; i < transcriptRenderWindow+5; i++ {
		a.messages = append(a.messages, &chatMessage{ID: string(rune('a' + i%26))})
	}
	window, hidden := a.transcriptWindow()
	if len(window) != transcriptRenderWindow || hidden != 5 {
		t.Fatalf("window = %d hidden = %d", len(window), hidden)
	}
	if window[len(window)-1] != a.messages[len(a.messages)-1] {
		t.Fatal("window must end at the newest message")
	}
}

func TestForkRequestCarriesExpertOnlyWhenReplacing(t *testing.T) {
	session := listedSession{SessionID: "s1", Cwd: "/work"}
	plain := buildSessionForkRequest(session)
	if _, ok := plain["expertId"]; ok {
		t.Fatalf("plain fork must not carry expertId: %+v", plain)
	}
	withExpert := buildSessionForkRequest(session, "team-1")
	if withExpert["expertId"] != "team-1" {
		t.Fatalf("expert fork = %+v", withExpert)
	}
}

func TestImageContentDecodesIntoMessage(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	// 1x1 transparent PNG.
	const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	raw, _ := json.Marshal(contentBlock{Type: "image", MimeType: "image/png", Data: pixel})
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "agent_message_chunk",
		MessageID:     "m1",
		Content:       raw,
	}})
	if len(a.messages) != 1 || len(a.messages[0].Images) != 1 || a.messages[0].Images[0].Image == nil {
		t.Fatalf("image message = %+v", a.messages)
	}
	if _, err := base64.StdEncoding.DecodeString(pixel); err != nil {
		t.Fatal(err)
	}
}

func TestStatusLineFollowsRunAndSettles(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.handleSessionEvent(sessionEvent{SessionID: "s1", Event: "turn_started"})
	if a.statusMessageID == "" {
		t.Fatal("turn_started did not create a status line")
	}
	m := a.messageByID(a.statusMessageID)
	if m == nil || m.Kind != messageStatus || m.Text != "正在生成…" {
		t.Fatalf("status line = %+v", m)
	}
	a.handleSessionEvent(sessionEvent{SessionID: "s1", Event: "terminal", Status: "failed", Error: "boom"})
	if m := a.messageByID(a.statusMessageID); m == nil || !m.Done || m.Text != "运行失败" {
		t.Fatalf("settled status = %+v", m)
	}
	if len(a.messages) != 2 || a.messages[1].Kind != messageError || a.messages[1].Text != "boom" {
		t.Fatalf("structured error message missing: %+v", a.messages)
	}
}

func TestStructuredTerminalErrorCarriesCode(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.handleSessionEvent(sessionEvent{
		SessionID: "s1",
		Event:     "terminal",
		Status:    "failed",
		Error:     "provider unavailable",
		ErrorInfo: map[string]any{"code": "provider_unavailable", "retryable": true, "detail": "upstream 503"},
	})
	found := false
	for _, m := range a.messages {
		if m.Kind == messageError {
			found = true
			if m.ErrorCode != "provider_unavailable" || !m.ErrorRetryable || !strings.Contains(m.Text, "upstream 503") {
				t.Fatalf("error card = %+v", m)
			}
		}
	}
	if !found {
		t.Fatalf("no error card: %+v", a.messages)
	}
}

func TestNativePreferencesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), nativePreferencesFile)
	a := testApp()
	a.prefsPath = path
	setRuntimeBinaryPreference("", false)
	t.Cleanup(func() { setRuntimeBinaryPreference("", false) })
	a.themeMode = "dark"
	a.home.preset = "design"
	a.home.logoVisible = false
	a.home.logoPath = "/tmp/logo.png"
	a.home.backgroundPath = "/tmp/bg.png"
	a.home.backgroundOpacity = 0.42
	a.home.backgroundBlur = 3.5
	a.home.backgroundFit = "contain"
	a.home.backgroundPosition = "topright"
	setRuntimeBinaryPreference("/opt/mothx", false)
	a.saveNativePreferences()

	prefs, ok := loadNativePreferences(path)
	if !ok {
		t.Fatal("preferences not saved")
	}
	if prefs.ThemeMode != "dark" || prefs.HomePreset != "design" || prefs.HomeBackgroundFit != "contain" ||
		prefs.HomeBackgroundPosition != "topright" || prefs.HomeBackgroundOpacity == nil || *prefs.HomeBackgroundOpacity != 0.42 || prefs.HomeLogoVisible == nil || *prefs.HomeLogoVisible {
		t.Fatalf("preferences = %+v", prefs)
	}
	if prefs.RuntimeBinary != "/opt/mothx" {
		t.Fatalf("runtime binary = %q", prefs.RuntimeBinary)
	}

	// A fully transparent home background is a legitimate persisted value: it
	// must survive the round trip instead of being read back as the default 1.
	a.home.backgroundOpacity = 0
	a.saveNativePreferences()
	prefs, ok = loadNativePreferences(path)
	if !ok || prefs.HomeBackgroundOpacity == nil || *prefs.HomeBackgroundOpacity != 0 {
		t.Fatalf("opacity 0 was not persisted: %+v", prefs)
	}
	restored := testApp()
	restored.prefsPath = path
	restored.applyNativePreferences(prefs)
	if restored.home.backgroundOpacity != 0 {
		t.Fatalf("opacity 0 was not restored: %v", restored.home.backgroundOpacity)
	}
}
