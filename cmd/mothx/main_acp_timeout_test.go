package main

import (
	"testing"
	"time"

	"github.com/oschina/mothx/internal/acp"
)

func TestResolveACPTimeoutPrefersFlagThenEnvAndIgnoresInvalid(t *testing.T) {
	t.Setenv("MOTHX_ACP_TEST_TIMEOUT", "10m")
	if got := resolveACPTimeout("45m", "MOTHX_ACP_TEST_TIMEOUT"); got != 45*time.Minute {
		t.Fatalf("explicit flag = %v, want 45m", got)
	}
	if got := resolveACPTimeout("", "MOTHX_ACP_TEST_TIMEOUT"); got != 10*time.Minute {
		t.Fatalf("env fallback = %v, want 10m", got)
	}
	if got := resolveACPTimeout("bogus", "MOTHX_ACP_TEST_TIMEOUT"); got != 10*time.Minute {
		t.Fatalf("invalid flag = %v, want env fallback 10m", got)
	}
	t.Setenv("MOTHX_ACP_TEST_TIMEOUT", "not-a-duration")
	if got := resolveACPTimeout("", "MOTHX_ACP_TEST_TIMEOUT"); got != 0 {
		t.Fatalf("invalid env = %v, want zero so ACP falls back to its default", got)
	}
	if got := resolveACPTimeout("-5s", "MOTHX_ACP_TEST_TIMEOUT_UNSET"); got != 0 {
		t.Fatalf("negative flag = %v, want zero", got)
	}
	if got := resolveACPTimeout("0s", "MOTHX_ACP_TEST_TIMEOUT_UNSET"); got != 0 {
		t.Fatalf("zero flag = %v, want zero", got)
	}
	if got := resolveACPTimeout("", "MOTHX_ACP_TEST_TIMEOUT_UNSET"); got != 0 {
		t.Fatalf("unset sources = %v, want zero", got)
	}
}

func TestACPCommandInjectsTimeoutFlagsIntoRunOptions(t *testing.T) {
	var gotOpts acp.RunOptions
	captured := false
	cmd := newRootCommand(
		func(args []string, opts runOptions) error {
			t.Fatal("unexpected CLI execution")
			return nil
		},
		func(opts acp.RunOptions) error {
			gotOpts = opts
			captured = true
			return nil
		},
	)
	cmd.SetArgs([]string{"acp", "--permission-timeout", "30m", "--question-timeout", "45m"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute acp command: %v", err)
	}
	if !captured {
		t.Fatal("acp run function was not invoked")
	}
	if gotOpts.PermissionTimeout != 30*time.Minute {
		t.Fatalf("PermissionTimeout = %v, want 30m from the flag", gotOpts.PermissionTimeout)
	}
	if gotOpts.QuestionTimeout != 45*time.Minute {
		t.Fatalf("QuestionTimeout = %v, want 45m from the flag", gotOpts.QuestionTimeout)
	}
}

func TestACPCommandInjectsTimeoutEnvIntoRunOptions(t *testing.T) {
	t.Setenv("MOTHX_ACP_PERMISSION_TIMEOUT", "15m")
	t.Setenv("MOTHX_ACP_QUESTION_TIMEOUT", "invalid")
	var gotOpts acp.RunOptions
	cmd := newRootCommand(
		func(args []string, opts runOptions) error {
			t.Fatal("unexpected CLI execution")
			return nil
		},
		func(opts acp.RunOptions) error {
			gotOpts = opts
			return nil
		},
	)
	cmd.SetArgs([]string{"acp"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute acp command: %v", err)
	}
	if gotOpts.PermissionTimeout != 15*time.Minute {
		t.Fatalf("PermissionTimeout = %v, want 15m from MOTHX_ACP_PERMISSION_TIMEOUT", gotOpts.PermissionTimeout)
	}
	if gotOpts.QuestionTimeout != 0 {
		t.Fatalf("QuestionTimeout = %v, want zero fallback for an invalid env value", gotOpts.QuestionTimeout)
	}
}
