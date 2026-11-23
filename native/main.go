package main

import "github.com/oschina/mothx/native/internal/desktop"

// NativeBuildVersion is the release version injected by the build:
// -ldflags "-X main.NativeBuildVersion=...". The private desktop package keeps
// its own build-info fallback for source builds, and the entry point only
// forwards the release value when one was supplied.
var NativeBuildVersion string

func main() {
	if NativeBuildVersion != "" {
		desktop.NativeBuildVersion = NativeBuildVersion
	}
	desktop.Run()
}
