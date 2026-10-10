//go:build windows

package platform

import (
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
// current Windows architecture into the Windows config bin directory when it
// is missing.
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

	dir := filepath.Join(ConfigDir(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create busybox dir: %w", err)
	}

	path := filepath.Join(dir, name)
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("busybox path is a directory: %s", path)
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("check busybox path: %w", err)
	}

	if err := writeAtomic(path, data); err != nil {
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
