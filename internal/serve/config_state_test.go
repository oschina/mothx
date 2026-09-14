package serve

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func newTestConfigState(t *testing.T, content string) (*ServeConfigState, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultConfig()
	cfg.Channels.Wechat.Enabled = true
	cfg.Channels.Wechat.CredPath = "old.json"
	return &ServeConfigState{
		Effective:     cfg,
		WritablePath:  path,
		WritableLayer: ConfigLayerExplicit,
	}, path
}

func TestServeConfigStateUpdateChannelMergesOmittedFields(t *testing.T) {
	state, path := newTestConfigState(t, `{"channels":{"wechat":{"enabled":true,"credPath":"old.json"}},"features":{"wechat":true}}`)
	result, err := state.UpdateChannel("wechat", []byte(`{"enabled":false}`), nil)
	if err != nil {
		t.Fatalf("UpdateChannel: %v", err)
	}
	configured, ok := result.Configured.(map[string]any)
	if !ok || configured["credPath"] != "old.json" {
		t.Fatalf("configured patch lost omitted field: %#v", result.Configured)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	wechat := root["channels"].(map[string]any)["wechat"].(map[string]any)
	if wechat["enabled"] != false || wechat["credPath"] != "old.json" {
		t.Fatalf("persisted merge = %#v", wechat)
	}
	if state.Snapshot().Channels.Wechat.Enabled {
		t.Fatal("effective config did not apply enabled=false")
	}
}

func TestServeConfigStateUpdateChannelMapsExternalFieldNamesToRuntime(t *testing.T) {
	state, _ := newTestConfigState(t, `{"channels":{"wechat":{"enabled":false,"credPath":"old.json","autoTyping":false}},"features":{"wechat":false}}`)
	if _, err := state.UpdateChannel("wechat", []byte(`{"enabled":true,"credPath":"new.json","autoTyping":true}`), nil); err != nil {
		t.Fatal(err)
	}
	got := state.Snapshot().Channels.Wechat
	if !got.Enabled || got.CredPath != "new.json" || !got.AutoTyping {
		t.Fatalf("runtime channel patch = %#v", got)
	}
}

func TestServeConfigStateSelectsProjectWritableLayer(t *testing.T) {
	project := t.TempDir()
	globalDir := t.TempDir()
	t.Chdir(project)
	t.Setenv("MOTHX_DIR", globalDir)
	if err := os.MkdirAll(filepath.Join(project, ".mothx"), 0700); err != nil {
		t.Fatal(err)
	}
	globalPath := filepath.Join(globalDir, "serve.json")
	projectPath := filepath.Join(project, ".mothx", "serve.json")
	if err := os.WriteFile(globalPath, []byte(`{"channels":{"wechat":{"enabled":false,"credPath":"global.json"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"channels":{"wechat":{"enabled":true,"credPath":"project.json"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := loadServeConfigState(RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if state.WritableLayer != ConfigLayerProject || filepath.Clean(state.WritablePath) != filepath.Clean(filepath.Join(".mothx", "serve.json")) {
		t.Fatalf("writable layer = %s/%s, want project/.mothx/serve.json", state.WritableLayer, state.WritablePath)
	}
	if _, err := state.UpdateChannel("wechat", []byte(`{"credPath":"updated.json"}`), nil); err != nil {
		t.Fatal(err)
	}
	projectData, err := os.ReadFile(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	globalData, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectData), "updated.json") {
		t.Fatalf("project config was not updated: %s", projectData)
	}
	if strings.Contains(string(globalData), "updated.json") {
		t.Fatalf("global config was unexpectedly updated: %s", globalData)
	}
}

func TestServeConfigStateUpdateChannelRestoresOnApplyFailure(t *testing.T) {
	state, path := newTestConfigState(t, `{"channels":{"wechat":{"enabled":true,"credPath":"old.json"}},"features":{"wechat":true}}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.UpdateChannel("wechat", []byte(`{"enabled":false}`), func(*Config) error {
		return errors.New("prepare failed")
	})
	if err == nil {
		t.Fatal("expected apply failure")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(before) != string(after) {
		t.Fatalf("config changed after failed apply: before=%s after=%s", before, after)
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatalf("config mtime changed after failed apply: before=%v after=%v", beforeInfo.ModTime(), afterInfo.ModTime())
	}
	if !state.Snapshot().Channels.Wechat.Enabled {
		t.Fatal("effective config changed after failed apply")
	}
}

func TestServeConfigStateUpdateChannelAtomicWriteFailureKeepsOldState(t *testing.T) {
	state, path := newTestConfigState(t, `{"channels":{"wechat":{"enabled":true,"credPath":"old.json"}},"features":{"wechat":true}}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state.writeAtomic = func(string, []byte) error {
		return errors.New("rename injected failure")
	}
	if _, err := state.UpdateChannel("wechat", []byte(`{"credPath":"new.json"}`), nil); err == nil {
		t.Fatal("expected atomic write failure")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("config changed after failed atomic write: before=%s after=%s", before, after)
	}
	if got := state.Snapshot().Channels.Wechat.CredPath; got != "old.json" {
		t.Fatalf("effective config changed after failed atomic write: %q", got)
	}
}

func TestServeConfigStateUpdateChannelSerializesConcurrentPatches(t *testing.T) {
	state, path := newTestConfigState(t, `{"channels":{"wechat":{"enabled":true}},"features":{"wechat":true}}`)
	patches := [][]byte{
		[]byte(`{"credPath":"new.json"}`),
		[]byte(`{"autoTyping":true}`),
	}
	var wg sync.WaitGroup
	for _, patch := range patches {
		patch := patch
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := state.UpdateChannel("wechat", patch, nil); err != nil {
				t.Errorf("UpdateChannel: %v", err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	wechat := root["channels"].(map[string]any)["wechat"].(map[string]any)
	if wechat["credPath"] != "new.json" || wechat["autoTyping"] != true {
		t.Fatalf("concurrent patches lost a field: %#v", wechat)
	}
}

func TestServeConfigStateUpdateFullKeepsRuntimeOverridesEphemeral(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.json")
	state := &ServeConfigState{
		Effective:     DefaultConfig(),
		WritablePath:  path,
		WritableLayer: ConfigLayerExplicit,
		overrides:     RunOptions{Port: "9999", Provider: "runtime-provider"},
	}
	next, err := state.UpdateFull([]byte(`{"api":{"enableWebSearch":true}}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.API.Listen != ":9999" || next.API.Provider != "runtime-provider" || !next.API.EnableWebSearch {
		t.Fatalf("effective config lost runtime overrides: %#v", next.API)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := DecodeConfigBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.API.Listen == ":9999" || persisted.API.Provider == "runtime-provider" {
		t.Fatalf("runtime overrides were persisted: %#v", persisted.API)
	}
}

func TestServeConfigStateUpdateFullRestoresOnApplyFailure(t *testing.T) {
	state, path := newTestConfigState(t, `{"api":{"listen":":7000"},"channels":{"wechat":{"enabled":true}}}`)
	oldListen := state.Snapshot().API.Listen
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = state.UpdateFull([]byte(`{"api":{"listen":":8000"},"features":{"wechat":false}}`), func(*Config) error {
		return errors.New("dispatcher prepare failed")
	})
	if err == nil {
		t.Fatal("expected full update apply failure")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("full update changed config after apply failure: before=%s after=%s", before, after)
	}
	if got := state.Snapshot().API.Listen; got != oldListen {
		t.Fatalf("effective config changed after apply failure: %q", got)
	}
}

func TestSyncConfigDirForOSSkipsOnWindows(t *testing.T) {
	// Windows FlushFileBuffers on a read-only directory handle always returns
	// ERROR_ACCESS_DENIED ("Access is denied") on every filesystem, exFAT
	// included, so the Windows branch must not touch the directory at all.
	if err := syncConfigDirForOS(filepath.Join(t.TempDir(), "missing"), true); err != nil {
		t.Fatalf("windows directory sync must be skipped: %v", err)
	}
}

func TestSyncConfigDirForOSFlushesOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory fsync is unavailable on Windows")
	}
	dir := t.TempDir()
	if err := syncConfigDirForOS(dir, false); err != nil {
		t.Fatalf("syncConfigDirForOS: %v", err)
	}
	if err := syncConfigDirForOS(filepath.Join(dir, "missing"), false); err == nil {
		t.Fatal("expected error for a missing directory")
	}
}

func TestAtomicWritePrivateFileReplacesAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	if err := os.WriteFile(path, []byte("stale\n"), 0600); err != nil {
		t.Fatal(err)
	}
	want := "{\"api\":{\"listen\":\":7000\"}}\n"
	if err := atomicWritePrivateFile(path, []byte(want)); err != nil {
		t.Fatalf("atomicWritePrivateFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("written config = %q, want %q", data, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("temp file leaked: %v", names)
	}
}

func TestChannelRuntimeConfigSnapshotIsIsolated(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Channels.Wechat.CredPath = "original.json"
	rt := &channelRuntime{cfg: cfg}
	snapshot := rt.configSnapshot()
	if snapshot == nil {
		t.Fatal("config snapshot is nil")
	}
	snapshot.Channels.Wechat.CredPath = "mutated.json"
	if rt.cfg.Channels.Wechat.CredPath != "original.json" {
		t.Fatalf("snapshot mutation changed runtime config: %q", rt.cfg.Channels.Wechat.CredPath)
	}
}
