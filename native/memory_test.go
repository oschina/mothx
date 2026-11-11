package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
)

func memoryTestApp(t *testing.T, features map[string]bool) *nativeApp {
	t.Helper()
	a := testApp()
	a.themeSet = true
	a.dark = true
	a.setFeatures(features)
	a.settingsOpen = true
	a.settingsCategory = "integrations"
	a.settingsTab = "memory"
	a.ensureSettingsTabVisible()
	a.memory.Content = "# Memory\n\n- keep tests deterministic"
	a.memory.Size = memoryContentBytes(a.memory.Content)
	a.memory.Path = "/home/user/.mothx/memory.md"
	a.memory.Source = "global"
	a.memory.UpdatedAt = "2026-11-11T08:30:00Z"
	a.memory.Loaded = true
	return a
}

func TestMemoryViewDecodeAndByteBoundaryValidation(t *testing.T) {
	raw := json.RawMessage(`{
		"content": "remember the tests",
		"path": "/home/user/.mothx/memory.md",
		"source": "global",
		"size": 19,
		"updatedAt": "2026-11-11T08:30:00Z"
	}`)
	var view memoryView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.Content != "remember the tests" || view.Path != "/home/user/.mothx/memory.md" ||
		view.Source != "global" || view.Size != 19 || view.UpdatedAt != "2026-11-11T08:30:00Z" {
		t.Fatalf("memory view = %+v", view)
	}

	if got := memoryContentBytes("记忆"); got != 6 {
		t.Fatalf("byte size = %d, want 6", got)
	}
	if err := validateMemoryContent(strings.Repeat("a", memoryMaxBytes)); err != nil {
		t.Fatalf("exactly 1 MB must be accepted at the server boundary: %v", err)
	}
	if err := validateMemoryContent(strings.Repeat("a", memoryMaxBytes+1)); err == nil ||
		!strings.Contains(err.Error(), "1 MB") {
		t.Fatalf("one byte over 1 MB must fail, got %v", err)
	}

	// The limit is bytes, not runes: this string is under 1M runes but over
	// 1 MB once UTF-8 encoded.
	multiByte := strings.Repeat("记", memoryMaxBytes/3+1)
	if len([]rune(multiByte)) >= memoryMaxBytes {
		t.Fatalf("test fixture is not rune-under/byte-over: runes=%d", len([]rune(multiByte)))
	}
	if memoryContentValid(multiByte) {
		t.Fatalf("multi-byte content of %d bytes must be rejected", memoryContentBytes(multiByte))
	}

	request := buildMemoryPutRequest("hello")
	if request["content"] != "hello" {
		t.Fatalf("put request = %#v", request)
	}
}

