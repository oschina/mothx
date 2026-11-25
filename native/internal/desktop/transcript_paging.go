package desktop

import (
	"encoding/json"
	"sync/atomic"
)

// This file owns transcript paging for the Native chat shell. The Runtime
// returns a bounded page of canonical session updates and an older cursor;
// this client only replays them through the same reducer as live updates.

const transcriptPageSize = 15

func (a *nativeApp) transcriptPagingEnabled() bool {
	return a.featureEnabled("sessionHistoryPaging")
}

// applyTranscriptPage folds one canonical history page into the transcript.
// The first page is merged into whatever the task already shows; older pages
// are reduced in an isolated shell and prepended, preserving the current
// transcript's newer state and message identity.
func (a *nativeApp) applyTranscriptPage(sessionID string, page *transcriptPageResult, prepend bool) {
	if page == nil || page.SessionID != "" && page.SessionID != a.currentSession && page.SessionID != a.loadingSession {
		return
	}
	if sessionID == "" {
		sessionID = a.currentSession
	}
	if !prepend {
		a.mergeInitialTranscriptPage(sessionID, page)
		a.transcriptNextCursor = page.NextCursor
		return
	}
	temp := &nativeApp{
		currentSession:   sessionID,
		messageIndex:     make(map[string]*chatMessage),
		replayingHistory: true,
	}
	for _, update := range page.Updates {
		temp.applyUpdate(sessionUpdateNotification{SessionID: sessionID, Update: update})
	}
	existing := make(map[string]bool, len(a.messages))
	for _, message := range a.messages {
		existing[message.ID] = true
	}
	older := make([]*chatMessage, 0, len(temp.messages))
	for _, message := range temp.messages {
		if existing[message.ID] {
			continue
		}
		// Older pages are entirely history: a reasoning block that ends the
		// page has already finished streaming, even without a following event.
		if message.Kind == messageThought {
			message.Done = true
		}
		older = append(older, message)
	}
	if len(older) > 0 {
		a.messages = append(older, a.messages...)
		a.messageIndex = make(map[string]*chatMessage, len(a.messages))
		for _, message := range a.messages {
			a.messageIndex[message.ID] = message
		}
	}
	a.transcriptNextCursor = page.NextCursor
}

// transcriptMessageUpdate reports whether an update builds a transcript
// message. Anything else a page may carry (plans, artifacts, later additions)
// keeps applying to the visible transcript directly.
func transcriptMessageUpdate(kind string) bool {
	switch kind {
	case "user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "tool_call", "tool_call_update":
		return true
	}
	return false
}

// mergeInitialTranscriptPage folds the canonical first page into a transcript
// that may already show the same turns. A task keeps its cached transcript
// while it is in the background, so the page meets messages that an earlier
// load replayed (same ids, refreshed in place) and messages that streamed live
// (stream ids the page never reuses, adopted under their canonical id). Only
// messages the transcript has never seen are appended, so switching back to a
// task never shows a turn twice.
func (a *nativeApp) mergeInitialTranscriptPage(sessionID string, page *transcriptPageResult) {
	if sessionID != a.currentSession {
		return
	}
	temp := &nativeApp{
		currentSession:   sessionID,
		messageIndex:     make(map[string]*chatMessage),
		replayingHistory: true,
	}
	a.replayingHistory = true
	defer func() { a.replayingHistory = false }()
	for _, update := range page.Updates {
		notification := sessionUpdateNotification{SessionID: sessionID, Update: update}
		if transcriptMessageUpdate(update.SessionUpdate) {
			temp.applyUpdate(notification)
			continue
		}
		a.applyUpdate(notification)
	}

	cursor := 0
	var added []*chatMessage
	for _, message := range temp.messages {
		if prior := a.messageByID(message.ID); prior != nil {
			prior.absorbReplay(message)
			if index := a.messagePosition(prior); index >= cursor {
				cursor = index + 1
			}
			continue
		}
		if index := a.findLiveCounterpart(message, cursor); index >= 0 {
			cursor = index + 1
			continue
		}
		added = append(added, message)
	}
	for _, message := range added {
		a.appendMessage(message)
	}
}

// messagePosition is the position of one transcript message.
func (a *nativeApp) messagePosition(message *chatMessage) int {
	for index, candidate := range a.messages {
		if candidate == message {
			return index
		}
	}
	return -1
}

// findLiveCounterpart locates the live message a replayed message stands for.
// The Runtime identifies a block one way while it streams and another when it
// is replayed, so identity comes from position and complete text: only a
// finished message that was not itself replayed, after everything matched so
// far, with the same kind and the same text qualifies. The live message then
// takes the canonical id, so later loads and older pages meet it by id.
func (a *nativeApp) findLiveCounterpart(message *chatMessage, from int) int {
	switch message.Kind {
	case messageUser, messageAssistant, messageThought:
	default:
		return -1
	}
	if message.Text == "" {
		return -1
	}
	for index := from; index < len(a.messages); index++ {
		candidate := a.messages[index]
		if candidate.FromHistory || candidate.Kind != message.Kind || !candidate.Done {
			continue
		}
		// The optimistic bubble is still waiting for the server echo.
		if candidate.ID == a.localUserMessageID {
			continue
		}
		if candidate.Text != message.Text {
			continue
		}
		delete(a.messageIndex, candidate.ID)
		candidate.ID = message.ID
		candidate.FromHistory = true
		a.messageIndex[candidate.ID] = candidate
		return index
	}
	return -1
}

func (a *nativeApp) loadOlderTranscript() {
	sessionID := a.currentSession
	cursor := a.transcriptNextCursor
	if sessionID == "" || cursor == "" || a.transcriptLoading || !a.transcriptPagingEnabled() {
		return
	}
	client := a.client()
	if client == nil {
		return
	}
	seq := atomic.AddInt64(&a.transcriptSeq, 1)
	a.transcriptLoading = true
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.Call("mothx/session/history", map[string]any{
			"sessionId": sessionID,
			"cursor":    cursor,
			"limit":     transcriptPageSize,
		})
		a.dispatch(func() {
			if seq != atomic.LoadInt64(&a.transcriptSeq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) ||
				a.currentSession != sessionID {
				return
			}
			a.transcriptLoading = false
			if err != nil {
				a.status = "加载更早消息失败：" + err.Error()
				return
			}
			var page transcriptPageResult
			if err := json.Unmarshal(raw, &page); err != nil {
				a.status = "更早消息响应无效：" + err.Error()
				return
			}
			if page.SessionID == "" {
				page.SessionID = sessionID
			}
			a.applyTranscriptPage(sessionID, &page, true)
		})
	}()
}
