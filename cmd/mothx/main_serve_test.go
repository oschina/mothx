package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/startvibecoding/mothx/internal/serve"
)

func TestServeFlagsIncludeExtendedExecutionOptions(t *testing.T) {
	flags := &cliFlags{}
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	registerServeFlags(fs, flags)

	if err := fs.Parse([]string{"--web-search", "--browser", "--artifact", "--enable-a2a-master", "--unsafe"}); err != nil {
		t.Fatalf("parse serve flags: %v", err)
	}
	opts := flags.serveOptions()
	if !opts.WebSearch {
		t.Fatal("expected web-search serve option")
	}
	if !opts.Browser {
		t.Fatal("expected browser serve option")
	}
	if !opts.Artifact {
		t.Fatal("expected artifact serve option")
	}
	if !opts.A2AMaster {
		t.Fatal("expected A2A master serve option")
	}
	if !opts.Unsafe {
		t.Fatal("expected unsafe serve option")
	}
}

// TestServeInitConfigCommandWarnsAboutPlaceholderToken guards the operator
// warning on the config template: the generated token is a public value, so
// creating the file must say so loudly.
func TestServeInitConfigCommandWarnsAboutPlaceholderToken(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MOTHX_CONFIG_DIR", "")
	t.Setenv("MOTHX_DIR", "")

	command := newServeInitConfigCommand()
	command.SetArgs([]string{"global"})
	var stderr bytes.Buffer
	command.SetErr(&stderr)
	command.SetOut(&stderr)
	if err := command.Execute(); err != nil {
		t.Fatalf("execute init-config: %v", err)
	}
	output := stderr.String()
	if !strings.Contains(output, "Created serve config") {
		t.Fatalf("output = %q, want the created config path", output)
	}
	if !strings.Contains(output, serve.PlaceholderAuthTokenWarning) {
		t.Fatalf("output = %q, want the placeholder token warning", output)
	}
}
