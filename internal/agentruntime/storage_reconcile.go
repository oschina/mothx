package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

// artifactStorageDirectoryName is the Runtime-private subtree acceptArtifact
// writes into. It is spelled once here and matched against the durable storage
// keys, so the reconciliation and the intake can never disagree about where
// attachment content lives.
const artifactStorageDirectoryName = "artifacts"

const (
	// artifactReconcileGrace is added on top of the attachment retention window
	// before an unreferenced artifact directory may be reclaimed. Without an age
	// floor the sweep could race an intake that has created its directory but not
	// yet committed the durable row, and it would compete with `mothx pure`, whose
	// whole promise is that the archived database can still be restored.
	//
	// The floor is what makes deletion safe in every orphan case: retention is how
	// long a *referenced* object is guaranteed to survive, so bytes the database no
	// longer claims and that have already outlived retention plus a grace period
	// could not be served again even if the archive were restored.
	artifactReconcileGrace = 24 * time.Hour
	// artifactReconcileInterval throttles the opportunistic sweep to at most one
	// pass per interval per process. Reclamation is storage pressure relief, not a
	// per-request duty, so an intake burst must not turn into a directory walk.
	artifactReconcileInterval = time.Hour
)

// lastArtifactReconcile holds the unix-nanos start of the most recent
// opportunistic pass; Swap (not Load/Store) makes "exactly one sweep per
// interval" race-free without holding a lock across a filesystem walk.
var lastArtifactReconcile atomic.Int64

// ArtifactReconciliation reports one private-store reconciliation pass. The
// counters are deliberately explicit: an operator has to be able to tell "the
// store was already clean" apart from "everything was too young to reclaim".
type ArtifactReconciliation struct {
	// Scanned counts the entries found directly under the artifact store.
	Scanned int
	// Removed counts the directories reclaimed in this pass.
	Removed int
	// Freed is the total size of the reclaimed content.
	Freed int64
	// SkippedReferenced counts directories a durable row still claims.
	SkippedReferenced int
	// SkippedYoung counts unreferenced directories still inside the retention
	// plus grace floor.
	SkippedYoung int
	// SkippedUnrecognized counts entries that are not artifact storage this
	// function may interpret - an unexpected name, a file, or a symlink.
	SkippedUnrecognized int
	// AgeFloor is the modification time a directory must be older than to be
	// reclaimable. It is reported so callers can state the rule they applied.
	AgeFloor time.Time
}

// ArtifactStorageDirectoryName returns the session-directory subtree that holds
// Runtime-private attachment content. It is exported so a caller can label what a
// reset left behind without duplicating the layout rule this package owns.
func ArtifactStorageDirectoryName() string { return artifactStorageDirectoryName }

// ArtifactReclaimFloor returns the modification time an unreferenced artifact
// directory must predate before ReconcileArtifactStorage may reclaim it. It is
// exported so a caller can state the rule it applied instead of restating the
// retention and grace window the Runtime owns.
func ArtifactReclaimFloor(policy AttachmentPolicy, now time.Time) time.Time {
	if policy.Retention <= 0 {
		return now
	}
	return now.Add(-(policy.Retention + artifactReconcileGrace))
}

