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
			tool := NewDelegateWorkerMgrTool(nil)
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

// In Router mode the schema must pin the agent value to the activated worker: a
// model that guesses a plausible product name should be structurally prevented
// from doing so. Other configured profiles are correction context only.
func TestDelegateSchemaPinsActivatedWorker(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(nil)
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
	if agentField.Const != "opencode" {
		t.Fatalf("agent const = %q, want the activated worker", agentField.Const)
	}
	if len(agentField.Enum) != 0 {
		t.Fatalf("agent field advertises other profiles as accepted values: %v", agentField.Enum)
	}
	if !strings.Contains(agentField.Description, "not accepted") {
		t.Fatalf("agent description does not mark other profiles as unaccepted: %q", agentField.Description)
	}
}

// Multiple configured profiles must still produce one const value. Otherwise a
// provider may legally choose a profile that Execute will reject.
func TestDelegateSchemaUsesConstWithMultipleProfiles(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(nil)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: []string{"opencode", "codex", "claude-code"}})
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
		t.Fatalf("multiple profiles produced an enum: %v", field.Enum)
	}
}

// A Router session may bind several workers. The schema must then enumerate the
// bound allowlist and permit each value, rather than pinning only the default.
func TestDelegateSchemaEnumeratesBoundWorkers(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(nil)
	tool.SetRouterTarget(&RouterTarget{
		ID:       "opencode",
		Accepted: []string{"opencode", "codex"},
		Available: []string{
			"opencode", "codex", "claude-code",
		},
	})

	var schema struct {
		Properties map[string]struct {
			Enum        []string `json:"enum"`
			Const       string   `json:"const"`
			Description string   `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatal(err)
	}
	field := schema.Properties["agent"]
	if field.Const != "" {
		t.Fatalf("multi-worker schema pinned a single const: %q", field.Const)
	}
	if got := strings.Join(field.Enum, ","); got != "opencode,codex" {
		t.Fatalf("agent enum = %v", field.Enum)
	}
	if !strings.Contains(field.Description, "claude-code") || !strings.Contains(field.Description, "not accepted") {
		t.Fatalf("description does not explain the unbound profile: %q", field.Description)
	}
	if strings.Contains(field.Description, "not accepted: opencode") ||
		strings.Contains(field.Description, "not accepted: codex") {
		t.Fatalf("description lists a bound worker as unaccepted: %q", field.Description)
	}
}

// Execute must accept a non-default bound worker; otherwise the schema and the
// runtime contract disagree.
func TestDelegateAcceptsNonDefaultBoundWorker(t *testing.T) {
	state := &routerTargetState{}
	state.SetRouterTarget(&RouterTarget{ID: "opencode", Accepted: []string{"opencode", "codex"}})
	ctx := ContextWithParentMode(context.Background(), "router")
	got, err := resolveRouterExternalAgent(ctx, state, map[string]any{
		"task": "inspect", "agent": "codex",
	})
	if err != nil {
		t.Fatalf("resolve bound worker: %v", err)
	}
	if got != "codex" {
		t.Fatalf("resolved worker = %q", got)
	}
	if _, err := resolveRouterExternalAgent(ctx, state, map[string]any{
		"task": "inspect", "agent": "claude-code",
	}); err == nil || !strings.Contains(err.Error(), "opencode, codex") {
		t.Fatalf("unbound worker error = %v", err)
	}
}

// With exactly one eligible profile the schema must be the tightest form: const.
func TestDelegateSchemaUsesConstForSingleWorker(t *testing.T) {
	tool := NewDelegateWorkerMgrTool(nil)
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
	tool := NewDelegateWorkerMgrTool(nil)
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
	tool := NewDelegateWorkerMgrTool(nil)
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
	tool := NewDelegateWorkerMgrTool(nil)
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
	tool := NewDelegateWorkerMgrTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode", Available: []string{"opencode", "codex"}})
	_, err := tool.Execute(ContextWithParentMode(context.Background(), "router"), map[string]any{"action": "delegate", "task": "x", "agent": "codex"})
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
	tool := NewDelegateWorkerMgrTool(manager)
	tool.SetRouterTarget(&RouterTarget{ID: "opencode"})
	_, err := tool.Execute(ContextWithParentMode(context.Background(), "router"), map[string]any{"action": "delegate", "task": "x"})
	if err == nil {
		t.Fatal("a missing agent was accepted in Router mode")
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Fatalf("error does not name the required field: %s", err.Error())
	}
}

// Local-subagent parameters must fail explicitly in Router rather than being
// silently ignored: the external worker's profile is the only authority for its
// mode, tools, worktree, and lifetime.
func TestDelegateRouterRejectsLocalOnlyParameters(t *testing.T) {
	cases := map[string]map[string]any{
		"mode":           {"mode": "plan"},
		"work_dir":       {"work_dir": "/tmp/elsewhere"},
		"worktree":       {"worktree": "isolated"},
		"tools":          {"tools": []any{"read"}},
		"max_iterations": {"max_iterations": float64(10)},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			tool := NewDelegateWorkerMgrTool(NewAgentManager(nil))
			tool.SetRouterTarget(&RouterTarget{ID: "opencode"})
			params := map[string]any{"action": "delegate", "task": "inspect", "agent": "opencode"}
			for key, value := range extra {
				params[key] = value
			}
			_, err := tool.Execute(ContextWithParentMode(context.Background(), "router"), params)
			if err == nil {
				t.Fatalf("%s was silently accepted for an external Router worker", name)
			}
			if !strings.Contains(err.Error(), "not available for an external Router worker") {
				t.Fatalf("%s error = %q", name, err)
			}
		})
	}
}
