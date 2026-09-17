package session

import (
	"errors"
	"fmt"
	"os"
	"time"

	database "github.com/startvibecoding/mothx/internal/db"
	"github.com/startvibecoding/mothx/internal/platform"
)

// databaseSidecarSuffixes lists the SQLite sidecar files that belong to a
// sessions.db. They are moved together with the database so a freshly created
// file can never inherit a stale write-ahead log or shared-memory index.
var databaseSidecarSuffixes = []string{"-wal", "-shm", "-journal"}

// ResetDatabase moves the shared sessions database aside and creates a fresh,
// migrated one in its place. The previous database and its sidecars are renamed
// (never deleted) next to the new file, so the old sessions remain recoverable.
// The returned backup path is empty when there was no database to move.
//
// Callers must ensure no other process is running against the session
// directory: a database moved while another process still holds it open keeps
// being written there, and this process cannot detect that.
func ResetDatabase(sessionDir string) (string, error) {
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	dbPath := rootDBPath(sessionDir)

	// Retire the connection this process may hold so the file is not renamed
	// while open. CloseAll folds committed WAL frames into the main file first.
	if err := database.CloseAll(); err != nil {
		return "", fmt.Errorf("close session databases: %w", err)
	}

	backupPath := ""
	switch _, err := os.Stat(dbPath); {
	case err == nil:
		backup, err := resetBackupPath(dbPath)
		if err != nil {
			return "", err
		}
		if err := moveDatabaseFiles(dbPath, backup); err != nil {
			return "", err
		}
		backupPath = backup
	case errors.Is(err, os.ErrNotExist):
		// Nothing to move; the database created below is the first one.
	default:
		return "", fmt.Errorf("inspect sessions database: %w", err)
	}

	if _, err := OpenRootDB(sessionDir); err != nil {
		return backupPath, fmt.Errorf("create fresh sessions database: %w", err)
	}
	if err := CloseDatabases(); err != nil {
		return backupPath, fmt.Errorf("close fresh sessions database: %w", err)
	}
	return backupPath, nil
}

// moveDatabaseFiles renames a database and its sidecars to backupPath. The main
// file moves first because it is what defines the database; if a sidecar cannot
// be moved, the whole move is rolled back so the directory is never left with a
// database whose WAL was renamed away from it.
func moveDatabaseFiles(dbPath, backupPath string) error {
	if err := os.Rename(dbPath, backupPath); err != nil {
		return fmt.Errorf("move sessions database: %w", err)
	}
	moved := make([]string, 0, len(databaseSidecarSuffixes))
	for _, suffix := range databaseSidecarSuffixes {
		source := dbPath + suffix
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return rollbackDatabaseMove(dbPath, backupPath, moved, fmt.Errorf("inspect %s: %w", source, err))
		}
		if err := os.Rename(source, backupPath+suffix); err != nil {
			return rollbackDatabaseMove(dbPath, backupPath, moved, fmt.Errorf("move %s: %w", source, err))
		}
		moved = append(moved, suffix)
	}
	return nil
}

func rollbackDatabaseMove(dbPath, backupPath string, moved []string, cause error) error {
	errs := []error{cause}
	for i := len(moved) - 1; i >= 0; i-- {
		suffix := moved[i]
		if err := os.Rename(backupPath+suffix, dbPath+suffix); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", dbPath+suffix, err))
		}
	}
	if err := os.Rename(backupPath, dbPath); err != nil {
		errs = append(errs, fmt.Errorf("restore %s: %w", dbPath, err))
	}
	return errors.Join(errs...)
}

// resetBackupPath returns an unused backup name next to the database so the
// moved file stays in the same directory as the data it preserves.
func resetBackupPath(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for attempt := 0; ; attempt++ {
		candidate := path + ".pure-" + stamp + ".bak"
		if attempt > 0 {
			candidate = fmt.Sprintf("%s.pure-%s-%d.bak", path, stamp, attempt)
		}
		_, err := os.Stat(candidate)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return candidate, nil
		case err != nil:
			return "", fmt.Errorf("check backup path %s: %w", candidate, err)
		case attempt >= 1000:
			return "", fmt.Errorf("no free backup path for %s", path)
		}
	}
}
