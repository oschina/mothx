package openaiapi

import (
	"net/http"
	"sync"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
	"golang.org/x/net/websocket"
)

type runWebSocketMessage struct {
	Type          string                     `json:"type"`
	ClientID      string                     `json:"clientId,omitempty"`
	Subscriptions []runWebSocketSubscription `json:"subscriptions,omitempty"`
	SessionIDs    []string                   `json:"sessionIds,omitempty"`
	// Replay fields
	SessionID string              `json:"sessionId,omitempty"`
	Cursor    sessionStreamCursor `json:"cursor,omitempty"`
}

type runWebSocketSubscription struct {
	SessionID string              `json:"sessionId"`
	Cursor    sessionStreamCursor `json:"cursor"`
}

type runWebSocketEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	Stream    string `json:"stream,omitempty"`
	Event     string `json:"event,omitempty"`
	Seq       int64  `json:"seq,omitempty"`
	Data      any    `json:"data,omitempty"`
}

func (s *Server) RunWebSocketHandler() websocket.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		s.runWebSocketLoop(ws)
	})
}

// HandleRunWebSocket serves the durable session/run event protocol used by WebUI.
// Disconnecting this socket only removes subscriptions; it never cancels a run.
func (s *Server) HandleRunWebSocket(w http.ResponseWriter, r *http.Request) {
	s.RunWebSocketHandler().ServeHTTP(w, r)
}

func (s *Server) runWebSocketLoop(ws *websocket.Conn) {
	if s == nil || ws == nil {
		return
	}
	socketDone := make(chan struct{})
	defer close(socketDone)
	var writeMu sync.Mutex
	write := func(value any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return websocket.JSON.Send(ws, value)
	}
	type subscription struct {
		sessionID string
		cancel    func()
	}
	subs := make(map[string]subscription)
	defer func() {
		for _, sub := range subs {
			sub.cancel()
		}
	}()

	for {
		var msg runWebSocketMessage
		if err := websocket.JSON.Receive(ws, &msg); err != nil {
			return
		}
		switch msg.Type {
		case "hello":
			if err := write(map[string]any{"type": "ready", "protocol": 1, "clientId": msg.ClientID}); err != nil {
				return
			}
		case "subscribe":
			for _, item := range msg.Subscriptions {
				if item.SessionID == "" {
					continue
				}
				// Validate session access before subscribing. Sessions created
				// client-side (e.g. the Web UI) do not exist yet — subscribe
				// without replay so events flow once the session is created.
				_, found, err := s.findSessionWorkDir(item.SessionID)
				if err != nil {
					_ = write(runWebSocketEvent{Type: "error", SessionID: item.SessionID, Data: "session not found or access denied"})
					continue
				}
				if old, ok := subs[item.SessionID]; ok {
					old.cancel()
				}
				// Subscribe first, then capture the boundary immediately so events
				// published during replay are retained by the forwarder.
				events, resync, cancel := s.getEventBroker().SubscribeWithResync(item.SessionID)
				subs[item.SessionID] = subscription{sessionID: item.SessionID, cancel: cancel}
				// A broker overflow means this connection missed live state. Close the
				// socket so the client reconnects and replays durable SQLite cursors.
				// Normal unsubscribe never closes resync.
				go func(resync <-chan struct{}) {
					select {
					case <-resync:
						_ = ws.Close()
					case <-socketDone:
					}
				}(resync)
				// Capture the broker boundary before replay. Events published after
				// this point must remain in the live stream even if they arrive while
				// the SQLite replay is still being written to the socket.
				replayBoundary := s.getEventBroker().CurrentSeq(item.SessionID)
				cursor := item.Cursor
				if found {
					if err := s.writeRunWebSocketReplay(write, item.SessionID, &cursor); err != nil {
						cancel()
						delete(subs, item.SessionID)
						_ = write(runWebSocketEvent{Type: "error", SessionID: item.SessionID, Data: websocketReplayError(err)})
						continue
					}
					// Approval and question requests are held in the live session
					// runtime, while the replay above only contains durable transcript
					// and run events. Send a post-replay snapshot so a request that was
					// emitted before this subscription (especially a newly-created WebUI
					// session) cannot leave the client waiting forever with no prompt.
					s.writeRunWebSocketRuntimeSnapshot(write, item.SessionID)
				}
				go s.forwardRunWebSocketEvents(write, item.SessionID, events, &cursor, replayBoundary)
				_ = write(runWebSocketEvent{Type: "subscribed", SessionID: item.SessionID})
			}
		case "unsubscribe":
			for _, id := range msg.SessionIDs {
				if sub, ok := subs[id]; ok {
					sub.cancel()
					delete(subs, id)
				}
			}
		case "replay":
			if msg.SessionID != "" {
				cursor := msg.Cursor
				if err := s.writeRunWebSocketReplay(write, msg.SessionID, &cursor); err != nil {
					_ = write(runWebSocketEvent{Type: "error", SessionID: msg.SessionID, Data: websocketReplayError(err)})
					continue
				}
				s.writeRunWebSocketRuntimeSnapshot(write, msg.SessionID)
			}
		default:
			_ = write(map[string]any{"type": "error", "error": "unknown websocket message type"})
		}
	}
}

