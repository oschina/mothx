package skills

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/oschina/mothx/internal/config"
)

// SkillReference represents a reference file within a skill.
type SkillReference struct {
	Path     string // relative path (e.g. "references/audio.md")
	FullPath string // absolute path
	Label    string // display label (e.g. "音频")
	AutoLoad bool   // true if marked [已加载], false if [待按需加载]
	Loaded   bool   // whether this reference has been loaded
	Content  string // loaded content
	fsys     fs.FS
}

// Skill represents a loaded skill.
type Skill struct {
	Name        string            // skill name (directory name)
	Path        string            // absolute path to SKILL.md
	Dir         string            // skill directory
	Description string            // first line or heading description
	Content     string            // full SKILL.md content
	Source      string            // "global" or "project"
	References  []*SkillReference // parsed references
	fsys        fs.FS
	fsDir       string
}

// Manager manages skill discovery and loading.
type Manager struct {
	globalDir   string   // ~/.mothx/skills
	projectDir  string   // highest-priority project skills dir
	projectDirs []string // project skills dirs, highest priority first
	skills      map[string]*Skill

	// disabled holds the skill enable/disable toggles (settings.skills.disabled).
	// It is guarded by its own mutex because management surfaces may update the
	// toggles while prompt construction reads the discovery methods.
	disabledMu sync.RWMutex
	disabled   map[string]bool
}

// NewManager creates a new skills manager.
func NewManager(globalDir, projectDir string, additionalProjectDirs ...string) *Manager {
	projectDirs := dedupeDirs(append([]string{projectDir}, additionalProjectDirs...))
	return &Manager{
		globalDir:   globalDir,
		projectDir:  projectDir,
		projectDirs: projectDirs,
		skills:      make(map[string]*Skill),
	}
}

// NewManagerWithProjectDirs creates a new skills manager with explicit project
// directories in priority order.
func NewManagerWithProjectDirs(globalDir string, projectDirs []string) *Manager {
	projectDirs = dedupeDirs(projectDirs)
	projectDir := ""
	if len(projectDirs) > 0 {
		projectDir = projectDirs[0]
	}
	return &Manager{
		globalDir:   globalDir,
		projectDir:  projectDir,
		projectDirs: projectDirs,
		skills:      make(map[string]*Skill),
	}
}

// ProjectSkillDirs returns project-local skill directories in priority order.
func ProjectSkillDirs(projectRoot string) []string {
	if projectRoot == "" {
		return nil
	}
	return []string{
		config.ProjectPathFor(projectRoot, "skills"),
		filepath.Join(projectRoot, ".skills"),
		filepath.Join(projectRoot, ".agents", "skills"),
		filepath.Join(projectRoot, "skills"),
	}
}

// Load discovers built-in, global, and project skills. The precedence is
// project > global > builtin, so a user can intentionally replace a built-in
// skill without any adapter-specific fallback path.
func (m *Manager) Load() error {
	// Built-ins are always available to every Runtime. A missing embedded
	// directory is treated the same way as an empty external skill directory,
	// but an unexpected embedded filesystem failure is actionable.
	if err := m.LoadFS(BuiltinFS, "builtin", "builtin"); err != nil {
		return fmt.Errorf("load built-in skills: %w", err)
	}

	// Load global skills first (lower priority)
	if m.globalDir != "" {
		if err := m.loadFromDir(m.globalDir, "global"); err != nil {
			// Non-fatal
			fmt.Fprintf(os.Stderr, "Warning: could not load global skills: %v\n", err)
		}
	}

	// Load project skills from lowest to highest priority so later entries
	// override earlier entries with the same name.
	for i := len(m.projectDirs) - 1; i >= 0; i-- {
		dir := m.projectDirs[i]
		if dir == "" {
			continue
		}
		if err := m.loadFromDir(dir, "project"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load project skills from %s: %v\n", dir, err)
		}
	}

	m.applyConfiguredDisabledSkills()

	return nil
}

// applyConfiguredDisabledSkills reads the global settings.skills.disabled list
// so every adapter that loads skills (TUI, serve, ACP, agent runtime) shares
// one enable/disable source without constructor changes.
func (m *Manager) applyConfiguredDisabledSkills() {
	settings, err := config.LoadGlobalSettingsSparse()
	if err != nil || settings == nil {
		return
	}
	m.SetDisabledSkills(settings.SkillsDisabled())
}

