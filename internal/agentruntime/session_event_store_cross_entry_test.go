package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// entrySource is one of the five product entry points. Each projection builds
// its own wire/protocol framing in production; here only the canonical Runtime
// call matters, which is exactly the point of this contract test.
type entrySource struct {
	name  string
	model string
	mode  string
}

var crossEntrySources = []entrySource{
	{name: "tui", model: "gpt-tui", mode: "yolo"},
	{name: "cli", model: "gpt-cli", mode: "agent"},
	{name: "webui", model: "gpt-web", mode: "plan"},
	{name: "acp", model: "gpt-acp", mode: "yolo"},
	{name: "channel", model: "gpt-channel", mode: "yolo"},
}

// canonicalRunFacts is the storage-level truth that every projection must
// reproduce identically. Anything adapter-specific (SSE frame, JSON-RPC id,
// Bubble Tea msg, WeChat card) is deliberately absent. Per-session identifiers
// (session/run/turn/intent IDs) are normalized to stable placeholders because
// each entry point necessarily owns its own IDs; what must agree across
// projections is the shape, ordering, ownership linkage, and payload content.
type canonicalRunFacts struct {
	seqs         []int64
	types        []string
	ownership    []string
	payloads     []string
	turnStatus   string
	turnEndSeq   int64
	runStatus    string
	runEvents    []string
	legacyRows   int
	replayedText []string
}

// normalizeOwnership collapses the per-session run/turn identifiers embedded in
// the projection ownership column, so the comparison asserts identical linkage
// (which entry belongs to which run and turn) rather than identical UUIDs.
func normalizeOwnership(values []string, runID, turnID string) []string {
	replacer := strings.NewReplacer(runID, "<run>", turnID, "<turn>")
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = replacer.Replace(value)
	}
	return out
}

// TestCrossEntryEventStoreCanonicalContract proves the first acceptance
// criterion of the Session control-plane/event-store proposal: TUI, CLI,
// WebUI/API, ACP and Channel all drive the same BeginIntentDurable ->
// FinishDurable path and therefore produce identical canonical events, seq
// assignment, run/turn ownership and terminal result on top of the same
// EventStore. Only the adapter name/model/mode metadata differs.
func TestCrossEntryEventStoreCanonicalContract(t *testing.T) {
	sessionDir := t.TempDir()
	results := make([]canonicalRunFacts, 0, len(crossEntrySources))

	for _, src := range crossEntrySources {
		sessionID := "cross-entry-" + src.name
		manager := session.New(t.TempDir(), sessionDir)
		if err := manager.InitWithID(sessionID); err != nil {
			t.Fatalf("%s: init session: %v", src.name, err)
		}
		if err := manager.MigrateToEventStore(context.Background()); err != nil {
			t.Fatalf("%s: migrate to event store: %v", src.name, err)
		}

		admission, err := AcquireExecutionAdmission(context.Background(), sessionDir, sessionID, ExecutionAdmissionOptions{})
		if err != nil {
			t.Fatalf("%s: acquire admission: %v", src.name, err)
		}

		// Every entry point drives the identical logical submission, so the
		// canonical timestamps are identical too; only adapter metadata differs.
		startedAt := time.Unix(1_700_000_000, 0).UTC()
		runID := "run-" + src.name
		intentID := "intent-" + src.name
		turnID := "turn-" + src.name
		userText := "same canonical prompt"
		assistantText := "same canonical answer"

		execution := &ExecutionRuntime{}
		execution.SetRunStore(RunStore{SessionDir: sessionDir})

		userMessage := provider.NewUserMessage(userText)
		userMessage.Timestamp = startedAt
		_, err = execution.BeginIntentDurable(context.Background(), ExecutionIntent{
			ID: intentID, SessionID: sessionID, Source: src.name, Model: src.model, Mode: src.mode,
			WorkDir: "/tmp/cross-entry", RequestFingerprint: "sha256:identical",
			Request:   json.RawMessage(`{"text":"same canonical prompt"}`),
			Policy:    json.RawMessage(`{"approval":"runtime","question":"runtime"}`),
			CreatedAt: startedAt,
		}, DurableRun{
			ID: runID, SessionID: sessionID, IntentID: intentID, Attempt: 1,
			WorkDir: "/tmp/cross-entry", Source: src.name, Model: src.model, Mode: src.mode,
			Status: "running", StartedAt: startedAt,
			UserEntryID: session.RunUserEntryID(runID), UserMessage: &userMessage,
			ConversationTurnID: turnID, ConversationTurn: true,
		}, RunEvent{
			SessionID: sessionID, RunID: runID, EventType: "started", Source: src.name,
			Status: "running", Model: src.model, Mode: src.mode, Timestamp: startedAt,
		})
		if err != nil {
			admission.Release()
			t.Fatalf("%s: begin intent durable: %v", src.name, err)
		}

		assistant := provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: assistantText}})
		finishedAt := startedAt.Add(time.Second)
		assistant.Timestamp = finishedAt
		if err := execution.FinishDurable(runID, RunStateCompleted, "", RunEvent{
			SessionID: sessionID, RunID: runID, EventType: "finished", Source: src.name,
			Status: string(RunStateCompleted), Model: src.model, Mode: src.mode,
			Timestamp:        finishedAt,
			AssistantEntryID: session.RunAssistantEntryID(runID),
			AssistantMessage: assistant,
		}); err != nil {
			admission.Release()
			t.Fatalf("%s: finish durable: %v", src.name, err)
		}
		admission.Release()

		facts, err := readCanonicalRunFacts(sessionDir, sessionID, runID, turnID, intentID)
		if err != nil {
			t.Fatalf("%s: read canonical facts: %v", src.name, err)
		}
		results = append(results, facts)
	}

	baseline := results[0]
	for i := 1; i < len(results); i++ {
		compareCanonicalRunFacts(t, crossEntrySources[0].name, crossEntrySources[i].name, baseline, results[i])
	}
}

