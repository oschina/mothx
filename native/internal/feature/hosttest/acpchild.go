package hosttest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake ACP runtime for process-level lifecycle tests.
//
// Client.Start always launches "<binary> acp", so a test re-executes its own
// test binary through a shell shim that sets EnvChild. The binary must then
// run exactly one test, which calls ServeChild; that test returns at once in
// the parent, where the variable is unset. The shim has to run the binary
// directly: the child exits inside the test, which go test's exit hooks would
// turn into a panic.
//
// Scenarios:
//
//   - "serve": complete the handshake, answer everything else with {};
//   - "hold": like serve, but hold every method named in EnvHold until the
//     release file exists (touching the got file first);
//   - "hang": never answer initialize, exit with a failure once the release
//     file exists;
//   - "failnew": session/new answers an error;
//   - "failprompt": create the session, fail the prompt;
//   - "prompt": hold session/prompt until the release file exists (touching
//     the got file), then answer EnvStop ("fail" answers an error instead).
const (
	EnvChild   = "MOTHX_NATIVE_ACP_TEST_CHILD"
	EnvRelease = "MOTHX_NATIVE_ACP_TEST_RELEASE"
	EnvGot     = "MOTHX_NATIVE_ACP_TEST_GOT"
	EnvStop    = "MOTHX_NATIVE_ACP_TEST_STOP"
	EnvHold    = "MOTHX_NATIVE_ACP_TEST_HOLD"
)

// ChildShim writes the script that runs this test binary as the scenario.
// helper is the name of the test that calls ServeChild.
func ChildShim(t *testing.T, helper, scenario string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(t.TempDir(), "fake-acp.sh")
	script := "#!/bin/sh\n" + EnvChild + "=" + scenario +
		" exec \"" + binary + "\" -test.run='^" + helper + "$' \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shim
}

// SignalFiles points a held child at fresh signal files and releases the child
// when the test ends, so it cannot outlive the test. The directory is removed
// only after the child has written its last signal: a child still writing
// into a t.TempDir races its removal.
func SignalFiles(t *testing.T) (got, release string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mothx-acp-signals-*")
	if err != nil {
		t.Fatal(err)
	}
	got = filepath.Join(dir, "got")
	release = filepath.Join(dir, "release")
	t.Setenv(EnvGot, got)
	t.Setenv(EnvRelease, release)
	t.Cleanup(func() {
		_ = os.WriteFile(release, []byte("1"), 0o644)
		done := got + ".done"
		if _, statErr := os.Stat(got); statErr == nil {
			if _, statErr = os.Stat(done); statErr != nil {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if _, statErr = os.Stat(done); statErr == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
		_ = os.RemoveAll(dir)
	})
	return got, release
}

// Release answers a held child.
func Release(t *testing.T, release string) {
	t.Helper()
	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// WaitForFile waits until a child has signalled that it received a request.
func WaitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for the child to signal %s", filepath.Base(path))
}

// ServeChild is the child side of the fake runtime. It reads the scenario from
// EnvChild and exits when stdin closes; it never returns while serving.
func ServeChild(scenario string) {
	switch scenario {
	case "serve", "hold", "hang", "failnew", "failprompt", "prompt":
	default:
		return
	}
	await := func(path string) {
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	touch := func(path string) {
		if path != "" {
			_ = os.WriteFile(path, []byte("1"), 0o644)
		}
	}
	hold := map[string]bool{}
	for _, method := range strings.Split(os.Getenv(EnvHold), ",") {
		if method = strings.TrimSpace(method); method != "" {
			hold[method] = true
		}
	}
	// A held request is answered from its own goroutine, as a real runtime
	// keeps serving other requests while one of them is blocked.
	var write sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	answer := func(id json.RawMessage, result any, rpcMessage, doneFile string) {
		var response any = map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		if rpcMessage != "" {
			response = map[string]any{"jsonrpc": "2.0", "id": id,
				"error": map[string]any{"code": -32000, "message": rpcMessage}}
		}
		encoded, _ := json.Marshal(response)
		write.Lock()
		out.Write(encoded)
		out.WriteByte('\n')
		out.Flush()
		write.Unlock()
		if doneFile != "" {
			touch(doneFile)
		}
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 1<<20)
	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		if req.Method == "session/prompt" && os.Getenv(EnvGot) != "" {
			// The .request file lets a test inspect the prompt it was sent.
			_ = os.WriteFile(os.Getenv(EnvGot)+".request", in.Bytes(), 0o644)
		}
		var result any = map[string]any{}
		rpcMessage := ""
		switch {
		case req.Method == "initialize" && scenario == "hang":
			await(os.Getenv(EnvRelease))
			os.Exit(3)
		case req.Method == "initialize":
			result = map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{},
				"agentInfo":         map[string]any{"name": "mothx-test-child", "version": "test-child"},
				"_meta":             map[string]any{"mothx.dev": map[string]any{"features": []string{}}},
			}
		case req.Method == "session/new" && scenario == "failnew":
			rpcMessage = "provider is not configured"
		case req.Method == "session/new":
			result = map[string]any{"sessionId": "created-1"}
		case req.Method == "session/prompt" && scenario == "failprompt":
			rpcMessage = "model unavailable"
		case req.Method == "session/prompt" && scenario == "prompt":
			// The .done file lets a test wait for the answer to be on the wire.
			doneFile := os.Getenv(EnvGot) + ".done"
			touch(os.Getenv(EnvGot))
			go func(id json.RawMessage) {
				await(os.Getenv(EnvRelease))
				stop := os.Getenv(EnvStop)
				if stop == "fail" {
					answer(id, map[string]any{}, "model unavailable", doneFile)
					return
				}
				answer(id, map[string]any{"stopReason": stop}, "", doneFile)
			}(req.ID)
			continue
		case hold[req.Method]:
			doneFile := os.Getenv(EnvGot) + ".done"
			touch(os.Getenv(EnvGot))
			go func(id json.RawMessage) {
				await(os.Getenv(EnvRelease))
				answer(id, map[string]any{}, "", doneFile)
			}(req.ID)
			continue
		}
		answer(req.ID, result, rpcMessage, "")
	}
	os.Exit(0)
}
