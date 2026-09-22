package openaiapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

type sessionStreamEvent struct {
	Name string
	Data any
}

type sessionStreamHub struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan sessionStreamEvent]struct{}
}

func newSessionStreamHub() *sessionStreamHub {
	return &sessionStreamHub{subscribers: make(map[string]map[chan sessionStreamEvent]struct{})}
}

func (h *sessionStreamHub) subscribe(sessionID string) (<-chan sessionStreamEvent, func()) {
	ch := make(chan sessionStreamEvent, 128)
	if h == nil || sessionID == "" {
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	if h.subscribers[sessionID] == nil {
		h.subscribers[sessionID] = make(map[chan sessionStreamEvent]struct{})
	}
	h.subscribers[sessionID][ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if subs := h.subscribers[sessionID]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subscribers, sessionID)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (h *sessionStreamHub) publish(sessionID string, event sessionStreamEvent) {
	if h == nil || sessionID == "" || event.Name == "" {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers[sessionID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (s *Server) getSessionStreamHub() *sessionStreamHub {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streamHub == nil {
		s.streamHub = newSessionStreamHub()
	}
	return s.streamHub
}

func (s *Server) getEventBroker() *EventBroker {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventBroker == nil {
		s.eventBroker = NewEventBroker()
	}
	return s.eventBroker
}

func (s *Server) publishSessionStreamEvent(sessionID, eventName string, data any) {
	broker := s.getEventBroker()
	if broker == nil {
		return
	}
	// Also publish to legacy hub for any remaining subscribers
	hub := s.getSessionStreamHub()
	if hub != nil {
		hub.publish(sessionID, sessionStreamEvent{Name: eventName, Data: data})
	}
	// Extract runID from data if present
	runID := ""
	if m, ok := data.(map[string]any); ok {
		if rid, ok := m["runId"].(string); ok {
			runID = rid
		}
	}
	broker.PublishRawJSON(sessionID, runID, eventName, data)
}

func (s *Server) activeRunIDForSession(sessionID string) string {
	if s == nil || sessionID == "" {
		return ""
	}
	// The shared Runtime snapshot is authoritative, including Runs created by
	// another process or a channel adapter. Do not let a stale RunManager entry
	// manufacture a second local ownership view.
	if s.settings != nil {
		if snapshot, err := agentruntime.InspectSessionExecution(s.settings.GetSessionDir(), sessionID); err == nil {
			if snapshot.ActiveRun != nil {
				return snapshot.ActiveRun.ID
			}
			return ""
		}
		// A durable state read failure is conservatively treated as unavailable;
		// do not manufacture an active Run from adapter-local memory.
		return ""
	}
	// Fall back to in-memory cache only for embedded servers without a shared
	// session root.
	if s.pool == nil {
		return ""
	}
	sess, err := s.pool.getExact(sessionID)
	if err != nil || sess == nil {
		return ""
	}
	return sess.ActiveRunID()
}

func (s *Server) publishToolEvent(sessionID string, event ToolStatusEvent) {
	if sessionID == "" {
		return
	}
	if event.SessionID == "" {
		event.SessionID = sessionID
	}
	if event.RunID == "" {
		event.RunID = s.activeRunIDForSession(sessionID)
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	broker := s.getEventBroker()
	if broker != nil {
		broker.PublishToolEvent(sessionID, event.RunID, event)
	}
	// Also publish to legacy hub
	hub := s.getSessionStreamHub()
	if hub != nil {
		hub.publish(sessionID, sessionStreamEvent{Name: "tool_event", Data: event})
	}
}

func (s *Server) publishTranscriptEvent(sessionID string, evt TranscriptStreamEvent) {
	if sessionID == "" {
		return
	}
	if evt.XSessionID == "" {
		evt.XSessionID = sessionID
	}
	if evt.RunID == "" {
		evt.RunID = s.activeRunIDForSession(sessionID)
	}
	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	broker := s.getEventBroker()
	if broker != nil {
		broker.PublishTranscriptEvent(sessionID, evt.RunID, evt)
	}
	// Also publish to legacy hub
	hub := s.getSessionStreamHub()
	if hub != nil {
		hub.publish(sessionID, sessionStreamEvent{Name: "transcript", Data: evt})
	}
}

func (s *Server) writeTranscriptEvent(sse *SSEWriter, sessionID string, evt TranscriptStreamEvent) {
	if evt.XSessionID == "" {
		evt.XSessionID = sessionID
	}
	if evt.RunID == "" {
		evt.RunID = s.activeRunIDForSession(sessionID)
	}
	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if sse != nil {
		sse.WriteTranscriptEvent(evt)
	}
	s.publishTranscriptEvent(sessionID, evt)
}

func (s *Server) publishSessionStreamDone(sessionID, runID, status string) {
	data := map[string]any{
		"sessionId": sessionID,
		"runId":     runID,
		"status":    status,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	}
	broker := s.getEventBroker()
	if broker != nil {
		broker.PublishDone(sessionID, runID, data)
	}
	// Also publish to legacy hub
	hub := s.getSessionStreamHub()
	if hub != nil {
		hub.publish(sessionID, sessionStreamEvent{Name: "done", Data: data})
	}
}

// PublishExternalSessionUpdate forwards newly persisted transcript and run
// events produced outside the OpenAI API (for example WeChat/Feishu runs).
// The per-session cursors make repeated lifecycle notifications cheap while
// retaining the durable replay path for clients that connect later.
func (s *Server) PublishExternalSessionUpdate(sessionID string) {
	if s == nil || s.settings == nil || sessionID == "" {
		return
	}
	s.PublishSessionRuntime(sessionID)

	s.externalSyncMu.Lock()
	defer s.externalSyncMu.Unlock()
	if s.externalCursors == nil {
		s.externalCursors = make(map[string]sessionStreamCursor)
	}
	cursor := s.externalCursors[sessionID]
	sessionDir := s.settings.GetSessionDir()
	broker := s.getEventBroker()
	if broker == nil {
		return
	}
	for {
		items, err := session.ListSessionMessagesAfter(sessionDir, sessionID, cursor.EntrySeq, 500)
		if err != nil {
			provider.DebugLogf("sync external session %q messages after %d: %v", sessionID, cursor.EntrySeq, err)
			return
		}
		for _, item := range items {
			for _, entry := range providerMessageToSessionEntries(item.Message, item.Seq, item.EntryID) {
				evt := messageTranscriptEvent(entry)
				evt.XSessionID = sessionID
				broker.PublishTranscriptEvent(sessionID, s.activeRunIDForSession(sessionID), evt)
				if hub := s.getSessionStreamHub(); hub != nil {
					hub.publish(sessionID, sessionStreamEvent{Name: "transcript", Data: evt})
				}
			}
			if item.Seq > cursor.EntrySeq {
				cursor.EntrySeq = item.Seq
			}
		}
		if len(items) < 500 {
			break
		}
	}
	for {
		items, err := session.ListSessionRunEventsAfter(sessionDir, sessionID, cursor.RunSeq, 500)
		if err != nil {
			provider.DebugLogf("sync external session %q run events after %d: %v", sessionID, cursor.RunSeq, err)
			return
		}
		for _, item := range items {
			entry := sessionRunEventToEntry(item.Event, item.Seq)
			broker.PublishRunEvent(sessionID, item.Event.RunID, entry)
			if hub := s.getSessionStreamHub(); hub != nil {
				hub.publish(sessionID, sessionStreamEvent{Name: "run_event", Data: entry})
			}
			if item.Seq > cursor.RunSeq {
				cursor.RunSeq = item.Seq
			}
		}
		if len(items) < 500 {
			break
		}
	}
	s.externalCursors[sessionID] = cursor
}

type sessionStreamCursor struct {
	EntrySeq      int64
	RunSeq        int64
	CapabilitySeq int64
}

// StreamSession streams persisted and live transcript/event updates for one WebUI session.
func (s *Server) StreamSession(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.settings == nil || id == "" {
		writeError(w, http.StatusNotFound, "session not found", "not_found")
		return
	}
	if _, found, err := s.findSessionWorkDir(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "server_error")
		return
	} else if !found {
		writeError(w, http.StatusNotFound, "session not found", "not_found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported", "server_error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	cursor := sessionStreamCursor{
		EntrySeq:      streamIntQuery(r, "after_entry_seq", "afterEntrySeq", "entrySeq"),
		RunSeq:        streamIntQuery(r, "after_run_seq", "afterRunSeq", "runSeq"),
		CapabilitySeq: streamIntQuery(r, "after_capability_seq", "afterCapabilitySeq", "capabilitySeq"),
	}
	broker := s.getEventBroker()
	events, cancel := broker.Subscribe(id)
	defer cancel()

	if _, err := s.replaySessionStream(w, flusher, id, &cursor, true); err != nil {
		return
	}
	if !s.isSessionRunActive(id) {
		_ = writeSessionSSE(w, flusher, "done", map[string]any{"sessionId": id})
		return
	}

	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			if evt.Event == "done" {
				if _, err := s.replaySessionStream(w, flusher, id, &cursor, false); err != nil {
					provider.DebugLogf("replay session %q before done event: %v", id, err)
					return
				}
				if err := writeSessionSSE(w, flusher, "done", evt.Data); err != nil {
					provider.DebugLogf("write session %q done event: %v", id, err)
				}
				return
			}
			if err := writeSessionSSE(w, flusher, evt.Event, evt.Data); err != nil {
				provider.DebugLogf("write session %q stream event %q: %v", id, evt.Event, err)
				return
			}
		case <-poll.C:
			if _, err := s.replaySessionStream(w, flusher, id, &cursor, false); err != nil {
				provider.DebugLogf("poll replay for session %q: %v", id, err)
				return
			}
			if !s.isSessionRunActive(id) {
				if _, err := s.replaySessionStream(w, flusher, id, &cursor, false); err != nil {
					provider.DebugLogf("final replay for session %q: %v", id, err)
					return
				}
				if err := writeSessionSSE(w, flusher, "done", map[string]any{"sessionId": id}); err != nil {
					provider.DebugLogf("write final session %q done event: %v", id, err)
				}
				return
			}
		case <-heartbeat.C:
			if err := writeSessionSSE(w, flusher, "heartbeat", map[string]any{"sessionId": id}); err != nil {
				provider.DebugLogf("write session %q heartbeat: %v", id, err)
				return
			}
		}
	}
}

func (s *Server) replaySessionStream(w http.ResponseWriter, flusher http.Flusher, sessionID string, cursor *sessionStreamCursor, includeMessages bool) (bool, error) {
	if s == nil || s.settings == nil || cursor == nil {
		return false, nil
	}
	sessionDir := s.settings.GetSessionDir()
	changed := false

	if includeMessages {
		messages, err := session.ListSessionMessagesAfter(sessionDir, sessionID, cursor.EntrySeq, 200)
		if err != nil {
			_ = writeSessionSSEFailure(w, flusher, err, agentruntime.PhasePersistence)
			return changed, err
		}
		for _, item := range messages {
			for _, entry := range providerMessageToSessionEntries(item.Message, item.Seq, item.EntryID) {
				evt := messageTranscriptEvent(entry)
				evt.XSessionID = sessionID
				if err := writeSessionSSE(w, flusher, "transcript", evt); err != nil {
					return changed, err
				}
				changed = true
			}
			if item.Seq > cursor.EntrySeq {
				cursor.EntrySeq = item.Seq
			}
		}
	}

	runEvents, err := session.ListSessionRunEventsAfter(sessionDir, sessionID, cursor.RunSeq, 200)
	if err != nil {
		_ = writeSessionSSEFailure(w, flusher, err, agentruntime.PhasePersistence)
		return changed, err
	}
	for _, item := range runEvents {
		if err := writeSessionSSE(w, flusher, "run_event", sessionRunEventToEntry(item.Event, item.Seq)); err != nil {
			return changed, err
		}
		if item.Seq > cursor.RunSeq {
			cursor.RunSeq = item.Seq
		}
		changed = true
	}

	capabilityEvents, err := session.ListSessionCapabilityEventsAfter(sessionDir, sessionID, cursor.CapabilitySeq, 200)
	if err != nil {
		_ = writeSessionSSEFailure(w, flusher, err, agentruntime.PhasePersistence)
		return changed, err
	}
	for _, item := range capabilityEvents {
		if err := writeSessionSSE(w, flusher, "capability_event", sessionCapabilityEventToEntry(item.Event, item.Seq)); err != nil {
			return changed, err
		}
		if item.Seq > cursor.CapabilitySeq {
			cursor.CapabilitySeq = item.Seq
		}
		changed = true
	}

	return changed, nil
}

func (s *Server) isSessionRunActive(id string) bool {
	if s == nil || id == "" {
		return false
	}
	if s.settings != nil {
		snapshot, err := agentruntime.InspectSessionExecution(s.settings.GetSessionDir(), id)
		if err != nil {
			// A stream must not emit a false terminal event while the durable
			// execution state is temporarily unavailable.
			return true
		}
		return snapshot.ActiveRun != nil
	}
	if s.pool == nil {
		return false
	}
	sess, err := s.pool.getExact(id)
	if err != nil || sess == nil {
		return false
	}
	if execution := sess.executionRuntime(); execution != nil {
		_, active := execution.Active()
		return active
	}
	return false
}

func streamIntQuery(r *http.Request, keys ...string) int64 {
	values := r.URL.Query()
	for _, key := range keys {
		raw := values.Get(key)
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}
	return 0
}

func writeSessionSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func writeSessionSSEFailure(w http.ResponseWriter, flusher http.Flusher, err error, phase agentruntime.RunPhase) error {
	info := agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{
		Phase: phase, Type: "server_error", MessageKey: "run.error.persistence",
	})
	info.RetryMode = agentruntime.RetryReconcile
	info.Retryable = true
	return writeSessionSSE(w, flusher, "error", map[string]any{"errorInfo": info, "error": agentruntime.DisplayErrorMessage(info)})
}
