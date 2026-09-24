package agentruntime

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// knowledgeConfigFileNames is the fixed set of unambiguous configuration file
// basenames (lowercased) that a code file may be configured by. A source file
// that literally references one of these names is linked to the corresponding
// config file node when that file is part of the same snapshot.
var knowledgeConfigFileNames = map[string]struct{}{
	"go.mod": {}, "go.sum": {},
	"package.json": {}, "package-lock.json": {}, "pnpm-lock.yaml": {}, "yarn.lock": {}, "tsconfig.json": {},
	"pyproject.toml": {}, "requirements.txt": {}, "setup.py": {}, "setup.cfg": {}, "pipfile": {},
	"cargo.toml": {}, "cargo.lock": {},
	"pom.xml": {}, "build.gradle": {},
	"dockerfile": {}, "docker-compose.yml": {}, "docker-compose.yaml": {},
	"makefile": {}, ".editorconfig": {},
	"vite.config.ts": {}, "vite.config.js": {}, "webpack.config.js": {}, "rollup.config.js": {},
}

// knowledgeConfigFilePattern matches any known config basename as a whole path
// component (surrounded by a non-identifier character or a boundary), so
// "package.json" matches but "mypackage.json" does not.
var knowledgeConfigFilePattern = func() *regexp.Regexp {
	names := make([]string, 0, len(knowledgeConfigFileNames))
	for name := range knowledgeConfigFileNames {
		names = append(names, regexp.QuoteMeta(name))
	}
	sort.Strings(names)
	return regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_])(` + strings.Join(names, "|") + `)(?:$|[^A-Za-z0-9_])`)
}()

// appendKnowledgeConfiguredByEdges adds deterministic "configured_by" edges from
// a code file node to a config file node it literally references. The reference
// is parsed from the source file's own lines and the target must already be part
// of the snapshot, so the relation is locally verifiable and rebuilt over the
// whole assembled graph on every index (surviving incremental per-file reuse).
func appendKnowledgeConfiguredByEdges(graph *session.KnowledgeGraphSnapshot, profile string) {
	if graph == nil {
		return
	}
	switch profile {
	case "code", "mixed":
	default:
		return
	}
	fileNodeByLowerPath := make(map[string]session.KnowledgeNode)
	pathsByLowerBase := make(map[string][]string)
	for _, node := range graph.Nodes {
		if node.Kind != "file" {
			continue
		}
		lowerPath := strings.ToLower(node.Label)
		if _, exists := fileNodeByLowerPath[lowerPath]; exists {
			continue
		}
		fileNodeByLowerPath[lowerPath] = node
		base := strings.ToLower(path.Base(node.Label))
		pathsByLowerBase[base] = append(pathsByLowerBase[base], lowerPath)
	}
	if len(fileNodeByLowerPath) == 0 {
		return
	}
	fileByID := make(map[string]session.KnowledgeFile, len(graph.Files))
	for _, file := range graph.Files {
		fileByID[file.ID] = file
	}
	existing := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existing[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	for _, chunk := range graph.Chunks {
		file, ok := fileByID[chunk.FileID]
		if !ok {
			continue
		}
		lowerPath := strings.ToLower(file.RelativePath)
		sourceNode, ok := fileNodeByLowerPath[lowerPath]
		if !ok {
			continue
		}
		sourceBase := strings.ToLower(path.Base(file.RelativePath))
		if _, isConfig := knowledgeConfigFileNames[sourceBase]; isConfig {
			continue
		}
		dir := strings.ToLower(path.Dir(file.RelativePath))
		if dir == "." {
			dir = ""
		} else {
			dir += "/"
		}
		for index, line := range strings.Split(chunk.Text, "\n") {
			for _, match := range knowledgeConfigFilePattern.FindAllStringSubmatch(line, -1) {
				name := strings.ToLower(match[1])
				if name == sourceBase {
					continue
				}
				target, ok := resolveKnowledgeConfigTarget(fileNodeByLowerPath, pathsByLowerBase, dir, name)
				if !ok || target.ID == sourceNode.ID {
					continue
				}
				key := knowledgeEdgeKey(sourceNode.ID, target.ID, "configured_by")
				if _, duplicate := existing[key]; duplicate {
					continue
				}
				lineNo := chunk.StartLine + index
				edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: sourceNode.ID,
					ToNodeID: target.ID, RelationType: "configured_by", Confidence: 1}
				graph.Edges = append(graph.Edges, edge)
				graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
					EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: lineNo, EndLine: lineNo, Confidence: 1})
				existing[key] = struct{}{}
			}
		}
	}
}

// resolveKnowledgeConfigTarget prefers a config file in the referencing file's
// own directory and otherwise accepts only a globally unambiguous basename, so
// a mention never links to an arbitrary same-named file in another directory.
func resolveKnowledgeConfigTarget(byPath map[string]session.KnowledgeNode, byBase map[string][]string, dir, name string) (session.KnowledgeNode, bool) {
	if node, ok := byPath[dir+name]; ok {
		return node, true
	}
	paths := byBase[name]
	if len(paths) != 1 {
		return session.KnowledgeNode{}, false
	}
	node, ok := byPath[paths[0]]
	return node, ok
}
