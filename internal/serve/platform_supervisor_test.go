package serve

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/messaging"
	channels "github.com/oschina/mothx/internal/serve/channels"
)

type supervisorTestPlatform struct {
	mu          sync.Mutex
	name        string
	stopped     bool
	startErr    error
	startBlock  <-chan struct{}
	ready       chan error
	signalReady bool
}

func (p *supervisorTestPlatform) Name() string { return p.name }
func (p *supervisorTestPlatform) Start(ctx context.Context, _ messaging.MessageHandler) error {
	if p.startBlock != nil {
		<-p.startBlock
	}
	if p.ready != nil && p.signalReady {
		p.ready <- p.startErr
	}
	return p.startErr
}
func (p *supervisorTestPlatform) Ready() <-chan error { return p.ready }
func (p *supervisorTestPlatform) Stop() error {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	return nil
}
func (p *supervisorTestPlatform) SendMessage(context.Context, string, string) error { return nil }
func (p *supervisorTestPlatform) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.stopped
}

func TestPlatformSupervisorRemoveIfDoesNotRemoveReplacement(t *testing.T) {
	s := NewPlatformSupervisor()
	old := &supervisorTestPlatform{name: "wechat"}
	next := &supervisorTestPlatform{name: "wechat"}
	s.Replace("wechat", old)
	s.Replace("wechat", next)
	if s.RemoveIf("wechat", old) {
		t.Fatal("RemoveIf removed a replacement instance")
	}
	if s.Get("wechat") != next {
		t.Fatal("replacement was not retained")
	}
}

func TestPlatformSupervisorStopAllClearsInstances(t *testing.T) {
	s := NewPlatformSupervisor()
	wechat := &supervisorTestPlatform{name: "wechat"}
	feishu := &supervisorTestPlatform{name: "feishu"}
	s.Replace("wechat", wechat)
	s.Replace("feishu", feishu)
	if err := s.StopAll(); err != nil {
		t.Fatal(err)
	}
	if wechat.IsConnected() || feishu.IsConnected() {
		t.Fatal("StopAll did not stop every platform")
	}
	if len(s.Snapshot()) != 0 {
		t.Fatal("StopAll left instances in the supervisor")
	}
}

func TestChannelRuntimeRestartsPreviousPlatformAfterEarlyCandidateFailure(t *testing.T) {
	supervisor := NewPlatformSupervisor()
	previous := &supervisorTestPlatform{name: "wechat", startBlock: make(chan struct{})}
	candidate := &supervisorTestPlatform{name: "wechat", startErr: context.Canceled}
	supervisor.Replace("wechat", candidate)
	rt := &channelRuntime{platforms: supervisor, dispatcher: &channels.Dispatcher{}}
	go rt.runPlatform(candidate, previous)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if supervisor.Get("wechat") == previous {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("platform supervisor did not restore previous instance; got %#v", supervisor.Get("wechat"))
}

func TestChannelRuntimeCandidateFailureKeepsHealthyPlatform(t *testing.T) {
	previous := &supervisorTestPlatform{name: "wechat", ready: make(chan error, 1)}
	candidate := &supervisorTestPlatform{name: "wechat", startErr: context.Canceled, ready: make(chan error, 1), signalReady: true}
	supervisor := NewPlatformSupervisor()
	supervisor.Replace("wechat", previous)
	rt := &channelRuntime{platforms: supervisor, dispatcher: &channels.Dispatcher{}}
	rt.startPlatformCandidate("wechat", candidate, previous)
	if !previous.IsConnected() {
		t.Fatal("healthy platform was stopped after candidate startup failure")
	}
	if supervisor.Get("wechat") != previous {
		t.Fatal("candidate failure replaced the healthy platform")
	}
}

func TestChannelRuntimeCandidatePromotesOnlyAfterReadiness(t *testing.T) {
	previous := &supervisorTestPlatform{name: "wechat"}
	ready := make(chan error, 1)
	startBlock := make(chan struct{})
	candidate := &supervisorTestPlatform{name: "wechat", ready: ready, startBlock: startBlock}
	supervisor := NewPlatformSupervisor()
	supervisor.Replace("wechat", previous)
	rt := &channelRuntime{platforms: supervisor, dispatcher: &channels.Dispatcher{}}
	done := make(chan struct{})
	go func() {
		rt.startPlatformCandidate("wechat", candidate, previous)
		close(done)
	}()
	// Start is blocked until the test explicitly reports readiness.
	select {
	case <-done:
		t.Fatal("candidate was promoted before readiness")
	case <-time.After(20 * time.Millisecond):
	}
	ready <- nil
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("candidate startup did not complete")
	}
	if supervisor.Get("wechat") != candidate {
		t.Fatal("ready candidate was not promoted")
	}
	if previous.IsConnected() {
		t.Fatal("previous platform was not stopped after promotion")
	}
	close(startBlock)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && supervisor.Get("wechat") == candidate {
		time.Sleep(time.Millisecond)
	}
}

