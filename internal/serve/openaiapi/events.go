package openaiapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	ctxpkg "github.com/oschina/mothx/internal/context"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

type capabilitySnapshot struct {
	Mode         string
	DelegateMode bool
	MultiAgent   bool
	Workflows    bool
	WebSearch    bool
	Browser      bool
	A2AMaster    bool
}

func newRunID() string {
	return "run_" + session.GenerateID()
}

func newExecutionIntentID() string {
	return "intent_" + session.GenerateID()
}

// Compatibility aliases keep the OpenAI API error surface stable while the
// reconciliation implementation is owned by the shared Runtime.
var ErrIdempotencyKeyConflict = agentruntime.ErrIdempotencyKeyConflict
var ErrIdempotencyRunMissing = agentruntime.ErrIdempotencyRunMissing

// requestFingerprint returns a stable, non-sensitive digest for the request
// fields selected by the caller. Only the digest is persisted in run events.
func requestFingerprint(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest[:])
}

// idempotencyKeyFingerprint keeps the client-generated key out of durable
// events. The key is only used as an equality token during an unknown-submit
// reconciliation, so a stable digest is sufficient for lookup.
func idempotencyKeyFingerprint(key string) string {
	return agentruntime.IdempotencyKeyFingerprint(key)
}

func retryIdempotencyScope(intentID, retryOf string) string {
	if intentID == "" || retryOf == "" {
		return "retry"
	}
	return "retry:" + intentID + ":" + retryOf
}

func findIdempotentRun(sessionDir, sessionID, key, fingerprint, scope string) (*session.SessionRun, error) {
	return agentruntime.FindIdempotentRun(context.Background(), sessionDir, sessionID, key, fingerprint, scope)
}

func capabilitySnapshotFromSession(sess *APISession) capabilitySnapshot {
	if sess == nil {
		return capabilitySnapshot{}
	}
	return capabilitySnapshot{
		Mode:         sess.Mode,
		DelegateMode: sess.DelegateMode,
		MultiAgent:   sess.MultiAgent,
		Workflows:    sess.Workflows,
		WebSearch:    sess.WebSearch,
		Browser:      sess.Browser,
		A2AMaster:    sess.A2AMaster,
	}
}

func (c capabilitySnapshot) values() map[string]string {
	return map[string]string{
		"mode":         c.Mode,
		"delegateMode": strconv.FormatBool(c.DelegateMode),
		"multiAgent":   strconv.FormatBool(c.MultiAgent),
		"workflows":    strconv.FormatBool(c.Workflows),
		"webSearch":    strconv.FormatBool(c.WebSearch),
		"browser":      strconv.FormatBool(c.Browser),
		"a2aMaster":    strconv.FormatBool(c.A2AMaster),
	}
}

func (s *Server) persistSessionCapabilitiesWithEvents(sess *APISession, before capabilitySnapshot, source, actor, runID string, data map[string]any) error {
	if err := s.persistSessionCapabilities(sess); err != nil {
		return err
	}
	return s.recordSessionCapabilityChanges(sess, before, source, actor, runID, data)
}

func (s *Server) recordSessionCapabilityChanges(sess *APISession, before capabilitySnapshot, source, actor, runID string, data map[string]any) error {
	if s == nil || s.settings == nil || sess == nil || sess.ID == "" {
		return nil
	}
	after := capabilitySnapshotFromSession(sess)
	beforeValues := before.values()
	afterValues := after.values()
	eventData := rawEventData(data)
	for _, capability := range []string{"mode", "delegateMode", "multiAgent", "workflows", "webSearch", "browser", "a2aMaster"} {
		oldValue := beforeValues[capability]
		newValue := afterValues[capability]
		if oldValue == newValue {
			continue
		}
		ev := session.SessionCapabilityEvent{
			SessionID:  sess.ID,
			RunID:      runID,
			EventType:  "changed",
			Source:     source,
			Actor:      actor,
			Capability: capability,
			OldValue:   oldValue,
			NewValue:   newValue,
			Timestamp:  time.Now(),
			Data:       eventData,
		}
		id, err := session.SaveSessionCapabilityEvent(s.settings.GetSessionDir(), ev)
		if err != nil {
			return fmt.Errorf("save capability event: %w", err)
		}
		ev.ID = id
		s.publishSessionStreamEvent(sess.ID, "capability_event", sessionCapabilityEventToEntry(ev, 0))
		s.getEventBroker().PublishCapabilityEvent(sess.ID, runID, sessionCapabilityEventToEntry(ev, 0))
	}
	return nil
}