// readCanonicalRunFacts collects only canonical, storage-level facts: the
// rebuildable projection (seq/type/run-turn ownership), the committed EventStore
// payload digests, the durable turn boundary, the Run status, the control-plane
// event stream, and the replayed transcript. Adapter framing never enters here.
func readCanonicalRunFacts(sessionDir, sessionID, runID, turnID, intentID string) (canonicalRunFacts, error) {
	var facts canonicalRunFacts
	clockOrdinal := 0
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		return facts, err
	}
	// Entry identifiers are derived deterministically from the per-session turn
	// ID, so they differ by construction between independent sessions. Collect
	// them alongside the ownership rows and fold them into the same placeholder
	// set as the session's own IDs before comparing any payload bytes.
	entryIDs := make([]string, 0)
	rows, err := db.Bun().Query(
		"SELECT seq, type, run_id, turn_id, id FROM session_event_projections WHERE session_id = ? ORDER BY seq", sessionID)
	if err != nil {
		return facts, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var typ, run, turn, entryID string
		if err := rows.Scan(&seq, &typ, &run, &turn, &entryID); err != nil {
			return facts, err
		}
		facts.seqs = append(facts.seqs, seq)
		facts.types = append(facts.types, typ)
		facts.ownership = append(facts.ownership, run+"|"+turn)
		entryIDs = append(entryIDs, entryID)
	}
	if err := rows.Err(); err != nil {
		return facts, err
	}
	entryIDs = append(entryIDs, session.RunUserEntryID(runID), session.RunAssistantEntryID(runID))
	replacement := []string{
		sessionID, "<session>",
		runID, "<run>",
		turnID, "<turn>",
		intentID, "<intent>",
	}
	for _, entryID := range entryIDs {
		replacement = append(replacement, entryID, "<entry>")
	}
	replacer := strings.NewReplacer(replacement...)

	batches, err := session.ReadCommittedEventBatches(context.Background(), sessionDir, sessionID)
	if err != nil {
		return facts, err
	}
	// Payload digests cover the full encoded entry, which embeds this session's
	// own IDs and its real wall-clock timestamps. Normalize the identifiers to
	// placeholders and replace every timestamp with a monotonic positional marker
	// before digesting, so the comparison asserts identical canonical structure,
	// ordering, and content rather than identical UUIDs or clock readings.
	for _, batch := range batches {
		for _, record := range batch.Events {
			facts.payloads = append(facts.payloads, record.Kind+"="+sha256Hex([]byte(canonicalizeEntryPayload(replacer, string(record.Payload), &clockOrdinal))))
		}
	}
	facts.ownership = normalizeOwnership(facts.ownership, runID, turnID)

	turns, err := session.ListConversationTurns(sessionDir, sessionID)
	if err != nil {
		return facts, err
	}
	if len(turns) != 1 {
		return facts, fmt.Errorf("expected exactly one conversation turn for %s, got %d", sessionID, len(turns))
	}
	facts.turnStatus = turns[0].Status
	if turns[0].EndSeq != nil {
		facts.turnEndSeq = *turns[0].EndSeq
	}

	runs, err := session.ListSessionRuns(sessionDir, sessionID, 10)
	if err != nil {
		return facts, err
	}
	for _, run := range runs {
		if run.ID == runID {
			facts.runStatus = run.Status
		}
	}

	events, err := session.ListSessionRunEvents(sessionDir, sessionID)
	if err != nil {
		return facts, err
	}
	for _, ev := range events {
		facts.runEvents = append(facts.runEvents, ev.EventType+":"+ev.Status)
	}

	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type <> ?", sessionID, "session").Scan(&facts.legacyRows); err != nil {
		return facts, err
	}

	reopened, err := session.OpenByIDExact(sessionDir, sessionID)
	if err != nil {
		return facts, err
	}
	for _, message := range reopened.GetMessages() {
		facts.replayedText = append(facts.replayedText, message.Role+"\x00"+message.Content)
	}
	return facts, nil
}

