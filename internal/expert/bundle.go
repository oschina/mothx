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
)

const (
	manifestFileName = "expert.json"
	agentsDirName    = "agents"
	skillsDirName    = "skills"
)

// validModes is the allowed frontmatter mode set ("" = inherit session
// policy resolution).
var validModes = map[string]bool{"": true, "plan": true, "agent": true, "yolo": true, "os": true}

// LoadBundle loads and validates an expert bundle from an OS directory.
// Validation failures are reported via Bundle.Invalid/InvalidReason; the
// returned error is reserved for IO failures.
func LoadBundle(dir string) (*Bundle, error) {
	cleaned := filepath.Clean(dir)
	return loadBundle(&osSource{root: cleaned}, filepath.Base(cleaned))
}

// LoadBundleFS loads and validates an expert bundle from dir inside fsys,
// e.g. LoadBundleFS(experts.BuiltinFS, "software-company"). When dir is "."
// or empty the bundle name is taken from the manifest itself and the
// directory-name consistency check is skipped.
func LoadBundleFS(fsys fs.FS, dir string) (*Bundle, error) {
	if fsys == nil {
		return nil, errors.New("expert: nil fs.FS")
	}
	cleaned := strings.Trim(path.Clean("/"+strings.TrimSpace(dir)), "/")
	return loadBundle(&fsSource{fsys: fsys, root: cleaned}, path.Base(cleaned))
}

// bundleSource abstracts bundle file access for OS directories and fs.FS.
// Paths are bundle-relative slash paths ("expert.json", "agents/x.md").
type bundleSource interface {
	readFile(name string) ([]byte, error)
	listDir(name string) ([]string, error)
	statDir(name string) bool
	// resolve returns an externally meaningful location for a bundle-relative
	// directory (filesystem path for OS loads, slash path for fs.FS loads).
	resolve(name string) string
	// fsHandle returns the backing fs.FS for FS-based sources, nil otherwise.
	fsHandle() fs.FS
}

type osSource struct{ root string }

func (s *osSource) readFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.root, filepath.FromSlash(name)))
}

func (s *osSource) listDir(name string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (s *osSource) statDir(name string) bool {
	info, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(name)))
	return err == nil && info.IsDir()
}

func (s *osSource) resolve(name string) string {
	return filepath.Join(s.root, filepath.FromSlash(name))
}

func (s *osSource) fsHandle() fs.FS { return nil }

type fsSource struct {
	fsys fs.FS
	// root is the slash path of the bundle inside fsys; "" when fsys is
	// rooted at the bundle itself.
	root string
}

func (s *fsSource) join(name string) string {
	if s.root == "" {
		return name
	}
	return s.root + "/" + name
}

func (s *fsSource) readFile(name string) ([]byte, error) {
	return fs.ReadFile(s.fsys, s.join(name))
}

