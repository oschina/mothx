package acp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oschina/mothx/internal/config"
)

func parseLastMessage(t *testing.T, output *syncedBuffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(output.String())
	if line == "" {
		t.Fatalf("expected a response, got none")
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatalf("parse response: %v (%q)", err, line)
	}
	return message
}

func manageEnvFixture(t *testing.T) (*server, *syncedBuffer, string) {
	t.Helper()
	cwd := t.TempDir()
	dir := t.TempDir()
	t.Setenv("MOTHX_DIR", dir)
	output := &syncedBuffer{}
	srv := newManageFixtureServer(output, cwd)
	return srv, output, dir
}

func TestManageEnvGetNoValues(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	if err := os.MkdirAll(config.ConfigDir(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ConfigDir(), "env.json")
	if err := os.WriteFile(path, []byte(`{"vars":{"SECRET_KEY":"shhh","PLAIN":"ok"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/get", map[string]any{}))
	vars, ok := result["variables"].([]any)
	if !ok || len(vars) != 2 {
		t.Fatalf("expected 2 variables, got %#v", result)
	}
	for _, v := range vars {
		m := v.(map[string]any)
		if m["valueConfigured"] != true {
			t.Fatalf("expected valueConfigured=true, got %#v", m)
		}
		if _, ok := m["value"]; ok {
			t.Fatalf("response must not contain value, got %#v", m)
		}
	}
	if result["vars"] != nil {
		t.Fatalf("response must not contain vars map, got %#v", result)
	}
}

func TestManageEnvPatchSetReplaceUnset(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	if err := os.MkdirAll(config.ConfigDir(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ConfigDir(), "env.json")
	if err := os.WriteFile(path, []byte(`{"vars":{"KEEP":"old","REMOVE":"gone"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/patch", map[string]any{
		"set": []map[string]string{
			{"name": "KEEP", "value": "new"},
			{"name": "ADD", "value": "fresh"},
		},
		"unset": []string{"REMOVE"},
	}))

	vars := result["variables"].([]any)
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		m := v.(map[string]any)
		names = append(names, m["name"].(string))
	}
	slices.Sort(names)
	if len(names) != 2 || names[0] != "ADD" || names[1] != "KEEP" {
		t.Fatalf("unexpected variables: %#v", result)
	}

	cfg := config.LoadEnv()
	if cfg.Vars["KEEP"] != "new" || cfg.Vars["ADD"] != "fresh" {
		t.Fatalf("unexpected vars: %#v", cfg.Vars)
	}
	if _, ok := cfg.Vars["REMOVE"]; ok {
		t.Fatalf("REMOVE should be deleted")
	}
}

func TestManageEnvPatchEmptyValue(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/patch", map[string]any{
		"set": []map[string]string{{"name": "EMPTY", "value": ""}},
	}))

	vars := result["variables"].([]any)
	if len(vars) != 1 || vars[0].(map[string]any)["name"] != "EMPTY" {
		t.Fatalf("unexpected response: %#v", result)
	}
	cfg := config.LoadEnv()
	if v, ok := cfg.Vars["EMPTY"]; !ok || v != "" {
		t.Fatalf("expected empty string value to be preserved, got %#v", cfg.Vars)
	}
}

func TestManageEnvPatchInvalidNames(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	for _, name := range []string{"", "BAD=NAME", "BAD\x00NAME", "BAD\rNAME", "BAD\nNAME"} {
		_, data := manageFixtureError(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/patch", map[string]any{
			"set": []map[string]string{{"name": name, "value": "x"}},
		}))
		if data["code"] != "env_name_invalid" {
			t.Fatalf("expected env_name_invalid for %q, got %#v", name, data)
		}
	}
}

