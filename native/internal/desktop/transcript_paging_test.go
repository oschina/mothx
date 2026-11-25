package desktop

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

// Live streaming delivers deltas: every chunk appends, even when the next
// delta happens to start with everything received so far. A prefix heuristic
// used to swallow such chunks and corrupt code fences and repeated tokens.
func TestLiveStreamChunksAlwaysAppend(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	// Chunk text goes through a real encoder: these chunks contain newlines
	// and quotes that the concatenating helper above cannot carry.
	stream := func(id string, chunks ...string) {
		for _, chunk := range chunks {
			content, _ := json.Marshal(map[string]any{"type": "text", "text": chunk})
			a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: sessionUpdate{
				SessionUpdate: "agent_message_chunk", MessageID: id, Content: content,
			}})
		}
	}
	stream("fence", "`", "`", "`", "go\nfmt.Println(1)\n", "```")
	if got := a.messageByID("agent:fence").Text; got != "```go\nfmt.Println(1)\n```" {
		t.Fatalf("streamed code fence = %q", got)
	}
	// A horizontal rule streamed as three dashes, and a repeated short token.
	stream("rule", "-", "-", "-")
	if got := a.messageByID("agent:rule").Text; got != "---" {
		t.Fatalf("streamed horizontal rule = %q", got)
	}
	stream("repeat", "好", "好的")
	if got := a.messageByID("agent:repeat").Text; got != "好好的" {
		t.Fatalf("streamed repeated token = %q", got)
	}
}

// Switching back to a task replays its history onto the cached transcript.
// Live messages carry stream ids the page never reuses, so replaying used to
// append every live turn a second time.
func TestInitialPageMergeDoesNotDuplicateLiveTurn(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	first := &transcriptPageResult{SessionID: "s1", Updates: []sessionUpdate{
		transcriptTextUpdate("user_message_chunk", "acp_replay_user_1", "hi"),
		transcriptTextUpdate("agent_message_chunk", "acp_replay_message_1", "hello"),
	}}
	a.applyTranscriptPage("s1", first, false)

	// A live turn streams in under the Runtime's stream ids.
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: transcriptTextUpdate("user_message_chunk", "acp_s1_p2_user", "again")})
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: transcriptTextUpdate("agent_message_chunk", "acp_s1_p2_message_0", "wor")})
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: transcriptTextUpdate("agent_message_chunk", "acp_s1_p2_message_0", "ld")})
	a.markRunDone("end_turn")

	// Switch away and back: the cached transcript is restored, then loaded.
	a.saveActiveSession()
	a.selectSessionState("s1")
	reloaded := &transcriptPageResult{SessionID: "s1", Updates: []sessionUpdate{
		transcriptTextUpdate("user_message_chunk", "acp_replay_user_1", "hi"),
		transcriptTextUpdate("agent_message_chunk", "acp_replay_message_1", "hello"),
		transcriptTextUpdate("user_message_chunk", "acp_replay_user_2", "again"),
		transcriptTextUpdate("agent_message_chunk", "acp_replay_message_2", "world"),
	}}
	a.applyTranscriptPage("s1", reloaded, false)

	want := []struct{ id, text string }{
		{"user:acp_replay_user_1", "hi"},
		{"agent:acp_replay_message_1", "hello"},
		{"user:acp_replay_user_2", "again"},
		{"agent:acp_replay_message_2", "world"},
	}
	if len(a.messages) != len(want) {
		names := make([]string, 0, len(a.messages))
		for _, m := range a.messages {
			names = append(names, m.ID+"="+m.Text)
		}
		t.Fatalf("merged transcript has %d messages, want %d: %v", len(a.messages), len(want), names)
	}
	for i, expected := range want {
		if a.messages[i].ID != expected.id || a.messages[i].Text != expected.text {
			t.Fatalf("message %d = %s %q, want %s %q", i, a.messages[i].ID, a.messages[i].Text, expected.id, expected.text)
		}
		if a.messageByID(expected.id) != a.messages[i] {
			t.Fatalf("message %d is not indexed under its canonical id", i)
		}
	}

	// Loading the same page again changes nothing.
	a.applyTranscriptPage("s1", reloaded, false)
	if len(a.messages) != len(want) || a.messages[3].Text != "world" {
		t.Fatalf("re-applying the page changed the transcript: %d messages", len(a.messages))
	}
}

