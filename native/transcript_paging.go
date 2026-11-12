package main

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
// Older pages are reduced in an isolated shell and prepended, preserving the
// current transcript's newer state and message identity.
func (a *nativeApp) applyTranscriptPage(sessionID string, page *transcriptPageResult, prepend bool) {
	if page == nil || page.SessionID != "" && page.SessionID != a.currentSession && page.SessionID != a.loadingSession {
		return
	}
	if sessionID == "" {
		sessionID = a.currentSession
	}
	if !prepend {
		a.replayingHistory = true
		for _, update := range page.Updates {
			a.applyUpdate(sessionUpdateNotification{SessionID: sessionID, Update: update})
		}
		a.replayingHistory = false
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

func (a *nativeApp) loadOlderTranscript() {
	sessionID := a.currentSession
	cursor := a.transcriptNextCursor
	if sessionID == "" || cursor == "" || a.transcriptLoading || !a.transcriptPagingEnabled() {
		return
	}
	client := a.acp
	if client == nil {
		return
	}
	seq := atomic.AddInt64(&a.transcriptSeq, 1)
	a.transcriptLoading = true
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.call("mothx/session/history", map[string]any{
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
