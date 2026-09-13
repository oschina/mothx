package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/startvibecoding/mothx/internal/serve"
)

func newServeCommand(flags *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the unified OpenAI API, Web UI, and messaging channels",
		Long:  "Start MothX as a unified server exposing OpenAI-compatible APIs, a Web UI, and optional Feishu/WeChat channels.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return serve.Run(flags.serveOptions(), version)
		},
	}
	registerServeFlags(cmd.Flags(), flags)
	cmd.AddCommand(newServeInitConfigCommand())
	return cmd
}

func newServeInitConfigCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init-config [global|project]",
		Short: "Create serve.json config template",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := "global"
			if len(args) > 0 {
				scope = args[0]
			}
			var project bool
			switch scope {
			case "global":
				project = false
			case "project":
				project = true
			default:
				return fmt.Errorf("invalid scope %q: expected global or project", scope)
			}
			path, err := serve.InitConfigForProject(project, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Created serve config: %s\n", path)
			fmt.Fprintln(cmd.ErrOrStderr(), serve.PlaceholderAuthTokenWarning)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing file")
	return cmd
}

func registerServeFlags(fs *pflag.FlagSet, flags *cliFlags) {
	fs.StringVar(&flags.servePort, "port", "", "Listen port or address (e.g. 8080, :8080, 0.0.0.0:8080)")
	fs.StringVar(&flags.serveConfig, "config", "", "Path to serve.json")
	fs.StringVar(&flags.serveWebUIDir, "web-ui-dir", "", "Override embedded Serve Web UI assets with a built frontend directory")
	fs.BoolVar(&flags.serveUnsafe, "unsafe", false, "Disable auth and bind Serve to all interfaces")
	fs.StringVar(&flags.workDir, "work-dir", "", "Default working directory")
	fs.StringVarP(&flags.provider, "provider", "p", "", "Provider (openai, anthropic, or custom provider name)")
	fs.StringVarP(&flags.model, "model", "m", "", "Model ID")
	fs.BoolVar(&flags.sandbox, "sandbox", false, "Enable sandbox (bwrap) for secure execution")
	fs.BoolVar(&flags.multiAgent, "multi-agent", false, "Enable multi-agent mode (sub-agent tools)")
	fs.BoolVar(&flags.delegate, "delegate", false, "Enable delegation mode (blocking single sub-agent tool)")
	fs.BoolVar(&flags.workflows, "workflows", false, "Enable workflow mode (JavaScript workflow tools)")
	fs.BoolVar(&flags.webSearch, "web-search", false, "Enable configured web search provider for serve sessions")
	fs.BoolVar(&flags.browser, "browser", false, "Enable browser automation tool")
	fs.BoolVar(&flags.artifact, "artifact", false, "Enable generated artifacts for WebUI/API sessions")
	fs.BoolVar(&flags.enableA2AMaster, "enable-a2a-master", false, "Enable A2A master mode (dispatch tasks to remote agents)")
	fs.BoolVar(&flags.lobsterMode, "lobster", false, "Enable lobster mode (yolo, no sandbox, sub-agents on)")
	fs.BoolVar(&flags.verbose, "verbose", false, "Verbose output")
	fs.BoolVar(&flags.debug, "debug", false, "Enable debug logging")
}

func (f *cliFlags) serveOptions() serve.RunOptions {
	return serve.RunOptions{
		ConfigPath: f.serveConfig,
		Port:       f.servePort,
		WebUIDir:   f.serveWebUIDir,
		Provider:   f.provider,
		Model:      f.model,
		WorkDir:    f.workDir,
		Unsafe:     f.serveUnsafe,
		Sandbox:    f.sandbox,
		MultiAgent: f.multiAgent,
		Delegate:   f.delegate,
		Workflows:  f.workflows,
		WebSearch:  f.webSearch,
		Browser:    f.browser,
		Artifact:   f.artifact,
		A2AMaster:  f.enableA2AMaster,
		Lobster:    f.lobsterMode,
		Verbose:    f.verbose,
		Debug:      f.debug,
	}
}
