package doctor

import (
	"context"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

// splitSessionForTest creates the split-store population this repair exists for:
// a session whose content store already says event_store while every transcript
// row still lives in the legacy SQLite table (a migrated session without an
// export, or one an older build kept writing).
func splitSessionForTest(t *testing.T, sessionDir, sessionID, workDir string) {
	t.Helper()
	manager := session.New(workDir, sessionDir)
	if err := manager.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendMessage(provider.NewUserMessage("legacy only")); err != nil {
		t.Fatal(err)
	}
	if err := session.WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewSessionDAO(nil).SetContentStore(context.Background(), tx, sessionID, "event_store")
	}); err != nil {
		t.Fatal(err)
	}
}

func doctorSettingsForTest(t *testing.T, sessionDir string) *config.Settings {
	t.Helper()
	settings := config.DefaultSettings()
	settings.SessionDir = sessionDir
	return settings
}

func TestCheckSessionContentStoreReportsAndRepairsSplitStore(t *testing.T) {
	sessionDir := t.TempDir()
	const sessionID = "doctor-split-session"
	splitSessionForTest(t, sessionDir, sessionID, t.TempDir())
	settings := doctorSettingsForTest(t, sessionDir)

	check := checkSessionContentStore(settings)
	if check.Status != StatusWarn {
		t.Fatalf("split-store check = %#v, want a warning", check)
	}
	if !strings.Contains(check.Detail, sessionID) || !strings.Contains(check.Detail, "not covered") {
		t.Fatalf("split-store detail = %q, want the session ID and the uncovered-row count", check.Detail)
	}
	if !strings.Contains(check.Fix, "--repair-session-content") {
		t.Fatalf("split-store fix = %q, want the repair command", check.Fix)
	}

	repairs := RepairSessionContent(context.Background(), settings, "all")
	if len(repairs) != 1 {
		t.Fatalf("repairs = %#v, want one session", repairs)
	}
	repair := repairs[0]
	if repair.Error != "" || repair.Deferred {
		t.Fatalf("repair = %#v, want a completed adoption", repair)
	}
	if repair.AdoptedEntries == 0 || repair.RetiredEntries == 0 {
		t.Fatalf("repair = %#v, want the delta adopted and the duplicate retired", repair)
	}

	// The check must describe the post-repair state: fully covered, no warnings.
	after := checkSessionContentStore(settings)
	if after.Status != StatusOK {
		t.Fatalf("split-store check after repair = %#v, want ok", after)
	}
	repairs = RepairSessionContent(context.Background(), settings, sessionID)
	if len(repairs) != 1 || repairs[0].Error != "" {
		t.Fatalf("idempotent repair = %#v, want a successful no-op", repairs)
	}
	if repairs[0].AdoptedEntries != 0 || repairs[0].RetiredEntries != 0 {
		t.Fatalf("idempotent repair = %#v, want nothing left to change", repairs[0])
	}
}

func TestRunWithOptionsRepairsBeforeChecking(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "doctor-repair"
	settings.DefaultModel = "model"
	settings.Providers = map[string]*config.ProviderConfig{
		"doctor-repair": {
			APIKey:  "test-key",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "model", Name: "Model"}},
		},
	}
	sessionDir := t.TempDir()
	settings.SessionDir = sessionDir
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	const sessionID = "doctor-run-repair"
	splitSessionForTest(t, sessionDir, sessionID, workDir)

	result := RunWithOptions(Options{CWD: workDir, Version: "test-version", RepairSessionContent: "all"})
	if len(result.Repairs) != 1 || result.Repairs[0].SessionID != sessionID || result.Repairs[0].Error != "" {
		t.Fatalf("run repairs = %#v, want the split session adopted", result.Repairs)
	}
	check := checkByID(t, result, "sessions.content_store")
	if check.Status != StatusOK {
		t.Fatalf("content-store check after --repair = %#v, want ok", check)
	}
}
