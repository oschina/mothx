package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/session"
)

// newMaintenanceCommand exposes the Runtime-owned session-storage maintenance
// pass as an explicit one-shot operation. The same work runs daily through the
// shared cron lifecycle; this entry point exists for operators who want to
// inspect or reclaim space now without enabling the scheduled job.
func newMaintenanceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "maintenance",
		Short: "Inspect or compact the session database without deleting messages",
		Long: "Session-storage maintenance never deletes transcript messages. Transcript rows (entries and sub_entries) are only counted; " +
			"the scheduled Runtime job separately bounds completed tool-execution idempotency records and can backfill the derived search index.\n\n" +
			"These commands operate on the shared sessions.db and are safe to run while MothX is idle. VACUUM rewrites the database file, " +
			"needs free disk space, and holds the SQLite writer for the duration of the rewrite.",
	}
	cmd.AddCommand(newMaintenanceStatusCommand(), newMaintenanceVacuumCommand())
	return cmd
}

func newMaintenanceStatusCommand() *cobra.Command {
	var sessionDir string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show session-database size, transcript counts, and reclaimable free pages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := maintenanceSessionDir(sessionDir)
			if err != nil {
				return err
			}
			if !sessionDatabaseExists(dir) {
				fmt.Fprintf(cmd.OutOrStdout(), "No session database at %s.\n", dir)
				return nil
			}
			stats, err := session.InspectSessionStorage(cmd.Context(), dir)
			if err != nil {
				return fmt.Errorf("inspect session storage: %w", err)
			}
			printSessionStorageStatus(cmd.OutOrStdout(), dir, stats)
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "Session directory to inspect (default: the configured session directory)")
	return cmd
}

func newMaintenanceVacuumCommand() *cobra.Command {
	var sessionDir string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "vacuum",
		Short: "Reclaim free pages by rewriting sessions.db (transcript messages are preserved)",
		Long: "Run SQLite VACUUM against the shared sessions.db and report how much free space was released.\n\n" +
			"Only free pages are reclaimed: every session message, sub-agent entry, compaction summary, and EventStore byte is preserved. " +
			"No tool-execution record is pruned by this command. VACUUM rewrites the whole database, needs free disk space equal to its size, " +
			"and holds the writer lock; run it while other MothX processes are idle.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := maintenanceSessionDir(sessionDir)
			if err != nil {
				return err
			}
			if !sessionDatabaseExists(dir) {
				return fmt.Errorf("no session database at %s", dir)
			}
			before, err := session.InspectSessionStorage(cmd.Context(), dir)
			if err != nil {
				return fmt.Errorf("inspect session storage: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Session database: %s\n", dir)
			fmt.Fprintf(out, "Before: %d transcript entries, %d sub-agent entries, %d tool records, %d free pages (%s reclaimable).\n",
				before.Entries, before.SubEntries, before.ToolRecords, before.FreelistPages,
				formatByteSize(before.FreelistPages*before.PageSize))
			if dryRun {
				fmt.Fprintln(out, "Dry run: sessions.db was not rewritten.")
				return nil
			}

			started := time.Now()
			result, err := session.MaintainSessionStorage(cmd.Context(), dir, session.SessionStoragePolicy{Vacuum: true}, time.Now().UTC())
			if err != nil {
				return fmt.Errorf("vacuum session database (stop other MothX processes and retry): %w", err)
			}
			if !result.Vacuumed {
				fmt.Fprintln(out, "No free pages were present; no rewrite was needed.")
				return nil
			}
			fmt.Fprintf(out, "Vacuumed in %s. Transcript preserved: %d entries, %d sub-agent entries.\n",
				time.Since(started).Round(time.Millisecond), result.Entries, result.SubEntries)
			fmt.Fprintf(out, "After: %d free pages (%s reclaimable).\n",
				result.FreelistPages, formatByteSize(result.FreelistPages*result.PageSize))
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "Session directory to vacuum (default: the configured session directory)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report reclaimable free pages without rewriting the database")
	return cmd
}

func maintenanceSessionDir(flag string) (string, error) {
	if trimmed := strings.TrimSpace(flag); trimmed != "" {
		return filepath.Clean(platform.ExpandHome(trimmed)), nil
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return "", fmt.Errorf("load settings: %w", err)
	}
	return settings.GetSessionDir(), nil
}

func printSessionStorageStatus(out io.Writer, sessionDir string, stats session.SessionStorageResult) {
	fmt.Fprintf(out, "Session database: %s\n", sessionDir)
	fmt.Fprintf(out, "  Database size:  %s (sessions.db + WAL)\n", formatByteSize(sessionDatabaseBytes(sessionDir)))
	fmt.Fprintf(out, "  Transcript:     %d entries, %d sub-agent entries (never deleted)\n", stats.Entries, stats.SubEntries)
	fmt.Fprintf(out, "  Tool records:   %d\n", stats.ToolRecords)
	fmt.Fprintf(out, "  Free pages:     %d of %d (%s reclaimable)\n",
		stats.FreelistPages, stats.PageCount, formatByteSize(stats.FreelistPages*stats.PageSize))
	if stats.FreelistPages > 0 {
		fmt.Fprintln(out, "  Run \"mothx maintenance vacuum\" to reclaim free pages.")
	}
}

func sessionDatabaseExists(sessionDir string) bool {
	_, err := os.Stat(session.RootDatabasePath(sessionDir))
	return err == nil
}

func sessionDatabaseBytes(sessionDir string) int64 {
	var total int64
	base := session.RootDatabasePath(sessionDir)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(base + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}
