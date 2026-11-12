package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Native shell preferences are presentation-only state that ACP does not own:
// theme, home preset, home logo/background appearance, and the local runtime
// binary choice. They live in the app's PathUserData, never in settings.json or
// a session database, matching Desktop's desktop-store boundary.

const nativePreferencesFile = "native-preferences.json"

type nativePreferences struct {
	ThemeMode              string   `json:"themeMode,omitempty"`
	HomePreset             string   `json:"homePreset,omitempty"`
	HomeLogoVisible        *bool    `json:"homeLogoVisible,omitempty"`
	HomeLogoPath           string   `json:"homeLogoPath,omitempty"`
	HomeBackgroundPath     string   `json:"homeBackgroundPath,omitempty"`
	HomeBackgroundOpacity  *float64 `json:"homeBackgroundOpacity,omitempty"`
	HomeBackgroundBlur     float64  `json:"homeBackgroundBlur,omitempty"`
	HomeBackgroundFit      string   `json:"homeBackgroundFit,omitempty"`
	HomeBackgroundPosition string   `json:"homeBackgroundPosition,omitempty"`
	RuntimeBinary          string   `json:"runtimeBinary,omitempty"`
}

type homeAppearance struct {
	preset             string
	logoVisible        bool
	logoPath           string
	logoImage          ui.ImageSource
	backgroundPath     string
	backgroundImage    ui.ImageSource
	backgroundOpacity  float64
	backgroundBlur     float64
	backgroundFit      string
	backgroundPosition string
}

func defaultHomeAppearance() homeAppearance {
	return homeAppearance{
		preset:             "working",
		logoVisible:        true,
		backgroundOpacity:  1,
		backgroundFit:      "cover",
		backgroundPosition: "center",
	}
}

func nativePreferencesPath() string {
	if override := strings.TrimSpace(os.Getenv("MOTHX_NATIVE_PREFERENCES")); override != "" {
		return override
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil || strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, nativePreferencesFile)
}

func loadNativePreferences(path string) (nativePreferences, bool) {
	if strings.TrimSpace(path) == "" {
		return nativePreferences{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nativePreferences{}, false
	}
	var prefs nativePreferences
	if err := json.Unmarshal(data, &prefs); err != nil {
		return nativePreferences{}, false
	}
	return prefs, true
}

func (a *nativeApp) saveNativePreferences() {
	if strings.TrimSpace(a.prefsPath) == "" {
		return
	}
	prefs := nativePreferences{
		ThemeMode:              a.themeMode,
		HomePreset:             a.home.preset,
		HomeLogoVisible:        &a.home.logoVisible,
		HomeLogoPath:           a.home.logoPath,
		HomeBackgroundPath:     a.home.backgroundPath,
		HomeBackgroundOpacity:  &a.home.backgroundOpacity,
		HomeBackgroundBlur:     a.home.backgroundBlur,
		HomeBackgroundFit:      a.home.backgroundFit,
		HomeBackgroundPosition: a.home.backgroundPosition,
		RuntimeBinary:          runtimeBinaryPreference,
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.prefsPath), 0o755); err != nil {
		return
	}
	tmp := a.prefsPath + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err == nil {
		_ = os.Rename(tmp, a.prefsPath)
	}
}

func (a *nativeApp) applyNativePreferences(prefs nativePreferences) {
	if mode := strings.ToLower(strings.TrimSpace(prefs.ThemeMode)); mode == "dark" || mode == "light" || mode == "system" {
		a.themeMode = mode
		a.themeSet = false
	}
	if preset := strings.TrimSpace(prefs.HomePreset); preset != "" {
		a.home.preset = preset
	}
	if prefs.HomeLogoVisible != nil {
		a.home.logoVisible = *prefs.HomeLogoVisible
	}
	a.home.logoPath = strings.TrimSpace(prefs.HomeLogoPath)
	a.home.backgroundPath = strings.TrimSpace(prefs.HomeBackgroundPath)
	if prefs.HomeBackgroundOpacity != nil {
		opacity := *prefs.HomeBackgroundOpacity
		if opacity < 0 {
			opacity = 0
		}
		if opacity > 1 {
			opacity = 1
		}
		a.home.backgroundOpacity = opacity
	}
	if prefs.HomeBackgroundBlur >= 0 {
		a.home.backgroundBlur = prefs.HomeBackgroundBlur
	}
	switch strings.ToLower(strings.TrimSpace(prefs.HomeBackgroundFit)) {
	case "cover", "contain", "fill", "scaledown":
		a.home.backgroundFit = strings.ToLower(strings.TrimSpace(prefs.HomeBackgroundFit))
	}
	switch strings.ToLower(strings.TrimSpace(prefs.HomeBackgroundPosition)) {
	case "center", "top", "bottom", "left", "right", "topleft", "topright", "bottomleft", "bottomright":
		a.home.backgroundPosition = strings.ToLower(strings.TrimSpace(prefs.HomeBackgroundPosition))
	}
	if bin := strings.TrimSpace(prefs.RuntimeBinary); bin != "" {
		setRuntimeBinaryPreference(bin, false)
	}
	a.loadHomeImages()
}