func (s *fsSource) listDir(name string) ([]string, error) {
	entries, err := fs.ReadDir(s.fsys, s.join(name))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func (s *fsSource) statDir(name string) bool {
	info, err := fs.Stat(s.fsys, s.join(name))
	return err == nil && info.IsDir()
}

func (s *fsSource) resolve(name string) string {
	if s.root == "" {
		return name
	}
	return s.root + "/" + name
}

func (s *fsSource) fsHandle() fs.FS { return s.fsys }

// loadBundle is the shared load+validation core. dirName is the bundle
// directory base name ("" or "." when it cannot be derived from the path).
func loadBundle(src bundleSource, dirName string) (*Bundle, error) {
	if dirName == "." || dirName == "/" {
		dirName = ""
	}
	data, err := src.readFile(manifestFileName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return invalidBundle(dirName, Manifest{}, manifestFileName+" 不存在"), nil
		}
		return nil, fmt.Errorf("read %s: %w", manifestFileName, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return invalidBundle(dirName, Manifest{}, fmt.Sprintf("%s 解析失败: %v", manifestFileName, err)), nil
	}
	// Rule 1: schemaVersion, non-empty name, directory-name consistency.
	if reason := validateManifest(&manifest, dirName); reason != "" {
		name := manifest.Name
		if name == "" {
			name = dirName
		}
		return invalidBundle(name, manifest, reason), nil
	}
	agentIDs, err := listAgentIDs(src)
	if err != nil {
		return nil, err
	}
	// Rules 2-4: expertType-specific structural checks.
	have := make(map[string]bool, len(agentIDs))
	for _, id := range agentIDs {
		have[id] = true
	}
	if reason := validateStructure(&manifest, have); reason != "" {
		return invalidBundle(manifest.Name, manifest, reason), nil
	}
	// Rules 5-6: parse every agents/*.md; unreferenced files load into Defs
	// without affecting validity.
	defs := make(map[string]*AgentDef, len(agentIDs))
	for _, id := range agentIDs {
		def, reason, err := loadAgentDef(src, id)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			return invalidBundle(manifest.Name, manifest, reason), nil
		}
		defs[id] = def
	}
	assignRoles(&manifest, defs)
	applyDisplayNames(&manifest, defs)
	bundle := &Bundle{Name: manifest.Name, Manifest: manifest, Defs: defs}
	// Rule 7: expose the optional skills/ source so Runtime resource assembly
	// can load it into the canonical skills manager; other extra files are
	// ignored here.
	if src.statDir(skillsDirName) {
		bundle.SkillsDir = src.resolve(skillsDirName)
		bundle.SkillsFS = src.fsHandle()
	}
	return bundle, nil
}

// validateManifest applies the manifest-level checks that do not require the
// agents/ directory (spec rule 1). dirName "" skips the directory-name
// consistency check. It returns the failure reason, or "" when valid.
func validateManifest(m *Manifest, dirName string) string {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Sprintf("schemaVersion 必须为 %d，实际为 %d", SchemaVersion, m.SchemaVersion)
	}
	if strings.TrimSpace(m.Name) == "" {
		return "manifest name 不能为空"
	}
	if dirName != "" && m.Name != dirName {
		return fmt.Sprintf("manifest name %q 与包目录名 %q 不一致", m.Name, dirName)
	}
	return ""
}

// validateStructure applies the expertType-specific checks (spec rules 2-4).
func validateStructure(m *Manifest, haveAgentFile map[string]bool) string {
	switch m.ExpertType {
	case TypeTeam:
		if m.TeamInfo == nil {
			return "expertType team 必须提供 teamInfo"
		}
		lead := strings.TrimSpace(m.TeamInfo.LeadAgent)
		if lead == "" {
			return "teamInfo.leadAgent 不能为空"
		}
		if !haveAgentFile[lead] {
			return fmt.Sprintf("teamInfo.leadAgent %q 缺少对应的 agents/%s.md", lead, lead)
		}
		seen := make(map[string]bool, len(m.TeamInfo.MemberAgents))
		for _, member := range m.TeamInfo.MemberAgents {
			if member == lead {
				return fmt.Sprintf("memberAgents 不能包含 leadAgent %q", lead)
			}
			if member == "" {
				return "memberAgents 含空项"
			}
			if seen[member] {
				return fmt.Sprintf("memberAgents 含重复项 %q", member)
			}
			seen[member] = true
			if !haveAgentFile[member] {
				return fmt.Sprintf("memberAgents %q 缺少对应的 agents/%s.md", member, member)
			}
		}
		return ""
	case TypeAgent:
		name := strings.TrimSpace(m.AgentName)
		if name == "" {
			return "expertType agent 必须提供 agentName"
		}
		if !haveAgentFile[name] {
			return fmt.Sprintf("agentName %q 缺少对应的 agents/%s.md", name, name)
		}
		return ""
	case "skill":
		return "不支持 expertType \"skill\"：skill 型由 skills/skillhub 承载，请走技能机制分发"
	default:
		return fmt.Sprintf("不支持的 expertType %q（仅支持 agent/team）", m.ExpertType)
	}
}

