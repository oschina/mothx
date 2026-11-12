package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"
)

// transcriptExportFilename derives a safe default filename from the current
// session title. The selected path still comes from the OS save dialog; this
// client never writes into the worktree or an artifact store directly.
func (a *nativeApp) transcriptExportFilename() string {
	title := strings.TrimSpace(a.sessionTitle())
	if title == "" {
		title = "mothx-transcript"
	}
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-", "?", "-",
		"\"", "-", "<", "-", ">", "-", "|", "-", "\n", " ", "\r", " ",
	)
	title = strings.TrimSpace(replacer.Replace(title))
	title = strings.Trim(title, ". ")
	if title == "" {
		title = "mothx-transcript"
	}
	if len(title) > 80 {
		title = title[:80]
	}
	return title + ".md"
}

// transcriptExportText is the full canonical transcript, independent of the
// 300-entry render window.
func (a *nativeApp) transcriptExportText() string {
	text := a.transcriptPlainText()
	if strings.TrimSpace(text) == "" {
		return ""
	}
	title := strings.TrimSpace(a.sessionTitle())
	if title == "" {
		title = "MothX transcript"
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n\n")
	b.WriteString(text)
	b.WriteString("\n")
	return b.String()
}

func (a *nativeApp) copyTranscript() {
	text := a.transcriptPlainText()
	if strings.TrimSpace(text) == "" {
		a.showToast(T("当前会话还没有可复制的转录"))
		return
	}
	a.services.WriteClipboard(text)
	a.showToast(T("已复制完整转录"))
}

func (a *nativeApp) exportTranscript() {
	text := a.transcriptExportText()
	if strings.TrimSpace(text) == "" {
		a.showToast(T("当前会话还没有可导出的转录"))
		return
	}
	filename := a.transcriptExportFilename()
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       T("导出转录"),
			DefaultPath: filename,
		})
		if err != nil || strings.TrimSpace(path) == "" {
			return
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			a.dispatch(func() { a.showErrorToast(T("导出转录失败：") + err.Error()) })
			return
		}
		a.dispatch(func() { a.showToast(fmt.Sprintf("%s%s", T("已导出转录到 "), path)) })
	}()
}
