package expert

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oschina/mothx/experts"
	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/platform"
)

// Center resolves expert bundles from three layered sources with name
// shadowing: project > global > builtin. Scans are lazy (per call), so users
// can drop a bundle into a directory and it takes effect without a watcher.
type Center struct {
	// ProjectDir is the project root for the project-level source. Empty
	// means the project layer is disabled.
	ProjectDir string
}

// Summary is a lightweight List entry built from the manifest only; persona
// bodies are never loaded during listing.
type Summary struct {
	Name          string        `json:"name"`
	ExpertType    string        `json:"expertType"`
	DisplayName   LocalizedText `json:"displayName"`
	Source        string        `json:"source"` // builtin | global | project
	Invalid       bool          `json:"invalid"`
	InvalidReason string        `json:"invalidReason,omitempty"`
}

// GlobalExpertsDir returns the global experts directory
// (<platform.ConfigDir()>/experts).
func GlobalExpertsDir() string {
	return filepath.Join(platform.ConfigDir(), "experts")
}

// ProjectExpertsDir returns the project experts directory
// (<ProjectDir>/.mothx/experts), or "" when the project layer is disabled.
func (c *Center) ProjectExpertsDir() string {
	if strings.TrimSpace(c.ProjectDir) == "" {
		return ""
	}
	return config.ProjectPathFor(c.ProjectDir, "experts")
}

// List lazily scans all three layers and returns summaries sorted by name.
// Higher-priority layers shadow lower ones with the same bundle name. A
// missing or unreadable directory is an empty layer, not an error. Only
// directories containing expert.json count as bundles.
func (c *Center) List() []Summary {
	summaries := make(map[string]Summary)
	// builtin (lowest priority; later layers overwrite by name)
	if entries, err := fs.ReadDir(experts.BuiltinFS, "."); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			data, err := fs.ReadFile(experts.BuiltinFS, path.Join(name, manifestFileName))
			if err != nil {
				continue
			}
			summaries[name] = summaryFromManifest(name, SourceBuiltin, data)
		}
	}
	// global
	for _, summary := range listOSLayer(GlobalExpertsDir(), SourceGlobal) {
		summaries[summary.Name] = summary
	}
	// project (highest priority)
	if dir := c.ProjectExpertsDir(); dir != "" {
		for _, summary := range listOSLayer(dir, SourceProject) {
			summaries[summary.Name] = summary
		}
	}
	out := make([]Summary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get loads the highest-priority bundle with the given name. A bundle that is
// absent from every layer returns an error; a bundle that fails validation is
// returned with Invalid/InvalidReason set (no error).
func (c *Center) Get(name string) (*Bundle, error) {
	if err := validateBundleName(name); err != nil {
		return nil, err
	}
	if dir := c.ProjectExpertsDir(); dir != "" {
		bundleDir := filepath.Join(dir, name)
		if isFile(filepath.Join(bundleDir, manifestFileName)) {
			return LoadBundle(bundleDir)
		}
	}
	globalDir := filepath.Join(GlobalExpertsDir(), name)
	if isFile(filepath.Join(globalDir, manifestFileName)) {
		return LoadBundle(globalDir)
	}
	if _, err := fs.Stat(experts.BuiltinFS, path.Join(name, manifestFileName)); err == nil {
		return LoadBundleFS(experts.BuiltinFS, name)
	}
	return nil, fmt.Errorf("expert %q not found", name)
}

// validateBundleName rejects empty names and path traversal so a layer lookup
// can never escape its experts directory.
func validateBundleName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("expert name 不能为空")
	}
	if name != path.Base(name) || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid expert name %q", name)
	}
	return nil
}

func listOSLayer(dir, source string) []Summary {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // missing/unreadable directory = empty layer
	}
	var out []Summary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, err := os.ReadFile(filepath.Join(dir, name, manifestFileName))
		if err != nil {
			continue // no expert.json: not an expert bundle
		}
		out = append(out, summaryFromManifest(name, source, data))
	}
	return out
}

// summaryFromManifest builds a Summary from raw expert.json bytes. Manifest
// parse failures and manifest-level validation failures are flagged Invalid
// with a reason instead of being dropped.
func summaryFromManifest(dirName, source string, data []byte) Summary {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Summary{
			Name:          dirName,
			Source:        source,
			Invalid:       true,
			InvalidReason: fmt.Sprintf("%s 解析失败: %v", manifestFileName, err),
		}
	}
	summary := Summary{
		Name:        dirName,
		ExpertType:  manifest.ExpertType,
		DisplayName: manifest.DisplayName,
		Source:      source,
	}
	if reason := validateManifest(&manifest, dirName); reason != "" {
		summary.Invalid = true
		summary.InvalidReason = reason
	}
	return summary
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
