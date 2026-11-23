package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/feature/memory"
)

type memoryView = memory.View

const memoryMaxBytes = memory.MaxBytes

func memoryContentBytes(content string) int      { return memory.ContentBytes(content) }
func memoryContentValid(content string) bool     { return memory.ContentValid(content) }
func validateMemoryContent(content string) error { return memory.ValidateContent(content) }
func buildMemoryPutRequest(content string) map[string]any {
	return memory.BuildPutRequest(content)
}
func memoryUpdatedAtLabel(updatedAt string) string { return memory.UpdatedAtLabel(updatedAt) }

func (a *nativeApp) memoryCapable() bool {
	return a.memory.Capable(a)
}

func (a *nativeApp) memoryPanelCurrent(seq, reloadSeq int64) bool {
	return a.memory.Current(a, seq, reloadSeq)
}

func (a *nativeApp) invalidateMemoryPanel() {
	a.memory.Invalidate()
}

func (a *nativeApp) startMemoryPanelLoad() {
	a.memory.StartLoad(a)
}

func (a *nativeApp) applyMemoryGetView(view *memory.View) {
	a.memory.ApplyGetView(view)
}

func (a *nativeApp) applyMemoryPutView(view *memory.View, content string) {
	a.memory.ApplyPutView(view, content)
}

func (a *nativeApp) saveMemoryContent() {
	a.memory.SaveContent(a)
}

func (a *nativeApp) settingsMemoryPane(c *ui.Context, p palette) {
	a.memory.View(a, c, p)
}
