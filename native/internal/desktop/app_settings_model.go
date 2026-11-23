package desktop

import (
	"encoding/json"
	"sync/atomic"
)

func (a *nativeApp) loadApplicationSettings(seq int64) {
	client := a.client()
	if client == nil {
		a.dispatch(func() {
			if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
				return
			}
			a.settingsLoading = false
			a.settingsErr = "尚未连接到 MothX ACP。"
		})
		return
	}
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	raw, err := client.Call("mothx/manage/application/get", map[string]any{})
	a.dispatch(func() {
		if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
			return
		}
		if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
			return
		}
		a.settingsLoading = false
		if err != nil {
			a.settingsErr = "读取应用设置失败：" + err.Error()
			return
		}
		var view applicationSettingsView
		if err := json.Unmarshal(raw, &view); err != nil {
			a.settingsErr = "应用设置响应无效：" + err.Error()
			return
		}
		a.settingsView = &view
		a.settingsForm = applicationSettingsFormFromView(&view)
		a.settingsErr = ""
	})
}

func (a *nativeApp) saveApplicationSettings() {
	if a.settingsSaving || a.settingsLoading {
		return
	}
	if a.settingsView == nil {
		a.settingsStatus = "保存失败：设置尚未加载。"
		return
	}
	patch := applicationSettingsPatch(a.settingsView, &a.settingsForm)
	if len(patch) == 0 {
		a.settingsStatus = "没有需要保存的变更"
		return
	}
	client := a.client()
	if client == nil {
		a.settingsStatus = "保存失败：尚未连接到 MothX ACP。"
		return
	}
	seq := atomic.AddInt64(&a.settingsSeq, 1)
	a.settingsSaving = true
	a.settingsStatus = "正在保存…"
	startReloadSeq := atomic.LoadInt64(&a.reloadSeq)
	go func() {
		raw, err := client.Call("mothx/manage/application/patch", map[string]any{"patch": patch})
		a.dispatch(func() {
			if !a.settingsOpen || seq != atomic.LoadInt64(&a.settingsSeq) {
				return
			}
			if startReloadSeq != atomic.LoadInt64(&a.reloadSeq) {
				return
			}
			a.settingsSaving = false
			if err != nil {
				a.settingsStatus = "保存失败：" + err.Error()
				return
			}
			var view applicationSettingsView
			if err := json.Unmarshal(raw, &view); err != nil {
				a.settingsStatus = "保存后响应无效：" + err.Error()
				return
			}
			a.settingsView = &view
			a.settingsForm = applicationSettingsFormFromView(&view)
			a.settingsStatus = "应用设置已保存"
		})
	}()
}

func (a *nativeApp) onWebSearchBackendChanged(backend string) {
	if a.settingsForm.WebSearchBackend == backend {
		return
	}
	a.settingsForm.WebSearchBackend = backend
	a.settingsForm.WebSearchBaseURL = ""
	a.settingsForm.WebSearchToken = ""
	a.settingsForm.ClearWebSearchToken = true
}

// small helpers used by the view.

func (a *nativeApp) currentModelOption() *configOption {
	for i := range a.configOptions {
		if a.configOptions[i].ID == "model" {
			return &a.configOptions[i]
		}
	}
	return nil
}

func (a *nativeApp) currentThinkingOption() *configOption {
	for i := range a.configOptions {
		switch a.configOptions[i].ID {
		case "thinking_level", "thought_level", "thinking":
			return &a.configOptions[i]
		}
	}
	return nil
}