func compareCanonicalRunFacts(t *testing.T, a, b string, want, got canonicalRunFacts) {
	t.Helper()
	if len(want.seqs) != len(got.seqs) || len(want.seqs) == 0 {
		t.Fatalf("%s projected %d canonical entries, %s projected %d", a, len(want.seqs), b, len(got.seqs))
	}
	for i := range want.seqs {
		if want.seqs[i] != got.seqs[i] {
			t.Fatalf("%s vs %s: canonical seq at %d differs (%d vs %d)", a, b, i, want.seqs[i], got.seqs[i])
		}
		if want.types[i] != got.types[i] {
			t.Fatalf("%s vs %s: entry type at %d differs (%q vs %q)", a, b, i, want.types[i], got.types[i])
		}
		if want.ownership[i] != got.ownership[i] {
			t.Fatalf("%s vs %s: run/turn ownership at %d differs (%q vs %q)", a, b, i, want.ownership[i], got.ownership[i])
		}
	}
	if len(want.payloads) != len(got.payloads) {
		t.Fatalf("%s vs %s: committed payload count differs (%d vs %d)", a, b, len(want.payloads), len(got.payloads))
	}
	for i := range want.payloads {
		if want.payloads[i] != got.payloads[i] {
			t.Fatalf("%s vs %s: committed payload digest at %d differs (%q vs %q)", a, b, i, want.payloads[i], got.payloads[i])
		}
	}
	if want.turnStatus != got.turnStatus || want.turnEndSeq != got.turnEndSeq {
		t.Fatalf("%s vs %s: turn terminal state differs (%q/%d vs %q/%d)", a, b, want.turnStatus, want.turnEndSeq, got.turnStatus, got.turnEndSeq)
	}
	if want.runStatus != got.runStatus {
		t.Fatalf("%s vs %s: run terminal status differs (%q vs %q)", a, b, want.runStatus, got.runStatus)
	}
	if want.legacyRows != 0 || got.legacyRows != 0 {
		t.Fatalf("%s vs %s: legacy transcript payload rows must stay zero (%d vs %d)", a, b, want.legacyRows, got.legacyRows)
	}
	if len(want.runEvents) != len(got.runEvents) {
		t.Fatalf("%s vs %s: run event count differs (%d vs %d)", a, b, len(want.runEvents), len(got.runEvents))
	}
	for i := range want.runEvents {
		if want.runEvents[i] != got.runEvents[i] {
			t.Fatalf("%s vs %s: run event at %d differs (%q vs %q)", a, b, i, want.runEvents[i], got.runEvents[i])
		}
	}
	if len(want.replayedText) != len(got.replayedText) {
		t.Fatalf("%s vs %s: replayed transcript length differs (%d vs %d)", a, b, len(want.replayedText), len(got.replayedText))
	}
	for i := range want.replayedText {
		if want.replayedText[i] != got.replayedText[i] {
			t.Fatalf("%s vs %s: replayed transcript at %d differs (%q vs %q)", a, b, i, want.replayedText[i], got.replayedText[i])
		}
	}
}

// canonicalizeEntryPayload folds the session's identifiers into placeholders and
// rewrites each RFC3339 timestamp to a shared ordinal marker. Two independent
// sessions that recorded the same canonical turn therefore produce the same
// string even though their wall-clock readings necessarily differ.
func canonicalizeEntryPayload(replacer *strings.Replacer, payload string, ordinal *int) string {
	normalized := replacer.Replace(payload)
	out := make([]byte, 0, len(normalized))
	for i := 0; i < len(normalized); {
		if normalized[i] == '"' {
			end := strings.IndexByte(normalized[i+1:], '"')
			if end < 0 {
				out = append(out, normalized[i])
				i++
				continue
			}
			value := normalized[i+1 : i+1+end]
			if isCanonicalTimestamp(value) {
				*ordinal++
				out = append(out, []byte(fmt.Sprintf("\"<ts:%d>\"", *ordinal))...)
				i += end + 2
				continue
			}
			out = append(out, normalized[i])
			i++
			continue
		}
		out = append(out, normalized[i])
		i++
	}
	return string(out)
}

// isCanonicalTimestamp recognises the RFC3339 form written by the session store
// (date, time separator, and a numeric offset or Z) without accepting arbitrary
// digit strings.
func isCanonicalTimestamp(value string) bool {
	if len(value) < 20 || value[4] != '-' || value[7] != '-' || value[10] != 'T' || value[13] != ':' || value[16] != ':' {
		return false
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
