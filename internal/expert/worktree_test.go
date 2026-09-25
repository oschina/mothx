package expert

import (
	"testing"
	"testing/fstest"
)

func worktreeBundleSource(files map[string]string) bundleSource {
	mfs := fstest.MapFS{}
	for name, content := range files {
		mfs[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return &fsSource{fsys: mfs}
}

func TestParseFrontmatterWorktree(t *testing.T) {
	fm, _, err := parseFrontmatter("---\nname: a\nworktree: true\n---\nbody\n", "f")
	if err != nil {
		t.Fatal(err)
	}
	if !fm.Worktree {
		t.Fatal("worktree: true was not parsed")
	}
	fm, _, err = parseFrontmatter("---\nname: a\nworktree: false\n---\nbody\n", "f")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Worktree {
		t.Fatal("worktree: false must not enable the worktree")
	}
}

func TestLoadAgentDefRejectsWorkDirAndWorktree(t *testing.T) {
	src := worktreeBundleSource(map[string]string{"agents/a.md": "---\nname: a\nwork_dir: /tmp/ws\nworktree: true\n---\nbody\n"})
	def, reason, err := loadAgentDef(src, "a")
	if err != nil {
		t.Fatalf("loadAgentDef: %v", err)
	}
	if def != nil || reason == "" {
		t.Fatalf("expected an invalid-bundle reason, got def=%v reason=%q", def, reason)
	}
}

func TestLoadAgentDefAcceptsWorktreeAlone(t *testing.T) {
	src := worktreeBundleSource(map[string]string{"agents/a.md": "---\nname: a\nworktree: true\n---\nbody\n"})
	def, reason, err := loadAgentDef(src, "a")
	if err != nil || reason != "" {
		t.Fatalf("loadAgentDef = %v, %q, %v", def, reason, err)
	}
	if def == nil || !def.Meta.Worktree {
		t.Fatalf("worktree declaration was dropped: %+v", def)
	}
}
