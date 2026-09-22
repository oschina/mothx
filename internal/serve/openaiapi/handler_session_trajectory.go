package openaiapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// SessionTrajectoryResponse is the read-only, front-end-neutral trajectory
// window assembled from the canonical session stores.
type SessionTrajectoryResponse struct {
	SessionID string           `json:"sessionId"`
	Records   []map[string]any `json:"records"`
	HighWater map[string]int64 `json:"highWater"`
	HasMore   bool             `json:"hasMore"`
}

type trajectoryCursor struct {
	EntrySeq      int64 `json:"entrySeq"`
	RunSeq        int64 `json:"runSeq"`
	CapabilitySeq int64 `json:"capabilitySeq"`
	DecisionSeq   int64 `json:"decisionSeq"`
}

type trajectoryRecord struct {
	value  map[string]any
	source string
	seq    int64
	when   time.Time
}

const (
	trajectoryLimitDefault = 200
	trajectoryLimitMax     = 500
)

var ErrInvalidTrajectoryCursor = errors.New("invalid trajectory cursor")

// GetSessionTrajectory assembles a deterministic trajectory projection from
// the existing transcript, run-event, and capability-event stores.
func (s *Server) GetSessionTrajectory(id, before string, limit int) (*SessionTrajectoryResponse, error) {
	if s == nil || id == "" {
		return nil, ErrSessionNotFound
	}
	if _, found, err := s.findSessionWorkDir(id); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrSessionNotFound
	}
	if limit <= 0 {
		limit = trajectoryLimitDefault
	}
	if limit > trajectoryLimitMax {
		limit = trajectoryLimitMax
	}
	cursor, err := decodeTrajectoryCursor(before)
	if err != nil {
		return nil, err
	}
	records, highWater, err := s.trajectoryRecords(id)
	if err != nil {
		return nil, err
	}
	filtered := filterTrajectoryRecords(records, cursor)
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[len(filtered)-limit:]
	}
	out := make([]map[string]any, 0, len(filtered))
	for _, item := range filtered {
		out = append(out, item.value)
	}
	return &SessionTrajectoryResponse{
		SessionID: id,
		Records:   out,
		HighWater: highWater,
		HasMore:   hasMore,
	}, nil
}

// HandleSessionExport serves the browser-facing session.log exporter. GET is
// streamed directly to the response; HEAD validates the same snapshot without
// buffering or returning a body.
func (s *Server) HandleSessionExport(w http.ResponseWriter, r *http.Request, id string) {
	if s == nil || id == "" {
		writeTrajectoryError(w, ErrSessionNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "log"
	}
	if format != "log" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported export format"})
		return
	}
	includeDescendants, err := parseBoolQuery(r.URL.Query().Get("include_descendants"), true)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid include_descendants"})
		return
	}
	sessions, err := s.exportSessionIDs(id, includeDescendants)
	if err != nil {
		writeTrajectoryError(w, err)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeSessionFilename(id)+`.log"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Mothx-Session-Count", strconv.Itoa(len(sessions)))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeSessionFilename(id)+`.log"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(map[string]any{
		"schemaVersion":      1,
		"type":               "manifest",
		"sessionId":          id,
		"generatedAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"includeDescendants": includeDescendants,
		"sessionCount":       len(sessions),
	}); err != nil {
		return
	}
	for _, sessionID := range sessions {
		select {
		case <-r.Context().Done():
			return
		default:
		}
		records, highWater, err := s.trajectoryRecords(sessionID)
		if err != nil {
			log.Printf("[session-export] projection failed session=%s: %v", sessionID, err)
			return
		}
		activeRunIDs := make([]string, 0)
		for _, item := range records {
			if snapshot, ok := item.value["snapshot"].(bool); ok && snapshot {
				if runID, ok := item.value["runId"].(string); ok && runID != "" {
					activeRunIDs = append(activeRunIDs, runID)
				}
			}
		}
		if err := encoder.Encode(map[string]any{
			"schemaVersion": 1,
			"type":          "session_snapshot",
			"sessionId":     sessionID,
			"highWater":     highWater,
			"activeRunIds":  activeRunIDs,
		}); err != nil {
			return
		}
		for _, item := range records {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			line := make(map[string]any, len(item.value)+2)
			line["schemaVersion"] = 1
			line["type"] = "record"
			for key, value := range item.value {
				line[key] = value
			}
			if err := encoder.Encode(line); err != nil {
				return
			}
		}
	}
}

