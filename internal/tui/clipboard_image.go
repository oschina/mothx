package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oschina/mothx/internal/agentruntime"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/session"
	"github.com/oschina/mothx/internal/tui/i18n"
)

const pastedImageMaxBytes = 20 << 20

// ClipboardImageSource exposes authenticated clipboard bytes as a one-shot
// Runtime input stream. The TUI never chooses a project cache path.
type ClipboardImageSource interface {
	OpenImage(ctx context.Context) (stream agentruntime.InputStream, ok bool, err error)
}

// ClipboardImageSaver is a compatibility bridge for old tests/integrations.
// Production uses ClipboardImageSource and Runtime.PrepareInput instead.
type ClipboardImageSaver interface {
	SaveImage(ctx context.Context, projectDir string) (path string, ok bool, err error)
}

type FileOpener interface {
	Open(path string) error
}

type systemFileOpener struct{}

func newSystemClipboardImageSource() ClipboardImageSource {
	return systemClipboardImageSource{}
}

func (systemFileOpener) Open(path string) error {
	return platform.OpenFile(path)
}

func (a *App) handlePasteImageCommand() {
	// Keep the old injected saver usable for existing package tests. It is not
	// installed by NewApp and therefore cannot become the production path.
	if a.clipboardImageSaver != nil {
		path, ok, err := a.clipboardImageSaver.SaveImage(context.Background(), a.currentCwd())
		if err != nil {
			a.addCommandError(a.translator.Text(i18n.MsgClipboardPasteFailed, err))
			return
		}
		if !ok {
			a.addCommandStatus(a.translator.Text(i18n.MsgClipboardNoPNG))
			return
		}
		a.insertPastedImage(path)
		return
	}
	if err := a.ensureSession(); err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgClipboardPasteFailed, err))
		return
	}
	if err := a.ensureRuntime(); err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgClipboardPasteFailed, err))
		return
	}
	if a.clipboardImageSource == nil {
		a.clipboardImageSource = newSystemClipboardImageSource()
	}
	stream, ok, err := a.clipboardImageSource.OpenImage(context.Background())
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgClipboardPasteFailed, err))
		return
	}
	if !ok {
		a.addCommandStatus(a.translator.Text(i18n.MsgClipboardNoPNG))
		return
	}
	prepared, err := a.runtime.PrepareInput(context.Background(), agentruntime.InputIngress{
		Origin: "tui", EventID: "tui-paste-" + session.GenerateID(), Kind: agentruntime.AttachmentImage,
		FilenameHint: "clipboard.png", MediaTypeHint: "image/png",
		Open: func(context.Context) (agentruntime.InputStream, error) { return stream, nil },
	})
	if err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgClipboardPasteFailed, err))
		return
	}
	a.pendingInputResources = append(a.pendingInputResources, prepared)
	path := filepath.Join(a.currentCwd(), filepath.FromSlash(prepared.RelativePath))
	a.insertPastedImage(path)
}

func (a *App) insertPastedImage(path string) {
	displayPath := pastedImageDisplayPath(a.currentCwd(), path)
	a.input = a.input.InsertString(a.translator.Text(i18n.MsgClipboardImagePath, displayPath))
	a.lastPastedImagePath = path
	a.updateCommandSuggestions()
	a.scheduleRender()
	a.addCommandStatus(a.translator.Text(i18n.MsgClipboardImagePasted, displayPath), a.translator.Text(i18n.MsgClipboardPreviewHint))
}

func (a *App) previewLastPastedImage() tea.Cmd {
	if a.lastPastedImagePath == "" {
		a.addCommandStatus(a.translator.Text(i18n.MsgClipboardNoImage))
		return nil
	}
	if a.fileOpener == nil {
		a.fileOpener = systemFileOpener{}
	}
	if err := a.fileOpener.Open(a.lastPastedImagePath); err != nil {
		a.addCommandError(a.translator.Text(i18n.MsgClipboardOpenFailed, err, a.lastPastedImagePath))
		return nil
	}
	a.addCommandStatus(a.translator.Text(i18n.MsgClipboardOpened, a.lastPastedImagePath))
	return nil
}

func pastedImageDisplayPath(projectDir string, path string) string {
	if rel, err := filepath.Rel(projectDir, path); err == nil && rel != "." && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	return path
}

type systemClipboardImageSource struct{}

func (systemClipboardImageSource) OpenImage(ctx context.Context) (agentruntime.InputStream, bool, error) {
	var output []byte
	var ok bool
	var err error
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("pngpaste"); err != nil {
			return agentruntime.InputStream{}, false, fmt.Errorf("pngpaste not found; install pngpaste or enter an image path manually")
		}
		output, err = exec.CommandContext(ctx, "pngpaste", "-").Output()
		ok = err == nil
	case "windows":
		output, ok, err = readWindowsClipboardPNG(ctx)
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("wl-paste"); err == nil {
				output, ok, err = clipboardCommandOutput(ctx, "wl-paste", "--type", "image/png")
				if ok || err != nil {
					break
				}
			}
		}
		if !ok && err == nil {
			if _, lookErr := exec.LookPath("xclip"); lookErr == nil {
				output, ok, err = clipboardCommandOutput(ctx, "xclip", "-selection", "clipboard", "-t", "image/png", "-o")
			}
		}
		if !ok && err == nil {
			if os.Getenv("WAYLAND_DISPLAY") != "" {
				return agentruntime.InputStream{}, false, fmt.Errorf("wl-paste or xclip not found; install wl-clipboard or xclip, or enter an image path manually")
			}
			return agentruntime.InputStream{}, false, fmt.Errorf("xclip not found; install xclip or enter an image path manually")
		}
	}
	if err != nil {
		if ok {
			return agentruntime.InputStream{}, false, err
		}
		return agentruntime.InputStream{}, false, nil
	}
	if len(output) == 0 {
		return agentruntime.InputStream{}, false, nil
	}
	if len(output) > pastedImageMaxBytes {
		return agentruntime.InputStream{}, false, fmt.Errorf("pasted image too large: %d bytes (max %d)", len(output), pastedImageMaxBytes)
	}
	return agentruntime.InputStream{Reader: io.NopCloser(bytes.NewReader(output)), Filename: "clipboard.png", MediaType: "image/png", ContentSize: int64(len(output))}, true, nil
}

func clipboardCommandOutput(ctx context.Context, name string, args ...string) ([]byte, bool, error) {
	output, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, false, nil
	}
	return output, true, nil
}

func readWindowsClipboardPNG(ctx context.Context) ([]byte, bool, error) {
	powershell, err := exec.LookPath("powershell.exe")
	if err != nil {
		powershell, err = exec.LookPath("powershell")
	}
	if err != nil {
		return nil, false, fmt.Errorf("PowerShell not found; enter an image path manually")
	}
	script := `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$image = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -eq $image) { exit 2 }
$stream = New-Object System.IO.MemoryStream
$image.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
[Convert]::ToBase64String($stream.ToArray())
`
	output, err := exec.CommandContext(ctx, powershell, "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return nil, false, nil
		}
		return nil, false, err
	}
	data := bytes.TrimSpace(output)
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, false, err
	}
	if len(decoded) > pastedImageMaxBytes {
		return nil, false, fmt.Errorf("pasted image too large: %d bytes (max %d)", len(decoded), pastedImageMaxBytes)
	}
	return decoded, len(decoded) > 0, nil
}