func TestManageEnvPatchDuplicateOrConflict(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	_, data := manageFixtureError(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/patch", map[string]any{
		"set": []map[string]string{{"name": "A", "value": "1"}, {"name": "A", "value": "2"}},
	}))
	if data["code"] != "env_name_duplicate" {
		t.Fatalf("expected duplicate error, got %#v", data)
	}

	_, data = manageFixtureError(t, callManageFixture(t, srv, output, 2, "mothx/manage/env/patch", map[string]any{
		"set":   []map[string]string{{"name": "A", "value": "1"}},
		"unset": []string{"A"},
	}))
	if data["code"] != "env_name_conflict" {
		t.Fatalf("expected conflict error, got %#v", data)
	}
}

func TestManageEnvPatchUnknownField(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	_, data := manageFixtureError(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/patch", map[string]any{
		"set":  []map[string]string{{"name": "A", "value": "1"}},
		"evil": true,
	}))
	if data["code"] != "env_field_not_allowed" {
		t.Fatalf("expected env_field_not_allowed, got %#v", data)
	}
}

func TestManageEnvPatchRejectsMalformedInputWithoutWriting(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)
	if err := os.MkdirAll(config.ConfigDir(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ConfigDir(), "env.json")
	if err := os.WriteFile(path, []byte(`{"vars":{"KEEP":"old"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		patch map[string]any
		want  string
	}{
		{name: "empty", patch: map[string]any{}, want: "invalid_params"},
		{name: "null set", patch: map[string]any{"set": nil}, want: "env_field_invalid"},
		{name: "missing value", patch: map[string]any{"set": []map[string]any{{"name": "NEW"}}}, want: "env_field_invalid"},
		{name: "unknown entry field", patch: map[string]any{"set": []map[string]any{{"name": "NEW", "value": "new", "extra": true}}}, want: "env_field_invalid"},
		{name: "non-string value", patch: map[string]any{"set": []map[string]any{{"name": "NEW", "value": 1}}}, want: "env_field_invalid"},
		{name: "duplicate unset", patch: map[string]any{"unset": []string{"KEEP", "KEEP"}}, want: "env_name_duplicate"},
		{name: "conflict", patch: map[string]any{"set": []map[string]string{{"name": "KEEP", "value": "new"}}, "unset": []string{"KEEP"}}, want: "env_name_conflict"},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message := callManageFixture(t, srv, output, i+1, "mothx/manage/env/patch", tc.patch)
			code, _ := manageFixtureError(t, message)
			if code != tc.want {
				t.Fatalf("error code = %q, want %q; message = %#v", code, tc.want, message)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("rejected patch changed env.json: before=%s after=%s", before, after)
			}
		})
	}
}

func TestManageEnvFeatureDiscovery(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	srv.handleInitialize(rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize", Params: []byte(`{"protocolVersion":1,"clientCapabilities":{}}`)})
	result := manageFixtureResult(t, parseLastMessage(t, output))
	meta, ok := result["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected _meta, got %#v", result)
	}
	mothx, ok := meta["mothx.dev"].(map[string]any)
	if !ok {
		t.Fatalf("expected mothx.dev, got %#v", meta)
	}
	features, ok := mothx["features"].([]any)
	if !ok {
		t.Fatalf("expected features, got %#v", mothx)
	}
	found := false
	for _, f := range features {
		if f == "manageEnv" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("manageEnv must be in features, got %#v", features)
	}
}

func TestManageEnvGetResponseHasNoValueLength(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	if err := os.MkdirAll(config.ConfigDir(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.ConfigDir(), "env.json")
	if err := os.WriteFile(path, []byte(`{"vars":{"SECRET":"longsecretvaluehere"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/get", map[string]any{}))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("longsecretvaluehere")) || bytes.Contains(data, []byte("19")) {
		t.Fatalf("response leaked secret value or length: %s", string(data))
	}
}

func TestManageEnvPatchDoesNotEchoValue(t *testing.T) {
	srv, output, _ := manageEnvFixture(t)

	result := manageFixtureResult(t, callManageFixture(t, srv, output, 1, "mothx/manage/env/patch", map[string]any{
		"set": []map[string]string{{"name": "KEY", "value": "supersecret"}},
	}))
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("supersecret")) {
		t.Fatalf("patch response echoed submitted value: %s", string(data))
	}
}
