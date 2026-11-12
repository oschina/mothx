package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPublishArtifactFallbackCardDedupedByCanonicalArtifact(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    "c1",
		Title:         "publish_artifact",
		Status:        "completed",
		RawInput:      map[string]any{"path": "/work/report.md", "kind": "text"},
	}})
	if len(a.messages) != 2 {
		t.Fatalf("messages = %d, want tool + fallback: %+v", len(a.messages), a.messages)
	}
	fallback := a.messages[1]
	if fallback.Kind != messageArtifact || !fallback.ArtifactFromToolCall || fallback.Filename != "report.md" {
		t.Fatalf("fallback = %+v", fallback)
	}
	if fallback.ArtifactID != "" {
		t.Fatalf("fallback must not invent an artifact id: %+v", fallback)
	}
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "artifact",
		ArtifactID:    "a1",
		Filename:      "report.md",
		Kind:          "text",
		Size:          42,
	}})
	var artifacts []*chatMessage
	for _, m := range a.messages {
		if m.Kind == messageArtifact {
			artifacts = append(artifacts, m)
		}
	}
	if len(artifacts) != 1 {
		t.Fatalf("canonical artifact must replace fallback, got %d: %+v", len(artifacts), artifacts)
	}
	canonical := artifacts[0]
	if canonical.ArtifactID != "a1" || canonical.ArtifactFromToolCall || canonical.ArtifactSize != 42 {
		t.Fatalf("canonical artifact = %+v", canonical)
	}
}

func TestPublishArtifactFallbackUsesFilenameInputAndDedupesExisting(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "artifact",
		ArtifactID:    "a1",
		Filename:      "named.md",
	}})
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
		SessionUpdate: "tool_call",
		ToolCallID:    "c2",
		Title:         "publish_artifact",
		Status:        "completed",
		RawInput:      map[string]any{"filename": "named.md", "path": "/work/other.md"},
	}})
	for _, m := range a.messages {
		if m.Kind == messageArtifact && m.ArtifactFromToolCall {
			t.Fatalf("fallback duplicated an existing canonical artifact: %+v", m)
		}
	}
}

func TestArtifactFallbackOpenIsNotAPathRead(t *testing.T) {
	a := testApp()
	m := &chatMessage{Kind: messageArtifact, Filename: "report.md", ArtifactFromToolCall: true}
	a.openArtifactMessage(m)
	if len(a.toasts) != 1 {
		t.Fatalf("fallback open must surface a toast, got %+v", a.toasts)
	}
	if !strings.Contains(a.toasts[0].Text, "mothx/attachment/fetch") {
		t.Fatalf("fallback toast = %q", a.toasts[0].Text)
	}
}

func TestTranscriptExportTextKeepsFullHistoryOutsideRenderWindow(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.currentTitle = "Demo / Session"
	for i := 0; i < transcriptRenderWindow+25; i++ {
		a.appendMessage(&chatMessage{
			ID:   "m:" + string(rune('a'+i%26)) + string(rune('0'+i%10)) + string(rune('0'+i/10)),
			Kind: messageUser,
			Text: "message " + string(rune('a'+i%26)),
			Done: true,
		})
	}
	text := a.transcriptExportText()
	if !strings.HasPrefix(text, "# Demo / Session") {
		t.Fatalf("export header = %q", text[:min(len(text), 40)])
	}
	if !strings.Contains(text, "## User") || len(text) < 100 {
		t.Fatalf("export text too small: %d", len(text))
	}
	if name := a.transcriptExportFilename(); name != "Demo - Session.md" {
		t.Fatalf("export filename = %q", name)
	}
}

func TestSessionMenuExposesTranscriptCopyAndExport(t *testing.T) {
	a := sidebarTestApp(t)
	a.currentSession = "s1"
	a.messages = []*chatMessage{{ID: "u1", Kind: messageUser, Text: "hello", Done: true}}
	a.messageIndex = map[string]*chatMessage{"u1": a.messages[0]}
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("会话操作"); err != nil {
		t.Fatalf("open session menu: %v", err)
	}
	menu := tt.Menu()
	foundCopy, foundExport := false, false
	for _, item := range menu {
		if item == "复制完整转录" {
			foundCopy = true
		}
		if item == "导出转录…" {
			foundExport = true
		}
	}
	if !foundCopy || !foundExport {
		t.Fatalf("session menu transcript actions = %v", menu)
	}
}

func TestArtifactKindAndSizeDecode(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	raw := json.RawMessage(`{"sessionId":"s1","update":{"sessionUpdate":"artifact","artifactId":"a9","filename":"photo.png","mediaType":"image/png","kind":"image","size":1234}}`)
	var n sessionUpdateNotification
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatal(err)
	}
	a.applyUpdate(n)
	m := a.messageByID("artifact:a9")
	if m == nil || m.ArtifactKind != "image" || m.ArtifactSize != 1234 || m.MediaType != "image/png" {
		t.Fatalf("artifact projection = %+v", m)
	}
}
