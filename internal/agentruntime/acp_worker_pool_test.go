package agentruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

func TestACPWorkerBridgePoolReusesOneProcessForConcurrentSessions(t *testing.T) {
	startFile := filepath.Join(t.TempDir(), "starts")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_START_FILE", startFile)
	workDir := t.TempDir()
	pool := newACPConnectionPool()
	defer pool.Close()

	profile := config.ACPAgentProfile{
		ID: "pool-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_START_FILE"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	run := func() (ACPWorkerResult, []string) {
		var mu sync.Mutex
		var sessionIDs []string
		result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
			Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{}, pool: pool,
			Contract: SubagentTaskContract{
				Objective: "inspect this task", Role: SubagentRoleInvestigate,
				ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
				TargetProfile:        "pool-helper",
			},
			OnNotification: func(message acpclient.Message) {
				notification, ok := acpclient.DecodeSessionNotification(message)
				if !ok {
					return
				}
				mu.Lock()
				sessionIDs = append(sessionIDs, notification.SessionID)
				mu.Unlock()
			},
		})
		if err != nil {
			t.Errorf("bridge Run: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return result, append([]string(nil), sessionIDs...)
	}

	type outcome struct {
		result     ACPWorkerResult
		sessionIDs []string
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			result, ids := run()
			results <- outcome{result: result, sessionIDs: ids}
		}()
	}

	seenSessions := map[string]bool{}
	for range 2 {
		select {
		case got := <-results:
			if got.result.SessionID == "" || got.result.Output != "output:"+got.result.SessionID {
				t.Fatalf("result = %#v", got.result)
			}
			if len(got.sessionIDs) != 1 || got.sessionIDs[0] != got.result.SessionID {
				t.Fatalf("routed session updates = %#v, want %q", got.sessionIDs, got.result.SessionID)
			}
			seenSessions[got.result.SessionID] = true
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for pooled ACP runs")
		}
	}
	if len(seenSessions) != 2 {
		t.Fatalf("remote sessions = %#v, want two distinct IDs", seenSessions)
	}
	starts, err := os.ReadFile(startFile)
	if err != nil {
		t.Fatalf("read process start marker: %v", err)
	}
	if strings.Count(string(starts), "x") != 1 {
		t.Fatalf("ACP process starts = %q, want one reused process", starts)
	}
}

func TestACPWorkerPoolRetainsUpdatesSentBeforeSessionRegistration(t *testing.T) {
	startFile := filepath.Join(t.TempDir(), "starts")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_START_FILE", startFile)
	t.Setenv("MOTHX_ACP_POOL_EARLY_UPDATE", "1")
	workDir := t.TempDir()
	pool := newACPConnectionPool()
	defer pool.Close()

	profile := config.ACPAgentProfile{
		ID: "pool-early-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_START_FILE", "MOTHX_ACP_POOL_EARLY_UPDATE"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	var notifications []acpclient.Message
	result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{}, pool: pool,
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "pool-early-helper",
		},
		OnNotification: func(message acpclient.Message) {
			notifications = append(notifications, message)
		},
	})
	if err != nil {
		t.Fatalf("bridge Run: %v", err)
	}
	wantPrefix := "early:" + result.SessionID + "\n"
	if !strings.HasPrefix(result.Output, wantPrefix) || !strings.Contains(result.Output, "output:"+result.SessionID) {
		t.Fatalf("result output = %q, want early and prompt updates", result.Output)
	}
	if len(notifications) != 2 {
		t.Fatalf("notifications = %d, want early update plus prompt update", len(notifications))
	}
}

// TestACPWorkerBridgePassesSessionAdditionalDirectories proves the remote ACP
// session sees the same authorized session roots the Runtime filesystem bridge
// resolves against. Without this the Agent would be told about a narrower
// workspace than the read-only bridge actually permits.
func TestACPWorkerBridgePassesSessionAdditionalDirectories(t *testing.T) {
	dirsFile := filepath.Join(t.TempDir(), "session-dirs.jsonl")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_DIRS_FILE", dirsFile)
	workDir := t.TempDir()
	extraDir := t.TempDir()

	profile := config.ACPAgentProfile{
		ID: "pool-dirs-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_DIRS_FILE"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	result, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{},
		AdditionalDirectories: []string{extraDir},
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "pool-dirs-helper",
		},
	})
	if err != nil {
		t.Fatalf("bridge Run: %v", err)
	}
	if result.SessionID == "" {
		t.Fatalf("result = %#v, want a remote session", result)
	}
	raw, err := os.ReadFile(dirsFile)
	if err != nil {
		t.Fatalf("read session/new directories: %v", err)
	}
	var received []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &received); err != nil {
		t.Fatalf("decode session/new directories %q: %v", raw, err)
	}
	if len(received) != 1 || received[0] != extraDir {
		t.Fatalf("session/new additionalDirectories = %#v, want %q", received, extraDir)
	}
}