func (s *Server) recordSessionRunEvent(sess *APISession, runID, eventType, status, source, modelID, mode string, data map[string]any) error {
	if s == nil || s.settings == nil || sess == nil || sess.ID == "" || runID == "" {
		return nil
	}
	execution := sess.ensureExecution()
	// Once a canonical row exists, its source/mode/model are authoritative for
	// every later projection. This prevents provider-specific background code
	// from accidentally reintroducing its adapter fallback in durable events.
	var persistedRun *session.SessionRun
	if s.settings != nil {
		if run, lookupErr := agentruntime.GetDurableRun(context.Background(), s.settings.GetSessionDir(), runID); lookupErr == nil && run != nil {
			persistedRun = run
			if strings.TrimSpace(run.Source) != "" {
				source = run.Source
			}
			if strings.TrimSpace(run.Mode) != "" {
				mode = run.Mode
			}
			if strings.TrimSpace(modelID) == "" {
				modelID = run.Model
			}
		}
	}
	var err error
	source, mode, err = s.canonicalRunIdentity(sess, source, mode)
	if err != nil {
		return err
	}
	data = s.safeRunEventData(persistedRun, eventType, status, data)
	if info, ok := runEventErrorInfo(data); ok && execution != nil {
		recorded, recordErr := execution.RecordErrorInfo(info)
		if recordErr != nil {
			return fmt.Errorf("persist run error info: %w", recordErr)
		}
		data = cloneRunEventData(data)
		data["error"] = recorded
		data["errorInfo"] = recorded
		data["errorMessage"] = recorded.Message
	}
	// A durable Runtime has one terminalization owner. Legacy background
	// coordinators may still report a terminal-shaped detail event before their
	// defer calls FinishDurable; retain its ErrorInfo fact but do not append a
	// second terminal event to the canonical stream.
	if persistedRun != nil && sess.isDurableRun(runID) && isTerminalRunStatus(status) {
		return nil
	}
	ev := session.SessionRunEvent{
		SessionID: sess.ID,
		RunID:     runID,
		EventType: eventType,
		Source:    source,
		Status:    status,
		Model:     modelID,
		Mode:      mode,
		Timestamp: time.Now(),
		Data:      rawEventData(data),
	}
	sink := agentruntime.SessionRunEventSink{SessionDir: s.settings.GetSessionDir()}
	execution.SetEventSink(sink)
	id, err := execution.RecordEvent(agentruntime.RunEvent{
		SessionID: ev.SessionID, RunID: ev.RunID, EventType: ev.EventType,
		Source: ev.Source, Status: ev.Status, Model: ev.Model, Mode: ev.Mode,
		Timestamp: ev.Timestamp, Data: ev.Data,
	})
	if err != nil {
		return fmt.Errorf("save run event: %w", err)
	}
	ev.ID = id
	s.publishSessionStreamEvent(sess.ID, "run_event", sessionRunEventToEntry(ev, 0))
	s.getEventBroker().PublishRunEvent(sess.ID, runID, sessionRunEventToEntry(ev, 0))
	// Keep the live projection sink installed for the next Runtime-owned event;
	// this method uses a persistence-only sink only to avoid double publication
	// for the event it just emitted.
	execution.SetEventSink(s.runtimeRunEventSink(sess))
	return nil
}

func isTerminalRunStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "incomplete", "failed", "cancelled", "canceled", "timed_out", "expired":
		return true
	default:
		return false
	}
}

func (s *Server) safeRunEventData(run *session.SessionRun, eventType, status string, data map[string]any) map[string]any {
	if len(data) == 0 {
		return data
	}
	value, hasError := data["error"]
	if !hasError {
		return data
	}
	if info, ok := value.(agentruntime.ErrorInfo); ok {
		copy := cloneRunEventData(data)
		copy["errorInfo"] = info
		copy["errorMessage"] = agentruntime.DisplayErrorMessage(info)
		return copy
	}
	if info, ok := value.(*agentruntime.ErrorInfo); ok && info != nil {
		copy := cloneRunEventData(data)
		copy["error"] = *info
		copy["errorInfo"] = *info
		copy["errorMessage"] = agentruntime.DisplayErrorMessage(*info)
		return copy
	}
	message, ok := value.(string)
	if !ok || strings.TrimSpace(message) == "" {
		return data
	}
	phase := agentruntime.PhaseModel
	if strings.Contains(strings.ToLower(eventType), "remote") || strings.Contains(strings.ToLower(eventType), "transport") {
		phase = agentruntime.PhaseTransport
	}
	opts := agentruntime.ErrorClassificationOptions{Phase: phase, SideEffectState: agentruntime.SideEffectUnknown}
	if run != nil {
		opts.RunID = run.ID
		opts.IntentID = run.IntentID
		opts.Attempt = run.Attempt
	}
	info := agentruntime.ClassifyError(errors.New(message), opts)
	copy := cloneRunEventData(data)
	copy["error"] = info
	copy["errorInfo"] = info
	copy["errorMessage"] = agentruntime.DisplayErrorMessage(info)
	if run != nil && s.settings != nil {
		encoded, err := json.Marshal(info)
		if err == nil {
			_ = session.UpdateSessionRunErrorInfo(s.settings.GetSessionDir(), run.ID, encoded)
		}
	}
	return copy
}

