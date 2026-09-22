package openaiapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

// SSEWriter helps write Server-Sent Events to an HTTP response.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	model   string
	id      string
	created int64
	sessID  string
}

// NewSSEWriter creates an SSE writer and sets the appropriate headers.
func NewSSEWriter(w http.ResponseWriter, model, sessionID string) *SSEWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	flusher, _ := w.(http.Flusher)

	id := newCompletionID()
	return &SSEWriter{
		w:       w,
		flusher: flusher,
		model:   model,
		id:      id,
		created: time.Now().Unix(),
		sessID:  sessionID,
	}
}

// WriteContentDelta sends a text content delta chunk.
func (s *SSEWriter) WriteContentDelta(content string) {
	chunk := ChatCompletionChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Delta: &ResponseMessage{Content: content},
			},
		},
	}
	s.writeData(chunk)
}

// WriteRoleDelta sends the initial role delta.
func (s *SSEWriter) WriteRoleDelta() {
	chunk := ChatCompletionChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Delta: &ResponseMessage{Role: "assistant"},
			},
		},
	}
	s.writeData(chunk)
}

// WriteToolStatusContent sends a tool status in content mode (text in content delta).
// Uses a compact title like "read: path=main.go" rather than dumping full args.
func (s *SSEWriter) WriteToolStatusContent(title, status string) {
	text := fmt.Sprintf("[%s] %s\n", status, title)
	s.WriteContentDelta(text)
}

// WriteToolResult sends formatted tool output based on detail level.
func (s *SSEWriter) WriteToolResult(tc *toolCallInfo, detail string) {
	text := formatToolResult(tc, detail)
	s.WriteContentDelta(text)
}

// WriteToolStatusEvent sends a tool status as an SSE event (sse_event mode).
func (s *SSEWriter) WriteToolStatusEvent(evt ToolStatusEvent) {
	data, _ := json.Marshal(evt)
	fmt.Fprintf(s.w, "event: tool_status\ndata: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteTranscriptEvent sends a WebUI transcript event that can be rendered with
// the same components used for persisted session history.
func (s *SSEWriter) WriteTranscriptEvent(evt TranscriptStreamEvent) {
	if evt.XSessionID == "" {
		evt.XSessionID = s.sessID
	}
	data, _ := json.Marshal(evt)
	fmt.Fprintf(s.w, "event: transcript\ndata: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteAttachments sends provider-neutral artifacts as a dedicated SSE event.
func (s *SSEWriter) WriteAttachments(items []provider.Attachment) {
	if len(items) == 0 {
		return
	}
	data, _ := json.Marshal(items)
	fmt.Fprintf(s.w, "event: attachments\ndata: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteHostedItem sends a native hosted-tool lifecycle event without exposing
// the provider's unbounded canonical payload to the live client.
func (s *SSEWriter) WriteHostedItem(item *HostedItemEvent) {
	if item == nil {
		return
	}
	data, _ := json.Marshal(item)
	fmt.Fprintf(s.w, "event: hosted_item\ndata: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteApprovalRequest sends a pending tool approval to the client that initiated
// this streaming completion. It is also published on the session stream so other
// clients and reconnecting WebUI instances receive the same request.
func (s *SSEWriter) WriteApprovalRequest(request SessionApprovalRequest) {
	data, _ := json.Marshal(request)
	fmt.Fprintf(s.w, "event: approval_request\ndata: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteStatusEvent sends a non-error progress/status event.
func (s *SSEWriter) WriteStatusEvent(message string) {
	data, _ := json.Marshal(map[string]string{"message": message})
	fmt.Fprintf(s.w, "event: status\ndata: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *SSEWriter) WriteDone(usage *CompletionUsage) {
	s.WriteDoneReason(usage, "stop")
}

// WriteDoneReason sends the final completion chunk with the provider-compatible finish reason.
func (s *SSEWriter) WriteDoneReason(usage *CompletionUsage, finishReason string) {
	if finishReason == "" {
		finishReason = "stop"
	}

	chunk := ChatCompletionChunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.model,
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Delta:        &ResponseMessage{},
				FinishReason: &finishReason,
			},
		},
		Usage: usage,
	}
	s.writeData(chunk)

	// Send [DONE] sentinel
	fmt.Fprintf(s.w, "data: [DONE]\n\n")
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// WriteError sends an error as a final chunk.
func (s *SSEWriter) WriteError(errMsg string) {
	// Streaming errors use a regular SSE data frame. The endpoint does not emit
	// provider-specific event names or VibeCoding extension fields.
	data, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": errMsg, "type": "server_error"},
	})
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *SSEWriter) writeData(v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}
