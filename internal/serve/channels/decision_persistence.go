package channels

import (
	"log"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
)

func (d *Dispatcher) persistChannelDecision(sess *ChannelSession, id string, kind agentruntime.DecisionKind, status, value string, payload map[string]any) error {
	if d == nil || sess == nil || id == "" || sess.ID == "" || sess.runID == "" {
		return nil
	}
	_, err := agentruntime.RecordDecisionEvent(
		agentruntime.SessionRunEventSink{SessionDir: d.sessionDir},
		agentruntime.DecisionTransition{
			Request: agentruntime.DecisionRequest{ID: id, SessionID: sess.ID, RunID: sess.runID, Kind: kind},
			Status:  status, Value: value, Payload: payload,
			Source: channelRunSource(sess), Mode: sess.Mode,
		})
	return err
}

func (d *Dispatcher) persistChannelDecisionRequest(sess *ChannelSession, id string, kind agentruntime.DecisionKind, payload map[string]any) {
	d.persistChannelDecisionRequestWithDeadline(sess, id, kind, payload, time.Time{})
}

func (d *Dispatcher) persistChannelDecisionRequestWithDeadline(sess *ChannelSession, id string, kind agentruntime.DecisionKind, payload map[string]any, expiresAt time.Time) {
	if d == nil || sess == nil || id == "" || sess.ID == "" || sess.runID == "" {
		return
	}
	if _, err := agentruntime.RecordDecisionEvent(
		agentruntime.SessionRunEventSink{SessionDir: d.sessionDir},
		agentruntime.DecisionTransition{
			Request: agentruntime.DecisionRequest{ID: id, SessionID: sess.ID, RunID: sess.runID, Kind: kind},
			Status:  agentruntime.DecisionStatusPending, Payload: payload, ExpiresAt: expiresAt,
			Source: channelRunSource(sess), Mode: sess.Mode,
		}); err != nil {
		log.Printf("[channels] save decision request %s: %v", id, err)
	}
}
