package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/sandbox"
	"github.com/oschina/mothx/internal/session"
)

// newWorktreeCommand wires the `mothx worktree` family. Every subcommand is a
// thin projection over the shared agentruntime.WorktreeManager; the CLI never
// runs git worktree commands itself.
func newWorktreeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Manage isolated git worktrees",
		Long:  "Create, list, reset and remove isolated git worktrees used as session working directories.",
	}
	cmd.AddCommand(newWorktreeListCommand())
	cmd.AddCommand(newWorktreeCreateCommand())
	cmd.AddCommand(newWorktreeRemoveCommand())
	cmd.AddCommand(newWorktreeResetCommand())
	return cmd
}

// worktreeManager returns the shared Runtime worktree manager for CLI/TUI
// runs, or nil when worktrees are disabled.
func worktreeManager(settings *config.Settings, root string) *agentruntime.WorktreeManager {
	if settings == nil || !settings.IsWorktreeEnabled() {
		return nil
	}
	sessionDir := settings.GetSessionDir()
	if strings.TrimSpace(sessionDir) == "" {
		sessionDir = platform.SessionDir()
	}
	mgr, err := agentruntime.NewWorktreeManager(agentruntime.WorktreeManagerOptions{
		SessionDir:          sessionDir,
		Root:                root,
		BranchPrefix:        settings.WorktreeBranchPrefix(),
		DefaultStartCommand: settings.WorktreeStartCommand(),
		SandboxOptions:      settings.Sandbox.Options(),
		SandboxLevel:        worktreeSandboxLevel(settings),
	})
	if err != nil {
		return nil
	}
	return mgr
}

func newWorktreeManager(root string) (*agentruntime.WorktreeManager, *config.Settings, error) {
	settings, err := config.LoadSettingsFor("")
	if err != nil {
		return nil, nil, err
	}
	mgr := worktreeManager(settings, root)
	if mgr == nil {
		return nil, settings, fmt.Errorf("worktree manager is unavailable")
	}
	return mgr, settings, nil
}

func newWorktreeListCommand() *cobra.Command {
	var repo, projectID, root string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, _, err := newWorktreeManager(root)
			if err != nil {
				return err
			}
			base := strings.TrimSpace(repo)
			if base == "" {
				base, _ = os.Getwd()
			}
			worktrees, err := mgr.List(cmd.Context(), base, projectID)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(worktrees)
			}
			return printWorktreeTable(cmd, worktrees)
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "Repository directory (default: current directory)")
	cmd.Flags().StringVar(&projectID, "project", "", "Filter by project id")
	cmd.Flags().StringVar(&root, "root", "", "Override the worktree root directory")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print machine-readable JSON")
	return cmd
}

func newWorktreeCreateCommand() *cobra.Command {
	var repo, name, root, start, projectID string
	var detached, jsonOut bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a managed worktree",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, settings, err := newWorktreeManager(root)
			if err != nil {
				return err
			}
			if settings != nil && !settings.IsWorktreeEnabled() {
				return fmt.Errorf("worktree creation is disabled (settings.json worktree.enabled)")
			}
			base := strings.TrimSpace(repo)
			if base == "" {
				base, _ = os.Getwd()
			}
			created, err := mgr.Create(cmd.Context(), agentruntime.CreateWorktreeRequest{
				BaseCwd:      base,
				Name:         name,
				Detached:     detached,
				StartCommand: start,
				ProjectID:    projectID,
			})
			if err != nil {
				return err
			}
			// Population runs asynchronously; wait so the CLI reports the
			// terminal status it just produced.
			mgr.Wait()
			final := created
			if got, err := mgr.Get(cmd.Context(), created.ID); err == nil && got.ID != "" {
				final = got
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(final)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", final.Status, final.Name, final.Branch, final.Directory)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "Repository directory (default: current directory)")
	cmd.Flags().StringVar(&name, "name", "", "Worktree name (default: derived from the repository)")
	cmd.Flags().BoolVar(&detached, "detach", false, "Create a detached worktree instead of a branch")
	cmd.Flags().StringVar(&start, "start", "", "Startup command stored for the worktree (executed in a later phase)")
	cmd.Flags().StringVar(&projectID, "project", "", "Associate the worktree with a project id")
	cmd.Flags().StringVar(&root, "root", "", "Override the worktree root directory")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print machine-readable JSON")
	return cmd
}

func newWorktreeRemoveCommand() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "remove <id|directory>",
		Short: "Remove a managed worktree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, _, err := newWorktreeManager(root)
			if err != nil {
				return err
			}
			id, directory := worktreeTarget(args[0])
			if err := mgr.Remove(cmd.Context(), id, directory); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "removed")
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Override the worktree root directory")
	return cmd
}

func newWorktreeResetCommand() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "reset <id|directory>",
		Short: "Reset a managed worktree to the default branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, _, err := newWorktreeManager(root)
			if err != nil {
				return err
			}
			id, directory := worktreeTarget(args[0])
			if err := mgr.Reset(cmd.Context(), id, directory); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "reset")
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "", "Override the worktree root directory")
	return cmd
}

// worktreeTarget interprets a positional argument as an id or an absolute
// directory path. Relative values are treated as ids.
func worktreeTarget(value string) (id, directory string) {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) || strings.ContainsRune(value, os.PathSeparator) {
		return "", value
	}
	return value, ""
}

// worktreeSandboxLevel resolves the sandbox level for a worktree start command
// from the shared settings policy, defaulting to direct execution when the
// sandbox is disabled.
func worktreeSandboxLevel(settings *config.Settings) *sandbox.Level {
	level := sandbox.LevelNone
	if settings != nil && settings.Sandbox.Enabled {
		level = sandbox.LevelStandard
		if settings.Sandbox.Level == "strict" {
			level = sandbox.LevelStrict
		}
	}
	return &level
}

func printWorktreeTable(cmd *cobra.Command, worktrees []session.Worktree) error {
	out := cmd.OutOrStdout()
	if len(worktrees) == 0 {
		fmt.Fprintln(out, "no worktrees")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tNAME\tBRANCH\tDIRECTORY")
	for _, wt := range worktrees {
		id := wt.ID
		if wt.External {
			id = "(external)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, wt.Status, wt.Name, wt.Branch, wt.Directory)
	}
	return w.Flush()
}
