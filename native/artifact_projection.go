package main

import (
	"path/filepath"
	"strings"
)

// publish_artifact tool fallback projection.
//
// The canonical runtime emits an `artifact` session update when a publish
// completes. Older runtimes (and sessions whose canonical artifact event is
// absent) only expose the tool call. Native therefore projects a read-only
// fallback card so the user can still see what the task produced. The card is
// never a canonical attachment: opening it without an artifactId shows the
// same "not supported" feedback as Desktop instead of reading a worktree path.

func (a *nativeApp) maybeAddToolArtifactFallback(m *chatMessage) {
	if m == nil || m.Kind != messageTool {
		return
	}
	if m.ToolStatus != "completed" && m.ToolStatus != "complete" {
		return
	}
	title := strings.TrimSpace(m.ToolTitle)
	if !strings.HasPrefix(title, "publish_artifact") {
		return
	}
	filename := toolArtifactFilename(m.ToolInput)
	if filename == "" {
		return
	}
	for _, existing := range a.messages {
		if existing.Kind == messageArtifact && !existing.ArtifactFromToolCall && existing.Filename == filename {
			return
		}
	}
	id := "artifact-tool:" + m.ToolID
	fallback := a.upsertMessage(id, messageArtifact)
	fallback.Filename = filename
	fallback.ArtifactKind = toolStringInput(m.ToolInput, "kind")
	fallback.ArtifactFromToolCall = true
	fallback.Done = true
}

func toolArtifactFilename(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	if name := strings.TrimSpace(toolStringInput(input, "filename")); name != "" {
		return name
	}
	path := strings.TrimSpace(toolStringInput(input, "path"))
	if path == "" {
		return ""
	}
	path = strings.TrimRight(path, "/\\")
	if name := filepath.Base(path); name != "" && name != "." && name != string(filepath.Separator) {
		return name
	}
	return ""
}

func toolStringInput(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}

// dropToolArtifactFallbacks removes a fallback card once the canonical
// artifact projection for the same filename arrives, so the transcript never
// shows the same artifact twice.
func (a *nativeApp) dropToolArtifactFallbacks(filename string) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return
	}
	var kept []*chatMessage
	for _, m := range a.messages {
		if m.Kind == messageArtifact && m.ArtifactFromToolCall && m.Filename == filename {
			delete(a.messageIndex, m.ID)
			continue
		}
		kept = append(kept, m)
	}
	a.messages = kept
}

// openArtifactMessage opens only canonical attachments. A publish_artifact
// fallback has no attachment identity, and a mutable worktree path is not an
// artifact capability, so it must not be read or inferred client-side.
func (a *nativeApp) openArtifactMessage(m *chatMessage) {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.ArtifactID) == "" || m.ArtifactFromToolCall {
		a.showToast(T("当前运行时未提供制品获取（mothx/attachment/fetch），等待 P0 补全"))
		return
	}
	a.openArtifactPreview(m)
}
