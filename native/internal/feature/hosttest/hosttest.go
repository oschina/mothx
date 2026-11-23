// Package hosttest provides a configurable implementation of the narrow
// Native host contract for feature-package guard tests. It is a test helper
// package (not a production dependency) and implements every optional host
// extension the feature packages declare.
package hosttest

import (
	"github.com/egoist/mygo/ui"
	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/skillhub"
	"github.com/oschina/mothx/native/internal/kit"
)

type Host struct {
	Features        map[string]bool
	SettingsVisible bool
	SettingsTabName string
	Reload          int64
	SessionID       string
	PageName        string
	// Panels is keyed by "page|tab".
	Panels             map[string]bool
	SelectedCwd        string
	ExpertWorkspaceCwd string
}

func New() *Host {
	return &Host{Features: make(map[string]bool), Panels: make(map[string]bool)}
}

func (h *Host) Dispatch(func()) {}

func (h *Host) FeatureEnabled(name string) bool { return h.Features[name] }

func (h *Host) ACP() *acp.Client { return nil }

func (h *Host) SettingsOpen() bool { return h.SettingsVisible }

func (h *Host) SettingsTab() string { return h.SettingsTabName }

func (h *Host) ReloadSequence() int64 { return h.Reload }

func (h *Host) CurrentSession() string { return h.SessionID }

func (h *Host) PanelVisible(page, tab string) bool { return h.Panels[page+"|"+tab] }

func (h *Host) Page() string { return h.PageName }

func (h *Host) SelectedWorkspaceCwd() string { return h.SelectedCwd }

func (h *Host) ExpertsWorkspace() string { return h.ExpertWorkspaceCwd }

func (h *Host) StartNewTask() {}

func (h *Host) OpenDirectory(string, func(string)) {}

func (h *Host) SkillIconElement(*ui.Context, kit.Palette, skillhub.Skill, float32) {}
