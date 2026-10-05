package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewManager(t *testing.T) {
	m := NewManager("/global", "/project")

	if m == nil {
		t.Fatal("expected non-nil manager")
	}

	if m.globalDir != "/global" {
		t.Errorf("expected globalDir '/global', got '%s'", m.globalDir)
	}

	if m.projectDir != "/project" {
		t.Errorf("expected projectDir '/project', got '%s'", m.projectDir)
	}
}

func TestLoadFSLoadsEmbeddedSkillAndReference(t *testing.T) {
	fsys := fstest.MapFS{
		"bundle/skills/embedded/SKILL.md": {
			Data: []byte("# Embedded Skill\n### Guide (references/guide.md) [已加载]\n"),
		},
		"bundle/skills/embedded/references/guide.md": {
			Data: []byte("# Embedded Guide\nUse the shared runtime."),
		},
	}
	m := NewManager("", "")
	if err := m.LoadFS(fsys, "bundle/skills", "expert"); err != nil {
		t.Fatalf("load embedded skills: %v", err)
	}

	skill := m.Get("embedded")
	if skill == nil {
		t.Fatal("embedded skill missing")
	}
	if skill.Source != "expert" {
		t.Fatalf("skill source = %q, want expert", skill.Source)
	}
	context := m.BuildSkillContext("embedded")
	if !strings.Contains(context, "Embedded Guide") {
		t.Fatalf("auto-loaded embedded reference missing from context: %q", context)
	}
	content, ok := m.LoadReference("embedded", "references/guide.md")
	if !ok || !strings.Contains(content, "shared runtime") {
		t.Fatalf("embedded reference = %q, %v", content, ok)
	}
}

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	projectDir := filepath.Join(tmpDir, "project")

	// Create global skill
	globalSkillDir := filepath.Join(globalDir, "test-skill")
	os.MkdirAll(globalSkillDir, 0755)
	os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte("# Test Skill\nThis is a test skill."), 0644)

	// Create project skill
	projectSkillDir := filepath.Join(projectDir, "project-skill")
	os.MkdirAll(projectSkillDir, 0755)
	os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte("# Project Skill\nProject specific skill."), 0644)

	m := NewManager(globalDir, projectDir)
	err := m.Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	skills := m.List()
	if len(skills) != 4 {
		t.Errorf("expected 2 local and 2 built-in skills, got %d", len(skills))
	}
}

func TestLoadOverride(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	projectDir := filepath.Join(tmpDir, "project")

	// Create global skill
	globalSkillDir := filepath.Join(globalDir, "test-skill")
	os.MkdirAll(globalSkillDir, 0755)
	os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte("# Global Skill"), 0644)

	// Create project skill with same name
	projectSkillDir := filepath.Join(projectDir, "test-skill")
	os.MkdirAll(projectSkillDir, 0755)
	os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte("# Project Skill"), 0644)

	m := NewManager(globalDir, projectDir)
	m.Load()

	skills := m.List()
	if len(skills) != 3 {
		t.Errorf("expected project override plus 2 built-in skills, got %d", len(skills))
	}

	// Should be project version
	skill := m.Get("test-skill")
	if skill == nil {
		t.Fatal("expected project skill")
	}
	if skill.Content != "# Project Skill" {
		t.Errorf("expected project skill content, got '%s'", skill.Content)
	}

	if skill.Source != "project" {
		t.Errorf("expected source 'project', got '%s'", skill.Source)
	}
}

func TestLoadProjectDirsPriority(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	projectSkillsDir := filepath.Join(tmpDir, "repo", ".skills")
	projectPlainSkillsDir := filepath.Join(tmpDir, "repo", "skills")

	globalSkillDir := filepath.Join(globalDir, "test-skill")
	os.MkdirAll(globalSkillDir, 0755)
	os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte("# Global Skill"), 0644)

	plainSkillDir := filepath.Join(projectPlainSkillsDir, "test-skill")
	os.MkdirAll(plainSkillDir, 0755)
	os.WriteFile(filepath.Join(plainSkillDir, "SKILL.md"), []byte("# Plain Project Skill"), 0644)

	dotSkillDir := filepath.Join(projectSkillsDir, "test-skill")
	os.MkdirAll(dotSkillDir, 0755)
	os.WriteFile(filepath.Join(dotSkillDir, "SKILL.md"), []byte("# Dot Project Skill"), 0644)

	m := NewManagerWithProjectDirs(globalDir, []string{projectSkillsDir, projectPlainSkillsDir})
	m.Load()

	skill := m.Get("test-skill")
	if skill == nil {
		t.Fatal("expected test-skill to load")
	}
	if skill.Content != "# Dot Project Skill" {
		t.Errorf("expected .skills to override skills and global, got '%s'", skill.Content)
	}
	if skill.Source != "project" {
		t.Errorf("expected source 'project', got '%s'", skill.Source)
	}
}

