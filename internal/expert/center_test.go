package expert

import (
	"path/filepath"
	"strings"
	"testing"
)

// shadowManifest builds a minimal valid agent-bundle manifest with a
// distinguishable displayName for layer-shadow assertions.
func shadowManifest(name, displayZh string) string {
	return `{
  "schemaVersion": 1,
  "name": "` + name + `",
  "expertType": "agent",
  "agentName": "solo",
  "displayName": { "zh": "` + displayZh + `", "en": "` + name + `" },
  "members": [ { "id": "solo", "name": { "zh": "` + displayZh + `", "en": "Solo" }, "role": "lead" } ]
}`
}

func shadowFiles(name, displayZh string) map[string]string {
	return map[string]string{
		"expert.json":    shadowManifest(name, displayZh),
		"agents/solo.md": "---\nname: solo\n---\nsolo body\n",
	}
}

func writeLayerBundle(t *testing.T, layerDir, name, displayZh string) {
	t.Helper()
	writeBundle(t, layerDir, name, shadowFiles(name, displayZh))
}

func TestCenterListShadowAndSort(t *testing.T) {
	globalRoot := t.TempDir()
	t.Setenv("MOTHX_DIR", globalRoot)
	projectRoot := t.TempDir()

	globalDir := filepath.Join(globalRoot, "experts")
	projectDir := filepath.Join(projectRoot, ".mothx", "experts")

	// global shadows builtin frontend-developer and adds a global-only bundle.
	writeLayerBundle(t, globalDir, "frontend-developer", "全局前端")
	writeLayerBundle(t, globalDir, "zz-global-only", "全局专属")
	// project shadows builtin software-company, global frontend-developer and
	// global zz-global-only, and adds a project-only bundle.
	writeLayerBundle(t, projectDir, "software-company", "项目软件公司")
	writeLayerBundle(t, projectDir, "frontend-developer", "项目前端")
	writeLayerBundle(t, projectDir, "zz-global-only", "项目覆盖全局")
	writeLayerBundle(t, projectDir, "aaa-project-only", "项目专属")

	c := &Center{ProjectDir: projectRoot}
	list := c.List()

	byName := make(map[string]Summary, len(list))
	for _, s := range list {
		byName[s.Name] = s
	}
	for _, want := range []struct {
		name        string
		source      string
		displayName string
	}{
		{"aaa-project-only", SourceProject, "项目专属"},
		{"frontend-developer", SourceProject, "项目前端"},
		{"software-company", SourceProject, "项目软件公司"},
		{"zz-global-only", SourceProject, "项目覆盖全局"},
	} {
		got, ok := byName[want.name]
		if !ok {
			t.Errorf("List missing %q: %v", want.name, list)
			continue
		}
		if got.Source != want.source {
			t.Errorf("%s Source = %q, want %q", want.name, got.Source, want.source)
		}
		if got.DisplayName.Zh != want.displayName {
			t.Errorf("%s DisplayName.Zh = %q, want %q", want.name, got.DisplayName.Zh, want.displayName)
		}
		if got.Invalid {
			t.Errorf("%s unexpectedly invalid: %s", want.name, got.InvalidReason)
		}
	}
	// List is sorted by name.
	for i := 1; i < len(list); i++ {
		if list[i-1].Name > list[i].Name {
			t.Fatalf("List not sorted: %v", list)
		}
	}

	// Without the project layer, global shadows builtin; builtin survives.
	globalOnly := &Center{}
	byName2 := make(map[string]Summary)
	for _, s := range globalOnly.List() {
		byName2[s.Name] = s
	}
	if got := byName2["frontend-developer"]; got.Source != SourceGlobal || got.DisplayName.Zh != "全局前端" {
		t.Errorf("global-only frontend-developer = %+v, want Source global / 全局前端", got)
	}
	if got := byName2["software-company"]; got.Source != SourceBuiltin {
		t.Errorf("global-only software-company Source = %q, want builtin", got.Source)
	}
	if got := byName2["zz-global-only"]; got.Source != SourceGlobal {
		t.Errorf("zz-global-only Source = %q, want global", got.Source)
	}
}

