package skillhub

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxDownloadBytes = 50 << 20
	maxUnpackedBytes = 100 << 20
	maxFiles         = 500
	maxFileBytes     = 25 << 20
)

var (
	ErrLocalSkillExists = errors.New("skill directory already exists and is not managed by SkillHub")
	ErrInvalidArchive   = errors.New("skill archive is invalid")
)

type InstallRequest struct {
	Market    Market
	ID        string
	Version   string
	Scope     string
	TargetDir string
	Overwrite bool
}

type InstallResult struct {
	Name             string   `json:"name"`
	Market           Market   `json:"market"`
	Version          string   `json:"version"`
	Scope            string   `json:"scope"`
	Dir              string   `json:"dir"`
	Installed        bool     `json:"installed"`
	AlreadyInstalled bool     `json:"alreadyInstalled,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

func Install(ctx context.Context, client MarketClient, request InstallRequest) (InstallResult, error) {
	if client == nil {
		return InstallResult{}, errors.New("missing marketplace client")
	}
	if request.Market == "" {
		request.Market = client.Market().ID
	}
	if request.Market != client.Market().ID {
		return InstallResult{}, fmt.Errorf("client market %q does not match requested market %q", client.Market().ID, request.Market)
	}
	if request.ID == "" || request.TargetDir == "" {
		return InstallResult{}, errors.New("skill id and target directory are required")
	}
	detail, err := client.Detail(ctx, SkillID{Market: request.Market, ID: request.ID})
	if err != nil {
		return InstallResult{}, err
	}
	version := request.Version
	if version == "" {
		version = detail.Version
	}
	name, err := installName(detail, request.ID)
	if err != nil {
		return InstallResult{}, err
	}
	destination := filepath.Join(request.TargetDir, name)
	if existing, err := readMetadata(destination); err == nil {
		if existing.Market != request.Market || existing.ID != request.ID {
			return InstallResult{}, fmt.Errorf("skill %q is managed by %s/%s, not %s/%s", name, existing.Market, existing.ID, request.Market, request.ID)
		}
		if existing.Market == request.Market && existing.ID == request.ID && existing.Version == version {
			return InstallResult{Name: name, Market: request.Market, Version: version, Scope: request.Scope, Dir: destination, Installed: true, AlreadyInstalled: true}, nil
		}
		if !request.Overwrite {
			return InstallResult{}, fmt.Errorf("skill %q is already installed; set overwrite to update", name)
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, os.ErrInvalid) {
		return InstallResult{}, err
	} else if _, statErr := os.Stat(destination); statErr == nil {
		return InstallResult{}, ErrLocalSkillExists
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return InstallResult{}, statErr
	}

	if err := os.MkdirAll(request.TargetDir, 0755); err != nil {
		return InstallResult{}, err
	}
	body, source, err := client.Download(ctx, SkillID{Market: request.Market, ID: request.ID}, version)
	if err != nil {
		return InstallResult{}, err
	}
	defer body.Close()
	tempDir, err := os.MkdirTemp(request.TargetDir, ".skillhub-download-")
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(tempDir)
	archivePath := filepath.Join(tempDir, "skill.zip")
	if err := copyLimited(archivePath, body, maxDownloadBytes); err != nil {
		return InstallResult{}, err
	}
	if source.SHA256 != "" {
		if err := verifyArchiveSHA256(archivePath, source.SHA256); err != nil {
			return InstallResult{}, err
		}
	}
	extractDir := filepath.Join(tempDir, "extract")
	if err := extractZip(archivePath, extractDir); err != nil {
		return InstallResult{}, err
	}
	sourceDir, err := skillRoot(extractDir)
	if err != nil {
		return InstallResult{}, err
	}
	stageDir := filepath.Join(tempDir, "install")
	if err := os.Rename(sourceDir, stageDir); err != nil {
		return InstallResult{}, err
	}
	metadata := InstallMetadata{Market: request.Market, ID: request.ID, Slug: detail.Slug, Version: version, InstalledAt: time.Now().UTC(), SourceURL: source.SourceURL}
	if err := writeMetadata(stageDir, metadata); err != nil {
		return InstallResult{}, err
	}
	if err := replaceDirectory(destination, stageDir); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Name: name, Market: request.Market, Version: version, Scope: request.Scope, Dir: destination, Installed: true}, nil
}

func copyLimited(path string, source io.Reader, limit int64) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	written, err := io.Copy(file, io.LimitReader(source, limit+1))
	if err != nil {
		return err
	}
	if written > limit {
		return fmt.Errorf("download exceeds %d byte limit", limit)
	}
	return nil
}

func verifyArchiveSHA256(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("downloaded archive sha256 mismatch: got %s, want %s", actual, expected)
	}
	return nil
}

func extractZip(archivePath, target string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	defer reader.Close()
	if len(reader.File) == 0 || len(reader.File) > maxFiles {
		return fmt.Errorf("%w: unsupported file count", ErrInvalidArchive)
	}
	var total int64
	for _, file := range reader.File {
		if err := archivePathSafe(file.Name); err != nil {
			return err
		}
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not allowed", ErrInvalidArchive)
		}
		if file.UncompressedSize64 > uint64(maxFileBytes) {
			return fmt.Errorf("%w: file %q exceeds size limit", ErrInvalidArchive, file.Name)
		}
		total += int64(file.UncompressedSize64)
		if total > maxUnpackedBytes {
			return fmt.Errorf("%w: unpacked archive exceeds size limit", ErrInvalidArchive)
		}
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return err
	}
	for _, file := range reader.File {
		path := filepath.Join(target, filepath.FromSlash(file.Name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			input.Close()
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, maxFileBytes+1))
		closeErr := output.Close()
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if written > maxFileBytes {
			return fmt.Errorf("%w: file %q exceeds size limit", ErrInvalidArchive, file.Name)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func archivePathSafe(name string) error {
	if name == "" || strings.Contains(name, "\\") || filepath.IsAbs(name) || filepath.VolumeName(name) != "" || isWindowsDrivePath(name) {
		return fmt.Errorf("%w: unsafe path %q", ErrInvalidArchive, name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: unsafe path %q", ErrInvalidArchive, name)
	}
	return nil
}

func isWindowsDrivePath(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':'
}

func skillRoot(extractDir string) (string, error) {
	if hasSkillFile(extractDir) {
		return extractDir, nil
	}
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		root := filepath.Join(extractDir, entries[0].Name())
		if hasSkillFile(root) {
			return root, nil
		}
	}
	return "", fmt.Errorf("%w: SKILL.md is missing from archive root", ErrInvalidArchive)
}

func hasSkillFile(dir string) bool {
	_, upperErr := os.Stat(filepath.Join(dir, "SKILL.md"))
	if upperErr == nil {
		return true
	}
	_, lowerErr := os.Stat(filepath.Join(dir, "skill.md"))
	return lowerErr == nil
}

func installName(detail SkillDetail, id string) (string, error) {
	name := detail.Slug
	if name == "" {
		name = id
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "" || name == ".." || name != filepath.Base(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	return name, nil
}
func writeMetadata(dir string, metadata InstallMetadata) error {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, metadataFileName), append(data, '\n'), 0644)
}

func replaceDirectory(destination, stage string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	backup := ""
	if _, err := os.Stat(destination); err == nil {
		backup = filepath.Join(filepath.Dir(destination), ".backup", filepath.Base(destination)+"-"+time.Now().UTC().Format("20060102T150405.000000000"))
		if err := os.MkdirAll(filepath.Dir(backup), 0755); err != nil {
			return err
		}
		if err := os.Rename(destination, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(stage, destination); err != nil {
		if backup != "" {
			_ = os.Rename(backup, destination)
		}
		return err
	}
	return nil
}
