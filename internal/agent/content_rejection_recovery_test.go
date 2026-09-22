package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func imageMessage(role, text string) provider.Message {
	msg := provider.Message{Role: role, Content: text}
	if role == "toolResult" {
		msg.ToolCallID = "call-" + text
		msg.ToolName = "read"
	}
	msg.Contents = []provider.ContentBlock{{Type: "image", Image: &provider.ImageContent{Data: "AAAA", MimeType: "image/png"}}}
	return msg
}

func newContentRejectionAgent(t *testing.T) (*Agent, *session.Manager) {
	t.Helper()
	sess := session.New(t.TempDir(), t.TempDir())
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	messages := []provider.Message{
		provider.NewUserMessage("old question"),
		imageMessage("toolResult", "old"),
		provider.NewUserMessage("current question"),
		imageMessage("toolResult", "current"),
	}
	ids := make([]string, len(messages))
	for i, msg := range messages {
		id, err := sess.AppendMessage(msg)
		if err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
		ids[i] = id
	}
	a := &Agent{
		config:     AgentLoopConfig{Config: Config{Session: sess}},
		messages:   messages,
		messageIDs: ids,
		context:    &AgentContext{Messages: append([]provider.Message(nil), messages...)},
	}
	return a, sess
}

func hasImage(msg provider.Message) bool {
	for _, block := range msg.Contents {
		if block.Type == "image" || block.Image != nil {
			return true
		}
	}
	return false
}

func TestContentRejectionRecoveryStripsInTwoStages(t *testing.T) {
	a, sess := newContentRejectionAgent(t)
	ch := make(chan Event, 16)
	stage := 0
	cause := errors.New(`API error 400: {"message":"<400> InternalError.Algo.DataInspectionFailed: Input image data may contain inappropriate content."}`)

	// Stage 1 strips only images introduced since the last user turn (index 3),
	// keeping the older historical image (index 1).
	if !a.tryRecoverContentRejection(ch, &stage, false, cause) {
		t.Fatal("stage 1 recovery returned false")
	}
	if stage != 1 {
		t.Fatalf("stage = %d, want 1", stage)
	}
	if hasImage(a.messages[3]) {
		t.Fatal("current-turn image was not stripped")
	}
	if !hasImage(a.messages[1]) {
		t.Fatal("older historical image must survive stage 1")
	}
	if !strings.Contains(a.messages[3].Content, "image unavailable") {
		t.Fatalf("model placeholder missing: %q", a.messages[3].Content)
	}
	if !strings.Contains(a.messages[3].Content, "content filter") {
		t.Fatalf("placeholder does not explain the rejection reason: %q", a.messages[3].Content)
	}
	if a.messages[3].Role != "toolResult" || a.messages[3].ToolCallID != "call-current" {
		t.Fatalf("stage 1 changed message identity: %#v", a.messages[3])
	}

	// Stage 2 strips every remaining image so the session can always recover.
	if !a.tryRecoverContentRejection(ch, &stage, false, cause) {
		t.Fatal("stage 2 recovery returned false")
	}
	if stage != 2 {
		t.Fatalf("stage = %d, want 2", stage)
	}
	if hasImage(a.messages[1]) {
		t.Fatal("historical image was not stripped in stage 2")
	}

	// Budget is exhausted: a third attempt must give up so the run fails.
	if a.tryRecoverContentRejection(ch, &stage, false, cause) {
		t.Fatal("recovery must stop after the final stage")
	}

	// The persisted replay must no longer contain any image, so later turns
	// (and reloads) never re-send the refused content.
	for i, msg := range sess.GetReplayState().Messages {
		if hasImage(msg) {
			t.Fatalf("replayed message %d still contains an image", i)
		}
	}
}

func TestContentRejectionRecoveryIgnoresOtherErrors(t *testing.T) {
	a, _ := newContentRejectionAgent(t)
	ch := make(chan Event, 16)
	stage := 0
	if a.tryRecoverContentRejection(ch, &stage, false, errors.New("API error 400: invalid parameter: model")) {
		t.Fatal("non content-rejection error must not trigger recovery")
	}
	// Nothing was stripped.
	if !hasImage(a.messages[3]) {
		t.Fatal("unrelated error mutated the conversation")
	}
}

func TestContentRejectionRecoveryHealsWithoutRetryAfterPartialOutput(t *testing.T) {
	a, sess := newContentRejectionAgent(t)
	ch := make(chan Event, 16)
	stage := 0
	cause := errors.New("Input image data may contain inappropriate content")

	// With already-streamed output, recovery must not request a retry (which
	// would duplicate output) but must still strip every image so the session
	// recovers on the next turn.
	if a.tryRecoverContentRejection(ch, &stage, true, cause) {
		t.Fatal("recovery must not request a retry after visible output")
	}
	if hasImage(a.messages[1]) || hasImage(a.messages[3]) {
		t.Fatal("visible-output recovery must strip every image")
	}
	for i, msg := range sess.GetReplayState().Messages {
		if hasImage(msg) {
			t.Fatalf("replayed message %d still contains an image", i)
		}
	}
}

func TestContentRejectionRecoveryEscalatesWhenTurnHasNoImages(t *testing.T) {
	sess := session.New(t.TempDir(), t.TempDir())
	if err := sess.Init(); err != nil {
		t.Fatalf("init session: %v", err)
	}
	messages := []provider.Message{
		imageMessage("toolResult", "historical"),
		provider.NewUserMessage("text only current turn"),
	}
	ids := make([]string, len(messages))
	for i, msg := range messages {
		id, err := sess.AppendMessage(msg)
		if err != nil {
			t.Fatalf("append message %d: %v", i, err)
		}
		ids[i] = id
	}
	a := &Agent{
		config:     AgentLoopConfig{Config: Config{Session: sess}},
		messages:   messages,
		messageIDs: ids,
		context:    &AgentContext{Messages: append([]provider.Message(nil), messages...)},
	}
	ch := make(chan Event, 16)
	stage := 0
	cause := errors.New("Input image data may contain inappropriate content")
	if !a.tryRecoverContentRejection(ch, &stage, false, cause) {
		t.Fatal("recovery should escalate past an image-free current turn")
	}
	if stage != 2 {
		t.Fatalf("stage = %d, want 2 (escalated)", stage)
	}
	if hasImage(a.messages[0]) {
		t.Fatal("historical image was not stripped during escalation")
	}
}
