package openaiapi

import (
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/session"
)

// runtimeRunEventSink persists canonical events and mirrors them to WebUI
// projections. Persistence remains Runtime-owned while publication remains an
// adapter concern.
func (s *Server) runtimeRunEventSink(sess *APISession) agentruntime.RunEventSink {
	return runtimeRunEventSink{server: s, session: sess}
}

type runtimeRunEventSink struct {
	server  *Server
	session *APISession
}

func (sink runtimeRunEventSink) Record(ev agentruntime.RunEvent) (string, error) {
	s := sink.server
	sess := sink.session
	if s == nil || sess == nil {
		return "", nil
	}
	id, err := (agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()}).Record(ev)
	if err != nil {
		return "", err
	}
	entry := sessionRunEventToEntry(session.SessionRunEvent{
		ID: id, SessionID: ev.SessionID, RunID: ev.RunID, EventType: ev.EventType,
		Source: ev.Source, Status: ev.Status, Model: ev.Model, Mode: ev.Mode,
		Timestamp: ev.Timestamp, Data: ev.Data,
	}, 0)
	s.publishSessionStreamEvent(sess.ID, "run_event", entry)
	s.getEventBroker().PublishRunEvent(sess.ID, ev.RunID, entry)
	return id, nil
}

// Project fans out an event that was already committed as part of an atomic
// intent admission. It deliberately does not touch SQLite a second time.
func (sink runtimeRunEventSink) Project(ev agentruntime.RunEvent, id string) error {
	s := sink.server
	sess := sink.session
	if s == nil || sess == nil {
		return nil
	}
	entry := sessionRunEventToEntry(session.SessionRunEvent{
		ID: id, SessionID: ev.SessionID, RunID: ev.RunID, EventType: ev.EventType,
		Source: ev.Source, Status: ev.Status, Model: ev.Model, Mode: ev.Mode,
		Timestamp: ev.Timestamp, Data: ev.Data,
	}, 0)
	s.publishSessionStreamEvent(sess.ID, "run_event", entry)
	s.getEventBroker().PublishRunEvent(sess.ID, ev.RunID, entry)
	return nil
}
