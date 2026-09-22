package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/a2a"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
	providerfactory "github.com/oschina/mothx/internal/provider/factory"
	"github.com/oschina/mothx/internal/sandbox"
)

// newA2ACommand builds the "a2a" command tree.
func newA2ACommand() *cobra.Command {
	var (
		flagPort          int
		flagWorkDir       string
		flagProvider      string
		flagModel         string
		flagSandbox       bool
		flagAuthToken     string
		flagInitA2AConfig bool
		flagForce         bool
	)

	a2aCmd := &cobra.Command{
		Use:   "a2a",
		Short: "Run the A2A (Agent-to-Agent) server",
		Long:  "Start VibeCoding A2A Server — a JSON-RPC 2.0 endpoint for other agents to send tasks.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagInitA2AConfig {
				path, err := a2a.InitA2AConfig(flagForce)
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Created a2a config: %s\n", path)
				return nil
			}
			return cmd.Help()
		},
	}

	a2aFlags := a2aCmd.Flags()
	a2aFlags.BoolVar(&flagInitA2AConfig, "init-a2a-config", false, "Create a2a.json config template")
	a2aFlags.BoolVar(&flagForce, "force", false, "Force overwrite existing files (used with --init-a2a-config)")

	// --- start ---

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the A2A server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := a2a.DefaultConfig()

			if flagPort != 0 {
				cfg.Port = flagPort
			}
			if flagWorkDir != "" {
				cfg.WorkDir = flagWorkDir
			}
			if flagAuthToken != "" {
				cfg.AuthToken = flagAuthToken
			}

			// Resolve working directory
			if cfg.WorkDir == "" || cfg.WorkDir == "." {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
				cfg.WorkDir = cwd
			}

			// Load settings for provider
			settings, err := config.LoadSettings()
			if err != nil {
				return fmt.Errorf("load settings: %w", err)
			}

			providerName := flagProvider
			if providerName == "" {
				providerName = settings.DefaultProvider
			}
			modelID := flagModel
			if modelID == "" {
				if flagProvider != "" {
					modelID = ""
				} else {
					modelID = settings.DefaultModel
				}
			}

			// Create provider (lazy import to avoid circular deps)
			// For now, we use a simple factory that wraps the agent creation
			factory := &simpleAgentFactory{
				settings: settings,
				allow:    config.LoadAllow(),
				provider: providerName,
				model:    modelID,
				workDir:  cfg.GetWorkDir(),
				sandbox:  flagSandbox,
			}

			executor := a2a.NewDefaultExecutor(factory)
			return a2a.Run(cfg, version, executor)
		},
	}

	startFlags := startCmd.Flags()
	startFlags.IntVar(&flagPort, "port", 0, "Listen port (default: 8093)")
	startFlags.StringVar(&flagWorkDir, "work-dir", "", "Default working directory")
	startFlags.StringVarP(&flagProvider, "provider", "p", "", "Default provider name")
	startFlags.StringVarP(&flagModel, "model", "m", "", "Default model ID")
	startFlags.BoolVar(&flagSandbox, "sandbox", false, "Enable sandbox mode (bwrap)")
	startFlags.StringVar(&flagAuthToken, "auth-token", "", "Bearer token for authentication")

	// --- stop ---

	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the A2A server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// For simplicity, use HTTP health check.
			cfg := a2a.DefaultConfig()
			url := fmt.Sprintf("http://%s/.well-known/agent.json", cfg.GetListenAddr())
			client := &http.Client{Timeout: 2 * time.Second}
			_, err := client.Get(url)
			if err != nil {
				return fmt.Errorf("A2A server is not running (cannot reach %s)", url)
			}
			fmt.Fprintf(os.Stderr, "A2A server is running at %s\n", cfg.GetListenAddr())
			fmt.Fprintf(os.Stderr, "Note: Use Ctrl+C or kill the process to stop.\n")
			return nil
		},
	}

	// --- status ---

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show A2A server status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := a2a.DefaultConfig()
			url := fmt.Sprintf("http://%s/.well-known/agent.json", cfg.GetListenAddr())
			client := &http.Client{Timeout: 2 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				fmt.Fprintf(os.Stderr, "A2A server is not running (cannot reach %s)\n", url)
				return nil
			}
			defer resp.Body.Close()

			var card a2a.AgentCard
			json.NewDecoder(resp.Body).Decode(&card)
			fmt.Fprintf(os.Stderr, "A2A server is running at %s\n", cfg.GetListenAddr())
			fmt.Fprintf(os.Stderr, "  Name: %s\n", card.Name)
			fmt.Fprintf(os.Stderr, "  Version: %s\n", card.Version)
			fmt.Fprintf(os.Stderr, "  Skills: %d\n", len(card.Skills))
			for _, s := range card.Skills {
				fmt.Fprintf(os.Stderr, "    - %s: %s\n", s.Name, s.Description)
			}
			return nil
		},
	}

	// --- card ---

	cardCmd := &cobra.Command{
		Use:   "card",
		Short: "Show or generate the Agent Card",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := a2a.DefaultConfig()
			card := a2a.DefaultAgentCard(version, fmt.Sprintf("http://%s", cfg.GetListenAddr()))
			data, _ := json.MarshalIndent(card, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}

	a2aCmd.AddCommand(startCmd, stopCmd, statusCmd, cardCmd)

	// --- send ---

	var flagTarget string

	sendCmd := &cobra.Command{
		Use:   "send <message>",
		Short: "Send a message to an A2A server",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			msg := strings.Join(args, " ")
			target := flagTarget
			if target == "" {
				target = "http://localhost:8093"
			}

			client := a2a.NewClient(target, flagAuthToken)
			task, err := client.SendMessage(cmd.Context(), "", &a2a.Message{
				Role:  "user",
				Parts: []a2a.MessagePart{{Type: "text", Text: msg}},
			})
			if err != nil {
				return fmt.Errorf("send message: %w", err)
			}

			// Print response
			if len(task.Artifacts) > 0 {
				for _, a := range task.Artifacts {
					for _, p := range a.Parts {
						if p.Type == "text" {
							fmt.Println(p.Text)
						}
					}
				}
			} else if task.Message != nil {
				for _, p := range task.Message.Parts {
					if p.Type == "text" {
						fmt.Println(p.Text)
					}
				}
			}
			return nil
		},
	}
	sendCmd.Flags().StringVar(&flagTarget, "target", "", "A2A server URL (default: http://localhost:8093)")
	sendCmd.Flags().StringVar(&flagAuthToken, "auth-token", "", "Bearer token")

	// --- discover ---

	discoverCmd := &cobra.Command{
		Use:   "discover <url>",
		Short: "Discover an A2A server's Agent Card",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := a2a.NewClient(args[0], flagAuthToken)
			card, err := client.GetAgentCard(cmd.Context())
			if err != nil {
				return fmt.Errorf("discover: %w", err)
			}
			data, _ := json.MarshalIndent(card, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}

	a2aCmd.AddCommand(sendCmd, discoverCmd)
	return a2aCmd
}

