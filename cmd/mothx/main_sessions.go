package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/session"
)

// listSessionsDefaultLimit is the number of sessions `mothx --list-sessions`
// prints by default. Enough to find a conversation started from another entry
// point (Desktop, Serve, a channel) without flooding the terminal.
const listSessionsDefaultLimit = 20

// listRecentSessions projects the recent session catalog across every working
// directory. The listing is a read-only projection of the shared session
// store: it never opens a Run, acquires a lease, or mutates a session.
func listRecentSessions(out io.Writer, limit int, cwd string) error {
	if limit <= 0 {
		limit = listSessionsDefaultLimit
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	details, err := session.ListAllDetailed(settings.GetSessionDir(),
		session.WithMessagesOnly(), session.WithLimit(limit))
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	filtered := details
	if trimmed := strings.TrimSpace(cwd); trimmed != "" {
		filtered = nil
		for _, detail := range details {
			if sessionPathsMatch(detail.Cwd, trimmed) {
				filtered = append(filtered, detail)
			}
		}
	}
	return printSessionList(out, filtered)
}

func sessionPathsMatch(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if strings.EqualFold(left, right) {
		return true
	}
	// Case-sensitive platforms still accept the same directory spelled through a
	// different but equivalent path.
	return strings.HasSuffix(left, right) || strings.HasSuffix(right, left)
}

func printSessionList(out io.Writer, details []session.SessionDetail) error {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION ID\tLAST USED\tMSGS\tWORKDIR\tTITLE")
	if len(details) == 0 {
		fmt.Fprintln(tw, "  No sessions with messages yet.")
		if err := tw.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(out, "\nRun one with: mothx --session <id>")
		return nil
	}
	for _, detail := range details {
		title := strings.TrimSpace(detail.Name)
		if title == "" {
			title = strings.TrimSpace(detail.Preview)
		}
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			detail.ID,
			formatSessionListTime(detail.ModTime),
			detail.MessageCount,
			orDash(detail.Cwd),
			oneLine(title, 60),
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nContinue one with: mothx --continue   Open one with: mothx --session <id>")
	return nil
}

func formatSessionListTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04")
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func oneLine(value string, limit int) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
	if limit > 0 && len(value) > limit {
		return value[:limit] + "…"
	}
	return value
}
