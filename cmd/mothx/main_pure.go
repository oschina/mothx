package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/session"
)

// resetSessionsDatabase is the reset entry point. It is a variable only so the
// failure guidance below can be tested deterministically: making the real reset
// fail after it has already archived the database requires an unportable
// filesystem state, not an assertion about this command.
var resetSessionsDatabase = session.ResetDatabase

// newPureCommand creates the `pure` subcommand: it archives the current
// sessions database and starts a brand-new empty one, so the next run begins
// with no sessions, projects, runs, or decisions.
func newPureCommand() *cobra.Command {
	var sessionDir string
	var force bool
	var pruneArtifacts bool
	cmd := &cobra.Command{
		Use:   "pure",
		Short: "Archive the sessions database and start a brand-new empty one",
		Long: "Move the shared sessions.db (and its SQLite sidecars) aside and create a fresh, empty database in its place.\n" +
			"Nothing is deleted: the previous files are renamed next to the new database as sessions.db.pure-<timestamp>.bak[.<suffix>].\n\n" +
			"Only sessions.db is archived. Everything else in the session directory stays where it is, and it stays there for different\n" +
			"reasons: channel directories are separate session roots with their own sessions.db, each knowledge-base file is that base's\n" +
			"own authoritative store, and attachment storage is content the archived database used to describe. Of those, only unreferenced\n" +
			"attachment storage is reclaimable, which the Runtime does automatically once it is past the attachment retention window;\n" +
			"--prune-unreferenced runs that pass immediately. Nothing is ever deleted while it could still be inside that window.\n\n" +
			"Stop every other mothx process first; a database moved while another process still holds it open keeps being written there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pureSessionDir(sessionDir)
			if err != nil {
				return err
			}
			if !force {
				if err := refuseActiveSessionReset(cmd.ErrOrStderr(), dir); err != nil {
					return err
				}
			}
			report, err := resetSessionsDatabase(dir)
			if err != nil {
				// Once files have been archived the user must be told where they are:
				// on Windows the underlying error is typically "Access is denied" from
				// a handle another mothx process holds, which reads like a plain
				// failure and would suggest the data is still where it was.
				if len(report.Archived) > 0 {
					archived := make([]string, 0, len(report.Archived))
					for _, file := range report.Archived {
						archived = append(archived, file.To)
					}
					return fmt.Errorf("reset sessions database in %s failed after archiving %s; the previous files are still recoverable there (stop every other mothx process using this directory and retry): %w", dir, strings.Join(archived, ", "), err)
				}
				return fmt.Errorf("reset sessions database in %s (stop every other mothx process using this directory and retry): %w", dir, err)
			}
			printPureReport(cmd.OutOrStdout(), dir, report)
			if !pruneArtifacts {
				return nil
			}
			return prunePureArtifacts(cmd.OutOrStdout(), dir)
		},
	}
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "Session directory to reset (default: the configured session directory)")
	cmd.Flags().BoolVar(&force, "force", false, "Reset even while another mothx process holds an active session run")
	cmd.Flags().BoolVar(&pruneArtifacts, "prune-unreferenced", false, "Also reclaim attachment storage no longer referenced by any row and already past the retention window")
	return cmd
}

// prunePureArtifacts runs the Runtime-owned private-store reconciliation against
// the session directory and reports what it reclaimed. The command only calls
// and renders the shared API: the retention window, the grace floor, and which
// layout counts as attachment storage all stay in internal/agentruntime.
func prunePureArtifacts(out io.Writer, sessionDir string) error {
	policy := agentruntime.DefaultAttachmentPolicy()
	reconciliation, err := agentruntime.ReconcileArtifactStorage(context.Background(), sessionDir, policy, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("reset succeeded, but reclaiming unreferenced attachment storage in %s failed: %w", sessionDir, err)
	}
	fmt.Fprintf(out, "Reclaimed %d unreferenced attachment %s (%s) older than %s\n",
		reconciliation.Removed,
		pureDirectoryWord(reconciliation.Removed),
		formatPureBytes(reconciliation.Freed),
		reconciliation.AgeFloor.Format(time.RFC3339),
	)
	if reconciliation.SkippedYoung > 0 {
		fmt.Fprintf(out, "Kept %d unreferenced attachment %s written after that floor\n",
			reconciliation.SkippedYoung, pureDirectoryWord(reconciliation.SkippedYoung))
	}
	if reconciliation.SkippedUnrecognized > 0 {
		fmt.Fprintf(out, "Left %d entries untouched because they are not attachment storage this command recognizes\n", reconciliation.SkippedUnrecognized)
	}
	return nil
}

