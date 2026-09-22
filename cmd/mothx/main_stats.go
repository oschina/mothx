package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/stats"
)

func newStatsCommand() *cobra.Command {
	flags := &statsFlags{}
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show usage statistics",
		Long:  "Show usage statistics (tokens and requests) in a web dashboard or directly in the CLI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.cli {
				return runStatsCLI(cmd.OutOrStdout(), flags)
			}
			return runStatsServer(flags)
		},
	}
	registerStatsFlags(cmd.Flags(), flags)
	return cmd
}

type statsFlags struct {
	addr          string
	dbPath        string
	cli           bool
	noBrowserOpen bool
}

func registerStatsFlags(fs *pflag.FlagSet, flags *statsFlags) {
	fs.StringVar(&flags.addr, "addr", "127.0.0.1:7878", "Listen address for the stats web server")
	fs.StringVar(&flags.dbPath, "db", "", "Path to sessions.db (default: <config-dir>/sessions/sessions.db)")
	fs.BoolVar(&flags.cli, "cli", false, "Print stats in the terminal instead of starting the web server")
	fs.BoolVar(&flags.noBrowserOpen, "no-browser-open", false, "Do not open the stats dashboard in the default browser")
}

func runStatsServer(flags *statsFlags) error {
	db, err := openStatsDB(flags)
	if err != nil {
		return err
	}
	defer db.Close()

	server := stats.NewServer(db, flags.addr)
	listener, err := net.Listen("tcp", flags.addr)
	if err != nil {
		return fmt.Errorf("listen stats server: %w", err)
	}
	url := "http://" + listener.Addr().String()
	if !flags.noBrowserOpen {
		if err := openURLInDefaultBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "stats dashboard: could not open browser: %v\n", err)
			fmt.Fprintf(os.Stderr, "stats dashboard: open %s manually\n", url)
		}
	}
	return server.Serve(listener)
}

func openURLInDefaultBrowser(url string) error {
	var candidates [][]string
	switch runtime.GOOS {
	case "darwin":
		candidates = [][]string{{"open", url}}
	case "windows":
		candidates = [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	default:
		candidates = [][]string{{"xdg-open", url}, {"gio", "open", url}, {"sensible-browser", url}}
	}
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate[0]); err != nil {
			continue
		}
		cmd := exec.Command(candidate[0], candidate[1:]...)
		if err := cmd.Start(); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("no browser opener found")
}

func runStatsCLI(w io.Writer, flags *statsFlags) error {
	db, err := openStatsDB(flags)
	if err != nil {
		return err
	}
	defer db.Close()

	query := stats.Query{}
	summary, err := db.Summary(query)
	if err != nil {
		return fmt.Errorf("query summary: %w", err)
	}
	byProvider, err := db.ByProvider(query)
	if err != nil {
		return fmt.Errorf("query providers: %w", err)
	}
	byModel, err := db.ByModel(query)
	if err != nil {
		return fmt.Errorf("query models: %w", err)
	}
	recent, err := db.Recent(1, 10)
	if err != nil {
		return fmt.Errorf("query recent requests: %w", err)
	}

	return printStatsCLI(w, summary, byProvider, byModel, recent)
}

func openStatsDB(flags *statsFlags) (*stats.DB, error) {
	dbPath := flags.dbPath
	if dbPath == "" {
		settings, err := config.LoadSettings()
		if err != nil {
			return nil, fmt.Errorf("load settings: %w", err)
		}
		dbPath = filepath.Join(settings.GetSessionDir(), "sessions.db")
	}

	db, err := stats.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open stats database: %w", err)
	}
	return db, nil
}

func printStatsCLI(w io.Writer, summary *stats.Summary, byProvider, byModel []stats.Aggregate, recent *stats.RecentPage) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VibeCoding Stats")
	fmt.Fprintln(tw)
	fmt.Fprintf(tw, "Requests:\t%s\n", formatStatsInt(summary.TotalRequests))
	fmt.Fprintf(tw, "Input tokens:\t%s\n", formatStatsInt(summary.InputTokens))
	fmt.Fprintf(tw, "Output tokens:\t%s\n", formatStatsInt(summary.OutputTokens))
	fmt.Fprintf(tw, "Total tokens:\t%s\n", formatStatsInt(summary.TotalTokens))

	printStatsAggregates(tw, "By Provider", "Provider", byProvider, 5, func(a stats.Aggregate) string {
		if a.Protocol == "" {
			return a.Vendor
		}
		return fmt.Sprintf("%s (%s)", a.Vendor, a.Protocol)
	})
	printStatsAggregates(tw, "By Model", "Model", byModel, 5, func(a stats.Aggregate) string {
		if a.Model != "" {
			return a.Model
		}
		return a.Label
	})
	printStatsRecent(tw, recent)
	return tw.Flush()
}

func printStatsAggregates(w io.Writer, title, labelHeader string, rows []stats.Aggregate, limit int, labelFn func(stats.Aggregate) string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, title)
	if len(rows) == 0 {
		fmt.Fprintln(w, "  No data")
		return
	}
	fmt.Fprintf(w, "%s\tRequests\tInput\tOutput\tTotal\n", labelHeader)
	for i, row := range rows {
		if i >= limit {
			break
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			emptyDash(labelFn(row)),
			formatStatsInt(row.Requests),
			formatStatsInt(row.InputTokens),
			formatStatsInt(row.OutputTokens),
			formatStatsInt(row.TotalTokens),
		)
	}
}

func printStatsRecent(w io.Writer, recent *stats.RecentPage) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Recent Requests")
	if recent == nil || len(recent.Items) == 0 {
		fmt.Fprintln(w, "  No data")
		return
	}
	fmt.Fprintln(w, "Time\tProvider\tProtocol\tModel\tInput\tOutput\tDuration")
	for _, item := range recent.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			formatStatsTime(item.Timestamp),
			emptyDash(item.Vendor),
			emptyDash(item.Protocol),
			emptyDash(item.Model),
			formatStatsInt(item.InputTokens),
			formatStatsInt(item.OutputTokens),
			formatStatsDuration(item.DurationMs),
		)
	}
}

func formatStatsInt(n int) string {
	return fmt.Sprintf("%d", n)
}

func formatStatsTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func formatStatsDuration(ms int) string {
	if ms <= 0 {
		return "-"
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
