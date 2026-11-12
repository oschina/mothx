package main

import (
	"runtime/debug"
	"strings"
)

// nativeBuildVersion is the fallback version shown before the ACP Runtime
// reports its own. Release builds set it to the MothX version with
// -ldflags "-X main.nativeBuildVersion=..."; source builds fall back to the
// same embedded VCS revision internal/version uses.
var nativeBuildVersion = nativeVersionFromBuildInfo()

func nativeVersionFromBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if value := strings.TrimSpace(info.Main.Version); value != "" && value != "(devel)" {
		return value
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			modified = strings.TrimSpace(setting.Value)
		}
	}
	if revision == "" {
		return "unknown"
	}
	if modified == "true" {
		return revision + "-dirty"
	}
	return revision
}

// version follows the connected MothX Runtime, falling back to the build
// version before initialize completes. A leading "v" is stripped because the
// shell adds it where it shows a release tag, as Desktop's title bar does.
func (a *nativeApp) version() string {
	value := strings.TrimSpace(a.runtimeVersion)
	if value == "" {
		value = nativeBuildVersion
	}
	return strings.TrimPrefix(value, "v")
}