// ReconcileArtifactStorage reclaims artifact directories that no durable
// attachment row references any more and that are already past retention plus the
// grace window. Orphans reach this state through three independent paths - an
// intake whose row write failed after its content was committed, a session
// deletion (which removes rows only), and a sessions database that was archived
// away by `mothx pure` - and none of them is visible to the row-driven expiry
// path, which is why a directory-driven pass is needed beside it.
//
// It fails closed: an unreadable or missing sessions database returns an error
// and deletes nothing, because an empty reference set is indistinguishable from
// a store whose every object is orphaned. Only plain directories named like a
// generated attachment ID, containing only regular files, are ever considered.
func ReconcileArtifactStorage(ctx context.Context, sessionDir string, policy AttachmentPolicy, now time.Time) (ArtifactReconciliation, error) {
	var report ArtifactReconciliation
	if strings.TrimSpace(sessionDir) == "" {
		return report, fmt.Errorf("attachment session directory is required")
	}
	if policy.Retention <= 0 {
		return report, fmt.Errorf("attachment retention must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	report.AgeFloor = ArtifactReclaimFloor(policy, now)

	root := filepath.Join(sessionDir, artifactStorageDirectoryName)
	switch info, err := os.Lstat(root); {
	case errors.Is(err, os.ErrNotExist):
		return report, nil
	case err != nil:
		return report, fmt.Errorf("inspect attachment storage: %w", err)
	case !info.IsDir():
		return report, fmt.Errorf("attachment storage path %s is not a directory", root)
	}

	referenced, err := referencedArtifactDirectories(ctx, sessionDir)
	if err != nil {
		return report, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return report, fmt.Errorf("read attachment storage: %w", err)
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		report.Scanned++
		path := filepath.Join(root, entry.Name())
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !isAttachmentDirectoryID(entry.Name()) {
			report.SkippedUnrecognized++
			continue
		}
		if _, claimed := referenced[entry.Name()]; claimed {
			report.SkippedReferenced++
			continue
		}
		newest, size, err := artifactDirectoryContents(path)
		if err != nil {
			// Anything this pass cannot interpret is left alone: an unexpected
			// layout is not evidence that a directory is disposable.
			report.SkippedUnrecognized++
			continue
		}
		if !newest.Before(report.AgeFloor) {
			report.SkippedYoung++
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return report, fmt.Errorf("reclaim unreferenced attachment storage %s: %w", path, err)
		}
		report.Removed++
		report.Freed += size
	}
	return report, nil
}

// referencedArtifactDirectories returns every artifact directory a durable row
// still claims, keyed by directory name. Both the row ID and the storage key are
// consulted so an object stays protected even if the two disagree (a legacy or
// hand-edited key never becomes a reason to delete content).
func referencedArtifactDirectories(ctx context.Context, sessionDir string) (map[string]struct{}, error) {
	dbPath := session.RootDatabasePath(sessionDir)
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("sessions database %s does not exist; refusing to reconcile attachment storage against an unknown reference set", dbPath)
		}
		return nil, fmt.Errorf("inspect sessions database: %w", err)
	}
	referenced := map[string]struct{}{}
	err := session.QueryRootDatabase(sessionDir, func(db *dao.Database) error {
		records, err := dao.NewAttachmentDAO(db.Bun()).ListStorageReferences(ctx, db.Bun())
		if err != nil {
			return err
		}
		for _, record := range records {
			if id := strings.TrimSpace(record.ID); id != "" {
				referenced[id] = struct{}{}
			}
			key := filepath.ToSlash(strings.TrimSpace(record.StorageKey))
			if parts := strings.Split(key, "/"); len(parts) >= 2 && parts[0] == artifactStorageDirectoryName {
				referenced[parts[1]] = struct{}{}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read attachment storage references: %w", err)
	}
	return referenced, nil
}

// artifactDirectoryContents measures one artifact directory without following
// symbolic links and rejects any layout the Runtime does not write itself: the
// committed `content` object, or the `.incoming-*` temporary of an interrupted
// intake. A subdirectory or an unrelated file makes the whole directory
// unrecognized, which is reported as skipped rather than deleted.
func artifactDirectoryContents(dir string) (newest time.Time, size int64, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, 0, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return time.Time{}, 0, err
		}
		acceptable := !entry.IsDir() && info.Mode()&os.ModeSymlink == 0 &&
			(entry.Name() == "content" || strings.HasPrefix(entry.Name(), ".incoming-"))
		if !acceptable {
			return time.Time{}, 0, fmt.Errorf("%s contains unexpected entry %s", dir, entry.Name())
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		size += info.Size()
	}
	if newest.IsZero() {
		// An empty directory is a leftover of a failed intake, and its age is the
		// directory's own modification time.
		info, err := os.Stat(dir)
		if err != nil {
			return time.Time{}, 0, err
		}
		newest = info.ModTime()
	}
	return newest, size, nil
}

// isAttachmentDirectoryID reports whether name is the shape acceptArtifact
// generates (a 16-character lowercase hex identifier). Requiring it keeps the
// sweep from ever treating a directory someone else placed in the session
// directory as disposable storage.
func isAttachmentDirectoryID(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, character := range name {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// ReconcileStorage runs the private-store reconciliation for this service's own
// session directory under its own policy. Adapters call it to report or reclaim;
// they never walk the store themselves.
func (s *AttachmentService) ReconcileStorage(ctx context.Context, now time.Time) (ArtifactReconciliation, error) {
	if s == nil {
		return ArtifactReconciliation{}, fmt.Errorf("attachment service is nil")
	}
	return ReconcileArtifactStorage(ctx, s.sessionDir, s.policy, now)
}

// reconcileArtifactStorageOpportunistic performs the sweep at most once per
// artifactReconcileInterval per process. It is reached from the attachment intake
// path, which is the same place expiry cleanup already runs, because reclamation
// is background maintenance rather than part of accepting an attachment: its
// outcome never changes whether the new attachment succeeds.
func reconcileArtifactStorageOpportunistic(sessionDir string, policy AttachmentPolicy) {
	now := time.Now()
	previous := lastArtifactReconcile.Swap(now.UnixNano())
	if previous != 0 && now.Sub(time.Unix(0, previous)) < artifactReconcileInterval {
		return
	}
	_, _ = ReconcileArtifactStorage(context.Background(), sessionDir, policy, now.UTC())
}
