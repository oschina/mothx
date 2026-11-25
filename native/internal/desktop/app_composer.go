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
			// One prompt is one request line, so the budget is shared by every
			// embedded attachment already in the composer.
			embedded := embeddedAttachmentBytes(a.attachments)
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
					if embedded+info.Size() > nativeMaxEmbeddedAttachment {
						a.status = "内嵌附件总大小超出上限，未附加：" + entry.Name
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
					embedded += info.Size()
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
	// Like Desktop, an attachment on its own is a prompt: a pasted screenshot
	// with no caption must be sendable.
	if (text == "" && len(a.attachments) == 0) || a.running || a.creatingSession {
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
	// Attaching already enforces this; the guard keeps a prompt the Runtime
	// cannot accept from clearing the composer and then waiting forever.
	if embeddedAttachmentBytes(attachments) > nativeMaxEmbeddedAttachment {
		a.status = "内嵌附件总大小超出上限，请移除部分附件"
		return
	}
	unsent := unsentPrompt{text: a.draft, attachments: attachments}
	// origin is the task the prompt is sent from: a draft ("") that still has
	// to be created, or an open session.
	origin := a.currentSession
	sessionID := a.currentSession
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	client := a.client()
	features := a.featureSnapshot()
	draftConfig := a.cloneDraftConfig()
	// The token identifies this send inside its task. A long run can answer
	// after the user switched tasks, or after a cancelled run was followed by a
	// new one, so its completion and its failure are applied only to the task
	// that still owns the token.
	a.sendSeq++
	token := a.sendSeq
	a.promptToken = token

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
		note := T("附件：") + strings.Join(names, T("、"))
		if display == "" {
			display = note
		} else {
			display += "\n\n" + note
		}
	}
	localUserID := a.nextLocalMessageID("user")
	a.appendMessage(&chatMessage{ID: localUserID, Kind: messageUser, Text: display, Done: true, Images: attachmentChatImages(attachments)})
	a.awaitingUserEcho = true
	a.localUserMessageID = localUserID

	go func() {
		if sessionID == "" {
			raw, err := client.Call("session/new", map[string]any{"cwd": cwd})
			if err != nil {
				a.promptFailed(origin, token, unsent, err)
				return
			}
			var res sessionResult
			if err := json.Unmarshal(raw, &res); err != nil {
				a.promptFailed(origin, token, unsent, err)
				return
			}
			sessionID = res.SessionID
			a.dispatch(func() { a.adoptCreatedSession(res, token) })
			if warning := a.flushDraftConfig(client, sessionID, draftConfig); warning != "" {
				a.dispatch(func() {
					if a.currentSession == sessionID {
						a.status = warning
					}
				})
			}
			go a.refreshSessions(client, cwd, features)
		}
		var blocks []contentBlock
		if text != "" {
			blocks = append(blocks, contentBlock{Type: "text", Text: text})
		}
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
			a.promptFailed(sessionID, token, unsent, err)
			return
		}
		var res promptResult
		_ = json.Unmarshal(raw, &res)
		a.dispatch(func() { a.completePrompt(sessionID, token, res.StopReason) })
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

func (a *nativeApp) promptFailed(origin string, token int64, unsent unsentPrompt, err error) {
	a.dispatch(func() { a.applyPromptFailure(origin, token, unsent, err) })
}

// unsentPrompt is what a failed send gives back to the composer.
type unsentPrompt struct {
	text        string
	attachments []attachment
}

// adoptCreatedSession folds the session a draft's prompt just created. When
// the shell still shows that draft it becomes the open task as before. The
// person may have opened another task while the session was being created;
// the new task then becomes a cached background session instead of pulling the
// shell back onto it, and the prompt still runs there.
func (a *nativeApp) adoptCreatedSession(res sessionResult, token int64) {
	if a.currentSession == "" && a.promptToken == token {
		a.applySessionResult(res)
		a.currentTitle = "新会话"
		a.draftConfig = make(map[string]string)
		a.finishSessionCreation()
		return
	}
	a.creatingSession = false
	a.withSessionState(res.SessionID, func() {
		a.applySessionResult(res)
		a.promptToken = token
		a.running = true
		a.status = "正在生成…"
	})
	a.settleWorktreeOpen()
}

// completePrompt projects the end of one task's prompt. A long run can finish
// after the user switched to another task, whose run state, status line, and
// transcript must not be touched; and a cancelled run's late answer must not
// end the run that followed it.
func (a *nativeApp) completePrompt(sessionID string, token int64, stopReason string) {
	if sessionID == "" {
		return
	}
	apply := func() {
		if a.promptToken != token {
			return
		}
		a.markRunDone(stopReason)
		if stopReason != "" && stopReason != "end_turn" && stopReason != "cancelled" {
			a.appendRunFinished(stopReason)
		}
	}
	if sessionID == a.currentSession {
		apply()
		return
	}
	a.withSessionState(sessionID, apply)
}

// applyPromptFailure restores the idle state after a failed send. Everything
// it changes belongs to the task the prompt was sent from: by the time a
// request fails the user may be looking at another task, whose run state and
// transcript must stay as they are. The unsent text and attachments go back to
// the composer when it still shows that task and is empty, so a failed first
// send (an unconfigured provider is the common case) does not cost the person
// what they typed. Any deferred worktree intent is consumed before the error
// is appended, so the new chat keeps the failure visible instead of silently
// swallowing it.
func (a *nativeApp) applyPromptFailure(origin string, token int64, unsent unsentPrompt, err error) {
	addDiagnostic("error", "prompt failed: "+err.Error())
	a.showErrorToast("发送失败：" + err.Error())
	// creatingSession is a shell flag this send set. Release it whatever the
	// user is looking at now, or New Task would stay blocked.
	a.creatingSession = false
	apply := func(toOpenTask bool) {
		if a.promptToken != token {
			return
		}
		a.running = false
		a.awaitingUserEcho = false
		localID := a.localUserMessageID
		a.localUserMessageID = ""
		a.status = ""
		a.settleWorktreeOpen()
		a.appendErrorFromErr("发送失败：", err)
		// The composer belongs to the open task. Text typed in another task
		// while this one was failing must never be replaced, and an empty
		// composer there is not this task's to fill.
		if !toOpenTask || strings.TrimSpace(a.draft) != "" || len(a.attachments) != 0 {
			return
		}
		a.draft = unsent.text
		a.attachments = append([]attachment(nil), unsent.attachments...)
		// The bubble stood for a prompt the Runtime never accepted. With its
		// text back in the composer it would only read as a duplicate.
		a.removeMessage(localID)
	}
	if origin == a.currentSession {
		apply(true)
	} else if origin != "" {
		a.withSessionState(origin, func() { apply(false) })
	}
	a.settleWorktreeOpen()
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
