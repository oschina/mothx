# busybox_assets

Vendored Windows shell fallbacks, embedded into the binary.

| File | Architecture |
|---|---|
| `busybox-windows-x86.exe` | 32-bit (`GOARCH=386`) |
| `busybox-windows-x86_64.exe` | 64-bit (`GOARCH=amd64`) |

## Source

[`startvibecoding/agentbusybox`](https://github.com/startvibecoding/agentbusybox) —
Windows release assets, taken from the latest published release.

## Use

`internal/platform/busybox_windows.go` embeds both files with `go:embed`,
extracts the one matching the current Windows architecture into the Windows
config `bin` directory on first use, and exposes it as the default shell for the
`bash` tool when no other shell is available.

## Update

1. Download `busybox-windows-x86.exe` and `busybox-windows-x86_64.exe` from
   the latest `startvibecoding/agentbusybox` release.
2. Replace both files here with the same names.
3. Run `go test ./internal/platform/` and `go build ./...`.

These files are third-party binaries; do not edit them by hand.
