package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/mcp"
)

func newKnowledgeMCPCommand() *cobra.Command {
	var knowledgeBaseIDs []string
	var sessionDir string
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve configured knowledge bases over the MCP stdio transport",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(sessionDir) == "" {
				settings, err := config.LoadSettings()
				if err != nil {
					return fmt.Errorf("load settings for knowledge MCP: %w", err)
				}
				sessionDir = settings.GetSessionDir()
			}
			handler, err := agentruntime.NewKnowledgeMCPHandler(sessionDir, knowledgeBaseIDs)
			if err != nil {
				return err
			}
			return mcp.ServeStdio(context.Background(), os.Stdin, os.Stdout, handler)
		},
	}
	serve.Flags().StringSliceVar(&knowledgeBaseIDs, "knowledge-base", nil, "Knowledge base ID enabled for this MCP server (repeatable)")
	serve.Flags().StringVar(&sessionDir, "session-dir", "", "Session storage directory (defaults to settings.json)")
	command := &cobra.Command{Use: "knowledge-mcp", Short: "Expose managed knowledge bases as MCP tools"}
	command.AddCommand(serve)
	return command
}
