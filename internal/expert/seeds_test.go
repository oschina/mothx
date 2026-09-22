package expert

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/oschina/mothx/experts"
)

func loadSeed(t *testing.T, name string) *Bundle {
	t.Helper()
	b, err := LoadBundleFS(experts.BuiltinFS, name)
	if err != nil {
		t.Fatalf("LoadBundleFS(%s): %v", name, err)
	}
	if b.Invalid {
		t.Fatalf("seed %s invalid: %s", name, b.InvalidReason)
	}
	return b
}

func TestBuiltinSeedsLoadValid(t *testing.T) {
	// The embedded builtin layer exposes exactly the two seed packages.
	entries, err := fs.ReadDir(experts.BuiltinFS, ".")
	if err != nil {
		t.Fatalf("read BuiltinFS root: %v", err)
	}
	found := make(map[string]bool, len(entries))
	for _, entry := range entries {
		found[entry.Name()] = true
	}
	for _, seed := range []string{"software-company", "frontend-developer"} {
		if !found[seed] {
			t.Errorf("BuiltinFS missing seed directory %s", seed)
		}
		b := loadSeed(t, seed)
		if b.Name != seed {
			t.Errorf("%s: Bundle.Name = %q", seed, b.Name)
		}
	}
}

func TestSoftwareCompanySeed(t *testing.T) {
	b := loadSeed(t, "software-company")

	if b.Manifest.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", b.Manifest.SchemaVersion, SchemaVersion)
	}
	if b.Manifest.ExpertType != TypeTeam {
		t.Errorf("expertType = %q, want team", b.Manifest.ExpertType)
	}
	if b.Manifest.TeamInfo == nil {
		t.Fatalf("teamInfo missing")
	}
	if b.Manifest.AgentName != "software-team-lead" || b.Manifest.TeamInfo.LeadAgent != "software-team-lead" {
		t.Errorf("agentName/leadAgent = %q/%q", b.Manifest.AgentName, b.Manifest.TeamInfo.LeadAgent)
	}
	if b.Manifest.DisplayName.Zh != "一人公司" || b.Manifest.DisplayName.En != "Software Company" {
		t.Errorf("displayName = %+v", b.Manifest.DisplayName)
	}
	if b.Manifest.CategoryID != "engineering" {
		t.Errorf("categoryId = %q", b.Manifest.CategoryID)
	}
	if len(b.Manifest.QuickPrompts) < 2 || len(b.Manifest.QuickPrompts) > 3 {
		t.Errorf("quickPrompts = %d, want 2-3", len(b.Manifest.QuickPrompts))
	}
	for i, qp := range b.Manifest.QuickPrompts {
		if qp.Zh == "" || qp.En == "" {
			t.Errorf("quickPrompts[%d] missing zh/en: %+v", i, qp)
		}
	}
	if b.Manifest.DefaultInitPrompt.Zh == "" || b.Manifest.DefaultInitPrompt.En == "" {
		t.Errorf("defaultInitPrompt missing zh/en: %+v", b.Manifest.DefaultInitPrompt)
	}
	if len(b.Manifest.Members) != 5 {
		t.Errorf("members = %d, want 5", len(b.Manifest.Members))
	}

	wantIDs := []string{
		"software-team-lead",
		"software-product-manager",
		"software-architect",
		"software-engineer",
		"software-qa-engineer",
	}
	if len(b.Defs) != len(wantIDs) {
		t.Fatalf("len(Defs) = %d, want %d: %v", len(b.Defs), len(wantIDs), defIDs(b))
	}
	for _, id := range wantIDs {
		if _, ok := b.Defs[id]; !ok {
			t.Errorf("Defs missing %s", id)
		}
	}
	lead := b.Defs["software-team-lead"]
	if lead.Role != RoleLead {
		t.Errorf("lead role = %q, want lead", lead.Role)
	}
	if lead.DisplayName != "齐活林" {
		t.Errorf("lead DisplayName = %q, want 齐活林", lead.DisplayName)
	}
	if lead.Emoji == "" || lead.Meta.Vibe == "" || lead.Meta.Color == "" {
		t.Errorf("lead frontmatter metadata incomplete: %+v", lead.Meta)
	}
	for _, id := range wantIDs[1:] {
		def := b.Defs[id]
		if def.Role != RoleMember {
			t.Errorf("%s role = %q, want member", id, def.Role)
		}
		if def.DisplayName == "" || def.Emoji == "" || def.Description == "" {
			t.Errorf("%s persona metadata incomplete: %+v", id, def)
		}
		if def.Prompt == "" {
			t.Errorf("%s prompt empty", id)
		}
	}

	// Lead SOP must carry the full orchestration specification (proposal §7).
	for _, want := range []string{
		"subagent_spawn", "subagent_wait", "subagent_status", "subagent_send", "subagent_destroy",
		"禁止代写", "禁止模拟", "已由系统绑定",
		"非重叠", "反射式", "写集不相交",
		"⚡", "🔧", "🏗️", "📋",
		"最多 2 轮",
		"TL;DR", "文件清单", "下一步建议",
		"deliverables/software-company/",
		"hub-and-spoke",
	} {
		if !strings.Contains(lead.Prompt, want) {
			t.Errorf("lead SOP missing %q", want)
		}
	}
	// Member persona contracts.
	pm := b.Defs["software-product-manager"].Prompt
	for _, want := range []string{"背景", "用户故事", "验收标准", "非目标"} {
		if !strings.Contains(pm, want) {
			t.Errorf("PM prompt missing %q", want)
		}
	}
	arch := b.Defs["software-architect"].Prompt
	for _, want := range []string{"decision-complete", "文件级改动清单", "接口签名", "数据结构", "任务分解"} {
		if !strings.Contains(arch, want) {
			t.Errorf("architect prompt missing %q", want)
		}
	}
	eng := b.Defs["software-engineer"]
	for _, want := range []string{"ALL-AT-ONCE", "脚手架", "GLOBAL_CONSISTENCY_CHECK", "IS_PASS", "设计文档"} {
		if !strings.Contains(eng.Prompt, want) {
			t.Errorf("engineer prompt missing %q", want)
		}
	}
	// Engineer capability fields (mode/tools/max_iterations).
	if eng.Meta.Mode != "yolo" {
		t.Errorf("engineer mode = %q, want yolo", eng.Meta.Mode)
	}
	wantTools := []string{"read", "write", "edit", "bash", "grep", "find"}
	if len(eng.Meta.Tools) != len(wantTools) {
		t.Errorf("engineer tools = %v, want %v", eng.Meta.Tools, wantTools)
	} else {
		for i := range wantTools {
			if eng.Meta.Tools[i] != wantTools[i] {
				t.Errorf("engineer tools = %v, want %v", eng.Meta.Tools, wantTools)
				break
			}
		}
	}
	if eng.Meta.MaxIterations != 80 {
		t.Errorf("engineer max_iterations = %d, want 80", eng.Meta.MaxIterations)
	}
	qa := b.Defs["software-qa-engineer"].Prompt
	for _, want := range []string{"测试计划", "回归", "最小复现", "2 轮"} {
		if !strings.Contains(qa, want) {
			t.Errorf("QA prompt missing %q", want)
		}
	}
}