func (s *Server) trajectoryRecords(id string) ([]trajectoryRecord, map[string]int64, error) {
	parentSessionID := ""
	if manager, err := session.OpenByIDExact(s.SessionDir(), id); err == nil && manager.GetHeader() != nil {
		parentSessionID = manager.GetHeader().ParentSession
	}
	messages, err := s.GetSessionMessages(id)
	if err != nil {
		return nil, nil, err
	}
	runEvents, err := s.GetSessionRunEvents(id)
	if err != nil {
		return nil, nil, err
	}
	capabilityEvents, err := s.GetSessionCapabilityEvents(id)
	if err != nil {
		return nil, nil, err
	}
	records := make([]trajectoryRecord, 0, len(messages)+len(runEvents)+len(capabilityEvents))
	highWater := map[string]int64{"entrySeq": 0, "runSeq": 0, "capabilitySeq": 0, "decisionSeq": 0}
	for _, run := range sessionRunsForTrajectory(s.SessionDir(), id) {
		status := normalizeTrajectoryStatus(run.Status, "")
		startedAt := formatTrajectoryTime(run.StartedAt)
		completedAt := ""
		if run.FinishedAt != nil {
			completedAt = formatTrajectoryTime(*run.FinishedAt)
		}
		value := map[string]any{
			"id":        "run:" + id + ":snapshot:" + run.ID,
			"sessionId": id, "parentSessionId": parentSessionID, "runId": run.ID,
			"source": "run", "kind": "run", "status": status, "attempt": run.Attempt,
			"summary": "Run " + run.Status, "preview": run.Error, "timestamp": startedAt,
			"startedAt": startedAt, "completedAt": completedAt, "snapshot": isActiveTrajectoryRun(run.Status),
			"model": run.Model, "mode": run.Mode, "error": run.Error,
			"usage": redactTrajectoryValue(run.Usage), "output": redactTrajectoryValue(run.Progress),
			"sourceEvent": redactTrajectoryValue(run),
		}
		records = append(records, trajectoryRecord{value: compactTrajectoryValue(value), source: "run", when: run.StartedAt})
	}
	for _, message := range messages {
		kind, status := trajectoryMessageKindStatus(message)
		idValue := trajectoryMessageID(id, message)
		value := map[string]any{
			"id": idValue, "sessionId": id, "parentSessionId": parentSessionID, "seq": message.Seq,
			"source": "transcript", "kind": kind, "status": status,
			"role": message.Role, "summary": trajectoryMessageSummary(message),
			"preview": trajectoryMessagePreview(message), "toolCallId": message.ToolCallID,
			"toolName": message.ToolName, "hasDetail": message.HasDetail,
			"error":       message.InvalidArgs,
			"sourceEvent": redactTrajectoryValue(message),
		}
		if message.Content != "" {
			value["content"] = message.Content
		}
		if len(message.Contents) > 0 {
			value["contents"] = redactTrajectoryValue(message.Contents)
		}
		if len(message.Attachments) > 0 {
			value["attachments"] = redactTrajectoryValue(message.Attachments)
		}
		if len(message.Arguments) > 0 && json.Valid(message.Arguments) {
			var input any
			if json.Unmarshal(message.Arguments, &input) == nil {
				value["input"] = redactTrajectoryValue(input)
			}
		}
		records = append(records, trajectoryRecord{value: compactTrajectoryValue(value), source: "transcript", seq: message.Seq})
		if message.Seq > highWater["entrySeq"] {
			highWater["entrySeq"] = message.Seq
		}
	}
	for _, event := range runEvents {
		kind := "run"
		recordSource := "run"
		status := normalizeTrajectoryStatus(event.Status, event.EventType)
		if strings.Contains(strings.ToLower(event.EventType), "approval") || strings.Contains(strings.ToLower(event.EventType), "question") {
			kind = "decision"
			recordSource = "decision"
			if event.Seq > highWater["decisionSeq"] {
				highWater["decisionSeq"] = event.Seq
			}
		}
		value := map[string]any{
			"id": trajectoryEventID(recordSource, id, event.ID), "sessionId": event.SessionID, "parentSessionId": parentSessionID, "runId": event.RunID,
			"seq": event.Seq, "source": recordSource, "kind": kind, "status": status,
			"eventType": event.EventType, "summary": event.EventType, "preview": event.Status,
			"model": event.Model, "mode": event.Mode, "timestamp": event.Timestamp,
			"output": redactTrajectoryValue(event.Data), "sourceEvent": redactTrajectoryValue(event),
		}
		records = append(records, trajectoryRecord{value: compactTrajectoryValue(value), source: recordSource, seq: event.Seq, when: parseTrajectoryTime(event.Timestamp)})
		if event.Seq > highWater["runSeq"] {
			highWater["runSeq"] = event.Seq
		}
	}
	for _, event := range capabilityEvents {
		value := map[string]any{
			"id": "capability:" + id + ":" + event.ID, "sessionId": event.SessionID, "parentSessionId": parentSessionID, "runId": event.RunID,
			"seq": event.Seq, "source": "capability", "kind": "capability", "status": "completed",
			"eventType": event.EventType, "summary": event.Capability, "preview": event.OldValue + " -> " + event.NewValue,
			"capability": event.Capability, "oldValue": event.OldValue, "newValue": event.NewValue,
			"timestamp": event.Timestamp, "output": redactTrajectoryValue(event.Data), "sourceEvent": redactTrajectoryValue(event),
		}
		records = append(records, trajectoryRecord{value: compactTrajectoryValue(value), source: "capability", seq: event.Seq, when: parseTrajectoryTime(event.Timestamp)})
		if event.Seq > highWater["capabilitySeq"] {
			highWater["capabilitySeq"] = event.Seq
		}
	}
	records = mergeTrajectoryRecords(records)
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].when.IsZero() && !records[j].when.IsZero() && !records[i].when.Equal(records[j].when) {
			return records[i].when.Before(records[j].when)
		}
		if sourceOrder(records[i].source) != sourceOrder(records[j].source) {
			return sourceOrder(records[i].source) < sourceOrder(records[j].source)
		}
		if records[i].seq != records[j].seq {
			return records[i].seq < records[j].seq
		}
		return fmt.Sprint(records[i].value["id"]) < fmt.Sprint(records[j].value["id"])
	})
	return records, highWater, nil
}

