package acp

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	database "github.com/oschina/mothx/internal/db"
	"github.com/oschina/mothx/internal/session"
)

func TestNotifyDatabaseIntegrityProjectsFinding(t *testing.T) {
	var output bytes.Buffer
	s := &server{w: &output}
	at := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	s.notifyDatabaseIntegrity(session.DatabaseIntegrityFinding{
		Path:   "/data/sessions.db",
		Kind:   session.DatabaseIntegrityCorrupt,
		Report: "*** in database main ***\nTree 2 page 2: btreeInitPage() returns error code 11",
		Err:    &database.IntegrityError{Path: "/data/sessions.db", Report: "broken"},
		At:     at,
	})

	line, err := output.ReadString('\n')
	if err != nil {
		t.Fatalf("read notification: %v (output %q)", err, output.String())
	}
	var message struct {
		JSONRPC string                        `json:"jsonrpc"`
		ID      any                           `json:"id"`
		Method  string                        `json:"method"`
		Params  databaseIntegrityNotification `json:"params"`
	}
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatal(err)
	}
	if message.JSONRPC != "2.0" || message.ID != nil {
		t.Fatalf("message = %#v, want a JSON-RPC notification without an id", line)
	}
	if message.Method != "mothx/database/integrity" {
		t.Fatalf("method = %q, want %q", message.Method, databaseIntegrityMethod)
	}
	params := message.Params
	if params.Kind != "corrupt" || params.Path != "/data/sessions.db" {
		t.Fatalf("params = %#v, want the corruption kind and the canonical path", params)
	}
	if params.At != at.Format(time.RFC3339Nano) {
		t.Fatalf("params.At = %q, want %q", params.At, at.Format(time.RFC3339Nano))
	}
	if !strings.Contains(params.Report, "btreeInitPage()") {
		t.Fatalf("params.Report = %q, want the quick_check output", params.Report)
	}
	if !strings.Contains(params.Detail, "integrity check failed") || !strings.Contains(params.Detail, "/data/sessions.db") {
		t.Fatalf("params.Detail = %q, want the operator-facing summary naming the database", params.Detail)
	}

	// Nothing is projected for an empty finding, which is what a healthy
	// database produces.
	output.Reset()
	s.notifyDatabaseIntegrity(session.DatabaseIntegrityFinding{})
	if output.Len() != 0 {
		t.Fatalf("an empty finding was written to the wire: %q", output.String())
	}
	repaired := databaseIntegrityNotificationFor(session.DatabaseIntegrityFinding{
		Path:   "/data/sessions.db",
		Kind:   session.DatabaseIntegrityIndexRepaired,
		Report: "wrong # of entries in index idx_entries_session_type",
		At:     at,
	})
	if repaired.Kind != "index_repaired" || repaired.Report == "" || repaired.At == "" {
		t.Fatalf("index-repaired projection = %#v, want the kind, the report and the time", repaired)
	}
}

// TestACPDatabaseIntegrityProcessHelper is the Runtime side of
// TestACPProcessReportsBackgroundDatabaseIntegrity. It runs in the subprocess
// the test spawns and does nothing otherwise.
func TestACPDatabaseIntegrityProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_ACP_DATABASE_INTEGRITY_PROCESS_HELPER") != "1" {
		return
	}
	if err := Run(RunOptions{}); err != nil {
		t.Fatal(err)
	}
}

