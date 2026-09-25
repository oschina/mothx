package agent

import (
	"context"
	"testing"
)

func TestParseWorktreeRequest(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  bool
		spec  WorktreeSpec
	}{
		{name: "absent", value: nil, want: false},
		{name: "true bool", value: true, want: true},
		{name: "false bool", value: false, want: false},
		{name: "empty", value: "", want: false},
		{name: "false string", value: "false", want: false},
		{name: "none", value: "none", want: false},
		{name: "off", value: "off", want: false},
		{name: "true string", value: "true", want: true},
		{name: "named", value: "exp-a", want: true, spec: WorktreeSpec{Name: "exp-a"}},
		{name: "trimmed named", value: "  exp-b  ", want: true, spec: WorktreeSpec{Name: "exp-b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseWorktreeRequest(tc.value)
			if !tc.want {
				if got != nil {
					t.Fatalf("parseWorktreeRequest(%v) = %+v, want nil", tc.value, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseWorktreeRequest(%v) = nil, want a request", tc.value)
			}
			if got.Name != tc.spec.Name {
				t.Fatalf("name = %q, want %q", got.Name, tc.spec.Name)
			}
		})
	}
}

func TestWorkDirContextRoundTrip(t *testing.T) {
	if _, ok := WorkDirFromContext(context.Background()); ok {
		t.Fatal("empty context must not report a work directory")
	}
	ctx := ContextWithWorkDir(context.Background(), "/repo/wt")
	dir, ok := WorkDirFromContext(ctx)
	if !ok || dir != "/repo/wt" {
		t.Fatalf("WorkDirFromContext = %q, %v", dir, ok)
	}
	if _, ok := WorkDirFromContext(ContextWithWorkDir(context.Background(), "   ")); ok {
		t.Fatal("blank work directory must not be reported")
	}
}
