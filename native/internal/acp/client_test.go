package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Prompt turns are intentionally unbounded; every other request has a bounded
// default so a wedged child cannot leave a management panel loading forever.
func TestRequestTimeoutPolicy(t *testing.T) {
	if timeout, bounded := requestTimeout("session/prompt"); bounded || timeout != 0 {
		t.Fatalf("session/prompt timeout = (%v, %v), want unbounded", timeout, bounded)
	}
	for _, method := range []string{"initialize", "session/load", "mothx/manage/providers/list", "mothx/worktree/create"} {
		timeout, bounded := requestTimeout(method)
		if !bounded || timeout != defaultRequestTimeout {
			t.Fatalf("%s timeout = (%v, %v), want (%v, true)", method, timeout, bounded, defaultRequestTimeout)
		}
	}
}

// TestCallContextReturnsOnDeadline covers the handshake-style timeout: a child
// that never answers must fail through the caller's context instead of
// blocking a worker forever. The prompt path keeps the unbounded Call.
func TestCallContextReturnsOnDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-acp.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	client := NewClient(nil, nil, nil)
	if err := client.Start(script, dir, os.Environ()); err != nil {
		t.Fatalf("start fake ACP child: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := client.CallContext(ctx, "initialize", map[string]any{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CallContext error = %v, want context deadline", err)
	}
}

// TestACPStdioChild is the child side of the stdio lifecycle tests. The parent
// re-executes the test binary through a shell shim because Client.Start always
// launches "<binary> acp"; MOTHX_NATIVE_ACP_TEST_CHILD selects the scenario:
//
//   - "final": answer one request with a large line and exit immediately;
//   - "huge": answer every request with a line larger than the reader accepts.
//
// It writes nothing except protocol lines and exits inside the test, so the
// shim must run it directly rather than through go test.
func TestACPStdioChild(t *testing.T) {
	scenario := os.Getenv("MOTHX_NATIVE_ACP_TEST_CHILD")
	if scenario == "" {
		return
	}
	out := bufio.NewWriter(os.Stdout)
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 1<<20)
	for in.Scan() {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil {
			continue
		}
		if scenario == "huge" {
			fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{"blob":"%s"}}`+"\n", req.ID, strings.Repeat("a", 256*1024))
			out.Flush()
			continue
		}
		// A large final line keeps the reader busy, which widens the window in
		// which cmd.Wait used to close the pipe before the line was drained.
		fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%d,"result":{"ok":true,"pad":"%s"}}`+"\n", req.ID, strings.Repeat("a", 1<<20))
		out.Flush()
		os.Exit(0)
	}
	os.Exit(0)
}

func acpChildShim(t *testing.T, scenario string) (shim, scenarioEnv string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell helper")
	}
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	shim = filepath.Join(t.TempDir(), "fake-acp.sh")
	script := "#!/bin/sh\nexec \"" + binary + "\" -test.run='^TestACPStdioChild$' \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return shim, "MOTHX_NATIVE_ACP_TEST_CHILD=" + scenario
}

func waitClientClosed(t *testing.T, client *Client, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if client.Closed() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s: the ACP client never reached a closed state", what)
}

// The pipes the client owns must deliver everything a child writes before it
// exits. With cmd.StdoutPipe, cmd.Wait closed the read end as soon as the
// process exited and the final response was lost part of the time on a
// multi-core machine; the loss is scheduling dependent, so this is a guard
// rather than a deterministic reproduction.
func TestOwnedPipesDeliverTheFinalMessageBeforeExit(t *testing.T) {
	shim, scenarioEnv := acpChildShim(t, "final")
	env := append(os.Environ(), scenarioEnv)
	iterations := 30
	if raceDetector {
		// The race-instrumented child takes about a second to start.
		iterations = 6
	}
	for iteration := 0; iteration < iterations; iteration++ {
		client := NewClient(nil, nil, nil)
		if err := client.Start(shim, t.TempDir(), env); err != nil {
			t.Fatalf("iteration %d: start child: %v", iteration, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		raw, err := client.CallContext(ctx, "initialize", map[string]any{})
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: the final response was lost: %v", iteration, err)
		}
		if len(raw) <= 1<<20 {
			t.Fatalf("iteration %d: final response was truncated or lost (len=%d): %v", iteration, len(raw), err)
		}
		waitClientClosed(t, client, fmt.Sprintf("iteration %d", iteration))
		for _, line := range client.StderrTail(4) {
			if strings.Contains(line, "ACP stdout reader") {
				t.Fatalf("iteration %d: spurious reader error %q", iteration, line)
			}
		}
		client.Close()
	}
}

// A line beyond the reader's limit cannot be skipped safely, so it must fail
// the connection like a crash. Before, the reader stopped silently: the child
// stayed alive, onExit never fired, and every later call waited for its
// deadline.
func TestOversizedLineFailsTheConnectionInsteadOfStalling(t *testing.T) {
	shim, scenarioEnv := acpChildShim(t, "huge")
	exited := make(chan error, 1)
	client := NewClient(nil, nil, func(err error) { exited <- err })
	client.maxLineBytes = 64 * 1024
	if err := client.Start(shim, t.TempDir(), append(os.Environ(), scenarioEnv)); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.CallContext(ctx, "initialize", map[string]any{}); !errors.Is(err, ErrStreamFailed) {
		t.Fatalf("oversized line error = %v, want ErrStreamFailed", err)
	}
	select {
	case err := <-exited:
		if !errors.Is(err, ErrStreamFailed) {
			t.Fatalf("onExit cause = %v, want ErrStreamFailed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("onExit was not reported for the broken stream")
	}
	// The connection is closed: a later call fails immediately instead of
	// waiting for its deadline, on the bounded path and the unbounded one.
	for _, method := range []string{"mothx/manage/env/get", "session/prompt"} {
		start := time.Now()
		_, err := client.Call(method, map[string]any{})
		if !errors.Is(err, errNotConnected) {
			t.Fatalf("%s error = %v, want the not-connected error", method, err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("%s waited %v on a failed connection", method, elapsed)
		}
	}
}
