package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	database "github.com/startvibecoding/mothx/internal/db"
	"github.com/startvibecoding/mothx/internal/platform"
)

// databaseSidecarSuffixes lists the SQLite sidecar files that belong to a
// sessions.db. They are moved together with the database so a freshly created
// file can never inherit a stale write-ahead log or shared-memory index.
var databaseSidecarSuffixes = []string{"-wal", "-shm", "-journal"}

// ResetReport describes what one sessions database reset did, and what it
// deliberately did not do.
type ResetReport struct {
	// DatabaseBackup is the renamed sessions.db. It is empty when no database
	// file existed before the reset.
	DatabaseBackup string
	// Archived lists every file the reset moved aside, database first then each
	// sidecar, so a caller can report what left the directory without deriving
	// the archive naming itself.
	Archived []ArchivedFile
	// LeftBehind lists the session-directory entries a reset does not archive.
	// Attachment, channel, and knowledge-base storage each derive their own
	// paths elsewhere, so the reset reports everything beside sessions.db
	// instead of restating those rules.
	LeftBehind []LeftBehindEntry
}

// ArchivedFile is one sessions.db file a reset renamed.
type ArchivedFile struct {
	// From is the path the file had in the session directory.
	From string
	// To is the archive path it was renamed to, beside From.
	To string
}

// LeftBehindEntry is one session-directory entry that outlives a reset.
type LeftBehindEntry struct {
	// Name is the entry name relative to the session directory.
	Name string
	// Directory reports whether the entry is a directory.
	Directory bool
	// Bytes is the entry size, summed recursively for directories. Symbolic
	// links are not followed.
	Bytes int64
}

// ResetDatabase moves the shared sessions database aside and creates a fresh,
// migrated one in its place. The previous database and its sidecars are renamed
// (never deleted) next to the new file, so the old sessions remain recoverable.
// A sidecar that outlived its database - an interrupted reset, or a database
// deleted by hand - is archived under the same name as well: leaving it in
// place would let the fresh database inherit it and make SQLite's own handling
// of the leftover decide whether startup succeeds.
//
// Callers must ensure no other process is running against the session
// directory: a database moved while another process still holds it open keeps
// being written there, and this process cannot detect that. ActiveRuntimeLeases
// reports the processes known to hold a run in the database being archived.
func ResetDatabase(sessionDir string) (ResetReport, error) {
	var report ResetReport
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	dbPath := rootDBPath(sessionDir)

	// Retire the connection this process may hold so the file is not renamed
	// while open. CloseAll folds committed WAL frames into the main file first.
	if err := database.CloseAll(); err != nil {
		return report, fmt.Errorf("close session databases: %w", err)
	}

	sources, err := existingDatabaseFiles(dbPath)
	if err != nil {
		return report, err
	}
	if len(sources) > 0 {
		backup, err := resetBackupPath(dbPath)
		if err != nil {
			return report, err
		}
		archived, err := moveDatabaseFiles(dbPath, sources, backup)
		if err != nil {
			return report, err
		}
		report.Archived = archived
		if sources[0] == dbPath {
			report.DatabaseBackup = backup
		}
	}

	if _, err := OpenRootDB(sessionDir); err != nil {
		return report, fmt.Errorf("create fresh sessions database: %w", err)
	}
	if err := CloseDatabases(); err != nil {
		return report, fmt.Errorf("close fresh sessions database: %w", err)
	}
	leftBehind, err := leftBehindEntries(sessionDir, dbPath)
	if err != nil {
		return report, err
	}
	report.LeftBehind = leftBehind
	return report, nil
}

// existingDatabaseFiles returns the sessions database and every sidecar
// currently present, ordered so the main file comes first. Absent files are
// skipped; any other stat outcome is fatal, because guessing here would mean
// moving a database this process cannot see.
func existingDatabaseFiles(dbPath string) ([]string, error) {
	found := make([]string, 0, 1+len(databaseSidecarSuffixes))
	switch _, err := os.Stat(dbPath); {
	case err == nil:
		found = append(found, dbPath)
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("inspect sessions database: %w", err)
	}
	for _, suffix := range databaseSidecarSuffixes {
		sidecar := dbPath + suffix
		switch _, err := os.Stat(sidecar); {
		case err == nil:
			found = append(found, sidecar)
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, fmt.Errorf("inspect %s: %w", sidecar, err)
		}
	}
	return found, nil
}