func TestCenterListMissingLayersTolerated(t *testing.T) {
	// Empty MOTHX_DIR (no experts/ subdir), no project layer: builtin only.
	globalRoot := t.TempDir()
	t.Setenv("MOTHX_DIR", globalRoot)

	c := &Center{}
	list := c.List()
	if len(list) != len(builtinSeedNames) {
		t.Fatalf("len(List) = %d, want %d builtin seeds: %+v", len(list), len(builtinSeedNames), list)
	}
	for i, name := range builtinSeedNames {
		if list[i].Name != name {
			t.Errorf("builtin list[%d] = %q, want %q", i, list[i].Name, name)
		}
	}
	for _, s := range list {
		if s.Source != SourceBuiltin {
			t.Errorf("%s Source = %q, want builtin", s.Name, s.Source)
		}
		if s.Invalid {
			t.Errorf("%s invalid: %s", s.Name, s.InvalidReason)
		}
		if s.ExpertType == "" {
			t.Errorf("%s ExpertType empty", s.Name)
		}
	}

	// A project directory with bundles but empty Center.ProjectDir must not
	// activate the project layer.
	projectRoot := t.TempDir()
	writeLayerBundle(t, filepath.Join(projectRoot, ".mothx", "experts"), "proj-only", "项目专属")
	if len((&Center{}).List()) != len(builtinSeedNames) {
		t.Errorf("empty ProjectDir must disable the project layer")
	}
	if len((&Center{ProjectDir: projectRoot}).List()) != len(builtinSeedNames)+1 {
		t.Errorf("ProjectDir set must add proj-only to the list")
	}
}

func TestCenterListInvalidManifestFlagged(t *testing.T) {
	globalRoot := t.TempDir()
	t.Setenv("MOTHX_DIR", globalRoot)
	projectRoot := t.TempDir()
	projectDir := filepath.Join(projectRoot, ".mothx", "experts")

	// Broken JSON in the project layer.
	writeBundle(t, projectDir, "broken-pkg", map[string]string{"expert.json": "{not json"})
	// Project bundle shadowing a valid builtin with a manifest-name mismatch.
	writeBundle(t, projectDir, "frontend-developer", map[string]string{
		"expert.json":    strings.ReplaceAll(shadowManifest("frontend-developer", "坏影"), `"name": "frontend-developer"`, `"name": "mismatched"`),
		"agents/solo.md": "---\nname: solo\n---\nbody\n",
	})

	c := &Center{ProjectDir: projectRoot}
	byName := make(map[string]Summary)
	for _, s := range c.List() {
		byName[s.Name] = s
	}
	broken, ok := byName["broken-pkg"]
	if !ok {
		t.Fatalf("broken-pkg missing from List: %+v", byName)
	}
	if !broken.Invalid || !strings.Contains(broken.InvalidReason, "expert.json") {
		t.Errorf("broken-pkg = %+v, want invalid with expert.json reason", broken)
	}
	// Shadowing does not fall through to the valid builtin copy.
	shadow, ok := byName["frontend-developer"]
	if !ok {
		t.Fatalf("frontend-developer missing from List")
	}
	if shadow.Source != SourceProject || !shadow.Invalid || !strings.Contains(shadow.InvalidReason, "不一致") {
		t.Errorf("frontend-developer = %+v, want invalid project shadow", shadow)
	}
	if _, ok := byName["software-company"]; !ok {
		t.Errorf("builtin software-company must still be listed")
	}
}

func TestCenterGet(t *testing.T) {
	globalRoot := t.TempDir()
	t.Setenv("MOTHX_DIR", globalRoot)
	projectRoot := t.TempDir()

	// Builtin seed loads valid with clean upper layers.
	c := &Center{ProjectDir: projectRoot}
	b, err := c.Get("software-company")
	if err != nil {
		t.Fatalf("Get builtin seed: %v", err)
	}
	if b.Invalid {
		t.Fatalf("builtin seed invalid: %s", b.InvalidReason)
	}
	if b.Manifest.ExpertType != TypeTeam || len(b.Defs) != 5 {
		t.Errorf("builtin seed = type %q with %d defs, want team with 5", b.Manifest.ExpertType, len(b.Defs))
	}

	// Project layer wins over builtin.
	writeLayerBundle(t, filepath.Join(projectRoot, ".mothx", "experts"), "software-company", "项目软件公司")
	b2, err := c.Get("software-company")
	if err != nil {
		t.Fatalf("Get project seed: %v", err)
	}
	if b2.Invalid {
		t.Fatalf("project bundle invalid: %s", b2.InvalidReason)
	}
	if b2.Manifest.DisplayName.Zh != "项目软件公司" {
		t.Errorf("Get returned %+v, want the project-layer bundle", b2.Manifest.DisplayName)
	}

	// Global layer wins over builtin when no project copy exists.
	writeLayerBundle(t, filepath.Join(globalRoot, "experts"), "frontend-developer", "全局前端")
	b3, err := c.Get("frontend-developer")
	if err != nil {
		t.Fatalf("Get global: %v", err)
	}
	if b3.Manifest.DisplayName.Zh != "全局前端" {
		t.Errorf("Get returned %+v, want the global-layer bundle", b3.Manifest.DisplayName)
	}

	// Not found.
	if _, err := c.Get("does-not-exist"); err == nil {
		t.Errorf("Get(does-not-exist) should fail")
	}
	// Name hygiene.
	for _, bad := range []string{"", "  ", "../escape", "a/b", `a\b`, ".."} {
		if _, err := c.Get(bad); err == nil {
			t.Errorf("Get(%q) should fail", bad)
		}
	}
}
