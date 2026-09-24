package agentruntime

import (
	"path"
	"regexp"
	"strings"

	"github.com/oschina/mothx/internal/session"
)

// knowledgeImportNodeKind is the node kind for an imported module specifier. It
// is a deterministic, Runtime-derived fact, never a model assertion.
const knowledgeImportNodeKind = "module"

var (
	knowledgeGoImportSingle = regexp.MustCompile(`^\s*import\s+(?:[A-Za-z_.][A-Za-z0-9_.]*\s+|_\s+|\.\s+)?"([^"]+)"`)
	knowledgeGoImportItem   = regexp.MustCompile(`^\s*(?:[A-Za-z_.][A-Za-z0-9_.]*\s+|_\s+|\.\s+)?"([^"]+)"`)
	knowledgePythonFrom     = regexp.MustCompile(`^\s*from\s+([A-Za-z_][A-Za-z0-9_.]*)\s+import\b`)
	knowledgePythonImport   = regexp.MustCompile(`^\s*import\s+(.+)$`)
	knowledgeJSFrom         = regexp.MustCompile(`\bfrom\s+['"]([^'"]+)['"]`)
	knowledgeJSBare         = regexp.MustCompile(`^\s*import\s+['"]([^'"]+)['"]`)
	knowledgeJSRequire      = regexp.MustCompile(`\brequire\(\s*['"]([^'"]+)['"]\s*\)`)
)

// appendKnowledgeImportEdges adds deterministic "imports" edges from a code file
// node to a module node for each import statement the file contains. The target
// is parsed from the file's own source lines, so no model assertion is involved
// and the relation is rebuilt over the whole assembled graph on every index,
// which is what lets it survive incremental per-file reuse even though the file
// and module nodes live in different files.
func appendKnowledgeImportEdges(graph *session.KnowledgeGraphSnapshot, profile string) {
	if graph == nil {
		return
	}
	switch profile {
	case "code", "mixed":
	default:
		return
	}
	fileNodeByPath := make(map[string]session.KnowledgeNode)
	for _, node := range graph.Nodes {
		if node.Kind == "file" {
			fileNodeByPath[node.Label] = node
		}
	}
	if len(fileNodeByPath) == 0 {
		return
	}
	fileByID := make(map[string]session.KnowledgeFile, len(graph.Files))
	for _, file := range graph.Files {
		fileByID[file.ID] = file
	}
	moduleNodeID := make(map[string]string)
	for _, node := range graph.Nodes {
		if node.Kind == knowledgeImportNodeKind {
			moduleNodeID[knowledgeImportNodeKind+"\x00"+node.NormalizedLabel] = node.ID
		}
	}
	existingEdges := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		existingEdges[knowledgeEdgeKey(edge.FromNodeID, edge.ToNodeID, edge.RelationType)] = struct{}{}
	}
	for _, chunk := range graph.Chunks {
		file, ok := fileByID[chunk.FileID]
		if !ok {
			continue
		}
		fileNode, ok := fileNodeByPath[file.RelativePath]
		if !ok {
			continue
		}
		ext := strings.ToLower(path.Ext(file.RelativePath))
		for _, hit := range knowledgeImportHits(ext, chunk.Text) {
			target := strings.TrimSpace(hit.target)
			normalized := normalizeKnowledgeLabel(target)
			if target == "" || normalized == "" {
				continue
			}
			line := chunk.StartLine + hit.line - 1
			key := knowledgeImportNodeKind + "\x00" + normalized
			toID, exists := moduleNodeID[key]
			if !exists {
				node := session.KnowledgeNode{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, Kind: knowledgeImportNodeKind,
					Label: target, NormalizedLabel: normalized, Summary: target,
					Status: session.KnowledgeNodeStatusFact, Confidence: 1}
				graph.Nodes = append(graph.Nodes, node)
				toID = node.ID
				moduleNodeID[key] = toID
				graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
					NodeID: toID, ChunkID: chunk.ID, StartLine: line, EndLine: line, Confidence: 1})
			}
			if toID == fileNode.ID {
				continue
			}
			edgeKey := knowledgeEdgeKey(fileNode.ID, toID, "imports")
			if _, duplicate := existingEdges[edgeKey]; duplicate {
				continue
			}
			edge := session.KnowledgeEdge{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID, FromNodeID: fileNode.ID,
				ToNodeID: toID, RelationType: "imports", Confidence: 1}
			graph.Edges = append(graph.Edges, edge)
			graph.Evidence = append(graph.Evidence, session.KnowledgeEvidence{ID: session.GenerateID(), SnapshotID: graph.Snapshot.ID,
				EdgeID: edge.ID, ChunkID: chunk.ID, StartLine: line, EndLine: line, Confidence: 1})
			existingEdges[edgeKey] = struct{}{}
		}
	}
}

// knowledgeImportHit is one import statement found in a chunk: the target
// specifier and the 1-based line number inside the chunk where it appears.
type knowledgeImportHit struct {
	line   int
	target string
}

func knowledgeImportHits(ext, text string) []knowledgeImportHit {
	lines := strings.Split(text, "\n")
	switch ext {
	case ".go":
		return knowledgeGoImportHits(lines)
	case ".py":
		return knowledgePythonImportHits(lines)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return knowledgeJSImportHits(lines)
	default:
		return nil
	}
}

func knowledgeGoImportHits(lines []string) []knowledgeImportHit {
	hits := make([]knowledgeImportHit, 0)
	inBlock := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if match := knowledgeGoImportSingle.FindStringSubmatch(line); len(match) == 2 {
				hits = append(hits, knowledgeImportHit{line: index + 1, target: match[1]})
				continue
			}
			if strings.HasPrefix(trimmed, "import") && strings.Contains(trimmed, "(") {
				inBlock = true
			}
			continue
		}
		if strings.HasPrefix(trimmed, ")") {
			inBlock = false
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if match := knowledgeGoImportItem.FindStringSubmatch(line); len(match) == 2 {
			hits = append(hits, knowledgeImportHit{line: index + 1, target: match[1]})
		}
	}
	return hits
}

func knowledgePythonImportHits(lines []string) []knowledgeImportHit {
	hits := make([]knowledgeImportHit, 0)
	for index, line := range lines {
		if match := knowledgePythonFrom.FindStringSubmatch(line); len(match) == 2 {
			hits = append(hits, knowledgeImportHit{line: index + 1, target: match[1]})
			continue
		}
		match := knowledgePythonImport.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		for _, item := range strings.Split(match[1], ",") {
			if fields := strings.Fields(strings.TrimSpace(item)); len(fields) > 0 {
				hits = append(hits, knowledgeImportHit{line: index + 1, target: fields[0]})
			}
		}
	}
	return hits
}

func knowledgeJSImportHits(lines []string) []knowledgeImportHit {
	hits := make([]knowledgeImportHit, 0)
	for index, line := range lines {
		for _, pattern := range []*regexp.Regexp{knowledgeJSFrom, knowledgeJSBare, knowledgeJSRequire} {
			if match := pattern.FindStringSubmatch(line); len(match) == 2 {
				hits = append(hits, knowledgeImportHit{line: index + 1, target: match[1]})
			}
		}
	}
	return hits
}
