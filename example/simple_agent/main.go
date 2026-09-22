package main

import (
	"context"
	"fmt"
	"os"

	"github.com/oschina/mothx/agent"
	_ "github.com/oschina/mothx/bootstrap" // Register internal agent builder and provider resolution
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Please set the OPENAI_API_KEY environment variable to run this example.")
		fmt.Println("Alternatively, you can modify this file to use another provider/model.")
		os.Exit(1)
	}

	fmt.Println("Initializing agent...")

	// Create an agent using the Builder.
	// We use the convenient WithProviderByName method which links to the underlying
	// OpenAI provider adapter.
	a, err := agent.NewBuilder().
		WithProviderByName("openai", "https://api.openai.com/v1", "openai-chat", apiKey).
		WithModel("gpt-4o-mini").
		WithMode("agent"). // Can be "plan", "agent", "yolo", or "os"
		WithWorkDir(".").  // The directory where agent tools (like read, write, edit, bash) can run
		Build()
	if err != nil {
		fmt.Printf("Error building agent: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	userMessage := "Write a short 2-sentence poem about terminal coding."
	fmt.Printf("\nUser: %s\n\nAssistant: ", userMessage)

	// Run the agent and stream events back.
	events := a.Run(ctx, userMessage)
	for ev := range events {
		switch ev.Type {
		case agent.EventTextDelta:
			// Print assistant response stream
			fmt.Print(ev.TextDelta)
		case agent.EventThinkDelta:
			// Print assistant reasoning stream (if available)
			fmt.Print(ev.ThinkDelta)
		case agent.EventToolCall:
			fmt.Printf("\n\n[Tool Call] Executing %s with args: %v\n", ev.ToolName, ev.ToolArgs)
		case agent.EventToolResult:
			fmt.Printf("\n[Tool Result] Output:\n%s\n", ev.ToolResult)
		case agent.EventRunFinished:
			// Canonical terminal event: classify the task outcome from Status.
			switch ev.Status {
			case agent.TaskSuccess:
				fmt.Println("\n\n--- Agent Execution Finished (success) ---")
			case agent.TaskIncomplete:
				fmt.Printf("\n\n--- Agent Execution Finished (incomplete: %s) ---\n", ev.StopReason)
			case agent.TaskCanceled:
				fmt.Println("\n\n--- Agent Execution Canceled ---")
			default: // TaskFailed
				fmt.Printf("\nError during run: %v\n", ev.Error)
			}
		case agent.EventDone:
			// Legacy terminal event; outcome is carried by EventRunFinished.
		case agent.EventError:
			// Legacy error event; prefer EventRunFinished.Status above.
		}
	}
}
