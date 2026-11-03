package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The generated schema must always be valid JSON: it is sent to providers
// verbatim, and a malformed schema makes the tool unusable.
func TestDelegateParametersAreValidJSON(t *testing.T) {
	cases := map[string]*RouterTarget{
		"no router target":  nil,
		"single worker":     {ID: "opencode"},
		"worker plus peers": {ID: "opencode", Available: []string{"opencode", "claude-code", "codex"}},
		// A profile ID with characters that need JSON escaping.
		"quoting worker": {ID: `odd"id`},
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			tool := NewDelegateSubAgentTool(nil)
			tool.SetRouterTarget(target)
			var schema map[string]any
			if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
				t.Fatalf("schema is not valid JSON: %v\n%s", err, tool.Parameters())
			}
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema has no properties: %s", tool.Parameters())
			}
			if _, ok := properties["agent"]; !ok {
				t.Fatal("agent property is missing")
			}
			if _, ok := properties["task"]; !ok {
				t.Fatal("task property is missing")
			}
		})
	}
}

// In Router mode the schema must pin the agent value: a model that guesses a
// plausible product name should be structurally prevented from doing so.
func TestDelegateSchemaPinsActivatedWorker(t *testing.T) {
	tool := NewDelegateSubAgentTool(nil)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: []string{"opencode", "codex"}})

	var schema struct {
		Properties map[string]struct {
			Type        string   `json:"type"`
			Enum        []string `json:"enum"`
			Const       string   `json:"const"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatal(err)
	}
	agentField := schema.Properties["agent"]
	if agentField.Description == "" {
		t.Fatal("agent field has no description")
	}
	if !strings.Contains(agentField.Description, "opencode") {
		t.Fatalf("agent description does not name the activated worker: %q", agentField.Description)
	}
	listed := append([]string(nil), agentField.Enum...)
	if agentField.Const != "" {
		listed = append(listed, agentField.Const)
	}
	if len(listed) == 0 {
		t.Fatal("agent field does not constrain the value")
	}
	first := listed[0]
	if first != "opencode" {
		t.Fatalf("agent field leads with %q, want opencode", first)
	}
}

// With exactly one eligible profile the schema must be the tightest form: const.
func TestDelegateSchemaUsesConstForSingleWorker(t *testing.T) {
	tool := NewDelegateSubAgentTool(nil)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: []string{"opencode"}})

	var schema struct {
		Properties map[string]struct {
			Enum  []string `json:"enum"`
			Const string   `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatal(err)
	}
	field := schema.Properties["agent"]
	if field.Const != "opencode" {
		t.Fatalf("const = %q, want opencode", field.Const)
	}
	if len(field.Enum) != 0 {
		t.Fatalf("a single worker should not produce an enum, got %v", field.Enum)
	}
}

// Outside Router mode there is no target, and the schema must not claim one.
func TestDelegateSchemaWithoutTargetHasNoConst(t *testing.T) {
	tool := NewDelegateSubAgentTool(nil)
	var schema struct {
		Properties map[string]struct {
			Enum  []string `json:"enum"`
			Const string   `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatal(err)
	}
	field := schema.Properties["agent"]
	if field.Const != "" || len(field.Enum) != 0 {
		t.Fatalf("schema constrained agent without a Router target: %#v", field)
	}
}

// Clear_target must be reversible: activation publishes, removal clears.
func TestDelegateRouterTargetIsReplaceable(t *testing.T) {
	tool := NewDelegateSubAgentTool(nil)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode"})
	if target, ok := tool.RouterTarget(); !ok || target.ID != "opencode" {
		t.Fatalf("target = %#v, ok=%v", target, ok)
	}
	tool.SetRouterTarget(&RouterTarget{ID: "codex"})
	if target, _ := tool.RouterTarget(); target.ID != "codex" {
		t.Fatalf("target after rebind = %q", target.ID)
	}
	tool.SetRouterTarget(nil)
	if _, ok := tool.RouterTarget(); ok {
		t.Fatal("target survived being cleared")
	}
	tool.SetRouterTarget(&RouterTarget{ID: "  "})
	if _, ok := tool.RouterTarget(); ok {
		t.Fatal("a blank target was accepted")
	}
}

// The published target must be defensively copied: a caller mutating its own
// struct must not change what the model sees.
func TestDelegateRouterTargetIsCopied(t *testing.T) {
	tool := NewDelegateSubAgentTool(nil)
	available := []string{"opencode", "codex"}
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: available})
	available[0] = "mutated"
	target, ok := tool.RouterTarget()
	if !ok {
		t.Fatal("target missing")
	}
	if target.Available[0] != "opencode" {
		t.Fatalf("target aliased the caller slice: %#v", target.Available)
	}
}

// A wrong agent name must produce a correction naming the real worker, because
// that message is the model's only feedback.
func TestDelegateRejectsWrongAgentWithCorrection(t *testing.T) {
	manager := NewAgentManager(nil)
	tool := NewDelegateSubAgentTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: []string{"opencode", "codex"}})
	_, err := tool.Execute(ContextWithParentMode(context.Background(), "router"), map[string]any{"task": "x", "agent": "codex"})
	if err == nil {
		t.Fatal("a wrong agent name was accepted")
	}
	message := err.Error()
	if !strings.Contains(message, "opencode") {
		t.Fatalf("error does not name the activated worker: %s", message)
	}
	if !strings.Contains(message, "codex") {
		t.Fatalf("error does not echo the rejected value: %s", message)
	}
}

// Omitting agent in Router mode must also name the required value.
func TestDelegateMissingAgentNamesWorker(t *testing.T) {
	manager := NewAgentManager(nil)
	tool := NewDelegateSubAgentTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode"})
	_, err := tool.Execute(ContextWithParentMode(context.Background(), "router"), map[string]any{"task": "x"})
	if err == nil {
		t.Fatal("a missing agent was accepted in Router mode")
	}
	if !strings.Contains(err.Error(), "opencode") {
		t.Fatalf("error does not name the required worker: %s", err.Error())
	}
}
