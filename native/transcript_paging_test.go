package main

import (
	"encoding/json"
	"testing"
)

func transcriptTextUpdate(kind, id, text string) sessionUpdate {
	return sessionUpdate{
		SessionUpdate: kind,
		MessageID:     id,
		Content:       json.RawMessage(`{"type":"text","text":"` + text + `"}`),
	}
}

func TestApplyTranscriptPageInitialAndPrepend(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyTranscriptPage("s1", &transcriptPageResult{
		SessionID:  "s1",
		Updates:    []sessionUpdate{transcriptTextUpdate("agent_message_chunk", "m2", "newer")},
		NextCursor: "older-1",
	}, false)
	if len(a.messages) != 1 || a.messages[0].Text != "newer" || a.transcriptNextCursor != "older-1" {
		t.Fatalf("initial page = %+v cursor=%q", a.messages, a.transcriptNextCursor)
	}

	a.applyTranscriptPage("s1", &transcriptPageResult{
		SessionID: "s1",
		Updates: []sessionUpdate{
			transcriptTextUpdate("agent_message_chunk", "m1", "older"),
			transcriptTextUpdate("agent_message_chunk", "m2", "duplicate"),
		},
		NextCursor: "",
	}, true)
	if len(a.messages) != 2 {
		t.Fatalf("prepend dedupe = %+v", a.messages)
	}
	if a.messages[0].ID != "agent:m1" || a.messages[0].Text != "older" {
		t.Fatalf("older message = %+v", a.messages[0])
	}
	if a.messages[1].ID != "agent:m2" || a.messages[1].Text != "newer" {
		t.Fatalf("newer message must stay authoritative: %+v", a.messages[1])
	}
	if a.transcriptNextCursor != "" {
		t.Fatalf("cursor = %q", a.transcriptNextCursor)
	}
	if a.messageByID("agent:m1") != a.messages[0] {
		t.Fatal("message index was not rebuilt")
	}
}

func TestTranscriptPagingStaysOnTheCurrentSession(t *testing.T) {
	a := testApp()
	a.currentSession = "s2"
	a.applyTranscriptPage("s1", &transcriptPageResult{
		SessionID:  "s1",
		Updates:    []sessionUpdate{transcriptTextUpdate("agent_message_chunk", "x", "other")},
		NextCursor: "c",
	}, false)
	if len(a.messages) != 0 || a.transcriptNextCursor != "" {
		t.Fatalf("stale page landed: %+v cursor=%q", a.messages, a.transcriptNextCursor)
	}
}

func TestResetTranscriptInvalidatesOlderLoad(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.transcriptNextCursor = "cursor"
	a.transcriptLoading = true
	seq := a.transcriptSeq
	a.resetTranscript()
	if a.transcriptNextCursor != "" || a.transcriptLoading {
		t.Fatalf("transcript paging state leaked: cursor=%q loading=%v", a.transcriptNextCursor, a.transcriptLoading)
	}
	if a.transcriptSeq <= seq {
		t.Fatalf("reset must invalidate in-flight paging: %d -> %d", seq, a.transcriptSeq)
	}
}
