package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/oschina/mothx/internal/skills"
)

// SkillRefTool loads on-demand reference files from skills.
type SkillRefTool struct {
	skillsMgr *skills.Manager
}

// NewSkillRefTool creates a new skill reference loading tool.
func NewSkillRefTool(skillsMgr *skills.Manager) *SkillRefTool {
	return &SkillRefTool{skillsMgr: skillsMgr}
}

func (t *SkillRefTool) Name() string {
	return "skill_ref"
}

func (t *SkillRefTool) Description() string {
	return "Load a reference file from an active skill. Use this to access on-demand knowledge from skills that have reference files (e.g. references/audio.md)."
}

func (t *SkillRefTool) PromptSnippet() string {
	return "Load reference files from skills"
}

func (t *SkillRefTool) PromptGuidelines() []string {
	return nil
}

func (t *SkillRefTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"skill": {
				"type": "string",
				"description": "The skill name (directory name)"
			},
			"ref": {
				"type": "string",
				"description": "The reference file path relative to the skill directory (e.g. 'references/audio.md')"
			}
		},
		"required": ["skill", "ref"]
	}`)
}

func (t *SkillRefTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	if t.skillsMgr == nil {
		return ToolResult{}, fmt.Errorf("no skills manager available")
	}

	skillName, ok := params["skill"].(string)
	if !ok || skillName == "" {
		return ToolResult{}, fmt.Errorf("missing required parameter: skill")
	}

	refPath, ok := params["ref"].(string)
	if !ok || refPath == "" {
		return ToolResult{}, fmt.Errorf("missing required parameter: ref")
	}

	content, found := t.skillsMgr.LoadReference(skillName, refPath)
	if !found {
		// List available references to help the model
		refs := t.skillsMgr.ListReferences(skillName)
		if refs == nil {
			return ToolResult{}, fmt.Errorf("skill '%s' not found", skillName)
		}
		var available string
		for _, r := range refs {
			status := "on-demand"
			if r.AutoLoad {
				status = "auto-loaded"
			}
			if r.Loaded {
				status = "loaded"
			}
			available += fmt.Sprintf("  - %s (%s): %s\n", r.Path, status, r.Label)
		}
		return ToolResult{}, fmt.Errorf("reference '%s' not found in skill '%s'. Available references:\n%s", refPath, skillName, available)
	}

	return NewTextToolResult(content), nil
}
