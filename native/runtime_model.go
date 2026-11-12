package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
)

// This file owns the Native Runtime diagnostics projection. Connection state
// and stderr are shell facts; mothx/doctor is the shared Runtime's check
// projection. The client never reads settings, the database, or a provider
// directly to diagnose them.

type doctorCheckView struct {
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type doctorResultView struct {
	Version string            `json:"version,omitempty"`
	Checks  []doctorCheckView `json:"checks"`
}

type runtimePanelState struct {
	Doctor        *doctorResultView
	DoctorLoading bool
	DoctorErr     string
	DoctorStatus  string
	Seq           int64
}

func (a *nativeApp) doctorCapable() bool {
	return a.featureEnabled("doctor")
}

func (a *nativeApp) runDoctor() {
	if !a.doctorCapable() || a.runtime.DoctorLoading {
		return
	}
	client := a.acp
	if client == nil {
		a.runtime.DoctorErr = "尚未连接到 MothX ACP。"
		return
	}
	cwd := a.workspace
	if cwd == "" {
		cwd = a.draftCwd
	}
	params := map[string]any{}
	if strings.TrimSpace(cwd) != "" {
		params["cwd"] = cwd
	}
	seq := atomic.AddInt64(&a.runtime.Seq, 1)
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	a.runtime.DoctorLoading = true
	a.runtime.DoctorErr = ""
	a.runtime.DoctorStatus = "正在运行诊断…"
	go func() {
		raw, err := client.call("mothx/doctor", params)
		a.dispatch(func() {
			if seq != atomic.LoadInt64(&a.runtime.Seq) || startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			r := &a.runtime
			r.DoctorLoading = false
			if err != nil {
				r.DoctorErr = "诊断失败：" + err.Error()
				r.DoctorStatus = ""
				return
			}
			var result doctorResultView
			if err := json.Unmarshal(raw, &result); err != nil {
				r.DoctorErr = "诊断响应无效：" + err.Error()
				r.DoctorStatus = ""
				return
			}
			r.Doctor = &result
			r.DoctorStatus = "诊断完成"
		})
	}()
}

func (a *nativeApp) reconnectACP() {
	if a.closing.Load() {
		return
	}
	old := a.acp
	a.acp = nil
	a.conn = connStarting
	a.connErr = ""
	a.stderr = nil
	a.setFeatures(map[string]bool{})
	a.sessions = nil
	a.sessionsLoaded = false
	a.sessionsErr = ""
	a.sessionStates = make(map[string]sessionSnapshot)
	a.projects = nil
	a.projectsLoaded = false
	a.projectsErr = ""
	atomic.AddInt64(&a.projectsSeq, 1)
	a.currentSession = ""
	a.loadingSession = ""
	a.currentTitle = ""
	a.running = false
	a.creatingSession = false
	a.status = "正在重新连接 ACP…"
	a.clearPendingWorktreeOpen()
	a.resetTranscript()
	a.invalidateHistoryPanel()
	a.invalidateRouterPanel()
	a.invalidateSubagentPanel()
	a.closeArtifactPreview()
	a.closeWorktreePanel()
	a.invalidateMCPPanel()
	if old != nil {
		old.close()
	}
	go a.connect()
}

func doctorStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "pass", "passed":
		return "正常"
	case "warn", "warning":
		return "警告"
	case "error", "fail", "failed":
		return "失败"
	case "":
		return "未知"
	default:
		return status
	}
}