func TestApplyConfigUpdateRollsBackInvalidPlatformCandidate(t *testing.T) {
	dir := t.TempDir()
	previousCfg := DefaultConfig()
	supervisor := NewPlatformSupervisor()
	previous := &supervisorTestPlatform{name: "wechat"}
	supervisor.Replace("wechat", previous)
	rt := &channelRuntime{cfg: previousCfg, platforms: supervisor, platformMu: sync.Mutex{}, sessionDir: dir}
	next := cloneServeConfig(previousCfg)
	next.Channels.Wechat.Enabled = true
	next.Features.Wechat = true
	next.Channels.Wechat.CredPath = filepath.Join(dir, "missing-credentials.json")
	if err := os.WriteFile(next.Channels.Wechat.CredPath, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rt.applyConfigUpdate(next); err == nil {
		t.Fatal("expected invalid platform candidate to fail config apply")
	}
	if rt.configSnapshot().Channels.Wechat.Enabled {
		t.Fatal("runtime config was not rolled back after candidate failure")
	}
	if supervisor.Get("wechat") != previous {
		t.Fatal("healthy platform owner changed after candidate failure")
	}
}

func TestChannelConfigUpdateRestoresFileAfterPlatformCandidateFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serve.json")
	oldBytes := []byte(`{"channels":{"wechat":{"enabled":false,"credPath":"old.json"}},"features":{"wechat":false}}`)
	if err := os.WriteFile(path, oldBytes, 0600); err != nil {
		t.Fatal(err)
	}
	state := &ServeConfigState{Effective: DefaultConfig(), WritablePath: path, WritableLayer: ConfigLayerExplicit, explicitPath: path}
	if err := state.Reload(); err != nil {
		t.Fatal(err)
	}
	rt := &channelRuntime{cfg: state.Effective, configState: state, platforms: NewPlatformSupervisor(), sessionDir: dir}
	badPath := filepath.Join(dir, "missing-credentials.json")
	if err := os.WriteFile(badPath, []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := state.UpdateChannel("wechat", []byte(`{"enabled":true,"credPath":"`+badPath+`"}`), rt.applyConfigUpdate)
	if err == nil {
		t.Fatal("expected platform candidate failure")
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(oldBytes) {
		t.Fatalf("config file changed after candidate failure: %s", got)
	}
	if rt.configSnapshot().Channels.Wechat.Enabled {
		t.Fatal("runtime config remained enabled after candidate failure")
	}
}

func TestPlatformTransportFingerprintIsScopedToOnePlatform(t *testing.T) {
	old := DefaultConfig()
	next := cloneServeConfig(old)
	next.Channels.Feishu.AppID = "new-app"
	if platformTransportChanged(old, next, "wechat") {
		t.Fatal("Feishu-only change incorrectly requested a WeChat restart")
	}
	if !platformTransportChanged(old, next, "feishu") {
		t.Fatal("Feishu credential change did not request a Feishu restart")
	}
	next = cloneServeConfig(old)
	next.Channels.Wechat.WorkDir = filepath.Join(t.TempDir(), "work")
	if platformTransportChanged(old, next, "wechat") {
		t.Fatal("WeChat workDir-only change incorrectly requested transport restart")
	}
}
