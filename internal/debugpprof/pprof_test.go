package debugpprof

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	// Register the mothx_sqlite contention metrics in the process expvar map
	// so the /debug/vars contract can be asserted end to end.
	_ "github.com/oschina/mothx/internal/db"
)

// TestMuxServesExpvarsWithSQLiteStats pins the observability endpoint: the
// debug server's /debug/vars renders the process expvar map including the
// SQLite contention metrics internal/db publishes under "mothx_sqlite".
func TestMuxServesExpvarsWithSQLiteStats(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/debug/vars", nil)
	rec := httptest.NewRecorder()

	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &vars); err != nil {
		t.Fatalf("decode /debug/vars body: %v", err)
	}
	if _, ok := vars["mothx_sqlite"]; !ok {
		t.Fatalf("/debug/vars is missing mothx_sqlite (keys: %d)", len(vars))
	}
}

func TestListenAddrDefaultsToLocalhost(t *testing.T) {
	t.Setenv(AddrEnv, "")

	if got := listenAddr(); got != DefaultAddr {
		t.Fatalf("listenAddr() = %q, want %q", got, DefaultAddr)
	}
}

func TestListenAddrUsesEnvOverride(t *testing.T) {
	t.Setenv(AddrEnv, "127.0.0.1:0")

	if got := listenAddr(); got != "127.0.0.1:0" {
		t.Fatalf("listenAddr() = %q, want 127.0.0.1:0", got)
	}
}

func TestMuxServesPprofIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	rec := httptest.NewRecorder()

	newMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestStartServesPprof(t *testing.T) {
	t.Setenv(AddrEnv, "127.0.0.1:0")

	addr, _, err := Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://" + addr + "/debug/pprof/")
	if err != nil {
		t.Fatalf("GET pprof index: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