// moveDatabaseFiles renames every existing sessions.db file to the backup name.
// sources starts with the main file, so the database is never separated from its
// WAL even momentarily. A file that cannot move rolls the whole move back.
func moveDatabaseFiles(dbPath string, sources []string, backupPath string) ([]ArchivedFile, error) {
	moved := make([]ArchivedFile, 0, len(sources))
	for _, source := range sources {
		destination := backupPath + strings.TrimPrefix(source, dbPath)
		if err := os.Rename(source, destination); err != nil {
			return rollbackDatabaseMove(dbPath, sources, moved, fmt.Errorf("move %s: %w", source, err))
		}
		moved = append(moved, ArchivedFile{From: source, To: destination})
	}
	return moved, nil
}

// rollbackDatabaseMove restores an interrupted move, pairing each archived file
// with the source it came from. The main database goes back before any sidecar:
// a stale -wal or -journal beside a missing sessions.db is precisely the orphan
// state a reset exists to clear, so a rollback must never leave one behind.
// The returned slice lists the sources that were successfully restored.
func rollbackDatabaseMove(dbPath string, sources []string, moved []ArchivedFile, cause error) ([]ArchivedFile, error) {
	errs := []error{cause}
	restored := make([]ArchivedFile, 0, len(moved))
	restore := func(index int) {
		source, archived := sources[index], moved[index].To
		if err := os.Rename(archived, source); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", source, err))
			return
		}
		restored = append(restored, ArchivedFile{From: archived, To: source})
	}
	start := 0
	if len(moved) > 0 && sources[0] == dbPath {
		restore(0)
		start = 1
	}
	for index := len(moved) - 1; index >= start; index-- {
		restore(index)
	}
	return restored, errors.Join(errs...)
}

// leftBehindEntries lists what stays in the session directory after a reset.
// Everything named like sessions.db (the fresh file, its sidecars, and the
// archives just created) belongs to the reset itself and is not reported.
func leftBehindEntries(sessionDir, dbPath string) ([]LeftBehindEntry, error) {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("inspect session directory: %w", err)
	}
	prefix := filepath.Base(dbPath)
	leftBehind := make([]LeftBehindEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		item := LeftBehindEntry{Name: entry.Name(), Directory: entry.IsDir()}
		size, err := entryBytes(filepath.Join(sessionDir, entry.Name()), entry)
		if err != nil {
			return nil, err
		}
		item.Bytes = size
		leftBehind = append(leftBehind, item)
	}
	return leftBehind, nil
}

// entryBytes measures one entry without following symbolic links, so reporting
// what a reset left behind can never be turned into an unbounded walk.
func entryBytes(path string, entry os.DirEntry) (int64, error) {
	if !entry.IsDir() {
		info, err := entry.Info()
		if err != nil {
			return 0, fmt.Errorf("inspect %s: %w", path, err)
		}
		return info.Size(), nil
	}
	var total int64
	err := filepath.WalkDir(path, func(child string, childEntry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if childEntry.IsDir() {
			return nil
		}
		info, err := childEntry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", path, err)
	}
	return total, nil
}

// resetBackupPath returns an unused backup name next to the database so the
// moved file stays in the same directory as the data it preserves. The sidecar
// archives are derived from the same name, so they are checked too: an occupied
// sidecar destination would fail the move the name was chosen for.
func resetBackupPath(path string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for attempt := 0; ; attempt++ {
		candidate := path + ".pure-" + stamp + ".bak"
		if attempt > 0 {
			candidate = fmt.Sprintf("%s.pure-%s-%d.bak", path, stamp, attempt)
		}
		available, err := backupPathAvailable(candidate)
		if err != nil {
			return "", err
		}
		if available {
			return candidate, nil
		}
		if attempt >= 1000 {
			return "", fmt.Errorf("no free backup path for %s", path)
		}
	}
}

// backupPathAvailable reports whether neither the candidate name nor any of its
// sidecar derivatives exists yet.
func backupPathAvailable(candidate string) (bool, error) {
	names := make([]string, 0, 1+len(databaseSidecarSuffixes))
	names = append(names, candidate)
	for _, suffix := range databaseSidecarSuffixes {
		names = append(names, candidate+suffix)
	}
	for _, name := range names {
		switch _, err := os.Stat(name); {
		case err == nil:
			return false, nil
		case errors.Is(err, os.ErrNotExist):
		default:
			return false, fmt.Errorf("check backup path %s: %w", name, err)
		}
	}
	return true, nil
}