// SetDisabledSkills replaces the disabled-skill set. Management surfaces call
// it after persisting settings.skills.disabled so the live process reflects
// the toggle without reloading skill content.
func (m *Manager) SetDisabledSkills(names []string) {
	next := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			next[name] = true
		}
	}
	m.disabledMu.Lock()
	m.disabled = next
	m.disabledMu.Unlock()
}

// DisabledSkills returns the sorted disabled-skill names.
func (m *Manager) DisabledSkills() []string {
	m.disabledMu.RLock()
	defer m.disabledMu.RUnlock()
	if len(m.disabled) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.disabled))
	for name := range m.disabled {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsSkillDisabled reports whether a skill name is toggled off.
func (m *Manager) IsSkillDisabled(name string) bool {
	m.disabledMu.RLock()
	defer m.disabledMu.RUnlock()
	return m.disabled[name]
}

func dedupeDirs(dirs []string) []string {
	result := make([]string, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, dir)
	}
	return result
}

// loadFromDir loads all skills from a directory.
// Each skill is a subdirectory containing a SKILL.md file.
func (m *Manager) loadFromDir(dir string, source string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil // directory doesn't exist, that's ok
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read skills directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillDir := filepath.Join(dir, entry.Name())
		skillFile := filepath.Join(skillDir, "SKILL.md")

		data, err := os.ReadFile(skillFile)
		if err != nil {
			// Try lowercase
			skillFile = filepath.Join(skillDir, "skill.md")
			data, err = os.ReadFile(skillFile)
			if err != nil {
				continue // no SKILL.md, skip
			}
		}

		skill := &Skill{
			Name:    entry.Name(),
			Path:    skillFile,
			Dir:     skillDir,
			Content: string(data),
			Source:  source,
		}

		// Extract description from first heading or first non-empty line
		skill.Description = extractDescription(string(data))

		// Parse references from SKILL.md.
		skill.References = parseReferences(string(data), skillDir, nil)

		m.skills[entry.Name()] = skill
	}

	return nil
}

// LoadFS discovers skills below dir in fsys. It is used for embedded or other
// non-OS skill sources while preserving the same name-shadowing behavior as
// directory-backed project skills: later loads replace earlier names.
func (m *Manager) LoadFS(fsys fs.FS, dir, source string) error {
	if m == nil {
		return fmt.Errorf("skills manager is nil")
	}
	if fsys == nil {
		return fmt.Errorf("skills filesystem is nil")
	}
	dir = path.Clean(dir)
	if dir == "." || strings.HasPrefix(dir, "../") || path.IsAbs(dir) {
		return fmt.Errorf("invalid skills filesystem directory %q", dir)
	}
	return m.loadFromFS(fsys, dir, source)
}

func (m *Manager) loadFromFS(fsys fs.FS, dir, source string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read skills filesystem directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := path.Join(dir, entry.Name())
		skillFile := path.Join(skillDir, "SKILL.md")
		data, err := fs.ReadFile(fsys, skillFile)
		if err != nil {
			skillFile = path.Join(skillDir, "skill.md")
			data, err = fs.ReadFile(fsys, skillFile)
			if err != nil {
				continue
			}
		}
		skill := &Skill{
			Name:        entry.Name(),
			Path:        skillFile,
			Dir:         skillDir,
			Content:     string(data),
			Source:      source,
			fsys:        fsys,
			fsDir:       skillDir,
			Description: extractDescription(string(data)),
		}
		skill.References = parseReferences(string(data), skillDir, fsys)
		m.skills[entry.Name()] = skill
	}
	return nil
}

// Get returns a skill by name. Disabled skills are hidden so every runtime
// consumer (prompt context, /skill commands, skill_ref) honors the toggle.
func (m *Manager) Get(name string) *Skill {
	if m.IsSkillDisabled(name) {
		return nil
	}
	return m.skills[name]
}