func TestProjectSkillDirsIncludesAgentsSkills(t *testing.T) {
	projectRoot := filepath.Join("/tmp", "repo")
	dirs := ProjectSkillDirs(projectRoot)
	want := filepath.Join(projectRoot, ".agents", "skills")

	found := false
	for _, dir := range dirs {
		if dir == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ProjectSkillDirs() = %v, want %q", dirs, want)
	}
}

func TestLoadAgentsSkillsDir(t *testing.T) {
	projectRoot := t.TempDir()
	skillDir := filepath.Join(projectRoot, ".agents", "skills", "agents-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir agents skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Agents Skill"), 0644); err != nil {
		t.Fatalf("write agents skill: %v", err)
	}

	m := NewManagerWithProjectDirs("", ProjectSkillDirs(projectRoot))
	if err := m.Load(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if skill := m.Get("agents-skill"); skill == nil {
		t.Fatal("expected skill from .agents/skills")
	} else if skill.Path != filepath.Join(skillDir, "SKILL.md") {
		t.Errorf("skill path = %q, want %q", skill.Path, filepath.Join(skillDir, "SKILL.md"))
	}
}
func TestLoadProjectPlainSkillsDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	projectRoot := filepath.Join(tmpDir, "repo")
	projectPlainSkillsDir := filepath.Join(projectRoot, "skills")

	plainSkillDir := filepath.Join(projectPlainSkillsDir, "plain-skill")
	os.MkdirAll(plainSkillDir, 0755)
	os.WriteFile(filepath.Join(plainSkillDir, "SKILL.md"), []byte("# Plain Project Skill"), 0644)

	m := NewManagerWithProjectDirs(globalDir, ProjectSkillDirs(projectRoot))
	m.Load()

	skill := m.Get("plain-skill")
	if skill == nil {
		t.Fatal("expected plain-skill to load from project skills directory")
	}
	if skill.Content != "# Plain Project Skill" {
		t.Errorf("expected project skills content, got '%s'", skill.Content)
	}
}

func TestLoadNonExistentDir(t *testing.T) {
	m := NewManager("/nonexistent", "/nonexistent")
	err := m.Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	skills := m.List()
	if len(skills) != 2 || m.Get(ExpertCreaterSkillName) == nil || m.Get("vibe-browser") == nil {
		t.Errorf("expected both built-in skills, got %#v", skills)
	}
}

func TestLoadEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	m := NewManager(tmpDir, "")
	err := m.Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	skills := m.List()
	if len(skills) != 2 || m.Get(ExpertCreaterSkillName) == nil || m.Get("vibe-browser") == nil {
		t.Errorf("expected both built-in skills, got %#v", skills)
	}
}

func TestLoadSkillMd(t *testing.T) {
	tmpDir := t.TempDir()

	// Test lowercase skill.md
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "skill.md"), []byte("# Lowercase Skill"), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	skills := m.List()
	if len(skills) != 3 {
		t.Errorf("expected lowercase and 2 built-in skills, got %d", len(skills))
	}
}

func TestGet(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill"), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	// Get existing skill
	skill := m.Get("test-skill")
	if skill == nil {
		t.Fatal("expected non-nil skill")
	}

	if skill.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got '%s'", skill.Name)
	}

	// Get non-existing skill
	skill = m.Get("nonexistent")
	if skill != nil {
		t.Error("expected nil skill")
	}
}

func TestList(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple skills
	for _, name := range []string{"skill1", "skill2", "skill3"} {
		skillDir := filepath.Join(tmpDir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name), 0644)
	}

	m := NewManager(tmpDir, "")
	m.Load()

	skills := m.List()
	if len(skills) != 5 {
		t.Errorf("expected 3 local and 2 built-in skills, got %d", len(skills))
	}
}

func TestListBySource(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	projectDir := filepath.Join(tmpDir, "project")

	// Create global skill
	globalSkillDir := filepath.Join(globalDir, "global-skill")
	os.MkdirAll(globalSkillDir, 0755)
	os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte("# Global"), 0644)

	// Create project skill
	projectSkillDir := filepath.Join(projectDir, "project-skill")
	os.MkdirAll(projectSkillDir, 0755)
	os.WriteFile(filepath.Join(projectSkillDir, "SKILL.md"), []byte("# Project"), 0644)

	m := NewManager(globalDir, projectDir)
	m.Load()

	// List global
	globalSkills := m.ListBySource("global")
	if len(globalSkills) != 1 {
		t.Errorf("expected 1 global skill, got %d", len(globalSkills))
	}

	// List project
	projectSkills := m.ListBySource("project")
	if len(projectSkills) != 1 {
		t.Errorf("expected 1 project skill, got %d", len(projectSkills))
	}

	// List unknown
	unknownSkills := m.ListBySource("unknown")
	if len(unknownSkills) != 0 {
		t.Errorf("expected 0 unknown skills, got %d", len(unknownSkills))
	}
}

