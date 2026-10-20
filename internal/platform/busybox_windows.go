//go:build windows

package platform

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

//go:embed busybox_assets/busybox-windows-x86.exe
var busyboxX86 []byte

//go:embed busybox_assets/busybox-windows-x86_64.exe
var busyboxX8664 []byte

var (
	busyboxOnce sync.Once
	busyboxPath string
	busyboxErr  error
)

// EnsureWindowsBusybox extracts the embedded BusyBox executable for the
// current Windows architecture into the Windows config bin directory. A
// previously extracted copy is replaced when its contents differ from the
// embedded asset, so upgrades do not keep using a stale shell.
func EnsureWindowsBusybox() error {
	busyboxOnce.Do(func() {
		busyboxPath, busyboxErr = ensureWindowsBusybox()
	})
	return busyboxErr
}

// WindowsBusyboxPath returns the extracted BusyBox path when available.
func WindowsBusyboxPath() (string, bool) {
	if err := EnsureWindowsBusybox(); err != nil {
		return "", false
	}
	if busyboxPath == "" {
		return "", false
	}
	return busyboxPath, true
}

func ensureWindowsBusybox() (string, error) {
	name, data, ok := busyboxAssetForArch(runtime.GOARCH)
	if !ok {
		return "", nil
	}
	return ensureBusyboxAsset(filepath.Join(ConfigDir(), "bin"), name, data)
}

// ensureBusyboxAsset installs data as dir/name and returns its path. The
// existing file is compared by content hash, so replacing an embedded BusyBox
// does not leave an old extracted copy behind.
func ensureBusyboxAsset(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create busybox dir: %w", err)
	}

	path := filepath.Join(dir, name)
	want := sha256.Sum256(data)

	info, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		if info.IsDir() {
			return "", fmt.Errorf("busybox path is a directory: %s", path)
		}
		if info.Size() == int64(len(data)) {
			if current, readErr := os.ReadFile(path); readErr == nil && sha256.Sum256(current) == want {
				return path, nil
			}
		}
	case !errors.Is(statErr, os.ErrNotExist):
		return "", fmt.Errorf("check busybox path: %w", statErr)
	}

	if err := writeAtomic(path, data); err != nil {
		// A running BusyBox can keep its executable image locked. Keep using
		// the old copy rather than breaking the shell; the next startup will
		// retry the refresh.
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			return path, nil
		}
		return "", err
	}
	return path, nil
}

func busyboxAssetForArch(arch string) (name string, data []byte, ok bool) {
	switch arch {
	case "amd64":
		return "busybox-windows-x86_64.exe", busyboxX8664, true
	case "386":
		return "busybox-windows-x86.exe", busyboxX86, true
	default:
		return "", nil, false
	}
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".busybox-*")
	if err != nil {
		return fmt.Errorf("create temp busybox file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write busybox file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close busybox file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod busybox file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install busybox file: %w", err)
	}
	return nil
}
