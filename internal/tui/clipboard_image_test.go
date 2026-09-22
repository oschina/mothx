package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

type fakeClipboardImageSaver struct {
	path string
	ok   bool
	err  error
}

func (f fakeClipboardImageSaver) SaveImage(context.Context, string) (string, bool, error) {
	return f.path, f.ok, f.err
}

type fakeFileOpener struct {
	opened []string
	err    error
}

func (f *fakeFileOpener) Open(path string) error {
	f.opened = append(f.opened, path)
	return f.err
}

func TestPasteImageCommandInsertsPathAndHintsPreview(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.cwd = t.TempDir()
	absPath := config.ProjectPathFor(a.cwd, "tmp", "paste-1.png")
	a.clipboardImageSaver = fakeClipboardImageSaver{path: absPath, ok: true}

	a.handleCommand("/paste-image")

	if got := a.input.Value(); got != "Image Path : .mothx/tmp/paste-1.png" {
		t.Fatalf("input = %q, want pasted image path", got)
	}
	if a.lastPastedImagePath != absPath {
		t.Fatalf("last pasted path = %q", a.lastPastedImagePath)
	}
	if len(a.messages) == 0 {
		t.Fatal("expected status message")
	}
	plain := stripANSI(a.messages[len(a.messages)-1])
	if !strings.Contains(plain, "Image pasted: .mothx/tmp/paste-1.png") || !strings.Contains(plain, "Press Ctrl+R to preview.") {
		t.Fatalf("status = %q, want paste path and Ctrl+R hint", plain)
	}
}

func TestPasteImageCommandHandlesNonImageClipboard(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.clipboardImageSaver = fakeClipboardImageSaver{ok: false}

	a.handleCommand("/paste-image")

	if got := a.input.Value(); got != "" {
		t.Fatalf("input = %q, want empty", got)
	}
	plain := stripANSI(a.messages[len(a.messages)-1])
	if !strings.Contains(plain, "Clipboard does not contain a PNG image") {
		t.Fatalf("status = %q, want non-image clipboard message", plain)
	}
}

func TestPreviewLastPastedImageUsesCtrlR(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	opener := &fakeFileOpener{}
	a.fileOpener = opener
	a.lastPastedImagePath = "/tmp/paste-1.png"

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	if len(opener.opened) != 1 || opener.opened[0] != "/tmp/paste-1.png" {
		t.Fatalf("opened = %#v, want latest pasted image path", opener.opened)
	}
	plain := stripANSI(a.messages[len(a.messages)-1])
	if !strings.Contains(plain, "Opened preview: /tmp/paste-1.png") {
		t.Fatalf("status = %q, want opened preview message", plain)
	}
}

func TestPreviewLastPastedImageReportsOpenError(t *testing.T) {
	settings := config.DefaultSettings()
	settings.TUILang = "en"
	a := NewApp(nil, &provider.Model{Name: "test"}, settings, nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.fileOpener = &fakeFileOpener{err: errors.New("no display")}
	a.lastPastedImagePath = "/tmp/paste-1.png"

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	plain := stripANSI(a.messages[len(a.messages)-1])
	if !strings.Contains(plain, "Could not open pasted image: no display") || !strings.Contains(plain, "/tmp/paste-1.png") {
		t.Fatalf("status = %q, want open error and path", plain)
	}
}