func TestMemoryPanelHeadlessRendersCardTextareaAndMetadata(t *testing.T) {
	a := memoryTestApp(t, map[string]bool{"manageMemory": true})
	tt := ui.NewTester(a.view, 1180, 900)
	for _, want := range []string{
		"全局记忆", "/home/user/.mothx/memory.md", "记忆内容", "memory.md",
		"大小：36 字节", "当前 36 字节", "上限 1 MB", "更新时间：", "保存记忆",
	} {
		if !textPresent(tt, want) {
			t.Fatalf("memory panel missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("需要 ACP 能力 manageMemory") {
		t.Fatalf("supported memory panel rendered the unsupported gate: %q", tt.Texts())
	}

	a.memory.Loading = true
	a.memory.Loaded = false
	tt.Frame()
	if !textPresent(tt, "正在读取全局记忆…") {
		t.Fatalf("memory loading state missing: %q", tt.Texts())
	}

	a.memory.Loading = false
	a.memory.Err = "读取记忆失败：boom"
	tt.Frame()
	for _, want := range []string{"记忆管理不可用", "读取记忆失败：boom", "重试"} {
		if !textPresent(tt, want) {
			t.Fatalf("memory error state missing %q: %q", want, tt.Texts())
		}
	}
}

func TestMemoryCapabilityGatePreventsLoadAndHidesTab(t *testing.T) {
	a := testApp()
	a.setFeatures(nil)
	a.settingsOpen = true
	a.settingsTab = "memory"
	a.startMemoryPanelLoad()
	if a.memoryCapable() {
		t.Fatal("manageMemory must be required")
	}
	if a.memory.Loading || !a.memory.Loaded || !strings.Contains(a.memory.Err, "manageMemory") {
		t.Fatalf("gated load state = %+v", a.memory)
	}

	a.settingsCategory = "integrations"
	a.settingsTab = "memory"
	a.ensureSettingsTabVisible()
	if a.settingsTab == "memory" {
		t.Fatalf("memory tab must be hidden without manageMemory: %+v", a.memory)
	}
}

func TestMemorySaveValidatesBoundaryAndReportsOfflineErrors(t *testing.T) {
	a := memoryTestApp(t, map[string]bool{"manageMemory": true})

	a.memory.Content = strings.Repeat("a", memoryMaxBytes+1)
	a.saveMemoryContent()
	if a.memory.Saving {
		t.Fatal("oversized content must not enter the saving state")
	}
	if !a.memory.StatusIsError || !strings.Contains(a.memory.Status, "1 MB") {
		t.Fatalf("oversized save state = %+v", a.memory)
	}

	a.memory.Status = ""
	a.memory.StatusIsError = false
	a.memory.Content = "hello"
	a.saveMemoryContent()
	if a.memory.Saving {
		t.Fatalf("offline save must not remain in flight: %+v", a.memory)
	}
	if !a.memory.StatusIsError || !strings.Contains(a.memory.Status, "尚未连接") {
		t.Fatalf("offline save state = %+v", a.memory)
	}

	// A successful put response carries metadata but no content; the panel must
	// keep the edited content and adopt the Runtime-authoritative size/time.
	a.applyMemoryPutView(&memoryView{
		Path:      "/home/user/.mothx/memory.md",
		Source:    "global",
		Size:      5,
		UpdatedAt: "2026-11-11T09:00:00Z",
	}, "hello")
	if a.memory.Content != "hello" || a.memory.Size != 5 || !a.memory.Loaded ||
		a.memory.UpdatedAt != "2026-11-11T09:00:00Z" {
		t.Fatalf("put view application = %+v", a.memory)
	}
}

func TestMemoryPanelSequenceGuardsAndInvalidation(t *testing.T) {
	a := memoryTestApp(t, map[string]bool{"manageMemory": true})
	seq := atomic.LoadInt64(&a.memory.Seq)
	reloadSeq := atomic.LoadInt64(&a.reloadSeq)
	if !a.memoryPanelCurrent(seq, reloadSeq) {
		t.Fatal("current memory request should be accepted")
	}
	atomic.AddInt64(&a.reloadSeq, 1)
	if a.memoryPanelCurrent(seq, reloadSeq) {
		t.Fatal("a response from the previous ACP connection must be rejected")
	}

	b := memoryTestApp(t, map[string]bool{"manageMemory": true})
	b.memory.Saving = true
	b.memory.Status = "正在保存记忆…"
	oldSeq := atomic.LoadInt64(&b.memory.Seq)
	b.setSettingsTab("appearance")
	if atomic.LoadInt64(&b.memory.Seq) <= oldSeq {
		t.Fatal("leaving the memory tab must invalidate in-flight state")
	}
	if b.memory.Loaded || b.memory.Saving || b.memory.Content != "" || b.memory.Path != "" ||
		b.memory.UpdatedAt != "" {
		t.Fatalf("memory state was not invalidated on tab exit: %+v", b.memory)
	}

	c := memoryTestApp(t, map[string]bool{"manageMemory": true})
	c.closeSettings()
	if c.memory.Loaded || c.memory.Saving || c.memory.Content != "" || c.memory.Path != "" {
		t.Fatalf("closing settings must invalidate memory state: %+v", c.memory)
	}

	d := memoryTestApp(t, map[string]bool{"manageMemory": true})
	oldSeq = atomic.LoadInt64(&d.memory.Seq)
	d.setSettingsCategory("agent")
	if atomic.LoadInt64(&d.memory.Seq) <= oldSeq || d.memory.Loaded {
		t.Fatalf("changing settings category must invalidate memory state: %+v", d.memory)
	}
}
