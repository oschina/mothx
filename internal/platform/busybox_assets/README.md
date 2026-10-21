# busybox_assets

Vendored Windows shell fallbacks, embedded into the binary.

| File | Architecture |
|---|---|
| `busybox-windows-x86.exe` | 32-bit (`GOARCH=386`) |
| `busybox-windows-x86_64.exe` | 64-bit (`GOARCH=amd64`) |

## Source

[`startvibecoding/agentbusybox`](https://github.com/startvibecoding/agentbusybox) —
Windows release assets. The current files are built from the fork's **UTF-8
configs** (`mingw64u_defconfig` for x86_64 via `./build.sh win64`,
`mingw32w_defconfig` for x86): UTF8 manifest (`activeCodePage=UTF-8`),
`FEATURE_UTF8_INPUT`, `FEATURE_UTF8_CONSOLE`, `FEATURE_UTF8_AUTODETECT`,
`FEATURE_UTF8_CHILD_ENV` (`LC_ALL`/`LANG=C.UTF-8`, `PYTHONIOENCODING=utf-8`)
and `UNICODE_SUPPORT`. The UTF8 manifest makes the process ACP UTF-8, so
non-ASCII `sh -c` command text from MothX flows through argv as UTF-8 instead
of being downconverted to the system ANSI code page (GBK on zh-CN Windows).
These binaries require Windows 10+ (`FEATURE_FAIL_IF_UTF8_MANIFEST_UNSUPPORTED=y`).
Replace them with the matching UTF-8 assets from the next published release.

## Use

`internal/platform/busybox_windows.go` embeds both files with `go:embed`,
extracts the one matching the current Windows architecture into the Windows
config `bin` directory on first use, and exposes it as the default shell for the
`bash` tool when no other shell is available.

MothX does not rely on the shell for arbitrary child-process encoding. Captured
stdout/stderr is normalized at the tool boundary by
`platform.DecodeProcessOutput`; the shell's UTF-8 environment baseline is a
best-effort optimization for tools which honor it.

## Update

1. Download `busybox-windows-x86.exe` and `busybox-windows-x86_64.exe` from
   the latest `startvibecoding/agentbusybox` release.
2. Replace both files here with the same names.
3. Run `go test ./internal/platform/` and `go build ./...`.

Existing extracted copies under `%APPDATA%\mothx\bin\` are compared by
SHA-256 with the embedded asset and replaced on the next startup when they
differ. If a running BusyBox holds the executable image locked, MothX keeps
using the old copy for that run and retries the refresh on the next startup.

These files are third-party binaries; do not edit them by hand.
