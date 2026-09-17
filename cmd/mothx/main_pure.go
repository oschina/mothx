package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/startvibecoding/mothx/internal/config"
	"github.com/startvibecoding/mothx/internal/platform"
	"github.com/startvibecoding/mothx/internal/session"
)

// newPureCommand creates the `pure` subcommand: it archives the current
// sessions database and starts a brand-new empty one, so the next run begins
// with no sessions, projects, runs, or decisions.
func newPureCommand() *cobra.Command {
	var sessionDir string
	cmd := &cobra.Command{
		Use:   "pure",
		Short: "Archive the sessions database and start a brand-new empty one",
		Long: "Move the shared sessions.db (and its SQLite sidecars) aside and create a fresh, empty database in its place.\n" +
			"Nothing is deleted: the previous database is renamed next to the new one as sessions.db.pure-<timestamp>.bak.\n\n" +
			"Stop every other mothx process first; a database moved while another process still holds it open keeps being written there.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := pureSessionDir(sessionDir)
			if err != nil {
				return err
			}
			backup, err := session.ResetDatabase(dir)
			if err != nil {
				// On Windows an open handle blocks the move, so point the user at
				// the usual cause instead of a bare "Access is denied".
				return fmt.Errorf("reset sessions database in %s (stop every other mothx process using this directory and retry): %w", dir, err)
			}
			out := cmd.OutOrStdout()
			if backup != "" {
				fmt.Fprintf(out, "Moved previous sessions database to: %s\n", backup)
			}
			fmt.Fprintf(out, "Created a fresh sessions database: %s\n", session.RootDatabasePath(dir))
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "Session directory to reset (default: the configured session directory)")
	return cmd
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
