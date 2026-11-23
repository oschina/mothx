// Package memory owns the Native projection of the ACP memory management
// contract (mothx/manage/memory/get and mothx/manage/memory/put). Memory is a
// single Runtime-owned memory.md text file; this panel only reads and replaces
// its content through ACP and never touches the file or settings.json directly.
package memory

import (
	"encoding/json"
	"strings"
	"sync/atomic"

	"github.com/oschina/mothx/native/internal/feature/host"
	"github.com/oschina/mothx/native/internal/i18n"
)

// MaxBytes mirrors internal/acp/manage.go manageMemoryMaxBytes. The server cap
// is measured in UTF-8 bytes and accepts exactly the boundary, so client
// validation must use bytes and allow the same value rather than runes.
const MaxBytes = 1 << 20

// View mirrors the mothx/manage/memory get/put responses. A put response omits
// content, so Content is only authoritative for get.
type View struct {
	Content   string `json:"content"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source,omitempty"`
	Size      int    `json:"size"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// State is the memory panel's projected state.
type State struct {
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

// Panel couples the memory state with its ACP lifecycle.
type Panel struct {
	State
}

func NewPanel() Panel {
	return Panel{}
}

// ContentBytes is the exact unit the ACP server uses for its 1 MB cap.
func ContentBytes(content string) int {
	return len(content)
}

func ContentValid(content string) bool {
	return ContentBytes(content) <= MaxBytes
}

func ValidateContent(content string) error {
	size := ContentBytes(content)
	if size > MaxBytes {
		return i18n.LocalizedErrorf("记忆内容为 %d 字节，超过 1 MB（%d 字节）上限", size, MaxBytes)
	}
	return nil
}

func BuildPutRequest(content string) map[string]any {
	return map[string]any{"content": content}
}

func (p *Panel) Capable(h host.Base) bool {
	return h.FeatureEnabled("manageMemory")
}

func (p *Panel) Current(h host.Base, seq, reloadSeq int64) bool {
	return h.SettingsOpen() && h.SettingsTab() == "memory" &&
		seq == atomic.LoadInt64(&p.Seq) &&
		reloadSeq == h.ReloadSequence()
}

// Invalidate drops transient state and makes every in-flight memory response
// stale. Loaded is reset so re-entering the tab reloads.
func (p *Panel) Invalidate() {
	atomic.AddInt64(&p.Seq, 1)
	p.Content = ""
	p.Path = ""
	p.Source = ""
	p.Size = 0
	p.UpdatedAt = ""
	p.Loaded = false
	p.Loading = false
	p.Saving = false
	p.Err = ""
	p.Status = ""
	p.StatusIsError = false
}

// StartLoad starts one capability-gated memory read. It is called from the UI
// thread on tab entry.
func (p *Panel) StartLoad(h host.Base) {
	if !h.SettingsOpen() || h.SettingsTab() != "memory" {
		return
	}
	if p.Loading {
		return
	}
	if !p.Capable(h) {
		p.Loaded = true
		p.Err = "当前运行时不支持记忆管理（manageMemory）。"
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

func (p *Panel) load(h host.Base, seq, startReloadSeq int64) {
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
	raw, err := client.Call("mothx/manage/memory/get", map[string]any{})
	h.Dispatch(func() {
		if !p.Current(h, seq, startReloadSeq) {
			return
		}
		p.Loading = false
		if err != nil {
			p.Err = "读取记忆失败：" + err.Error()
			return
		}
		var view View
		if err := json.Unmarshal(raw, &view); err != nil {
			p.Err = "记忆响应无效：" + err.Error()
			return
		}
		p.ApplyGetView(&view)
	})
}

func (p *Panel) ApplyGetView(view *View) {
	if view == nil {
		return
	}
	size := view.Size
	if size == 0 && view.Content != "" {
		size = ContentBytes(view.Content)
	}
	p.Content = view.Content
	p.Size = size
	p.Path = view.Path
	p.Source = view.Source
	p.UpdatedAt = view.UpdatedAt
	p.Loaded = true
	p.Loading = false
	p.Err = ""
}

func (p *Panel) ApplyPutView(view *View, content string) {
	if view == nil {
		return
	}
	size := view.Size
	if size == 0 && content != "" {
		size = ContentBytes(content)
	}
	p.Content = content
	p.Size = size
	if strings.TrimSpace(view.Path) != "" {
		p.Path = view.Path
	}
	if strings.TrimSpace(view.Source) != "" {
		p.Source = view.Source
	}
	p.UpdatedAt = view.UpdatedAt
	p.Loaded = true
	p.Err = ""
}

// SaveContent replaces the whole memory file through memory/put. The byte check
// runs before the request so an oversized payload never crosses the ACP
// boundary, and the server's 1 MB limit remains the final authority.
func (p *Panel) SaveContent(h host.Base) {
	if !p.Capable(h) {
		p.Status = "当前运行时不支持记忆管理（manageMemory）。"
		p.StatusIsError = true
		return
	}
	if p.Loading || p.Saving {
		return
	}
	if err := ValidateContent(p.Content); err != nil {
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
	content := p.Content
	p.Saving = true
	p.Status = i18n.Tf("正在保存记忆（%d 字节）…", ContentBytes(content))
	p.StatusIsError = false
	go func() {
		raw, err := client.Call("mothx/manage/memory/put", BuildPutRequest(content))
		h.Dispatch(func() {
			if !p.Current(h, seq, startReloadSeq) {
				return
			}
			p.Saving = false
			if err != nil {
				p.Status = "保存记忆失败：" + err.Error()
				p.StatusIsError = true
				return
			}
			var view View
			if err := json.Unmarshal(raw, &view); err != nil {
				p.Status = "保存后记忆响应无效：" + err.Error()
				p.StatusIsError = true
				return
			}
			p.ApplyPutView(&view, content)
			p.Status = i18n.Tf("记忆已保存（%d 字节）", p.Size)
			p.StatusIsError = false
		})
	}()
}
