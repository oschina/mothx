package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDatabaseIntegrityNotificationReachesTheUser(t *testing.T) {
	SetLocale("zh")
	t.Cleanup(func() { SetLocale("zh") })
	clearDiagnostics()
	t.Cleanup(clearDiagnostics)
	a := testApp()
	a.post = func(fn func()) { fn() }
	notify := func(params string) {
		t.Helper()
		a.handleIncoming(nil, incoming{Method: "mothx/database/integrity", Params: json.RawMessage(params)})
	}

	// A rebuilt index changed no row: it is recorded, not shown.
	notify(`{"kind":"index_repaired","path":"/x/sessions.db","detail":"rebuilt stale SQLite indexes in /x/sessions.db","at":"2026-11-26T12:00:00Z"}`)
	if len(a.messages) != 0 {
		t.Fatalf("a rebuilt index was put in the transcript: %#v", a.messages)
	}
	entries := diagnosticSnapshot()
	if len(entries) != 1 || entries[0].Level != "info" || !strings.Contains(entries[0].Text, "index_repaired") {
		t.Fatalf("diagnostics = %#v, want one info entry for the repaired index", entries)
	}

	// Damage needs the user, in the transcript and not only in a log panel.
	notify(`{"kind":"corrupt","path":"/x/sessions.db","detail":"SQLite integrity check failed for /x/sessions.db: *** in database main ***","report":"*** in database main ***","at":"2026-11-26T12:00:01Z"}`)
	if len(a.messages) != 1 {
		t.Fatalf("messages = %#v, want the corruption in the transcript", a.messages)
	}
	corruption := a.messages[0]
	if corruption.Kind != messageSystem || !corruption.Done || corruption.ID == "" {
		t.Fatalf("corruption message = %#v, want a finished system line", corruption)
	}
	if !strings.Contains(corruption.Text, "SQLite integrity check failed for /x/sessions.db") || !strings.Contains(corruption.Text, "请停止 MothX") {
		t.Fatalf("corruption text = %q, want the Runtime's detail and what to do about it", corruption.Text)
	}
	entries = diagnosticSnapshot()
	if last := entries[len(entries)-1]; last.Level != "error" || !strings.Contains(last.Text, "corrupt") {
		t.Fatalf("diagnostics = %#v, want the corruption recorded as an error", entries)
	}

	// A kind this build does not know is read as a check that could not
	// finish, which is the safe reading of an unknown outcome.
	notify(`{"kind":"something_new","path":"/x/sessions.db","detail":"unknown outcome"}`)
	if len(a.messages) != 2 || !strings.Contains(a.messages[1].Text, "mothx doctor") {
		t.Fatalf("messages = %#v, want an unknown kind treated as an unfinished check", a.messages)
	}

	// A payload that does not parse changes nothing.
	notify(`{`)
	if len(a.messages) != 2 {
		t.Fatalf("messages = %#v, want a malformed payload ignored", a.messages)
	}

	SetLocale("en")
	b := testApp()
	SetLocale("en")
	b.applyDatabaseIntegrity(databaseIntegrityPayload{Kind: databaseIntegrityCorrupt, Path: "/x/sessions.db", Detail: "SQLite integrity check failed for /x/sessions.db"})
	if len(b.messages) != 1 {
		t.Fatalf("english messages = %#v, want the corruption in the transcript", b.messages)
	}
	if text := b.messages[0].Text; !strings.HasPrefix(text, "The session database failed its integrity check: SQLite integrity check failed for /x/sessions.db\n") || !strings.Contains(text, "Stop MothX, back up the database") {
		t.Fatalf("english text = %q, want the translated lead-in, the Runtime's detail, and the translated advice", text)
	}
}
