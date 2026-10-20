//go:build windows

package platform

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

func TestBusyboxAssetForArch(t *testing.T) {
	tests := []struct {
		arch string
		want string
		ok   bool
	}{
		{arch: "amd64", want: "busybox-windows-x86_64.exe", ok: true},
		{arch: "386", want: "busybox-windows-x86.exe", ok: true},
		{arch: "arm64", want: "", ok: false},
	}

	for _, tt := range tests {
		gotName, _, gotOK := busyboxAssetForArch(tt.arch)
		if gotName != tt.want || gotOK != tt.ok {
			t.Fatalf("busyboxAssetForArch(%q) = (%q, %v), want (%q, %v)", tt.arch, gotName, gotOK, tt.want, tt.ok)
		}
	}
}

func TestWindowsBusyboxDirUsesConfigBin(t *testing.T) {
	if got := filepath.Join("C:\\Users\\tester\\AppData\\Roaming\\vibecoding", "bin"); got != "C:\\Users\\tester\\AppData\\Roaming\\vibecoding\\bin" {
		t.Fatalf("filepath.Join() = %q", got)
	}
}

func TestEnsureBusyboxAssetExtractsAndRefreshes(t *testing.T) {
	dir := t.TempDir()
	name := "busybox-test.exe"
	v1 := []byte("embedded-v1")
	v2 := []byte("embedded-v2")

	path, err := ensureBusyboxAsset(dir, name, v1)
	if err != nil {
		t.Fatalf("ensureBusyboxAsset() error = %v", err)
	}
	if path != filepath.Join(dir, name) {
		t.Fatalf("ensureBusyboxAsset() path = %q, want %q", path, filepath.Join(dir, name))
	}
	assertFileBytes(t, path, v1)

	// Unchanged content is reused.
	if again, err := ensureBusyboxAsset(dir, name, v1); err != nil || again != path {
		t.Fatalf("ensureBusyboxAsset() = (%q, %v), want (%q, nil)", again, err, path)
	}

	// A modified installed copy is replaced. Keep the same size so the
	// content hash, not the size shortcut, detects the change.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(v1)), 0o755); err != nil {
		t.Fatalf("tamper installed busybox: %v", err)
	}
	if _, err := ensureBusyboxAsset(dir, name, v1); err != nil {
		t.Fatalf("ensureBusyboxAsset() after tamper error = %v", err)
	}
	assertFileBytes(t, path, v1)

	// A new embedded version replaces the old copy.
	if _, err := ensureBusyboxAsset(dir, name, v2); err != nil {
		t.Fatalf("ensureBusyboxAsset() after version change error = %v", err)
	}
	assertFileBytes(t, path, v2)
}

func TestEnsureBusyboxAssetKeepsLockedCopy(t *testing.T) {
	dir := t.TempDir()
	name := "busybox-locked.exe"
	v1 := []byte("embedded-v1")
	path, err := ensureBusyboxAsset(dir, name, v1)
	if err != nil {
		t.Fatalf("ensureBusyboxAsset() error = %v", err)
	}

	namePtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q): %v", path, err)
	}
	handle, err := windows.CreateFile(namePtr, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Skipf("cannot open %q with an exclusive handle: %v", path, err)
	}

	refreshed, refreshErr := ensureBusyboxAsset(dir, name, []byte("embedded-v2"))
	if closeErr := windows.CloseHandle(handle); closeErr != nil {
		t.Fatalf("CloseHandle(): %v", closeErr)
	}
	if refreshErr != nil {
		t.Fatalf("ensureBusyboxAsset() error = %v", refreshErr)
	}
	if refreshed != path {
		t.Fatalf("ensureBusyboxAsset() = %q, want %q", refreshed, path)
	}
	// The locked old copy is kept for this run; the next startup refreshes it.
	assertFileBytes(t, path, v1)
}

func TestEnsureBusyboxAssetRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "busybox.exe"), 0o755); err != nil {
		t.Fatalf("create busybox directory: %v", err)
	}
	if _, err := ensureBusyboxAsset(dir, "busybox.exe", []byte("x")); err == nil {
		t.Fatal("ensureBusyboxAsset() expected an error when the path is a directory")
	}
}

func TestEnsureWindowsBusyboxRefreshesTamperedAsset(t *testing.T) {
	if _, _, ok := busyboxAssetForArch(runtime.GOARCH); !ok {
		t.Skipf("no embedded busybox for %s", runtime.GOARCH)
	}

	t.Setenv("MOTHX_DIR", t.TempDir())
	resetBusyboxCache()
	t.Cleanup(resetBusyboxCache)

	if err := EnsureWindowsBusybox(); err != nil {
		t.Fatalf("EnsureWindowsBusybox() error = %v", err)
	}
	path, ok := WindowsBusyboxPath()
	if !ok {
		t.Fatal("WindowsBusyboxPath() returned false")
	}

	name, data, _ := busyboxAssetForArch(runtime.GOARCH)
	if filepath.Base(path) != name {
		t.Fatalf("extracted busybox name = %q, want %q", filepath.Base(path), name)
	}
	assertFileBytes(t, path, data)

	// Simulate an old/corrupted extracted copy and a process restart.
	if err := os.WriteFile(path, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("tamper extracted busybox: %v", err)
	}
	resetBusyboxCache()

	if err := EnsureWindowsBusybox(); err != nil {
		t.Fatalf("EnsureWindowsBusybox() after tamper error = %v", err)
	}
	refreshed, ok := WindowsBusyboxPath()
	if !ok || refreshed != path {
		t.Fatalf("WindowsBusyboxPath() = (%q, %v), want (%q, true)", refreshed, ok, path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read refreshed busybox: %v", err)
	}
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatal("refreshed busybox content does not match the embedded asset")
	}
}

func resetBusyboxCache() {
	busyboxOnce = sync.Once{}
	busyboxPath = ""
	busyboxErr = nil
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