func mergeTrajectoryRecords(records []trajectoryRecord) []trajectoryRecord {
	merged := make([]trajectoryRecord, 0, len(records))
	indexes := make(map[string]int, len(records))
	for _, item := range records {
		id, _ := item.value["id"].(string)
		if id == "" {
			merged = append(merged, item)
			continue
		}
		index, exists := indexes[id]
		if !exists {
			indexes[id] = len(merged)
			merged = append(merged, item)
			continue
		}
		current := &merged[index]
		for key, value := range item.value {
			current.value[key] = value
		}
		if current.seq == 0 || (item.seq > 0 && item.seq < current.seq) {
			current.seq = item.seq
			current.value["seq"] = item.seq
		}
		if current.when.IsZero() || (!item.when.IsZero() && item.when.Before(current.when)) {
			current.when = item.when
		}
	}
	return merged
}

func sessionRunsForTrajectory(sessionDir, sessionID string) []session.SessionRun {
	runs, err := session.ListSessionRuns(sessionDir, sessionID, 500)
	if err != nil {
		return nil
	}
	return runs
}

func formatTrajectoryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func isActiveTrajectoryRun(status string) bool {
	return session.IsNonTerminalSessionRunStatus(strings.ToLower(strings.TrimSpace(status)))
}

func (s *Server) exportSessionIDs(rootID string, includeDescendants bool) ([]string, error) {
	if _, err := session.OpenByIDExact(s.SessionDir(), rootID); err != nil {
		return nil, ErrSessionNotFound
	}
	if !includeDescendants {
		return []string{rootID}, nil
	}
	details, err := session.ListAllDetailed(s.SessionDir())
	if err != nil {
		return []string{rootID}, nil
	}
	parent := make(map[string]string, len(details))
	for _, detail := range details {
		manager, err := session.OpenByIDExact(s.SessionDir(), detail.ID)
		if err != nil || manager.GetHeader() == nil {
			continue
		}
		parent[detail.ID] = manager.GetHeader().ParentSession
	}
	result := []string{rootID}
	seen := map[string]bool{rootID: true}
	for _, detail := range details {
		if detail.ID == rootID || seen[detail.ID] {
			continue
		}
		current := detail.ID
		visited := map[string]bool{}
		for current != "" && !visited[current] {
			if current == rootID {
				result = append(result, detail.ID)
				seen[detail.ID] = true
				break
			}
			visited[current] = true
			current = parent[current]
		}
	}
	if len(result) > 1 {
		sort.Strings(result[1:])
	}
	return result, nil
}

func decodeTrajectoryCursor(raw string) (trajectoryCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return trajectoryCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return trajectoryCursor{}, ErrInvalidTrajectoryCursor
	}
	var cursor trajectoryCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return trajectoryCursor{}, ErrInvalidTrajectoryCursor
	}
	return cursor, nil
}

func filterTrajectoryRecords(records []trajectoryRecord, cursor trajectoryCursor) []trajectoryRecord {
	if cursor.EntrySeq == 0 && cursor.RunSeq == 0 && cursor.CapabilitySeq == 0 && cursor.DecisionSeq == 0 {
		return records
	}
	out := make([]trajectoryRecord, 0, len(records))
	for _, item := range records {
		if snapshot, ok := item.value["snapshot"].(bool); ok && snapshot {
			continue
		}
		limit := int64(0)
		switch item.source {
		case "entry", "transcript":
			limit = cursor.EntrySeq
		case "run":
			limit = cursor.RunSeq
		case "decision":
			limit = cursor.DecisionSeq
		case "capability":
			limit = cursor.CapabilitySeq
		}
		if limit == 0 || item.seq < limit {
			out = append(out, item)
		}
	}
	return out
}

