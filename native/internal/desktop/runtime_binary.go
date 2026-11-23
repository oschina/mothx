package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// launchBinaryOverride is the executable named by the Native shell's own
// launch arguments (--mothx-bin). It is presentation-neutral launch state: ACP
// never sees it, no settings file stores it, and it is only resolved once at
// startup. It intentionally outranks MOTHX_BIN because an explicit shell
// argument is more specific than the inherited environment.
var launchBinaryOverride string

// runtimeBinaryPreference is the shell's persisted binary choice. Like the
// launch flag it is client launch state: ACP and settings.json never own it.
var runtimeBinaryPreference string

func getRuntimeBinaryPreference() string {
	runtimeBinaryPersistMu.RLock()
	defer runtimeBinaryPersistMu.RUnlock()
	return runtimeBinaryPreference
}

func setRuntimeBinaryPreference(path string, persist bool) {
	runtimeBinaryPersistMu.Lock()
	runtimeBinaryPreference = strings.TrimSpace(path)
	runtimeBinaryPersistMu.Unlock()
	if persist {
		if app := activeNativeApp; app != nil {
			app.saveNativePreferences()
		}
	}
}

var activeNativeApp *nativeApp

// setLaunchBinaryOverride records the shell's binary choice. It is called once
// from main before any ACP connection is attempted.
func setLaunchBinaryOverride(path string) {
	launchBinaryOverride = strings.TrimSpace(path)
}

// parseLaunchBinaryArg extracts --mothx-bin from the shell arguments. It
// accepts both "--mothx-bin PATH" and "--mothx-bin=PATH" and never consumes
// another flag as a value.
func parseLaunchBinaryArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--mothx-bin=") {
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--mothx-bin="))
			return value, value != ""
		}
		if arg != "--mothx-bin" {
			continue
		}
		if i+1 >= len(args) {
			return "", false
		}
		value := strings.TrimSpace(args[i+1])
		if value == "" || strings.HasPrefix(value, "--") {
			return "", false
		}
		return value, true
	}
	return "", false
}

// resolveMothxBinary finds the mothx executable this app drives. An explicit
// --mothx-bin launch argument wins; then MOTHX_BIN; then PATH; then the
// repository's bin/ directory, so running the app from a checkout works
// without setup.
func resolveMothxBinary() string {
	bin, _ := resolveMothxBinarySource()
	return bin
}

// resolveMothxBinarySource returns the executable and the launch-time source
// that selected it. The settings Runtime page renders this verbatim instead of
// pretending the choice is a persistent Native preference.
func resolveMothxBinarySource() (bin, source string) {
	if launchBinaryOverride != "" {
		if abs, err := filepath.Abs(launchBinaryOverride); err == nil {
			return abs, "启动参数 --mothx-bin"
		}
		return launchBinaryOverride, "启动参数 --mothx-bin"
	}
	if preference := getRuntimeBinaryPreference(); preference != "" {
		if abs, err := filepath.Abs(preference); err == nil {
			return abs, "Native 设置"
		}
		return preference, "Native 设置"
	}
	if p := os.Getenv("MOTHX_BIN"); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, "MOTHX_BIN"
		}
		return p, "MOTHX_BIN"
	}
	if p, err := exec.LookPath("mothx"); err == nil {
		return p, "PATH"
	}
	candidates := []string{"bin/mothx", "../bin/mothx", "../../bin/mothx", "../../../bin/mothx"}
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return abs, "仓库 bin/"
		}
	}
	return "", ""
}