// Two different turns with identical text are two messages; position, not
// text alone, decides what a replayed message stands for.
func TestInitialPageMergeKeepsIdenticalTurns(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: transcriptTextUpdate("agent_message_chunk", "stream_a", "好的")})
	a.markRunDone("end_turn")
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: transcriptTextUpdate("agent_message_chunk", "stream_b", "好的")})
	a.markRunDone("end_turn")

	a.applyTranscriptPage("s1", &transcriptPageResult{SessionID: "s1", Updates: []sessionUpdate{
		transcriptTextUpdate("agent_message_chunk", "replay_a", "好的"),
		transcriptTextUpdate("agent_message_chunk", "replay_b", "好的"),
	}}, false)
	if len(a.messages) != 2 {
		t.Fatalf("identical turns collapsed: %d messages", len(a.messages))
	}
	if a.messages[0].ID != "agent:replay_a" || a.messages[1].ID != "agent:replay_b" {
		t.Fatalf("canonical ids = %q, %q", a.messages[0].ID, a.messages[1].ID)
	}
}

// A message that is still streaming is never mistaken for a replay of an
// older message with the same text.
func TestInitialPageMergeLeavesStreamingMessagesAlone(t *testing.T) {
	a := testApp()
	a.currentSession = "s1"
	a.applyUpdate(sessionUpdateNotification{SessionID: "s1", Update: transcriptTextUpdate("agent_message_chunk", "stream_new", "好的")})
	if a.messageByID("agent:stream_new").Done {
		t.Fatal("a streamed message must still be open before the run ends")
	}
	a.applyTranscriptPage("s1", &transcriptPageResult{SessionID: "s1", Updates: []sessionUpdate{
		transcriptTextUpdate("agent_message_chunk", "replay_old", "好的"),
	}}, false)
	if len(a.messages) != 2 || a.messages[0].ID != "agent:stream_new" || a.messages[1].ID != "agent:replay_old" {
		t.Fatalf("streaming message was adopted by an older replay: %d messages", len(a.messages))
	}
}

// A task is reloaded from its cached transcript only when the Runtime pages
// history. Without paging the whole transcript is replayed as notifications
// under ids derived from the text, which is only idempotent on an empty
// transcript.
func TestSelectSessionRestoresCacheOnlyWithTranscriptPaging(t *testing.T) {
	s := listedSession{SessionID: "s1", Cwd: "/work", Title: "task"}

	a := testApp()
	a.currentSession = "s1"
	a.applyTranscriptPage("s1", &transcriptPageResult{SessionID: "s1", Updates: []sessionUpdate{
		transcriptTextUpdate("agent_message_chunk", "m1", "cached"),
	}}, false)
	a.saveActiveSession()
	a.currentSession = ""
	a.setFeatures(map[string]bool{"sessionHistoryPaging": true})
	a.selectSession(s)
	if len(a.messages) != 1 || a.messages[0].Text != "cached" {
		t.Fatalf("paged runtime must restore the cached transcript: %d messages", len(a.messages))
	}

	b := testApp()
	b.currentSession = "s1"
	b.applyTranscriptPage("s1", &transcriptPageResult{SessionID: "s1", Updates: []sessionUpdate{
		transcriptTextUpdate("agent_message_chunk", "m1", "cached"),
	}}, false)
	b.saveActiveSession()
	b.currentSession = ""
	b.setFeatures(map[string]bool{})
	b.selectSession(s)
	if len(b.messages) != 0 {
		t.Fatalf("unpaged runtime must start from a clean transcript: %d messages", len(b.messages))
	}
	if _, cached := b.sessionStates["s1"]; cached {
		t.Fatal("the stale cache must be dropped so a later switch cannot restore it")
	}
}
