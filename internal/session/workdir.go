package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateWorkDir verifies that a durable session work directory exists now.
// Authorization remains an entry-point policy concern; this boundary only
// prevents a session header from retaining an unusable filesystem path.
func ValidateWorkDir(workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return "", fmt.Errorf("session work directory is required")
	}
	if !filepath.IsAbs(workDir) {
		return "", fmt.Errorf("session work directory must be an absolute path")
	}
	workDir = filepath.Clean(workDir)
	info, err := os.Stat(workDir)
	if err != nil {
		return "", fmt.Errorf("session work directory is unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("session work directory is not a directory")
	}
	return workDir, nil
}
