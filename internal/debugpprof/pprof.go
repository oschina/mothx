package debugpprof

import (
	"errors"
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAddr keeps debug profiling local-only by default.
	DefaultAddr = "127.0.0.1:6060"
	// AddrEnv overrides the debug pprof listen address.
	AddrEnv = "VIBECODING_PPROF_ADDR"
)

var (
	mu          sync.Mutex
	started     bool
	startedAddr string
)

// Start starts the pprof HTTP server once per process.
func Start() (addr string, startedNow bool, err error) {
	return start(os.Stderr)
}

func start(logWriter io.Writer) (addr string, startedNow bool, err error) {
	if logWriter == nil {
		logWriter = io.Discard
	}
	mu.Lock()
	defer mu.Unlock()

	if started {
		return startedAddr, false, nil
	}

	addr = listenAddr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", false, fmt.Errorf("listen %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           newMux(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	started = true
	startedAddr = ln.Addr().String()

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(logWriter, "[DEBUG] pprof server stopped: %v\n", err)
		}
	}()

	return startedAddr, true, nil
}

// StartForDebug starts pprof and prints the endpoint when it starts.
func StartForDebug(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	addr, startedNow, err := start(w)
	if err != nil {
		fmt.Fprintf(w, "[DEBUG] pprof unavailable: %v\n", err)
		return
	}
	if startedNow {
		fmt.Fprintf(w, "[DEBUG] pprof listening on http://%s/debug/pprof/\n", addr)
	}
}

func listenAddr() string {
	addr := strings.TrimSpace(os.Getenv(AddrEnv))
	if addr == "" {
		return DefaultAddr
	}
	return addr
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// Standard expvar endpoint. Packages publish their own metrics through
	// expvar.Publish (for example internal/db reports SQLite writer
	// contention under "mothx_sqlite"), so the debug server never imports
	// metric owners directly.
	mux.Handle("/debug/vars", expvar.Handler())
	return mux
}
