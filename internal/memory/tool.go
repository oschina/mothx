package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/oschina/mothx/internal/tools"
)

// MemoryTool provides persistent memory read/write via memory.md.
type MemoryTool struct {
	store *Store
}

// NewMemoryTool creates a new memory tool.
func NewMemoryTool(store *Store) *MemoryTool {
	return &MemoryTool{store: store}
}

func (t *MemoryTool) Name() string {
	return "memory"
}

func (t *MemoryTool) Description() string {
	return "Read and write persistent memory (memory.md). Use to recall user preferences, project context, and lessons learned. Memory persists across sessions."
}

func (t *MemoryTool) PromptSnippet() string {
	return "Read/write persistent memory across sessions"
}

func (t *MemoryTool) PromptGuidelines() []string {
	return []string{
		"A persistent memory file (memory.md) is available via the `memory` tool. Read it at the start of complex tasks to recall user preferences and prior context. Update it when you learn important facts about the user or project.",
	}
}

func (t *MemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"description": "The action to perform: read, add, update, delete",
				"enum": ["read", "add", "update", "delete"]
			},
			"section": {
				"type": "string",
				"description": "The section name (e.g. 'User Profile', 'Working Memory', 'Lessons Learned'). Required for add/update/delete. Optional for read (omit to read all)."
			},
			"content": {
				"type": "string",
				"description": "The content to add or delete. Required for add and delete actions."
			},
			"old": {
				"type": "string",
				"description": "The old text to replace. Required for update action."
			},
			"new": {
				"type": "string",
				"description": "The new text to replace with. Required for update action."
			}
		},
		"required": ["action"]
	}`)
}

func (t *MemoryTool) Execute(ctx context.Context, params map[string]any) (tools.ToolResult, error) {
	action, _ := params["action"].(string)
	section, _ := params["section"].(string)
	content, _ := params["content"].(string)
	old, _ := params["old"].(string)
	new_, _ := params["new"].(string)

	switch action {
	case "read":
		return t.executeRead(section)
	case "add":
		return t.executeAdd(section, content)
	case "update":
		return t.executeUpdate(section, old, new_)
	case "delete":
		return t.executeDelete(section, content)
	default:
		return tools.ToolResult{}, fmt.Errorf("unknown action: %s (use: read, add, update, delete)", action)
	}
}

func (t *MemoryTool) executeRead(section string) (tools.ToolResult, error) {
	if section != "" {
		content, err := t.store.ReadSection(section)
		if err != nil {
			return tools.ToolResult{}, err
		}
		if content == "" {
			return tools.NewTextToolResult(fmt.Sprintf("Section '%s' is empty or not found.", section)), nil
		}
		return tools.NewTextToolResult(content), nil
	}

	// Read all
	content, path, source, err := t.store.Read()
	if err != nil {
		return tools.ToolResult{}, err
	}
	if content == "" {
		return tools.NewTextToolResult("No memory file found. Use memory(action=\"add\", section=\"...\", content=\"...\") to create one."), nil
	}

	header := fmt.Sprintf("[source: %s — %s]\n\n", source, path)
	return tools.NewTextToolResult(header + content), nil
}

func (t *MemoryTool) executeAdd(section, content string) (tools.ToolResult, error) {
	if section == "" {
		return tools.ToolResult{}, fmt.Errorf("section is required for add action")
	}
	if content == "" {
		return tools.ToolResult{}, fmt.Errorf("content is required for add action")
	}

	if err := t.store.Add(section, content); err != nil {
		return tools.ToolResult{}, err
	}
	return tools.NewTextToolResult(fmt.Sprintf("Added to '%s': %s", section, content)), nil
}

func (t *MemoryTool) executeUpdate(section, old, new_ string) (tools.ToolResult, error) {
	if section == "" {
		return tools.ToolResult{}, fmt.Errorf("section is required for update action")
	}
	if old == "" {
		return tools.ToolResult{}, fmt.Errorf("old text is required for update action")
	}
	if new_ == "" {
		return tools.ToolResult{}, fmt.Errorf("new text is required for update action")
	}

	if err := t.store.Update(section, old, new_); err != nil {
		return tools.ToolResult{}, err
	}
	return tools.NewTextToolResult(fmt.Sprintf("Updated in '%s': '%s' → '%s'", section, old, new_)), nil
}

func (t *MemoryTool) executeDelete(section, content string) (tools.ToolResult, error) {
	if section == "" {
		return tools.ToolResult{}, fmt.Errorf("section is required for delete action")
	}
	if content == "" {
		return tools.ToolResult{}, fmt.Errorf("content is required for delete action")
	}

	if err := t.store.Delete(section, content); err != nil {
		return tools.ToolResult{}, err
	}
	return tools.NewTextToolResult(fmt.Sprintf("Deleted from '%s': %s", section, content)), nil
}
