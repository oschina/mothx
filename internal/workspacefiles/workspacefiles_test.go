package workspacefiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveContainment(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()

	if got, err := Resolve(root, ""); err != nil || strings.TrimSuffix(got, string(filepath.Separator)) != strings.TrimSuffix(mustAbs(root), string(filepath.Separator)) {
		t.Errorf("empty rel should resolve to root, got %q err=%v", got, err)
	}
	if got, err := Resolve(root, "a/b"); err != nil || got != sub {
		t.Errorf("a/b = %q err=%v", got, err)
	}
	if _, err := Resolve(root, "../escape"); err != ErrInvalidPath {
		t.Errorf("parent traversal must be rejected, got %v", err)
	}
	if _, err := Resolve(root, "/absolute"); err != ErrInvalidPath {
		t.Errorf("absolute rel must be rejected, got %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink unsupported: %v", err)
		return
	}
	if _, err := Resolve(root, "link/inside.txt"); err != ErrInvalidPath {
		t.Errorf("symlink escape must be rejected, got %v", err)
	}
}

func mustAbs(path string) string {
	abs, _ := filepath.Abs(path)
	return abs
}

func TestListAndRead(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "zeta.txt", "hello world\n")
	writeTestFile(t, root, "alpha.txt", "alpha\n")
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, filepath.Join("docs", "guide.md"), "# Guide\n")
	writeTestFile(t, root, "blob.bin", "\x7fELF\x00\x01")

	result, err := List(root, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs", "alpha.txt", "blob.bin", "zeta.txt"}
	var got []string
	for _, entry := range result.Entries {
		got = append(got, entry.Name)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("order = %v want %v", got, want)
	}

	sub, err := List(root, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Dir != "docs" || len(sub.Entries) != 1 || sub.Entries[0].Path != "docs/guide.md" {
		t.Errorf("sub listing = %+v", sub)
	}

	file, err := Read(root, "docs/guide.md")
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != "docs/guide.md" || file.Content != "# Guide\n" || file.Lang != "markdown" || file.Binary {
		t.Errorf("content = %+v", file)
	}

	bin, err := Read(root, "blob.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bin.Binary || bin.Content != "" {
		t.Errorf("binary read = %+v", bin)
	}

	if _, err := Read(root, "docs"); err != ErrNotFound {
		t.Errorf("directory read status = %v", err)
	}
	if _, err := Read(root, "../../etc/passwd"); err != ErrInvalidPath {
		t.Errorf("traversal read status = %v", err)
	}
}

func TestReadTooLarge(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxRead + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	result, err := Read(root, "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !result.TooLarge || result.Size != MaxRead+1 || result.Content != "" {
		t.Errorf("oversize = %+v", result)
	}
}

func TestLangFor(t *testing.T) {
	if LangFor("main.go") != "go" || LangFor("Dockerfile") != "dockerfile" || LangFor("x.y.z") != "text" {
		t.Errorf("lang mapping wrong")
	}
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
