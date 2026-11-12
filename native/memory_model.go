package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

// This file owns the Native projection of the ACP memory management contract
// (mothx/manage/memory/get and mothx/manage/memory/put). Memory is a single
// Runtime-owned memory.md text file; this panel only reads and replaces its
// content through ACP and never touches the file or settings.json directly.

// memoryMaxBytes mirrors internal/acp/manage.go manageMemoryMaxBytes. The
// server cap is measured in UTF-8 bytes and accepts exactly the boundary, so
// client validation must use bytes and allow the same value rather than runes.
const memoryMaxBytes = 1 << 20

// memoryView mirrors the mothx/manage/memory get/put responses. A put response
// omits content, so Content is only authoritative for get.
type memoryView struct {
	Content   string `json:"content"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source,omitempty"`
	Size      int    `json:"size"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type memoryPanelState struct {
	Content   string
	Path      string
	Source    string
	Size      int
	UpdatedAt string

	Loaded  bool
	Loading bool
	Saving  bool
	Err     string

	Status        string
	StatusIsError bool

	// Seq guards every asynchronous memory call. Bumping it invalidates any
	// in-flight get/put response, which is how tab exits, settings close, and
	// reconnects prevent stale state from landing.
	Seq int64
}

func newMemoryPanelState() memoryPanelState {
	return memoryPanelState{}
}

// memoryContentBytes is the exact unit the ACP server uses for its 1 MB cap.
func memoryContentBytes(content string) int {
	return len(content)
}

func memoryContentValid(content string) bool {
	return memoryContentBytes(content) <= memoryMaxBytes
}

func validateMemoryContent(content string) error {
	size := memoryContentBytes(content)
	if size > memoryMaxBytes {
		return localizedErrorf("记忆内容为 %d 字节，超过 1 MB（%d 字节）上限", size, memoryMaxBytes)
	}
	return nil
}

func buildMemoryPutRequest(content string) map[string]any {
	return map[string]any{"content": content}
}

func (a *nativeApp) memoryCapable() bool {
	return a.featureEnabled("manageMemory")
}

func (a *nativeApp) memoryPanelCurrent(seq, reloadSeq int64) bool {
	return a.settingsOpen && a.settingsTab == "memory" &&
		seq == atomic.LoadInt64(&a.memory.Seq) &&
		reloadSeq == atomic.LoadInt64(&a.reloadSeq)
}

// invalidateMemoryPanel drops transient state and makes every in-flight memory
// response stale. Loaded is reset so re-entering the tab reloads.
func (a *nativeApp) invalidateMemoryPanel() {
	m := &a.memory
	atomic.AddInt64(&m.Seq, 1)
	m.Content = ""
	m.Path = ""
	m.Source = ""
	m.Size = 0
	m.UpdatedAt = ""
	m.Loaded = false
	m.Loading = false
	m.Saving = false
	m.Err = ""
	m.Status = ""
	m.StatusIsError = false
}

// startMemoryPanelLoad starts one capability-gated memory read. It is called
// from the UI thread on tab entry.
func (a *nativeApp) startMemoryPanelLoad() {
	if !a.settingsOpen || a.settingsTab != "memory" {
		return
	}
	m := &a.memory
	if m.Loading {
		return
	}
	if !a.memoryCapable() {
		m.Loaded = true
		m.Err = "当前运行时不支持记忆管理（manageMemory）。"
		return
	}
	seq := atomic.AddInt64(&m.Seq, 1)
	m.Loading = true
	m.Err = ""
	m.Status = ""
	m.StatusIsError = false
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go a.loadMemoryPanel(seq, startReloadSeq)
}

func (a *nativeApp) loadMemoryPanel(seq, startReloadSeq int64) {
	client := a.acp
	if client == nil {
		a.dispatch(func() {
			if !a.memoryPanelCurrent(seq, startReloadSeq) {
				return
			}
			a.memory.Loading = false
			a.memory.Err = "尚未连接到 MothX ACP。"
		})
		return
	}
	raw, err := client.call("mothx/manage/memory/get", map[string]any{})
	a.dispatch(func() {
		if !a.memoryPanelCurrent(seq, startReloadSeq) {
			return
		}
		m := &a.memory
		m.Loading = false
		if err != nil {
			m.Err = "读取记忆失败：" + err.Error()
			return
		}
		var view memoryView
		if err := json.Unmarshal(raw, &view); err != nil {
			m.Err = "记忆响应无效：" + err.Error()
			return
		}
		a.applyMemoryGetView(&view)
	})
}

func (a *nativeApp) applyMemoryGetView(view *memoryView) {
	if view == nil {
		return
	}
	m := &a.memory
	size := view.Size
	if size == 0 && view.Content != "" {
		size = memoryContentBytes(view.Content)
	}
	m.Content = view.Content
	m.Size = size
	m.Path = view.Path
	m.Source = view.Source
	m.UpdatedAt = view.UpdatedAt
	m.Loaded = true
	m.Loading = false
	m.Err = ""
}

func (a *nativeApp) applyMemoryPutView(view *memoryView, content string) {
	if view == nil {
		return
	}
	m := &a.memory
	size := view.Size
	if size == 0 && content != "" {
		size = memoryContentBytes(content)
	}
	m.Content = content
	m.Size = size
	if strings.TrimSpace(view.Path) != "" {
		m.Path = view.Path
	}
	if strings.TrimSpace(view.Source) != "" {
		m.Source = view.Source
	}
	m.UpdatedAt = view.UpdatedAt
	m.Loaded = true
	m.Err = ""
}

// saveMemoryContent replaces the whole memory file through memory/put. The
// byte check runs before the request so an oversized payload never crosses the
// ACP boundary, and the server's 1 MB limit remains the final authority.
func (a *nativeApp) saveMemoryContent() {
	m := &a.memory
	if !a.memoryCapable() {
		m.Status = "当前运行时不支持记忆管理（manageMemory）。"
		m.StatusIsError = true
		return
	}
	if m.Loading || m.Saving {
		return
	}
	if err := validateMemoryContent(m.Content); err != nil {
		m.Status = "保存失败：" + err.Error()
		m.StatusIsError = true
		return
	}
	client := a.acp
	if client == nil {
		m.Status = "保存失败：尚未连接到 MothX ACP。"
		m.StatusIsError = true
		return
	}
	seq := atomic.AddInt64(&m.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	content := m.Content
	m.Saving = true
	m.Status = Tf("正在保存记忆（%d 字节）…", memoryContentBytes(content))
	m.StatusIsError = false
	go func() {
		raw, err := client.call("mothx/manage/memory/put", buildMemoryPutRequest(content))
		a.dispatch(func() {
			if !a.memoryPanelCurrent(seq, startReloadSeq) {
				return
			}
			m := &a.memory
			m.Saving = false
			if err != nil {
				m.Status = "保存记忆失败：" + err.Error()
				m.StatusIsError = true
				return
			}
			var view memoryView
			if err := json.Unmarshal(raw, &view); err != nil {
				m.Status = "保存后记忆响应无效：" + err.Error()
				m.StatusIsError = true
				return
			}
			a.applyMemoryPutView(&view, content)
			m.Status = Tf("记忆已保存（%d 字节）", m.Size)
			m.StatusIsError = false
		})
	}()
}
