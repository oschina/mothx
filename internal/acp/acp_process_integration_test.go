package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

func TestACPStdioProcessInitializeLoadResumeClose(t *testing.T) {
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
	if err := session.CreateSessionRun(settings.GetSessionDir(), session.SessionRun{
		ID: "process-run", SessionID: mgr.GetHeader().ID, WorkDir: workDir,
		Source: "acp", Model: "process-model", Mode: "agent", Status: "running", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	decisionRequest := agentruntime.DecisionRequest{ID: "process-question", SessionID: mgr.GetHeader().ID, RunID: "process-run", Kind: agentruntime.DecisionQuestion}
	decisionRecord, err := agentruntime.NewDecisionRequestRecordWithDeadline(decisionRequest, questionRequest{SessionID: mgr.GetHeader().ID, Question: "continue?"}, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	decisionData, err := json.Marshal(map[string]any{"decision": decisionRecord})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.SaveSessionRunEvent(settings.GetSessionDir(), session.SessionRunEvent{SessionID: mgr.GetHeader().ID, RunID: "process-run", EventType: "decision_pending", Source: "acp", Status: "pending", Data: decisionData}); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestACPStdioProcessHelper$")
	cmd.Env = append(os.Environ(), "MOTHX_ACP_PROCESS_HELPER=1", "MOTHX_DIR="+configDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	assertACPResponseID(t, reader, 1)
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/load", "params": map[string]any{"sessionId": mgr.GetHeader().ID, "cwd": workDir}})
	assertACPResponseID(t, reader, 2)
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session/resume", "params": map[string]any{"sessionId": mgr.GetHeader().ID, "cwd": workDir}})
	assertACPResponseID(t, reader, 3)
	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "session/close", "params": map[string]any{"sessionId": mgr.GetHeader().ID}})
	assertACPResponseID(t, reader, 4)
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("ACP process did not exit after stdin EOF")
	}
	events, err := session.ListSessionRunEvents(settings.GetSessionDir(), mgr.GetHeader().ID)
	if err != nil {
		t.Fatal(err)
	}
	latestDecisionStatus := ""
	for _, event := range events {
		var envelope struct {
			Decision agentruntime.DecisionRecord `json:"decision"`
		}
		if json.Unmarshal(event.Data, &envelope) == nil && envelope.Decision.ID == decisionRequest.ID {
			latestDecisionStatus = envelope.Decision.Status
		}
	}
	if latestDecisionStatus != "cancelled" {
		t.Fatalf("restored offline decision status = %q, want cancelled", latestDecisionStatus)
	}
	run, err := session.GetSessionRun(settings.GetSessionDir(), "process-run")
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != "failed" {
		t.Fatalf("restored local run = %#v, want failed orphan recovery", run)
	}
}

func TestACPStdioProcessInitializeDoctorWithoutSession(t *testing.T) {
	configDir := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "doctor-process"
	settings.DefaultModel = "doctor-model"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"doctor-process": {
			APIKey:  "test-key",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "doctor-model", Name: "Doctor Model"}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestACPStdioProcessHelper$")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "MOTHX_ACP_PROCESS_HELPER=1", "MOTHX_DIR="+configDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1}})
	initialize := assertACPResponseID(t, reader, 1)["result"].(map[string]any)
	agentInfo := initialize["agentInfo"].(map[string]any)
	if agentInfo["name"] != "mothx" || agentInfo["title"] != "MothX" {
		t.Fatalf("agentInfo = %#v, want MothX identity", agentInfo)
	}
	version, _ := agentInfo["version"].(string)
	if version == "" {
		t.Fatalf("agentInfo = %#v, want version", agentInfo)
	}
	meta := initialize["_meta"].(map[string]any)[mothxExtensionNamespace].(map[string]any)
	if meta["doctor"] != true {
		t.Fatalf("initialize metadata = %#v, want doctor=true", meta)
	}

	sendACPRequest(t, stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "mothx/doctor", "params": map[string]any{}})
	doctorResult := assertACPResponseID(t, reader, 2)["result"].(map[string]any)
	if doctorResult["version"] != version {
		t.Fatalf("doctor version = %#v, want %q", doctorResult["version"], version)
	}
	if _, ok := doctorResult["checks"].([]any); !ok {
		t.Fatalf("doctor result = %#v, want checks", doctorResult)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("ACP stderr = %q, want empty", stderr.String())
	}
}

func TestACPStartupFailureEmitsStructuredErrorLine(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "doctor-missing-key"
	settings.DefaultModel = "doctor-model"
	settings.Providers = map[string]*config.ProviderConfig{
		"doctor-missing-key": {
			APIKey:  "${DOCTOR_MISSING_KEY}",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models:  []config.ModelConfig{{ID: "doctor-model", Name: "Doctor Model"}},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestACPStartupFailureProcessHelper$")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "MOTHX_ACP_STARTUP_FAILURE_HELPER=1", "MOTHX_DIR="+configDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("ACP startup stdout = %q, want empty", stdout.String())
	}
	line := strings.TrimSpace(stderr.String())
	if !strings.HasPrefix(line, "MOTHX_ACP_ERROR ") || strings.Contains(line, "\n") {
		t.Fatalf("ACP startup stderr = %q, want one structured line", line)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Fix     string `json:"fix"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "MOTHX_ACP_ERROR ")), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != "provider_unusable" || !strings.Contains(payload.Message, "no API key") || payload.Fix == "" {
		t.Fatalf("startup payload = %#v", payload)
	}
	if strings.Contains(line, "${DOCTOR_MISSING_KEY}") {
		t.Fatalf("startup payload exposes configured key reference: %q", line)
	}
}

func TestACPStdioProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_ACP_PROCESS_HELPER") != "1" {
		return
	}
	// Phase 1 wire tests drive the same helper with additive scenario options;
	// the defaults keep every pre-existing process test unchanged.
	opts := RunOptions{}
	if os.Getenv("MOTHX_ACP_HELPER_MULTI_AGENT") == "1" {
		opts.MultiAgent = true
		opts.Delegate = true
	}
	if value := os.Getenv("MOTHX_ACP_HELPER_QUESTION_TIMEOUT"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			opts.QuestionTimeout = parsed
		}
	}
	if value := os.Getenv("MOTHX_ACP_HELPER_PERMISSION_TIMEOUT"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			opts.PermissionTimeout = parsed
		}
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
}

func TestACPStartupFailureProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_ACP_STARTUP_FAILURE_HELPER") != "1" {
		return
	}
	// Run writes the structured startup line before returning. Returning from
	// the test would add Go's PASS output to stdout, which is not part of the
	// real ACP executable's contract.
	_ = Run(RunOptions{})
	os.Exit(0)
}

func sendACPRequest(t *testing.T, stdin interface{ Write([]byte) (int, error) }, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if _, err := stdin.Write(data); err != nil {
		t.Fatal(err)
	}
}

func assertACPResponseID(t *testing.T, reader *bufio.Reader, id float64) map[string]any {
	t.Helper()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var message map[string]any
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatal(err)
		}
		if got, ok := message["id"].(float64); ok && got == id {
			if rpcErr := message["error"]; rpcErr != nil {
				t.Fatalf("ACP response %v error: %#v", id, rpcErr)
			}
			return message
		}
	}
}
