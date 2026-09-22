package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/doctor"
)

func newDoctorCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check environment, configuration, and provider status",
		Long:  "Diagnose your MothX environment: OS info, config files, providers, models, sandbox, MCP, and more.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput {
				// Keep stdout strictly machine-readable. Error statuses are part of
				// the response and do not turn the JSON stream into Cobra text.
				result := doctor.Run("", version)
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			return runDoctor()
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print one machine-readable JSON diagnosis")
	return cmd
}

func runDoctor() error {
	result := doctor.Run("", version)
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
	fmt.Println()
	fmt.Printf("  Result: %s\n\n", result.Summary)
	return nil
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
