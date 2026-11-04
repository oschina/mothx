package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

func TestACPWorkerBridgeResumesSessionThroughLoad(t *testing.T) {
	resumeFile := filepath.Join(t.TempDir(), "resume")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_NO_RESUME", "1")
	t.Setenv("MOTHX_ACP_POOL_RESUME_FILE", resumeFile)
	workDir := t.TempDir()
	pool := newACPConnectionPool()
	defer pool.Close()

	profile := config.ACPAgentProfile{
		ID: "resume-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_RESUME_FILE", "MOTHX_ACP_POOL_NO_RESUME"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	invocation := ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{}, pool: pool,
		Contract: SubagentTaskContract{
			Objective: "inspect this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "resume-helper",
		},
	}
	first, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open first worker: %v", err)
	}
	result, err := first.Prompt(context.Background(), []acpclient.ContentBlock{{Type: "text", Text: "first"}}, nil)
	if err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	sessionID := result.SessionID
	if sessionID == "" {
		t.Fatal("first prompt returned an empty session id")
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("close first worker: %v", err)
	}

	invocation.ResumeSessionID = sessionID
	resumed, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("resume worker: %v", err)
	}
	defer resumed.Close(context.Background())
	if got := resumed.SessionID(); got != sessionID {
		t.Fatalf("resumed session id = %q, want %q", got, sessionID)
	}
	if _, err := resumed.Prompt(context.Background(), []acpclient.ContentBlock{{Type: "text", Text: "second"}}, nil); err != nil {
		t.Fatalf("resumed prompt: %v", err)
	}
	raw, err := os.ReadFile(resumeFile)
	if err != nil {
		t.Fatalf("read resume marker: %v", err)
	}
	if string(raw) != "session/load:"+sessionID {
		t.Fatalf("resume marker = %q", raw)
	}
}

func TestACPWorkerBridgePrefersSessionResumeWithoutReplay(t *testing.T) {
	resumeFile := filepath.Join(t.TempDir(), "resume")
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_RESUME_FILE", resumeFile)
	workDir := t.TempDir()
	pool := newACPConnectionPool()
	defer pool.Close()

	profile := config.ACPAgentProfile{
		ID: "resume-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_RESUME_FILE"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	invocation := ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{}, pool: pool,
		Contract: SubagentTaskContract{
			Objective: "resume this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "resume-helper",
		},
	}
	first, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("open first worker: %v", err)
	}
	result, err := first.Prompt(context.Background(), []acpclient.ContentBlock{{Type: "text", Text: "first"}}, nil)
	if err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("close first worker: %v", err)
	}

	invocation.ResumeSessionID = result.SessionID
	resumed, err := (ACPWorkerBridge{}).Open(context.Background(), invocation)
	if err != nil {
		t.Fatalf("resume worker: %v", err)
	}
	defer resumed.Close(context.Background())
	if _, err := resumed.Prompt(context.Background(), []acpclient.ContentBlock{{Type: "text", Text: "second"}}, nil); err != nil {
		t.Fatalf("resumed prompt: %v", err)
	}
	raw, err := os.ReadFile(resumeFile)
	if err != nil {
		t.Fatalf("read resume marker: %v", err)
	}
	if string(raw) != "session/resume:"+result.SessionID {
		t.Fatalf("resume marker = %q", raw)
	}
}

func TestACPWorkerBridgeRejectsResumeWithoutLoadCapability(t *testing.T) {
	t.Setenv("MOTHX_ACP_POOL_HELPER", "1")
	t.Setenv("MOTHX_ACP_POOL_NO_LOAD", "1")
	t.Setenv("MOTHX_ACP_POOL_NO_RESUME", "1")
	workDir := t.TempDir()
	pool := newACPConnectionPool()
	defer pool.Close()
	profile := config.ACPAgentProfile{
		ID: "no-load-helper", Command: []string{os.Args[0], "-test.run=TestACPWorkerPoolHelperProcess", "--"},
		Enabled: true, Environment: []string{"MOTHX_ACP_POOL_HELPER", "MOTHX_ACP_POOL_NO_LOAD", "MOTHX_ACP_POOL_NO_RESUME"},
		PermissionPolicy: config.ACPPermissionAutoAllowOnce,
	}
	_, err := (ACPWorkerBridge{}).Open(context.Background(), ACPWorkerInvocation{
		Profile: profile, WorkDir: workDir, Launcher: acpProcessLauncher{}, pool: pool,
		ResumeSessionID: "remote-session",
		Contract: SubagentTaskContract{
			Objective: "resume this task", Role: SubagentRoleInvestigate,
			ExpectedDeliverables: []SubagentDeliverable{SubagentDeliverableFindings},
			TargetProfile:        "no-load-helper",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support session/resume or session/load") {
		t.Fatalf("resume without load capability = %v", err)
	}
}
