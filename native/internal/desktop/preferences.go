package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	HomeBackgroundScope    string   `json:"homeBackgroundScope,omitempty"`
	HomeBackgroundOpacity  *float64 `json:"homeBackgroundOpacity,omitempty"`
	HomeBackgroundBlur     float64  `json:"homeBackgroundBlur,omitempty"`
	HomeBackgroundFit      string   `json:"homeBackgroundFit,omitempty"`
	HomeBackgroundPosition string   `json:"homeBackgroundPosition,omitempty"`
	RuntimeBinary          string   `json:"runtimeBinary,omitempty"`
	Locale                 string   `json:"locale,omitempty"`
	// LastWorkspace/RecentWorkspaces are picker conveniences only, matching
	// Desktop's desktop-store boundary: the session cwd stays Runtime-owned
	// and a remembered directory never becomes a session fallback.
	LastWorkspace    string   `json:"lastWorkspace,omitempty"`
	RecentWorkspaces []string `json:"recentWorkspaces,omitempty"`
}

type homeAppearance struct {
	preset             string
	logoVisible        bool
	logoPath           string
	logoImage          ui.ImageSource
	backgroundPath     string
	backgroundScope    string
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
		backgroundScope:    "app",
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
		HomeBackgroundScope:    a.home.backgroundScope,
		HomeBackgroundOpacity:  &a.home.backgroundOpacity,
		HomeBackgroundBlur:     a.home.backgroundBlur,
		HomeBackgroundFit:      a.home.backgroundFit,
		HomeBackgroundPosition: a.home.backgroundPosition,
		RuntimeBinary:          runtimeBinaryPreference,
		Locale:                 CurrentLocale(),
		LastWorkspace:          a.lastWorkspace,
		RecentWorkspaces:       a.recentWorkspaces,
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
	switch strings.ToLower(strings.TrimSpace(prefs.HomeBackgroundScope)) {
	case "app", "home":
		a.home.backgroundScope = strings.ToLower(strings.TrimSpace(prefs.HomeBackgroundScope))
	}
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
	switch strings.ToLower(strings.TrimSpace(prefs.Locale)) {
	case "zh", "en":
		SetNativeLocale(strings.ToLower(strings.TrimSpace(prefs.Locale)))
	}
	a.lastWorkspace = strings.TrimSpace(prefs.LastWorkspace)
	a.recentWorkspaces = nil
	for _, entry := range prefs.RecentWorkspaces {
		if entry = strings.TrimSpace(entry); entry != "" {
			a.recentWorkspaces = append(a.recentWorkspaces, entry)
		}
	}
	if len(a.recentWorkspaces) > maxRecentWorkspaces {
		a.recentWorkspaces = a.recentWorkspaces[:maxRecentWorkspaces]
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

// runtimeBinaryPersistMu guards the shell's persisted binary choice, which is
// read by the ACP launch goroutine and written by the settings UI.
var runtimeBinaryPersistMu sync.RWMutex

// --- New-session directory history ------------------------------------------
//
// Desktop keeps the picker's last directory and a bounded recent list in its
// client-local store; they are conveniences for the native directory dialog,
// never a session cwd fallback. Native keeps the same presentation state in
// its own preferences file.

const maxRecentWorkspaces = 12

// recordWorkspace remembers an explicitly selected directory for the picker.
// It does not touch any session: session cwd changes go through setWorkDir.
func (a *nativeApp) recordWorkspace(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	a.lastWorkspace = path
	recent := make([]string, 0, maxRecentWorkspaces)
	recent = append(recent, path)
	for _, entry := range a.recentWorkspaces {
		if entry != path && len(recent) < maxRecentWorkspaces {
			recent = append(recent, entry)
		}
	}
	a.recentWorkspaces = recent
	a.saveNativePreferences()
}

// quickStartDirectoryTimestamp mirrors Desktop's platform-safe scratch name
// (no ':' so Windows paths stay valid).
func quickStartDirectoryTimestamp(now time.Time) string {
	return now.Format("20060102_150405")
}

// createQuickStartDirectory creates the launch quick-start scratch directory
// under ~/MothX, matching Desktop's initialNewSessionDirectory. A failure
// returns "" so startup keeps the process cwd instead of blocking.
func createQuickStartDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	parent := filepath.Join(home, "MothX")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return ""
	}
	dir, err := os.MkdirTemp(parent, "mothx_projects_"+quickStartDirectoryTimestamp(time.Now())+"_")
	if err != nil {
		return ""
	}
	return dir
}

// initQuickStartWorkspace gives the first draft of a fresh process a private
// scratch directory, like Desktop's quick-start. Every later session still
// supplies its own cwd through ACP; the scratch is only the initial default.
func (a *nativeApp) initQuickStartWorkspace() {
	dir := createQuickStartDirectory()
	if dir == "" {
		return
	}
	a.cwd = dir
	a.workspace = dir
	a.draftCwd = dir
	a.draftSource = "quick-start"
}

// --- App-wide background ----------------------------------------------------

// nativeBackgroundVeilAlpha is the theme-colored scrim over a user background.
// Acceptance renders over a busy photo-like image showed 0.52 left transcript
// text under-contrasted on bright regions; 0.66 keeps the image visible while
// body text stays readable in both themes.
const nativeBackgroundVeilAlpha = 0.66

// appBackgroundActive reports whether the user background paints the whole
// shell (Desktop's homeBackgroundScope=app) instead of only the Home view.
func (a *nativeApp) appBackgroundActive() bool {
	return a.home.backgroundScope != "home" && a.home.backgroundImage != nil
}
