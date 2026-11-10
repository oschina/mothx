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

// builtinSeedNames is the complete builtin catalog. Keep it sorted: the test
// also asserts that the embedded root has exactly these directories.
var builtinSeedNames = []string{
	"agent-platform",
	"backend-platform",
	"data-ml-ai",
	"devops-sre",
	"docs-research",
	"enterprise-jvm-dotnet",
	"frontend-developer",
	"mobile-crossplatform",
	"product-architecture",
	"qa-testing",
	"security-compliance",
	"software-company",
	"systems-lowlevel",
	"web-frontend",
}

// roleBasedSeedNames are the bundles added in the ECC absorption pass. Their
// UI names use a duty-based convention ("...负责人") instead of personal names.
var roleBasedSeedNames = []string{
	"agent-platform",
	"backend-platform",
	"data-ml-ai",
	"devops-sre",
	"docs-research",
	"enterprise-jvm-dotnet",
	"mobile-crossplatform",
	"product-architecture",
	"qa-testing",
	"security-compliance",
	"systems-lowlevel",
	"web-frontend",
}

func TestBuiltinSeedsLoadValid(t *testing.T) {
	entries, err := fs.ReadDir(experts.BuiltinFS, ".")
	if err != nil {
		t.Fatalf("read BuiltinFS root: %v", err)
	}
	found := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			found[entry.Name()] = true
		}
	}
	if len(found) != len(builtinSeedNames) {
		t.Fatalf("BuiltinFS has %d bundles, want %d: %v", len(found), len(builtinSeedNames), found)
	}
	for _, seed := range builtinSeedNames {
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

// The counts below are the intended builtin catalog contract. Adding or
// removing a bundle or role must update them deliberately instead of letting
// the catalog drift unnoticed.
func TestBuiltinSeedTotals(t *testing.T) {
	newRoleCount := 0
	totalDefs := 0
	for _, seed := range builtinSeedNames {
		b := loadSeed(t, seed)
		totalDefs += len(b.Defs)
		for _, name := range roleBasedSeedNames {
			if name == seed {
				newRoleCount += len(b.Defs)
				break
			}
		}
	}
	if len(builtinSeedNames) != 14 {
		t.Fatalf("builtin bundle count = %d, want 14", len(builtinSeedNames))
	}
	if len(roleBasedSeedNames) != 12 {
		t.Fatalf("role-based bundle count = %d, want 12", len(roleBasedSeedNames))
	}
	if newRoleCount != 58 {
		t.Fatalf("role-based agent count = %d, want 58", newRoleCount)
	}
	if totalDefs != 64 {
		t.Fatalf("total builtin agent count = %d, want 64", totalDefs)
	}
}

func TestRoleBasedSeedsUseDutyNames(t *testing.T) {
	for _, seed := range roleBasedSeedNames {
		t.Run(seed, func(t *testing.T) {
			b := loadSeed(t, seed)
			if b.Manifest.ExpertType != TypeTeam {
				t.Fatalf("expertType = %q, want team", b.Manifest.ExpertType)
			}
			if b.Manifest.TeamInfo == nil {
				t.Fatal("teamInfo missing")
			}
			if b.Manifest.AgentName != b.Manifest.TeamInfo.LeadAgent {
				t.Fatalf("agentName %q != leadAgent %q", b.Manifest.AgentName, b.Manifest.TeamInfo.LeadAgent)
			}
			if len(b.Manifest.Members) < 4 || len(b.Manifest.Members) > 5 {
				t.Fatalf("members = %d, want 4-5", len(b.Manifest.Members))
			}
			if len(b.Defs) != len(b.Manifest.Members) {
				t.Fatalf("Defs = %d, members = %d", len(b.Defs), len(b.Manifest.Members))
			}
			if len(b.Manifest.QuickPrompts) < 2 || len(b.Manifest.QuickPrompts) > 3 {
				t.Fatalf("quickPrompts = %d, want 2-3", len(b.Manifest.QuickPrompts))
			}
			if b.Manifest.Members[0].ID != b.Manifest.TeamInfo.LeadAgent || b.Manifest.Members[0].Role != RoleLead {
				t.Fatalf("members[0] = %+v, want the lead first", b.Manifest.Members[0])
			}
			seenMembers := make(map[string]bool, len(b.Manifest.Members))
			for _, member := range b.Manifest.Members {
				if seenMembers[member.ID] {
					t.Errorf("duplicate members[] id %q", member.ID)
				}
				seenMembers[member.ID] = true
			}
			for _, id := range append([]string{b.Manifest.TeamInfo.LeadAgent}, b.Manifest.TeamInfo.MemberAgents...) {
				if !seenMembers[id] {
					t.Errorf("teamInfo references %q but members[] does not list it", id)
				}
			}
			if len(seenMembers) != len(b.Manifest.TeamInfo.MemberAgents)+1 {
				t.Errorf("members[] has %d entries, teamInfo describes %d", len(seenMembers), len(b.Manifest.TeamInfo.MemberAgents)+1)
			}
			for i, prompt := range b.Manifest.QuickPrompts {
				if prompt.Zh == "" || prompt.En == "" {
					t.Errorf("quickPrompts[%d] missing zh/en: %+v", i, prompt)
				}
			}
			if b.Manifest.DefaultInitPrompt.Zh == "" || b.Manifest.DefaultInitPrompt.En == "" {
				t.Error("defaultInitPrompt missing zh/en")
			}
			if strings.TrimSpace(b.Manifest.CategoryID) == "" {
				t.Error("categoryId is required for the builtin catalog")
			}
			if b.SkillsDir == "" || b.SkillsFS == nil {
				t.Fatal("role-based team must ship a skills/ pack")
			}
			skillEntries, err := fs.ReadDir(b.SkillsFS, b.SkillsDir)
			if err != nil {
				t.Fatalf("read bundle skills: %v", err)
			}
			if len(skillEntries) == 0 {
				t.Fatal("bundle skills/ is empty")
			}
			for _, member := range b.Manifest.Members {
				if !strings.HasSuffix(member.Name.Zh, "负责人") {
					t.Errorf("member %s name %q must use the duty-based 负责人 convention", member.ID, member.Name.Zh)
				}
				if member.Name.En == "" || !strings.HasSuffix(member.Name.En, "Lead") {
					t.Errorf("member %s English name %q must end with Lead", member.ID, member.Name.En)
				}
				if member.Profession.Zh == "" || member.Profession.En == "" {
					t.Errorf("member %s profession missing zh/en", member.ID)
				}
				if member.Name.Zh == member.Profession.Zh {
					t.Errorf("member %s name and profession are identical: %q", member.ID, member.Name.Zh)
				}
				def := b.Defs[member.ID]
				if def == nil {
					t.Errorf("missing def %s", member.ID)
					continue
				}
				if def.Prompt == "" || def.Description == "" || def.Emoji == "" || def.Meta.Color == "" || def.Meta.Vibe == "" {
					t.Errorf("member %s persona metadata incomplete: %+v", member.ID, def)
				}
				if def.Role != member.Role {
					t.Errorf("member %s role = %q, want %q", member.ID, def.Role, member.Role)
				}
			}
			lead := b.Defs[b.Manifest.TeamInfo.LeadAgent]
			if lead == nil {
				t.Fatalf("missing lead %s", b.Manifest.TeamInfo.LeadAgent)
			}
			for _, want := range []string{"subagent_spawn", "写集不相交", "反射式", "调度纪律"} {
				if !strings.Contains(lead.Prompt, want) {
					t.Errorf("lead prompt missing %q", want)
				}
			}
		})
	}
}