func cloneRunEventData(data map[string]any) map[string]any {
	copy := make(map[string]any, len(data)+2)
	for key, value := range data {
		copy[key] = value
	}
	return copy
}

func runEventErrorInfo(data map[string]any) (agentruntime.ErrorInfo, bool) {
	if len(data) == 0 {
		return agentruntime.ErrorInfo{}, false
	}
	for _, key := range []string{"errorInfo", "error"} {
		value, ok := data[key]
		if !ok {
			continue
		}
		switch info := value.(type) {
		case agentruntime.ErrorInfo:
			return info, info.Code != ""
		case *agentruntime.ErrorInfo:
			if info != nil {
				return *info, info.Code != ""
			}
		default:
			encoded, err := json.Marshal(value)
			if err != nil {
				continue
			}
			var decoded agentruntime.ErrorInfo
			if json.Unmarshal(encoded, &decoded) == nil && decoded.Code != "" {
				return decoded, true
			}
		}
	}
	return agentruntime.ErrorInfo{}, false
}

func (s *Server) canonicalRunIdentity(sess *APISession, fallbackSource, requestedMode string) (string, string, error) {
	resolution, mode, err := s.resolveSessionPolicy(sess, requestedMode)
	if err != nil {
		return "", "", err
	}
	if policy := agentruntime.PolicyForSource(resolution.Source, ""); policy.HasForcedMode() {
		return string(resolution.Source), mode, nil
	}
	return fallbackSource, mode, nil
}

func rawEventData(data map[string]any) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	return raw
}

// safeHostedItemRunData keeps durable hosted lifecycle events useful for
// reconnects without persisting arbitrary provider metadata. Canonical output
// remains in the provider archive, where its dedicated redaction/size policy
// applies.
func safeHostedItemRunData(item *provider.HostedItem) map[string]any {
	if item == nil {
		return nil
	}
	data := map[string]any{
		"id": boundedHostedString(item.ID), "type": boundedHostedString(item.Type), "status": boundedHostedString(item.Status), "outputIndex": item.OutputIndex,
	}
	allowed := map[string]struct{}{
		"annotationType": {}, "title": {}, "start_index": {}, "end_index": {},
		"score": {}, "responseItemId": {}, "responseItemType": {}, "status": {}, "tool": {},
	}
	metadata := make(map[string]any)
	for key, value := range item.Metadata {
		if _, ok := allowed[key]; !ok {
			continue
		}
		switch value.(type) {
		case string:
			metadata[key] = boundedHostedString(value.(string))
		case bool, float64, float32, int, int64, uint, uint64, nil:
			metadata[key] = value
		}
	}
	if len(metadata) > 0 {
		data["metadata"] = metadata
	}
	return data
}

const maxHostedRunString = 512

func boundedHostedString(value string) string {
	if len(value) <= maxHostedRunString {
		return value
	}
	return value[:maxHostedRunString] + "..."
}

func runEventTypeForStatus(status string) string {
	switch status {
	case "failed":
		return "failed"
	case "canceled":
		return "canceled"
	default:
		return "finished"
	}
}

// IsSuccessfulRunStatus reports only a fully completed Responses run.
func IsSuccessfulRunStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "completed"
}

// IsIncompleteRunStatus reports a run that produced a partial result without
// completing its objective.
func IsIncompleteRunStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "incomplete"
}

func usageEventData(usage CompletionUsage, errMsg string) map[string]any {
	data := map[string]any{
		"usage": map[string]any{
			"prompt_tokens":      usage.PromptTokens,
			"completion_tokens":  usage.CompletionTokens,
			"total_tokens":       usage.TotalTokens,
			"cache_read_tokens":  usage.CacheReadTokens,
			"cache_write_tokens": usage.CacheWriteTokens,
		},
	}
	if errMsg != "" {
		data["error"] = errMsg
	}
	return data
}

// withContextUsageEventData adds the final request-context footprint to a
// durable run event. Unlike cumulative CompletionUsage, this reflects the
// currently occupied portion of the selected model's context window.
func withContextUsageEventData(data map[string]any, usage *ctxpkg.ContextUsage) map[string]any {
	if usage == nil || usage.ContextWindow <= 0 {
		return data
	}
	copy := *usage
	data["contextUsage"] = copy
	return data
}
