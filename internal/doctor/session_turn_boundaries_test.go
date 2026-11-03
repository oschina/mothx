package doctor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// migratedTurnSessionForTest builds a session that the event store owns and that
// has one completed conversation turn, so its boundary markers exist.
func migratedTurnSessionForTest(t *testing.T, sessionDir, sessionID, workDir string) {
	t.Helper()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if _, err := manager.AppendMessage(provider.NewUserMessage("hello")); err != nil {
		t.Fatal(err)
	}
	guard, err := session.AcquireMutation(sessionDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.StartConversationTurn(sessionDir, session.ConversationTurn{
		ID: "turn-1", SessionID: sessionID, IntentID: "intent-1", RunID: "run-1",
		Kind: "conversation", Status: "open", StartedAt: startedAt,
	}); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewAssistantMessage([]provider.ContentBlock{{Type: "text", Text: "world"}})); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	if err := session.EndConversationTurn(sessionDir, sessionID, "turn-1", "completed", "end_turn", startedAt.Add(time.Second)); err != nil {
		guard.Release()
		t.Fatal(err)
	}
	guard.Release()
	if err := manager.MigrateToEventStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := session.CloseDatabases(); err != nil {
		t.Fatal(err)
	}
}

// TestCheckSessionTurnBoundariesPassesForMigratedSession proves the check does
// not fire on the healthy population: a turn whose marker the event store owns.
func TestCheckSessionTurnBoundariesPassesForMigratedSession(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "doctor-turn-healthy"
	migratedTurnSessionForTest(t, sessionDir, sessionID, t.TempDir())

	check := checkSessionTurnBoundaries(doctorSettingsForTest(t, sessionDir))
	if check.Status != StatusOK {
		t.Fatalf("turn-boundary check = %#v, want ok for a migrated session", check)
	}
}

// TestCheckSessionTurnBoundariesReportsTurnWithNoMarker is the defect this check
// exists for: an event-store session whose turn row survived while the marker
// that bounds it did not. The turn still reads, but it has no fork point, and
// the check must say so instead of reporting a clean store.
func TestCheckSessionTurnBoundariesReportsTurnWithNoMarker(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "doctor-turn-unmapped"
	migratedTurnSessionForTest(t, sessionDir, sessionID, t.TempDir())

	// Drop the marker rows exactly as unrecoverable content loss would: the
	// control row remains and every bound now addresses nothing.
	if err := session.WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM session_event_projections WHERE session_id = ? AND type IN ('turn_start','turn_end')`, sessionID)
		return err
	}); err != nil {
		t.Fatalf("drop turn markers: %v", err)
	}

	check := checkSessionTurnBoundaries(doctorSettingsForTest(t, sessionDir))
	if check.Status != StatusWarn {
		t.Fatalf("turn-boundary check = %#v, want a warning for an unmapped turn", check)
	}
	if !strings.Contains(check.Detail, sessionID) {
		t.Fatalf("detail = %q, want the affected session ID", check.Detail)
	}
	if !strings.Contains(check.Detail, "1 turn(s)") {
		t.Fatalf("detail = %q, want the unmapped turn count", check.Detail)
	}
	if check.Fix == "" {
		t.Fatal("a warning about unrecoverable boundaries must explain what the user can do")
	}
}

// TestDoctorReportsTurnBoundaryCheck proves the check is actually registered in
// the doctor report, not merely implemented. Without this, dropping it from the
// check list would leave every other test green while users stop being told.
func TestDoctorReportsTurnBoundaryCheck(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "doctor-turn-registered"
	migratedTurnSessionForTest(t, sessionDir, sessionID, t.TempDir())
	t.Setenv("MOTHX_DIR", t.TempDir())

	settings := doctorSettingsForTest(t, sessionDir)
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	result := RunWithOptions(Options{CWD: cwdForDoctorTest(t)})
	found := false
	for _, check := range result.Checks {
		if check.ID == "sessions.turn_boundaries" {
			found = true
			if check.Status != StatusOK {
				t.Fatalf("turn-boundary check = %#v, want ok", check)
			}
		}
	}
	if !found {
		t.Fatal("doctor did not report sessions.turn_boundaries")
	}
}

func cwdForDoctorTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// TestCheckSessionTurnBoundariesIgnoresLegacyStore keeps the check honest: a
// legacy session's bounds are `entries.seq` values and have no projection marker
// by design, so reporting them would be a false alarm on a healthy database.
func TestCheckSessionTurnBoundariesIgnoresLegacyStore(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "doctor-turn-legacy"
	manager := session.New(t.TempDir(), sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy")); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	if err := session.StartConversationTurn(sessionDir, session.ConversationTurn{
		ID: "turn-legacy", SessionID: sessionID, IntentID: "intent-legacy", RunID: "run-legacy",
		Kind: "conversation", Status: "open", StartedAt: startedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.CloseDatabases(); err != nil {
		t.Fatal(err)
	}

	check := checkSessionTurnBoundaries(doctorSettingsForTest(t, sessionDir))
	if check.Status != StatusOK {
		t.Fatalf("turn-boundary check = %#v, want ok for a session still on the legacy store", check)
	}
}
