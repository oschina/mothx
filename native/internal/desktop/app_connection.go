package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// nativeAutoRestartDelay mirrors Desktop's 1/2/4/8/15 second backoff.
func nativeAutoRestartDelay(attempt int) time.Duration {
	if attempt < 1 {
		return 0
	}
	delay := time.Second << (attempt - 1)
	if delay > 15*time.Second {
		delay = 15 * time.Second
	}
	return delay
}

// nativeNonRetryableStartupError mirrors Desktop: deterministic configuration
// failures must not enter a restart loop; the person has to fix them first.
func nativeNonRetryableStartupError(err nativeStartupError) bool {
	switch strings.TrimSpace(err.Code) {
	case "config_invalid", "provider_unusable":
		return true
	default:
		return false
	}
}

// scheduleAutoRestartOrRollback prefers the binary rollback reconnect when a
// user-selected runtime just failed; otherwise it enters the bounded restart
// scheduler. Never do both for one failure.
func (a *nativeApp) scheduleAutoRestartOrRollback() {
	if a.binaryRollbackPending {
		a.rollbackRuntimeBinaryIfPending()
		return
	}
	a.maybeScheduleAutoRestart()
}

// maybeScheduleAutoRestart is called on the UI thread after an unexpected ACP
// child exit. It keeps the Desktop parity rule: retry at most five times with
// bounded backoff, and never retry deterministic configuration failures.
func (a *nativeApp) maybeScheduleAutoRestart() {
	if a.closing.Load() || a.autoRestartPending {
		return
	}
	startup := a.loadStartupError()
	if nativeNonRetryableStartupError(startup) {
		addDiagnostic("error", "ACP restart disabled for non-retryable startup error: "+startup.Code)
		return
	}
	if a.autoRestartAttempts >= nativeMaxAutoRestartAttempts {
		addDiagnostic("warn", "ACP auto-restart attempts exhausted")
		return
	}
	a.autoRestartAttempts++
	attempt := a.autoRestartAttempts
	interval := nativeAutoRestartDelay(attempt)
	a.autoRestartPending = true
	a.status = fmt.Sprintf("ACP 已退出，%s后自动重连（%d/%d）", interval, attempt, nativeMaxAutoRestartAttempts)
	go func() {
		time.Sleep(interval)
		a.dispatch(func() {
			a.autoRestartPending = false
			if a.closing.Load() {
				return
			}
			if a.conn == connReady || a.conn == connStarting {
				return
			}
			extra := a.loadStartupError()
			if nativeNonRetryableStartupError(extra) {
				return
			}
			a.status = "ACP 已退出，正在自动重连…"
			go a.connect()
		})
	}()
}

// nativeACPEnvironment preserves the user's environment but extends the
// approval/question decision deadlines to 30 minutes, matching Desktop's ACP
// child launch. These are human-paced decisions; the Runtime defaults are much
// shorter and would expire a decision while a person is still reviewing it.
func nativeACPEnvironment() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "MOTHX_ACP_PERMISSION_TIMEOUT=") ||
			strings.HasPrefix(entry, "MOTHX_ACP_QUESTION_TIMEOUT=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"MOTHX_ACP_PERMISSION_TIMEOUT=30m",
		"MOTHX_ACP_QUESTION_TIMEOUT=30m",
	)
}