// writeRunWebSocketRuntimeSnapshot projects the current in-memory runtime
// after durable replay. Decision requests are intentionally runtime-owned and
// therefore are not reconstructed by the transcript/run event cursor alone.
// Snapshot failures are best-effort: the live broker remains authoritative.
func (s *Server) writeRunWebSocketRuntimeSnapshot(write func(any) error, sessionID string) {
	if s == nil || write == nil || sessionID == "" {
		return
	}
	snapshot, err := s.GetSessionRuntime(sessionID)
	if err != nil || snapshot == nil {
		return
	}
	runID := ""
	if snapshot.ActiveRun != nil {
		runID = snapshot.ActiveRun.RunID
	}
	_ = write(runWebSocketEvent{
		Type: "session_event", SessionID: sessionID, RunID: runID,
		Stream: "runtime", Event: "runtime_event", Data: snapshot,
	})
}

func websocketReplayError(err error) agentruntime.ErrorInfo {
	info := agentruntime.ClassifyError(err, agentruntime.ErrorClassificationOptions{
		Phase: agentruntime.PhasePersistence, Type: "server_error", MessageKey: "run.error.persistence",
	})
	info.RetryMode = agentruntime.RetryReconcile
	info.Retryable = true
	return info
}

func (s *Server) writeRunWebSocketReplay(write func(any) error, sessionID string, cursor *sessionStreamCursor) error {
	if _, found, err := s.findSessionWorkDir(sessionID); err != nil {
		return err
	} else if !found {
		return ErrSessionNotFound
	}
	sessionDir := s.settings.GetSessionDir()
	messages, err := session.ListSessionMessagesAfter(sessionDir, sessionID, cursor.EntrySeq, 200)
	if err != nil {
		return err
	}
	for _, item := range messages {
		for _, entry := range providerMessageToSessionEntries(item.Message, item.Seq, item.EntryID) {
			if err := write(runWebSocketEvent{Type: "session_event", SessionID: sessionID, Stream: "transcript", Event: "transcript", Seq: item.Seq, Data: messageTranscriptEvent(entry)}); err != nil {
				return err
			}
		}
		if item.Seq > cursor.EntrySeq {
			cursor.EntrySeq = item.Seq
		}
	}
	runEvents, err := session.ListSessionRunEventsAfter(sessionDir, sessionID, cursor.RunSeq, 200)
	if err != nil {
		return err
	}
	for _, item := range runEvents {
		if err := write(runWebSocketEvent{Type: "session_event", SessionID: sessionID, RunID: item.Event.RunID, Stream: "run", Event: item.Event.EventType, Seq: item.Seq, Data: sessionRunEventToEntry(item.Event, item.Seq)}); err != nil {
			return err
		}
		cursor.RunSeq = item.Seq
	}
	capEvents, err := session.ListSessionCapabilityEventsAfter(sessionDir, sessionID, cursor.CapabilitySeq, 200)
	if err != nil {
		return err
	}
	for _, item := range capEvents {
		if err := write(runWebSocketEvent{Type: "session_event", SessionID: sessionID, RunID: item.Event.RunID, Stream: "capability", Event: item.Event.EventType, Seq: item.Seq, Data: sessionCapabilityEventToEntry(item.Event, item.Seq)}); err != nil {
			return err
		}
		cursor.CapabilitySeq = item.Seq
	}
	return nil
}

func (s *Server) forwardRunWebSocketEvents(write func(any) error, sessionID string, events <-chan BrokerEvent, cursor *sessionStreamCursor, replayBoundary int64) {
	for ev := range events {
		// Skip events that were already covered by the SQLite replay.
		// replayBoundary is the EventBroker seq captured before subscribing.
		// Events with seq <= replayBoundary were persisted before the subscription
		// and should have been sent during the replay phase.
		if replayBoundary > 0 && ev.Seq <= replayBoundary {
			continue
		}
		if err := write(runWebSocketEvent{
			Type:      "session_event",
			SessionID: ev.SessionID,
			RunID:     ev.RunID,
			Stream:    ev.Stream,
			Event:     ev.Event,
			Seq:       ev.Seq,
			Data:      ev.Data,
		}); err != nil {
			return
		}
	}
}