func TestNames(t *testing.T) {
	tmpDir := t.TempDir()

	// Create multiple skills
	for _, name := range []string{"skill1", "skill2", "skill3"} {
		skillDir := filepath.Join(tmpDir, name)
		os.MkdirAll(skillDir, 0755)
		os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# "+name), 0644)
	}

	m := NewManager(tmpDir, "")
	m.Load()

	names := m.Names()
	if len(names) != 5 {
		t.Errorf("expected 3 local and 2 built-in names, got %d", len(names))
	}
}

func TestBuildSkillContext(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill\nThis is a test."), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	context := m.BuildSkillContext("test-skill")

	if context == "" {
		t.Error("expected non-empty context")
	}

	if !contains(context, "test-skill") {
		t.Error("expected context to contain 'test-skill'")
	}

	// Test non-existing skill
	context = m.BuildSkillContext("nonexistent")
	if context != "" {
		t.Error("expected empty context for non-existing skill")
	}
}

func TestBuildAllSkillsContext(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill\nThis is a test."), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	context := m.BuildAllSkillsContext()

	if context == "" {
		t.Error("expected non-empty context")
	}

	if !contains(context, "test-skill") {
		t.Error("expected context to contain 'test-skill'")
	}
}

func TestBuiltInExpertCreaterSkillIsDiscoverable(t *testing.T) {
	m := NewManager("", "")
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	skill := m.Get(ExpertCreaterSkillName)
	if skill == nil || skill.Source != "builtin" {
		t.Fatalf("built-in expert creator = %#v", skill)
	}
	context := m.BuildSkillContext(ExpertCreaterSkillName)
	if !strings.Contains(context, ".mothx/experts/<team-id>") || !strings.Contains(context, "expert.json") {
		t.Fatalf("expert creator instructions are incomplete: %q", context)
	}
}

func TestBuildAllSkillsContextIncludesBuiltIns(t *testing.T) {
	m := NewManager("", "")
	m.Load()
	context := m.BuildAllSkillsContext()
	if !strings.Contains(context, ExpertCreaterSkillName) {
		t.Errorf("expected built-in skill listing, got %q", context)
	}
}

func TestExtractDescription(t *testing.T) {
	tests := []struct {
		content  string
		expected string
	}{
		{"# Test Skill\nDescription here", "Test Skill"},
		{"## Heading\nDescription", "Heading"},
		{"Description without heading", "Description without heading"},
		{"", "(no description)"},
	}

	for _, tt := range tests {
		result := extractDescription(tt.content)
		if result != tt.expected {
			t.Errorf("expected '%s', got '%s'", tt.expected, result)
		}
	}
}

func TestCreateProjectSkillsDir(t *testing.T) {
	tmpDir := t.TempDir()

	err := CreateProjectSkillsDir(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check directory was created
	skillsDir := filepath.Join(tmpDir, ".skills")
	if _, err := os.Stat(skillsDir); os.IsNotExist(err) {
		t.Error("expected skills directory to exist")
	}
}

func TestParseReferences(t *testing.T) {
	tmpDir := t.TempDir()

	content := `# API Skill

### 1. 基础 (references/base.md) [已加载]
### 2. 高级 (references/advanced.md) [待按需加载]

## References
- [概述](references/overview.md)
`

	refs := parseReferences(content, tmpDir, nil)
	if len(refs) != 3 {
		t.Fatalf("expected 3 references, got %d", len(refs))
	}

	// Check first ref is auto-load
	if !refs[0].AutoLoad {
		t.Error("expected first ref to be auto-loaded")
	}
	if refs[0].Path != "references/base.md" {
		t.Errorf("expected path 'references/base.md', got %q", refs[0].Path)
	}

	// Check second ref is on-demand
	if refs[1].AutoLoad {
		t.Error("expected second ref to be on-demand")
	}

	// Check third ref from markdown link
	if refs[2].Path != "references/overview.md" {
		t.Errorf("expected path 'references/overview.md', got %q", refs[2].Path)
	}
	if refs[2].Label != "概述" {
		t.Errorf("expected label '概述', got %q", refs[2].Label)
	}
}

func TestParseReferencesDedup(t *testing.T) {
	tmpDir := t.TempDir()

	// Same ref in both header and link - should deduplicate
	content := `# Skill
### 1. Base (references/base.md) [已加载]
- [Base](references/base.md)
`
	refs := parseReferences(content, tmpDir, nil)
	if len(refs) != 1 {
		t.Errorf("expected 1 reference (deduped), got %d", len(refs))
	}
}

func TestParseReferencesRejectsEscapingPaths(t *testing.T) {
	tmpDir := t.TempDir()

	content := `# Skill
### 1. Escape (../../../etc/passwd.md) [已加载]
### 2. Abs (/etc/shadow.md) [已加载]
### 3. OK (references/base.md) [已加载]
- [Escape Link](../../secret.txt)
- [OK Link](references/overview.md)
`
	refs := parseReferences(content, tmpDir, nil)
	if len(refs) != 2 {
		t.Fatalf("expected 2 references (escaping paths rejected), got %d", len(refs))
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.FullPath, filepath.Clean(tmpDir)+string(filepath.Separator)) {
			t.Errorf("reference %q escapes skill dir: %q", ref.Path, ref.FullPath)
		}
	}
	if refs[0].Path != "references/base.md" || refs[1].Path != "references/overview.md" {
		t.Errorf("unexpected surviving refs: %q, %q", refs[0].Path, refs[1].Path)
	}
}