// simpleAgentFactory creates agents for A2A task execution.
// This bridges the a2a package to the agent package.
type simpleAgentFactory struct {
	settings *config.Settings
	allow    *config.AllowConfig
	provider string
	model    string
	workDir  string
	sandbox  bool
}

func (f *simpleAgentFactory) CreateForA2A(workDir string, mode string) (*agent.Agent, error) {
	if workDir == "" {
		workDir = f.workDir
	}

	p, model, err := createProviderForA2A(f.settings, f.provider, f.model)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}

	sbMgr := sandbox.NewManagerWithOptions(workDir, f.settings.Sandbox.Options())
	if f.sandbox {
		if err := sbMgr.SetLevel(sandbox.LevelStandard); err != nil {
			return nil, fmt.Errorf("sandbox enabled but unavailable: %w", err)
		}
		if err := sbMgr.FallbackError(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: sandbox unavailable; using direct execution: %v\n", err)
		}
	}

	level := sandbox.LevelNone
	if f.sandbox {
		level = sandbox.LevelStandard
	}
	runtime, err := (agentruntime.Builder{Settings: f.settings, SandboxLevel: level}).Build(context.Background(), agentruntime.BuildOptions{
		Source: agentruntime.SourceACP, WorkDir: workDir,
	})
	if err != nil {
		return nil, err
	}
	defer runtime.Close()
	a, err := runtime.BuildAgent(agentruntime.AgentBuildOptions{
		Provider: p, ProviderName: f.provider, Model: model, Settings: f.settings,
		Allow: f.allow, Mode: mode, ThinkingLevel: provider.ThinkingOff,
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// createProviderForA2A creates a provider for A2A task execution.
func createProviderForA2A(settings *config.Settings, providerName, modelID string) (provider.Provider, *provider.Model, error) {
	return providerfactory.Create(settings, providerName, modelID)
}
