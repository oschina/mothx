package main

import (
	"context"
	"fmt"
	"os"

	"github.com/oschina/mothx/agent"
	_ "github.com/oschina/mothx/bootstrap" // Register internal agent builder and provider resolution
)

// CustomProvider implements the public agent.Provider interface.
// External developers can use this pattern to integrate custom LLM backends.
type CustomProvider struct {
	agent.BaseProvider
}

func NewCustomProvider() *CustomProvider {
	models := []agent.ModelInfo{
		{
			ID:            "mock-model",
			Name:          "Mock Model",
			Provider:      "custom-mock",
			Reasoning:     false,
			ContextWindow: 4096,
			MaxTokens:     1024,
		},
	}
	return &CustomProvider{
		BaseProvider: agent.NewBaseProvider("custom-mock", models),
	}
}

// Chat simulates a streaming response from a custom LLM.
// It detects certain keywords in the prompt to simulate either pure text replies
// or initiating a tool call (e.g. requesting the agent to run a tool).
func (cp *CustomProvider) Chat(ctx context.Context, params agent.ChatParams) <-chan agent.StreamEvent {
	ch := make(chan agent.StreamEvent, 10)

	go func() {
		defer close(ch)

		// Get the last user message
		var lastMsg string
		var hasToolResultAfterUser bool

		// Find the last user message text from Content or Contents
		for i := len(params.Messages) - 1; i >= 0; i-- {
			m := params.Messages[i]
			if m.Role == agent.RoleToolResult {
				hasToolResultAfterUser = true
			}
			if m.Role == agent.RoleUser {
				if m.Content != "" {
					lastMsg = m.Content
					break
				}
				for _, cb := range m.Contents {
					if cb.Type == "text" && cb.Text != "" {
						lastMsg = cb.Text
						break
					}
				}
				if lastMsg != "" {
					break
				}
			}
		}

		ch <- agent.StreamEvent{Type: agent.StreamStart}

		if contains(lastMsg, "test tool") && !hasToolResultAfterUser {
			// Simulate the LLM deciding to call a tool (e.g. "read" tool to read "example_file.txt")
			fmt.Println("\n[Provider] Simulating LLM deciding to call 'read' tool...")

			// We construct a ToolCall request
			ch <- agent.StreamEvent{
				Type: agent.StreamToolCall,
				ToolCall: &agent.ToolCallBlock{
					ID:        "call_mock_1",
					Name:      "read",
					Arguments: []byte(`{"path": "README.md"}`),
				},
			}
		} else if hasToolResultAfterUser {
			// Respond to the tool result
			ch <- agent.StreamEvent{
				Type:      agent.StreamTextDelta,
				TextDelta: "I have successfully read the README.md file and verified its content!",
			}
		} else {
			// Stream standard text response delta by delta
			textDeltas := []string{
				"Hello! ",
				"I am ",
				"running ",
				"from ",
				"your ",
				"custom ",
				"provider ",
				"implementation!",
			}
			for _, delta := range textDeltas {
				ch <- agent.StreamEvent{
					Type:      agent.StreamTextDelta,
					TextDelta: delta,
				}
			}
		}

		ch <- agent.StreamEvent{
			Type:       agent.StreamDone,
			StopReason: "stop",
		}
	}()

	return ch
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func main() {
	fmt.Println("Initializing agent with custom mock provider...")

	// 1. Create our custom provider instance
	customProv := NewCustomProvider()

	// 2. Build the agent using our custom provider
	a, err := agent.NewBuilder().
		WithProvider(customProv).
		WithModel("mock-model").
		WithMode("agent").
		WithWorkDir("."). // Allows built-in tools (like read) to run in this dir
		Build()
	if err != nil {
		fmt.Printf("Error building agent: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// --- Phase 1: Pure Text Response ---
	userMsg1 := "hello"
	fmt.Printf("\n--- Phase 1: Text Response ---\nUser: %s\nAssistant: ", userMsg1)
	events1 := a.Run(ctx, userMsg1)
	for ev := range events1 {
		if ev.Type == agent.EventTextDelta {
			fmt.Print(ev.TextDelta)
		}
	}
	fmt.Println()

	// --- Phase 2: Tool Call Response ---
	userMsg2 := "please test tool execution"
	fmt.Printf("\n--- Phase 2: Tool Call response ---\nUser: %s\n", userMsg2)
	events2 := a.Run(ctx, userMsg2)
	for ev := range events2 {
		switch ev.Type {
		case agent.EventTextDelta:
			fmt.Print(ev.TextDelta)
		case agent.EventToolCall:
			fmt.Printf("[Agent Loop] Executing tool: %s with args: %s\n", ev.ToolName, ev.ToolCall.Arguments)
		case agent.EventToolResult:
			// Show the truncated output of reading README.md
			output := ev.ToolResult
			if len(output) > 200 {
				output = output[:200] + "... [truncated]"
			}
			fmt.Printf("[Agent Loop] Tool Result returned:\n%s\n", output)
		case agent.EventDone:
			fmt.Println("\nCustom provider workflow completed successfully!")
		}
	}
}