// loadHomeImages reads the persisted logo/background once into decodable
// images. A missing or undecodable file clears only that image; the rest of the
// shell keeps working instead of failing startup on a bad path.
func (a *nativeApp) loadHomeImages() {
	a.home.logoImage = nil
	if a.home.logoPath != "" {
		if data, err := os.ReadFile(a.home.logoPath); err == nil {
			if image, err := ui.DecodeImage(data); err == nil {
				a.home.logoImage = image
			}
		}
	}
	a.home.backgroundImage = nil
	if a.home.backgroundPath != "" {
		if data, err := os.ReadFile(a.home.backgroundPath); err == nil {
			if image, err := ui.DecodeImage(data); err == nil {
				a.home.backgroundImage = image
			}
		}
	}
}

func (a *nativeApp) chooseHomeLogo() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: T("选择首页 Logo")})
		if err != nil || len(paths) == 0 {
			return
		}
		a.dispatch(func() {
			a.home.logoPath = paths[0]
			a.home.logoVisible = true
			a.loadHomeImages()
			a.saveNativePreferences()
			a.showToast("首页 Logo 已更新")
		})
	}()
}

func (a *nativeApp) chooseHomeBackground() {
	win := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: T("选择首页背景图")})
		if err != nil || len(paths) == 0 {
			return
		}
		a.dispatch(func() {
			a.home.backgroundPath = paths[0]
			a.loadHomeImages()
			a.saveNativePreferences()
			a.showToast("首页背景已更新")
		})
	}()
}

func (a *nativeApp) clearHomeLogo() {
	a.home.logoPath = ""
	a.home.logoImage = nil
	a.saveNativePreferences()
}

func (a *nativeApp) clearHomeBackground() {
	a.home.backgroundPath = ""
	a.home.backgroundImage = nil
	a.saveNativePreferences()
}

func (a *nativeApp) homeFit() ui.Fit {
	switch strings.ToLower(a.home.backgroundFit) {
	case "contain":
		return ui.Contain
	case "fill":
		return ui.FillBox
	case "scaledown":
		return ui.ScaleDown
	default:
		return ui.Cover
	}
}

func (a *nativeApp) homeObjectPosition() (float32, float32) {
	switch strings.ToLower(a.home.backgroundPosition) {
	case "top":
		return 0.5, 0
	case "bottom":
		return 0.5, 1
	case "left":
		return 0, 0.5
	case "right":
		return 1, 0.5
	case "topleft":
		return 0, 0
	case "topright":
		return 1, 0
	case "bottomleft":
		return 0, 1
	case "bottomright":
		return 1, 1
	default:
		return 0.5, 0.5
	}
}

func (a *nativeApp) backgroundFitOptions() []string {
	return []string{"Cover", "Contain", "Fill", "Scale down"}
}

func (a *nativeApp) backgroundFitValue() string {
	switch strings.ToLower(a.home.backgroundFit) {
	case "contain":
		return "Contain"
	case "fill":
		return "Fill"
	case "scaledown":
		return "Scale down"
	default:
		return "Cover"
	}
}

func (a *nativeApp) setBackgroundFit(value string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "contain":
		a.home.backgroundFit = "contain"
	case "fill":
		a.home.backgroundFit = "fill"
	case "scale down", "scaledown":
		a.home.backgroundFit = "scaledown"
	default:
		a.home.backgroundFit = "cover"
	}
}

func (a *nativeApp) backgroundPositionOptions() []string {
	return []string{"Center", "Top", "Bottom", "Left", "Right", "Top left", "Top right", "Bottom left", "Bottom right"}
}

func (a *nativeApp) backgroundPositionValue() string {
	switch strings.ToLower(a.home.backgroundPosition) {
	case "top":
		return "Top"
	case "bottom":
		return "Bottom"
	case "left":
		return "Left"
	case "right":
		return "Right"
	case "topleft":
		return "Top left"
	case "topright":
		return "Top right"
	case "bottomleft":
		return "Bottom left"
	case "bottomright":
		return "Bottom right"
	default:
		return "Center"
	}
}

func (a *nativeApp) setBackgroundPosition(value string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "top":
		a.home.backgroundPosition = "top"
	case "bottom":
		a.home.backgroundPosition = "bottom"
	case "left":
		a.home.backgroundPosition = "left"
	case "right":
		a.home.backgroundPosition = "right"
	case "top left":
		a.home.backgroundPosition = "topleft"
	case "top right":
		a.home.backgroundPosition = "topright"
	case "bottom left":
		a.home.backgroundPosition = "bottomleft"
	case "bottom right":
		a.home.backgroundPosition = "bottomright"
	default:
		a.home.backgroundPosition = "center"
	}
}

// runtimeBinaryPersistMu guards the shell's persisted binary choice, which is
// read by the ACP launch goroutine and written by the settings UI.
var runtimeBinaryPersistMu sync.RWMutex