// refuseActiveSessionReset is the preflight that makes the "stop every other
// process" instruction enforceable where the rename itself cannot: a move
// succeeds silently on POSIX even while another process keeps writing the
// archived inode, so the only reliable signal is a lease another process is
// still renewing in the database being archived.
//
// An unreadable lease table is an unknown ownership state, not proof that no
// process is writing. Refuse by default: the explicit --force switch is the
// operator's acknowledgement that archiving a database a peer may still hold is
// acceptable.
func refuseActiveSessionReset(warnings io.Writer, sessionDir string) error {
	holders, err := session.ActiveRuntimeLeases(sessionDir)
	if err != nil {
		message := fmt.Sprintf("cannot safely check for running mothx processes in %s; refusing to reset without --force", sessionDir)
		fmt.Fprintf(warnings, "%s: %v\n", message, err)
		return fmt.Errorf("%s: %w", message, err)
	}
	if len(holders) == 0 {
		return nil
	}
	lines := make([]string, 0, len(holders)+2)
	lines = append(lines, fmt.Sprintf("refusing to reset the sessions database in %s while a session run is active:", sessionDir))
	for _, holder := range holders {
		lines = append(lines, "  - "+holder.Describe())
	}
	lines = append(lines, "Stop those processes first, or pass --force to archive the database anyway.")
	return errors.New(strings.Join(lines, "\n"))
}

// printPureReport states what left the directory, what was created, and what the
// reset deliberately did not touch.
func printPureReport(out io.Writer, sessionDir string, report session.ResetReport) {
	sidecarKind := "orphaned SQLite sidecar"
	if report.DatabaseBackup != "" {
		sidecarKind = "SQLite sidecar"
	}
	for _, archived := range report.Archived {
		if archived.To == report.DatabaseBackup {
			fmt.Fprintf(out, "Moved previous sessions database to: %s\n", archived.To)
			continue
		}
		fmt.Fprintf(out, "Moved %s %s to: %s\n", sidecarKind, filepath.Base(archived.From), archived.To)
	}
	fmt.Fprintf(out, "Created a fresh sessions database: %s\n", session.RootDatabasePath(sessionDir))
	if len(report.LeftBehind) == 0 {
		return
	}
	fmt.Fprintln(out, "Left in place by this reset (they are not part of the sessions database):")
	artifactsReclaimed := false
	for _, entry := range report.LeftBehind {
		name := entry.Name
		if entry.Directory {
			name += string(filepath.Separator)
		}
		fmt.Fprintf(out, "  %-24s %s\n", name, formatPureBytes(entry.Bytes))
		if entry.Name == agentruntime.ArtifactStorageDirectoryName() {
			artifactsReclaimed = true
		}
	}
	if artifactsReclaimed {
		// Only attachment storage has an ownership rule that makes leaving it behind
		// a leak: channel directories and knowledge-base files are authoritative in
		// their own right, so the command offers reclamation for the one that is not.
		fmt.Fprintln(out, "Unreferenced attachment storage is reclaimed automatically once it is past the retention window; --prune-unreferenced runs that pass now.")
	}
}

func formatPureBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value, units := float64(bytes), []string{"KB", "MB", "GB", "TB"}
	for index, unitName := range units {
		value /= unit
		if index == len(units)-1 || value < unit {
			return fmt.Sprintf("%.1f %s", value, unitName)
		}
	}
	return fmt.Sprintf("%d B", bytes)
}

// pureDirectoryWord renders "attachment directory" for a count.
func pureDirectoryWord(count int) string {
	if count == 1 {
		return "directory"
	}
	return "directories"
}

// pureSessionDir resolves the session directory to reset. An explicit flag wins;
// otherwise the configured session directory is used so `pure` targets the same
// database the other entry points would open.
func pureSessionDir(flagValue string) (string, error) {
	if dir := strings.TrimSpace(flagValue); dir != "" {
		return platform.ExpandHome(dir), nil
	}
	settings, err := config.LoadSettingsFor("")
	if err != nil {
		return "", fmt.Errorf("load settings: %w", err)
	}
	return settings.GetSessionDir(), nil
}
