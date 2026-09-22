package serve

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/esm"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/session"
)

func TestServeHTTPProcessHealthShutdownAndOrphanRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal integration uses SIGTERM semantics")
	}
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "process-test"
	settings.DefaultModel = "process-model"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"process-test": {APIKey: "test-key", BaseURL: "http://127.0.0.1:1/v1", API: "openai-chat", Models: []config.ModelConfig{{ID: "process-model", Name: "Process Model", ContextWindow: 32768, MaxTokens: 1024}}},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	mgr := session.New(workDir, settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	run := session.SessionRun{
		ID: "serve-process-orphan", SessionID: mgr.GetHeader().ID, WorkDir: workDir,
		Source: "webui", Model: "process-model", Mode: "agent", Status: "running",
		StartedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := session.SaveSessionRun(settings.GetSessionDir(), run); err != nil {
		t.Fatal(err)
	}

	addr := reserveProcessTestAddress(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeHTTPProcessHelper$")
	cmd.Env = append(os.Environ(),
		"MOTHX_SERVE_PROCESS_HELPER=1",
		"MOTHX_SERVE_PROCESS_ADDR="+addr,
		"MOTHX_SERVE_PROCESS_WORKDIR="+workDir,
		"MOTHX_DIR="+configDir,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitServeHealth(t, "http://"+addr+"/health", &stderr)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve process exit: %v\n%s", err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("serve process did not stop\n%s", stderr.String())
	}

	recovered, err := session.GetSessionRun(settings.GetSessionDir(), run.ID)
	if err != nil || recovered == nil || recovered.Status != "failed" {
		t.Fatalf("recovered run = %#v, err=%v", recovered, err)
	}
	events, err := session.ListSessionRunEvents(settings.GetSessionDir(), run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "recovered" || events[0].Status != "failed" {
		t.Fatalf("recovery events = %#v", events)
	}
}

func TestServeHTTPProcessDoesNotReplayHistoricalESMObjective(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal integration uses SIGTERM semantics")
	}
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "process-test"
	settings.DefaultModel = "process-model"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"process-test": {APIKey: "test-key", BaseURL: "http://127.0.0.1:1/v1", API: "openai-chat", Models: []config.ModelConfig{{ID: "process-model", Name: "Process Model", ContextWindow: 32768, MaxTokens: 1024}}},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	mgr := session.New(workDir, settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	store := esm.NewStore(settings.GetSessionDir())
	if _, err := store.Create(t.Context(), mgr.GetHeader().ID, "historical objective must wait"); err != nil {
		t.Fatal(err)
	}

	addr := reserveProcessTestAddress(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeHTTPProcessHelper$")
	cmd.Env = append(os.Environ(),
		"MOTHX_SERVE_PROCESS_HELPER=1",
		"MOTHX_SERVE_PROCESS_ADDR="+addr,
		"MOTHX_SERVE_PROCESS_WORKDIR="+workDir,
		"MOTHX_DIR="+configDir,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitServeHealth(t, "http://"+addr+"/health", &stderr)
	// The old startup reconciler would create an ESM role Run almost
	// immediately. Allow the coordinator/recovery startup windows to settle
	// before stopping the process and inspecting the shared database.
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("serve process exit: %v\n%s", err, stderr.String())
	}

	obj, err := store.Get(t.Context(), mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	if obj.Status != esm.StatusActive || obj.RecoveryCount != 0 {
		t.Fatalf("historical ESM objective changed during startup: %#v", obj)
	}
	events, err := session.ListSessionRunEvents(settings.GetSessionDir(), mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("historical ESM objective produced startup run events: %#v", events)
	}
}

func TestServeHTTPProcessRecoversRemoteResponsesRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal integration uses SIGTERM semantics")
	}
	upstream := http.Server{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream.Addr = listener.Addr().String()
	upstream.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/responses/resp-process-recovery" {
			_, _ = w.Write([]byte(`{"id":"resp-process-recovery","status":"completed","output":[{"id":"msg-process-recovery","type":"message","status":"completed","content":[{"type":"output_text","text":"remote process recovered"}]}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()

	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "responses-process-test"
	settings.DefaultModel = "responses-process-model"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	background := true
	supports := true
	settings.Providers = map[string]*config.ProviderConfig{
		"responses-process-test": {
			APIKey: "test-key", BaseURL: "http://" + upstream.Addr + "/v1", API: "openai-responses",
			Responses: config.ResponsesConfig{Background: &background},
			Models:    []config.ModelConfig{{ID: "responses-process-model", Name: "Responses Process Model", ContextWindow: 32768, MaxTokens: 1024, Compat: &config.ModelCompat{SupportsResponses: &supports, SupportsBackground: &supports}}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	mgr := session.New(workDir, settings.GetSessionDir())
	if err := mgr.Init(); err != nil {
		t.Fatal(err)
	}
	localRunID := "serve-process-remote-recovery"
	now := time.Now()
	if err := session.SaveSessionRun(settings.GetSessionDir(), session.SessionRun{ID: localRunID, SessionID: mgr.GetHeader().ID, WorkDir: workDir, Source: "responses_background", Model: "responses-process-model", Mode: "agent", Status: "running", StartedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := session.SaveResponseRun(settings.GetSessionDir(), session.ResponseRun{SessionID: mgr.GetHeader().ID, LocalRunID: "remote-process-run", LocalTurnID: localRunID, ResponseID: "resp-process-recovery", Provider: "responses-process-test", API: "openai-responses", State: "queued", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	addr := reserveProcessTestAddress(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestServeHTTPProcessHelper$")
	cmd.Env = append(os.Environ(), "MOTHX_SERVE_PROCESS_HELPER=1", "MOTHX_SERVE_PROCESS_ADDR="+addr, "MOTHX_SERVE_PROCESS_WORKDIR="+workDir, "MOTHX_SERVE_PROCESS_PROVIDER=responses-process-test", "MOTHX_SERVE_PROCESS_MODEL=responses-process-model", "MOTHX_DIR="+configDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitServeHealth(t, "http://"+addr+"/health", &stderr)
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := session.GetSessionRun(settings.GetSessionDir(), localRunID)
		if err != nil {
			_ = cmd.Process.Kill()
			t.Fatal(err)
		}
		if run != nil && run.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("remote Responses run did not complete: %#v\n%s", run, stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("serve process exit: %v\n%s", err, stderr.String())
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	messages := mgr.GetMessages()
	if len(messages) == 0 || processMessageText(messages[len(messages)-1]) != "remote process recovered" {
		t.Fatalf("recovered messages = %#v", messages)
	}
}
func TestServeHTTPProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_SERVE_PROCESS_HELPER") != "1" {
		return
	}
	providerName := os.Getenv("MOTHX_SERVE_PROCESS_PROVIDER")
	if providerName == "" {
		providerName = "process-test"
	}
	modelID := os.Getenv("MOTHX_SERVE_PROCESS_MODEL")
	if modelID == "" {
		modelID = "process-model"
	}
	if err := Run(RunOptions{
		Port: os.Getenv("MOTHX_SERVE_PROCESS_ADDR"), WorkDir: os.Getenv("MOTHX_SERVE_PROCESS_WORKDIR"),
		Provider: providerName, Model: modelID, Unsafe: true,
	}, "process-test"); err != nil {
		t.Fatal(err)
	}
}

func processMessageText(message provider.Message) string {
	if message.Content != "" {
		return message.Content
	}
	var text strings.Builder
	for _, block := range message.Contents {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func reserveProcessTestAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func waitServeHealth(t *testing.T, url string, stderr *bytes.Buffer) {
	t.Helper()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			var body map[string]any
			decodeErr := json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			if decodeErr == nil && resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("serve health endpoint did not become ready: %s\n%s", url, stderr.String())
}
