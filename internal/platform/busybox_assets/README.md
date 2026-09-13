# busybox_assets

Vendored Windows shell fallbacks, embedded into the binary.

| File | Architecture |
|---|---|
| `busybox32u.exe` | 32-bit (`GOARCH=386`) |
| `busybox64u.exe` | 64-bit (`GOARCH=amd64`) |

## Source

[`rmyorston/busybox-w32`](https://github.com/rmyorston/busybox-w32) — the
"unicode-enabled" (`*u.exe`) release assets, taken from the latest published
release.

## Use

`internal/platform/busybox_windows.go` embeds both files with `go:embed`,
extracts the one matching the current Windows architecture into the Windows
config `bin` directory on first use, and exposes it as the default shell for the
`bash` tool when no other shell is available.

## Update

1. Download `busybox32u.exe` and `busybox64u.exe` from the latest
   `rmyorston/busybox-w32` release.
2. Replace both files here (keep the existing names).
3. Run `go test ./internal/platform/` and `go build ./...`.

These files are third-party binaries; do not edit them by hand.
