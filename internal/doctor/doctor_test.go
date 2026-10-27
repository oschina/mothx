package doctor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
	database "github.com/oschina/mothx/internal/db"
	"github.com/oschina/mothx/internal/session"
)

func TestRunReportsMissingProviderKeyWithoutLeakingConfiguredValue(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "doctor-test"
	settings.DefaultModel = "model"
	settings.Providers = map[string]*config.ProviderConfig{
		"doctor-test": {
			APIKey:  "${DOCTOR_TEST_API_KEY}",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "model", Name: "Model"}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	result := Run(workDir, "test-version")
	if result.OK {
		t.Fatalf("result = %#v, want provider error", result)
	}
	providerCheck := checkByID(t, result, "provider.default")
	if providerCheck.Status != StatusError || providerCheck.Detail != "doctor-test: missing API key" {
		t.Fatalf("provider check = %#v", providerCheck)
	}
	if providerCheck.Fix != "Set doctor-test.apiKey or DOCTOR_TEST_API_KEY" {
		t.Fatalf("provider fix = %q", providerCheck.Fix)
	}
}

func TestRunUsesProjectSettingsForRequestedCWD(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	projectDir := filepath.Join(workDir, config.ProjectDirName)
	if err := os.MkdirAll(projectDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.json"), []byte(`{
  "defaultProvider": "project-provider",
  "defaultModel": "model",
  "providers": {
    "project-provider": {
      "apiKey": "project-key",
      "baseUrl": "http://127.0.0.1:1/v1",
      "api": "openai-chat",
      "models": [{"id": "model"}]
    }
  }
}`), 0600); err != nil {
		t.Fatal(err)
	}
	result := Run(workDir, "test-version")
	if got := checkByID(t, result, "provider.default").Detail; got != "project-provider" {
		t.Fatalf("provider detail = %q, want project-provider", got)
	}
}

func TestCheckSessionIntegritySkipsMissingAndValidatesExistingDatabase(t *testing.T) {
	settings := config.DefaultSettings()
	settings.SessionDir = t.TempDir()
	missing := checkSessionIntegrity(settings)
	if missing.ID != "sessions.integrity" || missing.Status != StatusSkip {
		t.Fatalf("missing integrity check = %#v, want skip", missing)
	}

	path := session.RootDatabasePath(settings.SessionDir)
	if _, err := database.Open(path, func(sqlDB *sql.DB) error {
		_, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS doctor_probe (id TEXT PRIMARY KEY)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(path); err != nil {
		t.Fatal(err)
	}
	healthy := checkSessionIntegrity(settings)
	if healthy.Status != StatusOK {
		t.Fatalf("healthy integrity check = %#v, want ok", healthy)
	}

	if err := os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := checkSessionIntegrity(settings)
	if broken.Status != StatusError || broken.Fix == "" || !strings.Contains(broken.Fix, "restore") {
		t.Fatalf("broken integrity check = %#v, want backup/restore guidance", broken)
	}
}

func TestSessionIntegrityFixMatchesRepairCapability(t *testing.T) {
	stale := &database.IntegrityError{Path: "sessions.db", Report: "wrong # of entries in index idx_entries_session_type"}
	t.Setenv("MOTHX_SQLITE_QUICK_CHECK", "")
	if fix := sessionIntegrityFix(stale); !strings.Contains(fix, "restart") || strings.Contains(fix, "unset") {
		t.Fatalf("enabled startup fix = %q, want restart guidance", fix)
	}
	t.Setenv("MOTHX_SQLITE_QUICK_CHECK", "off")
	if fix := sessionIntegrityFix(stale); !strings.Contains(fix, "unset MOTHX_SQLITE_QUICK_CHECK") || !strings.Contains(fix, "restart") {
		t.Fatalf("disabled startup fix = %q, want unset-and-restart guidance", fix)
	}

	damage := &database.IntegrityError{Path: "sessions.db", Report: "database disk image is malformed"}
	if fix := sessionIntegrityFix(damage); !strings.Contains(fix, "restore") || strings.Contains(fix, "repair stale indexes") {
		t.Fatalf("table damage fix = %q, want backup/restore guidance", fix)
	}
}

func TestValidateProviderReportsMissingModelWhenNoModelCanBeSelected(t *testing.T) {
	settings := config.DefaultSettings()
	settings.DefaultProvider = "doctor-empty-models"
	settings.DefaultModel = ""
	settings.Providers = map[string]*config.ProviderConfig{
		"doctor-empty-models": {
			APIKey:  "configured-key",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
		},
	}

	checks := ValidateProvider(settings, settings.DefaultProvider, settings.DefaultModel)
	if len(checks) != 2 || checks[0].ID != "provider.default" || checks[0].Status != StatusOK || checks[1].ID != "model.default" || checks[1].Status != StatusError {
		t.Fatalf("checks = %#v, want missing model error", checks)
	}
}

func TestRunNeverSerializesAPIKey(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	const apiKey = "doctor-test-secret-value"
	settings := config.DefaultSettings()
	settings.DefaultProvider = "doctor-secret"
	settings.DefaultModel = "model"
	settings.Providers = map[string]*config.ProviderConfig{
		"doctor-secret": {
			APIKey:  apiKey,
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "model", Name: "Model"}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(Run(workDir, "test-version"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), apiKey) {
		t.Fatalf("doctor response exposes API key: %s", data)
	}
}

func checkByID(t *testing.T, result Response, id string) Check {
	t.Helper()
	for _, check := range result.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("missing check %q in %#v", id, result.Checks)
	return Check{}
}