func (a *nativeApp) connect() {
	if a.closing.Load() {
		return
	}
	seq := atomic.AddInt64(&a.reloadSeq, 1)
	bin, source := resolveMothxBinarySource()
	addDiagnostic("info", "connect: binary="+bin+" source="+source)
	if bin == "" {
		addDiagnostic("error", "connect: no mothx binary resolved")
		a.dispatch(func() {
			a.setConn(connFailed, "找不到 mothx 可执行文件。请设置 MOTHX_BIN，或把 mothx 放进 PATH。")
		})
		return
	}
	a.dispatch(func() {
		a.setConn(connStarting, "")
		a.stderr = nil
		a.setStartupError(nativeStartupError{})
		a.featuresMu.Lock()
		a.features = make(map[string]bool)
		a.featuresReady = false
		a.featuresMu.Unlock()
		// A reconnect invalidates the management projection as well as ordinary
		// session requests. Leave the settings page so an in-flight response
		// can neither render stale values nor leave its spinner active.
		a.closeSettings()
		a.projects = nil
		a.projectsLoaded = false
		a.projectsErr = ""
		a.resetSessionPages()
		atomic.AddInt64(&a.projectsSeq, 1)
		a.invalidateRouterPanel()
		a.invalidateSubagentPanel()
		a.closeWorktreePanel()
	})

	var client *acpClient
	client = newACPClient(
		func(msg incoming) { a.handleIncoming(client, msg) },
		func(line string) {
			if parsed, ok := parseNativeStartupErrorLine(line); ok {
				a.setStartupError(parsed)
			}
			a.dispatch(func() {
				a.stderr = append(a.stderr, line)
				if len(a.stderr) > 40 {
					a.stderr = a.stderr[len(a.stderr)-40:]
				}
			})
		},
		func(err error) {
			if a.closing.Load() {
				return
			}
			text := "ACP 进程已退出"
			if err != nil {
				text += "：" + err.Error()
			}
			a.dispatch(func() {
				// A stale child (manual reconnect or binary rollback) must not
				// overwrite the connection state of the child that replaced it.
				if a.client() != client {
					return
				}
				if a.conn != connFailed {
					a.failConnection(text)
				}
				a.running = false
				a.creatingSession = false
				// The ACP child is gone, so a queued worktree open can no longer be
				// fulfilled. Drop it instead of leaving a resolved-looking promise.
				a.clearPendingWorktreeOpen()
				a.status = ""
				a.maybeScheduleAutoRestart()
			})
		},
	)
	if err := client.Start(bin, a.cwd, nativeACPEnvironment()); err != nil {
		addDiagnostic("error", "ACP start failed: "+err.Error())
		a.dispatch(func() {
			a.setConn(connFailed, "无法启动 ACP："+err.Error())
			a.scheduleAutoRestartOrRollback()
		})
		return
	}
	a.dispatch(func() { a.setClient(client) })

	initializeCtx, cancelInitialize := context.WithTimeout(context.Background(), nativeACPInitializeTimeout)
	raw, err := client.CallContext(initializeCtx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
			"session":  map[string]any{"configOptions": map[string]any{"boolean": map[string]any{}}},
		},
		"clientInfo": map[string]any{
			"name":    "mothx-native",
			"title":   "MothX Native",
			"version": NativeBuildVersion,
		},
		"_meta": map[string]any{"mothx": map[string]any{"surface": "native"}},
	})
	cancelInitialize()
	if err != nil {
		client.Close()
		addDiagnostic("error", "ACP initialize failed: "+err.Error())
		a.dispatch(func() {
			a.failConnection("ACP 初始化失败：" + err.Error())
			a.scheduleAutoRestartOrRollback()
		})
		return
	}
	var init initializeResult
	if err := json.Unmarshal(raw, &init); err != nil {
		client.Close()
		addDiagnostic("error", "ACP initialize response invalid: "+err.Error())
		a.dispatch(func() {
			a.failConnection("ACP initialize 响应无效：" + err.Error())
			a.scheduleAutoRestartOrRollback()
		})
		return
	}
	features := parseFeatures(init)
	a.dispatch(func() {
		if seq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		if version := strings.TrimSpace(init.AgentInfo.Version); version != "" {
			a.runtimeVersion = version
		}
		a.setConn(connReady, "")
		a.autoRestartAttempts = 0
		a.autoRestartPending = false
		a.setFeatures(features)
		a.binaryRollbackPending = false
		a.binaryRollbackPrevious = ""
	})
	if seq != atomic.LoadInt64(&a.reloadSeq) {
		client.Close()
		return
	}
	// None of these projections is required for the handshake itself. Start
	// them together so the first list page does not wait behind draft config,
	// and let the session catalog render as soon as its own response arrives.
	go a.refreshDraftConfig(client, a.cwd, features)
	go a.refreshSessions(client, a.cwd, features)
	go a.refreshModelCatalog(client, features)
}

func (a *nativeApp) setFeatures(features map[string]bool) {
	a.featuresMu.Lock()
	a.features = features
	a.featuresReady = true
	a.featuresMu.Unlock()
	// A history view opened during initialize must resume once capabilities are
	// known instead of keeping the pre-init “not advertised” fallback forever.
	if a.client() != nil && a.historyOpen && !a.historyPanel.Loaded {
		a.historyPanel.Loading = false
		a.startHistoryLoad(false)
	}
}

func (a *nativeApp) featuresInitialized() bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	return a.featuresReady
}

func (a *nativeApp) featureSnapshot() map[string]bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	if a.features == nil {
		return nil
	}
	out := make(map[string]bool, len(a.features))
	for k, v := range a.features {
		out[k] = v
	}
	return out
}

func (a *nativeApp) featureEnabled(name string) bool {
	a.featuresMu.RLock()
	defer a.featuresMu.RUnlock()
	return a.features[name]
}

func parseFeatures(init initializeResult) map[string]bool {
	features := make(map[string]bool)
	raw, ok := init.Meta["mothx.dev"]
	if !ok {
		raw, ok = init.Meta["mothx"]
	}
	if !ok {
		return features
	}
	var meta struct {
		Features []string `json:"features"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return features
	}
	for _, f := range meta.Features {
		features[f] = true
	}
	return features
}
