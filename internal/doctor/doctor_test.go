package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
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