func TestFrontendDeveloperSeed(t *testing.T) {
	b := loadSeed(t, "frontend-developer")

	if b.Manifest.ExpertType != TypeAgent {
		t.Errorf("expertType = %q, want agent", b.Manifest.ExpertType)
	}
	if b.Manifest.AgentName != "frontend-developer" {
		t.Errorf("agentName = %q", b.Manifest.AgentName)
	}
	if b.Manifest.DisplayName.Zh != "前端开发专家" || b.Manifest.DisplayName.En != "Frontend Developer" {
		t.Errorf("displayName = %+v", b.Manifest.DisplayName)
	}
	if len(b.Manifest.Members) != 1 || b.Manifest.Members[0].Role != RoleLead {
		t.Errorf("members = %+v, want one lead entry", b.Manifest.Members)
	}
	if len(b.Defs) != 1 {
		t.Fatalf("len(Defs) = %d, want 1: %v", len(b.Defs), defIDs(b))
	}
	def := b.Defs["frontend-developer"]
	if def == nil {
		t.Fatalf("Defs missing frontend-developer")
	}
	if def.Role != RoleLead {
		t.Errorf("role = %q, want lead", def.Role)
	}
	if def.DisplayName != "前小端" {
		t.Errorf("DisplayName = %q, want 前小端", def.DisplayName)
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{"name", def.Meta.Name},
		{"description", def.Meta.Description},
		{"role", def.Meta.Role},
		{"emoji", def.Meta.Emoji},
		{"color", def.Meta.Color},
		{"vibe", def.Meta.Vibe},
	} {
		if field.value == "" {
			t.Errorf("frontmatter %s empty", field.label)
		}
	}
	for _, want := range []string{"组件化", "可访问性", "性能"} {
		if !strings.Contains(def.Prompt, want) {
			t.Errorf("frontend prompt missing %q", want)
		}
	}
	// The in-bundle skill directory is exposed but not loaded.
	if b.SkillsDir == "" {
		t.Errorf("SkillsDir empty; seed ships skills/frontend-review")
	}
	if b.SkillsFS == nil {
		t.Errorf("SkillsFS nil for embedded load")
	} else if _, err := fs.Stat(b.SkillsFS, b.SkillsDir+"/frontend-review/SKILL.md"); err != nil {
		t.Errorf("bundled skill not resolvable via SkillsFS: %v", err)
	}
	if len(b.Defs) != 1 {
		t.Errorf("skills/ must not leak into Defs")
	}
}