// TestACPProcessReportsBackgroundDatabaseIntegrity is the cross-process
// contract for a damaged session database: the Runtime starts, advertises the
// databaseIntegrity feature, and projects the verdict the background check
// reaches as mothx/database/integrity. A client is never left with a Runtime
// that started on a damaged database and said nothing.
func TestACPProcessReportsBackgroundDatabaseIntegrity(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MOTHX_DIR", configDir)
	settings := config.DefaultSettings()
	settings.DefaultProvider = "process-test"
	settings.DefaultModel = "process-model-1"
	settings.SessionDir = filepath.Join(configDir, "sessions")
	settings.Providers = map[string]*config.ProviderConfig{
		"process-test": {
			APIKey:  "test-key",
			BaseURL: "http://127.0.0.1:1/v1",
			API:     "openai-chat",
			Models: []config.ModelConfig{
				{ID: "process-model-1", Name: "Process Model 1", ContextWindow: 32768, MaxTokens: 1024},
			},
		},
	}
	if err := config.SaveGlobalSettings(settings); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(settings.SessionDir, "sessions.db")
	brokenSessionDatabase(t, dbPath)

	cmd := exec.Command(os.Args[0], "-test.run=^TestACPDatabaseIntegrityProcessHelper$")
	cmd.Env = append(os.Environ(),
		"MOTHX_ACP_DATABASE_INTEGRITY_PROCESS_HELPER=1",
		"MOTHX_DIR="+configDir,
		// Never wait for the verdict. A large database takes this route by
		// default once the bounded wait runs out, so the check runs behind the
		// handshake exactly as it does for a user.
		"MOTHX_SQLITE_QUICK_CHECK=async",
		"MOTHX_RUNTIME_BUS_PORT="+acpTestLeaseBusPort(t),
	)
	workDir := t.TempDir()
	cmd.Dir = workDir
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
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	})

	type received struct {
		message map[string]any
		err     error
	}
	messages := make(chan received, 64)
	reader := bufio.NewReader(stdout)
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				messages <- received{err: err}
				return
			}
			var message map[string]any
			if err := json.Unmarshal(line, &message); err != nil {
				messages <- received{err: fmt.Errorf("non-JSON line on the ACP stdout %q: %w", strings.TrimSpace(string(line)), err)}
				return
			}
			messages <- received{message: message}
		}
	}()
	sendACPRequest(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": 1},
	})

	// The check runs beside the handshake, so the notification may arrive
	// before or after the initialize response.
	var initialize, notification map[string]any
	deadline := time.After(45 * time.Second)
	for initialize == nil || notification == nil {
		select {
		case <-deadline:
			t.Fatalf("initialize = %#v, notification = %#v: the Runtime did not report the damaged database", initialize, notification)
		case item := <-messages:
			if item.err != nil {
				t.Fatalf("read ACP output (initialize = %#v, notification = %#v): %v", initialize, notification, item.err)
			}
			if id, ok := item.message["id"].(float64); ok && id == 1 {
				initialize = item.message
				continue
			}
			if item.message["method"] == databaseIntegrityMethod {
				notification = item.message
			}
		}
	}

	result, _ := initialize["result"].(map[string]any)
	if _, rejected := result["error"]; rejected {
		t.Fatalf("initialize was rejected on a database whose check runs in the background: %s", initialize["error"])
	}
	dev, _ := result["_meta"].(map[string]any)[mothxExtensionNamespace].(map[string]any)
	advertised := false
	for _, feature := range dev["features"].([]any) {
		if feature == "databaseIntegrity" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatalf("features = %#v, want databaseIntegrity", dev["features"])
	}

	params, _ := notification["params"].(map[string]any)
	if params["kind"] != "corrupt" {
		t.Fatalf("notification params = %#v, want the corruption kind", params)
	}
	if params["path"] != dbPath {
		t.Fatalf("notification path = %q, want %q", params["path"], dbPath)
	}
	detail, _ := params["detail"].(string)
	if !strings.Contains(detail, "integrity check failed") || !strings.Contains(detail, dbPath) {
		t.Fatalf("notification detail = %q, want the summary naming the database", detail)
	}
	if report, _ := params["report"].(string); !strings.Contains(report, "btreeInitPage()") {
		t.Fatalf("notification report = %q, want the quick_check output", report)
	}
	if at, ok := params["at"].(string); !ok {
		t.Fatalf("notification params = %#v, want the time the check finished", params)
	} else if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		t.Fatalf("notification at = %q is not RFC 3339: %v", at, err)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ACP process: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ACP process did not exit after stdin EOF")
	}
}

// brokenSessionDatabase writes a session database with the real, fully migrated
// schema and one broken table page. quick_check reports the damage while the
// schema stays readable and no migration is pending, so the Runtime opens the
// file the way it opens a damaged session store instead of failing before the
// handshake or rebuilding it. The damaged table is one no startup path reads.
func brokenSessionDatabase(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := session.OpenRootDB(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	// The child opens the file itself, so this process must let go of it first.
	if err := database.CloseAll(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", database.DSNForOS(path, false))
	if err != nil {
		t.Fatal(err)
	}
	var rootPage, pageSize int
	if err := raw.QueryRow(`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'tool_execution_records'`).Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Break the table b-tree page type: damage quick_check reports, with no
	// stale index for it to repair.
	contents[(rootPage-1)*pageSize] = 0xf5
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

// acpTestLeaseBusPort reserves an unused loopback port for the child's
// advisory lease bus, so a test Runtime never shares the bus of a MothX process
// that happens to be running on this host.
func acpTestLeaseBusPort(t *testing.T) string {
	t.Helper()
	probe, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("reserve a lease bus port for the ACP subprocess: %v", err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()
	return strconv.Itoa(port)
}
