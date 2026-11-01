package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/doctor"
)

func newDoctorCommand() *cobra.Command {
	var jsonOutput bool
	var repairSessionContent string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check environment, configuration, and provider status",
		Long:  "Diagnose your MothX environment: OS info, config files, providers, models, sandbox, MCP, and more.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			options := doctor.Options{RepairSessionContent: repairSessionContent}
			if jsonOutput {
				// Keep stdout strictly machine-readable. Error statuses are part of
				// the response and do not turn the JSON stream into Cobra text.
				return json.NewEncoder(cmd.OutOrStdout()).Encode(doctor.RunWithOptions(options))
			}
			return runDoctor(options)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print one machine-readable JSON diagnosis")
	cmd.Flags().StringVar(&repairSessionContent, "repair-session-content", "", "Adopt session content that only the legacy transcript holds (\"all\" or a session ID)")
	return cmd
}

func runDoctor(options doctor.Options) error {
	result := doctor.RunWithOptions(options)
	fmt.Println()
	fmt.Println("  MothX Doctor")
	fmt.Println("  ------------")
	for _, check := range result.Checks {
		fmt.Printf("    %s %s", doctorIcon(check.Status), check.Title)
		if check.Detail != "" {
			fmt.Printf(" - %s", check.Detail)
		}
		fmt.Println()
	}
	printSessionContentRepairs(result.Repairs)
	fmt.Println()
	fmt.Printf("  Result: %s\n\n", result.Summary)
	return nil
}

// printSessionContentRepairs renders the side effects of
// --repair-session-content so the operator sees exactly what was adopted,
// retired, deferred, or refused.
func printSessionContentRepairs(repairs []doctor.SessionContentRepair) {
	if len(repairs) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("  Session content repair")
	fmt.Println("  ----------------------")
	for _, repair := range repairs {
		switch {
		case repair.Error != "":
			fmt.Printf("    [error] %s: %s\n", repairLabel(repair), repair.Error)
		case repair.Deferred:
			fmt.Printf("    [skip] %s: another process owns the session; retry when it is idle\n", repairLabel(repair))
		case repair.AdoptedEntries == 0 && repair.RetiredEntries == 0:
			fmt.Printf("    [ok] %s: nothing to adopt\n", repairLabel(repair))
		default:
			fmt.Printf("    [ok] %s: adopted %d row(s), retired %d duplicate row(s)\n", repairLabel(repair), repair.AdoptedEntries, repair.RetiredEntries)
		}
	}
}

func repairLabel(repair doctor.SessionContentRepair) string {
	if repair.CWD == "" {
		return repair.SessionID
	}
	return repair.SessionID + " (" + repair.CWD + ")"
}

func doctorIcon(status string) string {
	switch status {
	case doctor.StatusOK:
		return "[ok]"
	case doctor.StatusWarn:
		return "[warn]"
	case doctor.StatusError:
		return "[error]"
	default:
		return "[skip]"
	}
}