func TestParseReferencesEmpty(t *testing.T) {
	refs := parseReferences("# No references here", "/tmp", nil)
	if len(refs) != 0 {
		t.Errorf("expected 0 references, got %d", len(refs))
	}
}

func TestLoadReference(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	refsDir := filepath.Join(skillDir, "references")
	os.MkdirAll(refsDir, 0755)

	// Create skill with references
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`# Test
### 1. Base (references/base.md) [待按需加载]
`), 0644)
	os.WriteFile(filepath.Join(refsDir, "base.md"), []byte("# Base Content\nThis is the base."), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	// Load a known reference
	content, ok := m.LoadReference("test-skill", "references/base.md")
	if !ok {
		t.Fatal("expected successful load")
	}
	if !contains(content, "Base Content") {
		t.Errorf("expected content to contain 'Base Content', got %q", content)
	}

	// Load again (should use cached)
	content2, ok := m.LoadReference("test-skill", "references/base.md")
	if !ok {
		t.Fatal("expected successful cached load")
	}
	if content != content2 {
		t.Error("expected same content on cached load")
	}
}

func TestLoadReferenceDirectFile(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)

	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill"), 0644)
	os.WriteFile(filepath.Join(skillDir, "extra.md"), []byte("# Extra"), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	// Load directly by path (not a parsed reference)
	content, ok := m.LoadReference("test-skill", "extra.md")
	if !ok {
		t.Fatal("expected successful direct load")
	}
	if !contains(content, "Extra") {
		t.Error("expected content to contain 'Extra'")
	}
}

func TestLoadReferencePathEscape(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test"), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	// Attempt path traversal
	_, ok := m.LoadReference("test-skill", "../../etc/passwd")
	if ok {
		t.Error("expected path escape to be blocked")
	}
}

func TestLoadReferenceNonexistentSkill(t *testing.T) {
	m := NewManager("", "")
	m.Load()

	_, ok := m.LoadReference("nonexistent", "file.md")
	if ok {
		t.Error("expected false for nonexistent skill")
	}
}

func TestListReferences(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`# Test
### 1. Ref (references/ref.md) [待按需加载]
`), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	refs := m.ListReferences("test-skill")
	if len(refs) != 1 {
		t.Errorf("expected 1 reference, got %d", len(refs))
	}

	// Nonexistent skill
	refs = m.ListReferences("nonexistent")
	if refs != nil {
		t.Error("expected nil for nonexistent skill")
	}
}

func TestBuildSkillContextWithReferences(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	refsDir := filepath.Join(skillDir, "references")
	os.MkdirAll(refsDir, 0755)

	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`# Test Skill
### 1. Auto (references/auto.md) [已加载]
### 2. OnDemand (references/ondemand.md) [待按需加载]
`), 0644)
	os.WriteFile(filepath.Join(refsDir, "auto.md"), []byte("Auto-loaded content"), 0644)
	os.WriteFile(filepath.Join(refsDir, "ondemand.md"), []byte("On-demand content"), 0644)

	m := NewManager(tmpDir, "")
	m.Load()

	ctx := m.BuildSkillContext("test-skill")

	if !contains(ctx, "Auto-loaded content") {
		t.Error("expected auto-loaded content in context")
	}
	if contains(ctx, "On-demand content") {
		t.Error("on-demand content should NOT be auto-loaded")
	}
	if !contains(ctx, "On-Demand References") {
		t.Error("expected on-demand references section")
	}
	if !contains(ctx, "skill_ref") {
		t.Error("expected skill_ref tool mention")
	}
}

func TestSkill(t *testing.T) {
	skill := &Skill{
		Name:        "test",
		Path:        "/path/to/skill",
		Description: "Test skill",
		Content:     "# Test",
		Source:      "project",
	}

	if skill.Name != "test" {
		t.Errorf("expected name 'test', got '%s'", skill.Name)
	}

	if skill.Source != "project" {
		t.Errorf("expected source 'project', got '%s'", skill.Source)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