// List returns all enabled skills sorted by name.
func (m *Manager) List() []*Skill {
	var result []*Skill
	for _, s := range m.skills {
		if m.IsSkillDisabled(s.Name) {
			continue
		}
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListAll returns every discovered skill sorted by name, including disabled
// ones. Management surfaces use it to project the enabled flag and to allow
// re-enabling a disabled skill.
func (m *Manager) ListAll() []*Skill {
	result := make([]*Skill, 0, len(m.skills))
	for _, s := range m.skills {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListBySource returns enabled skills filtered by source.
func (m *Manager) ListBySource(source string) []*Skill {
	var result []*Skill
	for _, s := range m.skills {
		if s.Source == source && !m.IsSkillDisabled(s.Name) {
			result = append(result, s)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Names returns a list of all enabled skill names.
func (m *Manager) Names() []string {
	var names []string
	for name := range m.skills {
		if m.IsSkillDisabled(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// BuildSkillContext returns the content of a skill for injection into the system prompt.
// It includes the SKILL.md content plus all auto-loaded references.
func (m *Manager) BuildSkillContext(name string) string {
	skill := m.Get(name)
	if skill == nil {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n## Active Skill: %s\n\n%s\n", skill.Name, skill.Content))

	// Auto-load references marked as [已加载]
	for _, ref := range skill.References {
		if ref.AutoLoad {
			if content := loadReferenceContent(ref); content != "" {
				ref.Loaded = true
				ref.Content = content
				sb.WriteString(fmt.Sprintf("\n### Reference: %s\n\n%s\n", ref.Label, content))
			}
		}
	}

	// Add reference loading instructions if there are on-demand references
	hasOnDemand := false
	var onDemandRefs []string
	for _, ref := range skill.References {
		if !ref.AutoLoad {
			if !hasOnDemand {
				sb.WriteString("\n### On-Demand References\n\n")
				sb.WriteString("The following references are available but not loaded. " +
					"Use the `skill_ref` tool to load them when needed:\n\n")
				hasOnDemand = true
			}
			onDemandRefs = append(onDemandRefs, fmt.Sprintf("- `%s` (%s)", ref.Path, ref.Label))
		}
	}
	if hasOnDemand {
		sb.WriteString(strings.Join(onDemandRefs, "\n"))
		sb.WriteString("\n")
	}

	return sb.String()
}

// LoadReference loads a specific reference file by path for a skill.
// Returns the content and true if successful.
func (m *Manager) LoadReference(skillName, refPath string) (string, bool) {
	skill := m.Get(skillName)
	if skill == nil {
		return "", false
	}

	// Normalize the path
	refPath = filepath.Clean(refPath)

	for _, ref := range skill.References {
		if ref.Path == refPath || filepath.Clean(ref.Path) == refPath {
			if ref.Loaded {
				return ref.Content, true
			}
			if content := loadReferenceContent(ref); content != "" {
				ref.Loaded = true
				ref.Content = content
				return content, true
			}
			return "", false
		}
	}

	var (
		data     []byte
		fullPath string
		err      error
	)
	if skill.fsys != nil {
		refPath = path.Clean(filepath.ToSlash(refPath))
		if refPath == "." || refPath == ".." || strings.HasPrefix(refPath, "../") || path.IsAbs(refPath) {
			return "", false
		}
		fullPath = path.Join(skill.fsDir, refPath)
		data, err = fs.ReadFile(skill.fsys, fullPath)
	} else {
		// Try loading directly from the skill directory.
		fullPath = filepath.Join(skill.Dir, refPath)
		fullPath = filepath.Clean(fullPath)
		rel, relErr := filepath.Rel(filepath.Clean(skill.Dir), fullPath)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", false
		}
		data, err = os.ReadFile(fullPath)
	}
	if err != nil {
		return "", false
	}

	content := string(data)
	// Add to references list for tracking
	skill.References = append(skill.References, &SkillReference{
		Path:     refPath,
		FullPath: fullPath,
		Label:    refPath,
		AutoLoad: false,
		Loaded:   true,
		Content:  content,
		fsys:     skill.fsys,
	})
	return content, true
}

// ListReferences returns the reference files for a skill with their load status.
func (m *Manager) ListReferences(skillName string) []*SkillReference {
	skill := m.Get(skillName)
	if skill == nil {
		return nil
	}
	return skill.References
}

// loadReferenceContent reads the content of a reference file.
func loadReferenceContent(ref *SkillReference) string {
	if ref == nil {
		return ""
	}
	var (
		data []byte
		err  error
	)
	if ref.fsys != nil {
		data, err = fs.ReadFile(ref.fsys, ref.FullPath)
	} else {
		data, err = os.ReadFile(ref.FullPath)
	}
	if err != nil {
		return ""
	}
	return string(data)
}

// parseReferences parses reference links from SKILL.md content.
// It looks for patterns like:
//   - Section headers: "### N. Label (references/file.md) [已加载]" or "[待按需加载]"
//   - Markdown links: "- [Label](references/file.md)"
func parseReferences(content, skillDir string, fsys fs.FS) []*SkillReference {
	var refs []*SkillReference
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Pattern 1: Section headers with (path) [status]
		// e.g.: "### 1. 基础 (references/base.md) [已加载]"
		if strings.HasPrefix(line, "###") {
			pathStart := strings.Index(line, "(")
			pathEnd := strings.Index(line, ")")
			if pathStart > 0 && pathEnd > pathStart {
				path := line[pathStart+1 : pathEnd]
				if strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".txt") {
					fullPath := skillReferencePath(skillDir, path, fsys)
					if !seen[path] {
						seen[path] = true
						label := strings.TrimPrefix(line, "#")
						label = strings.TrimSpace(label)
						// Remove the path part
						if idx := strings.Index(label, "("); idx > 0 {
							label = strings.TrimSpace(label[:idx])
							// Remove leading number and dot
							label = strings.TrimLeft(label, "0123456789. ")
						}
						autoLoad := strings.Contains(line, "[已加载]")
						refs = append(refs, &SkillReference{
							Path:     path,
							FullPath: fullPath,
							Label:    label,
							AutoLoad: autoLoad,
							fsys:     fsys,
						})
					}
				}
			}
		}

		// Pattern 2: Markdown links at bottom
		// e.g.: "- [基础](references/base.md)"
		if strings.HasPrefix(line, "-") && strings.Contains(line, "[") && strings.Contains(line, "](") {
			linkStart := strings.Index(line, "](")
			linkEnd := strings.Index(line[linkStart+2:], ")")
			if linkStart > 0 && linkEnd > 0 {
				path := line[linkStart+2 : linkStart+2+linkEnd]
				if (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".txt")) && !seen[path] {
					seen[path] = true
					fullPath := skillReferencePath(skillDir, path, fsys)
					// Extract label
					labelStart := strings.Index(line, "[")
					label := ""
					if labelStart >= 0 && labelStart < linkStart {
						label = line[labelStart+1 : linkStart]
					}
					// Check if this ref was already parsed with autoLoad info from headers
					// If not, default to on-demand
					refs = append(refs, &SkillReference{
						Path:     path,
						FullPath: fullPath,
						Label:    label,
						AutoLoad: false,
						fsys:     fsys,
					})
				}
			}
		}
	}

	return refs
}

func skillReferencePath(skillDir, referencePath string, fsys fs.FS) string {
	if fsys != nil {
		return path.Join(skillDir, filepath.ToSlash(referencePath))
	}
	return filepath.Join(skillDir, referencePath)
}

// BuildAllSkillsContext returns a summary of all available skills for the system prompt.
func (m *Manager) BuildAllSkillsContext() string {
	skills := m.List()
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Available Skills\n\n")
	sb.WriteString("Use `/skill:<name>` to load a skill. Available skills:\n\n")

	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", s.Name, s.Source, s.Description))
	}

	sb.WriteString("\n")
	return sb.String()
}

// extractDescription extracts a short description from skill content.
func extractDescription(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Return first heading
		if strings.HasPrefix(line, "#") {
			return strings.TrimLeft(line, "# ")
		}
		// Or first non-empty line
		return line
	}
	return "(no description)"
}

// CreateProjectSkillsDir creates the .skills directory in the project root.
func CreateProjectSkillsDir(projectDir string) error {
	dir := filepath.Join(projectDir, ".skills")
	return os.MkdirAll(dir, 0755)
}
