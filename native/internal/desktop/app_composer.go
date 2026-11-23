package desktop

import (
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *nativeApp) attachFiles() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   win,
			Title:    T("附加文件"),
			Multiple: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.dispatch(func() {
			root := a.workspace
			if root == "" {
				root = a.draftCwd
			}
			for _, p := range paths {
				abs, err := filepath.Abs(p)
				if err != nil {
					abs = p
				}
				entry := attachment{Name: filepath.Base(abs), Path: abs, MimeType: mime.TypeByExtension(filepath.Ext(abs))}
				if strings.HasPrefix(strings.ToLower(entry.MimeType), "image/") {
					if info, statErr := os.Stat(abs); statErr == nil && info.Size() <= nativeMaxEmbeddedAttachment {
						if data, readErr := os.ReadFile(abs); readErr == nil {
							if image, decodeErr := ui.DecodeImage(data); decodeErr == nil {
								entry.Preview = image
							}
						}
					}
				}
				if !pathWithinDirectory(root, abs) {
					info, statErr := os.Stat(abs)
					if statErr != nil {
						a.status = "无法读取附件：" + entry.Name
						continue
					}
					if info.Size() > nativeMaxEmbeddedAttachment {
						a.status = "附件过大，未附加：" + entry.Name
						continue
					}
					data, readErr := os.ReadFile(abs)
					if readErr != nil {
						a.status = "无法读取附件：" + entry.Name
						continue
					}
					entry.Data = base64.StdEncoding.EncodeToString(data)
					entry.Size = info.Size()
					entry.Embedded = true
				}
				a.attachments = append(a.attachments, entry)
			}
		})
	}()
}

func (a *nativeApp) removeAttachment(i int) {
	if i < 0 || i >= len(a.attachments) {
		return
	}
	a.attachments = append(a.attachments[:i], a.attachments[i+1:]...)
}

func (a *nativeApp) send() {
	text := strings.TrimSpace(a.draft)
	if text == "" || a.running || a.creatingSession {
		return
	}
	if a.conn != connReady {
		a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "尚未连接到 MothX ACP。", Done: true})
		return
	}
	if a.currentSession == "" && a.draftSource == "new-task" &&
		strings.TrimSpace(a.workspace) == "" && strings.TrimSpace(a.draftCwd) == "" {
		// Desktop asks again at send time when the New Task draft still has
		// no directory; cancelling keeps the draft instead of sending nowhere.
		a.requestDraftDirectory(func(picked string) {
			if picked == "" {
				a.status = "未选择工作目录，新任务未发送"
				return
			}
			a.applyWorkspaceChoice(picked)
			a.send()
		})
		return
	}
	if a.client() == nil {
		a.appendMessage(&chatMessage{ID: a.nextLocalMessageID("error"), Kind: messageSystem, Text: "尚未连接到 MothX ACP。", Done: true})
		return
	}
	attachments := append([]attachment(nil), a.attachments...)
	sessionID := a.currentSession
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	client := a.client()
	features := a.featureSnapshot()
	draftConfig := a.cloneDraftConfig()

	a.draft = ""
	a.attachments = nil
	a.running = true
	if a.page == "home" {
		// Desktop's home composer returns to the chat view once a prompt is
		// sent; the transcript then shows the same session's run.
		a.page = ""
	}
	if sessionID == "" {
		a.creatingSession = true
	}
	a.status = "正在生成…"
	a.setStatusLine("正在生成…")
	display := text
	if len(attachments) > 0 {
		var names []string
		for _, f := range attachments {
			names = append(names, f.Name)
		}
		display += "\n\n" + T("附件：") + strings.Join(names, T("、"))
	}
	localUserID := a.nextLocalMessageID("user")
	a.appendMessage(&chatMessage{ID: localUserID, Kind: messageUser, Text: display, Done: true, Images: attachmentChatImages(attachments)})
	a.awaitingUserEcho = true
	a.localUserMessageID = localUserID

	go func() {
		if sessionID == "" {
			raw, err := client.Call("session/new", map[string]any{"cwd": cwd})
			if err != nil {
				a.promptFailed(err)
				return
			}
			var res sessionResult
			if err := json.Unmarshal(raw, &res); err != nil {
				a.promptFailed(err)
				return
			}
			sessionID = res.SessionID
			a.dispatch(func() {
				a.applySessionResult(res)
				a.currentTitle = "新会话"
				a.draftConfig = make(map[string]string)
				a.finishSessionCreation()
			})
			if warning := a.flushDraftConfig(client, sessionID, draftConfig); warning != "" {
				a.dispatch(func() { a.status = warning })
			}
			go a.refreshSessions(client, cwd, features)
		}
		blocks := []contentBlock{{Type: "text", Text: text}}
		for _, f := range attachments {
			if f.Embedded && f.Data != "" {
				blocks = append(blocks, contentBlock{Type: "resource", Name: f.Name, MimeType: f.MimeType, Data: f.Data})
				continue
			}
			uri := fileURI(f.Path)
			blocks = append(blocks, contentBlock{Type: "resource_link", Name: f.Name, URI: uri, MimeType: f.MimeType})
		}
		addDiagnostic("info", "session/prompt session="+sessionID)
		raw, err := client.Call("session/prompt", map[string]any{
			"sessionId": sessionID,
			"prompt":    blocks,
			"_meta": map[string]any{
				"mothx": map[string]any{"surface": "native"},
			},
		})
		if err != nil {
			a.promptFailed(err)
			return
		}
		var res promptResult
		_ = json.Unmarshal(raw, &res)
		a.dispatch(func() {
			a.markRunDone(res.StopReason)
			if res.StopReason != "" && res.StopReason != "end_turn" && res.StopReason != "cancelled" {
				a.appendRunFinished(res.StopReason)
			}
		})
		go a.refreshSessions(client, cwd, features)
	}()
}

