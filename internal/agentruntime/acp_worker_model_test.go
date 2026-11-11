package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

// acpModelTestInvocation returns one helper-process invocation whose profile
// uses the shared subprocess ACP fixture. Tests select the advertised model
// state and observe the resulting selection through the returned file.
func acpModelTestInvocation(t *testing.T, model string, pool *acpConnectionPool) (ACPWorkerInvocation, string) {
	t.Helper()
	workDir := t.TempDir()
	modelFile := filepath.Join(t.TempDir(), "model-selection")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_MODEL_FILE", modelFile)
	envNames := []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_MODEL_FILE"}
	for _, name := range []string{"MOTHX_ACP_POOL_MODEL_STATE", "MOTHX_ACP_POOL_RESUME_NO_MODEL"} {
		if os.Getenv(name) != "" {
			envNames = append(envNames, name)
		}
	}
	profile := config.ACPAgentProfile{
		ID: "model-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled:          true,
		Environment:      envNames,
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
		Model:            model,
	}
	invocation := ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{}, pool: pool,
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        profile.ID,
		},
	}
	return invocation, modelFile
}

func TestACPWorkerProfileModelSelectsModernConfigOption(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_MODEL_STATE", "config")
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, modelFile := acpModelTestInvocation(t, "fast-model", pool)
	worker, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}
	defer worker.Close(context.Background())

	raw, err := os.ReadFile(modelFile)
	if err != nil {
		t.Fatalf("read model selection: %v", err)
	}
	want := "session/set_config_option:" + worker.SessionID() + ":worker-model:fast-model"
	if string(raw) != want {
		t.Fatalf("model selection = %q, want %q", raw, want)
	}
}

func TestACPWorkerProfileModelUsesLegacyModelsFallback(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_MODEL_STATE", "legacy")
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, modelFile := acpModelTestInvocation(t, "fast-model", pool)
	worker, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}
	defer worker.Close(context.Background())

	raw, err := os.ReadFile(modelFile)
	if err != nil {
		t.Fatalf("read model selection: %v", err)
	}
	want := "session/set_model:" + worker.SessionID() + ":fast-model"
	if string(raw) != want {
		t.Fatalf("legacy model selection = %q, want %q", raw, want)
	}
}

func TestACPWorkerProfileModelEmptyKeepsWorkerDefault(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_MODEL_STATE", "config")
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, modelFile := acpModelTestInvocation(t, "", pool)
	worker, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}
	defer worker.Close(context.Background())

	if _, err := os.Stat(modelFile); !os.IsNotExist(err) {
		t.Fatalf("empty profile model still selected a model (stat error = %v)", err)
	}
}

func TestACPWorkerProfileModelAlreadyCurrentSkipsSelection(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_MODEL_STATE", "config")
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, modelFile := acpModelTestInvocation(t, "default-model", pool)
	worker, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open worker: %v", err)
	}
	defer worker.Close(context.Background())

	if _, err := os.Stat(modelFile); !os.IsNotExist(err) {
		t.Fatalf("an already-current model was selected again (stat error = %v)", err)
	}
}

func TestACPWorkerProfileModelRejectsUnavailableChoice(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_MODEL_STATE", "config")
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, modelFile := acpModelTestInvocation(t, "missing-model", pool)
	_, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err == nil || !strings.Contains(err.Error(), "not offered by the worker") {
		t.Fatalf("unsupported model error = %v, want an offered-model diagnostic", err)
	}
	if !strings.Contains(err.Error(), "fast-model") {
		t.Fatalf("error does not list available models: %v", err)
	}
	if _, statErr := os.Stat(modelFile); !os.IsNotExist(statErr) {
		t.Fatalf("unsupported model still produced a selection call")
	}
}

func TestACPWorkerProfileModelRequiresAdvertisedOption(t *testing.T) {
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, _ := acpModelTestInvocation(t, "fast-model", pool)
	_, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err == nil || !strings.Contains(err.Error(), "does not advertise a model config option") {
		t.Fatalf("missing model option error = %v", err)
	}
}

func TestACPWorkerProfileModelResumeKeepsAgentSelection(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_MODEL_STATE", "config")
	t.Setenv("MOTHX_ACP_POOL_RESUME_NO_MODEL", "1")
	pool := newACPConnectionPool()
	defer pool.Close()

	invocation, modelFile := acpModelTestInvocation(t, "", pool)
	first, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open first worker: %v", err)
	}
	sessionID := first.SessionID()
	if sessionID == "" {
		t.Fatal("first worker returned an empty session id")
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("close first worker: %v", err)
	}

	invocation.Profile.Model = "fast-model"
	invocation.ResumeSessionID = sessionID
	resumed, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("resume worker with configured model: %v", err)
	}
	defer resumed.Close(context.Background())
	if _, statErr := os.Stat(modelFile); !os.IsNotExist(statErr) {
		t.Fatalf("a resume without a returned catalog still attempted a selection call")
	}
}
