# Real-Windows gate for the Serve PTY backend (proposal T1).
#
# ConPTY only exists on Windows 10 1809+ and Windows 11, so this cannot be
# verified from a cross-compile. Run it from a Windows shell with Go installed:
#
#   pwsh -File scripts/verify-conpty.ps1
#
# The script fails when the round-trip test is skipped. A skip means ConPTY is
# unavailable on that host (for example an older Windows build) and is never
# pass evidence.

$ErrorActionPreference = "Stop"

$onWindows = if ($null -ne $IsWindows) {
    $IsWindows
} else {
    [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
}
if (-not $onWindows) {
    throw "This script must run on Windows 10 1809+ or Windows 11; ConPTY does not exist on other platforms."
}

if ($env:GOOS -and $env:GOOS -ne "windows") {
    throw "GOOS=$env:GOOS; run this script on Windows (clear GOOS first)."
}

Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $output = & go test ./internal/terminal -run '^(TestConPTYRoundTrip|TestConPTYCloseIsIdempotent)$' -count=1 -v 2>&1
    $code = $LASTEXITCODE
    $text = ($output | Out-String)
    Write-Host $text

    if ($code -ne 0) {
        throw "ConPTY tests failed (go test exit $code)."
    }
    foreach ($name in @('TestConPTYRoundTrip', 'TestConPTYCloseIsIdempotent')) {
        if ($text -match "--- SKIP: $name") {
            throw "$name was SKIPPED: ConPTY is unavailable on this Windows build. A skip is not pass evidence."
        }
        if ($text -notmatch "--- PASS: $name") {
            throw "$name did not report PASS; the run is not valid evidence."
        }
    }

    Write-Host "ConPTY round-trip and idempotent-close tests passed on this Windows host (start/input/resize/output/exit/close)."
} finally {
    Pop-Location
}