// appendRunFinished records a non-normal terminal stop reason as a transcript
// system line. The canonical Chinese text stays the dictionary key; the
// transcript renderer projects it through T, so both locales read naturally and
// a later locale switch re-renders existing history consistently.
func (a *nativeApp) appendRunFinished(stopReason string) {
	a.appendMessage(&chatMessage{
		ID:   a.nextLocalMessageID("system"),
		Kind: messageSystem,
		Text: "运行结束：" + stopReason,
		Done: true,
	})
}

func (a *nativeApp) promptFailed(err error) {
	a.dispatch(func() { a.applyPromptFailure(err) })
}

// applyPromptFailure restores the idle shell after a failed send. Any deferred
// worktree intent is consumed before the error is appended, so the new chat
// keeps the failure visible instead of silently swallowing it.
func (a *nativeApp) applyPromptFailure(err error) {
	addDiagnostic("error", "prompt failed: "+err.Error())
	a.showErrorToast("发送失败：" + err.Error())
	a.running = false
	a.creatingSession = false
	a.awaitingUserEcho = false
	a.localUserMessageID = ""
	a.status = ""
	a.settleWorktreeOpen()
	a.appendErrorFromErr("发送失败：", err)
}

func pathWithinDirectory(root, path string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	return (&url.URL{Scheme: "file", Path: uriPath}).String()
}

func (a *nativeApp) setConfigOption(configID, value string) {
	if a.running || a.creatingSession {
		return
	}
	// A draft shell has no session to mutate. Keep the selection local and
	// flush it once after session/new; browsing menus must not create rows.
	if a.currentSession == "" {
		a.applyDraftConfigOption(configID, value)
		return
	}
	client := a.client()
	if client == nil {
		return
	}
	sessionID := a.currentSession
	go func() {
		raw, err := client.Call("session/set_config_option", map[string]any{
			"sessionId": sessionID,
			"configId":  configID,
			"value":     value,
		})
		if err != nil {
			a.dispatch(func() { a.status = "切换失败：" + err.Error() })
			return
		}
		var res sessionResult
		if err := json.Unmarshal(raw, &res); err == nil && len(res.ConfigOptions) > 0 {
			a.dispatch(func() {
				if a.currentSession != sessionID {
					return
				}
				a.configOptions = res.ConfigOptions
				a.syncModelState()
			})
		}
	}()
}
