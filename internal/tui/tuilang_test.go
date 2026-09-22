package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/tui/i18n"
)

func TestTUILangCommandPersistsDefaultGlobalAndExplicitProject(t *testing.T) {
	tmpDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTHX_DIR", filepath.Join(tmpDir, "global"))

	a := &App{
		settings:     config.DefaultSettings(),
		translator:   i18n.New(i18n.LanguageEN),
		tuiLangScope: "global",
	}
	a.handleTUILangCommand([]string{"/tuilang", "zh"})
	global, err := config.LoadGlobalSettingsSparse()
	if err != nil {
		t.Fatal(err)
	}
	if global.TUILang != "zh" {
		t.Fatalf("global tuilang = %q, want zh", global.TUILang)
	}
	if a.translator.Language() != i18n.LanguageZH {
		t.Fatalf("effective language = %q, want zh", a.translator.Language())
	}

	a.handleTUILangCommand([]string{"/tuilang", "project", "en"})
	project, err := config.LoadProjectSettingsSparse()
	if err != nil {
		t.Fatal(err)
	}
	if project.TUILang != "en" {
		t.Fatalf("project tuilang = %q, want en", project.TUILang)
	}
	if a.tuiLangScope != "project" || a.translator.Language() != i18n.LanguageEN {
		t.Fatalf("scope/language = %q/%q, want project/en", a.tuiLangScope, a.translator.Language())
	}
}

func TestSettingsRootRoutesTUILangActions(t *testing.T) {
	tmpDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTHX_DIR", filepath.Join(tmpDir, "global"))

	a := &App{
		settings:     config.DefaultSettings(),
		translator:   i18n.New(i18n.LanguageEN),
		tuiLangScope: "global",
	}
	a.selectSettingsRoot("tuilang.scope")
	if a.tuiLangScope != "project" {
		t.Fatalf("scope = %q, want project", a.tuiLangScope)
	}
}
