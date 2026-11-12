package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// This file owns the Native artifact-preview projection. The attachment is
// fetched from the Runtime by canonical artifact ID; the client never scans a
// worktree or infers an artifact from assistant text.

type artifactFetchResult struct {
	Filename      string `json:"filename,omitempty"`
	MediaType     string `json:"mediaType,omitempty"`
	Size          int64  `json:"size,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

func (a *nativeApp) artifactPreviewCapable() bool {
	return a.featureEnabled("attachmentFetch")
}

func (a *nativeApp) openArtifactPreview(message *chatMessage) {
	if message == nil || strings.TrimSpace(message.ArtifactID) == "" {
		return
	}
	client := a.acp
	sessionID := a.currentSession
	if client == nil || sessionID == "" {
		a.status = "尚未连接到 MothX ACP"
		return
	}
	if !a.artifactPreviewCapable() {
		a.status = "当前运行时未提供附件获取（mothx/attachment/fetch）"
		return
	}
	seq := atomic.AddInt64(&a.previewSeq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.previewOpen = true
	a.previewTitle = message.Filename
	a.previewImage = nil
	a.previewData = nil
	a.previewMediaType = message.MediaType
	a.previewErr = ""
	a.previewLoading = true
	go func() {
		raw, err := client.call("mothx/attachment/fetch", map[string]any{
			"sessionId":    sessionID,
			"attachmentId": message.ArtifactID,
		})
		a.dispatch(func() {
			if seq != atomic.LoadInt64(&a.previewSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.previewLoading = false
			if err != nil {
				a.previewErr = "获取附件失败：" + err.Error()
				return
			}
			var result artifactFetchResult
			if err := json.Unmarshal(raw, &result); err != nil {
				a.previewErr = "附件响应无效：" + err.Error()
				return
			}
			if result.Filename != "" {
				a.previewTitle = result.Filename
			}
			data, err := base64.StdEncoding.DecodeString(result.ContentBase64)
			if err != nil || len(data) == 0 {
				a.previewErr = "附件内容为空或无法解码"
				return
			}
			a.previewData = data
			if result.MediaType != "" {
				a.previewMediaType = result.MediaType
			}
			image, err := ui.DecodeImage(data)
			if err != nil {
				return
			}
			a.previewImage = image
		})
	}()
}

func (a *nativeApp) closeArtifactPreview() {
	atomic.AddInt64(&a.previewSeq, 1)
	a.previewOpen = false
	a.previewTitle = ""
	a.previewImage = nil
	a.previewData = nil
	a.previewMediaType = ""
	a.previewErr = ""
	a.previewLoading = false
}

func (a *nativeApp) saveArtifactPreview() {
	if len(a.previewData) == 0 {
		return
	}
	filename := strings.TrimSpace(a.previewTitle)
	if filename == "" {
		filename = "attachment"
	}
	data := append([]byte(nil), a.previewData...)
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Title:       T("保存制品"),
			DefaultPath: filename,
		})
		if err != nil || strings.TrimSpace(path) == "" {
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			a.dispatch(func() { a.previewErr = "保存失败：" + err.Error() })
			return
		}
		a.dispatch(func() { a.status = "已保存到 " + path })
	}()
}

func (a *nativeApp) artifactPreviewDialog(c *ui.Context, p palette) {
	if !a.previewOpen {
		return
	}
	dialog := ui.DialogBase(c, &a.previewOpen, func(backdrop, dialog ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.65))
		dialog.Width(760).MaxWidthPercent(94).Padding(16).Radius(14).
			Background(p.t.Background).Border(1, p.t.Border).Gap(10)
		dialog.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, iconFile).FontSize(16).TextColor(p.t.Accent)
				ui.Text(c, a.previewTitle).FontSize(13).FontWeight(650).TextColor(p.t.Text).Grow(1).SingleLine()
				if ui.Button(c.Key("artifact-close"), T("关闭")).Clicked() {
					a.closeArtifactPreview()
				}
			})
			if a.previewLoading {
				ui.Text(c, T("正在获取制品…")).FontSize(11.5).TextColor(p.t.TextMuted)
				return
			}
			if a.previewErr != "" {
				ui.Text(c, T(a.previewErr)).FontSize(11.5).TextColor(p.t.Danger).Selectable()
				return
			}
			if a.previewImage != nil {
				if w, h := ui.ImageSize(a.previewImage); w > 0 && h > 0 {
					ui.Image(c, a.previewImage).MaxWidthPercent(100).AspectRatio(w / h).Radius(10)
				}
				return
			}
			if len(a.previewData) > 0 {
				ui.Text(c, T("该附件类型不支持 Native 预览，可保存到本地。")).
					FontSize(11.5).TextColor(p.t.TextMuted)
				if ui.PrimaryButton(c.Key("artifact-save"), T("保存附件…")).Clicked() {
					a.saveArtifactPreview()
				}
			}
		})
	})
	if dialog.Dismissed() {
		a.closeArtifactPreview()
	}
}
