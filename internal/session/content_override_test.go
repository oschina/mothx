package session

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/startvibecoding/mothx/internal/provider"
)

func imageToolResultMessage() provider.Message {
	msg := provider.NewToolResultMessage("call-1", "read", "[Image file: /tmp/x.png, 4x4, 10B, mode: auto]", false)
	msg.Contents = []provider.ContentBlock{
		{Type: "image", Image: &provider.ImageContent{Data: "AAAA", MimeType: "image/png", Width: 4, Height: 4}},
	}
	return msg
}

func TestAppendContentOverrideReplacesMessageOnReplay(t *testing.T) {
	sessionDir := t.TempDir()
	m := New(t.TempDir(), sessionDir)
	if err := m.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := m.AppendMessage(provider.NewUserMessage("look at this")); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	target := imageToolResultMessage()
	targetID, err := m.AppendMessage(target)
	if err != nil {
		t.Fatalf("append image tool result: %v", err)
	}

	replacement := target
	replacement.Contents = nil
	replacement.Content = target.Content + "\n\n[image unavailable] 1 image(s) could not be sent to the model"
	if _, err := m.AppendContentOverride(targetID, replacement, "rejected", "content_filter"); err != nil {
		t.Fatalf("append content override: %v", err)
	}

	assertOverrideApplied := func(t *testing.T, state ReplayState) {
		t.Helper()
		if len(state.Messages) != 2 {
			t.Fatalf("message count = %d, want 2", len(state.Messages))
		}
		got := state.Messages[1]
		if len(got.Contents) != 0 {
			t.Fatalf("overridden message still has image content: %#v", got.Contents)
		}
		if !strings.Contains(got.Content, "image unavailable") {
			t.Fatalf("overridden message missing placeholder: %q", got.Content)
		}
		if got.Role != "toolResult" || got.ToolCallID != "call-1" || got.ToolName != "read" {
			t.Fatalf("override changed message identity: %#v", got)
		}
		// The target entry ID is preserved so compaction pivots and forks stay valid.
		if state.EntryIDs[1] != targetID {
			t.Fatalf("entry id = %q, want %q", state.EntryIDs[1], targetID)
		}
	}

	assertOverrideApplied(t, m.GetReplayState())

	// A reload must apply the persisted override, not the original image entry.
	reopened, err := OpenByIDExact(sessionDir, m.GetHeader().ID)
	if err != nil {
		t.Fatalf("reopen session: %v", err)
	}
	assertOverrideApplied(t, reopened.GetReplayState())

	sequenced, err := ListSessionMessagesWithSeq(sessionDir, m.GetHeader().ID)
	if err != nil {
		t.Fatalf("list sequenced messages: %v", err)
	}
	if len(sequenced) != 2 {
		t.Fatalf("sequenced message count = %d, want 2", len(sequenced))
	}
	if len(sequenced[1].Message.Contents) != 0 || !strings.Contains(sequenced[1].Message.Content, "image unavailable") {
		t.Fatalf("sequenced replay did not apply override: %#v", sequenced[1].Message)
	}
}

func TestRemapForkDataRewritesContentOverrideTarget(t *testing.T) {
	entry := ContentOverrideEntry{
		EntryBase:     EntryBase{Type: EntryContentOverride, ID: "override-1"},
		TargetEntryID: "message-1",
		Message:       provider.NewUserMessage("stripped"),
		Reason:        "rejected",
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}

	out, err := remapForkData(string(EntryContentOverride), string(raw), map[string]string{"message-1": "message-1-fork"}, nil)
	if err != nil {
		t.Fatalf("remap fork data: %v", err)
	}
	var got ContentOverrideEntry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal remapped entry: %v", err)
	}
	if got.TargetEntryID != "message-1-fork" {
		t.Fatalf("target entry ID = %q, want message-1-fork", got.TargetEntryID)
	}
}

func TestAppendContentOverrideRejectsUnknownTarget(t *testing.T) {
	sessionDir := t.TempDir()
	m := New(t.TempDir(), sessionDir)
	if err := m.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := m.AppendContentOverride("missing", provider.NewUserMessage("x"), "reason", ""); err == nil {
		t.Fatal("expected an error for an unknown target entry")
	}
}
