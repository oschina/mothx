package providers

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/hosttest"
)

// TestProvidersACPChild is the child side of the process-level tests;
// hosttest.ServeChild documents the scenarios.
func TestProvidersACPChild(t *testing.T) {
	hosttest.ServeChild(os.Getenv(hosttest.EnvChild))
}

// runtimeHost is a host with a real ACP client and a UI-thread queue, so the
// panel's asynchronous actions can be driven against a real child process.
type runtimeHost struct {
	*hosttest.Host
	client *acp.Client
	queue  *hosttest.Queue
}

func (h *runtimeHost) ACP() *acp.Client   { return h.client }
func (h *runtimeHost) Dispatch(fn func()) { h.queue.Post(fn) }

func connectedRuntimeHost(t *testing.T, holdMethod string) *runtimeHost {
	t.Helper()
	hosttest.SignalFiles(t)
	t.Setenv(hosttest.EnvHold, holdMethod)
	client := acp.NewClient(nil, nil, nil)
	if err := client.Start(hosttest.ChildShim(t, "TestProvidersACPChild", "hold"), t.TempDir(), os.Environ()); err != nil {
		t.Fatalf("start fake runtime: %v", err)
	}
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := client.CallContext(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	host := &runtimeHost{Host: hosttest.New(), client: client, queue: hosttest.NewQueue()}
	host.Features["manageProviders"] = true
	host.Features["manageProviderConfig"] = true
	host.SettingsVisible = true
	host.SettingsTabName = "providers"
	return host
}

// A connection test that is still running when the person starts model
// discovery is superseded, and its answer is dropped. It must not keep the
// panel busy after that: a Testing flag left set kept Save and Test disabled
// until the provider was selected again.
func TestSupersededActionReleasesItsBusyFlag(t *testing.T) {
	host := connectedRuntimeHost(t, "mothx/manage/providers/test")
	got := os.Getenv(hosttest.EnvGot)

	panel := NewPanel()
	panel.Catalog = &CatalogView{ProviderConfigs: []ConfigView{
		{ID: "p1", Provider: ConfigShape{BaseURL: "https://example.test", Models: []Model{{ID: "m1"}}}},
	}}
	panel.SelectDraft("p1", false)

	panel.TestDraft(host)
	if !panel.Testing {
		t.Fatal("the connection test must be in flight")
	}
	hosttest.WaitForFile(t, got)

	panel.DiscoverModels(host)
	if !panel.Discovering {
		t.Fatal("model discovery must be in flight")
	}
	if panel.Testing {
		t.Fatal("starting another action must release the superseded test's flag")
	}
	if !host.queue.RunUntil(t, 20*time.Second, func() bool { return !panel.Discovering }) {
		t.Fatal("model discovery never finished")
	}

	// The held test is answered only now; its response is stale.
	hosttest.Release(t, os.Getenv(hosttest.EnvRelease))
	hosttest.WaitForFile(t, got+".done")
	// A dropped response changes nothing observable, so give the panel a
	// moment to read it and post the work that is then discarded.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		host.queue.Drain()
		time.Sleep(10 * time.Millisecond)
	}

	if panel.Testing || panel.Discovering || panel.Saving {
		t.Fatalf("a superseded action left the panel busy: testing=%v discovering=%v saving=%v",
			panel.Testing, panel.Discovering, panel.Saving)
	}
	if panel.Status != "未发现新的模型" {
		t.Fatalf("status = %q, want the discovery result", panel.Status)
	}
}