func TestACPWorkerBridgeOmitsAdditionalDirectoriesWithoutCapability(t *testing.T) {
	dirsFile := filepath.Join(t.TempDir(), "session-dirs.jsonl")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_NO_DIRS", "1")
	t.Setenv("MOTHX_ACP_POOL_DIRS_FILE", dirsFile)
	workDir := t.TempDir()
	extraDir := t.TempDir()

	profile := config.ACPAgentProfile{
		ID: "pool-no-dirs-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_NO_DIRS", "MOTHX_ACP_POOL_DIRS_FILE"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	if _, err := (ACPWorkerBridge{}).Run(context.Background(), ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{},
		AdditionalDirectories: []string{extraDir},
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "pool-no-dirs-helper",
		},
	}); err != nil {
		t.Fatalf("bridge Run: %v", err)
	}
	raw, err := os.ReadFile(dirsFile)
	if err != nil {
		t.Fatalf("read session/new directories: %v", err)
	}
	var received []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &received); err != nil {
		t.Fatalf("decode session/new directories %q: %v", raw, err)
	}
	if len(received) != 0 {
		t.Fatalf("session/new additionalDirectories = %#v, want capability omission", received)
	}
}

// TestACPWorkerSessionEnqueueNeverBlocks proves the per-session notification
// buffer cannot stall the pooled connection's read loop. The ACP client
// delivers notifications on that shared loop, so a blocking enqueue would
// delay every other session hosted by the same process.
func TestACPWorkerSessionEnqueueNeverBlocks(t *testing.T) {
	session := &acpWorkerSession{done: make(chan struct{})}
	session.cond = sync.NewCond(&session.mu)

	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		for i := 0; i < maxACPWorkerPendingNotifications*2; i++ {
			session.enqueue(acpclient.Message{Method: "session/update"})
		}
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("enqueue blocked on a full notification buffer")
	}

	session.mu.Lock()
	pending := len(session.pending)
	accepted := session.accepted
	dropped := session.dropped
	session.mu.Unlock()
	if pending > maxACPWorkerPendingNotifications {
		t.Fatalf("pending = %d, want at most %d", pending, maxACPWorkerPendingNotifications)
	}
	if accepted != uint64(maxACPWorkerPendingNotifications*2) {
		t.Fatalf("accepted = %d, want %d", accepted, maxACPWorkerPendingNotifications*2)
	}
	if dropped == 0 {
		t.Fatal("dropped = 0, want the overflow to be dropped rather than buffered")
	}

	// waitForPending must still return once every accepted notification was
	// projected or dropped: a dropped notification no longer exists, so
	// counting it as handled is what keeps the wait from deadlocking.
	session.stop()
	done := make(chan struct{})
	go func() {
		session.waitForPending()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForPending did not return after the session stopped")
	}
}

// acpPoolModelConfigOptions projects the helper's optional modern model
// config option. Tests select it through MOTHX_ACP_POOL_MODEL_STATE=config.
func acpPoolModelConfigOptions(current string) []acpclient.SessionConfigOption {
	if os.Getenv("MOTHX_ACP_POOL_MODEL_STATE") != "config" {
		return nil
	}
	return []acpclient.SessionConfigOption{{
		Type: "select", ID: "worker-model", Name: "Model", Category: "model", CurrentValue: current,
		Options: []acpclient.SessionConfigOptionChoice{
			{Value: "default-model", Name: "Default"},
			{Value: "fast-model", Name: "Fast"},
		},
	}}
}

// acpPoolLegacyModelState projects the helper's deprecated models state. Tests
// select it through MOTHX_ACP_POOL_MODEL_STATE=legacy.
func acpPoolResumeModelConfigOptions(current string) []acpclient.SessionConfigOption {
	if os.Getenv("MOTHX_ACP_POOL_RESUME_NO_MODEL") == "1" {
		return nil
	}
	return acpPoolModelConfigOptions(current)
}

func acpPoolLegacyModelState(current string) *acpclient.SessionModelState {
	if os.Getenv("MOTHX_ACP_POOL_MODEL_STATE") != "legacy" {
		return nil
	}
	return &acpclient.SessionModelState{
		CurrentModelID: current,
		AvailableModels: []acpclient.SessionModel{
			{ModelID: "default-model", Name: "Default"},
			{ModelID: "fast-model", Name: "Fast"},
		},
	}
}

func acpPoolRecordModelSelection(params acpclient.SetSessionConfigOptionParams) {
	path := os.Getenv("MOTHX_ACP_POOL_MODEL_FILE")
	if path == "" {
		return
	}
	_ = os.WriteFile(path, []byte("session/set_config_option:"+params.SessionID+":"+params.ConfigID+":"+params.Value), 0o600)
}