func trajectoryMessageKindStatus(message SessionMessageEntry) (string, string) {
	switch strings.ToLower(message.Role) {
	case "toolcall":
		return "tool", "running"
	case "toolresult":
		if message.IsError {
			return "tool", "failed"
		}
		return "tool", "completed"
	case "assistant":
		if message.IsError {
			return "error", "failed"
		}
		return "assistant", "completed"
	case "user":
		return "user", "completed"
	default:
		return "reasoning", "completed"
	}
}

func trajectoryMessageSummary(message SessionMessageEntry) string {
	if message.ToolName != "" {
		return message.ToolName
	}
	if strings.TrimSpace(message.Summary) != "" {
		return message.Summary
	}
	if strings.TrimSpace(message.Content) != "" {
		return firstLine(message.Content)
	}
	return message.Role
}

func trajectoryMessagePreview(message SessionMessageEntry) string {
	if message.Content != "" {
		return firstLine(message.Content)
	}
	return message.Summary
}

func trajectoryMessageID(sessionID string, message SessionMessageEntry) string {
	if message.ToolCallID != "" && (strings.EqualFold(message.Role, "toolCall") || strings.EqualFold(message.Role, "toolResult")) {
		return "tool:" + sessionID + ":" + message.ToolCallID
	}
	id := message.ID
	if id == "" {
		id = fmt.Sprintf("message:%d:%s", message.Seq, message.Role)
	}
	return "transcript:" + sessionID + ":" + id
}

func firstLine(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return value[:idx]
	}
	if len(value) > 180 {
		return value[:177] + "..."
	}
	return value
}

func normalizeTrajectoryStatus(status, eventType string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" {
		switch status {
		case "created", "queued", "running", "retrying", "terminalizing", "cancelling":
			return "running"
		case "waiting_for_approval", "waiting_for_question", "pending":
			return "pending"
		case "cancelled", "canceled":
			return "canceled"
		case "completed", "succeeded", "success", "done":
			return "completed"
		case "failed", "error", "timed_out", "incomplete":
			return "failed"
		}
		return status
	}
	eventType = strings.ToLower(eventType)
	if strings.Contains(eventType, "fail") || strings.Contains(eventType, "error") {
		return "failed"
	}
	if strings.Contains(eventType, "start") || strings.Contains(eventType, "begin") {
		return "running"
	}
	return "completed"
}

func parseTrajectoryTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func sourceOrder(source string) int {
	switch source {
	case "entry", "transcript":
		return 0
	case "run":
		return 1
	case "decision":
		return 2
	case "capability":
		return 3
	default:
		return 3
	}
}

func trajectoryEventID(source, sessionID, eventID string) string {
	return source + ":" + sessionID + ":" + eventID
}

func compactTrajectoryValue(value map[string]any) map[string]any {
	for key, item := range value {
		if item == nil {
			delete(value, key)
			continue
		}
		if text, ok := item.(string); ok && text == "" {
			delete(value, key)
			continue
		}
		if flag, ok := item.(bool); ok {
			if !flag && key == "hasDetail" {
				delete(value, key)
			}
		}
	}
	return value
}

func redactTrajectoryValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	return redactTrajectoryJSON(decoded)
}

func redactTrajectoryJSON(value any) any {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			lower := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
				item[key] = "[REDACTED]"
				continue
			}
			if lower == "workdir" || lower == "cwd" || lower == "sessiondir" || lower == "dbpath" || lower == "serverpath" || lower == "absolutepath" {
				item[key] = "[OMITTED]"
				continue
			}
			if lower == "data" || lower == "data_url" || lower == "dataurl" {
				if _, ok := child.(string); ok {
					item[key] = "[OMITTED]"
					continue
				}
			}
			item[key] = redactTrajectoryJSON(child)
		}
		return item
	case []any:
		for index := range item {
			item[index] = redactTrajectoryJSON(item[index])
		}
		return item
	default:
		return value
	}
}

func parseBoolQuery(raw string, fallback bool) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	return value, err
}

func safeSessionFilename(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		name = "session"
	}
	return "mothx-session-" + name
}

func writeTrajectoryError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "session trajectory unavailable"
	if err == ErrSessionNotFound {
		status = http.StatusNotFound
		message = err.Error()
	} else if err == ErrInvalidTrajectoryCursor {
		status = http.StatusBadRequest
		message = err.Error()
	}
	writeJSON(w, status, map[string]string{"error": message})
}
