package main

import (
	"testing"

	"github.com/spf13/pflag"
)

func TestRootFlagsParseRouterActivation(t *testing.T) {
	flags := &cliFlags{}
	fs := pflag.NewFlagSet("root", pflag.ContinueOnError)
	registerRootFlags(fs, flags)
	if err := fs.Parse([]string{"--router", "--acp-agent", "codex"}); err != nil {
		t.Fatalf("parse root flags: %v", err)
	}
	opts := flags.runOptions()
	if !opts.router {
		t.Fatal("--router did not map into run options")
	}
	if opts.acpAgent != "codex" {
		t.Fatalf("--acp-agent = %q, want codex", opts.acpAgent)
	}
}

func TestRouterActivationProfileValidation(t *testing.T) {
	cases := []struct {
		name    string
		opts    runOptions
		want    string
		wantErr bool
	}{
		{name: "not requested", opts: runOptions{}, want: ""},
		{name: "agent alone is not enough", opts: runOptions{acpAgent: "codex"}, wantErr: true},
		{name: "router flag with agent", opts: runOptions{router: true, acpAgent: "codex"}, want: "codex"},
		{name: "router requires agent", opts: runOptions{router: true}, wantErr: true},
		{name: "router mode requires agent", opts: runOptions{mode: "router"}, wantErr: true},
		{name: "mode conflict", opts: runOptions{router: true, acpAgent: "codex", mode: "yolo"}, wantErr: true},
		{name: "explicit router mode", opts: runOptions{acpAgent: "codex", mode: "router"}, want: "codex"},
		{name: "multiple workers", opts: runOptions{router: true, acpAgent: "codex, reviewer,codex"}, want: "codex, reviewer,codex"},
		{name: "empty allowlist", opts: runOptions{router: true, acpAgent: " , "}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := routerActivationProfile(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("routerActivationProfile(%+v) accepted an invalid combination", tc.opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("routerActivationProfile(%+v): %v", tc.opts, err)
			}
			if got != tc.want {
				t.Fatalf("profile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRouterProfileIDsPreservesDefaultAndDeduplicates(t *testing.T) {
	got := parseRouterProfileIDs(" codex, reviewer ,codex, ")
	if len(got) != 2 || got[0] != "codex" || got[1] != "reviewer" {
		t.Fatalf("profiles = %v", got)
	}
}