func TestACPWorkerPoolHelperProcess(t *testing.T) {
	if os.Getenv("MOTHX_ACP_POOL_HELPER") != "1" {
		return
	}
	if path := os.Getenv("MOTHX_ACP_POOL_START_FILE"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.WriteString("x")
			_ = file.Close()
		}
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	var writeMu sync.Mutex
	var nextSession int
	var prompts sync.WaitGroup
	respond := func(id json.RawMessage, result any) {
		encoded, _ := json.Marshal(result)
		message, _ := json.Marshal(acpclient.Message{JSONRPC: "2.0", ID: id, Result: encoded})
		writeMu.Lock()
		_, _ = writer.Write(append(message, '\n'))
		_ = writer.Flush()
		writeMu.Unlock()
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			prompts.Wait()
			return
		}
		var request acpclient.Message
		if err := json.Unmarshal(line, &request); err != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			sessionCapabilities := map[string]any{"close": map[string]any{}}
			if os.Getenv("MOTHX_ACP_POOL_NO_RESUME") != "1" {
				sessionCapabilities["resume"] = map[string]any{}
			}
			if os.Getenv("MOTHX_ACP_POOL_NO_DIRS") != "1" {
				sessionCapabilities["additionalDirectories"] = map[string]any{}
			}
			respond(request.ID, acpclient.InitializeResult{
				ProtocolVersion: acpclient.ProtocolVersion,
				AgentInfo:       acpclient.Implementation{Name: "pool-helper", Version: "1"},
				AgentCapabilities: map[string]any{
					"loadSession":         os.Getenv("MOTHX_ACP_POOL_NO_LOAD") != "1",
					"sessionCapabilities": sessionCapabilities,
				},
			})
		case "session/new":
			if path := os.Getenv("MOTHX_ACP_POOL_DIRS_FILE"); path != "" {
				var params acpclient.NewSessionParams
				if err := json.Unmarshal(request.Params, &params); err != nil {
					os.Exit(2)
				}
				encoded, _ := json.Marshal(params.AdditionalDirectories)
				_ = os.WriteFile(path, encoded, 0o600)
			}
			nextSession++
			sessionID := "pool-session-" + string(rune('0'+nextSession))
			respond(request.ID, acpclient.NewSessionResult{
				SessionID:     sessionID,
				ConfigOptions: acpPoolModelConfigOptions("default-model"),
				Models:        acpPoolLegacyModelState("default-model"),
			})
			if os.Getenv("MOTHX_ACP_POOL_EARLY_UPDATE") == "1" {
				update := map[string]any{
					"sessionId": sessionID,
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "early:" + sessionID + "\n"},
					},
				}
				encoded, _ := json.Marshal(update)
				message, _ := json.Marshal(acpclient.Message{JSONRPC: "2.0", Method: "session/update", Params: encoded})
				writeMu.Lock()
				_, _ = writer.Write(append(message, '\n'))
				_ = writer.Flush()
				writeMu.Unlock()
			}
		case "session/load":
			var params acpclient.LoadSessionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(2)
			}
			if path := os.Getenv("MOTHX_ACP_POOL_RESUME_FILE"); path != "" {
				_ = os.WriteFile(path, []byte("session/load:"+params.SessionID), 0o600)
			}
			respond(request.ID, acpclient.LoadSessionResult{SessionID: params.SessionID})
		case "session/resume":
			var params acpclient.ResumeSessionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(2)
			}
			if path := os.Getenv("MOTHX_ACP_POOL_RESUME_FILE"); path != "" {
				_ = os.WriteFile(path, []byte("session/resume:"+params.SessionID), 0o600)
			}
			respond(request.ID, acpclient.ResumeSessionResult{
				ConfigOptions: acpPoolResumeModelConfigOptions("default-model"),
				Models:        acpPoolLegacyModelState("default-model"),
			})
		case "session/set_config_option":
			var params acpclient.SetSessionConfigOptionParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(2)
			}
			acpPoolRecordModelSelection(params)
			respond(request.ID, acpclient.SetSessionConfigOptionResult{ConfigOptions: acpPoolModelConfigOptions(params.Value)})
		case "session/set_model":
			var params acpclient.SetSessionModelParams
			if err := json.Unmarshal(request.Params, &params); err != nil {
				os.Exit(2)
			}
			if path := os.Getenv("MOTHX_ACP_POOL_MODEL_FILE"); path != "" {
				_ = os.WriteFile(path, []byte("session/set_model:"+params.SessionID+":"+params.ModelID), 0o600)
			}
			respond(request.ID, map[string]any{})
		case "session/prompt":
			var params acpclient.PromptParams
			_ = json.Unmarshal(request.Params, &params)
			prompts.Add(1)
			go func() {
				defer prompts.Done()
				update := map[string]any{
					"sessionId": params.SessionID,
					"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]any{"type": "text", "text": "output:" + params.SessionID},
					},
				}
				encoded, _ := json.Marshal(update)
				message, _ := json.Marshal(acpclient.Message{JSONRPC: "2.0", Method: "session/update", Params: encoded})
				writeMu.Lock()
				_, _ = writer.Write(append(message, '\n'))
				_ = writer.Flush()
				writeMu.Unlock()
				time.Sleep(20 * time.Millisecond)
				respond(request.ID, acpclient.PromptResult{StopReason: "end_turn"})
			}()
		case "session/close":
			respond(request.ID, map[string]any{})
		default:
			respond(request.ID, map[string]any{})
		}
	}
}