// listAgentIDs returns sorted ids (md file names without extension) present
// in agents/. A missing agents/ directory yields no ids; referenced-file
// absence is reported by validateStructure.
func listAgentIDs(src bundleSource) ([]string, error) {
	names, err := src.listDir(agentsDirName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s/ directory: %w", agentsDirName, err)
	}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasSuffix(name, ".md") {
			ids = append(ids, strings.TrimSuffix(name, ".md"))
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// loadAgentDef parses one agents/<id>.md (spec rule 5). It returns either an
// invalid-bundle reason or an IO error.
func loadAgentDef(src bundleSource, id string) (*AgentDef, string, error) {
	fileName := id + ".md"
	data, err := src.readFile(agentsDirName + "/" + fileName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Sprintf("agents/%s 不存在", fileName), nil
		}
		return nil, "", fmt.Errorf("read agents/%s: %w", fileName, err)
	}
	fm, prompt, err := parseFrontmatter(string(data), id)
	if err != nil {
		return nil, fmt.Sprintf("agents/%s: frontmatter 解析失败: %v", fileName, err), nil
	}
	if fm.Name != id {
		return nil, fmt.Sprintf("agents/%s: frontmatter name %q 与文件名不一致", fileName, fm.Name), nil
	}
	if !validModes[fm.Mode] {
		return nil, fmt.Sprintf("agents/%s: mode %q 非法（允许：空、plan、agent、yolo、os）", fileName, fm.Mode), nil
	}
	if fm.MaxIterations < 0 {
		return nil, fmt.Sprintf("agents/%s: max_iterations 不能为负数（%d）", fileName, fm.MaxIterations), nil
	}
	if fm.Worktree && strings.TrimSpace(fm.WorkDir) != "" {
		return nil, fmt.Sprintf("agents/%s: work_dir 与 worktree 不能同时声明", fileName), nil
	}
	def := &AgentDef{
		ID:          id,
		Emoji:       fm.Emoji,
		Description: fm.Description,
		Prompt:      prompt,
		Meta:        fm,
	}
	return def, "", nil
}

// assignRoles resolves Def.Role: manifest decision first (team leadAgent /
// memberAgents, agent agentName), frontmatter role only as fallback.
func assignRoles(m *Manifest, defs map[string]*AgentDef) {
	switch m.ExpertType {
	case TypeTeam:
		if m.TeamInfo != nil {
			if def, ok := defs[m.TeamInfo.LeadAgent]; ok {
				def.Role = RoleLead
			}
			for _, id := range m.TeamInfo.MemberAgents {
				if def, ok := defs[id]; ok {
					def.Role = RoleMember
				}
			}
		}
	case TypeAgent:
		if def, ok := defs[m.AgentName]; ok {
			def.Role = RoleLead
		}
	}
	for _, def := range defs {
		if def.Role == "" {
			def.Role = def.Meta.Role
		}
	}
}

// applyDisplayNames resolves Def.DisplayName: manifest members[] name.zh,
// else name.en, else the def ID.
func applyDisplayNames(m *Manifest, defs map[string]*AgentDef) {
	for _, meta := range m.Members {
		def, ok := defs[meta.ID]
		if !ok {
			continue
		}
		if zh := strings.TrimSpace(meta.Name.Zh); zh != "" {
			def.DisplayName = zh
			continue
		}
		if en := strings.TrimSpace(meta.Name.En); en != "" {
			def.DisplayName = en
		}
	}
	for id, def := range defs {
		if def.DisplayName == "" {
			def.DisplayName = id
		}
	}
}

func invalidBundle(name string, manifest Manifest, reason string) *Bundle {
	return &Bundle{
		Name:          name,
		Manifest:      manifest,
		Defs:          map[string]*AgentDef{},
		Invalid:       true,
		InvalidReason: reason,
	}
}
